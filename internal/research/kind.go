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
	KindUnknown        Kind = ""
	KindProject        Kind = "project"
	KindPolicy         Kind = "policy"
	KindIdea           Kind = "idea"
	KindResourcePool   Kind = "resource_pool"
	KindQueue          Kind = "queue"
	KindQueueAdvice    Kind = "queue_advice"
	KindBattle         Kind = "battle"
	KindPlan           Kind = "plan"
	KindExperiment     Kind = "experiment"
	KindRun            Kind = "run"
	KindAttempt        Kind = "attempt"
	KindEvaluationSpec Kind = "evaluation_spec"
	KindEvaluation     Kind = "evaluation"
	KindFinding        Kind = "finding"
	KindCandidate      Kind = "candidate"
	KindRelease        Kind = "release"
	KindPromotionSpec  Kind = "promotion_spec"
	KindPromotion      Kind = "promotion"
	KindDecision       Kind = "decision"
)

// Schema identifies an exact on-disk decoder.
type Schema string

const (
	SchemaProject        Schema = "exp.project/v1"
	SchemaPolicy         Schema = "exp.policy/v1"
	SchemaIdea           Schema = "exp.idea/v1"
	SchemaResourcePool   Schema = "exp.resource-pool/v1"
	SchemaQueue          Schema = "exp.queue/v1"
	SchemaQueueAdvice    Schema = "exp.queue-advice/v1"
	SchemaBattle         Schema = "exp.battle/v1"
	SchemaPlan           Schema = "exp.plan/v1"
	SchemaPlanV2         Schema = "exp.plan/v2"
	SchemaExperiment     Schema = "exp.experiment/v1"
	SchemaExperimentV2   Schema = "exp.experiment/v2"
	SchemaRun            Schema = "exp.run/v1"
	SchemaAttempt        Schema = "exp.attempt/v1"
	SchemaAttemptV2      Schema = "exp.attempt/v2"
	SchemaEvaluationSpec Schema = "exp.evaluation-spec/v1"
	SchemaEvaluation     Schema = "exp.evaluation/v1"
	SchemaFinding        Schema = "exp.finding/v1"
	SchemaCandidate      Schema = "exp.candidate/v1"
	SchemaRelease        Schema = "exp.release/v1"
	SchemaPromotionSpec  Schema = "exp.promotion-spec/v1"
	SchemaPromotion      Schema = "exp.promotion/v1"
	SchemaDecision       Schema = "exp.decision/v1"
)

var (
	ErrUnknownKind   = errors.New("unknown research record kind")
	ErrUnknownSchema = errors.New("unknown research record schema")
)

// RecordKinds is the deterministic order used by inventories.
var RecordKinds = []Kind{
	KindProject,
	KindPolicy,
	KindIdea,
	KindResourcePool,
	KindQueue,
	KindQueueAdvice,
	KindBattle,
	KindPlan,
	KindExperiment,
	KindRun,
	KindAttempt,
	KindEvaluationSpec,
	KindEvaluation,
	KindFinding,
	KindCandidate,
	KindRelease,
	KindPromotionSpec,
	KindPromotion,
	KindDecision,
}

func (k Kind) String() string { return string(k) }

// Valid reports whether k names a canonical kind.
func (k Kind) Valid() bool {
	_, err := k.Schema()
	return err == nil
}

// Schema returns the baseline schema for k. Versioned producers may select a
// newer schema constant explicitly; KindForSchema accepts every supported
// exact decoder.
func (k Kind) Schema() (Schema, error) {
	switch k {
	case KindProject:
		return SchemaProject, nil
	case KindPolicy:
		return SchemaPolicy, nil
	case KindIdea:
		return SchemaIdea, nil
	case KindResourcePool:
		return SchemaResourcePool, nil
	case KindQueue:
		return SchemaQueue, nil
	case KindQueueAdvice:
		return SchemaQueueAdvice, nil
	case KindBattle:
		return SchemaBattle, nil
	case KindPlan:
		return SchemaPlan, nil
	case KindExperiment:
		return SchemaExperiment, nil
	case KindRun:
		return SchemaRun, nil
	case KindAttempt:
		return SchemaAttempt, nil
	case KindEvaluationSpec:
		return SchemaEvaluationSpec, nil
	case KindEvaluation:
		return SchemaEvaluation, nil
	case KindFinding:
		return SchemaFinding, nil
	case KindCandidate:
		return SchemaCandidate, nil
	case KindRelease:
		return SchemaRelease, nil
	case KindPromotionSpec:
		return SchemaPromotionSpec, nil
	case KindPromotion:
		return SchemaPromotion, nil
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
	case SchemaPolicy:
		return KindPolicy, nil
	case SchemaIdea:
		return KindIdea, nil
	case SchemaResourcePool:
		return KindResourcePool, nil
	case SchemaQueue:
		return KindQueue, nil
	case SchemaQueueAdvice:
		return KindQueueAdvice, nil
	case SchemaBattle:
		return KindBattle, nil
	case SchemaPlan, SchemaPlanV2:
		return KindPlan, nil
	case SchemaExperiment, SchemaExperimentV2:
		return KindExperiment, nil
	case SchemaRun:
		return KindRun, nil
	case SchemaAttempt, SchemaAttemptV2:
		return KindAttempt, nil
	case SchemaEvaluationSpec:
		return KindEvaluationSpec, nil
	case SchemaEvaluation:
		return KindEvaluation, nil
	case SchemaFinding:
		return KindFinding, nil
	case SchemaCandidate:
		return KindCandidate, nil
	case SchemaRelease:
		return KindRelease, nil
	case SchemaPromotionSpec:
		return KindPromotionSpec, nil
	case SchemaPromotion:
		return KindPromotion, nil
	case SchemaDecision:
		return KindDecision, nil
	default:
		return KindUnknown, fmt.Errorf("schema %q: %w", schema, ErrUnknownSchema)
	}
}

// IDPrefix is the persisted typed-ID prefix. Project has a bare UUID.
func (k Kind) IDPrefix() (string, error) {
	switch k {
	case KindIdea:
		return "idea_", nil
	case KindResourcePool:
		return "pool_", nil
	case KindQueue:
		return "queue_", nil
	case KindQueueAdvice:
		return "advice_", nil
	case KindBattle:
		return "battle_", nil
	case KindPlan:
		return "plan_", nil
	case KindExperiment:
		return "exp_", nil
	case KindRun:
		return "run_", nil
	case KindAttempt:
		return "att_", nil
	case KindEvaluationSpec:
		return "evalspec_", nil
	case KindEvaluation:
		return "eval_", nil
	case KindFinding:
		return "fnd_", nil
	case KindCandidate:
		return "cand_", nil
	case KindRelease:
		return "rel_", nil
	case KindPromotionSpec:
		return "promspec_", nil
	case KindPromotion:
		return "prom_", nil
	case KindDecision:
		return "dec_", nil
	case KindProject, KindPolicy:
		return "", fmt.Errorf("%s does not use a typed ID: %w", k, ErrUnknownKind)
	default:
		return "", fmt.Errorf("kind %q: %w", k, ErrUnknownKind)
	}
}

// DisplayLetter is the kind letter used by short display codes.
func (k Kind) DisplayLetter() (byte, error) {
	switch k {
	case KindIdea:
		return 'I', nil
	case KindResourcePool:
		return 'O', nil
	case KindQueue:
		return 'Q', nil
	case KindQueueAdvice:
		return 'V', nil
	case KindBattle:
		return 'B', nil
	case KindPlan:
		return 'P', nil
	case KindExperiment:
		return 'E', nil
	case KindRun:
		return 'R', nil
	case KindAttempt:
		return 'A', nil
	case KindEvaluationSpec:
		return 'S', nil
	case KindEvaluation:
		return 'N', nil
	case KindFinding:
		return 'F', nil
	case KindCandidate:
		return 'C', nil
	case KindRelease:
		return 'L', nil
	case KindPromotionSpec:
		return 'T', nil
	case KindPromotion:
		return 'M', nil
	case KindDecision:
		return 'D', nil
	default:
		return 0, fmt.Errorf("kind %q has no display letter: %w", k, ErrUnknownKind)
	}
}

// KindForDisplayLetter resolves a short-code letter case-insensitively.
func KindForDisplayLetter(letter byte) (Kind, error) {
	switch strings.ToUpper(string(letter)) {
	case "I":
		return KindIdea, nil
	case "O":
		return KindResourcePool, nil
	case "Q":
		return KindQueue, nil
	case "V":
		return KindQueueAdvice, nil
	case "B":
		return KindBattle, nil
	case "P":
		return KindPlan, nil
	case "E":
		return KindExperiment, nil
	case "R":
		return KindRun, nil
	case "A":
		return KindAttempt, nil
	case "S":
		return KindEvaluationSpec, nil
	case "N":
		return KindEvaluation, nil
	case "F":
		return KindFinding, nil
	case "C":
		return KindCandidate, nil
	case "L":
		return KindRelease, nil
	case "T":
		return KindPromotionSpec, nil
	case "M":
		return KindPromotion, nil
	case "D":
		return KindDecision, nil
	default:
		return KindUnknown, fmt.Errorf("display letter %q: %w", letter, ErrUnknownKind)
	}
}
