package lifecycle

import (
	"context"
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

// ConclusionEvidenceInput pins one Run used by a concluded Experiment.
type ConclusionEvidenceInput struct {
	Run         RevisionRef
	Disposition research.EvidenceDisposition
	Reason      string
}

// FindingEvidenceInput pins one Run supporting a new Finding.
type FindingEvidenceInput struct {
	Run    RevisionRef
	Detail string
}

// FindingInput is one Finding created atomically with Experiment closure.
type FindingInput struct {
	Title      string
	Body       string
	Statement  string
	Scope      string
	Weakens    []RevisionRef
	Overturns  []RevisionRef
	Evidence   []FindingEvidenceInput
	Tags       []string
	Extensions research.Extensions
}

// CloseExperimentRequest concludes an active Experiment and completes its
// originating Plan in one prepared transaction.
type CloseExperimentRequest struct {
	Experiment RevisionRef
	Plan       RevisionRef
	Verdict    research.Verdict
	Summary    string
	Evidence   []ConclusionEvidenceInput
	Findings   []FindingInput
}

// CloseExperimentResult contains every canonical document changed by closure.
type CloseExperimentResult struct {
	TransactionID string
	Experiment    *record.Document
	Plan          *record.Document
	Findings      []*record.Document
}

// CloseExperiment atomically publishes the Experiment Conclusion, optional
// Findings, and originating Plan completion.
func (service *Service) CloseExperiment(ctx context.Context, request CloseExperimentRequest) (*CloseExperimentResult, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	experimentDocument, err := resolve(inventory, request.Experiment, research.KindExperiment)
	if err != nil {
		return nil, err
	}
	planDocument, err := resolve(inventory, request.Plan, research.KindPlan)
	if err != nil {
		return nil, err
	}
	experiment := experimentDocument.Record.(*research.Experiment)
	plan := planDocument.Record.(*research.Plan)
	if experiment.Lifecycle != research.LifecycleActive {
		return nil, fmt.Errorf("Experiment %s is %s, not active: %w", experiment.ID, experiment.Lifecycle, ErrPrecondition)
	}
	if plan.State != research.PlanStarted || plan.ResultingExperiment != experiment.ID {
		return nil, fmt.Errorf("Plan %s is not the started origin of Experiment %s: %w", plan.ID, experiment.ID, ErrPrecondition)
	}
	switch request.Verdict {
	case research.VerdictSupported, research.VerdictRefuted, research.VerdictInconclusive, research.VerdictInvalid:
	default:
		return nil, fmt.Errorf("verdict %q is not valid for a conclusion: %w", request.Verdict, ErrPrecondition)
	}

	now := service.now()
	if now.Before(experiment.UpdatedAt) || now.Before(plan.UpdatedAt) {
		return nil, fmt.Errorf("closure timestamp precedes canonical state: %w", ErrPrecondition)
	}
	changes := newGuardedChanges()
	conclusionEvidence := make([]research.ConclusionEvidence, 0, len(request.Evidence))
	includedEvidence := 0
	includedRuns := map[research.ID]struct{}{}
	for _, input := range request.Evidence {
		runDocument, resolveErr := resolve(inventory, input.Run, research.KindRun)
		if resolveErr != nil {
			return nil, resolveErr
		}
		run := runDocument.Record.(*research.Run)
		if run.Experiment != experiment.ID {
			return nil, fmt.Errorf("Run %s belongs to Experiment %s, not %s: %w", run.ID, run.Experiment, experiment.ID, ErrPrecondition)
		}
		if guardErr := changes.guard(runDocument, input.Run.Revision); guardErr != nil {
			return nil, guardErr
		}
		conclusionEvidence = append(conclusionEvidence, research.ConclusionEvidence{
			Run: input.Run.ID, Disposition: input.Disposition, Reason: input.Reason,
		})
		if input.Disposition == research.EvidenceIncluded {
			if !runHasSuccessfulDirectAttempt(inventory, input.Run.ID) {
				return nil, fmt.Errorf("included Run %s has no successful direct Attempt: %w", input.Run.ID, ErrPrecondition)
			}
			includedEvidence++
			includedRuns[input.Run.ID] = struct{}{}
		}
	}
	if (request.Verdict == research.VerdictSupported || request.Verdict == research.VerdictRefuted) && includedEvidence == 0 {
		return nil, fmt.Errorf("definitive verdict %s requires at least one included Run: %w", request.Verdict, ErrPrecondition)
	}

	closedDocument := experimentDocument.Clone()
	closed := closedDocument.Record.(*research.Experiment)
	closed.UpdatedAt = now
	closed.Lifecycle = research.LifecycleClosed
	closed.Closure = research.ClosureConcluded
	closed.Verdict = request.Verdict
	closed.ClosureDetail = nil
	closed.Conclusion = &research.Conclusion{ConcludedAt: now, Summary: request.Summary, Evidence: conclusionEvidence}
	if err := changes.replace(closedDocument, request.Experiment.Revision); err != nil {
		return nil, err
	}

	completedDocument := planDocument.Clone()
	completed := completedDocument.Record.(*research.Plan)
	completed.UpdatedAt = now
	completed.State = research.PlanCompleted
	if err := changes.replace(completedDocument, request.Plan.Revision); err != nil {
		return nil, err
	}

	reserved := make(map[research.ID]struct{})
	findingIDs := make([]research.ID, 0, len(request.Findings))
	for _, input := range request.Findings {
		id, allocateErr := service.allocate(inventory, research.KindFinding, now, reserved)
		if allocateErr != nil {
			return nil, allocateErr
		}
		findingIDs = append(findingIDs, id)
		evidence := make([]research.FindingEvidence, 0, len(input.Evidence))
		for _, evidenceInput := range input.Evidence {
			if _, included := includedRuns[evidenceInput.Run.ID]; !included {
				return nil, fmt.Errorf("Finding evidence Run %s is not included in the Experiment conclusion: %w", evidenceInput.Run.ID, ErrPrecondition)
			}
			runDocument, resolveErr := resolve(inventory, evidenceInput.Run, research.KindRun)
			if resolveErr != nil {
				return nil, resolveErr
			}
			run := runDocument.Record.(*research.Run)
			if run.Experiment != experiment.ID {
				return nil, fmt.Errorf("Finding evidence Run %s belongs to Experiment %s, not %s: %w", run.ID, run.Experiment, experiment.ID, ErrPrecondition)
			}
			if guardErr := changes.guard(runDocument, evidenceInput.Run.Revision); guardErr != nil {
				return nil, guardErr
			}
			evidence = append(evidence, research.FindingEvidence{
				Kind: research.FindingEvidenceRun, Ref: evidenceInput.Run.ID, Detail: evidenceInput.Detail,
			})
		}
		weakens, resolveErr := resolveFindingRelations(inventory, changes, input.Weakens)
		if resolveErr != nil {
			return nil, resolveErr
		}
		overturns, resolveErr := resolveFindingRelations(inventory, changes, input.Overturns)
		if resolveErr != nil {
			return nil, resolveErr
		}
		finding := &research.Finding{
			Common: research.Common{
				Schema: research.SchemaFinding, ID: id, Title: input.Title,
				CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), input.Tags...),
			},
			Statement: input.Statement, Scope: input.Scope,
			Weakens: weakens, Overturns: overturns, Evidence: evidence,
			Extensions: cloneExtensions(input.Extensions),
		}
		changes.create(&record.Document{Record: finding, Body: input.Body})
	}

	transaction, err := service.store.Transact(ctx, record.TransactionRequest{
		Operation: "experiment.close", Changes: changes.changes, AllowStale: true,
	})
	if err != nil {
		return nil, err
	}
	closedResult, err := resultDocument(transaction, experiment.ID)
	if err != nil {
		return nil, err
	}
	planResult, err := resultDocument(transaction, plan.ID)
	if err != nil {
		return nil, err
	}
	result := &CloseExperimentResult{TransactionID: transaction.TransactionID, Experiment: closedResult, Plan: planResult}
	for _, id := range findingIDs {
		document, findErr := resultDocument(transaction, id)
		if findErr != nil {
			return nil, findErr
		}
		result.Findings = append(result.Findings, document)
	}
	return result, nil
}

func runHasSuccessfulDirectAttempt(inventory *record.Inventory, run research.ID) bool {
	if inventory == nil {
		return false
	}
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		if attempt.Run == run && attempt.State == research.AttemptSucceeded && attempt.Terminal != nil && attempt.Terminal.Source == "direct" {
			return true
		}
	}
	return false
}

func resolveFindingRelations(inventory *record.Inventory, changes *guardedChanges, references []RevisionRef) ([]research.ID, error) {
	ids := make([]research.ID, 0, len(references))
	for _, reference := range references {
		document, err := resolve(inventory, reference, research.KindFinding)
		if err != nil {
			return nil, err
		}
		if err := changes.guard(document, reference.Revision); err != nil {
			return nil, err
		}
		ids = append(ids, reference.ID)
	}
	return ids, nil
}
