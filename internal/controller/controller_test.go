package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/operation"
)

const testCanonicalScope = "scope-test"

type fakeCanonical struct {
	pools      []Pool
	ready      map[string][]Selection
	prepared   []Selection
	submitted  []Selection
	reconciled int
}

func (value *fakeCanonical) Pools(context.Context) ([]Pool, error) { return value.pools, nil }
func (value *fakeCanonical) Next(_ context.Context, pool, lane string) (Selection, error) {
	items := value.ready[pool+":"+lane]
	if len(items) == 0 {
		return Selection{}, ErrNoWork
	}
	return items[0], nil
}
func (value *fakeCanonical) Prepare(_ context.Context, selection Selection) (Prepared, error) {
	key := selection.Pool + ":" + selection.Lane
	value.ready[key] = value.ready[key][1:]
	value.prepared = append(value.prepared, selection)
	payload, _ := json.Marshal(map[string]any{"selection": selection.ID})
	return Prepared{
		Job: operation.JobInput{ID: "job-" + selection.ID, IdempotencyKey: "job:" + selection.ID, Kind: "run", Role: "execute", CanonicalScope: testCanonicalScope,
			SubjectID: selection.ID, Pool: selection.Pool, Lane: selection.Lane, Units: selection.Units, Profile: "worker", Payload: payload, MaxAttempts: 1},
		Priority: 10, Worker: "/usr/local/bin/exp", Args: []string{"worker", "run", "--job", "job-" + selection.ID},
		CWD: "/tmp", Label: "exp:test:job-" + selection.ID,
	}, nil
}
func (value *fakeCanonical) Submitted(_ context.Context, selection Selection, _ operation.Job, _ int64) error {
	value.submitted = append(value.submitted, selection)
	return nil
}
func (value *fakeCanonical) Reconcile(context.Context, SchedulerSnapshot) error {
	value.reconciled++
	return nil
}

type fakeScheduler struct {
	tasks   []SchedulerTask
	submits []Dispatch
	nextID  int64
}

func (value *fakeScheduler) Snapshot(context.Context) (SchedulerSnapshot, error) {
	return SchedulerSnapshot{Tasks: append([]SchedulerTask(nil), value.tasks...)}, nil
}
func (value *fakeScheduler) Submit(_ context.Context, dispatch Dispatch) (int64, error) {
	value.nextID++
	value.submits = append(value.submits, dispatch)
	value.tasks = append(value.tasks, SchedulerTask{ID: value.nextID, Label: dispatch.Label, Group: dispatch.Group, State: "running"})
	return value.nextID, nil
}

func TestTickDispatchesOneReadyFrontierAndHonorsCapacity(t *testing.T) {
	store := testOperational(t)
	canonical := &fakeCanonical{
		pools: []Pool{{Name: "gpu0", NativeGroup: "exp-gpu0", LabelPrefix: "exp:test:", Capacity: 1, ExploitWeight: 80, ExploreWeight: 20}},
		ready: map[string][]Selection{"gpu0:exploit": {{ID: "plan-1", Weight: 2}}},
	}
	scheduler := &fakeScheduler{}
	controller := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon-a", Canonical: canonical, Operational: store, Scheduler: scheduler,
		NewID: func(prefix string) string { return prefix + "-1" }}
	result, err := controller.Tick(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Dispatched) != 1 || result.Dispatched[0] != "plan-1" || len(scheduler.submits) != 1 || len(canonical.submitted) != 1 {
		t.Fatalf("result=%#v submits=%#v canonical=%#v", result, scheduler.submits, canonical)
	}
	// The scheduler snapshot now occupies the only group slot; another tick
	// cannot dispatch even if more canonical work appears.
	canonical.ready["gpu0:exploit"] = []Selection{{ID: "plan-2"}}
	result, err = controller.Tick(t.Context())
	if err != nil || len(result.Dispatched) != 0 || len(scheduler.submits) != 1 {
		t.Fatalf("second tick=%#v submits=%d err=%v", result, len(scheduler.submits), err)
	}
}

func TestTickAccountsForMultiUnitResourceNeeds(t *testing.T) {
	store := testOperational(t)
	canonical := &fakeCanonical{
		pools: []Pool{{Name: "gpu0", NativeGroup: "gpu", LabelPrefix: "exp:test:", Capacity: 4, ExploitWeight: 80, ExploreWeight: 20}},
		ready: map[string][]Selection{"gpu0:exploit": {{ID: "wide-1", Units: 3, Weight: 6}, {ID: "wide-2", Units: 3, Weight: 6}}},
	}
	scheduler := &fakeScheduler{}
	controller := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon", Canonical: canonical, Operational: store, Scheduler: scheduler, NewID: func(prefix string) string { return prefix + "-units" }}
	result, err := controller.Tick(t.Context())
	if err != nil || len(result.Dispatched) != 1 || result.Dispatched[0] != "wide-1" || len(scheduler.submits) != 1 {
		t.Fatalf("result=%#v submits=%d err=%v", result, len(scheduler.submits), err)
	}
	jobs, err := store.ListJobs(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].Units != 3 {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
}

func TestTickReconcilesButDoesNotDispatchWhilePaused(t *testing.T) {
	store := testOperational(t)
	if _, err := store.SetPaused(t.Context(), true, "human pause"); err != nil {
		t.Fatal(err)
	}
	canonical := &fakeCanonical{pools: []Pool{{Name: "gpu0", NativeGroup: "gpu", Capacity: 1}}, ready: map[string][]Selection{"gpu0:exploit": {{ID: "plan-1"}}}}
	scheduler := &fakeScheduler{}
	controller := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon-a", Canonical: canonical, Operational: store, Scheduler: scheduler}
	result, err := controller.Tick(t.Context())
	if err != nil || !result.Paused || !result.Reconciled || len(scheduler.submits) != 0 || canonical.reconciled != 1 {
		t.Fatalf("result=%#v reconciled=%d err=%v", result, canonical.reconciled, err)
	}
}

func TestRecoverOutboxAttachesExistingTaskWithoutDuplicateSubmit(t *testing.T) {
	store := testOperational(t)
	canonical := &fakeCanonical{pools: []Pool{}, ready: map[string][]Selection{}}
	jobInput := operation.JobInput{ID: "job-1", IdempotencyKey: "job:1", Kind: "run", Role: "execute", SubjectID: "plan-1", CanonicalScope: testCanonicalScope, Pool: "gpu0", Lane: "exploit", Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	if _, _, err := store.EnqueueJob(t.Context(), jobInput); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimJob(t.Context(), "gpu0", "exploit", "old-daemon", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	prepared := Prepared{Job: jobInput, Priority: 1, Worker: "/usr/local/bin/exp", Args: []string{"worker", "run", "--job", "job-1"}, CWD: "/tmp", Label: "exp:test:job-1"}
	intent, _ := json.Marshal(dispatchIntent{Selection: Selection{ID: "plan-1", Pool: "gpu0", Lane: "exploit"}, Prepared: prepared,
		Dispatch: Dispatch{Group: "gpu", Label: prepared.Label, Worker: prepared.Worker, WorkerArgs: prepared.Args, WorkingDir: prepared.CWD}, FencingToken: job.FencingToken})
	if _, _, err := store.AddOutbox(t.Context(), operation.OutboxInput{ID: "out-1", OperationID: job.ID, Kind: "scheduler.submit", IdempotencyKey: "submit:1", Payload: intent}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	scheduler := &fakeScheduler{tasks: []SchedulerTask{{ID: 77, Label: prepared.Label, Group: "gpu", State: "running"}}}
	controller := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon-new", Canonical: canonical, Operational: store, Scheduler: scheduler}
	result, err := controller.Tick(t.Context())
	if err != nil || len(result.Recovered) != 1 || len(scheduler.submits) != 0 {
		t.Fatalf("result=%#v submits=%d err=%v", result, len(scheduler.submits), err)
	}
	loaded, err := store.GetJob(t.Context(), job.ID)
	if err != nil || loaded.PueueTaskID == nil || *loaded.PueueTaskID != 77 {
		t.Fatalf("job=%#v err=%v", loaded, err)
	}
}

func TestRecoverOutboxRequiresExactGroupAndLabel(t *testing.T) {
	store := testOperational(t)
	canonical := &fakeCanonical{pools: []Pool{{Name: "gpu0", NativeGroup: "right", LabelPrefix: "exp:test:", Capacity: 1}}, ready: map[string][]Selection{}}
	jobInput := operation.JobInput{ID: "job-route", IdempotencyKey: "job:route", Kind: "run", Role: "execute", SubjectID: "plan-route", CanonicalScope: testCanonicalScope, Pool: "gpu0", Lane: "exploit", Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	_, _, _, err := store.PrepareSubmission(t.Context(), jobInput, "old-daemon", time.Minute, func(claimed operation.Job) (operation.OutboxInput, error) {
		prepared := Prepared{Job: jobInput, Worker: "/usr/local/bin/exp", CWD: "/tmp", Label: "exp:test:route"}
		intent, _ := json.Marshal(dispatchIntent{Selection: Selection{ID: "plan-route", Pool: "gpu0", Lane: "exploit"}, Prepared: prepared,
			Dispatch: Dispatch{Group: "right", Label: prepared.Label, Worker: prepared.Worker, WorkingDir: prepared.CWD}, FencingToken: claimed.FencingToken})
		return operation.OutboxInput{ID: "out-route", OperationID: claimed.ID, Kind: "scheduler.submit", IdempotencyKey: "submit:" + claimed.ID, Payload: intent}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := &fakeScheduler{tasks: []SchedulerTask{{ID: 77, Label: "exp:test:route", Group: "wrong", State: "running"}}}
	control := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon-new", Canonical: canonical, Operational: store, Scheduler: scheduler}
	result, err := control.Tick(t.Context())
	if err != nil || len(result.Recovered) != 1 || len(scheduler.submits) != 1 || scheduler.submits[0].Group != "right" {
		t.Fatalf("result=%#v submits=%#v err=%v", result, scheduler.submits, err)
	}
}

func TestUnknownSchedulerTaskConsumesWholePoolAfterDatabaseLoss(t *testing.T) {
	store := testOperational(t)
	canonical := &fakeCanonical{
		pools: []Pool{{Name: "gpu0", NativeGroup: "gpu", LabelPrefix: "exp:test:", Capacity: 4, ExploitWeight: 1}},
		ready: map[string][]Selection{"gpu0:exploit": {{ID: "new", Units: 3}}},
	}
	scheduler := &fakeScheduler{tasks: []SchedulerTask{{ID: 99, Label: "exp:test:lost-db", Group: "gpu", State: "running"}}}
	control := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon", Canonical: canonical, Operational: store, Scheduler: scheduler}
	result, err := control.Tick(t.Context())
	if err != nil || len(result.Dispatched) != 0 || len(scheduler.submits) != 0 {
		t.Fatalf("result=%#v submits=%#v err=%v", result, scheduler.submits, err)
	}
}

func TestPausedControllerDoesNotSubmitPendingOutbox(t *testing.T) {
	store := testOperational(t)
	if _, err := store.SetPaused(t.Context(), true, "human pause"); err != nil {
		t.Fatal(err)
	}
	canonical := &fakeCanonical{pools: []Pool{}, ready: map[string][]Selection{}}
	jobInput := operation.JobInput{ID: "job-paused", IdempotencyKey: "job:paused", Kind: "run", Role: "execute", SubjectID: "plan-paused", CanonicalScope: testCanonicalScope, Pool: "gpu0", Lane: "exploit", Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	job, item, _, err := store.PrepareSubmission(t.Context(), jobInput, "old-daemon", time.Minute, func(claimed operation.Job) (operation.OutboxInput, error) {
		prepared := Prepared{Job: jobInput, Worker: "/usr/local/bin/exp", Args: []string{"worker", "run", "--job", claimed.ID}, CWD: "/tmp", Label: "exp:test:" + claimed.ID}
		intent, _ := json.Marshal(dispatchIntent{Selection: Selection{ID: "plan-paused", Pool: "gpu0", Lane: "exploit"}, Prepared: prepared,
			Dispatch: Dispatch{Group: "gpu", Label: prepared.Label, Worker: prepared.Worker, WorkerArgs: prepared.Args, WorkingDir: prepared.CWD}, FencingToken: claimed.FencingToken})
		return operation.OutboxInput{ID: "out-paused", OperationID: claimed.ID, Kind: "scheduler.submit", IdempotencyKey: "submit:" + claimed.ID, Payload: intent}, nil
	})
	if err != nil || job.State != operation.JobRunning || item.State != operation.OutboxPending {
		t.Fatalf("prepared job=%#v item=%#v err=%v", job, item, err)
	}
	scheduler := &fakeScheduler{}
	controller := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon-new", Canonical: canonical, Operational: store, Scheduler: scheduler}
	result, err := controller.Tick(t.Context())
	if err != nil || !result.Paused || len(scheduler.submits) != 0 {
		t.Fatalf("result=%#v submits=%d err=%v", result, len(scheduler.submits), err)
	}
	if due, err := store.DueOutbox(t.Context(), 10); err != nil || len(due) != 1 || due[0].ID != item.ID {
		t.Fatalf("paused outbox was consumed: due=%#v err=%v", due, err)
	}
}

func TestRecoveredSubmissionConsumesCapacityInSameTick(t *testing.T) {
	store := testOperational(t)
	canonical := &fakeCanonical{
		pools: []Pool{{Name: "gpu0", NativeGroup: "gpu", LabelPrefix: "exp:test:", Capacity: 1, ExploitWeight: 80, ExploreWeight: 20}},
		ready: map[string][]Selection{"gpu0:exploit": {{ID: "plan-new"}}},
	}
	jobInput := operation.JobInput{ID: "job-recover", IdempotencyKey: "job:recover", Kind: "run", Role: "execute", SubjectID: "plan-old", CanonicalScope: testCanonicalScope, Pool: "gpu0", Lane: "exploit", Profile: "worker", Payload: json.RawMessage(`{}`), MaxAttempts: 1}
	_, _, _, err := store.PrepareSubmission(t.Context(), jobInput, "old-daemon", time.Minute, func(claimed operation.Job) (operation.OutboxInput, error) {
		prepared := Prepared{Job: jobInput, Worker: "/usr/local/bin/exp", Args: []string{"worker", "run", "--job", claimed.ID}, CWD: "/tmp", Label: "exp:test:" + claimed.ID}
		intent, _ := json.Marshal(dispatchIntent{Selection: Selection{ID: "plan-old", Pool: "gpu0", Lane: "exploit"}, Prepared: prepared,
			Dispatch: Dispatch{Group: "gpu", Label: prepared.Label, Worker: prepared.Worker, WorkerArgs: prepared.Args, WorkingDir: prepared.CWD}, FencingToken: claimed.FencingToken})
		return operation.OutboxInput{ID: "out-recover", OperationID: claimed.ID, Kind: "scheduler.submit", IdempotencyKey: "submit:" + claimed.ID, Payload: intent}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := &fakeScheduler{}
	controller := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon-new", Canonical: canonical, Operational: store, Scheduler: scheduler}
	result, err := controller.Tick(t.Context())
	if err != nil || len(result.Recovered) != 1 || len(result.Dispatched) != 0 || len(scheduler.submits) != 1 {
		t.Fatalf("result=%#v submits=%d err=%v", result, len(scheduler.submits), err)
	}
}

func TestControllerRequiresSingleLeader(t *testing.T) {
	store := testOperational(t)
	lease, err := store.AcquireLease(t.Context(), "daemon:test", "other", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.ReleaseLease(context.Background(), lease) })
	controller := Controller{ProjectID: "test", Scope: testCanonicalScope, Holder: "daemon", Canonical: &fakeCanonical{}, Operational: store, Scheduler: &fakeScheduler{}}
	if _, err := controller.Tick(t.Context()); !errors.Is(err, operation.ErrLeaseHeld) {
		t.Fatalf("leader error = %v", err)
	}
}

func testOperational(t *testing.T) *operation.Store {
	t.Helper()
	gitCommon := filepath.Join(t.TempDir(), ".git")
	if err := os.Mkdir(gitCommon, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := operation.Open(t.Context(), gitCommon)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
