package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/tui"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
)

func TestUIRequiresTerminalBeforeRunnerOrReads(t *testing.T) {
	var runs atomic.Int32
	var stdout, stderr bytes.Buffer
	app := NewApp(t.Context(), strings.NewReader(""), &stdout, &stderr)
	app.IsTUITerminal = func(io.Reader, io.Writer) bool { return false }
	app.RunTUI = func(context.Context, io.Reader, io.Writer, tui.Options) error {
		runs.Add(1)
		return nil
	}
	root := NewRootCommand(app)
	root.SetArgs([]string{"ui"})
	_, err := root.ExecuteContextC(t.Context())
	if err == nil || !strings.Contains(err.Error(), "requires real terminal stdin and stdout") {
		t.Fatalf("ui error = %v", err)
	}
	if runs.Load() != 0 {
		t.Fatalf("nonterminal invocation ran TUI %d time(s)", runs.Load())
	}
	if stdout.Len() != 0 {
		t.Fatalf("nonterminal invocation wrote stdout: %q", stdout.String())
	}
}

func TestUIHasNoJSONMode(t *testing.T) {
	var runs atomic.Int32
	app := NewApp(t.Context(), strings.NewReader(""), io.Discard, io.Discard)
	app.IsTUITerminal = func(io.Reader, io.Writer) bool { return true }
	app.RunTUI = func(context.Context, io.Reader, io.Writer, tui.Options) error {
		runs.Add(1)
		return nil
	}
	root := NewRootCommand(app)
	root.SetArgs([]string{"ui", "--json"})
	_, err := root.ExecuteContextC(t.Context())
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --json") {
		t.Fatalf("ui --json error = %v", err)
	}
	if runs.Load() != 0 {
		t.Fatalf("ui --json ran TUI %d time(s)", runs.Load())
	}
}

func TestBareRootRemainsHelpAfterUIRegistration(t *testing.T) {
	var runs atomic.Int32
	var stdout bytes.Buffer
	app := NewApp(t.Context(), strings.NewReader(""), &stdout, io.Discard)
	app.RunTUI = func(context.Context, io.Reader, io.Writer, tui.Options) error {
		runs.Add(1)
		return nil
	}
	root := NewRootCommand(app)
	root.SetArgs(nil)
	if _, err := root.ExecuteContextC(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 0 {
		t.Fatalf("bare exp ran TUI %d time(s)", runs.Load())
	}
	if !strings.Contains(stdout.String(), "Available Commands:") || !strings.Contains(stdout.String(), "ui") {
		t.Fatalf("bare exp did not remain help:\n%s", stdout.String())
	}
}

func TestUIAdapterSharesInventoryAndDefersAllLiveProbes(t *testing.T) {
	now := time.Date(2026, time.September, 4, 15, 0, 0, 0, time.UTC)
	resolved, inventory, gitCommon := newUITestSnapshot(t, now)
	resolver := &uiSnapshotResolver{resolved: resolved, inventory: inventory}
	var storeLoads, binaryLookups, runnerCalls atomic.Int32
	app := NewApp(t.Context(), strings.NewReader(""), io.Discard, io.Discard)
	app.Now = func() time.Time { return now }
	app.Getwd = func() (string, error) { return resolved.InvocationDir, nil }
	app.ResolveWorkspace = resolver
	app.NewStore = func(*project.Info) (RecordStore, error) {
		storeLoads.Add(1)
		return nil, errors.New("TUI must reuse resolver inventory")
	}
	app.BinaryLookup = func(string) (string, error) {
		binaryLookups.Add(1)
		return "", fs.ErrNotExist
	}
	app.IsTUITerminal = func(io.Reader, io.Writer) bool { return true }
	app.RunTUI = func(ctx context.Context, _ io.Reader, _ io.Writer, options tui.Options) error {
		runnerCalls.Add(1)
		model := tui.NewModel(options)
		if view := model.View(); !strings.Contains(view, "[LOADING]") {
			t.Fatalf("initial frame omitted loading state:\n%s", view)
		}
		if resolver.calls.Load() != 0 || binaryLookups.Load() != 0 {
			t.Fatalf("constructing/View performed reads: resolver=%d lookup=%d", resolver.calls.Load(), binaryLookups.Load())
		}
		request := tui.NewRequest(1, tui.NewIdentity("test", tui.ViewWorkflow), now)
		response := options.Callbacks.LoadLocal(ctx, request)
		if response.Err() != nil {
			t.Fatalf("local callback: %v", response.Err())
		}
		snapshot := response.Snapshot()
		for _, view := range tui.Views() {
			if len(snapshot.Sections(view)) == 0 {
				t.Fatalf("local snapshot has no %s section", view)
			}
		}
		if binaryLookups.Load() != 0 {
			t.Fatalf("local callback performed %d provider lookup(s)", binaryLookups.Load())
		}
		readiness := snapshot.Sections(tui.ViewReadiness)
		foundUnprobed := false
		for _, section := range readiness {
			for _, row := range section.Rows() {
				foundUnprobed = foundUnprobed || row.State() == "unprobed"
			}
		}
		if !foundUnprobed {
			t.Fatal("local readiness descriptors omitted explicit unprobed rows")
		}

		probeRequest := tui.NewRequest(2, tui.NewIdentity("test", tui.ViewReadiness), now.Add(time.Second))
		probe := options.Callbacks.ProbeLive(ctx, probeRequest)
		if probe.Err() != nil {
			t.Fatalf("live callback: %v", probe.Err())
		}
		foundMissing, foundRemediation := false, false
		for _, section := range probe.Snapshot().Sections(tui.ViewReadiness) {
			for _, row := range section.Rows() {
				foundMissing = foundMissing || row.State() == "missing"
				foundRemediation = foundRemediation || strings.Contains(row.Remediation(), "outside exp ui")
			}
		}
		if !foundMissing || !foundRemediation {
			t.Fatalf("live readiness missing rows/remediation: missing=%t remediation=%t", foundMissing, foundRemediation)
		}
		return nil
	}

	root := NewRootCommand(app)
	root.SetArgs([]string{"ui"})
	if _, err := root.ExecuteContextC(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runnerCalls.Load() != 1 || resolver.calls.Load() != 1 || storeLoads.Load() != 0 {
		t.Fatalf("runs=%d resolver snapshots=%d fallback inventory loads=%d", runnerCalls.Load(), resolver.calls.Load(), storeLoads.Load())
	}
	path, err := operation.PathFor(gitCommon)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read-only TUI created operation database %s: %v", path, err)
	}
}

func TestUIImmediateSelectorsRedactCredentialShapedValues(t *testing.T) {
	const canary = "UI_SELECTOR_CANARY_71d2"
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{name: "workspace path", args: []string{"--workspace", "/tmp/api_token=" + canary, "ui"}},
		{name: "source selector", args: []string{"--source", "api_token=" + canary, "ui"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			app := NewApp(t.Context(), strings.NewReader(""), io.Discard, io.Discard)
			app.Getwd = func() (string, error) { return "/safe/start", nil }
			app.IsTUITerminal = func(io.Reader, io.Writer) bool { return true }
			app.RunTUI = func(_ context.Context, _ io.Reader, _ io.Writer, options tui.Options) error {
				if strings.Contains(options.Workspace, canary) || !strings.Contains(options.Workspace, "[REDACTED]") {
					t.Fatalf("immediate workspace selector was not redacted: %q", options.Workspace)
				}
				return nil
			}
			invocation := invokeCommand(t, app, "", testCase.args...)
			if invocation.err != nil || invocation.stdout != "" || invocation.stderr != "" {
				t.Fatalf("ui invocation: error=%v stdout=%q stderr=%q", invocation.err, invocation.stdout, invocation.stderr)
			}
		})
	}
}

func TestUILocalSnapshotAppliesEffectiveColorUnlessCLIOverridesIt(t *testing.T) {
	now := time.Date(2026, time.September, 4, 16, 15, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name           string
		args           []string
		wantMonochrome bool
	}{
		{name: "configured always", args: []string{"ui"}, wantMonochrome: false},
		{name: "explicit never", args: []string{"--color", "never", "ui"}, wantMonochrome: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resolved, inventory, _ := newUITestSnapshot(t, now)
			effective := config.Builtins()
			effective.UI.Color = config.ColorAlways
			resolved.Config = &config.Result{Effective: effective}
			app := NewApp(t.Context(), strings.NewReader(""), io.Discard, io.Discard)
			app.Now = func() time.Time { return now }
			app.Getwd = func() (string, error) { return resolved.InvocationDir, nil }
			app.ResolveWorkspace = &uiSnapshotResolver{resolved: resolved, inventory: inventory}
			app.IsTUITerminal = func(io.Reader, io.Writer) bool { return true }
			app.RunTUI = func(ctx context.Context, _ io.Reader, _ io.Writer, options tui.Options) error {
				request := tui.NewRequest(1, tui.NewIdentity(options.Workspace, tui.ViewWorkflow), now)
				response := options.Callbacks.LoadLocal(ctx, request)
				if response.Err() != nil {
					return response.Err()
				}
				monochrome, configured := response.Snapshot().Monochrome()
				if !configured || monochrome != testCase.wantMonochrome {
					t.Fatalf("snapshot monochrome=(%t,%t), want (%t,true)", monochrome, configured, testCase.wantMonochrome)
				}
				return nil
			}
			invocation := invokeCommand(t, app, "", testCase.args...)
			if invocation.err != nil || invocation.stdout != "" || invocation.stderr != "" {
				t.Fatalf("ui invocation: error=%v stdout=%q stderr=%q", invocation.err, invocation.stdout, invocation.stderr)
			}
		})
	}
}

func TestUIQueuedAttemptClassificationMatchesCanonicalRuntimePolicy(t *testing.T) {
	now := time.Date(2026, time.September, 4, 16, 30, 0, 0, time.UTC)
	schema, err := research.KindAttempt.Schema()
	if err != nil {
		t.Fatal(err)
	}
	for index, state := range []research.AttemptState{research.AttemptRunning, research.AttemptUnknown} {
		id := mustManifestID(t, []string{
			"att_01a09100-0000-7001-8000-000000000001",
			"att_01a09100-0000-7002-8000-000000000002",
		}[index])
		attempt := &research.Attempt{Common: research.Common{
			Schema: schema, ID: id, Title: "Attempt disagreement", CreatedAt: now, UpdatedAt: now,
		}, State: state}
		inventory := &record.Inventory{Documents: []*record.Document{{Record: attempt}}}
		rows := uiAttemptRows(inventory, []operation.JobSummary{{SubjectID: id.String(), State: operation.JobQueued}}, now)
		if len(rows) != 1 || rows[0].State() != "uncertain" || rows[0].Reason() != "queued job disagrees with canonical state" || !strings.Contains(rows[0].Remediation(), "Inspect") {
			t.Errorf("canonical %s + queued job row = %#v", state, rows)
		}
	}
}

type uiSnapshotResolver struct {
	calls     atomic.Int32
	resolved  *workspace.Context
	inventory *record.Inventory
}

func (resolver *uiSnapshotResolver) Resolve(context.Context, workspace.ResolveRequest) (*workspace.Context, error) {
	return resolver.resolved, nil
}

func (resolver *uiSnapshotResolver) ResolveSnapshot(context.Context, workspace.ResolveRequest) (*workspace.Context, *record.Inventory, error) {
	resolver.calls.Add(1)
	return resolver.resolved, resolver.inventory, nil
}

func newUITestSnapshot(t *testing.T, now time.Time) (*workspace.Context, *record.Inventory, string) {
	t.Helper()
	repositoryRoot := t.TempDir()
	gitCommon := filepath.Join(repositoryRoot, ".git")
	if err := os.MkdirAll(gitCommon, 0o700); err != nil {
		t.Fatal(err)
	}
	experimentsRoot := filepath.Join(repositoryRoot, "experiments")
	if err := os.MkdirAll(experimentsRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	projectID, err := research.ParseUUID("01a09000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	projectDocument := &record.Document{Path: record.ProjectFile, Body: "# UI test\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "UI test", CreatedAt: now, ExperimentsRoot: ".",
	}}
	inventory := record.InventoryFromDocuments(experimentsRoot, []*record.Document{projectDocument})
	if !inventory.Valid() {
		t.Fatalf("test inventory invalid: %v", inventory.Diagnostics)
	}
	info := &project.Info{
		Root: experimentsRoot,
		Repository: gitx.Repository{
			Root: repositoryRoot, GitDir: gitCommon, GitCommonDir: gitCommon,
		},
		Document: projectDocument,
	}
	resolved := &workspace.Context{
		Project: info, InvocationDir: repositoryRoot,
		Association: workspace.AssociationObservation{
			Kind: workspace.ResolutionCurrentProject, MatchedLocatorHints: []string{}, ValidatedAt: now,
		},
	}
	return resolved, inventory, gitCommon
}
