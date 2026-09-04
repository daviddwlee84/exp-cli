package record_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestLinkedWorktreeStoresShareGitCommonLock(t *testing.T) {
	mainInfo := initializeStoreProject(t)
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.name", "Exp Test")
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.email", "exp-test@example.invalid")
	runGitCommand(t, mainInfo.Repository.Root, "add", "experiments")
	runGitCommand(t, mainInfo.Repository.Root, "commit", "--quiet", "-m", "initialize experiments")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runGitCommand(t, mainInfo.Repository.Root, "worktree", "add", "--quiet", "-b", "linked-test", linkedRoot)
	linkedInfo, err := project.Discover(context.Background(), linkedRoot)
	if err != nil {
		t.Fatalf("discover linked worktree: %v", err)
	}
	if linkedInfo.Repository.GitCommonDir != mainInfo.Repository.GitCommonDir || !linkedInfo.Repository.IsLinkedWorktree {
		t.Fatalf("linked repository = %#v; main = %#v", linkedInfo.Repository, mainInfo.Repository)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	mainStore := record.NewStore(mainInfo.Root, mainInfo.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e80-0000-7101-8000-000000000101"), nil
		}),
		record.WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage == record.StageTempWrite {
				once.Do(func() { close(entered) })
				<-release
			}
			return nil
		}),
	)
	linkedStore := record.NewStore(linkedInfo.Root, linkedInfo.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 13, 0, 1, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e81-0000-7202-8000-000000000202"), nil
		}),
	)

	type result struct {
		document *record.Document
		err      error
	}
	mainResult := make(chan result, 1)
	go func() {
		document, err := mainStore.CreatePlan(context.Background(), linkedPlanInput("Main worktree Plan"))
		mainResult <- result{document: document, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("main writer did not reach atomic hook")
	}

	linkedStarted := make(chan struct{})
	linkedResult := make(chan result, 1)
	go func() {
		close(linkedStarted)
		document, err := linkedStore.CreatePlan(context.Background(), linkedPlanInput("Linked worktree Plan"))
		linkedResult <- result{document: document, err: err}
	}()
	<-linkedStarted
	select {
	case result := <-linkedResult:
		t.Fatalf("linked writer bypassed common lock: %#v", result)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	first := <-mainResult
	second := <-linkedResult
	if first.err != nil || second.err != nil {
		t.Fatalf("writers failed: main %v, linked %v", first.err, second.err)
	}
	firstID, _ := first.document.ID()
	secondID, _ := second.document.ID()
	if firstID == secondID {
		t.Fatalf("linked creates reused ID %s", firstID)
	}
	for name, store := range map[string]*record.Store{"main": mainStore, "linked": linkedStore} {
		plans, diagnostics, err := store.ListPlans(context.Background())
		if err != nil || len(diagnostics) != 0 || len(plans) != 1 {
			t.Fatalf("%s inventory = %d plans, %v, %v", name, len(plans), diagnostics, err)
		}
	}
}

func TestPreparedTransactionRecoveryIsScopedToOriginatingWorktree(t *testing.T) {
	mainInfo := initializeStoreProject(t)
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.name", "Exp Test")
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.email", "exp-test@example.invalid")
	runGitCommand(t, mainInfo.Repository.Root, "add", "experiments")
	runGitCommand(t, mainInfo.Repository.Root, "commit", "--quiet", "-m", "initialize transaction origin")
	linkedRoot := filepath.Join(t.TempDir(), "linked-origin")
	runGitCommand(t, mainInfo.Repository.Root, "worktree", "add", "--quiet", "-b", "linked-origin-test", linkedRoot)
	linkedInfo, err := project.Discover(context.Background(), linkedRoot)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC)
	injected := errors.New("leave linked transaction prepared")
	linkedStore := record.NewStore(linkedInfo.Root, linkedInfo.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return now }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a09000-0000-7001-8000-000000000001"), nil
		}),
		record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
			if stage == record.StageTransactionCanonicalCreate {
				return injected
			}
			return nil
		}),
	)
	candidate := transactionPlan(t, "01a09000-0000-7002-8000-000000000002", "Linked prepared", now)
	result, err := linkedStore.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.linked-origin",
		Changes:   []record.TransactionChange{{Operation: record.TransactionCreate, Document: candidate}},
	})
	if !errors.Is(err, injected) || result == nil || len(result.Paths) != 1 || result.Paths[0].Published {
		t.Fatalf("linked prepared transaction = %#v, %v", result, err)
	}
	journalPath := filepath.Join(linkedStore.CoordinationDir(), "transactions-v2", result.TransactionID, "journal.toml")
	journal, err := os.ReadFile(journalPath)
	if err != nil || !strings.Contains(string(journal), "worktree_id = \"sha256:") {
		t.Fatalf("scoped journal = %q, %v", journal, err)
	}
	if strings.Contains(string(journal), linkedInfo.Repository.Root) || strings.Contains(string(journal), linkedInfo.Repository.GitDir) {
		t.Fatalf("journal leaked an absolute worktree path: %s", journal)
	}

	legacy := make([]string, 0)
	for _, line := range strings.Split(string(journal), "\n") {
		if !strings.HasPrefix(line, "worktree_id = ") {
			legacy = append(legacy, line)
		}
	}
	if err := os.WriteFile(journalPath, []byte(strings.Join(legacy, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	mainStore := record.NewStore(mainInfo.Root, mainInfo.Repository.GitCommonDir)
	if err := mainStore.Recover(context.Background()); !errors.Is(err, record.ErrUnsupportedTransaction) {
		t.Fatalf("legacy unscoped linked journal recovery = %v", err)
	}
	if err := os.WriteFile(journalPath, journal, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mainStore.Recover(context.Background()); err != nil {
		t.Fatalf("sibling recovery rejected scoped journal: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(mainInfo.Root, filepath.FromSlash(result.Paths[0].Path))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sibling recovery published linked transaction into main worktree: %v", err)
	}
	if _, err := mainStore.Inventory(context.Background()); err != nil {
		t.Fatalf("sibling prepared journal blocked main inventory: %v", err)
	}
	if _, err := linkedStore.Inventory(context.Background()); !errors.Is(err, record.ErrTransactionRecoveryRequired) {
		t.Fatalf("origin inventory did not require recovery: %v", err)
	}
	if err := record.NewStore(linkedInfo.Root, linkedInfo.Repository.GitCommonDir).Recover(context.Background()); err != nil {
		t.Fatalf("origin recovery: %v", err)
	}
	if _, err := os.Stat(filepath.Join(linkedInfo.Root, filepath.FromSlash(result.Paths[0].Path))); err != nil {
		t.Fatalf("origin recovery did not publish transaction: %v", err)
	}
}

func linkedPlanInput(title string) record.PlanInput {
	return record.PlanInput{
		Title:          title,
		Body:           "body\n",
		Priority:       research.PriorityP1,
		Effort:         research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Prove coordination", Metric: "score", Unit: "score"},
	}
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
