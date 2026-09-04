package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const (
	jsonFlagUsage = "emit one exp.cli/v1 machine-readable JSON envelope"
	rootLong      = `exp is a Git-native research control plane, not another tracker or scheduler.

It keeps Sources, bounded Tries, Ideas, priced Plans, queue order, experimental
evidence, typed Releases, and human Promotions reviewable in ordinary Git and
Markdown. A dedicated private experiment repository may govern independent
Source repositories without storing host paths. Private SQLite coordinates jobs
but never becomes scientific authority; Git, Pueue, MLflow/DVC/object storage,
and Plan-scoped search retain their upstream responsibilities.

Start with "exp guide setup", then choose "exp guide quick" or
"exp guide research". Autonomy defaults to manual. Assisted or limited formal
dispatch requires an explicit policy change, while production Promotion always
requires a named human approver. Agent advice remains advisory; uncertainty
leaves incumbent queue order unchanged for human review.`
)

type rootOptions struct {
	startDir         string
	workspace        string
	source           string
	workspaceBackend string
	mlflowProfile    string
	skill            bool
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
	app.colorMode = colorAuto
	app.colorExplicit = false
	app.jsonIntent = false
	app.machineOutput = false
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
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			if completionProtocolInvocation(command) {
				command.Root().SetErr(completionDiagnosticWriter{destination: app.Err})
			}
			if flag := command.Flags().Lookup("color"); flag != nil {
				app.colorExplicit = flag.Changed
			}
			app.machineOutput = app.jsonIntent || commandJSONRequested(command)
			if app.jsonIntent && !commandSupportsJSON(command) {
				return fmt.Errorf("unknown flag: --json")
			}
			return validateColorMode(app.colorMode)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			if options.skill {
				return runSkillPrint(command, app)
			}
			return command.Help()
		},
	}
	root.SetContext(app.Context)
	root.SetIn(app.In)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	root.Flags().BoolVar(&options.skill, "skill", false, "print this build's embedded SKILL.md")
	root.PersistentFlags().Var(colorFlagValue{target: &app.colorMode}, "color", "colorize human output: auto, always, or never")
	root.PersistentFlags().BoolVar(&app.jsonIntent, "json", false, jsonFlagUsage)
	if err := root.PersistentFlags().MarkHidden("json"); err != nil {
		panic(err)
	}
	root.PersistentFlags().StringVar(&options.startDir, "start-dir", "", "start project discovery from this directory (defaults to the current directory)")
	root.PersistentFlags().StringVar(&options.workspace, "workspace", "", "select a canonical workspace by Project UUID or path")
	root.PersistentFlags().StringVar(&options.source, "source", "", "select a canonical Source by key, ID, prefix, or display code")
	root.PersistentFlags().StringVar(&options.workspaceBackend, "workspace-backend", "", "select native_git or dev_cli for this invocation")
	root.PersistentFlags().StringVar(&options.mlflowProfile, "mlflow-profile", "", "select a trusted named MLflow profile for this invocation")
	root.AddCommand(
		newInitCommand(app, options),
		newDoctorCommand(app),
		newUICommand(app, options),
		newWorkspaceCommand(app, options),
		newSourceCommand(app, options),
		newConfigCommand(app, options),
		newProviderCommand(app, options),
		newAgentCommand(app),
		newPolicyCommand(app, options),
		newIdeaCommand(app, options),
		newTryCommand(app, options),
		newPoolCommand(app, options),
		newQueueCommand(app, options),
		newMigrateCommand(app, options),
		newPlanCommand(app, options),
		newExperimentCommand(app, options),
		newEvaluationCommand(app, options),
		newCandidateCommand(app, options),
		newReleaseCommand(app, options),
		newPromotionCommand(app, options),
		newChampionCommand(app, options),
		newRecordCommand(app, options),
		newValidateCommand(app, options),
		newRenderCommand(app, options),
		newContextCommand(app, options),
		newDaemonCommand(app, options),
		newSkillCommand(app),
		newGuideCommand(app),
		newWorkerCommand(app, options),
	)
	installCompletions(root, app, options)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	installHelpRecovery(root)
	installCompletionRecovery(root)
	installUsageRecovery(root)
	if err := annotateFlowHelp(root); err != nil {
		panic(err)
	}
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(command *cobra.Command, args []string) {
		var buffer bytes.Buffer
		previous := command.OutOrStdout()
		command.SetOut(&buffer)
		defaultHelp(command, args)
		command.SetOut(previous)
		safe := safeStaticHelp(buffer.String())
		_, _ = io.WriteString(previous, renderCobraHelp(safe, app.outStyle()))
	})
	wrapCommandErrorBoundaries(root)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return safeCLIError(err) })
	return root
}

func safeStaticHelp(value string) string {
	value = stripANSI(value)
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	return strings.Map(func(character rune) rune {
		switch character {
		case '\n', '\t':
			return character
		case '\r':
			return '\n'
		}
		if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
			return ' '
		}
		return character
	}, value)
}

func wrapCommandErrorBoundaries(command *cobra.Command) {
	if command.PersistentPreRunE != nil {
		run := command.PersistentPreRunE
		command.PersistentPreRunE = func(command *cobra.Command, args []string) error {
			return safeCLIError(run(command, args))
		}
	}
	if command.PreRunE != nil {
		run := command.PreRunE
		command.PreRunE = func(command *cobra.Command, args []string) error {
			return safeCLIError(run(command, args))
		}
	}
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
	if err == nil || outputWasAbandoned(err) {
		return 0
	}

	// Flag parsing and positional-argument validation happen before RunE, where
	// commandFailure normally emits machine output. Supply the same one-envelope
	// contract here, but never emit again after a handler (or failed writer) has
	// already attempted an envelope.
	machine := app.jsonIntent || jsonRequested(executed, args)
	if machine && !app.jsonEnvelopeAttempted {
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
	style := styleForWriter(root.ErrOrStderr(), app.colorMode)
	if machine {
		style = cliStyle{}
	}
	_, _ = fmt.Fprintf(root.ErrOrStderr(), "%s %s\n", style.danger("exp:"), message)
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

func markRequiredFlags(command *cobra.Command, names ...string) {
	for _, name := range names {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
}

func commandJSONRequested(command *cobra.Command) bool {
	if command == nil || command.Flags().Lookup("json") == nil {
		return false
	}
	requested, err := command.Flags().GetBool("json")
	return err == nil && requested
}

func commandSupportsJSON(command *cobra.Command) bool {
	return command != nil && command.LocalNonPersistentFlags().Lookup("json") != nil
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
