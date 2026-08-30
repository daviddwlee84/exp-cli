package ranking

import (
	"context"
	"math"
	"reflect"
	"testing"
)

func TestScoreRewardsExpectedValueAndInformationPerCost(t *testing.T) {
	value := Assessment{
		ProbabilityImprove: Interval{Low: 0.4, High: 0.6}, GainIfSuccess: Interval{Low: 2, High: 4},
		InformationValue: 1, UnblockValue: 0.5, Downside: 0.25, PoolHours: 2, Confidence: 0.8, AgeHours: 10,
	}
	score, err := Score(value, DefaultWeights())
	if err != nil || score <= 1 || math.IsNaN(score) {
		t.Fatalf("score=%g err=%v", score, err)
	}
	value.PoolHours = -1
	if _, err := Score(value, DefaultWeights()); err == nil {
		t.Fatal("negative cost should fail")
	}
}

func TestAdviceRanksEveryCandidateExactlyOnce(t *testing.T) {
	candidates := []Candidate{{ID: "a"}, {ID: "b"}}
	advice := Advice{Schema: AdviceSchema, SuggestedIDs: []string{"b", "a"}, Confidence: 0.9}
	index, err := ProvisionalIndex(advice, candidates, "b")
	if err != nil || index != 0 {
		t.Fatalf("index=%d err=%v", index, err)
	}
	advice.SuggestedIDs = []string{"a", "a"}
	if _, err := ProvisionalIndex(advice, candidates, "b"); err == nil {
		t.Fatal("duplicate advice should fail")
	}
}

func TestInsertUsesOrderSwappedBattles(t *testing.T) {
	queue := []Candidate{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	challenger := Candidate{ID: "x"}
	judge := JudgeFunc(func(_ context.Context, left, right Candidate) (Judgment, error) {
		winner := left.ID
		if left.ID == "x" && right.ID == "a" || left.ID == "a" && right.ID == "x" {
			winner = "a"
		} else if left.ID == "x" || right.ID == "x" {
			winner = "x"
		}
		return Judgment{Schema: BattleSchema, Verdict: VerdictWinner, WinnerID: winner, Confidence: 0.9}, nil
	})
	result, err := Insert(t.Context(), queue, challenger, 2, judge, InsertOptions{TieIncumbentFirst: true})
	if err != nil || !result.Applied || result.NeedsHuman {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	ids := []string{}
	for _, candidate := range result.Order {
		ids = append(ids, candidate.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "x", "b", "c"}) {
		t.Fatalf("order=%v audits=%#v", ids, result.Audits)
	}
	if len(result.Audits) < 2 {
		t.Fatalf("expected swapped neighbor audits, got %#v", result.Audits)
	}
}

func TestInsertAbstainsOnOrderDisagreement(t *testing.T) {
	queue := []Candidate{{ID: "a"}}
	call := 0
	judge := JudgeFunc(func(_ context.Context, left, right Candidate) (Judgment, error) {
		call++
		return Judgment{Schema: BattleSchema, Verdict: VerdictWinner, WinnerID: left.ID, Confidence: 0.9}, nil
	})
	result, err := Insert(t.Context(), queue, Candidate{ID: "x"}, 0, judge, InsertOptions{})
	if err != nil || !result.NeedsHuman || result.Applied || call != 2 || len(result.Order) != 1 || result.Order[0].ID != "a" {
		t.Fatalf("result=%#v calls=%d err=%v", result, call, err)
	}
}

func TestTieKeepsIncumbentFirst(t *testing.T) {
	judge := JudgeFunc(func(_ context.Context, left, right Candidate) (Judgment, error) {
		return Judgment{Schema: BattleSchema, Verdict: VerdictTie, Confidence: 0.9}, nil
	})
	result, err := Insert(t.Context(), []Candidate{{ID: "a"}}, Candidate{ID: "x"}, 0, judge, InsertOptions{TieIncumbentFirst: true})
	if err != nil || !result.Applied || result.Order[0].ID != "a" || result.Order[1].ID != "x" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
