package projection

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

type inventoryView struct {
	inventory *record.Inventory
	codes     map[research.ID]string
	nodes     map[research.ID]string
}

func newView(inventory *record.Inventory) (*inventoryView, error) {
	candidates := make([]research.ReferenceCandidate, 0, len(inventory.Documents))
	for _, document := range inventory.Documents {
		id, ok := document.ID()
		if !ok {
			continue
		}
		common := document.Record.GetCommon()
		aliases := []string{}
		if common != nil {
			aliases = append(aliases, common.LegacyAliases...)
		}
		candidates = append(candidates, research.ReferenceCandidate{ID: id, Aliases: aliases})
	}

	view := &inventoryView{
		inventory: inventory,
		codes:     make(map[research.ID]string, len(candidates)),
		nodes:     make(map[research.ID]string, len(candidates)),
	}
	for _, candidate := range candidates {
		code, err := research.DisplayCode(candidate.ID, candidates)
		if err != nil {
			return nil, fmt.Errorf("allocate display code for %s: %w", candidate.ID, err)
		}
		view.codes[candidate.ID] = code
		view.nodes[candidate.ID] = strings.ToLower(code[:1]) + "_" + strings.ToLower(strings.TrimPrefix(code, code[:2]))
	}
	return view, nil
}

func (view *inventoryView) readme() string {
	project := view.inventory.Project.Record.(*research.Project)
	var output strings.Builder
	output.WriteString(generatedHeader)
	fmt.Fprintf(&output, "\n# %s\n\n", project.Name)
	fmt.Fprintf(&output, "Project `%s`\n\n", project.ProjectID)
	output.WriteString("## Inventory\n\n")
	output.WriteString("| Plans | Experiments | Runs | Attempts | Findings | Decisions |\n")
	output.WriteString("|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&output, "| %d | %d | %d | %d | %d | %d |\n",
		len(view.inventory.OfKind(research.KindPlan)),
		len(view.inventory.OfKind(research.KindExperiment)),
		len(view.inventory.OfKind(research.KindRun)),
		len(view.inventory.OfKind(research.KindAttempt)),
		len(view.inventory.OfKind(research.KindFinding)),
		len(view.inventory.OfKind(research.KindDecision)),
	)
	controlKinds := []research.Kind{
		research.KindIdea, research.KindResourcePool, research.KindQueue,
		research.KindQueueAdvice, research.KindBattle, research.KindEvaluationSpec,
		research.KindEvaluation, research.KindCandidate, research.KindRelease,
		research.KindPromotionSpec, research.KindPromotion,
	}
	controlCount := 0
	for _, kind := range controlKinds {
		controlCount += len(view.inventory.OfKind(kind))
	}
	if controlCount > 0 || view.inventory.Policy != nil {
		output.WriteString("\n## Research control plane\n\n")
		output.WriteString("| Policy | Ideas | Pools | Queues | Advice | Battles | Evaluations | Candidates | Releases | Promotions |\n")
		output.WriteString("|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		policyCount := 0
		if view.inventory.Policy != nil {
			policyCount = 1
		}
		fmt.Fprintf(&output, "| %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			policyCount,
			len(view.inventory.OfKind(research.KindIdea)),
			len(view.inventory.OfKind(research.KindResourcePool)),
			len(view.inventory.OfKind(research.KindQueue)),
			len(view.inventory.OfKind(research.KindQueueAdvice)),
			len(view.inventory.OfKind(research.KindBattle)),
			len(view.inventory.OfKind(research.KindEvaluation)),
			len(view.inventory.OfKind(research.KindCandidate)),
			len(view.inventory.OfKind(research.KindRelease)),
			len(view.inventory.OfKind(research.KindPromotion)),
		)
	}

	output.WriteString("\n## Experiments\n\n")
	experiments := view.documents(research.KindExperiment)
	sort.SliceStable(experiments, func(left, right int) bool {
		leftRecord := experiments[left].Record.(*research.Experiment)
		rightRecord := experiments[right].Record.(*research.Experiment)
		if leftRecord.Lifecycle != rightRecord.Lifecycle {
			return lifecycleOrder(leftRecord.Lifecycle) < lifecycleOrder(rightRecord.Lifecycle)
		}
		return leftRecord.ID.String() < rightRecord.ID.String()
	})
	if len(experiments) == 0 {
		output.WriteString("_No experiments._\n")
	} else {
		output.WriteString("| Experiment | Lifecycle | Closure | Verdict | Title |\n")
		output.WriteString("|---|---|---|---|---|\n")
		for _, document := range experiments {
			experiment := document.Record.(*research.Experiment)
			fmt.Fprintf(&output, "| [%s](%s) | %s | %s | %s | %s |\n",
				view.codes[experiment.ID], document.Path,
				tableCell(string(experiment.Lifecycle)),
				tableCell(orDash(string(experiment.Closure))),
				tableCell(orDash(string(experiment.Verdict))),
				tableCell(experiment.Title),
			)
		}
	}

	output.WriteString("\n## Research graph\n\n")
	output.WriteString("```mermaid\nflowchart LR\n")
	for _, kind := range []research.Kind{
		research.KindIdea,
		research.KindResourcePool,
		research.KindQueue,
		research.KindQueueAdvice,
		research.KindBattle,
		research.KindPlan,
		research.KindExperiment,
		research.KindRun,
		research.KindAttempt,
		research.KindEvaluationSpec,
		research.KindEvaluation,
		research.KindFinding,
		research.KindCandidate,
		research.KindRelease,
		research.KindPromotionSpec,
		research.KindPromotion,
		research.KindDecision,
	} {
		for _, document := range view.documents(kind) {
			id, _ := document.ID()
			fmt.Fprintf(&output, "  %s[\"%s %s\"]\n", view.nodes[id], view.codes[id], graphKind(kind))
		}
	}
	for _, edge := range view.graphEdges() {
		fmt.Fprintf(&output, "  %s --> %s\n", view.nodes[edge.from], view.nodes[edge.to])
	}
	output.WriteString("```\n")
	return output.String()
}

func (view *inventoryView) roadmap() string {
	plans := view.documents(research.KindPlan)
	sort.SliceStable(plans, func(left, right int) bool {
		leftRecord := plans[left].Record.(*research.Plan)
		rightRecord := plans[right].Record.(*research.Plan)
		if leftRecord.State != rightRecord.State {
			return planStateOrder(leftRecord.State) < planStateOrder(rightRecord.State)
		}
		if leftRecord.Priority != rightRecord.Priority {
			return priorityOrder(leftRecord.Priority) < priorityOrder(rightRecord.Priority)
		}
		return leftRecord.ID.String() < rightRecord.ID.String()
	})

	var output strings.Builder
	output.WriteString(generatedHeader)
	output.WriteString("\n# Roadmap\n")
	frontier := view.inventory.QueueFrontier()
	if len(frontier) > 0 {
		output.WriteString("\n## Canonical queue frontier\n\n")
		output.WriteString("| Queue | Pool | Lane | Plan | Score |\n")
		output.WriteString("|---|---|---|---|---:|\n")
		for _, entry := range frontier {
			queueDocument, _ := view.inventory.ByID(entry.Queue)
			poolDocument, _ := view.inventory.ByID(entry.Pool)
			planDocument, _ := view.inventory.ByID(entry.Entry.Plan)
			fmt.Fprintf(&output, "| [%s](%s) | [%s](%s) | %s | [%s](%s) | %s |\n",
				view.codes[entry.Queue], queueDocument.Path,
				view.codes[entry.Pool], poolDocument.Path,
				entry.Lane, view.codes[entry.Entry.Plan], planDocument.Path,
				strconv.FormatFloat(entry.Entry.Score, 'g', -1, 64),
			)
		}
	}
	if len(plans) == 0 {
		output.WriteString("\n_No plans._\n")
		return output.String()
	}
	for _, state := range []research.PlanState{research.PlanQueued, research.PlanStarted, research.PlanCompleted, research.PlanDropped} {
		var group []*record.Document
		for _, document := range plans {
			if document.Record.(*research.Plan).State == state {
				group = append(group, document)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&output, "\n## %s\n\n", planStateHeading(state))
		output.WriteString("| Plan | Priority | Effort | Expected payoff | Result |\n")
		output.WriteString("|---|---|---|---|---|\n")
		for _, document := range group {
			plan := document.Record.(*research.Plan)
			result := "—"
			if !plan.ResultingExperiment.IsZero() {
				if target, err := view.inventory.ByID(plan.ResultingExperiment); err == nil {
					result = fmt.Sprintf("[%s](%s)", view.codes[plan.ResultingExperiment], target.Path)
				}
			}
			fmt.Fprintf(&output, "| [%s](%s) %s | %s | %s | %s | %s |\n",
				view.codes[plan.ID], document.Path, tableCell(plan.Title),
				tableCell(string(plan.Priority)), tableCell(string(plan.Effort)),
				tableCell(expectedPayoff(plan.ExpectedPayoff)), result,
			)
		}
	}
	return output.String()
}

func (view *inventoryView) ledger() string {
	findings := view.documents(research.KindFinding)
	sort.SliceStable(findings, func(left, right int) bool {
		leftRecord := findings[left].Record.(*research.Finding)
		rightRecord := findings[right].Record.(*research.Finding)
		if !leftRecord.CreatedAt.Equal(rightRecord.CreatedAt) {
			return leftRecord.CreatedAt.Before(rightRecord.CreatedAt)
		}
		return leftRecord.ID.String() < rightRecord.ID.String()
	})
	statuses := view.findingStatuses()

	var output strings.Builder
	output.WriteString(generatedHeader)
	output.WriteString("\n# Findings ledger\n")
	if len(findings) == 0 {
		output.WriteString("\n_No findings._\n")
		return output.String()
	}
	output.WriteString("\n| Finding | Status | Statement | Evidence |\n")
	output.WriteString("|---|---|---|---|\n")
	for _, document := range findings {
		finding := document.Record.(*research.Finding)
		evidence := make([]string, 0, len(finding.Evidence))
		for _, item := range finding.Evidence {
			if target, err := view.inventory.ByID(item.Ref); err == nil {
				evidence = append(evidence, fmt.Sprintf("[%s](%s)", view.codes[item.Ref], target.Path))
			}
		}
		fmt.Fprintf(&output, "| [%s](%s) | %s | %s | %s |\n",
			view.codes[finding.ID], document.Path, statuses[finding.ID],
			tableCell(finding.Statement), strings.Join(evidence, "<br>"),
		)
	}
	return output.String()
}

func (view *inventoryView) decisions() string {
	decisions := view.documents(research.KindDecision)
	sort.SliceStable(decisions, func(left, right int) bool {
		leftRecord := decisions[left].Record.(*research.Decision)
		rightRecord := decisions[right].Record.(*research.Decision)
		if !leftRecord.CreatedAt.Equal(rightRecord.CreatedAt) {
			return leftRecord.CreatedAt.Before(rightRecord.CreatedAt)
		}
		return leftRecord.ID.String() < rightRecord.ID.String()
	})
	statuses := view.decisionStatuses()

	var output strings.Builder
	output.WriteString(generatedHeader)
	output.WriteString("\n# Decisions\n")
	champions, _ := view.inventory.CurrentChampions()
	if len(champions) > 0 {
		output.WriteString("\n## Current champions\n\n")
		output.WriteString("| Target | Release | Promotion |\n")
		output.WriteString("|---|---|---|\n")
		for _, champion := range champions {
			release, _ := view.inventory.ByID(champion.Release)
			promotion, _ := view.inventory.ByID(champion.Promotion)
			fmt.Fprintf(&output, "| %s | [%s](%s) | [%s](%s) |\n",
				tableCell(champion.Target), view.codes[champion.Release], release.Path,
				view.codes[champion.Promotion], promotion.Path)
		}
	}
	if len(decisions) == 0 {
		output.WriteString("\n_No decisions._\n")
		return output.String()
	}
	output.WriteString("\n| Decision | Status | Statement | Based on | Action |\n")
	output.WriteString("|---|---|---|---|---|\n")
	for _, document := range decisions {
		decision := document.Record.(*research.Decision)
		basedOn := make([]string, 0, len(decision.BasedOn))
		for _, id := range sortedIDs(decision.BasedOn) {
			if target, err := view.inventory.ByID(id); err == nil {
				basedOn = append(basedOn, fmt.Sprintf("[%s](%s)", view.codes[id], target.Path))
			}
		}
		fmt.Fprintf(&output, "| [%s](%s) | %s | %s | %s | %s |\n",
			view.codes[decision.ID], document.Path, statuses[decision.ID],
			tableCell(decision.Statement), strings.Join(basedOn, "<br>"), tableCell(decision.Action),
		)
	}
	return output.String()
}

func (view *inventoryView) documents(kind research.Kind) []*record.Document {
	return append([]*record.Document(nil), view.inventory.OfKind(kind)...)
}

type graphEdge struct {
	from research.ID
	to   research.ID
}

func (view *inventoryView) graphEdges() []graphEdge {
	var edges []graphEdge
	seen := make(map[string]struct{})
	add := func(from, to research.ID) {
		if from.IsZero() || to.IsZero() || view.nodes[from] == "" || view.nodes[to] == "" {
			return
		}
		key := from.String() + "\x00" + to.String()
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		edges = append(edges, graphEdge{from: from, to: to})
	}
	for _, document := range view.documents(research.KindIdea) {
		idea := document.Record.(*research.Idea)
		for _, parent := range sortedIDs(idea.Parents) {
			add(parent, idea.ID)
		}
		add(idea.ID, idea.ResultingPlan)
		add(idea.ID, idea.MergedInto)
	}
	for _, document := range view.documents(research.KindQueue) {
		queue := document.Record.(*research.Queue)
		for _, partition := range queue.Partitions {
			add(partition.Pool, queue.ID)
			for _, entry := range partition.Entries {
				add(entry.Plan, queue.ID)
			}
		}
	}
	for _, document := range view.documents(research.KindQueueAdvice) {
		advice := document.Record.(*research.QueueAdvice)
		add(advice.Queue, advice.ID)
		add(advice.CandidatePlan, advice.ID)
		add(advice.Pool, advice.ID)
	}
	for _, document := range view.documents(research.KindBattle) {
		battle := document.Record.(*research.Battle)
		add(battle.Advice, battle.ID)
		add(battle.CandidatePlan, battle.ID)
		add(battle.IncumbentPlan, battle.ID)
	}

	for _, document := range view.documents(research.KindPlan) {
		plan := document.Record.(*research.Plan)
		if !plan.ResultingExperiment.IsZero() {
			add(plan.ID, plan.ResultingExperiment)
		}
		for _, assumption := range sortedIDs(plan.Assumptions) {
			add(assumption, plan.ID)
		}
		for _, dependency := range plan.Dependencies {
			add(dependency.Finding, plan.ID)
		}
	}
	for _, document := range view.documents(research.KindRun) {
		run := document.Record.(*research.Run)
		add(run.Experiment, run.ID)
	}
	for _, document := range view.documents(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		add(attempt.Run, attempt.ID)
	}
	for _, document := range view.documents(research.KindExperiment) {
		experiment := document.Record.(*research.Experiment)
		for _, parent := range sortedIDs(experiment.Parents) {
			add(parent, experiment.ID)
		}
		for _, candidate := range sortedIDs(experiment.CandidateInputs) {
			add(candidate, experiment.ID)
		}
		if experiment.ClosureDetail != nil {
			add(experiment.ID, experiment.ClosureDetail.SupersededBy)
		}
	}
	for _, document := range view.documents(research.KindEvaluation) {
		evaluation := document.Record.(*research.Evaluation)
		add(evaluation.Spec, evaluation.ID)
		add(evaluation.Subject, evaluation.ID)
	}
	for _, document := range view.documents(research.KindEvaluationSpec) {
		spec := document.Record.(*research.EvaluationSpec)
		add(spec.BudgetPool, spec.ID)
	}
	for _, document := range view.documents(research.KindCandidate) {
		candidate := document.Record.(*research.Candidate)
		add(candidate.Experiment, candidate.ID)
		add(candidate.Evaluation, candidate.ID)
		for _, parent := range sortedIDs(candidate.Parents) {
			add(parent, candidate.ID)
		}
	}
	for _, document := range view.documents(research.KindRelease) {
		release := document.Record.(*research.Release)
		for _, slot := range release.Slots {
			add(slot.Candidate, release.ID)
		}
		add(release.CombinationExperiment, release.ID)
		add(release.CombinationEvaluation, release.ID)
		add(release.Evaluation, release.ID)
	}
	for _, document := range view.documents(research.KindPromotionSpec) {
		spec := document.Record.(*research.PromotionSpec)
		add(spec.EvaluationSpec, spec.ID)
	}
	for _, document := range view.documents(research.KindPromotion) {
		promotion := document.Record.(*research.Promotion)
		add(promotion.Spec, promotion.ID)
		add(promotion.Previous, promotion.ID)
		add(promotion.Challenger, promotion.ID)
		add(promotion.Evaluation, promotion.ID)
	}
	for _, document := range view.documents(research.KindFinding) {
		finding := document.Record.(*research.Finding)
		for _, evidence := range finding.Evidence {
			add(evidence.Ref, finding.ID)
		}
		for _, target := range sortedIDs(finding.Weakens) {
			add(finding.ID, target)
		}
		for _, target := range sortedIDs(finding.Overturns) {
			add(finding.ID, target)
		}
	}
	for _, document := range view.documents(research.KindDecision) {
		decision := document.Record.(*research.Decision)
		for _, source := range sortedIDs(decision.BasedOn) {
			add(source, decision.ID)
		}
		for _, target := range sortedIDs(decision.Supersedes) {
			add(decision.ID, target)
		}
	}
	return edges
}

func (view *inventoryView) findingStatuses() map[research.ID]string {
	statuses := make(map[research.ID]string)
	for _, document := range view.documents(research.KindFinding) {
		finding := document.Record.(*research.Finding)
		statuses[finding.ID] = "active"
	}
	for _, document := range view.documents(research.KindFinding) {
		finding := document.Record.(*research.Finding)
		for _, target := range finding.Weakens {
			if statuses[target] == "active" {
				statuses[target] = "weakened"
			}
		}
		for _, target := range finding.Overturns {
			statuses[target] = "overturned"
		}
	}
	return statuses
}

func (view *inventoryView) decisionStatuses() map[research.ID]string {
	statuses := make(map[research.ID]string)
	for _, document := range view.documents(research.KindDecision) {
		decision := document.Record.(*research.Decision)
		statuses[decision.ID] = "current"
	}
	for _, document := range view.documents(research.KindDecision) {
		decision := document.Record.(*research.Decision)
		for _, target := range decision.Supersedes {
			statuses[target] = "superseded"
		}
	}
	return statuses
}

func expectedPayoff(payoff research.ExpectedPayoff) string {
	parts := make([]string, 0, 3)
	if payoff.Estimate != nil {
		parts = append(parts, strconv.FormatFloat(*payoff.Estimate, 'g', -1, 64))
	}
	parts = append(parts, payoff.Metric, payoff.Unit)
	return strings.Join(parts, " ") + " — " + payoff.Summary
}

func tableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "|", `\|`)
	return strings.ReplaceAll(value, "\n", "<br>")
}

func orDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func lifecycleOrder(value research.ExperimentLifecycle) int {
	switch value {
	case research.LifecyclePlanned:
		return 0
	case research.LifecycleActive:
		return 1
	case research.LifecycleClosed:
		return 2
	default:
		return 3
	}
}

func planStateOrder(value research.PlanState) int {
	switch value {
	case research.PlanQueued:
		return 0
	case research.PlanStarted:
		return 1
	case research.PlanCompleted:
		return 2
	case research.PlanDropped:
		return 3
	default:
		return 4
	}
}

func priorityOrder(value research.Priority) int {
	switch value {
	case research.PriorityP1:
		return 0
	case research.PriorityP2:
		return 1
	case research.PriorityP3:
		return 2
	case research.PriorityUnknown:
		return 3
	default:
		return 4
	}
}

func planStateHeading(value research.PlanState) string {
	switch value {
	case research.PlanQueued:
		return "Queued"
	case research.PlanStarted:
		return "Started"
	case research.PlanCompleted:
		return "Completed"
	case research.PlanDropped:
		return "Dropped"
	default:
		return string(value)
	}
}

func graphKind(kind research.Kind) string {
	switch kind {
	case research.KindIdea:
		return "Idea"
	case research.KindResourcePool:
		return "Pool"
	case research.KindQueue:
		return "Queue"
	case research.KindQueueAdvice:
		return "Advice"
	case research.KindBattle:
		return "Battle"
	case research.KindPlan:
		return "Plan"
	case research.KindExperiment:
		return "Experiment"
	case research.KindRun:
		return "Run"
	case research.KindAttempt:
		return "Attempt"
	case research.KindEvaluationSpec:
		return "EvalSpec"
	case research.KindEvaluation:
		return "Evaluation"
	case research.KindFinding:
		return "Finding"
	case research.KindCandidate:
		return "Candidate"
	case research.KindRelease:
		return "Release"
	case research.KindPromotionSpec:
		return "PromotionSpec"
	case research.KindPromotion:
		return "Promotion"
	case research.KindDecision:
		return "Decision"
	default:
		return "Record"
	}
}

func sortedIDs(values []research.ID) []research.ID {
	out := append([]research.ID(nil), values...)
	sort.Slice(out, func(left, right int) bool { return out[left].String() < out[right].String() })
	return out
}
