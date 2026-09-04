package record

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestSourceCodecRoundTripIsDeterministicAndClosed(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 123_000_000, time.FixedZone("UTC+0", 0))
	source := recordTestSource(t, "src_01a03100-0000-7001-8000-000000000001", "production", now)
	source.Extensions = research.Extensions{"org.example.source": {"mirror": true}}
	document := &Document{Record: source, Body: "\n# Production source\n"}
	encoded, err := Encode(document)
	if err != nil {
		t.Fatalf("Encode Source: %v", err)
	}
	for _, fragment := range []string{
		`schema = "exp.source/v1"`,
		`id = "src_01a03100-0000-7001-8000-000000000001"`,
		`key = "production"`,
		`kind = "git"`,
		`subdir = "."`,
		`locator_hints = ["https://github.com/example/repo.git"]`,
		`state = "active"`,
	} {
		if !bytes.Contains(encoded, []byte(fragment)) {
			t.Errorf("encoded Source lacks %q:\n%s", fragment, encoded)
		}
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode Source: %v\n%s", err, encoded)
	}
	got, ok := decoded.Record.(*research.Source)
	if !ok || got.Key != source.Key || got.Kind != research.SourceGit || len(got.LocatorHints) != 1 || got.LocatorHints[0] != source.LocatorHints[0] {
		t.Fatalf("decoded Source = %#v", decoded.Record)
	}
	if !ValidRevision(decoded.Revision) {
		t.Fatalf("Source revision = %q", decoded.Revision)
	}
	reencoded, err := Encode(decoded)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("Source re-encode differs: %v\n%s\n%s", err, encoded, reencoded)
	}

	unknown := bytes.Replace(encoded, []byte("state = \"active\"\n"), []byte("state = \"active\"\nunknown_source_field = true\n"), 1)
	_, err = Decode(unknown)
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != "record.unknown_field" || !strings.Contains(coded.Message, "unknown_source_field") {
		t.Fatalf("Source unknown-field error = %v", err)
	}
	missing := bytes.Replace(encoded, []byte(`locator_hints = ["https://github.com/example/repo.git"]`+"\n"), nil, 1)
	_, err = Decode(missing)
	if !errors.As(err, &coded) || coded.Code != "record.missing_field" || !strings.Contains(coded.Message, "locator_hints") {
		t.Fatalf("Source missing required locator_hints error = %v", err)
	}
}

func TestAddingSourceDoesNotOpenExistingSchemaDecoder(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	encoded, err := Encode(&Document{Record: validTestPlan(t, now), Body: "body\n"})
	if err != nil {
		t.Fatal(err)
	}
	withSourceField := bytes.Replace(encoded, []byte("priority = \"P1\"\n"), []byte("key = \"must-remain-unknown\"\npriority = \"P1\"\n"), 1)
	_, err = Decode(withSourceField)
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != "record.unknown_field" {
		t.Fatalf("legacy Plan decoder accepted Source field: %v", err)
	}
}

func TestSourceFlatLayoutClassifiesKindAndIdentity(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	source := recordTestSource(t, "src_01a03100-0000-7002-8000-000000000002", "workspace", now)
	canonical, err := PathForNew(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "sources/src_01a03100-0000-7002-8000-000000000002-production-workspace.md"
	if canonical != want {
		t.Fatalf("Source path = %q, want %q", canonical, want)
	}
	location, recognized, err := ClassifyPath(canonical)
	if err != nil || !recognized || location.Kind != research.KindSource || location.ID != source.ID || location.Slug != "production-workspace" {
		t.Fatalf("Source location = %#v, recognized=%v, err=%v", location, recognized, err)
	}
	if err := ValidateDocumentPath(location, &Document{Record: source, Path: canonical}); err != nil {
		t.Fatalf("validate Source path: %v", err)
	}
	if _, recognized, err := ClassifyPath("sources/archive/" + source.ID.String() + ".md"); recognized || err != nil {
		t.Fatalf("legacy nested sources/ content = recognized %v, err %v", recognized, err)
	}
	found := false
	for _, directory := range CanonicalFlatDirs() {
		found = found || directory == SourcesDir
	}
	if !found {
		t.Fatalf("%s missing from canonical flat directories", SourcesDir)
	}
}

func TestInventoryIndexesSourceKeysAndRejectsDuplicates(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	projectID, err := research.ParseUUID("01a03100-0000-7000-8000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	project := &Document{Path: ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Source inventory", CreatedAt: now, ExperimentsRoot: ".",
	}}
	first := &Document{Body: "# First\n", Record: recordTestSource(t, "src_01a03100-0000-7003-8000-000000000003", "production", now)}
	second := &Document{Body: "# Second\n", Record: recordTestSource(t, "src_01a03100-0000-7004-8000-000000000004", "production", now)}
	first.Path, _ = PathForNew(first.Record, nil)
	second.Path, _ = PathForNew(second.Record, nil)
	inventory := InventoryFromDocuments(t.TempDir(), []*Document{project, first, second})
	if inventory.Valid() {
		t.Fatal("duplicate Source keys formed a valid inventory")
	}
	count := 0
	for _, diagnostic := range inventory.Diagnostics {
		if diagnostic.Code == "source.duplicate_key" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("duplicate Source-key diagnostics = %d, want 2: %v", count, inventory.Diagnostics)
	}
	if _, err := inventory.BySourceKey("production"); !errors.Is(err, research.ErrAmbiguousReference) {
		t.Fatalf("ambiguous Source key lookup = %v", err)
	}

	inventory = InventoryFromDocuments(t.TempDir(), []*Document{project, first})
	if !inventory.Valid() {
		t.Fatalf("unique Source inventory invalid: %v", inventory.Diagnostics)
	}
	for _, query := range []string{"production", " Production ", first.Record.(*research.Source).ID.String(), "U-01A03100"} {
		document, err := inventory.ResolveSource(query)
		if err != nil || document.Record.(*research.Source).ID != first.Record.(*research.Source).ID {
			t.Fatalf("ResolveSource(%q) = %#v, %v", query, document, err)
		}
	}
	if got := inventory.OfKind(research.KindSource); len(got) != 1 || got[0].Path != first.Path {
		t.Fatalf("Source inventory projection = %#v", got)
	}
}

func TestSourceReferencesCannotBeShadowedByNormalizedKeys(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 30, 0, 0, time.UTC)
	projectID, err := research.ParseUUID("01a03120-0000-7000-8000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	project := &Document{Path: ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Reference authority", CreatedAt: now, ExperimentsRoot: ".",
	}}
	target := recordTestSource(t, "src_01a03110-0000-7001-8000-000000000001", "production", now)
	collisions := []*research.Source{
		recordTestSource(t, "src_01a03210-0000-7002-8000-000000000002", "src-01a03110-0000-7001-8000-000000000001", now),
		recordTestSource(t, "src_01a03310-0000-7003-8000-000000000003", "src-01a03110", now),
		recordTestSource(t, "src_01a03410-0000-7004-8000-000000000004", "u-01a03110", now),
	}
	documents := []*Document{project, &Document{Body: "# Target\n", Record: target}}
	for _, source := range collisions {
		documents = append(documents, &Document{Body: "# Collision\n", Record: source})
	}
	for _, document := range documents[1:] {
		document.Path, err = PathForNew(document.Record, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	inventory := InventoryFromDocuments(t.TempDir(), documents)
	if !inventory.Valid() {
		t.Fatalf("collision fixture is invalid: %v", inventory.Diagnostics)
	}
	for _, query := range []string{target.ID.String(), "src_01a03110", "U-01A03110", "u-01a03110"} {
		document, resolveErr := inventory.ResolveSource(query)
		if resolveErr != nil || document.Record.(*research.Source).ID != target.ID {
			t.Fatalf("ResolveSource(%q) = %#v, %v; want %s", query, document, resolveErr, target.ID)
		}
	}
}

func TestLegacyUnrelatedSourcesTreeRemainsValid(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 45, 0, 0, time.UTC)
	projectID, err := research.ParseUUID("01a03130-0000-7000-8000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(&Document{Path: ProjectFile, Body: "# Legacy Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Legacy sources subtree", CreatedAt: now, ExperimentsRoot: ".",
	}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ProjectFile), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, SourcesDir, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, SourcesDir, "notes.md"), []byte("unrelated legacy notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, SourcesDir, "archive", "old.md"), []byte("old material\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("notes.md", filepath.Join(root, SourcesDir, "guide")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	inventory, err := LoadInventory(root)
	if err != nil || !inventory.Valid() || len(inventory.OfKind(research.KindSource)) != 0 {
		t.Fatalf("legacy sources tree inventory = %#v, %v", inventory, err)
	}
}

func recordTestSource(t *testing.T, idText, key string, now time.Time) *research.Source {
	t.Helper()
	id, err := research.ParseIDForKind(idText, research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	return &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: id, Title: "Production workspace", CreatedAt: now, UpdatedAt: now},
		Key:    key, Kind: research.SourceGit, Subdir: ".",
		LocatorHints: []string{"https://github.com/example/repo.git"}, State: research.SourceActive,
	}
}
