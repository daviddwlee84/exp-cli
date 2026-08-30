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
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/skill"
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

// ProviderRegistry is the local-only discovery surface used by doctor.
type ProviderRegistry interface {
	List() []provider.Descriptor
	DiscoverLocal(context.Context, provider.LocalDiscoveryOptions) ([]provider.ProbeResult, error)
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

	Now            func() time.Time
	Getwd          func() (string, error)
	GenerateUUID   research.UUIDGenerator
	ExecutablePath func() (string, error)

	DiscoverProject        func(context.Context, string) (*project.Info, error)
	InitializeProject      func(context.Context, project.InitRequest) (*project.Info, bool, error)
	NewStore               func(*project.Info) (RecordStore, error)
	NewTransactionalStore  func(*project.Info) (TransactionalRecordStore, error)
	OpenOperational        func(context.Context, *project.Info) (OperationalStore, error)
	ResolveAgentConfigPath func() (string, error)
	LoadAgentConfig        func(string) (agentcli.Config, error)

	Registry     ProviderRegistry
	BinaryLookup provider.BinaryLookup
	Invoker      execx.Invoker

	RenderProjections func(context.Context, *record.Inventory) (projection.Result, error)
	CheckProjections  func(context.Context, *record.Inventory) (projection.Result, error)

	RenderSkill            func() (string, error)
	InstallSkill           func(context.Context, string, bool) (skill.InstallResult, error)
	CheckSkill             func(context.Context, string, skill.CheckOptions) (skill.CheckResult, error)
	ResolveDefaultSkillDir func() (string, error)

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
	if a.DiscoverProject == nil {
		a.DiscoverProject = project.Discover
	}
	if a.InitializeProject == nil {
		a.InitializeProject = func(ctx context.Context, request project.InitRequest) (*project.Info, bool, error) {
			return project.Initialize(ctx, request,
				project.WithClock(a.clock),
				project.WithUUIDGenerator(a.GenerateUUID),
			)
		}
	}
	if a.NewStore == nil {
		a.NewStore = func(info *project.Info) (RecordStore, error) {
			if info == nil {
				return nil, fmt.Errorf("project information is required")
			}
			return record.NewStore(info.Root, info.Repository.GitCommonDir,
				record.WithClock(a.clock),
				record.WithUUIDGenerator(a.GenerateUUID),
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
			), nil
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
	if a.RenderProjections == nil {
		a.RenderProjections = projection.Render
	}
	if a.CheckProjections == nil {
		a.CheckProjections = projection.Check
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

// Warnf writes a human warning to stderr. Machine-readable stdout is therefore
// never contaminated by warnings emitted through the application boundary.
func (a *App) Warnf(format string, args ...any) error {
	_, err := fmt.Fprintf(a.Err, "exp: warning: "+format+"\n", args...)
	return err
}
