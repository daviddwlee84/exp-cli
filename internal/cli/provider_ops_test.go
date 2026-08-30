package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/controlplane"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestPueueCancelRequiresUniqueSchedulerOwnershipAndLiveRoute(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	attemptID := mustProviderID(t, "att_01a01e61-0000-7001-8000-000000000001")
	runID := mustProviderID(t, "run_01a01e61-0000-7002-8000-000000000002")
	reference := research.ExternalRef{
		Role: research.ExternalScheduler, Provider: "pueue", Context: controlplane.LocalPueueContext,
		NativeKind: "task", NativeID: "42", ObservedAt: &now,
	}
	attempt := &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttempt, ID: attemptID, Title: "Owned task", CreatedAt: now, UpdatedAt: now},
		Run:    runID, State: research.AttemptRunning, Runner: "direct", Scheduler: "pueue", CWD: ".", Argv: []string{"/bin/true"},
		ExternalRefs: []research.ExternalRef{reference},
	}
	inventory := providerInventory(t, attempt)
	owned, observed, err := findPueueCancelOwnership(inventory, "42")
	if err != nil || owned.ID != attemptID || observed.NativeID != "42" {
		t.Fatalf("ownership = %#v %#v, %v", owned, observed, err)
	}

	expected := controlplane.PueueTaskIdentity{Context: controlplane.LocalPueueContext, Group: "gpu", Label: "study-dispatch"}
	if err := verifyPueueCancelIdentity(pueue.Snapshot{Tasks: []pueue.Task{{ID: 42, Group: "gpu", Label: "study-dispatch"}}}, 42, observed, expected); err != nil {
		t.Fatalf("valid live identity: %v", err)
	}
	for name, snapshot := range map[string]pueue.Snapshot{
		"missing":        {},
		"group mismatch": {Tasks: []pueue.Task{{ID: 42, Group: "cpu", Label: "study-dispatch"}}},
		"label mismatch": {Tasks: []pueue.Task{{ID: 42, Group: "gpu", Label: "other"}}},
		"ambiguous":      {Tasks: []pueue.Task{{ID: 42, Group: "gpu", Label: "study-dispatch"}, {ID: 42, Group: "gpu", Label: "study-dispatch"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifyPueueCancelIdentity(snapshot, 42, observed, expected); err == nil {
				t.Fatal("unsafe live identity was accepted")
			}
		})
	}
	wrongContext := observed
	wrongContext.Context = "remote"
	if err := verifyPueueCancelIdentity(pueue.Snapshot{Tasks: []pueue.Task{{ID: 42, Group: "gpu", Label: "study-dispatch"}}}, 42, wrongContext, expected); err == nil {
		t.Fatal("foreign provider context was accepted")
	}

	notOwner := research.Clone(attempt).(*research.Attempt)
	notOwner.Scheduler = "direct"
	if _, _, err := findPueueCancelOwnership(providerInventory(t, notOwner), "42"); err == nil || !strings.Contains(err.Error(), "scheduling ownership") {
		t.Fatalf("non-Pueue scheduler ownership error = %v", err)
	}
	duplicate := research.Clone(attempt).(*research.Attempt)
	duplicate.ExternalRefs = append(duplicate.ExternalRefs, reference)
	if _, _, err := findPueueCancelOwnership(providerInventory(t, duplicate), "42"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous ownership error = %v", err)
	}
}

func TestMLflowAttachmentRequiresCanonicalAttemptOwnershipTag(t *testing.T) {
	fixture := mlflowOwnershipInventory(t)
	subjectA, err := fixture.inventory.ByID(fixture.experimentA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := requireMLflowWorkloadOwnership(fixture.inventory, subjectA, map[string]string{}); err == nil || !strings.Contains(err.Error(), mlflowAttemptOwnershipTag) {
		t.Fatalf("missing Attempt ownership tag error = %v", err)
	}
	unknown := "att_01a01e61-0000-7013-8000-000000000013"
	if _, err := requireMLflowWorkloadOwnership(fixture.inventory, subjectA, map[string]string{
		mlflowAttemptOwnershipTag: unknown,
	}); err == nil {
		t.Fatal("unknown Attempt ownership was accepted")
	}
	owner, err := requireMLflowWorkloadOwnership(fixture.inventory, subjectA, fixture.tags())
	if err != nil || owner != fixture.attempt {
		t.Fatalf("owner = %s, %v", owner, err)
	}

	for name, subjectID := range map[string]research.ID{
		"other experiment": fixture.experimentB,
		"other candidate":  fixture.candidateB,
		"other release":    fixture.releaseB,
	} {
		t.Run(name, func(t *testing.T) {
			subject, err := fixture.inventory.ByID(subjectID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := requireMLflowWorkloadOwnership(fixture.inventory, subject, fixture.tags()); err == nil || !strings.Contains(err.Error(), "outside Evaluation subject") {
				t.Fatalf("cross-subject Attempt accepted: %v", err)
			}
		})
	}
	for name, subjectID := range map[string]research.ID{
		"own candidate": fixture.candidateA,
		"own release":   fixture.releaseA,
	} {
		t.Run(name, func(t *testing.T) {
			subject, err := fixture.inventory.ByID(subjectID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := requireMLflowWorkloadOwnership(fixture.inventory, subject, fixture.tags()); err != nil {
				t.Fatalf("supported subject lineage rejected: %v", err)
			}
		})
	}
}

type mlflowOwnershipFixture struct {
	inventory                *record.Inventory
	attempt                  research.ID
	experimentA, experimentB research.ID
	candidateA, candidateB   research.ID
	releaseA, releaseB       research.ID
}

func (fixture mlflowOwnershipFixture) tags() map[string]string {
	return map[string]string{mlflowAttemptOwnershipTag: fixture.attempt.String()}
}

func mlflowOwnershipInventory(t *testing.T) mlflowOwnershipFixture {
	t.Helper()
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	fixture := mlflowOwnershipFixture{
		attempt:     mustProviderID(t, "att_01a01e61-0000-7031-8000-000000000031"),
		experimentA: mustProviderID(t, "exp_01a01e61-0000-7032-8000-000000000032"),
		experimentB: mustProviderID(t, "exp_01a01e61-0000-7033-8000-000000000033"),
		candidateA:  mustProviderID(t, "cand_01a01e61-0000-7034-8000-000000000034"),
		candidateB:  mustProviderID(t, "cand_01a01e61-0000-7035-8000-000000000035"),
		releaseA:    mustProviderID(t, "rel_01a01e61-0000-7036-8000-000000000036"),
		releaseB:    mustProviderID(t, "rel_01a01e61-0000-7037-8000-000000000037"),
	}
	runID := mustProviderID(t, "run_01a01e61-0000-7038-8000-000000000038")
	evaluationA := mustProviderID(t, "eval_01a01e61-0000-7039-8000-000000000039")
	evaluationB := mustProviderID(t, "eval_01a01e61-0000-7040-8000-000000000040")
	projectID, err := research.ParseUUID("01a01e61-0000-7099-8000-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	documents := []*record.Document{{Path: record.ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "MLflow lineage", CreatedAt: now, ExperimentsRoot: ".",
	}}}
	newExperiment := func(id research.ID, title, slug string) (*research.Experiment, string) {
		design := research.Design{
			Question: "Does it work?", Hypothesis: "It works.", Kind: research.ExperimentSingleFactor,
			PrimaryFactor: "implementation", SecondaryFactors: []string{}, Baseline: "baseline",
			ComparabilitySpec: "same protocol", SuccessCriteria: []string{"score improves"}, DecisionRule: "keep if improved", DesignLockedAt: &now,
		}
		design.DesignDigest, err = research.DesignDigest(design)
		if err != nil {
			t.Fatal(err)
		}
		return &research.Experiment{
			Common:    research.Common{Schema: research.SchemaExperiment, ID: id, Title: title, CreatedAt: now, UpdatedAt: now},
			Lifecycle: research.LifecycleActive, Design: design,
		}, "e-" + id.UUIDHex()[:16] + "-" + slug
	}
	experimentA, directoryA := newExperiment(fixture.experimentA, "Experiment A", "experiment-a")
	experimentB, directoryB := newExperiment(fixture.experimentB, "Experiment B", "experiment-b")
	documents = append(documents,
		&record.Document{Path: directoryA + "/REPORT.md", Body: "# Experiment A\n", Record: experimentA},
		&record.Document{Path: directoryB + "/REPORT.md", Body: "# Experiment B\n", Record: experimentB},
	)
	run := &research.Run{
		Common:     research.Common{Schema: research.SchemaRun, ID: runID, Title: "Run A", CreatedAt: now, UpdatedAt: now},
		Experiment: fixture.experimentA, Role: research.RunValidation, Objective: "Evaluate A", ExpectedOutputs: []string{},
	}
	attempt := &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttempt, ID: fixture.attempt, Title: "MLflow owner", CreatedAt: now, UpdatedAt: now},
		Run:    runID, State: research.AttemptSucceeded, Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"/bin/true"},
		Terminal: &research.Terminal{Source: "worker", ObservedAt: now, EndedAt: now},
	}
	documents = append(documents,
		&record.Document{Path: directoryA + "/runs/" + runID.String() + "-run-a.md", Body: "# Run A\n", Record: run},
		&record.Document{Path: directoryA + "/attempts/" + fixture.attempt.String() + ".md", Body: "# Attempt\n", Record: attempt},
	)
	for _, value := range []research.Record{
		&research.Candidate{Common: research.Common{Schema: research.SchemaCandidate, ID: fixture.candidateA, Title: "Candidate A", CreatedAt: now, UpdatedAt: now}, Experiment: fixture.experimentA, Evaluation: evaluationA, GitCommit: strings.Repeat("a", 40), ChangeSet: []string{"model-a.go"}},
		&research.Candidate{Common: research.Common{Schema: research.SchemaCandidate, ID: fixture.candidateB, Title: "Candidate B", CreatedAt: now, UpdatedAt: now}, Experiment: fixture.experimentB, Evaluation: evaluationB, GitCommit: strings.Repeat("b", 40), ChangeSet: []string{"model-b.go"}},
		&research.Release{Common: research.Common{Schema: research.SchemaRelease, ID: fixture.releaseA, Title: "Release A", CreatedAt: now, UpdatedAt: now}, Target: "production", Version: "a", State: research.ReleaseDraft, Slots: []research.ReleaseSlot{{Name: "main", Candidate: fixture.candidateA}}},
		&research.Release{Common: research.Common{Schema: research.SchemaRelease, ID: fixture.releaseB, Title: "Release B", CreatedAt: now, UpdatedAt: now}, Target: "production", Version: "b", State: research.ReleaseDraft, Slots: []research.ReleaseSlot{{Name: "main", Candidate: fixture.candidateB}}},
	} {
		path, pathErr := record.PathForNew(value, nil)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		documents = append(documents, &record.Document{Path: path, Body: "# Record\n", Record: value})
	}
	fixture.inventory = record.InventoryFromDocuments(t.TempDir(), documents)
	return fixture
}

func providerInventory(t *testing.T, attempts ...*research.Attempt) *record.Inventory {
	t.Helper()
	projectID, err := research.ParseUUID("01a01e61-0000-7099-8000-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	documents := []*record.Document{{Path: record.ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Provider test", CreatedAt: now, ExperimentsRoot: ".",
	}}}
	for _, attempt := range attempts {
		documents = append(documents, &record.Document{
			Path: "e-01a01e61-provider/attempts/" + attempt.ID.String() + ".md", Body: "# Attempt\n", Record: attempt,
		})
	}
	return record.InventoryFromDocuments(t.TempDir(), documents)
}

func mustProviderID(t *testing.T, value string) research.ID {
	t.Helper()
	id, err := research.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
