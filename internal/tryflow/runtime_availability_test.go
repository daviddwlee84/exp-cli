package tryflow

import (
	"context"
	"errors"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/worker"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

type unavailableResolver struct{ calls *int }

func (resolver unavailableResolver) Resolve(context.Context, workspace.ResolveRequest) (*workspace.Context, error) {
	*resolver.calls++
	return nil, errors.New("resolver must not run")
}

type unavailableCapturer struct{}

func (unavailableCapturer) CaptureClean(context.Context, sourcesnapshot.Request) (research.SourceSnapshot, error) {
	panic("capture must not run")
}
func (unavailableCapturer) CaptureDirty(context.Context, sourcesnapshot.Request) (sourcesnapshot.Result, error) {
	panic("capture must not run")
}
func (unavailableCapturer) VerifyDirty(context.Context, sourcesnapshot.Request, research.SourceSnapshot) (research.SourceSnapshot, error) {
	panic("capture must not run")
}

type unavailableBundles struct{}

func (unavailableBundles) Open(context.Context, string) (sourcesnapshot.Bundle, error) {
	panic("bundle store must not run")
}
func (unavailableBundles) Cleanup(context.Context, sourcesnapshot.Bundle) error {
	panic("bundle store must not run")
}

type unavailableBackend struct{}

func (unavailableBackend) Prepare(context.Context, workspacebackend.PrepareRequest) (workspacebackend.Workspace, error) {
	panic("backend must not run")
}
func (unavailableBackend) VerifyPrepared(context.Context, workspacebackend.PrepareRequest) (workspacebackend.Workspace, error) {
	panic("backend must not run")
}
func (unavailableBackend) Inspect(context.Context, workspacebackend.PrepareRequest) (workspacebackend.Inspection, error) {
	panic("backend must not run")
}
func (unavailableBackend) CleanupOrHandoff(context.Context, workspacebackend.PrepareRequest, workspacebackend.FinalizeAction) (workspacebackend.Inspection, error) {
	panic("backend must not run")
}

func TestUnavailableOperationalRuntimeFailsBeforeCanonicalResolution(t *testing.T) {
	resolverCalls := 0
	direct := NewDirect(Dependencies{
		Resolver:  unavailableResolver{calls: &resolverCalls},
		OpenStore: func(*project.Info) (Store, error) { panic("store must not open") },
		OpenOperations: func(context.Context, *project.Info) (OperationalStore, error) {
			panic("operational store must not open")
		},
		OperationalAvailable: func() error { return operation.ErrUnsupported },
		Capturer:             unavailableCapturer{}, Bundles: unavailableBundles{}, Backend: unavailableBackend{},
		RunJob: func(context.Context, OperationalStore, string, operation.Job, func(context.Context, worker.Workload) error) (worker.Terminal, error) {
			panic("worker must not run")
		},
		LoadTerminal: func(context.Context, string, string) (worker.Terminal, bool, error) {
			panic("terminal store must not run")
		},
	})
	result, err := direct.Run(t.Context(), RunRequest{Title: "Unsupported runtime", Goal: "Fail before publication", Argv: []string{"true"}})
	var runtimeErr *RuntimeError
	if result != nil || !errors.Is(err, operation.ErrUnsupported) || !errors.As(err, &runtimeErr) || runtimeErr.Partial || runtimeErr.Recoverable || resolverCalls != 0 {
		t.Fatalf("unavailable runtime result=%#v error=%v runtime=%#v resolver_calls=%d", result, err, runtimeErr, resolverCalls)
	}
}
