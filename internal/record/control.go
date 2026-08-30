package record

import (
	"fmt"
	"sort"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

// FrontierEntry is the next dispatchable canonical queue entry for one
// pool/lane partition. Running work is intentionally not represented here.
type FrontierEntry struct {
	Queue    research.ID
	Pool     research.ID
	Lane     research.ResearchLane
	Position uint64
	Entry    research.QueueEntry
}

func (inventory *Inventory) QueueFrontier() []FrontierEntry {
	if inventory == nil {
		return nil
	}
	var frontier []FrontierEntry
	for _, document := range inventory.OfKind(research.KindQueue) {
		queue := document.Record.(*research.Queue)
		if queue.Paused {
			continue
		}
		for _, partition := range queue.Partitions {
			if len(partition.Entries) == 0 {
				continue
			}
			frontier = append(frontier, FrontierEntry{
				Queue: queue.ID, Pool: partition.Pool, Lane: partition.Lane,
				Position: 0, Entry: partition.Entries[0],
			})
		}
	}
	sort.Slice(frontier, func(i, j int) bool {
		if frontier[i].Queue != frontier[j].Queue {
			return frontier[i].Queue.String() < frontier[j].Queue.String()
		}
		if frontier[i].Pool != frontier[j].Pool {
			return frontier[i].Pool.String() < frontier[j].Pool.String()
		}
		return frontier[i].Lane < frontier[j].Lane
	})
	return frontier
}

type Champion struct {
	Target    string
	Release   research.ID
	Promotion research.ID
}

// CurrentChampions derives downstream release pointers solely from the
// append-only Promotion chains. No generated manifest is read as authority.
func (inventory *Inventory) CurrentChampions() ([]Champion, error) {
	if inventory == nil || !inventory.Valid() {
		if inventory == nil {
			return nil, fmt.Errorf("derive champions from nil inventory")
		}
		return nil, inventory.Error()
	}
	roots := map[string]*research.Promotion{}
	followers := map[research.ID]*research.Promotion{}
	for _, document := range inventory.OfKind(research.KindPromotion) {
		promotion := document.Record.(*research.Promotion)
		if promotion.Previous.IsZero() {
			roots[promotion.Target] = promotion
		} else {
			followers[promotion.Previous] = promotion
		}
	}
	targets := make([]string, 0, len(roots))
	for target := range roots {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	champions := make([]Champion, 0, len(targets))
	for _, target := range targets {
		current := roots[target]
		var release research.ID
		var promotionID research.ID
		for current != nil {
			if current.Outcome == research.PromotionAccepted || current.Outcome == research.PromotionRolledBack {
				release = current.Challenger
				promotionID = current.ID
			}
			current = followers[current.ID]
		}
		if !release.IsZero() {
			champions = append(champions, Champion{Target: target, Release: release, Promotion: promotionID})
		}
	}
	return champions, nil
}

type ResearchEdge struct {
	From     research.ID
	To       research.ID
	Relation string
}

// ResearchEdges projects the complete typed research DAG in deterministic
// order. Relationship owners remain the canonical records themselves.
func (inventory *Inventory) ResearchEdges() []ResearchEdge {
	if inventory == nil {
		return nil
	}
	var edges []ResearchEdge
	seen := map[string]struct{}{}
	add := func(from, to research.ID, relation string) {
		if from.IsZero() || to.IsZero() {
			return
		}
		key := from.String() + "\x00" + to.String() + "\x00" + relation
		if _, found := seen[key]; found {
			return
		}
		seen[key] = struct{}{}
		edges = append(edges, ResearchEdge{From: from, To: to, Relation: relation})
	}
	for _, document := range inventory.Documents {
		switch value := document.Record.(type) {
		case *research.Idea:
			for _, parent := range value.Parents {
				add(parent, value.ID, "derives")
			}
			add(value.ID, value.ResultingPlan, "qualifies")
			add(value.ID, value.MergedInto, "merges")
		case *research.Queue:
			for _, partition := range value.Partitions {
				add(partition.Pool, value.ID, "partitions")
				for _, entry := range partition.Entries {
					add(entry.Plan, value.ID, "queued")
				}
			}
		case *research.QueueAdvice:
			add(value.Queue, value.ID, "advises")
			add(value.CandidatePlan, value.ID, "ranks")
			add(value.Pool, value.ID, "prices")
		case *research.Battle:
			add(value.Advice, value.ID, "tests")
			add(value.CandidatePlan, value.ID, "candidate")
			add(value.IncumbentPlan, value.ID, "incumbent")
		case *research.Plan:
			for _, assumption := range value.Assumptions {
				add(assumption, value.ID, "assumes")
			}
			for _, dependency := range value.Dependencies {
				add(dependency.Finding, value.ID, "depends")
			}
			add(value.ID, value.ResultingExperiment, "starts")
		case *research.Experiment:
			for _, parent := range value.Parents {
				add(parent, value.ID, "follows")
			}
			for _, candidate := range value.CandidateInputs {
				add(candidate, value.ID, "combines")
			}
			if value.ClosureDetail != nil {
				add(value.ID, value.ClosureDetail.SupersededBy, "superseded_by")
			}
		case *research.Run:
			add(value.Experiment, value.ID, "contains")
		case *research.Attempt:
			add(value.Run, value.ID, "attempts")
		case *research.Evaluation:
			add(value.Spec, value.ID, "evaluates_with")
			add(value.Subject, value.ID, "evaluated_by")
		case *research.EvaluationSpec:
			add(value.BudgetPool, value.ID, "budgeted_by")
		case *research.Finding:
			for _, evidence := range value.Evidence {
				add(evidence.Ref, value.ID, "supports")
			}
			for _, target := range value.Weakens {
				add(value.ID, target, "weakens")
			}
			for _, target := range value.Overturns {
				add(value.ID, target, "overturns")
			}
		case *research.Candidate:
			add(value.Experiment, value.ID, "produces")
			add(value.Evaluation, value.ID, "scientific_evidence")
			for _, parent := range value.Parents {
				add(parent, value.ID, "follows")
			}
		case *research.Release:
			for _, slot := range value.Slots {
				add(slot.Candidate, value.ID, "fills:"+slot.Name)
			}
			add(value.CombinationExperiment, value.ID, "validates_combination")
			add(value.CombinationEvaluation, value.ID, "combination_evidence")
			add(value.Evaluation, value.ID, "release_evidence")
		case *research.PromotionSpec:
			add(value.EvaluationSpec, value.ID, "seals")
		case *research.Promotion:
			add(value.Spec, value.ID, "governs")
			add(value.Previous, value.ID, "next_promotion")
			add(value.Challenger, value.ID, "challenges")
			add(value.Evaluation, value.ID, "promotion_evidence")
		case *research.Decision:
			for _, finding := range value.BasedOn {
				add(finding, value.ID, "informs")
			}
			for _, target := range value.Supersedes {
				add(value.ID, target, "supersedes")
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From.String() < edges[j].From.String()
		}
		if edges[i].To != edges[j].To {
			return edges[i].To.String() < edges[j].To.String()
		}
		return edges[i].Relation < edges[j].Relation
	})
	return edges
}
