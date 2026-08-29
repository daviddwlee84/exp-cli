package research_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestProviderExternalRefMetadataRoundTripsThroughResearchRecord(t *testing.T) {
	reference, err := provider.NewExternalRef(provider.ExternalRef{
		Role:       provider.ExternalRoleTracker,
		Provider:   "future-tracker",
		Context:    "local",
		NativeKind: "run",
		NativeID:   "native-run-7",
		Metadata: map[string]any{
			"future-tracker.run_id":      json.Number("7"),
			"future-tracker.score_value": json.Number("0.25"),
			"future-tracker.details": map[string]any{
				"labels": []string{"baseline", "candidate"},
			},
		},
	})
	if err != nil {
		t.Fatalf("provider NewExternalRef() error = %v", err)
	}
	if _, ok := reference.Metadata["future-tracker.run_id"].(int64); !ok {
		t.Fatalf("integer metadata type = %T", reference.Metadata["future-tracker.run_id"])
	}
	if _, ok := reference.Metadata["future-tracker.score_value"].(float64); !ok {
		t.Fatalf("float metadata type = %T", reference.Metadata["future-tracker.score_value"])
	}

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	attemptID, err := research.ParseID("att_01a01e69-b800-7505-8000-000000000505")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := research.ParseID("run_01a01e68-cda0-7404-8000-000000000404")
	if err != nil {
		t.Fatal(err)
	}
	attempt := &research.Attempt{
		Common: research.Common{
			Schema: research.SchemaAttempt, ID: attemptID, Title: "Attempt",
			CreatedAt: now, UpdatedAt: now,
		},
		Run: runID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "direct",
		CWD: ".", Argv: []string{"python3", "train.py"},
		ExternalRefs: []research.ExternalRef{{
			Role:       research.ExternalRole(reference.Role),
			Provider:   string(reference.Provider),
			Context:    string(reference.Context),
			NativeKind: string(reference.NativeKind),
			NativeID:   string(reference.NativeID),
			Metadata:   reference.Metadata,
		}},
	}
	encoded, err := record.Encode(&record.Document{Record: attempt, Body: "\n# Attempt\n"})
	if err != nil {
		t.Fatalf("provider-produced reference did not encode as a research record: %v", err)
	}
	decoded, err := record.Decode(encoded)
	if err != nil {
		t.Fatalf("provider-produced reference did not decode as a research record: %v", err)
	}
	metadata := decoded.Record.(*research.Attempt).ExternalRefs[0].Metadata
	if _, ok := metadata["future-tracker.run_id"].(int64); !ok {
		t.Fatalf("round-tripped integer metadata type = %T", metadata["future-tracker.run_id"])
	}
	if _, ok := metadata["future-tracker.score_value"].(float64); !ok {
		t.Fatalf("round-tripped float metadata type = %T", metadata["future-tracker.score_value"])
	}
}

func TestProviderExternalRefAtByteLimitHasIdenticalResearchProjection(t *testing.T) {
	candidate := provider.ExternalRef{
		Role:       provider.ExternalRoleTracker,
		Provider:   "future-tracker",
		Context:    "local",
		NativeKind: "run",
		NativeID:   "native-run-boundary",
		Metadata:   map[string]any{"future-tracker.note": ""},
	}
	base, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("marshal base provider reference: %v", err)
	}
	candidate.Metadata["future-tracker.note"] = strings.Repeat("x", provider.MaxExternalRefBytes-len(base))
	reference, err := provider.NewExternalRef(candidate)
	if err != nil {
		t.Fatalf("provider rejected boundary reference: %v", err)
	}
	providerJSON, err := json.Marshal(reference)
	if err != nil {
		t.Fatalf("marshal provider reference: %v", err)
	}
	if len(providerJSON) != provider.MaxExternalRefBytes {
		t.Fatalf("provider reference size = %d, want %d", len(providerJSON), provider.MaxExternalRefBytes)
	}

	researchReference := research.ExternalRef{
		Role:       research.ExternalRole(reference.Role),
		Provider:   string(reference.Provider),
		Context:    string(reference.Context),
		NativeKind: string(reference.NativeKind),
		NativeID:   string(reference.NativeID),
		URI:        reference.URI,
		ObservedAt: reference.ObservedAt,
		Metadata:   reference.Metadata,
	}
	researchJSON, err := json.Marshal(researchReference)
	if err != nil {
		t.Fatalf("marshal research reference: %v", err)
	}
	if !bytes.Equal(researchJSON, providerJSON) {
		t.Fatalf("provider/research size projections differ: %d != %d", len(providerJSON), len(researchJSON))
	}

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	attemptID, err := research.ParseID("att_01a01e69-b800-7505-8000-000000000505")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := research.ParseID("run_01a01e68-cda0-7404-8000-000000000404")
	if err != nil {
		t.Fatal(err)
	}
	attempt := &research.Attempt{
		Common: research.Common{
			Schema: research.SchemaAttempt, ID: attemptID, Title: "Attempt",
			CreatedAt: now, UpdatedAt: now,
		},
		Run: runID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "direct",
		CWD: ".", Argv: []string{"python3", "train.py"}, ExternalRefs: []research.ExternalRef{researchReference},
	}
	if err := research.Validate(attempt); err != nil {
		t.Fatalf("research rejected provider-valid boundary reference: %v", err)
	}
}
