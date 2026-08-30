package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

type ideaOptions struct {
	json           bool
	title          string
	summary        string
	body           string
	proposedBy     string
	cluster        string
	domain         string
	work           string
	method         string
	component      string
	lane           string
	risk           string
	horizon        string
	origin         string
	tags           []string
	parents        []string
	planTitle      string
	priority       string
	effort         string
	payoffSummary  string
	payoffMetric   string
	payoffUnit     string
	payoffEstimate float64
	resources      []string
	dependencies   []string
	probability    float64
	impact         float64
	information    float64
	unblock        float64
	riskPenalty    float64
	expectedRev    string
	config         string
	profile        string
	apply          bool
}

type ideaData struct {
	Idea canonicalRecordView `json:"idea"`
	Plan canonicalRecordView `json:"plan,omitempty"`
}

func newIdeaCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "idea", Short: "Capture and qualify human or agent research ideas", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newIdeaAddCommand(app, root), newIdeaListCommand(app, root), newIdeaQualifyCommand(app, root), newIdeaDevelopCommand(app, root))
	return command
}

func newIdeaAddCommand(app *App, root *rootOptions) *cobra.Command {
	options := &ideaOptions{}
	command := &cobra.Command{Use: "add", Short: "Create an unqueued canonical Idea", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runIdeaAdd(command, app, root, options) }
	flags := command.Flags()
	flags.StringVar(&options.title, "title", "", "set the Idea title")
	flags.StringVar(&options.summary, "summary", "", "state the proposed mechanism or direction")
	flags.StringVar(&options.body, "body", "", "set optional Markdown detail")
	flags.StringVar(&options.proposedBy, "proposed-by", "human", "identify the human or agent proposer")
	flags.StringVar(&options.cluster, "cluster", "general", "set the primary research cluster slug")
	flags.StringVar(&options.domain, "domain", "general", "set the controlled domain")
	flags.StringVar(&options.work, "work", "experiment", "set the controlled work class")
	flags.StringVar(&options.method, "method", "empirical", "set the controlled method")
	flags.StringVar(&options.component, "component", "core", "set the controlled component")
	flags.StringVar(&options.lane, "lane", string(research.LaneExplore), "set exploit or explore")
	flags.StringVar(&options.risk, "risk", string(research.RiskMedium), "set low, medium, or high")
	flags.StringVar(&options.horizon, "horizon", string(research.HorizonMedium), "set short, medium, or long")
	flags.StringVar(&options.origin, "origin", string(research.OriginHuman), "set human, agent, hybrid, or imported")
	flags.StringSliceVar(&options.tags, "tags", nil, "set free discovery tags")
	flags.StringSliceVar(&options.parents, "parents", nil, "link parent Ideas by ID or display code")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newIdeaListCommand(app *App, root *rootOptions) *cobra.Command {
	options := &ideaOptions{}
	command := &cobra.Command{Use: "list", Short: "List canonical Ideas", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runIdeaList(command, app, root, options) }
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newIdeaQualifyCommand(app *App, root *rootOptions) *cobra.Command {
	options := &ideaOptions{priority: string(research.PriorityP2), effort: string(research.EffortM), probability: .5, impact: 1}
	command := &cobra.Command{Use: "qualify <idea>", Short: "Atomically turn an Idea into a fully priced exp.plan/v2", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runIdeaQualify(command, app, root, options, args[0])
	}
	flags := command.Flags()
	flags.StringVar(&options.planTitle, "plan-title", "", "override the resulting Plan title")
	flags.StringVar(&options.body, "body", "", "set the resulting Plan Markdown detail")
	flags.StringVar(&options.priority, "priority", string(research.PriorityP2), "set P1, P2, P3, or P?")
	flags.StringVar(&options.effort, "effort", string(research.EffortM), "set S, M, L, or XL")
	flags.StringVar(&options.payoffSummary, "payoff-summary", "", "describe the measurable expected payoff")
	flags.StringVar(&options.payoffMetric, "payoff-metric", "", "set the key metric slug")
	flags.StringVar(&options.payoffUnit, "payoff-unit", "", "set the metric unit")
	flags.Float64Var(&options.payoffEstimate, "payoff-estimate", 0, "set an optional point payoff estimate")
	flags.StringSliceVar(&options.resources, "resource", nil, "add POOL:UNITS:HOURS (repeatable)")
	flags.StringSliceVar(&options.dependencies, "depends-on", nil, "pin a Finding revision and current belief digest")
	flags.Float64Var(&options.probability, "probability", .5, "probability of material improvement")
	flags.Float64Var(&options.impact, "impact", 1, "impact if successful")
	flags.Float64Var(&options.information, "information-value", 0, "value of information even if refuted")
	flags.Float64Var(&options.unblock, "unblock-value", 0, "value of unblocking dependent work")
	flags.Float64Var(&options.riskPenalty, "risk-penalty", 0, "expected downside penalty")
	flags.StringVar(&options.expectedRev, "expected-revision", "", "require an exact current Idea revision")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runIdeaAdd(command *cobra.Command, app *App, root *rootOptions, options *ideaOptions) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "idea add", ideaData{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "idea add", ideaData{}, false, nil, err)
	}
	if inventory.Policy == nil {
		return commandFailure(app, options.json, "idea add", ideaData{}, false, nil, errors.New("POLICY.md is required; run exp policy init"))
	}
	parents, err := resolveMany(inventory, options.parents, research.KindIdea)
	if err != nil {
		return commandFailure(app, options.json, "idea add", ideaData{}, false, nil, err)
	}
	now := app.clock()
	id, err := generatedID(app, research.KindIdea, now)
	if err != nil {
		return commandFailure(app, options.json, "idea add", ideaData{}, false, nil, err)
	}
	body := options.body
	if body == "" {
		body = "\n# " + options.title + "\n\n" + options.summary + "\n"
	}
	idea := &research.Idea{
		Common: research.Common{Schema: research.SchemaIdea, ID: id, Title: options.title, CreatedAt: now, UpdatedAt: now, Tags: options.tags},
		State:  research.IdeaProposed, Summary: options.summary, ProposedBy: options.proposedBy,
		PrimaryCluster: options.cluster, Parents: parents,
		Classification: research.Classification{Domain: options.domain, Work: options.work, Method: options.method, Component: options.component, Lane: research.ResearchLane(options.lane), Risk: research.RiskClass(options.risk), Horizon: research.HorizonClass(options.horizon), Origin: research.OriginClass(options.origin)},
	}
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "idea.add", Changes: []record.TransactionChange{{Operation: record.TransactionCreate, Document: &record.Document{Record: idea, Body: body}}}})
	if err != nil {
		return commandFailure(app, options.json, "idea add", ideaData{}, false, nil, err)
	}
	published := transactionDocument(result, research.KindIdea)
	data := ideaData{Idea: canonicalView(published)}
	return commandSuccess(app, options.json, "idea add", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Created Idea %s.\n", id))
}

func runIdeaList(command *cobra.Command, app *App, root *rootOptions, options *ideaOptions) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "idea list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "idea list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	views := []canonicalRecordView{}
	var human strings.Builder
	for _, document := range inventory.OfKind(research.KindIdea) {
		views = append(views, canonicalView(document))
		idea := document.Record.(*research.Idea)
		fmt.Fprintf(&human, "%s\t%s\t%s\t%s\n", idea.ID, idea.State, idea.PrimaryCluster, idea.Title)
	}
	if len(views) == 0 {
		human.WriteString("No Ideas.\n")
	}
	return commandSuccess(app, options.json, "idea list", recordListData{Records: views}, false, convertRecordDiagnostics(inventory.Diagnostics), human.String())
}

func runIdeaQualify(command *cobra.Command, app *App, root *rootOptions, options *ideaOptions, reference string) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, err)
	}
	ideaDocument, err := inventory.Resolve(reference, research.KindIdea)
	if err != nil {
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, err)
	}
	if options.expectedRev != "" && options.expectedRev != ideaDocument.Revision {
		id, _ := ideaDocument.ID()
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, &record.ConflictError{ID: id, Expected: options.expectedRev, Actual: ideaDocument.Revision})
	}
	idea := ideaDocument.Record.(*research.Idea)
	if idea.State != research.IdeaProposed && idea.State != research.IdeaDeveloping {
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, fmt.Errorf("Idea state %s cannot be qualified", idea.State))
	}
	resources, err := parseResourceNeeds(inventory, options.resources)
	if err != nil {
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, err)
	}
	dependencies, err := pinnedDependencies(inventory, options.dependencies)
	if err != nil {
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, err)
	}
	now := app.clock()
	planID, err := generatedID(app, research.KindPlan, now)
	if err != nil {
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, err)
	}
	title := options.planTitle
	if title == "" {
		title = idea.Title
	}
	body := options.body
	if body == "" {
		body = "\n# " + title + "\n\n" + idea.Summary + "\n"
	}
	estimate := (*float64)(nil)
	if command.Flags().Changed("payoff-estimate") {
		value := options.payoffEstimate
		estimate = &value
	}
	plan := &research.Plan{
		Common:   research.Common{Schema: research.SchemaPlanV2, ID: planID, Title: title, CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), idea.Tags...)},
		Priority: research.Priority(options.priority), Effort: research.Effort(options.effort), State: research.PlanQueued,
		ExpectedPayoff: research.ExpectedPayoff{Summary: options.payoffSummary, Metric: options.payoffMetric, Unit: options.payoffUnit, Estimate: estimate},
		Idea:           idea.ID, PrimaryCluster: idea.PrimaryCluster, Classification: cloneClassification(idea.Classification),
		Dependencies: dependencies, Resources: resources,
		Utility: &research.UtilityEstimate{Probability: options.probability, Impact: options.impact, InformationGain: options.information, UnblockValue: options.unblock, RiskPenalty: options.riskPenalty},
	}
	updatedIdeaDocument := ideaDocument.Clone()
	updatedIdea := updatedIdeaDocument.Record.(*research.Idea)
	updatedIdea.State = research.IdeaQualified
	updatedIdea.ResultingPlan = planID
	updatedIdea.UpdatedAt = now
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "idea.qualify", Changes: []record.TransactionChange{
		{Operation: record.TransactionCreate, Document: &record.Document{Record: plan, Body: body}},
		{Operation: record.TransactionReplace, Document: updatedIdeaDocument, ExpectedRevision: ideaDocument.Revision},
	}})
	if err != nil {
		return commandFailure(app, options.json, "idea qualify", ideaData{}, false, nil, err)
	}
	publishedIdea := transactionDocument(result, research.KindIdea)
	publishedPlan := transactionDocument(result, research.KindPlan)
	data := ideaData{Idea: canonicalView(publishedIdea), Plan: canonicalView(publishedPlan)}
	return commandSuccess(app, options.json, "idea qualify", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Qualified %s as Plan %s.\n", idea.ID, planID))
}

func parseResourceNeeds(inventory *record.Inventory, values []string) ([]research.ResourceNeed, error) {
	if len(values) != 1 {
		return nil, errors.New("exactly one --resource POOL:UNITS:HOURS is required; use a composite ResourcePool for coupled resources")
	}
	result := make([]research.ResourceNeed, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("resource %q must be POOL:UNITS:HOURS", value)
		}
		document, err := inventory.Resolve(parts[0], research.KindResourcePool)
		if err != nil {
			return nil, err
		}
		id, _ := document.ID()
		units, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || units == 0 {
			return nil, fmt.Errorf("resource %q has invalid units", value)
		}
		hours, err := strconv.ParseFloat(parts[2], 64)
		if err != nil || hours <= 0 {
			return nil, fmt.Errorf("resource %q has invalid hours", value)
		}
		result = append(result, research.ResourceNeed{Pool: id, Units: units, EstimatedHours: hours})
	}
	return result, nil
}

func pinnedDependencies(inventory *record.Inventory, values []string) ([]research.FindingDependency, error) {
	result := make([]research.FindingDependency, 0, len(values))
	for _, value := range values {
		document, err := inventory.Resolve(value, research.KindFinding)
		if err != nil {
			return nil, err
		}
		id, _ := document.ID()
		digest, err := inventory.BeliefDigest(id)
		if err != nil {
			return nil, err
		}
		result = append(result, research.FindingDependency{Finding: id, Revision: document.Revision, BeliefDigest: digest})
	}
	return result, nil
}

func resolveMany(inventory *record.Inventory, values []string, kind research.Kind) ([]research.ID, error) {
	result := make([]research.ID, 0, len(values))
	for _, value := range values {
		document, err := inventory.Resolve(value, kind)
		if err != nil {
			return nil, err
		}
		id, _ := document.ID()
		result = append(result, id)
	}
	return result, nil
}

func generatedID(app *App, kind research.Kind, now time.Time) (research.ID, error) {
	value, err := app.GenerateUUID(now)
	if err != nil {
		return research.ID{}, err
	}
	return research.NewID(kind, value)
}

func cloneClassification(value research.Classification) *research.Classification {
	copy := value
	return &copy
}
