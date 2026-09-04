package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

// EvaluationData is the immutable payload shared by standalone and
// release-coupled Evaluation creation.
type EvaluationData struct {
	Title        string
	Body         string
	Outcome      research.EvaluationOutcome
	Metrics      []research.MetricValue
	ExternalRefs []research.ExternalRef
	Summary      string
	Tags         []string
	Extensions   research.Extensions
}

// CreateEvaluationRequest records one result against an existing subject and
// exact EvaluationSpec.
type CreateEvaluationRequest struct {
	Spec    RevisionRef
	Subject RevisionRef
	Attempt RevisionRef
	Data    EvaluationData
}

// CreateEvaluationResult identifies the immutable audit record transaction.
type CreateEvaluationResult struct {
	TransactionID string
	Evaluation    *record.Document
}

// CreateEvaluation creates an immutable Evaluation from the provided metrics
// and safe external references.
func (service *Service) CreateEvaluation(ctx context.Context, request CreateEvaluationRequest) (*CreateEvaluationResult, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	specDocument, err := resolve(inventory, request.Spec, research.KindEvaluationSpec)
	if err != nil {
		return nil, err
	}
	subjectDocument, err := resolveAny(inventory, request.Subject, research.KindExperiment, research.KindCandidate, research.KindRelease)
	if err != nil {
		return nil, err
	}
	spec := specDocument.Record.(*research.EvaluationSpec)
	if err := validateMetrics(spec, request.Data.Metrics, request.Data.Outcome); err != nil {
		return nil, err
	}
	var attemptDocument *record.Document
	if !request.Attempt.ID.IsZero() || request.Attempt.Revision != "" {
		if subjectDocument.Kind() != research.KindExperiment {
			return nil, fmt.Errorf("Attempt-bound Evaluations require an Experiment subject: %w", ErrPrecondition)
		}
		attemptDocument, err = resolve(inventory, request.Attempt, research.KindAttempt)
		if err != nil {
			return nil, err
		}
		attempt := attemptDocument.Record.(*research.Attempt)
		if attempt.Schema != research.SchemaAttemptV3 || !attempt.Try.IsZero() || attempt.Run.IsZero() || attempt.State != research.AttemptSucceeded || attempt.Terminal == nil {
			return nil, fmt.Errorf("Evaluation backing Attempt %s is not a successful formal exp.attempt/v3: %w", attempt.ID, ErrPrecondition)
		}
		runDocument, runErr := inventory.ByID(attempt.Run)
		if runErr != nil {
			return nil, runErr
		}
		run, ok := runDocument.Record.(*research.Run)
		if !ok || run.Experiment != request.Subject.ID {
			return nil, fmt.Errorf("Evaluation backing Attempt %s does not belong to Experiment %s: %w", attempt.ID, request.Subject.ID, ErrPrecondition)
		}
	}
	now := service.now()
	if attemptDocument != nil && attemptDocument.Record.(*research.Attempt).Terminal.EndedAt.After(now) {
		return nil, fmt.Errorf("Evaluation cannot predate its backing Attempt: %w", ErrPrecondition)
	}
	reserved := make(map[research.ID]struct{})
	id, err := service.allocate(inventory, research.KindEvaluation, now, reserved)
	if err != nil {
		return nil, err
	}
	attemptID := research.ID{}
	if attemptDocument != nil {
		attemptID, _ = attemptDocument.ID()
	}
	evaluation := newEvaluation(id, request.Spec.ID, request.Subject.ID, attemptID, now, request.Data)
	changes := newGuardedChanges()
	if err := changes.guard(specDocument, request.Spec.Revision); err != nil {
		return nil, err
	}
	if err := changes.guard(subjectDocument, request.Subject.Revision); err != nil {
		return nil, err
	}
	if attemptDocument != nil {
		if err := changes.guard(attemptDocument, request.Attempt.Revision); err != nil {
			return nil, err
		}
	}
	changes.create(&record.Document{Record: evaluation, Body: request.Data.Body})
	transaction, transactionErr := service.store.Transact(ctx, record.TransactionRequest{
		Operation: "evaluation.create", Changes: changes.changes,
	})
	result := &CreateEvaluationResult{}
	if transaction != nil {
		result.TransactionID = transaction.TransactionID
		result.Evaluation, _ = resultDocument(transaction, id)
	}
	if transactionErr != nil {
		if transaction == nil {
			return nil, transactionErr
		}
		return result, lifecycleTransactionError(transaction, transactionErr)
	}
	if result.Evaluation == nil {
		return nil, fmt.Errorf("canonical transaction omitted Evaluation %s", id)
	}
	return result, nil
}

func newEvaluation(id, spec, subject, attempt research.ID, now time.Time, data EvaluationData) *research.Evaluation {
	schema := research.SchemaEvaluation
	if !attempt.IsZero() {
		schema = research.SchemaEvaluationV2
	}
	return &research.Evaluation{
		Common: research.Common{
			Schema: schema, ID: id, Title: data.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), data.Tags...),
		},
		Spec: spec, Subject: subject, Attempt: attempt, Outcome: data.Outcome, EvaluatedAt: now,
		Metrics:      append([]research.MetricValue(nil), data.Metrics...),
		ExternalRefs: cloneExternalRefs(data.ExternalRefs), Summary: data.Summary,
		Extensions: cloneExtensions(data.Extensions),
	}
}

func validateMetrics(spec *research.EvaluationSpec, metrics []research.MetricValue, outcome research.EvaluationOutcome) error {
	declared := make(map[string]research.MetricSpec, len(spec.Metrics))
	for _, metric := range spec.Metrics {
		declared[metric.Name] = metric
	}
	observed := make(map[string]research.MetricValue, len(metrics))
	for _, metric := range metrics {
		if _, duplicate := observed[metric.Name]; duplicate {
			return fmt.Errorf("metric %s occurs more than once: %w", metric.Name, ErrPrecondition)
		}
		specification, found := declared[metric.Name]
		if !found || specification.Unit != metric.Unit {
			return fmt.Errorf("metric %s (%s) is not declared by EvaluationSpec %s: %w", metric.Name, metric.Unit, spec.ID, ErrPrecondition)
		}
		observed[metric.Name] = metric
	}
	if len(observed) != len(declared) {
		return fmt.Errorf("Evaluation supplies %d of %d declared metrics: %w", len(observed), len(declared), ErrPrecondition)
	}
	thresholds, passed := 0, true
	for name, specification := range declared {
		if specification.Threshold == nil {
			continue
		}
		thresholds++
		value := observed[name].Value
		switch specification.Direction {
		case research.MetricMaximize:
			passed = passed && value >= *specification.Threshold
		case research.MetricMinimize:
			passed = passed && value <= *specification.Threshold
		}
	}
	if thresholds > 0 && outcome != research.EvaluationInvalid {
		if passed && outcome != research.EvaluationPassed {
			return fmt.Errorf("all declared thresholds pass, so outcome must be passed: %w", ErrPrecondition)
		}
		if !passed && outcome != research.EvaluationFailed {
			return fmt.Errorf("at least one declared threshold fails, so outcome must be failed: %w", ErrPrecondition)
		}
	}
	return nil
}
