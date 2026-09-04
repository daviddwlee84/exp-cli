package sourcesnapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

type captureIdentity struct {
	repository   gitx.Repository
	source       *research.Source
	objectFormat research.GitObjectFormat
	base         string
	head         string
	capturedAt   time.Time
	limits       Limits
	rootInfo     fs.FileInfo
	commonInfo   fs.FileInfo
	indexFlags   string
}

type statusEntry struct {
	path string
	xy   string
	sub  string
}

type parsedStatus struct {
	tracked   []statusEntry
	untracked []string
}

type capturedFile struct {
	Path   string
	Mode   fs.FileMode
	Size   int64
	Digest string
	Data   []byte
	info   fs.FileInfo
}

type submoduleState struct {
	Path   string
	Commit string
}

func (capturer Capturer) captureClean(ctx context.Context, request Request) (research.SourceSnapshot, error) {
	identity, err := capturer.validateIdentity(ctx, request)
	if err != nil {
		return research.SourceSnapshot{}, err
	}
	status, err := capturer.git(ctx, identity.repository.Root, statusArguments()...)
	if err != nil {
		return research.SourceSnapshot{}, fmt.Errorf("inspect Source status: %w", err)
	}
	if int64(len(status)) > identity.limits.MaxStatusBytes {
		return research.SourceSnapshot{}, fmt.Errorf("Git status is %d bytes: %w", len(status), ErrLimit)
	}
	if status != "" {
		if _, parseErr := parsePorcelainV2(status); parseErr != nil {
			return research.SourceSnapshot{}, parseErr
		}
		return research.SourceSnapshot{}, ErrDirtySource
	}
	paths, err := capturer.committedPaths(ctx, identity)
	if err != nil {
		return research.SourceSnapshot{}, err
	}
	if len(paths) == 0 && !request.AllowNoChanges {
		return research.SourceSnapshot{}, ErrNoChanges
	}
	if err := capturer.recheckClean(ctx, identity, status, paths); err != nil {
		return research.SourceSnapshot{}, err
	}
	snapshot := research.SourceSnapshot{
		Source: identity.source.ID, Subdir: identity.source.Subdir,
		PolicyVersion: PolicyVersion, CapturedAt: identity.capturedAt,
		GitObjectFormat: identity.objectFormat, BaseCommit: identity.base, HeadCommit: identity.head,
		ChangeSet: append([]string{}, paths...), State: research.SourceSnapshotClean,
		Reproducibility: research.ReproducibilityExact,
	}
	snapshot.Digest, err = research.SourceSnapshotDigest(snapshot)
	if err != nil {
		return research.SourceSnapshot{}, err
	}
	if err := Validate(snapshot); err != nil {
		return research.SourceSnapshot{}, err
	}
	return snapshot, nil
}

func (capturer Capturer) captureDirty(ctx context.Context, request Request, publish bool) (Result, error) {
	if request.Try.IsZero() || request.Try.Kind() != research.KindTry {
		return Result{}, ErrDirtyModeRequiresTry
	}
	identity, err := capturer.validateIdentity(ctx, request)
	if err != nil {
		return Result{}, err
	}

	rawStatus, err := capturer.git(ctx, identity.repository.Root, statusArguments()...)
	if err != nil {
		return Result{}, fmt.Errorf("inspect dirty Source status: %w", err)
	}
	if int64(len(rawStatus)) > identity.limits.MaxStatusBytes {
		return Result{}, fmt.Errorf("Git status is %d bytes: %w", len(rawStatus), ErrLimit)
	}
	status, err := parsePorcelainV2(rawStatus)
	if err != nil {
		return Result{}, err
	}
	if err := requireDirtyPathsUnderSubdir(identity.source.Subdir, status); err != nil {
		return Result{}, err
	}

	committed, err := capturer.committedPaths(ctx, identity)
	if err != nil {
		return Result{}, err
	}
	patch, trackedPaths, err := capturer.trackedPatch(ctx, identity)
	if err != nil {
		return Result{}, err
	}
	if err := compareTrackedStatus(status.tracked, trackedPaths); err != nil {
		return Result{}, err
	}
	submodules, err := capturer.captureSubmodules(ctx, identity)
	if err != nil {
		return Result{}, err
	}

	root, err := capturer.fileSystem().OpenCanonicalRootNoSymlinks(identity.repository.Root)
	if err != nil {
		return Result{}, fmt.Errorf("open registered Source root: %w", err)
	}
	defer root.Close()
	if err := rejectUnsupportedNodes(root, identity.source.Subdir, submodules, identity.limits.MaxFilesystemEntries); err != nil {
		return Result{}, err
	}
	if len(status.tracked) == 0 && len(status.untracked) == 0 {
		return Result{}, ErrNoChanges
	}
	untracked, err := capturer.captureUntracked(ctx, root, identity, status.untracked, true)
	if err != nil {
		return Result{}, err
	}
	seedPaths, err := capturer.captureSeedPaths(ctx, root, status.tracked, untracked)
	if err != nil {
		return Result{}, err
	}
	if err := rejectPathOverlap(trackedPaths, status.untracked); err != nil {
		return Result{}, err
	}
	allPaths, err := unionPaths(identity.limits.MaxChangePaths, committed, trackedPaths, status.untracked)
	if err != nil {
		return Result{}, err
	}

	if err := capturer.recheckDirty(ctx, root, identity, rawStatus, committed, patch, trackedPaths, submodules, untracked); err != nil {
		return Result{}, err
	}
	dirtyDigest := computeDirtyDigest(identity, patch, untracked, submodules)
	totalBytes := int64(0)
	for _, file := range untracked {
		totalBytes += file.Size
	}
	summary := fmt.Sprintf("tracked_paths=%d;untracked_files=%d;untracked_bytes=%d;submodules=%d;policy=%s", len(trackedPaths), len(untracked), totalBytes, len(submodules), PolicyVersion)
	snapshot := research.SourceSnapshot{
		Source: identity.source.ID, Subdir: identity.source.Subdir,
		PolicyVersion: PolicyVersion, CapturedAt: identity.capturedAt,
		GitObjectFormat: identity.objectFormat, BaseCommit: identity.base, HeadCommit: identity.head,
		ChangeSet: allPaths, State: research.SourceSnapshotDirty,
		DirtyDigest: dirtyDigest, DirtySummary: summary,
		Reproducibility: research.ReproducibilityBounded,
	}
	snapshot.Digest, err = research.SourceSnapshotDigest(snapshot)
	if err != nil {
		return Result{}, err
	}
	if err := Validate(snapshot); err != nil {
		return Result{}, err
	}
	result := Result{Snapshot: snapshot}
	if publish {
		bundle, publishErr := capturer.bundleStore().publish(ctx, bundlePayload{
			Snapshot: snapshot, Patch: patch, Untracked: untracked, SeedPaths: seedPaths, Submodules: submodules,
		})
		if publishErr != nil {
			return result, publishErr
		}
		result.Bundle = &bundle
	}
	return result, nil
}

func (capturer Capturer) validateIdentity(ctx context.Context, request Request) (captureIdentity, error) {
	if ctx == nil {
		return captureIdentity{}, fmt.Errorf("context is required: %w", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return captureIdentity{}, err
	}
	if request.Source == nil {
		return captureIdentity{}, fmt.Errorf("canonical Source is required: %w", ErrInvalidRequest)
	}
	source := research.Clone(request.Source).(*research.Source)
	if err := research.Validate(source); err != nil {
		return captureIdentity{}, fmt.Errorf("validate canonical Source: %w", errors.Join(ErrInvalidRequest, err))
	}
	if source.Kind != research.SourceGit || source.State != research.SourceActive && !(request.AllowRetiredSource && source.State == research.SourceRetired) {
		return captureIdentity{}, fmt.Errorf("Source must be an active Git binding, except during explicit historical cleanup verification: %w", ErrInvalidRequest)
	}
	if err := validateAbsoluteCanonicalInput(request.RepositoryRoot); err != nil {
		return captureIdentity{}, fmt.Errorf("registered Source root: %w", err)
	}
	if err := validateAbsoluteCanonicalInput(request.RegisteredGitCommonDir); err != nil {
		return captureIdentity{}, fmt.Errorf("registered Git common directory: %w", err)
	}
	limits, err := normalizeLimits(request.Limits)
	if err != nil {
		return captureIdentity{}, err
	}
	filesystem := capturer.fileSystem()
	canonicalRoot, err := filesystem.Canonical(request.RepositoryRoot)
	if err != nil || canonicalRoot != request.RepositoryRoot {
		return captureIdentity{}, fmt.Errorf("registered Source root is not canonical: %w", errors.Join(ErrSourceMismatch, err))
	}
	canonicalCommon, err := filesystem.Canonical(request.RegisteredGitCommonDir)
	if err != nil || canonicalCommon != request.RegisteredGitCommonDir {
		return captureIdentity{}, fmt.Errorf("registered Git common directory is not canonical: %w", errors.Join(ErrSourceMismatch, err))
	}
	repository, err := gitx.DiscoverWithRunner(ctx, canonicalRoot, capturer.runner())
	if err != nil {
		return captureIdentity{}, fmt.Errorf("discover registered Source clone: %w", err)
	}
	if repository.Root != canonicalRoot || repository.GitCommonDir != canonicalCommon {
		return captureIdentity{}, ErrSourceMismatch
	}
	rootInfo, rootErr := os.Lstat(repository.Root)
	commonInfo, commonErr := os.Lstat(repository.GitCommonDir)
	if rootErr != nil || commonErr != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() || commonInfo.Mode()&os.ModeSymlink != 0 || !commonInfo.IsDir() {
		return captureIdentity{}, fmt.Errorf("registered Source root or Git common directory is unsafe: %w", errors.Join(ErrSourceMismatch, rootErr, commonErr))
	}
	if request.RegisteredGitCommonIdentity != "" {
		observed, identityErr := pathx.DirectoryFilesystemIdentity(repository.GitCommonDir)
		if identityErr != nil || observed != request.RegisteredGitCommonIdentity {
			return captureIdentity{}, fmt.Errorf("registered Git common filesystem identity differs: %w", errors.Join(ErrSourceMismatch, identityErr))
		}
	}
	resolvedSubdir, err := filesystem.ResolveUnderNoSymlinks(repository.Root, source.Subdir, true)
	if err != nil {
		return captureIdentity{}, fmt.Errorf("resolve canonical Source subdir: %w", errors.Join(ErrSourceMismatch, err))
	}
	info, err := os.Lstat(resolvedSubdir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return captureIdentity{}, fmt.Errorf("canonical Source subdir is not a real directory: %w", errors.Join(ErrSourceMismatch, err))
	}
	indexFlags, err := capturer.readOrdinaryIndexFlags(ctx, repository.Root, limits.MaxStatusBytes)
	if err != nil {
		return captureIdentity{}, err
	}

	formatOutput, err := capturer.git(ctx, repository.Root, "rev-parse", "--show-object-format")
	if err != nil {
		return captureIdentity{}, fmt.Errorf("read Git object format: %w", err)
	}
	formatText, err := oneLine(formatOutput)
	if err != nil {
		return captureIdentity{}, fmt.Errorf("read Git object format: %w", errors.Join(ErrSourceMismatch, err))
	}
	format := research.GitObjectFormat(formatText)
	objectLength := objectIDLength(format)
	if objectLength == 0 {
		return captureIdentity{}, fmt.Errorf("unsupported Git object format %q: %w", formatText, ErrSourceMismatch)
	}
	if !validObjectID(request.BaseCommit, objectLength) {
		return captureIdentity{}, fmt.Errorf("base commit must be a full lower-case %s object ID: %w", format, ErrInvalidRequest)
	}
	base, err := capturer.resolveExactCommit(ctx, repository.Root, request.BaseCommit, objectLength)
	if err != nil {
		return captureIdentity{}, fmt.Errorf("resolve exact base commit: %w", err)
	}
	headOutput, err := capturer.git(ctx, repository.Root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return captureIdentity{}, fmt.Errorf("resolve Source HEAD: %w", err)
	}
	head, err := oneLine(headOutput)
	if err != nil || !validObjectID(head, objectLength) {
		return captureIdentity{}, fmt.Errorf("Source HEAD is not a full %s object ID: %w", format, errors.Join(ErrSourceMismatch, err))
	}
	if request.ExpectedHead != "" {
		if !validObjectID(request.ExpectedHead, objectLength) {
			return captureIdentity{}, fmt.Errorf("expected HEAD must be a full lower-case %s object ID: %w", format, ErrInvalidRequest)
		}
		expected, resolveErr := capturer.resolveExactCommit(ctx, repository.Root, request.ExpectedHead, objectLength)
		if resolveErr != nil || expected != head {
			return captureIdentity{}, fmt.Errorf("Source HEAD differs from the exact expected object: %w", errors.Join(ErrSourceMismatch, resolveErr))
		}
	}
	if _, err := capturer.git(ctx, repository.Root, "merge-base", "--is-ancestor", base, head); err != nil {
		return captureIdentity{}, fmt.Errorf("base commit is not an ancestor of Source HEAD: %w", errors.Join(ErrSourceMismatch, err))
	}
	capturedAt := request.CapturedAt
	if capturedAt.IsZero() {
		clock := capturer.Clock
		if clock == nil {
			clock = time.Now
		}
		capturedAt = clock()
	}
	if capturedAt.IsZero() {
		return captureIdentity{}, fmt.Errorf("capture clock returned zero time: %w", ErrInvalidRequest)
	}
	capturedAt = capturedAt.UTC()
	return captureIdentity{
		repository: repository, source: source, objectFormat: format,
		base: base, head: head, capturedAt: capturedAt, limits: limits,
		rootInfo: rootInfo, commonInfo: commonInfo, indexFlags: indexFlags,
	}, nil
}

func (capturer Capturer) resolveExactCommit(ctx context.Context, root, requested string, objectLength int) (string, error) {
	output, err := capturer.git(ctx, root, "rev-parse", "--verify", requested+"^{commit}")
	if err != nil {
		return "", err
	}
	resolved, err := oneLine(output)
	if err != nil || resolved != requested || !validObjectID(resolved, objectLength) {
		return "", errors.Join(ErrSourceMismatch, err)
	}
	return resolved, nil
}

func (capturer Capturer) committedPaths(ctx context.Context, identity captureIdentity) ([]string, error) {
	output, err := capturer.git(ctx, identity.repository.Root, "diff", "--name-only", "-z", "--no-renames", identity.base, identity.head, "--")
	if err != nil {
		return nil, fmt.Errorf("list committed Source paths: %w", err)
	}
	paths, err := parseNULPaths(output)
	if err != nil {
		return nil, fmt.Errorf("parse committed Source paths: %w", err)
	}
	if len(paths) > identity.limits.MaxChangePaths {
		return nil, fmt.Errorf("committed change set has %d paths: %w", len(paths), ErrLimit)
	}
	return paths, nil
}

func (capturer Capturer) readOrdinaryIndexFlags(ctx context.Context, root string, maxBytes int64) (string, error) {
	output, err := capturer.git(ctx, root, "ls-files", "-v", "-z", "--")
	if err != nil {
		return "", fmt.Errorf("inspect Git index path flags: %w", err)
	}
	if int64(len(output)) > maxBytes {
		return "", fmt.Errorf("Git index flag listing is %d bytes: %w", len(output), ErrLimit)
	}
	if output == "" {
		return output, nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return "", ErrStatusMalformed
	}
	for _, record := range strings.Split(output[:len(output)-1], "\x00") {
		if len(record) < 3 || record[1] != ' ' || record[0] != 'H' {
			return "", fmt.Errorf("index path is assume-unchanged, skip-worktree, unmerged, or unsupported: %w", ErrUnsupportedIndex)
		}
		if err := validateGitPath(record[2:]); err != nil {
			return "", err
		}
	}
	return output, nil
}

func (capturer Capturer) trackedPatch(ctx context.Context, identity captureIdentity) ([]byte, []string, error) {
	patchOutput, err := capturer.git(ctx, identity.repository.Root, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--no-renames", "HEAD", "--")
	if err != nil {
		return nil, nil, fmt.Errorf("capture full-index tracked patch: %w", err)
	}
	patch := []byte(patchOutput)
	if int64(len(patch)) > identity.limits.MaxPatchBytes {
		return nil, nil, fmt.Errorf("tracked patch is %d bytes; limit is %d: %w", len(patch), identity.limits.MaxPatchBytes, ErrLimit)
	}
	pathsOutput, err := capturer.git(ctx, identity.repository.Root, "diff", "--name-only", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		return nil, nil, fmt.Errorf("list dirty tracked paths: %w", err)
	}
	paths, err := parseNULPaths(pathsOutput)
	if err != nil {
		return nil, nil, fmt.Errorf("parse dirty tracked paths: %w", err)
	}
	if len(paths) > identity.limits.MaxChangePaths {
		return nil, nil, fmt.Errorf("tracked change set has %d paths: %w", len(paths), ErrLimit)
	}
	if len(paths) == 0 && len(patch) != 0 || len(paths) != 0 && len(patch) == 0 {
		return nil, nil, fmt.Errorf("tracked patch and path set disagree: %w", ErrSourceChanged)
	}
	return append([]byte(nil), patch...), paths, nil
}

func rejectUnsupportedNodes(root *os.Root, subdir string, submodules []submoduleState, limit int) error {
	if subdir != "." {
		info, err := root.Lstat(subdir)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("Source subdir changed identity or became unsafe: %w", errors.Join(ErrSourceChanged, err))
		}
	}
	skipped := make(map[string]struct{}, len(submodules))
	for _, submodule := range submodules {
		skipped[submodule.Path] = struct{}{}
	}
	entries := 0
	err := fs.WalkDir(root.FS(), subdir, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > limit {
			return fmt.Errorf("Source tree has more than %d entries: %w", limit, ErrLimit)
		}
		if name == ".git" && subdir == "." {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if _, isSubmodule := skipped[name]; isSubmodule {
			if !entry.IsDir() {
				return fmt.Errorf("submodule path %q is not a directory: %w", name, ErrSubmodule)
			}
			return fs.SkipDir
		}
		if name != subdir && path.Base(name) == ".git" {
			return fmt.Errorf("nested Git metadata at %q: %w", name, ErrSubmodule)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsupported symlink %q: %w", name, pathx.ErrSymlink)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported filesystem node %q: %w", name, pathx.ErrNotRegular)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("validate Source filesystem node types: %w", err)
	}
	return nil
}

func (capturer Capturer) captureUntracked(ctx context.Context, root *os.Root, identity captureIdentity, paths []string, retain bool) ([]capturedFile, error) {
	if len(paths) > identity.limits.MaxUntrackedFiles {
		return nil, fmt.Errorf("untracked file count is %d; limit is %d: %w", len(paths), identity.limits.MaxUntrackedFiles, ErrLimit)
	}
	files := make([]capturedFile, 0, len(paths))
	total := int64(0)
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := capturer.fileSystem().ResolveUnderNoSymlinks(identity.repository.Root, name, false); err != nil {
			return nil, fmt.Errorf("untracked path %q is unsafe: %w", name, err)
		}
		file, info, err := capturer.fileSystem().OpenRegularFileNoFollow(root, name)
		if err != nil {
			return nil, fmt.Errorf("open untracked path %q: %w", name, err)
		}
		captured, readErr := readCapturedFile(ctx, root, file, info, name, identity.limits.MaxUntrackedFileBytes, retain)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return nil, fmt.Errorf("read untracked path %q: %w", name, errors.Join(readErr, closeErr))
		}
		if total > identity.limits.MaxUntrackedTotalBytes-captured.Size {
			return nil, fmt.Errorf("untracked bytes exceed %d: %w", identity.limits.MaxUntrackedTotalBytes, ErrLimit)
		}
		total += captured.Size
		files = append(files, captured)
	}
	return files, nil
}

func (capturer Capturer) captureSeedPaths(ctx context.Context, root *os.Root, tracked []statusEntry, untracked []capturedFile) ([]SeedPath, error) {
	result := make([]SeedPath, 0, len(tracked)+len(untracked))
	total := int64(0)
	for _, entry := range tracked {
		info, err := root.Lstat(entry.path)
		if errors.Is(err, fs.ErrNotExist) {
			result = append(result, SeedPath{Path: entry.path})
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("inspect dirty tracked path %q: %w", entry.path, errors.Join(pathx.ErrNotRegular, err))
		}
		file, opened, err := capturer.fileSystem().OpenRegularFileNoFollow(root, entry.path)
		if err != nil {
			return nil, fmt.Errorf("open dirty tracked path %q: %w", entry.path, err)
		}
		captured, readErr := readCapturedFile(ctx, root, file, opened, entry.path, absoluteMaxBundlePayloadBytes, false)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return nil, fmt.Errorf("hash dirty tracked path %q: %w", entry.path, errors.Join(readErr, closeErr))
		}
		if total > absoluteMaxBundlePayloadBytes-captured.Size {
			return nil, fmt.Errorf("dirty seed bytes exceed %d: %w", absoluteMaxBundlePayloadBytes, ErrLimit)
		}
		total += captured.Size
		result = append(result, SeedPath{Path: captured.Path, Exists: true, Mode: uint32(captured.Mode.Perm()), Size: captured.Size, SHA256: captured.Digest})
	}
	for _, captured := range untracked {
		if total > absoluteMaxBundlePayloadBytes-captured.Size {
			return nil, fmt.Errorf("dirty seed bytes exceed %d: %w", absoluteMaxBundlePayloadBytes, ErrLimit)
		}
		total += captured.Size
		result = append(result, SeedPath{Path: captured.Path, Exists: true, Mode: uint32(captured.Mode.Perm()), Size: captured.Size, SHA256: captured.Digest})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Path < result[right].Path })
	return result, nil
}

func readCapturedFile(ctx context.Context, root *os.Root, file *os.File, before fs.FileInfo, name string, maxBytes int64, retain bool) (capturedFile, error) {
	if before.Size() < 0 || before.Size() > maxBytes {
		return capturedFile{}, fmt.Errorf("file is %d bytes; limit is %d: %w", before.Size(), maxBytes, ErrLimit)
	}
	hash := sha256.New()
	var content bytes.Buffer
	writer := io.Writer(hash)
	if retain {
		content.Grow(int(before.Size()))
		writer = io.MultiWriter(hash, &content)
	}
	written, err := io.Copy(writer, io.LimitReader(&contextReader{ctx: ctx, reader: file}, maxBytes+1))
	if err != nil {
		return capturedFile{}, err
	}
	if written > maxBytes || written != before.Size() {
		return capturedFile{}, fmt.Errorf("file size changed while reading: %w", ErrSourceChanged)
	}
	afterOpen, statErr := file.Stat()
	afterPath, pathErr := root.Lstat(name)
	if statErr != nil || pathErr != nil || afterPath.Mode()&os.ModeSymlink != 0 || !afterPath.Mode().IsRegular() ||
		!os.SameFile(before, afterOpen) || !os.SameFile(before, afterPath) || afterOpen.Size() != before.Size() ||
		afterOpen.Mode().Perm() != before.Mode().Perm() || afterPath.Mode().Perm() != before.Mode().Perm() ||
		!afterOpen.ModTime().Equal(before.ModTime()) || afterPath.Size() != before.Size() || !afterPath.ModTime().Equal(before.ModTime()) {
		return capturedFile{}, fmt.Errorf("file identity changed while reading: %w", errors.Join(ErrSourceChanged, statErr, pathErr))
	}
	return capturedFile{
		Path: name, Mode: before.Mode().Perm(), Size: written,
		Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Data: append([]byte(nil), content.Bytes()...), info: before,
	}, nil
}

func (capturer Capturer) captureSubmodules(ctx context.Context, identity captureIdentity) ([]submoduleState, error) {
	output, err := capturer.git(ctx, identity.repository.Root, "ls-files", "--stage", "-z", "--")
	if err != nil {
		return nil, fmt.Errorf("inspect Source index for submodules: %w", err)
	}
	if int64(len(output)) > identity.limits.MaxStatusBytes {
		return nil, fmt.Errorf("Git index listing is %d bytes: %w", len(output), ErrLimit)
	}
	entries, err := parseIndexGitlinks(output, objectIDLength(identity.objectFormat))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		resolved, resolveErr := capturer.fileSystem().ResolveUnderNoSymlinks(identity.repository.Root, entry.Path, false)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve submodule %q: %w", entry.Path, errors.Join(ErrSubmodule, resolveErr))
		}
		info, statErr := os.Lstat(resolved)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("submodule %q is not initialized as a real directory: %w", entry.Path, errors.Join(ErrSubmodule, statErr))
		}
		repository, discoverErr := gitx.DiscoverWithRunner(ctx, resolved, capturer.runner())
		if discoverErr != nil || repository.Root != resolved {
			return nil, fmt.Errorf("discover submodule %q: %w", entry.Path, errors.Join(ErrSubmodule, discoverErr))
		}
		headOutput, headErr := capturer.git(ctx, resolved, "rev-parse", "--verify", "HEAD^{commit}")
		head, lineErr := oneLine(headOutput)
		if headErr != nil || lineErr != nil || head != entry.Commit {
			return nil, fmt.Errorf("submodule %q HEAD differs from the recorded gitlink: %w", entry.Path, errors.Join(ErrSubmodule, headErr, lineErr))
		}
		status, statusErr := capturer.git(ctx, resolved, statusArguments()...)
		if statusErr != nil || status != "" {
			return nil, fmt.Errorf("submodule %q is dirty: %w", entry.Path, errors.Join(ErrSubmodule, statusErr))
		}
		nestedOutput, nestedErr := capturer.git(ctx, resolved, "ls-files", "--stage", "-z", "--")
		if nestedErr != nil {
			return nil, fmt.Errorf("inspect submodule %q index: %w", entry.Path, errors.Join(ErrSubmodule, nestedErr))
		}
		nested, parseErr := parseIndexGitlinks(nestedOutput, len(entry.Commit))
		if parseErr != nil || len(nested) != 0 {
			return nil, fmt.Errorf("submodule %q contains nested submodules: %w", entry.Path, errors.Join(ErrSubmodule, parseErr))
		}
	}
	return entries, nil
}

func (capturer Capturer) recheckClean(ctx context.Context, identity captureIdentity, expectedStatus string, expectedPaths []string) error {
	if err := capturer.recheckHead(ctx, identity); err != nil {
		return err
	}
	status, err := capturer.git(ctx, identity.repository.Root, statusArguments()...)
	if err != nil || status != expectedStatus {
		return fmt.Errorf("Source status changed during capture: %w", errors.Join(ErrSourceChanged, err))
	}
	paths, err := capturer.committedPaths(ctx, identity)
	if err != nil || !equalStrings(paths, expectedPaths) {
		return fmt.Errorf("committed Source paths changed during capture: %w", errors.Join(ErrSourceChanged, err))
	}
	return nil
}

func (capturer Capturer) recheckDirty(ctx context.Context, root *os.Root, identity captureIdentity, expectedStatus string, expectedCommitted []string, expectedPatch []byte, expectedTracked []string, expectedSubmodules []submoduleState, expectedUntracked []capturedFile) error {
	if err := capturer.recheckHead(ctx, identity); err != nil {
		return err
	}
	status, err := capturer.git(ctx, identity.repository.Root, statusArguments()...)
	if err != nil || status != expectedStatus {
		return fmt.Errorf("Source status changed during dirty capture: %w", errors.Join(ErrSourceChanged, err))
	}
	committed, err := capturer.committedPaths(ctx, identity)
	if err != nil || !equalStrings(committed, expectedCommitted) {
		return fmt.Errorf("committed Source paths changed during dirty capture: %w", errors.Join(ErrSourceChanged, err))
	}
	patch, tracked, err := capturer.trackedPatch(ctx, identity)
	if err != nil || !bytes.Equal(patch, expectedPatch) || !equalStrings(tracked, expectedTracked) {
		return fmt.Errorf("tracked Source patch changed during capture: %w", errors.Join(ErrSourceChanged, err))
	}
	submodules, err := capturer.captureSubmodules(ctx, identity)
	if err != nil || !equalSubmodules(submodules, expectedSubmodules) {
		return fmt.Errorf("submodule state changed during capture: %w", errors.Join(ErrSourceChanged, err))
	}
	if err := rejectUnsupportedNodes(root, identity.source.Subdir, submodules, identity.limits.MaxFilesystemEntries); err != nil {
		return err
	}
	paths := make([]string, len(expectedUntracked))
	for index := range expectedUntracked {
		paths[index] = expectedUntracked[index].Path
	}
	untracked, err := capturer.captureUntracked(ctx, root, identity, paths, false)
	if err != nil || !equalCapturedMetadata(untracked, expectedUntracked) {
		return fmt.Errorf("untracked Source bytes changed during capture: %w", errors.Join(ErrSourceChanged, err))
	}
	return nil
}

func (capturer Capturer) recheckHead(ctx context.Context, identity captureIdentity) error {
	rootInfo, rootErr := os.Lstat(identity.repository.Root)
	commonInfo, commonErr := os.Lstat(identity.repository.GitCommonDir)
	if rootErr != nil || commonErr != nil || rootInfo.Mode()&os.ModeSymlink != 0 || commonInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity.rootInfo, rootInfo) || !os.SameFile(identity.commonInfo, commonInfo) {
		return fmt.Errorf("registered Source or Git common directory changed identity: %w", errors.Join(ErrSourceChanged, rootErr, commonErr))
	}
	indexFlags, flagErr := capturer.readOrdinaryIndexFlags(ctx, identity.repository.Root, identity.limits.MaxStatusBytes)
	if flagErr != nil || indexFlags != identity.indexFlags {
		return fmt.Errorf("Git index path flags changed during capture: %w", errors.Join(ErrSourceChanged, flagErr))
	}
	output, err := capturer.git(ctx, identity.repository.Root, "rev-parse", "--verify", "HEAD^{commit}")
	head, lineErr := oneLine(output)
	if err != nil || lineErr != nil || head != identity.head {
		return fmt.Errorf("Source HEAD changed during capture: %w", errors.Join(ErrSourceChanged, err, lineErr))
	}
	return nil
}

func computeDirtyDigest(identity captureIdentity, patch []byte, untracked []capturedFile, submodules []submoduleState) string {
	framed := newFrameHash("exp.source-dirty/v1")
	framed.addString("policy-version", PolicyVersion)
	framed.addString("source", identity.source.ID.String())
	framed.addString("subdir", identity.source.Subdir)
	framed.addString("git-object-format", string(identity.objectFormat))
	framed.addString("head-commit", identity.head)
	framed.add("tracked-patch", patch)
	framed.addUint("submodule-count", uint64(len(submodules)))
	for _, submodule := range submodules {
		framed.addString("submodule-path", submodule.Path)
		framed.addString("submodule-commit", submodule.Commit)
	}
	framed.addUint("untracked-count", uint64(len(untracked)))
	for _, file := range untracked {
		framed.addString("untracked-path", file.Path)
		framed.addUint("untracked-mode", uint64(file.Mode.Perm()))
		framed.addUint("untracked-size", uint64(file.Size))
		framed.addString("untracked-sha256", file.Digest)
	}
	return framed.sum()
}

func parsePorcelainV2(output string) (parsedStatus, error) {
	if output == "" {
		return parsedStatus{tracked: []statusEntry{}, untracked: []string{}}, nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return parsedStatus{}, ErrStatusMalformed
	}
	records := strings.Split(output[:len(output)-1], "\x00")
	result := parsedStatus{tracked: make([]statusEntry, 0), untracked: make([]string, 0)}
	seen := make(map[string]struct{}, len(records))
	for index := 0; index < len(records); index++ {
		record := records[index]
		if record == "" || !utf8.ValidString(record) {
			return parsedStatus{}, ErrStatusMalformed
		}
		switch record[0] {
		case '1':
			fields := strings.SplitN(record, " ", 9)
			if len(fields) != 9 || fields[0] != "1" || !validXY(fields[1]) || !validSubmoduleField(fields[2]) || !validIndexMode(fields[3]) || !validIndexMode(fields[4]) || !validIndexMode(fields[5]) || !validStatusObjectID(fields[6]) || !validStatusObjectID(fields[7]) {
				return parsedStatus{}, ErrStatusMalformed
			}
			if strings.ContainsAny(fields[1], "RC") {
				return parsedStatus{}, ErrRenameOrCopy
			}
			if fields[2] != "N..." {
				return parsedStatus{}, ErrSubmodule
			}
			name := fields[8]
			if err := validateGitPath(name); err != nil {
				return parsedStatus{}, err
			}
			if _, duplicate := seen[name]; duplicate {
				return parsedStatus{}, ErrStatusMalformed
			}
			seen[name] = struct{}{}
			result.tracked = append(result.tracked, statusEntry{path: name, xy: fields[1], sub: fields[2]})
		case '2':
			fields := strings.SplitN(record, " ", 10)
			if len(fields) != 10 || fields[0] != "2" || !validXY(fields[1]) || !validSubmoduleField(fields[2]) || !validIndexMode(fields[3]) || !validIndexMode(fields[4]) || !validIndexMode(fields[5]) || !validStatusObjectID(fields[6]) || !validStatusObjectID(fields[7]) || len(fields[8]) < 2 || index+1 >= len(records) {
				return parsedStatus{}, ErrStatusMalformed
			}
			if err := validateGitPath(fields[9]); err != nil {
				return parsedStatus{}, err
			}
			index++
			if err := validateGitPath(records[index]); err != nil {
				return parsedStatus{}, err
			}
			return parsedStatus{}, ErrRenameOrCopy
		case 'u':
			return parsedStatus{}, fmt.Errorf("unmerged index state: %w", ErrStatusMalformed)
		case '?':
			if !strings.HasPrefix(record, "? ") {
				return parsedStatus{}, ErrStatusMalformed
			}
			name := strings.TrimPrefix(record, "? ")
			if err := validateGitPath(name); err != nil {
				return parsedStatus{}, err
			}
			if _, duplicate := seen[name]; duplicate {
				return parsedStatus{}, ErrStatusMalformed
			}
			seen[name] = struct{}{}
			result.untracked = append(result.untracked, name)
		case '!':
			return parsedStatus{}, fmt.Errorf("ignored files cannot be implicitly captured: %w", ErrStatusMalformed)
		default:
			return parsedStatus{}, ErrStatusMalformed
		}
	}
	sort.Slice(result.tracked, func(i, j int) bool { return result.tracked[i].path < result.tracked[j].path })
	sort.Strings(result.untracked)
	return result, nil
}

func parseNULPaths(output string) ([]string, error) {
	if output == "" {
		return []string{}, nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return nil, ErrStatusMalformed
	}
	values := strings.Split(output[:len(output)-1], "\x00")
	seen := make(map[string]struct{}, len(values))
	paths := make([]string, 0, len(values))
	for _, value := range values {
		if err := validateGitPath(value); err != nil {
			return nil, err
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, ErrStatusMalformed
		}
		seen[value] = struct{}{}
		paths = append(paths, value)
	}
	sort.Strings(paths)
	return paths, nil
}

func parseIndexGitlinks(output string, objectLength int) ([]submoduleState, error) {
	if output == "" {
		return []submoduleState{}, nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return nil, ErrStatusMalformed
	}
	result := make([]submoduleState, 0)
	for _, record := range strings.Split(output[:len(output)-1], "\x00") {
		metadata, name, found := strings.Cut(record, "\t")
		fields := strings.Fields(metadata)
		if !found || len(fields) != 3 || !validIndexMode(fields[0]) || !validObjectID(fields[1], objectLength) || fields[2] != "0" {
			return nil, fmt.Errorf("malformed Git index entry: %w", ErrStatusMalformed)
		}
		if err := validateGitPath(name); err != nil {
			return nil, err
		}
		if fields[0] == "160000" {
			result = append(result, submoduleState{Path: name, Commit: fields[1]})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	for index := 1; index < len(result); index++ {
		if result[index-1].Path == result[index].Path {
			return nil, ErrStatusMalformed
		}
	}
	return result, nil
}

func validateGitPath(value string) error {
	if err := pathx.ValidateRelativePOSIX(value, false); err != nil {
		return fmt.Errorf("unsafe Git path: %w", err)
	}
	if err := research.ValidateCommittedPath(value, false); err != nil {
		return fmt.Errorf("unsafe Git path: %w", err)
	}
	if err := research.ValidateCommitSafeText(value); err != nil {
		return fmt.Errorf("credential-bearing Git path: %w", err)
	}
	return nil
}

func requireDirtyPathsUnderSubdir(subdir string, status parsedStatus) error {
	if subdir == "." {
		return nil
	}
	prefix := subdir + "/"
	for _, entry := range status.tracked {
		if !strings.HasPrefix(entry.path, prefix) {
			return fmt.Errorf("tracked path %q: %w", entry.path, ErrOutsideSourceSubdir)
		}
	}
	for _, name := range status.untracked {
		if !strings.HasPrefix(name, prefix) {
			return fmt.Errorf("untracked path %q: %w", name, ErrOutsideSourceSubdir)
		}
	}
	return nil
}

func compareTrackedStatus(entries []statusEntry, paths []string) error {
	statusPaths := make([]string, len(entries))
	for index := range entries {
		statusPaths[index] = entries[index].path
	}
	if !equalStrings(statusPaths, paths) {
		return fmt.Errorf("porcelain status and full-index patch path set disagree: %w", ErrSourceChanged)
	}
	return nil
}

func rejectPathOverlap(left, right []string) error {
	set := make(map[string]struct{}, len(left))
	for _, value := range left {
		set[value] = struct{}{}
	}
	for _, value := range right {
		if _, found := set[value]; found {
			return fmt.Errorf("path %q is both tracked and untracked: %w", value, ErrStatusMalformed)
		}
	}
	return nil
}

func unionPaths(limit int, sets ...[]string) ([]string, error) {
	set := make(map[string]struct{})
	for _, values := range sets {
		for _, value := range values {
			set[value] = struct{}{}
		}
	}
	if len(set) > limit {
		return nil, fmt.Errorf("combined change set has %d paths; limit is %d: %w", len(set), limit, ErrLimit)
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeLimits(input Limits) (Limits, error) {
	defaults := DefaultLimits()
	if input.MaxStatusBytes == 0 {
		input.MaxStatusBytes = defaults.MaxStatusBytes
	}
	if input.MaxPatchBytes == 0 {
		input.MaxPatchBytes = defaults.MaxPatchBytes
	}
	if input.MaxChangePaths == 0 {
		input.MaxChangePaths = defaults.MaxChangePaths
	}
	if input.MaxFilesystemEntries == 0 {
		input.MaxFilesystemEntries = defaults.MaxFilesystemEntries
	}
	if input.MaxUntrackedFiles == 0 {
		input.MaxUntrackedFiles = defaults.MaxUntrackedFiles
	}
	if input.MaxUntrackedFileBytes == 0 {
		input.MaxUntrackedFileBytes = defaults.MaxUntrackedFileBytes
	}
	if input.MaxUntrackedTotalBytes == 0 {
		input.MaxUntrackedTotalBytes = defaults.MaxUntrackedTotalBytes
	}
	if input.MaxStatusBytes < 1 || input.MaxStatusBytes > gitx.MaxGitOutputBytes || input.MaxPatchBytes < 1 || input.MaxPatchBytes > gitx.MaxGitOutputBytes ||
		input.MaxChangePaths < 1 || input.MaxFilesystemEntries < 1 || input.MaxFilesystemEntries > 1000000 || input.MaxUntrackedFiles < 1 || input.MaxUntrackedFileBytes < 1 || input.MaxUntrackedTotalBytes < 1 ||
		input.MaxUntrackedFileBytes > absoluteMaxBundlePayloadBytes || input.MaxUntrackedTotalBytes > absoluteMaxBundlePayloadBytes {
		return Limits{}, fmt.Errorf("capture limits are non-positive or exceed hard bounds: %w", ErrInvalidRequest)
	}
	return input, nil
}

func validateAbsoluteCanonicalInput(value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return ErrInvalidRequest
	}
	return nil
}

func validObjectID(value string, length int) bool {
	if len(value) != length || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func objectIDLength(format research.GitObjectFormat) int {
	switch format {
	case research.GitObjectSHA1:
		return 40
	case research.GitObjectSHA256:
		return 64
	default:
		return 0
	}
}

func validXY(value string) bool {
	if len(value) != 2 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune(".MADRCUT", character) {
			return false
		}
	}
	return value != ".."
}

func validSubmoduleField(value string) bool {
	if len(value) != 4 || value[0] != 'N' && value[0] != 'S' {
		return false
	}
	for _, character := range value[1:] {
		if character != '.' && character != 'C' && character != 'M' && character != 'U' {
			return false
		}
	}
	return true
}

func validStatusObjectID(value string) bool {
	return validObjectID(value, 40) || validObjectID(value, 64)
}

func validIndexMode(value string) bool {
	if len(value) != 6 {
		return false
	}
	_, err := strconv.ParseUint(value, 8, 32)
	return err == nil
}

func oneLine(output string) (string, error) {
	value := strings.TrimSuffix(output, "\n")
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", ErrStatusMalformed
	}
	return value, nil
}

func statusArguments() []string {
	return []string{"status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignored=no", "--ignore-submodules=none", "--renames"}
}

func (capturer Capturer) runner() gitx.Runner {
	if capturer.Git != nil {
		return capturer.Git
	}
	return gitx.ExecRunner{}
}

func (capturer Capturer) fileSystem() FileSystem {
	if capturer.FileSystem != nil {
		return capturer.FileSystem
	}
	return OSFileSystem{}
}

func (capturer Capturer) bundleStore() *BundleStore {
	if capturer.Bundles != nil {
		if capturer.Bundles.FileSystem == nil {
			copy := *capturer.Bundles
			copy.FileSystem = capturer.fileSystem()
			return &copy
		}
		return capturer.Bundles
	}
	return &BundleStore{FileSystem: capturer.fileSystem()}
}

func (capturer Capturer) git(ctx context.Context, directory string, arguments ...string) (string, error) {
	copy := []string{"--no-pager", "--no-replace-objects", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}
	copy = append(copy, arguments...)
	stdout, stderr, err := capturer.runner().Run(ctx, directory, copy)
	if err != nil {
		return "", &gitx.Error{Dir: directory, Args: copy, Stderr: stderr, Err: err}
	}
	return stdout, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalSubmodules(left, right []submoduleState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalCapturedMetadata(left, right []capturedFile) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Path != right[index].Path || left[index].Mode.Perm() != right[index].Mode.Perm() || left[index].Size != right[index].Size || left[index].Digest != right[index].Digest || !os.SameFile(left[index].info, right[index].info) {
			return false
		}
	}
	return true
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := reader.reader.Read(buffer)
	if err == nil {
		err = reader.ctx.Err()
	}
	return count, err
}
