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
	now := service.now()
	reserved := make(map[research.ID]struct{})
	id, err := service.allocate(inventory, research.KindEvaluation, now, reserved)
	if err != nil {
		return nil, err
	}
	evaluation := newEvaluation(id, request.Spec.ID, request.Subject.ID, now, request.Data)
	changes := newGuardedChanges()
	if err := changes.guard(specDocument, request.Spec.Revision); err != nil {
		return nil, err
	}
	if err := changes.guard(subjectDocument, request.Subject.Revision); err != nil {
		return nil, err
	}
	changes.create(&record.Document{Record: evaluation, Body: request.Data.Body})
	transaction, err := service.store.Transact(ctx, record.TransactionRequest{
		Operation: "evaluation.create", Changes: changes.changes,
	})
	if err != nil {
		return nil, err
	}
	document, err := resultDocument(transaction, id)
	if err != nil {
		return nil, err
	}
	return &CreateEvaluationResult{TransactionID: transaction.TransactionID, Evaluation: document}, nil
}

func newEvaluation(id, spec, subject research.ID, now time.Time, data EvaluationData) *research.Evaluation {
	return &research.Evaluation{
		Common: research.Common{
			Schema: research.SchemaEvaluation, ID: id, Title: data.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), data.Tags...),
		},
		Spec: spec, Subject: subject, Outcome: data.Outcome, EvaluatedAt: now,
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
