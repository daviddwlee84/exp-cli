package research

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// DesignDigest computes the exact RFC v1 digest over the nine scientific design fields.
func DesignDigest(design Design) (string, error) {
	value := map[string]any{
		"question":           design.Question,
		"hypothesis":         design.Hypothesis,
		"kind":               design.Kind,
		"primary_factor":     design.PrimaryFactor,
		"secondary_factors":  design.SecondaryFactors,
		"baseline":           design.Baseline,
		"comparability_spec": design.ComparabilitySpec,
		"success_criteria":   design.SuccessCriteria,
		"decision_rule":      design.DecisionRule,
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", fmt.Errorf("encode design digest input: %w", err)
	}
	data := bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// SourceSnapshotDigest computes the v1 host-independent digest over every
// SourceSnapshot field except Digest itself. Callers must supply the canonical
// Source-ID order and sorted change set enforced by Validate.
func SourceSnapshotDigest(snapshot SourceSnapshot) (string, error) {
	value := struct {
		Domain          string              `json:"domain"`
		Source          string              `json:"source"`
		Subdir          string              `json:"subdir"`
		PolicyVersion   string              `json:"policy_version"`
		CapturedAt      string              `json:"captured_at"`
		GitObjectFormat GitObjectFormat     `json:"git_object_format"`
		BaseCommit      string              `json:"base_commit"`
		HeadCommit      string              `json:"head_commit"`
		ChangeSet       []string            `json:"change_set"`
		State           SourceSnapshotState `json:"state"`
		DirtyDigest     string              `json:"dirty_digest,omitempty"`
		DirtySummary    string              `json:"dirty_summary,omitempty"`
		Reproducibility Reproducibility     `json:"reproducibility"`
	}{
		Domain: "exp.source-snapshot/v1", Source: snapshot.Source.String(),
		Subdir: snapshot.Subdir, PolicyVersion: snapshot.PolicyVersion,
		CapturedAt: snapshot.CapturedAt.UTC().Format(time.RFC3339Nano), GitObjectFormat: snapshot.GitObjectFormat,
		BaseCommit: snapshot.BaseCommit, HeadCommit: snapshot.HeadCommit,
		ChangeSet: append([]string{}, snapshot.ChangeSet...), State: snapshot.State,
		DirtyDigest: snapshot.DirtyDigest, DirtySummary: snapshot.DirtySummary,
		Reproducibility: snapshot.Reproducibility,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode Source snapshot digest input: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
