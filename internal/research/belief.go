package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

type BeliefRelation string

const (
	BeliefWeakens   BeliefRelation = "weakens"
	BeliefOverturns BeliefRelation = "overturns"
)

// BeliefInfluence pins one incoming belief-changing edge and the revision of
// the Finding that owns it. Including the source revision prevents a rewritten
// rationale from silently retaining an old dependency digest.
type BeliefInfluence struct {
	Source   ID             `json:"source"`
	Relation BeliefRelation `json:"relation"`
	Revision string         `json:"revision"`
}

// ComputeBeliefDigest returns a deterministic digest of a Finding revision and
// all incoming weakens/overturns edges. Inputs are copied and sorted.
func ComputeBeliefDigest(finding ID, findingRevision string, incoming []BeliefInfluence) (string, error) {
	if finding.IsZero() || finding.Kind() != KindFinding {
		return "", fmt.Errorf("belief target must be a Finding: %w", ErrWrongIDKind)
	}
	if !validDigest(findingRevision) {
		return "", fmt.Errorf("belief target revision is not a canonical sha256 digest")
	}
	edges := append([]BeliefInfluence(nil), incoming...)
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if edge.Source.IsZero() || edge.Source.Kind() != KindFinding {
			return "", fmt.Errorf("belief influence source must be a Finding: %w", ErrWrongIDKind)
		}
		if edge.Relation != BeliefWeakens && edge.Relation != BeliefOverturns {
			return "", fmt.Errorf("unknown belief relation %q", edge.Relation)
		}
		if !validDigest(edge.Revision) {
			return "", fmt.Errorf("belief influence %s revision is not a canonical sha256 digest", edge.Source)
		}
		key := edge.Source.String() + "\x00" + string(edge.Relation)
		if _, duplicate := seen[key]; duplicate {
			return "", fmt.Errorf("duplicate belief influence %s %s", edge.Source, edge.Relation)
		}
		seen[key] = struct{}{}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source.String() < edges[j].Source.String()
		}
		return edges[i].Relation < edges[j].Relation
	})
	payload := struct {
		Finding  string            `json:"finding"`
		Revision string            `json:"revision"`
		Incoming []BeliefInfluence `json:"incoming"`
	}{Finding: finding.String(), Revision: findingRevision, Incoming: edges}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode belief digest input: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
