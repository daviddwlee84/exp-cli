package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestDiscoverWithRunnerUsesSeparateQueries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo\nwith-newline")
	gitDir := filepath.Join(root, ".git", "worktrees", "linked")
	common := filepath.Join(root, ".git")
	for _, directory := range []string{root, gitDir, common} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root, _ = filepath.EvalSymlinks(root)
	gitDir = filepath.Join(root, ".git", "worktrees", "linked")
	common = filepath.Join(root, ".git")
	var gotDir string
	var gotArgs [][]string
	runner := RunnerFunc(func(_ context.Context, dir string, args []string) (string, string, error) {
		gotDir = dir
		gotArgs = append(gotArgs, append([]string(nil), args...))
		switch strings.Join(args, "\x00") {
		case "rev-parse\x00--is-bare-repository":
			return "false\n", "", nil
		case "rev-parse\x00--path-format=absolute\x00--show-toplevel":
			return root + "\n", "", nil
		case "rev-parse\x00--path-format=absolute\x00--git-dir":
			return gitDir + "\n", "", nil
		case "rev-parse\x00--path-format=absolute\x00--git-common-dir":
			return common + "\n", "", nil
		default:
			return "", "", errors.New("unexpected Git query")
		}
	})
	repository, err := DiscoverWithRunner(context.Background(), "/probe path", runner)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := [][]string{
		{"rev-parse", "--is-bare-repository"},
		{"rev-parse", "--path-format=absolute", "--show-toplevel"},
		{"rev-parse", "--path-format=absolute", "--git-dir"},
		{"rev-parse", "--path-format=absolute", "--git-common-dir"},
	}
	if gotDir != "/probe path" || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("runner calls = %q %#v", gotDir, gotArgs)
	}
	if repository.Root != root || repository.GitCommonDir != common || !repository.IsLinkedWorktree {
		t.Fatalf("repository = %#v", repository)
	}
}

func TestDiscoverUsesInstalledGitAndRequiresWorktree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "--quiet")
	subdir := filepath.Join(root, "nested")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	repository, err := Discover(context.Background(), subdir)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	if repository.Root != canonicalRoot || repository.GitCommonDir != filepath.Join(canonicalRoot, ".git") || repository.IsLinkedWorktree {
		t.Fatalf("repository = %#v, root %q", repository, canonicalRoot)
	}
	if _, err := Discover(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("non-repository error = %v", err)
	}
}

func TestDiscoverIgnoresInheritedRepositorySelectionEnvironment(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for _, root := range []string{first, second} {
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, root, "init", "--quiet")
	}
	t.Setenv("GIT_DIR", filepath.Join(second, ".git"))
	t.Setenv("GIT_WORK_TREE", second)
	t.Setenv("GIT_COMMON_DIR", filepath.Join(second, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(second, ".git", "index"))
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(second, ".git", "objects"))
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", filepath.Join(first, ".git", "objects"))

	repository, err := Discover(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(first)
	if repository.Root != canonical {
		t.Fatalf("repository root = %q, want %q", repository.Root, canonical)
	}
}

func TestDiscoverPreservesNewlineInRepositoryPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo\nwith-newline")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "--quiet")
	repository, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(root)
	if repository.Root != canonical {
		t.Fatalf("repository root = %q, want %q", repository.Root, canonical)
	}
}

func TestWorktreeRootsParsesNULDelimitedNewlinePaths(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first\nworktree")
	second := filepath.Join(t.TempDir(), "second")
	for _, value := range []string{first, second} {
		if err := os.MkdirAll(value, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := RunnerFunc(func(_ context.Context, _ string, args []string) (string, string, error) {
		want := []string{"worktree", "list", "--porcelain", "-z"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args = %#v, want %#v", args, want)
		}
		return "worktree " + first + "\x00HEAD abc\x00\x00worktree " + second + "\x00HEAD def\x00\x00", "", nil
	})
	roots, err := WorktreeRoots(context.Background(), first, runner)
	if err != nil {
		t.Fatal(err)
	}
	firstCanonical, _ := filepath.EvalSymlinks(first)
	secondCanonical, _ := filepath.EvalSymlinks(second)
	want := []string{firstCanonical, secondCanonical}
	sort.Strings(want)
	if !reflect.DeepEqual(roots, want) {
		t.Fatalf("roots = %#v, want %#v", roots, want)
	}
}

func TestExecRunnerBoundsCapturedOutput(t *testing.T) {
	t.Setenv("EXP_GIT_OUTPUT_HELPER", "1")
	runner := ExecRunner{Binary: os.Args[0]}
	stdout, _, err := runner.Run(context.Background(), t.TempDir(), []string{"-test.run=^TestGitOutputHelper$"})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("oversized Git output error = %v", err)
	}
	if len(stdout) != MaxGitOutputBytes {
		t.Fatalf("captured stdout = %d bytes, want %d", len(stdout), MaxGitOutputBytes)
	}
}

func TestGitOutputHelper(t *testing.T) {
	if os.Getenv("EXP_GIT_OUTPUT_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.Write(make([]byte, MaxGitOutputBytes+1))
	os.Exit(0)
}

func TestWorktreesSkipsOnlyPhysicallyMissingPrunableRegistrations(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "removed")
	runner := RunnerFunc(func(_ context.Context, _ string, args []string) (string, string, error) {
		want := []string{"worktree", "list", "--porcelain", "-z"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args = %#v, want %#v", args, want)
		}
		return "worktree " + existing + "\x00HEAD abc\x00\x00worktree " + missing + "\x00HEAD def\x00prunable gitdir file points to non-existent location\x00\x00", "", nil
	})
	worktrees, err := Worktrees(context.Background(), existing, runner)
	missingCount := 0
	for _, worktree := range worktrees {
		if worktree.Missing && worktree.Prunable && worktree.Root == missing {
			missingCount++
		}
	}
	if err != nil || len(worktrees) != 2 || missingCount != 1 {
		t.Fatalf("Worktrees() = %#v, %v", worktrees, err)
	}
	roots, err := WorktreeRoots(context.Background(), existing, runner)
	existingCanonical, canonicalErr := filepath.EvalSymlinks(existing)
	if err != nil || canonicalErr != nil || !reflect.DeepEqual(roots, []string{existingCanonical}) {
		t.Fatalf("WorktreeRoots() = %#v, %v (canonical %v)", roots, err, canonicalErr)
	}
	if err := VerifyMissingWorktrees(worktrees); err != nil {
		t.Fatalf("verify absent worktree: %v", err)
	}
	if err := os.Mkdir(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMissingWorktrees(worktrees); err == nil {
		t.Fatal("appearing prunable worktree was not detected")
	}
}

func TestWorktreesRejectsMissingNonPrunableRegistration(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "removed")
	runner := RunnerFunc(func(context.Context, string, []string) (string, string, error) {
		return "worktree " + missing + "\x00HEAD abc\x00\x00", "", nil
	})
	if _, err := Worktrees(context.Background(), t.TempDir(), runner); err == nil {
		t.Fatal("missing non-prunable worktree was accepted")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
