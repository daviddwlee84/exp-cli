package projection

import (
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestTryProjectionIncludesCountTableAndGraphEdges(t *testing.T) {
	now := time.Date(2026, 9, 3, 20, 0, 0, 0, time.UTC)
	projectID, _ := research.ParseUUID("01a09000-0000-7800-8000-000000000800")
	sourceID, _ := research.ParseID("src_01a09000-0000-7801-8000-000000000801")
	tryID, _ := research.ParseID("try_01a09000-0000-7802-8000-000000000802")
	project := &record.Document{Path: record.ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Try projection", CreatedAt: now, ExperimentsRoot: ".",
	}}
	source := &record.Document{Body: "# Source\n", Record: &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: sourceID, Title: "Production", CreatedAt: now, UpdatedAt: now},
		Key:    "production", Kind: research.SourceGit, Subdir: ".", LocatorHints: []string{"https://github.com/example/production.git"}, State: research.SourceActive,
	}}
	source.Path, _ = record.PathForNew(source.Record, nil)
	tryDocument := &record.Document{Body: "# Try\n", Record: &research.Try{
		Common: research.Common{Schema: research.SchemaTry, ID: tryID, Title: "Fast check", CreatedAt: now, UpdatedAt: now},
		State:  research.TryOpen, Goal: "Test one bounded direction.", Sources: []research.ID{sourceID},
	}}
	tryDocument.Path, _ = record.PathForNew(tryDocument.Record, nil)
	inventory := record.InventoryFromDocuments(t.TempDir(), []*record.Document{project, source, tryDocument})
	if !inventory.Valid() {
		t.Fatalf("Try projection inventory invalid: %v", inventory.Diagnostics)
	}
	files, err := Build(inventory)
	if err != nil {
		t.Fatal(err)
	}
	var readme string
	for _, file := range files {
		if file.Path == READMEFile {
			readme = string(file.Content)
		}
	}
	for _, fragment := range []string{
		"| Sources | Tries | Plans | Experiments | Runs | Attempts | Findings | Decisions |",
		"| 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 |",
		"## Tries",
		"[Y-01A09000](t-01a09000000078028000000000000802-fast-check/TRY.md)",
		"| open | Test one bounded direction. | Fast check |",
		`["Y-01A09000 Try"]`,
		"u_01a09000 --> y_01a09000",
	} {
		if !strings.Contains(readme, fragment) {
			t.Errorf("Try projection lacks %q:\n%s", fragment, readme)
		}
	}
}
