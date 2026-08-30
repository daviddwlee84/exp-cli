// Package ranking implements transparent expected-value scoring and stable,
// order-swapped pairwise insertion. Agent output is advisory input; queue
// mutation remains deterministic and auditable.
package ranking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
)

const (
	AdviceSchema = "exp.agent.advice/v1"
	BattleSchema = "exp.agent.battle/v1"
)

type Interval struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

func (value Interval) Validate(minimum, maximum float64) error {
	if !finite(value.Low) || !finite(value.High) || value.Low > value.High || value.Low < minimum || value.High > maximum {
		return fmt.Errorf("interval [%g,%g] is outside [%g,%g]", value.Low, value.High, minimum, maximum)
	}
	return nil
}

func (value Interval) Midpoint() float64 { return (value.Low + value.High) / 2 }

type Assessment struct {
	ProbabilityImprove Interval `json:"probability_improve"`
	GainIfSuccess      Interval `json:"gain_if_success"`
	InformationValue   float64  `json:"information_value"`
	UnblockValue       float64  `json:"unblock_value"`
	Downside           float64  `json:"downside"`
	PoolHours          float64  `json:"pool_hours"`
	Confidence         float64  `json:"confidence"`
	AgeHours           float64  `json:"age_hours"`
	Rationale          string   `json:"rationale"`
}

type Weights struct {
	Information float64
	Unblock     float64
	Downside    float64
	Aging       float64
	CostFloor   float64
	MaxAgeBonus float64
}

func DefaultWeights() Weights {
	return Weights{Information: 1, Unblock: 1, Downside: 1, Aging: 0.001, CostFloor: 0.05, MaxAgeBonus: 0.1}
}

func Score(value Assessment, weights Weights) (float64, error) {
	if err := value.ProbabilityImprove.Validate(0, 1); err != nil {
		return 0, fmt.Errorf("probability improve: %w", err)
	}
	if err := value.GainIfSuccess.Validate(-1e12, 1e12); err != nil {
		return 0, fmt.Errorf("gain if success: %w", err)
	}
	for name, number := range map[string]float64{
		"information value": value.InformationValue, "unblock value": value.UnblockValue,
		"downside": value.Downside, "pool hours": value.PoolHours, "confidence": value.Confidence, "age hours": value.AgeHours,
	} {
		if !finite(number) || number < 0 {
			return 0, fmt.Errorf("%s must be finite and non-negative", name)
		}
	}
	if value.Confidence > 1 {
		return 0, errors.New("confidence must not exceed 1")
	}
	if weights.CostFloor <= 0 {
		weights.CostFloor = DefaultWeights().CostFloor
	}
	expected := value.ProbabilityImprove.Midpoint() * value.GainIfSuccess.Midpoint()
	numerator := expected + weights.Information*value.InformationValue + weights.Unblock*value.UnblockValue - weights.Downside*value.Downside
	base := numerator / math.Max(value.PoolHours, weights.CostFloor)
	ageBonus := math.Min(weights.MaxAgeBonus, value.AgeHours*weights.Aging)
	return base + ageBonus, nil
}

type Candidate struct {
	ID       string     `json:"id"`
	Revision string     `json:"revision"`
	Title    string     `json:"title"`
	Brief    string     `json:"brief"`
	Score    float64    `json:"score"`
	Assess   Assessment `json:"assessment"`
}

type Advice struct {
	Schema       string            `json:"schema_version"`
	SuggestedIDs []string          `json:"suggested_ids"`
	Rationales   map[string]string `json:"rationales"`
	Confidence   float64           `json:"confidence"`
}

func (advice Advice) Validate(candidates []Candidate) error {
	if advice.Schema != AdviceSchema || !finite(advice.Confidence) || advice.Confidence < 0 || advice.Confidence > 1 {
		return errors.New("invalid advice schema or confidence")
	}
	want := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate.ID == "" {
			return errors.New("candidate id is empty")
		}
		want[candidate.ID] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, id := range advice.SuggestedIDs {
		if _, found := want[id]; !found {
			return fmt.Errorf("advice references unknown candidate %q", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("advice duplicates candidate %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != len(want) {
		return errors.New("advice does not rank every candidate exactly once")
	}
	return nil
}

func ProvisionalIndex(advice Advice, candidates []Candidate, challengerID string) (int, error) {
	if err := advice.Validate(candidates); err != nil {
		return 0, err
	}
	for index, id := range advice.SuggestedIDs {
		if id == challengerID {
			return index, nil
		}
	}
	return 0, fmt.Errorf("challenger %q is absent from advice", challengerID)
}

type Verdict string

const (
	VerdictWinner  Verdict = "winner"
	VerdictTie     Verdict = "tie"
	VerdictAbstain Verdict = "abstain"
)

type Judgment struct {
	Schema     string  `json:"schema_version"`
	Verdict    Verdict `json:"verdict"`
	WinnerID   string  `json:"winner_id,omitempty"`
	Confidence float64 `json:"confidence"`
	Rationale  string  `json:"rationale"`
}

func (value Judgment) Validate(left, right string) error {
	if value.Schema != BattleSchema || !finite(value.Confidence) || value.Confidence < 0 || value.Confidence > 1 {
		return errors.New("invalid battle schema or confidence")
	}
	switch value.Verdict {
	case VerdictWinner:
		if value.WinnerID != left && value.WinnerID != right {
			return errors.New("battle winner is not one of the compared candidates")
		}
	case VerdictTie, VerdictAbstain:
		if value.WinnerID != "" {
			return errors.New("tie or abstain cannot name a winner")
		}
	default:
		return errors.New("unknown battle verdict")
	}
	return nil
}

type Judge interface {
	Compare(context.Context, Candidate, Candidate) (Judgment, error)
}

type JudgeFunc func(context.Context, Candidate, Candidate) (Judgment, error)

func (function JudgeFunc) Compare(ctx context.Context, left, right Candidate) (Judgment, error) {
	return function(ctx, left, right)
}

type PairAudit struct {
	Challenger string   `json:"challenger"`
	Incumbent  string   `json:"incumbent"`
	Forward    Judgment `json:"forward"`
	Reverse    Judgment `json:"reverse"`
	Outcome    string   `json:"outcome"`
}

type InsertResult struct {
	Order      []Candidate `json:"order"`
	Position   int         `json:"position"`
	Applied    bool        `json:"applied"`
	NeedsHuman bool        `json:"needs_human"`
	Reason     string      `json:"reason,omitempty"`
	Audits     []PairAudit `json:"audits"`
}

type InsertOptions struct {
	MinimumConfidence float64
	TieIncumbentFirst bool
}

func Insert(ctx context.Context, queue []Candidate, challenger Candidate, provisional int, judge Judge, options InsertOptions) (InsertResult, error) {
	if judge == nil {
		return InsertResult{}, errors.New("battle judge is required")
	}
	if provisional < 0 {
		provisional = 0
	}
	if provisional > len(queue) {
		provisional = len(queue)
	}
	if options.MinimumConfidence <= 0 {
		options.MinimumConfidence = 0.6
	}
	order := append([]Candidate(nil), queue...)
	order = append(order, Candidate{})
	copy(order[provisional+1:], order[provisional:])
	order[provisional] = challenger
	position := provisional
	audits := []PairAudit{}

	compare := func(incumbent Candidate) (string, bool, error) {
		forward, err := judge.Compare(ctx, challenger, incumbent)
		if err != nil {
			return "", false, err
		}
		reverse, err := judge.Compare(ctx, incumbent, challenger)
		if err != nil {
			return "", false, err
		}
		if err := forward.Validate(challenger.ID, incumbent.ID); err != nil {
			return "", false, err
		}
		if err := reverse.Validate(incumbent.ID, challenger.ID); err != nil {
			return "", false, err
		}
		outcome := consistentOutcome(forward, reverse, challenger.ID, incumbent.ID, options.MinimumConfidence)
		audits = append(audits, PairAudit{Challenger: challenger.ID, Incumbent: incumbent.ID, Forward: forward, Reverse: reverse, Outcome: outcome})
		if outcome == "needs_human" {
			return outcome, false, nil
		}
		return outcome, true, nil
	}

	for position > 0 {
		outcome, decided, err := compare(order[position-1])
		if err != nil {
			return InsertResult{}, err
		}
		if !decided {
			return InsertResult{Order: append([]Candidate(nil), queue...), Position: -1, NeedsHuman: true, Reason: "order-swapped battle disagreed or lacked confidence", Audits: audits}, nil
		}
		if outcome != challenger.ID {
			break
		}
		order[position], order[position-1] = order[position-1], order[position]
		position--
	}
	for position < len(order)-1 {
		outcome, decided, err := compare(order[position+1])
		if err != nil {
			return InsertResult{}, err
		}
		if !decided {
			return InsertResult{Order: append([]Candidate(nil), queue...), Position: -1, NeedsHuman: true, Reason: "order-swapped battle disagreed or lacked confidence", Audits: audits}, nil
		}
		if outcome == challenger.ID {
			break
		}
		if outcome == "tie" {
			if !options.TieIncumbentFirst {
				break
			}
			order[position], order[position+1] = order[position+1], order[position]
			position++
			continue
		}
		if outcome == order[position+1].ID {
			order[position], order[position+1] = order[position+1], order[position]
			position++
			continue
		}
		break
	}
	return InsertResult{Order: order, Position: position, Applied: true, Audits: audits}, nil
}

func consistentOutcome(forward, reverse Judgment, challenger, incumbent string, minimum float64) string {
	if forward.Confidence < minimum || reverse.Confidence < minimum || forward.Verdict == VerdictAbstain || reverse.Verdict == VerdictAbstain {
		return "needs_human"
	}
	if forward.Verdict == VerdictTie && reverse.Verdict == VerdictTie {
		return "tie"
	}
	if forward.Verdict == VerdictWinner && reverse.Verdict == VerdictWinner && forward.WinnerID == reverse.WinnerID {
		return forward.WinnerID
	}
	return "needs_human"
}

func ByScore(candidates []Candidate) []Candidate {
	result := append([]Candidate(nil), candidates...)
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Score == result[right].Score {
			return result[left].ID < result[right].ID
		}
		return result[left].Score > result[right].Score
	})
	return result
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func MarshalSchemaExample(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}
