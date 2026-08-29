package provider

import (
	"fmt"
	"sort"
)

// EffectVocabularyVersion identifies the exact reviewed effect set used by
// operation plans.
const EffectVocabularyVersion = "exp.effect/v1"

// Effect is one declared side effect of a provider operation.
type Effect string

const (
	EffectLocalRead        Effect = "local_read"
	EffectRemoteRead       Effect = "remote_read"
	EffectLocalWrite       Effect = "local_write"
	EffectRemoteWrite      Effect = "remote_write"
	EffectExecutesUserCode Effect = "executes_user_code"
	EffectStartsService    Effect = "starts_service"
	EffectCredentialFlow   Effect = "credential_flow"
	EffectDestructive      Effect = "destructive"
	EffectSensitiveOutput  Effect = "sensitive_output"
	EffectBlocking         Effect = "blocking"
)

var effectVocabulary = []Effect{
	EffectLocalRead,
	EffectRemoteRead,
	EffectLocalWrite,
	EffectRemoteWrite,
	EffectExecutesUserCode,
	EffectStartsService,
	EffectCredentialFlow,
	EffectDestructive,
	EffectSensitiveOutput,
	EffectBlocking,
}

var effectIndexes = func() map[Effect]int {
	indexes := make(map[Effect]int, len(effectVocabulary))
	for index, effect := range effectVocabulary {
		indexes[effect] = index
	}
	return indexes
}()

// AllEffects returns the closed v1 vocabulary in contract order.
func AllEffects() []Effect { return append([]Effect(nil), effectVocabulary...) }

// Valid reports whether e belongs to the exact v1 vocabulary.
func (e Effect) Valid() bool {
	_, valid := effectIndexes[e]
	return valid
}

// EffectSet is a versioned, stable, duplicate-free effect collection.
type EffectSet struct {
	Version string   `json:"version"`
	Values  []Effect `json:"values"`
}

// NewEffectSet validates and orders effects in documented contract order.
func NewEffectSet(effects ...Effect) (EffectSet, error) {
	set := EffectSet{Version: EffectVocabularyVersion, Values: append([]Effect(nil), effects...)}
	if err := set.Validate(); err != nil {
		return EffectSet{}, err
	}
	set.sort()
	if set.Values == nil {
		set.Values = []Effect{}
	}
	return set, nil
}

// Validate rejects a mismatched vocabulary, unknown effects, and duplicates.
func (s EffectSet) Validate() error {
	if s.Version != EffectVocabularyVersion {
		return fmt.Errorf("unsupported effect vocabulary version")
	}
	seen := make(map[Effect]struct{}, len(s.Values))
	for _, effect := range s.Values {
		if !effect.Valid() {
			return fmt.Errorf("unknown effect value")
		}
		if _, duplicate := seen[effect]; duplicate {
			return fmt.Errorf("duplicate effect %q", effect)
		}
		seen[effect] = struct{}{}
	}
	return nil
}

// Has reports whether effect belongs to the set.
func (s EffectSet) Has(effect Effect) bool {
	for _, candidate := range s.Values {
		if candidate == effect {
			return true
		}
	}
	return false
}

func (s *EffectSet) sort() {
	sort.Slice(s.Values, func(i, j int) bool {
		return effectIndexes[s.Values[i]] < effectIndexes[s.Values[j]]
	})
}

func (s EffectSet) normalized() EffectSet {
	out := EffectSet{Version: s.Version, Values: append([]Effect(nil), s.Values...)}
	out.sort()
	if out.Values == nil {
		out.Values = []Effect{}
	}
	return out
}
