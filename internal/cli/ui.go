package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/exploration"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/tui"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
	"github.com/spf13/cobra"
)

func newUICommand(app *App, root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the read-only local research TUI",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runUI(command, app, root)
		},
	}
}

func runUI(command *cobra.Command, app *App, root *rootOptions) error {
	if app == nil || root == nil {
		return errors.New("exp ui requires an application and root options")
	}
	if app.IsTUITerminal == nil || !app.IsTUITerminal(app.In, app.Out) {
		return invalidUsagef("exp ui requires real terminal stdin and stdout; use `exp context` or `exp context --json` for noninteractive output")
	}
	start, err := app.startDir(root.startDir)
	if err != nil {
		return err
	}
	identity := uiWorkspaceSelector(start, root)
	selection := *root
	options := tui.Options{
		Context:     command.Context(),
		Workspace:   identity,
		InitialView: tui.ViewWorkflow,
		Monochrome:  !app.outStyle().enabled,
		Now:         app.clock,
		Callbacks: tui.Callbacks{
			LoadLocal: func(ctx context.Context, request tui.Request) tui.Response {
				snapshot, loadErr := loadUILocalSnapshot(ctx, app, &selection)
				if loadErr != nil {
					return tui.Failure(request, loadErr)
				}
				return tui.Success(request, snapshot)
			},
			ProbeLive: func(ctx context.Context, request tui.Request) tui.Response {
				snapshot, probeErr := loadUILiveReadiness(ctx, app, &selection)
				if probeErr != nil {
					return tui.Failure(request, probeErr)
				}
				return tui.Success(request, snapshot)
			},
		},
	}
	return app.RunTUI(command.Context(), app.In, app.Out, options)
}

func uiWorkspaceSelector(start string, root *rootOptions) string {
	redactor := safex.NewRedactor()
	if root != nil {
		if value := strings.TrimSpace(root.workspace); value != "" {
			safe, _ := redactor.SafeDiagnostic(redactor.Path(value), maxCLIDiagnosticBytes)
			return "workspace=" + safe
		}
		if value := strings.TrimSpace(root.source); value != "" {
			safe, _ := redactor.SafeDiagnostic(value, maxCLIDiagnosticBytes)
			return "source=" + safe
		}
	}
	safe, _ := redactor.SafeDiagnostic(redactor.Path(start), maxCLIDiagnosticBytes)
	return "path=" + safe
}

func resolveUISnapshot(ctx context.Context, app *App, root *rootOptions) (*workspace.Context, *record.Inventory, error) {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return nil, nil, err
	}
	request := workspace.ResolveRequest{
		InvocationDir: start,
		Workspace:     strings.TrimSpace(root.workspace),
		Source:        strings.TrimSpace(root.source),
	}
	var resolved *workspace.Context
	var inventory *record.Inventory
	if resolver, ok := app.ResolveWorkspace.(WorkspaceSnapshotResolver); ok {
		resolved, inventory, err = resolver.ResolveSnapshot(ctx, request)
	} else {
		resolved, err = app.ResolveWorkspace.Resolve(ctx, request)
		if err == nil && resolved != nil && resolved.Project != nil {
			var store RecordStore
			store, err = app.NewStore(resolved.Project)
			if err == nil {
				inventory, err = store.Inventory(ctx)
			}
		}
	}
	if err != nil {
		return nil, nil, err
	}
	if resolved == nil || resolved.Project == nil {
		return nil, nil, errors.New("workspace resolver returned no canonical Project")
	}
	if err := app.applyEffectiveColor(resolved.Config); err != nil {
		return nil, nil, err
	}
	return resolved, inventory, nil
}

type uiOperationSummary struct {
	initialized bool
	runtime     operation.RuntimeState
	jobs        []operation.JobSummary
	partial     bool
	reason      string
}

func readUIOperationSummary(ctx context.Context, app *App, resolved *workspace.Context) uiOperationSummary {
	if resolved == nil || resolved.Project == nil {
		return uiOperationSummary{partial: true, reason: "canonical workspace is unavailable"}
	}
	path, err := operation.PathFor(resolved.Project.Repository.GitCommonDir)
	if errors.Is(err, operation.ErrUnsupported) {
		return uiOperationSummary{reason: "operation summaries are unavailable on this platform"}
	}
	if err != nil {
		return uiOperationSummary{partial: true, reason: "operation database path could not be resolved"}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return uiOperationSummary{jobs: []operation.JobSummary{}}
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return uiOperationSummary{partial: true, reason: "operation database could not be safely inspected"}
	}
	store, err := app.OpenOperationalReadOnly(ctx, resolved.Project)
	if err != nil {
		return uiOperationSummary{partial: true, reason: "operation database could not be opened read-only"}
	}
	defer store.Close()
	summary := uiOperationSummary{initialized: true, jobs: []operation.JobSummary{}}
	summary.runtime, err = store.RuntimeState(ctx)
	if err != nil {
		summary.partial = true
		summary.reason = "operation runtime state could not be read"
		return summary
	}
	summary.jobs, err = store.ListJobSummaries(ctx)
	if err != nil {
		summary.jobs = []operation.JobSummary{}
		summary.partial = true
		summary.reason = "operation jobs could not be read"
	}
	return summary
}

func loadUILocalSnapshot(ctx context.Context, app *App, root *rootOptions) (tui.Snapshot, error) {
	resolved, inventory, err := resolveUISnapshot(ctx, app, root)
	if err != nil {
		return tui.Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return tui.Snapshot{}, err
	}
	if resolved == nil || resolved.Project == nil || inventory == nil {
		return tui.Snapshot{}, errors.New("resolved workspace snapshot is incomplete")
	}
	projectView, err := makeProjectView(resolved.Project)
	if err != nil {
		return tui.Snapshot{}, err
	}
	workspaceView, selectedSource, associationView, configView, err := makeWorkspaceContextViews(resolved, inventory)
	if err != nil {
		return tui.Snapshot{}, err
	}
	operationSummary := readUIOperationSummary(ctx, app, resolved)
	if err := ctx.Err(); err != nil {
		return tui.Snapshot{}, err
	}

	openTries, recentTries, attemptCounts := uiTryRows(inventory)
	frontiers := uiFrontierRows(inventory)
	attempts := uiAttemptRows(inventory, operationSummary.jobs, app.clock())
	candidates, champions, championPartial := uiCandidateRows(inventory)
	workspaceSections := uiWorkspaceSections(resolved, inventory, projectView, workspaceView, selectedSource, associationView, configView, operationSummary)
	sections := map[tui.ViewID][]tui.Section{
		tui.ViewWorkflow:  uiWorkflowSections(inventory, projectView, selectedSource, configView, operationSummary, len(openTries), len(frontiers), len(attempts), len(candidates), len(champions)),
		tui.ViewWorkspace: workspaceSections,
		tui.ViewTries: {
			tui.NewSection("Open Tries", "No open Tries.", openTries...),
			tui.NewSection("Recent completed Tries", "No completed Try history.", recentTries...),
			tui.NewSection("Saved results", "Use try exec to produce managed artifacts.", uiArtifactRows(inventory)...),
		},
		tui.ViewQueue: {
			tui.NewSection("Canonical queue frontiers", "No dispatchable queue frontiers.", frontiers...),
		},
		tui.ViewAttempts: {
			tui.NewSection("Active and recoverable Attempts", "No active or recoverable Attempts.", attempts...),
		},
		tui.ViewCandidates: {
			tui.NewSection("Candidates", "No canonical Candidates.", candidates...),
			tui.NewSection("Champions", "No Champions derived from Promotion chains.", champions...),
		},
		tui.ViewReadiness: uiUnprobedReadinessSections(app),
	}
	_ = attemptCounts
	partial := !inventory.Valid() || !configView.Trusted || operationSummary.partial || championPartial
	note := "Local canonical records remain authoritative; provider observations are never canonical."
	workspaceIdentity := projectView.Name + " (" + projectView.ID + ")"
	return tui.NewSnapshot(workspaceIdentity, app.observedAt(), sections, partial, note).WithMonochrome(!app.outStyle().enabled), nil
}

func uiWorkflowSections(inventory *record.Inventory, project projectView, source *selectedSourceView, config configSummaryView, operations uiOperationSummary, openTries, frontiers, attempts, candidates, champions int) []tui.Section {
	counts := countsFor(inventory)
	state := "valid"
	reason := ""
	if !inventory.Valid() {
		state = "invalid"
		reason = fmt.Sprintf("%d canonical diagnostic(s)", len(inventory.Diagnostics))
	}
	contextRows := []tui.Row{
		tui.NewRow(tui.RowSpec{ID: project.ID, Title: project.Name, State: state, Detail: fmt.Sprintf("%d canonical records", counts.Total), Reason: reason, Remediation: uiInventoryRemediation(inventory)}),
	}
	if source != nil {
		contextRows = append(contextRows, tui.NewRow(tui.RowSpec{
			ID: source.Source.ID, Title: "Selected Source: " + source.Source.Key, State: source.Source.State,
			Detail: source.Source.Title + " · backend " + source.WorkspaceBackendRequested,
		}))
	} else {
		contextRows = append(contextRows, tui.NewRow(tui.RowSpec{Title: "Selected Source", State: "none", Detail: "Canonical workspace context only"}))
	}
	trustState := "trusted"
	if !config.Trusted {
		trustState = "untrusted"
	}
	contextRows = append(contextRows, tui.NewRow(tui.RowSpec{ID: config.Digest, Title: "Configuration", State: trustState, Detail: fmt.Sprintf("%d applied layer(s)", len(config.Layers)), Remediation: uiConfigRemediation(config)}))

	focusRows := []tui.Row{
		tui.NewRow(tui.RowSpec{Title: "Open Tries", State: countState(openTries), Detail: strconv.Itoa(openTries)}),
		tui.NewRow(tui.RowSpec{Title: "Queue frontiers", State: countState(frontiers), Detail: strconv.Itoa(frontiers)}),
		tui.NewRow(tui.RowSpec{Title: "Active/recoverable Attempts", State: countState(attempts), Detail: strconv.Itoa(attempts)}),
		tui.NewRow(tui.RowSpec{Title: "Candidates / Champions", State: countState(candidates + champions), Detail: fmt.Sprintf("%d / %d", candidates, champions)}),
	}
	operationState := "not_initialized"
	operationDetail := "No private operation database; canonical views are still complete."
	if operations.initialized {
		operationState = "ready"
		if operations.runtime.Paused {
			operationState = "paused"
		}
		operationDetail = fmt.Sprintf("%d local job(s)", len(operations.jobs))
	}
	if operations.partial {
		operationState = "partial"
	}
	focusRows = append(focusRows, tui.NewRow(tui.RowSpec{Title: "Local operation summary", State: operationState, Detail: operationDetail, Reason: operations.reason, Remediation: "Use exp daemon status for a noninteractive summary."}))
	return []tui.Section{
		tui.NewSection("Research context", "Canonical context is empty.", contextRows...),
		tui.NewSection("Workflow", "No current workflow rows.", focusRows...),
	}
}

func uiWorkspaceSections(resolved *workspace.Context, inventory *record.Inventory, project projectView, workspaceView workspaceContextView, selected *selectedSourceView, association associationSummaryView, config configSummaryView, operations uiOperationSummary) []tui.Section {
	workspaceState := "resolved"
	if !inventory.Valid() {
		workspaceState = "invalid"
	}
	workspaceRows := []tui.Row{tui.NewRow(tui.RowSpec{
		ID: project.ID, Title: project.Name, State: workspaceState,
		Detail: workspaceView.RepositoryRoot + " · " + workspaceView.Resolution,
		Reason: fmt.Sprintf("project_registered=%t", association.ProjectRegistered),
	})}
	if operations.initialized || operations.reason != "" {
		state := "ready"
		if operations.partial {
			state = "partial"
		} else if operations.runtime.Paused {
			state = "paused"
		}
		workspaceRows = append(workspaceRows, tui.NewRow(tui.RowSpec{Title: "Private operation state", State: state, Detail: fmt.Sprintf("%d job(s)", len(operations.jobs)), Reason: operations.reason}))
	}

	sourceRows := make([]tui.Row, 0)
	views, err := makeSourceViews(resolved.Project, inventory.OfKind(research.KindSource))
	if err == nil {
		for _, view := range views {
			state := view.State
			detail := view.Title
			if view.Subdir != "" {
				detail += " · subdir " + view.Subdir
			}
			title := view.Key
			if selected != nil && selected.Source.ID == view.ID {
				title = "Selected: " + title
				detail += " · resolved " + selected.ResolvedRoot
			}
			sourceRows = append(sourceRows, tui.NewRow(tui.RowSpec{ID: view.ID, Title: title, State: state, Detail: detail}))
		}
	}

	configRows := make([]tui.Row, 0, len(config.Layers))
	for _, layer := range config.Layers {
		state := "trusted"
		if !layer.Trusted {
			state = "untrusted"
		}
		detail := layer.Source
		if len(layer.Fields) > 0 {
			detail += " · " + strings.Join(layer.Fields, ", ")
		}
		configRows = append(configRows, tui.NewRow(tui.RowSpec{
			ID: layer.Digest, Title: string(layer.Kind), State: state, Detail: detail,
			Remediation: uiLayerRemediation(layer.Trusted),
		}))
	}
	return []tui.Section{
		tui.NewSection("Canonical workspace", "No canonical workspace.", workspaceRows...),
		tui.NewSection("Canonical Sources", "No canonical Sources.", sourceRows...),
		tui.NewSection("Configuration layers and trust", "Only built-in configuration is active.", configRows...),
	}
}

func uiTryRows(inventory *record.Inventory) (openRows, recentRows []tui.Row, attemptCounts map[research.ID]int) {
	attemptCounts = make(map[research.ID]int)
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		if !attempt.Try.IsZero() {
			attemptCounts[attempt.Try]++
		}
	}
	tries := append([]*record.Document(nil), inventory.OfKind(research.KindTry)...)
	sort.SliceStable(tries, func(left, right int) bool {
		leftValue := tries[left].Record.(*research.Try)
		rightValue := tries[right].Record.(*research.Try)
		if !leftValue.UpdatedAt.Equal(rightValue.UpdatedAt) {
			return leftValue.UpdatedAt.After(rightValue.UpdatedAt)
		}
		return leftValue.ID.String() < rightValue.ID.String()
	})
	for _, document := range tries {
		view := makeTryRecordDetail(document, inventory)
		description := view.Goal
		if view.Summary != "" {
			description += " · " + view.Summary
		}
		if view.Conclusion != nil {
			description += " · " + view.Conclusion.Summary
		}
		if view.ConclusionAuthor == "agent" {
			description += " · agent observation, unreviewed"
		}
		row := tui.NewRow(tui.RowSpec{
			ID: view.ID, Title: view.Display + " " + view.Title, State: view.State,
			Detail: fmt.Sprintf("%d attempt(s) · %s", attemptCounts[document.Record.(*research.Try).ID], description),
		})
		if document.Record.(*research.Try).State == research.TryOpen {
			openRows = append(openRows, row)
		} else if len(recentRows) < 12 {
			recentRows = append(recentRows, row)
		}
	}
	return openRows, recentRows, attemptCounts
}

func uiFrontierRows(inventory *record.Inventory) []tui.Row {
	rows := make([]tui.Row, 0)
	for _, frontier := range inventory.QueueFrontier() {
		title := frontier.Entry.Plan.String()
		if document, err := inventory.ByID(frontier.Entry.Plan); err == nil {
			title = document.Record.(*research.Plan).Title
		}
		rows = append(rows, tui.NewRow(tui.RowSpec{
			ID: frontier.Entry.Plan.String(), Title: title, State: string(frontier.Lane),
			Detail: fmt.Sprintf("score %.6g · queue %s · pool %s", frontier.Entry.Score, frontier.Queue, frontier.Pool),
			Reason: uiPinnedReason(frontier.Entry.Pinned),
		}))
	}
	return rows
}

func uiAttemptRows(inventory *record.Inventory, jobs []operation.JobSummary, now time.Time) []tui.Row {
	jobsBySubject := make(map[string]operation.JobSummary, len(jobs))
	for _, job := range jobs {
		jobsBySubject[job.SubjectID] = job
	}
	documents := append([]*record.Document(nil), inventory.OfKind(research.KindAttempt)...)
	sort.SliceStable(documents, func(left, right int) bool {
		leftValue := documents[left].Record.(*research.Attempt)
		rightValue := documents[right].Record.(*research.Attempt)
		if !leftValue.UpdatedAt.Equal(rightValue.UpdatedAt) {
			return leftValue.UpdatedAt.After(rightValue.UpdatedAt)
		}
		return leftValue.ID.String() < rightValue.ID.String()
	})
	rows := make([]tui.Row, 0)
	for _, document := range documents {
		attempt := document.Record.(*research.Attempt)
		if uiTerminalAttempt(attempt.State) {
			continue
		}
		observation := &tryflow.AttemptStatus{}
		if job, found := jobsBySubject[attempt.ID.String()]; found {
			observation.Job = &operation.Job{State: job.State, LeaseExpiresAt: job.LeaseExpiresAt}
		}
		tryflow.ClassifyAttemptStatus(now.UTC(), attempt.State, observation)
		state := "uncertain"
		remediation := "Inspect with exp try status or exp daemon status."
		switch {
		case observation.Recoverable:
			state = "recoverable"
			remediation = "Use exp try resume for Try-owned work."
		case observation.Active:
			state = "active"
			remediation = "Observe with exp try status or exp daemon status."
		case observation.Uncertain:
			remediation = "Inspect before any explicit reconciliation."
		}
		reason := observation.Status
		if reason == "" {
			reason = "canonical and operational state could not be classified"
		}
		owner := "formal"
		if !attempt.Try.IsZero() {
			owner = "Try " + attempt.Try.String()
		} else if !attempt.Run.IsZero() {
			owner = "Run " + attempt.Run.String()
		}
		rows = append(rows, tui.NewRow(tui.RowSpec{
			ID: attempt.ID.String(), Title: attempt.Title, State: state,
			Detail: owner + " · canonical " + string(attempt.State), Reason: reason, Remediation: remediation,
		}))
	}
	return rows
}

func uiCandidateRows(inventory *record.Inventory) ([]tui.Row, []tui.Row, bool) {
	candidateRows := make([]tui.Row, 0)
	for _, document := range inventory.OfKind(research.KindCandidate) {
		candidate := document.Record.(*research.Candidate)
		candidateRows = append(candidateRows, tui.NewRow(tui.RowSpec{
			ID: candidate.ID.String(), Title: candidate.Title, State: "validated",
			Detail: "Experiment " + candidate.Experiment.String() + " · Evaluation " + candidate.Evaluation.String(),
		}))
	}
	champions, err := inventory.CurrentChampions()
	if err != nil {
		return candidateRows, nil, true
	}
	championRows := make([]tui.Row, 0, len(champions))
	for _, champion := range champions {
		title := champion.Release.String()
		if document, resolveErr := inventory.ByID(champion.Release); resolveErr == nil {
			title = document.Record.(*research.Release).Title
		}
		championRows = append(championRows, tui.NewRow(tui.RowSpec{
			ID: champion.Release.String(), Title: champion.Target + ": " + title, State: "champion",
			Detail: "derived from Promotion " + champion.Promotion.String(),
		}))
	}
	return candidateRows, championRows, false
}

func uiUnprobedReadinessSections(app *App) []tui.Section {
	providers := make([]tui.Row, 0)
	if app != nil && app.Registry != nil {
		for _, descriptor := range app.Registry.List() {
			state := "unprobed"
			reason := "live probe not requested"
			remediation := "Press r or leave and revisit this tab to probe explicitly."
			if len(descriptor.CandidateBinaries) == 0 {
				state, reason, remediation = "built-in", "compiled into exp", ""
			}
			providers = append(providers, tui.NewRow(tui.RowSpec{
				ID: string(descriptor.Name), Title: string(descriptor.Name), State: state,
				Detail: strings.Join(uiProviderCapabilities(descriptor), ", "), Reason: reason, Remediation: remediation,
			}))
		}
	}
	backends := make([]tui.Row, 0)
	if app != nil && app.WorkspaceRegistry != nil {
		for _, descriptor := range app.WorkspaceRegistry.Descriptors() {
			state, reason, remediation := "unprobed", "live probe not requested", "Press r or leave and revisit this tab to probe explicitly."
			if descriptor.BuiltIn {
				state, reason, remediation = "built-in", "compiled native correctness baseline", ""
			}
			backends = append(backends, tui.NewRow(tui.RowSpec{
				ID: descriptor.Name, Title: descriptor.Name, State: state,
				Detail: uiWorkspaceCapabilities(descriptor.Capabilities), Reason: reason, Remediation: remediation,
			}))
		}
	}
	return []tui.Section{
		tui.NewSection("Providers", "No compiled provider descriptors.", providers...),
		tui.NewSection("Workspace backends", "No compiled workspace backend descriptors.", backends...),
	}
}

func loadUILiveReadiness(ctx context.Context, app *App, root *rootOptions) (tui.Snapshot, error) {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return tui.Snapshot{}, err
	}
	discovery := provider.LocalDiscoveryOptions{
		Context:      provider.ContextName("local"),
		Lookup:       app.BinaryLookup,
		VersionProbe: doctorLiveProbe(app),
		Now:          app.clock,
		Redaction:    provider.DefaultRedactionPolicy(),
	}
	probes, err := app.Registry.DiscoverLocal(ctx, discovery)
	if err != nil {
		return tui.Snapshot{}, err
	}
	backends, err := app.WorkspaceRegistry.Statuses(ctx, start, true)
	if err != nil {
		return tui.Snapshot{}, err
	}
	providerViews := makeDoctorViews(app.Registry.List(), probes)
	providerRows := make([]tui.Row, 0, len(providerViews))
	for _, view := range providerViews {
		detail := strings.Join(view.Requirements, ", ")
		if view.Version != "" {
			detail += " · version " + view.Version
		}
		providerRows = append(providerRows, tui.NewRow(tui.RowSpec{
			ID: string(view.Name), Title: string(view.Name), State: string(view.State), Detail: strings.TrimSpace(strings.TrimPrefix(detail, " · ")),
			Reason: view.Reason, Remediation: uiProviderRemediation(view),
		}))
	}
	backendRows := make([]tui.Row, 0, len(backends))
	for _, view := range backends {
		backendRows = append(backendRows, tui.NewRow(tui.RowSpec{
			ID: view.Provider, Title: view.Provider, State: string(view.State),
			Detail: uiWorkspaceCapabilities(view.Capabilities), Reason: view.Reason,
			Remediation: uiWorkspaceRemediation(view),
		}))
	}
	sections := map[tui.ViewID][]tui.Section{
		tui.ViewReadiness: {
			tui.NewSection("Providers", "No provider readiness observations.", providerRows...),
			tui.NewSection("Workspace backends", "No workspace backend readiness observations.", backendRows...),
		},
	}
	partial := doctorPartial(providerViews, backends)
	note := "Explicit local probes only; no install, login, service start, workload, or canonical write was performed."
	return tui.NewSnapshot("", app.observedAt(), sections, partial, note), nil
}

func uiProviderCapabilities(descriptor provider.Descriptor) []string {
	values := make([]string, len(descriptor.Capabilities))
	for index, capability := range descriptor.Capabilities {
		values[index] = string(capability)
	}
	return values
}

func uiWorkspaceCapabilities(values []workspacebackend.CapabilityStatus) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, string(value.Capability)+"="+string(value.Support))
	}
	return strings.Join(parts, ", ")
}

func uiProviderRemediation(view doctorProviderView) string {
	switch view.State {
	case provider.ReadinessMissing:
		if view.Binary != "" {
			return "Install " + view.Binary + " outside exp ui, then press r."
		}
		return "Install the optional tool outside exp ui, then press r."
	case provider.ReadinessMisconfigured:
		return "Configure or start the tool outside exp ui, then press r."
	case provider.ReadinessUnsupported:
		return "Install a compatible tool version outside exp ui, then press r."
	case provider.ReadinessUnknown, provider.ReadinessInstalledNotProbed:
		return "Inspect with exp doctor --live for detailed diagnostics."
	default:
		return ""
	}
}

func uiWorkspaceRemediation(view workspacebackend.Readiness) string {
	switch view.State {
	case workspacebackend.ReadinessMissing:
		return "Install the optional workspace tool outside exp ui, then press r."
	case workspacebackend.ReadinessMisconfigured:
		return "Configure the workspace tool outside exp ui, then press r."
	case workspacebackend.ReadinessUnsupported:
		return "Use native_git or install a compatible version outside exp ui."
	case workspacebackend.ReadinessUnknown, workspacebackend.ReadinessInstalledNotProbed:
		return "Inspect with exp workspace backend status --probe."
	default:
		return ""
	}
}

func uiInventoryRemediation(inventory *record.Inventory) string {
	if inventory != nil && !inventory.Valid() {
		return "Use exp validate for canonical diagnostics."
	}
	return ""
}

func uiConfigRemediation(config configSummaryView) string {
	if !config.Trusted {
		return "Review with exp config explain; trust remains an explicit separate command."
	}
	return ""
}

func uiLayerRemediation(trusted bool) string {
	if !trusted {
		return "Review with exp config explain; exp ui never grants trust."
	}
	return ""
}

func uiPinnedReason(pinned bool) string {
	if pinned {
		return "human-pinned canonical queue entry"
	}
	return ""
}

func countState(count int) string {
	if count == 0 {
		return "empty"
	}
	return "active"
}

func uiTerminalAttempt(state research.AttemptState) bool {
	switch state {
	case research.AttemptSucceeded, research.AttemptFailed, research.AttemptCancelled,
		research.AttemptTimedOut, research.AttemptPreempted, research.AttemptOutOfMemory:
		return true
	default:
		return false
	}
}

func uiArtifactRows(inventory *record.Inventory) []tui.Row {
	rows := []tui.Row{}
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		metadata, err := exploration.MetadataFor(attempt)
		if err != nil || metadata == nil {
			continue
		}
		for _, artifact := range metadata.Artifacts {
			rows = append(rows, tui.NewRow(tui.RowSpec{ID: attempt.ID.String() + ":" + artifact.Name, Title: artifact.Name, State: metadata.ArchiveState, Detail: fmt.Sprintf("%s · %d bytes · %s · %s", attempt.ID, artifact.Bytes, artifact.Storage, artifact.Digest), Remediation: "exp results open " + attempt.ID.String() + " " + artifact.Name}))
			if len(rows) == 50 {
				return rows
			}
		}
	}
	return rows
}
