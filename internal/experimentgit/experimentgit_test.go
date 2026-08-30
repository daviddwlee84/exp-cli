package experimentgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	testBase = "1111111111111111111111111111111111111111"
	testHead = "2222222222222222222222222222222222222222"
)

type gitStep struct {
	dir    string
	args   []string
	stdout string
	stderr string
	err    error
	after  func() error
}

type sequenceRunner struct {
	t     *testing.T
	steps []gitStep
	index int
}

func (runner *sequenceRunner) Run(_ context.Context, dir string, args []string) (string, string, error) {
	runner.t.Helper()
	if runner.index >= len(runner.steps) {
		runner.t.Fatalf("unexpected git call in %s: %#v", dir, args)
	}
	step := runner.steps[runner.index]
	runner.index++
	if dir != step.dir || !reflect.DeepEqual(args, step.args) {
		runner.t.Fatalf("git call %d = dir %q args %#v, want dir %q args %#v", runner.index, dir, args, step.dir, step.args)
	}
	if step.after != nil {
		if err := step.after(); err != nil {
			runner.t.Fatal(err)
		}
	}
	return step.stdout, step.stderr, step.err
}

func (runner *sequenceRunner) requireDone() {
	runner.t.Helper()
	if runner.index != len(runner.steps) {
		runner.t.Fatalf("consumed %d of %d expected git calls", runner.index, len(runner.steps))
	}
}

func TestManagerUsesArgumentArraysAndStagesOnlyExactPaths(t *testing.T) {
	repository := filepath.Join(canonicalTempDir(t), "sample repo")
	gitDir := filepath.Join(repository, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dataHome := filepath.Join(canonicalTempDir(t), "data home")
	id := mustExperimentID(t, "exp_01a01e67-e340-7303-8000-000000000303")
	request := Request{
		RepositoryRoot: repository, BaseCommit: testBase, ExperimentID: id,
		ExperimentTitle: "Tune Encoder", AllowedPathGlobs: []string{"src/**"},
	}
	short := id.UUIDHex()
	project := projectNamespace(gitx.Repository{Name: filepath.Base(repository), GitCommonDir: gitDir})
	worktree := filepath.Join(dataHome, "exp", "worktrees", project, short+"-tune-encoder")
	worktreeGitDir := filepath.Join(worktree, ".git")
	branch := "exp/" + short + "-tune-encoder"

	discoverSource := []gitStep{
		{dir: repository, args: []string{"rev-parse", "--is-bare-repository"}, stdout: "false\n"},
		{dir: repository, args: []string{"rev-parse", "--path-format=absolute", "--show-toplevel"}, stdout: repository + "\n"},
		{dir: repository, args: []string{"rev-parse", "--path-format=absolute", "--git-dir"}, stdout: gitDir + "\n"},
		{dir: repository, args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, stdout: gitDir + "\n"},
	}
	discoverWorktree := []gitStep{
		{dir: worktree, args: []string{"rev-parse", "--is-bare-repository"}, stdout: "false\n"},
		{dir: worktree, args: []string{"rev-parse", "--path-format=absolute", "--show-toplevel"}, stdout: worktree + "\n"},
		{dir: worktree, args: []string{"rev-parse", "--path-format=absolute", "--git-dir"}, stdout: worktreeGitDir + "\n"},
		{dir: worktree, args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, stdout: gitDir + "\n"},
	}
	steps := append([]gitStep{}, discoverSource...)
	steps = append(steps,
		gitStep{dir: repository, args: []string{"rev-parse", "--verify", testBase + "^{commit}"}, stdout: testBase + "\n"},
		gitStep{dir: repository, args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}},
		gitStep{dir: repository, args: []string{"worktree", "add", "-b", branch, worktree, testBase}, after: func() error {
			return os.MkdirAll(worktreeGitDir, 0o755)
		}},
		gitStep{dir: worktree, args: []string{"rev-parse", "--verify", "HEAD"}, stdout: testBase + "\n"},
		gitStep{dir: worktree, args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, stdout: branch + "\n"},
		gitStep{dir: worktree, args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}},
	)
	steps = append(steps, discoverSource...)
	steps = append(steps, gitStep{dir: repository, args: []string{"rev-parse", "--verify", testBase + "^{commit}"}, stdout: testBase + "\n"})
	steps = append(steps, discoverWorktree...)
	steps = append(steps,
		gitStep{dir: worktree, args: []string{"rev-parse", "--verify", "HEAD"}, stdout: testBase + "\n"},
		gitStep{dir: worktree, args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, stdout: branch + "\n"},
		gitStep{dir: worktree, args: []string{"diff", "--name-only", "-z", "--no-renames", "HEAD", "--"}, stdout: "src/z.go\x00"},
		gitStep{dir: worktree, args: []string{"ls-files", "--others", "--exclude-standard", "-z", "--"}, stdout: "src/a.go\x00"},
		gitStep{dir: worktree, args: []string{"add", "--", "src/a.go", "src/z.go"}},
		gitStep{dir: worktree, args: []string{"diff", "--cached", "--name-only", "-z", "--no-renames", "HEAD", "--"}, stdout: "src/a.go\x00src/z.go\x00"},
		gitStep{dir: worktree, args: []string{"commit", "--no-gpg-sign", "--no-verify", "-m", "Record experiment " + short + " changes for tune-encoder"}},
		gitStep{dir: worktree, args: []string{"rev-parse", "--verify", "HEAD"}, stdout: testHead + "\n"},
		gitStep{dir: worktree, args: []string{"rev-list", "--parents", "-n", "1", testHead}, stdout: testHead + " " + testBase + "\n"},
		gitStep{dir: worktree, args: []string{"diff", "--name-only", "-z", "--no-renames", testBase, testHead, "--"}, stdout: "src/a.go\x00src/z.go\x00"},
		gitStep{dir: worktree, args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}},
	)
	runner := &sequenceRunner{t: t, steps: steps}
	manager := Manager{Git: runner, DataHome: dataHome}

	workspace, err := manager.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Worktree != worktree || workspace.Branch != branch || workspace.BaseCommit != testBase {
		t.Fatalf("workspace = %#v", workspace)
	}
	result, err := manager.Commit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{"exp-change-set-v1", testBase, testHead, "src/a.go", "src/z.go"}, "\x00")))
	wantDigest := "sha256:" + hex.EncodeToString(digest[:])
	if result.BaseCommit != testBase || result.HeadCommit != testHead || result.Branch != branch || result.Worktree != worktree || result.DiffDigest != wantDigest || !reflect.DeepEqual(result.Paths, []string{"src/a.go", "src/z.go"}) {
		t.Fatalf("change set = %#v", result)
	}
	runner.requireDone()
}

func TestRealGitWorktreeCommitAllowlistAndSafetyFailures(t *testing.T) {
	repository, base := newTestRepository(t)
	dataHome := filepath.Join(canonicalTempDir(t), "xdg-data")
	t.Setenv("XDG_DATA_HOME", dataHome)
	manager := Manager{}

	request := Request{
		RepositoryRoot: repository, BaseCommit: base,
		ExperimentID:    mustExperimentID(t, "exp_01a01e67-e340-7303-8000-000000000303"),
		ExperimentTitle: "Tune Encoder", AllowedPathGlobs: []string{"src/**"},
	}
	workspace, err := manager.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	repositoryInfo, err := gitx.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	short := request.ExperimentID.UUIDHex()
	wantWorktree := filepath.Join(dataHome, "exp", "worktrees", projectNamespace(repositoryInfo), short+"-tune-encoder")
	if workspace.Worktree != wantWorktree || workspace.Branch != "exp/"+short+"-tune-encoder" {
		t.Fatalf("workspace = %#v", workspace)
	}
	if err := os.WriteFile(filepath.Join(workspace.Worktree, "src", "model.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Worktree, "src", "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Commit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Paths, []string{"src/model.txt", "src/new.txt"}) || result.BaseCommit != base || result.HeadCommit == base || !strings.HasPrefix(result.DiffDigest, "sha256:") {
		t.Fatalf("change set = %#v", result)
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{"exp-change-set-v1", base, result.HeadCommit, "src/model.txt", "src/new.txt"}, "\x00")))
	if result.DiffDigest != "sha256:"+hex.EncodeToString(digest[:]) {
		t.Fatalf("diff digest = %s", result.DiffDigest)
	}
	replayed, err := manager.Commit(t.Context(), request)
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("idempotent commit replay = %#v, %v", replayed, err)
	}
	if got := strings.TrimSpace(string(runTestGitBytes(t, workspace.Worktree, "log", "-1", "--pretty=%s"))); got != "Record experiment "+short+" changes for tune-encoder" {
		t.Fatalf("commit message = %q", got)
	}
	if got := strings.TrimSpace(string(runTestGitBytes(t, repository, "rev-parse", "HEAD"))); got != base {
		t.Fatalf("source checkout HEAD = %s, want %s", got, base)
	}
	if _, err := os.Stat(workspace.Worktree); err != nil {
		t.Fatalf("worktree was removed: %v", err)
	}
	if listing := string(runTestGitBytes(t, repository, "worktree", "list", "--porcelain")); !strings.Contains(listing, workspace.Worktree) {
		t.Fatalf("worktree registration missing:\n%s", listing)
	}

	disallowed := Request{
		RepositoryRoot: repository, BaseCommit: base,
		ExperimentID:    mustExperimentID(t, "exp_01a01e70-0000-7108-8000-000000000808"),
		ExperimentTitle: "Disallowed Change", AllowedPathGlobs: []string{"src/**"},
	}
	disallowedWorkspace, err := manager.Prepare(t.Context(), disallowed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disallowedWorkspace.Worktree, "outside.txt"), []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Commit(t.Context(), disallowed); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("disallowed commit error = %v", err)
	}
	if staged := runTestGitBytes(t, disallowedWorkspace.Worktree, "diff", "--cached", "--name-only"); len(staged) != 0 {
		t.Fatalf("disallowed path was staged: %q", staged)
	}
	if got := strings.TrimSpace(string(runTestGitBytes(t, disallowedWorkspace.Worktree, "rev-parse", "HEAD"))); got != base {
		t.Fatalf("disallowed worktree HEAD = %s", got)
	}

	forbidden := Request{
		RepositoryRoot: repository, BaseCommit: base,
		ExperimentID:    mustExperimentID(t, "exp_01a01e71-0000-7109-8000-000000000909"),
		ExperimentTitle: "Forbidden Metadata", AllowedPathGlobs: []string{"**"},
	}
	forbiddenWorkspace, err := manager.Prepare(t.Context(), forbidden)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(forbiddenWorkspace.Worktree, "experiments", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Commit(t.Context(), forbidden); !errors.Is(err, ErrForbiddenMetadata) {
		t.Fatalf("forbidden metadata error = %v", err)
	}
	if _, err := os.Stat(forbiddenWorkspace.Worktree); err != nil {
		t.Fatalf("failed worktree was removed: %v", err)
	}

	forbiddenRecord := Request{
		RepositoryRoot: repository, BaseCommit: base,
		ExperimentID:    mustExperimentID(t, "exp_01a01e71-0000-7110-8000-000000000910"),
		ExperimentTitle: "Forbidden canonical record", AllowedPathGlobs: []string{"**"},
	}
	forbiddenRecordWorkspace, err := manager.Prepare(t.Context(), forbiddenRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(forbiddenRecordWorkspace.Worktree, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(forbiddenRecordWorkspace.Worktree, "experiments", "injected.md"), []byte("not canonical\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Commit(t.Context(), forbiddenRecord); !errors.Is(err, ErrForbiddenMetadata) {
		t.Fatalf("canonical experiments path error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty := Request{
		RepositoryRoot: repository, BaseCommit: base,
		ExperimentID:    mustExperimentID(t, "exp_01a01e72-0000-7110-8000-000000001010"),
		ExperimentTitle: "Dirty Source", AllowedPathGlobs: []string{"src/**"},
	}
	if _, err := manager.Prepare(t.Context(), dirty); !errors.Is(err, ErrDirtyBase) {
		t.Fatalf("dirty source prepare error = %v", err)
	}
	dirtyPath := filepath.Join(dataHome, "exp", "worktrees", projectNamespace(repositoryInfo), dirty.ExperimentID.UUIDHex()+"-dirty-source")
	if _, err := os.Stat(dirtyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirty source created worktree %s: %v", dirtyPath, err)
	}
}

func TestRecursiveAllowedPathGlobs(t *testing.T) {
	tests := []struct {
		glob  string
		path  string
		match bool
	}{
		{glob: "src/**", path: "src/model.go", match: true},
		{glob: "src/**", path: "src/deep/model.go", match: true},
		{glob: "**/*.go", path: "main.go", match: true},
		{glob: "**/*.go", path: "src/main.go", match: true},
		{glob: "src/*.go", path: "src/deep/main.go", match: false},
		{glob: "docs/file?.md", path: "docs/file1.md", match: true},
	}
	for _, test := range tests {
		if got := matchGlob(test.glob, test.path); got != test.match {
			t.Errorf("matchGlob(%q, %q) = %t", test.glob, test.path, got)
		}
	}
}

func newTestRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := filepath.Join(canonicalTempDir(t), "research-project")
	if err := os.MkdirAll(filepath.Join(repository, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGitBytes(t, repository, "init", "--quiet")
	runTestGitBytes(t, repository, "config", "user.name", "Experiment Agent")
	runTestGitBytes(t, repository, "config", "user.email", "experiment-agent@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "src", "model.txt"), []byte("baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGitBytes(t, repository, "add", "--", "README.md", "src/model.txt")
	runTestGitBytes(t, repository, "commit", "--quiet", "-m", "Initial baseline")
	base := strings.TrimSpace(string(runTestGitBytes(t, repository, "rev-parse", "HEAD")))
	return repository, base
}

func runTestGitBytes(t *testing.T, directory string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, directory, err, output)
	}
	return output
}

func mustExperimentID(t *testing.T, value string) research.ID {
	t.Helper()
	id, err := research.ParseIDForKind(value, research.KindExperiment)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
