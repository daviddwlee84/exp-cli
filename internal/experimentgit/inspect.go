package experimentgit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

// Inspection is a non-mutating, exact view of one managed worktree.
type Inspection struct {
	Workspace  Workspace `json:"workspace"`
	HeadCommit string    `json:"head_commit"`
	Clean      bool      `json:"clean"`
	Paths      []string  `json:"paths"`
}

// Inspect validates repository, registration, path, branch, ancestry, metadata,
// and allowlist identity before reporting managed worktree state.
func (manager Manager) Inspect(ctx context.Context, request Request) (Inspection, error) {
	if ctx == nil {
		return Inspection{}, fmt.Errorf("context is required: %w", ErrInvalidRequest)
	}
	normalized, err := normalizeRequest(request)
	if err != nil {
		return Inspection{}, err
	}
	runner := manager.gitRunner()
	repository, err := discoverExact(ctx, runner, normalized.repositoryRoot)
	if err != nil {
		return Inspection{}, err
	}
	if normalized.registeredCommon != "" && repository.GitCommonDir != normalized.registeredCommon {
		return Inspection{}, fmt.Errorf("registered Git common directory differs from discovery: %w", ErrWorkspaceState)
	}
	base, err := resolveCommit(ctx, runner, repository.Root, normalized.baseCommit)
	if err != nil {
		return Inspection{}, err
	}
	worktree, err := manager.existingWorktreePath(repository, normalized)
	if err != nil {
		return Inspection{}, err
	}
	checkout, err := discoverExact(ctx, runner, worktree)
	if err != nil {
		return Inspection{}, fmt.Errorf("discover managed worktree: %w", err)
	}
	if checkout.GitCommonDir != repository.GitCommonDir {
		return Inspection{}, fmt.Errorf("managed worktree belongs to a different Git repository: %w", ErrWorkspaceState)
	}
	head, err := readHead(ctx, runner, worktree)
	if err != nil {
		return Inspection{}, err
	}
	if err := requireWorkspaceBranch(ctx, runner, worktree, normalized.branch); err != nil {
		return Inspection{}, err
	}
	if _, err := runGit(ctx, runner, worktree, "merge-base", "--is-ancestor", base, head); err != nil {
		return Inspection{}, fmt.Errorf("managed worktree HEAD is not descended from its base: %w", errors.Join(ErrWorkspaceState, err))
	}
	if err := rejectForbiddenMetadata(worktree, normalized.metadataRoot); err != nil {
		return Inspection{}, err
	}
	status, err := runGit(ctx, runner, worktree, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return Inspection{}, err
	}
	workingPaths, err := changedPaths(ctx, runner, worktree)
	if err != nil {
		return Inspection{}, err
	}
	committedOutput, err := runGit(ctx, runner, worktree, "diff", "--name-only", "-z", "--no-renames", base, head, "--")
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect committed managed-worktree paths: %w", err)
	}
	committedPaths, err := parseGitPaths(committedOutput)
	if err != nil {
		return Inspection{}, err
	}
	paths := mergePathSets(workingPaths, committedPaths)
	for _, changed := range paths {
		if forbiddenPath(changed, normalized.metadataRoot) {
			return Inspection{}, fmt.Errorf("%s: %w", changed, ErrForbiddenMetadata)
		}
		if !allowedPath(normalized, changed) && !baselinePath(normalized, changed) {
			return Inspection{}, fmt.Errorf("%s: %w", changed, ErrPathNotAllowed)
		}
	}
	cwd, err := pathx.ResolveUnderNoSymlinks(worktree, normalized.sourceSubdir, true)
	if err != nil {
		return Inspection{}, fmt.Errorf("resolve managed Source root: %w", errors.Join(ErrWorkspaceState, err))
	}
	workspace := Workspace{
		RepositoryRoot: repository.Root, Worktree: worktree, CWD: cwd,
		BaseCommit: base, Branch: normalized.branch,
		ProjectID: normalized.projectID, SourceID: normalized.sourceID, OwnerID: normalized.ownerID,
		SourceSubdir: normalized.sourceSubdir,
		AllowedGlobs: append([]string(nil), normalized.globs...), AllowedPaths: append([]string(nil), normalized.paths...),
		BaselinePaths: append([]string(nil), normalized.baselinePaths...),
	}
	return Inspection{Workspace: workspace, HeadCommit: head, Clean: status == "", Paths: paths}, nil
}

// Cleanup removes only a still-clean managed worktree at its original commit.
// It never forces removal and never deletes the retained exp/... branch.
func (manager Manager) Cleanup(ctx context.Context, request Request) (Inspection, error) {
	inspection, err := manager.Inspect(ctx, request)
	if err != nil {
		return inspection, err
	}
	if !inspection.Clean || inspection.HeadCommit != inspection.Workspace.BaseCommit || len(inspection.Paths) != 0 {
		return inspection, ErrCleanupRefused
	}
	guard, err := manager.Inspect(ctx, request)
	if err != nil || !guard.Clean || guard.HeadCommit != inspection.HeadCommit || guard.Workspace.Worktree != inspection.Workspace.Worktree || len(guard.Paths) != 0 {
		return inspection, fmt.Errorf("managed worktree changed before cleanup: %w", errors.Join(ErrCleanupRefused, err))
	}
	runner := manager.gitRunner()
	if _, err := runGit(ctx, runner, inspection.Workspace.RepositoryRoot, "worktree", "remove", "--", inspection.Workspace.Worktree); err != nil {
		return inspection, fmt.Errorf("remove clean managed worktree: %w", errors.Join(ErrCleanupRefused, err))
	}
	if _, err := os.Lstat(inspection.Workspace.Worktree); !errors.Is(err, fs.ErrNotExist) {
		return inspection, fmt.Errorf("managed worktree remains after cleanup: %w", errors.Join(ErrWorkspaceState, err))
	}
	return inspection, nil
}

// CleanupSeeded removes a dirty managed worktree only when verify proves twice
// that its complete bytes still equal the reproducible private seed. The force
// flag is required by Git for a dirty worktree; it is never reached for changed,
// moved, unverified, or non-manager-owned state. The retained branch is not
// deleted or rewritten.
func (manager Manager) CleanupSeeded(ctx context.Context, request Request, verify func(context.Context, string) error) (Inspection, error) {
	if verify == nil {
		return Inspection{}, fmt.Errorf("seed verifier is required: %w", ErrCleanupRefused)
	}
	inspection, err := manager.Inspect(ctx, request)
	if err != nil {
		return inspection, err
	}
	if inspection.HeadCommit != inspection.Workspace.BaseCommit {
		return inspection, ErrCleanupRefused
	}
	if err := verify(ctx, inspection.Workspace.Worktree); err != nil {
		return inspection, fmt.Errorf("managed worktree differs from its private seed: %w", errors.Join(ErrCleanupRefused, err))
	}
	guard, err := manager.Inspect(ctx, request)
	if err != nil || guard.HeadCommit != inspection.HeadCommit || guard.Workspace.Worktree != inspection.Workspace.Worktree {
		return inspection, fmt.Errorf("managed seeded worktree changed before cleanup: %w", errors.Join(ErrCleanupRefused, err))
	}
	if err := verify(ctx, guard.Workspace.Worktree); err != nil {
		return inspection, fmt.Errorf("managed worktree changed after seed verification: %w", errors.Join(ErrCleanupRefused, err))
	}
	runner := manager.gitRunner()
	if _, err := runGit(ctx, runner, inspection.Workspace.RepositoryRoot, "worktree", "remove", "--force", "--", inspection.Workspace.Worktree); err != nil {
		return inspection, fmt.Errorf("remove exactly seeded managed worktree: %w", errors.Join(ErrCleanupRefused, err))
	}
	if _, err := os.Lstat(inspection.Workspace.Worktree); !errors.Is(err, fs.ErrNotExist) {
		return inspection, fmt.Errorf("managed worktree remains after seeded cleanup: %w", errors.Join(ErrWorkspaceState, err))
	}
	return inspection, nil
}

// RemovePreparingBranch removes only an unadvanced deterministic branch left by
// a preparation crash before its worktree became inspectable. Callers must hold
// and authenticate the private preparation phase.
func (manager Manager) RemovePreparingBranch(ctx context.Context, request Request) error {
	normalized, err := normalizeRequest(request)
	if err != nil {
		return err
	}
	runner := manager.gitRunner()
	repository, err := discoverExact(ctx, runner, normalized.repositoryRoot)
	if err != nil {
		return err
	}
	base, err := resolveCommit(ctx, runner, repository.Root, normalized.baseCommit)
	if err != nil {
		return err
	}
	output, err := runGit(ctx, runner, repository.Root, "branch", "--list", "--format=%(objectname)", "--", normalized.branch)
	if err != nil {
		return err
	}
	if output == "" {
		return nil
	}
	branchHead, err := singleLine(output)
	if err != nil || branchHead != base {
		return fmt.Errorf("preparing branch is not exactly at its registered base: %w", errors.Join(ErrCleanupRefused, err))
	}
	if _, err := runGit(ctx, runner, repository.Root, "branch", "-D", "--", normalized.branch); err != nil {
		return fmt.Errorf("remove orphaned preparing branch: %w", errors.Join(ErrWorkspaceState, err))
	}
	return nil
}

// RemovePreparing removes a manager-owned workspace whose private preparation
// phase is still active. Callers must hold the exact workspace lock and
// authenticate the phase marker before invoking this recovery-only force path.
func (manager Manager) RemovePreparing(ctx context.Context, request Request) (Inspection, error) {
	inspection, err := manager.Inspect(ctx, request)
	if err != nil {
		return inspection, err
	}
	if inspection.HeadCommit != inspection.Workspace.BaseCommit {
		return inspection, ErrCleanupRefused
	}
	runner := manager.gitRunner()
	if _, err := runGit(ctx, runner, inspection.Workspace.RepositoryRoot, "worktree", "remove", "--force", "--", inspection.Workspace.Worktree); err != nil {
		return inspection, fmt.Errorf("remove incomplete preparing worktree: %w", errors.Join(ErrCleanupRefused, err))
	}
	if _, err := os.Lstat(inspection.Workspace.Worktree); !errors.Is(err, fs.ErrNotExist) {
		return inspection, fmt.Errorf("preparing worktree remains after removal: %w", errors.Join(ErrWorkspaceState, err))
	}
	if _, err := runGit(ctx, runner, inspection.Workspace.RepositoryRoot, "worktree", "prune", "--expire", "now"); err != nil {
		return inspection, fmt.Errorf("prune removed preparing worktree metadata: %w", errors.Join(ErrWorkspaceState, err))
	}
	if _, err := runGit(ctx, runner, inspection.Workspace.RepositoryRoot, "branch", "-D", "--", inspection.Workspace.Branch); err != nil {
		return inspection, fmt.Errorf("remove incomplete preparing branch: %w", errors.Join(ErrWorkspaceState, err))
	}
	return inspection, nil
}

func mergePathSets(sets ...[]string) []string {
	set := make(map[string]struct{})
	for _, values := range sets {
		for _, value := range values {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
