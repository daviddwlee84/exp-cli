// Package research defines exp's canonical research records and invariants.
package research

import (
	"errors"
	"fmt"
	"strings"
)

// Kind identifies one canonical record kind.
type Kind string

const (
	KindUnknown    Kind = ""
	KindProject    Kind = "project"
	KindPlan       Kind = "plan"
	KindExperiment Kind = "experiment"
	KindRun        Kind = "run"
	KindAttempt    Kind = "attempt"
	KindFinding    Kind = "finding"
	KindDecision   Kind = "decision"
)

// Schema identifies an exact on-disk decoder.
type Schema string

const (
	SchemaProject    Schema = "exp.project/v1"
	SchemaPlan       Schema = "exp.plan/v1"
	SchemaExperiment Schema = "exp.experiment/v1"
	SchemaRun        Schema = "exp.run/v1"
	SchemaAttempt    Schema = "exp.attempt/v1"
	SchemaFinding    Schema = "exp.finding/v1"
	SchemaDecision   Schema = "exp.decision/v1"
)

var (
	ErrUnknownKind   = errors.New("unknown research record kind")
	ErrUnknownSchema = errors.New("unknown research record schema")
)

// RecordKinds is the deterministic order used by inventories.
var RecordKinds = []Kind{
	KindProject,
	KindPlan,
	KindExperiment,
	KindRun,
	KindAttempt,
	KindFinding,
	KindDecision,
}

func (k Kind) String() string { return string(k) }

// Valid reports whether k names a canonical kind.
func (k Kind) Valid() bool {
	_, err := k.Schema()
	return err == nil
}

// Schema returns the exact v1 schema for k.
func (k Kind) Schema() (Schema, error) {
	switch k {
	case KindProject:
		return SchemaProject, nil
	case KindPlan:
		return SchemaPlan, nil
	case KindExperiment:
		return SchemaExperiment, nil
	case KindRun:
		return SchemaRun, nil
	case KindAttempt:
		return SchemaAttempt, nil
	case KindFinding:
		return SchemaFinding, nil
	case KindDecision:
		return SchemaDecision, nil
	default:
		return "", fmt.Errorf("kind %q: %w", k, ErrUnknownKind)
	}
}

// KindForSchema selects the exact decoder for schema.
func KindForSchema(schema Schema) (Kind, error) {
	switch schema {
	case SchemaProject:
		return KindProject, nil
	case SchemaPlan:
		return KindPlan, nil
	case SchemaExperiment:
		return KindExperiment, nil
	case SchemaRun:
		return KindRun, nil
	case SchemaAttempt:
		return KindAttempt, nil
	case SchemaFinding:
		return KindFinding, nil
	case SchemaDecision:
		return KindDecision, nil
	default:
		return KindUnknown, fmt.Errorf("schema %q: %w", schema, ErrUnknownSchema)
	}
}

// IDPrefix is the persisted typed-ID prefix. Project has a bare UUID.
func (k Kind) IDPrefix() (string, error) {
	switch k {
	case KindPlan:
		return "plan_", nil
	case KindExperiment:
		return "exp_", nil
	case KindRun:
		return "run_", nil
	case KindAttempt:
		return "att_", nil
	case KindFinding:
		return "fnd_", nil
	case KindDecision:
		return "dec_", nil
	case KindProject:
		return "", fmt.Errorf("project uses a bare UUID: %w", ErrUnknownKind)
	default:
		return "", fmt.Errorf("kind %q: %w", k, ErrUnknownKind)
	}
}

// DisplayLetter is the kind letter used by short display codes.
func (k Kind) DisplayLetter() (byte, error) {
	switch k {
	case KindPlan:
		return 'P', nil
	case KindExperiment:
		return 'E', nil
	case KindRun:
		return 'R', nil
	case KindAttempt:
		return 'A', nil
	case KindFinding:
		return 'F', nil
	case KindDecision:
		return 'D', nil
	default:
		return 0, fmt.Errorf("kind %q has no display letter: %w", k, ErrUnknownKind)
	}
}

// KindForDisplayLetter resolves a short-code letter case-insensitively.
func KindForDisplayLetter(letter byte) (Kind, error) {
	switch strings.ToUpper(string(letter)) {
	case "P":
		return KindPlan, nil
	case "E":
		return KindExperiment, nil
	case "R":
		return KindRun, nil
	case "A":
		return KindAttempt, nil
	case "F":
		return KindFinding, nil
	case "D":
		return KindDecision, nil
	default:
		return KindUnknown, fmt.Errorf("display letter %q: %w", letter, ErrUnknownKind)
	}
}
