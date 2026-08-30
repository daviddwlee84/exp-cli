package lifecycle

import (
	"context"
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

// ReleaseSlotInput binds a typed release slot to an exact Candidate revision.
type ReleaseSlotInput struct {
	Name      string
	Candidate RevisionRef
}

// CombinationEvidence pins the supported combination Experiment and passing
// scientific Evaluation required when a Release composes multiple Candidates.
type CombinationEvidence struct {
	Experiment                     RevisionRef
	Evaluation                     RevisionRef
	EvaluationSpecExpectedRevision string
}

// ReleaseEvaluationInput creates the Release-scoped Evaluation in the same
// transaction as a validated Release.
type ReleaseEvaluationInput struct {
	Spec RevisionRef
	Data EvaluationData
}

// CreateReleaseRequest creates one typed release assembly. Draft releases have
// no release-scoped Evaluation; validated releases create one atomically.
type CreateReleaseRequest struct {
	Title       string
	Body        string
	Target      string
	Version     string
	State       research.ReleaseState
	Slots       []ReleaseSlotInput
	Combination *CombinationEvidence
	Evaluation  *ReleaseEvaluationInput
	Tags        []string
	Extensions  research.Extensions
}

// CreateReleaseResult contains the Release and its optional immutable
// release-scoped Evaluation.
type CreateReleaseResult struct {
	TransactionID string
	Release       *record.Document
	Evaluation    *record.Document
}

// CreateRelease enforces typed Candidate slots and requires supported
// combination evidence whenever more than one distinct Candidate is composed.
func (service *Service) CreateRelease(ctx context.Context, request CreateReleaseRequest) (*CreateReleaseResult, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if request.State != research.ReleaseDraft && request.State != research.ReleaseValidated {
		return nil, fmt.Errorf("new Release state %q must be draft or validated: %w", request.State, ErrPrecondition)
	}
	if request.State == research.ReleaseDraft && request.Evaluation != nil {
		return nil, fmt.Errorf("draft Release cannot carry a release-scoped Evaluation: %w", ErrPrecondition)
	}
	if request.State == research.ReleaseValidated && request.Evaluation == nil {
		return nil, fmt.Errorf("validated Release requires an atomic release-scoped Evaluation: %w", ErrPrecondition)
	}

	changes := newGuardedChanges()
	slots := make([]research.ReleaseSlot, 0, len(request.Slots))
	candidateIDs := make([]research.ID, 0, len(request.Slots))
	for _, slot := range request.Slots {
		document, resolveErr := resolve(inventory, slot.Candidate, research.KindCandidate)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if guardErr := changes.guard(document, slot.Candidate.Revision); guardErr != nil {
			return nil, guardErr
		}
		slots = append(slots, research.ReleaseSlot{Name: slot.Name, Candidate: slot.Candidate.ID})
		candidateIDs = append(candidateIDs, slot.Candidate.ID)
	}

	distinctCandidates := uniqueIDs(candidateIDs)
	var combinationExperiment, combinationEvaluation research.ID
	if len(distinctCandidates) > 1 {
		if request.Combination == nil {
			return nil, fmt.Errorf("Release combines %d Candidates without combination evidence: %w", len(distinctCandidates), ErrPrecondition)
		}
		combinationExperiment, combinationEvaluation, err = validateCombinationEvidence(inventory, changes, *request.Combination, distinctCandidates)
		if err != nil {
			return nil, err
		}
	} else if request.Combination != nil {
		return nil, fmt.Errorf("combination evidence is only accepted for multiple distinct Candidates: %w", ErrPrecondition)
	}

	now := service.now()
	reserved := make(map[research.ID]struct{})
	releaseID, err := service.allocate(inventory, research.KindRelease, now, reserved)
	if err != nil {
		return nil, err
	}
	var evaluationID research.ID
	var evaluation *research.Evaluation
	if request.Evaluation != nil {
		specDocument, resolveErr := resolve(inventory, request.Evaluation.Spec, research.KindEvaluationSpec)
		if resolveErr != nil {
			return nil, resolveErr
		}
		spec := specDocument.Record.(*research.EvaluationSpec)
		if spec.Purpose != research.EvaluationPromotion || spec.SealedAt == nil {
			return nil, fmt.Errorf("validated Release EvaluationSpec %s is not sealed for promotion: %w", spec.ID, ErrPrecondition)
		}
		if request.Evaluation.Data.Outcome != research.EvaluationPassed {
			return nil, fmt.Errorf("validated Release requires a passing Evaluation: %w", ErrPrecondition)
		}
		if err := validateMetrics(spec, request.Evaluation.Data.Metrics, request.Evaluation.Data.Outcome); err != nil {
			return nil, err
		}
		if err := changes.guard(specDocument, request.Evaluation.Spec.Revision); err != nil {
			return nil, err
		}
		evaluationID, err = service.allocate(inventory, research.KindEvaluation, now, reserved)
		if err != nil {
			return nil, err
		}
		evaluation = newEvaluation(evaluationID, spec.ID, releaseID, now, request.Evaluation.Data)
	}

	release := &research.Release{
		Common: research.Common{
			Schema: research.SchemaRelease, ID: releaseID, Title: request.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), request.Tags...),
		},
		Target: request.Target, Version: request.Version, State: request.State, Slots: slots,
		CombinationExperiment: combinationExperiment, CombinationEvaluation: combinationEvaluation, Evaluation: evaluationID,
		Extensions: cloneExtensions(request.Extensions),
	}
	changes.create(&record.Document{Record: release, Body: request.Body})
	if evaluation != nil {
		changes.create(&record.Document{Record: evaluation, Body: request.Evaluation.Data.Body})
	}
	transaction, err := service.store.Transact(ctx, record.TransactionRequest{
		Operation: "release.create", Changes: changes.changes,
	})
	if err != nil {
		return nil, err
	}
	releaseDocument, err := resultDocument(transaction, releaseID)
	if err != nil {
		return nil, err
	}
	result := &CreateReleaseResult{TransactionID: transaction.TransactionID, Release: releaseDocument}
	if !evaluationID.IsZero() {
		result.Evaluation, err = resultDocument(transaction, evaluationID)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validateCombinationEvidence(inventory *record.Inventory, changes *guardedChanges, input CombinationEvidence, candidates []research.ID) (research.ID, research.ID, error) {
	experimentDocument, err := resolve(inventory, input.Experiment, research.KindExperiment)
	if err != nil {
		return research.ID{}, research.ID{}, err
	}
	evaluationDocument, err := resolve(inventory, input.Evaluation, research.KindEvaluation)
	if err != nil {
		return research.ID{}, research.ID{}, err
	}
	experiment := experimentDocument.Record.(*research.Experiment)
	evaluation := evaluationDocument.Record.(*research.Evaluation)
	if experiment.Design.Kind != research.ExperimentCombination || experiment.Lifecycle != research.LifecycleClosed || experiment.Closure != research.ClosureConcluded || experiment.Verdict != research.VerdictSupported {
		return research.ID{}, research.ID{}, fmt.Errorf("Experiment %s is not a closed supported combination: %w", experiment.ID, ErrPrecondition)
	}
	if !sameIDSet(experiment.CandidateInputs, candidates) {
		return research.ID{}, research.ID{}, fmt.Errorf("combination Experiment %s inputs do not match Release Candidates: %w", experiment.ID, ErrPrecondition)
	}
	if evaluation.Subject != experiment.ID || evaluation.Outcome != research.EvaluationPassed {
		return research.ID{}, research.ID{}, fmt.Errorf("Evaluation %s does not pass combination Experiment %s: %w", evaluation.ID, experiment.ID, ErrPrecondition)
	}
	specReference := RevisionRef{ID: evaluation.Spec, Revision: input.EvaluationSpecExpectedRevision}
	specDocument, err := resolve(inventory, specReference, research.KindEvaluationSpec)
	if err != nil {
		return research.ID{}, research.ID{}, err
	}
	if specDocument.Record.(*research.EvaluationSpec).Purpose != research.EvaluationScientific {
		return research.ID{}, research.ID{}, fmt.Errorf("combination Evaluation %s is not scientific: %w", evaluation.ID, ErrPrecondition)
	}
	for _, guarded := range []struct {
		document *record.Document
		revision string
	}{{experimentDocument, input.Experiment.Revision}, {evaluationDocument, input.Evaluation.Revision}, {specDocument, specReference.Revision}} {
		if err := changes.guard(guarded.document, guarded.revision); err != nil {
			return research.ID{}, research.ID{}, err
		}
	}
	return experiment.ID, evaluation.ID, nil
}
