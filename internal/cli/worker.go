package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/daviddwlee84/exp-cli/internal/controlplane"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/worker"
	"github.com/spf13/cobra"
)

type workerRunOptions struct {
	jobID         string
	fencingToken  int64
	canonicalRoot string
	projectID     string
	scope         string
}

func newWorkerCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "worker", Hidden: true, Args: cobra.NoArgs}
	command.AddCommand(newWorkerRunCommand(app, root))
	return command
}

func newWorkerRunCommand(app *App, root *rootOptions) *cobra.Command {
	options := &workerRunOptions{}
	command := &cobra.Command{
		Use: "run", Hidden: true, Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runWorker(command, app, root, options) },
	}
	command.Flags().StringVar(&options.jobID, "job", "", "private operational job id")
	command.Flags().Int64Var(&options.fencingToken, "fencing-token", 0, "private operational fencing token")
	command.Flags().StringVar(&options.canonicalRoot, "canonical-root", "", "explicit canonical workspace root")
	command.Flags().StringVar(&options.projectID, "project", "", "explicit canonical Project UUID")
	command.Flags().StringVar(&options.scope, "scope", "", "explicit checkout-local canonical scope")
	_ = command.MarkFlagRequired("job")
	_ = command.MarkFlagRequired("fencing-token")
	return command
}

func runWorker(command *cobra.Command, app *App, root *rootOptions, options *workerRunOptions) error {
	explicit := options.canonicalRoot != "" || options.projectID != "" || options.scope != ""
	if explicit {
		return runAuthorizedWorker(command, app, options)
	}
	// Exact legacy compatibility path for already-queued exp.worker-job/v1 tasks.
	info, err := resolveProjectInfo(command, app, root)
	if err != nil {
		return err
	}
	store, err := app.OpenOperational(command.Context(), info)
	if err != nil {
		return err
	}
	defer store.Close()
	job, err := store.GetJob(command.Context(), options.jobID)
	if err != nil {
		return err
	}
	return executeWorkerJob(command, app, store, info.Repository.GitCommonDir, job, options.fencingToken, "", "")
}

type authorityOperationalStore interface {
	GetJobAuthority(context.Context, string) (operation.JobAuthority, error)
}

func runAuthorizedWorker(command *cobra.Command, app *App, options *workerRunOptions) error {
	if options.canonicalRoot == "" || options.projectID == "" || options.scope == "" {
		return errors.New("Source-aware worker requires --canonical-root, --project, and --scope together")
	}
	canonicalRoot, err := pathx.Canonical(options.canonicalRoot)
	if err != nil {
		return fmt.Errorf("validate explicit canonical workspace root: %w", err)
	}
	if canonicalRoot != filepath.Clean(options.canonicalRoot) {
		return errors.New("explicit canonical workspace root is not canonical")
	}
	projectID, err := research.ParseUUID(options.projectID)
	if err != nil {
		return errors.New("worker Project UUID is invalid")
	}
	info, err := app.DiscoverProject(command.Context(), canonicalRoot)
	if err != nil {
		return fmt.Errorf("discover explicit canonical workspace: %w", err)
	}
	if info == nil || info.Project() == nil || info.Repository.Root != canonicalRoot || info.Project().ProjectID != projectID {
		return errors.New("explicit canonical root does not own the requested Project")
	}
	expectedScope, err := controlplane.ScopeID(canonicalRoot)
	if err != nil {
		return err
	}
	if options.scope != expectedScope {
		return errors.New("worker canonical scope does not match the explicit checkout")
	}
	store, err := app.OpenOperational(command.Context(), info)
	if err != nil {
		return err
	}
	defer store.Close()
	authorityStore, ok := store.(authorityOperationalStore)
	if !ok {
		return errors.New("operational store cannot perform metadata-only worker authorization")
	}
	authority, err := authorityStore.GetJobAuthority(command.Context(), options.jobID)
	if err != nil {
		return err
	}
	if options.fencingToken <= 0 || authority.FencingToken != options.fencingToken || authority.CanonicalScope != options.scope ||
		authority.Kind != "experiment.run" || authority.Role != "execute" || authority.State == operation.JobQueued {
		return errors.New("worker job claim does not match canonical authority")
	}
	// Payload is selected only after root, Project, scope, and claim authorization.
	job, err := store.GetJob(command.Context(), options.jobID)
	if err != nil {
		return err
	}
	if job.ID != authority.ID || job.SubjectID != authority.SubjectID || job.CanonicalScope != authority.CanonicalScope ||
		job.State != authority.State || job.FencingToken != authority.FencingToken {
		return errors.New("worker job claim changed while reading its payload")
	}
	return executeWorkerJob(command, app, store, info.Repository.GitCommonDir, job, options.fencingToken, options.projectID, options.scope)
}

func executeWorkerJob(command *cobra.Command, app *App, store OperationalStore, canonicalGitCommon string, job operation.Job, fencingToken int64, projectID, scope string) error {
	if job.Kind == tryflow.DirectJobKind {
		return errors.New("direct Try jobs require exp try resume so Source, config trust, and worktree identity are revalidated")
	}
	if fencingToken <= 0 || job.FencingToken != fencingToken {
		return errors.New("worker fencing token is stale")
	}
	runner := worker.Runner{
		Store: store, Invoker: app.Invoker, MLflowInvoker: app.Invoker,
		LookupBinary: app.BinaryLookup, Git: app.GitRunner,
		MarkerRoot: filepath.Join(canonicalGitCommon, "exp", "v1", "attempts"),
		Clock:      app.clock, ProjectID: projectID, CanonicalScope: scope,
	}
	terminal, err := runner.Run(command.Context(), job)
	if err != nil {
		return fmt.Errorf("run operational worker: %w", err)
	}
	encoder := json.NewEncoder(app.Out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(terminal); err != nil {
		return err
	}
	if terminal.State != operation.JobSucceeded {
		return fmt.Errorf("workload completed with state %s", terminal.State)
	}
	return nil
}
