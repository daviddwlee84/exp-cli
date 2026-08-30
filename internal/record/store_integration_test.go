package record_test

import (
	"context"
	"errors"
	"os"
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

func TestPlanCreateCollisionListReadAndRevisionUpdate(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 29, 11, 0, 0, 0, time.UTC)
	firstUUID := uuid.MustParse("01a01e70-0000-7101-8000-000000000101")
	secondUUID := uuid.MustParse("01a01e71-0000-7202-8000-000000000202")
	generator, calls := uuidSequence(t, firstUUID, firstUUID, secondUUID)
	store := record.NewStore(info.Root, info.Repository.GitCommonDir, record.WithClock(func() time.Time { return now }), record.WithUUIDGenerator(generator))

	first, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title:          "Calibrate encoder learning rate",
		Body:           "\n# Plan rationale\n\nPreserve Unicode: 学習率",
		Priority:       research.PriorityP3,
		Effort:         research.EffortM,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve calibration", Metric: "macro_f1", Unit: "score"},
		Tags:           []string{"optimization", "encoder"},
	})
	if err != nil {
		t.Fatalf("CreatePlan first: %v", err)
	}
	if !record.ValidRevision(first.Revision) || first.Record.(*research.Plan).State != research.PlanQueued {
		t.Fatalf("created Plan = %#v", first)
	}
	if first.Body[len(first.Body)-1] != '\n' {
		t.Fatalf("normalized body lacks final LF: %q", first.Body)
	}
	if got := filepath.ToSlash(first.Path); got != "plans/plan_01a01e70-0000-7101-8000-000000000101-calibrate-encoder-learning-rate.md" {
		t.Fatalf("first path = %q", got)
	}
	assertStoreMode(t, filepath.Join(info.Root, filepath.FromSlash(first.Path)), 0o644)

	second, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title:          "Measure memory tradeoff",
		Body:           "body\n",
		Priority:       research.PriorityP1,
		Effort:         research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Reduce memory", Metric: "peak_memory", Unit: "MiB"},
	})
	if err != nil {
		t.Fatalf("CreatePlan collision retry: %v", err)
	}
	if *calls != 3 || second.Record.(*research.Plan).ID.UUID() != secondUUID {
		t.Fatalf("collision calls=%d second=%s", *calls, second.Record.(*research.Plan).ID)
	}

	plans, diagnostics, err := store.ListPlans(context.Background())
	if err != nil || len(diagnostics) != 0 || len(plans) != 2 {
		t.Fatalf("ListPlans = %d, %v, %v", len(plans), diagnostics, err)
	}
	if plans[0].Record.(*research.Plan).Priority != research.PriorityP1 {
		t.Fatalf("Plans are not priority-sorted: %#v", plans)
	}
	code, err := research.DisplayCode(first.Record.(*research.Plan).ID, []research.ReferenceCandidate{{ID: first.Record.(*research.Plan).ID}, {ID: second.Record.(*research.Plan).ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{first.Record.(*research.Plan).ID.String(), "plan_01a01e70", code} {
		read, gotDiagnostics, err := store.ReadPlan(context.Background(), reference)
		if err != nil || len(gotDiagnostics) != 0 || read.Revision != first.Revision {
			t.Fatalf("ReadPlan(%q) = %#v, %v, %v", reference, read, gotDiagnostics, err)
		}
	}

	replacement := first.Clone()
	replacement.Record.(*research.Plan).Title = "Renamed navigation title"
	replacement.Record.(*research.Plan).UpdatedAt = now.Add(time.Minute)
	replacement.Body = "updated rationale\n"
	updated, err := store.Update(context.Background(), replacement, first.Revision)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Revision == first.Revision || updated.Path != first.Path {
		t.Fatalf("update revision/path = %s, %q", updated.Revision, updated.Path)
	}
	if _, err := store.Update(context.Background(), replacement, first.Revision); !errors.Is(err, record.ErrConflict) {
		t.Fatalf("stale Update = %v", err)
	}
	read, _, err := store.ReadPlan(context.Background(), updated.Record.(*research.Plan).ID.String())
	if err != nil || read.Revision != updated.Revision || read.Record.(*research.Plan).Title != "Renamed navigation title" {
		t.Fatalf("read updated Plan = %#v, %v", read, err)
	}
}

func TestPlanCreationRejectsBrokenRelationsAndUnknownJournalArtifacts(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	generator, _ := uuidSequence(t,
		uuid.MustParse("01a01e72-0000-7101-8000-000000000101"),
		uuid.MustParse("01a01e73-0000-7202-8000-000000000202"),
	)
	store := record.NewStore(info.Root, info.Repository.GitCommonDir, record.WithClock(func() time.Time { return now }), record.WithUUIDGenerator(generator))
	missing, err := research.ParseID("fnd_01a01e9c-fd00-7606-8000-000000009999")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Broken assumption", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "No", Metric: "score", Unit: "score"}, Assumptions: []research.ID{missing},
	})
	if !errors.Is(err, record.ErrInvalidInventory) {
		t.Fatalf("broken relation error = %v", err)
	}
	plans, _, listErr := store.ListPlans(context.Background())
	if listErr != nil || len(plans) != 0 {
		t.Fatalf("broken Plan was published: %d, %v", len(plans), listErr)
	}

	journalArtifact := filepath.Join(store.CoordinationDir(), "transactions", "future-v2")
	if err := os.MkdirAll(journalArtifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, inventoryErr := store.Inventory(context.Background()); !errors.Is(inventoryErr, record.ErrUnsupportedTransaction) {
		t.Fatalf("read-only inventory accepted unknown transaction state: %v", inventoryErr)
	}
	_, err = store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Blocked mutation", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "No", Metric: "score", Unit: "score"},
	})
	if !errors.Is(err, record.ErrUnsupportedTransaction) {
		t.Fatalf("journal artifact error = %v", err)
	}
}

func TestStoreInventoryDoesNotCreateMissingCoordinationState(t *testing.T) {
	info := initializeStoreProject(t)
	store := record.NewStore(info.Root, info.Repository.GitCommonDir)
	if err := os.RemoveAll(store.CoordinationDir()); err != nil {
		t.Fatal(err)
	}
	inventory, err := store.Inventory(context.Background())
	if err != nil || !inventory.Valid() {
		t.Fatalf("Inventory = %#v, %v", inventory, err)
	}
	if _, err := os.Lstat(store.CoordinationDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only inventory created coordination state: %v", err)
	}
}

func TestStoreIgnoresOwnedTempsOnReadAndCleansThemOnlyUnderMutationLock(t *testing.T) {
	info := initializeStoreProject(t)
	temporary := filepath.Join(info.Root, record.PlansDir, ".exp-0123456789abcdef0123456789abcdef.tmp")
	if err := os.WriteFile(temporary, []byte("partial canonical bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e74-0000-7303-8000-000000000303"), nil
		}),
	)
	inventory, err := store.Inventory(context.Background())
	if err != nil || !inventory.Valid() {
		t.Fatalf("read with owned temporary = %#v, %v", inventory, err)
	}
	if _, err := os.Lstat(temporary); err != nil {
		t.Fatalf("read-only inventory removed temporary: %v", err)
	}
	if _, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Cleanup recovery", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Recover", Metric: "score", Unit: "score"},
	}); err != nil {
		t.Fatalf("mutation after abandoned temporary: %v", err)
	}
	if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mutation did not clean owned temporary: %v", err)
	}
}

func TestStoreRefusesSymlinkMasqueradingAsOwnedTemporary(t *testing.T) {
	info := initializeStoreProject(t)
	outside := filepath.Join(t.TempDir(), "outside")
	original := []byte("preserve me\n")
	if err := os.WriteFile(outside, original, 0o600); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(info.Root, record.PlansDir, ".exp-0123456789abcdef0123456789abcdef.tmp")
	if err := os.Symlink(outside, temporary); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e75-0000-7404-8000-000000000404"), nil
		}),
	)
	inventory, err := store.Inventory(context.Background())
	if err != nil || inventory.Valid() {
		t.Fatalf("symlink temporary inventory = %#v, %v", inventory, err)
	}
	if _, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Must not clean symlink", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Preserve", Metric: "score", Unit: "score"},
	}); err == nil {
		t.Fatal("mutation accepted symlink temporary")
	}
	content, readErr := os.ReadFile(outside)
	if readErr != nil || string(content) != string(original) {
		t.Fatalf("symlink target changed: %q, %v", content, readErr)
	}
	if info, statErr := os.Lstat(temporary); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink temporary was removed: %v, %v", info, statErr)
	}
}

func TestStoreRejectsSymlinkedLocalStateSubdirectory(t *testing.T) {
	info := initializeStoreProject(t)
	transactions := filepath.Join(info.Repository.CoordinationDir(), "transactions")
	if err := os.Remove(transactions); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, transactions); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e74-0000-7303-8000-000000000303"), nil
		}),
	)
	_, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Blocked local state", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "No", Metric: "score", Unit: "score"},
	})
	if err == nil {
		t.Fatal("symlinked transactions directory was accepted")
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("store wrote through local-state symlink: %v, %v", entries, readErr)
	}
}

func initializeStoreProject(t *testing.T) *project.Info {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--quiet")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	projectID := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	info, _, err := project.Initialize(context.Background(), project.InitRequest{StartDir: root, Name: "Store Test"}, project.WithClock(func() time.Time {
		return time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	}), project.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return projectID, nil }))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func uuidSequence(t *testing.T, values ...uuid.UUID) (research.UUIDGenerator, *int) {
	t.Helper()
	var mutex sync.Mutex
	calls := 0
	generator := research.UUIDGenerator(func(time.Time) (uuid.UUID, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if calls >= len(values) {
			t.Fatalf("UUID generator called %d times; only %d values", calls+1, len(values))
		}
		value := values[calls]
		calls++
		return value, nil
	})
	return generator, &calls
}

func assertStoreMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
