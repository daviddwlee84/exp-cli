package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestCountsAndContextIncludeTryWithoutChangingLegacyText(t *testing.T) {
	now := time.Date(2026, 9, 3, 19, 0, 0, 0, time.UTC)
	projectID, _ := research.ParseUUID("01a03900-0000-7000-8000-000000000000")
	sourceID, _ := research.ParseID("src_01a03900-0000-7001-8000-000000000001")
	tryID, _ := research.ParseID("try_01a03900-0000-7002-8000-000000000002")
	project := &record.Document{Path: record.ProjectFile, Body: "project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Try counts", CreatedAt: now, ExperimentsRoot: ".",
	}}
	source := &record.Document{Body: "source\n", Record: &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: sourceID, Title: "Workspace", CreatedAt: now, UpdatedAt: now},
		Key:    "workspace", Kind: research.SourceGit, Subdir: ".", LocatorHints: []string{}, State: research.SourceActive,
	}}
	source.Path, _ = record.PathForNew(source.Record, nil)
	tryDocument := &record.Document{Body: "try\n", Record: &research.Try{
		Common: research.Common{Schema: research.SchemaTry, ID: tryID, Title: "Quick check", CreatedAt: now, UpdatedAt: now},
		State:  research.TryOpen, Goal: "Count this Try.", Sources: []research.ID{sourceID},
	}}
	tryDocument.Path, _ = record.PathForNew(tryDocument.Record, nil)
	inventory := record.InventoryFromDocuments(t.TempDir(), []*record.Document{project, source, tryDocument})
	if !inventory.Valid() {
		t.Fatalf("Try count inventory invalid: %v", inventory.Diagnostics)
	}
	counts := countsFor(inventory)
	if counts.Sources != 1 || counts.Tries != 1 || counts.Total != 2 {
		t.Fatalf("Try counts = %#v", counts)
	}
	human := renderContextHuman(contextData{Project: projectView{Name: "Try counts", ID: projectID.String()}, Counts: counts})
	if !strings.Contains(human, "Records: sources=1 tries=1 ideas=0") {
		t.Fatalf("Try context count missing:\n%s", human)
	}
	legacy := renderContextHuman(contextData{Project: projectView{Name: "Legacy", ID: projectID.String()}})
	if strings.Contains(legacy, "tries=") || !strings.Contains(legacy, "Records: ideas=0 queues=0") {
		t.Fatalf("zero-Try legacy text changed:\n%s", legacy)
	}
}

func TestCountsAndContextIncludeSourcesWithoutChangingZeroSourceText(t *testing.T) {
	now := time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC)
	projectID, err := research.ParseUUID("01a03800-0000-7000-8000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := research.ParseIDForKind("src_01a03800-0000-7001-8000-000000000001", research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	project := &record.Document{Path: record.ProjectFile, Body: "project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Counts", CreatedAt: now, ExperimentsRoot: ".",
	}}
	source := &record.Document{Body: "source\n", Record: &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: sourceID, Title: "Workspace", CreatedAt: now, UpdatedAt: now},
		Key:    "workspace", Kind: research.SourceGit, Subdir: ".", LocatorHints: []string{}, State: research.SourceActive,
	}}
	source.Path, err = record.PathForNew(source.Record, nil)
	if err != nil {
		t.Fatal(err)
	}
	inventory := record.InventoryFromDocuments(t.TempDir(), []*record.Document{project, source})
	if !inventory.Valid() {
		t.Fatalf("Source count inventory invalid: %v", inventory.Diagnostics)
	}
	counts := countsFor(inventory)
	if counts.Sources != 1 || counts.Total != 1 {
		t.Fatalf("Source counts = %#v", counts)
	}
	withSource := renderContextHuman(contextData{Project: projectView{Name: "Counts", ID: projectID.String()}, Counts: counts})
	if !strings.Contains(withSource, "Records: sources=1 ideas=0") {
		t.Fatalf("Source context count missing:\n%s", withSource)
	}
	withoutSource := renderContextHuman(contextData{Project: projectView{Name: "Counts", ID: projectID.String()}})
	if strings.Contains(withoutSource, "sources=") || !strings.Contains(withoutSource, "Records: ideas=0 queues=0") {
		t.Fatalf("zero-Source context text changed:\n%s", withoutSource)
	}
}
