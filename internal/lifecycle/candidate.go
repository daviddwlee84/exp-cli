package lifecycle

import (
	"context"
	"fmt"
	"reflect"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

// CreateCandidateRequest promotes a scientifically supported Experiment result
// into a Git-addressed Candidate.
type CreateCandidateRequest struct {
	Title                          string
	Body                           string
	Experiment                     RevisionRef
	Evaluation                     RevisionRef
	EvaluationSpecExpectedRevision string
	Parents                        []RevisionRef
	Attempt                        RevisionRef
	GitCommit                      string
	ChangeSet                      []string
	ExternalRefs                   []research.ExternalRef
	Tags                           []string
	Extensions                     research.Extensions
}

// CreateCandidateResult identifies the Candidate creation transaction.
type CreateCandidateResult struct {
	TransactionID string
	Candidate     *record.Document
}

// CreateCandidate accepts only a concluded, supported Experiment and a passing
// scientific Evaluation of that exact Experiment.
func (service *Service) CreateCandidate(ctx context.Context, request CreateCandidateRequest) (*CreateCandidateResult, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	experimentDocument, err := resolve(inventory, request.Experiment, research.KindExperiment)
	if err != nil {
		return nil, err
	}
	evaluationDocument, err := resolve(inventory, request.Evaluation, research.KindEvaluation)
	if err != nil {
		return nil, err
	}
	experiment := experimentDocument.Record.(*research.Experiment)
	evaluation := evaluationDocument.Record.(*research.Evaluation)
	if experiment.Lifecycle != research.LifecycleClosed || experiment.Closure != research.ClosureConcluded || experiment.Verdict != research.VerdictSupported {
		return nil, fmt.Errorf("Experiment %s is not a closed supported conclusion: %w", experiment.ID, ErrPrecondition)
	}
	if evaluation.Subject != experiment.ID || evaluation.Outcome != research.EvaluationPassed {
		return nil, fmt.Errorf("Evaluation %s is not a passing result for Experiment %s: %w", evaluation.ID, experiment.ID, ErrPrecondition)
	}
	specReference := RevisionRef{ID: evaluation.Spec, Revision: request.EvaluationSpecExpectedRevision}
	specDocument, err := resolve(inventory, specReference, research.KindEvaluationSpec)
	if err != nil {
		return nil, err
	}
	spec := specDocument.Record.(*research.EvaluationSpec)
	if spec.Purpose != research.EvaluationScientific {
		return nil, fmt.Errorf("Evaluation %s does not use a scientific EvaluationSpec: %w", evaluation.ID, ErrPrecondition)
	}
	candidateSchema := research.SchemaCandidate
	var candidateSources []research.CandidateSource
	var attemptDocument, attemptRunDocument *record.Document
	if !request.Attempt.ID.IsZero() || request.Attempt.Revision != "" {
		if request.GitCommit != "" || len(request.ChangeSet) > 0 {
			return nil, fmt.Errorf("Candidate v2 copies Source identity from Attempt and forbids legacy Git fields: %w", ErrPrecondition)
		}
		attemptDocument, err = resolve(inventory, request.Attempt, research.KindAttempt)
		if err != nil {
			return nil, err
		}
		attemptRunDocument, candidateSources, err = formalCandidateAttempt(inventory, experiment, attemptDocument)
		if err != nil {
			return nil, err
		}
		candidateSchema = research.SchemaCandidateV2
	} else {
		attemptDocument, attemptRunDocument, err = successfulCandidateAttempt(inventory, experiment, request.GitCommit, request.ChangeSet)
		if err != nil {
			return nil, err
		}
	}
	if candidateSchema == research.SchemaCandidateV2 {
		attemptID, _ := attemptDocument.ID()
		if evaluation.Schema != research.SchemaEvaluationV2 || evaluation.Attempt != attemptID {
			return nil, fmt.Errorf("Candidate v2 Evaluation %s must be typed-bound to backing Attempt %s: %w", evaluation.ID, attemptID, ErrPrecondition)
		}
	}
	if owner, found, ownerErr := evaluationMLflowOwner(evaluation); ownerErr != nil {
		return nil, ownerErr
	} else if found {
		attemptID, _ := attemptDocument.ID()
		if owner != attemptID.String() {
			return nil, fmt.Errorf("Evaluation MLflow owner Attempt %s does not match Candidate backing Attempt %s: %w", owner, attemptID, ErrPrecondition)
		}
	}

	changes := newGuardedChanges()
	for _, guarded := range []struct {
		document *record.Document
		revision string
	}{{experimentDocument, request.Experiment.Revision}, {evaluationDocument, request.Evaluation.Revision}, {specDocument, specReference.Revision}} {
		if err := changes.guard(guarded.document, guarded.revision); err != nil {
			return nil, err
		}
	}
	if err := changes.guard(attemptDocument, attemptDocument.Revision); err != nil {
		return nil, err
	}
	if err := changes.guard(attemptRunDocument, attemptRunDocument.Revision); err != nil {
		return nil, err
	}
	parentIDs := make([]research.ID, 0, len(request.Parents))
	for _, parent := range request.Parents {
		document, resolveErr := resolve(inventory, parent, research.KindCandidate)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if guardErr := changes.guard(document, parent.Revision); guardErr != nil {
			return nil, guardErr
		}
		parentIDs = append(parentIDs, parent.ID)
	}

	now := service.now()
	reserved := make(map[research.ID]struct{})
	id, err := service.allocate(inventory, research.KindCandidate, now, reserved)
	if err != nil {
		return nil, err
	}
	backingAttempt := research.ID{}
	if candidateSchema == research.SchemaCandidateV2 {
		backingAttempt = attemptDocument.Record.(*research.Attempt).ID
	}
	candidate := &research.Candidate{
		Common: research.Common{
			Schema: candidateSchema, ID: id, Title: request.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), request.Tags...),
		},
		Experiment: experiment.ID, Evaluation: evaluation.ID, Parents: parentIDs,
		GitCommit: request.GitCommit, ChangeSet: append([]string(nil), request.ChangeSet...),
		Attempt: backingAttempt, Sources: cloneCandidateSources(candidateSources),
		ExternalRefs: cloneExternalRefs(request.ExternalRefs), Extensions: cloneExtensions(request.Extensions),
	}
	changes.create(&record.Document{Record: candidate, Body: request.Body})
	transaction, transactionErr := service.store.Transact(ctx, record.TransactionRequest{
		Operation: "candidate.create", Changes: changes.changes,
	})
	result := &CreateCandidateResult{}
	if transaction != nil {
		result.TransactionID = transaction.TransactionID
		result.Candidate, _ = resultDocument(transaction, id)
	}
	if transactionErr != nil {
		if transaction == nil {
			return nil, transactionErr
		}
		return result, lifecycleTransactionError(transaction, transactionErr)
	}
	if result.Candidate == nil {
		return nil, fmt.Errorf("canonical transaction omitted Candidate %s", id)
	}
	return result, nil
}

func formalCandidateAttempt(inventory *record.Inventory, experiment *research.Experiment, attemptDocument *record.Document) (*record.Document, []research.CandidateSource, error) {
	if inventory == nil || experiment == nil || attemptDocument == nil || attemptDocument.Kind() != research.KindAttempt {
		return nil, nil, fmt.Errorf("formal Candidate Attempt is incomplete: %w", ErrPrecondition)
	}
	attempt := attemptDocument.Record.(*research.Attempt)
	if attempt.Schema != research.SchemaAttemptV3 || attempt.Run.IsZero() || !attempt.Try.IsZero() ||
		attempt.State != research.AttemptSucceeded || attempt.Terminal == nil || len(attempt.SourceSnapshots) == 0 ||
		experiment.Conclusion == nil || attempt.CreatedAt.After(experiment.Conclusion.ConcludedAt) || attempt.Terminal.EndedAt.After(experiment.Conclusion.ConcludedAt) {
		return nil, nil, fmt.Errorf("Attempt %s is not a successful formal Run-backed exp.attempt/v3: %w", attempt.ID, ErrPrecondition)
	}
	for _, snapshot := range attempt.SourceSnapshots {
		if snapshot.State != research.SourceSnapshotClean {
			return nil, nil, fmt.Errorf("Attempt %s contains a dirty SourceSnapshot and cannot back a Candidate: %w", attempt.ID, ErrPrecondition)
		}
	}
	runDocument, err := inventory.ByID(attempt.Run)
	if err != nil {
		return nil, nil, err
	}
	run, ok := runDocument.Record.(*research.Run)
	if !ok || run.Experiment != experiment.ID || !conclusionIncludesRun(experiment, attempt.Run) {
		return nil, nil, fmt.Errorf("Attempt %s Run is not included by Experiment %s: %w", attempt.ID, experiment.ID, ErrPrecondition)
	}
	sources := make([]research.CandidateSource, len(attempt.SourceSnapshots))
	for index, snapshot := range attempt.SourceSnapshots {
		sources[index] = research.CandidateSource{
			Source: snapshot.Source, HeadCommit: snapshot.HeadCommit,
			ChangeSet: append([]string{}, snapshot.ChangeSet...),
		}
	}
	return runDocument, sources, nil
}

func cloneCandidateSources(sources []research.CandidateSource) []research.CandidateSource {
	if sources == nil {
		return nil
	}
	cloned := append([]research.CandidateSource(nil), sources...)
	for index := range cloned {
		cloned[index].ChangeSet = append([]string{}, sources[index].ChangeSet...)
	}
	return cloned
}

func successfulCandidateAttempt(inventory *record.Inventory, experiment *research.Experiment, commit string, changeSet []string) (*record.Document, *record.Document, error) {
	var matchedAttempt, matchedRun *record.Document
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		if attempt.Schema != research.SchemaAttemptV2 || attempt.State != research.AttemptSucceeded || attempt.Terminal == nil || attempt.Terminal.Source != "direct" || attempt.HeadCommit != commit || !reflect.DeepEqual(attempt.ChangeSet, changeSet) {
			continue
		}
		runDocument, err := inventory.ByID(attempt.Run)
		if err != nil {
			return nil, nil, err
		}
		if experiment == nil || runDocument.Record.(*research.Run).Experiment != experiment.ID || !conclusionIncludesRun(experiment, attempt.Run) {
			continue
		}
		if matchedAttempt == nil || document.Path < matchedAttempt.Path {
			matchedAttempt, matchedRun = document, runDocument
		}
	}
	if matchedAttempt == nil {
		return nil, nil, fmt.Errorf("Candidate Git commit and change set are not backed by a successful included Attempt for Experiment %s: %w", experiment.ID, ErrPrecondition)
	}
	return matchedAttempt, matchedRun, nil
}

func conclusionIncludesRun(experiment *research.Experiment, run research.ID) bool {
	if experiment == nil || experiment.Conclusion == nil {
		return false
	}
	for _, evidence := range experiment.Conclusion.Evidence {
		if evidence.Run == run && evidence.Disposition == research.EvidenceIncluded {
			return true
		}
	}
	return false
}

func evaluationMLflowOwner(evaluation *research.Evaluation) (string, bool, error) {
	if evaluation == nil {
		return "", false, nil
	}
	owner := ""
	for _, reference := range evaluation.ExternalRefs {
		if reference.Provider != "mlflow" || reference.Role != research.ExternalTracker {
			continue
		}
		if claimed, ok := reference.Metadata["mlflow.owner_attempt"].(string); ok && claimed != "" {
			if owner != "" && owner != claimed {
				return "", false, fmt.Errorf("Evaluation contains conflicting MLflow owner Attempts: %w", ErrPrecondition)
			}
			owner = claimed
		}
	}
	return owner, owner != "", nil
}
