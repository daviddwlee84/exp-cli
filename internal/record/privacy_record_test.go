package record

import (
	"errors"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestPrivacyValidationCoversEveryPersistedHumanTextFamily(t *testing.T) {
	const secret = "Authorization: Bearer CANARY"
	tests := []struct {
		name   string
		load   func(*testing.T) research.Record
		mutate func(research.Record)
	}{
		{
			name:   "Project name",
			load:   func(t *testing.T) research.Record { return decodeSchemaFixture(t, "PROJECT.md").Record },
			mutate: func(record research.Record) { record.(*research.Project).Name = secret },
		},
		{
			name: "Plan title",
			load: func(t *testing.T) research.Record {
				return decodeSchemaFixture(t, "plans", "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md").Record
			},
			mutate: func(record research.Record) { record.(*research.Plan).Title = secret },
		},
		{
			name: "Experiment criterion",
			load: func(t *testing.T) research.Record {
				return decodeSchemaFixture(t, "e-01a01e67-calibrate-encoder-learning-rate", "REPORT.md").Record
			},
			mutate: func(record research.Record) { record.(*research.Experiment).Design.SuccessCriteria[0] = secret },
		},
		{
			name: "Run objective",
			load: func(t *testing.T) research.Record {
				return decodeSchemaFixture(t, "e-01a01e67-calibrate-encoder-learning-rate", "runs", "run_01a01e68-cda0-7404-8000-000000000404-baseline-candidate-comparison.md").Record
			},
			mutate: func(record research.Record) { record.(*research.Run).Objective = secret },
		},
		{
			name: "Attempt state reason",
			load: func(t *testing.T) research.Record {
				return decodeSchemaFixture(t, "e-01a01e67-calibrate-encoder-learning-rate", "attempts", "att_01a01e69-b800-7505-8000-000000000505.md").Record
			},
			mutate: func(record research.Record) { record.(*research.Attempt).StateReason = secret },
		},
		{
			name: "Finding evidence detail",
			load: func(t *testing.T) research.Record {
				return decodeSchemaFixture(t, "findings", "fnd_01a01e9c-fd00-7606-8000-000000000606-moderate-learning-rate-improves-f1.md").Record
			},
			mutate: func(record research.Record) { record.(*research.Finding).Evidence[0].Detail = secret },
		},
		{
			name: "Decision statement",
			load: func(t *testing.T) research.Record {
				return decodeSchemaFixture(t, "decisions", "dec_01a01e9d-e760-7707-8000-000000000707-adopt-moderate-learning-rate.md").Record
			},
			mutate: func(record research.Record) { record.(*research.Decision).Statement = secret },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := test.load(t)
			test.mutate(record)
			if err := research.Validate(record); !hasResearchDiagnostic(err, "privacy.secret") {
				t.Fatalf("unsafe human text validated: %v", err)
			}
		})
	}
}

func TestPrivacyEncodeRejectsSecretAttemptFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*research.Attempt)
	}{
		{"argv", func(attempt *research.Attempt) {
			attempt.Argv = []string{"curl", "--header", "Authorization: Bearer ENCODE_CANARY"}
		}},
		{"native ID", func(attempt *research.Attempt) {
			attempt.ExternalRefs[0].NativeID = "token=ENCODE_CANARY"
		}},
		{"URI fragment", func(attempt *research.Attempt) {
			attempt.ExternalRefs[0].URI = "https://example.invalid/#access_token=ENCODE_CANARY"
		}},
		{"metadata", func(attempt *research.Attempt) {
			attempt.ExternalRefs[0].Metadata = map[string]any{"mlflow.token": "ENCODE_CANARY"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := decodeSchemaFixture(t, "e-01a01e67-calibrate-encoder-learning-rate", "attempts", "att_01a01e69-b800-7505-8000-000000000505.md")
			test.mutate(document.Record.(*research.Attempt))
			encoded, err := Encode(document)
			if err == nil || len(encoded) != 0 {
				t.Fatalf("Encode persisted secret-bearing Attempt: bytes=%d err=%v", len(encoded), err)
			}
			if strings.Contains(err.Error(), "ENCODE_CANARY") {
				t.Fatalf("Encode error leaked credential canary: %v", err)
			}
		})
	}
}

func TestPrivacyValidationRejectsPlanAndCanonicalMarkdownBodies(t *testing.T) {
	for _, body := range []string{
		"Use https://alice:pw@example.invalid/run for the comparison.\n",
		"Authorization: Bearer CANARY\n",
		"-----BEGIN PRIVATE KEY-----\nCANARY\n",
	} {
		if err := validatePlanBody(body); !errors.Is(err, ErrInvalidBody) {
			t.Errorf("Plan body %q error = %v", body, err)
		} else if strings.Contains(err.Error(), "CANARY") {
			t.Errorf("Plan body error leaked credential canary: %v", err)
		}
	}

	data := readSchemaFixture(t, "plans", "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md")
	data = append(data, []byte("Authorization: Bearer CANARY\n")...)
	if _, err := Decode(data); err == nil {
		t.Fatal("credential-bearing canonical Markdown body decoded")
	} else if strings.Contains(err.Error(), "CANARY") {
		t.Fatalf("Decode error leaked body credential canary: %v", err)
	}

	document := decodeSchemaFixture(t, "plans", "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md")
	document.Body = "Authorization: Bearer CANARY\n"
	encoded, err := Encode(document)
	if err == nil || len(encoded) != 0 {
		t.Fatalf("credential-bearing programmatic body encoded: bytes=%d err=%v", len(encoded), err)
	}
}

func decodeSchemaFixture(t *testing.T, components ...string) *Document {
	t.Helper()
	document, err := Decode(readSchemaFixture(t, components...))
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func hasResearchDiagnostic(err error, code string) bool {
	for _, issue := range research.IssuesFromError(err) {
		if issue.Code == code {
			return true
		}
	}
	return false
}
