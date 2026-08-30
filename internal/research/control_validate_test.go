package research

import (
	"testing"
	"time"
)

func TestControlValidationEnforcesCombinationAndBattleGates(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	release := &Release{
		Common: Common{Schema: SchemaRelease, ID: mustID(t, "rel_01a01e62-0000-7001-8000-000000000001"), Title: "Combined", CreatedAt: now, UpdatedAt: now},
		Target: "production", Version: "v1", State: ReleaseDraft,
		Slots: []ReleaseSlot{
			{Name: "signal", Candidate: mustID(t, "cand_01a01e62-0000-7002-8000-000000000002")},
			{Name: "risk", Candidate: mustID(t, "cand_01a01e62-0000-7003-8000-000000000003")},
		},
	}
	if err := Validate(release); !hasIssueCode(err, "release.combination") {
		t.Fatalf("unvalidated Candidate combination accepted: %v", err)
	}

	battle := &Battle{
		Common: Common{Schema: SchemaBattle, ID: mustID(t, "battle_01a01e62-0000-7004-8000-000000000004"), Title: "Battle", CreatedAt: now, UpdatedAt: now},
		Queue:  mustID(t, "queue_01a01e62-0000-7005-8000-000000000005"), QueueRevision: 1,
		CandidatePlan: mustID(t, "plan_01a01e62-0000-7006-8000-000000000006"),
		IncumbentPlan: mustID(t, "plan_01a01e62-0000-7007-8000-000000000007"),
		Pool:          mustID(t, "pool_01a01e62-0000-7008-8000-000000000008"), Lane: LaneExplore,
		OrderAB: BattleChooseCandidate, OrderBA: BattleChooseIncumbent,
		Outcome: BattleCandidateWins, Confidence: .5, Rationale: "Order-sensitive result.",
	}
	if err := Validate(battle); !hasIssueCode(err, "battle.disagreement") {
		t.Fatalf("order-swapped disagreement bypassed review: %v", err)
	}
}

func TestLegacyPlanRejectsV2FieldsAndSaturatedClusterNeedsReopenCondition(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	plan := &Plan{
		Common:   Common{Schema: SchemaPlan, ID: mustID(t, "plan_01a01e62-0000-7010-8000-000000000010"), Title: "Legacy", CreatedAt: now, UpdatedAt: now},
		Priority: PriorityP1, Effort: EffortS, State: PlanQueued,
		ExpectedPayoff: ExpectedPayoff{Summary: "Improve", Metric: "score", Unit: "score"},
		PrimaryCluster: "encoder",
	}
	if err := Validate(plan); !hasIssueCode(err, "record.schema_field") {
		t.Fatalf("v1 Plan accepted a v2 field: %v", err)
	}

	policy := &Policy{
		Schema: SchemaPolicy, CreatedAt: now, UpdatedAt: now,
		Autonomy: AutonomyManual, ExploitShare: .8, ExploreShare: .2,
		ScoreFormula: "utility-v1", TiePolicy: QueueTieKeepIncumbent, PromotionRequiresHuman: true,
		Taxonomy:          ClassificationTaxonomy{Domains: []string{"ml"}, Work: []string{"training"}, Methods: []string{"ablation"}, Components: []string{"encoder"}},
		ClusterSaturation: ClusterSaturationPolicy{BudgetHours: 10, PlateauWindow: 3, MinimumImprovement: .01, MinimumProbability: .1},
		Clusters: []ClusterPolicy{{
			Name: "encoder", State: ClusterSaturated, BudgetHours: 10, PlateauWindow: 3, MinimumImprovement: .01, MinimumProbability: .1,
		}},
	}
	if err := Validate(policy); !hasIssueCode(err, "policy.reopen_condition") {
		t.Fatalf("saturated cluster without reopen condition accepted: %v", err)
	}
}
