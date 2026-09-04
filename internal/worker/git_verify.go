package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
)

// verifyWorkloadGit closes the scheduling TOCTOU window by repeating the
// canonical base/head/change-set proof immediately before process start.
func verifyWorkloadGit(ctx context.Context, runner gitx.Runner, snapshots SnapshotVerifier, workload Workload) error {
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	if len(workload.Sources) > 0 {
		return verifyFormalSourceWorkload(ctx, runner, snapshots, workload)
	}
	if workload.SourceSnapshot != nil {
		return verifySnapshotWorkload(ctx, runner, snapshots, workload)
	}
	repository, err := gitx.DiscoverWithRunner(ctx, workload.CWD, runner)
	if err != nil {
		return err
	}
	expectedRoot, err := pathx.Canonical(workload.RepositoryRoot)
	if err != nil {
		return err
	}
	if repository.Root != expectedRoot {
		return errors.New("worker cwd moved to a different Git checkout")
	}
	head, err := workerGit(ctx, runner, repository.Root, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(head) != workload.HeadCommit {
		return fmt.Errorf("HEAD does not equal head_commit %s", workload.HeadCommit)
	}
	base, err := workerGit(ctx, runner, repository.Root, "rev-parse", "--verify", workload.BaseCommit+"^{commit}")
	if err != nil || strings.TrimSpace(base) != workload.BaseCommit {
		return fmt.Errorf("base_commit %s cannot be resolved exactly", workload.BaseCommit)
	}
	if _, err := workerGit(ctx, runner, repository.Root, "merge-base", "--is-ancestor", workload.BaseCommit, workload.HeadCommit); err != nil {
		return errors.New("base_commit is not an ancestor of head_commit")
	}
	dirty, err := workerGit(ctx, runner, repository.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if err := validateWorkerDirtyPaths(dirty, workload.RuntimeConfigPath); err != nil {
		return err
	}
	diff, err := workerGit(ctx, runner, repository.Root, "diff", "--name-only", "-z", "--no-renames", workload.BaseCommit, workload.HeadCommit, "--")
	if err != nil {
		return err
	}
	paths, err := parseWorkerPaths(diff)
	if err != nil {
		return err
	}
	if !sameWorkerPaths(paths, workload.ChangeSet) {
		return errors.New("committed base..head paths differ from change_set")
	}
	return nil
}

func verifyFormalSourceWorkload(ctx context.Context, runner gitx.Runner, verifier SnapshotVerifier, workload Workload) error {
	if verifier == nil {
		verifier = sourcesnapshot.Capturer{Git: runner}
	}
	for index := range workload.Sources {
		binding := workload.Sources[index]
		snapshot, err := binding.Snapshot.snapshot()
		if err != nil {
			return err
		}
		if snapshot.State != research.SourceSnapshotClean {
			return errors.New("formal worker Source snapshot is not clean")
		}
		source := &research.Source{
			Common: research.Common{
				Schema: research.SchemaSource, ID: snapshot.Source, Title: "Formal worker Source",
				CreatedAt: snapshot.CapturedAt, UpdatedAt: snapshot.CapturedAt,
			},
			Key: "formal-worker-source", Kind: research.SourceGit, Subdir: snapshot.Subdir,
			LocatorHints: []string{}, State: research.SourceActive,
		}
		observed, err := verifier.CaptureClean(ctx, sourcesnapshot.Request{
			Source: source, RepositoryRoot: binding.RepositoryRoot,
			RegisteredGitCommonDir:      binding.RegisteredGitCommonDir,
			RegisteredGitCommonIdentity: binding.RegisteredGitCommonIdentity,
			BaseCommit:                  snapshot.BaseCommit, ExpectedHead: snapshot.HeadCommit,
			AllowNoChanges: len(snapshot.ChangeSet) == 0, CapturedAt: snapshot.CapturedAt,
		})
		if err != nil {
			return fmt.Errorf("verify formal Source %s: %w", snapshot.Source, err)
		}
		if observed.Digest != snapshot.Digest {
			return sourcesnapshot.ErrSourceChanged
		}
		semantic, err := pathx.ResolveUnderNoSymlinks(binding.RepositoryRoot, snapshot.Subdir, true)
		if err != nil || semantic != binding.SemanticRoot {
			return fmt.Errorf("formal Source %s semantic root changed: %w", snapshot.Source, errors.Join(pathx.ErrOutsideRoot, err))
		}
	}
	return nil
}

func verifyReadOnlySourceWorkload(ctx context.Context, runner gitx.Runner, verifier SnapshotVerifier, workload Workload) error {
	readOnly := make([]SourceBinding, 0, len(workload.Sources))
	for _, binding := range workload.Sources {
		if binding.ReadOnly {
			readOnly = append(readOnly, binding)
		}
	}
	if len(readOnly) == 0 {
		return nil
	}
	copy := workload
	copy.Sources = readOnly
	return verifyFormalSourceWorkload(ctx, runner, verifier, copy)
}

func verifySnapshotWorkload(ctx context.Context, runner gitx.Runner, verifier SnapshotVerifier, workload Workload) error {
	snapshot, err := workload.SourceSnapshot.snapshot()
	if err != nil {
		return err
	}
	if verifier == nil {
		verifier = sourcesnapshot.Capturer{Git: runner}
	}
	source := &research.Source{
		Common: research.Common{
			Schema: research.SchemaSource, ID: snapshot.Source, Title: "Worker Source",
			CreatedAt: snapshot.CapturedAt, UpdatedAt: snapshot.CapturedAt,
		},
		Key: "worker-source", Kind: research.SourceGit, Subdir: snapshot.Subdir,
		LocatorHints: []string{}, State: research.SourceActive,
	}
	request := sourcesnapshot.Request{
		Source: source, RepositoryRoot: workload.RepositoryRoot,
		RegisteredGitCommonDir:      workload.RegisteredGitCommonDir,
		RegisteredGitCommonIdentity: workload.RegisteredGitCommonIdentity,
		BaseCommit:                  snapshot.BaseCommit,
		ExpectedHead:                snapshot.HeadCommit,
		CapturedAt:                  snapshot.CapturedAt,
	}
	switch snapshot.State {
	case research.SourceSnapshotClean:
		request.AllowNoChanges = len(snapshot.ChangeSet) == 0
		observed, captureErr := verifier.CaptureClean(ctx, request)
		if captureErr != nil {
			return captureErr
		}
		if observed.Digest != snapshot.Digest {
			return sourcesnapshot.ErrSourceChanged
		}
	case research.SourceSnapshotDirty:
		tryID, parseErr := research.ParseIDForKind(workload.TryID, research.KindTry)
		if parseErr != nil {
			return parseErr
		}
		request.Try = tryID
		if _, verifyErr := verifier.VerifyDirty(ctx, request, snapshot); verifyErr != nil {
			return verifyErr
		}
	default:
		return errors.New("worker Source snapshot state is unsupported")
	}
	return nil
}

func workerGit(ctx context.Context, runner gitx.Runner, directory string, arguments ...string) (string, error) {
	stdout, stderr, err := runner.Run(ctx, directory, append([]string(nil), arguments...))
	if err != nil {
		return "", &gitx.Error{Dir: directory, Args: append([]string(nil), arguments...), Stderr: stderr, Err: err}
	}
	return stdout, nil
}

func validateWorkerDirtyPaths(output, runtimeConfigPath string) error {
	if output == "" {
		return nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return errors.New("Git status output is not NUL terminated")
	}
	for _, field := range strings.Split(output[:len(output)-1], "\x00") {
		if len(field) < 4 || field[2] != ' ' {
			return errors.New("Git status contains a rename/copy or malformed path")
		}
		path := field[3:]
		if path == runtimeConfigPath || path == "experiments" || strings.HasPrefix(path, "experiments/") {
			continue
		}
		return fmt.Errorf("uncommitted executable-tree path %q is not allowed", path)
	}
	return nil
}

func parseWorkerPaths(output string) ([]string, error) {
	if output == "" {
		return []string{}, nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return nil, errors.New("Git path output is not NUL terminated")
	}
	paths := strings.Split(output[:len(output)-1], "\x00")
	for _, path := range paths {
		if path == "" || !utf8.ValidString(path) || filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
			return nil, errors.New("Git returned an invalid changed path")
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func sameWorkerPaths(left, right []string) bool {
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
