package research

import (
	"testing"
	"time"
)

func TestSchemaValidationRequiresSecondaryFactorsArrayEvenWhenEmpty(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	experiment := &Experiment{
		Common: Common{
			Schema: SchemaExperiment,
			ID:     mustID(t, "exp_01a01e67-e340-7303-8000-000000000303"),
			Title:  "Experiment", CreatedAt: now, UpdatedAt: now,
		},
		Lifecycle: LifecyclePlanned,
		Design: Design{
			Question: "Which setting improves accuracy?", Hypothesis: "The candidate improves accuracy.",
			Kind: ExperimentSingleFactor, PrimaryFactor: "learning rate", Baseline: "baseline",
			ComparabilitySpec: "same split and seed", SuccessCriteria: []string{"accuracy improves"},
			DecisionRule: "adopt only if accuracy improves",
		},
	}
	if err := Validate(experiment); !hasIssueCode(err, "record.list_required") {
		t.Fatalf("nil secondary_factors validated: %v", err)
	}
	experiment.Design.SecondaryFactors = []string{}
	if err := Validate(experiment); err != nil {
		t.Fatalf("present empty secondary_factors rejected: %v", err)
	}
}
