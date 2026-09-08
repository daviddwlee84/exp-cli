package record

import (
	"github.com/daviddwlee84/exp-cli/internal/research"
	"testing"
	"time"
)

func TestExplorationMetadataEvolutionPreservesEvidenceIdentity(t *testing.T) {
	current := &research.Attempt{State: research.AttemptSucceeded, Extensions: research.Extensions{explorationNamespace: {"schema_version": "exp.exploration/v1", "storage": "local", "context_digest": "fixed", "inputs": []any{map[string]any{"bytes": int64(11)}}, "artifacts": []any{}, "archive_state": "pending"}}}
	updated := research.Clone(current).(*research.Attempt)
	updated.Extensions[explorationNamespace]["inputs"] = []any{map[string]any{"bytes": float64(11)}}
	updated.Extensions[explorationNamespace]["artifacts"] = []any{map[string]any{"name": "plot.svg", "digest": "fixed-artifact", "bytes": float64(5), "storage": "local", "media_type": "image/svg+xml", "state": "local"}}
	updated.Extensions[explorationNamespace]["archive_state"] = "saved"
	if err := validateExplorationAttemptUpdate(current, updated); err != nil {
		t.Fatal(err)
	}
	corrupt := research.Clone(updated).(*research.Attempt)
	corrupt.Extensions[explorationNamespace]["context_digest"] = "different"
	if err := validateExplorationAttemptUpdate(updated, corrupt); err == nil {
		t.Fatal("storage identity was mutable")
	}
	corrupt = research.Clone(updated).(*research.Attempt)
	corrupt.Extensions[explorationNamespace]["artifacts"] = []any{}
	if err := validateExplorationAttemptUpdate(updated, corrupt); err == nil {
		t.Fatal("published artifacts were removable")
	}
	current.State = research.AttemptRunning
	if err := validateExplorationAttemptUpdate(current, updated); err == nil {
		t.Fatal("published artifacts from an active workload")
	}
}

func TestExplorationSummaryCannotRewriteTryRegistrationOrHumanReview(t *testing.T) {
	now := time.Now().UTC()
	current := &research.Try{Common: research.Common{CreatedAt: now, UpdatedAt: now}, State: research.TryOpen, Goal: "original", Extensions: research.Extensions{explorationNamespace: {"schema": "exp.exploration-session/v1", "scratch": true}}}
	updated := research.Clone(current).(*research.Try)
	table := updated.Extensions[explorationNamespace]
	table["summary"] = "observed a pattern"
	table["summary_author"] = "agent"
	table["summary_at"] = now.Format(time.RFC3339Nano)
	table["reviewed"] = false
	if err := validateTryUpdate(current, updated, "body", "body"); err != nil {
		t.Fatal(err)
	}
	updated.Goal = "rewritten"
	if err := validateTryUpdate(current, updated, "body", "body"); err == nil {
		t.Fatal("summary edit changed the registered goal")
	}
	updated.Goal = current.Goal
	table["reviewed"] = true
	if err := validateTryUpdate(current, updated, "body", "body"); err == nil {
		t.Fatal("agent summary claimed human review")
	}
}
