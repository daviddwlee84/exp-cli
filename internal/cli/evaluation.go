package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/lifecycle"
	"github.com/daviddwlee84/exp-cli/internal/mlflow"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

type evaluationOptions struct {
	json        bool
	title       string
	body        string
	purpose     string
	dataset     string
	protocol    string
	metrics     []string
	pool        string
	budgetHours float64
	sealed      bool
	spec        string
	subject     string
	outcome     string
	summary     string
	tags        []string
	mlflowRunID string
	mlflowCtx   string
	mlflowTags  []string
	allowEnv    []string
	secretEnv   []string
}

func newEvaluationCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "evaluation", Short: "Define comparable protocols and record immutable results", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newEvaluationSpecCommand(app, root), newEvaluationCreateCommand(app, root))
	return command
}

func newEvaluationSpecCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "spec", Short: "Work with EvaluationSpecs", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	options := &evaluationOptions{purpose: string(research.EvaluationScientific)}
	create := &cobra.Command{Use: "create", Short: "Create a scientific or sealed promotion EvaluationSpec", Args: cobra.NoArgs}
	create.RunE = func(command *cobra.Command, _ []string) error {
		return runEvaluationSpecCreate(command, app, root, options)
	}
	flags := create.Flags()
	flags.StringVar(&options.title, "title", "", "set the specification title")
	flags.StringVar(&options.body, "body", "", "set optional Markdown detail")
	flags.StringVar(&options.purpose, "purpose", string(research.EvaluationScientific), "set scientific or promotion")
	flags.StringVar(&options.dataset, "dataset", "", "identify the frozen dataset or split")
	flags.StringVar(&options.protocol, "protocol", "", "describe the comparable evaluation protocol")
	flags.StringSliceVar(&options.metrics, "metric", nil, "declare NAME:UNIT:DIRECTION[:THRESHOLD]")
	flags.StringVar(&options.pool, "pool", "", "set the budget ResourcePool")
	flags.Float64Var(&options.budgetHours, "budget-hours", 0, "set the finite evaluation budget")
	flags.BoolVar(&options.sealed, "sealed", false, "seal the spec now; required for promotion purpose")
	flags.StringSliceVar(&options.tags, "tags", nil, "set free tags")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	command.AddCommand(create)
	return command
}

func newEvaluationCreateCommand(app *App, root *rootOptions) *cobra.Command {
	options := &evaluationOptions{}
	command := &cobra.Command{Use: "create", Short: "Create an immutable Evaluation under an exact spec and subject", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error {
		return runEvaluationCreate(command, app, root, options)
	}
	flags := command.Flags()
	flags.StringVar(&options.title, "title", "", "set the Evaluation title")
	flags.StringVar(&options.body, "body", "", "set optional Markdown detail")
	flags.StringVar(&options.spec, "spec", "", "reference the EvaluationSpec")
	flags.StringVar(&options.subject, "subject", "", "reference an Experiment, Candidate, or Release")
	flags.StringVar(&options.outcome, "outcome", "", "set passed, failed, or invalid")
	flags.StringSliceVar(&options.metrics, "metric", nil, "record NAME=VALUE:UNIT for every declared metric")
	flags.StringVar(&options.summary, "summary", "", "summarize the result without overstating evidence")
	flags.StringSliceVar(&options.tags, "tags", nil, "set free tags")
	flags.StringVar(&options.mlflowRunID, "mlflow-run-id", "", "verify and attach a workload-owned MLflow run ID")
	flags.StringVar(&options.mlflowCtx, "mlflow-context", "default", "set a non-secret MLflow context name")
	flags.StringSliceVar(&options.mlflowTags, "mlflow-tag", nil, "require an MLflow NAME=VALUE tag (attachments require exp.attempt_id)")
	flags.StringSliceVar(&options.allowEnv, "allow-env", nil, "inherit a non-secret variable for MLflow verification")
	flags.StringSliceVar(&options.secretEnv, "secret-env", nil, "bind a required MLflow secret by environment name")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runEvaluationSpecCreate(command *cobra.Command, app *App, root *rootOptions, options *evaluationOptions) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "evaluation spec create", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "evaluation spec create", struct{}{}, false, nil, err)
	}
	poolDocument, err := inventory.Resolve(options.pool, research.KindResourcePool)
	if err != nil {
		return commandFailure(app, options.json, "evaluation spec create", struct{}{}, false, nil, err)
	}
	metrics, err := parseMetricSpecs(options.metrics)
	if err != nil {
		return commandFailure(app, options.json, "evaluation spec create", struct{}{}, false, nil, err)
	}
	now := app.clock()
	id, err := generatedID(app, research.KindEvaluationSpec, now)
	if err != nil {
		return commandFailure(app, options.json, "evaluation spec create", struct{}{}, false, nil, err)
	}
	poolID, _ := poolDocument.ID()
	var sealedAt *time.Time
	if options.sealed {
		value := now
		sealedAt = &value
	}
	body := options.body
	if body == "" {
		body = "\n# " + options.title + "\n\n" + options.protocol + "\n"
	}
	spec := &research.EvaluationSpec{
		Common:  research.Common{Schema: research.SchemaEvaluationSpec, ID: id, Title: options.title, CreatedAt: now, UpdatedAt: now, Tags: options.tags},
		Purpose: research.EvaluationPurpose(options.purpose), Dataset: options.dataset, Protocol: options.protocol,
		Metrics: metrics, BudgetPool: poolID, BudgetHours: options.budgetHours, SealedAt: sealedAt,
	}
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "evaluation-spec.create", Changes: []record.TransactionChange{{Operation: record.TransactionCreate, Document: &record.Document{Record: spec, Body: body}}}})
	if err != nil {
		return commandFailure(app, options.json, "evaluation spec create", struct{}{}, false, nil, err)
	}
	published := transactionDocument(result, research.KindEvaluationSpec)
	data := struct {
		Spec canonicalRecordView `json:"spec"`
	}{Spec: canonicalView(published)}
	return commandSuccess(app, options.json, "evaluation spec create", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Created EvaluationSpec %s.\n", id))
}

func runEvaluationCreate(command *cobra.Command, app *App, root *rootOptions, options *evaluationOptions) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, err)
	}
	specDocument, err := inventory.Resolve(options.spec, research.KindEvaluationSpec)
	if err != nil {
		return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, err)
	}
	subjectDocument, err := inventory.Resolve(options.subject, research.KindUnknown)
	if err != nil {
		return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, err)
	}
	switch subjectDocument.Kind() {
	case research.KindExperiment, research.KindCandidate, research.KindRelease:
	default:
		return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, errors.New("evaluation subject must be an Experiment, Candidate, or Release"))
	}
	subjectID, _ := subjectDocument.ID()
	metrics, err := parseMetricValues(options.metrics)
	if err != nil {
		return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, err)
	}
	externalRefs := []research.ExternalRef{}
	if options.mlflowRunID != "" {
		expectedTags := map[string]string{}
		for _, item := range options.mlflowTags {
			name, value, found := strings.Cut(item, "=")
			if !found || name == "" {
				return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, fmt.Errorf("MLflow tag %q must be NAME=VALUE", item))
			}
			if _, duplicate := expectedTags[name]; duplicate {
				return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, fmt.Errorf("MLflow tag %q is supplied more than once", name))
			}
			expectedTags[name] = value
		}
		ownerAttempt, ownershipErr := requireMLflowWorkloadOwnership(inventory, subjectDocument, expectedTags)
		if ownershipErr != nil {
			return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, ownershipErr)
		}
		allowed := append(execx.MinimalAllowlist(), options.allowEnv...)
		bindings := make([]execx.Binding, 0, len(options.secretEnv))
		for _, name := range options.secretEnv {
			bindings = append(bindings, execx.BindSecretFromEnv(name, name))
		}
		environment, environmentErr := execx.NewEnvironment(allowed, bindings...)
		if environmentErr != nil {
			return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, environmentErr)
		}
		metricNames := make([]string, 0, len(metrics))
		for _, metric := range metrics {
			metricNames = append(metricNames, metric.Name)
		}
		verified, verifyErr := (mlflow.Adapter{Invoker: app.Invoker, LookupBinary: app.BinaryLookup}).Describe(command.Context(), mlflow.DescribeRequest{
			RunID: options.mlflowRunID, MetricNames: metricNames, ExpectedTags: expectedTags, Environment: environment, CWD: info.Repository.Root,
		})
		if verifyErr != nil || !verified.Verified {
			if verifyErr == nil {
				verifyErr = fmt.Errorf("MLflow verification failed: %s", strings.Join(verified.Diagnostics, "; "))
			}
			return commandFailure(app, options.json, "evaluation create", verified, false, nil, verifyErr)
		}
		for _, metric := range metrics {
			if observedValue, found := verified.Metrics[metric.Name]; !found || observedValue != metric.Value {
				return commandFailure(app, options.json, "evaluation create", verified, false, nil, fmt.Errorf("metric %s does not equal the verified MLflow value", metric.Name))
			}
		}
		observed := app.clock()
		externalRefs = append(externalRefs, research.ExternalRef{
			Role: research.ExternalTracker, Provider: "mlflow", Context: options.mlflowCtx, NativeKind: "run", NativeID: options.mlflowRunID,
			URI: verified.ArtifactURI, ObservedAt: &observed,
			Metadata: map[string]any{"mlflow.status": verified.Status, "mlflow.experiment_id": verified.ExperimentID, "mlflow.verified": true, "mlflow.owner_attempt": ownerAttempt.String(), "mlflow.owner_subject": subjectID.String()},
		})
	}
	specID, _ := specDocument.ID()
	service := lifecycle.New(store, lifecycle.WithClock(app.clock), lifecycle.WithUUIDGenerator(app.GenerateUUID))
	result, err := service.CreateEvaluation(command.Context(), lifecycle.CreateEvaluationRequest{
		Spec:    lifecycle.RevisionRef{ID: specID, Revision: specDocument.Revision},
		Subject: lifecycle.RevisionRef{ID: subjectID, Revision: subjectDocument.Revision},
		Data:    lifecycle.EvaluationData{Title: options.title, Body: options.body, Outcome: research.EvaluationOutcome(options.outcome), Metrics: metrics, ExternalRefs: externalRefs, Summary: options.summary, Tags: options.tags},
	})
	if err != nil {
		return commandFailure(app, options.json, "evaluation create", struct{}{}, false, nil, err)
	}
	data := struct {
		Evaluation  canonicalRecordView `json:"evaluation"`
		Transaction string              `json:"transaction_id"`
	}{Evaluation: canonicalView(result.Evaluation), Transaction: result.TransactionID}
	return commandSuccess(app, options.json, "evaluation create", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Created Evaluation %s.\n", data.Evaluation.ID))
}

const mlflowAttemptOwnershipTag = "exp.attempt_id"

// requireMLflowWorkloadOwnership makes the read-only tag assertion do double
// duty as local ownership evidence: the run must identify an exact canonical
// Attempt whose Run belongs to the Evaluation subject's supported lineage,
// rather than merely being any finished MLflow run in the project.
func requireMLflowWorkloadOwnership(inventory *record.Inventory, subject *record.Document, expectedTags map[string]string) (research.ID, error) {
	if inventory == nil {
		return research.ID{}, errors.New("MLflow attachment requires a canonical inventory")
	}
	if subject == nil {
		return research.ID{}, errors.New("MLflow attachment requires a canonical Evaluation subject")
	}
	subjectID, ok := subject.ID()
	if !ok {
		return research.ID{}, errors.New("MLflow Evaluation subject has no canonical ID")
	}
	value, found := expectedTags[mlflowAttemptOwnershipTag]
	if !found || value == "" {
		return research.ID{}, fmt.Errorf("MLflow attachment requires --mlflow-tag %s=<canonical-attempt-id>", mlflowAttemptOwnershipTag)
	}
	id, err := research.ParseIDForKind(value, research.KindAttempt)
	if err != nil {
		return research.ID{}, fmt.Errorf("MLflow ownership tag %s: %w", mlflowAttemptOwnershipTag, err)
	}
	document, err := inventory.ByID(id)
	if err != nil {
		return research.ID{}, fmt.Errorf("MLflow ownership tag %s does not identify an Attempt in this project: %w", mlflowAttemptOwnershipTag, err)
	}
	if document.Kind() != research.KindAttempt {
		return research.ID{}, fmt.Errorf("MLflow ownership tag %s does not identify a canonical Attempt", mlflowAttemptOwnershipTag)
	}
	attempt := document.Record.(*research.Attempt)
	if attempt.State != research.AttemptSucceeded || attempt.Terminal == nil {
		return research.ID{}, fmt.Errorf("MLflow owner Attempt %s is not a successful terminal execution", id)
	}
	runDocument, err := inventory.ByID(attempt.Run)
	if err != nil || runDocument.Kind() != research.KindRun {
		return research.ID{}, fmt.Errorf("MLflow owner Attempt %s has no canonical Run lineage", id)
	}
	run := runDocument.Record.(*research.Run)
	if !mlflowSubjectIncludesExperiment(inventory, subject, run.Experiment) {
		return research.ID{}, fmt.Errorf("MLflow owner Attempt %s is outside Evaluation subject %s lineage", id, subjectID)
	}
	return id, nil
}

func mlflowSubjectIncludesExperiment(inventory *record.Inventory, subject *record.Document, experimentID research.ID) bool {
	switch value := subject.Record.(type) {
	case *research.Experiment:
		return value.ID == experimentID
	case *research.Candidate:
		return value.Experiment == experimentID
	case *research.Release:
		if !value.CombinationExperiment.IsZero() {
			return value.CombinationExperiment == experimentID
		}
		for _, slot := range value.Slots {
			candidate, err := inventory.ByID(slot.Candidate)
			if err == nil && candidate.Record.(*research.Candidate).Experiment == experimentID {
				return true
			}
		}
	}
	return false
}

func parseMetricSpecs(values []string) ([]research.MetricSpec, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one --metric NAME:UNIT:DIRECTION[:THRESHOLD] is required")
	}
	result := make([]research.MetricSpec, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) < 3 || len(parts) > 4 {
			return nil, fmt.Errorf("metric %q must be NAME:UNIT:DIRECTION[:THRESHOLD]", value)
		}
		metric := research.MetricSpec{Name: parts[0], Unit: parts[1], Direction: research.MetricDirection(parts[2])}
		if len(parts) == 4 {
			threshold, err := strconv.ParseFloat(parts[3], 64)
			if err != nil {
				return nil, fmt.Errorf("metric %q threshold: %w", value, err)
			}
			metric.Threshold = &threshold
		}
		result = append(result, metric)
	}
	return result, nil
}

func parseMetricValues(values []string) ([]research.MetricValue, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one --metric NAME=VALUE:UNIT is required")
	}
	result := make([]research.MetricValue, 0, len(values))
	for _, item := range values {
		name, rest, found := strings.Cut(item, "=")
		valueText, unit, unitFound := strings.Cut(rest, ":")
		if !found || !unitFound || name == "" || unit == "" {
			return nil, fmt.Errorf("metric %q must be NAME=VALUE:UNIT", item)
		}
		value, err := strconv.ParseFloat(valueText, 64)
		if err != nil {
			return nil, fmt.Errorf("metric %q value: %w", item, err)
		}
		result = append(result, research.MetricValue{Name: name, Value: value, Unit: unit})
	}
	return result, nil
}
