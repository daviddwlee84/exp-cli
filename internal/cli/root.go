package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const (
	jsonFlagUsage = "emit one exp.cli/v1 machine-readable JSON envelope"
	rootLong      = `exp is a Git-native research control plane, not another tracker or scheduler.

It keeps the research thread reviewable and resumable in ordinary Git and
Markdown. Execution, queueing, telemetry, artifacts, registries,
authentication, and notebook runtimes remain delegated to the upstream tools
that already own them.

This walking-skeleton build supports project initialization, priced Plan
creation and listing, canonical validation, deterministic projections, local
resume context, local-only provider diagnostics, and its embedded guidance
skill. Experiment lifecycle transitions, provider operations, migration, and
artifact transfer are explicitly deferred.`
)

type rootOptions struct {
	startDir string
}

// NewRootCommandWithIO builds the command tree from explicit invocation
// context and process streams.
func NewRootCommandWithIO(ctx context.Context, in io.Reader, out, errOut io.Writer) *cobra.Command {
	return NewRootCommand(NewApp(ctx, in, out, errOut))
}

// NewRootCommand builds the approved functional command tree from App.
func NewRootCommand(app *App) *cobra.Command {
	if app == nil {
		app = NewApp(nil, nil, nil, nil)
	} else {
		app.setDefaults()
	}
	app.jsonEnvelopeAttempted = false
	options := &rootOptions{}
	root := &cobra.Command{
		Use:           "exp",
		Short:         "Git-native research control plane",
		Long:          rootLong,
		Version:       VersionFromBuild(),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetContext(app.Context)
	root.SetIn(app.In)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	root.PersistentFlags().StringVar(&options.startDir, "start-dir", "", "start project discovery from this directory (defaults to the current directory)")
	root.AddCommand(
		newInitCommand(app, options),
		newDoctorCommand(app),
		newPlanCommand(app, options),
		newValidateCommand(app, options),
		newRenderCommand(app, options),
		newContextCommand(app, options),
		newSkillCommand(app),
	)
	wrapCommandErrorBoundaries(root)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return safeCLIError(err) })
	return root
}

func wrapCommandErrorBoundaries(command *cobra.Command) {
	if command.RunE != nil {
		run := command.RunE
		command.RunE = func(command *cobra.Command, args []string) error {
			return safeCLIError(run(command, args))
		}
	}
	if command.Args != nil {
		validateArgs := command.Args
		command.Args = func(command *cobra.Command, args []string) error {
			return safeCLIError(validateArgs(command, args))
		}
	}
	for _, child := range command.Commands() {
		wrapCommandErrorBoundaries(child)
	}
}

// Execute runs one invocation and maps command errors to process exit codes.
// Cobra is silenced so all terminal error output remains centralized here.
func Execute(ctx context.Context, in io.Reader, out, errOut io.Writer, args []string) int {
	app := NewApp(ctx, in, out, errOut)
	root := NewRootCommand(app)
	root.SetArgs(args)
	executionContext := ctx
	if executionContext == nil {
		executionContext = root.Context()
	}
	executed, err := root.ExecuteContextC(executionContext)
	if err == nil {
		return 0
	}

	// Flag parsing and positional-argument validation happen before RunE, where
	// commandFailure normally emits machine output. Supply the same one-envelope
	// contract here, but never emit again after a handler (or failed writer) has
	// already attempted an envelope.
	if jsonRequested(executed, args) && !app.jsonEnvelopeAttempted {
		diagnostic := Diagnostic{
			Severity: SeverityError,
			Code:     "command.invalid_usage",
			Message:  safeDiagnosticText(err.Error()),
		}
		writeErr := app.WriteJSON(app.NewEnvelope(executedCommandName(root, executed), false, false, struct{}{}, []Diagnostic{diagnostic}))
		if writeErr != nil {
			err = errors.Join(err, fmt.Errorf("write JSON failure envelope: %w", writeErr))
		}
	}
	message := safeDiagnosticText(err.Error())
	if message == "" {
		message = "command failed"
	}
	_, _ = fmt.Fprintf(root.ErrOrStderr(), "exp: %s\n", message)
	return 1
}

func executedCommandName(root, executed *cobra.Command) string {
	if executed == nil {
		executed = root
	}
	path := strings.TrimSpace(executed.CommandPath())
	rootName := root.Name()
	if path == rootName {
		return rootName
	}
	if suffix, found := strings.CutPrefix(path, rootName+" "); found {
		return suffix
	}
	return path
}

func jsonRequested(root *cobra.Command, args []string) bool {
	requested := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		if !strings.HasPrefix(argument, "--") || argument == "--" {
			continue
		}
		nameValue := strings.TrimPrefix(argument, "--")
		name, value, hasValue := strings.Cut(nameValue, "=")
		if name == "json" {
			if !hasValue {
				requested = true
				continue
			}
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return true
			}
			requested = parsed
			continue
		}
		if !hasValue && flagConsumesFollowingArgument(root, name) {
			index++
		}
	}
	return requested
}

func flagConsumesFollowingArgument(command *cobra.Command, name string) bool {
	for current := command; current != nil; current = current.Parent() {
		if flag := current.Flags().Lookup(name); flag != nil {
			return flag.NoOptDefVal == ""
		}
		if flag := current.PersistentFlags().Lookup(name); flag != nil {
			return flag.NoOptDefVal == ""
		}
	}
	return false
}
