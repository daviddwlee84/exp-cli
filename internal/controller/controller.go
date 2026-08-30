// Package controller coordinates canonical queue selections with private jobs
// and an external scheduler. It is deliberately interface-driven: research
// queue membership/order remains canonical, while this loop owns only live
// admission, idempotent dispatch, and reconciliation.
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/worker"
)

var ErrNoWork = errors.New("no ready canonical work")

type Pool struct {
	Name          string
	NativeGroup   string
	LabelPrefix   string
	Capacity      int
	ExploitWeight float64
	ExploreWeight float64
}

type Selection struct {
	ID     string
	Pool   string
	Lane   string
	Weight float64
	Units  int
}

type Prepared struct {
	Job        operation.JobInput
	Priority   int
	Worker     string
	Args       []string
	CWD        string
	Label      string
	Units      int
	AllowedEnv []string
}

type Canonical interface {
	Pools(context.Context) ([]Pool, error)
	Next(context.Context, string, string) (Selection, error)
	Prepare(context.Context, Selection) (Prepared, error)
	Submitted(context.Context, Selection, operation.Job, int64) error
	Reconcile(context.Context, SchedulerSnapshot) error
}

type SchedulerTask struct {
	ID    int64
	Label string
	Group string
	State string
}

type SchedulerSnapshot struct {
	Tasks []SchedulerTask
	Jobs  []operation.Job
}

type Dispatch struct {
	Group      string
	Label      string
	Priority   int
	WorkingDir string
	Worker     string
	WorkerArgs []string
	AllowedEnv []string
}

type Scheduler interface {
	Snapshot(context.Context) (SchedulerSnapshot, error)
	Submit(context.Context, Dispatch) (int64, error)
}

type Operational interface {
	RuntimeState(context.Context) (operation.RuntimeState, error)
	AcquireLease(context.Context, string, string, time.Duration) (operation.Lease, error)
	RenewLease(context.Context, operation.Lease, time.Duration) (operation.Lease, error)
	ReleaseLease(context.Context, operation.Lease) error
	EnqueueJob(context.Context, operation.JobInput) (operation.Job, bool, error)
	GetJob(context.Context, string) (operation.Job, error)
	ClaimJob(context.Context, string, string, string, time.Duration) (operation.Job, error)
	SetJobExternalRefs(context.Context, string, int64, *int64, string) error
	AddOutbox(context.Context, operation.OutboxInput, time.Time) (operation.OutboxItem, bool, error)
	DueOutbox(context.Context, int) ([]operation.OutboxItem, error)
	SetOutboxState(context.Context, string, operation.OutboxState, time.Time, string) error
	Fairness(context.Context, string) (operation.Fairness, error)
	RecordDispatch(context.Context, string, string, float64) (operation.Fairness, error)
}

type atomicSubmissionOperational interface {
	PrepareSubmission(context.Context, operation.JobInput, string, time.Duration, operation.OutboxFactory) (operation.Job, operation.OutboxItem, bool, error)
}

type exactClaimOperational interface {
	ClaimJobByID(context.Context, string, string, time.Duration) (operation.Job, error)
}

type dispatchLeaseOperational interface {
	WithDispatchLease(context.Context, operation.Lease, time.Duration, operation.DispatchAction) (int64, operation.Lease, error)
}

type terminalReconciliationOperational interface {
	ListUnreconciledTerminalJobs(context.Context, string, int) ([]operation.Job, error)
	MarkJobReconciled(context.Context, string, int64) error
}

type acknowledgedCanonical interface {
	ReconcileAcknowledged(context.Context, SchedulerSnapshot) ([]string, error)
}

type activeAllocationOperational interface {
	ListActiveAllocations(context.Context) ([]operation.ActiveAllocation, error)
}

type scopedOutboxOperational interface {
	DueOutboxForScope(context.Context, string, int) ([]operation.OutboxItem, error)
}

type exactFairnessOperational interface {
	RecordDispatchOnce(context.Context, string, string, string, float64) (operation.Fairness, error)
}

type workerRecoveryOperational interface {
	FinishJob(context.Context, string, int64, operation.JobState, json.RawMessage, string) (operation.Job, error)
}

type Controller struct {
	ProjectID   string
	Scope       string
	MarkerRoot  string
	Holder      string
	Canonical   Canonical
	Operational Operational
	Scheduler   Scheduler
	Clock       func() time.Time
	LeaseTTL    time.Duration
	PollEvery   time.Duration
	NewID       func(string) string
}

type TickResult struct {
	Paused       bool     `json:"paused"`
	Reconciled   bool     `json:"reconciled"`
	Dispatched   []string `json:"dispatched"`
	Recovered    []string `json:"recovered"`
	BorrowedLane []string `json:"borrowed_lane"`
}

func (controller Controller) Tick(ctx context.Context) (TickResult, error) {
	if controller.Canonical == nil || controller.Operational == nil || controller.Scheduler == nil {
		return TickResult{}, errors.New("controller dependencies are incomplete")
	}
	if controller.ProjectID == "" || controller.Scope == "" || controller.Holder == "" {
		return TickResult{}, errors.New("controller project, canonical scope, and holder are required")
	}
	clock := controller.Clock
	if clock == nil {
		clock = time.Now
	}
	ttl := controller.LeaseTTL
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	lease, err := controller.Operational.AcquireLease(ctx, "daemon:"+controller.ProjectID, controller.Holder, ttl)
	if err != nil {
		return TickResult{}, err
	}
	defer controller.Operational.ReleaseLease(context.Background(), lease)

	snapshot, err := controller.Scheduler.Snapshot(ctx)
	if err != nil {
		return TickResult{}, fmt.Errorf("scheduler snapshot: %w", err)
	}
	if controller.MarkerRoot != "" {
		if lister, ok := controller.Operational.(jobLister); ok {
			if recoveryStore, recoverable := controller.Operational.(workerRecoveryOperational); recoverable {
				running, listErr := lister.ListJobs(ctx, operation.JobRunning)
				if listErr != nil {
					return TickResult{}, listErr
				}
				tasksByID := make(map[int64]SchedulerTask, len(snapshot.Tasks))
				for _, task := range snapshot.Tasks {
					tasksByID[task.ID] = task
				}
				for _, job := range running {
					if _, found, loadErr := worker.LoadTerminal(ctx, controller.MarkerRoot, job.ID); loadErr != nil {
						return TickResult{}, loadErr
					} else if found {
						if _, replayErr := (worker.Runner{Store: recoveryStore, MarkerRoot: controller.MarkerRoot, Clock: clock}).Run(ctx, job); replayErr != nil {
							return TickResult{}, fmt.Errorf("replay durable worker terminal for %s: %w", job.ID, replayErr)
						}
					} else if job.PueueTaskID != nil {
						if task, observed := tasksByID[*job.PueueTaskID]; observed && schedulerTerminal(task.State) {
							payload, _ := json.Marshal(map[string]string{"scheduler_state": task.State})
							if _, finishErr := recoveryStore.FinishJob(ctx, job.ID, job.FencingToken, operation.JobUnknown, payload, "scheduler became terminal before a durable worker marker"); finishErr != nil {
								return TickResult{}, fmt.Errorf("finish markerless job %s as unknown: %w", job.ID, finishErr)
							}
						}
					}
				}
			}
		}
	}
	if reconciler, ok := controller.Operational.(terminalReconciliationOperational); ok {
		snapshot.Jobs, err = reconciler.ListUnreconciledTerminalJobs(ctx, controller.Scope, 100)
	} else if lister, ok := controller.Operational.(jobLister); ok {
		snapshot.Jobs, err = lister.ListJobs(ctx, operation.JobSucceeded, operation.JobFailed, operation.JobCancelled)
	}
	if err != nil {
		return TickResult{}, fmt.Errorf("operational job snapshot: %w", err)
	}
	acknowledged := []string{}
	if canonical, ok := controller.Canonical.(acknowledgedCanonical); ok {
		acknowledged, err = canonical.ReconcileAcknowledged(ctx, snapshot)
	} else {
		err = controller.Canonical.Reconcile(ctx, snapshot)
	}
	if err != nil {
		return TickResult{}, err
	}
	if reconciler, ok := controller.Operational.(terminalReconciliationOperational); ok {
		jobsByID := make(map[string]operation.Job, len(snapshot.Jobs))
		for _, job := range snapshot.Jobs {
			jobsByID[job.ID] = job
		}
		for _, id := range acknowledged {
			job, found := jobsByID[id]
			if !found {
				return TickResult{}, fmt.Errorf("canonical acknowledged unknown terminal job %s", id)
			}
			if err := reconciler.MarkJobReconciled(ctx, job.ID, job.FencingToken); err != nil {
				return TickResult{}, fmt.Errorf("mark terminal job %s reconciled: %w", job.ID, err)
			}
		}
	}
	result := TickResult{Reconciled: true, Dispatched: []string{}, Recovered: []string{}, BorrowedLane: []string{}}
	state, err := controller.Operational.RuntimeState(ctx)
	if err != nil {
		return result, err
	}
	var pools []Pool
	if !state.Paused {
		pools, err = controller.Canonical.Pools(ctx)
		if err != nil {
			return result, err
		}
	}
	recovered, additions, err := controller.recoverOutbox(ctx, snapshot, clock(), pools, !state.Paused, &lease, ttl)
	if err != nil {
		if errors.Is(err, operation.ErrPaused) {
			result.Paused = true
			return result, nil
		}
		return result, err
	}
	result.Recovered = recovered
	snapshot.Tasks = append(snapshot.Tasks, additions...)
	if state.Paused {
		result.Paused = true
		return result, nil
	}
	for _, pool := range pools {
		if pool.Capacity <= 0 {
			continue
		}
		used, err := controller.activeUnitsForPool(ctx, snapshot, pool)
		if err != nil {
			return result, err
		}
		for used < pool.Capacity {
			if lease.ExpiresAt.Sub(clock().UTC()) < ttl/2 {
				lease, err = controller.Operational.RenewLease(ctx, lease, ttl)
				if err != nil {
					return result, fmt.Errorf("renew daemon lease: %w", err)
				}
			}
			latestState, err := controller.Operational.RuntimeState(ctx)
			if err != nil {
				return result, err
			}
			if latestState.Paused {
				result.Paused = true
				return result, nil
			}
			fairness, err := controller.Operational.Fairness(ctx, pool.Name)
			if err != nil {
				return result, err
			}
			exploit, exploitErr := controller.Canonical.Next(ctx, pool.Name, "exploit")
			explore, exploreErr := controller.Canonical.Next(ctx, pool.Name, "explore")
			exploitReady := exploitErr == nil
			exploreReady := exploreErr == nil
			if exploitErr != nil && !errors.Is(exploitErr, ErrNoWork) {
				return result, exploitErr
			}
			if exploreErr != nil && !errors.Is(exploreErr, ErrNoWork) {
				return result, exploreErr
			}
			lane, ok := operation.ChooseLane(fairness, exploitReady, exploreReady, pool.ExploitWeight, pool.ExploreWeight)
			if !ok {
				break
			}
			selection := exploit
			if lane == "explore" {
				selection = explore
			}
			selection.Pool, selection.Lane = pool.Name, lane
			units := selection.Units
			if units <= 0 {
				units = 1
			}
			if units > pool.Capacity-used {
				alternate := explore
				if lane == "explore" {
					alternate = exploit
				}
				alternateUnits := alternate.Units
				if alternateUnits <= 0 {
					alternateUnits = 1
				}
				if (lane == "exploit" && exploreReady || lane == "explore" && exploitReady) && alternateUnits <= pool.Capacity-used {
					selection = alternate
					if lane == "exploit" {
						lane = "explore"
					} else {
						lane = "exploit"
					}
					units = alternateUnits
					selection.Pool, selection.Lane = pool.Name, lane
				} else {
					break
				}
			}
			prepared, err := controller.Canonical.Prepare(ctx, selection)
			if err != nil {
				return result, err
			}
			outboxID := controller.id("outbox")
			factory := func(job operation.Job) (operation.OutboxInput, error) {
				dispatch := dispatchFrom(prepared, pool, job.FencingToken)
				intent := dispatchIntent{Selection: selection, Prepared: prepared, Dispatch: dispatch, FencingToken: job.FencingToken}
				payload, marshalErr := json.Marshal(intent)
				return operation.OutboxInput{ID: outboxID, OperationID: job.ID, Kind: "scheduler.submit", IdempotencyKey: "scheduler.submit:" + job.ID, Payload: payload}, marshalErr
			}
			var job operation.Job
			var item operation.OutboxItem
			duplicate := false
			if atomic, ok := controller.Operational.(atomicSubmissionOperational); ok {
				job, item, duplicate, err = atomic.PrepareSubmission(ctx, prepared.Job, controller.Holder, ttl, factory)
			} else {
				job, duplicate, err = controller.Operational.EnqueueJob(ctx, prepared.Job)
				if err == nil && job.State == operation.JobQueued {
					if exact, ok := controller.Operational.(exactClaimOperational); ok {
						job, err = exact.ClaimJobByID(ctx, job.ID, controller.Holder, ttl)
					} else {
						err = errors.New("operational store does not support exact-ID job claims")
					}
				}
				if err == nil {
					outbox, factoryErr := factory(job)
					if factoryErr != nil {
						err = factoryErr
					} else {
						item, _, err = controller.Operational.AddOutbox(ctx, outbox, time.Time{})
					}
				}
			}
			if err != nil {
				return result, err
			}
			if job.State != operation.JobRunning || job.ID != prepared.Job.ID {
				return result, errors.New("prepared submission did not claim the exact job")
			}
			if duplicate {
				if job.PueueTaskID != nil {
					if err := controller.Canonical.Submitted(ctx, selection, job, *job.PueueTaskID); err != nil {
						return result, err
					}
					result.Recovered = append(result.Recovered, job.ID)
				}
				// Existing outbox intents are retried only by recoverOutbox when
				// they become due. Never bypass retry backoff or resubmit one here.
				break
			}
			if item.State != operation.OutboxPending {
				return result, fmt.Errorf("new scheduler outbox %s is unexpectedly %s", item.ID, item.State)
			}
			dispatch := dispatchFrom(prepared, pool, job.FencingToken)
			taskID, err := controller.submitAuthorized(ctx, &lease, ttl, dispatch)
			if err != nil {
				if errors.Is(err, operation.ErrPaused) {
					result.Paused = true
					return result, nil
				}
				_ = controller.Operational.SetOutboxState(ctx, item.ID, operation.OutboxFailed, clock().Add(time.Minute), err.Error())
				return result, err
			}
			if err := controller.Operational.SetJobExternalRefs(ctx, job.ID, job.FencingToken, &taskID, ""); err != nil {
				return result, err
			}
			if err := controller.recordDispatch(ctx, job.ID, pool.Name, lane, positiveWeight(selection.Weight)); err != nil {
				return result, err
			}
			if err := controller.Operational.SetOutboxState(ctx, item.ID, operation.OutboxSucceeded, time.Time{}, ""); err != nil {
				return result, err
			}
			if err := controller.Canonical.Submitted(ctx, selection, job, taskID); err != nil {
				return result, err
			}
			result.Dispatched = append(result.Dispatched, selection.ID)
			used += units
		}
	}
	return result, nil
}

func (controller Controller) Run(ctx context.Context) error {
	interval := controller.PollEvery
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := controller.Tick(ctx); err != nil && !errors.Is(err, operation.ErrLeaseHeld) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type dispatchIntent struct {
	Selection    Selection `json:"selection"`
	Prepared     Prepared  `json:"prepared"`
	Dispatch     Dispatch  `json:"dispatch"`
	FencingToken int64     `json:"fencing_token"`
}

func (controller Controller) recoverOutbox(ctx context.Context, snapshot SchedulerSnapshot, now time.Time, pools []Pool, allowSubmit bool, lease *operation.Lease, ttl time.Duration) ([]string, []SchedulerTask, error) {
	scoped, ok := controller.Operational.(scopedOutboxOperational)
	if !ok {
		return nil, nil, errors.New("operational store does not support scoped outbox recovery")
	}
	recovered := []string{}
	additions := []SchedulerTask{}
	poolsByName := make(map[string]Pool, len(pools))
	for _, pool := range pools {
		poolsByName[pool.Name] = pool
	}
	for page := 0; page < 100; page++ {
		items, err := scoped.DueOutboxForScope(ctx, controller.Scope, 100)
		if err != nil {
			return recovered, additions, err
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			var intent dispatchIntent
			if err := json.Unmarshal(item.Payload, &intent); err != nil {
				_ = controller.Operational.SetOutboxState(ctx, item.ID, operation.OutboxFailed, now.Add(time.Hour), "invalid dispatch intent")
				continue
			}
			job, err := controller.Operational.GetJob(ctx, intent.Prepared.Job.ID)
			if err != nil {
				return recovered, additions, err
			}
			if job.CanonicalScope != controller.Scope || intent.Prepared.Job.CanonicalScope != controller.Scope {
				return recovered, additions, fmt.Errorf("outbox %s belongs to a different canonical scope", item.ID)
			}
			task, found, routeErr := taskByRoute(snapshot, intent.Dispatch.Group, intent.Dispatch.Label)
			if routeErr != nil {
				return recovered, additions, routeErr
			}
			if found {
				if err := controller.Operational.SetJobExternalRefs(ctx, job.ID, intent.FencingToken, &task.ID, ""); err != nil {
					return recovered, additions, err
				}
				if err := controller.recordDispatch(ctx, job.ID, intent.Selection.Pool, intent.Selection.Lane, positiveWeight(intent.Selection.Weight)); err != nil {
					return recovered, additions, err
				}
				if err := controller.Operational.SetOutboxState(ctx, item.ID, operation.OutboxSucceeded, time.Time{}, ""); err != nil {
					return recovered, additions, err
				}
				recovered = append(recovered, job.ID)
				continue
			}
			if !allowSubmit {
				continue
			}
			pool, configured := poolsByName[intent.Selection.Pool]
			if !configured || pool.Capacity <= 0 {
				_ = controller.Operational.SetOutboxState(ctx, item.ID, operation.OutboxFailed, now.Add(10*time.Minute), "canonical pool is unavailable")
				continue
			}
			if intent.Dispatch.Group != pool.NativeGroup || pool.LabelPrefix != "" && !strings.HasPrefix(intent.Dispatch.Label, pool.LabelPrefix) {
				return recovered, additions, fmt.Errorf("outbox %s routing no longer matches pool %s", item.ID, pool.Name)
			}
			used, unitsErr := controller.activeUnitsForPool(ctx, snapshot, pool)
			if unitsErr != nil {
				return recovered, additions, unitsErr
			}
			units := job.Units
			if units <= 0 {
				units = intent.Prepared.Units
			}
			if units <= 0 {
				units = 1
			}
			if units > pool.Capacity-used {
				_ = controller.Operational.SetOutboxState(ctx, item.ID, operation.OutboxFailed, now.Add(time.Minute), "insufficient pool capacity")
				continue
			}
			taskID, submitErr := controller.submitAuthorized(ctx, lease, ttl, intent.Dispatch)
			if submitErr != nil {
				if errors.Is(submitErr, operation.ErrPaused) || errors.Is(submitErr, operation.ErrFenced) {
					return recovered, additions, submitErr
				}
				_ = controller.Operational.SetOutboxState(ctx, item.ID, operation.OutboxFailed, now.Add(time.Minute), submitErr.Error())
				continue
			}
			if err := controller.Operational.SetJobExternalRefs(ctx, job.ID, intent.FencingToken, &taskID, ""); err != nil {
				return recovered, additions, err
			}
			if err := controller.recordDispatch(ctx, job.ID, intent.Selection.Pool, intent.Selection.Lane, positiveWeight(intent.Selection.Weight)); err != nil {
				return recovered, additions, err
			}
			if err := controller.Operational.SetOutboxState(ctx, item.ID, operation.OutboxSucceeded, time.Time{}, ""); err != nil {
				return recovered, additions, err
			}
			if err := controller.Canonical.Submitted(ctx, intent.Selection, job, taskID); err != nil {
				return recovered, additions, err
			}
			recovered = append(recovered, job.ID)
			addition := SchedulerTask{ID: taskID, Label: intent.Dispatch.Label, Group: intent.Dispatch.Group, State: "queued"}
			additions = append(additions, addition)
			snapshot.Tasks = append(snapshot.Tasks, addition)
		}
		if !allowSubmit {
			break
		}
	}
	return recovered, additions, nil
}

func (controller Controller) recordDispatch(ctx context.Context, jobID, pool, lane string, weight float64) error {
	if exact, ok := controller.Operational.(exactFairnessOperational); ok {
		_, err := exact.RecordDispatchOnce(ctx, jobID, pool, lane, weight)
		return err
	}
	_, err := controller.Operational.RecordDispatch(ctx, pool, lane, weight)
	return err
}

func positiveWeight(weight float64) float64 {
	if weight <= 0 {
		return 1
	}
	return weight
}

func (controller Controller) submitAuthorized(ctx context.Context, lease *operation.Lease, ttl time.Duration, dispatch Dispatch) (int64, error) {
	guard, ok := controller.Operational.(dispatchLeaseOperational)
	if !ok || lease == nil {
		return 0, errors.New("operational store does not support fenced dispatch authorization")
	}
	taskID, renewed, err := guard.WithDispatchLease(ctx, *lease, ttl, func() (int64, error) {
		return controller.Scheduler.Submit(ctx, dispatch)
	})
	if err == nil {
		*lease = renewed
	}
	return taskID, err
}

func activeForPool(snapshot SchedulerSnapshot, pool Pool) int {
	count := 0
	for _, task := range snapshot.Tasks {
		if task.Group != pool.NativeGroup || pool.LabelPrefix != "" && !strings.HasPrefix(task.Label, pool.LabelPrefix) {
			continue
		}
		if !schedulerTerminal(task.State) {
			count += pool.Capacity
		}
	}
	return count
}

type jobLister interface {
	ListJobs(context.Context, ...operation.JobState) ([]operation.Job, error)
}

func (controller Controller) activeUnitsForPool(ctx context.Context, snapshot SchedulerSnapshot, pool Pool) (int, error) {
	byTask := map[int64]operation.ActiveAllocation{}
	if allocator, ok := controller.Operational.(activeAllocationOperational); ok {
		allocations, err := allocator.ListActiveAllocations(ctx)
		if err != nil {
			return 0, err
		}
		for _, allocation := range allocations {
			byTask[allocation.PueueTaskID] = allocation
		}
	} else if lister, ok := controller.Operational.(jobLister); ok {
		jobs, err := lister.ListJobs(ctx, operation.JobQueued, operation.JobRunning)
		if err != nil {
			return 0, err
		}
		for _, job := range jobs {
			if job.PueueTaskID != nil {
				byTask[*job.PueueTaskID] = operation.ActiveAllocation{PueueTaskID: *job.PueueTaskID, Pool: job.Pool, Units: job.Units}
			}
		}
	} else {
		return activeForPool(snapshot, pool), nil
	}
	used := 0
	for _, task := range snapshot.Tasks {
		if task.Group != pool.NativeGroup || pool.LabelPrefix != "" && !strings.HasPrefix(task.Label, pool.LabelPrefix) || schedulerTerminal(task.State) {
			continue
		}
		// Private SQLite is explicitly rebuildable. If its scheduler mapping is
		// absent, fail safe by charging the whole pool rather than assuming one
		// unit and oversubscribing a multi-unit canonical experiment.
		units := pool.Capacity
		if allocation, found := byTask[task.ID]; found && allocation.Pool == pool.Name && allocation.Units > 0 {
			units = allocation.Units
		}
		used += units
	}
	return used, nil
}

func schedulerTerminal(state string) bool {
	switch state {
	case "succeeded", "failed", "cancelled", "dependency_failed":
		return true
	default:
		return false
	}
}

func taskByRoute(snapshot SchedulerSnapshot, group, label string) (SchedulerTask, bool, error) {
	var matched SchedulerTask
	found := false
	for _, task := range snapshot.Tasks {
		if task.Group == group && task.Label == label {
			if found {
				return SchedulerTask{}, false, fmt.Errorf("scheduler route %s/%s is ambiguous", group, label)
			}
			matched, found = task, true
		}
	}
	return matched, found, nil
}

func dispatchFrom(prepared Prepared, pool Pool, fencingToken int64) Dispatch {
	arguments := append([]string(nil), prepared.Args...)
	arguments = append(arguments, "--fencing-token", strconv.FormatInt(fencingToken, 10))
	return Dispatch{Group: pool.NativeGroup, Label: prepared.Label, Priority: prepared.Priority, WorkingDir: prepared.CWD, Worker: prepared.Worker, WorkerArgs: arguments, AllowedEnv: append([]string(nil), prepared.AllowedEnv...)}
}

func (controller Controller) id(prefix string) string {
	if controller.NewID != nil {
		return controller.NewID(prefix)
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
