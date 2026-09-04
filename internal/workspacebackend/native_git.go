package workspacebackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/experimentgit"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
)

// NativeGit creates deterministic linked worktrees through experimentgit and
// uses sourcesnapshot for dirty seed validation and round-trip recapture.
type NativeGit struct {
	Git        gitx.Runner
	DataHome   string
	FileSystem sourcesnapshot.FileSystem
	Clock      func() time.Time
	Bundles    *sourcesnapshot.BundleStore
}

// Prepare leaves the selected production checkout untouched. Dirty Try state is
// applied only to the newly-created worktree and must round-trip exactly.
func (backend NativeGit) Prepare(ctx context.Context, request PrepareRequest) (Workspace, error) {
	if err := backend.validateRequest(request); err != nil {
		return Workspace{}, err
	}
	var workspace Workspace
	err := withWorkspaceLock(ctx, request, func(lockRoot *os.Root) error {
		var prepareErr error
		workspace, prepareErr = backend.prepareLocked(ctx, lockRoot, request)
		return prepareErr
	})
	return workspace, err
}

// RollbackPreparation removes only the deterministic owner workspace and branch
// while the caller still owns the unpublished preparation identity.
func (backend NativeGit) RollbackPreparation(ctx context.Context, request PrepareRequest) error {
	if err := backend.validateRequest(request); err != nil {
		return err
	}
	return withWorkspaceLock(ctx, request, func(lockRoot *os.Root) error {
		if _, err := existingPreparationMarker(lockRoot, request); err != nil {
			return err
		}
		managerRequest := backend.managerRequest(request)
		if _, err := backend.manager().Inspect(ctx, managerRequest); err == nil {
			if _, removeErr := backend.manager().RemovePreparing(ctx, managerRequest); removeErr != nil {
				return removeErr
			}
		} else if errors.Is(err, fs.ErrNotExist) {
			if branchErr := backend.manager().RemovePreparingBranch(ctx, managerRequest); branchErr != nil {
				return branchErr
			}
		} else {
			return err
		}
		return clearPreparationMarker(lockRoot)
	})
}

func (backend NativeGit) prepareLocked(ctx context.Context, lockRoot *os.Root, request PrepareRequest) (Workspace, error) {
	managerRequest := backend.managerRequest(request)
	var openedBundle sourcesnapshot.Bundle
	if request.Snapshot.State == research.SourceSnapshotDirty {
		if request.SeedBundle == nil {
			return Workspace{}, fmt.Errorf("dirty Try preparation requires the authenticated seed bundle: %w", ErrInvalidRequest)
		}
		var err error
		openedBundle, err = backend.bundleStore().Open(ctx, seedBundleDigest(request))
		if err != nil {
			return Workspace{}, fmt.Errorf("open dirty Try seed: %w", err)
		}
		if err := validateBundleBinding(openedBundle, request); err != nil {
			return Workspace{}, err
		}
	}
	markerExisted, err := ensurePreparationMarker(lockRoot, request)
	if err != nil {
		return Workspace{}, err
	}
	var managed experimentgit.Workspace
	inspection, inspectErr := backend.manager().Inspect(ctx, managerRequest)
	if inspectErr == nil {
		managed = inspection.Workspace
		if verifyErr := backend.verifyPreparedSeed(ctx, request, managed); verifyErr == nil {
			if clearErr := clearPreparationMarker(lockRoot); clearErr != nil {
				return backend.workspace(request, managed), clearErr
			}
			return backend.workspace(request, managed), nil
		}
		if !markerExisted {
			return Workspace{}, ErrSeedMismatch
		}
		if _, removeErr := backend.manager().RemovePreparing(ctx, managerRequest); removeErr != nil {
			return Workspace{}, removeErr
		}
	} else if !errors.Is(inspectErr, fs.ErrNotExist) {
		return Workspace{}, inspectErr
	} else if markerExisted {
		if err := backend.manager().RemovePreparingBranch(ctx, managerRequest); err != nil {
			return Workspace{}, err
		}
	}
	managed, err = backend.manager().Prepare(ctx, managerRequest)
	if err != nil {
		return Workspace{}, err
	}
	workspace := backend.workspace(request, managed)
	if request.Snapshot.State == research.SourceSnapshotClean {
		if err := backend.verifyPreparedSeed(ctx, request, managed); err != nil {
			return workspace, fmt.Errorf("recapture prepared clean Source snapshot: %w", err)
		}
		if err := clearPreparationMarker(lockRoot); err != nil {
			return workspace, err
		}
		return workspace, nil
	}
	if err := backend.materializeSubmodules(ctx, request, managed, openedBundle.Manifest.Submodules); err != nil {
		return workspace, err
	}
	patchPath, err := backend.bundleStore().TrackedPatchPath(ctx, openedBundle)
	if err != nil {
		return workspace, fmt.Errorf("validate dirty Try patch: %w", err)
	}
	if openedBundle.Manifest.TrackedPatch.Size != 0 {
		if _, err := backend.runGit(ctx, managed.Worktree, "apply", "--binary", "--whitespace=nowarn", "--", patchPath); err != nil {
			return workspace, fmt.Errorf("apply exact dirty Try patch: %w", err)
		}
	}
	if err := backend.bundleStore().RestoreUntracked(ctx, openedBundle, managed.Worktree); err != nil {
		return workspace, fmt.Errorf("restore exact dirty Try untracked files: %w", err)
	}
	if _, err := backend.manager().Inspect(ctx, managerRequest); err != nil {
		return workspace, fmt.Errorf("inspect seeded dirty Try worktree: %w", err)
	}
	captureRequest := sourcesnapshot.Request{
		Source: request.Source, RepositoryRoot: managed.Worktree,
		RegisteredGitCommonDir:      request.RegisteredGitCommonDir,
		RegisteredGitCommonIdentity: request.RegisteredGitCommonIdentity,
		BaseCommit:                  request.Snapshot.BaseCommit, ExpectedHead: request.Snapshot.HeadCommit,
		Try: backend.tryID(request), CapturedAt: request.Snapshot.CapturedAt,
	}
	verified, err := backend.capturer().VerifyDirty(ctx, captureRequest, request.Snapshot)
	if err != nil {
		return workspace, fmt.Errorf("recapture seeded dirty Try source: %w", errors.Join(ErrSeedMismatch, err))
	}
	if verified.Digest != request.Snapshot.Digest {
		return workspace, ErrSeedMismatch
	}
	if err := clearPreparationMarker(lockRoot); err != nil {
		return workspace, err
	}
	return workspace, nil
}

const preparationMarkerName = "preparing"

// VerifyPrepared re-captures the complete canonical snapshot without taking the
// workspace lock itself. Execution callers hold WithWorkspaceLock across this
// check and process creation; Prepare uses the same verifier while already locked.
func (backend NativeGit) VerifyPrepared(ctx context.Context, request PrepareRequest) (Workspace, error) {
	if err := backend.validateRequest(request); err != nil {
		return Workspace{}, err
	}
	inspection, err := backend.manager().Inspect(ctx, backend.managerRequest(request))
	if err != nil {
		return Workspace{}, err
	}
	workspace := backend.workspace(request, inspection.Workspace)
	if err := backend.verifyPreparedSeed(ctx, request, inspection.Workspace); err != nil {
		return workspace, err
	}
	return workspace, nil
}

func (backend NativeGit) verifyPreparedSeed(ctx context.Context, request PrepareRequest, managed experimentgit.Workspace) error {
	inspection, err := backend.manager().Inspect(ctx, backend.managerRequest(request))
	if err != nil {
		return err
	}
	if inspection.Workspace.Worktree != managed.Worktree || inspection.HeadCommit != inspection.Workspace.BaseCommit {
		return ErrSeedMismatch
	}
	if request.Snapshot.State == research.SourceSnapshotClean {
		if !inspection.Clean || len(inspection.Paths) != 0 {
			return ErrSeedMismatch
		}
		observed, captureErr := backend.capturer().CaptureClean(ctx, sourcesnapshot.Request{
			Source: request.Source, RepositoryRoot: managed.Worktree,
			RegisteredGitCommonDir:      request.RegisteredGitCommonDir,
			RegisteredGitCommonIdentity: request.RegisteredGitCommonIdentity,
			BaseCommit:                  request.Snapshot.BaseCommit,
			ExpectedHead:                request.Snapshot.HeadCommit,
			AllowNoChanges:              len(request.Snapshot.ChangeSet) == 0,
			AllowRetiredSource:          request.AllowRetiredSource,
			CapturedAt:                  request.Snapshot.CapturedAt,
		})
		if captureErr != nil || observed.Digest != request.Snapshot.Digest {
			return errors.Join(ErrSeedMismatch, captureErr)
		}
		return nil
	}
	captureRequest := sourcesnapshot.Request{
		Source: request.Source, RepositoryRoot: managed.Worktree,
		RegisteredGitCommonDir:      request.RegisteredGitCommonDir,
		RegisteredGitCommonIdentity: request.RegisteredGitCommonIdentity,
		BaseCommit:                  request.Snapshot.BaseCommit, ExpectedHead: request.Snapshot.HeadCommit,
		Try: backend.tryID(request), CapturedAt: request.Snapshot.CapturedAt,
	}
	verified, err := backend.capturer().VerifyDirty(ctx, captureRequest, request.Snapshot)
	if err != nil || verified.Digest != request.Snapshot.Digest {
		return errors.Join(ErrSeedMismatch, err)
	}
	return nil
}

func preparationMarkerContent(request PrepareRequest) string {
	return "exp.workspace-preparation/v1\n" + request.ProjectID.String() + "\n" + request.Source.ID.String() + "\n" + request.OwnerID.String() + "\n" + request.Snapshot.Digest + "\n"
}

func existingPreparationMarker(root *os.Root, request PrepareRequest) (bool, error) {
	if root == nil {
		return false, errors.New("workspace preparation lock root is unavailable")
	}
	content, info, err := pathx.ReadBoundedRegularFile(context.Background(), root, preparationMarkerName, 1024)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	checked, checkErr := pathx.CheckPrivateFile(root, preparationMarkerName, 0o600, "workspace preparation marker")
	if checkErr != nil || !os.SameFile(info, checked) || string(content) != preparationMarkerContent(request) {
		return true, errors.New("workspace preparation marker does not match the requested identity")
	}
	return true, nil
}

func ensurePreparationMarker(root *os.Root, request PrepareRequest) (bool, error) {
	exists, err := existingPreparationMarker(root, request)
	if err != nil || exists {
		return exists, err
	}
	want := preparationMarkerContent(request)
	file, err := root.OpenFile(preparationMarkerName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	_, writeErr := file.WriteString(want)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return false, errors.Join(writeErr, syncErr, closeErr)
	}
	protected, err := root.Open(preparationMarkerName)
	if err != nil {
		return false, err
	}
	if err := errors.Join(pathx.ProtectPrivateOpenFile(protected, 0o600), protected.Close()); err != nil {
		return false, err
	}
	return false, pathx.SyncRoot(root)
}

func clearPreparationMarker(root *os.Root) error {
	if err := root.Remove(preparationMarkerName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return pathx.SyncRoot(root)
}

// Inspect validates the deterministic manager location and reports its state.
func (backend NativeGit) Inspect(ctx context.Context, request PrepareRequest) (Inspection, error) {
	if err := backend.validateRequest(request); err != nil {
		return Inspection{}, err
	}
	managed, err := backend.manager().Inspect(ctx, backend.managerRequest(request))
	if err != nil {
		return Inspection{}, err
	}
	if request.Snapshot.State == research.SourceSnapshotDirty {
		if err := backend.verifyProtectedSeedPaths(ctx, request, managed.Workspace); err != nil {
			return backend.inspection(request, managed), err
		}
	}
	return backend.inspection(request, managed), nil
}

func (backend NativeGit) verifyProtectedSeedPaths(ctx context.Context, request PrepareRequest, managed experimentgit.Workspace) error {
	bundle, err := backend.bundleStore().Open(ctx, seedBundleDigest(request))
	if err != nil {
		return fmt.Errorf("open dirty seed for inspection: %w", err)
	}
	if err := validateBundleBinding(bundle, request); err != nil {
		return err
	}
	if bundle.Manifest.SeedPaths == nil {
		if len(request.AllowedPaths) != 0 || len(request.AllowedPathGlobs) != 0 {
			return fmt.Errorf("legacy dirty seed cannot distinguish writable paths: %w", ErrSeedMismatch)
		}
		return backend.verifyPreparedSeed(ctx, request, managed)
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(managed.Worktree)
	if err != nil {
		return fmt.Errorf("open managed dirty workspace: %w", err)
	}
	defer root.Close()
	for _, expected := range bundle.Manifest.SeedPaths {
		semantic, ok := seedSemanticPath(request.Source.Subdir, expected.Path)
		if !ok {
			return fmt.Errorf("dirty seed path is outside Source subdir: %w", ErrSeedMismatch)
		}
		if seedPathWritable(request, semantic) {
			continue
		}
		info, statErr := root.Lstat(expected.Path)
		if !expected.Exists {
			if !errors.Is(statErr, fs.ErrNotExist) {
				return fmt.Errorf("protected deleted seed path %s reappeared: %w", expected.Path, ErrSeedMismatch)
			}
			continue
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != fs.FileMode(expected.Mode).Perm() || info.Size() != expected.Size {
			return fmt.Errorf("protected dirty seed path %s changed metadata: %w", expected.Path, errors.Join(ErrSeedMismatch, statErr))
		}
		file, opened, openErr := pathx.OpenRegularFileNoFollow(root, expected.Path)
		if openErr != nil {
			return fmt.Errorf("open protected dirty seed path %s: %w", expected.Path, errors.Join(ErrSeedMismatch, openErr))
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, io.LimitReader(file, expected.Size+1))
		afterOpen, openStatErr := file.Stat()
		afterPath, pathErr := root.Lstat(expected.Path)
		closeErr := file.Close()
		if copyErr != nil || openStatErr != nil || pathErr != nil || closeErr != nil || written != expected.Size ||
			!os.SameFile(info, opened) || !os.SameFile(info, afterOpen) || !os.SameFile(info, afterPath) ||
			afterPath.Mode()&os.ModeSymlink != 0 || afterOpen.Mode().Perm() != fs.FileMode(expected.Mode).Perm() ||
			"sha256:"+hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("protected dirty seed path %s changed bytes or identity: %w", expected.Path, errors.Join(ErrSeedMismatch, copyErr, openStatErr, pathErr, closeErr))
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := pathx.VerifyRootPath(managed.Worktree, root); err != nil {
		return fmt.Errorf("managed dirty workspace changed identity: %w", errors.Join(ErrSeedMismatch, err))
	}
	return nil
}

func seedSemanticPath(subdir, repositoryPath string) (string, bool) {
	if subdir == "." {
		return repositoryPath, repositoryPath != ""
	}
	prefix := strings.TrimSuffix(subdir, "/") + "/"
	if !strings.HasPrefix(repositoryPath, prefix) {
		return "", false
	}
	semantic := strings.TrimPrefix(repositoryPath, prefix)
	return semantic, semantic != ""
}

func seedPathWritable(request PrepareRequest, semantic string) bool {
	for _, allowed := range request.AllowedPaths {
		if allowed == semantic {
			return true
		}
	}
	return experimentgit.AllowsSemanticPath(nil, request.AllowedPathGlobs, semantic)
}

// CleanupOrHandoff never commits, merges, or removes branches. Handoff is
// non-destructive; cleanup accepts only clean-at-base worktrees or a dirty
// worktree that still round-trips exactly to its authenticated private seed.
func (backend NativeGit) CleanupOrHandoff(ctx context.Context, request PrepareRequest, action FinalizeAction) (Inspection, error) {
	if err := backend.validateRequest(request); err != nil {
		return Inspection{}, err
	}
	var inspection Inspection
	err := withWorkspaceLock(ctx, request, func(*os.Root) error {
		var finalizeErr error
		inspection, finalizeErr = backend.cleanupOrHandoffLocked(ctx, request, action)
		return finalizeErr
	})
	return inspection, err
}

func (backend NativeGit) cleanupOrHandoffLocked(ctx context.Context, request PrepareRequest, action FinalizeAction) (Inspection, error) {
	switch action {
	case FinalizeHandoff:
		return Inspection{}, &UnsupportedCapabilityError{Provider: NativeGitName, Capability: CapabilityHandoff}
	case FinalizeCleanup:
		managerRequest := backend.managerRequest(request)
		_, inspectErr := backend.manager().Inspect(ctx, managerRequest)
		workspaceAbsent := errors.Is(inspectErr, fs.ErrNotExist)
		if inspectErr != nil && !workspaceAbsent {
			return Inspection{}, errors.Join(ErrUnsafeCleanup, inspectErr)
		}
		cleanupBundle := request.SeedBundle
		if request.Snapshot.State == research.SourceSnapshotDirty {
			opened, openErr := backend.bundleStore().Open(ctx, seedBundleDigest(request))
			switch {
			case openErr == nil:
				if bindErr := validateBundleBinding(opened, request); bindErr != nil {
					return Inspection{}, fmt.Errorf("validate private seed binding before cleanup: %w", errors.Join(ErrUnsafeCleanup, bindErr))
				}
				cleanupBundle = &opened
			case workspaceAbsent && errors.Is(openErr, sourcesnapshot.ErrBundleNotFound):
				cleanupBundle = nil
			default:
				return Inspection{}, fmt.Errorf("validate private seed before cleanup: %w", errors.Join(ErrUnsafeCleanup, openErr))
			}
		}
		if workspaceAbsent {
			inspection := Inspection{}
			if cleanupBundle != nil {
				if err := backend.bundleStore().Cleanup(ctx, *cleanupBundle); err != nil {
					return inspection, fmt.Errorf("private seed cleanup remains incomplete: %w", err)
				}
			}
			return inspection, nil
		}
		var managed experimentgit.Inspection
		var err error
		if request.Snapshot.State == research.SourceSnapshotDirty {
			managed, err = backend.manager().CleanupSeeded(ctx, managerRequest, func(verifyContext context.Context, worktree string) error {
				captureRequest := sourcesnapshot.Request{
					Source: request.Source, RepositoryRoot: worktree,
					RegisteredGitCommonDir:      request.RegisteredGitCommonDir,
					RegisteredGitCommonIdentity: request.RegisteredGitCommonIdentity,
					BaseCommit:                  request.Snapshot.BaseCommit,
					ExpectedHead:                request.Snapshot.HeadCommit,
					Try:                         backend.tryID(request),
					CapturedAt:                  request.Snapshot.CapturedAt,
					AllowRetiredSource:          request.AllowRetiredSource,
				}
				_, verifyErr := backend.capturer().VerifyDirty(verifyContext, captureRequest, request.Snapshot)
				return verifyErr
			})
		} else {
			managed, err = backend.manager().Cleanup(ctx, managerRequest)
		}
		inspection := backend.inspection(request, managed)
		if err != nil {
			return inspection, errors.Join(ErrUnsafeCleanup, err)
		}
		inspection.Removed = true
		inspection.RetireProvider = NativeGitName
		if cleanupBundle != nil {
			if err := backend.bundleStore().Cleanup(ctx, *cleanupBundle); err != nil {
				return inspection, fmt.Errorf("worktree removed but private seed cleanup failed: %w", err)
			}
		}
		return inspection, nil
	default:
		return Inspection{}, fmt.Errorf("unknown finalization action %q: %w", action, ErrInvalidRequest)
	}
}

func (backend NativeGit) validateRequest(request PrepareRequest) error {
	if request.ProjectID.IsZero() || request.Source == nil || request.Source.ID.IsZero() || request.Source.ID.Kind() != research.KindSource || request.OwnerID.IsZero() {
		return ErrInvalidRequest
	}
	if err := research.Validate(request.Source); err != nil || request.Source.Kind != research.SourceGit || request.Source.State != research.SourceActive && !(request.AllowRetiredSource && request.Source.State == research.SourceRetired) {
		return fmt.Errorf("invalid canonical Source: %w", errors.Join(ErrInvalidRequest, err))
	}
	if request.Snapshot.Source != request.Source.ID || request.Snapshot.Subdir != request.Source.Subdir || request.Snapshot.Digest == "" {
		return ErrInvalidRequest
	}
	if err := sourcesnapshot.Validate(request.Snapshot); err != nil {
		return fmt.Errorf("invalid Source snapshot: %w", errors.Join(ErrInvalidRequest, err))
	}
	if request.RepositoryRoot == "" || request.RegisteredGitCommonDir == "" {
		return ErrInvalidRequest
	}
	if request.RegisteredGitCommonIdentity != "" {
		observed, err := pathx.DirectoryFilesystemIdentity(request.RegisteredGitCommonDir)
		if err != nil || observed != request.RegisteredGitCommonIdentity {
			return fmt.Errorf("registered Git common filesystem identity differs: %w", errors.Join(ErrInvalidRequest, err))
		}
	}
	switch request.Snapshot.State {
	case research.SourceSnapshotClean:
		if request.OwnerID.Kind() != research.KindTry && request.OwnerID.Kind() != research.KindExperiment && request.OwnerID.Kind() != research.KindAttempt || request.SeedBundle != nil {
			return ErrInvalidRequest
		}
	case research.SourceSnapshotDirty:
		if request.OwnerID.Kind() != research.KindTry && request.OwnerID.Kind() != research.KindAttempt || seedBundleDigest(request) == "" || backend.tryID(request).IsZero() {
			return ErrInvalidRequest
		}
		if request.SeedBundle != nil && request.SeedBundleDigest != "" && request.SeedBundle.Digest() != request.SeedBundleDigest {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	if !request.TryID.IsZero() && request.TryID.Kind() != research.KindTry {
		return ErrInvalidRequest
	}
	return nil
}

func seedBundleDigest(request PrepareRequest) string {
	if request.SeedBundleDigest != "" {
		return request.SeedBundleDigest
	}
	if request.SeedBundle != nil {
		return request.SeedBundle.Digest()
	}
	return ""
}

func (backend NativeGit) tryID(request PrepareRequest) research.ID {
	if !request.TryID.IsZero() {
		return request.TryID
	}
	if request.OwnerID.Kind() == research.KindTry {
		return request.OwnerID
	}
	return research.ID{}
}

func (backend NativeGit) materializeSubmodules(ctx context.Context, request PrepareRequest, managed experimentgit.Workspace, submodules []sourcesnapshot.BundleSubmodule) error {
	for _, submodule := range submodules {
		sourcePath, err := pathx.ResolveUnderNoSymlinks(request.RepositoryRoot, submodule.Path, false)
		if err != nil {
			return fmt.Errorf("resolve captured submodule %q: %w", submodule.Path, errors.Join(ErrSeedMismatch, err))
		}
		sourceInfo, err := os.Lstat(sourcePath)
		if err != nil || sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
			return fmt.Errorf("captured submodule %q is no longer a real directory: %w", submodule.Path, errors.Join(ErrSeedMismatch, err))
		}
		target, err := pathx.ResolveUnderNoSymlinks(managed.Worktree, submodule.Path, false)
		if err != nil {
			return fmt.Errorf("resolve managed submodule target %q: %w", submodule.Path, errors.Join(ErrSeedMismatch, err))
		}
		if targetInfo, statErr := os.Lstat(target); statErr == nil {
			if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() {
				return fmt.Errorf("managed submodule target %q already exists unsafely: %w", submodule.Path, ErrSeedMismatch)
			}
			entries, readErr := os.ReadDir(target)
			if readErr != nil || len(entries) != 0 {
				return fmt.Errorf("managed submodule target %q is not empty: %w", submodule.Path, errors.Join(ErrSeedMismatch, readErr))
			}
			if removeErr := os.Remove(target); removeErr != nil {
				return fmt.Errorf("remove empty managed submodule placeholder %q: %w", submodule.Path, removeErr)
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("inspect managed submodule target %q: %w", submodule.Path, statErr)
		}
		// A linked worktree would register target in the production submodule's
		// common directory and parent cleanup could not deregister it. A shared
		// local clone reuses the same objects without creating such ownership.
		if _, err := backend.runGit(ctx, sourcePath, "-c", "core.hooksPath="+os.DevNull,
			"clone", "--quiet", "--shared", "--no-checkout", "--no-recurse-submodules", "--", sourcePath, target); err != nil {
			return fmt.Errorf("materialize captured submodule %q from local objects: %w", submodule.Path, err)
		}
		complete := false
		defer func() {
			if !complete {
				_ = os.RemoveAll(target)
			}
		}()
		if _, err := backend.runGit(ctx, target, "-c", "core.hooksPath="+os.DevNull,
			"checkout", "--quiet", "--detach", "--no-recurse-submodules", submodule.Commit); err != nil {
			return fmt.Errorf("check out captured submodule %q: %w", submodule.Path, err)
		}
		resolvedTarget, err := filepath.EvalSymlinks(target)
		if err != nil || filepath.Clean(resolvedTarget) != target {
			return fmt.Errorf("verify managed submodule target %q: %w", submodule.Path, errors.Join(ErrSeedMismatch, err))
		}
		sourceRepository, sourceErr := gitx.DiscoverWithRunner(ctx, sourcePath, backend.runner())
		targetRepository, targetErr := gitx.DiscoverWithRunner(ctx, target, backend.runner())
		if sourceErr != nil || targetErr != nil || targetRepository.Root != target || targetRepository.GitCommonDir == sourceRepository.GitCommonDir {
			return fmt.Errorf("verify unregistered managed submodule %q: %w", submodule.Path, errors.Join(ErrSeedMismatch, sourceErr, targetErr))
		}
		registrations, listErr := gitx.Worktrees(ctx, sourceRepository.Root, backend.runner())
		if listErr != nil {
			return fmt.Errorf("verify submodule worktree registrations %q: %w", submodule.Path, listErr)
		}
		for _, registration := range registrations {
			if filepath.Clean(registration.Root) == target {
				return fmt.Errorf("managed submodule %q was registered as a production worktree: %w", submodule.Path, ErrSeedMismatch)
			}
		}
		complete = true
	}
	return nil
}

func validateBundleBinding(bundle sourcesnapshot.Bundle, request PrepareRequest) error {
	manifest := bundle.Manifest
	if manifest.Source != request.Source.ID.String() || manifest.Subdir != request.Source.Subdir ||
		manifest.GitObjectFormat != request.Snapshot.GitObjectFormat || manifest.BaseCommit != request.Snapshot.BaseCommit ||
		manifest.HeadCommit != request.Snapshot.HeadCommit || manifest.SnapshotDigest != request.Snapshot.Digest ||
		manifest.DirtyDigest != request.Snapshot.DirtyDigest || manifest.PolicyVersion != request.Snapshot.PolicyVersion {
		return ErrSeedMismatch
	}
	return nil
}

func (backend NativeGit) managerRequest(request PrepareRequest) experimentgit.Request {
	return experimentgit.Request{
		RepositoryRoot: request.RepositoryRoot, BaseCommit: request.Snapshot.HeadCommit,
		ProjectID: request.ProjectID, SourceID: request.Source.ID,
		OwnerID: request.OwnerID, OwnerTitle: request.OwnerTitle,
		SourceSubdir: request.Source.Subdir, RegisteredGitCommonDir: request.RegisteredGitCommonDir,
		AllowedPaths: request.AllowedPaths, BaselinePaths: request.BaselinePaths, AllowedPathGlobs: request.AllowedPathGlobs,
		CanonicalMetadataRoot: request.CanonicalMetadataRoot,
		AllowDirtySource:      request.Snapshot.State == research.SourceSnapshotDirty,
	}
}

func (backend NativeGit) manager() experimentgit.Manager {
	return experimentgit.Manager{Git: backend.runner(), DataHome: backend.DataHome}
}

func (backend NativeGit) capturer() sourcesnapshot.Capturer {
	return sourcesnapshot.Capturer{
		Git: backend.runner(), FileSystem: backend.FileSystem,
		Clock: backend.Clock, Bundles: backend.bundleStore(),
	}
}

func (backend NativeGit) bundleStore() *sourcesnapshot.BundleStore {
	if backend.Bundles != nil {
		if backend.Bundles.FileSystem == nil && backend.FileSystem != nil {
			copy := *backend.Bundles
			copy.FileSystem = backend.FileSystem
			return &copy
		}
		return backend.Bundles
	}
	return &sourcesnapshot.BundleStore{FileSystem: backend.FileSystem}
}

func (backend NativeGit) runner() gitx.Runner {
	if backend.Git != nil {
		return backend.Git
	}
	return gitx.ExecRunner{}
}

func (backend NativeGit) runGit(ctx context.Context, directory string, arguments ...string) (string, error) {
	args := append([]string(nil), arguments...)
	stdout, stderr, err := backend.runner().Run(ctx, directory, args)
	if err != nil {
		return "", &gitx.Error{Dir: directory, Args: args, Stderr: stderr, Err: err}
	}
	return stdout, nil
}

func (backend NativeGit) workspace(request PrepareRequest, managed experimentgit.Workspace) Workspace {
	workspace := Workspace{
		Backend: NativeGitName, ProjectID: request.ProjectID, SourceID: request.Source.ID, OwnerID: request.OwnerID,
		Root: managed.Worktree, CWD: managed.CWD, Branch: managed.Branch,
		HeadCommit: request.Snapshot.HeadCommit, SnapshotDigest: request.Snapshot.Digest,
	}
	workspace.SeedDigest = seedBundleDigest(request)
	return workspace
}

func (backend NativeGit) inspection(request PrepareRequest, managed experimentgit.Inspection) Inspection {
	return Inspection{
		Workspace:  backend.workspace(request, managed.Workspace),
		HeadCommit: managed.HeadCommit, Clean: managed.Clean,
		Paths: append([]string(nil), managed.Paths...),
	}
}

var _ Backend = NativeGit{}
