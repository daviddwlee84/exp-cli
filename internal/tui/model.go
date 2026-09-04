package tui

import (
	"context"
	"errors"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/daviddwlee84/exp-cli/internal/safex"
)

// Options configures the pure TUI model and its injected read boundaries.
type Options struct {
	Context     context.Context
	Workspace   string
	InitialView ViewID
	Monochrome  bool
	Now         func() time.Time
	Callbacks   Callbacks
}

type loadState uint8

const (
	stateIdle loadState = iota
	stateLoading
	stateReady
	stateEmpty
	stateError
	stateStale
)

const maxTUIDiagnosticBytes = 16 << 10

func safeTUIDiagnostic(value string) string {
	message, _ := safex.NewRedactor().SafeDiagnostic(value, maxTUIDiagnosticBytes)
	return safeText(message)
}

type viewStatus struct {
	state   loadState
	partial bool
	err     string
}

type requestKind uint8

const (
	requestLocal requestKind = iota + 1
	requestProbe
)

// LocalResponseMsg and ProbeResponseMsg are exported to permit deterministic
// model tests and alternative Bubble Tea runtimes without exposing mutable data.
type LocalResponseMsg struct{ Response Response }
type ProbeResponseMsg struct{ Response Response }

// WorkspaceChangedMsg invalidates in-flight reads and starts a fresh local read
// for a new exact selector. The CLI does not synthesize this message today, but
// it keeps identity-change behavior explicit and testable.
type WorkspaceChangedMsg struct{ Workspace string }

// Model is a Bubble Tea model containing presentation state only.
type Model struct {
	baseContext context.Context
	now         func() time.Time
	callbacks   Callbacks
	monochrome  bool

	identity   Identity
	generation uint64
	requestCtx context.Context
	cancel     context.CancelFunc
	local      *Request
	probe      *Request

	snapshot Snapshot
	hasLocal bool
	hasProbe bool
	statuses map[ViewID]viewStatus

	width     int
	height    int
	selection int
	offset    int
	showHelp  bool
	quitting  bool
	spinner   spinner.Model
}

// NewModel creates the immediate loading frame without invoking any callback.
// LoadLocal is first reachable only through the command returned by Init.
func NewModel(options Options) Model {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	view := options.InitialView
	if !validView(view) {
		view = ViewWorkflow
	}
	spin := spinner.New()
	spin.Spinner = spinner.MiniDot
	model := Model{
		baseContext: ctx,
		now:         now,
		callbacks:   options.Callbacks,
		monochrome:  options.Monochrome,
		identity:    NewIdentity(options.Workspace, view),
		generation:  1,
		statuses:    make(map[ViewID]viewStatus, len(orderedViews)),
		width:       80,
		height:      24,
		spinner:     spin,
	}
	for _, candidate := range orderedViews {
		model.statuses[candidate] = viewStatus{state: stateIdle}
	}
	model.requestCtx, model.cancel = context.WithCancel(ctx)
	request := NewRequest(model.generation, model.identity, model.observedNow())
	model.local = &request
	model.statuses[view] = viewStatus{state: stateLoading}
	return model
}

func (model Model) Init() tea.Cmd {
	return tea.Batch(model.spinner.Tick, model.callbackCommand(requestLocal, model.local))
}

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	model = model.cloneForUpdate()
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		model.width = typed.Width
		model.height = typed.Height
		model.normalizeWindow()
		model.ensureSelectionVisible()
		return model, nil
	case tea.KeyMsg:
		return model.updateKey(typed)
	case LocalResponseMsg:
		model.applyResponse(requestLocal, typed.Response)
		return model, nil
	case ProbeResponseMsg:
		model.applyResponse(requestProbe, typed.Response)
		return model, nil
	case WorkspaceChangedMsg:
		workspace := safeText(typed.Workspace)
		if workspace == "" {
			workspace = "current"
		}
		if workspace == model.identity.workspace {
			return model, nil
		}
		model.identity = NewIdentity(workspace, model.identity.view)
		model.snapshot = Snapshot{}
		model.hasLocal, model.hasProbe = false, false
		model.selection, model.offset = 0, 0
		return model.beginReads(true, false)
	case spinner.TickMsg:
		if !model.loadingAny() {
			return model, nil
		}
		var command tea.Cmd
		model.spinner, command = model.spinner.Update(typed)
		return model, command
	default:
		return model, nil
	}
}

func (model Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		model.stopReads()
		model.quitting = true
		return model, tea.Quit
	case "?":
		model.showHelp = !model.showHelp
		return model, nil
	case "tab", "right", "l":
		return model.changeView(1)
	case "shift+tab", "left", "h":
		return model.changeView(-1)
	case "1", "2", "3", "4", "5", "6", "7":
		index := int(key.Runes[0] - '1')
		return model.selectView(orderedViews[index])
	case "up", "k":
		model.moveSelection(-1)
		return model, nil
	case "down", "j":
		model.moveSelection(1)
		return model, nil
	case "pgup":
		model.moveSelection(-model.pageRows())
		return model, nil
	case "pgdown":
		model.moveSelection(model.pageRows())
		return model, nil
	case "home", "g":
		model.selection = 0
		model.ensureSelectionVisible()
		return model, nil
	case "end", "G":
		model.selection = max(0, model.currentRowCount()-1)
		model.ensureSelectionVisible()
		return model, nil
	case "r":
		if model.identity.view == ViewReadiness {
			return model.beginReads(false, true)
		}
		return model.beginReads(true, false)
	default:
		return model, nil
	}
}

func (model Model) changeView(delta int) (tea.Model, tea.Cmd) {
	index := 0
	for candidate, view := range orderedViews {
		if view == model.identity.view {
			index = candidate
			break
		}
	}
	index = (index + delta + len(orderedViews)) % len(orderedViews)
	return model.selectView(orderedViews[index])
}

func (model Model) selectView(view ViewID) (tea.Model, tea.Cmd) {
	if !validView(view) || view == model.identity.view {
		return model, nil
	}
	localPending := model.local != nil
	model.stopReads()
	model.generation++
	model.identity = NewIdentity(model.identity.workspace, view)
	model.selection, model.offset = 0, 0
	if localPending || !model.hasLocal {
		return model.scheduleReads(true, view == ViewReadiness)
	}
	if view == ViewReadiness {
		return model.scheduleReads(false, true)
	}
	return model, nil
}

func (model Model) beginReads(local, probe bool) (tea.Model, tea.Cmd) {
	model.stopReads()
	model.generation++
	return model.scheduleReads(local, probe)
}

func (model Model) scheduleReads(local, probe bool) (tea.Model, tea.Cmd) {
	if !local && !probe {
		return model, nil
	}
	model.requestCtx, model.cancel = context.WithCancel(model.baseContext)
	request := NewRequest(model.generation, model.identity, model.observedNow())
	commands := make([]tea.Cmd, 0, 3)
	if local {
		copy := request
		model.local = &copy
		model.markLocalLoading()
		commands = append(commands, model.callbackCommand(requestLocal, model.local))
	}
	if probe {
		copy := request
		model.probe = &copy
		model.statuses[ViewReadiness] = viewStatus{state: stateLoading}
		commands = append(commands, model.callbackCommand(requestProbe, model.probe))
	}
	commands = append(commands, model.spinner.Tick)
	return model, tea.Batch(commands...)
}

func (model Model) callbackCommand(kind requestKind, pending *Request) tea.Cmd {
	if pending == nil {
		return nil
	}
	request := *pending
	ctx := model.requestCtx
	callback := model.callbacks.LoadLocal
	if kind == requestProbe {
		callback = model.callbacks.ProbeLive
	}
	return func() tea.Msg {
		var response Response
		if callback == nil {
			response = Failure(request, errors.New("read callback is unavailable"))
		} else {
			response = callback(ctx, request)
		}
		if kind == requestProbe {
			return ProbeResponseMsg{Response: response}
		}
		return LocalResponseMsg{Response: response}
	}
}

func (model *Model) applyResponse(kind requestKind, response Response) {
	pending := model.local
	if kind == requestProbe {
		pending = model.probe
	}
	if pending == nil || !sameRequest(*pending, response.request) ||
		response.request.generation != model.generation || response.request.identity != model.identity {
		return
	}
	if kind == requestProbe {
		model.probe = nil
	} else {
		model.local = nil
	}
	if response.err != nil {
		model.applyFailure(kind, response.err)
	} else {
		model.applySuccess(kind, response.snapshot)
	}
	model.finishReadIfIdle()
	model.clampSelection()
}

func (model *Model) applySuccess(kind requestKind, incoming Snapshot) {
	incoming = incoming.clone()
	if kind == requestLocal && incoming.hasMonochrome {
		model.monochrome = incoming.monochrome
	}
	if kind == requestProbe {
		metadata := incoming.metadataFor(ViewReadiness)
		sections := incoming.Sections(ViewReadiness)
		if !model.hasLocal && !model.hasProbe {
			model.snapshot = incoming
		} else {
			model.snapshot = model.snapshot.withView(ViewReadiness, sections, metadata.observedAt, metadata.partial, metadata.note)
		}
		model.hasProbe = true
		state := stateReady
		if model.snapshot.rowCount(ViewReadiness) == 0 {
			state = stateEmpty
		}
		model.statuses[ViewReadiness] = viewStatus{state: state, partial: metadata.partial}
		return
	}

	var readiness []Section
	var readinessMetadata viewMetadata
	if model.hasProbe {
		readiness = model.snapshot.Sections(ViewReadiness)
		readinessMetadata = model.snapshot.metadataFor(ViewReadiness)
	}
	model.snapshot = incoming
	if model.hasProbe {
		model.snapshot = model.snapshot.withView(ViewReadiness, readiness, readinessMetadata.observedAt, readinessMetadata.partial, readinessMetadata.note)
	}
	model.hasLocal = true
	for _, view := range orderedViews {
		if view == ViewReadiness && model.hasProbe {
			continue
		}
		state := stateReady
		if model.snapshot.rowCount(view) == 0 {
			state = stateEmpty
		}
		model.statuses[view] = viewStatus{state: state, partial: model.snapshot.metadataFor(view).partial}
	}
	if model.probe != nil {
		model.statuses[ViewReadiness] = viewStatus{state: stateLoading}
	}
}

func (model *Model) applyFailure(kind requestKind, err error) {
	message := safeTUIDiagnostic(err.Error())
	if message == "" {
		message = "read failed"
	}
	if kind == requestProbe {
		state := stateError
		if model.hasProbe || model.hasLocal && model.snapshot.rowCount(ViewReadiness) > 0 {
			state = stateStale
		}
		model.statuses[ViewReadiness] = viewStatus{state: state, partial: true, err: message}
		return
	}
	for _, view := range orderedViews {
		if view == ViewReadiness && model.hasProbe {
			continue
		}
		state := stateError
		if model.hasLocal {
			state = stateStale
		}
		model.statuses[view] = viewStatus{state: state, partial: true, err: message}
	}
}

func (model *Model) markLocalLoading() {
	for _, view := range orderedViews {
		if view == ViewReadiness && model.hasProbe {
			continue
		}
		model.statuses[view] = viewStatus{state: stateLoading}
	}
}

func (model *Model) finishReadIfIdle() {
	if model.local != nil || model.probe != nil {
		return
	}
	if model.cancel != nil {
		model.cancel()
	}
	model.cancel = nil
	model.requestCtx = nil
}

func (model *Model) stopReads() {
	if model.cancel != nil {
		model.cancel()
	}
	model.cancel = nil
	model.requestCtx = nil
	model.local = nil
	model.probe = nil
}

func (model Model) observedNow() time.Time {
	observed := model.now().UTC()
	if observed.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return observed
}

func sameRequest(left, right Request) bool {
	return left.generation == right.generation && left.identity == right.identity && left.observedAt.Equal(right.observedAt)
}

func (model Model) cloneForUpdate() Model {
	statuses := make(map[ViewID]viewStatus, len(model.statuses))
	for view, status := range model.statuses {
		statuses[view] = status
	}
	model.statuses = statuses
	return model
}

func (model Model) loadingAny() bool { return model.local != nil || model.probe != nil }

func (model *Model) normalizeWindow() {
	if model.width < 1 {
		model.width = 1
	}
	if model.height < 1 {
		model.height = 1
	}
}

func (model Model) currentRowCount() int {
	if !model.hasLocal && !model.hasProbe {
		return 0
	}
	return model.snapshot.rowCount(model.identity.view)
}

func (model *Model) moveSelection(delta int) {
	model.selection += delta
	model.clampSelection()
}

func (model *Model) clampSelection() {
	count := model.currentRowCount()
	if count == 0 {
		model.selection, model.offset = 0, 0
		return
	}
	if model.selection < 0 {
		model.selection = 0
	}
	if model.selection >= count {
		model.selection = count - 1
	}
	model.ensureSelectionVisible()
}

func (model *Model) ensureSelectionVisible() {
	first, _ := model.visibleRowRange()
	model.offset = first
}

func (model Model) pageRows() int {
	first, last := model.visibleRowRange()
	return max(1, last-first)
}

func (model Model) visibleRowRange() (int, int) {
	count := model.currentRowCount()
	if count == 0 {
		return 0, 0
	}
	first := model.offset
	if first < 0 {
		first = 0
	}
	if first >= count {
		first = count - 1
	}
	if model.selection < first {
		first = model.selection
	}
	budget := model.rowLineBudget()
	last := first
	for last < count {
		candidate := last + 1
		if model.renderedRangeLineCount(first, candidate) > budget && last > first {
			break
		}
		last = candidate
		if model.renderedRangeLineCount(first, last) > budget {
			break
		}
	}
	if model.selection < last {
		return first, last
	}

	first = model.selection
	last = model.selection + 1
	for first > 0 && model.renderedRangeLineCount(first-1, last) <= budget {
		first--
	}
	for last < count && model.renderedRangeLineCount(first, last+1) <= budget {
		last++
	}
	return first, last
}

func (model Model) rowLineBudget() int {
	width := max(1, model.width)
	height := max(1, model.height)
	style := newTheme(model.monochrome)
	headerLines := 3 + len(packTokens(model.tabTokens(style), width))
	footerTokens := []string{"tab/←/→ views", "j/k move", "r refresh", "? help", "q/Ctrl-C quit"}
	if model.showHelp {
		footerTokens = []string{"? close help", "q/Ctrl-C quit"}
	}
	footerLines := len(packTokens(footerTokens, width))
	if footerLines == 0 {
		footerLines = 1
	}
	budget := height - headerLines - footerLines
	status := model.statuses[model.identity.view]
	if status.state == stateLoading {
		budget--
	}
	if status.err != "" {
		budget--
	}
	return max(1, budget)
}

func (model Model) renderedRangeLineCount(first, last int) int {
	if first < 0 || last <= first {
		return 0
	}
	width := max(1, model.width)
	style := newTheme(model.monochrome)
	lines := 0
	global := 0
	for _, section := range model.snapshot.Sections(model.identity.view) {
		sectionShown := false
		for _, row := range section.Rows() {
			if global >= first && global < last {
				if !sectionShown && section.Title() != "" {
					lines++
					sectionShown = true
				}
				lines += len(renderRow(row, global == model.selection, width, style))
			}
			global++
		}
	}
	if first > 0 {
		lines++
	}
	if last < model.currentRowCount() {
		lines++
	}
	if model.snapshot.metadataFor(model.identity.view).note != "" {
		lines++
	}
	return lines
}

// Generation returns the current stale-response fence.
func (model Model) Generation() uint64 { return model.generation }
func (model Model) Identity() Identity { return model.identity }

// Snapshot returns a deep defensive copy of the currently retained rows.
func (model Model) Snapshot() (Snapshot, bool) {
	if !model.hasLocal && !model.hasProbe {
		return Snapshot{}, false
	}
	return model.snapshot.clone(), true
}
