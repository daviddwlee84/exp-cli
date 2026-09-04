package workspacebackend

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/experimentgit"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
)

func TestNativeGitDirtySeedRoundTripLeavesProductionUntouched(t *testing.T) {
	repository, head := newBackendRepository(t)
	info, err := gitx.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	source := backendSource(t, "src_01a09000-0000-7101-8000-000000000101", "services/api")
	tryID := backendID(t, "try_01a09000-0000-7102-8000-000000000102", research.KindTry)
	writeBackendFile(t, filepath.Join(repository, "services", "api", "model.txt"), "staged binary\x00state", 0o644)
	runBackendGit(t, repository, "add", "--", "services/api/model.txt")
	writeBackendFile(t, filepath.Join(repository, "services", "api", "model.txt"), "dirty tracked\x00final", 0o644)
	writeBackendFile(t, filepath.Join(repository, "services", "api", "new.sh"), "#!/bin/sh\nseed canary\n", 0o755)
	statusBefore := runBackendGit(t, repository, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	bundleStore := &sourcesnapshot.BundleStore{Root: filepath.Join(canonicalBackendTemp(t), "seed bundles")}
	captured, err := (sourcesnapshot.Capturer{Bundles: bundleStore}).CaptureDirty(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, Try: tryID,
		CapturedAt: time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := backendProjectID(t, "01a09000-0000-7100-8000-000000000100")
	dataHome := filepath.Join(canonicalBackendTemp(t), "data home with spaces")
	backend := NativeGit{DataHome: dataHome, Bundles: bundleStore}
	request := PrepareRequest{
		ProjectID: projectID, Source: source, RepositoryRoot: repository,
		RegisteredGitCommonDir: info.GitCommonDir, OwnerID: tryID, OwnerTitle: "Dirty Seed Try",
		Snapshot: captured.Snapshot, AllowedPathGlobs: []string{"**"}, SeedBundle: captured.Bundle,
	}
	workspace, err := backend.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(dataHome, "exp", "worktrees", projectID.String(), source.ID.String(), tryID.String()+"-dirty-seed-try")
	if workspace.Root != wantRoot || workspace.CWD != filepath.Join(wantRoot, "services", "api") || workspace.SnapshotDigest != captured.Snapshot.Digest || workspace.SeedDigest != captured.Bundle.Digest() {
		t.Fatalf("workspace = %#v; want root %s", workspace, wantRoot)
	}
	if got := readBackendFile(t, filepath.Join(workspace.Root, "services", "api", "model.txt")); got != "dirty tracked\x00final" {
		t.Fatalf("seeded tracked bytes = %q", got)
	}
	if got := readBackendFile(t, filepath.Join(workspace.Root, "services", "api", "new.sh")); got != "#!/bin/sh\nseed canary\n" {
		t.Fatalf("seeded untracked bytes = %q", got)
	}
	if info, err := os.Lstat(filepath.Join(workspace.Root, "services", "api", "new.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("seeded mode = %v, %v", info, err)
	}
	if statusAfter := runBackendGit(t, repository, "status", "--porcelain=v2", "-z", "--untracked-files=all"); statusAfter != statusBefore {
		t.Fatalf("production status changed:\nbefore=%q\nafter=%q", statusBefore, statusAfter)
	}
	if got := backendGitLine(t, repository, "rev-parse", "HEAD"); got != head {
		t.Fatalf("production HEAD = %s, want %s", got, head)
	}
	inspection, err := backend.Inspect(t.Context(), request)
	if err != nil || inspection.Clean || !reflect.DeepEqual(inspection.Paths, []string{"services/api/model.txt", "services/api/new.sh"}) {
		t.Fatalf("inspection = %#v, %v", inspection, err)
	}
	cleaned, err := backend.CleanupOrHandoff(t.Context(), request, FinalizeCleanup)
	if err != nil || !cleaned.Removed {
		t.Fatalf("exact dirty-seed cleanup = %#v, %v", cleaned, err)
	}
	if _, err := bundleStore.Open(t.Context(), captured.Bundle.Digest()); !errors.Is(err, sourcesnapshot.ErrBundleNotFound) {
		t.Fatalf("cleaned seed bundle remains: %v", err)
	}
}

func TestNativeGitDirtyCleanupRefusesChangedSeedAndRetainsBundle(t *testing.T) {
	repository, head := newBackendRepository(t)
	info, err := gitx.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	source := backendSource(t, "src_01a09000-0000-7151-8000-000000000151", "services/api")
	tryID := backendID(t, "try_01a09000-0000-7152-8000-000000000152", research.KindTry)
	writeBackendFile(t, filepath.Join(repository, "services", "api", "model.txt"), "captured dirty\n", 0o644)
	bundleStore := &sourcesnapshot.BundleStore{Root: filepath.Join(canonicalBackendTemp(t), "changed-seed-bundles")}
	captured, err := (sourcesnapshot.Capturer{Bundles: bundleStore}).CaptureDirty(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, Try: tryID,
		CapturedAt: time.Date(2026, 9, 3, 15, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := NativeGit{DataHome: filepath.Join(canonicalBackendTemp(t), "changed-seed-data"), Bundles: bundleStore}
	request := PrepareRequest{
		ProjectID: backendProjectID(t, "01a09000-0000-7150-8000-000000000150"),
		Source:    source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		OwnerID: tryID, OwnerTitle: "Changed Dirty Seed", Snapshot: captured.Snapshot,
		AllowedPaths: []string{"model.txt"}, SeedBundle: captured.Bundle,
	}
	workspace, err := backend.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	writeBackendFile(t, filepath.Join(workspace.CWD, "model.txt"), "changed after seed\n", 0o644)
	if _, err := backend.Inspect(t.Context(), request); err != nil {
		t.Fatalf("structural inspection should permit the already-allowed path: %v", err)
	}
	if _, err := backend.Prepare(t.Context(), request); !errors.Is(err, ErrSeedMismatch) {
		t.Fatalf("idempotent Prepare accepted changed seed bytes: %v", err)
	}
	if err := WithWorkspaceLock(t.Context(), request, func() error {
		_, verifyErr := backend.VerifyPrepared(t.Context(), request)
		return verifyErr
	}); !errors.Is(err, ErrSeedMismatch) {
		t.Fatalf("spawn-time verifier accepted changed seed bytes: %v", err)
	}
	if _, err := backend.CleanupOrHandoff(t.Context(), request, FinalizeCleanup); !errors.Is(err, ErrUnsafeCleanup) || !errors.Is(err, experimentgit.ErrCleanupRefused) {
		t.Fatalf("changed dirty cleanup error = %v", err)
	}
	if _, err := bundleStore.Open(t.Context(), captured.Bundle.Digest()); err != nil {
		t.Fatalf("refused dirty cleanup removed bundle: %v", err)
	}
	if _, err := os.Stat(workspace.Root); err != nil {
		t.Fatalf("refused dirty cleanup removed worktree: %v", err)
	}
}

func TestNativeGitCleanNamespacesCleanupAndBranchRetention(t *testing.T) {
	repository, head := newBackendRepository(t)
	info, _ := gitx.Discover(t.Context(), repository)
	dataHome := filepath.Join(canonicalBackendTemp(t), "xdg-data")
	owner := backendID(t, "exp_01a09000-0000-7203-8000-000000000203", research.KindExperiment)
	projectOne := backendProjectID(t, "01a09000-0000-7200-8000-000000000200")
	projectTwo := backendProjectID(t, "01a09000-0000-7201-8000-000000000201")
	sourceOne := backendSource(t, "src_01a09000-0000-7202-8000-000000000202", "services/api")
	sourceTwo := backendSource(t, "src_01a09000-0000-7204-8000-000000000204", "services/api")
	backend := NativeGit{DataHome: dataHome}

	prepare := func(projectID research.UUID, source *research.Source) (PrepareRequest, Workspace) {
		t.Helper()
		snapshot, err := (sourcesnapshot.Capturer{}).CaptureClean(t.Context(), sourcesnapshot.Request{
			Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
			BaseCommit: head, ExpectedHead: head, AllowNoChanges: true,
			CapturedAt: time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		request := PrepareRequest{
			ProjectID: projectID, Source: source, RepositoryRoot: repository,
			RegisteredGitCommonDir: info.GitCommonDir, OwnerID: owner, OwnerTitle: "Formal α Study",
			Snapshot: snapshot, AllowedPaths: []string{"model.txt"},
		}
		workspace, err := backend.Prepare(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		return request, workspace
	}
	firstRequest, first := prepare(projectOne, sourceOne)
	secondRequest, second := prepare(projectTwo, sourceTwo)
	if first.Root == second.Root || first.Branch == second.Branch {
		t.Fatalf("Project/Source namespaces collided: %#v %#v", first, second)
	}
	for request, workspace := range map[*PrepareRequest]Workspace{&firstRequest: first, &secondRequest: second} {
		inspection, err := backend.CleanupOrHandoff(t.Context(), *request, FinalizeCleanup)
		if err != nil || !inspection.Removed {
			t.Fatalf("clean cleanup = %#v, %v", inspection, err)
		}
		if _, err := os.Lstat(workspace.Root); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("worktree remains: %v", err)
		}
		if got := runBackendGit(t, repository, "show-ref", "--verify", "--hash", "refs/heads/"+workspace.Branch); strings.TrimSpace(got) != head {
			t.Fatalf("retained branch = %q", got)
		}
	}
	if got := backendGitLine(t, repository, "rev-parse", "HEAD"); got != head {
		t.Fatalf("production HEAD changed to %s", got)
	}
}

func TestNativeGitSubdirAllowlistTranslationAndConditionalMetadata(t *testing.T) {
	repository, head := newBackendRepository(t)
	info, _ := gitx.Discover(t.Context(), repository)
	source := backendSource(t, "src_01a09000-0000-7301-8000-000000000301", "services/api")
	owner := backendID(t, "try_01a09000-0000-7302-8000-000000000302", research.KindTry)
	snapshot, err := (sourcesnapshot.Capturer{}).CaptureClean(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, AllowNoChanges: true,
		CapturedAt: time.Date(2026, 9, 3, 17, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := NativeGit{DataHome: filepath.Join(canonicalBackendTemp(t), "data")}
	request := PrepareRequest{
		ProjectID: backendProjectID(t, "01a09000-0000-7300-8000-000000000300"),
		Source:    source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		OwnerID: owner, OwnerTitle: "Allowlist Translation", Snapshot: snapshot,
		AllowedPaths: []string{"model.txt"},
	}
	workspace, err := backend.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	writeBackendFile(t, filepath.Join(workspace.CWD, "model.txt"), "allowed\n", 0o644)
	inspection, err := backend.Inspect(t.Context(), request)
	if err != nil || !reflect.DeepEqual(inspection.Paths, []string{"services/api/model.txt"}) {
		t.Fatalf("translated inspection = %#v, %v", inspection, err)
	}
	writeBackendFile(t, filepath.Join(workspace.Root, "outside.txt"), "outside\n", 0o644)
	if _, err := backend.Inspect(t.Context(), request); !errors.Is(err, experimentgit.ErrPathNotAllowed) {
		t.Fatalf("outside-subdir inspection error = %v", err)
	}
	if _, err := backend.CleanupOrHandoff(t.Context(), request, FinalizeCleanup); !errors.Is(err, ErrUnsafeCleanup) {
		t.Fatalf("changed worktree cleanup error = %v", err)
	}

	// External Sources do not inherit the embedded canonical experiments/ ban.
	externalSource := backendSource(t, "src_01a09000-0000-7303-8000-000000000303", ".")
	externalSnapshot := snapshot
	externalSnapshot.Source = externalSource.ID
	externalSnapshot.Subdir = "."
	externalSnapshot.Digest, _ = research.SourceSnapshotDigest(externalSnapshot)
	externalRequest := experimentgit.Request{
		RepositoryRoot: repository, BaseCommit: head,
		ProjectID: request.ProjectID, SourceID: externalSource.ID,
		OwnerID: backendID(t, "try_01a09000-0000-7304-8000-000000000304", research.KindTry), OwnerTitle: "External Metadata",
		SourceSubdir: ".", RegisteredGitCommonDir: info.GitCommonDir,
		AllowedPaths: []string{"experiments/local.txt"},
	}
	manager := experimentgit.Manager{DataHome: filepath.Join(canonicalBackendTemp(t), "external-data")}
	externalWorkspace, err := manager.Prepare(t.Context(), externalRequest)
	if err != nil {
		t.Fatal(err)
	}
	writeBackendFile(t, filepath.Join(externalWorkspace.Worktree, "experiments", "local.txt"), "external\n", 0o644)
	if _, err := manager.Inspect(t.Context(), externalRequest); err != nil {
		t.Fatalf("arbitrary external experiments path rejected: %v", err)
	}
	externalRequest.CanonicalMetadataRoot = "experiments"
	if _, err := manager.Inspect(t.Context(), externalRequest); !errors.Is(err, experimentgit.ErrForbiddenMetadata) {
		t.Fatalf("explicit canonical metadata exclusion error = %v", err)
	}
}

func TestNativeGitDirtySeedWithCleanDirectSubmodule(t *testing.T) {
	child, _ := newBackendRepository(t)
	repository, _ := newBackendRepository(t)
	runBackendGit(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", "-q", child, "services/api/module")
	runBackendGit(t, repository, "commit", "-qam", "add direct module")
	head := backendGitLine(t, repository, "rev-parse", "HEAD")
	info, _ := gitx.Discover(t.Context(), repository)
	source := backendSource(t, "src_01a09000-0000-7501-8000-000000000501", "services/api")
	owner := backendID(t, "try_01a09000-0000-7502-8000-000000000502", research.KindTry)
	writeBackendFile(t, filepath.Join(repository, "services", "api", "model.txt"), "dirty with module\n", 0o644)
	store := &sourcesnapshot.BundleStore{Root: filepath.Join(canonicalBackendTemp(t), "module-seed")}
	captured, err := (sourcesnapshot.Capturer{Bundles: store}).CaptureDirty(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, Try: owner,
		CapturedAt: time.Date(2026, 9, 3, 19, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Bundle.Manifest.Submodules) != 1 || captured.Bundle.Manifest.Submodules[0].Path != "services/api/module" {
		t.Fatalf("captured submodules = %#v", captured.Bundle.Manifest.Submodules)
	}
	backend := NativeGit{DataHome: filepath.Join(canonicalBackendTemp(t), "module-data"), Bundles: store}
	request := PrepareRequest{
		ProjectID: backendProjectID(t, "01a09000-0000-7500-8000-000000000500"),
		Source:    source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		OwnerID: owner, OwnerTitle: "Direct Module", Snapshot: captured.Snapshot,
		AllowedPathGlobs: []string{"**"}, SeedBundle: captured.Bundle,
	}
	workspace, err := backend.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got := readBackendFile(t, filepath.Join(workspace.CWD, "module", "README.md")); got != "production\n" {
		t.Fatalf("seeded submodule bytes = %q", got)
	}
	childRepository, err := gitx.Discover(t.Context(), child)
	if err != nil {
		t.Fatal(err)
	}
	registrations, err := gitx.Worktrees(t.Context(), childRepository.Root, gitx.ExecRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if len(registrations) != 1 || registrations[0].Root != childRepository.Root {
		t.Fatalf("managed submodule leaked linked-worktree registration: %#v", registrations)
	}
	if _, err := backend.CleanupOrHandoff(t.Context(), request, FinalizeCleanup); err != nil {
		t.Fatalf("cleanup with materialized submodule: %v", err)
	}
	registrations, err = gitx.Worktrees(t.Context(), childRepository.Root, gitx.ExecRunner{})
	if err != nil || len(registrations) != 1 || registrations[0].Root != childRepository.Root {
		t.Fatalf("submodule registrations after cleanup = %#v, %v", registrations, err)
	}
}

func TestNativeGitRegisteredLinkedWorktreeSource(t *testing.T) {
	repository, head := newBackendRepository(t)
	linked := filepath.Join(canonicalBackendTemp(t), "registered linked source")
	runBackendGit(t, repository, "worktree", "add", "-q", "-b", "linked-source", linked, head)
	linkedInfo, err := gitx.Discover(t.Context(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !linkedInfo.IsLinkedWorktree {
		t.Fatal("fixture was not discovered as a linked worktree")
	}
	source := backendSource(t, "src_01a09000-0000-7401-8000-000000000401", "services/api")
	snapshot, err := (sourcesnapshot.Capturer{}).CaptureClean(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: linked, RegisteredGitCommonDir: linkedInfo.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, AllowNoChanges: true,
		CapturedAt: time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := NativeGit{DataHome: filepath.Join(canonicalBackendTemp(t), "linked-data")}
	request := PrepareRequest{
		ProjectID: backendProjectID(t, "01a09000-0000-7400-8000-000000000400"),
		Source:    source, RepositoryRoot: linked, RegisteredGitCommonDir: linkedInfo.GitCommonDir,
		OwnerID:    backendID(t, "try_01a09000-0000-7402-8000-000000000402", research.KindTry),
		OwnerTitle: "Linked Source", Snapshot: snapshot, AllowedPathGlobs: []string{"**"},
	}
	unsafeDataHome := filepath.Join(repository, "nested-xdg")
	if _, err := (NativeGit{DataHome: unsafeDataHome}).Prepare(t.Context(), request); !errors.Is(err, experimentgit.ErrInvalidRequest) {
		t.Fatalf("managed path inside alternate registered worktree error = %v", err)
	}
	if _, err := os.Lstat(unsafeDataHome); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unsafe XDG path was created before rejection: %v", err)
	}
	workspace, err := backend.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	preparedInfo, err := gitx.Discover(t.Context(), workspace.Root)
	if err != nil || preparedInfo.GitCommonDir != linkedInfo.GitCommonDir {
		t.Fatalf("prepared linked identity = %#v, %v", preparedInfo, err)
	}
	if got := backendGitLine(t, linked, "rev-parse", "HEAD"); got != head {
		t.Fatalf("registered linked checkout HEAD changed to %s", got)
	}
	if status := runBackendGit(t, linked, "status", "--porcelain=v2", "-z", "--untracked-files=all"); status != "" {
		t.Fatalf("registered linked checkout changed: %q", status)
	}
	if _, err := backend.CleanupOrHandoff(t.Context(), request, FinalizeCleanup); err != nil {
		t.Fatal(err)
	}
}

func TestNativeGitRepairsInterruptedDirtyPreparation(t *testing.T) {
	repository, head := newBackendRepository(t)
	info, _ := gitx.Discover(t.Context(), repository)
	source := backendSource(t, "src_01a09000-0000-7801-8000-000000000801", "services/api")
	tryID := backendID(t, "try_01a09000-0000-7802-8000-000000000802", research.KindTry)
	writeBackendFile(t, filepath.Join(repository, "services", "api", "model.txt"), "captured tracked\n", 0o644)
	writeBackendFile(t, filepath.Join(repository, "services", "api", "untracked.txt"), "captured untracked\n", 0o644)
	bundles := &sourcesnapshot.BundleStore{Root: filepath.Join(canonicalBackendTemp(t), "repair-bundles")}
	captured, err := (sourcesnapshot.Capturer{Bundles: bundles}).CaptureDirty(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, Try: tryID, CapturedAt: time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := NativeGit{DataHome: filepath.Join(canonicalBackendTemp(t), "repair-data"), Bundles: bundles}
	request := PrepareRequest{
		ProjectID: backendProjectID(t, "01a09000-0000-7800-8000-000000000800"), Source: source,
		RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		OwnerID: tryID, OwnerTitle: "Interrupted Seed", Snapshot: captured.Snapshot,
		AllowedPathGlobs: []string{"**"}, SeedBundle: captured.Bundle,
	}
	var partial experimentgit.Workspace
	if err := withWorkspaceLock(t.Context(), request, func(lockRoot *os.Root) error {
		if _, markerErr := ensurePreparationMarker(lockRoot, request); markerErr != nil {
			return markerErr
		}
		var prepareErr error
		partial, prepareErr = backend.manager().Prepare(t.Context(), backend.managerRequest(request))
		if prepareErr != nil {
			return prepareErr
		}
		patch, patchErr := bundles.TrackedPatchPath(t.Context(), *captured.Bundle)
		if patchErr != nil {
			return patchErr
		}
		_, applyErr := backend.runGit(t.Context(), partial.Worktree, "apply", "--binary", "--whitespace=nowarn", "--", patch)
		return applyErr
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(partial.Worktree, "services", "api", "untracked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial fixture unexpectedly restored untracked file: %v", err)
	}
	workspace, err := backend.Prepare(t.Context(), request)
	if err != nil {
		t.Fatalf("repair interrupted preparation: %v", err)
	}
	if got := readBackendFile(t, filepath.Join(workspace.CWD, "model.txt")); got != "captured tracked\n" {
		t.Fatalf("repaired tracked bytes = %q", got)
	}
	if got := readBackendFile(t, filepath.Join(workspace.CWD, "untracked.txt")); got != "captured untracked\n" {
		t.Fatalf("repaired untracked bytes = %q", got)
	}
}

func TestNativeGitCleanupResumesAfterResourcesAlreadyRemoved(t *testing.T) {
	repository, head := newBackendRepository(t)
	info, _ := gitx.Discover(t.Context(), repository)
	source := backendSource(t, "src_01a09000-0000-7811-8000-000000000811", "services/api")
	tryID := backendID(t, "try_01a09000-0000-7812-8000-000000000812", research.KindTry)
	writeBackendFile(t, filepath.Join(repository, "services", "api", "model.txt"), "cleanup seed\n", 0o644)
	bundles := &sourcesnapshot.BundleStore{Root: filepath.Join(canonicalBackendTemp(t), "cleanup-resume-bundles")}
	captured, err := (sourcesnapshot.Capturer{Bundles: bundles}).CaptureDirty(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, Try: tryID, CapturedAt: time.Date(2026, 9, 4, 2, 15, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := NativeGit{DataHome: filepath.Join(canonicalBackendTemp(t), "cleanup-resume-data"), Bundles: bundles}
	request := PrepareRequest{
		ProjectID: backendProjectID(t, "01a09000-0000-7810-8000-000000000810"), Source: source,
		RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		OwnerID: tryID, OwnerTitle: "Cleanup Resume", Snapshot: captured.Snapshot,
		AllowedPathGlobs: []string{"**"}, SeedBundle: captured.Bundle, SeedBundleDigest: captured.Bundle.Digest(),
	}
	workspace, err := backend.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.manager().CleanupSeeded(t.Context(), backend.managerRequest(request), func(ctx context.Context, worktree string) error {
		_, verifyErr := backend.capturer().VerifyDirty(ctx, sourcesnapshot.Request{
			Source: source, RepositoryRoot: worktree, RegisteredGitCommonDir: info.GitCommonDir,
			BaseCommit: captured.Snapshot.BaseCommit, ExpectedHead: captured.Snapshot.HeadCommit,
			Try: tryID, CapturedAt: captured.Snapshot.CapturedAt,
		}, captured.Snapshot)
		return verifyErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial cleanup left worktree: %v", err)
	}
	if _, err := backend.CleanupOrHandoff(t.Context(), request, FinalizeCleanup); err != nil {
		t.Fatalf("resume bundle cleanup: %v", err)
	}
	if _, err := backend.CleanupOrHandoff(t.Context(), request, FinalizeCleanup); err != nil {
		t.Fatalf("idempotent absent cleanup: %v", err)
	}
}

func TestNativeGitCleanupWaitsForWorkspaceExecutionLock(t *testing.T) {
	repository, head := newBackendRepository(t)
	info, _ := gitx.Discover(t.Context(), repository)
	source := backendSource(t, "src_01a09000-0000-7821-8000-000000000821", ".")
	owner := backendID(t, "try_01a09000-0000-7822-8000-000000000822", research.KindTry)
	snapshot, err := (sourcesnapshot.Capturer{}).CaptureClean(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, AllowNoChanges: true, CapturedAt: time.Date(2026, 9, 4, 2, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := NativeGit{DataHome: filepath.Join(canonicalBackendTemp(t), "cleanup-lock-data")}
	request := PrepareRequest{ProjectID: backendProjectID(t, "01a09000-0000-7820-8000-000000000820"), Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir, OwnerID: owner, OwnerTitle: "Locked Cleanup", Snapshot: snapshot}
	if _, err := backend.Prepare(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	entered, release, held := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		held <- WithWorkspaceLock(t.Context(), request, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	cleaned := make(chan error, 1)
	go func() {
		_, cleanupErr := backend.CleanupOrHandoff(t.Context(), request, FinalizeCleanup)
		cleaned <- cleanupErr
	}()
	select {
	case err := <-cleaned:
		t.Fatalf("cleanup bypassed active workspace lock: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if err := <-cleaned; err != nil {
		t.Fatal(err)
	}
}

func newBackendRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := filepath.Join(canonicalBackendTemp(t), "production source")
	if err := os.MkdirAll(filepath.Join(repository, "services", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	runBackendGit(t, repository, "init", "-q")
	runBackendGit(t, repository, "config", "user.name", "Backend Test")
	runBackendGit(t, repository, "config", "user.email", "backend@example.invalid")
	writeBackendFile(t, filepath.Join(repository, "README.md"), "production\n", 0o644)
	writeBackendFile(t, filepath.Join(repository, "services", "api", "model.txt"), "baseline\n", 0o644)
	runBackendGit(t, repository, "add", "--", ".")
	runBackendGit(t, repository, "commit", "-qm", "baseline")
	return repository, backendGitLine(t, repository, "rev-parse", "HEAD")
}

func backendSource(t *testing.T, id, subdir string) *research.Source {
	t.Helper()
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	value := &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: backendID(t, id, research.KindSource), Title: id, CreatedAt: now, UpdatedAt: now},
		Key:    "source-" + valueSuffix(id), Kind: research.SourceGit, Subdir: subdir,
		LocatorHints: []string{}, State: research.SourceActive,
	}
	if err := research.Validate(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func valueSuffix(value string) string {
	if len(value) < 4 {
		return value
	}
	return value[len(value)-4:]
}

func backendID(t *testing.T, value string, kind research.Kind) research.ID {
	t.Helper()
	id, err := research.ParseIDForKind(value, kind)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func backendProjectID(t *testing.T, value string) research.UUID {
	t.Helper()
	id, err := research.ParseUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func canonicalBackendTemp(t *testing.T) string {
	t.Helper()
	value, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeBackendFile(t *testing.T, name, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		t.Fatal(err)
	}
}

func readBackendFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runBackendGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", arguments, directory, err, output)
	}
	return string(output)
}

func backendGitLine(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	return strings.TrimSuffix(runBackendGit(t, directory, arguments...), "\n")
}
