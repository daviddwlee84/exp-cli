package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/worker"
	"github.com/spf13/cobra"
)

type workerRunOptions struct {
	jobID        string
	fencingToken int64
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
	_ = command.MarkFlagRequired("job")
	_ = command.MarkFlagRequired("fencing-token")
	return command
}

func runWorker(command *cobra.Command, app *App, root *rootOptions, options *workerRunOptions) error {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return err
	}
	info, err := app.DiscoverProject(command.Context(), start)
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
	if options.fencingToken <= 0 || job.FencingToken != options.fencingToken {
		return errors.New("worker fencing token is stale")
	}
	runner := worker.Runner{
		Store: store, Invoker: app.Invoker,
		MarkerRoot: filepath.Join(info.Repository.GitCommonDir, "exp", "v1", "attempts"),
		Clock:      app.clock,
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
