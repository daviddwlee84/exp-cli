package research

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestDesignDigestMatchesRFCFixture(t *testing.T) {
	design := Design{
		Question:          "Which encoder learning rate improves held-out macro-F1 under a frozen training protocol?",
		Hypothesis:        "A learning rate of 0.0003 improves macro-F1 by at least 0.03 over the 0.0001 baseline.",
		Kind:              ExperimentSingleFactor,
		PrimaryFactor:     "learning_rate",
		SecondaryFactors:  []string{},
		Baseline:          "learning_rate=0.0001",
		ComparabilitySpec: "same split, seed, tokenizer, batch size, epochs, and evaluation code",
		SuccessCriteria:   []string{"candidate macro-F1 minus baseline macro-F1 >= 0.03"},
		DecisionRule:      "Adopt 0.0003 only if the success criterion is met on the held-out split.",
	}
	got, err := DesignDigest(design)
	if err != nil {
		t.Fatal(err)
	}
	const want = "sha256:5cfa52900fa35b522bc21a2edeead0c90c71b3ac15c4d4cf406190650fd43e5f"
	if got != want {
		t.Fatalf("DesignDigest = %s, want %s", got, want)
	}
}

func TestValidatePlanMigrationAndPayoffInvariants(t *testing.T) {
	now := time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)
	plan := &Plan{
		Common:         Common{Schema: SchemaPlan, ID: mustID(t, "plan_01a01e66-f8e0-7202-8000-000000000202"), Title: "Unicode 学習率", CreatedAt: now, UpdatedAt: now},
		Priority:       PriorityP1,
		Effort:         EffortS,
		State:          PlanQueued,
		ExpectedPayoff: ExpectedPayoff{Summary: "Improve score", Metric: "macro_f1", Unit: "score"},
	}
	if err := Validate(plan); err != nil {
		t.Fatalf("valid Plan: %v", err)
	}
	estimate := math.Inf(1)
	plan.ExpectedPayoff.Estimate = &estimate
	if err := Validate(plan); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("non-finite estimate = %v", err)
	}

	plan.ExpectedPayoff.Estimate = nil
	plan.ID = mustID(t, "plan_74738ff5-5367-5958-9aee-98fffdcd1876")
	if err := Validate(plan); err == nil {
		t.Fatal("UUIDv5 native Plan unexpectedly validated")
	}
	plan.Extensions = Extensions{MigrationExtension: {"fingerprint": "synthetic"}}
	if err := Validate(plan); !hasIssueCode(err, "record.id_version") {
		t.Fatalf("migration extension authorized UUIDv5 in ordinary validation: %v", err)
	}
}

func TestValidateAttemptSeparatesOperationalAndScientificState(t *testing.T) {
	now := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)
	attempt := &Attempt{
		Common: Common{Schema: SchemaAttempt, ID: mustID(t, "att_01a01e69-b800-7505-8000-000000000505"), Title: "Attempt", CreatedAt: now, UpdatedAt: now.Add(time.Minute)},
		Run:    mustID(t, "run_01a01e68-cda0-7404-8000-000000000404"),
		State:  AttemptSucceeded,
		Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"python3", "train.py"},
	}
	if err := Validate(attempt); err == nil {
		t.Fatal("terminal state without terminal observation validated")
	}
	ended := now.Add(30 * time.Second)
	attempt.Terminal = &Terminal{Source: "direct", ObservedAt: now.Add(time.Minute), EndedAt: ended}
	if err := Validate(attempt); err != nil {
		t.Fatalf("valid terminal Attempt: %v", err)
	}
	attempt.State = AttemptRunning
	if err := Validate(attempt); err == nil {
		t.Fatal("nonterminal state with terminal observation validated")
	}
}

func TestCommittedPathAndURIPolicy(t *testing.T) {
	for _, value := range []string{"artifacts/metrics.json", "configs/学習.toml"} {
		if err := ValidateCommittedPath(value, false); err != nil {
			t.Errorf("safe path %q: %v", value, err)
		}
	}
	if err := ValidateCommittedPath(".", true); err != nil {
		t.Errorf("cwd root: %v", err)
	}
	for _, value := range []string{"/tmp/x", `C:\\Users\\x`, `..\\escape`, "a//b", "~/secret"} {
		if err := ValidateCommittedPath(value, false); err == nil {
			t.Errorf("unsafe path %q validated", value)
		}
	}
	if err := ValidateCommittedURI("https://mlflow.example.invalid/#/runs/1"); err != nil {
		t.Fatalf("safe URI: %v", err)
	}
	for _, value := range []string{"https://user:pass@example.invalid/run", "https://example.invalid/run?token=x", "file:///tmp/run"} {
		if err := ValidateCommittedURI(value); err == nil {
			t.Errorf("unsafe URI %q validated", value)
		}
	}
}
