package lifecycle

import (
	"context"
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

// CreatePromotionSpecRequest seals the production target and holdout evaluator
// before any Promotion outcome is known.
type CreatePromotionSpecRequest struct {
	Title              string
	Body               string
	Target             string
	EvaluationSpec     RevisionRef
	HoldoutBudgetHours float64
	Tags               []string
	Extensions         research.Extensions
}

// CreatePromotionSpecResult identifies the sealed specification transaction.
type CreatePromotionSpecResult struct {
	TransactionID string
	Spec          *record.Document
}

// CreatePromotionSpec creates a sealed, human-gated PromotionSpec from an
// already sealed promotion EvaluationSpec.
func (service *Service) CreatePromotionSpec(ctx context.Context, request CreatePromotionSpecRequest) (*CreatePromotionSpecResult, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	evaluationSpecDocument, err := resolve(inventory, request.EvaluationSpec, research.KindEvaluationSpec)
	if err != nil {
		return nil, err
	}
	evaluationSpec := evaluationSpecDocument.Record.(*research.EvaluationSpec)
	if evaluationSpec.Purpose != research.EvaluationPromotion || evaluationSpec.SealedAt == nil {
		return nil, fmt.Errorf("EvaluationSpec %s is not sealed for promotion: %w", evaluationSpec.ID, ErrPrecondition)
	}
	if request.HoldoutBudgetHours <= 0 || request.HoldoutBudgetHours > evaluationSpec.BudgetHours {
		return nil, fmt.Errorf("holdout budget must be positive and no greater than EvaluationSpec budget %.3f: %w", evaluationSpec.BudgetHours, ErrPrecondition)
	}
	now := service.now()
	reserved := make(map[research.ID]struct{})
	id, err := service.allocate(inventory, research.KindPromotionSpec, now, reserved)
	if err != nil {
		return nil, err
	}
	spec := &research.PromotionSpec{
		Common: research.Common{
			Schema: research.SchemaPromotionSpec, ID: id, Title: request.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), request.Tags...),
		},
		Target: request.Target, EvaluationSpec: evaluationSpec.ID, SealedAt: now,
		HoldoutBudgetHours: request.HoldoutBudgetHours, HumanApprovalRequired: true,
		Extensions: cloneExtensions(request.Extensions),
	}
	changes := newGuardedChanges()
	if err := changes.guard(evaluationSpecDocument, request.EvaluationSpec.Revision); err != nil {
		return nil, err
	}
	changes.create(&record.Document{Record: spec, Body: request.Body})
	transaction, err := service.store.Transact(ctx, record.TransactionRequest{
		Operation: "promotion-spec.create", Changes: changes.changes,
	})
	if err != nil {
		return nil, err
	}
	document, err := resultDocument(transaction, id)
	if err != nil {
		return nil, err
	}
	return &CreatePromotionSpecResult{TransactionID: transaction.TransactionID, Spec: document}, nil
}

// AppendPromotionRequest appends one human decision to a target's Promotion
// chain. ExpectedPrevious and ExpectedChampion are explicit compare-and-swap
// values; zero means the caller expects none.
type AppendPromotionRequest struct {
	Title                          string
	Body                           string
	Target                         string
	Spec                           RevisionRef
	Challenger                     RevisionRef
	Evaluation                     RevisionRef
	EvaluationSpecExpectedRevision string
	Outcome                        research.PromotionOutcome
	ApprovedBy                     string
	ExpectedPrevious               research.ID
	PreviousExpectedRevision       string
	ExpectedChampion               research.ID
	IncumbentExpectedRevision      string
	Tags                           []string
	Extensions                     research.Extensions
}

// AppendPromotionResult contains the append-only event and champion derived
// immediately from that event. Champion is nil when a rejection occurs before
// any accepted Promotion.
type AppendPromotionResult struct {
	TransactionID string
	Promotion     *record.Document
	Champion      *record.Champion
}

// AppendPromotion validates the sealed holdout evidence, appends a
// human-approved Promotion, and derives the resulting champion. A rollback can
// restore only the incumbent displaced by the current champion-setting event.
func (service *Service) AppendPromotion(ctx context.Context, request AppendPromotionRequest) (*AppendPromotionResult, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	state, err := promotionStateFor(inventory, request.Target)
	if err != nil {
		return nil, err
	}
	if request.ExpectedPrevious != state.tipID() {
		return nil, fmt.Errorf("Promotion chain tip is %s, caller expected %s: %w", state.tipID(), request.ExpectedPrevious, ErrPrecondition)
	}
	if request.ExpectedChampion != state.championRelease {
		return nil, fmt.Errorf("current champion is %s, caller expected %s: %w", state.championRelease, request.ExpectedChampion, ErrPrecondition)
	}

	specDocument, err := resolve(inventory, request.Spec, research.KindPromotionSpec)
	if err != nil {
		return nil, err
	}
	challengerDocument, err := resolve(inventory, request.Challenger, research.KindRelease)
	if err != nil {
		return nil, err
	}
	evaluationDocument, err := resolve(inventory, request.Evaluation, research.KindEvaluation)
	if err != nil {
		return nil, err
	}
	spec := specDocument.Record.(*research.PromotionSpec)
	challenger := challengerDocument.Record.(*research.Release)
	evaluation := evaluationDocument.Record.(*research.Evaluation)
	if spec.Target != request.Target || challenger.Target != request.Target {
		return nil, fmt.Errorf("Promotion target does not match its spec and challenger: %w", ErrPrecondition)
	}
	if !spec.HumanApprovalRequired {
		return nil, fmt.Errorf("PromotionSpec %s does not require human approval: %w", spec.ID, ErrPrecondition)
	}
	if challenger.State != research.ReleaseValidated {
		return nil, fmt.Errorf("challenger Release %s is not validated: %w", challenger.ID, ErrPrecondition)
	}
	if evaluation.Subject != challenger.ID || evaluation.Spec != spec.EvaluationSpec {
		return nil, fmt.Errorf("Evaluation %s does not evaluate challenger %s under sealed spec %s: %w", evaluation.ID, challenger.ID, spec.EvaluationSpec, ErrPrecondition)
	}
	if !evaluation.EvaluatedAt.After(spec.SealedAt) {
		return nil, fmt.Errorf("Evaluation %s was not produced strictly after sealed PromotionSpec %s: %w", evaluation.ID, spec.ID, ErrPrecondition)
	}
	for _, document := range inventory.OfKind(research.KindPromotion) {
		if document.Record.(*research.Promotion).Evaluation == evaluation.ID {
			return nil, fmt.Errorf("Evaluation %s was already consumed by a Promotion: %w", evaluation.ID, ErrPrecondition)
		}
	}
	evaluationSpecReference := RevisionRef{ID: spec.EvaluationSpec, Revision: request.EvaluationSpecExpectedRevision}
	evaluationSpecDocument, err := resolve(inventory, evaluationSpecReference, research.KindEvaluationSpec)
	if err != nil {
		return nil, err
	}
	evaluationSpec := evaluationSpecDocument.Record.(*research.EvaluationSpec)
	if evaluationSpec.Purpose != research.EvaluationPromotion || evaluationSpec.SealedAt == nil {
		return nil, fmt.Errorf("Promotion uses an unsealed or non-promotion EvaluationSpec: %w", ErrPrecondition)
	}

	switch request.Outcome {
	case research.PromotionAccepted:
		if evaluation.Outcome != research.EvaluationPassed {
			return nil, fmt.Errorf("accepted Promotion requires a passing Evaluation: %w", ErrPrecondition)
		}
		if challenger.ID == state.championRelease {
			return nil, fmt.Errorf("challenger is already the champion: %w", ErrPrecondition)
		}
	case research.PromotionRejected:
		if challenger.ID == state.championRelease {
			return nil, fmt.Errorf("cannot reject the current champion as its own challenger: %w", ErrPrecondition)
		}
	case research.PromotionRolledBack:
		if evaluation.Outcome != research.EvaluationPassed {
			return nil, fmt.Errorf("rollback Promotion requires a passing Evaluation: %w", ErrPrecondition)
		}
		if state.championPromotion == nil {
			return nil, fmt.Errorf("target %s has no champion-setting Promotion to roll back: %w", request.Target, ErrPrecondition)
		}
		previousChampion := state.championPromotion.Record.(*research.Promotion).Incumbent
		if previousChampion.IsZero() || challenger.ID != previousChampion {
			return nil, fmt.Errorf("rollback must restore displaced incumbent %s, got %s: %w", previousChampion, challenger.ID, ErrPrecondition)
		}
	default:
		return nil, fmt.Errorf("unknown Promotion outcome %q: %w", request.Outcome, ErrPrecondition)
	}

	changes := newGuardedChanges()
	for _, guarded := range []struct {
		document *record.Document
		revision string
	}{{specDocument, request.Spec.Revision}, {challengerDocument, request.Challenger.Revision}, {evaluationDocument, request.Evaluation.Revision}, {evaluationSpecDocument, evaluationSpecReference.Revision}} {
		if err := changes.guard(guarded.document, guarded.revision); err != nil {
			return nil, err
		}
	}
	if state.tip != nil {
		if !record.ValidRevision(request.PreviousExpectedRevision) {
			return nil, fmt.Errorf("Promotion chain tip %s: %w", state.tipID(), ErrRevisionRequired)
		}
		if state.tip.Revision != request.PreviousExpectedRevision {
			return nil, &record.ConflictError{ID: state.tipID(), Expected: request.PreviousExpectedRevision, Actual: state.tip.Revision}
		}
		if err := changes.guard(state.tip, request.PreviousExpectedRevision); err != nil {
			return nil, err
		}
	} else if request.PreviousExpectedRevision != "" {
		return nil, fmt.Errorf("previous revision supplied for an empty Promotion chain: %w", ErrPrecondition)
	}
	if !state.championRelease.IsZero() {
		incumbentDocument, resolveErr := resolve(inventory, RevisionRef{ID: state.championRelease, Revision: request.IncumbentExpectedRevision}, research.KindRelease)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if incumbentDocument.Record.(*research.Release).Target != request.Target {
			return nil, fmt.Errorf("incumbent Release target mismatch: %w", ErrPrecondition)
		}
		if err := changes.guard(incumbentDocument, request.IncumbentExpectedRevision); err != nil {
			return nil, err
		}
	} else if request.IncumbentExpectedRevision != "" {
		return nil, fmt.Errorf("incumbent revision supplied when no champion exists: %w", ErrPrecondition)
	}

	now := service.now()
	reserved := make(map[research.ID]struct{})
	id, err := service.allocate(inventory, research.KindPromotion, now, reserved)
	if err != nil {
		return nil, err
	}
	promotion := &research.Promotion{
		Common: research.Common{
			Schema: research.SchemaPromotion, ID: id, Title: request.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), request.Tags...),
		},
		Target: request.Target, Spec: spec.ID, Challenger: challenger.ID,
		Incumbent: state.championRelease, Evaluation: evaluation.ID, Outcome: request.Outcome,
		AppliedAt: now, Previous: state.tipID(), ApprovedBy: request.ApprovedBy,
		Extensions: cloneExtensions(request.Extensions),
	}
	changes.create(&record.Document{Record: promotion, Body: request.Body})
	transaction, err := service.store.Transact(ctx, record.TransactionRequest{
		Operation: "promotion.append", Changes: changes.changes,
	})
	if err != nil {
		return nil, err
	}
	document, err := resultDocument(transaction, id)
	if err != nil {
		return nil, err
	}
	result := &AppendPromotionResult{TransactionID: transaction.TransactionID, Promotion: document}
	if request.Outcome == research.PromotionAccepted || request.Outcome == research.PromotionRolledBack {
		result.Champion = &record.Champion{Target: request.Target, Release: challenger.ID, Promotion: id}
	} else if !state.championRelease.IsZero() {
		result.Champion = &record.Champion{Target: request.Target, Release: state.championRelease, Promotion: state.championPromotionID}
	}
	return result, nil
}

type promotionState struct {
	tip                 *record.Document
	championRelease     research.ID
	championPromotionID research.ID
	championPromotion   *record.Document
}

func (state promotionState) tipID() research.ID {
	if state.tip == nil {
		return research.ID{}
	}
	id, _ := state.tip.ID()
	return id
}

func promotionStateFor(inventory *record.Inventory, target string) (promotionState, error) {
	var state promotionState
	byID := make(map[research.ID]*record.Document)
	followers := make(map[research.ID]*record.Document)
	var root *record.Document
	for _, document := range inventory.OfKind(research.KindPromotion) {
		promotion := document.Record.(*research.Promotion)
		id, _ := document.ID()
		byID[id] = document
		if promotion.Target != target {
			continue
		}
		if promotion.Previous.IsZero() {
			if root != nil {
				return state, fmt.Errorf("target %s has multiple Promotion roots: %w", target, ErrPrecondition)
			}
			root = document
		} else {
			if followers[promotion.Previous] != nil {
				return state, fmt.Errorf("Promotion %s has multiple followers: %w", promotion.Previous, ErrPrecondition)
			}
			followers[promotion.Previous] = document
		}
	}
	current := root
	visited := make(map[research.ID]struct{})
	for current != nil {
		id, _ := current.ID()
		if _, found := visited[id]; found {
			return state, fmt.Errorf("Promotion chain for %s is cyclic: %w", target, ErrPrecondition)
		}
		visited[id] = struct{}{}
		promotion := current.Record.(*research.Promotion)
		if promotion.Outcome == research.PromotionAccepted || promotion.Outcome == research.PromotionRolledBack {
			state.championRelease = promotion.Challenger
			state.championPromotionID = promotion.ID
			state.championPromotion = current
		}
		state.tip = current
		current = followers[id]
	}
	for previous := range followers {
		if _, found := byID[previous]; !found {
			return state, fmt.Errorf("Promotion chain references missing previous %s: %w", previous, ErrPrecondition)
		}
	}
	return state, nil
}
