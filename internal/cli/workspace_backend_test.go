package cli

import (
	"context"
	"encoding/json"
	"errors"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

func TestWorkspaceBackendListIsLookPathOnlyByDefault(t *testing.T) {
	app := NewApp(t.Context(), nil, nil, nil)
	app.Getwd = func() (string, error) { return t.TempDir(), nil }
	var lookups []string
	app.BinaryLookup = func(name string) (string, error) {
		lookups = append(lookups, name)
		return "", errors.New("missing")
	}
	app.Invoker = execx.InvokerFunc(func(context.Context, execx.CommandSpec) (execx.Result, error) {
		t.Fatal("default workspace backend list invoked a provider")
		return execx.Result{}, nil
	})
	invocation := invokeCommand(t, app, "", "workspace", "backend", "list", "--json")
	requireCommandSuccess(t, invocation)
	var data workspaceBackendListData
	decodeData(t, decodeEnvelope(t, invocation.stdout), &data)
	if data.SchemaVersion != workspaceBackendListDataSchema || len(data.Descriptors) != 2 || len(data.Readiness) != 2 {
		t.Fatalf("backend list = %#v", data)
	}
	if !reflectWorkspaceReadiness(data.Readiness, workspacebackend.DevCLIName, workspacebackend.ReadinessMissing) ||
		!reflectWorkspaceReadiness(data.Readiness, workspacebackend.NativeGitName, workspacebackend.ReadinessBuiltInNative) {
		t.Fatalf("backend readiness = %#v", data.Readiness)
	}
	if len(lookups) != 1 || lookups[0] != "dev" {
		t.Fatalf("backend lookups = %v", lookups)
	}
}

func TestWorkspaceBackendStatusReportsExplicitFallbackWithoutPaths(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/provider-source.git",
		"01a09600-0000-7001-8000-000000000001",
		"01a09600-0000-7002-8000-000000000002",
		"01a09600-0000-7003-8000-000000000003",
	)
	addFixtureSource(t, fixture, "production")
	fixture.app.BinaryLookup = func(name string) (string, error) {
		if name == "dev" {
			return "/synthetic/bin/dev", nil
		}
		return "", errors.New("missing")
	}
	fixture.app.Invoker = execx.InvokerFunc(func(context.Context, execx.CommandSpec) (execx.Result, error) {
		t.Fatal("unprobed workspace backend status invoked dev-cli")
		return execx.Result{}, nil
	})
	invocation := invokeCommand(t, fixture.app, "",
		"--start-dir", fixture.source, "--workspace-backend", "dev_cli",
		"workspace", "backend", "status", "--json")
	requireCommandSuccess(t, invocation)
	var data workspaceBackendStatusData
	decodeData(t, decodeEnvelope(t, invocation.stdout), &data)
	if data.SchemaVersion != workspaceBackendStatusDataSchema || data.Selection.Origin != workspacebackend.SelectionExplicit ||
		data.Resolution.Requested != workspacebackend.DevCLIName || data.Resolution.Actual != workspacebackend.NativeGitName ||
		!data.Resolution.Fallback || data.Resolution.FallbackReason != "capability-unsupported" ||
		data.Resolution.Readiness.State != workspacebackend.ReadinessInstalledNotProbed {
		t.Fatalf("backend status = %#v", data)
	}
	for _, local := range []string{fixture.canonical, fixture.source, "/synthetic/bin/dev"} {
		if strings.Contains(invocation.stdout, local) {
			t.Fatalf("backend status leaked local path %q: %s", local, invocation.stdout)
		}
	}

	fixture.app.BinaryLookup = func(string) (string, error) { return "", errors.New("missing") }
	missing := invokeCommand(t, fixture.app, "",
		"--start-dir", fixture.source, "--workspace-backend", "dev_cli",
		"workspace", "backend", "status", "--json")
	requireCommandSuccess(t, missing)
	decodeData(t, decodeEnvelope(t, missing.stdout), &data)
	if data.Resolution.Actual != workspacebackend.NativeGitName || !data.Resolution.Fallback ||
		data.Resolution.Readiness.State != workspacebackend.ReadinessMissing {
		t.Fatalf("explicit missing unsupported backend status = %#v", data)
	}
}

func TestDoctorLiveUsesBoundedWorkspaceProbeAndOmitsBinaryPath(t *testing.T) {
	app := NewApp(t.Context(), nil, nil, nil)
	app.Getwd = func() (string, error) { return t.TempDir(), nil }
	app.BinaryLookup = func(name string) (string, error) {
		if name == "dev" {
			return "/synthetic/private/bin/dev", nil
		}
		return "", errors.New("missing")
	}
	calls := 0
	app.Invoker = execx.InvokerFunc(func(_ context.Context, spec execx.CommandSpec) (execx.Result, error) {
		calls++
		if len(spec.Argv) == 5 && spec.Argv[0] == "--no-runtime" && spec.Argv[1] == "ls" {
			return execx.Result{Stdout: "[]\n", ExitCode: 0}, nil
		}
		return execx.Result{Stdout: "human output ignored\n", ExitCode: 0}, nil
	})
	invocation := invokeCommand(t, app, "", "doctor", "--live", "--json")
	requireCommandSuccess(t, invocation)
	var data doctorData
	decodeData(t, decodeEnvelope(t, invocation.stdout), &data)
	if !data.LiveProbesPerformed || !reflectWorkspaceReadiness(data.WorkspaceBackends, workspacebackend.DevCLIName, workspacebackend.ReadinessUnsupported) || calls != 0 {
		t.Fatalf("live doctor = %#v calls=%d", data.WorkspaceBackends, calls)
	}
	if strings.Contains(invocation.stdout, "/synthetic/private/bin/dev") {
		t.Fatalf("doctor leaked backend binary path: %s", invocation.stdout)
	}
}

func TestExplicitDevCLIUsesNativePrepareAndKeepsProviderIDsLocal(t *testing.T) {
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := osexec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	const devBinary = "/synthetic/private/bin/dev"
	const providerID = "dev-local-task-and-worktree-id"
	baseLookup := fixture.app.BinaryLookup
	fixture.app.BinaryLookup = func(name string) (string, error) {
		if name == "dev" {
			return devBinary, nil
		}
		return baseLookup(name)
	}
	baseInvoker := fixture.app.Invoker
	devCalls := 0
	fixture.app.Invoker = execx.InvokerFunc(func(ctx context.Context, spec execx.CommandSpec) (execx.Result, error) {
		if spec.Executable != devBinary {
			return baseInvoker.Invoke(ctx, spec)
		}
		devCalls++
		switch {
		case len(spec.Argv) == 1 && spec.Argv[0] == "--version":
			return execx.Result{Stdout: "human version ignored\n", ExitCode: 0}, nil
		case len(spec.Argv) >= 2 && spec.Argv[len(spec.Argv)-1] == "--help":
			return execx.Result{Stdout: "human help ignored\n", ExitCode: 0}, nil
		case len(spec.Argv) == 5 && spec.Argv[0] == "--no-runtime" && spec.Argv[1] == "ls":
			return execx.Result{Stdout: "[]\n", ExitCode: 0}, nil
		case len(spec.Argv) == 7 && spec.Argv[2] == "wt" && spec.Argv[3] == "open":
			return execx.Result{Stdout: "opened " + providerID + " at /malicious/provider/path\n", ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected dev-cli argv: %#v", spec.Argv)
			return execx.Result{}, nil
		}
	})

	run := invokeCommand(t, fixture.app, "",
		"--start-dir", fixture.sourcePath, "--workspace-backend", "dev_cli",
		"try", "run", "--title", "Provider truth", "--goal", "Keep native Git authoritative",
		"--json", "--", trueBinary)
	requireCommandSuccess(t, run)
	var execution tryExecutionData
	decodeData(t, decodeEnvelope(t, run.stdout), &execution)
	if execution.Try == nil || execution.Attempt == nil || execution.Runtime.WorkspaceBackend != workspacebackend.NativeGitName {
		t.Fatalf("Try provider result = %#v", execution)
	}

	info, err := fixture.app.DiscoverProject(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	store, err := fixture.app.NewTransactionalStore(info)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := store.Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := research.ParseIDForKind(execution.Attempt.ID, research.KindAttempt)
	if err != nil {
		t.Fatal(err)
	}
	attemptDocument, err := inventory.ByID(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	extensions, err := json.Marshal(attemptDocument.Record.(*research.Attempt).Extensions)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(extensions), workspacebackend.NativeGitName) || strings.Contains(string(extensions), workspacebackend.DevCLIName) || strings.Contains(string(extensions), providerID) {
		t.Fatalf("canonical Attempt provider metadata = %s", extensions)
	}

	handoff := invokeCommand(t, fixture.app, "",
		"--start-dir", fixture.sourcePath, "--workspace-backend", "dev_cli",
		"workspace", "backend", "handoff", execution.Try.ID, "--attempt", execution.Attempt.ID, "--json")
	if !errors.Is(handoff.err, workspacebackend.ErrUnsupportedCapability) {
		t.Fatalf("unsupported exact-path handoff = %v", handoff.err)
	}
	if strings.Contains(handoff.stdout, providerID) || strings.Contains(handoff.stdout, "/malicious/provider/path") || strings.Contains(handoff.stdout, devBinary) {
		t.Fatalf("handoff leaked provider-local data: %s", handoff.stdout)
	}
	if devCalls != 0 {
		t.Fatalf("unsupported dev-cli capabilities invoked %d subprocesses", devCalls)
	}
}

func reflectWorkspaceReadiness(rows []workspacebackend.Readiness, name string, state workspacebackend.ReadinessState) bool {
	for _, row := range rows {
		if row.Provider == name {
			return row.State == state
		}
	}
	return false
}
