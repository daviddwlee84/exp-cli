package cli

import (
	"bytes"
	"encoding/json"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestChampionManifestSlotVersionsAndNoHostPaths(t *testing.T) {
	now := time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC)
	sourceID := mustManifestID(t, "src_01a09000-0000-7701-8000-000000000701")
	attemptID := mustManifestID(t, "att_01a09000-0000-7702-8000-000000000702")
	experimentID := mustManifestID(t, "exp_01a09000-0000-7703-8000-000000000703")
	evaluationID := mustManifestID(t, "eval_01a09000-0000-7704-8000-000000000704")
	projectID, err := research.ParseUUID("01a09000-0000-7700-8000-000000000700")
	if err != nil {
		t.Fatal(err)
	}
	project := &record.Document{Path: record.ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Manifest", CreatedAt: now, ExperimentsRoot: ".",
	}}
	source := &record.Document{Record: &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: sourceID, Title: "Production", CreatedAt: now, UpdatedAt: now},
		Key:    "production", Kind: research.SourceGit, Subdir: "services/api",
		LocatorHints: []string{"https://github.com/example/production.git"}, State: research.SourceActive,
	}, Body: "# Source\n"}
	source.Path, err = record.PathForNew(source.Record, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := research.SourceSnapshot{
		Source: sourceID, Subdir: "services/api", PolicyVersion: "capture-v1", CapturedAt: now,
		GitObjectFormat: research.GitObjectSHA1,
		BaseCommit:      strings.Repeat("0", 40), HeadCommit: strings.Repeat("b", 40),
		ChangeSet: []string{"services/api/model.go"}, State: research.SourceSnapshotClean,
		Reproducibility: research.ReproducibilityExact,
	}
	snapshot.Digest, err = research.SourceSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	attempt := &record.Document{Record: &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV3, ID: attemptID, Title: "Formal attempt", CreatedAt: now, UpdatedAt: now},
		Run:    mustManifestID(t, "run_01a09000-0000-7705-8000-000000000705"), State: research.AttemptPlanned,
		Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"true"},
		ExecutionSource: sourceID, SourceSnapshots: []research.SourceSnapshot{snapshot},
	}, Body: "# Attempt\n", Path: path.Join("e-01a09000-formal", "attempts", attemptID.String()+".md")}
	candidate := &research.Candidate{
		Common:     research.Common{Schema: research.SchemaCandidateV2, ID: mustManifestID(t, "cand_01a09000-0000-7706-8000-000000000706"), Title: "Candidate", CreatedAt: now, UpdatedAt: now},
		Experiment: experimentID, Evaluation: evaluationID, Attempt: attemptID,
		Sources: []research.CandidateSource{{Source: sourceID, HeadCommit: snapshot.HeadCommit, ChangeSet: append([]string{}, snapshot.ChangeSet...)}},
	}
	candidateDocument := &record.Document{Record: candidate, Body: "# Candidate\n"}
	candidateDocument.Path, err = record.PathForNew(candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	inventory := record.InventoryFromDocuments("/Users/private/checkout/experiments", []*record.Document{project, source, attempt, candidateDocument})
	projected, sourceAware, err := projectChampionManifestSlot(inventory, "model", candidate)
	if err != nil || !sourceAware {
		t.Fatalf("project v2 manifest slot = %#v, %v, %v", projected, sourceAware, err)
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, required := range []string{
		`"attempt":"` + attemptID.String() + `"`,
		`"execution_source":"` + sourceID.String() + `"`,
		`"locator_hints":["https://github.com/example/production.git"]`,
		`"subdir":"services/api"`,
		`"head_commit":"` + snapshot.HeadCommit + `"`,
		`"change_set":["services/api/model.go"]`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("manifest lacks %s: %s", required, text)
		}
	}
	if strings.Contains(text, "/Users/") || strings.Contains(text, "private/checkout") || strings.Contains(text, `"git_commit"`) {
		t.Fatalf("Source-aware manifest leaked host/legacy identity: %s", text)
	}

	inventorySource, err := inventory.ByID(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	inventorySource.Record.(*research.Source).Subdir = "."
	candidate.Sources[0].ChangeSet = []string{}
	rootProjected, sourceAware, err := projectChampionManifestSlot(inventory, "model", candidate)
	if err != nil || !sourceAware || len(rootProjected.Sources) != 1 || rootProjected.Sources[0].Subdir != "." || rootProjected.Sources[0].ChangeSet == nil || len(rootProjected.Sources[0].ChangeSet) != 0 {
		t.Fatalf("root empty-change-set manifest = %#v, %v, %v", rootProjected, sourceAware, err)
	}

	legacy := &research.Candidate{
		Common:     research.Common{Schema: research.SchemaCandidate, ID: mustManifestID(t, "cand_01a09000-0000-7707-8000-000000000707"), Title: "Legacy", CreatedAt: now, UpdatedAt: now},
		Experiment: experimentID, Evaluation: evaluationID,
		GitCommit: strings.Repeat("a", 40), ChangeSet: []string{"model.go"},
	}
	legacySlot, sourceAware, err := projectChampionManifestSlot(inventory, "model", legacy)
	if err != nil || sourceAware {
		t.Fatalf("project v1 manifest slot = %#v, %v, %v", legacySlot, sourceAware, err)
	}
	legacyJSON, _ := json.Marshal(legacySlot)
	if strings.Contains(string(legacyJSON), "attempt") || strings.Contains(string(legacyJSON), "execution_source") || strings.Contains(string(legacyJSON), "sources") {
		t.Fatalf("legacy manifest shape changed: %s", legacyJSON)
	}
}

func TestCandidateLegacyFlagShapeRemainsImplicitlyCompatible(t *testing.T) {
	if !candidateUsesLegacyShape(&scientificOptions{gitCommit: strings.Repeat("a", 40), changeSet: []string{"model.go"}}) {
		t.Fatal("unambiguous pre-existing Candidate v1 flags no longer infer the legacy path")
	}
	if candidateUsesLegacyShape(&scientificOptions{attempt: "att_01a09000-0000-7000-8000-000000000000"}) {
		t.Fatal("Candidate v2 Attempt-only flags inferred the legacy path")
	}
}

func TestChampionManifestHumanOutputPreservesCanonicalSSHLocator(t *testing.T) {
	var output bytes.Buffer
	app := NewApp(t.Context(), nil, &output, nil)
	encoded := []byte(`{"schema_version":"exp.champion-manifest/v3","locator_hints":["ssh://git@github.com/org/repo.git"]}`)
	if err := writeChampionManifestOutput(app, false, championManifest{}, nil, encoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "ssh://git@github.com/org/repo.git") || strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("canonical SSH locator was corrupted: %s", output.String())
	}
}

func mustManifestID(t *testing.T, value string) research.ID {
	t.Helper()
	id, err := research.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
