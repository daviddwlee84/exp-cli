package cli

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/safex"
)

type projectView struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Root           string `json:"root"`
	RepositoryRoot string `json:"repository_root"`
}

type recordCounts struct {
	Policy          int `json:"policy"`
	Ideas           int `json:"ideas"`
	ResourcePools   int `json:"resource_pools"`
	Queues          int `json:"queues"`
	QueueAdvice     int `json:"queue_advice"`
	Battles         int `json:"battles"`
	Plans           int `json:"plans"`
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

type initData struct {
	Project     projectView       `json:"project"`
	Created     bool              `json:"created"`
	Projections projection.Result `json:"projections"`
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
	Project          projectView           `json:"project"`
	Counts           recordCounts          `json:"counts"`
	QueuedPlans      []planView            `json:"queued_plans"`
	QueueFrontier    []contextFrontierView `json:"queue_frontier"`
	Champions        []record.Champion     `json:"champions"`
	ProviderRefresh  bool                  `json:"provider_refresh"`
	LiveObservations bool                  `json:"live_observations"`
	ObservationScope string                `json:"observation_scope"`
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
	Name         provider.ProviderName  `json:"name"`
	BuiltIn      bool                   `json:"built_in"`
	Found        bool                   `json:"found"`
	Missing      bool                   `json:"missing"`
	Binary       string                 `json:"binary,omitempty"`
	Version      string                 `json:"version,omitempty"`
	Capabilities []doctorCapabilityView `json:"capabilities"`
	Diagnostics  []provider.Diagnostic  `json:"diagnostics"`
}

type doctorData struct {
	LiveRequested       bool                 `json:"live_requested"`
	LiveProbesPerformed bool                 `json:"live_probes_performed"`
	Providers           []doctorProviderView `json:"providers"`
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

func countsFor(inventory *record.Inventory) recordCounts {
	if inventory == nil {
		return recordCounts{}
	}
	counts := recordCounts{
		Ideas:           len(inventory.OfKind(research.KindIdea)),
		ResourcePools:   len(inventory.OfKind(research.KindResourcePool)),
		Queues:          len(inventory.OfKind(research.KindQueue)),
		QueueAdvice:     len(inventory.OfKind(research.KindQueueAdvice)),
		Battles:         len(inventory.OfKind(research.KindBattle)),
		Plans:           len(inventory.OfKind(research.KindPlan)),
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
	counts.Total = counts.Policy + counts.Ideas + counts.ResourcePools + counts.Queues + counts.QueueAdvice + counts.Battles +
		counts.Plans + counts.Experiments + counts.Runs + counts.Attempts + counts.EvaluationSpecs + counts.Evaluations +
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
		capabilities := make([]doctorCapabilityView, 0, len(probe.Capabilities))
		for _, capability := range probe.Capabilities {
			capabilities = append(capabilities, doctorCapabilityView{Name: capability.Capability, Support: capability.Support})
		}
		diagnostics := append([]provider.Diagnostic(nil), probe.Diagnostics...)
		if diagnostics == nil {
			diagnostics = []provider.Diagnostic{}
		}
		views = append(views, doctorProviderView{
			Name:         descriptor.Name,
			BuiltIn:      builtIn,
			Found:        found,
			Missing:      !builtIn && !found,
			Binary:       probe.ResolvedBinaryPath,
			Version:      probe.ProviderVersion,
			Capabilities: capabilities,
			Diagnostics:  diagnostics,
		})
	}
	if views == nil {
		views = []doctorProviderView{}
	}
	return views
}
