package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

const ideaPlanAgentSchema = "exp.agent.idea-plan/v1"

type ideaPlanResource struct {
	Pool           string  `json:"pool"`
	Units          uint64  `json:"units"`
	EstimatedHours float64 `json:"estimated_hours"`
}

type ideaPlanProposal struct {
	SchemaVersion  string               `json:"schema_version"`
	PlanTitle      string               `json:"plan_title"`
	Body           string               `json:"body"`
	Priority       string               `json:"priority"`
	Effort         string               `json:"effort"`
	ExpectedPayoff planAddPayoffRequest `json:"expected_payoff"`
	Resources      []ideaPlanResource   `json:"resources"`
	Dependencies   []string             `json:"dependencies"`
	Utility        ideaPlanUtility      `json:"utility"`
	Rationale      string               `json:"rationale"`
}

type ideaPlanUtility struct {
	Probability     float64 `json:"probability"`
	Impact          float64 `json:"impact"`
	InformationGain float64 `json:"information_gain"`
	UnblockValue    float64 `json:"unblock_value"`
	RiskPenalty     float64 `json:"risk_penalty"`
}

func newIdeaDevelopCommand(app *App, root *rootOptions) *cobra.Command {
	options := &ideaOptions{}
	command := &cobra.Command{Use: "develop <idea>", Short: "Ask one fresh agent to turn an Idea into a queue-ready Plan proposal", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runIdeaDevelop(command, app, root, options, args[0])
	}
	flags := command.Flags()
	flags.StringVar(&options.config, "config", "", "agent profile TOML path")
	flags.StringVar(&options.profile, "profile", "", "override the idea_planner role profile")
	flags.BoolVar(&options.apply, "apply", false, "atomically qualify the Idea using the validated proposal")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runIdeaDevelop(command *cobra.Command, app *App, root *rootOptions, options *ideaOptions, reference string) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "idea develop", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "idea develop", struct{}{}, false, nil, err)
	}
	ideaDocument, err := inventory.Resolve(reference, research.KindIdea)
	if err != nil {
		return commandFailure(app, options.json, "idea develop", struct{}{}, false, nil, err)
	}
	idea := ideaDocument.Record.(*research.Idea)
	if idea.State != research.IdeaProposed && idea.State != research.IdeaDeveloping {
		return commandFailure(app, options.json, "idea develop", struct{}{}, false, nil, fmt.Errorf("Idea state %s cannot be developed", idea.State))
	}
	prompt, err := ideaDevelopmentPrompt(inventory, ideaDocument)
	if err != nil {
		return commandFailure(app, options.json, "idea develop", struct{}{}, false, nil, err)
	}
	_, config, err := agentConfig(app, options.config)
	if err != nil {
		return commandFailure(app, options.json, "idea develop", struct{}{}, false, nil, err)
	}
	run, err := (agentcli.Runner{Config: config, Invoker: app.Invoker, LookupBinary: app.BinaryLookup}).Run(command.Context(), agentcli.Request{
		Role: "idea_planner", Profile: options.profile, Prompt: prompt, Schema: json.RawMessage(ideaPlanJSONSchema), CWD: info.Repository.Root,
	})
	if err != nil {
		return commandFailure(app, options.json, "idea develop", struct{}{}, false, nil, err)
	}
	var proposal ideaPlanProposal
	if err := decodeStrictJSON(run.Output, &proposal); err != nil || proposal.SchemaVersion != ideaPlanAgentSchema {
		if err == nil {
			err = fmt.Errorf("agent schema_version must be %q", ideaPlanAgentSchema)
		}
		return commandFailure(app, options.json, "idea develop", struct{}{}, false, nil, err)
	}
	data := struct {
		Proposal      ideaPlanProposal     `json:"proposal"`
		Applied       bool                 `json:"applied"`
		Idea          *canonicalRecordView `json:"idea,omitempty"`
		Plan          *canonicalRecordView `json:"plan,omitempty"`
		AgentProfile  string               `json:"agent_profile"`
		ReportedModel string               `json:"reported_model,omitempty"`
	}{Proposal: proposal, AgentProfile: run.Profile, ReportedModel: run.ReportedModel}
	if !options.apply {
		pretty, _ := json.MarshalIndent(proposal, "", "  ")
		return commandSuccess(app, options.json, "idea develop", data, false, nil, string(pretty)+"\n")
	}
	resources, err := proposalResourceNeeds(inventory, proposal.Resources)
	if err != nil {
		return commandFailure(app, options.json, "idea develop", data, false, nil, err)
	}
	dependencies, err := pinnedDependencies(inventory, proposal.Dependencies)
	if err != nil {
		return commandFailure(app, options.json, "idea develop", data, false, nil, err)
	}
	now := app.clock()
	planID, err := generatedID(app, research.KindPlan, now)
	if err != nil {
		return commandFailure(app, options.json, "idea develop", data, false, nil, err)
	}
	plan := &research.Plan{
		Common:   research.Common{Schema: research.SchemaPlanV2, ID: planID, Title: proposal.PlanTitle, CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), idea.Tags...)},
		Priority: research.Priority(proposal.Priority), Effort: research.Effort(proposal.Effort), State: research.PlanQueued,
		ExpectedPayoff: research.ExpectedPayoff{Summary: proposal.ExpectedPayoff.Summary, Metric: proposal.ExpectedPayoff.Metric, Unit: proposal.ExpectedPayoff.Unit, Estimate: proposal.ExpectedPayoff.Estimate},
		Idea:           idea.ID, PrimaryCluster: idea.PrimaryCluster, Classification: cloneClassification(idea.Classification),
		Dependencies: dependencies, Resources: resources, Utility: &research.UtilityEstimate{
			Probability: proposal.Utility.Probability, Impact: proposal.Utility.Impact,
			InformationGain: proposal.Utility.InformationGain, UnblockValue: proposal.Utility.UnblockValue,
			RiskPenalty: proposal.Utility.RiskPenalty,
		},
	}
	updatedIdea := ideaDocument.Clone()
	updatedIdea.Record.(*research.Idea).State = research.IdeaQualified
	updatedIdea.Record.(*research.Idea).ResultingPlan = planID
	updatedIdea.Record.(*research.Idea).UpdatedAt = now
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "idea.agent-qualify", Changes: []record.TransactionChange{
		{Operation: record.TransactionCreate, Document: &record.Document{Record: plan, Body: proposal.Body}},
		{Operation: record.TransactionReplace, Document: updatedIdea, ExpectedRevision: ideaDocument.Revision},
	}})
	if err != nil {
		return commandFailure(app, options.json, "idea develop", data, false, nil, err)
	}
	ideaResult := canonicalView(transactionDocument(result, research.KindIdea))
	planResult := canonicalView(transactionDocument(result, research.KindPlan))
	data.Applied, data.Idea, data.Plan = true, &ideaResult, &planResult
	diagnostics := refreshAfterTransaction(command, app, info, store)
	return commandSuccess(app, options.json, "idea develop", data, false, diagnostics, fmt.Sprintf("Agent proposal qualified Idea %s as Plan %s.\n", idea.ID, planID))
}

func proposalResourceNeeds(inventory *record.Inventory, values []ideaPlanResource) ([]research.ResourceNeed, error) {
	if len(values) != 1 {
		return nil, errors.New("agent proposal requires exactly one ResourcePool; use a composite pool for coupled resources")
	}
	result := make([]research.ResourceNeed, 0, len(values))
	for _, value := range values {
		document, err := inventory.Resolve(value.Pool, research.KindResourcePool)
		if err != nil {
			return nil, err
		}
		id, _ := document.ID()
		result = append(result, research.ResourceNeed{Pool: id, Units: value.Units, EstimatedHours: value.EstimatedHours})
	}
	return result, nil
}

func ideaDevelopmentPrompt(inventory *record.Inventory, document *record.Document) ([]byte, error) {
	idea := document.Record.(*research.Idea)
	type poolBrief struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Capacity uint64 `json:"capacity"`
		Unit     string `json:"unit"`
	}
	type findingBrief struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Statement    string `json:"statement"`
		Revision     string `json:"revision"`
		BeliefDigest string `json:"belief_digest"`
	}
	pools := []poolBrief{}
	for _, item := range inventory.OfKind(research.KindResourcePool) {
		pool := item.Record.(*research.ResourcePool)
		pools = append(pools, poolBrief{ID: pool.ID.String(), Title: pool.Title, Capacity: pool.Capacity, Unit: pool.Unit})
	}
	findings := []findingBrief{}
	for _, item := range inventory.OfKind(research.KindFinding) {
		finding := item.Record.(*research.Finding)
		digest, err := inventory.BeliefDigest(finding.ID)
		if err != nil {
			return nil, err
		}
		findings = append(findings, findingBrief{ID: finding.ID.String(), Title: finding.Title, Statement: finding.Statement, Revision: item.Revision, BeliefDigest: digest})
	}
	var policy *research.Policy
	if inventory.Policy != nil {
		policy = inventory.Policy.Record.(*research.Policy)
	}
	payload := struct {
		Task     string           `json:"task"`
		Idea     *research.Idea   `json:"idea"`
		IdeaBody string           `json:"idea_body"`
		Policy   *research.Policy `json:"policy,omitempty"`
		Pools    []poolBrief      `json:"resource_pools"`
		Findings []findingBrief   `json:"findings"`
	}{
		Task: "Develop this Idea into one falsifiable, bounded, queue-ready Plan. Price expected utility and constrained pool-hours. Reuse only supplied IDs and do not invent evidence. Return the exact requested JSON schema.",
		Idea: idea, IdeaBody: strings.TrimSpace(document.Body), Policy: policy, Pools: pools, Findings: findings,
	}
	return json.MarshalIndent(payload, "", "  ")
}

const ideaPlanJSONSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object","additionalProperties":false,
  "required":["schema_version","plan_title","body","priority","effort","expected_payoff","resources","dependencies","utility","rationale"],
  "properties":{
    "schema_version":{"const":"exp.agent.idea-plan/v1"},
    "plan_title":{"type":"string"},"body":{"type":"string"},
    "priority":{"enum":["P1","P2","P3","P?"]},"effort":{"enum":["S","M","L","XL"]},
    "expected_payoff":{"type":"object","additionalProperties":false,"required":["summary","metric","unit"],"properties":{"summary":{"type":"string"},"metric":{"type":"string"},"unit":{"type":"string"},"estimate":{"type":"number"}}},
    "resources":{"type":"array","minItems":1,"maxItems":1,"items":{"type":"object","additionalProperties":false,"required":["pool","units","estimated_hours"],"properties":{"pool":{"type":"string"},"units":{"type":"integer","minimum":1},"estimated_hours":{"type":"number","exclusiveMinimum":0}}}},
    "dependencies":{"type":"array","items":{"type":"string"},"uniqueItems":true},
    "utility":{"type":"object","additionalProperties":false,"required":["probability","impact","information_gain","unblock_value","risk_penalty"],"properties":{"probability":{"type":"number","minimum":0,"maximum":1},"impact":{"type":"number","minimum":0},"information_gain":{"type":"number","minimum":0},"unblock_value":{"type":"number","minimum":0},"risk_penalty":{"type":"number","minimum":0}}},
    "rationale":{"type":"string"}
  }
}`
