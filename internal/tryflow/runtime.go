package tryflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/experimentgit"
	"github.com/daviddwlee84/exp-cli/internal/exploration"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/mlflow"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/worker"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

const (
	DirectExtensionNamespace = "io.github.daviddwlee84.exp-cli.tryflow"
	DirectPolicySchema       = "exp.tryflow-direct/v1"
	DirectJobKind            = "try-direct"
	DirectJobRole            = "runner"
	DirectJobPool            = "direct"
	DirectJobLane            = "explore"
	defaultClaimTTL          = 2 * time.Minute
	maxRuntimeTimeout        = 7 * 24 * time.Hour
	maxAllowedGlobs          = 128
	maxAllowedGlobBytes      = 512
)

var (
	ErrRuntimeUnavailable    = errors.New("direct Try runtime is not configured")
	ErrSourceRequired        = errors.New("direct Try execution requires one resolved Source")
	ErrUnsupportedBackend    = errors.New("selected workspace backend is unsupported for direct Try execution")
	ErrConfigChanged         = errors.New("effective execution configuration changed after Attempt registration")
	ErrExecutionUncertain    = errors.New("direct Try execution may have started but has no durable terminal marker")
	ErrExecutionInProgress   = errors.New("direct Try execution is still active under a live job lease")
	ErrCommandFailed         = errors.New("direct Try command completed unsuccessfully")
	ErrNoRetryableAttempt    = errors.New("Try has no Attempt to retry or resume")
	ErrCleanupIncomplete     = errors.New("one or more managed Try resources could not be cleaned safely")
	ErrCanonicalUnavailable  = errors.New("canonical Try state is unavailable")
	ErrExecutableUnavailable = errors.New("direct Try executable is unavailable")
)

// RuntimeStage identifies the last completed side-effect boundary. It is safe to
// expose in JSON and lets callers distinguish a pre-publication failure from a
// recoverable canonical-to-operational gap.
type RuntimeStage string

const (
	StageResolve          RuntimeStage = "resolve"
	StageCapture          RuntimeStage = "capture"
	StageCanonicalPlan    RuntimeStage = "canonical_plan"
	StageProjection       RuntimeStage = "projection"
	StageWorkspace        RuntimeStage = "workspace"
	StageOperationalQueue RuntimeStage = "operational_queue"
	StageOperationalClaim RuntimeStage = "operational_claim"
	StageInvocation       RuntimeStage = "invocation"
	StageOperationalDone  RuntimeStage = "operational_terminal"
	StageCanonicalDone    RuntimeStage = "canonical_terminal"
	StageComplete         RuntimeStage = "complete"
)

// RuntimeError retains truthful partial state without putting private paths in
// its Error string. CLI views decide which result fields are safe to render.
type RuntimeError struct {
	Stage       RuntimeStage
	Result      *ExecutionResult
	Recoverable bool
	Partial     bool
	Err         error
}

func (failure *RuntimeError) Error() string {
	if failure == nil {
		return "direct Try runtime failed"
	}
	message := "direct Try runtime failed"
	if failure.Stage != "" {
		message += " at " + string(failure.Stage)
	}
	if failure.Err != nil {
		message += ": " + failure.Err.Error()
	}
	return message
}

func (failure *RuntimeError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

// SourceCapturer is the exact clean/dirty registration and recapture boundary.
type SourceCapturer interface {
	CaptureClean(context.Context, sourcesnapshot.Request) (research.SourceSnapshot, error)
	CaptureDirty(context.Context, sourcesnapshot.Request) (sourcesnapshot.Result, error)
	VerifyDirty(context.Context, sourcesnapshot.Request, research.SourceSnapshot) (research.SourceSnapshot, error)
}

// BundleStore exposes only validated private seed capabilities.
type BundleStore interface {
	Open(context.Context, string) (sourcesnapshot.Bundle, error)
	Cleanup(context.Context, sourcesnapshot.Bundle) error
}

// OperationalStore is the private job surface needed by direct execution.
type OperationalStore interface {
	Close() error
	EnqueueJob(context.Context, operation.JobInput) (operation.Job, bool, error)
	GetJob(context.Context, string) (operation.Job, error)
	ClaimJobByID(context.Context, string, string, time.Duration) (operation.Job, error)
	RenewJobClaim(context.Context, string, int64, string, time.Duration) (operation.Job, error)
	FinishJob(context.Context, string, int64, operation.JobState, json.RawMessage, string) (operation.Job, error)
}

// StoreFactory opens the canonical store selected by workspace resolution.
type StoreFactory func(*project.Info) (Store, error)

// OperationFactory opens writable private orchestration state for one Project.
type OperationFactory func(context.Context, *project.Info) (OperationalStore, error)

// OperationalStatusReader is the payload-free, non-mutating job projection used
// by list/show/status.
type OperationalStatusReader interface {
	Close() error
	ListJobSummaries(context.Context) ([]operation.JobSummary, error)
}

// OperationStatusFactory opens only an existing current-schema read-only store.
type OperationStatusFactory func(context.Context, *project.Info) (OperationalStatusReader, error)

// ProjectionRefresher rebuilds derived views after a successful canonical
// transaction. A refresh failure never erases the already-published result.
type ProjectionRefresher func(context.Context, *project.Info, Store) error

// RunJobFunc executes or repairs one claimed job. preflight must run immediately
// before worker Git verification and process start.
type RunJobFunc func(context.Context, OperationalStore, string, operation.Job, func(context.Context, worker.Workload) error) (worker.Terminal, error)

// LoadTerminalFunc reads one durable marker without trusting SQLite.
type LoadTerminalFunc func(context.Context, string, string) (worker.Terminal, bool, error)

// Dependencies are all nondeterministic or side-effecting direct-runtime seams.
type Dependencies struct {
	Resolver interface {
		Resolve(context.Context, workspace.ResolveRequest) (*workspace.Context, error)
	}
	OpenStore            StoreFactory
	OpenOperations       OperationFactory
	OpenOperationStatus  OperationStatusFactory
	OperationalAvailable func() error
	Capturer             SourceCapturer
	Bundles              BundleStore
	Backend              workspacebackend.Backend
	Git                  gitx.Runner
	Invoker              execx.Invoker
	LookupExecutable     func(string) (string, error)
	RunJob               RunJobFunc
	LoadTerminal         LoadTerminalFunc
	Refresh              ProjectionRefresher
	Clock                func() time.Time
	GenerateUUID         research.UUIDGenerator
	MarkerRoot           func(*project.Info) (string, error)
	ClaimTTL             time.Duration
	Holder               string
}

// Direct coordinates canonical Try meaning, private jobs, managed worktrees, and
// durable worker markers. It never contacts Pueue or MLflow.
type Direct struct {
	dependencies Dependencies
}

func NewDirect(dependencies Dependencies) *Direct {
	if dependencies.OperationalAvailable == nil {
		dependencies.OperationalAvailable = operation.Available
	}
	if dependencies.Git == nil {
		dependencies.Git = gitx.ExecRunner{}
	}
	if dependencies.Invoker == nil {
		dependencies.Invoker = execx.NewInvoker()
	}
	if dependencies.Clock == nil {
		dependencies.Clock = time.Now
	}
	if dependencies.GenerateUUID == nil {
		dependencies.GenerateUUID = research.DefaultUUIDGenerator
	}
	if dependencies.LookupExecutable == nil {
		dependencies.LookupExecutable = exec.LookPath
	}
	if dependencies.ClaimTTL <= 0 {
		dependencies.ClaimTTL = defaultClaimTTL
	}
	if dependencies.Holder == "" {
		dependencies.Holder = "exp-try-direct"
	}
	if dependencies.MarkerRoot == nil {
		dependencies.MarkerRoot = func(info *project.Info) (string, error) {
			if info == nil || info.Repository.GitCommonDir == "" {
				return "", errors.New("Project Git common directory is unavailable")
			}
			return filepath.Join(info.Repository.GitCommonDir, "exp", "v1", "attempts"), nil
		}
	}
	if dependencies.LoadTerminal == nil {
		dependencies.LoadTerminal = worker.LoadTerminal
	}
	if dependencies.RunJob == nil {
		dependencies.RunJob = func(ctx context.Context, store OperationalStore, markerRoot string, job operation.Job, preflight func(context.Context, worker.Workload) error) (worker.Terminal, error) {
			return (worker.Runner{
				Store: store, Invoker: dependencies.Invoker, MLflowInvoker: dependencies.Invoker,
				LookupBinary: dependencies.LookupExecutable, Git: dependencies.Git,
				Snapshots: dependencies.Capturer, Preflight: preflight,
				MarkerRoot: markerRoot, Clock: dependencies.Clock,
			}).Run(ctx, job)
		}
	}
	return &Direct{dependencies: dependencies}
}

// Selection separates physical invocation from explicit canonical selectors.
type Selection struct {
	InvocationDir    string
	Workspace        string
	Source           string
	WorkspaceBackend string
	MLflowProfile    string
}

// RunRequest registers and runs one new Try. Argv is passed directly; no field
// is interpreted as shell text.
type RunRequest struct {
	Selection
	ExistingTry    string
	ManagedOutputs bool
	StorageProfile string
	RunnerProfile  string
	InputNames     []string
	AllowLarge     bool
	Title          string
	Body           string
	Goal           string
	Argv           []string
	DirtyCapture   bool
	AllowedGlobs   []string
	Timeout        time.Duration
	Tags           []string
}

// ResumeRequest resumes only a provably unstarted planned/queued Attempt or
// imports its durable terminal marker. It never re-invokes an uncertain job.
type ResumeRequest struct {
	Selection
	Try string
}

// RetryRequest creates another Attempt only after the latest one is terminal.
// A planned/queued latest Attempt is resumed instead.
type RetryRequest struct {
	Selection
	Try string
}

// ExecutionResult carries internal and canonical state. Callers must not encode
// Workspace or Context directly because they contain host-local paths.
type ExecutionResult struct {
	Stage          RuntimeStage
	Project        *project.Info
	Context        *workspace.Context
	Try            *record.Document
	Attempt        *record.Document
	Workspace      *workspacebackend.Workspace
	Job            *operation.Job
	Terminal       *worker.Terminal
	Transactions   []string
	Recovered      bool
	RetryCreated   bool
	RecoveryAction string
}

func (result *ExecutionResult) clone() *ExecutionResult {
	if result == nil {
		return nil
	}
	copy := *result
	copy.Transactions = append([]string{}, result.Transactions...)
	if result.Try != nil {
		copy.Try = result.Try.Clone()
	}
	if result.Attempt != nil {
		copy.Attempt = result.Attempt.Clone()
	}
	if result.Workspace != nil {
		value := *result.Workspace
		copy.Workspace = &value
	}
	if result.Job != nil {
		value := *result.Job
		value.Payload = append(json.RawMessage(nil), result.Job.Payload...)
		value.Result = append(json.RawMessage(nil), result.Job.Result...)
		copy.Job = &value
	}
	if result.Terminal != nil {
		value := *result.Terminal
		value.Outputs = cloneStringMap(result.Terminal.Outputs)
		if result.Terminal.MLflow != nil {
			attachment := *result.Terminal.MLflow
			attachment.Metrics = cloneFloatMap(result.Terminal.MLflow.Metrics)
			attachment.Tags = cloneStringMap(result.Terminal.MLflow.Tags)
			attachment.Reasons = append([]string{}, result.Terminal.MLflow.Reasons...)
			value.MLflow = &attachment
		}
		copy.Terminal = &value
	}
	return &copy
}

func runtimeFailure(stage RuntimeStage, result *ExecutionResult, recoverable, partial bool, err error) error {
	if result != nil {
		result.Stage = stage
	}
	return &RuntimeError{Stage: stage, Result: result.clone(), Recoverable: recoverable, Partial: partial, Err: err}
}

// Run publishes the canonical Try and planned Attempt in one transaction before
// creating any operational job or managed worktree execution.
func (direct *Direct) Run(ctx context.Context, request RunRequest) (*ExecutionResult, error) {
	if err := direct.validateDependencies(); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if err := direct.validateOperationalAvailability(); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	var existingTry *record.Document
	if request.ExistingTry != "" {
		resolved, _, _, document, err := direct.resolveTry(ctx, request.Selection, request.ExistingTry)
		if err != nil {
			return nil, runtimeFailure(StageResolve, nil, false, false, err)
		}
		value := document.Record.(*research.Try)
		if value.State != research.TryOpen {
			return nil, runtimeFailure(StageResolve, nil, false, false, errors.New("only an open Try accepts new exploration steps"))
		}
		existingTry = document
		request.Workspace = resolved.Project.Root
		if request.Source == "" && len(value.Sources) == 1 {
			request.Source = value.Sources[0].String()
		}
		if request.Title == "" {
			request.Title = value.Title
		}
		request.Goal = value.Goal
	}
	if err := validateRunRequest(request); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	resolved, store, inventory, err := direct.resolveRunContext(ctx, request.Selection)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if err := requireDirectConfig(resolved, request.WorkspaceBackend); err != nil {
		return nil, runtimeFailure(StageResolve, nil, true, false, err)
	}
	mlflowProfile, err := mlflow.ResolveProfile(resolved.Config, request.MLflowProfile)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, true, false, err)
	}
	if err := validateDirectArgv(request.Argv, resolved); err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	sourceDocument, err := inventory.ByID(resolved.Source.ID)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	now := direct.now()
	allocator := New(store, WithClock(direct.dependencies.Clock), WithUUIDGenerator(direct.dependencies.GenerateUUID))
	reserved := make(map[research.ID]struct{})
	tryID, err := allocator.allocate(inventory, research.KindTry, now, reserved)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	if existingTry != nil {
		value := existingTry.Record.(*research.Try)
		tryID = value.ID
		allowedSource := false
		for _, source := range value.Sources {
			if source == resolved.Source.ID {
				allowedSource = true
			}
		}
		if !allowedSource {
			return nil, runtimeFailure(StageResolve, nil, false, false, errors.New("execution Source is not declared by this Try"))
		}
	}
	attemptID, err := allocator.allocate(inventory, research.KindAttempt, now, reserved)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	allowed, err := normalizeAllowedGlobs(request.AllowedGlobs)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	cwd, err := invocationCWD(resolved)
	if err != nil {
		return nil, runtimeFailure(StageResolve, nil, false, false, err)
	}
	captured, err := direct.capture(ctx, resolved, tryID, now, request.DirtyCapture)
	if err != nil {
		return nil, runtimeFailure(StageCapture, nil, false, false, err)
	}
	tryValue := &research.Try{
		Common: research.Common{
			Schema: research.SchemaTry, ID: tryID, Title: request.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string{}, request.Tags...),
		},
		State: research.TryOpen, Goal: request.Goal, Sources: []research.ID{resolved.Source.ID},
	}
	attemptValue := newDirectAttempt(attemptID, tryID, request.Title, cwd, request.Argv, captured, resolved, allowed, request.Timeout, mlflowProfile, now)
	if request.ManagedOutputs {
		settings, err := exploration.Load(ctx, "")
		if err != nil {
			return nil, runtimeFailure(StageCapture, nil, false, false, err)
		}
		execution, metadata, err := exploration.Prepare(ctx, settings, resolved.ProjectID().String(), tryID.String(), attemptID.String(), request.StorageProfile, request.RunnerProfile, request.InputNames, request.AllowLarge)
		if err != nil {
			return nil, runtimeFailure(StageCapture, nil, false, false, err)
		}
		for _, root := range []string{resolved.SourceRoot, resolved.Project.Repository.Root} {
			inside, checkErr := pathx.Contains(root, execution.Storage.Root)
			if checkErr != nil || inside {
				return nil, runtimeFailure(StageCapture, nil, false, false, errors.New("artifact storage must be outside the Source and canonical repositories"))
			}
		}
		if err := exploration.SetMetadata(attemptValue, metadata); err != nil {
			return nil, runtimeFailure(StageCapture, nil, false, false, err)
		}
		if err := exploration.PersistExecution(ctx, execution); err != nil {
			return nil, runtimeFailure(StageCapture, nil, false, false, err)
		}
	}
	if err := research.Validate(tryValue); err != nil {
		direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
		return nil, runtimeFailure(StageCapture, nil, false, false, err)
	}
	if err := research.Validate(attemptValue); err != nil {
		direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
		return nil, runtimeFailure(StageCapture, nil, false, false, err)
	}
	changes := newChanges()
	if err := changes.guard(sourceDocument, sourceDocument.Revision); err != nil {
		direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
		return nil, runtimeFailure(StageCanonicalPlan, nil, false, false, err)
	}
	if existingTry == nil {
		changes.create(&record.Document{Record: tryValue, Body: defaultBody(request.Body, request.Title)})
	} else if err := changes.guard(existingTry, existingTry.Revision); err != nil {
		return nil, runtimeFailure(StageCanonicalPlan, nil, false, false, err)
	}
	changes.create(&record.Document{Record: attemptValue, Body: "# Direct Try Attempt\n"})
	transaction, transactionErr := store.Transact(ctx, record.TransactionRequest{Operation: "try.run.plan", Changes: changes.values})
	result := &ExecutionResult{Stage: StageCanonicalPlan, Project: resolved.Project, Context: resolved}
	if transaction != nil {
		result.Transactions = append(result.Transactions, transaction.TransactionID)
		result.Try, _ = resultDocument(transaction, tryID)
		result.Attempt, _ = resultDocument(transaction, attemptID)
		if existingTry != nil {
			result.Try = existingTry.Clone()
		}
	}
	if transactionErr != nil {
		if transaction == nil {
			direct.cleanupUnpublishedBundle(ctx, captured.Bundle)
		}
		return result, runtimeFailure(StageCanonicalPlan, result, transaction != nil, transaction != nil, transactionErr)
	}
	if result.Try == nil || result.Attempt == nil {
		return result, runtimeFailure(StageCanonicalPlan, result, true, true, errors.New("canonical transaction omitted Try or Attempt"))
	}
	if err := direct.refresh(ctx, resolved.Project, store); err != nil {
		result.RecoveryAction = "exp try resume " + tryID.String()
		return result, runtimeFailure(StageProjection, result, true, true, err)
	}
	return direct.execute(ctx, request.Selection, result, captured.Bundle)
}

// ValidateRunPlan performs the direct runtime's complete mutation-free request,
// config, Source-selection, argv, allowlist, and MLflow-profile validation. CLI
// wizards use it before review; Run repeats every check before publication.
func ValidateRunPlan(request RunRequest, resolved *workspace.Context) error {
	if err := validateRunRequest(request); err != nil {
		return err
	}
	if resolved == nil || resolved.Source == nil {
		return ErrSourceRequired
	}
	if err := requireDirectConfig(resolved, request.WorkspaceBackend); err != nil {
		return err
	}
	if _, err := mlflow.ResolveProfile(resolved.Config, request.MLflowProfile); err != nil {
		return err
	}
	if err := validateDirectArgv(request.Argv, resolved); err != nil {
		return err
	}
	_, err := normalizeAllowedGlobs(request.AllowedGlobs)
	return err
}

func validateRunRequest(request RunRequest) error {
	if strings.TrimSpace(request.Title) == "" || request.Title != strings.TrimSpace(request.Title) {
		return fmt.Errorf("direct Try run requires an explicit trimmed --title: %w", ErrPrecondition)
	}
	if strings.TrimSpace(request.Goal) == "" || request.Goal != strings.TrimSpace(request.Goal) {
		return fmt.Errorf("direct Try run requires an explicit trimmed --goal; no prompt is performed: %w", ErrPrecondition)
	}
	if len(request.Argv) == 0 {
		return fmt.Errorf("direct Try run requires COMMAND after --: %w", ErrPrecondition)
	}
	for _, argument := range request.Argv {
		if argument == "" || !utf8.ValidString(argument) || strings.ContainsAny(argument, "\x00\r\n") {
			return fmt.Errorf("direct Try argv contains an empty or invalid argument: %w", ErrPrecondition)
		}
	}
	if request.Timeout < 0 || request.Timeout > maxRuntimeTimeout {
		return fmt.Errorf("direct Try timeout must be zero or at most %s: %w", maxRuntimeTimeout, ErrPrecondition)
	}
	return nil
}

func validateDirectArgv(argv []string, resolved *workspace.Context) error {
	if len(argv) == 0 {
		return fmt.Errorf("direct Try argv is empty: %w", ErrPrecondition)
	}
	knownRoots := []string{}
	if resolved != nil {
		knownRoots = append(knownRoots, resolved.InvocationDir, resolved.SourceRoot, resolved.ResolvedRoot)
		if resolved.Project != nil {
			knownRoots = append(knownRoots, resolved.Project.Root, resolved.Project.Repository.Root, resolved.Project.Repository.GitCommonDir)
		}
		if resolved.Association.Source != nil {
			knownRoots = append(knownRoots, resolved.Association.Source.Root, resolved.Association.Source.GitCommonDir)
		}
	}
	for index, argument := range argv {
		if hostAbsoluteArgument(argument) {
			return fmt.Errorf("direct Try argument %d contains a host-absolute path; use a PATH basename or managed-worktree-relative path: %w", index, ErrPrecondition)
		}
		if index == 0 {
			if err := validateExecutableReference(argument); err != nil {
				return err
			}
		}
		for _, root := range knownRoots {
			if root != "" && strings.Contains(argument, root) {
				return fmt.Errorf("direct Try argument %d embeds a host-local workspace path: %w", index, ErrPrecondition)
			}
		}
	}
	return nil
}

func validateExecutableReference(command string) error {
	if !strings.ContainsAny(command, `/\\`) {
		if strings.Contains(command, ":") {
			return fmt.Errorf("direct Try executable must be a PATH basename or managed-worktree-relative path: %w", ErrPrecondition)
		}
		return nil
	}
	relative := strings.TrimPrefix(command, "./")
	if relative == command && strings.HasPrefix(command, ".") {
		return fmt.Errorf("direct Try executable escapes the managed worktree: %w", ErrPrecondition)
	}
	if err := pathx.ValidateRelativePOSIX(relative, false); err != nil {
		return fmt.Errorf("direct Try executable must be a PATH basename or managed-worktree-relative path: %w", errors.Join(ErrPrecondition, err))
	}
	return nil
}

func hostAbsoluteArgument(argument string) bool {
	if hostAbsolutePath(argument) {
		return true
	}
	if _, attached, found := strings.Cut(argument, "="); found && hostAbsolutePath(attached) {
		return true
	}
	return false
}

func hostAbsolutePath(value string) bool {
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	return filepath.IsAbs(value) || path.IsAbs(value) ||
		len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' ||
		strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") || strings.HasPrefix(lower, "file:/")
}

func (direct *Direct) validateDependencies() error {
	if direct == nil || direct.dependencies.Resolver == nil || direct.dependencies.OpenStore == nil || direct.dependencies.OpenOperations == nil || direct.dependencies.Capturer == nil || direct.dependencies.Bundles == nil || direct.dependencies.Backend == nil || direct.dependencies.RunJob == nil || direct.dependencies.LoadTerminal == nil {
		return ErrRuntimeUnavailable
	}
	return nil
}

func (direct *Direct) validateOperationalAvailability() error {
	if direct == nil || direct.dependencies.OperationalAvailable == nil {
		return ErrRuntimeUnavailable
	}
	if err := direct.dependencies.OperationalAvailable(); err != nil {
		return errors.Join(ErrRuntimeUnavailable, err)
	}
	return nil
}

func (direct *Direct) resolveRunContext(ctx context.Context, selection Selection) (*workspace.Context, Store, *record.Inventory, error) {
	resolved, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: selection.InvocationDir, Workspace: strings.TrimSpace(selection.Workspace), Source: strings.TrimSpace(selection.Source),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if resolved == nil || resolved.Project == nil {
		return nil, nil, nil, ErrCanonicalUnavailable
	}
	store, err := direct.dependencies.OpenStore(resolved.Project)
	if err != nil {
		return nil, nil, nil, err
	}
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if !inventory.Valid() {
		return nil, nil, nil, &record.InventoryError{Diagnostics: append([]record.Diagnostic{}, inventory.Diagnostics...)}
	}
	if resolved.Source == nil {
		selector := ""
		if resolved.Config != nil {
			selector = strings.TrimSpace(resolved.Config.Effective.Defaults.Source)
			if selector != "" {
				if err := resolved.Config.RequireTrusted("defaults.source"); err != nil {
					return nil, nil, nil, err
				}
			}
		}
		if selector == "" {
			active := activeSources(inventory)
			if len(active) == 1 {
				selector = active[0].ID.String()
			}
		}
		if selector == "" {
			return nil, nil, nil, ErrSourceRequired
		}
		resolved, err = direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
			InvocationDir: selection.InvocationDir, Workspace: resolved.Project.Root, Source: selector,
		})
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if resolved.Source == nil || resolved.Association.Source == nil || resolved.SourceRoot == "" || resolved.ResolvedRoot == "" {
		return nil, nil, nil, ErrSourceRequired
	}
	return resolved, store, inventory, nil
}

func activeSources(inventory *record.Inventory) []*research.Source {
	values := make([]*research.Source, 0)
	if inventory == nil {
		return values
	}
	for _, document := range inventory.OfKind(research.KindSource) {
		value := document.Record.(*research.Source)
		if value.State == research.SourceActive {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID.String() < values[j].ID.String() })
	return values
}

func requireDirectConfig(resolved *workspace.Context, explicit string) error {
	if resolved == nil || resolved.Config == nil {
		return errors.New("effective configuration is unavailable")
	}
	if _, err := workspacebackend.ResolveSelection(explicit, resolved.Config); err != nil {
		return errors.Join(ErrUnsupportedBackend, err)
	}
	return nil
}

func (direct *Direct) capture(ctx context.Context, resolved *workspace.Context, tryID research.ID, capturedAt time.Time, dirty bool) (sourcesnapshot.Result, error) {
	head, err := direct.gitLine(ctx, resolved.SourceRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return sourcesnapshot.Result{}, err
	}
	request := sourcesnapshot.Request{
		Source: resolved.Source, RepositoryRoot: resolved.SourceRoot,
		RegisteredGitCommonDir:      resolved.Association.Source.GitCommonDir,
		RegisteredGitCommonIdentity: resolved.Association.Source.GitCommonIdentity,
		BaseCommit:                  head, ExpectedHead: head, Try: tryID,
		CapturedAt: capturedAt,
	}
	if dirty {
		return direct.dependencies.Capturer.CaptureDirty(ctx, request)
	}
	request.AllowNoChanges = true
	snapshot, err := direct.dependencies.Capturer.CaptureClean(ctx, request)
	return sourcesnapshot.Result{Snapshot: snapshot}, err
}

func newDirectAttempt(id, tryID research.ID, title, cwd string, argv []string, captured sourcesnapshot.Result, resolved *workspace.Context, allowed []string, timeout time.Duration, profile *mlflow.ResolvedProfile, now time.Time) *research.Attempt {
	snapshot := captured.Snapshot
	policy := map[string]any{
		"schema":            DirectPolicySchema,
		"workspace_backend": workspacebackend.NativeGitName,
		"allowed_globs":     append([]string{}, allowed...),
		"timeout":           timeout.String(),
	}
	if profile != nil {
		policy["mlflow_profile"] = profile.Name
		policy["mlflow_context"] = profile.Context
	}
	if captured.Bundle != nil {
		policy["seed_bundle_digest"] = captured.Bundle.Digest()
	}
	configDigest := ""
	if resolved != nil && resolved.Config != nil {
		configDigest = resolved.Config.Digest
	}
	return &research.Attempt{
		Common: research.Common{
			Schema: research.SchemaAttemptV3, ID: id, Title: title + " — direct attempt",
			CreatedAt: now, UpdatedAt: now,
		},
		Try: tryID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "direct",
		CWD: cwd, Argv: append([]string{}, argv...), ExecutionSource: snapshot.Source,
		SourceSnapshots: []research.SourceSnapshot{snapshot},
		Provenance: &research.Provenance{
			CapturedAt: now, GitCommit: snapshot.HeadCommit,
			GitDirty:    snapshot.State == research.SourceSnapshotDirty,
			DirtyDigest: snapshot.DirtyDigest, ConfigDigest: configDigest,
			Reproducibility: snapshot.Reproducibility,
		},
		Extensions: research.Extensions{DirectExtensionNamespace: policy},
	}
}

func defaultBody(body, title string) string {
	if body != "" {
		return body
	}
	return "# " + title + "\n"
}

func invocationCWD(resolved *workspace.Context) (string, error) {
	if resolved == nil || resolved.ResolvedRoot == "" || resolved.InvocationDir == "" {
		return ".", nil
	}
	inside, err := pathx.Contains(resolved.ResolvedRoot, resolved.InvocationDir)
	if err != nil || !inside {
		return ".", nil
	}
	relative, err := filepath.Rel(resolved.ResolvedRoot, resolved.InvocationDir)
	if err != nil {
		return "", err
	}
	relative = filepath.ToSlash(relative)
	if relative == "" {
		relative = "."
	}
	if err := pathx.ValidateRelativePOSIX(relative, true); err != nil {
		return "", err
	}
	return relative, nil
}

func normalizeAllowedGlobs(values []string) ([]string, error) {
	if len(values) > maxAllowedGlobs {
		return nil, fmt.Errorf("--allow exceeds %d entries: %w", maxAllowedGlobs, ErrPrecondition)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) || len(value) > maxAllowedGlobBytes || !utf8.ValidString(value) || strings.ContainsRune(value, '\\') {
			return nil, fmt.Errorf("invalid --allow glob %q: %w", value, ErrPrecondition)
		}
		for _, character := range value {
			if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
				return nil, fmt.Errorf("invalid control character in --allow: %w", ErrPrecondition)
			}
		}
		if err := pathx.ValidateRelativePOSIX(value, false); err != nil {
			return nil, fmt.Errorf("invalid --allow glob %q: %w", value, err)
		}
		for _, component := range strings.Split(value, "/") {
			if component == "**" {
				continue
			}
			if _, err := path.Match(component, "probe"); err != nil {
				return nil, fmt.Errorf("invalid --allow glob %q: %w", value, err)
			}
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	if result == nil {
		result = []string{}
	}
	return result, nil
}

func (direct *Direct) execute(ctx context.Context, selection Selection, result *ExecutionResult, knownBundle *sourcesnapshot.Bundle) (*ExecutionResult, error) {
	if result == nil || result.Try == nil || result.Attempt == nil {
		return result, runtimeFailure(StageCanonicalPlan, result, false, false, ErrCanonicalUnavailable)
	}
	tryValue := result.Try.Record.(*research.Try)
	attemptValue := result.Attempt.Record.(*research.Attempt)
	if terminalAttempt(attemptValue.State) {
		result.Stage = StageComplete
		return terminalOutcome(result)
	}
	resolved, store, inventory, err := direct.resolveAttemptContext(ctx, selection, tryValue.ID, attemptValue)
	if err != nil {
		result.RecoveryAction = "exp try resume " + tryValue.ID.String()
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	result.Project, result.Context = resolved.Project, resolved
	currentTry, currentAttempt, err := lookupTryAttempt(inventory, tryValue.ID, attemptValue.ID)
	if err != nil {
		return result, runtimeFailure(StageCanonicalPlan, result, true, true, err)
	}
	result.Try, result.Attempt = currentTry, currentAttempt
	tryValue = currentTry.Record.(*research.Try)
	attemptValue = currentAttempt.Record.(*research.Attempt)
	if terminalAttempt(attemptValue.State) {
		result.Stage = StageComplete
		return terminalOutcome(result)
	}
	if tryValue.State != research.TryOpen {
		return result, runtimeFailure(StageCanonicalPlan, result, false, true, fmt.Errorf("Try %s is %s and cannot execute pending work: %w", tryValue.ID, tryValue.State, ErrPrecondition))
	}
	if err := validateDirectArgv(attemptValue.Argv, resolved); err != nil {
		return result, runtimeFailure(StageWorkspace, result, false, true, err)
	}
	if err := direct.verifyRegisteredSource(ctx, resolved, attemptValue, selection.WorkspaceBackend); err != nil {
		result.RecoveryAction = "restore the registered Source/config identity, then run exp try resume " + tryValue.ID.String()
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	bundle, err := direct.bundleForAttempt(ctx, attemptValue, knownBundle)
	if err != nil {
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	prepare, err := prepareRequest(resolved, tryValue, attemptValue, bundle, selection.WorkspaceBackend)
	if err != nil {
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	// A running workload owns the workspace lock and may legitimately mutate
	// allowed paths. Observe its live operational lease before attempting the
	// idempotent preparation verifier, otherwise resume would block behind the
	// active process rather than reporting that execution is in progress.
	markerRoot, err := direct.dependencies.MarkerRoot(resolved.Project)
	if err != nil {
		return result, runtimeFailure(StageOperationalQueue, result, true, true, err)
	}
	operations, err := direct.dependencies.OpenOperations(ctx, resolved.Project)
	if err != nil {
		return result, runtimeFailure(StageOperationalQueue, result, true, true, err)
	}
	defer operations.Close()
	jobID := directJobID(attemptValue.ID)
	existing, lookupErr := operations.GetJob(ctx, jobID)
	if lookupErr == nil {
		result.Job = &existing
		if existing.State == operation.JobRunning {
			_, markerFound, markerErr := direct.dependencies.LoadTerminal(ctx, markerRoot, jobID)
			if markerErr != nil {
				return result, runtimeFailure(StageInvocation, result, true, true, markerErr)
			}
			if !markerFound && existing.LeaseExpiresAt != nil && direct.now().Before(existing.LeaseExpiresAt.UTC()) {
				result.RecoveryAction = "wait for the active command or inspect it with exp try status"
				return result, runtimeFailure(StageOperationalClaim, result, true, true, ErrExecutionInProgress)
			}
		}
	} else if !errors.Is(lookupErr, operation.ErrNotFound) {
		return result, runtimeFailure(StageOperationalQueue, result, true, true, lookupErr)
	}
	managed, err := direct.prepareManaged(ctx, prepare)
	if err != nil {
		result.RecoveryAction = "inspect the managed worktree, then run exp try resume " + tryValue.ID.String()
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	result.Workspace = &managed
	executionCWD, err := managedCWD(managed, attemptValue.CWD)
	if err != nil {
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	executable, err := direct.resolveExecutable(executionCWD, attemptValue.Argv[0])
	if err != nil {
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	policy, err := directPolicyFor(attemptValue)
	if err != nil {
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	mlflowProfile, err := resolveDirectMLflowProfile(resolved.Config, selection.MLflowProfile, policy)
	if err != nil {
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	workload, err := buildWorkload(attemptValue, prepare, managed, executionCWD, executable, mlflowProfile)
	if err != nil {
		return result, runtimeFailure(StageWorkspace, result, true, true, err)
	}
	metadata, metadataErr := exploration.MetadataFor(attemptValue)
	if metadataErr != nil {
		return result, runtimeFailure(StageWorkspace, result, false, true, metadataErr)
	}
	if metadata != nil {
		execution, err := exploration.LoadExecution(ctx, resolved.ProjectID().String(), attemptValue.ID.String())
		if err != nil || execution.Digest() != metadata.ContextDigest {
			return result, runtimeFailure(StageWorkspace, result, false, true, errors.New("exploration execution receipt does not match the canonical Attempt"))
		}
		workload.Schema = worker.ExplorationJobSchema
		workload.Exploration = &execution
		if metadata.Runner == nil {
			identity, err := exploration.InspectRunner(ctx, execution, executionCWD, executable, attemptValue.SourceSnapshots[0].HeadCommit)
			if err != nil {
				return result, runtimeFailure(StageWorkspace, result, true, true, err)
			}
			metadata.Runner = identity
			updated, err := saveExplorationMetadata(ctx, store, result.Attempt, *metadata)
			if err != nil {
				return result, runtimeFailure(StageWorkspace, result, true, true, err)
			}
			result.Attempt, currentAttempt = updated, updated
			attemptValue = updated.Record.(*research.Attempt)
		}
	}
	payload, err := json.Marshal(workload)
	if err != nil {
		return result, runtimeFailure(StageOperationalQueue, result, true, true, err)
	}
	jobInput := directJobInput(resolved, attemptValue, payload)
	if errors.Is(lookupErr, operation.ErrNotFound) {
		_, markerFound, markerErr := direct.dependencies.LoadTerminal(ctx, markerRoot, jobInput.ID)
		if markerErr != nil {
			return result, runtimeFailure(StageOperationalQueue, result, true, true, markerErr)
		}
		if attemptValue.State != research.AttemptPlanned && !markerFound {
			result.RecoveryAction = "do not rerun; private job state was lost after the Attempt left planned state"
			return result, runtimeFailure(StageOperationalQueue, result, false, true, ErrExecutionUncertain)
		}
	}
	job, _, err := operations.EnqueueJob(ctx, jobInput)
	if err != nil {
		return result, runtimeFailure(StageOperationalQueue, result, true, true, err)
	}
	result.Job = &job
	if attemptValue.State == research.AttemptPlanned {
		updated, transactionID, updateErr := direct.updateAttemptState(ctx, store, currentAttempt, research.AttemptQueued, "", nil, nil, nil)
		if transactionID != "" {
			result.Transactions = append(result.Transactions, transactionID)
		}
		if updateErr != nil {
			return result, runtimeFailure(StageOperationalQueue, result, true, true, updateErr)
		}
		result.Attempt, currentAttempt, attemptValue = updated, updated, updated.Record.(*research.Attempt)
		if err := direct.refresh(ctx, resolved.Project, store); err != nil {
			return result, runtimeFailure(StageProjection, result, true, true, err)
		}
	}
	return direct.claimOrRecover(ctx, selection, resolved, store, prepare, managed, markerRoot, operations, result)
}

func (direct *Direct) claimOrRecover(ctx context.Context, selection Selection, resolved *workspace.Context, store Store, prepare workspacebackend.PrepareRequest, managed workspacebackend.Workspace, markerRoot string, operations OperationalStore, result *ExecutionResult) (*ExecutionResult, error) {
	job, err := operations.GetJob(ctx, result.Job.ID)
	if err != nil {
		return result, runtimeFailure(StageOperationalQueue, result, true, true, err)
	}
	result.Job = &job
	attempt := result.Attempt.Record.(*research.Attempt)
	if job.State == operation.JobRunning {
		terminal, found, loadErr := direct.dependencies.LoadTerminal(ctx, markerRoot, job.ID)
		if loadErr != nil {
			return result, runtimeFailure(StageInvocation, result, true, true, loadErr)
		}
		if !found {
			if job.LeaseExpiresAt != nil && direct.now().Before(job.LeaseExpiresAt.UTC()) {
				result.RecoveryAction = "wait for the active command or inspect it with exp try status"
				return result, runtimeFailure(StageOperationalClaim, result, true, true, ErrExecutionInProgress)
			}
			updated, transactionID, updateErr := direct.updateAttemptState(context.WithoutCancel(ctx), store, result.Attempt, research.AttemptUnknown, "direct job lease expired without a durable terminal marker", nil, nil, nil)
			if transactionID != "" {
				result.Transactions = append(result.Transactions, transactionID)
			}
			if updateErr == nil {
				result.Attempt = updated
				_ = direct.refresh(context.WithoutCancel(ctx), resolved.Project, store)
			}
			result.RecoveryAction = "inspect the managed workspace; use exp try retry only after deciding the uncertain Attempt must not be trusted"
			return result, runtimeFailure(StageOperationalClaim, result, false, true, errors.Join(ErrExecutionUncertain, updateErr))
		}
		result.Terminal = &terminal
		result.Recovered = true
		return direct.repairAndImport(ctx, selection, resolved, store, prepare, managed, markerRoot, operations, result, job)
	}
	if terminalJob(job.State) {
		terminal, found, loadErr := direct.dependencies.LoadTerminal(ctx, markerRoot, job.ID)
		if loadErr != nil || !found {
			if loadErr == nil {
				loadErr = errors.New("operational job is terminal but its durable marker is missing")
			}
			return result, runtimeFailure(StageOperationalDone, result, false, true, loadErr)
		}
		result.Terminal = &terminal
		result.Recovered = true
		return direct.importTerminal(ctx, resolved, store, prepare, result, job)
	}
	if job.State != operation.JobQueued {
		return result, runtimeFailure(StageOperationalQueue, result, false, true, fmt.Errorf("direct job %s has unsupported state %s", job.ID, job.State))
	}
	if attempt.State == research.AttemptQueued || attempt.State == research.AttemptPlanned || attempt.State == research.AttemptUnknown {
		updated, transactionID, updateErr := direct.updateAttemptState(ctx, store, result.Attempt, research.AttemptStarting, "", nil, nil, nil)
		if transactionID != "" {
			result.Transactions = append(result.Transactions, transactionID)
		}
		if updateErr != nil {
			return result, runtimeFailure(StageOperationalQueue, result, true, true, updateErr)
		}
		result.Attempt = updated
	}
	holder := direct.invocationHolder()
	job, err = operations.ClaimJobByID(ctx, job.ID, holder, direct.dependencies.ClaimTTL)
	if err != nil {
		return result, runtimeFailure(StageOperationalClaim, result, true, true, err)
	}
	result.Job = &job
	runContext, stopHeartbeat := direct.startJobHeartbeat(ctx, operations, job, holder)
	heartbeatStopped := false
	defer func() {
		if !heartbeatStopped {
			_ = stopHeartbeat()
		}
	}()
	updated, transactionID, updateErr := direct.updateAttemptState(ctx, store, result.Attempt, research.AttemptRunning, "", nil, nil, nil)
	if transactionID != "" {
		result.Transactions = append(result.Transactions, transactionID)
	}
	if updateErr != nil {
		result.RecoveryAction = "do not rerun; status will report the claimed job as uncertain unless a marker appears"
		return result, runtimeFailure(StageOperationalClaim, result, false, true, updateErr)
	}
	result.Attempt = updated
	// Projection is derived and must not strand a known-unstarted claimed job. A
	// later canonical terminal refresh repairs any transient rendering failure.
	_ = direct.refresh(ctx, resolved.Project, store)
	preflight := func(preflightContext context.Context, workload worker.Workload) error {
		return direct.preflight(preflightContext, selection, resolved.Project, prepare, managed, result.Attempt.Record.(*research.Attempt), workload)
	}
	var terminal worker.Terminal
	runErr := workspacebackend.WithWorkspaceLock(runContext, prepare, func() error {
		var workerErr error
		terminal, workerErr = direct.dependencies.RunJob(runContext, operations, markerRoot, job, preflight)
		return workerErr
	})
	heartbeatErr := stopHeartbeat()
	heartbeatStopped = true
	if runErr == nil && heartbeatErr != nil {
		runErr = heartbeatErr
	}
	if terminal.JobID != "" {
		result.Terminal = &terminal
	}
	if runErr != nil {
		result.RecoveryAction = "run exp try resume " + result.Try.Record.(*research.Try).ID.String() + " to reconcile the durable marker"
		return result, runtimeFailure(StageInvocation, result, true, true, runErr)
	}
	job, err = operations.GetJob(context.WithoutCancel(ctx), job.ID)
	if err != nil {
		return result, runtimeFailure(StageOperationalDone, result, true, true, err)
	}
	result.Job = &job
	return direct.importTerminal(ctx, resolved, store, prepare, result, job)
}

func (direct *Direct) repairAndImport(ctx context.Context, selection Selection, resolved *workspace.Context, store Store, prepare workspacebackend.PrepareRequest, managed workspacebackend.Workspace, markerRoot string, operations OperationalStore, result *ExecutionResult, job operation.Job) (*ExecutionResult, error) {
	preflight := func(preflightContext context.Context, workload worker.Workload) error {
		return direct.preflight(preflightContext, selection, resolved.Project, prepare, managed, result.Attempt.Record.(*research.Attempt), workload)
	}
	terminal, err := direct.dependencies.RunJob(context.WithoutCancel(ctx), operations, markerRoot, job, preflight)
	if terminal.JobID != "" {
		result.Terminal = &terminal
	}
	if err != nil {
		return result, runtimeFailure(StageOperationalDone, result, true, true, err)
	}
	repaired, err := operations.GetJob(context.WithoutCancel(ctx), job.ID)
	if err != nil {
		return result, runtimeFailure(StageOperationalDone, result, true, true, err)
	}
	result.Job = &repaired
	return direct.importTerminal(ctx, resolved, store, prepare, result, repaired)
}

func (direct *Direct) importTerminal(ctx context.Context, resolved *workspace.Context, store Store, prepare workspacebackend.PrepareRequest, result *ExecutionResult, job operation.Job) (*ExecutionResult, error) {
	terminal := result.Terminal
	if terminal == nil {
		markerRoot, markerErr := direct.dependencies.MarkerRoot(resolved.Project)
		if markerErr != nil {
			return result, runtimeFailure(StageOperationalDone, result, true, true, markerErr)
		}
		loaded, found, loadErr := direct.dependencies.LoadTerminal(context.WithoutCancel(ctx), markerRoot, job.ID)
		if loadErr != nil || !found {
			if loadErr == nil {
				loadErr = errors.New("durable terminal marker is unavailable")
			}
			return result, runtimeFailure(StageOperationalDone, result, true, true, loadErr)
		}
		terminal = &loaded
		result.Terminal = terminal
	}
	attemptID := result.Attempt.Record.(*research.Attempt).ID.String()
	if err := worker.ValidateTerminalForJob(*terminal, job, attemptID); err != nil {
		return result, runtimeFailure(StageOperationalDone, result, false, true, err)
	}
	terminalState, reason := canonicalTerminalState(*terminal)
	if _, inspectErr := direct.dependencies.Backend.Inspect(context.WithoutCancel(ctx), prepare); inspectErr != nil {
		if errors.Is(inspectErr, experimentgit.ErrPathNotAllowed) || errors.Is(inspectErr, experimentgit.ErrForbiddenMetadata) || errors.Is(inspectErr, experimentgit.ErrWorkspaceState) || errors.Is(inspectErr, workspacebackend.ErrSeedMismatch) {
			terminalState = research.AttemptFailed
			reason = "managed workspace violated post-invocation safety policy"
		} else {
			result.RecoveryAction = "restore the registered managed workspace identity, then run exp try resume " + result.Try.Record.(*research.Try).ID.String()
			return result, runtimeFailure(StageOperationalDone, result, true, true, inspectErr)
		}
	}
	canonical := canonicalTerminal(*terminal, direct.now(), result.Attempt.Record.(*research.Attempt).CreatedAt)
	digests := terminalDigests(*terminal)
	updated, transactionID, err := direct.updateAttemptState(context.WithoutCancel(ctx), store, result.Attempt, terminalState, reason, canonical, digests, terminal.MLflow)
	if transactionID != "" {
		result.Transactions = append(result.Transactions, transactionID)
	}
	if err != nil {
		result.RecoveryAction = "run exp try resume " + result.Try.Record.(*research.Try).ID.String() + " to import the durable terminal marker"
		return result, runtimeFailure(StageOperationalDone, result, true, true, err)
	}
	result.Attempt = updated
	if metadata, metadataErr := exploration.MetadataFor(updated.Record.(*research.Attempt)); metadataErr != nil {
		return result, metadataErr
	} else if metadata != nil {
		archived, archiveErr := SaveArtifacts(context.WithoutCancel(ctx), store, resolved.ProjectID().String(), updated.Record.(*research.Attempt).ID, false)
		if archived != nil {
			result.Attempt = archived
		}
		if archiveErr != nil {
			result.RecoveryAction = "exp results save " + updated.Record.(*research.Attempt).ID.String()
			return result, runtimeFailure(StageCanonicalDone, result, true, true, archiveErr)
		}
	}
	result.Stage = StageCanonicalDone
	if err := direct.refresh(context.WithoutCancel(ctx), resolved.Project, store); err != nil {
		return result, runtimeFailure(StageProjection, result, true, true, err)
	}
	result.Stage = StageComplete
	return terminalOutcome(result)
}

func terminalOutcome(result *ExecutionResult) (*ExecutionResult, error) {
	if result == nil || result.Attempt == nil {
		return result, ErrCanonicalUnavailable
	}
	attempt := result.Attempt.Record.(*research.Attempt)
	if attempt.State == research.AttemptSucceeded {
		return result, nil
	}
	if attempt.State == research.AttemptCancelled {
		return result, runtimeFailure(StageComplete, result, false, false, context.Canceled)
	}
	if attempt.State == research.AttemptTimedOut {
		return result, runtimeFailure(StageComplete, result, false, false, context.DeadlineExceeded)
	}
	if terminalAttempt(attempt.State) {
		return result, runtimeFailure(StageComplete, result, false, false, ErrCommandFailed)
	}
	return result, nil
}

func (direct *Direct) resolveAttemptContext(ctx context.Context, selection Selection, tryID research.ID, attempt *research.Attempt) (*workspace.Context, Store, *record.Inventory, error) {
	initial, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: selection.InvocationDir, Workspace: strings.TrimSpace(selection.Workspace), Source: strings.TrimSpace(selection.Source), SkipConfig: true,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if initial == nil || initial.Project == nil {
		return nil, nil, nil, ErrCanonicalUnavailable
	}
	identity, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: initial.Project.Root, Workspace: initial.Project.Root, Source: attempt.ExecutionSource.String(), SkipConfig: true,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	attemptInvocation := filepath.Join(identity.ResolvedRoot, filepath.FromSlash(attempt.CWD))
	resolved, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: attemptInvocation, Workspace: initial.Project.Root, Source: attempt.ExecutionSource.String(),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if resolved.ProjectID() != initial.ProjectID() || resolved.Source == nil || resolved.Source.ID != attempt.ExecutionSource {
		return nil, nil, nil, errors.New("resolved Project or Source differs from registered Attempt")
	}
	store, err := direct.dependencies.OpenStore(resolved.Project)
	if err != nil {
		return nil, nil, nil, err
	}
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	tryDocument, attemptDocument, err := lookupTryAttempt(inventory, tryID, attempt.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if tryDocument.Record.(*research.Try).ID != tryID || attemptDocument.Record.(*research.Attempt).Try != tryID {
		return nil, nil, nil, errors.New("Attempt ownership changed")
	}
	return resolved, store, inventory, nil
}

func (direct *Direct) resolveAttemptWorkspaceContext(ctx context.Context, selection Selection, info *project.Info, attempt *research.Attempt) (*workspace.Context, error) {
	if info == nil || info.Project() == nil || attempt == nil {
		return nil, ErrCanonicalUnavailable
	}
	identity, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: info.Root, Workspace: info.Root, Source: attempt.ExecutionSource.String(), SkipConfig: true,
	})
	if err != nil {
		return nil, err
	}
	attemptInvocation := filepath.Join(identity.ResolvedRoot, filepath.FromSlash(attempt.CWD))
	resolved, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: attemptInvocation, Workspace: info.Root, Source: attempt.ExecutionSource.String(),
	})
	if err != nil {
		return nil, err
	}
	if resolved.ProjectID() != info.Project().ProjectID || resolved.Source == nil || resolved.Source.ID != attempt.ExecutionSource {
		return nil, errors.New("resolved Project or Source differs from registered Attempt")
	}
	return resolved, nil
}

func (direct *Direct) resolveHistoricalAttemptContext(ctx context.Context, selection Selection, tryID research.ID, attempt *research.Attempt) (*workspace.Context, Store, *record.Inventory, error) {
	initial, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: selection.InvocationDir, Workspace: strings.TrimSpace(selection.Workspace),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if initial == nil || initial.Project == nil {
		return nil, nil, nil, ErrCanonicalUnavailable
	}
	historical, ok := direct.dependencies.Resolver.(interface {
		ResolveHistoricalSource(context.Context, workspace.ResolveRequest) (*workspace.Context, error)
	})
	if !ok {
		return direct.resolveAttemptContext(ctx, selection, tryID, attempt)
	}
	resolved, err := historical.ResolveHistoricalSource(ctx, workspace.ResolveRequest{
		InvocationDir: selection.InvocationDir, Workspace: initial.Project.Root,
		Source: attempt.ExecutionSource.String(), SkipConfig: true,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if resolved.ProjectID() != initial.ProjectID() || resolved.Source == nil || resolved.Source.ID != attempt.ExecutionSource {
		return nil, nil, nil, errors.New("historical Project or Source differs from registered Attempt")
	}
	store, err := direct.dependencies.OpenStore(resolved.Project)
	if err != nil {
		return nil, nil, nil, err
	}
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	_, attemptDocument, err := lookupTryAttempt(inventory, tryID, attempt.ID)
	if err != nil || attemptDocument.Record.(*research.Attempt).Try != tryID {
		return nil, nil, nil, errors.Join(errors.New("Attempt ownership changed"), err)
	}
	return resolved, store, inventory, nil
}

func (direct *Direct) verifyRegisteredSource(ctx context.Context, resolved *workspace.Context, attempt *research.Attempt, explicitBackend string) error {
	if err := requireDirectConfig(resolved, explicitBackend); err != nil {
		return err
	}
	if attempt == nil || len(attempt.SourceSnapshots) != 1 || attempt.Provenance == nil {
		return errors.New("direct Attempt has incomplete Source identity")
	}
	if resolved.Config == nil || resolved.Config.Digest != attempt.Provenance.ConfigDigest {
		return ErrConfigChanged
	}
	snapshot := attempt.SourceSnapshots[0]
	request := sourcesnapshot.Request{
		Source: resolved.Source, RepositoryRoot: resolved.SourceRoot,
		RegisteredGitCommonDir:      resolved.Association.Source.GitCommonDir,
		RegisteredGitCommonIdentity: resolved.Association.Source.GitCommonIdentity,
		BaseCommit:                  snapshot.BaseCommit,
		ExpectedHead:                snapshot.HeadCommit,
		CapturedAt:                  snapshot.CapturedAt,
	}
	if snapshot.State == research.SourceSnapshotDirty {
		request.Try = attempt.Try
		_, err := direct.dependencies.Capturer.VerifyDirty(ctx, request, snapshot)
		return err
	}
	request.AllowNoChanges = len(snapshot.ChangeSet) == 0
	observed, err := direct.dependencies.Capturer.CaptureClean(ctx, request)
	if err != nil {
		return err
	}
	if observed.Digest != snapshot.Digest {
		return sourcesnapshot.ErrSourceChanged
	}
	return nil
}

func (direct *Direct) preflight(ctx context.Context, selection Selection, info *project.Info, prepare workspacebackend.PrepareRequest, managed workspacebackend.Workspace, attempt *research.Attempt, workload worker.Workload) error {
	if attempt == nil || len(attempt.Argv) == 0 || len(attempt.SourceSnapshots) != 1 || workload.SourceSnapshot == nil {
		return errors.New("direct worker payload or canonical Attempt is incomplete")
	}
	expectedCWD, err := managedCWD(managed, attempt.CWD)
	if err != nil {
		return err
	}
	expectedExecutable, err := direct.resolveExecutable(expectedCWD, attempt.Argv[0])
	if err != nil {
		return err
	}
	invocation := filepath.Join(prepare.RepositoryRoot, filepath.FromSlash(prepare.Source.Subdir), filepath.FromSlash(attempt.CWD))
	resolved, err := direct.dependencies.Resolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: invocation, Workspace: info.Root, Source: prepare.Source.ID.String(),
	})
	if err != nil {
		return err
	}
	if resolved.ProjectID() != prepare.ProjectID || resolved.Source == nil || resolved.Source.ID != prepare.Source.ID || !reflect.DeepEqual(resolved.Source, prepare.Source) {
		return errors.New("Project or Source identity changed immediately before invocation")
	}
	policy, err := directPolicyFor(attempt)
	if err != nil {
		return err
	}
	profile, err := resolveDirectMLflowProfile(resolved.Config, selection.MLflowProfile, policy)
	if err != nil {
		return err
	}
	var expectedMLflow *mlflow.WorkloadProfile
	if profile != nil {
		value := profile.WorkloadProfile
		value.Environment = append([]mlflow.EnvironmentBinding{}, value.Environment...)
		value.DefaultMetrics = append([]string{}, value.DefaultMetrics...)
		expectedMLflow = &value
	}
	expectedTimeout := ""
	if policy.Timeout > 0 {
		expectedTimeout = policy.Timeout.String()
	}
	snapshot := attempt.SourceSnapshots[0]
	expectedSchema := worker.SourceJobSchema
	metadata, err := exploration.MetadataFor(attempt)
	if err != nil {
		return err
	}
	if metadata != nil {
		expectedSchema = worker.ExplorationJobSchema
		if workload.Exploration == nil || workload.Exploration.Digest() != metadata.ContextDigest || workload.Exploration.Project != info.Project().ProjectID.String() || workload.Exploration.Attempt != attempt.ID.String() {
			return errors.New("exploration job identity mismatch")
		}
		if metadata.Runner == nil {
			return errors.New("exploration runner identity is missing")
		}
		digest, err := exploration.HashExecutable(ctx, expectedExecutable)
		if err != nil || digest != metadata.Runner.ExecutableDigest {
			return errors.New("runner executable changed since preparation")
		}
		if err := exploration.VerifyInputs(ctx, *workload.Exploration); err != nil {
			return err
		}
	} else if workload.Exploration != nil {
		return errors.New("unexpected exploration routing on a legacy Attempt")
	}
	if workload.Schema != expectedSchema || workload.AttemptID != attempt.ID.String() || workload.TryID != attempt.Try.String() ||
		workload.Executable != expectedExecutable || workload.CWD != expectedCWD || workload.RepositoryRoot != managed.Root ||
		workload.RegisteredGitCommonDir != prepare.RegisteredGitCommonDir || workload.RegisteredGitCommonIdentity != prepare.RegisteredGitCommonIdentity ||
		workload.Timeout != expectedTimeout || !sameStrings(workload.Args, attempt.Argv[1:]) ||
		workload.BaseCommit != snapshot.BaseCommit || workload.HeadCommit != snapshot.HeadCommit || !sameStrings(workload.ChangeSet, snapshot.ChangeSet) ||
		workload.SourceSnapshot.Source != snapshot.Source.String() || workload.SourceSnapshot.Digest != snapshot.Digest ||
		len(workload.AllowedEnv) != 0 || len(workload.SecretEnv) != 0 || !reflect.DeepEqual(workload.MLflow, expectedMLflow) ||
		len(workload.ExpectedOutputs) != 0 || workload.RuntimeConfigPath != "" {
		return errors.New("private direct job payload does not match its canonical Attempt")
	}
	if err := direct.verifyRegisteredSource(ctx, resolved, attempt, selection.WorkspaceBackend); err != nil {
		return err
	}
	verified, err := direct.dependencies.Backend.VerifyPrepared(ctx, prepare)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(verified, managed) {
		return errors.New("managed worktree identity changed immediately before invocation")
	}
	return nil
}

func prepareRequest(resolved *workspace.Context, tryValue *research.Try, attempt *research.Attempt, bundle *sourcesnapshot.Bundle, explicitBackend string) (workspacebackend.PrepareRequest, error) {
	if resolved == nil || resolved.Project == nil || resolved.Source == nil || resolved.Association.Source == nil || tryValue == nil || attempt == nil || len(attempt.SourceSnapshots) != 1 {
		return workspacebackend.PrepareRequest{}, errors.New("managed workspace request is incomplete")
	}
	policy, err := directPolicyFor(attempt)
	if err != nil {
		return workspacebackend.PrepareRequest{}, err
	}
	providerSelection, err := workspacebackend.ResolveSelection(explicitBackend, resolved.Config)
	if err != nil {
		return workspacebackend.PrepareRequest{}, err
	}
	snapshot := attempt.SourceSnapshots[0]
	baselinePaths := []string{}
	if snapshot.State == research.SourceSnapshotDirty {
		seedPaths := []string{}
		if bundle != nil && bundle.Manifest.SeedPaths != nil {
			for _, seed := range bundle.Manifest.SeedPaths {
				seedPaths = append(seedPaths, seed.Path)
			}
		} else {
			// Legacy private bundles did not carry selective seed descriptors. They
			// remain usable only through the backend's exact whole-snapshot fallback.
			seedPaths = append(seedPaths, snapshot.ChangeSet...)
		}
		for _, changed := range seedPaths {
			semantic, ok := sourceRelativePath(snapshot.Subdir, changed)
			if !ok {
				return workspacebackend.PrepareRequest{}, fmt.Errorf("dirty snapshot path %q is outside Source subdir %q", changed, snapshot.Subdir)
			}
			baselinePaths = append(baselinePaths, semantic)
		}
		sort.Strings(baselinePaths)
	}
	return workspacebackend.PrepareRequest{
		ProjectID: resolved.ProjectID(), Source: research.Clone(resolved.Source).(*research.Source),
		RepositoryRoot:              resolved.SourceRoot,
		RegisteredGitCommonDir:      resolved.Association.Source.GitCommonDir,
		RegisteredGitCommonIdentity: resolved.Association.Source.GitCommonIdentity,
		OwnerID:                     attempt.ID, OwnerTitle: tryValue.Title + " direct attempt", TryID: tryValue.ID,
		Snapshot: snapshot, AllowedPaths: []string{}, BaselinePaths: baselinePaths, AllowedPathGlobs: policy.AllowedGlobs,
		CanonicalMetadataRoot: canonicalMetadataRoot(resolved), SeedBundle: bundle, SeedBundleDigest: policy.SeedBundleDigest,
		Provider: providerSelection,
	}, nil
}

func sourceRelativePath(subdir, repositoryPath string) (string, bool) {
	if subdir == "." {
		return repositoryPath, repositoryPath != ""
	}
	prefix := strings.TrimSuffix(subdir, "/") + "/"
	if !strings.HasPrefix(repositoryPath, prefix) {
		return "", false
	}
	value := strings.TrimPrefix(repositoryPath, prefix)
	return value, value != ""
}

func canonicalMetadataRoot(resolved *workspace.Context) string {
	if resolved == nil || resolved.Project == nil || resolved.SourceRoot == "" {
		return ""
	}
	inside, err := pathx.Contains(resolved.SourceRoot, resolved.Project.Root)
	if err != nil || !inside {
		return ""
	}
	relative, err := filepath.Rel(resolved.SourceRoot, resolved.Project.Root)
	if err != nil {
		return ""
	}
	relative = filepath.ToSlash(relative)
	if relative == "." || research.ValidateCommittedPath(relative, false) != nil {
		return ""
	}
	return relative
}

func (direct *Direct) prepareManaged(ctx context.Context, request workspacebackend.PrepareRequest) (workspacebackend.Workspace, error) {
	// Prepare is deliberately idempotent and is the only reuse path that verifies
	// the workspace bytes against the canonical SourceSnapshot. Inspect alone
	// proves Git/path identity but cannot attest an already-allowed file's bytes.
	return direct.dependencies.Backend.Prepare(ctx, request)
}

func managedCWD(managed workspacebackend.Workspace, relative string) (string, error) {
	value, err := pathx.ResolveUnderNoSymlinks(managed.CWD, relative, true)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(value)
	if err != nil {
		return "", fmt.Errorf("inspect managed command cwd: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("managed command cwd is not a real directory")
	}
	return value, nil
}

func (direct *Direct) resolveExecutable(cwd, command string) (string, error) {
	if command == "" || strings.ContainsAny(command, "\x00\r\n") {
		return "", errors.New("command executable is invalid")
	}
	candidate := command
	if !filepath.IsAbs(candidate) && strings.ContainsAny(candidate, `/\\`) {
		candidate = filepath.Join(cwd, candidate)
	} else if !filepath.IsAbs(candidate) {
		resolved, err := direct.dependencies.LookupExecutable(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve executable %q: %w", command, ErrExecutableUnavailable)
		}
		candidate = resolved
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve executable %q: %w", command, ErrExecutableUnavailable)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve executable %q: %w", command, ErrExecutableUnavailable)
	}
	canonical = filepath.Clean(canonical)
	if !filepath.IsAbs(canonical) {
		return "", errors.New("resolved executable is not absolute")
	}
	return filepath.Clean(absolute), nil
}

func buildWorkload(attempt *research.Attempt, prepare workspacebackend.PrepareRequest, managed workspacebackend.Workspace, cwd, executable string, profile *mlflow.ResolvedProfile) (worker.Workload, error) {
	policy, err := directPolicyFor(attempt)
	if err != nil {
		return worker.Workload{}, err
	}
	snapshot := attempt.SourceSnapshots[0]
	binding := worker.BindSourceSnapshot(snapshot)
	timeout := ""
	if policy.Timeout > 0 {
		timeout = policy.Timeout.String()
	}
	var workloadProfile *mlflow.WorkloadProfile
	if profile != nil {
		value := profile.WorkloadProfile
		value.Environment = append([]mlflow.EnvironmentBinding{}, value.Environment...)
		value.DefaultMetrics = append([]string{}, value.DefaultMetrics...)
		workloadProfile = &value
	}
	return worker.Workload{
		Schema: worker.SourceJobSchema, AttemptID: attempt.ID.String(), TryID: attempt.Try.String(),
		Executable: executable, Args: append([]string{}, attempt.Argv[1:]...), CWD: cwd,
		Timeout: timeout, AllowedEnv: []string{}, SecretEnv: []string{}, MLflow: workloadProfile,
		RepositoryRoot:              managed.Root,
		RegisteredGitCommonDir:      prepare.RegisteredGitCommonDir,
		RegisteredGitCommonIdentity: prepare.RegisteredGitCommonIdentity,
		SourceSnapshot:              &binding,
		BaseCommit:                  snapshot.BaseCommit, HeadCommit: snapshot.HeadCommit,
		ChangeSet: append([]string{}, snapshot.ChangeSet...), ExpectedOutputs: []string{},
	}, nil
}

func directJobInput(resolved *workspace.Context, attempt *research.Attempt, payload json.RawMessage) operation.JobInput {
	id := directJobID(attempt.ID)
	return operation.JobInput{
		ID: id, IdempotencyKey: "tryflow.direct." + attempt.ID.String(),
		Kind: DirectJobKind, Role: DirectJobRole, SubjectID: attempt.ID.String(),
		CanonicalScope: resolved.ProjectID().String(), Pool: DirectJobPool, Lane: DirectJobLane,
		Units: 1, Profile: workspacebackend.NativeGitName, Payload: payload, MaxAttempts: 1,
	}
}

func directJobID(attempt research.ID) string { return "try-direct-" + attempt.String() }

func (direct *Direct) invocationHolder() string {
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err == nil {
		return direct.dependencies.Holder + "-" + hex.EncodeToString(entropy[:])
	}
	return fmt.Sprintf("%s-%d-%d", direct.dependencies.Holder, os.Getpid(), direct.now().UnixNano())
}

func (direct *Direct) startJobHeartbeat(parent context.Context, operations OperationalStore, job operation.Job, holder string) (context.Context, func() error) {
	runContext, cancel := context.WithCancel(parent)
	interval := direct.dependencies.ClaimTTL / 3
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-runContext.Done():
				done <- runContext.Err()
				return
			case <-ticker.C:
				renewed, err := operations.RenewJobClaim(runContext, job.ID, job.FencingToken, holder, direct.dependencies.ClaimTTL)
				if err != nil || renewed.FencingToken != job.FencingToken || renewed.State != operation.JobRunning {
					observed, observedErr := operations.GetJob(context.WithoutCancel(runContext), job.ID)
					if observedErr == nil && observed.FencingToken == job.FencingToken && terminalJob(observed.State) {
						done <- nil
						return
					}
					if err == nil {
						err = operation.ErrFenced
					}
					cancel()
					done <- fmt.Errorf("renew direct job lease: %w", errors.Join(err, observedErr))
					return
				}
			}
		}
	}()
	var once sync.Once
	var stopErr error
	return runContext, func() error {
		once.Do(func() {
			close(stop)
			stopErr = <-done
			cancel()
		})
		return stopErr
	}
}

func directMLflowAlreadyImported(attempt *research.Attempt, attachment *mlflow.Attachment) bool {
	if attachment == nil || attachment.State != mlflow.ObservationVerified {
		return true
	}
	if attempt == nil {
		return false
	}
	expected, err := attachment.ExternalRef(attempt.ID.String())
	if err != nil {
		return false
	}
	observed, found := findExternalRef(attempt.ExternalRefs, expected)
	return found && reflect.DeepEqual(observed, expected)
}

func (direct *Direct) updateAttemptState(ctx context.Context, store Store, current *record.Document, state research.AttemptState, reason string, terminal *research.Terminal, resultDigests []string, attachment *mlflow.Attachment) (*record.Document, string, error) {
	if current == nil || current.Kind() != research.KindAttempt {
		return nil, "", ErrCanonicalUnavailable
	}
	value := current.Record.(*research.Attempt)
	if value.State == state && reflect.DeepEqual(value.Terminal, terminal) && directMLflowAlreadyImported(value, attachment) {
		return current.Clone(), "", nil
	}
	replacement := current.Clone()
	attempt := replacement.Record.(*research.Attempt)
	attempt.State = state
	attempt.StateReason = reason
	attempt.Terminal = terminal
	updatedAt := direct.after(value.UpdatedAt)
	if terminal != nil && updatedAt.Before(terminal.ObservedAt) {
		updatedAt = terminal.ObservedAt
	}
	attempt.UpdatedAt = updatedAt
	if terminal != nil {
		observedAt := terminal.ObservedAt
		for _, role := range []research.ExternalRole{research.ExternalRunner, research.ExternalScheduler} {
			reference := research.ExternalRef{
				Role: role, Provider: "direct", Context: "local", NativeKind: "job",
				NativeID: directJobID(attempt.ID), ObservedAt: &observedAt,
				Metadata: map[string]any{"direct.state": string(state)},
			}
			if !containsExternalRef(attempt.ExternalRefs, reference) {
				attempt.ExternalRefs = append(attempt.ExternalRefs, reference)
			}
		}
		if attachment != nil && attachment.State == mlflow.ObservationVerified {
			reference, referenceErr := attachment.ExternalRef(attempt.ID.String())
			if referenceErr != nil {
				return nil, "", errors.New("direct worker MLflow observation does not belong to its Attempt")
			}
			if existing, found := findExternalRef(attempt.ExternalRefs, reference); found {
				if !reflect.DeepEqual(existing, reference) {
					return nil, "", errors.New("direct worker MLflow observation conflicts with its Attempt")
				}
			} else {
				attempt.ExternalRefs = append(attempt.ExternalRefs, reference)
			}
		}
		if attempt.Extensions == nil {
			attempt.Extensions = research.Extensions{}
		}
		table := attempt.Extensions[DirectExtensionNamespace]
		if table == nil {
			table = map[string]any{}
			attempt.Extensions[DirectExtensionNamespace] = table
		}
		if existing, found := table["result_digests"]; found {
			observed := extensionStringSlice(existing)
			sort.Strings(observed)
			expected := append([]string{}, resultDigests...)
			sort.Strings(expected)
			if !sameStrings(observed, expected) {
				return nil, "", errors.New("durable terminal result digests disagree with the canonical Attempt")
			}
		} else {
			table["result_digests"] = append([]string{}, resultDigests...)
		}
	}
	transaction, err := store.Transact(ctx, record.TransactionRequest{
		Operation: "try.attempt.state",
		Changes:   []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: current.Revision}},
	})
	if err != nil {
		return nil, transactionID(transaction), err
	}
	document, err := resultDocument(transaction, attempt.ID)
	return document, transactionID(transaction), err
}

func canonicalTerminal(terminal worker.Terminal, observed, notBefore time.Time) *research.Terminal {
	started := terminal.StartedAt.UTC()
	if started.Before(notBefore) {
		started = notBefore.UTC()
	}
	ended := terminal.EndedAt.UTC()
	if ended.Before(started) {
		ended = started
	}
	if observed.Before(ended) {
		observed = ended
	}
	exitCode := terminal.ExitCode
	return &research.Terminal{
		Source: "direct", ObservedAt: observed.UTC(), StartedAt: &started,
		EndedAt: ended, ExitCode: &exitCode,
	}
}

func canonicalTerminalState(terminal worker.Terminal) (research.AttemptState, string) {
	if terminal.TimedOut {
		return research.AttemptTimedOut, "direct command timed out"
	}
	if terminal.Cancelled || terminal.State == operation.JobCancelled {
		return research.AttemptCancelled, "direct command was cancelled"
	}
	if terminal.State == operation.JobSucceeded && terminal.ExitCode == 0 {
		return research.AttemptSucceeded, ""
	}
	return research.AttemptFailed, "direct command exited unsuccessfully"
}

func terminalDigests(terminal worker.Terminal) []string {
	seen := map[string]struct{}{}
	if terminal.ResultSHA256 != "" {
		seen[terminal.ResultSHA256] = struct{}{}
	}
	for _, digest := range terminal.Outputs {
		if digest != "" {
			seen[digest] = struct{}{}
		}
	}
	values := make([]string, 0, len(seen))
	for digest := range seen {
		values = append(values, digest)
	}
	sort.Strings(values)
	return values
}

func findExternalRef(values []research.ExternalRef, candidate research.ExternalRef) (research.ExternalRef, bool) {
	for _, value := range values {
		if value.Role == candidate.Role && value.Provider == candidate.Provider && value.NativeKind == candidate.NativeKind {
			return value, true
		}
	}
	return research.ExternalRef{}, false
}

func containsExternalRef(values []research.ExternalRef, candidate research.ExternalRef) bool {
	for _, value := range values {
		if value.Role == candidate.Role && value.Provider == candidate.Provider && value.Context == candidate.Context && value.NativeKind == candidate.NativeKind && value.NativeID == candidate.NativeID {
			return true
		}
	}
	return false
}

func lookupTryAttempt(inventory *record.Inventory, tryID, attemptID research.ID) (*record.Document, *record.Document, error) {
	if inventory == nil || !inventory.Valid() {
		return nil, nil, ErrCanonicalUnavailable
	}
	tryDocument, err := inventory.ByID(tryID)
	if err != nil {
		return nil, nil, err
	}
	attemptDocument, err := inventory.ByID(attemptID)
	if err != nil {
		return nil, nil, err
	}
	attempt := attemptDocument.Record.(*research.Attempt)
	if attempt.Try != tryID {
		return nil, nil, errors.New("Attempt does not belong to Try")
	}
	return tryDocument, attemptDocument, nil
}

func (direct *Direct) bundleForAttempt(ctx context.Context, attempt *research.Attempt, known *sourcesnapshot.Bundle) (*sourcesnapshot.Bundle, error) {
	if attempt == nil || len(attempt.SourceSnapshots) != 1 || attempt.SourceSnapshots[0].State != research.SourceSnapshotDirty {
		return nil, nil
	}
	policy, err := directPolicyFor(attempt)
	if err != nil {
		return nil, err
	}
	if policy.SeedBundleDigest == "" {
		return nil, errors.New("dirty direct Attempt omits its private seed digest")
	}
	if known != nil && known.Digest() == policy.SeedBundleDigest {
		copy := *known
		return &copy, nil
	}
	bundle, err := direct.dependencies.Bundles.Open(ctx, policy.SeedBundleDigest)
	if err != nil {
		return nil, err
	}
	return &bundle, nil
}

type directPolicy struct {
	Backend          string
	AllowedGlobs     []string
	Timeout          time.Duration
	SeedBundleDigest string
	MLflowProfile    string
	MLflowContext    string
	ResultDigests    []string
	CleanupCompleted bool
}

func directPolicyFor(attempt *research.Attempt) (directPolicy, error) {
	if attempt == nil || attempt.Extensions == nil {
		return directPolicy{}, errors.New("Attempt is not registered for direct Try execution")
	}
	table, found := attempt.Extensions[DirectExtensionNamespace]
	if !found || table == nil {
		return directPolicy{}, errors.New("Attempt lacks direct Try execution policy")
	}
	policy := directPolicy{}
	if schema, ok := table["schema"].(string); !ok || schema != DirectPolicySchema {
		return directPolicy{}, errors.New("direct Try execution policy schema is invalid")
	}
	var ok bool
	if policy.Backend, ok = table["workspace_backend"].(string); !ok || policy.Backend != workspacebackend.NativeGitName {
		return directPolicy{}, ErrUnsupportedBackend
	}
	allowed, valid := stringSliceValue(table["allowed_globs"])
	if !valid {
		return directPolicy{}, errors.New("direct Try allowlist policy is invalid")
	}
	normalizedAllowed, err := normalizeAllowedGlobs(allowed)
	if err != nil {
		return directPolicy{}, err
	}
	policy.AllowedGlobs = normalizedAllowed
	timeoutText, ok := table["timeout"].(string)
	if !ok {
		return directPolicy{}, errors.New("direct Try timeout policy is invalid")
	}
	if timeoutText != "0s" {
		parsed, err := time.ParseDuration(timeoutText)
		if err != nil || parsed < 0 || parsed > maxRuntimeTimeout {
			return directPolicy{}, errors.New("direct Try timeout policy is invalid")
		}
		policy.Timeout = parsed
	}
	policy.MLflowProfile, _ = table["mlflow_profile"].(string)
	policy.MLflowContext, _ = table["mlflow_context"].(string)
	if (policy.MLflowProfile == "") != (policy.MLflowContext == "") {
		return directPolicy{}, errors.New("direct Try MLflow profile binding is incomplete")
	}
	policy.SeedBundleDigest, _ = table["seed_bundle_digest"].(string)
	policy.ResultDigests, _ = stringSliceValue(table["result_digests"])
	policy.CleanupCompleted, _ = table["cleanup_completed"].(bool)
	return policy, nil
}

func resolveDirectMLflowProfile(result *config.Result, explicit string, policy directPolicy) (*mlflow.ResolvedProfile, error) {
	explicit = strings.TrimSpace(explicit)
	if policy.MLflowProfile == "" {
		if explicit != "" {
			return nil, errors.New("retry cannot add an MLflow profile to an Attempt lineage that did not select one")
		}
		return nil, nil
	}
	if explicit != "" && explicit != policy.MLflowProfile {
		return nil, errors.New("explicit MLflow profile differs from the Attempt binding")
	}
	profile, err := mlflow.ResolveProfile(result, policy.MLflowProfile)
	if err != nil {
		return nil, err
	}
	if profile == nil || profile.Context != policy.MLflowContext {
		return nil, ErrConfigChanged
	}
	return profile, nil
}

func stringSliceValue(value any) ([]string, bool) {
	if value == nil {
		return []string{}, true
	}
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...), true
	case []any:
		result := make([]string, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result[index] = text
		}
		return result, true
	default:
		reflected := reflect.ValueOf(value)
		if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
			return nil, false
		}
		result := make([]string, reflected.Len())
		for index := range result {
			if reflected.Index(index).Kind() != reflect.String {
				return nil, false
			}
			result[index] = reflected.Index(index).String()
		}
		return result, true
	}
}

func terminalAttempt(state research.AttemptState) bool {
	switch state {
	case research.AttemptSucceeded, research.AttemptFailed, research.AttemptCancelled,
		research.AttemptTimedOut, research.AttemptPreempted, research.AttemptOutOfMemory:
		return true
	default:
		return false
	}
}

func terminalJob(state operation.JobState) bool {
	return state == operation.JobSucceeded || state == operation.JobFailed || state == operation.JobCancelled
}

func transactionID(result *record.TransactionResult) string {
	if result == nil {
		return ""
	}
	return result.TransactionID
}

func (direct *Direct) refresh(ctx context.Context, info *project.Info, store Store) error {
	if direct.dependencies.Refresh == nil {
		return nil
	}
	return direct.dependencies.Refresh(ctx, info, store)
}

func (direct *Direct) cleanupUnpublishedBundle(ctx context.Context, bundle *sourcesnapshot.Bundle) {
	if bundle != nil && direct != nil && direct.dependencies.Bundles != nil {
		_ = direct.dependencies.Bundles.Cleanup(context.WithoutCancel(ctx), *bundle)
	}
}

func (direct *Direct) gitLine(ctx context.Context, directory string, arguments ...string) (string, error) {
	stdout, stderr, err := direct.dependencies.Git.Run(ctx, directory, append([]string{}, arguments...))
	if err != nil {
		return "", &gitx.Error{Dir: directory, Args: append([]string{}, arguments...), Stderr: stderr, Err: err}
	}
	value := strings.TrimSuffix(stdout, "\n")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("Git returned an invalid one-line identity")
	}
	return value, nil
}

func (direct *Direct) now() time.Time { return direct.dependencies.Clock().UTC() }

func (direct *Direct) after(previous time.Time) time.Time {
	now := direct.now()
	if !now.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return now
}

func cloneFloatMap(input map[string]float64) map[string]float64 {
	if input == nil {
		return nil
	}
	output := make(map[string]float64, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
