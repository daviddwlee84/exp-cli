package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/controlplane"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/mlflow"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

type providerOpsOptions struct {
	json      bool
	runID     string
	metrics   []string
	tags      []string
	allowEnv  []string
	secretEnv []string
	confirm   bool
	config    string
}

func newProviderCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "provider", Short: "Run explicit audited reads or controls against supported tools", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newPueueProviderCommand(app, root), newMLflowProviderCommand(app, root))
	return command
}

func newPueueProviderCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "pueue", Short: "Inspect or explicitly cancel local Pueue tasks", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	statusOptions := &providerOpsOptions{}
	status := &cobra.Command{Use: "status", Short: "Read a sanitized Pueue scheduler snapshot", Args: cobra.NoArgs}
	status.RunE = func(command *cobra.Command, _ []string) error {
		return runPueueStatus(command, app, root, statusOptions)
	}
	status.Flags().BoolVar(&statusOptions.json, "json", false, jsonFlagUsage)
	cancelOptions := &providerOpsOptions{}
	cancel := &cobra.Command{Use: "cancel <task-id>", Short: "Explicitly cancel one Pueue task", Args: cobra.ExactArgs(1)}
	cancel.RunE = func(command *cobra.Command, args []string) error {
		return runPueueCancel(command, app, root, cancelOptions, args[0])
	}
	cancel.Flags().BoolVar(&cancelOptions.confirm, "confirm", false, "confirm cancellation of the exact task ID")
	cancel.Flags().StringVar(&cancelOptions.config, "config", controlplane.DefaultConfigPath, "set the project-relative runtime contract path")
	cancel.Flags().BoolVar(&cancelOptions.json, "json", false, jsonFlagUsage)
	command.AddCommand(status, cancel)
	return command
}

func newMLflowProviderCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "mlflow", Short: "Verify workload-owned MLflow runs without creating them", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	options := &providerOpsOptions{}
	verify := &cobra.Command{Use: "verify", Short: "Read only requested metrics/tags from an explicit MLflow run", Args: cobra.NoArgs}
	verify.RunE = func(command *cobra.Command, _ []string) error { return runMLflowVerify(command, app, root, options) }
	flags := verify.Flags()
	flags.StringVar(&options.runID, "run-id", "", "set the explicit workload-owned MLflow run ID")
	flags.StringSliceVar(&options.metrics, "metric", nil, "request a metric by exact name")
	flags.StringSliceVar(&options.tags, "tag", nil, "verify NAME=VALUE without returning other tags")
	flags.StringSliceVar(&options.allowEnv, "allow-env", nil, "inherit an additional non-secret environment variable")
	flags.StringSliceVar(&options.secretEnv, "secret-env", nil, "bind a required secret from the same parent environment name")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	_ = verify.MarkFlagRequired("run-id")
	command.AddCommand(verify)
	return command
}

func runPueueStatus(command *cobra.Command, app *App, root *rootOptions, options *providerOpsOptions) error {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return commandFailure(app, options.json, "provider pueue status", pueue.Snapshot{}, false, nil, err)
	}
	if _, err = app.DiscoverProject(command.Context(), start); err != nil {
		return commandFailure(app, options.json, "provider pueue status", pueue.Snapshot{}, false, nil, err)
	}
	snapshot, err := (pueue.Adapter{Invoker: app.Invoker, LookupBinary: app.BinaryLookup}).Status(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "provider pueue status", snapshot, false, nil, err)
	}
	var human strings.Builder
	for _, group := range snapshot.Groups {
		fmt.Fprintf(&human, "group %s state=%s parallelism=%d\n", group.Name, group.State, group.Parallelism)
	}
	for _, task := range snapshot.Tasks {
		fmt.Fprintf(&human, "task %d group=%s state=%s label=%s\n", task.ID, task.Group, task.State, task.Label)
	}
	if len(snapshot.Tasks) == 0 {
		human.WriteString("No Pueue tasks.\n")
	}
	return commandSuccess(app, options.json, "provider pueue status", snapshot, false, nil, human.String())
}

func runPueueCancel(command *cobra.Command, app *App, root *rootOptions, options *providerOpsOptions, rawID string) error {
	if !options.confirm {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, errors.New("Pueue cancellation requires --confirm"))
	}
	taskID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || taskID < 0 {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, errors.New("task ID must be a non-negative integer"))
	}
	start, err := app.startDir(root.startDir)
	var info *project.Info
	if err == nil {
		info, err = app.DiscoverProject(command.Context(), start)
	}
	if err != nil {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, err)
	}
	store, err := app.NewTransactionalStore(info)
	if err != nil {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, err)
	}
	nativeID := strconv.FormatInt(taskID, 10)
	ownedAttempt, reference, err := findPueueCancelOwnership(inventory, nativeID)
	if err != nil {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, err)
	}
	identity, err := controlplane.ResolvePueueTaskIdentity(command.Context(), info.Repository.Root, options.config, ownedAttempt)
	if err != nil {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, err)
	}
	adapter := pueue.Adapter{Invoker: app.Invoker, LookupBinary: app.BinaryLookup}
	snapshot, err := adapter.Status(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, err)
	}
	if err := verifyPueueCancelIdentity(snapshot, taskID, reference, identity); err != nil {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, err)
	}
	environment, err := execx.MinimalEnvironment()
	if err == nil {
		err = adapter.Cancel(command.Context(), taskID, environment)
	}
	if err != nil {
		return commandFailure(app, options.json, "provider pueue cancel", struct{}{}, false, nil, err)
	}
	data := struct {
		TaskID int64 `json:"task_id"`
	}{TaskID: taskID}
	return commandSuccess(app, options.json, "provider pueue cancel", data, false, nil, fmt.Sprintf("Cancellation requested for Pueue task %d.\n", taskID))
}

func findPueueCancelOwnership(inventory *record.Inventory, nativeID string) (*research.Attempt, research.ExternalRef, error) {
	if inventory == nil {
		return nil, research.ExternalRef{}, errors.New("canonical inventory is required")
	}
	type ownership struct {
		attempt   *research.Attempt
		reference research.ExternalRef
	}
	var matches []ownership
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		for _, reference := range attempt.ExternalRefs {
			if reference.Role == research.ExternalScheduler && reference.Provider == "pueue" && reference.NativeKind == "task" && reference.NativeID == nativeID {
				matches = append(matches, ownership{attempt: attempt, reference: reference})
			}
		}
	}
	if len(matches) == 0 {
		return nil, research.ExternalRef{}, errors.New("Pueue task is not referenced by a canonical Attempt in this project")
	}
	if len(matches) != 1 {
		return nil, research.ExternalRef{}, errors.New("Pueue task ownership is ambiguous across canonical Attempt references")
	}
	if matches[0].attempt.Scheduler != "pueue" {
		return nil, research.ExternalRef{}, errors.New("canonical Attempt does not assign scheduling ownership to Pueue")
	}
	return matches[0].attempt, matches[0].reference, nil
}

func verifyPueueCancelIdentity(snapshot pueue.Snapshot, taskID int64, reference research.ExternalRef, expected controlplane.PueueTaskIdentity) error {
	if reference.Context != expected.Context {
		return errors.New("Pueue task reference context does not match the local runtime context")
	}
	var live *pueue.Task
	for index := range snapshot.Tasks {
		if snapshot.Tasks[index].ID != taskID {
			continue
		}
		if live != nil {
			return errors.New("Pueue status contains an ambiguous task identity")
		}
		live = &snapshot.Tasks[index]
	}
	if live == nil {
		return errors.New("Pueue task is not present in the live scheduler snapshot")
	}
	if live.Group != expected.Group || live.Label != expected.Label {
		return errors.New("live Pueue task group/label does not match the canonical runtime identity")
	}
	return nil
}

func runMLflowVerify(command *cobra.Command, app *App, root *rootOptions, options *providerOpsOptions) error {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return commandFailure(app, options.json, "provider mlflow verify", mlflow.Run{}, false, nil, err)
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, "provider mlflow verify", mlflow.Run{}, false, nil, err)
	}
	expectedTags := map[string]string{}
	for _, item := range options.tags {
		name, value, found := strings.Cut(item, "=")
		if !found || name == "" {
			return commandFailure(app, options.json, "provider mlflow verify", mlflow.Run{}, false, nil, fmt.Errorf("tag %q must be NAME=VALUE", item))
		}
		expectedTags[name] = value
	}
	allowed := append(execx.MinimalAllowlist(), options.allowEnv...)
	bindings := make([]execx.Binding, 0, len(options.secretEnv))
	for _, name := range options.secretEnv {
		bindings = append(bindings, execx.BindSecretFromEnv(name, name))
	}
	environment, err := execx.NewEnvironment(allowed, bindings...)
	if err != nil {
		return commandFailure(app, options.json, "provider mlflow verify", mlflow.Run{}, false, nil, err)
	}
	run, err := (mlflow.Adapter{Invoker: app.Invoker, LookupBinary: app.BinaryLookup}).Describe(command.Context(), mlflow.DescribeRequest{
		RunID: options.runID, MetricNames: options.metrics, ExpectedTags: expectedTags,
		Environment: environment, CWD: info.Repository.Root,
	})
	if err != nil {
		return commandFailure(app, options.json, "provider mlflow verify", run, false, nil, err)
	}
	diagnostics := []Diagnostic{}
	for _, message := range run.Diagnostics {
		diagnostics = append(diagnostics, Diagnostic{Severity: SeverityWarning, Code: "mlflow.verification", Message: message})
	}
	human := fmt.Sprintf("MLflow run %s status=%s verified=%t metrics=%d tags=%d\n", run.RunID, run.Status, run.Verified, len(run.Metrics), len(run.Tags))
	if !run.Verified {
		return commandFailure(app, options.json, "provider mlflow verify", run, false, diagnostics, errors.New("MLflow verification assertions failed"))
	}
	return commandSuccess(app, options.json, "provider mlflow verify", run, false, diagnostics, human)
}
