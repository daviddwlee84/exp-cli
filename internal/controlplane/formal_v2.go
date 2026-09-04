package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/controller"
	"github.com/daviddwlee84/exp-cli/internal/mlflow"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/worker"
)

func (adapter Adapter) prepareV2(ctx context.Context, snapshot canonicalSnapshot, candidate dispatchCandidate, selection controller.Selection) (controller.Prepared, error) {
	if candidate.runtime.V2 == nil {
		return controller.Prepared{}, errors.New("Source-aware preparation requires an exp.runtime/v2 Plan route")
	}
	info, err := adapter.requireCanonicalWorkspace(ctx)
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
	resolved, err := adapter.resolvePlanRuntimeV2(
		ctx, info, snapshot.inventory, *candidate.runtime.V2, candidate.runtime.digest,
		snapshot.runtime.configDigest, now, attemptID, candidate.plan.Title+" formal runtime", nil, true,
	)
	if err != nil {
		return controller.Prepared{}, fmt.Errorf("prepare Source-aware runtime: %w", errors.Join(ErrRuntimeSourceUnavailable, err))
	}
	rollbackFailure := func(cause error) (controller.Prepared, error) {
		return controller.Prepared{}, errors.Join(cause, adapter.rollbackManagedPreparations(context.WithoutCancel(ctx), resolved.preparations))
	}
	primary := resolved.executionSource()
	if primary == nil || primary.snapshot.State != research.SourceSnapshotClean {
		return rollbackFailure(errors.New("formal runtime did not produce one clean execution Source snapshot"))
	}

	design := designForV2(candidate.plan, resolved, now)
	designDigest, err := research.DesignDigest(design)
	if err != nil {
		return rollbackFailure(err)
	}
	design.DesignDigest = designDigest
	experiment := &research.Experiment{
		Common:    common(research.SchemaExperimentV2, experimentID, candidate.plan.Title, candidate.plan.Tags, now),
		Lifecycle: research.LifecycleActive, Design: design, Parents: experimentParents(snapshot.inventory, candidate.plan),
		CandidateInputs: []research.ID{},
	}
	run := &research.Run{
		Common:     common(research.SchemaRun, runID, candidate.plan.Title, candidate.plan.Tags, now),
		Experiment: experimentID, Role: research.RunCandidate, Objective: candidate.plan.ExpectedPayoff.Summary,
		ConfigDigest: resolved.configDigest, Seeds: []int64{},
		ExpectedOutputs: append([]string{}, resolved.contract.ExpectedOutputs...),
	}
	attempt := &research.Attempt{
		Common: common(research.SchemaAttemptV3, attemptID, candidate.plan.Title, candidate.plan.Tags, now),
		Run:    runID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "pueue",
		CWD:             resolved.contract.CWD,
		Argv:            append([]string{resolved.contract.Executable}, resolved.contract.Argv...),
		ExecutionSource: resolved.contract.Execution.Source,
		SourceSnapshots: resolvedSnapshots(resolved), ExternalRefs: []research.ExternalRef{},
		Provenance: &research.Provenance{
			CapturedAt: now, GitCommit: primary.snapshot.HeadCommit, GitDirty: false,
			ConfigDigest: resolved.configDigest, Reproducibility: research.ReproducibilityExact,
		},
		Pool: candidate.frontier.Pool, Queue: candidate.frontier.Queue,
		QueueRevision: candidate.queue.Revision, Lane: candidate.frontier.Lane, DispatchID: candidate.dispatchID,
		Extensions: research.Extensions{attemptExtension: {
			"pueue_group":    candidate.poolRuntime.PueueGroup,
			"pueue_label":    candidate.poolRuntime.LabelPrefix + candidate.dispatchID,
			"runtime_digest": resolved.configDigest,
			"runtime_schema": RuntimeSchemaV2,
		}},
	}
	if resolved.mlflowProfile != nil {
		table := attempt.Extensions[attemptExtension]
		table["mlflow_profile"] = resolved.mlflowProfile.Name
		table["mlflow_context"] = resolved.mlflowProfile.Context
	}
	if err := research.Validate(attempt); err != nil {
		return rollbackFailure(fmt.Errorf("validate Source-aware formal Attempt: %w", err))
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
	transaction, err := adapter.Store.Transact(ctx, request)
	if err != nil {
		if refreshed, loadErr := adapter.snapshot(ctx); loadErr == nil {
			if existing := findAttemptByDispatch(refreshed.inventory, selection.ID); existing != nil {
				return adapter.preparedForAttempt(ctx, refreshed, existing)
			}
		}
		if transaction == nil {
			return rollbackFailure(err)
		}
		// A non-nil transaction may already own published paths and must be
		// recovered canonically rather than losing its managed workspaces.
		return controller.Prepared{}, err
	}
	return adapter.buildPreparedV2(candidate.plan, attempt, resolved, candidate.poolRuntime, snapshot.scope, info)
}

func (adapter Adapter) preparedForAttemptV2(ctx context.Context, snapshot canonicalSnapshot, plan *research.Plan, run *research.Run, attempt *research.Attempt, runtime validatedPlanRuntime, pool PoolRuntime) (controller.Prepared, error) {
	if runtime.V2 == nil || attempt.Schema != research.SchemaAttemptV3 {
		return controller.Prepared{}, errors.New("prepared Source-aware dispatch has incompatible schema")
	}
	info, err := adapter.requireCanonicalWorkspace(ctx)
	if err != nil {
		return controller.Prepared{}, err
	}
	expected, err := snapshotMap(attempt.SourceSnapshots)
	if err != nil {
		return controller.Prepared{}, err
	}
	resolved, err := adapter.resolvePlanRuntimeV2(
		ctx, info, snapshot.inventory, *runtime.V2, runtime.digest,
		snapshot.runtime.configDigest, attempt.CreatedAt, attempt.ID, attempt.Title+" formal runtime", expected, false,
	)
	if err != nil {
		return controller.Prepared{}, fmt.Errorf("revalidate prepared Source-aware runtime: %w", errors.Join(ErrRuntimeSourceUnavailable, err))
	}
	primary := resolved.executionSource()
	if primary == nil || attempt.ExecutionSource != primary.source.ID || attempt.CWD != resolved.contract.CWD ||
		!sameStrings(attempt.Argv, append([]string{resolved.contract.Executable}, resolved.contract.Argv...)) ||
		!reflect.DeepEqual(attempt.SourceSnapshots, resolvedSnapshots(resolved)) || run.ConfigDigest != resolved.configDigest ||
		!formalMLflowProfileMatches(attempt, resolved.mlflowProfile) ||
		attempt.Provenance == nil || attempt.Provenance.GitCommit != primary.snapshot.HeadCommit || attempt.Provenance.GitDirty ||
		attempt.Provenance.ConfigDigest != resolved.configDigest || attempt.Provenance.Reproducibility != research.ReproducibilityExact {
		return controller.Prepared{}, errors.New("runtime contract drifted after Source-aware canonical dispatch preparation")
	}
	return adapter.buildPreparedV2(plan, attempt, resolved, pool, snapshot.scope, info)
}

func (adapter Adapter) buildPreparedV2(plan *research.Plan, attempt *research.Attempt, runtime resolvedPlanRuntimeV2, pool PoolRuntime, canonicalScope string, info *project.Info) (controller.Prepared, error) {
	if adapter.WorkerExecutable == "" {
		return controller.Prepared{}, errors.New("worker executable is required")
	}
	projectRecord := info.Project()
	if projectRecord == nil || projectRecord.ProjectID.IsZero() {
		return controller.Prepared{}, errors.New("canonical Project identity is required")
	}
	primary := runtime.executionSource()
	if primary == nil {
		return controller.Prepared{}, errors.New("execution Source checkout is missing")
	}
	jobID := jobID(attempt.DispatchID)
	workerArgs := append(append([]string{}, adapter.WorkerArgs...), jobID,
		"--canonical-root", adapter.canonicalRepositoryRoot(),
		"--project", projectRecord.ProjectID.String(), "--scope", canonicalScope,
	)
	if _, err := pueue.WorkerCommandV2(adapter.WorkerExecutable, workerArgs); err != nil {
		return controller.Prepared{}, fmt.Errorf("worker command: %w", err)
	}
	bindings := make([]worker.SourceBinding, len(runtime.sources))
	for index := range runtime.sources {
		source := runtime.sources[index]
		bindings[index] = worker.SourceBinding{
			Checkout: source.route.Checkout, RepositoryRoot: source.repositoryRoot,
			SemanticRoot: source.semanticRoot, RegisteredGitCommonDir: source.registeredGitCommonDir,
			RegisteredGitCommonIdentity: source.registeredGitCommonIdentity,
			ReadOnly:                    source.route.ReadOnly, Snapshot: worker.BindSourceSnapshot(source.snapshot),
		}
	}
	checkoutIdentity := worker.SourceCheckoutIdentity(projectRecord.ProjectID.String(), canonicalScope, attempt.ExecutionSource.String(), bindings)
	var workloadProfile *mlflow.WorkloadProfile
	if runtime.mlflowProfile != nil {
		value := runtime.mlflowProfile.WorkloadProfile
		value.Environment = append([]mlflow.EnvironmentBinding{}, value.Environment...)
		value.DefaultMetrics = append([]string{}, value.DefaultMetrics...)
		workloadProfile = &value
	}
	payload, err := json.Marshal(worker.Workload{
		Schema: worker.SourceJobSchema, AttemptID: attempt.ID.String(),
		ProjectID: projectRecord.ProjectID.String(), CanonicalScope: canonicalScope,
		ExecutionSource: attempt.ExecutionSource.String(), CheckoutIdentity: checkoutIdentity,
		Sources: bindings, Executable: runtime.contract.Executable,
		Args: append([]string{}, runtime.contract.Argv...), CWD: runtime.absoluteCWD,
		Timeout: runtime.contract.Timeout, AllowedEnv: append([]string{}, runtime.contract.AllowedEnv...),
		SecretEnv: []string{}, MLflow: workloadProfile, RepositoryRoot: primary.repositoryRoot, OutputRoot: runtime.outputRoot,
		BaseCommit: primary.snapshot.BaseCommit, HeadCommit: primary.snapshot.HeadCommit,
		ChangeSet:       append([]string{}, primary.snapshot.ChangeSet...),
		ExpectedOutputs: append([]string{}, runtime.contract.ExpectedOutputs...),
	})
	if err != nil {
		return controller.Prepared{}, err
	}
	label := pool.LabelPrefix + attempt.DispatchID
	if _, routedLabel, routed := attemptRoute(attempt); routed {
		label = routedLabel
	}
	if !validToken(label) {
		return controller.Prepared{}, errors.New("derived Pueue label is invalid")
	}
	return controller.Prepared{
		Job: operation.JobInput{
			ID: jobID, IdempotencyKey: "dispatch:" + attempt.DispatchID, Kind: "experiment.run", Role: "execute",
			SubjectID: attempt.ID.String(), CanonicalScope: canonicalScope, Pool: attempt.Pool.String(), Lane: string(attempt.Lane),
			Units: planUnits(plan, attempt.Pool), Profile: "source-runtime-v2", Payload: payload, MaxAttempts: 1,
		},
		Priority: priority(plan.Priority), Worker: adapter.WorkerExecutable, Args: workerArgs,
		CWD: adapter.canonicalRepositoryRoot(), Label: label, Units: planUnits(plan, attempt.Pool),
		AllowedEnv: append([]string{}, runtime.contract.AllowedEnv...), QuotedWorkerArgs: true,
	}, nil
}

func formalMLflowProfileMatches(attempt *research.Attempt, profile *mlflow.ResolvedProfile) bool {
	if attempt == nil || attempt.Extensions == nil {
		return profile == nil
	}
	table := attempt.Extensions[attemptExtension]
	name, _ := table["mlflow_profile"].(string)
	contextName, _ := table["mlflow_context"].(string)
	if profile == nil {
		return name == "" && contextName == ""
	}
	return name == profile.Name && contextName == profile.Context
}

// RevalidateSubmission is called only when no scheduler task exists for the
// outbox route. V1 jobs retain their historical dispatch behavior; v2 jobs must
// still match canonical state and every clean Source snapshot.
func (adapter Adapter) RevalidateSubmission(ctx context.Context, selection controller.Selection, job operation.Job) error {
	var selector struct {
		Schema string `json:"schema_version"`
	}
	if err := json.Unmarshal(job.Payload, &selector); err != nil {
		return err
	}
	if selector.Schema == worker.JobSchema {
		return nil
	}
	if selector.Schema != worker.SourceJobSchema {
		return fmt.Errorf("unsupported worker job schema %q", selector.Schema)
	}
	snapshot, err := adapter.snapshot(ctx)
	if err != nil {
		return errors.Join(controller.ErrSubmissionBlocked, err)
	}
	document := findAttemptByDispatch(snapshot.inventory, selection.ID)
	if document == nil || document.Record.(*research.Attempt).Schema != research.SchemaAttemptV3 {
		return errors.Join(controller.ErrSubmissionBlocked, errors.New("Source-aware canonical Attempt is missing"))
	}
	prepared, err := adapter.preparedForAttempt(ctx, snapshot, document)
	if err != nil {
		return errors.Join(controller.ErrSubmissionBlocked, err)
	}
	if !samePreparedJob(prepared.Job, job.JobInput) || prepared.Job.ID != job.ID || job.State != operation.JobRunning || job.FencingToken <= 0 {
		return errors.Join(controller.ErrSubmissionBlocked, errors.New("Source-aware operational job drifted from canonical preparation"))
	}
	return nil
}

func samePreparedJob(left, right operation.JobInput) bool {
	return left.ID == right.ID && left.IdempotencyKey == right.IdempotencyKey && left.Kind == right.Kind &&
		left.Role == right.Role && left.SubjectID == right.SubjectID && left.CanonicalScope == right.CanonicalScope &&
		left.Pool == right.Pool && left.Lane == right.Lane && left.Units == right.Units && left.Profile == right.Profile &&
		left.MaxAttempts == right.MaxAttempts && sameJSONPayload(left.Payload, right.Payload)
}

func sameJSONPayload(left, right []byte) bool {
	decode := func(value []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	}
	leftValue, leftErr := decode(left)
	rightValue, rightErr := decode(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftValue, rightValue)
}

// SubmissionBlocked advances only operational canonical state. It never creates
// a terminal observation or scientific conclusion.
func (adapter Adapter) SubmissionBlocked(ctx context.Context, selection controller.Selection, job operation.Job) error {
	inventory, _, err := adapter.canonical(ctx)
	if err != nil {
		return err
	}
	document := findAttemptByDispatch(inventory, selection.ID)
	if document == nil {
		return errors.New("blocked submission has no canonical Attempt")
	}
	attempt := document.Record.(*research.Attempt)
	if attempt.Schema != research.SchemaAttemptV3 || attempt.ID.String() != job.SubjectID || job.ID != jobID(attempt.DispatchID) ||
		job.CanonicalScope == "" || job.State != operation.JobUnknown {
		return errors.New("blocked operational job does not match Source-aware canonical Attempt")
	}
	if attempt.State == research.AttemptBlocked && attempt.StateReason == "Runtime Source unavailable before scheduler submission" {
		return nil
	}
	if attempt.State != research.AttemptPlanned && attempt.State != research.AttemptQueued {
		return fmt.Errorf("Attempt %s cannot become blocked from state %s", attempt.ID, attempt.State)
	}
	replacement := document.Clone()
	updated := replacement.Record.(*research.Attempt)
	updated.State = research.AttemptBlocked
	updated.StateReason = "Runtime Source unavailable before scheduler submission"
	updated.UpdatedAt = adapter.atLeastCreated(updated.CreatedAt)
	_, err = adapter.Store.Transact(ctx, record.TransactionRequest{Operation: "dispatch.blocked", Changes: []record.TransactionChange{{
		Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: document.Revision,
	}}})
	return err
}

func designForV2(plan *research.Plan, runtime resolvedPlanRuntimeV2, now time.Time) research.Design {
	factor := plan.PrimaryCluster
	if plan.Classification != nil && plan.Classification.Component != "" {
		factor = plan.Classification.Component
	}
	if factor == "" {
		factor = "implementation"
	}
	primary := runtime.executionSource()
	base := "registered Source baseline"
	if primary != nil && len(primary.snapshot.BaseCommit) >= 12 {
		base = "Committed Source baseline " + primary.snapshot.BaseCommit[:12]
	}
	locked := now.UTC()
	return research.Design{
		Question:   "Will " + plan.Title + " improve " + plan.ExpectedPayoff.Metric + " under the registered Source runtime contract?",
		Hypothesis: plan.ExpectedPayoff.Summary, Kind: research.ExperimentSingleFactor, PrimaryFactor: factor,
		SecondaryFactors: []string{}, Baseline: base,
		ComparabilitySpec: "Use the canonical Plan dependencies, resources, exact clean SourceSnapshots, and trusted runtime contract without unregistered changes.",
		SuccessCriteria:   []string{"Meet the canonical Plan expected payoff for " + plan.ExpectedPayoff.Metric + " in " + plan.ExpectedPayoff.Unit + "."},
		DecisionRule:      "Advance only when comparable evaluation meets the registered success criterion.", DesignLockedAt: &locked,
	}
}
