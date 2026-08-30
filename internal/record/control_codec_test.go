package record

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestControlPlanePolicyAndPlanV2RoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	policy := &research.Policy{
		Schema: research.SchemaPolicy, CreatedAt: now, UpdatedAt: now,
		Autonomy: research.AutonomyManual, ExploitShare: .8, ExploreShare: .2,
		ScoreFormula: "utility-v1", TiePolicy: research.QueueTieKeepIncumbent,
		PromotionRequiresHuman: true,
		Taxonomy: research.ClassificationTaxonomy{
			Domains: []string{"ml"}, Work: []string{"training"},
			Methods: []string{"ablation"}, Components: []string{"encoder"},
		},
		ClusterSaturation: research.ClusterSaturationPolicy{
			BudgetHours: 24, PlateauWindow: 5, MinimumImprovement: .01, MinimumProbability: .1,
		},
	}
	encodedPolicy, err := Encode(&Document{Record: policy, Body: "# Policy\n"})
	if err != nil {
		t.Fatalf("encode Policy: %v", err)
	}
	decodedPolicy, err := Decode(encodedPolicy)
	if err != nil {
		t.Fatalf("decode Policy: %v\n%s", err, encodedPolicy)
	}
	if _, ok := decodedPolicy.Record.(*research.Policy); !ok {
		t.Fatalf("decoded Policy as %T", decodedPolicy.Record)
	}

	poolID := mustControlID(t, "pool_01a01e60-0000-7002-8000-000000000002")
	planID := mustControlID(t, "plan_01a01e60-0000-7003-8000-000000000003")
	plan := &research.Plan{
		Common:   research.Common{Schema: research.SchemaPlanV2, ID: planID, Title: "Try encoder ablation", CreatedAt: now, UpdatedAt: now},
		Priority: research.PriorityP1, Effort: research.EffortS, State: research.PlanQueued,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve score", Metric: "macro_f1", Unit: "score"},
		PrimaryCluster: "encoder", Classification: &research.Classification{
			Domain: "ml", Work: "training", Method: "ablation", Component: "encoder",
			Lane: research.LaneExploit, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman,
		},
		Resources: []research.ResourceNeed{{Pool: poolID, Units: 1, EstimatedHours: 2}},
		Utility:   &research.UtilityEstimate{Probability: .6, Impact: .1, InformationGain: .2, UnblockValue: .1, RiskPenalty: .05},
	}
	encoded, err := Encode(&Document{Record: plan, Body: "# Plan\n"})
	if err != nil {
		t.Fatalf("encode Plan v2: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode Plan v2: %v\n%s", err, encoded)
	}
	got := decoded.Record.(*research.Plan)
	if got.Schema != research.SchemaPlanV2 || got.Classification == nil || len(got.Resources) != 1 {
		t.Fatalf("Plan v2 fields lost: %#v", got)
	}

	legacyClaim := bytes.Replace(encoded, []byte(research.SchemaPlanV2), []byte(research.SchemaPlan), 1)
	_, err = Decode(legacyClaim)
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != "record.unknown_field" {
		t.Fatalf("v1 decoder accepted v2 fields: %v", err)
	}
}

func TestControlPlaneFlatLayoutsRoundTripKindAndIdentity(t *testing.T) {
	now := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	common := func(schema research.Schema, id string, title string) research.Common {
		return research.Common{Schema: schema, ID: mustControlID(t, id), Title: title, CreatedAt: now, UpdatedAt: now}
	}
	records := []research.Record{
		&research.Idea{Common: common(research.SchemaIdea, "idea_01a01e60-0000-7010-8000-000000000010", "Idea")},
		&research.ResourcePool{Common: common(research.SchemaResourcePool, "pool_01a01e60-0000-7011-8000-000000000011", "GPU pool")},
		&research.Queue{Common: common(research.SchemaQueue, "queue_01a01e60-0000-7012-8000-000000000012", "Main queue")},
		&research.QueueAdvice{Common: common(research.SchemaQueueAdvice, "advice_01a01e60-0000-7013-8000-000000000013", "Queue advice")},
		&research.Battle{Common: common(research.SchemaBattle, "battle_01a01e60-0000-7014-8000-000000000014", "Queue battle")},
		&research.EvaluationSpec{Common: common(research.SchemaEvaluationSpec, "evalspec_01a01e60-0000-7015-8000-000000000015", "Evaluation spec")},
		&research.Evaluation{Common: common(research.SchemaEvaluation, "eval_01a01e60-0000-7016-8000-000000000016", "Evaluation")},
		&research.Candidate{Common: common(research.SchemaCandidate, "cand_01a01e60-0000-7017-8000-000000000017", "Candidate")},
		&research.Release{Common: common(research.SchemaRelease, "rel_01a01e60-0000-7018-8000-000000000018", "Release")},
		&research.PromotionSpec{Common: common(research.SchemaPromotionSpec, "promspec_01a01e60-0000-7019-8000-000000000019", "Promotion spec")},
		&research.Promotion{Common: common(research.SchemaPromotion, "prom_01a01e60-0000-7020-8000-000000000020", "Promotion")},
	}
	for _, value := range records {
		path, err := PathForNew(value, nil)
		if err != nil {
			t.Fatalf("PathForNew(%s): %v", value.GetKind(), err)
		}
		location, recognized, err := ClassifyPath(path)
		id, _ := value.GetID()
		if err != nil || !recognized || location.Kind != value.GetKind() || location.ID != id {
			t.Fatalf("classify %s = %#v, %v, %v", path, location, recognized, err)
		}
		if !strings.HasSuffix(path, ".md") {
			t.Fatalf("canonical path lacks Markdown suffix: %s", path)
		}
	}

	policyPath, err := PathForNew(&research.Policy{Schema: research.SchemaPolicy}, nil)
	if err != nil || policyPath != PolicyFile {
		t.Fatalf("Policy path = %q, %v", policyPath, err)
	}
}

func TestQueueCodecPreservesPoolLaneOrder(t *testing.T) {
	now := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	queue := &research.Queue{
		Common: research.Common{
			Schema: research.SchemaQueue,
			ID:     mustControlID(t, "queue_01a01e60-0000-7004-8000-000000000004"),
			Title:  "Main queue", CreatedAt: now, UpdatedAt: now,
		},
		Revision: 1,
		Partitions: []research.QueuePartition{{
			Pool: mustControlID(t, "pool_01a01e60-0000-7002-8000-000000000002"),
			Lane: research.LaneExploit, Entries: []research.QueueEntry{},
		}},
	}
	encoded, err := Encode(&Document{Record: queue, Body: "# Queue\n"})
	if err != nil {
		t.Fatalf("encode Queue: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode Queue: %v\n%s", err, encoded)
	}
	got := decoded.Record.(*research.Queue)
	if len(got.Partitions) != 1 || got.Partitions[0].Entries == nil || got.Partitions[0].Lane != research.LaneExploit {
		t.Fatalf("Queue ordering data changed: %#v", got.Partitions)
	}
}

func TestExperimentAndAttemptV2RoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	experiment := &research.Experiment{
		Common: research.Common{
			Schema: research.SchemaExperimentV2,
			ID:     mustControlID(t, "exp_01a01e60-0000-7021-8000-000000000021"),
			Title:  "Combine candidates", CreatedAt: now, UpdatedAt: now,
		},
		Lifecycle: research.LifecyclePlanned,
		Design: research.Design{
			Question: "Does the combined release improve score?", Hypothesis: "The combination improves score.",
			Kind: research.ExperimentCombination, PrimaryFactor: "candidate composition",
			SecondaryFactors: []string{}, Baseline: "current champion", ComparabilitySpec: "same sealed evaluator",
			SuccessCriteria: []string{"macro F1 improves"}, DecisionRule: "adopt only if the sealed gate passes",
		},
		CandidateInputs: []research.ID{
			mustControlID(t, "cand_01a01e60-0000-7022-8000-000000000022"),
			mustControlID(t, "cand_01a01e60-0000-7023-8000-000000000023"),
		},
	}
	encodedExperiment, err := Encode(&Document{Record: experiment, Body: "# Combination\n"})
	if err != nil {
		t.Fatalf("encode Experiment v2: %v", err)
	}
	decodedExperiment, err := Decode(encodedExperiment)
	if err != nil {
		t.Fatalf("decode Experiment v2: %v\n%s", err, encodedExperiment)
	}
	if got := decodedExperiment.Record.(*research.Experiment); got.Design.Kind != research.ExperimentCombination || len(got.CandidateInputs) != 2 {
		t.Fatalf("Experiment v2 fields lost: %#v", got)
	}

	attempt := &research.Attempt{
		Common: research.Common{
			Schema: research.SchemaAttemptV2,
			ID:     mustControlID(t, "att_01a01e60-0000-7024-8000-000000000024"),
			Title:  "Run candidate", CreatedAt: now, UpdatedAt: now,
		},
		Run:   mustControlID(t, "run_01a01e60-0000-7025-8000-000000000025"),
		State: research.AttemptPlanned, Runner: "direct", Scheduler: "pueue", CWD: ".", Argv: []string{"go", "test", "./..."},
		Pool:          mustControlID(t, "pool_01a01e60-0000-7026-8000-000000000026"),
		Queue:         mustControlID(t, "queue_01a01e60-0000-7027-8000-000000000027"),
		QueueRevision: 3, Lane: research.LaneExploit, DispatchID: "pueue-42",
		BaseCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		HeadCommit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ChangeSet:  []string{"internal/model/train.go"},
	}
	encodedAttempt, err := Encode(&Document{Record: attempt, Body: "# Attempt\n"})
	if err != nil {
		t.Fatalf("encode Attempt v2: %v", err)
	}
	decodedAttempt, err := Decode(encodedAttempt)
	if err != nil {
		t.Fatalf("decode Attempt v2: %v\n%s", err, encodedAttempt)
	}
	if got := decodedAttempt.Record.(*research.Attempt); got.QueueRevision != 3 || len(got.ChangeSet) != 1 || got.Pool.IsZero() {
		t.Fatalf("Attempt v2 fields lost: %#v", got)
	}
}

func TestControlAuditReleaseAndPromotionRecordsRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	promotionThreshold := .8
	common := func(schema research.Schema, id, title string) research.Common {
		return research.Common{Schema: schema, ID: mustControlID(t, id), Title: title, CreatedAt: now, UpdatedAt: now}
	}
	queueID := mustControlID(t, "queue_01a01e60-0000-7030-8000-000000000030")
	poolID := mustControlID(t, "pool_01a01e60-0000-7031-8000-000000000031")
	planA := mustControlID(t, "plan_01a01e60-0000-7032-8000-000000000032")
	planB := mustControlID(t, "plan_01a01e60-0000-7033-8000-000000000033")
	evalSpecID := mustControlID(t, "evalspec_01a01e60-0000-7034-8000-000000000034")
	experimentID := mustControlID(t, "exp_01a01e60-0000-7035-8000-000000000035")
	evaluationID := mustControlID(t, "eval_01a01e60-0000-7036-8000-000000000036")
	candidateID := mustControlID(t, "cand_01a01e60-0000-7037-8000-000000000037")
	releaseID := mustControlID(t, "rel_01a01e60-0000-7038-8000-000000000038")
	promotionSpecID := mustControlID(t, "promspec_01a01e60-0000-7039-8000-000000000039")

	records := []research.Record{
		&research.Idea{
			Common: common(research.SchemaIdea, "idea_01a01e60-0000-7040-8000-000000000040", "Try an ablation"),
			State:  research.IdeaProposed, Summary: "Test a smaller encoder", ProposedBy: "human:david", PrimaryCluster: "encoder",
			Classification: research.Classification{Domain: "ml", Work: "training", Method: "ablation", Component: "encoder", Lane: research.LaneExplore, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman},
		},
		&research.QueueAdvice{
			Common: common(research.SchemaQueueAdvice, "advice_01a01e60-0000-7041-8000-000000000041", "Rank plan"),
			Queue:  queueID, QueueRevision: 1, CandidatePlan: planA, Pool: poolID, Lane: research.LaneExploit,
			ProposedPosition: 0, ListwiseOrder: []research.ID{planA},
			Score: research.QueueScore{ExpectedUtility: .1, InformationGain: .2, UnblockValue: .1, RiskPenalty: .05, PoolHours: 2, Total: .175},
			Model: "agent-v1", PromptDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Rationale: "Best expected utility per GPU-hour.",
		},
		&research.Battle{
			Common: common(research.SchemaBattle, "battle_01a01e60-0000-7042-8000-000000000042", "Battle plans"),
			Queue:  queueID, QueueRevision: 1, CandidatePlan: planA, IncumbentPlan: planB, Pool: poolID, Lane: research.LaneExploit,
			OrderAB: research.BattleChooseCandidate, OrderBA: research.BattleChooseCandidate,
			Outcome: research.BattleCandidateWins, Confidence: .8, Rationale: "Candidate wins in both orders.",
		},
		&research.EvaluationSpec{
			Common:  common(research.SchemaEvaluationSpec, evalSpecID.String(), "Sealed score"),
			Purpose: research.EvaluationPromotion, Dataset: "holdout-v1", Protocol: "Run the fixed evaluator once.",
			Metrics:    []research.MetricSpec{{Name: "macro_f1", Unit: "score", Direction: research.MetricMaximize, Threshold: &promotionThreshold}},
			BudgetPool: poolID, BudgetHours: 2, SealedAt: &now,
		},
		&research.Evaluation{
			Common: common(research.SchemaEvaluation, evaluationID.String(), "Candidate score"),
			Spec:   evalSpecID, Subject: experimentID, Outcome: research.EvaluationPassed, EvaluatedAt: now,
			Metrics: []research.MetricValue{{Name: "macro_f1", Value: .91, Unit: "score"}}, Summary: "Gate passed.",
		},
		&research.Candidate{
			Common:     common(research.SchemaCandidate, candidateID.String(), "Encoder candidate"),
			Experiment: experimentID, Evaluation: evaluationID,
			GitCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ChangeSet: []string{"internal/model/encoder.go"},
		},
		&research.Release{
			Common: common(research.SchemaRelease, releaseID.String(), "Release v1"),
			Target: "production", Version: "v1", State: research.ReleaseDraft,
			Slots: []research.ReleaseSlot{{Name: "main", Candidate: candidateID}},
		},
		&research.PromotionSpec{
			Common: common(research.SchemaPromotionSpec, promotionSpecID.String(), "Production gate"),
			Target: "production", EvaluationSpec: evalSpecID, SealedAt: now, HoldoutBudgetHours: 2, HumanApprovalRequired: true,
		},
		&research.Promotion{
			Common: common(research.SchemaPromotion, "prom_01a01e60-0000-7043-8000-000000000043", "Promote release"),
			Target: "production", Spec: promotionSpecID, Challenger: releaseID, Evaluation: evaluationID,
			Outcome: research.PromotionAccepted, AppliedAt: now, ApprovedBy: "human:david",
		},
	}
	for _, value := range records {
		encoded, err := Encode(&Document{Record: value, Body: "# Record\n"})
		if err != nil {
			t.Fatalf("encode %s: %v", value.GetKind(), err)
		}
		decoded, err := Decode(encoded)
		if err != nil {
			t.Fatalf("decode %s: %v\n%s", value.GetKind(), err, encoded)
		}
		if decoded.Kind() != value.GetKind() {
			t.Fatalf("decoded %s as %s", value.GetKind(), decoded.Kind())
		}
	}
}

func mustControlID(t *testing.T, value string) research.ID {
	t.Helper()
	id, err := research.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
