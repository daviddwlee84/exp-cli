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
	candidates := make([]research.Candidate, 0, len(inventory.Documents))
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
		candidates = append(candidates, research.Candidate{ID: id, Aliases: aliases})
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
		research.KindPlan,
		research.KindExperiment,
		research.KindRun,
		research.KindAttempt,
		research.KindFinding,
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

	for _, document := range view.documents(research.KindPlan) {
		plan := document.Record.(*research.Plan)
		if !plan.ResultingExperiment.IsZero() {
			add(plan.ID, plan.ResultingExperiment)
		}
		for _, assumption := range sortedIDs(plan.Assumptions) {
			add(assumption, plan.ID)
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
		if experiment.ClosureDetail != nil {
			add(experiment.ID, experiment.ClosureDetail.SupersededBy)
		}
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
	case research.KindPlan:
		return "Plan"
	case research.KindExperiment:
		return "Experiment"
	case research.KindRun:
		return "Run"
	case research.KindAttempt:
		return "Attempt"
	case research.KindFinding:
		return "Finding"
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
