// Package queueflow turns canonical Plan utility estimates into an auditable
// pool/lane queue insertion. Agent output is advisory; the final mutation is a
// deterministic prepared transaction against the exact Queue revision read
// before any external process ran.
package queueflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/ranking"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	AdvisorRole         = "queue_advisor"
	BattleRole          = "queue_battle"
	agentAuditExtension = "io.github.daviddwlee84.exp-cli.agent-audit"
)

var (
	ErrAlreadyQueued = errors.New("Plan is already present in a canonical Queue")
	ErrHumanReview   = errors.New("queue insertion requires human review")
)

// Store is the canonical boundary required by Service.
type Store interface {
	Inventory(context.Context) (*record.Inventory, error)
	Transact(context.Context, record.TransactionRequest) (*record.TransactionResult, error)
}

// Agent is the fresh, single-shot JSON agent boundary.
type Agent interface {
	Run(context.Context, agentcli.Request) (agentcli.Result, error)
}

type Service struct {
	Store        Store
	Agent        Agent
	Now          func() time.Time
	GenerateUUID research.UUIDGenerator
}

type InsertRequest struct {
	Queue             research.ID
	Pool              research.ID
	Lane              research.ResearchLane
	Plan              research.ID
	Position          *int
	Score             *float64
	UseAgent          bool
	AdvisorProfile    string
	BattleProfile     string
	AgentCWD          string
	MinimumConfidence float64
	TieIncumbentFirst bool
	TieRequiresHuman  bool
	Pinned            bool
}

type InsertResult struct {
	Queue         *record.Document   `json:"-"`
	Position      int                `json:"position"`
	Score         float64            `json:"score"`
	Applied       bool               `json:"applied"`
	NeedsHuman    bool               `json:"needs_human"`
	Reason        string             `json:"reason,omitempty"`
	Advice        *record.Document   `json:"-"`
	Battles       []*record.Document `json:"-"`
	TransactionID string             `json:"transaction_id,omitempty"`
	AgentProfile  string             `json:"agent_profile,omitempty"`
	ReportedModel string             `json:"reported_model,omitempty"`
}

type RemoveRequest struct {
	Queue research.ID
	Plan  research.ID
}

func (service Service) Insert(ctx context.Context, request InsertRequest) (InsertResult, error) {
	if service.Store == nil {
		return InsertResult{}, errors.New("queue store is required")
	}
	inventory, err := service.Store.Inventory(ctx)
	if err != nil {
		return InsertResult{}, err
	}
	if !inventory.Valid() {
		return InsertResult{}, inventory.Error()
	}
	queueDocument, queue, planDocument, plan, partition, err := insertionContext(inventory, request)
	if err != nil {
		return InsertResult{}, err
	}
	if len(plan.Resources) != 1 {
		return InsertResult{}, errors.New("autonomous queue admission requires exactly one ResourcePool need; model coupled resources as one composite pool")
	}
	assessment, score, components, err := assessPlan(plan, request.Pool, planDocument, service.now())
	if err != nil {
		return InsertResult{}, err
	}
	if request.Score != nil {
		if math.IsNaN(*request.Score) || math.IsInf(*request.Score, 0) {
			return InsertResult{}, errors.New("queue score must be finite")
		}
		score = *request.Score
		components.Total = score
	}
	challenger := ranking.Candidate{
		ID: plan.ID.String(), Revision: planDocument.Revision, Title: plan.Title,
		Brief: brief(planDocument), Score: score, Assess: assessment,
	}
	incumbents, err := partitionCandidates(inventory, partition, service.now())
	if err != nil {
		return InsertResult{}, err
	}

	if !request.UseAgent {
		position, err := deterministicPosition(incumbents, score, request.Position)
		if err != nil {
			return InsertResult{}, err
		}
		updated := insertQueueEntry(queueDocument, queue, request.Pool, request.Lane, planDocument, score, position, request.Pinned, service.now())
		changes := []record.TransactionChange{{Operation: record.TransactionReplace, Document: updated, ExpectedRevision: queueDocument.Revision}}
		if ideaChange := queuedIdeaChange(inventory, plan, service.now()); ideaChange != nil {
			changes = append(changes, *ideaChange)
		}
		transaction, err := service.Store.Transact(ctx, record.TransactionRequest{Operation: "queue.insert", Changes: changes})
		if err != nil {
			return InsertResult{}, err
		}
		return InsertResult{Queue: resultDocument(transaction, research.KindQueue), Position: position, Score: score, Applied: true, TransactionID: transaction.TransactionID}, nil
	}
	if service.Agent == nil {
		return InsertResult{}, errors.New("agent-backed insertion requires a configured agent runner")
	}
	if request.AgentCWD == "" {
		return InsertResult{}, errors.New("agent-backed insertion requires an absolute working directory")
	}

	all := append(append([]ranking.Candidate(nil), incumbents...), challenger)
	prompt, err := advisorPrompt(queue, request, all)
	if err != nil {
		return InsertResult{}, err
	}
	adviceRun, err := service.Agent.Run(ctx, agentcli.Request{
		Role: AdvisorRole, Profile: request.AdvisorProfile, Prompt: prompt,
		Schema: json.RawMessage(adviceJSONSchema), CWD: request.AgentCWD,
	})
	if err != nil {
		return InsertResult{}, fmt.Errorf("queue advisor: %w", err)
	}
	var advice ranking.Advice
	if err := decodeStrict(adviceRun.Output, &advice); err != nil {
		return InsertResult{}, fmt.Errorf("decode queue advice: %w", err)
	}
	provisional, err := ranking.ProvisionalIndex(advice, all, challenger.ID)
	if err != nil {
		return InsertResult{}, err
	}
	if request.Position != nil {
		provisional = *request.Position
		if provisional < 0 || provisional > len(incumbents) {
			return InsertResult{}, fmt.Errorf("queue position %d is outside 0..%d", provisional, len(incumbents))
		}
	}

	judge := &agentJudge{
		agent: service.Agent, profile: request.BattleProfile, cwd: request.AgentCWD,
		queue: queue.ID.String(), pool: request.Pool.String(), lane: string(request.Lane),
	}
	inserted, err := ranking.Insert(ctx, incumbents, challenger, provisional, judge, ranking.InsertOptions{
		MinimumConfidence: request.MinimumConfidence,
		TieIncumbentFirst: request.TieIncumbentFirst,
	})
	if err != nil {
		return InsertResult{}, err
	}
	if request.TieRequiresHuman {
		for _, audit := range inserted.Audits {
			if audit.Outcome == "tie" {
				inserted.Order = append([]ranking.Candidate(nil), incumbents...)
				inserted.Position = -1
				inserted.Applied = false
				inserted.NeedsHuman = true
				inserted.Reason = "queue policy requires human review for a pairwise tie"
				break
			}
		}
	}
	now := service.now()
	adviceDocument, err := service.adviceDocument(now, queue, request, challenger, components, advice, prompt, adviceRun)
	if err != nil {
		return InsertResult{}, err
	}
	battleDocuments, err := service.battleDocuments(now, queue, request, adviceDocument, inserted.Audits)
	if err != nil {
		return InsertResult{}, err
	}
	changes := []record.TransactionChange{{Operation: record.TransactionCreate, Document: adviceDocument}}
	for _, battle := range battleDocuments {
		changes = append(changes, record.TransactionChange{Operation: record.TransactionCreate, Document: battle})
	}
	if inserted.Applied {
		updated := queueFromRanking(queueDocument, queue, request.Pool, request.Lane, planDocument, score, request.Pinned, inserted.Order, now)
		changes = append(changes, record.TransactionChange{Operation: record.TransactionReplace, Document: updated, ExpectedRevision: queueDocument.Revision})
		if ideaChange := queuedIdeaChange(inventory, plan, now); ideaChange != nil {
			changes = append(changes, *ideaChange)
		}
	}
	transaction, err := service.Store.Transact(ctx, record.TransactionRequest{Operation: "queue.battle-insert", Changes: changes})
	if err != nil {
		return InsertResult{}, err
	}
	result := InsertResult{
		Position: inserted.Position, Score: score, Applied: inserted.Applied, NeedsHuman: inserted.NeedsHuman,
		Reason: inserted.Reason, TransactionID: transaction.TransactionID,
		AgentProfile: adviceRun.Profile, ReportedModel: adviceRun.ReportedModel,
	}
	for _, document := range transaction.Documents {
		switch document.Kind() {
		case research.KindQueue:
			result.Queue = document
		case research.KindQueueAdvice:
			result.Advice = document
		case research.KindBattle:
			result.Battles = append(result.Battles, document)
		}
	}
	if result.NeedsHuman {
		return result, ErrHumanReview
	}
	return result, nil
}

func (service Service) Remove(ctx context.Context, request RemoveRequest) (*record.Document, error) {
	if service.Store == nil {
		return nil, errors.New("queue store is required")
	}
	inventory, err := service.Store.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	queueDocument, err := inventory.ByID(request.Queue)
	if err != nil {
		return nil, err
	}
	_, ok := queueDocument.Record.(*research.Queue)
	if !ok {
		return nil, errors.New("queue reference does not name a Queue")
	}
	replacement := queueDocument.Clone()
	updated := replacement.Record.(*research.Queue)
	found := false
	for partitionIndex := range updated.Partitions {
		entries := updated.Partitions[partitionIndex].Entries[:0]
		for _, entry := range updated.Partitions[partitionIndex].Entries {
			if entry.Plan == request.Plan {
				found = true
				continue
			}
			entries = append(entries, entry)
		}
		updated.Partitions[partitionIndex].Entries = entries
	}
	if !found {
		return nil, fmt.Errorf("Plan %s is not in Queue %s", request.Plan, request.Queue)
	}
	updated.Revision++
	updated.UpdatedAt = service.now()
	changes := []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: queueDocument.Revision}}
	if planDocument, planErr := inventory.ByID(request.Plan); planErr == nil {
		plan := planDocument.Record.(*research.Plan)
		if !plan.Idea.IsZero() {
			if ideaDocument, ideaErr := inventory.ByID(plan.Idea); ideaErr == nil {
				idea := ideaDocument.Record.(*research.Idea)
				if idea.State == research.IdeaQueued {
					updatedIdea := ideaDocument.Clone()
					updatedIdea.Record.(*research.Idea).State = research.IdeaQualified
					updatedIdea.Record.(*research.Idea).UpdatedAt = service.now()
					changes = append(changes, record.TransactionChange{Operation: record.TransactionReplace, Document: updatedIdea, ExpectedRevision: ideaDocument.Revision})
				}
			}
		}
	}
	transaction, err := service.Store.Transact(ctx, record.TransactionRequest{Operation: "queue.remove", Changes: changes})
	if err != nil {
		return nil, err
	}
	return resultDocument(transaction, research.KindQueue), nil
}

func queuedIdeaChange(inventory *record.Inventory, plan *research.Plan, now time.Time) *record.TransactionChange {
	if inventory == nil || plan == nil || plan.Idea.IsZero() {
		return nil
	}
	document, err := inventory.ByID(plan.Idea)
	if err != nil {
		return nil
	}
	idea, ok := document.Record.(*research.Idea)
	if !ok || idea.State != research.IdeaQualified {
		return nil
	}
	replacement := document.Clone()
	updated := replacement.Record.(*research.Idea)
	updated.State = research.IdeaQueued
	updated.UpdatedAt = now
	return &record.TransactionChange{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: document.Revision}
}

func insertionContext(inventory *record.Inventory, request InsertRequest) (*record.Document, *research.Queue, *record.Document, *research.Plan, research.QueuePartition, error) {
	if request.Queue.Kind() != research.KindQueue || request.Pool.Kind() != research.KindResourcePool || request.Plan.Kind() != research.KindPlan {
		return nil, nil, nil, nil, research.QueuePartition{}, errors.New("queue, pool, and Plan references have the wrong kind")
	}
	if request.Lane != research.LaneExploit && request.Lane != research.LaneExplore {
		return nil, nil, nil, nil, research.QueuePartition{}, errors.New("lane must be exploit or explore")
	}
	queueDocument, err := inventory.ByID(request.Queue)
	if err != nil {
		return nil, nil, nil, nil, research.QueuePartition{}, err
	}
	poolDocument, err := inventory.ByID(request.Pool)
	if err != nil {
		return nil, nil, nil, nil, research.QueuePartition{}, err
	}
	pool := poolDocument.Record.(*research.ResourcePool)
	if !pool.Enabled {
		return nil, nil, nil, nil, research.QueuePartition{}, errors.New("ResourcePool is disabled")
	}
	planDocument, err := inventory.ByID(request.Plan)
	if err != nil {
		return nil, nil, nil, nil, research.QueuePartition{}, err
	}
	plan := planDocument.Record.(*research.Plan)
	if plan.Schema != research.SchemaPlanV2 || plan.State != research.PlanQueued {
		return nil, nil, nil, nil, research.QueuePartition{}, errors.New("only queued exp.plan/v2 Plans can enter the research queue")
	}
	for _, document := range inventory.OfKind(research.KindQueue) {
		other := document.Record.(*research.Queue)
		for _, partition := range other.Partitions {
			for _, entry := range partition.Entries {
				if entry.Plan == plan.ID {
					return nil, nil, nil, nil, research.QueuePartition{}, ErrAlreadyQueued
				}
			}
		}
	}
	queue := queueDocument.Record.(*research.Queue)
	if queue.Paused {
		return nil, nil, nil, nil, research.QueuePartition{}, errors.New("Queue is paused")
	}
	for _, partition := range queue.Partitions {
		if partition.Pool == request.Pool && partition.Lane == request.Lane {
			return queueDocument, queue, planDocument, plan, partition, nil
		}
	}
	return nil, nil, nil, nil, research.QueuePartition{}, errors.New("Queue does not define the requested pool/lane partition")
}

func assessPlan(plan *research.Plan, pool research.ID, document *record.Document, now time.Time) (ranking.Assessment, float64, research.QueueScore, error) {
	if plan.Utility == nil {
		return ranking.Assessment{}, 0, research.QueueScore{}, errors.New("Plan has no utility estimate")
	}
	hours := 0.0
	for _, need := range plan.Resources {
		if need.Pool == pool {
			hours += float64(need.Units) * need.EstimatedHours
		}
	}
	if hours <= 0 {
		return ranking.Assessment{}, 0, research.QueueScore{}, errors.New("Plan has no positive resource estimate for this pool")
	}
	age := now.Sub(plan.CreatedAt).Hours()
	if age < 0 {
		age = 0
	}
	assessment := ranking.Assessment{
		ProbabilityImprove: ranking.Interval{Low: plan.Utility.Probability, High: plan.Utility.Probability},
		GainIfSuccess:      ranking.Interval{Low: plan.Utility.Impact, High: plan.Utility.Impact},
		InformationValue:   plan.Utility.InformationGain, UnblockValue: plan.Utility.UnblockValue,
		Downside: plan.Utility.RiskPenalty, PoolHours: hours, Confidence: 1, AgeHours: age,
		Rationale: strings.TrimSpace(plan.ExpectedPayoff.Summary),
	}
	score, err := ranking.Score(assessment, ranking.DefaultWeights())
	if err != nil {
		return ranking.Assessment{}, 0, research.QueueScore{}, err
	}
	expected := plan.Utility.Probability * plan.Utility.Impact
	components := research.QueueScore{
		ExpectedUtility: expected, InformationGain: plan.Utility.InformationGain,
		UnblockValue: plan.Utility.UnblockValue, RiskPenalty: plan.Utility.RiskPenalty,
		PoolHours: hours, Total: score,
	}
	_ = document
	return assessment, score, components, nil
}

func partitionCandidates(inventory *record.Inventory, partition research.QueuePartition, now time.Time) ([]ranking.Candidate, error) {
	result := make([]ranking.Candidate, 0, len(partition.Entries))
	for _, entry := range partition.Entries {
		document, err := inventory.ByID(entry.Plan)
		if err != nil {
			return nil, err
		}
		plan := document.Record.(*research.Plan)
		assessment, _, _, err := assessPlan(plan, partition.Pool, document, now)
		if err != nil {
			return nil, err
		}
		result = append(result, ranking.Candidate{ID: plan.ID.String(), Revision: document.Revision, Title: plan.Title, Brief: brief(document), Score: entry.Score, Assess: assessment})
	}
	return result, nil
}

func deterministicPosition(queue []ranking.Candidate, score float64, explicit *int) (int, error) {
	if explicit != nil {
		if *explicit < 0 || *explicit > len(queue) {
			return 0, fmt.Errorf("queue position %d is outside 0..%d", *explicit, len(queue))
		}
		return *explicit, nil
	}
	for index, incumbent := range queue {
		if score > incumbent.Score {
			return index, nil
		}
	}
	return len(queue), nil
}

func insertQueueEntry(document *record.Document, queue *research.Queue, pool research.ID, lane research.ResearchLane, plan *record.Document, score float64, position int, pinned bool, now time.Time) *record.Document {
	replacement := document.Clone()
	updated := replacement.Record.(*research.Queue)
	planID, _ := plan.ID()
	for index := range updated.Partitions {
		partition := &updated.Partitions[index]
		if partition.Pool != pool || partition.Lane != lane {
			continue
		}
		entry := research.QueueEntry{Plan: planID, PlanRevision: plan.Revision, Score: score, InsertedAt: now, Pinned: pinned}
		partition.Entries = append(partition.Entries, research.QueueEntry{})
		copy(partition.Entries[position+1:], partition.Entries[position:])
		partition.Entries[position] = entry
	}
	updated.Revision = queue.Revision + 1
	updated.UpdatedAt = now
	return replacement
}

func queueFromRanking(document *record.Document, queue *research.Queue, pool research.ID, lane research.ResearchLane, plan *record.Document, score float64, pinned bool, order []ranking.Candidate, now time.Time) *record.Document {
	replacement := document.Clone()
	updated := replacement.Record.(*research.Queue)
	for index := range updated.Partitions {
		partition := &updated.Partitions[index]
		if partition.Pool != pool || partition.Lane != lane {
			continue
		}
		byID := make(map[string]research.QueueEntry, len(partition.Entries)+1)
		for _, entry := range partition.Entries {
			byID[entry.Plan.String()] = entry
		}
		planID, _ := plan.ID()
		byID[planID.String()] = research.QueueEntry{Plan: planID, PlanRevision: plan.Revision, Score: score, InsertedAt: now, Pinned: pinned}
		entries := make([]research.QueueEntry, 0, len(order))
		for _, candidate := range order {
			entries = append(entries, byID[candidate.ID])
		}
		partition.Entries = entries
	}
	updated.Revision = queue.Revision + 1
	updated.UpdatedAt = now
	return replacement
}

func advisorPrompt(queue *research.Queue, request InsertRequest, candidates []ranking.Candidate) ([]byte, error) {
	payload := struct {
		Task       string              `json:"task"`
		Queue      string              `json:"queue"`
		Revision   uint64              `json:"revision"`
		Pool       string              `json:"pool"`
		Lane       string              `json:"lane"`
		Challenger string              `json:"challenger"`
		Candidates []ranking.Candidate `json:"candidates"`
	}{
		Task:  "Rank every candidate exactly once by expected scientific value per constrained pool-hour. Treat numeric score as transparent prior, use the brief for qualitative corrections, and do not invent evidence.",
		Queue: queue.ID.String(), Revision: queue.Revision, Pool: request.Pool.String(), Lane: string(request.Lane),
		Challenger: request.Plan.String(), Candidates: candidates,
	}
	return json.MarshalIndent(payload, "", "  ")
}

type agentJudge struct {
	agent   Agent
	profile string
	cwd     string
	queue   string
	pool    string
	lane    string
}

func (judge *agentJudge) Compare(ctx context.Context, left, right ranking.Candidate) (ranking.Judgment, error) {
	prompt, err := json.MarshalIndent(struct {
		Task  string            `json:"task"`
		Queue string            `json:"queue"`
		Pool  string            `json:"pool"`
		Lane  string            `json:"lane"`
		Left  ranking.Candidate `json:"left"`
		Right ranking.Candidate `json:"right"`
	}{
		Task:  "Compare only these two plans for the next constrained experiment slot. Return winner, tie, or abstain; preserve the presented order and do not infer missing facts.",
		Queue: judge.queue, Pool: judge.pool, Lane: judge.lane, Left: left, Right: right,
	}, "", "  ")
	if err != nil {
		return ranking.Judgment{}, err
	}
	run, err := judge.agent.Run(ctx, agentcli.Request{Role: BattleRole, Profile: judge.profile, Prompt: prompt, Schema: json.RawMessage(battleJSONSchema), CWD: judge.cwd})
	if err != nil {
		return ranking.Judgment{}, err
	}
	var judgment ranking.Judgment
	if err := decodeStrict(run.Output, &judgment); err != nil {
		return ranking.Judgment{}, err
	}
	return judgment, nil
}

func (service Service) adviceDocument(now time.Time, queue *research.Queue, request InsertRequest, challenger ranking.Candidate, score research.QueueScore, advice ranking.Advice, prompt []byte, run agentcli.Result) (*record.Document, error) {
	id, err := service.newID(research.KindQueueAdvice, now)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(prompt)
	position := 0
	for index, value := range advice.SuggestedIDs {
		if value == challenger.ID {
			position = index
			break
		}
	}
	order := make([]research.ID, 0, len(advice.SuggestedIDs))
	for _, value := range advice.SuggestedIDs {
		parsed, err := research.ParseIDForKind(value, research.KindPlan)
		if err != nil {
			return nil, err
		}
		order = append(order, parsed)
	}
	rationale := advice.Rationales[challenger.ID]
	if strings.TrimSpace(rationale) == "" {
		rationale = "Agent supplied a complete listwise order without a challenger-specific rationale."
	}
	model := run.ReportedModel
	if model == "" {
		model = run.Profile
	}
	value := &research.QueueAdvice{
		Common: research.Common{Schema: research.SchemaQueueAdvice, ID: id, Title: "Queue advice for " + challenger.Title, CreatedAt: now, UpdatedAt: now},
		Queue:  queue.ID, QueueRevision: queue.Revision, CandidatePlan: request.Plan, Pool: request.Pool, Lane: request.Lane,
		ProposedPosition: uint64(position), ListwiseOrder: order, Score: score, Model: model,
		PromptDigest: "sha256:" + hex.EncodeToString(digest[:]), Rationale: rationale,
		Extensions: research.Extensions{agentAuditExtension: {"confidence": advice.Confidence, "rationales": stringMapAny(advice.Rationales)}},
	}
	return &record.Document{Record: value, Body: "\n# Queue advice\n\n" + rationale + "\n"}, nil
}

func (service Service) battleDocuments(now time.Time, queue *research.Queue, request InsertRequest, advice *record.Document, audits []ranking.PairAudit) ([]*record.Document, error) {
	adviceID, _ := advice.ID()
	result := make([]*record.Document, 0, len(audits))
	for index, audit := range audits {
		id, err := service.newID(research.KindBattle, now.Add(time.Duration(index)*time.Nanosecond))
		if err != nil {
			return nil, err
		}
		incumbent, err := research.ParseIDForKind(audit.Incumbent, research.KindPlan)
		if err != nil {
			return nil, err
		}
		forward, reverse := choicesForAudit(audit)
		outcome := research.BattleHumanReview
		switch audit.Outcome {
		case audit.Challenger:
			outcome = research.BattleCandidateWins
		case audit.Incumbent:
			outcome = research.BattleIncumbentWins
		case "tie":
			outcome = research.BattleTie
		}
		confidence := math.Min(audit.Forward.Confidence, audit.Reverse.Confidence)
		rationale := strings.TrimSpace(audit.Forward.Rationale + "\n" + audit.Reverse.Rationale)
		if rationale == "" {
			rationale = "Order-swapped comparison supplied no rationale."
		}
		value := &research.Battle{
			Common: research.Common{Schema: research.SchemaBattle, ID: id, Title: fmt.Sprintf("Queue battle %d", index+1), CreatedAt: now, UpdatedAt: now},
			Queue:  queue.ID, QueueRevision: queue.Revision, Advice: adviceID, CandidatePlan: request.Plan,
			IncumbentPlan: incumbent, Pool: request.Pool, Lane: request.Lane,
			OrderAB: forward, OrderBA: reverse, Outcome: outcome, Confidence: confidence, Rationale: rationale,
			Extensions: research.Extensions{agentAuditExtension: {
				"forward_verdict": string(audit.Forward.Verdict), "forward_winner": audit.Forward.WinnerID, "forward_confidence": audit.Forward.Confidence,
				"reverse_verdict": string(audit.Reverse.Verdict), "reverse_winner": audit.Reverse.WinnerID, "reverse_confidence": audit.Reverse.Confidence,
			}},
		}
		result = append(result, &record.Document{Record: value, Body: "\n# Queue battle\n\n" + rationale + "\n"})
	}
	return result, nil
}

func stringMapAny(values map[string]string) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func choicesForAudit(audit ranking.PairAudit) (research.BattleChoice, research.BattleChoice) {
	forward := judgmentChoice(audit.Forward, audit.Challenger, audit.Incumbent)
	reverse := judgmentChoice(audit.Reverse, audit.Challenger, audit.Incumbent)
	if audit.Outcome == "needs_human" {
		// Canonical Battle v1 has no separate abstain choice. Encode a synthetic
		// disagreement so a low-confidence or abstaining comparison cannot be
		// mistaken for a decisive winner or true tie. The exact judgments remain
		// in the rationale and the confidence remains canonical.
		forward, reverse = research.BattleChooseCandidate, research.BattleChooseIncumbent
	}
	return forward, reverse
}

func judgmentChoice(value ranking.Judgment, challenger, incumbent string) research.BattleChoice {
	if value.Verdict == ranking.VerdictWinner {
		if value.WinnerID == challenger {
			return research.BattleChooseCandidate
		}
		if value.WinnerID == incumbent {
			return research.BattleChooseIncumbent
		}
	}
	return research.BattleChooseTie
}

func (service Service) newID(kind research.Kind, now time.Time) (research.ID, error) {
	generator := service.GenerateUUID
	if generator == nil {
		generator = research.DefaultUUIDGenerator
	}
	value, err := generator(now)
	if err != nil {
		return research.ID{}, err
	}
	return research.NewID(kind, value)
}

func (service Service) now() time.Time {
	if service.Now == nil {
		return time.Now().UTC()
	}
	return service.Now().UTC()
}

func brief(document *record.Document) string {
	text := strings.TrimSpace(document.Body)
	if len(text) > 2000 {
		text = text[:2000]
	}
	return text
}

func resultDocument(result *record.TransactionResult, kind research.Kind) *record.Document {
	if result == nil {
		return nil
	}
	for _, document := range result.Documents {
		if document.Kind() == kind {
			return document
		}
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains a trailing value")
		}
		return err
	}
	return nil
}

const adviceJSONSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "additionalProperties":false,
  "required":["schema_version","suggested_ids","rationales","confidence"],
  "properties":{
    "schema_version":{"const":"exp.agent.advice/v1"},
    "suggested_ids":{"type":"array","items":{"type":"string"},"uniqueItems":true},
    "rationales":{"type":"object","additionalProperties":{"type":"string"}},
    "confidence":{"type":"number","minimum":0,"maximum":1}
  }
}`

const battleJSONSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "additionalProperties":false,
  "required":["schema_version","verdict","confidence","rationale"],
  "properties":{
    "schema_version":{"const":"exp.agent.battle/v1"},
    "verdict":{"enum":["winner","tie","abstain"]},
    "winner_id":{"type":"string"},
    "confidence":{"type":"number","minimum":0,"maximum":1},
    "rationale":{"type":"string"}
  }
}`
