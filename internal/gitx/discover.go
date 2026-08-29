package gitx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Repository identifies one checkout and the common administrative directory
// shared by every linked worktree of its clone.
type Repository struct {
	Root             string
	GitDir           string
	GitCommonDir     string
	Name             string
	IsLinkedWorktree bool
	Bare             bool
}

// CoordinationDir is the shared local-state root for exp v1.
func (repository Repository) CoordinationDir() string {
	return filepath.Join(repository.GitCommonDir, "exp", "v1")
}

// Discover resolves the Git working tree containing dir using installed Git.
func Discover(ctx context.Context, dir string) (Repository, error) {
	return DiscoverWithRunner(ctx, dir, ExecRunner{})
}

// DiscoverWithRunner exposes the exact argument-array seam for tests. Values are
// queried separately because Git path output may legally contain newlines.
func DiscoverWithRunner(ctx context.Context, dir string, runner Runner) (Repository, error) {
	if dir == "" {
		dir = "."
	}
	bareValue, err := run(ctx, runner, dir, "rev-parse", "--is-bare-repository")
	if err != nil {
		return Repository{}, err
	}
	if bareValue != "true" && bareValue != "false" {
		return Repository{}, fmt.Errorf("parse Git bare-repository result %q", bareValue)
	}
	if bareValue == "true" {
		return Repository{}, ErrBareRepository
	}
	root, err := run(ctx, runner, dir, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return Repository{}, err
	}
	gitDir, err := run(ctx, runner, dir, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return Repository{}, err
	}
	commonDir, err := run(ctx, runner, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Repository{}, err
	}
	repository := Repository{Root: filepath.Clean(root), GitDir: filepath.Clean(gitDir), GitCommonDir: filepath.Clean(commonDir)}
	for name, value := range map[string]string{"worktree root": repository.Root, "git dir": repository.GitDir, "git common dir": repository.GitCommonDir} {
		if value == "" || !filepath.IsAbs(value) {
			return Repository{}, fmt.Errorf("Git returned non-absolute %s %q", name, value)
		}
	}
	repository.Root, err = filepath.EvalSymlinks(repository.Root)
	if err != nil {
		return Repository{}, fmt.Errorf("canonicalize Git worktree root: %w", err)
	}
	repository.GitDir, err = filepath.EvalSymlinks(repository.GitDir)
	if err != nil {
		return Repository{}, fmt.Errorf("canonicalize Git directory: %w", err)
	}
	repository.GitCommonDir, err = filepath.EvalSymlinks(repository.GitCommonDir)
	if err != nil {
		return Repository{}, fmt.Errorf("canonicalize Git common directory: %w", err)
	}
	repository.IsLinkedWorktree = repository.GitDir != repository.GitCommonDir
	repository.Name = filepath.Base(repository.Root)
	return repository, nil
}

// Worktree describes one Git-common worktree registration. Missing is true only
// when Git marks the registration prunable and the path has no filesystem entry.
type Worktree struct {
	Root     string
	Prunable bool
	Missing  bool
}

// Worktrees returns every worktree registration. Porcelain -z preserves spaces
// and embedded newlines exactly. An inaccessible or malformed registration fails
// closed; only a prunable path proven physically absent is retained as Missing.
func Worktrees(ctx context.Context, dir string, runner Runner) ([]Worktree, error) {
	output, err := run(ctx, runner, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	type rawWorktree struct {
		path     string
		prunable bool
	}
	var raw []rawWorktree
	current := rawWorktree{}
	flush := func() {
		if current.path != "" {
			raw = append(raw, current)
		}
		current = rawWorktree{}
	}
	for _, field := range strings.Split(output, "\x00") {
		switch {
		case field == "":
			flush()
		case strings.HasPrefix(field, "worktree "):
			flush()
			current.path = strings.TrimPrefix(field, "worktree ")
		case field == "prunable" || strings.HasPrefix(field, "prunable "):
			current.prunable = true
		}
	}
	flush()
	if len(raw) == 0 {
		return nil, errorsNoWorktrees()
	}

	seen := make(map[string]struct{})
	worktrees := make([]Worktree, 0, len(raw))
	for _, entry := range raw {
		value := entry.path
		if value == "" || !filepath.IsAbs(value) {
			return nil, fmt.Errorf("Git returned invalid worktree path %q", value)
		}
		canonical, canonicalErr := filepath.EvalSymlinks(value)
		missing := false
		if canonicalErr != nil {
			if !entry.prunable || !errors.Is(canonicalErr, fs.ErrNotExist) {
				return nil, fmt.Errorf("canonicalize linked worktree %q: %w", value, canonicalErr)
			}
			if _, lstatErr := os.Lstat(value); !errors.Is(lstatErr, fs.ErrNotExist) {
				return nil, fmt.Errorf("verify missing prunable worktree %q: %w", value, errors.Join(canonicalErr, lstatErr))
			}
			canonical = filepath.Clean(value)
			missing = true
		} else {
			canonical = filepath.Clean(canonical)
		}
		key := canonical
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		worktrees = append(worktrees, Worktree{Root: canonical, Prunable: entry.prunable, Missing: missing})
	}
	sort.Slice(worktrees, func(left, right int) bool { return worktrees[left].Root < worktrees[right].Root })
	return worktrees, nil
}

// WorktreeRoots returns currently accessible worktree roots. Missing prunable
// registrations are intentionally omitted; mutation callers use Worktrees so
// they can keep those absences in their publication guard.
func WorktreeRoots(ctx context.Context, dir string, runner Runner) ([]string, error) {
	worktrees, err := Worktrees(ctx, dir, runner)
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(worktrees))
	for _, worktree := range worktrees {
		if !worktree.Missing {
			roots = append(roots, worktree.Root)
		}
	}
	if len(roots) == 0 {
		return nil, errorsNoWorktrees()
	}
	return roots, nil
}

// VerifyMissingWorktrees confirms that every skipped prunable registration is
// still physically absent. Mutation callers invoke this at publication guards.
func VerifyMissingWorktrees(worktrees []Worktree) error {
	for _, worktree := range worktrees {
		if !worktree.Missing {
			continue
		}
		if _, err := os.Lstat(worktree.Root); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("recheck missing prunable worktree %q: %w", worktree.Root, err)
		}
		return fmt.Errorf("prunable worktree %q appeared during operation", worktree.Root)
	}
	return nil
}

func errorsNoWorktrees() error {
	return fmt.Errorf("Git returned no worktrees")
}
