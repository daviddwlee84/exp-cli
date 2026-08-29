package record_test

import (
	"context"
	"os/exec"
	"path/filepath"
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
