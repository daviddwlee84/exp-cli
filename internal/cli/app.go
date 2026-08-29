// Package cli assembles exp's command tree and owns process-level dependencies.
// Commands parse input, call injected services, and render through App; canonical
// validation and provider policy remain in their focused packages.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
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

// ProviderRegistry is the local-only discovery surface used by doctor.
type ProviderRegistry interface {
	List() []provider.Descriptor
	DiscoverLocal(context.Context, provider.LocalDiscoveryOptions) ([]provider.ProbeResult, error)
}

// App contains every invocation-scoped dependency shared by commands. Function
// fields are intentionally exported so focused tests can replace one boundary
// without touching the real cwd, HOME, installed tools, clock, or entropy.
type App struct {
	Context context.Context
	In      io.Reader
	Out     io.Writer
	Err     io.Writer

	Now          func() time.Time
	Getwd        func() (string, error)
	GenerateUUID research.UUIDGenerator

	DiscoverProject   func(context.Context, string) (*project.Info, error)
	InitializeProject func(context.Context, project.InitRequest) (*project.Info, bool, error)
	NewStore          func(*project.Info) (RecordStore, error)

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
