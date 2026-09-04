package projection

import (
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestSourceProjectionIncludesCountBindingAndGraphNode(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	projectID, err := research.ParseUUID("01a03200-0000-7000-8000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := research.ParseIDForKind("src_01a03200-0000-7001-8000-000000000001", research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	project := &record.Document{Path: record.ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "External workspace", CreatedAt: now, ExperimentsRoot: ".",
	}}
	source := &record.Document{Body: "# Source\n", Record: &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: sourceID, Title: "Production repository", CreatedAt: now, UpdatedAt: now},
		Key:    "production", Kind: research.SourceGit, Subdir: "services/api",
		LocatorHints: []string{"https://github.com/example/production.git"}, State: research.SourceActive,
	}}
	source.Path, err = record.PathForNew(source.Record, nil)
	if err != nil {
		t.Fatal(err)
	}
	inventory := record.InventoryFromDocuments(t.TempDir(), []*record.Document{project, source})
	if !inventory.Valid() {
		t.Fatalf("Source projection inventory invalid: %v", inventory.Diagnostics)
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
		"| Sources | Plans | Experiments | Runs | Attempts | Findings | Decisions |",
		"| 1 | 0 | 0 | 0 | 0 | 0 | 0 |",
		"## Sources",
		"[U-01A03200](sources/src_01a03200-0000-7001-8000-000000000001-production-repository.md)",
		"| production | active | services/api | Production repository |",
		`["U-01A03200 Source"]`,
	} {
		if !strings.Contains(readme, fragment) {
			t.Errorf("Source projection lacks %q:\n%s", fragment, readme)
		}
	}
	if strings.Contains(readme, "https://") {
		t.Fatalf("Source locator unexpectedly rendered into projection:\n%s", readme)
	}
}
