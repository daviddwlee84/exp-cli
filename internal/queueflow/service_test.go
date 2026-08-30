package queueflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

type fakeStore struct {
	inventory *record.Inventory
	request   record.TransactionRequest
}

func (store *fakeStore) Inventory(context.Context) (*record.Inventory, error) {
	return store.inventory, nil
}

func (store *fakeStore) Transact(_ context.Context, request record.TransactionRequest) (*record.TransactionResult, error) {
	store.request = request
	documents := []*record.Document{}
	for _, change := range request.Changes {
		if change.Document != nil {
			documents = append(documents, change.Document.Clone())
		}
	}
	return &record.TransactionResult{TransactionID: "tx-1", Documents: documents}, nil
}

type fakeAgent struct {
	outputs []json.RawMessage
	calls   int
}

func (agent *fakeAgent) Run(_ context.Context, request agentcli.Request) (agentcli.Result, error) {
	if agent.calls >= len(agent.outputs) {
		return agentcli.Result{}, errors.New("unexpected agent call")
	}
	output := agent.outputs[agent.calls]
	agent.calls++
	return agentcli.Result{Profile: request.Role, ReportedModel: "test-model", Output: output}, nil
}

func TestManualInsertUsesTransparentScoreAndCASReplacement(t *testing.T) {
	now, inventory, ids := queueInventory(t)
	store := &fakeStore{inventory: inventory}
	result, err := (Service{Store: store, Now: func() time.Time { return now.Add(time.Hour) }}).Insert(t.Context(), InsertRequest{
		Queue: ids.queue, Pool: ids.pool, Lane: research.LaneExploit, Plan: ids.challenger,
	})
	if err != nil || !result.Applied || result.Position != 0 || result.TransactionID != "tx-1" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if store.request.Operation != "queue.insert" || len(store.request.Changes) != 1 || store.request.Changes[0].ExpectedRevision == "" {
		t.Fatalf("transaction=%#v", store.request)
	}
	queue := store.request.Changes[0].Document.Record.(*research.Queue)
	if queue.Revision != 2 || queue.Partitions[0].Entries[0].Plan != ids.challenger || queue.Partitions[0].Entries[0].PlanRevision == "" {
		t.Fatalf("updated queue=%#v", queue)
	}
}

func TestOrderSwappedDisagreementPersistsAuditWithoutMovingQueue(t *testing.T) {
	now, inventory, ids := queueInventory(t)
	store := &fakeStore{inventory: inventory}
	agent := &fakeAgent{outputs: []json.RawMessage{
		json.RawMessage(fmt.Sprintf(`{"schema_version":"exp.agent.advice/v1","suggested_ids":[%q,%q],"rationales":{%q:"high EV"},"confidence":0.9}`, ids.challenger, ids.incumbent, ids.challenger)),
		json.RawMessage(fmt.Sprintf(`{"schema_version":"exp.agent.battle/v1","verdict":"winner","winner_id":%q,"confidence":0.9,"rationale":"left"}`, ids.challenger)),
		json.RawMessage(fmt.Sprintf(`{"schema_version":"exp.agent.battle/v1","verdict":"winner","winner_id":%q,"confidence":0.9,"rationale":"left again"}`, ids.incumbent)),
	}}
	sequence := []string{
		"01a03000-0000-7001-8000-000000000001",
		"01a03000-0000-7002-8000-000000000002",
	}
	result, err := (Service{
		Store: store, Agent: agent, Now: func() time.Time { return now.Add(time.Hour) },
		GenerateUUID: func(time.Time) (uuid.UUID, error) {
			value := uuid.MustParse(sequence[0])
			sequence = sequence[1:]
			return value, nil
		},
	}).Insert(t.Context(), InsertRequest{
		Queue: ids.queue, Pool: ids.pool, Lane: research.LaneExploit, Plan: ids.challenger,
		UseAgent: true, AgentCWD: t.TempDir(), TieIncumbentFirst: true,
	})
	if !errors.Is(err, ErrHumanReview) || !result.NeedsHuman || result.Applied || agent.calls != 3 {
		t.Fatalf("result=%#v calls=%d err=%v", result, agent.calls, err)
	}
	if len(store.request.Changes) != 2 {
		t.Fatalf("audit transaction changes=%d, want advice+battle only", len(store.request.Changes))
	}
	for _, change := range store.request.Changes {
		if change.Operation != record.TransactionCreate || change.Document.Kind() == research.KindQueue {
			t.Fatalf("unexpected mutation=%#v", change)
		}
	}
	battle := store.request.Changes[1].Document.Record.(*research.Battle)
	if battle.Outcome != research.BattleHumanReview || battle.OrderAB == battle.OrderBA {
		t.Fatalf("battle=%#v", battle)
	}
}

type testIDs struct {
	pool, queue, incumbent, challenger research.ID
}

func queueInventory(t *testing.T) (time.Time, *record.Inventory, testIDs) {
	t.Helper()
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	projectID, _ := research.ParseUUID("01a02fff-0000-7000-8000-000000000000")
	ids := testIDs{
		pool:       mustID(t, "pool_01a02fff-0000-7001-8000-000000000001"),
		queue:      mustID(t, "queue_01a02fff-0000-7002-8000-000000000002"),
		incumbent:  mustID(t, "plan_01a02fff-0000-7003-8000-000000000003"),
		challenger: mustID(t, "plan_01a02fff-0000-7004-8000-000000000004"),
	}
	documents := []*record.Document{
		{Path: record.ProjectFile, Body: "\n# Test\n", Record: &research.Project{Schema: research.SchemaProject, ProjectID: projectID, Name: "Test", CreatedAt: now, ExperimentsRoot: "."}},
		{Path: record.PolicyFile, Body: "\n# Policy\n", Record: &research.Policy{
			Schema: research.SchemaPolicy, CreatedAt: now, UpdatedAt: now, Autonomy: research.AutonomyManual,
			ExploitShare: .8, ExploreShare: .2, ScoreFormula: "utility-v1", TiePolicy: research.QueueTieKeepIncumbent, PromotionRequiresHuman: true,
			Taxonomy:          research.ClassificationTaxonomy{Domains: []string{"ml"}, Work: []string{"training"}, Methods: []string{"ablation"}, Components: []string{"encoder"}},
			ClusterSaturation: research.ClusterSaturationPolicy{BudgetHours: 24, PlateauWindow: 3, MinimumImprovement: .01, MinimumProbability: .1},
		}},
	}
	pool := &record.Document{Body: "\n# GPU\n", Record: &research.ResourcePool{
		Common:  research.Common{Schema: research.SchemaResourcePool, ID: ids.pool, Title: "GPU", CreatedAt: now, UpdatedAt: now},
		Enabled: true, Capacity: 1, Unit: "gpu", Bottleneck: "gpu",
	}}
	pool.Path, _ = record.PathForNew(pool.Record, nil)
	documents = append(documents, pool)
	makePlan := func(id research.ID, title string, probability, impact float64) *record.Document {
		document := &record.Document{Body: "\n# " + title + "\n", Record: &research.Plan{
			Common:   research.Common{Schema: research.SchemaPlanV2, ID: id, Title: title, CreatedAt: now, UpdatedAt: now},
			Priority: research.PriorityP1, Effort: research.EffortS, State: research.PlanQueued,
			ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve score", Metric: "score", Unit: "point"},
			PrimaryCluster: "encoder", Classification: &research.Classification{Domain: "ml", Work: "training", Method: "ablation", Component: "encoder", Lane: research.LaneExploit, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman},
			Resources: []research.ResourceNeed{{Pool: ids.pool, Units: 1, EstimatedHours: 1}},
			Utility:   &research.UtilityEstimate{Probability: probability, Impact: impact, InformationGain: .1, UnblockValue: .1, RiskPenalty: .01},
		}}
		document.Path, _ = record.PathForNew(document.Record, nil)
		document.Revision, _ = record.Revision(document)
		return document
	}
	incumbent := makePlan(ids.incumbent, "Incumbent", .2, .2)
	challenger := makePlan(ids.challenger, "Challenger", .9, .9)
	documents = append(documents, incumbent, challenger)
	queue := &record.Document{Body: "\n# Queue\n", Record: &research.Queue{
		Common:   research.Common{Schema: research.SchemaQueue, ID: ids.queue, Title: "Main queue", CreatedAt: now, UpdatedAt: now},
		Revision: 1, Partitions: []research.QueuePartition{{Pool: ids.pool, Lane: research.LaneExploit, Entries: []research.QueueEntry{{Plan: ids.incumbent, PlanRevision: incumbent.Revision, Score: .1, InsertedAt: now}}}},
	}}
	queue.Path, _ = record.PathForNew(queue.Record, nil)
	queue.Revision, _ = record.Revision(queue)
	documents = append(documents, queue)
	inventory := record.InventoryFromDocuments(t.TempDir(), documents)
	if !inventory.Valid() {
		t.Fatalf("fixture invalid: %v", inventory.Diagnostics)
	}
	return now, inventory, ids
}

func mustID(t *testing.T, value string) research.ID {
	t.Helper()
	id, err := research.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
