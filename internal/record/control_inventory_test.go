package record

import (
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestControlV2FixtureLoadsAsValidInventory(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "v2", "control-project"))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Valid() {
		t.Fatalf("v2 fixture invalid: %v", inventory.Diagnostics)
	}
	if inventory.Policy == nil || len(inventory.OfKind(research.KindResourcePool)) != 1 || len(inventory.OfKind(research.KindPlan)) != 1 || len(inventory.OfKind(research.KindQueue)) != 1 {
		t.Fatalf("v2 fixture inventory incomplete: %#v", inventory)
	}
}

func TestControlInventoryValidatesPinnedQueueAndProjectsFrontier(t *testing.T) {
	now := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	projectID, err := research.ParseUUID("01a01e60-0000-7001-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	project := &Document{Path: ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Control plane",
		CreatedAt: now, ExperimentsRoot: ".",
	}}
	policy := &Document{Path: PolicyFile, Body: "# Policy\n", Record: testControlPolicy(now)}
	poolID := mustControlID(t, "pool_01a01e60-0000-7002-8000-000000000002")
	pool := &Document{Body: "# Pool\n", Record: &research.ResourcePool{
		Common:  research.Common{Schema: research.SchemaResourcePool, ID: poolID, Title: "GPU", CreatedAt: now, UpdatedAt: now},
		Enabled: true, Capacity: 1, Unit: "gpu", Bottleneck: "gpu",
	}}
	pool.Path, err = PathForNew(pool.Record, nil)
	if err != nil {
		t.Fatal(err)
	}
	planID := mustControlID(t, "plan_01a01e60-0000-7003-8000-000000000003")
	plan := &Document{Body: "# Plan\n", Record: &research.Plan{
		Common:   research.Common{Schema: research.SchemaPlanV2, ID: planID, Title: "Ablate encoder", CreatedAt: now, UpdatedAt: now},
		Priority: research.PriorityP1, Effort: research.EffortS, State: research.PlanQueued,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve score", Metric: "macro_f1", Unit: "score"},
		PrimaryCluster: "encoder", Classification: &research.Classification{
			Domain: "ml", Work: "training", Method: "ablation", Component: "encoder",
			Lane: research.LaneExploit, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman,
		},
		Resources: []research.ResourceNeed{{Pool: poolID, Units: 1, EstimatedHours: 2}},
		Utility:   &research.UtilityEstimate{Probability: .5, Impact: .1, InformationGain: .2, UnblockValue: .1, RiskPenalty: .05},
	}}
	plan.Path, err = PathForNew(plan.Record, nil)
	if err != nil {
		t.Fatal(err)
	}
	planRevision, err := Revision(plan)
	if err != nil {
		t.Fatal(err)
	}
	queueID := mustControlID(t, "queue_01a01e60-0000-7004-8000-000000000004")
	queue := &Document{Body: "# Queue\n", Record: &research.Queue{
		Common:   research.Common{Schema: research.SchemaQueue, ID: queueID, Title: "Main queue", CreatedAt: now, UpdatedAt: now},
		Revision: 1,
		Partitions: []research.QueuePartition{{Pool: poolID, Lane: research.LaneExploit, Entries: []research.QueueEntry{{
			Plan: planID, PlanRevision: planRevision, Score: .7, InsertedAt: now,
		}}}},
	}}
	queue.Path, err = PathForNew(queue.Record, nil)
	if err != nil {
		t.Fatal(err)
	}

	inventory := InventoryFromDocuments(t.TempDir(), []*Document{project, policy, pool, plan, queue})
	if !inventory.Valid() {
		t.Fatalf("control inventory invalid: %v", inventory.Diagnostics)
	}
	frontier := inventory.QueueFrontier()
	if len(frontier) != 1 || frontier[0].Entry.Plan != planID || frontier[0].Queue != queueID {
		t.Fatalf("frontier = %#v", frontier)
	}
	edges := inventory.ResearchEdges()
	foundQueued := false
	for _, edge := range edges {
		foundQueued = foundQueued || edge.From == planID && edge.To == queueID && edge.Relation == "queued"
	}
	if !foundQueued {
		t.Fatalf("queue edge missing: %#v", edges)
	}

	stale := queue.Clone()
	stale.Record.(*research.Queue).Partitions[0].Entries[0].PlanRevision = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	inventory = InventoryFromDocuments(t.TempDir(), []*Document{project, policy, pool, plan, stale})
	if !hasDiagnosticCode(inventory.Diagnostics, "queue.plan_stale") {
		t.Fatalf("stale queue pin validated: %v", inventory.Diagnostics)
	}
}

func testControlPolicy(now time.Time) *research.Policy {
	return &research.Policy{
		Schema: research.SchemaPolicy, CreatedAt: now, UpdatedAt: now,
		Autonomy: research.AutonomyManual, ExploitShare: .8, ExploreShare: .2,
		ScoreFormula: "utility-v1", TiePolicy: research.QueueTieKeepIncumbent,
		PromotionRequiresHuman: true,
		Taxonomy: research.ClassificationTaxonomy{
			Domains: []string{"ml"}, Work: []string{"training"}, Methods: []string{"ablation"}, Components: []string{"encoder"},
		},
		ClusterSaturation: research.ClusterSaturationPolicy{
			BudgetHours: 24, PlateauWindow: 5, MinimumImprovement: .01, MinimumProbability: .1,
		},
	}
}

func TestCurrentChampionsDerivesAcceptedPromotionChain(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	evaluatedAt := now.Add(time.Minute)
	promotedAt := evaluatedAt.Add(time.Minute)
	promotionThreshold := .8
	projectID, _ := research.ParseUUID("01a01e61-0000-7001-8000-000000000001")
	poolID := mustControlID(t, "pool_01a01e61-0000-7002-8000-000000000002")
	experimentID := mustControlID(t, "exp_01a01e61-0000-7003-8000-000000000003")
	scientificSpecID := mustControlID(t, "evalspec_01a01e61-0000-7004-8000-000000000004")
	scientificEvaluationID := mustControlID(t, "eval_01a01e61-0000-7005-8000-000000000005")
	candidateID := mustControlID(t, "cand_01a01e61-0000-7006-8000-000000000006")
	releaseID := mustControlID(t, "rel_01a01e61-0000-7007-8000-000000000007")
	promotionEvalSpecID := mustControlID(t, "evalspec_01a01e61-0000-7008-8000-000000000008")
	promotionEvaluationID := mustControlID(t, "eval_01a01e61-0000-7009-8000-000000000009")
	promotionSpecID := mustControlID(t, "promspec_01a01e61-0000-7010-8000-000000000010")
	promotionID := mustControlID(t, "prom_01a01e61-0000-7011-8000-000000000011")
	queueID := mustControlID(t, "queue_01a01e61-0000-7020-8000-000000000020")
	runID := mustControlID(t, "run_01a01e61-0000-7021-8000-000000000021")
	attemptID := mustControlID(t, "att_01a01e61-0000-7022-8000-000000000022")
	common := func(schema research.Schema, id research.ID, title string) research.Common {
		return research.Common{Schema: schema, ID: id, Title: title, CreatedAt: now, UpdatedAt: now}
	}
	documents := []*Document{{
		Path: ProjectFile, Body: "# Project\n", Record: &research.Project{
			Schema: research.SchemaProject, ProjectID: projectID, Name: "Promotion", CreatedAt: now, ExperimentsRoot: ".",
		},
	}}
	appendRecord := func(value research.Record) {
		path, err := PathForNew(value, nil)
		if err != nil {
			t.Fatalf("path for %s: %v", value.GetKind(), err)
		}
		documents = append(documents, &Document{Path: path, Body: "# Record\n", Record: value})
	}
	appendRecord(&research.ResourcePool{Common: common(research.SchemaResourcePool, poolID, "GPU"), Enabled: true, Capacity: 1, Unit: "gpu", Bottleneck: "gpu"})
	locked := now
	design := research.Design{
		Question: "Does it improve?", Hypothesis: "It improves.", Kind: research.ExperimentSingleFactor,
		PrimaryFactor: "encoder", SecondaryFactors: []string{}, Baseline: "champion",
		ComparabilitySpec: "same split", SuccessCriteria: []string{"macro F1 improves"}, DecisionRule: "keep if improved", DesignLockedAt: &locked,
	}
	design.DesignDigest, _ = research.DesignDigest(design)
	appendRecord(&research.Experiment{
		Common: common(research.SchemaExperimentV2, experimentID, "Train candidate"), Lifecycle: research.LifecycleClosed,
		Closure: research.ClosureConcluded, Verdict: research.VerdictSupported, Design: design,
		Conclusion: &research.Conclusion{ConcludedAt: now, Summary: "Candidate passed.", Evidence: []research.ConclusionEvidence{{Run: runID, Disposition: research.EvidenceIncluded}}},
	})
	experimentPath := documents[len(documents)-1].Path
	appendRecord(&research.Queue{
		Common: common(research.SchemaQueue, queueID, "Research queue"), Revision: 1,
		Partitions: []research.QueuePartition{{Pool: poolID, Lane: research.LaneExploit, Entries: []research.QueueEntry{}}, {Pool: poolID, Lane: research.LaneExplore, Entries: []research.QueueEntry{}}},
	})
	run := &research.Run{Common: common(research.SchemaRun, runID, "Candidate run"), Experiment: experimentID, Role: research.RunCandidate, Objective: "Train candidate."}
	documents = append(documents, &Document{Path: path.Join(path.Dir(experimentPath), "runs", runID.String()+"-candidate-run.md"), Body: "# Run\n", Record: run})
	started := now
	exitCode := 0
	attempt := &research.Attempt{
		Common: common(research.SchemaAttemptV2, attemptID, "Successful attempt"), Run: runID, State: research.AttemptSucceeded,
		Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"/bin/true"}, Pool: poolID, Queue: queueID, QueueRevision: 1,
		Lane: research.LaneExploit, DispatchID: "fixture-" + attemptID.UUIDHex(), BaseCommit: strings.Repeat("0", 40),
		HeadCommit: strings.Repeat("a", 40), ChangeSet: []string{"model.go"},
		Terminal: &research.Terminal{Source: "direct", ObservedAt: now, StartedAt: &started, EndedAt: now, ExitCode: &exitCode},
	}
	documents = append(documents, &Document{Path: path.Join(path.Dir(experimentPath), "attempts", attemptID.String()+".md"), Body: "# Attempt\n", Record: attempt})
	appendRecord(&research.EvaluationSpec{
		Common:  common(research.SchemaEvaluationSpec, scientificSpecID, "Scientific evaluation"),
		Purpose: research.EvaluationScientific, Dataset: "validation-v1", Protocol: "Run fixed evaluator.",
		Metrics: []research.MetricSpec{{Name: "macro_f1", Unit: "score", Direction: research.MetricMaximize}}, BudgetPool: poolID, BudgetHours: 1,
	})
	appendRecord(&research.Evaluation{
		Common: common(research.SchemaEvaluation, scientificEvaluationID, "Scientific result"),
		Spec:   scientificSpecID, Subject: experimentID, Outcome: research.EvaluationPassed, EvaluatedAt: now,
		Metrics: []research.MetricValue{{Name: "macro_f1", Unit: "score", Value: .91}}, Summary: "Improved.",
	})
	appendRecord(&research.Candidate{
		Common: common(research.SchemaCandidate, candidateID, "Candidate"), Experiment: experimentID, Evaluation: scientificEvaluationID,
		GitCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ChangeSet: []string{"model.go"},
	})
	appendRecord(&research.EvaluationSpec{
		Common:  common(research.SchemaEvaluationSpec, promotionEvalSpecID, "Sealed evaluation"),
		Purpose: research.EvaluationPromotion, Dataset: "holdout-v1", Protocol: "Run sealed evaluator.",
		Metrics:    []research.MetricSpec{{Name: "macro_f1", Unit: "score", Direction: research.MetricMaximize, Threshold: &promotionThreshold}},
		BudgetPool: poolID, BudgetHours: 1, SealedAt: &now,
	})
	appendRecord(&research.Release{
		Common: common(research.SchemaRelease, releaseID, "Release v1"), Target: "production", Version: "v1", State: research.ReleaseValidated,
		Slots: []research.ReleaseSlot{{Name: "main", Candidate: candidateID}}, Evaluation: promotionEvaluationID,
	})
	appendRecord(&research.Evaluation{
		Common: research.Common{Schema: research.SchemaEvaluation, ID: promotionEvaluationID, Title: "Promotion result", CreatedAt: evaluatedAt, UpdatedAt: evaluatedAt},
		Spec:   promotionEvalSpecID, Subject: releaseID, Outcome: research.EvaluationPassed, EvaluatedAt: evaluatedAt,
		Metrics: []research.MetricValue{{Name: "macro_f1", Unit: "score", Value: .9}}, Summary: "Holdout passed.",
	})
	appendRecord(&research.PromotionSpec{
		Common: common(research.SchemaPromotionSpec, promotionSpecID, "Production gate"), Target: "production",
		EvaluationSpec: promotionEvalSpecID, SealedAt: now, HoldoutBudgetHours: 1, HumanApprovalRequired: true,
	})
	appendRecord(&research.Promotion{
		Common: research.Common{Schema: research.SchemaPromotion, ID: promotionID, Title: "Promote v1", CreatedAt: promotedAt, UpdatedAt: promotedAt}, Target: "production", Spec: promotionSpecID,
		Challenger: releaseID, Evaluation: promotionEvaluationID, Outcome: research.PromotionAccepted, AppliedAt: promotedAt, ApprovedBy: "human:david",
	})

	inventory := InventoryFromDocuments(t.TempDir(), documents)
	if !inventory.Valid() {
		t.Fatalf("promotion inventory invalid: %v", inventory.Diagnostics)
	}
	champions, err := inventory.CurrentChampions()
	if err != nil || len(champions) != 1 || champions[0].Target != "production" || champions[0].Release != releaseID || champions[0].Promotion != promotionID {
		t.Fatalf("champions = %#v, %v", champions, err)
	}

	t.Run("holdout must be strictly newer than seal", func(t *testing.T) {
		candidate := cloneDocuments(documents)
		for _, document := range candidate {
			if id, ok := document.ID(); ok && id == promotionEvaluationID {
				evaluation := document.Record.(*research.Evaluation)
				evaluation.CreatedAt, evaluation.UpdatedAt, evaluation.EvaluatedAt = now, now, now
			}
		}
		invalid := InventoryFromDocuments(t.TempDir(), candidate)
		if !hasDiagnosticCode(invalid.Diagnostics, "promotion.holdout_stale") {
			t.Fatalf("seal-time holdout validated: %v", invalid.Diagnostics)
		}
	})

	t.Run("challenger must be validated", func(t *testing.T) {
		candidate := cloneDocuments(documents)
		for _, document := range candidate {
			if id, ok := document.ID(); ok && id == releaseID {
				document.Record.(*research.Release).State = research.ReleaseDraft
			}
		}
		invalid := InventoryFromDocuments(t.TempDir(), candidate)
		if !hasDiagnosticCode(invalid.Diagnostics, "promotion.challenger_state") {
			t.Fatalf("draft challenger validated: %v", invalid.Diagnostics)
		}
	})

	t.Run("holdout can be consumed only once and rollback restores displaced incumbent", func(t *testing.T) {
		candidate := cloneDocuments(documents)
		secondReleaseID := mustControlID(t, "rel_01a01e61-0000-7012-8000-000000000012")
		secondRelease := &research.Release{
			Common: research.Common{Schema: research.SchemaRelease, ID: secondReleaseID, Title: "Release v2", CreatedAt: now, UpdatedAt: now},
			Target: "production", Version: "v2", State: research.ReleaseDraft, Slots: []research.ReleaseSlot{{Name: "main", Candidate: candidateID}},
		}
		secondReleasePath, err := PathForNew(secondRelease, nil)
		if err != nil {
			t.Fatal(err)
		}
		rollbackID := mustControlID(t, "prom_01a01e61-0000-7013-8000-000000000013")
		rollback := &research.Promotion{
			Common: research.Common{Schema: research.SchemaPromotion, ID: rollbackID, Title: "Invalid rollback", CreatedAt: promotedAt.Add(time.Minute), UpdatedAt: promotedAt.Add(time.Minute)},
			Target: "production", Spec: promotionSpecID, Challenger: secondReleaseID, Incumbent: releaseID,
			Evaluation: promotionEvaluationID, Outcome: research.PromotionRolledBack, AppliedAt: promotedAt.Add(time.Minute), Previous: promotionID, ApprovedBy: "human:david",
		}
		rollbackPath, err := PathForNew(rollback, nil)
		if err != nil {
			t.Fatal(err)
		}
		candidate = append(candidate,
			&Document{Path: secondReleasePath, Body: "# Release v2\n", Record: secondRelease},
			&Document{Path: rollbackPath, Body: "# Invalid rollback\n", Record: rollback},
		)
		invalid := InventoryFromDocuments(t.TempDir(), candidate)
		for _, code := range []string{"promotion.holdout_reused", "promotion.rollback_incumbent"} {
			if !hasDiagnosticCode(invalid.Diagnostics, code) {
				t.Fatalf("missing %s: %v", code, invalid.Diagnostics)
			}
		}
	})

	t.Run("rollback requires a passed holdout", func(t *testing.T) {
		candidate := cloneDocuments(documents)
		for _, document := range candidate {
			id, ok := document.ID()
			if !ok {
				continue
			}
			switch id {
			case promotionEvaluationID:
				document.Record.(*research.Evaluation).Outcome = research.EvaluationFailed
			case promotionID:
				document.Record.(*research.Promotion).Outcome = research.PromotionRolledBack
			}
		}
		invalid := InventoryFromDocuments(t.TempDir(), candidate)
		if !hasDiagnosticCode(invalid.Diagnostics, "promotion.evaluation_outcome") {
			t.Fatalf("failed rollback holdout validated: %v", invalid.Diagnostics)
		}
	})
}
