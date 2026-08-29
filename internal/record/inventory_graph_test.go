package record

import (
	"reflect"
	"slices"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestInventoryDetectsDuplicateIDsAndAliases(t *testing.T) {
	inventory := loadValidTestInventory(t)
	plan := inventory.OfKind(research.KindPlan)[0]
	duplicate := plan.Clone()
	duplicate.Path = "plans/" + duplicate.Record.(*research.Plan).ID.String() + "-duplicate-navigation-slug.md"
	candidate := InventoryFromDocuments(inventory.Root, append(cloneDocuments(inventory.Documents), duplicate))
	if !hasDiagnosticCode(candidate.Diagnostics, "record.duplicate_id") {
		t.Fatalf("duplicate ID diagnostics = %v", candidate.Diagnostics)
	}

	base := inventory.OfKind(research.KindFinding)[0]
	first := migratedFindingClone(t, base, "one", "F-039")
	second := migratedFindingClone(t, base, "two", "F-039")
	withAliases := InventoryFromDocuments(inventory.Root, append(cloneDocuments(inventory.Documents), first, second))
	if !hasDiagnosticCode(withAliases.Diagnostics, "record.duplicate_alias") {
		t.Fatalf("duplicate alias diagnostics = %v", withAliases.Diagnostics)
	}
}

func TestInventoryDetectsOwnershipAndCyclesDeterministically(t *testing.T) {
	inventory := loadValidTestInventory(t)
	documents := cloneDocuments(inventory.Documents)
	for _, document := range documents {
		if document.Kind() == research.KindRun {
			run := document.Record.(*research.Run)
			document.Path = "e-deadbeef-other-experiment/runs/" + run.ID.String() + "-moved.md"
		}
	}
	owned := InventoryFromDocuments(inventory.Root, documents)
	if !hasDiagnosticCode(owned.Diagnostics, "relationship.wrong_owner") {
		t.Fatalf("ownership diagnostics = %v", owned.Diagnostics)
	}

	original := inventory.OfKind(research.KindFinding)[0]
	first := original.Clone()
	firstID, _ := first.ID()
	secondID, err := research.ParseID("fnd_01a01e9e-0000-7808-8000-000000000808")
	if err != nil {
		t.Fatal(err)
	}
	first.Record.(*research.Finding).Weakens = []research.ID{secondID}
	second := original.Clone()
	secondFinding := second.Record.(*research.Finding)
	secondFinding.ID = secondID
	secondFinding.Title = "Second cyclic finding"
	secondFinding.Weakens = []research.ID{firstID}
	secondFinding.Overturns = nil
	second.Path = "findings/" + secondID.String() + "-second-cyclic-finding.md"
	cycleDocs := replaceDocument(cloneDocuments(inventory.Documents), firstID, first)
	cycleDocs = append(cycleDocs, second)
	cycles := InventoryFromDocuments(inventory.Root, cycleDocs)
	cycleCount := 0
	for _, diagnostic := range cycles.Diagnostics {
		if diagnostic.Code == "reference.cycle" {
			cycleCount++
		}
	}
	if cycleCount != 2 {
		t.Fatalf("cycle diagnostics = %v", cycles.Diagnostics)
	}
	slices.Reverse(cycleDocs)
	reversed := InventoryFromDocuments(inventory.Root, cycleDocs)
	if !reflect.DeepEqual(cycles.Diagnostics, reversed.Diagnostics) {
		t.Fatalf("diagnostics depend on input order\nforward: %v\nreverse: %v", cycles.Diagnostics, reversed.Diagnostics)
	}
}

func TestInventoryRejectsAttemptThatPredatesDesignLock(t *testing.T) {
	inventory := loadValidTestInventory(t)
	documents := cloneDocuments(inventory.Documents)
	var experimentID research.ID
	for _, document := range documents {
		if experiment, ok := document.Record.(*research.Experiment); ok {
			locked := experiment.CreatedAt.Add(5 * 60 * 1e9)
			experiment.Design.DesignLockedAt = &locked
			experimentID = experiment.ID
		}
	}
	candidate := InventoryFromDocuments(inventory.Root, documents)
	if !hasDiagnosticCode(candidate.Diagnostics, "experiment.design_unlocked") {
		t.Fatalf("Attempt predating %s lock diagnostics = %v", experimentID, candidate.Diagnostics)
	}
}

func migratedFindingClone(t *testing.T, base *Document, name, alias string) *Document {
	t.Helper()
	value := base.Clone()
	finding := value.Record.(*research.Finding)
	generated := map[string]uuid.UUID{
		"one": uuid.MustParse("01a01ea0-0000-7101-8000-000000000901"),
		"two": uuid.MustParse("01a01ea1-0000-7202-8000-000000000902"),
	}[name]
	id, err := research.NewID(research.KindFinding, generated)
	if err != nil {
		t.Fatal(err)
	}
	finding.ID = id
	finding.Title = "Migrated finding " + name
	finding.LegacyAliases = []string{alias}
	finding.Extensions = research.Extensions{research.MigrationExtension: {"fingerprint": name}}
	finding.Weakens = nil
	finding.Overturns = nil
	value.Path = "findings/" + id.String() + "-migrated-finding-" + name + ".md"
	value.Revision = ""
	return value
}

func loadValidTestInventory(t *testing.T) *Inventory {
	t.Helper()
	inventory, err := LoadInventory("../../testdata/v1/valid-project")
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Valid() {
		t.Fatalf("fixture invalid: %v", inventory.Diagnostics)
	}
	return inventory
}

func hasDiagnosticCode(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
