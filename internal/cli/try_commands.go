package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/exploration"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
	"github.com/spf13/cobra"
)

const (
	tryExecutionDataSchema  = "exp.try-execution/v1"
	tryStatusDataSchema     = "exp.try-status/v1"
	tryTransitionDataSchema = "exp.try-transition/v1"
	tryCleanupDataSchema    = "exp.try-cleanup/v1"
)

type tryRunOptions struct {
	outputs    bool
	storage    string
	runner     string
	inputs     []string
	allowLarge bool
	json       bool
	title      string
	body       string
	goal       string
	dirty      string
	allow      []string
	timeout    time.Duration
	tags       []string
}

type tryHumanOptions struct {
	author           string
	savedResults     bool
	json             bool
	confirm          bool
	expectedRevision string
	expectedParents  map[string]string
	attempt          string
	abandon          bool
	summary          string
	reason           string
	resultDigests    []string
	externalRefs     []string
	noResults        bool
	title            string
	body             string
	proposedBy       string
	cluster          string
	domain           string
	work             string
	method           string
	component        string
	lane             string
	risk             string
	horizon          string
	origin           string
	parents          []string
	tags             []string
}

type tryOptions struct {
	json   bool
	limit  int
	offset int
}

type trySnapshotView struct {
	Source          string   `json:"source"`
	Subdir          string   `json:"subdir"`
	State           string   `json:"state"`
	BaseCommit      string   `json:"base_commit"`
	HeadCommit      string   `json:"head_commit"`
	ChangeSet       []string `json:"change_set"`
	Digest          string   `json:"digest"`
	DirtyDigest     string   `json:"dirty_digest,omitempty"`
	DirtySummary    string   `json:"dirty_summary,omitempty"`
	Reproducibility string   `json:"reproducibility"`
}

type tryTerminalView struct {
	Source     string `json:"source"`
	ObservedAt string `json:"observed_at"`
	StartedAt  string `json:"started_at,omitempty"`
	EndedAt    string `json:"ended_at"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Signal     string `json:"signal,omitempty"`
}

type tryAttemptView struct {
	Exploration      *exploration.Metadata `json:"exploration,omitempty"`
	ID               string                `json:"id"`
	Path             string                `json:"path"`
	Revision         string                `json:"revision"`
	Title            string                `json:"title"`
	State            string                `json:"state"`
	StateReason      string                `json:"state_reason,omitempty"`
	Runner           string                `json:"runner"`
	Scheduler        string                `json:"scheduler"`
	CWD              string                `json:"cwd"`
	Argv             []string              `json:"argv"`
	ExecutionSource  string                `json:"execution_source"`
	SourceSnapshots  []trySnapshotView     `json:"source_snapshots"`
	Terminal         *tryTerminalView      `json:"terminal,omitempty"`
	ResultDigests    []string              `json:"result_digests"`
	CleanupCompleted bool                  `json:"cleanup_completed"`
}

type tryConclusionView struct {
	ConcludedAt   string                 `json:"concluded_at"`
	Summary       string                 `json:"summary"`
	ResultDigests []string               `json:"result_digests"`
	ExternalRefs  []research.ExternalRef `json:"external_refs"`
}

type tryAbandonmentView struct {
	AbandonedAt string `json:"abandoned_at"`
	Reason      string `json:"reason"`
}

type tryRecordDetailView struct {
	Summary          string              `json:"summary,omitempty"`
	SummaryAuthor    string              `json:"summary_author,omitempty"`
	ConclusionAuthor string              `json:"conclusion_author,omitempty"`
	ID               string              `json:"id"`
	Display          string              `json:"display"`
	Path             string              `json:"path"`
	Revision         string              `json:"revision"`
	Title            string              `json:"title"`
	State            string              `json:"state"`
	Goal             string              `json:"goal"`
	Sources          []string            `json:"sources"`
	Conclusion       *tryConclusionView  `json:"conclusion,omitempty"`
	Abandonment      *tryAbandonmentView `json:"abandonment,omitempty"`
	AdoptedIdea      string              `json:"adopted_idea,omitempty"`
}

type tryRuntimeView struct {
	JobID            string `json:"job_id,omitempty"`
	JobState         string `json:"job_state,omitempty"`
	FencingToken     int64  `json:"fencing_token,omitempty"`
	MarkerState      string `json:"marker_state,omitempty"`
	Stdout           string `json:"stdout,omitempty"`
	Stderr           string `json:"stderr,omitempty"`
	StdoutTruncated  bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated  bool   `json:"stderr_truncated,omitempty"`
	WorkspaceBackend string `json:"workspace_backend,omitempty"`
	WorkspacePresent bool   `json:"workspace_present"`
	WorkspaceClean   *bool  `json:"workspace_clean,omitempty"`
	Recoverable      bool   `json:"recoverable"`
	Active           bool   `json:"active"`
	LeaseExpired     bool   `json:"lease_expired"`
	Uncertain        bool   `json:"uncertain"`
	CleanupCompleted bool   `json:"cleanup_completed"`
	Status           string `json:"status,omitempty"`
	Error            string `json:"error,omitempty"`
}

type tryExecutionData struct {
	SchemaVersion  string               `json:"schema_version"`
	Stage          string               `json:"stage"`
	Recoverable    bool                 `json:"recoverable"`
	Recovered      bool                 `json:"recovered"`
	RetryCreated   bool                 `json:"retry_created"`
	RecoveryAction string               `json:"recovery_action,omitempty"`
	Transactions   []string             `json:"transaction_ids"`
	Try            *tryRecordDetailView `json:"try,omitempty"`
	Attempt        *tryAttemptView      `json:"attempt,omitempty"`
	Runtime        tryRuntimeView       `json:"runtime"`
}

type tryStatusEntryView struct {
	Try          tryRecordDetailView `json:"try"`
	AttemptCount int                 `json:"attempt_count"`
	Attempts     []struct {
		Attempt tryAttemptView `json:"attempt"`
		Runtime tryRuntimeView `json:"runtime"`
	} `json:"attempts"`
}

type tryStatusData struct {
	SchemaVersion string               `json:"schema_version"`
	Project       projectView          `json:"project"`
	Tries         []tryStatusEntryView `json:"tries"`
	Total         int                  `json:"total"`
	Limit         int                  `json:"limit"`
	Offset        int                  `json:"offset"`
}

type tryTransitionData struct {
	SchemaVersion string                    `json:"schema_version"`
	TransactionID string                    `json:"transaction_id,omitempty"`
	Transaction   *record.TransactionResult `json:"transaction,omitempty"`
	Try           tryRecordDetailView       `json:"try"`
	Idea          *canonicalRecordView      `json:"idea,omitempty"`
}

type tryCleanupItemView struct {
	Attempt          tryAttemptView `json:"attempt"`
	RemovedWorkspace bool           `json:"removed_workspace"`
	RemovedBundle    bool           `json:"removed_bundle"`
	AlreadyCompleted bool           `json:"already_completed"`
	Refused          bool           `json:"refused"`
	Reason           string         `json:"reason,omitempty"`
}

type tryCleanupData struct {
	SchemaVersion string               `json:"schema_version"`
	Try           tryRecordDetailView  `json:"try"`
	Transactions  []string             `json:"transaction_ids"`
	Items         []tryCleanupItemView `json:"items"`
}

func newTryCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "try", Short: "Run bounded exploratory work in managed native Git worktrees", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(
		newTryRunCommand(app, root),
		newTryStartCommand(app, root),
		newTryExecCommand(app, root),
		newTryRootCommand(app),
		newTrySummarizeCommand(app, root),
		newTryRetryCommand(app, root),
		newTryResumeCommand(app, root),
		newTryReconcileCommand(app, root),
		newTryFinishCommand(app, root),
		newTryAbandonCommand(app, root),
		newTryAdoptCommand(app, root),
		newTryListCommand(app, root),
		newTryShowCommand(app, root),
		newTryStatusCommand(app, root),
		newTryCleanupCommand(app, root),
	)
	return command
}

func newTryRunCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryRunOptions{}
	command := &cobra.Command{
		Use:   "run [--title TITLE --goal GOAL] [--dirty=capture] [--allow GLOB] [-- COMMAND [ARG...]]",
		Short: "Review and run bounded work in a managed Source worktree",
		Args: func(command *cobra.Command, args []string) error {
			if command.ArgsLenAtDash() < 0 && len(args) > 0 {
				return invalidUsagef("direct Try command arguments must follow an explicit -- delimiter")
			}
			if (command.ArgsLenAtDash() < 0 || len(args) == 0) && !app.interactive(options.json) {
				return invalidUsagef("COMMAND is required after -- in non-interactive or JSON mode")
			}
			return nil
		},
	}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runTryRun(command, app, root, options, args)
	}
	flags := command.Flags()
	addExplorationFlags(command, options)
	flags.StringVar(&options.title, "title", "", "set the canonical Try title")
	flags.StringVar(&options.goal, "goal", "", "state the bounded evidence-gathering goal")
	flags.StringVar(&options.body, "body", "", "set optional canonical Markdown detail")
	flags.StringVar(&options.dirty, "dirty", "", "set capture to reproduce bounded dirty Source state")
	flags.StringArrayVar(&options.allow, "allow", nil, "allow a Source-relative changed path glob (repeatable; empty is read-only)")
	flags.DurationVar(&options.timeout, "timeout", 0, "bound direct command runtime")
	flags.StringSliceVar(&options.tags, "tags", nil, "set canonical Try tags")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryRetryCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryOptions{}
	command := &cobra.Command{Use: "retry TRY", Short: "Retry a terminal Try Attempt with identical Source and argv identity", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runTryRetry(command, app, root, options, args[0])
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryResumeCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryOptions{}
	command := &cobra.Command{Use: "resume TRY", Short: "Resume a provably unstarted Attempt or import its durable terminal marker", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runTryResume(command, app, root, options, args[0])
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryReconcileCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryHumanOptions{}
	command := &cobra.Command{Use: "reconcile TRY --attempt ATTEMPT --abandon-uncertain --reason TEXT --confirm", Short: "Import late evidence or explicitly abandon one uncertain Attempt", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runTryReconcile(command, app, root, options, args[0])
	}
	flags := command.Flags()
	flags.StringVar(&options.attempt, "attempt", "", "select the exact uncertain Attempt")
	flags.BoolVar(&options.abandon, "abandon-uncertain", false, "record a durable human abandonment when no marker or live lease exists")
	flags.StringVar(&options.reason, "reason", "", "record the human reconciliation reason")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm the explicit uncertain-execution disposition")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryFinishCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryHumanOptions{}
	command := &cobra.Command{Use: "finish [TRY] [--summary TEXT] [--confirm]", Short: "Review and record a human Try conclusion", Args: cobra.MaximumNArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		reference := ""
		if len(args) == 1 {
			reference = args[0]
		}
		return runTryFinish(command, app, root, options, reference)
	}
	flags := command.Flags()
	flags.StringVar(&options.summary, "summary", "", "record the conclusion summary")
	flags.StringVar(&options.author, "author", "human", "human or agent; agent conclusions are explicitly unreviewed")
	flags.BoolVar(&options.savedResults, "saved-results", false, "select saved artifact manifests by ownership without manual JSON")
	flags.StringArrayVar(&options.resultDigests, "result-digest", nil, "select one sha256 result digest (repeatable)")
	flags.StringArrayVar(&options.externalRefs, "external-ref", nil, "select one ExternalRef as a strict JSON object (repeatable)")
	flags.BoolVar(&options.noResults, "no-results", false, "explicitly conclude without selecting a result")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm the exact human conclusion without prompting")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryAbandonCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryHumanOptions{}
	command := &cobra.Command{Use: "abandon TRY --reason TEXT --confirm", Short: "Record a human reason for abandoning an open Try", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runTryAbandon(command, app, root, options, args[0])
	}
	flags := command.Flags()
	flags.StringVar(&options.reason, "reason", "", "record the human abandonment reason")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm abandonment without prompting")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryAdoptCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryHumanOptions{}
	command := &cobra.Command{Use: "adopt [TRY] [--title TITLE --summary TEXT --proposed-by HUMAN] [classification flags] [--confirm]", Short: "Review and atomically adopt a concluded Try as Idea v2", Args: cobra.MaximumNArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		reference := ""
		if len(args) == 1 {
			reference = args[0]
		}
		return runTryAdopt(command, app, root, options, reference)
	}
	flags := command.Flags()
	flags.StringVar(&options.title, "title", "", "set the adopted Idea title")
	flags.StringVar(&options.summary, "summary", "", "state the adopted formal direction")
	flags.StringVar(&options.body, "body", "", "set optional adopted Idea Markdown detail")
	flags.StringVar(&options.proposedBy, "proposed-by", "", "identify the confirming human")
	flags.StringVar(&options.cluster, "cluster", "", "set the primary cluster slug")
	flags.StringVar(&options.domain, "domain", "", "set the controlled domain")
	flags.StringVar(&options.work, "work", "", "set the controlled work class")
	flags.StringVar(&options.method, "method", "", "set the controlled method")
	flags.StringVar(&options.component, "component", "", "set the controlled component")
	flags.StringVar(&options.lane, "lane", "", "set exploit or explore")
	flags.StringVar(&options.risk, "risk", "", "set low, medium, or high")
	flags.StringVar(&options.horizon, "horizon", "", "set short, medium, or long")
	flags.StringVar(&options.origin, "origin", "", "set human or hybrid")
	flags.StringSliceVar(&options.parents, "parent", nil, "link a parent Idea (repeatable)")
	flags.StringSliceVar(&options.tags, "tags", nil, "set adopted Idea tags")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm adoption without prompting")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryListCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryOptions{limit: 100}
	command := &cobra.Command{Use: "list", Short: "List canonical Tries and Attempt counts", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error {
		return runTryStatus(command, app, root, options, "", "try list", true)
	}
	command.Flags().IntVar(&options.limit, "limit", 100, "limit unfiltered Try rows to 1..1000")
	command.Flags().IntVar(&options.offset, "offset", 0, "skip this many unfiltered Try rows")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryShowCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryOptions{}
	command := &cobra.Command{Use: "show TRY", Short: "Show one Try with canonical and local direct status", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runTryStatus(command, app, root, options, args[0], "try show", false)
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryStatusCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryOptions{limit: 100}
	command := &cobra.Command{Use: "status [TRY]", Short: "Inspect canonical Attempts, jobs, markers, and managed worktrees without executing", Args: cobra.MaximumNArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		reference := ""
		if len(args) == 1 {
			reference = args[0]
		}
		return runTryStatus(command, app, root, options, reference, "try status", false)
	}
	command.Flags().IntVar(&options.limit, "limit", 100, "limit unfiltered Try rows to 1..1000")
	command.Flags().IntVar(&options.offset, "offset", 0, "skip this many unfiltered Try rows")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newTryCleanupCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryHumanOptions{}
	command := &cobra.Command{Use: "cleanup TRY --confirm", Short: "Remove only verified manager-owned worktrees and dirty seed bundles", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runTryCleanup(command, app, root, options, args[0])
	}
	command.Flags().BoolVar(&options.confirm, "confirm", false, "confirm safe local resource cleanup without prompting")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runTryRun(command *cobra.Command, app *App, root *rootOptions, options *tryRunOptions, argv []string) error {
	if strings.TrimSpace(options.title) == "" || strings.TrimSpace(options.goal) == "" || len(argv) == 0 {
		if !app.interactive(options.json) {
			return commandFailure(app, options.json, "try run", emptyTryExecutionData(), false, nil, invalidUsagef("Try run requires --title, --goal, and COMMAND after -- in non-interactive or JSON mode"))
		}
		prepared, err := prepareGuidedTryRun(command, app, root, options, argv)
		if err != nil {
			return commandFailure(app, options.json, "try run", emptyTryExecutionData(), false, nil, err)
		}
		argv = prepared
	}
	dirty := false
	switch strings.TrimSpace(options.dirty) {
	case "":
	case "capture":
		dirty = true
	default:
		return commandFailure(app, options.json, "try run", emptyTryExecutionData(), false, nil, errors.New("--dirty accepts only capture; dirty state is never implicit"))
	}
	selection, err := trySelection(app, root)
	if err != nil {
		return commandFailure(app, options.json, "try run", emptyTryExecutionData(), false, nil, err)
	}
	managed := options.outputs || options.storage != "" || options.runner != "" || len(options.inputs) > 0
	if options.runner != "" {
		info, _, resolveErr := openTransactionalStore(command, app, root)
		if resolveErr != nil {
			return commandFailure(app, options.json, "try run", emptyTryExecutionData(), false, nil, resolveErr)
		}
		argv, options.runner, err = runnerArgv(command.Context(), info.Project().ProjectID.String(), options.runner, argv)
		if err != nil {
			return commandFailure(app, options.json, "try run", emptyTryExecutionData(), false, nil, err)
		}
	}
	coordinator := app.NewTryCoordinator()
	result, runErr := coordinator.Run(command.Context(), tryflow.RunRequest{
		Selection: selection, Title: options.title, Body: options.body, Goal: options.goal,
		ManagedOutputs: managed, StorageProfile: options.storage, RunnerProfile: options.runner, InputNames: options.inputs, AllowLarge: options.allowLarge,
		Argv: append([]string{}, argv...), DirtyCapture: dirty,
		AllowedGlobs: append([]string{}, options.allow...), Timeout: options.timeout,
		Tags: append([]string{}, options.tags...),
	})
	return renderTryExecution(app, options.json, "try run", result, runErr)
}

func runTryRetry(command *cobra.Command, app *App, root *rootOptions, options *tryOptions, reference string) error {
	selection, err := trySelection(app, root)
	if err != nil {
		return commandFailure(app, options.json, "try retry", emptyTryExecutionData(), false, nil, err)
	}
	result, retryErr := app.NewTryCoordinator().Retry(command.Context(), tryflow.RetryRequest{Selection: selection, Try: reference})
	return renderTryExecution(app, options.json, "try retry", result, retryErr)
}

func runTryResume(command *cobra.Command, app *App, root *rootOptions, options *tryOptions, reference string) error {
	selection, err := trySelection(app, root)
	if err != nil {
		return commandFailure(app, options.json, "try resume", emptyTryExecutionData(), false, nil, err)
	}
	result, resumeErr := app.NewTryCoordinator().Resume(command.Context(), tryflow.ResumeRequest{Selection: selection, Try: reference})
	return renderTryExecution(app, options.json, "try resume", result, resumeErr)
}

func runTryReconcile(command *cobra.Command, app *App, root *rootOptions, options *tryHumanOptions, reference string) error {
	if !options.confirm || !options.abandon || strings.TrimSpace(options.attempt) == "" || strings.TrimSpace(options.reason) == "" {
		return commandFailure(app, options.json, "try reconcile", emptyTryExecutionData(), false, nil, errors.New("Try reconcile requires --attempt, --abandon-uncertain, --reason, and --confirm"))
	}
	selection, err := trySelection(app, root)
	if err != nil {
		return commandFailure(app, options.json, "try reconcile", emptyTryExecutionData(), false, nil, err)
	}
	result, reconcileErr := app.NewTryCoordinator().Reconcile(command.Context(), tryflow.ReconcileRequest{
		Selection: selection, Try: reference, Attempt: options.attempt,
		Reason: options.reason, AbandonUncertain: options.abandon,
	})
	return renderTryExecution(app, options.json, "try reconcile", result, reconcileErr)
}

func renderTryExecution(app *App, machine bool, commandName string, result *tryflow.ExecutionResult, err error) error {
	partial, recoverable := false, false
	var runtimeErr *tryflow.RuntimeError
	if errors.As(err, &runtimeErr) {
		partial, recoverable = runtimeErr.Partial, runtimeErr.Recoverable
		if result == nil {
			result = runtimeErr.Result
		}
	}
	data := makeTryExecutionData(result, recoverable)
	if err != nil {
		return commandFailure(app, machine, commandName, data, partial, nil, err)
	}
	human := "Direct Try completed.\n"
	if data.Try != nil && data.Attempt != nil {
		human = fmt.Sprintf("Try %s Attempt %s is %s.\n", data.Try.ID, data.Attempt.ID, data.Attempt.State)
	}
	if data.Runtime.Stdout != "" {
		human += data.Runtime.Stdout
		if !strings.HasSuffix(human, "\n") {
			human += "\n"
		}
	}
	if data.Runtime.Stderr != "" {
		human += data.Runtime.Stderr
		if !strings.HasSuffix(human, "\n") {
			human += "\n"
		}
	}
	return commandSuccess(app, machine, commandName, data, false, nil, human)
}

func runTryFinish(command *cobra.Command, app *App, root *rootOptions, options *tryHumanOptions, reference string) error {
	if options.author != "" && options.author != "human" && options.author != "agent" {
		return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, errors.New("author must be human or agent"))
	}
	if options.author == "agent" {
		options.confirm = true
		if !options.noResults && len(options.resultDigests) == 0 && len(options.externalRefs) == 0 {
			options.savedResults = true
		}
	}
	if options.savedResults && (options.noResults || len(options.resultDigests) > 0 || len(options.externalRefs) > 0) {
		return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, errors.New("--saved-results cannot be combined with another result selection"))
	}
	selectionComplete := options.savedResults || options.noResults != (len(options.resultDigests) > 0 || len(options.externalRefs) > 0)
	if strings.TrimSpace(reference) == "" || !options.confirm || strings.TrimSpace(options.summary) == "" || !selectionComplete {
		if !app.interactive(options.json) {
			return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, invalidUsagef("Try finish requires TRY, --summary, an explicit result selection, and --confirm in non-interactive or JSON mode"))
		}
		prepared, err := prepareGuidedTryFinish(command, app, root, options, reference)
		if err != nil {
			return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, err)
		}
		reference = prepared
	}
	if !options.savedResults && options.noResults == (len(options.resultDigests) > 0 || len(options.externalRefs) > 0) {
		return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, errors.New("select --result-digest/--external-ref, or explicitly use --no-results"))
	}
	for _, digest := range options.resultDigests {
		if !validTryDigest(digest) {
			return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, fmt.Errorf("invalid result digest %q", digest))
		}
	}
	externalRefs, err := parseTryExternalRefs(options.externalRefs)
	if err != nil {
		return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, err)
	}
	info, store, inventory, tryReference, err := openTryForTransition(command, app, root, reference)
	if err != nil {
		return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, err)
	}
	if options.expectedRevision != "" && tryReference.Revision != options.expectedRevision {
		return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, &guidedPlanStaleError{subject: "Try revision"})
	}
	if options.savedResults {
		for _, document := range inventory.OfKind(research.KindAttempt) {
			attempt := document.Record.(*research.Attempt)
			if attempt.Try != tryReference.ID {
				continue
			}
			metadata, metaErr := exploration.MetadataFor(attempt)
			if metaErr != nil {
				return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, metaErr)
			}
			if metadata == nil {
				continue
			}
			if metadata.ArchiveState != "saved" {
				return commandFailure(app, options.json, "try finish", emptyTryTransitionData(), false, nil, errors.New("save pending artifacts with exp results save before finishing"))
			}
			if len(metadata.Artifacts) > 0 {
				externalRefs = append(externalRefs, research.ExternalRef{Role: research.ExternalArtifact, Provider: "exp-storage", Context: metadata.Storage, NativeKind: "artifact-manifest", NativeID: attempt.ID.String()})
			}
		}
	}
	result, err := tryflow.New(store, tryflow.WithClock(app.clock), tryflow.WithUUIDGenerator(app.GenerateUUID)).Conclude(command.Context(), tryflow.ConcludeRequest{
		Try: tryReference, Summary: options.summary, Author: options.author, ResultDigests: append([]string{}, options.resultDigests...), ExternalRefs: externalRefs,
	})
	if err != nil {
		data := tryTransitionFailureData(result, nil, inventory, err)
		return commandFailure(app, options.json, "try finish", data, data.Transaction != nil, transactionFailureDiagnostics(data.Transaction), err)
	}
	data := tryTransitionData{SchemaVersion: tryTransitionDataSchema, TransactionID: result.TransactionID, Try: makeTryRecordDetail(result.Try, inventory)}
	diagnostics := []Diagnostic(nil)
	if result.TransactionID != "" {
		diagnostics = refreshAfterTransaction(command, app, info, store)
	}
	return commandSuccess(app, options.json, "try finish", data, false, diagnostics, fmt.Sprintf("Concluded Try %s.\n", data.Try.ID))
}

func runTryAbandon(command *cobra.Command, app *App, root *rootOptions, options *tryHumanOptions, reference string) error {
	if !options.confirm || strings.TrimSpace(options.reason) == "" {
		return commandFailure(app, options.json, "try abandon", emptyTryTransitionData(), false, nil, errors.New("Try abandon requires --reason and --confirm; no prompt is performed"))
	}
	info, store, inventory, tryReference, err := openTryForTransition(command, app, root, reference)
	if err != nil {
		return commandFailure(app, options.json, "try abandon", emptyTryTransitionData(), false, nil, err)
	}
	result, err := tryflow.New(store, tryflow.WithClock(app.clock), tryflow.WithUUIDGenerator(app.GenerateUUID)).Abandon(command.Context(), tryflow.AbandonRequest{Try: tryReference, Reason: options.reason})
	if err != nil {
		data := tryTransitionFailureData(result, nil, inventory, err)
		return commandFailure(app, options.json, "try abandon", data, data.Transaction != nil, transactionFailureDiagnostics(data.Transaction), err)
	}
	data := tryTransitionData{SchemaVersion: tryTransitionDataSchema, TransactionID: result.TransactionID, Try: makeTryRecordDetail(result.Try, inventory)}
	diagnostics := []Diagnostic(nil)
	if result.TransactionID != "" {
		diagnostics = refreshAfterTransaction(command, app, info, store)
	}
	return commandSuccess(app, options.json, "try abandon", data, false, diagnostics, fmt.Sprintf("Abandoned Try %s.\n", data.Try.ID))
}

func runTryAdopt(command *cobra.Command, app *App, root *rootOptions, options *tryHumanOptions, reference string) error {
	if strings.TrimSpace(reference) == "" || !options.confirm || !explicitAdoptFields(options) {
		if !app.interactive(options.json) {
			return commandFailure(app, options.json, "try adopt", emptyTryTransitionData(), false, nil, invalidUsagef("Try adopt requires TRY, explicit Idea/classification/human fields, and --confirm in non-interactive or JSON mode"))
		}
		prepared, err := prepareGuidedTryAdopt(command, app, root, options, reference)
		if err != nil {
			return commandFailure(app, options.json, "try adopt", emptyTryTransitionData(), false, nil, err)
		}
		reference = prepared
	}
	info, store, inventory, tryReference, err := openTryForTransition(command, app, root, reference)
	if err != nil {
		return commandFailure(app, options.json, "try adopt", emptyTryTransitionData(), false, nil, err)
	}
	if options.expectedRevision != "" && tryReference.Revision != options.expectedRevision {
		return commandFailure(app, options.json, "try adopt", emptyTryTransitionData(), false, nil, &guidedPlanStaleError{subject: "Try revision"})
	}
	parents := make([]tryflow.RevisionRef, 0, len(options.parents))
	for _, parent := range options.parents {
		value, resolveErr := currentRevisionRef(inventory, parent, research.KindIdea)
		if resolveErr != nil {
			return commandFailure(app, options.json, "try adopt", emptyTryTransitionData(), false, nil, resolveErr)
		}
		if expected, guarded := options.expectedParents[value.ID.String()]; guarded && value.Revision != expected {
			return commandFailure(app, options.json, "try adopt", emptyTryTransitionData(), false, nil, &guidedPlanStaleError{subject: "parent Idea revision"})
		}
		parents = append(parents, tryflow.RevisionRef{ID: value.ID, Revision: value.Revision})
	}
	result, err := tryflow.New(store, tryflow.WithClock(app.clock), tryflow.WithUUIDGenerator(app.GenerateUUID)).Adopt(command.Context(), tryflow.AdoptRequest{
		Try: tryReference, Title: options.title, Body: options.body, Summary: options.summary,
		ProposedBy: options.proposedBy, PrimaryCluster: options.cluster,
		Classification: research.Classification{
			Domain: options.domain, Work: options.work, Method: options.method, Component: options.component,
			Lane: research.ResearchLane(options.lane), Risk: research.RiskClass(options.risk),
			Horizon: research.HorizonClass(options.horizon), Origin: research.OriginClass(options.origin),
		},
		Parents: parents, Tags: append([]string{}, options.tags...),
	})
	if err != nil {
		data := tryTransitionFailureData(nil, result, inventory, err)
		return commandFailure(app, options.json, "try adopt", data, data.Transaction != nil, transactionFailureDiagnostics(data.Transaction), err)
	}
	data := tryTransitionData{SchemaVersion: tryTransitionDataSchema, TransactionID: result.TransactionID, Try: makeTryRecordDetail(result.Try, inventory)}
	idea := canonicalView(result.Idea)
	data.Idea = &idea
	diagnostics := []Diagnostic(nil)
	if result.TransactionID != "" {
		diagnostics = refreshAfterTransaction(command, app, info, store)
	}
	return commandSuccess(app, options.json, "try adopt", data, false, diagnostics, fmt.Sprintf("Adopted Try %s as Idea %s.\n", data.Try.ID, idea.ID))
}

func runTryStatus(command *cobra.Command, app *App, root *rootOptions, options *tryOptions, reference, commandName string, listOnly bool) error {
	selection, err := trySelection(app, root)
	if err != nil {
		return commandFailure(app, options.json, commandName, tryStatusData{SchemaVersion: tryStatusDataSchema, Tries: []tryStatusEntryView{}}, false, nil, err)
	}
	status, err := app.NewTryCoordinator().Status(command.Context(), tryflow.StatusRequest{Selection: selection, Try: reference, Limit: options.limit, Offset: options.offset})
	if err != nil {
		return commandFailure(app, options.json, commandName, tryStatusData{SchemaVersion: tryStatusDataSchema, Tries: []tryStatusEntryView{}}, false, nil, err)
	}
	data, err := makeTryStatusData(status, listOnly)
	if err != nil {
		return commandFailure(app, options.json, commandName, tryStatusData{SchemaVersion: tryStatusDataSchema, Tries: []tryStatusEntryView{}}, status.Partial, nil, err)
	}
	var human strings.Builder
	for _, entry := range data.Tries {
		fmt.Fprintf(&human, "%s\t%s\t%d attempt(s)\t%s\n", entry.Try.ID, entry.Try.State, entry.AttemptCount, entry.Try.Title)
		if !listOnly {
			for _, attempt := range entry.Attempts {
				fmt.Fprintf(&human, "  %s\t%s", attempt.Attempt.ID, attempt.Attempt.State)
				if attempt.Runtime.JobState != "" {
					fmt.Fprintf(&human, "\tjob=%s", attempt.Runtime.JobState)
				}
				if attempt.Runtime.Uncertain {
					human.WriteString("\tuncertain")
				}
				human.WriteByte('\n')
			}
		}
	}
	if len(data.Tries) == 0 {
		human.WriteString("No Tries.\n")
	}
	return commandSuccess(app, options.json, commandName, data, status.Partial, statusDiagnostics(status), human.String())
}

func runTryCleanup(command *cobra.Command, app *App, root *rootOptions, options *tryHumanOptions, reference string) error {
	if !options.confirm {
		return commandFailure(app, options.json, "try cleanup", emptyTryCleanupData(), false, nil, errors.New("Try cleanup requires --confirm; no prompt is performed"))
	}
	selection, err := trySelection(app, root)
	if err != nil {
		return commandFailure(app, options.json, "try cleanup", emptyTryCleanupData(), false, nil, err)
	}
	result, cleanupErr := app.NewTryCoordinator().Cleanup(command.Context(), tryflow.CleanupRequest{Selection: selection, Try: reference})
	data := makeTryCleanupData(result)
	if cleanupErr != nil {
		partial := result != nil && result.Partial
		return commandFailure(app, options.json, "try cleanup", data, partial, nil, cleanupErr)
	}
	return commandSuccess(app, options.json, "try cleanup", data, false, nil, fmt.Sprintf("Cleaned verified local resources for Try %s.\n", data.Try.ID))
}

func trySelection(app *App, root *rootOptions) (tryflow.Selection, error) {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return tryflow.Selection{}, err
	}
	return tryflow.Selection{
		InvocationDir: start, Workspace: strings.TrimSpace(root.workspace), Source: strings.TrimSpace(root.source),
		WorkspaceBackend: strings.TrimSpace(root.workspaceBackend), MLflowProfile: strings.TrimSpace(root.mlflowProfile),
	}, nil
}

func openTryForTransition(command *cobra.Command, app *App, root *rootOptions, reference string) (*project.Info, TransactionalRecordStore, *record.Inventory, tryflow.RevisionRef, error) {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return nil, nil, nil, tryflow.RevisionRef{}, err
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return nil, nil, nil, tryflow.RevisionRef{}, err
	}
	current, err := currentRevisionRef(inventory, reference, research.KindTry)
	if err != nil {
		return nil, nil, nil, tryflow.RevisionRef{}, err
	}
	return info, store, inventory, tryflow.RevisionRef{ID: current.ID, Revision: current.Revision}, nil
}

func explicitAdoptFields(options *tryHumanOptions) bool {
	if options == nil {
		return false
	}
	values := []string{options.title, options.summary, options.proposedBy, options.cluster, options.domain, options.work, options.method, options.component, options.lane, options.risk, options.horizon, options.origin}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func parseTryExternalRefs(values []string) ([]research.ExternalRef, error) {
	result := make([]research.ExternalRef, 0, len(values))
	for index, value := range values {
		decoder := json.NewDecoder(bytes.NewBufferString(value))
		decoder.DisallowUnknownFields()
		var reference research.ExternalRef
		if err := decoder.Decode(&reference); err != nil {
			return nil, fmt.Errorf("decode --external-ref %d: %w", index+1, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("--external-ref %d contains trailing JSON", index+1)
		}
		result = append(result, reference)
	}
	return result, nil
}

func validTryDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func makeTryExecutionData(result *tryflow.ExecutionResult, recoverable bool) tryExecutionData {
	data := emptyTryExecutionData()
	data.Recoverable = recoverable
	if result == nil {
		return data
	}
	data.Stage = string(result.Stage)
	data.Recovered = result.Recovered
	data.RetryCreated = result.RetryCreated
	data.RecoveryAction = result.RecoveryAction
	data.Transactions = append([]string{}, result.Transactions...)
	if result.Try != nil {
		view := makeTryRecordDetail(result.Try, nil)
		data.Try = &view
	}
	if result.Attempt != nil {
		view := makeTryAttemptView(result.Attempt)
		data.Attempt = &view
		data.Runtime.WorkspaceBackend = canonicalAttemptWorkspaceBackend(result.Attempt)
	}
	if result.Job != nil {
		data.Runtime.JobID = result.Job.ID
		data.Runtime.JobState = string(result.Job.State)
		data.Runtime.FencingToken = result.Job.FencingToken
	}
	if result.Terminal != nil {
		data.Runtime.MarkerState = string(result.Terminal.State)
		data.Runtime.Stdout = safeDiagnosticText(result.Terminal.Stdout)
		data.Runtime.Stderr = safeDiagnosticText(result.Terminal.Stderr)
		data.Runtime.StdoutTruncated = result.Terminal.StdoutTruncated
		data.Runtime.StderrTruncated = result.Terminal.StderrTruncated
	}
	if result.Workspace != nil {
		data.Runtime.WorkspaceBackend = result.Workspace.Backend
		data.Runtime.WorkspacePresent = true
	}
	data.Runtime.Recoverable = recoverable
	return data
}

func emptyTryExecutionData() tryExecutionData {
	return tryExecutionData{SchemaVersion: tryExecutionDataSchema, Transactions: []string{}, Runtime: tryRuntimeView{}}
}

func emptyTryTransitionData() tryTransitionData {
	return tryTransitionData{SchemaVersion: tryTransitionDataSchema, Try: tryRecordDetailView{Sources: []string{}}}
}

func tryTransitionFailureData(transition *tryflow.TransitionResult, adoption *tryflow.AdoptResult, inventory *record.Inventory, err error) tryTransitionData {
	data := emptyTryTransitionData()
	var tryDocument *record.Document
	if transition != nil {
		data.TransactionID = transition.TransactionID
		tryDocument = transition.Try
	}
	if adoption != nil {
		data.TransactionID = adoption.TransactionID
		tryDocument = adoption.Try
		if adoption.Idea != nil {
			view := canonicalView(adoption.Idea)
			data.Idea = &view
		}
	}
	if transaction, found := tryflow.TransactionResultFromError(err); found {
		data.Transaction = transaction
		data.TransactionID = transaction.TransactionID
	}
	if tryDocument != nil {
		data.Try = makeTryRecordDetail(tryDocument, inventory)
	}
	return data
}

func emptyTryCleanupData() tryCleanupData {
	return tryCleanupData{
		SchemaVersion: tryCleanupDataSchema, Try: tryRecordDetailView{Sources: []string{}},
		Transactions: []string{}, Items: []tryCleanupItemView{},
	}
}

func makeTryStatusData(status *tryflow.StatusResult, listOnly bool) (tryStatusData, error) {
	data := tryStatusData{SchemaVersion: tryStatusDataSchema, Tries: []tryStatusEntryView{}}
	if status == nil {
		return data, errors.New("Try status result is nil")
	}
	project, err := makeProjectView(status.Project)
	if err != nil {
		return data, err
	}
	data.Project = project
	data.Total, data.Limit, data.Offset = status.Total, status.Limit, status.Offset
	for _, entry := range status.Tries {
		view := tryStatusEntryView{Try: makeTryRecordDetail(entry.Try, nil), AttemptCount: len(entry.Attempts), Attempts: []struct {
			Attempt tryAttemptView `json:"attempt"`
			Runtime tryRuntimeView `json:"runtime"`
		}{}}
		if !listOnly {
			for _, attempt := range entry.Attempts {
				runtime := tryRuntimeView{
					WorkspaceBackend: canonicalAttemptWorkspaceBackend(attempt.Attempt),
					WorkspacePresent: attempt.WorkspacePresent, Recoverable: attempt.Recoverable,
					Active: attempt.Active, LeaseExpired: attempt.LeaseExpired,
					Uncertain: attempt.Uncertain, CleanupCompleted: attempt.CleanupCompleted,
					Status: attempt.Status, Error: attempt.Error,
				}
				if attempt.Job != nil {
					runtime.JobID = attempt.Job.ID
					runtime.JobState = string(attempt.Job.State)
					runtime.FencingToken = attempt.Job.FencingToken
				}
				if attempt.Marker != nil {
					runtime.MarkerState = string(attempt.Marker.State)
					runtime.Stdout = safeDiagnosticText(attempt.Marker.Stdout)
					runtime.Stderr = safeDiagnosticText(attempt.Marker.Stderr)
					runtime.StdoutTruncated = attempt.Marker.StdoutTruncated
					runtime.StderrTruncated = attempt.Marker.StderrTruncated
				}
				if attempt.Workspace != nil {
					runtime.WorkspaceBackend = attempt.Workspace.Workspace.Backend
					clean := attempt.Workspace.Clean
					runtime.WorkspaceClean = &clean
				}
				view.Attempts = append(view.Attempts, struct {
					Attempt tryAttemptView `json:"attempt"`
					Runtime tryRuntimeView `json:"runtime"`
				}{Attempt: makeTryAttemptView(attempt.Attempt), Runtime: runtime})
			}
		}
		data.Tries = append(data.Tries, view)
	}
	return data, nil
}

func makeTryCleanupData(result *tryflow.CleanupResult) tryCleanupData {
	data := emptyTryCleanupData()
	if result == nil {
		return data
	}
	data.Try = makeTryRecordDetail(result.Try, nil)
	data.Transactions = append(data.Transactions, result.Transactions...)
	for _, item := range result.Items {
		data.Items = append(data.Items, tryCleanupItemView{
			Attempt: makeTryAttemptView(item.Attempt), RemovedWorkspace: item.RemovedWorkspace,
			RemovedBundle: item.RemovedBundle, AlreadyCompleted: item.AlreadyCompleted,
			Refused: item.Refused, Reason: item.Reason,
		})
	}
	return data
}

func makeTryRecordDetail(document *record.Document, inventory *record.Inventory) tryRecordDetailView {
	if document == nil || document.Kind() != research.KindTry {
		return tryRecordDetailView{Sources: []string{}}
	}
	value := document.Record.(*research.Try)
	display := value.ID.String()
	if inventory != nil {
		candidates := make([]research.ReferenceCandidate, 0)
		for _, candidate := range inventory.OfKind(research.KindTry) {
			id, _ := candidate.ID()
			candidates = append(candidates, research.ReferenceCandidate{ID: id})
		}
		if code, err := research.DisplayCode(value.ID, candidates); err == nil {
			display = code
		}
	}
	sources := make([]string, len(value.Sources))
	for index, source := range value.Sources {
		sources[index] = source.String()
	}
	view := tryRecordDetailView{
		ID: value.ID.String(), Display: display, Path: document.Path, Revision: document.Revision,
		Title: value.Title, State: string(value.State), Goal: value.Goal, Sources: sources,
		AdoptedIdea: value.AdoptedIdea.String(),
	}
	if table := value.Extensions[exploration.Namespace]; table != nil {
		view.Summary, _ = table["summary"].(string)
		view.SummaryAuthor, _ = table["summary_author"].(string)
		view.ConclusionAuthor, _ = table["conclusion_author"].(string)
	}
	if value.Conclusion != nil {
		view.Conclusion = &tryConclusionView{
			ConcludedAt:   value.Conclusion.ConcludedAt.UTC().Format(time.RFC3339Nano),
			Summary:       value.Conclusion.Summary,
			ResultDigests: append([]string{}, value.Conclusion.ResultDigests...),
			ExternalRefs:  append([]research.ExternalRef{}, value.Conclusion.ExternalRefs...),
		}
	}
	if value.Abandonment != nil {
		view.Abandonment = &tryAbandonmentView{
			AbandonedAt: value.Abandonment.AbandonedAt.UTC().Format(time.RFC3339Nano),
			Reason:      value.Abandonment.Reason,
		}
	}
	return view
}

func canonicalAttemptWorkspaceBackend(document *record.Document) string {
	if document == nil || document.Kind() != research.KindAttempt {
		return ""
	}
	attempt := document.Record.(*research.Attempt)
	if attempt.Extensions == nil {
		return ""
	}
	table := attempt.Extensions[tryflow.DirectExtensionNamespace]
	backend, _ := table["workspace_backend"].(string)
	if backend != workspacebackend.NativeGitName {
		return ""
	}
	return backend
}

func makeTryAttemptView(document *record.Document) tryAttemptView {
	view := tryAttemptView{Argv: []string{}, SourceSnapshots: []trySnapshotView{}, ResultDigests: []string{}}
	if document == nil || document.Kind() != research.KindAttempt {
		return view
	}
	value := document.Record.(*research.Attempt)
	view.Exploration, _ = exploration.MetadataFor(value)
	view.ID, view.Path, view.Revision, view.Title = value.ID.String(), document.Path, document.Revision, value.Title
	view.State, view.StateReason = string(value.State), safeDiagnosticText(value.StateReason)
	view.Runner, view.Scheduler, view.CWD = value.Runner, value.Scheduler, value.CWD
	view.Argv = safex.NewRedactor().Argv(value.Argv, safex.SensitiveArgvIndexes(value.Argv)...)
	view.ExecutionSource = value.ExecutionSource.String()
	for _, snapshot := range value.SourceSnapshots {
		view.SourceSnapshots = append(view.SourceSnapshots, trySnapshotView{
			Source: snapshot.Source.String(), Subdir: snapshot.Subdir, State: string(snapshot.State),
			BaseCommit: snapshot.BaseCommit, HeadCommit: snapshot.HeadCommit,
			ChangeSet: append([]string{}, snapshot.ChangeSet...), Digest: snapshot.Digest,
			DirtyDigest: snapshot.DirtyDigest, DirtySummary: snapshot.DirtySummary,
			Reproducibility: string(snapshot.Reproducibility),
		})
	}
	if value.Terminal != nil {
		terminal := &tryTerminalView{
			Source: value.Terminal.Source, ObservedAt: value.Terminal.ObservedAt.UTC().Format(time.RFC3339Nano),
			EndedAt: value.Terminal.EndedAt.UTC().Format(time.RFC3339Nano), ExitCode: value.Terminal.ExitCode,
			Signal: value.Terminal.Signal,
		}
		if value.Terminal.StartedAt != nil {
			terminal.StartedAt = value.Terminal.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		view.Terminal = terminal
	}
	if table := value.Extensions[tryflow.DirectExtensionNamespace]; table != nil {
		view.ResultDigests = extensionStrings(table["result_digests"])
		view.CleanupCompleted, _ = table["cleanup_completed"].(bool)
	}
	sort.Strings(view.ResultDigests)
	return view
}

func extensionStrings(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		result = append(result, typed...)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

func statusDiagnostics(status *tryflow.StatusResult) []Diagnostic {
	if status == nil || !status.Partial {
		return nil
	}
	diagnostics := []Diagnostic{}
	for _, entry := range status.Tries {
		for _, attempt := range entry.Attempts {
			if attempt.Error != "" {
				diagnostics = append(diagnostics, Diagnostic{Severity: SeverityWarning, Code: "try.status_partial", Message: attempt.Error})
			}
		}
	}
	if len(diagnostics) == 0 {
		diagnostics = append(diagnostics, Diagnostic{Severity: SeverityWarning, Code: "try.status_partial", Message: "some optional local Try status could not be inspected"})
	}
	return diagnostics
}
