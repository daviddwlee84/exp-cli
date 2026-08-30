//go:build !aix

package operation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStoreOperationsAreIdempotent(t *testing.T) {
	clock := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	store := openTestStore(t, &clock)
	input := OperationInput{
		ID: "op-1", Kind: "queue.insert", SubjectID: "plan-1", IdempotencyKey: "queue.insert:plan-1:v1",
		SnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Payload:        json.RawMessage(`{"plan":"plan-1"}`),
	}
	created, duplicate, err := store.BeginOperation(t.Context(), input)
	if err != nil || duplicate || created.State != OperationPending {
		t.Fatalf("first operation = %#v duplicate=%t err=%v", created, duplicate, err)
	}
	again, duplicate, err := store.BeginOperation(t.Context(), OperationInput{
		ID: "other-id", Kind: "ignored", SubjectID: "ignored", IdempotencyKey: input.IdempotencyKey,
		Payload: json.RawMessage(`{"different":true}`),
	})
	if !errors.Is(err, ErrConflict) || !duplicate || again.ID != created.ID || string(again.Payload) != string(created.Payload) {
		t.Fatalf("duplicate operation = %#v duplicate=%t err=%v", again, duplicate, err)
	}
	again, duplicate, err = store.BeginOperation(t.Context(), created.OperationInput)
	if err != nil || !duplicate || again.ID != created.ID {
		t.Fatalf("exact operation replay = %#v duplicate=%t err=%v", again, duplicate, err)
	}
	finished, err := store.SetOperationState(t.Context(), created.ID, OperationSucceeded, json.RawMessage(`{"ok":true}`), "")
	if err != nil || finished.State != OperationSucceeded || string(finished.Result) != `{"ok":true}` {
		t.Fatalf("finished operation = %#v err=%v", finished, err)
	}
	loaded, err := store.GetOperationByKey(t.Context(), input.IdempotencyKey)
	if err != nil || loaded.State != OperationSucceeded {
		t.Fatalf("loaded operation = %#v err=%v", loaded, err)
	}
}

func TestLeaseFencing(t *testing.T) {
	clock := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	store := openTestStore(t, &clock)
	first, err := store.AcquireLease(t.Context(), "queue:gpu", "daemon-a", time.Minute)
	if err != nil || first.FencingToken != 1 {
		t.Fatalf("first lease = %#v err=%v", first, err)
	}
	if _, err := store.AcquireLease(t.Context(), "queue:gpu", "daemon-b", time.Minute); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("competing lease error = %v", err)
	}
	clock = clock.Add(2 * time.Minute)
	second, err := store.AcquireLease(t.Context(), "queue:gpu", "daemon-b", time.Minute)
	if err != nil || second.FencingToken != 2 {
		t.Fatalf("second lease = %#v err=%v", second, err)
	}
	if _, err := store.RenewLease(t.Context(), first, time.Minute); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale renewal error = %v", err)
	}
	if err := store.ReleaseLease(t.Context(), second); err != nil {
		t.Fatal(err)
	}
}

func TestJobsOutboxAndFairness(t *testing.T) {
	clock := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	store := openTestStore(t, &clock)
	job, duplicate, err := store.EnqueueJob(t.Context(), JobInput{
		ID: "job-1", IdempotencyKey: "implement:exp-1", Kind: "agent", Role: "implement", SubjectID: "exp-1",
		Pool: "agents", Lane: "exploit", Profile: "test", Payload: json.RawMessage(`{"prompt":"safe"}`), MaxAttempts: 2,
	})
	if err != nil || duplicate || job.State != JobQueued {
		t.Fatalf("job = %#v duplicate=%t err=%v", job, duplicate, err)
	}
	_, duplicate, err = store.EnqueueJob(t.Context(), JobInput{
		ID: "job-2", IdempotencyKey: "implement:exp-1", Kind: "agent", Role: "implement", SubjectID: "exp-1",
		Pool: "agents", Lane: "exploit", Profile: "test", Payload: json.RawMessage(`{}`),
	})
	if !duplicate || !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting duplicate job duplicate=%t err=%v", duplicate, err)
	}
	_, duplicate, err = store.EnqueueJob(t.Context(), job.JobInput)
	if err != nil || !duplicate {
		t.Fatalf("exact duplicate job duplicate=%t err=%v", duplicate, err)
	}
	claimed, err := store.ClaimJob(t.Context(), "agents", "exploit", "daemon-a", time.Minute)
	if err != nil || claimed.State != JobRunning || claimed.FencingToken != 1 || claimed.AttemptCount != 1 {
		t.Fatalf("claimed = %#v err=%v", claimed, err)
	}
	taskID := int64(42)
	if err := store.SetJobExternalRefs(t.Context(), claimed.ID, claimed.FencingToken, &taskID, "run-abc"); err != nil {
		t.Fatal(err)
	}
	finished, err := store.FinishJob(t.Context(), claimed.ID, claimed.FencingToken, JobSucceeded, json.RawMessage(`{"commit":"abc"}`), "")
	if err != nil || finished.State != JobSucceeded || finished.PueueTaskID == nil || *finished.PueueTaskID != 42 {
		t.Fatalf("finished = %#v err=%v", finished, err)
	}

	item, duplicate, err := store.AddOutbox(t.Context(), OutboxInput{
		ID: "out-1", OperationID: "op-1", Kind: "pueue.submit", IdempotencyKey: "pueue:att-1", Payload: json.RawMessage(`{"attempt":"att-1"}`),
	}, time.Time{})
	if err != nil || duplicate || item.State != OutboxPending {
		t.Fatalf("outbox = %#v duplicate=%t err=%v", item, duplicate, err)
	}
	due, err := store.DueOutbox(t.Context(), 10)
	if err != nil || len(due) != 1 || due[0].ID != item.ID {
		t.Fatalf("due = %#v err=%v", due, err)
	}
	if err := store.SetOutboxState(t.Context(), item.ID, OutboxSucceeded, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}

	if lane, ok := ChooseLane(Fairness{}, true, true, 80, 20); !ok || lane != "exploit" {
		t.Fatalf("initial lane = %q, %t", lane, ok)
	}
	fairness, err := store.RecordDispatch(t.Context(), "gpu", "exploit", 4)
	if err != nil {
		t.Fatal(err)
	}
	if lane, _ := ChooseLane(fairness, true, true, 80, 20); lane != "explore" {
		t.Fatalf("after exploit service lane = %q, fairness=%#v", lane, fairness)
	}
}

func TestPrepareSubmissionClaimsExactJobAndPublishesOutboxAtomically(t *testing.T) {
	clock := time.Date(2026, 8, 30, 10, 30, 0, 0, time.UTC)
	store := openTestStore(t, &clock)
	older := JobInput{ID: "job-older", IdempotencyKey: "older", Kind: "run", Role: "execute", SubjectID: "older", Pool: "gpu", Lane: "exploit", Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	if _, _, err := store.EnqueueJob(t.Context(), older); err != nil {
		t.Fatal(err)
	}
	target := JobInput{ID: "job-target", IdempotencyKey: "target", Kind: "run", Role: "execute", SubjectID: "target", Pool: "gpu", Lane: "exploit", Profile: "worker", Payload: json.RawMessage(`{"safe":true}`), MaxAttempts: 1}
	job, item, duplicate, err := store.PrepareSubmission(t.Context(), target, "daemon", time.Minute, func(claimed Job) (OutboxInput, error) {
		if claimed.ID != target.ID || claimed.FencingToken != 1 {
			t.Fatalf("factory received %#v", claimed)
		}
		payload, _ := json.Marshal(map[string]any{"job": claimed.ID, "token": claimed.FencingToken})
		return OutboxInput{ID: "out-target", OperationID: claimed.ID, Kind: "scheduler.submit", IdempotencyKey: "submit:" + claimed.ID, Payload: payload}, nil
	})
	if err != nil || duplicate || job.ID != target.ID || job.State != JobRunning || item.OperationID != target.ID {
		t.Fatalf("job=%#v item=%#v duplicate=%t err=%v", job, item, duplicate, err)
	}
	olderLoaded, err := store.GetJob(t.Context(), older.ID)
	if err != nil || olderLoaded.State != JobQueued {
		t.Fatalf("older job=%#v err=%v", olderLoaded, err)
	}
	if due, err := store.DueOutbox(t.Context(), 10); err != nil || len(due) != 1 || due[0].ID != item.ID {
		t.Fatalf("due=%#v err=%v", due, err)
	}

	failed := JobInput{ID: "job-rollback", IdempotencyKey: "rollback", Kind: "run", Role: "execute", SubjectID: "rollback", Pool: "gpu", Lane: "exploit", Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	if _, _, _, err := store.PrepareSubmission(t.Context(), failed, "daemon", time.Minute, func(Job) (OutboxInput, error) {
		return OutboxInput{}, errors.New("factory failed")
	}); err == nil {
		t.Fatal("expected factory failure")
	}
	if _, err := store.GetJob(t.Context(), failed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled back job error=%v", err)
	}
}

func TestDispatchLeaseFencesExpiredLeaderAndHonorsPause(t *testing.T) {
	clock := time.Date(2026, 8, 30, 10, 45, 0, 0, time.UTC)
	store := openTestStore(t, &clock)
	lease, err := store.AcquireLease(t.Context(), "daemon:test", "leader", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	taskID, renewed, err := store.WithDispatchLease(t.Context(), lease, time.Minute, func() (int64, error) {
		called++
		return 42, nil
	})
	if err != nil || taskID != 42 || called != 1 || !renewed.ExpiresAt.After(lease.ExpiresAt) && !renewed.ExpiresAt.Equal(lease.ExpiresAt) {
		t.Fatalf("task=%d renewed=%#v called=%d err=%v", taskID, renewed, called, err)
	}
	if _, err := store.SetPaused(t.Context(), true, "maintenance"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.WithDispatchLease(t.Context(), renewed, time.Minute, func() (int64, error) {
		called++
		return 43, nil
	}); !errors.Is(err, ErrPaused) || called != 1 {
		t.Fatalf("paused dispatch called=%d err=%v", called, err)
	}
	if _, err := store.SetPaused(t.Context(), false, ""); err != nil {
		t.Fatal(err)
	}
	clock = renewed.ExpiresAt.Add(time.Second)
	if _, _, err := store.WithDispatchLease(t.Context(), renewed, time.Minute, func() (int64, error) {
		called++
		return 44, nil
	}); !errors.Is(err, ErrFenced) || called != 1 {
		t.Fatalf("expired dispatch called=%d err=%v", called, err)
	}
}

func TestTerminalReconciliationAndActiveAllocationProjections(t *testing.T) {
	clock := time.Date(2026, 8, 30, 10, 50, 0, 0, time.UTC)
	store := openTestStore(t, &clock)
	terminalInput := JobInput{ID: "job-terminal", IdempotencyKey: "terminal", Kind: "experiment.run", Role: "execute", SubjectID: "att-terminal", CanonicalScope: "scope-a", Pool: "gpu", Lane: "exploit", Units: 2, Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	terminal, _, err := store.EnqueueJob(t.Context(), terminalInput)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err = store.ClaimJobByID(t.Context(), terminal.ID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishJob(t.Context(), terminal.ID, terminal.FencingToken, JobSucceeded, json.RawMessage(`{"ok":true}`), ""); err != nil {
		t.Fatal(err)
	}
	if jobs, err := store.ListUnreconciledTerminalJobs(t.Context(), "scope-b", 10); err != nil || len(jobs) != 0 {
		t.Fatalf("wrong-scope jobs=%#v err=%v", jobs, err)
	}
	jobs, err := store.ListUnreconciledTerminalJobs(t.Context(), "scope-a", 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != terminal.ID {
		t.Fatalf("terminal jobs=%#v err=%v", jobs, err)
	}
	if err := store.MarkJobReconciled(t.Context(), terminal.ID, terminal.FencingToken); err != nil {
		t.Fatal(err)
	}
	if jobs, err := store.ListUnreconciledTerminalJobs(t.Context(), "scope-a", 10); err != nil || len(jobs) != 0 {
		t.Fatalf("reconciled jobs=%#v err=%v", jobs, err)
	}

	first := JobInput{ID: "job-active-a", IdempotencyKey: "active-a", Kind: "run", Role: "execute", SubjectID: "a", Pool: "gpu", Lane: "exploit", Units: 4, Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	second := JobInput{ID: "job-active-b", IdempotencyKey: "active-b", Kind: "run", Role: "execute", SubjectID: "b", Pool: "gpu", Lane: "exploit", Units: 1, Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	for _, input := range []JobInput{first, second} {
		job, _, enqueueErr := store.EnqueueJob(t.Context(), input)
		if enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
		job, enqueueErr = store.ClaimJobByID(t.Context(), job.ID, "worker", time.Minute)
		if enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
		taskID := int64(91)
		if input.ID == second.ID {
			taskID = 91
		}
		setErr := store.SetJobExternalRefs(t.Context(), job.ID, job.FencingToken, &taskID, "")
		if input.ID == second.ID {
			if setErr == nil {
				t.Fatal("duplicate Pueue task identity was accepted")
			}
		} else if setErr != nil {
			t.Fatal(setErr)
		}
	}
	allocations, err := store.ListActiveAllocations(t.Context())
	if err != nil || len(allocations) != 1 || allocations[0].Units != 4 || allocations[0].PueueTaskID != 91 {
		t.Fatalf("allocations=%#v err=%v", allocations, err)
	}
	firstFairness, err := store.RecordDispatchOnce(t.Context(), first.ID, "gpu", "exploit", 4)
	if err != nil || firstFairness.ExploitUnits != 4 {
		t.Fatalf("first fairness=%#v err=%v", firstFairness, err)
	}
	secondFairness, err := store.RecordDispatchOnce(t.Context(), first.ID, "gpu", "exploit", 4)
	if err != nil || secondFairness.ExploitUnits != 4 {
		t.Fatalf("idempotent fairness=%#v err=%v", secondFairness, err)
	}
}

func TestOperationalPathPermissionsAndRemoveGuard(t *testing.T) {
	clock := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	store := openTestStore(t, &clock)
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %o", info.Mode().Perm())
	}
	if _, _, err := store.EnqueueJob(t.Context(), JobInput{
		ID: "job-active", IdempotencyKey: "active", Kind: "run", Role: "execute", SubjectID: "run-1",
		Pool: "gpu", Lane: "explore", Profile: "worker", Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveIfEmpty(); !errors.Is(err, ErrConflict) {
		t.Fatalf("remove active store error = %v", err)
	}
}

func TestOpenRejectsOperationalDatabaseSymlink(t *testing.T) {
	gitCommon := filepath.Join(t.TempDir(), ".git")
	directory := filepath.Join(gitCommon, "exp", "runtime", "v1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "control.sqlite")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Open(t.Context(), gitCommon); err == nil {
		t.Fatal("operational database symlink was accepted")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "unchanged" {
		t.Fatalf("outside target changed: %q err=%v", content, err)
	}
}

func TestOpenProtectsPreexistingOperationalDirectoryChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not authoritative on Windows")
	}
	gitCommon := filepath.Join(t.TempDir(), ".git")
	leaf := filepath.Join(gitCommon, "exp", "runtime", "v1")
	if err := os.MkdirAll(leaf, 0o777); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Join(gitCommon, "exp"), filepath.Join(gitCommon, "exp", "runtime"), leaf} {
		if err := os.Chmod(directory, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(t.Context(), gitCommon)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, directory := range []string{filepath.Join(gitCommon, "exp"), filepath.Join(gitCommon, "exp", "runtime"), leaf} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("directory %s mode=%v", directory, info.Mode().Perm())
		}
	}
}

func TestNormalizeJSONRejectsTrailingValues(t *testing.T) {
	if _, err := normalizeJSON(json.RawMessage(`{} {}`)); err == nil {
		t.Fatal("expected trailing JSON to fail")
	}
}

func TestSchemaV1DatabaseMigratesRuntimeStateToV2(t *testing.T) {
	gitCommon := filepath.Join(t.TempDir(), ".git")
	if err := os.Mkdir(gitCommon, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(gitCommon, filepath.FromSlash(operationalRelative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE schema_meta (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL); INSERT INTO schema_meta(id,version) VALUES(1,1)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range schemaV1 {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(t.Context(), gitCommon)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err := store.RuntimeState(t.Context())
	if err != nil || state.Paused {
		t.Fatalf("runtime state=%#v err=%v", state, err)
	}
	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
}

func TestSchemaV4DatabaseMigratesFairnessAccountingToV5(t *testing.T) {
	gitCommon := filepath.Join(t.TempDir(), ".git")
	if err := os.MkdirAll(filepath.Join(gitCommon, "exp", "runtime", "v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(gitCommon, filepath.FromSlash(operationalRelative))
	database, err := sql.Open("sqlite3", sqliteDSN(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE schema_meta (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL); INSERT INTO schema_meta(id,version) VALUES(1,4)`); err != nil {
		t.Fatal(err)
	}
	for _, statements := range [][]string{schemaV1, schemaV2, schemaV3, schemaV4} {
		for _, statement := range statements {
			if _, err := database.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(t.Context(), gitCommon)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	job, _, err := store.EnqueueJob(t.Context(), JobInput{ID: "job-v5", IdempotencyKey: "v5", Kind: "run", Role: "execute", SubjectID: "subject", Pool: "gpu", Lane: "exploit", Units: 1, Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordDispatchOnce(t.Context(), job.ID, "gpu", "exploit", 1); err != nil {
		t.Fatal(err)
	}
}

func openTestStore(t *testing.T, clock *time.Time) *Store {
	t.Helper()
	gitCommon := filepath.Join(t.TempDir(), ".git")
	if err := os.Mkdir(gitCommon, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := Open(context.Background(), gitCommon, WithClock(func() time.Time { return *clock }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
