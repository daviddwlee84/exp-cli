// Package controlplane adapts Git-canonical research records to the controller
// loop. Scheduler and SQLite observations may advance Attempt operational state;
// they never create scientific conclusions, Findings, Evaluations, or Decisions.
package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/controller"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/worker"
)

const attemptExtension = "io.github.daviddwlee84.exp-cli.controlplane"

// Store is the canonical read/transaction surface implemented by record.Store.
type Store interface {
	Inventory(context.Context) (*record.Inventory, error)
	Transact(context.Context, record.TransactionRequest) (*record.TransactionResult, error)
}

type recoveringStore interface {
	Recover(context.Context) error
}

// Adapter is the production controller.Canonical implementation.
type Adapter struct {
	Store            Store
	RepositoryRoot   string
	ConfigPath       string
	WorkerExecutable string
	WorkerArgs       []string
	Clock            func() time.Time
	GenerateUUID     research.UUIDGenerator
	Git              gitx.Runner
}

var _ controller.Canonical = Adapter{}

// FrontierItem exposes one canonical pool/lane frontier without granting it
// dispatch authority. Enabled is true only when canonical Policy explicitly
// selects assisted/limited autonomy and the pool and runtime bindings exist.
type FrontierItem struct {
	DispatchID    string                `json:"dispatch_id"`
	Queue         research.ID           `json:"queue"`
	QueueRevision uint64                `json:"queue_revision"`
	Plan          research.ID           `json:"plan"`
	PlanRevision  string                `json:"plan_revision"`
	Pool          research.ID           `json:"pool"`
	Lane          research.ResearchLane `json:"lane"`
	Score         float64               `json:"score"`
	Weight        float64               `json:"weight"`
	Units         int                   `json:"units"`
	Configured    bool                  `json:"configured"`
	Enabled       bool                  `json:"enabled"`
}

type canonicalSnapshot struct {
	inventory *record.Inventory
	policy    *research.Policy
	runtime   loadedRuntime
	scope     string
}

type dispatchCandidate struct {
	frontier       record.FrontierEntry
	queueDocument  *record.Document
	planDocument   *record.Document
	queue          *research.Queue
	plan           *research.Plan
	runtime        validatedPlanRuntime
	poolRuntime    PoolRuntime
	dispatchID     string
	weight         float64
	partitionIndex int
}

// Pools exposes canonical ResourcePools. Manual and shadow policy modes retain
// visibility but report zero dispatch capacity.
func (adapter Adapter) Pools(ctx context.Context) ([]controller.Pool, error) {
	snapshot, err := adapter.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	enabledByPolicy := dispatchEnabled(snapshot.policy)
	documents := snapshot.inventory.OfKind(research.KindResourcePool)
	pools := make([]controller.Pool, 0, len(documents))
	for _, document := range documents {
		pool := document.Record.(*research.ResourcePool)
		runtimePool, configured := snapshot.runtime.pools[pool.ID]
		capacity := 0
		if enabledByPolicy && pool.Enabled && configured {
			if pool.Capacity > uint64(math.MaxInt) {
				return nil, fmt.Errorf("ResourcePool %s capacity exceeds this platform", pool.ID)
			}
			capacity = int(pool.Capacity)
		}
		pools = append(pools, controller.Pool{
			Name: pool.ID.String(), NativeGroup: runtimePool.PueueGroup, LabelPrefix: runtimePool.LabelPrefix,
			Capacity: capacity, ExploitWeight: snapshot.policy.ExploitShare, ExploreWeight: snapshot.policy.ExploreShare,
		})
	}
	return pools, nil
}

// Frontier returns every canonical queue frontier in deterministic order.
func (adapter Adapter) Frontier(ctx context.Context) ([]FrontierItem, error) {
	snapshot, err := adapter.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return frontierItems(snapshot)
}

// Next selects the first canonical frontier for a pool/lane. Manual and shadow
// autonomy deliberately expose no ready work to the controller.
func (adapter Adapter) Next(ctx context.Context, poolName, laneName string) (controller.Selection, error) {
	snapshot, err := adapter.snapshot(ctx)
	if err != nil {
		return controller.Selection{}, err
	}
	if !dispatchEnabled(snapshot.policy) {
		return controller.Selection{}, controller.ErrNoWork
	}
	poolID, err := research.ParseIDForKind(poolName, research.KindResourcePool)
	if err != nil {
		return controller.Selection{}, fmt.Errorf("controller pool %q: %w", poolName, err)
	}
	lane := research.ResearchLane(laneName)
	if lane != research.LaneExploit && lane != research.LaneExplore {
		return controller.Selection{}, fmt.Errorf("controller lane %q is invalid", laneName)
	}
	poolDocument, err := snapshot.inventory.ByID(poolID)
	if err != nil {
		return controller.Selection{}, err
	}
	pool := poolDocument.Record.(*research.ResourcePool)
	if !pool.Enabled {
		return controller.Selection{}, controller.ErrNoWork
	}
	if _, configured := snapshot.runtime.pools[poolID]; !configured {
		return controller.Selection{}, fmt.Errorf("ResourcePool %s has no runtime Pueue binding", poolID)
	}
	// Canonical preparation intentionally precedes private operational state.
	// If the daemon crashed in that narrow gap, the queue entry is already gone
	// but its planned Attempt is the durable recovery frontier.
	for _, document := range snapshot.inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		if attempt.Schema != research.SchemaAttemptV2 || attempt.State != research.AttemptPlanned || attempt.DispatchID == "" ||
			attempt.Pool != poolID || attempt.Lane != lane {
			continue
		}
		if _, submitted := pueueReference(attempt); submitted {
			continue
		}
		plan, planErr := originatingPlan(snapshot.inventory, attempt)
		if planErr != nil {
			return controller.Selection{}, planErr
		}
		if err := dispatchStillActive(snapshot.inventory, plan, attempt); err != nil {
			return controller.Selection{}, err
		}
		if len(plan.Resources) != 1 {
			return controller.Selection{}, errors.New("autonomous dispatch requires exactly one ResourcePool need; use a composite pool")
		}
		if _, prepareErr := adapter.preparedForAttempt(snapshot, document); prepareErr != nil {
			return controller.Selection{}, prepareErr
		}
		return controller.Selection{ID: attempt.DispatchID, Pool: poolName, Lane: laneName, Weight: planWeight(plan, poolID), Units: planUnits(plan, poolID)}, nil
	}
	items, err := frontierItems(snapshot)
	if err != nil {
		return controller.Selection{}, err
	}
	for _, item := range items {
		if item.Pool != poolID || item.Lane != lane {
			continue
		}
		if !item.Configured {
			return controller.Selection{}, fmt.Errorf("frontier Plan %s has no runtime executable contract", item.Plan)
		}
		return controller.Selection{ID: item.DispatchID, Pool: poolName, Lane: laneName, Weight: item.Weight, Units: item.Units}, nil
	}
	return controller.Selection{}, controller.ErrNoWork
}

// Prepare atomically creates Experiment, Run, and Attempt records, marks the
// Plan started, and removes exactly the selected frontier entry from its Queue.
func (adapter Adapter) Prepare(ctx context.Context, selection controller.Selection) (controller.Prepared, error) {
	snapshot, err := adapter.snapshot(ctx)
	if err != nil {
		return controller.Prepared{}, err
	}
	if existing := findAttemptByDispatch(snapshot.inventory, selection.ID); existing != nil {
		return adapter.preparedForAttempt(snapshot, existing)
	}
	if !dispatchEnabled(snapshot.policy) {
		return controller.Prepared{}, controller.ErrNoWork
	}
	candidate, err := findCandidate(snapshot, selection)
	if err != nil {
		return controller.Prepared{}, err
	}
	now := adapter.now()
	experimentID, err := adapter.newID(research.KindExperiment, now)
	if err != nil {
		return controller.Prepared{}, err
	}
	runID, err := adapter.newID(research.KindRun, now)
	if err != nil {
		return controller.Prepared{}, err
	}
	attemptID, err := adapter.newID(research.KindAttempt, now)
	if err != nil {
		return controller.Prepared{}, err
	}

	design := designFor(candidate.plan, candidate.runtime, now)
	designDigest, err := research.DesignDigest(design)
	if err != nil {
		return controller.Prepared{}, err
	}
	design.DesignDigest = designDigest
	experiment := &research.Experiment{
		Common:    common(research.SchemaExperimentV2, experimentID, candidate.plan.Title, candidate.plan.Tags, now),
		Lifecycle: research.LifecycleActive, Design: design, Parents: experimentParents(snapshot.inventory, candidate.plan), CandidateInputs: []research.ID{},
	}
	run := &research.Run{
		Common:     common(research.SchemaRun, runID, candidate.plan.Title, candidate.plan.Tags, now),
		Experiment: experimentID, Role: research.RunCandidate, Objective: candidate.plan.ExpectedPayoff.Summary,
		ConfigDigest: candidate.runtime.digest, Seeds: []int64{}, ExpectedOutputs: append([]string(nil), candidate.runtime.ExpectedOutputs...),
	}
	attempt := &research.Attempt{
		Common: common(research.SchemaAttemptV2, attemptID, candidate.plan.Title, candidate.plan.Tags, now),
		Run:    runID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "pueue",
		CWD: candidate.runtime.CWD, Argv: append([]string{candidate.runtime.Executable}, candidate.runtime.Argv...),
		ExternalRefs: []research.ExternalRef{}, Pool: candidate.frontier.Pool, Queue: candidate.frontier.Queue,
		QueueRevision: candidate.queue.Revision, Lane: candidate.frontier.Lane, DispatchID: candidate.dispatchID,
		BaseCommit: candidate.runtime.BaseCommit, HeadCommit: candidate.runtime.HeadCommit,
		ChangeSet: append([]string(nil), candidate.runtime.ChangeSet...),
		Extensions: research.Extensions{attemptExtension: {
			"pueue_group":    candidate.poolRuntime.PueueGroup,
			"pueue_label":    candidate.poolRuntime.LabelPrefix + candidate.dispatchID,
			"runtime_digest": candidate.runtime.digest,
		}},
	}

	planDocument := candidate.planDocument.Clone()
	plan := planDocument.Record.(*research.Plan)
	plan.State = research.PlanStarted
	plan.ResultingExperiment = experimentID
	plan.UpdatedAt = now
	queueDocument := candidate.queueDocument.Clone()
	queue := queueDocument.Record.(*research.Queue)
	partition := &queue.Partitions[candidate.partitionIndex]
	partition.Entries = append([]research.QueueEntry(nil), partition.Entries[1:]...)
	if partition.Entries == nil {
		partition.Entries = []research.QueueEntry{}
	}
	queue.Revision++
	queue.UpdatedAt = now

	request := record.TransactionRequest{Operation: "dispatch.prepare", Changes: []record.TransactionChange{
		{Operation: record.TransactionCreate, Document: &record.Document{Record: experiment, Body: generatedBody("Experiment", candidate.plan, candidate.dispatchID)}},
		{Operation: record.TransactionCreate, Document: &record.Document{Record: run, Body: generatedBody("Run", candidate.plan, candidate.dispatchID)}},
		{Operation: record.TransactionCreate, Document: &record.Document{Record: attempt, Body: generatedBody("Attempt", candidate.plan, candidate.dispatchID)}},
		{Operation: record.TransactionReplace, Document: planDocument, ExpectedRevision: candidate.planDocument.Revision},
		{Operation: record.TransactionReplace, Document: queueDocument, ExpectedRevision: candidate.queueDocument.Revision},
	}}
	if _, err := adapter.Store.Transact(ctx, request); err != nil {
		// A durable transaction may have rolled forward even if its final sync
		// reported an error. Idempotently prefer the canonical dispatch record.
		if refreshed, loadErr := adapter.snapshot(ctx); loadErr == nil {
			if existing := findAttemptByDispatch(refreshed.inventory, selection.ID); existing != nil {
				return adapter.preparedForAttempt(refreshed, existing)
			}
		}
		return controller.Prepared{}, err
	}
	return adapter.buildPrepared(candidate.plan, attempt, candidate.runtime, candidate.poolRuntime)
}

func experimentParents(inventory *record.Inventory, plan *research.Plan) []research.ID {
	if inventory == nil || plan == nil || plan.Idea.IsZero() {
		return []research.ID{}
	}
	ideaDocument, err := inventory.ByID(plan.Idea)
	if err != nil {
		return []research.ID{}
	}
	idea := ideaDocument.Record.(*research.Idea)
	parents := []research.ID{}
	seen := map[research.ID]struct{}{}
	for _, parentIdeaID := range idea.Parents {
		parentIdeaDocument, err := inventory.ByID(parentIdeaID)
		if err != nil {
			continue
		}
		parentPlanID := parentIdeaDocument.Record.(*research.Idea).ResultingPlan
		if parentPlanID.IsZero() {
			continue
		}
		parentPlanDocument, err := inventory.ByID(parentPlanID)
		if err != nil {
			continue
		}
		parentExperiment := parentPlanDocument.Record.(*research.Plan).ResultingExperiment
		if parentExperiment.IsZero() {
			continue
		}
		if _, duplicate := seen[parentExperiment]; duplicate {
			continue
		}
		seen[parentExperiment] = struct{}{}
		parents = append(parents, parentExperiment)
	}
	sort.Slice(parents, func(i, j int) bool { return parents[i].String() < parents[j].String() })
	return parents
}

// Submitted attaches the accepted Pueue task identity and advances only the
// Attempt's operational state. Repeating the same task ID is a no-op.
func (adapter Adapter) Submitted(ctx context.Context, selection controller.Selection, job operation.Job, taskID int64) error {
	if taskID < 0 {
		return errors.New("Pueue task ID cannot be negative")
	}
	inventory, _, err := adapter.canonical(ctx)
	if err != nil {
		return err
	}
	document := findAttemptByDispatch(inventory, selection.ID)
	if document == nil {
		return fmt.Errorf("dispatch %s has no canonical Attempt", selection.ID)
	}
	attempt := document.Record.(*research.Attempt)
	if selection.Pool != attempt.Pool.String() || selection.Lane != string(attempt.Lane) ||
		job.SubjectID != attempt.ID.String() || job.ID != jobID(selection.ID) || job.Pool != attempt.Pool.String() ||
		job.Lane != string(attempt.Lane) || job.IdempotencyKey != "dispatch:"+selection.ID {
		return errors.New("operational job identity does not match canonical Attempt")
	}
	nativeID := strconv.FormatInt(taskID, 10)
	if existing, found := pueueReference(attempt); found {
		if existing.NativeID == nativeID {
			return nil
		}
		return errors.New("canonical Attempt already references a different Pueue task")
	}
	replacement := document.Clone()
	updated := replacement.Record.(*research.Attempt)
	now := adapter.atLeastCreated(updated.CreatedAt)
	updated.State = research.AttemptQueued
	updated.StateReason = "Pueue submission accepted"
	updated.UpdatedAt = now
	updated.ExternalRefs = append(updated.ExternalRefs, schedulerReference(nativeID, now))
	_, err = adapter.Store.Transact(ctx, record.TransactionRequest{Operation: "dispatch.submitted", Changes: []record.TransactionChange{{
		Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: document.Revision,
	}}})
	return err
}

// Reconcile imports observed Pueue operational states into Attempt records. A
// missing task never implies failure, and terminal scheduler state never closes
// an Experiment or establishes a scientific result.
func (adapter Adapter) Reconcile(ctx context.Context, scheduler controller.SchedulerSnapshot) error {
	_, err := adapter.ReconcileAcknowledged(ctx, scheduler)
	return err
}

// ReconcileAcknowledged returns only terminal operational job IDs that were
// actually matched to this canonical inventory. The controller uses this to
// avoid a daemon in another linked worktree consuming a shared SQLite marker.
func (adapter Adapter) ReconcileAcknowledged(ctx context.Context, scheduler controller.SchedulerSnapshot) ([]string, error) {
	inventory, _, err := adapter.canonical(ctx)
	if err != nil {
		return nil, err
	}
	runtime := loadedRuntime{plans: map[research.ID]validatedPlanRuntime{}, pools: map[research.ID]PoolRuntime{}}
	if loaded, loadErr := loadRuntime(ctx, adapter.RepositoryRoot, adapter.ConfigPath); loadErr == nil {
		runtime = loaded
	}
	markerRoot, err := adapter.workerMarkerRoot(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]controller.SchedulerTask, len(scheduler.Tasks))
	byLabel := make(map[string]controller.SchedulerTask, len(scheduler.Tasks))
	for _, task := range scheduler.Tasks {
		key := strconv.FormatInt(task.ID, 10)
		if _, duplicate := byID[key]; duplicate {
			return nil, fmt.Errorf("Pueue snapshot repeats task ID %s", key)
		}
		byID[key] = task
		if task.Label != "" {
			if _, duplicate := byLabel[task.Label]; duplicate {
				return nil, fmt.Errorf("Pueue snapshot repeats task label %s", task.Label)
			}
			byLabel[task.Label] = task
		}
	}
	jobsByAttempt := make(map[string]operation.Job, len(scheduler.Jobs))
	for _, job := range scheduler.Jobs {
		if job.Kind != "experiment.run" || job.Role != "execute" || !terminalJob(job.State) {
			continue
		}
		if _, duplicate := jobsByAttempt[job.SubjectID]; duplicate {
			return nil, fmt.Errorf("operational snapshot repeats terminal job for Attempt %s", job.SubjectID)
		}
		jobsByAttempt[job.SubjectID] = job
	}

	changes := []record.TransactionChange{}
	acknowledged := []string{}
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		if attempt.Schema != research.SchemaAttemptV2 || attempt.DispatchID == "" {
			continue
		}
		if attempt.Terminal == nil || attempt.Terminal.Source != "direct" {
			marker, found, loadErr := worker.LoadTerminal(ctx, markerRoot, jobID(attempt.DispatchID))
			if loadErr != nil {
				return nil, fmt.Errorf("load worker terminal for %s: %w", attempt.ID, loadErr)
			}
			if found {
				var taskID *int64
				if job, hasJob := jobsByAttempt[attempt.ID.String()]; hasJob {
					if job.ID != marker.JobID || job.FencingToken != marker.FencingToken || job.State != marker.State {
						return nil, errors.New("durable worker marker conflicts with terminal operational job")
					}
					taskID = job.PueueTaskID
					acknowledged = append(acknowledged, job.ID)
				}
				replacement, replaceErr := adapter.markerTerminalReplacement(document, marker, taskID)
				if replaceErr != nil {
					return nil, replaceErr
				}
				if replacement != nil {
					changes = append(changes, record.TransactionChange{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: document.Revision})
				}
				continue
			}
		}
		if job, found := jobsByAttempt[attempt.ID.String()]; found {
			replacement, replaceErr := adapter.workerTerminalReplacement(document, job)
			if replaceErr != nil {
				return nil, replaceErr
			}
			acknowledged = append(acknowledged, job.ID)
			if replacement != nil {
				changes = append(changes, record.TransactionChange{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: document.Revision})
			}
			continue
		}
		if terminalAttempt(attempt.State) {
			continue
		}
		task, found := controller.SchedulerTask{}, false
		if reference, referenced := pueueReference(attempt); referenced {
			task, found = byID[reference.NativeID]
		} else if group, label, routed := attemptRoute(attempt); routed {
			task, found = byLabel[label]
			if found && task.Group != group {
				found = false
			}
		} else if runtimePool, configured := runtime.pools[attempt.Pool]; configured {
			task, found = byLabel[runtimePool.LabelPrefix+attempt.DispatchID]
			if found && task.Group != runtimePool.PueueGroup {
				found = false
			}
		}
		if !found {
			continue
		}
		state, terminal, recognized := observedAttemptState(task.State)
		if !recognized {
			continue
		}
		nativeID := strconv.FormatInt(task.ID, 10)
		reference, referenced := pueueReference(attempt)
		needsReference := !referenced
		if referenced && reference.NativeID != nativeID {
			continue
		}
		if !needsReference && attempt.State == state {
			continue
		}
		replacement := document.Clone()
		updated := replacement.Record.(*research.Attempt)
		now := adapter.atLeastCreated(updated.CreatedAt)
		updated.State = state
		updated.StateReason = "Observed from Pueue scheduler"
		updated.UpdatedAt = now
		if needsReference {
			updated.ExternalRefs = append(updated.ExternalRefs, schedulerReference(nativeID, now))
		}
		if terminal {
			updated.Terminal = &research.Terminal{Source: "pueue", ObservedAt: now, EndedAt: now}
		}
		changes = append(changes, record.TransactionChange{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: document.Revision})
	}
	if len(changes) == 0 {
		return acknowledged, nil
	}
	_, err = adapter.Store.Transact(ctx, record.TransactionRequest{Operation: "dispatch.reconcile", Changes: changes})
	if err != nil {
		return nil, err
	}
	return acknowledged, nil
}

func terminalJob(state operation.JobState) bool {
	return state == operation.JobSucceeded || state == operation.JobFailed || state == operation.JobCancelled
}

func (adapter Adapter) workerTerminalReplacement(document *record.Document, job operation.Job) (*record.Document, error) {
	attempt := document.Record.(*research.Attempt)
	if job.ID != jobID(attempt.DispatchID) || job.IdempotencyKey != "dispatch:"+attempt.DispatchID ||
		job.SubjectID != attempt.ID.String() || job.Pool != attempt.Pool.String() || job.Lane != string(attempt.Lane) {
		return nil, errors.New("terminal operational job identity does not match canonical Attempt")
	}
	result, err := worker.DecodeJobResult(job.Result)
	if err != nil {
		return nil, fmt.Errorf("decode terminal worker result for %s: %w", attempt.ID, err)
	}
	terminal := result.Terminal
	if terminal.JobID != job.ID || terminal.AttemptID != attempt.ID.String() || terminal.FencingToken != job.FencingToken || terminal.State != job.State {
		return nil, errors.New("worker terminal envelope does not match operational job")
	}
	return adapter.markerTerminalReplacement(document, terminal, job.PueueTaskID)
}

func (adapter Adapter) markerTerminalReplacement(document *record.Document, terminal worker.Terminal, pueueTaskID *int64) (*record.Document, error) {
	attempt := document.Record.(*research.Attempt)
	if terminal.JobID != jobID(attempt.DispatchID) || terminal.AttemptID != attempt.ID.String() || !terminalJob(terminal.State) {
		return nil, errors.New("durable worker terminal identity does not match canonical Attempt")
	}
	state := research.AttemptFailed
	switch {
	case terminal.TimedOut:
		state = research.AttemptTimedOut
	case terminal.Cancelled || terminal.State == operation.JobCancelled:
		state = research.AttemptCancelled
	case terminal.State == operation.JobSucceeded:
		state = research.AttemptSucceeded
	}
	outputDigests := make(map[string]any, len(terminal.Outputs))
	for path, digest := range terminal.Outputs {
		outputDigests[path] = digest
	}
	if attempt.State == state && sameImportedWorkerTerminal(attempt, terminal, outputDigests) {
		if pueueTaskID == nil {
			return nil, nil
		}
		if reference, found := pueueReference(attempt); found && reference.NativeID == strconv.FormatInt(*pueueTaskID, 10) {
			return nil, nil
		}
	}
	replacement := document.Clone()
	updated := replacement.Record.(*research.Attempt)
	now := adapter.atLeastCreated(updated.CreatedAt)
	if now.Before(updated.UpdatedAt) {
		now = updated.UpdatedAt
	}
	if now.Before(terminal.EndedAt) {
		now = terminal.EndedAt
	}
	started := terminal.StartedAt
	exitCode := terminal.ExitCode
	updated.State = state
	updated.StateReason = "Observed from exp worker terminal marker"
	updated.UpdatedAt = now
	updated.Terminal = &research.Terminal{Source: "direct", ObservedAt: now, StartedAt: &started, EndedAt: terminal.EndedAt, ExitCode: &exitCode}
	if pueueTaskID != nil {
		nativeID := strconv.FormatInt(*pueueTaskID, 10)
		if existing, found := pueueReference(updated); found {
			if existing.NativeID != nativeID {
				return nil, errors.New("worker job Pueue identity conflicts with canonical Attempt")
			}
		} else {
			updated.ExternalRefs = append(updated.ExternalRefs, schedulerReference(nativeID, now))
		}
	}
	if updated.Extensions == nil {
		updated.Extensions = research.Extensions{}
	}
	table := updated.Extensions[attemptExtension]
	if table == nil {
		table = map[string]any{}
		updated.Extensions[attemptExtension] = table
	}
	table["worker_result_sha256"] = terminal.ResultSHA256
	table["worker_result_size"] = terminal.ResultSize
	table["worker_outputs"] = outputDigests
	if reflect.DeepEqual(document.Record, replacement.Record) {
		return nil, nil
	}
	return replacement, nil
}

func (adapter Adapter) workerMarkerRoot(ctx context.Context) (string, error) {
	runner := adapter.Git
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	repository, err := gitx.DiscoverWithRunner(ctx, adapter.RepositoryRoot, runner)
	if err != nil {
		return "", err
	}
	return filepath.Join(repository.GitCommonDir, "exp", "v1", "attempts"), nil
}

func sameImportedWorkerTerminal(attempt *research.Attempt, terminal worker.Terminal, outputs map[string]any) bool {
	if attempt == nil || attempt.Terminal == nil || attempt.Terminal.Source != "direct" || attempt.Terminal.StartedAt == nil || attempt.Terminal.ExitCode == nil {
		return false
	}
	if !attempt.Terminal.StartedAt.Equal(terminal.StartedAt) || !attempt.Terminal.EndedAt.Equal(terminal.EndedAt) || *attempt.Terminal.ExitCode != terminal.ExitCode {
		return false
	}
	table := attempt.Extensions[attemptExtension]
	return table != nil && table["worker_result_sha256"] == terminal.ResultSHA256 && table["worker_result_size"] == terminal.ResultSize && reflect.DeepEqual(table["worker_outputs"], outputs)
}

func (adapter Adapter) snapshot(ctx context.Context) (canonicalSnapshot, error) {
	inventory, policy, err := adapter.canonical(ctx)
	if err != nil {
		return canonicalSnapshot{}, err
	}
	runtime, err := loadRuntime(ctx, adapter.RepositoryRoot, adapter.ConfigPath)
	if err != nil {
		return canonicalSnapshot{}, err
	}
	if err := adapter.verifyRuntimeGit(ctx, inventory, &runtime); err != nil {
		return canonicalSnapshot{}, err
	}
	scope, err := ScopeID(adapter.RepositoryRoot)
	if err != nil {
		return canonicalSnapshot{}, err
	}
	return canonicalSnapshot{inventory: inventory, policy: policy, runtime: runtime, scope: scope}, nil
}

func (adapter Adapter) canonical(ctx context.Context) (*record.Inventory, *research.Policy, error) {
	if adapter.Store == nil {
		return nil, nil, errors.New("canonical Store is required")
	}
	if adapter.RepositoryRoot == "" {
		return nil, nil, errors.New("repository root is required")
	}
	if recovering, ok := adapter.Store.(recoveringStore); ok {
		if err := recovering.Recover(ctx); err != nil {
			return nil, nil, fmt.Errorf("recover canonical transactions: %w", err)
		}
	}
	inventory, err := adapter.Store.Inventory(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !inventory.Valid() {
		return nil, nil, &record.InventoryError{Diagnostics: append([]record.Diagnostic(nil), inventory.Diagnostics...)}
	}
	canonicalRepository, err := pathx.Canonical(adapter.RepositoryRoot)
	if err != nil {
		return nil, nil, err
	}
	inventoryRepository, err := pathx.Canonical(filepath.Dir(inventory.Root))
	if err != nil {
		return nil, nil, err
	}
	if canonicalRepository != inventoryRepository {
		return nil, nil, errors.New("runtime repository root does not own the canonical experiments root")
	}
	if inventory.Policy == nil {
		return nil, nil, errors.New("canonical POLICY.md is required")
	}
	policy, ok := inventory.Policy.Record.(*research.Policy)
	if !ok {
		return nil, nil, errors.New("canonical Policy has an unexpected type")
	}
	return inventory, policy, nil
}

func frontierItems(snapshot canonicalSnapshot) ([]FrontierItem, error) {
	enabledByPolicy := dispatchEnabled(snapshot.policy)
	entries := snapshot.inventory.QueueFrontier()
	items := make([]FrontierItem, 0, len(entries))
	for _, entry := range entries {
		queueDocument, err := snapshot.inventory.ByID(entry.Queue)
		if err != nil {
			return nil, err
		}
		queue := queueDocument.Record.(*research.Queue)
		planDocument, err := snapshot.inventory.ByID(entry.Entry.Plan)
		if err != nil {
			return nil, err
		}
		plan := planDocument.Record.(*research.Plan)
		if plan.Schema != research.SchemaPlanV2 || plan.State != research.PlanQueued {
			continue
		}
		if len(plan.Resources) != 1 {
			return nil, errors.New("autonomous dispatch requires exactly one ResourcePool need; use a composite pool")
		}
		poolDocument, err := snapshot.inventory.ByID(entry.Pool)
		if err != nil {
			return nil, err
		}
		pool := poolDocument.Record.(*research.ResourcePool)
		_, planConfigured := snapshot.runtime.plans[plan.ID]
		_, poolConfigured := snapshot.runtime.pools[pool.ID]
		items = append(items, FrontierItem{
			DispatchID: dispatchID(entry, queue.Revision, snapshot.scope), Queue: entry.Queue, QueueRevision: queue.Revision,
			Plan: plan.ID, PlanRevision: planDocument.Revision, Pool: entry.Pool, Lane: entry.Lane,
			Score: entry.Entry.Score, Weight: planWeight(plan, entry.Pool), Units: planUnits(plan, entry.Pool), Configured: planConfigured && poolConfigured,
			Enabled: enabledByPolicy && pool.Enabled && planConfigured && poolConfigured,
		})
	}
	return items, nil
}

func findCandidate(snapshot canonicalSnapshot, selection controller.Selection) (dispatchCandidate, error) {
	poolID, err := research.ParseIDForKind(selection.Pool, research.KindResourcePool)
	if err != nil {
		return dispatchCandidate{}, err
	}
	lane := research.ResearchLane(selection.Lane)
	for _, frontier := range snapshot.inventory.QueueFrontier() {
		if frontier.Pool != poolID || frontier.Lane != lane {
			continue
		}
		queueDocument, err := snapshot.inventory.ByID(frontier.Queue)
		if err != nil {
			return dispatchCandidate{}, err
		}
		queue := queueDocument.Record.(*research.Queue)
		candidateID := dispatchID(frontier, queue.Revision, snapshot.scope)
		if candidateID != selection.ID {
			continue
		}
		planDocument, err := snapshot.inventory.ByID(frontier.Entry.Plan)
		if err != nil {
			return dispatchCandidate{}, err
		}
		plan := planDocument.Record.(*research.Plan)
		if plan.Schema != research.SchemaPlanV2 || plan.State != research.PlanQueued {
			return dispatchCandidate{}, errors.New("selected Plan is not a queued v2 Plan")
		}
		if len(plan.Resources) != 1 {
			return dispatchCandidate{}, errors.New("autonomous dispatch requires exactly one ResourcePool need; use a composite pool")
		}
		runtimePlan, configured := snapshot.runtime.plans[plan.ID]
		if !configured {
			return dispatchCandidate{}, fmt.Errorf("Plan %s has no runtime executable contract", plan.ID)
		}
		poolRuntime, configured := snapshot.runtime.pools[poolID]
		if !configured {
			return dispatchCandidate{}, fmt.Errorf("ResourcePool %s has no runtime Pueue binding", poolID)
		}
		partitionIndex := -1
		for index := range queue.Partitions {
			partition := &queue.Partitions[index]
			if partition.Pool == poolID && partition.Lane == lane && len(partition.Entries) > 0 && partition.Entries[0].Plan == plan.ID {
				partitionIndex = index
				break
			}
		}
		if partitionIndex < 0 {
			return dispatchCandidate{}, errors.New("selected queue frontier changed")
		}
		return dispatchCandidate{
			frontier: frontier, queueDocument: queueDocument, planDocument: planDocument, queue: queue, plan: plan,
			runtime: runtimePlan, poolRuntime: poolRuntime, dispatchID: candidateID,
			weight: planWeight(plan, poolID), partitionIndex: partitionIndex,
		}, nil
	}
	return dispatchCandidate{}, controller.ErrNoWork
}

func (adapter Adapter) preparedForAttempt(snapshot canonicalSnapshot, document *record.Document) (controller.Prepared, error) {
	attempt := document.Record.(*research.Attempt)
	runDocument, err := snapshot.inventory.ByID(attempt.Run)
	if err != nil {
		return controller.Prepared{}, err
	}
	run := runDocument.Record.(*research.Run)
	var plan *research.Plan
	for _, candidate := range snapshot.inventory.OfKind(research.KindPlan) {
		value := candidate.Record.(*research.Plan)
		if value.ResultingExperiment == run.Experiment {
			plan = value
			break
		}
	}
	if plan == nil {
		return controller.Prepared{}, errors.New("canonical Attempt has no originating Plan")
	}
	if err := dispatchStillActive(snapshot.inventory, plan, attempt); err != nil {
		return controller.Prepared{}, err
	}
	runtimePlan, configured := snapshot.runtime.plans[plan.ID]
	if !configured {
		return controller.Prepared{}, fmt.Errorf("Plan %s has no runtime executable contract", plan.ID)
	}
	if run.ConfigDigest != runtimePlan.digest || attempt.CWD != runtimePlan.CWD ||
		attempt.BaseCommit != runtimePlan.BaseCommit || attempt.HeadCommit != runtimePlan.HeadCommit ||
		!sameStrings(attempt.ChangeSet, runtimePlan.ChangeSet) ||
		!sameStrings(attempt.Argv, append([]string{runtimePlan.Executable}, runtimePlan.Argv...)) {
		return controller.Prepared{}, errors.New("runtime contract drifted after canonical dispatch preparation")
	}
	poolRuntime, configured := snapshot.runtime.pools[attempt.Pool]
	if !configured {
		return controller.Prepared{}, fmt.Errorf("ResourcePool %s has no runtime Pueue binding", attempt.Pool)
	}
	if group, label, routed := attemptRoute(attempt); !routed || group != poolRuntime.PueueGroup || label != poolRuntime.LabelPrefix+attempt.DispatchID {
		return controller.Prepared{}, errors.New("Pueue routing drifted after canonical dispatch preparation")
	}
	return adapter.buildPrepared(plan, attempt, runtimePlan, poolRuntime)
}

func (adapter Adapter) buildPrepared(plan *research.Plan, attempt *research.Attempt, runtime validatedPlanRuntime, pool PoolRuntime) (controller.Prepared, error) {
	if adapter.WorkerExecutable == "" {
		return controller.Prepared{}, errors.New("worker executable is required")
	}
	jobID := jobID(attempt.DispatchID)
	canonicalScope, err := ScopeID(adapter.RepositoryRoot)
	if err != nil {
		return controller.Prepared{}, err
	}
	workerArgs := append(append([]string(nil), adapter.WorkerArgs...), jobID)
	if _, err := pueue.WorkerCommand(adapter.WorkerExecutable, workerArgs); err != nil {
		return controller.Prepared{}, fmt.Errorf("worker command: %w", err)
	}
	label := pool.LabelPrefix + attempt.DispatchID
	if _, routedLabel, routed := attemptRoute(attempt); routed {
		label = routedLabel
	}
	if !validToken(label) {
		return controller.Prepared{}, errors.New("derived Pueue label is invalid")
	}
	payload, err := json.Marshal(worker.Workload{
		Schema: worker.JobSchema, AttemptID: attempt.ID.String(), Executable: runtime.Executable,
		Args: append([]string(nil), runtime.Argv...), CWD: runtime.absoluteCWD, Timeout: runtime.Timeout,
		AllowedEnv: append([]string(nil), runtime.AllowedEnv...), SecretEnv: append([]string(nil), runtime.SecretEnv...),
		RepositoryRoot: runtime.repositoryRoot, ExpectedOutputs: append([]string(nil), runtime.ExpectedOutputs...),
		BaseCommit: runtime.BaseCommit, HeadCommit: runtime.HeadCommit, ChangeSet: append([]string(nil), runtime.ChangeSet...),
		RuntimeConfigPath: runtime.runtimeConfigPath,
	})
	if err != nil {
		return controller.Prepared{}, err
	}
	return controller.Prepared{
		Job: operation.JobInput{
			ID: jobID, IdempotencyKey: "dispatch:" + attempt.DispatchID, Kind: "experiment.run", Role: "execute",
			SubjectID: attempt.ID.String(), CanonicalScope: canonicalScope, Pool: attempt.Pool.String(), Lane: string(attempt.Lane), Units: planUnits(plan, attempt.Pool), Profile: "canonical-runtime",
			Payload: payload, MaxAttempts: 1,
		},
		Priority: priority(plan.Priority), Worker: adapter.WorkerExecutable, Args: workerArgs,
		CWD: runtime.absoluteCWD, Label: label, Units: planUnits(plan, attempt.Pool), AllowedEnv: append([]string(nil), runtime.AllowedEnv...),
	}, nil
}

// ScopeID identifies one canonical checkout inside a Git-common operational
// store. Linked worktrees share SQLite but must acknowledge terminal jobs only
// for their own canonical inventory.
func ScopeID(repositoryRoot string) (string, error) {
	canonical, err := pathx.Canonical(repositoryRoot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(canonical))
	return "scope-" + hex.EncodeToString(digest[:16]), nil
}

func findAttemptByDispatch(inventory *record.Inventory, dispatch string) *record.Document {
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		if attempt.Schema == research.SchemaAttemptV2 && attempt.DispatchID == dispatch {
			return document
		}
	}
	return nil
}

func originatingPlan(inventory *record.Inventory, attempt *research.Attempt) (*research.Plan, error) {
	if inventory == nil || attempt == nil {
		return nil, errors.New("canonical Attempt is missing")
	}
	runDocument, err := inventory.ByID(attempt.Run)
	if err != nil {
		return nil, err
	}
	run := runDocument.Record.(*research.Run)
	for _, candidate := range inventory.OfKind(research.KindPlan) {
		plan := candidate.Record.(*research.Plan)
		if plan.ResultingExperiment == run.Experiment {
			return plan, nil
		}
	}
	return nil, errors.New("canonical Attempt has no originating Plan")
}

func dispatchStillActive(inventory *record.Inventory, plan *research.Plan, attempt *research.Attempt) error {
	if inventory == nil || plan == nil || attempt == nil || plan.State != research.PlanStarted {
		return errors.New("prepared dispatch no longer belongs to a started Plan")
	}
	runDocument, err := inventory.ByID(attempt.Run)
	if err != nil {
		return err
	}
	run := runDocument.Record.(*research.Run)
	if plan.ResultingExperiment != run.Experiment {
		return errors.New("prepared dispatch Plan no longer points to its Experiment")
	}
	experimentDocument, err := inventory.ByID(run.Experiment)
	if err != nil {
		return err
	}
	if experimentDocument.Record.(*research.Experiment).Lifecycle != research.LifecycleActive {
		return errors.New("prepared dispatch Experiment is no longer active")
	}
	return nil
}

func dispatchEnabled(policy *research.Policy) bool {
	return policy != nil && (policy.Autonomy == research.AutonomyAssisted || policy.Autonomy == research.AutonomyLimited)
}

func dispatchID(frontier record.FrontierEntry, revision uint64, scope string) string {
	framed := strings.Join([]string{
		"exp-dispatch-v2", scope, frontier.Queue.String(), strconv.FormatUint(revision, 10), frontier.Pool.String(),
		string(frontier.Lane), frontier.Entry.Plan.String(), frontier.Entry.PlanRevision,
	}, "\x00")
	digest := sha256.Sum256([]byte(framed))
	return "dispatch-" + hex.EncodeToString(digest[:16])
}

func jobID(dispatch string) string { return "job-" + dispatch }

func planWeight(plan *research.Plan, pool research.ID) float64 {
	for _, need := range plan.Resources {
		if need.Pool == pool {
			weight := float64(need.Units) * need.EstimatedHours
			if weight > 0 && !math.IsInf(weight, 0) && !math.IsNaN(weight) {
				return weight
			}
		}
	}
	return 1
}

func planUnits(plan *research.Plan, pool research.ID) int {
	if plan == nil {
		return 1
	}
	for _, need := range plan.Resources {
		if need.Pool == pool && need.Units > 0 {
			if need.Units > uint64(math.MaxInt) {
				return math.MaxInt
			}
			return int(need.Units)
		}
	}
	return 1
}

func priority(value research.Priority) int {
	switch value {
	case research.PriorityP1:
		return 100
	case research.PriorityP2:
		return 50
	case research.PriorityP3:
		return 0
	default:
		return -10
	}
}

func designFor(plan *research.Plan, runtime validatedPlanRuntime, now time.Time) research.Design {
	factor := plan.PrimaryCluster
	if plan.Classification != nil && plan.Classification.Component != "" {
		factor = plan.Classification.Component
	}
	if factor == "" {
		factor = "implementation"
	}
	return research.Design{
		Question:   "Will " + plan.Title + " improve " + plan.ExpectedPayoff.Metric + " under the registered runtime contract?",
		Hypothesis: plan.ExpectedPayoff.Summary, Kind: research.ExperimentSingleFactor, PrimaryFactor: factor,
		SecondaryFactors: []string{}, Baseline: "Committed baseline " + runtime.BaseCommit[:12],
		ComparabilitySpec: "Use the canonical Plan dependencies, resources, and registered runtime contract without unregistered changes.",
		SuccessCriteria:   []string{"Meet the canonical Plan expected payoff for " + plan.ExpectedPayoff.Metric + " in " + plan.ExpectedPayoff.Unit + "."},
		DecisionRule:      "Advance only when comparable evaluation meets the registered success criterion.", DesignLockedAt: &now,
	}
}

func common(schema research.Schema, id research.ID, title string, tags []string, now time.Time) research.Common {
	return research.Common{Schema: schema, ID: id, Title: title, CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), tags...)}
}

func generatedBody(kind string, plan *research.Plan, dispatch string) string {
	return "\n# " + kind + ": " + plan.Title + "\n\nPrepared from canonical Plan " + plan.ID.String() + " for dispatch " + dispatch + ".\n"
}

func (adapter Adapter) newID(kind research.Kind, now time.Time) (research.ID, error) {
	generator := adapter.GenerateUUID
	if generator == nil {
		generator = research.DefaultUUIDGenerator
	}
	value, err := generator(now)
	if err != nil {
		return research.ID{}, fmt.Errorf("generate %s ID: %w", kind, err)
	}
	id, err := research.NewID(kind, value)
	if err != nil {
		return research.ID{}, err
	}
	return id, nil
}

func (adapter Adapter) now() time.Time {
	if adapter.Clock == nil {
		return time.Now().UTC()
	}
	return adapter.Clock().UTC()
}

func (adapter Adapter) atLeastCreated(created time.Time) time.Time {
	now := adapter.now()
	if now.Before(created) {
		return created
	}
	return now
}

func schedulerReference(nativeID string, observed time.Time) research.ExternalRef {
	value := observed
	return research.ExternalRef{
		Role: research.ExternalScheduler, Provider: "pueue", Context: LocalPueueContext, NativeKind: "task", NativeID: nativeID, ObservedAt: &value,
	}
}

func pueueReference(attempt *research.Attempt) (research.ExternalRef, bool) {
	for _, reference := range attempt.ExternalRefs {
		if reference.Role == research.ExternalScheduler && reference.Provider == "pueue" && reference.NativeKind == "task" {
			return reference, true
		}
	}
	return research.ExternalRef{}, false
}

func observedAttemptState(value string) (research.AttemptState, bool, bool) {
	switch value {
	case "queued":
		return research.AttemptQueued, false, true
	case "blocked":
		return research.AttemptBlocked, false, true
	case "running":
		return research.AttemptRunning, false, true
	case "succeeded":
		return research.AttemptSucceeded, true, true
	case "failed", "dependency_failed":
		return research.AttemptFailed, true, true
	case "cancelled":
		return research.AttemptCancelled, true, true
	default:
		return "", false, false
	}
}

func terminalAttempt(state research.AttemptState) bool {
	switch state {
	case research.AttemptSucceeded, research.AttemptFailed, research.AttemptCancelled, research.AttemptTimedOut, research.AttemptPreempted, research.AttemptOutOfMemory:
		return true
	default:
		return false
	}
}

func attemptRoute(attempt *research.Attempt) (string, string, bool) {
	if attempt == nil || attempt.Extensions == nil {
		return "", "", false
	}
	values, found := attempt.Extensions[attemptExtension]
	if !found {
		return "", "", false
	}
	group, groupOK := values["pueue_group"].(string)
	label, labelOK := values["pueue_label"].(string)
	return group, label, groupOK && labelOK && validToken(group) && validToken(label)
}

func sameStrings(left, right []string) bool {
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
