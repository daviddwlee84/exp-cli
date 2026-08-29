package research

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
