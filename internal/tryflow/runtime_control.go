package tryflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/experimentgit"
	"github.com/daviddwlee84/exp-cli/internal/exploration"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/worker"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

// StatusRequest observes every Try or one selected Try without starting work.
type StatusRequest struct {
	Selection
	Try    string
	Limit  int
	Offset int
}

// AttemptStatus combines canonical state with optional local observations. Error
// is intentionally a safe summary label; callers should not expose raw backend
// errors that may contain host paths.
type AttemptStatus struct {
	Attempt          *record.Document
	Job              *operation.Job
	Marker           *worker.Terminal
	Workspace        *workspacebackend.Inspection
	WorkspacePresent bool
	CleanupCompleted bool
	Recoverable      bool
	Active           bool
	LeaseExpired     bool
	Uncertain        bool
	Status           string
	Error            string
}

// TryStatus is one canonical Try and its chronologically ordered Attempts.
type TryStatus struct {
	Try      *record.Document
	Attempts []AttemptStatus
}

// StatusResult remains useful when optional operational state is missing.
type StatusResult struct {
	Project *project.Info
	Tries   []TryStatus
	Total   int
	Limit   int
	Offset  int
	Partial bool
}

// CleanupRequest explicitly asks to remove manager-owned local resources for a
// Try. The CLI owns human confirmation; this method still fails closed.
type CleanupRequest struct {
	Selection
	Try string
}

// CleanupItem reports one Attempt without exposing local paths.
type CleanupItem struct {
	Attempt          *record.Document
	RemovedWorkspace bool
	RemovedBundle    bool
	AlreadyCompleted bool
	Refused          bool
	Reason           string
}

// CleanupResult can be partial when one Attempt is unsafe while another was
// removed successfully.
type CleanupResult struct {
	Project      *project.Info
	Try          *record.Document
	Items        []CleanupItem
	Transactions []string
	Partial      bool
}

// HandoffRequest explicitly selects one canonical Try Attempt. Handoff never
// changes canonical records or trusts provider-local identifiers.
type HandoffRequest struct {
	Selection
	Try     string
	Attempt string
}

// HandoffResult contains only canonical identity and exp-owned inspection.
type HandoffResult struct {
	Project    *project.Info
	Try        *record.Document
	Attempt    *record.Document
	Inspection workspacebackend.Inspection
}

// ReconcileRequest records an explicit human abandonment of one uncertain
// Attempt after marker reconciliation has proved no terminal evidence exists.
type ReconcileRequest struct {
	Selection
	Try              string
	Attempt          string
	Reason           string
	AbandonUncertain bool
}

// Reconcile imports a late marker when present, otherwise it terminalizes only
// an explicitly selected unknown Attempt after rejecting every live lease.
func (direct *Direct) Reconcile(ctx context.Context, request ReconcileRequest) (*ExecutionResult, error) {
	if err := direct.validateDependencies(); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if err := direct.validateOperationalAvailability(); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if !request.AbandonUncertain || strings.TrimSpace(request.Attempt) == "" || strings.TrimSpace(request.Reason) == "" {
		return nil, runtimeFailure(StageResolve, nil, false, false, errors.New("uncertain Attempt reconciliation requires an explicit Attempt, reason, and abandonment confirmation"))
	}
	resolved, store, inventory, tryDocument, err := direct.resolveTry(ctx, request.Selection, request.Try)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if tryDocument.Record.(*research.Try).State != research.TryOpen {
		return nil, runtimeFailure(StageResolve, nil, false, false, fmt.Errorf("Try is not open: %w", ErrPrecondition))
	}
	attemptDocument, err := inventory.Resolve(strings.TrimSpace(request.Attempt), research.KindAttempt)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	attempt := attemptDocument.Record.(*research.Attempt)
	if attempt.Try != tryDocument.Record.(*research.Try).ID {
		return nil, runtimeFailure(StageResolve, nil, false, false, fmt.Errorf("Attempt does not belong to selected Try: %w", ErrPrecondition))
	}
	if recovered, recoverErr := direct.reconcileAttemptMarkers(ctx, request.Selection, resolved.Project, store, tryDocument, []*record.Document{attemptDocument}); recoverErr != nil {
		return recovered, recoverErr
	} else if recovered != nil && recovered.Recovered {
		recovered.Stage = StageComplete
		return terminalOutcome(recovered)
	}
	if attempt.State != research.AttemptUnknown {
		return nil, runtimeFailure(StageResolve, nil, false, false, fmt.Errorf("Attempt %s is %s, not unknown: %w", attempt.ID, attempt.State, ErrPrecondition))
	}
	result := &ExecutionResult{Stage: StageOperationalClaim, Project: resolved.Project, Context: resolved, Try: tryDocument, Attempt: attemptDocument}
	operations, openErr := direct.dependencies.OpenOperations(ctx, resolved.Project)
	if openErr != nil {
		return result, runtimeFailure(StageOperationalClaim, result, true, true, openErr)
	}
	defer operations.Close()
	job, jobErr := operations.GetJob(ctx, directJobID(attempt.ID))
	if jobErr != nil && !errors.Is(jobErr, operation.ErrNotFound) {
		return result, runtimeFailure(StageOperationalClaim, result, true, true, jobErr)
	}
	if jobErr == nil {
		result.Job = &job
		switch job.State {
		case operation.JobRunning:
			if job.LeaseExpiresAt != nil && direct.now().Before(job.LeaseExpiresAt.UTC()) {
				return result, runtimeFailure(StageOperationalClaim, result, false, true, ErrExecutionInProgress)
			}
			payload := json.RawMessage(`{"schema_version":"exp.try-reconciliation/v1"}`)
			finished, finishErr := operations.FinishJob(context.WithoutCancel(ctx), job.ID, job.FencingToken, operation.JobCancelled, payload, "uncertain execution explicitly abandoned")
			if errors.Is(finishErr, operation.ErrFenced) {
				if recovered, recoverErr := direct.reconcileAttemptMarkers(context.WithoutCancel(ctx), request.Selection, resolved.Project, store, tryDocument, []*record.Document{attemptDocument}); recoverErr != nil {
					return recovered, recoverErr
				} else if recovered != nil && recovered.Recovered {
					recovered.Stage = StageComplete
					return terminalOutcome(recovered)
				}
				job, finishErr = operations.GetJob(context.WithoutCancel(ctx), job.ID)
				if finishErr == nil {
					result.Job = &job
				}
				if finishErr != nil || job.State != operation.JobCancelled && job.State != operation.JobUnknown {
					return result, runtimeFailure(StageOperationalClaim, result, true, true, errors.Join(operation.ErrFenced, finishErr, ErrExecutionUncertain))
				}
			} else if finishErr != nil {
				return result, runtimeFailure(StageOperationalClaim, result, true, true, finishErr)
			} else {
				result.Job = &finished
			}
		case operation.JobCancelled, operation.JobUnknown:
			// These durable states prove there is no claim eligible to execute.
		default:
			return result, runtimeFailure(StageOperationalClaim, result, true, true, fmt.Errorf("operational job is %s and was not cancelled: %w", job.State, ErrExecutionUncertain))
		}
	}
	now := direct.after(attempt.UpdatedAt)
	terminal := &research.Terminal{Source: "human", ObservedAt: now, EndedAt: now}
	updated, transactionID, updateErr := direct.updateAttemptState(context.WithoutCancel(ctx), store, attemptDocument, research.AttemptCancelled, request.Reason, terminal, nil, nil)
	if transactionID != "" {
		result.Transactions = append(result.Transactions, transactionID)
	}
	if updateErr != nil {
		return result, runtimeFailure(StageCanonicalDone, result, true, true, updateErr)
	}
	result.Attempt = updated
	result.Stage = StageCanonicalDone
	if err := direct.refresh(context.WithoutCancel(ctx), resolved.Project, store); err != nil {
		result.RecoveryAction = "exp render"
		return result, runtimeFailure(StageProjection, result, true, true, err)
	}
	result.Stage = StageComplete
	return result, nil
}

// Resume reconciles or runs the latest Attempt. It never creates a second
// Attempt and never executes a running/unknown job without a terminal marker.
func (direct *Direct) Resume(ctx context.Context, request ResumeRequest) (*ExecutionResult, error) {
	if err := direct.validateDependencies(); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if err := direct.validateOperationalAvailability(); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	resolved, store, inventory, tryDocument, err := direct.resolveTry(ctx, request.Selection, request.Try)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	attempts := attemptsForTry(inventory, tryDocument.Record.(*research.Try).ID)
	if len(attempts) == 0 {
		return nil, runtimeFailure(StageResolve, nil, false, false, ErrNoRetryableAttempt)
	}
	recoveredMarkers, reconcileErr := direct.reconcileAttemptMarkers(ctx, request.Selection, resolved.Project, store, tryDocument, attempts)
	if reconcileErr != nil {
		return recoveredMarkers, reconcileErr
	}
	if recoveredMarkers != nil && recoveredMarkers.Recovered {
		inventory, err = store.Inventory(ctx)
		if err != nil {
			return recoveredMarkers, runtimeFailure(StageCanonicalDone, recoveredMarkers, true, true, err)
		}
		tryDocument, err = inventory.ByID(tryDocument.Record.(*research.Try).ID)
		if err != nil {
			return recoveredMarkers, runtimeFailure(StageCanonicalDone, recoveredMarkers, true, true, err)
		}
		attempts = attemptsForTry(inventory, tryDocument.Record.(*research.Try).ID)
	}
	latest := attempts[len(attempts)-1]
	result := &ExecutionResult{
		Stage: StageCanonicalPlan, Project: resolved.Project, Context: resolved,
		Try: tryDocument, Attempt: latest,
		RecoveryAction: "resume the existing Attempt without creating another execution",
	}
	if recoveredMarkers != nil {
		result.Transactions = append(result.Transactions, recoveredMarkers.Transactions...)
		if recoveredMarkers.Attempt != nil {
			result.Recovered = true
		}
		if recoveredMarkers.Terminal != nil && recoveredMarkers.Attempt != nil && recoveredMarkers.Attempt.Record.(*research.Attempt).ID == latest.Record.(*research.Attempt).ID {
			result.Terminal = recoveredMarkers.Terminal
		}
	}
	if terminalAttempt(latest.Record.(*research.Attempt).State) {
		result.Stage = StageCanonicalDone
		if err := direct.refresh(ctx, resolved.Project, store); err != nil {
			result.RecoveryAction = "exp render"
			return result, runtimeFailure(StageProjection, result, true, true, err)
		}
		result.Stage = StageComplete
		return terminalOutcome(result)
	}
	return direct.execute(ctx, request.Selection, result, nil)
}

func (direct *Direct) reconcileAttemptMarkers(ctx context.Context, selection Selection, info *project.Info, store Store, tryDocument *record.Document, attempts []*record.Document) (*ExecutionResult, error) {
	if info == nil || tryDocument == nil {
		return nil, nil
	}
	markerRoot, err := direct.dependencies.MarkerRoot(info)
	if err != nil {
		return nil, runtimeFailure(StageOperationalDone, nil, true, false, err)
	}
	var operations OperationalStore
	if opened, openErr := direct.dependencies.OpenOperations(ctx, info); openErr == nil {
		operations = opened
		defer operations.Close()
	}
	result := &ExecutionResult{Stage: StageCanonicalPlan, Project: info, Try: tryDocument}
	for _, attemptDocument := range attempts {
		attempt := attemptDocument.Record.(*research.Attempt)
		if terminalAttempt(attempt.State) {
			continue
		}
		terminal, found, loadErr := direct.dependencies.LoadTerminal(ctx, markerRoot, directJobID(attempt.ID))
		if loadErr != nil {
			return result, runtimeFailure(StageOperationalDone, result, true, result.Recovered, loadErr)
		}
		if !found {
			continue
		}
		var job *operation.Job
		if operations != nil {
			observed, jobErr := operations.GetJob(ctx, directJobID(attempt.ID))
			if jobErr == nil {
				job = &observed
				if bindErr := worker.ValidateTerminalForJob(terminal, observed, attempt.ID.String()); bindErr != nil {
					return result, runtimeFailure(StageOperationalDone, result, false, true, bindErr)
				}
			} else if !errors.Is(jobErr, operation.ErrNotFound) {
				// The durable marker remains canonical recovery authority when the
				// rebuildable operational database cannot be inspected.
				job = nil
			}
		}
		if job == nil {
			if bindErr := worker.ValidateTerminalIdentity(terminal, directJobID(attempt.ID), attempt.ID.String()); bindErr != nil {
				return result, runtimeFailure(StageOperationalDone, result, false, true, bindErr)
			}
		}
		state, reason, observeErr := direct.completedAttemptDisposition(ctx, selection, tryDocument, attemptDocument, terminal)
		if observeErr != nil {
			result.Attempt, result.Terminal = attemptDocument, &terminal
			return result, runtimeFailure(StageOperationalDone, result, true, true, observeErr)
		}
		canonical := canonicalTerminal(terminal, direct.now(), attempt.CreatedAt)
		updated, transactionID, updateErr := direct.updateAttemptState(context.WithoutCancel(ctx), store, attemptDocument, state, reason, canonical, terminalDigests(terminal), terminal.MLflow)
		if transactionID != "" {
			result.Transactions = append(result.Transactions, transactionID)
		}
		result.Attempt, result.Terminal, result.Recovered = updated, &terminal, true
		if job != nil {
			result.Job = job
		}
		if updateErr != nil {
			return result, runtimeFailure(StageCanonicalDone, result, true, true, updateErr)
		}
	}
	if result.Recovered {
		result.Stage = StageCanonicalDone
		if err := direct.refresh(context.WithoutCancel(ctx), info, store); err != nil {
			result.RecoveryAction = "exp render"
			return result, runtimeFailure(StageProjection, result, true, true, err)
		}
	}
	return result, nil
}

func (direct *Direct) completedAttemptDisposition(ctx context.Context, selection Selection, tryDocument, attemptDocument *record.Document, terminal worker.Terminal) (research.AttemptState, string, error) {
	state, reason := canonicalTerminalState(terminal)
	attempt := attemptDocument.Record.(*research.Attempt)
	resolved, _, _, err := direct.resolveAttemptContext(ctx, selection, tryDocument.Record.(*research.Try).ID, attempt)
	if err != nil {
		return state, reason, fmt.Errorf("reconstruct managed workspace identity: %w", err)
	}
	bundle, err := direct.bundleForAttempt(ctx, attempt, nil)
	if err != nil {
		return state, reason, fmt.Errorf("reopen managed workspace seed identity: %w", err)
	}
	prepare, err := prepareRequest(resolved, tryDocument.Record.(*research.Try), attempt, bundle, selection.WorkspaceBackend)
	if err != nil {
		return state, reason, fmt.Errorf("reconstruct managed workspace policy: %w", err)
	}
	_, inspectErr := direct.dependencies.Backend.Inspect(ctx, prepare)
	if inspectErr == nil {
		return state, reason, nil
	}
	if errors.Is(inspectErr, experimentgit.ErrPathNotAllowed) || errors.Is(inspectErr, experimentgit.ErrForbiddenMetadata) || errors.Is(inspectErr, experimentgit.ErrWorkspaceState) || errors.Is(inspectErr, workspacebackend.ErrSeedMismatch) {
		return research.AttemptFailed, "managed workspace violated post-invocation safety policy", nil
	}
	return state, reason, fmt.Errorf("managed workspace post-invocation inspection is unavailable: %w", inspectErr)
}

// Retry creates a new Attempt only when the latest Attempt is already terminal.
// If the latest is nonterminal, Retry delegates to Resume and therefore cannot
// duplicate a canonical-to-operational crash-gap execution.
func (direct *Direct) Retry(ctx context.Context, request RetryRequest) (*ExecutionResult, error) {
	if err := direct.validateDependencies(); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if err := direct.validateOperationalAvailability(); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	resolved, store, inventory, tryDocument, err := direct.resolveTry(ctx, request.Selection, request.Try)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	tryValue := tryDocument.Record.(*research.Try)
	if tryValue.State != research.TryOpen {
		return nil, runtimeFailure(StageResolve, nil, false, false, fmt.Errorf("Try %s is %s and cannot be retried: %w", tryValue.ID, tryValue.State, ErrPrecondition))
	}
	attempts := attemptsForTry(inventory, tryValue.ID)
	if len(attempts) == 0 {
		return nil, runtimeFailure(StageResolve, nil, false, false, ErrNoRetryableAttempt)
	}
	previousDocument := attempts[len(attempts)-1]
	previous := previousDocument.Record.(*research.Attempt)
	if !terminalAttempt(previous.State) {
		return direct.Resume(ctx, ResumeRequest{Selection: request.Selection, Try: request.Try})
	}
	resolved, store, inventory, err = direct.resolveAttemptContext(ctx, request.Selection, tryValue.ID, previous)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if err := requireDirectConfig(resolved, request.WorkspaceBackend); err != nil {
		return nil, runtimeFailure(StageResolve, nil, true, false, err)
	}
	if err := validateDirectArgv(previous.Argv, resolved); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if previous.Provenance == nil || resolved.Config == nil || resolved.Config.Digest != previous.Provenance.ConfigDigest {
		return nil, runtimeFailure(StageResolve, nil, true, false, ErrConfigChanged)
	}
	policy, err := directPolicyFor(previous)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	mlflowProfile, err := resolveDirectMLflowProfile(resolved.Config, request.MLflowProfile, policy)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, true, false, err)
	}
	now := direct.after(previous.UpdatedAt)
	allocator := New(store, WithClock(func() time.Time { return now }), WithUUIDGenerator(direct.dependencies.GenerateUUID))
	attemptID, err := allocator.allocate(inventory, research.KindAttempt, now, nil)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	captured, err := direct.capture(ctx, resolved, tryValue.ID, now, previous.SourceSnapshots[0].State == research.SourceSnapshotDirty)
	if err != nil {
		return nil, runtimeFailure(StageCapture, nil, false, false, err)
	}
	if !sameRetrySource(previous.SourceSnapshots[0], captured.Snapshot) {
		direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
		return nil, runtimeFailure(StageCapture, nil, false, false, errors.New("registered Source bytes changed; retry would not preserve the prior Attempt identity"))
	}
	attempt := newDirectAttempt(attemptID, tryValue.ID, tryValue.Title, previous.CWD, previous.Argv, captured, resolved, policy.AllowedGlobs, policy.Timeout, mlflowProfile, now)
	attempt.RetryOf = previous.ID
	if metadata, err := exploration.MetadataFor(previous); err != nil {
		return nil, err
	} else if metadata != nil {
		execution, err := exploration.LoadExecution(ctx, resolved.ProjectID().String(), previous.ID.String())
		if err != nil {
			return nil, err
		}
		if execution.Digest() != metadata.ContextDigest {
			return nil, errors.New("retry exploration receipt mismatch")
		}
		if err := exploration.VerifyInputs(ctx, execution); err != nil {
			return nil, err
		}
		execution.Attempt = attemptID.String()
		execution.OutputDir = filepath.Join(execution.Storage.Root, "attempts", execution.Project, execution.Attempt, "output")
		metadata.ContextDigest = execution.Digest()
		metadata.Artifacts = []exploration.Artifact{}
		metadata.ArchiveState = "pending"
		if err := exploration.SetMetadata(attempt, *metadata); err != nil {
			return nil, err
		}
		if err := exploration.PersistExecution(ctx, execution); err != nil {
			return nil, err
		}
	}
	if err := research.Validate(attempt); err != nil {
		direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
		return nil, runtimeFailure(StageCapture, nil, false, false, err)
	}
	sourceDocument, err := inventory.ByID(previous.ExecutionSource)
	if err != nil {
		direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
		return nil, runtimeFailure(StageCanonicalPlan, nil, false, false, err)
	}
	changes := newChanges()
	for _, guarded := range []*record.Document{tryDocument, sourceDocument, previousDocument} {
		if err := changes.guard(guarded, guarded.Revision); err != nil {
			direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
			return nil, runtimeFailure(StageCanonicalPlan, nil, false, false, err)
		}
	}
	changes.create(&record.Document{Record: attempt, Body: "# Direct Try Retry Attempt\n"})
	transaction, transactionErr := store.Transact(ctx, record.TransactionRequest{Operation: "try.retry.plan", Changes: changes.values})
	result := &ExecutionResult{
		Stage: StageCanonicalPlan, Project: resolved.Project, Context: resolved,
		Try: tryDocument, RetryCreated: true,
	}
	if transaction != nil {
		result.Transactions = append(result.Transactions, transaction.TransactionID)
		result.Attempt, _ = resultDocument(transaction, attemptID)
		if current, lookupErr := resultDocument(transaction, tryValue.ID); lookupErr == nil {
			result.Try = current
		}
	}
	if transactionErr != nil {
		if transaction == nil {
			direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
		}
		return result, runtimeFailure(StageCanonicalPlan, result, transaction != nil, transaction != nil, transactionErr)
	}
	if result.Attempt == nil {
		return result, runtimeFailure(StageCanonicalPlan, result, true, true, errors.New("retry transaction omitted its Attempt"))
	}
	if err := direct.refresh(ctx, resolved.Project, store); err != nil {
		return result, runtimeFailure(StageProjection, result, true, true, err)
	}
	return direct.execute(ctx, request.Selection, result, captured.Bundle)
}

func sameRetrySource(previous, current research.SourceSnapshot) bool {
	previous.CapturedAt, current.CapturedAt = time.Time{}, time.Time{}
	previous.Digest, current.Digest = "", ""
	return reflect.DeepEqual(previous, current)
}

// Status never invokes a command or creates a workspace. Operational database
// creation is delegated to the injected store; missing jobs/markers/worktrees
// are ordinary observations, not implicit recovery.
func (direct *Direct) Status(ctx context.Context, request StatusRequest) (*StatusResult, error) {
	if direct == nil || direct.dependencies.Resolver == nil || direct.dependencies.OpenStore == nil {
		return nil, ErrRuntimeUnavailable
	}
	resolved, store, inventory, err := direct.resolveProjectContext(ctx, request.Selection, false)
	if err != nil {
		return nil, err
	}
	tries := inventory.OfKind(research.KindTry)
	if strings.TrimSpace(request.Try) != "" {
		document, resolveErr := inventory.Resolve(strings.TrimSpace(request.Try), research.KindTry)
		if resolveErr != nil {
			return nil, resolveErr
		}
		tries = []*record.Document{document}
	}
	sort.Slice(tries, func(i, j int) bool {
		left, right := tries[i].Record.(*research.Try), tries[j].Record.(*research.Try)
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID.String() < right.ID.String()
	})
	total := len(tries)
	limit, offset := total, 0
	if strings.TrimSpace(request.Try) == "" {
		limit = request.Limit
		if limit == 0 {
			limit = 100
		}
		if limit < 1 || limit > 1000 || request.Offset < 0 {
			return nil, errors.New("Try status pagination requires --limit 1..1000 and a non-negative --offset")
		}
		offset = min(request.Offset, total)
		end := min(offset+limit, total)
		tries = tries[offset:end]
	}
	result := &StatusResult{Project: resolved.Project, Tries: make([]TryStatus, 0, len(tries)), Total: total, Limit: limit, Offset: offset}
	jobsByAttempt := make(map[string]operation.JobSummary)
	if direct.dependencies.OpenOperationStatus == nil {
		result.Partial = true
	} else if operations, operationErr := direct.dependencies.OpenOperationStatus(ctx, resolved.Project); operationErr != nil {
		if !errors.Is(operationErr, fs.ErrNotExist) {
			result.Partial = true
		}
	} else {
		defer operations.Close()
		summaries, summaryErr := operations.ListJobSummaries(ctx)
		if summaryErr != nil {
			result.Partial = true
		} else {
			for _, summary := range summaries {
				if _, duplicate := jobsByAttempt[summary.SubjectID]; duplicate {
					result.Partial = true
					continue
				}
				jobsByAttempt[summary.SubjectID] = summary
			}
		}
	}
	attemptIndex := attemptsByTry(inventory)
	contextCache := make(map[string]*workspace.Context)
	contextErrors := make(map[string]error)
	markerRoot := ""
	if direct.dependencies.MarkerRoot != nil {
		markerRoot, err = direct.dependencies.MarkerRoot(resolved.Project)
		if err != nil {
			result.Partial = true
		}
	}
	for _, tryDocument := range tries {
		entry := TryStatus{Try: tryDocument.Clone(), Attempts: []AttemptStatus{}}
		tryID := tryDocument.Record.(*research.Try).ID
		for _, attemptDocument := range attemptIndex[tryID] {
			observation := AttemptStatus{Attempt: attemptDocument.Clone(), Status: string(attemptDocument.Record.(*research.Attempt).State)}
			policy, policyErr := directPolicyFor(attemptDocument.Record.(*research.Attempt))
			if policyErr == nil {
				observation.CleanupCompleted = policy.CleanupCompleted
			}
			attemptValue := attemptDocument.Record.(*research.Attempt)
			if summary, found := jobsByAttempt[attemptValue.ID.String()]; found {
				observation.Job = &operation.Job{
					JobInput: operation.JobInput{ID: summary.ID, SubjectID: summary.SubjectID},
					State:    summary.State, FencingToken: summary.FencingToken, LeaseExpiresAt: summary.LeaseExpiresAt, UpdatedAt: summary.UpdatedAt,
				}
			}
			if markerRoot != "" {
				terminal, found, markerErr := direct.dependencies.LoadTerminal(ctx, markerRoot, directJobID(attemptValue.ID))
				if markerErr == nil && found {
					if observation.Job != nil {
						markerErr = worker.ValidateTerminalForJob(terminal, *observation.Job, attemptValue.ID.String())
					} else {
						markerErr = worker.ValidateTerminalIdentity(terminal, directJobID(attemptValue.ID), attemptValue.ID.String())
					}
				}
				if markerErr != nil {
					observation.Error = "durable terminal marker could not be verified"
					result.Partial = true
				} else if found {
					observation.Marker = &terminal
					observation.Recoverable = !terminalAttempt(attemptValue.State)
				}
			}
			classifyAttemptStatus(direct.now(), attemptValue.State, &observation)
			if !observation.CleanupCompleted {
				direct.inspectAttemptWorkspace(ctx, request.Selection, resolved.Project, tryDocument, attemptDocument, &observation, result, contextCache, contextErrors)
			}
			entry.Attempts = append(entry.Attempts, observation)
		}
		result.Tries = append(result.Tries, entry)
	}
	_ = store
	return result, nil
}

// ClassifyAttemptStatus applies the shared canonical-to-operational status
// policy without reading or mutating any external state.
func ClassifyAttemptStatus(now time.Time, canonical research.AttemptState, observation *AttemptStatus) {
	if observation == nil || terminalAttempt(canonical) {
		return
	}
	if observation.Marker != nil {
		observation.Recoverable = true
		observation.Status = "terminal marker pending canonical import"
		return
	}
	if observation.Job == nil {
		if canonical == research.AttemptPlanned {
			observation.Recoverable = true
			observation.Status = "planned and unstarted"
			return
		}
		observation.Uncertain = true
		observation.Status = "operational job missing"
		return
	}
	switch observation.Job.State {
	case operation.JobQueued:
		if canonical == research.AttemptPlanned || canonical == research.AttemptQueued || canonical == research.AttemptStarting {
			observation.Recoverable = true
			observation.Status = "queued and resumable"
		} else {
			observation.Uncertain = true
			observation.Status = "queued job disagrees with canonical state"
		}
	case operation.JobRunning:
		if observation.Job.LeaseExpiresAt != nil && now.Before(observation.Job.LeaseExpiresAt.UTC()) {
			observation.Active = true
			observation.Status = "active under live lease"
		} else {
			observation.LeaseExpired = true
			observation.Uncertain = true
			observation.Status = "running claim lease expired"
		}
	case operation.JobSucceeded, operation.JobFailed, operation.JobCancelled:
		observation.Uncertain = true
		observation.Status = "terminal job is missing its durable marker"
	default:
		observation.Uncertain = true
		observation.Status = "operational job state is uncertain"
	}
}

func classifyAttemptStatus(now time.Time, canonical research.AttemptState, observation *AttemptStatus) {
	ClassifyAttemptStatus(now, canonical, observation)
}

func (direct *Direct) inspectAttemptWorkspace(ctx context.Context, selection Selection, info *project.Info, tryDocument, attemptDocument *record.Document, observation *AttemptStatus, result *StatusResult, contexts map[string]*workspace.Context, contextErrors map[string]error) {
	attempt := attemptDocument.Record.(*research.Attempt)
	cacheKey := attempt.ExecutionSource.String() + "\x00" + attempt.CWD
	resolved, cached := contexts[cacheKey]
	err, failed := contextErrors[cacheKey]
	if !cached && !failed {
		resolved, err = direct.resolveAttemptWorkspaceContext(ctx, selection, info, attempt)
		if err != nil {
			contextErrors[cacheKey] = err
		} else {
			contexts[cacheKey] = resolved
		}
	}
	if err != nil {
		observation.Error = "registered Source context could not be revalidated"
		result.Partial = true
		return
	}
	bundle, err := direct.bundleForAttempt(ctx, attempt, nil)
	if err != nil {
		if errors.Is(err, sourcesnapshot.ErrBundleNotFound) && attempt.SourceSnapshots[0].State == research.SourceSnapshotDirty {
			observation.Error = "private dirty seed bundle is missing"
			result.Partial = true
			return
		}
		observation.Error = "private dirty seed bundle could not be verified"
		result.Partial = true
		return
	}
	prepare, err := prepareRequest(resolved, tryDocument.Record.(*research.Try), attempt, bundle, selection.WorkspaceBackend)
	if err != nil {
		observation.Error = "managed workspace policy is invalid"
		result.Partial = true
		return
	}
	inspection, err := direct.dependencies.Backend.Inspect(ctx, prepare)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		observation.Error = "managed workspace failed identity or safety inspection"
		result.Partial = true
		return
	}
	observation.Workspace = &inspection
	observation.WorkspacePresent = true
}

// Handoff delegates only the runtime-open step after reconstructing the exact
// canonical Attempt and native Git workspace identity.
func (direct *Direct) Handoff(ctx context.Context, request HandoffRequest) (*HandoffResult, error) {
	if err := direct.validateDependencies(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Try) == "" || strings.TrimSpace(request.Attempt) == "" {
		return nil, errors.New("workspace handoff requires explicit Try and Attempt references")
	}
	_, _, inventory, tryDocument, err := direct.resolveTry(ctx, request.Selection, request.Try)
	if err != nil {
		return nil, err
	}
	attemptDocument, err := inventory.Resolve(request.Attempt, research.KindAttempt)
	if err != nil {
		return nil, err
	}
	attempt := attemptDocument.Record.(*research.Attempt)
	tryValue := tryDocument.Record.(*research.Try)
	if attempt.Try != tryValue.ID {
		return nil, errors.New("selected Attempt does not belong to the selected Try")
	}
	attemptResolved, _, _, err := direct.resolveAttemptContext(ctx, request.Selection, tryValue.ID, attempt)
	if err != nil {
		attemptResolved, _, _, err = direct.resolveHistoricalAttemptContext(ctx, request.Selection, tryValue.ID, attempt)
		if err != nil {
			return nil, err
		}
	}
	bundle, err := direct.bundleForAttempt(ctx, attempt, nil)
	if err != nil {
		return nil, err
	}
	prepare, err := prepareRequest(attemptResolved, tryValue, attempt, bundle, request.WorkspaceBackend)
	if err != nil {
		return nil, err
	}
	inspection, err := direct.dependencies.Backend.CleanupOrHandoff(ctx, prepare, workspacebackend.FinalizeHandoff)
	result := &HandoffResult{
		Project: attemptResolved.Project, Try: tryDocument.Clone(), Attempt: attemptDocument.Clone(), Inspection: inspection,
	}
	return result, err
}

// Cleanup removes only workspaces accepted by Backend cleanup and, for dirty
// work, only bundles removed atomically after exact seeded cleanup. It never
// deletes branches, commits, operation rows, or terminal markers.
func (direct *Direct) Cleanup(ctx context.Context, request CleanupRequest) (*CleanupResult, error) {
	if err := direct.validateDependencies(); err != nil {
		return nil, err
	}
	resolved, store, inventory, tryDocument, err := direct.resolveTry(ctx, request.Selection, request.Try)
	if err != nil {
		return nil, err
	}
	result := &CleanupResult{Project: resolved.Project, Try: tryDocument.Clone(), Items: []CleanupItem{}, Transactions: []string{}}
	failures := []error{}
	attempts := attemptsForTry(inventory, tryDocument.Record.(*research.Try).ID)
	for _, attemptDocument := range attempts {
		item := CleanupItem{Attempt: attemptDocument.Clone()}
		attempt := attemptDocument.Record.(*research.Attempt)
		policy, policyErr := directPolicyFor(attempt)
		if policyErr != nil {
			item.Refused, item.Reason, result.Partial = true, "Attempt is not a managed direct Try", true
			result.Items = append(result.Items, item)
			continue
		}
		if policy.CleanupCompleted {
			item.AlreadyCompleted = true
			result.Items = append(result.Items, item)
			continue
		}
		if !terminalAttempt(attempt.State) {
			item.Refused, item.Reason, result.Partial = true, "Attempt is not terminal", true
			result.Items = append(result.Items, item)
			continue
		}
		attemptResolved, _, _, resolveErr := direct.resolveHistoricalAttemptContext(ctx, request.Selection, tryDocument.Record.(*research.Try).ID, attempt)
		if resolveErr != nil {
			item.Refused, item.Reason, result.Partial = true, "registered Source context is unavailable", true
			result.Items = append(result.Items, item)
			continue
		}
		bundle, bundleErr := direct.bundleForAttempt(ctx, attempt, nil)
		if bundleErr != nil && !(attempt.SourceSnapshots[0].State == research.SourceSnapshotDirty && errors.Is(bundleErr, sourcesnapshot.ErrBundleNotFound)) {
			item.Refused, item.Reason, result.Partial = true, "private seed bundle is invalid", true
			result.Items = append(result.Items, item)
			continue
		}
		prepare, prepareErr := prepareRequest(attemptResolved, tryDocument.Record.(*research.Try), attempt, bundle, request.WorkspaceBackend)
		if prepareErr != nil {
			item.Refused, item.Reason, result.Partial = true, "managed workspace policy is invalid", true
			result.Items = append(result.Items, item)
			continue
		}
		prepare.AllowRetiredSource = attemptResolved.Source.State == research.SourceRetired
		inspection, cleanupErr := direct.dependencies.Backend.CleanupOrHandoff(ctx, prepare, workspacebackend.FinalizeCleanup)
		item.RemovedWorkspace = inspection.Removed
		if cleanupErr != nil && inspection.Removed && bundle != nil {
			// The backend reports Removed before attempting the bundle deletion. A
			// transient bundle error is safe to retry because Cleanup authenticates
			// the exact bundle capability and is independently idempotent.
			if retryErr := direct.dependencies.Bundles.Cleanup(ctx, *bundle); retryErr == nil {
				cleanupErr = nil
				item.RemovedBundle = true
			}
		}
		if cleanupErr != nil {
			failures = append(failures, cleanupErr)
			item.Refused, result.Partial = true, true
			if inspection.Removed {
				item.Reason = "managed workspace was removed but private seed cleanup remains incomplete"
			} else {
				item.Reason = "managed workspace changed or failed safety inspection"
			}
			result.Items = append(result.Items, item)
			continue
		}
		item.RemovedBundle = item.RemovedBundle || bundle != nil
		updated, transactionID, markErr := direct.markAttemptCleaned(ctx, store, attemptDocument)
		if transactionID != "" {
			result.Transactions = append(result.Transactions, transactionID)
		}
		if markErr != nil {
			failures = append(failures, markErr)
			item.Refused, item.Reason, result.Partial = true, "resources were removed but canonical cleanup completion could not be recorded", true
		} else {
			item.Attempt = updated
		}
		result.Items = append(result.Items, item)
	}
	if len(result.Transactions) > 0 {
		if err := direct.refresh(ctx, resolved.Project, store); err != nil {
			result.Partial = true
			return result, runtimeFailure(StageProjection, nil, true, true, err)
		}
	}
	if result.Partial {
		return result, errors.Join(append([]error{ErrCleanupIncomplete}, failures...)...)
	}
	return result, nil
}

func (direct *Direct) markAttemptCleaned(ctx context.Context, store Store, current *record.Document) (*record.Document, string, error) {
	if current == nil || current.Kind() != research.KindAttempt {
		return nil, "", ErrCanonicalUnavailable
	}
	policy, err := directPolicyFor(current.Record.(*research.Attempt))
	if err != nil {
		return nil, "", err
	}
	if policy.CleanupCompleted {
		return current.Clone(), "", nil
	}
	replacement := current.Clone()
	attempt := replacement.Record.(*research.Attempt)
	if attempt.Extensions == nil {
		attempt.Extensions = research.Extensions{}
	}
	table := attempt.Extensions[DirectExtensionNamespace]
	if table == nil {
		table = map[string]any{}
		attempt.Extensions[DirectExtensionNamespace] = table
	}
	now := direct.after(attempt.UpdatedAt)
	table["cleanup_completed"] = true
	table["cleanup_completed_at"] = now
	attempt.UpdatedAt = now
	if updater, ok := store.(interface {
		Update(context.Context, *record.Document, string) (*record.Document, error)
	}); ok {
		document, err := updater.Update(ctx, replacement, current.Revision)
		return document, "", err
	}
	transaction, err := store.Transact(ctx, record.TransactionRequest{
		Operation: "try.cleanup.record",
		Changes:   []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: current.Revision}},
	})
	if err != nil {
		return nil, transactionID(transaction), err
	}
	document, err := resultDocument(transaction, attempt.ID)
	return document, transactionID(transaction), err
}

func (direct *Direct) resolveTry(ctx context.Context, selection Selection, reference string) (*workspace.Context, Store, *record.Inventory, *record.Document, error) {
	resolved, store, inventory, err := direct.resolveProjectContext(ctx, selection, true)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if strings.TrimSpace(reference) == "" {
		return nil, nil, nil, nil, errors.New("Try reference is required")
	}
	tryDocument, err := inventory.Resolve(strings.TrimSpace(reference), research.KindTry)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return resolved, store, inventory, tryDocument, nil
}

func (direct *Direct) resolveProjectContext(ctx context.Context, selection Selection, recoverTransactions bool) (*workspace.Context, Store, *record.Inventory, error) {
	resolved, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: selection.InvocationDir, Workspace: strings.TrimSpace(selection.Workspace), Source: strings.TrimSpace(selection.Source),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if resolved == nil || resolved.Project == nil {
		return nil, nil, nil, ErrCanonicalUnavailable
	}
	store, err := direct.dependencies.OpenStore(resolved.Project)
	if err != nil {
		return nil, nil, nil, err
	}
	inventory, err := store.Inventory(ctx)
	if recoverTransactions && errors.Is(err, record.ErrTransactionRecoveryRequired) {
		if recoverer, ok := store.(interface{ Recover(context.Context) error }); ok {
			if recoverErr := recoverer.Recover(ctx); recoverErr != nil {
				return nil, nil, nil, recoverErr
			}
			inventory, err = store.Inventory(ctx)
		}
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if !inventory.Valid() {
		return nil, nil, nil, &record.InventoryError{Diagnostics: append([]record.Diagnostic{}, inventory.Diagnostics...)}
	}
	return resolved, store, inventory, nil
}

func attemptsForTry(inventory *record.Inventory, tryID research.ID) []*record.Document {
	return attemptsByTry(inventory)[tryID]
}

func attemptsByTry(inventory *record.Inventory) map[research.ID][]*record.Document {
	indexed := make(map[research.ID][]*record.Document)
	if inventory == nil {
		return indexed
	}
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		indexed[attempt.Try] = append(indexed[attempt.Try], document)
	}
	for owner := range indexed {
		documents := indexed[owner]
		sort.Slice(documents, func(i, j int) bool {
			left, right := documents[i].Record.(*research.Attempt), documents[j].Record.(*research.Attempt)
			if !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			return left.ID.String() < right.ID.String()
		})
		indexed[owner] = documents
	}
	return indexed
}
