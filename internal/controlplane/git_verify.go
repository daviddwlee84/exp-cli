package controlplane

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
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

// verifyRuntimeGit proves that each runtime currently reachable from canonical
// queued or active work executes the exact clean commit and exact base..head
// path set claimed by the canonical Attempt. Runtime config is operational and
// may retain entries for completed, dropped, or otherwise unrelated Plans;
// those stale entries must not block the live research frontier.
func (adapter Adapter) verifyRuntimeGit(ctx context.Context, inventory *record.Inventory, runtime *loadedRuntime) error {
	needed := runtimePlansRequiringGitVerification(inventory)
	planIDs := make([]research.ID, 0, len(needed))
	for planID := range needed {
		if _, configured := runtime.plans[planID]; configured {
			planIDs = append(planIDs, planID)
		}
	}
	if len(planIDs) == 0 {
		return nil
	}
	sort.Slice(planIDs, func(left, right int) bool { return planIDs[left].String() < planIDs[right].String() })

	runner := adapter.Git
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	main, err := gitx.DiscoverWithRunner(ctx, adapter.RepositoryRoot, runner)
	if err != nil {
		return fmt.Errorf("discover runtime repository: %w", err)
	}
	verified := map[string]struct{}{}
	worktrees, err := gitx.Worktrees(ctx, main.Root, runner)
	if err != nil {
		return fmt.Errorf("list registered runtime worktrees: %w", err)
	}
	for _, planID := range planIDs {
		plan := runtime.plans[planID]
		checkoutRoot := main.Root
		if plan.Checkout == "registered_worktree" {
			checkoutRoot, err = registeredWorktreeForHead(ctx, runner, main, worktrees, plan.HeadCommit)
			if err != nil {
				return fmt.Errorf("runtime Plan %s: %w", planID, err)
			}
			plan.absoluteCWD, err = pathx.ResolveUnderNoSymlinks(checkoutRoot, plan.CWD, true)
			if err != nil {
				return fmt.Errorf("runtime Plan %s cwd: %w", planID, err)
			}
			plan.repositoryRoot = checkoutRoot
			runtime.plans[planID] = plan
		}
		key := strings.Join([]string{plan.absoluteCWD, plan.BaseCommit, plan.HeadCommit, strings.Join(plan.ChangeSet, "\x00")}, "\x00")
		if _, found := verified[key]; found {
			continue
		}
		checkout, err := gitx.DiscoverWithRunner(ctx, plan.absoluteCWD, runner)
		if err != nil {
			return fmt.Errorf("runtime Plan %s checkout: %w", planID, err)
		}
		if checkout.GitCommonDir != main.GitCommonDir {
			return fmt.Errorf("runtime Plan %s cwd belongs to a different Git repository", planID)
		}
		head, err := runtimeGit(ctx, runner, checkout.Root, "rev-parse", "--verify", "HEAD")
		if err != nil || strings.TrimSpace(head) != plan.HeadCommit {
			return fmt.Errorf("runtime Plan %s HEAD does not equal head_commit %s", planID, plan.HeadCommit)
		}
		base, err := runtimeGit(ctx, runner, checkout.Root, "rev-parse", "--verify", plan.BaseCommit+"^{commit}")
		if err != nil || strings.TrimSpace(base) != plan.BaseCommit {
			return fmt.Errorf("runtime Plan %s base_commit cannot be resolved exactly", planID)
		}
		if _, err := runtimeGit(ctx, runner, checkout.Root, "merge-base", "--is-ancestor", plan.BaseCommit, plan.HeadCommit); err != nil {
			return fmt.Errorf("runtime Plan %s base_commit is not an ancestor of head_commit", planID)
		}
		dirty, err := runtimeGit(ctx, runner, checkout.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
		if err != nil {
			return fmt.Errorf("runtime Plan %s inspect worktree: %w", planID, err)
		}
		if err := validateRuntimeDirtyPaths(dirty, runtime.configPath); err != nil {
			return fmt.Errorf("runtime Plan %s worktree: %w", planID, err)
		}
		diff, err := runtimeGit(ctx, runner, checkout.Root, "diff", "--name-only", "-z", "--no-renames", plan.BaseCommit, plan.HeadCommit, "--")
		if err != nil {
			return fmt.Errorf("runtime Plan %s inspect committed change set: %w", planID, err)
		}
		paths, err := parseRuntimePaths(diff)
		if err != nil || !equalRuntimePaths(paths, plan.ChangeSet) {
			return fmt.Errorf("runtime Plan %s committed base..head paths differ from change_set: %w", planID, err)
		}
		verified[key] = struct{}{}
	}
	return nil
}

func runtimePlansRequiringGitVerification(inventory *record.Inventory) map[research.ID]struct{} {
	needed := map[research.ID]struct{}{}
	if inventory == nil {
		return needed
	}
	for _, document := range inventory.OfKind(research.KindQueue) {
		queue := document.Record.(*research.Queue)
		for _, partition := range queue.Partitions {
			for _, entry := range partition.Entries {
				planDocument, err := inventory.ByID(entry.Plan)
				if err != nil {
					continue
				}
				plan := planDocument.Record.(*research.Plan)
				if plan.Schema == research.SchemaPlanV2 && plan.State == research.PlanQueued {
					needed[plan.ID] = struct{}{}
				}
			}
		}
	}
	for _, document := range inventory.OfKind(research.KindPlan) {
		plan := document.Record.(*research.Plan)
		if plan.Schema != research.SchemaPlanV2 || plan.State != research.PlanStarted || plan.ResultingExperiment.IsZero() {
			continue
		}
		experimentDocument, err := inventory.ByID(plan.ResultingExperiment)
		if err != nil {
			continue
		}
		if experimentDocument.Record.(*research.Experiment).Lifecycle == research.LifecycleActive {
			needed[plan.ID] = struct{}{}
		}
	}
	return needed
}

func registeredWorktreeForHead(ctx context.Context, runner gitx.Runner, main gitx.Repository, worktrees []gitx.Worktree, head string) (string, error) {
	matches := []string{}
	for _, worktree := range worktrees {
		if worktree.Missing || worktree.Root == main.Root {
			continue
		}
		checkout, err := gitx.DiscoverWithRunner(ctx, worktree.Root, runner)
		if err != nil || checkout.GitCommonDir != main.GitCommonDir {
			continue
		}
		value, err := runtimeGit(ctx, runner, checkout.Root, "rev-parse", "--verify", "HEAD")
		if err == nil && strings.TrimSpace(value) == head {
			matches = append(matches, checkout.Root)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("head_commit %s matches %d registered linked worktrees; expected exactly one", head, len(matches))
	}
	return matches[0], nil
}

func runtimeGit(ctx context.Context, runner gitx.Runner, directory string, arguments ...string) (string, error) {
	stdout, stderr, err := runner.Run(ctx, directory, append([]string(nil), arguments...))
	if err != nil {
		return "", &gitx.Error{Dir: directory, Args: append([]string(nil), arguments...), Stderr: stderr, Err: err}
	}
	return stdout, nil
}

func validateRuntimeDirtyPaths(output, configPath string) error {
	if output == "" {
		return nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return errors.New("Git status output is not NUL terminated")
	}
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	for _, field := range strings.Split(output[:len(output)-1], "\x00") {
		if len(field) < 4 || field[2] != ' ' {
			return errors.New("Git status contains a rename/copy or malformed path")
		}
		path := field[3:]
		if path == configPath || path == "experiments" || strings.HasPrefix(path, "experiments/") {
			continue
		}
		return fmt.Errorf("uncommitted executable-tree path %q is not allowed", path)
	}
	return nil
}

func parseRuntimePaths(output string) ([]string, error) {
	if output == "" {
		return []string{}, nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return nil, errors.New("Git path output is not NUL terminated")
	}
	result := strings.Split(output[:len(output)-1], "\x00")
	for _, path := range result {
		if path == "" || !utf8.ValidString(path) || filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
			return nil, errors.New("Git returned an invalid changed path")
		}
	}
	sort.Strings(result)
	return result, nil
}

func equalRuntimePaths(left, right []string) bool {
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
