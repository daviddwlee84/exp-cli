// Package cli assembles exp's command tree and owns process-level dependencies.
// Commands parse input, call injected services, and render through App; canonical
// validation and provider policy remain in their focused packages.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/skill"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/trust"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/tui"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

// RecordStore is the canonical read/write surface used by walking-path commands.
type RecordStore interface {
	Inventory(context.Context) (*record.Inventory, error)
	CreatePlan(context.Context, record.PlanInput) (*record.Document, error)
	ListPlans(context.Context) ([]*record.Document, []record.Diagnostic, error)
}

// TransactionalRecordStore is the canonical mutation boundary used by the
// autonomous-control-plane commands. The legacy walking-path interface stays
// narrow so existing command tests can continue to inject focused fakes.
type TransactionalRecordStore interface {
	Inventory(context.Context) (*record.Inventory, error)
	Transact(context.Context, record.TransactionRequest) (*record.TransactionResult, error)
	Recover(context.Context) error
}

// WorkspaceResolver resolves canonical authority independently from the physical
// invocation repository.
type WorkspaceResolver interface {
	Resolve(context.Context, workspace.ResolveRequest) (*workspace.Context, error)
}

// WorkspaceSnapshotResolver optionally returns the exact inventory already read
// during workspace resolution so read-only aggregate views do not reopen it.
type WorkspaceSnapshotResolver interface {
	ResolveSnapshot(context.Context, workspace.ResolveRequest) (*workspace.Context, *record.Inventory, error)
}

// AssociationStore is the private host-local mapping boundary. Its paths and
// Git-common identities must never be copied into canonical records.
type AssociationStore interface {
	workspace.AssociationReader
	Path() (string, error)
	LookupProject(context.Context, research.UUID) (workspace.ProjectAssociation, error)
	LookupSource(context.Context, research.UUID, research.ID) (workspace.SourceAssociation, error)
	RegisterProject(context.Context, *project.Info) (workspace.ProjectAssociation, error)
	RegisterProjectPath(context.Context, string) (workspace.ProjectAssociation, error)
	RegisterSource(context.Context, research.UUID, research.ID, string) (workspace.SourceAssociation, error)
	RemoveProject(context.Context, research.UUID) (bool, error)
	RemoveSource(context.Context, research.UUID, research.ID) (bool, error)
}

// ConfigLoader loads preferences only after workspace identity is resolved.
type ConfigLoader interface {
	Load(context.Context, config.Request) (*config.Result, error)
}

// TrustStore is the private exact-digest approval boundary used by config
// inspection and mutation commands.
type TrustStore interface {
	config.TrustChecker
	Path() (string, error)
	Inspect(context.Context, trust.Query) (trust.Inspection, error)
	Approve(context.Context, trust.Query) (trust.Receipt, error)
	Revoke(context.Context, trust.RevokeRequest) (int, error)
	List(context.Context) ([]trust.Receipt, error)
}

// SourceService is the canonical Source lifecycle surface used by Cobra.
type SourceService interface {
	ApplyAddPlan(context.Context, sourcepkg.AddPlan) (*record.Document, error)
	Lookup(context.Context, string) (*record.Document, error)
	List(context.Context) ([]*record.Document, error)
	AppendLocator(context.Context, sourcepkg.AppendLocatorRequest) (*record.Document, error)
	Retire(context.Context, sourcepkg.RetireRequest) (*record.Document, error)
}

// TryCoordinator is the replaceable cross-repository direct runtime boundary.
type TryCoordinator interface {
	Run(context.Context, tryflow.RunRequest) (*tryflow.ExecutionResult, error)
	Resume(context.Context, tryflow.ResumeRequest) (*tryflow.ExecutionResult, error)
	Retry(context.Context, tryflow.RetryRequest) (*tryflow.ExecutionResult, error)
	Reconcile(context.Context, tryflow.ReconcileRequest) (*tryflow.ExecutionResult, error)
	Status(context.Context, tryflow.StatusRequest) (*tryflow.StatusResult, error)
	Cleanup(context.Context, tryflow.CleanupRequest) (*tryflow.CleanupResult, error)
}

// ProviderRegistry is the local-only discovery surface used by doctor.
type ProviderRegistry interface {
	List() []provider.Descriptor
	DiscoverLocal(context.Context, provider.LocalDiscoveryOptions) ([]provider.ProbeResult, error)
}

// TUIRunner is the injected terminal-event boundary. Snapshot construction and
// all domain reads remain in callbacks supplied by the ui command.
type TUIRunner func(context.Context, io.Reader, io.Writer, tui.Options) error

type OperationalReader interface {
	Close() error
	Path() string
	RuntimeState(context.Context) (operation.RuntimeState, error)
	ListJobSummaries(context.Context) ([]operation.JobSummary, error)
}

type OperationalStore interface {
	Close() error
	Path() string
	RuntimeState(context.Context) (operation.RuntimeState, error)
	SetPaused(context.Context, bool, string) (operation.RuntimeState, error)
	AcquireLease(context.Context, string, string, time.Duration) (operation.Lease, error)
	RenewLease(context.Context, operation.Lease, time.Duration) (operation.Lease, error)
	ReleaseLease(context.Context, operation.Lease) error
	EnqueueJob(context.Context, operation.JobInput) (operation.Job, bool, error)
	GetJob(context.Context, string) (operation.Job, error)
	ClaimJob(context.Context, string, string, string, time.Duration) (operation.Job, error)
	ClaimJobByID(context.Context, string, string, time.Duration) (operation.Job, error)
	RenewJobClaim(context.Context, string, int64, string, time.Duration) (operation.Job, error)
	SetJobExternalRefs(context.Context, string, int64, *int64, string) error
	FinishJob(context.Context, string, int64, operation.JobState, json.RawMessage, string) (operation.Job, error)
	ListJobs(context.Context, ...operation.JobState) ([]operation.Job, error)
	AddOutbox(context.Context, operation.OutboxInput, time.Time) (operation.OutboxItem, bool, error)
	DueOutbox(context.Context, int) ([]operation.OutboxItem, error)
	SetOutboxState(context.Context, string, operation.OutboxState, time.Time, string) error
	Fairness(context.Context, string) (operation.Fairness, error)
	RecordDispatch(context.Context, string, string, float64) (operation.Fairness, error)
}

// App contains every invocation-scoped dependency shared by commands. Function
// fields are intentionally exported so focused tests can replace one boundary
// without touching the real cwd, HOME, installed tools, clock, or entropy.
type App struct {
	Context context.Context
	In      io.Reader
	Out     io.Writer
	Err     io.Writer

	Prompter      Prompter
	IsInteractive InteractiveCheck
	IsTUITerminal InteractiveCheck
	RunTUI        TUIRunner

	Now            func() time.Time
	Getwd          func() (string, error)
	GenerateUUID   research.UUIDGenerator
	ExecutablePath func() (string, error)
	GitRunner      gitx.Runner

	DiscoverProject         func(context.Context, string) (*project.Info, error)
	InitializeProject       func(context.Context, project.InitRequest) (*project.Info, bool, error)
	ResolveWorkspace        WorkspaceResolver
	Associations            AssociationStore
	ConfigLoader            ConfigLoader
	TrustStore              TrustStore
	NewStore                func(*project.Info) (RecordStore, error)
	NewTransactionalStore   func(*project.Info) (TransactionalRecordStore, error)
	NewSourceService        func(TransactionalRecordStore) SourceService
	NewTryCoordinator       func() TryCoordinator
	OpenOperational         func(context.Context, *project.Info) (OperationalStore, error)
	OpenOperationalReadOnly func(context.Context, *project.Info) (OperationalReader, error)
	ResolveAgentConfigPath  func() (string, error)
	LoadAgentConfig         func(string) (agentcli.Config, error)

	Registry          ProviderRegistry
	WorkspaceRegistry *workspacebackend.Registry
	BinaryLookup      provider.BinaryLookup
	Invoker           execx.Invoker

	RenderProjections func(context.Context, *record.Inventory) (projection.Result, error)
	CheckProjections  func(context.Context, *record.Inventory) (projection.Result, error)

	RenderSkill            func() (string, error)
	InstallSkill           func(context.Context, string, bool) (skill.InstallResult, error)
	CheckSkill             func(context.Context, string, skill.CheckOptions) (skill.CheckResult, error)
	ResolveDefaultSkillDir func() (string, error)

	colorMode             string
	colorExplicit         bool
	jsonIntent            bool
	machineOutput         bool
	jsonEnvelopeAttempted bool
}

// NewApp constructs an invocation-scoped composition root from explicit streams.
// All side-effecting defaults remain replaceable before command execution.
func NewApp(ctx context.Context, in io.Reader, out, errOut io.Writer) *App {
	app := &App{Context: ctx, In: in, Out: out, Err: errOut}
	app.setDefaults()
	return app
}

func (a *App) setDefaults() {
	if a.Context == nil {
		a.Context = context.Background()
	}
	if a.In == nil {
		a.In = strings.NewReader("")
	}
	if a.Out == nil {
		a.Out = io.Discard
	}
	if a.Err == nil {
		a.Err = io.Discard
	}
	if a.IsInteractive == nil {
		a.IsInteractive = terminalPair
	}
	if a.IsTUITerminal == nil {
		a.IsTUITerminal = terminalPair
	}
	if a.RunTUI == nil {
		a.RunTUI = tui.Run
	}
	if _, defaultPrompter := a.Prompter.(*terminalPrompter); a.Prompter == nil || defaultPrompter {
		a.Prompter = &terminalPrompter{app: a}
	}
	if a.Now == nil {
		a.Now = time.Now
	}
	if a.Getwd == nil {
		a.Getwd = os.Getwd
	}
	if a.GenerateUUID == nil {
		a.GenerateUUID = research.DefaultUUIDGenerator
	}
	if a.ExecutablePath == nil {
		a.ExecutablePath = os.Executable
	}
	if a.GitRunner == nil {
		a.GitRunner = gitx.ExecRunner{}
	}
	if a.DiscoverProject == nil {
		a.DiscoverProject = func(ctx context.Context, start string) (*project.Info, error) {
			return project.DiscoverWithGit(ctx, start, a.GitRunner)
		}
	}
	if a.InitializeProject == nil {
		a.InitializeProject = func(ctx context.Context, request project.InitRequest) (*project.Info, bool, error) {
			return project.Initialize(ctx, request,
				project.WithClock(a.clock),
				project.WithUUIDGenerator(a.GenerateUUID),
				project.WithGitRunner(a.GitRunner),
			)
		}
	}
	if a.Associations == nil {
		a.Associations = workspace.NewStore(
			workspace.WithClock(a.clock),
			workspace.WithGitRunner(gitx.RunnerFunc(func(ctx context.Context, directory string, arguments []string) (string, string, error) {
				return a.GitRunner.Run(ctx, directory, arguments)
			})),
		)
	}
	if a.TrustStore == nil {
		a.TrustStore = trust.NewStore(trust.WithClock(a.clock))
	}
	if a.ConfigLoader == nil {
		a.ConfigLoader = appConfigLoader{app: a}
	}
	if a.ResolveWorkspace == nil {
		a.ResolveWorkspace = appWorkspaceResolver{app: a}
	}
	if a.NewStore == nil {
		a.NewStore = func(info *project.Info) (RecordStore, error) {
			if info == nil {
				return nil, fmt.Errorf("project information is required")
			}
			return record.NewStore(info.Root, info.Repository.GitCommonDir,
				record.WithClock(a.clock),
				record.WithUUIDGenerator(a.GenerateUUID),
				record.WithGitRunner(gitx.RunnerFunc(func(ctx context.Context, directory string, arguments []string) (string, string, error) {
					return a.GitRunner.Run(ctx, directory, arguments)
				})),
			), nil
		}
	}
	if a.NewTransactionalStore == nil {
		a.NewTransactionalStore = func(info *project.Info) (TransactionalRecordStore, error) {
			if info == nil {
				return nil, fmt.Errorf("project information is required")
			}
			return record.NewStore(info.Root, info.Repository.GitCommonDir,
				record.WithClock(a.clock),
				record.WithUUIDGenerator(a.GenerateUUID),
				record.WithGitRunner(gitx.RunnerFunc(func(ctx context.Context, directory string, arguments []string) (string, string, error) {
					return a.GitRunner.Run(ctx, directory, arguments)
				})),
			), nil
		}
	}
	if a.NewSourceService == nil {
		a.NewSourceService = func(store TransactionalRecordStore) SourceService {
			return sourcepkg.New(store,
				sourcepkg.WithClock(a.clock),
				sourcepkg.WithUUIDGenerator(a.GenerateUUID),
			)
		}
	}
	if a.OpenOperational == nil {
		a.OpenOperational = func(ctx context.Context, info *project.Info) (OperationalStore, error) {
			if info == nil {
				return nil, fmt.Errorf("project information is required")
			}
			return operation.Open(ctx, info.Repository.GitCommonDir, operation.WithClock(a.clock))
		}
	}
	if a.OpenOperationalReadOnly == nil {
		a.OpenOperationalReadOnly = func(ctx context.Context, info *project.Info) (OperationalReader, error) {
			if info == nil {
				return nil, fmt.Errorf("project information is required")
			}
			return operation.OpenReadOnly(ctx, info.Repository.GitCommonDir, operation.WithClock(a.clock))
		}
	}
	if a.ResolveAgentConfigPath == nil {
		a.ResolveAgentConfigPath = func() (string, error) {
			root, err := os.UserConfigDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(root, "exp", "agents.toml"), nil
		}
	}
	if a.LoadAgentConfig == nil {
		a.LoadAgentConfig = agentcli.Load
	}
	if a.Registry == nil {
		a.Registry = provider.CompiledRegistry()
	}
	if a.BinaryLookup == nil {
		a.BinaryLookup = exec.LookPath
	}
	if a.Invoker == nil {
		a.Invoker = execx.NewInvoker()
	}
	if a.WorkspaceRegistry == nil {
		dynamicGit := gitx.RunnerFunc(func(ctx context.Context, directory string, arguments []string) (string, string, error) {
			return a.GitRunner.Run(ctx, directory, arguments)
		})
		dynamicInvoker := execx.InvokerFunc(func(ctx context.Context, spec execx.CommandSpec) (execx.Result, error) {
			return a.Invoker.Invoke(ctx, spec)
		})
		dynamicLookup := workspacebackend.BinaryLookup(func(name string) (string, error) {
			return a.BinaryLookup(name)
		})
		native := workspacebackend.NativeGit{Git: dynamicGit, Clock: a.clock}
		dev := workspacebackend.DevCLI{Invoker: dynamicInvoker, Git: dynamicGit, LookupBinary: dynamicLookup}
		a.WorkspaceRegistry = workspacebackend.MustRegistry(native, dev)
	}
	if a.RenderProjections == nil {
		a.RenderProjections = projection.Render
	}
	if a.CheckProjections == nil {
		a.CheckProjections = projection.Check
	}
	if a.NewTryCoordinator == nil {
		a.NewTryCoordinator = func() TryCoordinator {
			bundles := &sourcesnapshot.BundleStore{}
			capturer := sourcesnapshot.Capturer{Git: a.GitRunner, Clock: a.clock, Bundles: bundles}
			native := workspacebackend.NativeGit{Git: a.GitRunner, Clock: a.clock, Bundles: bundles}
			backend := a.WorkspaceRegistry.WithNative(native)
			return tryflow.NewDirect(tryflow.Dependencies{
				Resolver: a.ResolveWorkspace,
				OpenStore: func(info *project.Info) (tryflow.Store, error) {
					return a.NewTransactionalStore(info)
				},
				OpenOperations: func(ctx context.Context, info *project.Info) (tryflow.OperationalStore, error) {
					return a.OpenOperational(ctx, info)
				},
				OpenOperationStatus: func(ctx context.Context, info *project.Info) (tryflow.OperationalStatusReader, error) {
					return a.OpenOperationalReadOnly(ctx, info)
				},
				OperationalAvailable: operation.Available,
				Capturer:             capturer, Bundles: bundles, Backend: backend,
				Git: a.GitRunner, Invoker: a.Invoker,
				LookupExecutable: func(name string) (string, error) { return a.BinaryLookup(name) },
				Refresh: func(ctx context.Context, info *project.Info, store tryflow.Store) error {
					_, _, err := renderFreshProjections(ctx, a, info, store)
					return err
				},
				Clock: a.clock, GenerateUUID: a.GenerateUUID,
			})
		}
	}
	if a.RenderSkill == nil {
		a.RenderSkill = skill.Render
	}
	if a.InstallSkill == nil {
		a.InstallSkill = skill.Install
	}
	if a.CheckSkill == nil {
		a.CheckSkill = skill.CheckWithOptions
	}
	if a.ResolveDefaultSkillDir == nil {
		a.ResolveDefaultSkillDir = skill.ResolveDefaultDir
	}
}

type appConfigLoader struct{ app *App }

func (loader appConfigLoader) Load(ctx context.Context, request config.Request) (*config.Result, error) {
	if loader.app == nil {
		return nil, fmt.Errorf("config loader application is nil")
	}
	return config.NewLoader(config.WithTrustChecker(loader.app.TrustStore)).Load(ctx, request)
}

type appWorkspaceResolver struct{ app *App }

func (resolver appWorkspaceResolver) Resolve(ctx context.Context, request workspace.ResolveRequest) (*workspace.Context, error) {
	if resolver.app == nil {
		return nil, fmt.Errorf("workspace resolver application is nil")
	}
	app := resolver.app
	resolved := workspace.NewResolver(
		app.Associations,
		workspace.WithResolverGitRunner(app.GitRunner),
		workspace.WithProjectDiscoverer(func(ctx context.Context, start string, _ gitx.Runner) (*project.Info, error) {
			return app.DiscoverProject(ctx, start)
		}),
		workspace.WithConfigLoader(app.ConfigLoader),
		workspace.WithResolverClock(app.clock),
	)
	return resolved.Resolve(ctx, request)
}

func (resolver appWorkspaceResolver) ResolveSnapshot(ctx context.Context, request workspace.ResolveRequest) (*workspace.Context, *record.Inventory, error) {
	if resolver.app == nil {
		return nil, nil, fmt.Errorf("workspace resolver application is nil")
	}
	app := resolver.app
	resolved := workspace.NewResolver(
		app.Associations,
		workspace.WithResolverGitRunner(app.GitRunner),
		workspace.WithProjectDiscoverer(func(ctx context.Context, start string, _ gitx.Runner) (*project.Info, error) {
			return app.DiscoverProject(ctx, start)
		}),
		workspace.WithConfigLoader(app.ConfigLoader),
		workspace.WithResolverClock(app.clock),
	)
	return resolved.ResolveSnapshot(ctx, request)
}

func (resolver appWorkspaceResolver) ResolveHistoricalSource(ctx context.Context, request workspace.ResolveRequest) (*workspace.Context, error) {
	if resolver.app == nil {
		return nil, fmt.Errorf("workspace resolver application is nil")
	}
	app := resolver.app
	resolved := workspace.NewResolver(
		app.Associations,
		workspace.WithResolverGitRunner(app.GitRunner),
		workspace.WithProjectDiscoverer(func(ctx context.Context, start string, _ gitx.Runner) (*project.Info, error) {
			return app.DiscoverProject(ctx, start)
		}),
		workspace.WithConfigLoader(app.ConfigLoader),
		workspace.WithResolverClock(app.clock),
	)
	return resolved.ResolveHistoricalSource(ctx, request)
}

func (a *App) clock() time.Time {
	if a.Now == nil {
		return time.Now().UTC()
	}
	return a.Now().UTC()
}

func (a *App) observedAt() time.Time { return a.clock() }

func (a *App) startDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if a.Getwd == nil {
		return "", fmt.Errorf("current-directory lookup is not configured")
	}
	directory, err := a.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	if directory == "" {
		return "", fmt.Errorf("current-directory lookup returned an empty path")
	}
	return directory, nil
}

// Warnf writes a sanitized human warning to stderr. Machine-readable stdout is
// therefore never contaminated by warnings emitted through the application
// boundary, and only the trusted renderer may add terminal control bytes.
func (a *App) Warnf(format string, args ...any) error {
	message := safeDiagnosticText(fmt.Sprintf(format, args...))
	style := a.errStyle()
	if a.machineOutput {
		style = cliStyle{}
	}
	prefix := style.warning("exp: warning:")
	_, err := fmt.Fprintf(a.Err, "%s %s\n", prefix, message)
	return successfulOutputError(err)
}
