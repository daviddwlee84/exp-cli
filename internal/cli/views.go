package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/trust"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

type projectView struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Root           string `json:"root"`
	RepositoryRoot string `json:"repository_root"`
}

type sourceView struct {
	ID           string   `json:"id"`
	Display      string   `json:"display"`
	Path         string   `json:"path"`
	Revision     string   `json:"revision"`
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Kind         string   `json:"kind"`
	Subdir       string   `json:"subdir"`
	LocatorHints []string `json:"locator_hints"`
	State        string   `json:"state"`
	RetiredAt    string   `json:"retired_at,omitempty"`
	Tags         []string `json:"tags"`
}

type workspaceContextView struct {
	ProjectID      string `json:"project_id"`
	Root           string `json:"root"`
	RepositoryRoot string `json:"repository_root"`
	InvocationDir  string `json:"invocation_dir"`
	Resolution     string `json:"resolution"`
}

type selectedSourceView struct {
	Source                    sourceView `json:"source"`
	CloneRoot                 string     `json:"clone_root"`
	ResolvedRoot              string     `json:"resolved_root"`
	WorkspaceBackendRequested string     `json:"workspace_backend_requested"`
	WorkspaceBackendActual    string     `json:"workspace_backend_actual,omitempty"`
}

type associationSummaryView struct {
	Kind                string   `json:"kind"`
	ProjectRegistered   bool     `json:"project_registered"`
	SourceRegistered    bool     `json:"source_registered"`
	MatchedLocatorHints []string `json:"matched_locator_hints"`
	ValidatedAt         string   `json:"validated_at"`
}

type configLayerView struct {
	Kind         config.LayerKind   `json:"kind"`
	Source       string             `json:"source"`
	Digest       string             `json:"digest"`
	Trusted      bool               `json:"trusted"`
	Capabilities []trust.Capability `json:"capabilities"`
	Fields       []string           `json:"fields"`
}

type configProvenanceView struct {
	Field      string           `json:"field"`
	Source     string           `json:"source"`
	Layer      config.LayerKind `json:"layer"`
	Digest     string           `json:"digest"`
	Trusted    bool             `json:"trusted"`
	Capability trust.Capability `json:"capability,omitempty"`
}

type configLegacyView struct {
	Name    string           `json:"name"`
	Path    string           `json:"path"`
	Source  string           `json:"source"`
	Layer   config.LayerKind `json:"layer"`
	Trusted bool             `json:"trusted"`
}

type configSummaryView struct {
	Digest     string                 `json:"digest"`
	Trusted    bool                   `json:"trusted"`
	Layers     []configLayerView      `json:"layers"`
	Provenance []configProvenanceView `json:"provenance"`
	Legacy     []configLegacyView     `json:"legacy"`
}

type recordCounts struct {
	Policy          int `json:"policy"`
	Sources         int `json:"sources"`
	Ideas           int `json:"ideas"`
	ResourcePools   int `json:"resource_pools"`
	Queues          int `json:"queues"`
	QueueAdvice     int `json:"queue_advice"`
	Battles         int `json:"battles"`
	Plans           int `json:"plans"`
	Tries           int `json:"tries"`
	Experiments     int `json:"experiments"`
	Runs            int `json:"runs"`
	Attempts        int `json:"attempts"`
	EvaluationSpecs int `json:"evaluation_specs"`
	Evaluations     int `json:"evaluations"`
	Findings        int `json:"findings"`
	Candidates      int `json:"candidates"`
	Releases        int `json:"releases"`
	PromotionSpecs  int `json:"promotion_specs"`
	Promotions      int `json:"promotions"`
	Decisions       int `json:"decisions"`
	Total           int `json:"total"`
}

type payoffView struct {
	Summary  string   `json:"summary"`
	Metric   string   `json:"metric"`
	Unit     string   `json:"unit"`
	Estimate *float64 `json:"estimate,omitempty"`
}

type planView struct {
	ID                  string     `json:"id"`
	Display             string     `json:"display"`
	Path                string     `json:"path"`
	Revision            string     `json:"revision"`
	Title               string     `json:"title"`
	Priority            string     `json:"priority"`
	Effort              string     `json:"effort"`
	State               string     `json:"state"`
	ExpectedPayoff      payoffView `json:"expected_payoff"`
	Tags                []string   `json:"tags"`
	Assumptions         []string   `json:"assumptions"`
	ResultingExperiment string     `json:"resulting_experiment"`
}

type dedicatedInitPlanView struct {
	CanonicalRepository string            `json:"canonical_repository"`
	Source              sourceAddPlanView `json:"source"`
	Effects             []string          `json:"effects"`
}

type initData struct {
	SchemaVersion       string                    `json:"schema_version,omitempty"`
	Mode                string                    `json:"mode,omitempty"`
	Project             projectView               `json:"project"`
	Created             bool                      `json:"created"`
	Plan                *dedicatedInitPlanView    `json:"plan,omitempty"`
	Source              *sourceView               `json:"source,omitempty"`
	SourceCreated       bool                      `json:"source_created,omitempty"`
	WorkspaceRegistered bool                      `json:"workspace_registered,omitempty"`
	SourceAssociated    bool                      `json:"source_associated,omitempty"`
	Association         *sourceAssociationView    `json:"association,omitempty"`
	Transaction         *record.TransactionResult `json:"transaction,omitempty"`
	Projections         projection.Result         `json:"projections"`
}

type planAddData struct {
	Plan        planView          `json:"plan"`
	Projections projection.Result `json:"projections"`
}

type planListData struct {
	Project projectView `json:"project"`
	Plans   []planView  `json:"plans"`
}

type validateData struct {
	Project projectView  `json:"project"`
	Valid   bool         `json:"valid"`
	Counts  recordCounts `json:"counts"`
}

type renderData struct {
	Project projectView       `json:"project"`
	Check   bool              `json:"check"`
	Result  projection.Result `json:"result"`
}

type contextData struct {
	SchemaVersion    string                 `json:"schema_version"`
	Project          projectView            `json:"project"`
	Workspace        workspaceContextView   `json:"workspace"`
	Source           *selectedSourceView    `json:"source,omitempty"`
	Association      associationSummaryView `json:"association"`
	Config           configSummaryView      `json:"config"`
	Counts           recordCounts           `json:"counts"`
	QueuedPlans      []planView             `json:"queued_plans"`
	QueueFrontier    []contextFrontierView  `json:"queue_frontier"`
	Champions        []record.Champion      `json:"champions"`
	ProviderRefresh  bool                   `json:"provider_refresh"`
	LiveObservations bool                   `json:"live_observations"`
	ObservationScope string                 `json:"observation_scope"`
}

type contextFrontierView struct {
	Queue string  `json:"queue"`
	Pool  string  `json:"pool"`
	Lane  string  `json:"lane"`
	Plan  string  `json:"plan"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
}

type doctorCapabilityView struct {
	Name    provider.Capability `json:"name"`
	Support provider.Support    `json:"support"`
}

type doctorProviderView struct {
	Name         provider.ProviderName   `json:"name"`
	State        provider.ReadinessState `json:"state"`
	Reason       string                  `json:"reason,omitempty"`
	Probed       bool                    `json:"probed"`
	BuiltIn      bool                    `json:"built_in"`
	Found        bool                    `json:"found"`
	Missing      bool                    `json:"missing"`
	Binary       string                  `json:"binary,omitempty"`
	Version      string                  `json:"version,omitempty"`
	Requirements []string                `json:"requirements"`
	Capabilities []doctorCapabilityView  `json:"capabilities"`
	Diagnostics  []provider.Diagnostic   `json:"diagnostics"`
}

type doctorData struct {
	LiveRequested       bool                         `json:"live_requested"`
	LiveProbesPerformed bool                         `json:"live_probes_performed"`
	Partial             bool                         `json:"partial"`
	Providers           []doctorProviderView         `json:"providers"`
	WorkspaceBackends   []workspacebackend.Readiness `json:"workspace_backends"`
}

func makeProjectView(info *project.Info) (projectView, error) {
	if info == nil || info.Project() == nil {
		return projectView{}, fmt.Errorf("project information is incomplete")
	}
	redactor := safex.NewRedactor()
	return projectView{
		ID:             info.Project().ProjectID.String(),
		Name:           info.Project().Name,
		Root:           redactor.Path(info.Root),
		RepositoryRoot: redactor.Path(info.Repository.Root),
	}, nil
}

func makeSourceViews(info *project.Info, documents []*record.Document) ([]sourceView, error) {
	candidates := make([]research.ReferenceCandidate, 0, len(documents))
	for _, document := range documents {
		if id, ok := document.ID(); ok && id.Kind() == research.KindSource {
			candidates = append(candidates, research.ReferenceCandidate{ID: id})
		}
	}
	views := make([]sourceView, 0, len(documents))
	for _, document := range documents {
		source, ok := document.Record.(*research.Source)
		if !ok {
			continue
		}
		display, err := research.DisplayCode(source.ID, candidates)
		if err != nil {
			return nil, err
		}
		relative, err := repositoryRelativePath(info, document.Path)
		if err != nil {
			return nil, err
		}
		retiredAt := ""
		if source.RetiredAt != nil {
			retiredAt = source.RetiredAt.UTC().Format(time.RFC3339Nano)
		}
		tags := append([]string{}, source.Tags...)
		locators := append([]string{}, source.LocatorHints...)
		sort.Strings(tags)
		views = append(views, sourceView{
			ID: source.ID.String(), Display: display, Path: relative, Revision: document.Revision,
			Key: source.Key, Title: source.Title, Kind: string(source.Kind), Subdir: source.Subdir,
			LocatorHints: locators, State: string(source.State), RetiredAt: retiredAt, Tags: tags,
		})
	}
	return views, nil
}

func makeWorkspaceContextViews(resolved *workspace.Context, inventory *record.Inventory) (workspaceContextView, *selectedSourceView, associationSummaryView, configSummaryView, error) {
	if resolved == nil || resolved.Project == nil {
		return workspaceContextView{}, nil, associationSummaryView{}, configSummaryView{}, fmt.Errorf("resolved workspace context is incomplete")
	}
	redactor := safex.NewRedactor()
	workspaceView := workspaceContextView{
		ProjectID: resolved.ProjectID().String(), Root: redactor.Path(resolved.Project.Root),
		RepositoryRoot: redactor.Path(resolved.Project.Repository.Root), InvocationDir: redactor.Path(resolved.InvocationDir),
		Resolution: string(resolved.Association.Kind),
	}
	associationView := associationSummaryView{
		Kind: string(resolved.Association.Kind), ProjectRegistered: resolved.Association.Project != nil,
		SourceRegistered:    resolved.Association.Source != nil,
		MatchedLocatorHints: append([]string{}, resolved.Association.MatchedLocatorHints...),
		ValidatedAt:         resolved.Association.ValidatedAt.UTC().Format(time.RFC3339Nano),
	}
	configView := makeConfigSummaryView(resolved.Config)
	if resolved.Source == nil {
		return workspaceView, nil, associationView, configView, nil
	}
	if inventory == nil {
		return workspaceContextView{}, nil, associationSummaryView{}, configSummaryView{}, fmt.Errorf("canonical inventory is required for selected Source view")
	}
	document, err := inventory.ByID(resolved.Source.ID)
	if err != nil {
		return workspaceContextView{}, nil, associationSummaryView{}, configSummaryView{}, err
	}
	views, err := makeSourceViews(resolved.Project, inventory.OfKind(research.KindSource))
	if err != nil {
		return workspaceContextView{}, nil, associationSummaryView{}, configSummaryView{}, err
	}
	var selectedRecord sourceView
	found := false
	for _, view := range views {
		if view.ID == document.Record.(*research.Source).ID.String() {
			selectedRecord = view
			found = true
			break
		}
	}
	if !found {
		return workspaceContextView{}, nil, associationSummaryView{}, configSummaryView{}, fmt.Errorf("selected Source view is unavailable")
	}
	requested, actual := workspacebackend.NativeGitName, workspacebackend.NativeGitName
	if resolved.Config != nil {
		requested = resolved.Config.Effective.Defaults.WorkspaceBackend
		selection, selectionErr := workspacebackend.ResolveSelection("", resolved.Config)
		if selectionErr != nil || selection.Requested == workspacebackend.DevCLIName && !selection.AllowUnsupportedFallback {
			actual = ""
		}
	}
	selected := &selectedSourceView{
		Source: selectedRecord, CloneRoot: redactor.Path(resolved.SourceRoot), ResolvedRoot: redactor.Path(resolved.ResolvedRoot),
		WorkspaceBackendRequested: requested, WorkspaceBackendActual: actual,
	}
	return workspaceView, selected, associationView, configView, nil
}

func makeConfigSummaryView(result *config.Result) configSummaryView {
	view := configSummaryView{Trusted: true, Layers: []configLayerView{}, Provenance: []configProvenanceView{}, Legacy: []configLegacyView{}}
	if result == nil {
		return view
	}
	view.Digest = result.Digest
	redactor := safex.NewRedactor()
	for _, layer := range result.Layers {
		source := layer.Source
		if source != "<built-in>" {
			source = redactor.Path(source)
		}
		view.Layers = append(view.Layers, configLayerView{
			Kind: layer.Kind, Source: source, Digest: layer.Digest, Trusted: layer.Trusted,
			Capabilities: append([]trust.Capability{}, layer.Capabilities...), Fields: append([]string{}, layer.Fields...),
		})
	}
	fields := make([]string, 0, len(result.Provenance))
	for field := range result.Provenance {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		provenance := result.Provenance[field]
		source := provenance.Source
		if source != "<built-in>" {
			source = redactor.Path(source)
		}
		view.Provenance = append(view.Provenance, configProvenanceView{
			Field: field, Source: source, Layer: provenance.Layer, Digest: provenance.Digest,
			Trusted: provenance.Trusted, Capability: provenance.Capability,
		})
		if !provenance.Trusted {
			view.Trusted = false
		}
	}
	for _, legacy := range []struct {
		name string
		path config.LegacyPath
	}{{"agents", result.Legacy.Agents}, {"runtime", result.Legacy.Runtime}} {
		view.Legacy = append(view.Legacy, configLegacyView{
			Name: legacy.name, Path: redactor.Path(legacy.path.Path), Source: legacy.path.Source,
			Layer: legacy.path.Layer, Trusted: legacy.path.Trusted,
		})
	}
	return view
}

func countsFor(inventory *record.Inventory) recordCounts {
	if inventory == nil {
		return recordCounts{}
	}
	counts := recordCounts{
		Sources:         len(inventory.OfKind(research.KindSource)),
		Ideas:           len(inventory.OfKind(research.KindIdea)),
		ResourcePools:   len(inventory.OfKind(research.KindResourcePool)),
		Queues:          len(inventory.OfKind(research.KindQueue)),
		QueueAdvice:     len(inventory.OfKind(research.KindQueueAdvice)),
		Battles:         len(inventory.OfKind(research.KindBattle)),
		Plans:           len(inventory.OfKind(research.KindPlan)),
		Tries:           len(inventory.OfKind(research.KindTry)),
		Experiments:     len(inventory.OfKind(research.KindExperiment)),
		Runs:            len(inventory.OfKind(research.KindRun)),
		Attempts:        len(inventory.OfKind(research.KindAttempt)),
		EvaluationSpecs: len(inventory.OfKind(research.KindEvaluationSpec)),
		Evaluations:     len(inventory.OfKind(research.KindEvaluation)),
		Findings:        len(inventory.OfKind(research.KindFinding)),
		Candidates:      len(inventory.OfKind(research.KindCandidate)),
		Releases:        len(inventory.OfKind(research.KindRelease)),
		PromotionSpecs:  len(inventory.OfKind(research.KindPromotionSpec)),
		Promotions:      len(inventory.OfKind(research.KindPromotion)),
		Decisions:       len(inventory.OfKind(research.KindDecision)),
	}
	if inventory.Policy != nil {
		counts.Policy = 1
	}
	counts.Total = counts.Policy + counts.Sources + counts.Ideas + counts.ResourcePools + counts.Queues + counts.QueueAdvice + counts.Battles +
		counts.Plans + counts.Tries + counts.Experiments + counts.Runs + counts.Attempts + counts.EvaluationSpecs + counts.Evaluations +
		counts.Findings + counts.Candidates + counts.Releases + counts.PromotionSpecs + counts.Promotions + counts.Decisions
	return counts
}

func makePlanViews(info *project.Info, documents []*record.Document) ([]planView, error) {
	candidates := make([]research.ReferenceCandidate, 0, len(documents))
	for _, document := range documents {
		id, ok := document.ID()
		if ok && id.Kind() == research.KindPlan {
			candidates = append(candidates, research.ReferenceCandidate{ID: id})
		}
	}
	views := make([]planView, 0, len(documents))
	for _, document := range documents {
		plan, ok := document.Record.(*research.Plan)
		if !ok {
			continue
		}
		display, err := research.DisplayCode(plan.ID, candidates)
		if err != nil {
			return nil, err
		}
		path, err := repositoryRelativePath(info, document.Path)
		if err != nil {
			return nil, err
		}
		assumptions := make([]string, len(plan.Assumptions))
		for index, assumption := range plan.Assumptions {
			assumptions[index] = assumption.String()
		}
		sort.Strings(assumptions)
		tags := append([]string(nil), plan.Tags...)
		if tags == nil {
			tags = []string{}
		}
		estimate := plan.ExpectedPayoff.Estimate
		if estimate != nil {
			copy := *estimate
			estimate = &copy
		}
		views = append(views, planView{
			ID:       plan.ID.String(),
			Display:  display,
			Path:     path,
			Revision: document.Revision,
			Title:    plan.Title,
			Priority: string(plan.Priority),
			Effort:   string(plan.Effort),
			State:    string(plan.State),
			ExpectedPayoff: payoffView{
				Summary:  plan.ExpectedPayoff.Summary,
				Metric:   plan.ExpectedPayoff.Metric,
				Unit:     plan.ExpectedPayoff.Unit,
				Estimate: estimate,
			},
			Tags:                tags,
			Assumptions:         assumptions,
			ResultingExperiment: plan.ResultingExperiment.String(),
		})
	}
	if views == nil {
		views = []planView{}
	}
	return views, nil
}

func repositoryRelativePath(info *project.Info, rootRelative string) (string, error) {
	if info == nil {
		return "", fmt.Errorf("project information is required")
	}
	absolute := filepath.Join(info.Root, filepath.FromSlash(rootRelative))
	relative, err := filepath.Rel(info.Repository.Root, absolute)
	if err != nil {
		return "", fmt.Errorf("make record path repository-relative: %w", err)
	}
	if relative == ".." || filepath.IsAbs(relative) || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
		return "", fmt.Errorf("record path escapes repository root")
	}
	return filepath.ToSlash(relative), nil
}

func makeDoctorViews(descriptors []provider.Descriptor, probes []provider.ProbeResult) []doctorProviderView {
	byName := make(map[provider.ProviderName]provider.ProbeResult, len(probes))
	for _, probe := range probes {
		byName[probe.Provider] = probe
	}
	views := make([]doctorProviderView, 0, len(descriptors))
	for _, descriptor := range descriptors {
		probe := byName[descriptor.Name]
		builtIn := len(descriptor.CandidateBinaries) == 0
		found := builtIn || probe.ResolvedBinaryPath != ""
		state := probe.Readiness
		if !state.Valid() {
			switch {
			case builtIn:
				state = provider.ReadinessBuiltIn
			case found:
				state = provider.ReadinessInstalledNotProbed
			default:
				state = provider.ReadinessMissing
			}
		}
		binary := ""
		if len(descriptor.CandidateBinaries) > 0 {
			binary = descriptor.CandidateBinaries[0]
		}
		capabilities := make([]doctorCapabilityView, 0, len(probe.Capabilities))
		for _, capability := range probe.Capabilities {
			capabilities = append(capabilities, doctorCapabilityView{Name: capability.Capability, Support: capability.Support})
		}
		diagnostics := append([]provider.Diagnostic(nil), probe.Diagnostics...)
		if diagnostics == nil {
			diagnostics = []provider.Diagnostic{}
		}
		views = append(views, doctorProviderView{
			Name: descriptor.Name, State: state, Reason: probe.Reason, Probed: probe.Probed,
			BuiltIn: builtIn, Found: found, Missing: state == provider.ReadinessMissing,
			Binary: binary, Version: probe.ProviderVersion, Requirements: doctorProviderRequirements(descriptor),
			Capabilities: capabilities, Diagnostics: diagnostics,
		})
	}
	if views == nil {
		views = []doctorProviderView{}
	}
	return views
}

func doctorProviderRequirements(descriptor provider.Descriptor) []string {
	if len(descriptor.CandidateBinaries) == 0 {
		return []string{}
	}
	requirements := []string{"binary:" + descriptor.CandidateBinaries[0]}
	switch descriptor.Name {
	case provider.ProviderPueue:
		requirements = append(requirements, "service:pueue-daemon")
	case provider.ProviderMLflow:
		requirements = append(requirements, "context:mlflow-tracking")
	}
	return requirements
}
