package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var testObservedAt = time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)

func TestImmediateFirstViewAndCallbacksStartOnlyFromInitCommand(t *testing.T) {
	var calls atomic.Int32
	model := NewModel(Options{
		Workspace: "workspace-1",
		Now:       func() time.Time { return testObservedAt },
		Callbacks: Callbacks{LoadLocal: func(_ context.Context, request Request) Response {
			calls.Add(1)
			return Success(request, snapshotWithRows("workspace-1", ViewWorkflow,
				NewRow(RowSpec{ID: "row-1", Title: "Local context", State: "ready"})))
		}},
	})

	view := model.View()
	if calls.Load() != 0 {
		t.Fatalf("View invoked callback %d time(s)", calls.Load())
	}
	for _, wanted := range []string{"exp ui", "read-only", "[LOADING]", "Loading local canonical"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("immediate frame does not contain %q:\n%s", wanted, view)
		}
	}
	command := model.Init()
	if calls.Load() != 0 {
		t.Fatalf("constructing Init command invoked callback %d time(s)", calls.Load())
	}
	messages := executeCommand(command)
	if calls.Load() != 1 {
		t.Fatalf("executing Init command invoked callback %d time(s), want 1", calls.Load())
	}
	for _, message := range messages {
		if response, ok := message.(LocalResponseMsg); ok {
			model = updateModel(t, model, response)
		}
	}
	if !strings.Contains(model.View(), "Local context") {
		t.Fatalf("loaded frame omitted callback row:\n%s", model.View())
	}
}

func TestStaleGenerationTimestampAndViewResponsesAreRejected(t *testing.T) {
	model := NewModel(Options{Workspace: "one", Now: func() time.Time { return testObservedAt }})
	original := *model.local
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = next.(Model)
	if model.Generation() == original.Generation() {
		t.Fatal("refresh did not advance generation")
	}
	stale := Success(original, snapshotWithRows("one", ViewWorkflow, NewRow(RowSpec{Title: "stale"})))
	model = updateModel(t, model, LocalResponseMsg{Response: stale})
	if _, found := model.Snapshot(); found {
		t.Fatal("stale generation response installed a snapshot")
	}

	current := *model.local
	wrongTime := NewRequest(current.Generation(), current.Identity(), current.ObservedAt().Add(time.Second))
	model = updateModel(t, model, LocalResponseMsg{Response: Success(wrongTime, snapshotWithRows("one", ViewWorkflow, NewRow(RowSpec{Title: "wrong time"})))})
	if _, found := model.Snapshot(); found {
		t.Fatal("mismatched request timestamp installed a snapshot")
	}

	wrongView := NewRequest(current.Generation(), NewIdentity(current.Workspace(), ViewQueue), current.ObservedAt())
	model = updateModel(t, model, LocalResponseMsg{Response: Success(wrongView, snapshotWithRows("one", ViewQueue, NewRow(RowSpec{Title: "wrong view"})))})
	if _, found := model.Snapshot(); found {
		t.Fatal("mismatched view identity installed a snapshot")
	}
}

func TestSupersededReadIsCancelled(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	model := NewModel(Options{Callbacks: Callbacks{LoadLocal: func(ctx context.Context, request Request) Response {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return Failure(request, ctx.Err())
	}}})
	command := model.callbackCommand(requestLocal, model.local)
	done := make(chan struct{})
	go func() {
		_ = command()
		close(done)
	}()
	<-started
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = next.(Model)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("superseded callback context was not cancelled")
	}
	<-done
	if model.local == nil {
		t.Fatal("refresh did not schedule replacement read")
	}
}

func TestWorkspaceIdentityChangeCancelsAndRejectsPriorResponse(t *testing.T) {
	model := NewModel(Options{Workspace: "one", Now: func() time.Time { return testObservedAt }})
	prior := *model.local
	model = updateModel(t, model, WorkspaceChangedMsg{Workspace: "two"})
	if model.Identity().Workspace() != "two" {
		t.Fatalf("workspace identity = %q, want two", model.Identity().Workspace())
	}
	model = updateModel(t, model, LocalResponseMsg{Response: Success(prior, snapshotWithRows("one", ViewWorkflow, NewRow(RowSpec{Title: "old"})))})
	if _, found := model.Snapshot(); found {
		t.Fatal("response for prior workspace installed a snapshot")
	}
	current := *model.local
	model = updateModel(t, model, LocalResponseMsg{Response: Success(current, snapshotWithRows("two", ViewWorkflow, NewRow(RowSpec{Title: "new"})))})
	if !strings.Contains(model.View(), "new") {
		t.Fatalf("current workspace response was not rendered:\n%s", model.View())
	}
}

func TestValidEmptyRefreshReplacesPriorRows(t *testing.T) {
	model := loadedModel(t, ViewTries, NewRow(RowSpec{ID: "try-1", Title: "old Try", State: "open"}))
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = next.(Model)
	request := *model.local
	empty := NewSnapshot("workspace", testObservedAt.Add(time.Minute), map[ViewID][]Section{
		ViewTries: {NewSection("Open Tries", "No open Tries.")},
	}, false, "")
	model = updateModel(t, model, LocalResponseMsg{Response: Success(request, empty)})
	view := model.View()
	if strings.Contains(view, "old Try") {
		t.Fatalf("valid empty result retained prior row:\n%s", view)
	}
	for _, wanted := range []string{"[EMPTY]", "No open Tries."} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("empty frame omitted %q:\n%s", wanted, view)
		}
	}
}

func TestFailedRefreshRetainsRowsWithStalePartialBadge(t *testing.T) {
	model := loadedModel(t, ViewWorkflow, NewRow(RowSpec{ID: "row", Title: "retained", State: "ready"}))
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = next.(Model)
	request := *model.local
	model = updateModel(t, model, LocalResponseMsg{Response: Failure(request, errors.New("temporary read failure"))})
	view := model.View()
	for _, wanted := range []string{"retained", "[STALE]", "[PARTIAL]", "temporary read failure"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("failed-refresh frame omitted %q:\n%s", wanted, view)
		}
	}
}

func TestAsynchronousFailureRedactsCredentialsAndBoundsDiagnostic(t *testing.T) {
	const canary = "TUI_ASYNC_CANARY_9b47"
	model := NewModel(Options{Monochrome: true, Now: func() time.Time { return testObservedAt }})
	request := *model.local
	message := "/tmp/api_token=" + canary + " " + strings.Repeat("x", 20<<10)
	model = updateModel(t, model, LocalResponseMsg{Response: Failure(request, errors.New(message))})
	view := model.View()
	if strings.Contains(view, canary) || !strings.Contains(view, "[REDACTED]") {
		t.Fatalf("asynchronous failure leaked credential-shaped material: %q", view)
	}
	if len(model.statuses[ViewWorkflow].err) > 16<<10 {
		t.Fatalf("stored asynchronous diagnostic is not byte-bounded: %d", len(model.statuses[ViewWorkflow].err))
	}
}

func TestTabSwitchDuringLocalRefreshReschedulesAndClearsLoading(t *testing.T) {
	model := loadedModel(t, ViewWorkflow, NewRow(RowSpec{ID: "workflow-old", Title: "retained workflow"}))
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	cancelled := *model.local
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyTab})
	if model.Identity().View() != ViewWorkspace || model.local == nil || !model.loadingAny() {
		t.Fatalf("tab switch abandoned local refresh: identity=%#v local=%#v loading=%t", model.Identity(), model.local, model.loadingAny())
	}
	if model.statuses[ViewWorkspace].state != stateLoading {
		t.Fatalf("new tab status = %v, want loading", model.statuses[ViewWorkspace].state)
	}

	model = updateModel(t, model, LocalResponseMsg{Response: Success(cancelled, snapshotWithRows("workspace", ViewWorkspace, NewRow(RowSpec{Title: "cancelled stale"})))})
	if strings.Contains(model.View(), "cancelled stale") {
		t.Fatal("cancelled refresh response crossed the generation fence")
	}
	current := *model.local
	fresh := NewSnapshot("workspace", testObservedAt.Add(time.Minute), map[ViewID][]Section{
		ViewWorkflow:  {NewSection("Workflow", "Empty", NewRow(RowSpec{Title: "fresh workflow"}))},
		ViewWorkspace: {NewSection("Workspace", "Empty", NewRow(RowSpec{Title: "fresh workspace"}))},
	}, false, "")
	model = updateModel(t, model, LocalResponseMsg{Response: Success(current, fresh)})
	view := model.View()
	if model.loadingAny() || strings.Contains(view, "[REFRESHING]") || !strings.Contains(view, "fresh workspace") {
		t.Fatalf("replacement refresh did not settle the new tab:\n%s", view)
	}
}

func TestProviderProbeIsLazyUntilExplicitReadinessTabAction(t *testing.T) {
	var localCalls, probeCalls atomic.Int32
	model := NewModel(Options{
		Now: func() time.Time { return testObservedAt },
		Callbacks: Callbacks{
			LoadLocal: func(_ context.Context, request Request) Response {
				localCalls.Add(1)
				return Success(request, NewSnapshot("workspace", testObservedAt, map[ViewID][]Section{
					ViewWorkflow:  {NewSection("Context", "No context.", NewRow(RowSpec{Title: "context"}))},
					ViewReadiness: {NewSection("Providers", "No providers.", NewRow(RowSpec{Title: "tool", State: "unprobed"}))},
				}, false, ""))
			},
			ProbeLive: func(_ context.Context, request Request) Response {
				probeCalls.Add(1)
				return Success(request, snapshotWithRows("", ViewReadiness, NewRow(RowSpec{Title: "tool", State: "ready"})))
			},
		},
	})
	for _, message := range executeCommand(model.Init()) {
		if response, ok := message.(LocalResponseMsg); ok {
			model = updateModel(t, model, response)
		}
	}
	if localCalls.Load() != 1 || probeCalls.Load() != 0 {
		t.Fatalf("startup calls local=%d probe=%d, want 1/0", localCalls.Load(), probeCalls.Load())
	}
	next, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	model = next.(Model)
	if probeCalls.Load() != 0 {
		t.Fatal("tab Update invoked probe synchronously")
	}
	for _, message := range executeCommand(command) {
		if response, ok := message.(ProbeResponseMsg); ok {
			model = updateModel(t, model, response)
		}
	}
	if probeCalls.Load() != 1 {
		t.Fatalf("explicit readiness tab action invoked probe %d time(s), want 1", probeCalls.Load())
	}
	if !strings.Contains(model.View(), "ready") {
		t.Fatalf("probe result not rendered:\n%s", model.View())
	}
}

func TestReadinessMergeKeepsMetadataLocalToEachView(t *testing.T) {
	localObserved := testObservedAt
	probeObserved := testObservedAt.Add(5 * time.Minute)
	model := NewModel(Options{Monochrome: true, Now: func() time.Time { return localObserved }})
	localRequest := *model.local
	local := NewSnapshot("workspace", localObserved, map[ViewID][]Section{
		ViewWorkflow:  {NewSection("Workflow", "Empty", NewRow(RowSpec{Title: "local workflow"}))},
		ViewReadiness: {NewSection("Readiness", "Empty", NewRow(RowSpec{Title: "unprobed tool"}))},
	}, false, "local canonical note")
	model = updateModel(t, model, LocalResponseMsg{Response: Success(localRequest, local)})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	if model.probe == nil {
		t.Fatal("readiness tab did not schedule its explicit probe")
	}
	probeRequest := *model.probe
	probe := NewSnapshot("", probeObserved, map[ViewID][]Section{
		ViewReadiness: {NewSection("Readiness", "Empty", NewRow(RowSpec{Title: "probed tool"}))},
	}, true, "live probe note")
	model = updateModel(t, model, ProbeResponseMsg{Response: Success(probeRequest, probe)})
	readiness := model.View()
	for _, wanted := range []string{"probed tool", "[PARTIAL]", formatObserved(probeObserved), "live probe note"} {
		if !strings.Contains(readiness, wanted) {
			t.Fatalf("readiness frame omitted %q:\n%s", wanted, readiness)
		}
	}

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	workflow := model.View()
	for _, wanted := range []string{"local workflow", formatObserved(localObserved), "local canonical note"} {
		if !strings.Contains(workflow, wanted) {
			t.Fatalf("workflow frame omitted %q after readiness merge:\n%s", wanted, workflow)
		}
	}
	for _, unwanted := range []string{"[PARTIAL]", formatObserved(probeObserved), "live probe note"} {
		if strings.Contains(workflow, unwanted) {
			t.Fatalf("readiness metadata %q leaked into workflow frame:\n%s", unwanted, workflow)
		}
	}

	refreshedObserved := testObservedAt.Add(10 * time.Minute)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	refreshRequest := *model.local
	refreshed := NewSnapshot("workspace", refreshedObserved, map[ViewID][]Section{
		ViewWorkflow:  {NewSection("Workflow", "Empty", NewRow(RowSpec{Title: "refreshed workflow"}))},
		ViewReadiness: {NewSection("Readiness", "Empty", NewRow(RowSpec{Title: "replacement unprobed tool"}))},
	}, false, "refreshed local note")
	model = updateModel(t, model, LocalResponseMsg{Response: Success(refreshRequest, refreshed)})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	readiness = model.View()
	for _, wanted := range []string{"probed tool", "[PARTIAL]", formatObserved(probeObserved), "live probe note"} {
		if !strings.Contains(readiness, wanted) {
			t.Fatalf("retained readiness frame omitted %q after local refresh:\n%s", wanted, readiness)
		}
	}
	for _, unwanted := range []string{"replacement unprobed tool", formatObserved(refreshedObserved), "refreshed local note"} {
		if strings.Contains(readiness, unwanted) {
			t.Fatalf("local refresh metadata %q replaced retained readiness metadata:\n%s", unwanted, readiness)
		}
	}
}

func TestAcceptedLocalSnapshotAppliesResolvedPresentationMode(t *testing.T) {
	model := NewModel(Options{Monochrome: true, Now: func() time.Time { return testObservedAt }})
	request := *model.local
	snapshot := snapshotWithRows("workspace", ViewWorkflow, NewRow(RowSpec{Title: "styled row", State: "ready"})).WithMonochrome(false)
	model = updateModel(t, model, LocalResponseMsg{Response: Success(request, snapshot)})
	if model.monochrome {
		t.Fatalf("accepted local presentation policy was not applied:\n%s", model.View())
	}
}

func TestMissingToolsRemainVisibleWithReasonAndRemediation(t *testing.T) {
	model := loadedModel(t, ViewReadiness, NewRow(RowSpec{
		ID: "mlflow", Title: "mlflow", State: "missing", Reason: "binary-not-found",
		Remediation: "Install mlflow outside exp ui, then press r.",
	}))
	view := model.View()
	for _, wanted := range []string{"mlflow", "missing", "binary-not-found", "Install mlflow"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("missing-tool frame omitted %q:\n%s", wanted, view)
		}
	}
}

func TestResizeNarrowCJKMonochromeAndSelectionWindow(t *testing.T) {
	rows := make([]Row, 18)
	for index := range rows {
		rows[index] = NewRow(RowSpec{
			ID: "候補-識別子", Title: "実験候補資料", State: "running",
			Detail: "中文寬度與日本語の表示を確認する項目", Reason: "read-only observation",
		})
	}
	model := NewModel(Options{InitialView: ViewCandidates, Monochrome: true, Now: func() time.Time { return testObservedAt }})
	request := *model.local
	model = updateModel(t, model, LocalResponseMsg{Response: Success(request, NewSnapshot("研究工作區", testObservedAt, map[ViewID][]Section{
		ViewCandidates: {NewSection("候補", "空です", rows...)},
	}, false, ""))})
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 28, Height: 36})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	view := model.View()
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("monochrome frame contains ANSI escapes: %q", view)
	}
	for _, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if width := ansi.StringWidth(line); width > 28 {
			t.Fatalf("line width = %d, want <= 28: %q", width, line)
		}
	}
	if !strings.Contains(view, "↑") {
		t.Fatalf("selection did not move viewport window:\n%s", view)
	}
	if !strings.Contains(view, "実験候補") && !strings.Contains(view, "中文寬度") {
		t.Fatalf("narrow CJK row is not visible:\n%s", view)
	}
	if !strings.Contains(view, "> ") {
		t.Fatalf("selected row was truncated out of the measured viewport:\n%s", view)
	}
}

func TestQuestionMarkHelpAndCallbackFreeQuit(t *testing.T) {
	var calls atomic.Int32
	model := NewModel(Options{Callbacks: Callbacks{
		LoadLocal: func(_ context.Context, request Request) Response {
			calls.Add(1)
			return Failure(request, errors.New("must not run"))
		},
		ProbeLive: func(_ context.Context, request Request) Response {
			calls.Add(1)
			return Failure(request, errors.New("must not run"))
		},
	}})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !strings.Contains(model.View(), "No key mutates records") {
		t.Fatalf("help view omitted read-only guarantee:\n%s", model.View())
	}
	next, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = next.(Model)
	message := command()
	if _, ok := message.(tea.QuitMsg); !ok {
		t.Fatalf("q command returned %T, want tea.QuitMsg", message)
	}
	if calls.Load() != 0 {
		t.Fatalf("help/quit invoked %d callback(s)", calls.Load())
	}
}

func TestSnapshotsAndSectionsAreDefensiveCopies(t *testing.T) {
	rows := []Row{NewRow(RowSpec{ID: "original", Title: "Original"})}
	sections := []Section{NewSection("Rows", "Empty", rows...)}
	input := map[ViewID][]Section{ViewWorkflow: sections}
	snapshot := NewSnapshot("workspace", testObservedAt, input, false, "note")
	rows[0] = NewRow(RowSpec{ID: "mutated", Title: "Mutated"})
	sections[0] = NewSection("Changed", "Changed")
	input[ViewWorkflow] = nil
	if got := snapshot.Sections(ViewWorkflow)[0].Rows()[0].ID(); got != "original" {
		t.Fatalf("constructor retained mutable input: %q", got)
	}

	returned := snapshot.Sections(ViewWorkflow)
	returned[0].rows[0] = NewRow(RowSpec{ID: "getter-mutated"})
	if got := snapshot.Sections(ViewWorkflow)[0].Rows()[0].ID(); got != "original" {
		t.Fatalf("Sections exposed mutable storage: %q", got)
	}

	model := NewModel(Options{Now: func() time.Time { return testObservedAt }})
	request := *model.local
	model = updateModel(t, model, LocalResponseMsg{Response: Success(request, snapshot)})
	copy, ok := model.Snapshot()
	if !ok {
		t.Fatal("model has no accepted snapshot")
	}
	copy.sections[ViewWorkflow][0].rows[0] = NewRow(RowSpec{ID: "model-mutated"})
	again, _ := model.Snapshot()
	if got := again.Sections(ViewWorkflow)[0].Rows()[0].ID(); got != "original" {
		t.Fatalf("Model.Snapshot exposed mutable storage: %q", got)
	}
}

func loadedModel(t *testing.T, view ViewID, rows ...Row) Model {
	t.Helper()
	model := NewModel(Options{Workspace: "workspace", InitialView: view, Monochrome: true, Now: func() time.Time { return testObservedAt }})
	request := *model.local
	snapshot := NewSnapshot("workspace", testObservedAt, map[ViewID][]Section{
		view: {NewSection(ViewTitle(view), "No entries.", rows...)},
	}, false, "")
	return updateModel(t, model, LocalResponseMsg{Response: Success(request, snapshot)})
}

func snapshotWithRows(workspace string, view ViewID, rows ...Row) Snapshot {
	return NewSnapshot(workspace, testObservedAt, map[ViewID][]Section{
		view: {NewSection(ViewTitle(view), "No entries.", rows...)},
	}, false, "")
}

func updateModel(t *testing.T, model Model, message tea.Msg) Model {
	t.Helper()
	next, _ := model.Update(message)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", next)
	}
	return updated
}

func executeCommand(command tea.Cmd) []tea.Msg {
	if command == nil {
		return nil
	}
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{message}
	}
	messages := make([]tea.Msg, 0, len(batch))
	for _, child := range batch {
		messages = append(messages, executeCommand(child)...)
	}
	return messages
}
