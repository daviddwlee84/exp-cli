package projection

import (
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestControlProjectionRendersQueueFrontierAndDAG(t *testing.T) {
	now := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	projectID, _ := research.ParseUUID("01a01e60-0000-7001-8000-000000000001")
	poolID := controlProjectionID(t, "pool_01a01e60-0000-7002-8000-000000000002")
	planID := controlProjectionID(t, "plan_01a01e60-0000-7003-8000-000000000003")
	queueID := controlProjectionID(t, "queue_01a01e60-0000-7004-8000-000000000004")
	documents := []*record.Document{
		{Path: record.ProjectFile, Body: "# Project\n", Record: &research.Project{Schema: research.SchemaProject, ProjectID: projectID, Name: "Control", CreatedAt: now, ExperimentsRoot: "."}},
		{Path: record.PolicyFile, Body: "# Policy\n", Record: controlProjectionPolicy(now)},
	}
	pool := &record.Document{Body: "# Pool\n", Record: &research.ResourcePool{
		Common:  research.Common{Schema: research.SchemaResourcePool, ID: poolID, Title: "GPU", CreatedAt: now, UpdatedAt: now},
		Enabled: true, Capacity: 1, Unit: "gpu", Bottleneck: "gpu",
	}}
	pool.Path, _ = record.PathForNew(pool.Record, nil)
	plan := &record.Document{Body: "# Plan\n", Record: &research.Plan{
		Common:   research.Common{Schema: research.SchemaPlanV2, ID: planID, Title: "Ablate encoder", CreatedAt: now, UpdatedAt: now},
		Priority: research.PriorityP1, Effort: research.EffortS, State: research.PlanQueued,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve score", Metric: "macro_f1", Unit: "score"},
		PrimaryCluster: "encoder", Classification: &research.Classification{
			Domain: "ml", Work: "training", Method: "ablation", Component: "encoder",
			Lane: research.LaneExploit, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman,
		},
		Resources: []research.ResourceNeed{{Pool: poolID, Units: 1, EstimatedHours: 2}},
		Utility:   &research.UtilityEstimate{Probability: .5, Impact: .1, InformationGain: .2, UnblockValue: .1, RiskPenalty: .05},
	}}
	plan.Path, _ = record.PathForNew(plan.Record, nil)
	planRevision, err := record.Revision(plan)
	if err != nil {
		t.Fatal(err)
	}
	queue := &record.Document{Body: "# Queue\n", Record: &research.Queue{
		Common:   research.Common{Schema: research.SchemaQueue, ID: queueID, Title: "Main queue", CreatedAt: now, UpdatedAt: now},
		Revision: 1, Partitions: []research.QueuePartition{{Pool: poolID, Lane: research.LaneExploit, Entries: []research.QueueEntry{{
			Plan: planID, PlanRevision: planRevision, Score: .7, InsertedAt: now,
		}}}},
	}}
	queue.Path, _ = record.PathForNew(queue.Record, nil)
	documents = append(documents, pool, plan, queue)
	inventory := record.InventoryFromDocuments(t.TempDir(), documents)
	if !inventory.Valid() {
		t.Fatalf("inventory invalid: %v", inventory.Diagnostics)
	}
	files, err := Build(inventory)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{}
	for _, file := range files {
		contents[file.Path] = string(file.Content)
	}
	if !strings.Contains(contents[READMEFile], "## Research control plane") || !strings.Contains(contents[READMEFile], "Queue\"]") {
		t.Fatalf("README control DAG missing:\n%s", contents[READMEFile])
	}
	if !strings.Contains(contents[RoadmapFile], "## Canonical queue frontier") || !strings.Contains(contents[RoadmapFile], "Ablate encoder") {
		t.Fatalf("ROADMAP frontier missing:\n%s", contents[RoadmapFile])
	}
}

func controlProjectionID(t *testing.T, value string) research.ID {
	t.Helper()
	id, err := research.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func controlProjectionPolicy(now time.Time) *research.Policy {
	return &research.Policy{
		Schema: research.SchemaPolicy, CreatedAt: now, UpdatedAt: now,
		Autonomy: research.AutonomyManual, ExploitShare: .8, ExploreShare: .2,
		ScoreFormula: "utility-v1", TiePolicy: research.QueueTieKeepIncumbent,
		PromotionRequiresHuman: true,
		Taxonomy: research.ClassificationTaxonomy{
			Domains: []string{"ml"}, Work: []string{"training"}, Methods: []string{"ablation"}, Components: []string{"encoder"},
		},
		ClusterSaturation: research.ClusterSaturationPolicy{BudgetHours: 24, PlateauWindow: 5, MinimumImprovement: .01, MinimumProbability: .1},
	}
}
