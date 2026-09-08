package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

const explorationNamespace = "io.github.daviddwlee84.exp-cli.exploration"

// Extension tables can cross JSON and TOML boundaries: integral numbers and
// arrays then have different Go representations but the same canonical value.
func sameExplorationValue(a, b any) bool {
	left, le := json.Marshal(a)
	right, re := json.Marshal(b)
	return le == nil && re == nil && bytes.Equal(left, right)
}

func validateExplorationTryUpdate(current, replacement *research.Try) error {
	before, after := current.Extensions[explorationNamespace], replacement.Extensions[explorationNamespace]
	if sameExplorationValue(before, after) {
		return nil
	}
	if current.State == research.TryAbandoned || current.State == research.TryAdopted {
		return errors.New("archived/adopted Try annotations are immutable")
	}
	if after == nil || after["schema"] != "exp.exploration-session/v1" {
		return errors.New("Try exploration annotations require a versioned session schema")
	}
	allowed := map[string]bool{"summary": true, "summary_author": true, "summary_at": true, "reviewed": true, "conclusion_author": true, "conclusion_reviewed": true, "schema": true}
	for key, value := range before {
		if !allowed[key] && !sameExplorationValue(value, after[key]) {
			return errors.New("Try exploration registration is immutable")
		}
	}
	for key := range after {
		if !allowed[key] {
			if _, ok := before[key]; !ok {
				return errors.New("cannot add exploration registration fields after Try creation")
			}
		}
	}
	if summary, ok := after["summary"]; ok {
		text, textOK := summary.(string)
		author, _ := after["summary_author"].(string)
		at, _ := after["summary_at"].(string)
		reviewed, reviewOK := after["reviewed"].(bool)
		if !textOK || text == "" || len(text) > research.MaxTryConclusionSummaryBytes || (author != "agent" && author != "human") || !reviewOK || reviewed != (author == "human") {
			return errors.New("invalid attributed exploration summary")
		}
		parsed, err := time.Parse(time.RFC3339Nano, at)
		if err != nil || parsed.Before(current.CreatedAt) {
			return errors.New("invalid exploration summary timestamp")
		}
		if previous, _ := before["summary_at"].(string); previous != "" {
			old, err := time.Parse(time.RFC3339Nano, previous)
			if err != nil || parsed.Before(old) {
				return errors.New("summary timestamp cannot move backwards")
			}
		}
	}
	if before["conclusion_author"] != nil {
		if !sameExplorationValue(before["conclusion_author"], after["conclusion_author"]) || !sameExplorationValue(before["conclusion_reviewed"], after["conclusion_reviewed"]) {
			return errors.New("conclusion attribution is immutable")
		}
	} else if after["conclusion_author"] != nil {
		if current.State != research.TryOpen || replacement.State != research.TryConcluded || after["conclusion_author"] != "agent" || after["conclusion_reviewed"] != false {
			return errors.New("agent attribution can only accompany an unreviewed Try conclusion")
		}
	}
	return nil
}

func validateExplorationAttemptUpdate(current, replacement *research.Attempt) error {
	before, after := current.Extensions[explorationNamespace], replacement.Extensions[explorationNamespace]
	if sameExplorationValue(before, after) {
		return nil
	}
	if before == nil || after == nil {
		return errors.New("exploration execution registration cannot be added or removed")
	}
	for _, key := range []string{"schema_version", "storage", "context_digest", "inputs"} {
		if !sameExplorationValue(before[key], after[key]) {
			return errors.New("exploration input and storage identities are immutable")
		}
	}
	if before["runner"] != nil {
		if !sameExplorationValue(before["runner"], after["runner"]) {
			return errors.New("captured runner identity is immutable")
		}
	} else if after["runner"] != nil && current.State != research.AttemptPlanned {
		return errors.New("runner identity must be captured before dispatch")
	}
	if sameExplorationValue(before["artifacts"], after["artifacts"]) && sameExplorationValue(before["archive_state"], after["archive_state"]) {
		return nil
	}
	if !terminalAttemptState(current.State) {
		return errors.New("artifacts may only be published after execution is terminal")
	}
	if before["archive_state"] == "saved" && after["archive_state"] != "saved" {
		return errors.New("saved artifacts cannot return to pending")
	}
	var oldFiles, newFiles []map[string]any
	oldBytes, _ := json.Marshal(before["artifacts"])
	newBytes, _ := json.Marshal(after["artifacts"])
	if json.Unmarshal(oldBytes, &oldFiles) != nil || json.Unmarshal(newBytes, &newFiles) != nil {
		return errors.New("invalid artifact manifest")
	}
	if len(oldFiles) > 0 && len(oldFiles) != len(newFiles) {
		return errors.New("published artifact list is immutable")
	}
	for i, old := range oldFiles {
		for _, key := range []string{"name", "media_type", "bytes", "digest", "storage"} {
			if !reflect.DeepEqual(old[key], newFiles[i][key]) {
				return errors.New("published artifact content identity is immutable")
			}
		}
		if old["run_id"] != nil && !reflect.DeepEqual(old["run_id"], newFiles[i]["run_id"]) {
			return errors.New("published MLflow run identity is immutable")
		}
	}
	return nil
}
