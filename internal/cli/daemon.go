package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/controller"
	"github.com/daviddwlee84/exp-cli/internal/controlplane"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
	"github.com/spf13/cobra"
)

type daemonOptions struct {
	json     bool
	reason   string
	config   string
	interval string
	holder   string
}

type daemonStatusData struct {
	Initialized bool                   `json:"initialized"`
	Database    string                 `json:"database,omitempty"`
	Runtime     operation.RuntimeState `json:"runtime"`
	Jobs        map[string]int         `json:"jobs"`
}

func newDaemonCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "daemon", Short: "Inspect or control the local orchestration daemon", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(
		newDaemonStatusCommand(app, root),
		newDaemonFrontierCommand(app, root),
		newDaemonTickCommand(app, root),
		newDaemonRunCommand(app, root),
		newDaemonPauseCommand(app, root, true),
		newDaemonPauseCommand(app, root, false),
	)
	return command
}

func newDaemonFrontierCommand(app *App, root *rootOptions) *cobra.Command {
	options := &daemonOptions{}
	command := &cobra.Command{Use: "frontier", Short: "Show canonical dispatch frontiers without contacting Pueue", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runDaemonFrontier(command, app, root, options) }
	command.Flags().StringVar(&options.config, "config", controlplane.DefaultConfigPath, "set the project-relative runtime contract path")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newDaemonTickCommand(app *App, root *rootOptions) *cobra.Command {
	options := &daemonOptions{}
	command := &cobra.Command{Use: "tick", Short: "Reconcile Pueue and fill currently available pool capacity once", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error {
		return runDaemonController(command, app, root, options, false)
	}
	addDaemonControllerFlags(command, options, false)
	return command
}

func newDaemonRunCommand(app *App, root *rootOptions) *cobra.Command {
	options := &daemonOptions{interval: "5s"}
	command := &cobra.Command{Use: "run", Short: "Run the local reconcile/admission loop until cancelled", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error {
		return runDaemonController(command, app, root, options, true)
	}
	addDaemonControllerFlags(command, options, true)
	return command
}

func addDaemonControllerFlags(command *cobra.Command, options *daemonOptions, includeInterval bool) {
	command.Flags().StringVar(&options.config, "config", controlplane.DefaultConfigPath, "set the project-relative runtime contract path")
	command.Flags().StringVar(&options.holder, "holder", "", "override the local non-secret lease holder ID")
	if includeInterval {
		command.Flags().StringVar(&options.interval, "interval", "5s", "set the positive reconcile interval")
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
}

func newDaemonStatusCommand(app *App, root *rootOptions) *cobra.Command {
	options := &daemonOptions{}
	command := &cobra.Command{
		Use: "status", Short: "Show local daemon state without contacting providers", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runDaemonStatus(command, app, root, options) },
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newDaemonPauseCommand(app *App, root *rootOptions, paused bool) *cobra.Command {
	options := &daemonOptions{}
	name, short := "resume", "Allow the daemon to dispatch eligible work"
	if paused {
		name, short = "pause", "Pause new dispatch while retaining reconciliation state"
	}
	command := &cobra.Command{
		Use: name, Short: short, Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runDaemonPause(command, app, root, options, paused)
		},
	}
	command.Flags().StringVar(&options.reason, "reason", "", "record a bounded human reason")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runDaemonStatus(command *cobra.Command, app *App, root *rootOptions, options *daemonOptions) error {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return commandFailure(app, options.json, "daemon status", daemonStatusData{Jobs: map[string]int{}}, false, nil, err)
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, "daemon status", daemonStatusData{Jobs: map[string]int{}}, false, nil, err)
	}
	database, err := operation.PathFor(info.Repository.GitCommonDir)
	if err != nil {
		return commandFailure(app, options.json, "daemon status", daemonStatusData{Jobs: map[string]int{}}, false, nil, err)
	}
	data := daemonStatusData{Database: database, Jobs: map[string]int{}}
	if _, err := os.Stat(database); errors.Is(err, fs.ErrNotExist) {
		human := "Daemon operational state is not initialized.\n"
		return commandSuccess(app, options.json, "daemon status", data, false, nil, human)
	} else if err != nil {
		return commandFailure(app, options.json, "daemon status", data, false, nil, err)
	}
	store, err := app.OpenOperational(command.Context(), info)
	if err != nil {
		return commandFailure(app, options.json, "daemon status", data, false, nil, err)
	}
	defer store.Close()
	data.Initialized = true
	data.Runtime, err = store.RuntimeState(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "daemon status", data, false, nil, err)
	}
	jobs, err := store.ListJobs(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "daemon status", data, false, nil, err)
	}
	for _, job := range jobs {
		data.Jobs[string(job.State)]++
	}
	human := renderDaemonStatus(data)
	return commandSuccess(app, options.json, "daemon status", data, false, nil, human)
}

func runDaemonPause(command *cobra.Command, app *App, root *rootOptions, options *daemonOptions, paused bool) error {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return commandFailure(app, options.json, "daemon pause", operation.RuntimeState{}, false, nil, err)
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, "daemon pause", operation.RuntimeState{}, false, nil, err)
	}
	store, err := app.OpenOperational(command.Context(), info)
	if err != nil {
		return commandFailure(app, options.json, "daemon pause", operation.RuntimeState{}, false, nil, err)
	}
	defer store.Close()
	state, err := store.SetPaused(command.Context(), paused, options.reason)
	commandName := "daemon resume"
	if paused {
		commandName = "daemon pause"
	}
	if err != nil {
		return commandFailure(app, options.json, commandName, state, false, nil, err)
	}
	human := "Daemon dispatch resumed.\n"
	if paused {
		human = "Daemon dispatch paused; reconciliation remains available.\n"
	}
	return commandSuccess(app, options.json, commandName, state, false, nil, human)
}

func runDaemonFrontier(command *cobra.Command, app *App, root *rootOptions, options *daemonOptions) error {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return commandFailure(app, options.json, "daemon frontier", struct{}{}, false, nil, err)
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, "daemon frontier", struct{}{}, false, nil, err)
	}
	store, err := app.NewTransactionalStore(info)
	if err != nil {
		return commandFailure(app, options.json, "daemon frontier", struct{}{}, false, nil, err)
	}
	adapter := controlplane.Adapter{Store: store, RepositoryRoot: info.Repository.Root, ConfigPath: options.config, Clock: app.clock, GenerateUUID: app.GenerateUUID}
	items, err := adapter.Frontier(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "daemon frontier", struct{}{}, false, nil, err)
	}
	var human strings.Builder
	for _, item := range items {
		fmt.Fprintf(&human, "%s\t%s\t%s\t%s\tunits=%d\tscore=%.6g\tconfigured=%t\tenabled=%t\n", item.DispatchID, item.Pool, item.Lane, item.Plan, item.Units, item.Score, item.Configured, item.Enabled)
	}
	if len(items) == 0 {
		human.WriteString("No canonical queue frontiers.\n")
	}
	data := struct {
		Frontier []controlplane.FrontierItem `json:"frontier"`
	}{Frontier: items}
	return commandSuccess(app, options.json, "daemon frontier", data, false, nil, human.String())
}

func runDaemonController(command *cobra.Command, app *App, root *rootOptions, options *daemonOptions, continuous bool) error {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, err)
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, err)
	}
	canonicalStore, err := app.NewTransactionalStore(info)
	if err != nil {
		return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, err)
	}
	operationalStore, err := app.OpenOperational(command.Context(), info)
	if err != nil {
		return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, err)
	}
	defer operationalStore.Close()
	executable, err := app.ExecutablePath()
	if err != nil {
		return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, err)
	}
	environment, err := execx.MinimalEnvironment()
	if err != nil {
		return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, err)
	}
	holder := options.holder
	if holder == "" {
		instance, generateErr := app.GenerateUUID(app.clock())
		if generateErr != nil {
			return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, generateErr)
		}
		holder = "daemon-" + strconv.Itoa(os.Getpid()) + "-" + strings.ReplaceAll(instance.String(), "-", "")[:12]
	}
	canonical := controlplane.Adapter{
		Store: canonicalStore, RepositoryRoot: info.Repository.Root, ConfigPath: options.config,
		WorkerExecutable: filepath.Clean(executable), WorkerArgs: []string{"worker", "run", "--job"},
		Clock: app.clock, GenerateUUID: app.GenerateUUID,
	}
	scheduler := controller.PueueScheduler{Adapter: pueue.Adapter{Invoker: app.Invoker, LookupBinary: app.BinaryLookup}, Environment: environment}
	scope, err := controlplane.ScopeID(info.Repository.Root)
	if err != nil {
		return commandFailure(app, options.json, daemonCommandName(continuous), controller.TickResult{}, false, nil, err)
	}
	loop := controller.Controller{
		ProjectID: info.Project().ProjectID.String(), Scope: scope, Holder: holder, Canonical: canonical,
		MarkerRoot:  filepath.Join(info.Repository.GitCommonDir, "exp", "v1", "attempts"),
		Operational: operationalStore, Scheduler: scheduler, Clock: app.clock, LeaseTTL: 2 * time.Minute,
	}
	if !continuous {
		result, tickErr := loop.Tick(command.Context())
		if tickErr != nil {
			return commandFailure(app, options.json, "daemon tick", result, false, nil, tickErr)
		}
		human := fmt.Sprintf("Daemon tick reconciled=%t paused=%t dispatched=%d recovered=%d.\n", result.Reconciled, result.Paused, len(result.Dispatched), len(result.Recovered))
		return commandSuccess(app, options.json, "daemon tick", result, false, nil, human)
	}
	interval, err := time.ParseDuration(options.interval)
	if err != nil || interval <= 0 {
		return commandFailure(app, options.json, "daemon run", controller.TickResult{}, false, nil, errors.New("--interval must be a positive duration"))
	}
	if options.json {
		return commandFailure(app, true, "daemon run", controller.TickResult{}, false, nil, errors.New("daemon run is streaming and does not support --json; use daemon tick --json"))
	}
	loop.PollEvery = interval
	err = loop.Run(command.Context())
	if errors.Is(err, context.Canceled) || errors.Is(err, command.Context().Err()) {
		return nil
	}
	return err
}

func daemonCommandName(continuous bool) string {
	if continuous {
		return "daemon run"
	}
	return "daemon tick"
}

func renderDaemonStatus(data daemonStatusData) string {
	if !data.Initialized {
		return "Daemon operational state is not initialized.\n"
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Daemon state: paused=%t\n", data.Runtime.Paused)
	if data.Runtime.Reason != "" {
		fmt.Fprintf(&output, "Reason: %s\n", safeDiagnosticText(data.Runtime.Reason))
	}
	states := make([]string, 0, len(data.Jobs))
	for state := range data.Jobs {
		states = append(states, state)
	}
	sort.Strings(states)
	for _, state := range states {
		fmt.Fprintf(&output, "%s: %d\n", state, data.Jobs[state])
	}
	return output.String()
}
