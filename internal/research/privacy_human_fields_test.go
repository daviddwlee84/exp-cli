package research

import (
	"testing"
	"time"
)

func TestCommitSafeValidationCoversHumanStringsAcrossRecordKinds(t *testing.T) {
	unsafe := "Authorization: Bearer HUMAN_FIELD_CANARY_7e23"
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	common := func(schema Schema, id string, title string) Common {
		return Common{Schema: schema, ID: mustID(t, id), Title: title, CreatedAt: now, UpdatedAt: now}
	}

	records := map[string]Record{
		"project name": func() Record {
			id, err := ParseUUID("01a01e66-0e80-7101-8000-000000000101")
			if err != nil {
				t.Fatal(err)
			}
			return &Project{Schema: SchemaProject, ProjectID: id, Name: unsafe, CreatedAt: now, ExperimentsRoot: "."}
		}(),
		"experiment design": &Experiment{
			Common:    common(SchemaExperiment, "exp_01a01e67-e340-7303-8000-000000000303", "Experiment"),
			Lifecycle: LifecyclePlanned,
			Design: Design{
				Question: unsafe, Hypothesis: "Hypothesis", Kind: ExperimentSingleFactor,
				PrimaryFactor: "learning rate", SecondaryFactors: []string{}, Baseline: "baseline",
				ComparabilitySpec: "same protocol", SuccessCriteria: []string{"score improves"}, DecisionRule: "adopt if improved",
			},
		},
		"run objective": &Run{
			Common:     common(SchemaRun, "run_01a01e68-cda0-7404-8000-000000000404", "Run"),
			Experiment: mustID(t, "exp_01a01e67-e340-7303-8000-000000000303"), Role: RunBatch, Objective: unsafe,
		},
		"attempt state reason": func() Record {
			attempt := validPrivacyAttempt(t)
			attempt.StateReason = unsafe
			return attempt
		}(),
		"finding statement": &Finding{
			Common:    common(SchemaFinding, "fnd_01a01e9c-fd00-7606-8000-000000000606", "Finding"),
			Statement: unsafe, Scope: "Recorded protocol",
			Evidence: []FindingEvidence{{Kind: FindingEvidenceRun, Ref: mustID(t, "run_01a01e68-cda0-7404-8000-000000000404")}},
		},
		"finding evidence detail": &Finding{
			Common:    common(SchemaFinding, "fnd_01a01e9c-fd00-7606-8000-000000000606", "Finding"),
			Statement: "Score improved", Scope: "Recorded protocol",
			Evidence: []FindingEvidence{{Kind: FindingEvidenceRun, Ref: mustID(t, "run_01a01e68-cda0-7404-8000-000000000404"), Detail: unsafe}},
		},
		"decision statement": &Decision{
			Common:    common(SchemaDecision, "dec_01a01e9d-e760-7707-8000-000000000707", "Decision"),
			Statement: unsafe, BasedOn: []ID{mustID(t, "fnd_01a01e9c-fd00-7606-8000-000000000606")},
			Action: "Update configuration", EffectiveAt: now,
		},
		"decision action": &Decision{
			Common:    common(SchemaDecision, "dec_01a01e9d-e760-7707-8000-000000000707", "Decision"),
			Statement: "Adopt candidate", BasedOn: []ID{mustID(t, "fnd_01a01e9c-fd00-7606-8000-000000000606")},
			Action: unsafe, EffectiveAt: now,
		},
	}

	for name, record := range records {
		t.Run(name, func(t *testing.T) {
			if err := Validate(record); !hasIssueCode(err, "privacy.secret") {
				t.Fatalf("credential-bearing human field validated: %v", err)
			}
		})
	}
}
