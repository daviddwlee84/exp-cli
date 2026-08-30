package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestScientificLifecycleClosesAtomicallyAndCreatesCandidate(t *testing.T) {
	fixture := newLifecycleFixture(t)
	study := fixture.addStudy(t, "Encoder ablation", research.ExperimentSingleFactor, nil)
	fixture.addSuccessfulAttempt(t, study, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "internal/model/encoder.go")

	fixture.advance()
	invalid, err := fixture.service.CloseExperiment(context.Background(), CloseExperimentRequest{
		Experiment: ref(study.experiment), Plan: ref(study.plan), Verdict: research.VerdictSupported,
		Summary:  "The encoder improves score.",
		Evidence: []ConclusionEvidenceInput{{Run: ref(study.run), Disposition: research.EvidenceIncluded}},
		Findings: []FindingInput{{Title: "Missing evidence", Statement: "The encoder helps.", Scope: "validation"}},
	})
	if err == nil || invalid != nil {
		t.Fatalf("invalid atomic closure = %#v, %v", invalid, err)
	}
	inventory := fixture.inventory(t)
	assertExperimentLifecycle(t, inventory, study.experiment, research.LifecycleActive)
	assertPlanState(t, inventory, study.plan, research.PlanStarted)
	if findings := inventory.OfKind(research.KindFinding); len(findings) != 0 {
		t.Fatalf("invalid transaction published Findings: %#v", findings)
	}

	fixture.advance()
	closed, err := fixture.service.CloseExperiment(context.Background(), CloseExperimentRequest{
		Experiment: ref(study.experiment), Plan: ref(study.plan), Verdict: research.VerdictSupported,
		Summary:  "The encoder improves score.",
		Evidence: []ConclusionEvidenceInput{{Run: ref(study.run), Disposition: research.EvidenceIncluded}},
		Findings: []FindingInput{{
			Title: "Encoder gain", Body: "# Encoder gain\n", Statement: "The encoder improves validation score.", Scope: "validation-v1",
			Evidence: []FindingEvidenceInput{{Run: ref(study.run), Detail: "Controlled candidate run."}},
		}},
	})
	if err != nil || closed == nil || len(closed.Findings) != 1 {
		t.Fatalf("CloseExperiment = %#v, %v", closed, err)
	}
	if got := closed.Experiment.Record.(*research.Experiment); got.Lifecycle != research.LifecycleClosed || got.Verdict != research.VerdictSupported || got.Conclusion == nil {
		t.Fatalf("closed Experiment = %#v", got)
	}
	if got := closed.Plan.Record.(*research.Plan).State; got != research.PlanCompleted {
		t.Fatalf("completed Plan state = %s", got)
	}

	fixture.advance()
	evaluation, err := fixture.service.CreateEvaluation(context.Background(), CreateEvaluationRequest{
		Spec: ref(fixture.scientificSpec), Subject: ref(closed.Experiment),
		Data: EvaluationData{
			Title: "Scientific result", Body: "# Scientific result\n", Outcome: research.EvaluationPassed,
			Metrics: []research.MetricValue{{Name: "score", Unit: "points", Value: 0.91}}, Summary: "Scientific gate passed.",
		},
	})
	if err != nil || evaluation == nil {
		t.Fatalf("CreateEvaluation = %#v, %v", evaluation, err)
	}

	fixture.advance()
	candidate, err := fixture.service.CreateCandidate(context.Background(), CreateCandidateRequest{
		Title: "Encoder candidate", Body: "# Encoder candidate\n", Experiment: ref(closed.Experiment), Evaluation: ref(evaluation.Evaluation),
		EvaluationSpecExpectedRevision: fixture.scientificSpec.Revision,
		GitCommit:                      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ChangeSet: []string{"internal/model/encoder.go"},
	})
	if err != nil || candidate == nil {
		t.Fatalf("CreateCandidate = %#v, %v", candidate, err)
	}
	if got := candidate.Candidate.Record.(*research.Candidate); got.Experiment != mustDocumentID(study.experiment) || got.Evaluation != mustDocumentID(evaluation.Evaluation) {
		t.Fatalf("Candidate references = %#v", got)
	}

	_, err = fixture.service.CreateCandidate(context.Background(), CreateCandidateRequest{
		Title: "Stale candidate", Experiment: ref(study.experiment), Evaluation: ref(evaluation.Evaluation),
		EvaluationSpecExpectedRevision: fixture.scientificSpec.Revision,
		GitCommit:                      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ChangeSet: []string{"internal/model/stale.go"},
	})
	if !errors.Is(err, record.ErrConflict) {
		t.Fatalf("stale Experiment revision error = %v", err)
	}

	inventory = fixture.inventory(t)
	if !inventory.Valid() || len(inventory.OfKind(research.KindCandidate)) != 1 || len(inventory.OfKind(research.KindFinding)) != 1 {
		t.Fatalf("final scientific inventory = %#v", inventory.Diagnostics)
	}
}

func TestCombinationReleasePromotionAndRollback(t *testing.T) {
	fixture := newLifecycleFixture(t)
	first := fixture.produceCandidate(t, "Encoder", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "model/encoder.go")
	second := fixture.produceCandidate(t, "Sampling", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "strategy/sampling.go")
	combinationStudy := fixture.addStudy(t, "Combined system", research.ExperimentCombination, []research.ID{mustDocumentID(first), mustDocumentID(second)})
	fixture.addSuccessfulAttempt(t, combinationStudy, "cccccccccccccccccccccccccccccccccccccccc", "system/combined.go")
	fixture.advance()
	closedCombination, err := fixture.service.CloseExperiment(context.Background(), CloseExperimentRequest{
		Experiment: ref(combinationStudy.experiment), Plan: ref(combinationStudy.plan), Verdict: research.VerdictSupported,
		Summary:  "The combination passes.",
		Evidence: []ConclusionEvidenceInput{{Run: ref(combinationStudy.run), Disposition: research.EvidenceIncluded}},
	})
	if err != nil {
		t.Fatalf("close combination: %v", err)
	}
	fixture.advance()
	combinationEvaluation, err := fixture.service.CreateEvaluation(context.Background(), CreateEvaluationRequest{
		Spec: ref(fixture.scientificSpec), Subject: ref(closedCombination.Experiment),
		Data: passingEvaluation("Combination result"),
	})
	if err != nil {
		t.Fatalf("evaluate combination: %v", err)
	}

	fixture.advance()
	_, err = fixture.service.CreateRelease(context.Background(), CreateReleaseRequest{
		Title: "Unproven combination", Target: "production", Version: "v0", State: research.ReleaseDraft,
		Slots: []ReleaseSlotInput{{Name: "model", Candidate: ref(first)}, {Name: "strategy", Candidate: ref(second)}},
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("multi-Candidate Release without combination evidence = %v", err)
	}

	fixture.advance()
	releaseA, err := fixture.service.CreateRelease(context.Background(), CreateReleaseRequest{
		Title: "Combined release", Body: "# Combined release\n", Target: "production", Version: "v1", State: research.ReleaseValidated,
		Slots: []ReleaseSlotInput{{Name: "model", Candidate: ref(first)}, {Name: "strategy", Candidate: ref(second)}},
		Combination: &CombinationEvidence{
			Experiment: ref(closedCombination.Experiment), Evaluation: ref(combinationEvaluation.Evaluation),
			EvaluationSpecExpectedRevision: fixture.scientificSpec.Revision,
		},
		Evaluation: &ReleaseEvaluationInput{Spec: ref(fixture.promotionEvaluationSpec), Data: passingEvaluation("Release v1 holdout")},
	})
	if err != nil || releaseA == nil || releaseA.Evaluation == nil {
		t.Fatalf("CreateRelease(A) = %#v, %v", releaseA, err)
	}

	fixture.advance()
	releaseB, err := fixture.service.CreateRelease(context.Background(), CreateReleaseRequest{
		Title: "Sampling release", Body: "# Sampling release\n", Target: "production", Version: "v2", State: research.ReleaseValidated,
		Slots:      []ReleaseSlotInput{{Name: "strategy", Candidate: ref(second)}},
		Evaluation: &ReleaseEvaluationInput{Spec: ref(fixture.promotionEvaluationSpec), Data: passingEvaluation("Release v2 holdout")},
	})
	if err != nil || releaseB == nil || releaseB.Evaluation == nil {
		t.Fatalf("CreateRelease(B) = %#v, %v", releaseB, err)
	}

	fixture.advance()
	promotionSpec, err := fixture.service.CreatePromotionSpec(context.Background(), CreatePromotionSpecRequest{
		Title: "Production gate", Body: "# Production gate\n", Target: "production",
		EvaluationSpec: ref(fixture.promotionEvaluationSpec), HoldoutBudgetHours: 1,
	})
	if err != nil {
		t.Fatalf("CreatePromotionSpec: %v", err)
	}

	fixture.advance()
	holdoutA, err := fixture.service.CreateEvaluation(context.Background(), CreateEvaluationRequest{
		Spec: ref(fixture.promotionEvaluationSpec), Subject: ref(releaseA.Release), Data: passingEvaluation("Fresh v1 sealed holdout"),
	})
	if err != nil {
		t.Fatalf("fresh holdout A: %v", err)
	}
	fixture.advance()
	holdoutB, err := fixture.service.CreateEvaluation(context.Background(), CreateEvaluationRequest{
		Spec: ref(fixture.promotionEvaluationSpec), Subject: ref(releaseB.Release), Data: passingEvaluation("Fresh v2 sealed holdout"),
	})
	if err != nil {
		t.Fatalf("fresh holdout B: %v", err)
	}

	fixture.advance()
	acceptedA, err := fixture.service.AppendPromotion(context.Background(), AppendPromotionRequest{
		Title: "Promote v1", Body: "# Promote v1\n", Target: "production", Spec: ref(promotionSpec.Spec),
		Challenger: ref(releaseA.Release), Evaluation: ref(holdoutA.Evaluation),
		EvaluationSpecExpectedRevision: fixture.promotionEvaluationSpec.Revision,
		Outcome:                        research.PromotionAccepted, ApprovedBy: "human:david",
	})
	if err != nil || acceptedA.Champion == nil || acceptedA.Champion.Release != mustDocumentID(releaseA.Release) {
		t.Fatalf("accept A = %#v, %v", acceptedA, err)
	}

	fixture.advance()
	acceptedB, err := fixture.service.AppendPromotion(context.Background(), AppendPromotionRequest{
		Title: "Promote v2", Body: "# Promote v2\n", Target: "production", Spec: ref(promotionSpec.Spec),
		Challenger: ref(releaseB.Release), Evaluation: ref(holdoutB.Evaluation),
		EvaluationSpecExpectedRevision: fixture.promotionEvaluationSpec.Revision,
		Outcome:                        research.PromotionAccepted, ApprovedBy: "human:david",
		ExpectedPrevious: mustDocumentID(acceptedA.Promotion), PreviousExpectedRevision: acceptedA.Promotion.Revision,
		ExpectedChampion: mustDocumentID(releaseA.Release), IncumbentExpectedRevision: releaseA.Release.Revision,
	})
	if err != nil || acceptedB.Champion == nil || acceptedB.Champion.Release != mustDocumentID(releaseB.Release) {
		t.Fatalf("accept B = %#v, %v", acceptedB, err)
	}

	fixture.advance()
	_, err = fixture.service.AppendPromotion(context.Background(), AppendPromotionRequest{
		Title: "Wrong rollback", Target: "production", Spec: ref(promotionSpec.Spec),
		Challenger: ref(releaseB.Release), Evaluation: ref(holdoutB.Evaluation),
		EvaluationSpecExpectedRevision: fixture.promotionEvaluationSpec.Revision,
		Outcome:                        research.PromotionRolledBack, ApprovedBy: "human:david",
		ExpectedPrevious: mustDocumentID(acceptedB.Promotion), PreviousExpectedRevision: acceptedB.Promotion.Revision,
		ExpectedChampion: mustDocumentID(releaseB.Release), IncumbentExpectedRevision: releaseB.Release.Revision,
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("wrong rollback target error = %v", err)
	}

	fixture.advance()
	failedRollbackHoldout, err := fixture.service.CreateEvaluation(context.Background(), CreateEvaluationRequest{
		Spec: ref(fixture.promotionEvaluationSpec), Subject: ref(releaseA.Release), Data: EvaluationData{
			Title: "Failed rollback holdout", Body: "# Failed rollback holdout\n", Outcome: research.EvaluationFailed,
			Metrics: []research.MetricValue{{Name: "score", Unit: "points", Value: .4}}, Summary: "Rollback gate failed.",
		},
	})
	if err != nil {
		t.Fatalf("failed rollback holdout: %v", err)
	}
	fixture.advance()
	_, err = fixture.service.AppendPromotion(context.Background(), AppendPromotionRequest{
		Title: "Unsafe rollback", Target: "production", Spec: ref(promotionSpec.Spec),
		Challenger: ref(releaseA.Release), Evaluation: ref(failedRollbackHoldout.Evaluation),
		EvaluationSpecExpectedRevision: fixture.promotionEvaluationSpec.Revision,
		Outcome:                        research.PromotionRolledBack, ApprovedBy: "human:david",
		ExpectedPrevious: mustDocumentID(acceptedB.Promotion), PreviousExpectedRevision: acceptedB.Promotion.Revision,
		ExpectedChampion: mustDocumentID(releaseB.Release), IncumbentExpectedRevision: releaseB.Release.Revision,
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("failed-evaluation rollback error = %v", err)
	}

	fixture.advance()
	rollbackHoldout, err := fixture.service.CreateEvaluation(context.Background(), CreateEvaluationRequest{
		Spec: ref(fixture.promotionEvaluationSpec), Subject: ref(releaseA.Release), Data: passingEvaluation("Fresh rollback holdout"),
	})
	if err != nil {
		t.Fatalf("fresh rollback holdout: %v", err)
	}
	fixture.advance()
	rolledBack, err := fixture.service.AppendPromotion(context.Background(), AppendPromotionRequest{
		Title: "Rollback v2", Body: "# Rollback v2\n", Target: "production", Spec: ref(promotionSpec.Spec),
		Challenger: ref(releaseA.Release), Evaluation: ref(rollbackHoldout.Evaluation),
		EvaluationSpecExpectedRevision: fixture.promotionEvaluationSpec.Revision,
		Outcome:                        research.PromotionRolledBack, ApprovedBy: "human:david",
		ExpectedPrevious: mustDocumentID(acceptedB.Promotion), PreviousExpectedRevision: acceptedB.Promotion.Revision,
		ExpectedChampion: mustDocumentID(releaseB.Release), IncumbentExpectedRevision: releaseB.Release.Revision,
	})
	if err != nil || rolledBack.Champion == nil || rolledBack.Champion.Release != mustDocumentID(releaseA.Release) {
		t.Fatalf("rollback = %#v, %v", rolledBack, err)
	}

	champions, err := fixture.inventory(t).CurrentChampions()
	if err != nil || len(champions) != 1 || champions[0].Release != mustDocumentID(releaseA.Release) || champions[0].Promotion != mustDocumentID(rolledBack.Promotion) {
		t.Fatalf("derived champions = %#v, %v", champions, err)
	}
}

func TestEvaluationOutcomeMustMatchDeclaredThresholds(t *testing.T) {
	threshold := .8
	spec := &research.EvaluationSpec{Metrics: []research.MetricSpec{{Name: "score", Unit: "points", Direction: research.MetricMaximize, Threshold: &threshold}}}
	metrics := []research.MetricValue{{Name: "score", Unit: "points", Value: .7}}
	if err := validateMetrics(spec, metrics, research.EvaluationPassed); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("false passing outcome error = %v", err)
	}
	if err := validateMetrics(spec, metrics, research.EvaluationFailed); err != nil {
		t.Fatalf("matching failed outcome error = %v", err)
	}
}

type lifecycleFixture struct {
	store                   *record.Store
	service                 *Service
	now                     time.Time
	nextEntropy             byte
	pool                    *record.Document
	queue                   *record.Document
	scientificSpec          *record.Document
	promotionEvaluationSpec *record.Document
}

type studyFixture struct {
	experiment *record.Document
	run        *record.Document
	plan       *record.Document
}

func newLifecycleFixture(t *testing.T) *lifecycleFixture {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--quiet")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	initial := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	info, _, err := project.Initialize(context.Background(), project.InitRequest{StartDir: repository, Name: "Lifecycle Test"},
		project.WithClock(func() time.Time { return initial }),
		project.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a02e00-0000-7001-8000-000000000001"), nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &lifecycleFixture{store: record.NewStore(info.Root, info.Repository.GitCommonDir), now: initial.Add(time.Minute)}
	fixture.service = New(fixture.store, WithClock(func() time.Time { return fixture.now }), WithUUIDGenerator(fixture.generate))
	poolID := fixture.nextID(t, research.KindResourcePool)
	fixture.pool = fixture.create(t, &research.ResourcePool{
		Common: fixture.common(research.SchemaResourcePool, poolID, "GPU"), Enabled: true, Capacity: 2, Unit: "gpu", Bottleneck: "gpu",
	}, "# GPU\n")
	queueID := fixture.nextID(t, research.KindQueue)
	fixture.queue = fixture.create(t, &research.Queue{
		Common: fixture.common(research.SchemaQueue, queueID, "Research queue"), Revision: 1,
		Partitions: []research.QueuePartition{{Pool: poolID, Lane: research.LaneExploit, Entries: []research.QueueEntry{}}, {Pool: poolID, Lane: research.LaneExplore, Entries: []research.QueueEntry{}}},
	}, "# Research queue\n")
	scientificSpecID := fixture.nextID(t, research.KindEvaluationSpec)
	fixture.scientificSpec = fixture.create(t, &research.EvaluationSpec{
		Common:  fixture.common(research.SchemaEvaluationSpec, scientificSpecID, "Scientific gate"),
		Purpose: research.EvaluationScientific, Dataset: "validation-v1", Protocol: "Run the fixed evaluator.",
		Metrics:    []research.MetricSpec{{Name: "score", Unit: "points", Direction: research.MetricMaximize}},
		BudgetPool: poolID, BudgetHours: 1,
	}, "# Scientific gate\n")
	promotionSpecID := fixture.nextID(t, research.KindEvaluationSpec)
	sealed := fixture.now
	promotionThreshold := .5
	fixture.promotionEvaluationSpec = fixture.create(t, &research.EvaluationSpec{
		Common:  fixture.common(research.SchemaEvaluationSpec, promotionSpecID, "Promotion holdout"),
		Purpose: research.EvaluationPromotion, Dataset: "holdout-v1", Protocol: "Run the sealed evaluator.",
		Metrics:    []research.MetricSpec{{Name: "score", Unit: "points", Direction: research.MetricMaximize, Threshold: &promotionThreshold}},
		BudgetPool: poolID, BudgetHours: 1, SealedAt: &sealed,
	}, "# Promotion holdout\n")
	return fixture
}

func (fixture *lifecycleFixture) generate(at time.Time) (uuid.UUID, error) {
	fixture.nextEntropy++
	return research.NewUUIDv7(at, bytes.NewReader(bytes.Repeat([]byte{fixture.nextEntropy}, 10)))
}

func (fixture *lifecycleFixture) nextID(t *testing.T, kind research.Kind) research.ID {
	t.Helper()
	value, err := fixture.generate(fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := research.NewID(kind, value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (fixture *lifecycleFixture) common(schema research.Schema, id research.ID, title string) research.Common {
	return research.Common{Schema: schema, ID: id, Title: title, CreatedAt: fixture.now, UpdatedAt: fixture.now}
}

func (fixture *lifecycleFixture) create(t *testing.T, value research.Record, body string) *record.Document {
	t.Helper()
	document, err := fixture.store.Create(context.Background(), &record.Document{Record: value, Body: body})
	if err != nil {
		t.Fatalf("create %s: %v", value.GetKind(), err)
	}
	return document
}

func (fixture *lifecycleFixture) advance() { fixture.now = fixture.now.Add(time.Minute) }

func (fixture *lifecycleFixture) inventory(t *testing.T) *record.Inventory {
	t.Helper()
	inventory, err := fixture.store.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return inventory
}

func (fixture *lifecycleFixture) addStudy(t *testing.T, title string, kind research.ExperimentKind, candidateInputs []research.ID) studyFixture {
	t.Helper()
	experimentID := fixture.nextID(t, research.KindExperiment)
	design := research.Design{
		Question: "Does " + title + " improve score?", Hypothesis: title + " improves score.", Kind: kind,
		PrimaryFactor: "configuration", SecondaryFactors: []string{}, Baseline: "current champion",
		ComparabilitySpec: "same data and evaluator", SuccessCriteria: []string{"score improves"}, DecisionRule: "accept if the gate passes",
	}
	designLocked := fixture.now
	design.DesignLockedAt = &designLocked
	digest, err := research.DesignDigest(design)
	if err != nil {
		t.Fatal(err)
	}
	design.DesignDigest = digest
	schema := research.SchemaExperiment
	if kind == research.ExperimentCombination {
		schema = research.SchemaExperimentV2
	}
	experiment := fixture.create(t, &research.Experiment{
		Common: fixture.common(schema, experimentID, title), Lifecycle: research.LifecycleActive,
		Design: design, CandidateInputs: append([]research.ID(nil), candidateInputs...),
	}, "# "+title+"\n")
	runID := fixture.nextID(t, research.KindRun)
	run := fixture.create(t, &research.Run{
		Common: fixture.common(research.SchemaRun, runID, title+" run"), Experiment: experimentID,
		Role: research.RunCandidate, Objective: "Measure the candidate under the locked design.",
	}, "# Run\n")
	planID := fixture.nextID(t, research.KindPlan)
	plan := fixture.create(t, &research.Plan{
		Common: fixture.common(research.SchemaPlan, planID, title+" plan"), Priority: research.PriorityP1, Effort: research.EffortS,
		State: research.PlanStarted, ResultingExperiment: experimentID,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve score.", Metric: "score", Unit: "points"},
	}, "# Plan\n")
	return studyFixture{experiment: experiment, run: run, plan: plan}
}

func (fixture *lifecycleFixture) addSuccessfulAttempt(t *testing.T, study studyFixture, commit, changedPath string) *record.Document {
	t.Helper()
	attemptID := fixture.nextID(t, research.KindAttempt)
	poolID := fixture.pool.Record.(*research.ResourcePool).ID
	queueID := fixture.queue.Record.(*research.Queue).ID
	started := fixture.now
	return fixture.create(t, &research.Attempt{
		Common: fixture.common(research.SchemaAttemptV2, attemptID, "Successful implementation"),
		Run:    study.run.Record.(*research.Run).ID, State: research.AttemptSucceeded, Runner: "direct", Scheduler: "direct",
		CWD: ".", Argv: []string{"/bin/true"}, Pool: poolID, Queue: queueID, QueueRevision: 1, Lane: research.LaneExploit,
		DispatchID: "test-" + attemptID.UUIDHex(), BaseCommit: strings.Repeat("0", 40), HeadCommit: commit, ChangeSet: []string{changedPath},
		Terminal: &research.Terminal{Source: "direct", ObservedAt: fixture.now, StartedAt: &started, EndedAt: fixture.now, ExitCode: intPointer(0)},
	}, "# Successful implementation\n")
}

func intPointer(value int) *int { return &value }

func (fixture *lifecycleFixture) produceCandidate(t *testing.T, title, commit, changedPath string) *record.Document {
	t.Helper()
	study := fixture.addStudy(t, title, research.ExperimentSingleFactor, nil)
	fixture.addSuccessfulAttempt(t, study, commit, changedPath)
	fixture.advance()
	closed, err := fixture.service.CloseExperiment(context.Background(), CloseExperimentRequest{
		Experiment: ref(study.experiment), Plan: ref(study.plan), Verdict: research.VerdictSupported,
		Summary: title + " passes.", Evidence: []ConclusionEvidenceInput{{Run: ref(study.run), Disposition: research.EvidenceIncluded}},
	})
	if err != nil {
		t.Fatalf("close %s: %v", title, err)
	}
	fixture.advance()
	evaluation, err := fixture.service.CreateEvaluation(context.Background(), CreateEvaluationRequest{
		Spec: ref(fixture.scientificSpec), Subject: ref(closed.Experiment), Data: passingEvaluation(title + " scientific result"),
	})
	if err != nil {
		t.Fatalf("evaluate %s: %v", title, err)
	}
	fixture.advance()
	candidate, err := fixture.service.CreateCandidate(context.Background(), CreateCandidateRequest{
		Title: title + " candidate", Body: "# Candidate\n", Experiment: ref(closed.Experiment), Evaluation: ref(evaluation.Evaluation),
		EvaluationSpecExpectedRevision: fixture.scientificSpec.Revision, GitCommit: commit, ChangeSet: []string{changedPath},
	})
	if err != nil {
		t.Fatalf("create %s Candidate: %v", title, err)
	}
	fixture.advance()
	return candidate.Candidate
}

func passingEvaluation(title string) EvaluationData {
	return EvaluationData{
		Title: title, Body: "# " + title + "\n", Outcome: research.EvaluationPassed,
		Metrics: []research.MetricValue{{Name: "score", Unit: "points", Value: .9}}, Summary: "Gate passed.",
	}
}

func ref(document *record.Document) RevisionRef {
	id, _ := document.ID()
	return RevisionRef{ID: id, Revision: document.Revision}
}

func mustDocumentID(document *record.Document) research.ID {
	id, ok := document.ID()
	if !ok {
		panic("test document has no ID")
	}
	return id
}

func assertExperimentLifecycle(t *testing.T, inventory *record.Inventory, original *record.Document, want research.ExperimentLifecycle) {
	t.Helper()
	id, _ := original.ID()
	document, err := inventory.ByID(id)
	if err != nil || document.Record.(*research.Experiment).Lifecycle != want {
		t.Fatalf("Experiment lifecycle = %#v, %v; want %s", document, err, want)
	}
}

func assertPlanState(t *testing.T, inventory *record.Inventory, original *record.Document, want research.PlanState) {
	t.Helper()
	id, _ := original.ID()
	document, err := inventory.ByID(id)
	if err != nil || document.Record.(*research.Plan).State != want {
		t.Fatalf("Plan state = %#v, %v; want %s", document, err, want)
	}
}
