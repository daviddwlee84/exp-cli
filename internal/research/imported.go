package research

import "fmt"

// ValidateImported applies the ordinary record invariants while permitting the
// UUIDv5 identity form reserved for a provenance-checked migration reader.
//
// This function deliberately does not authenticate migration provenance. The
// record package performs that filesystem-backed check against the exact
// harness-v0 source archive before an imported document enters an Inventory.
// Ordinary Decode and Encode continue to call Validate and therefore reject
// UUIDv5 records.
func ValidateImported(record Record) error {
	err := Validate(record)
	if err == nil {
		return nil
	}
	issues := IssuesFromError(err)
	if len(issues) == 0 {
		return err
	}
	remaining := make([]Issue, 0, len(issues))
	removedVersionIssue := false
	for _, issue := range issues {
		switch issue.Code {
		case "record.id_version", "reference.id_version":
			removedVersionIssue = true
		default:
			remaining = append(remaining, issue)
		}
	}
	if len(remaining) != 0 {
		return &ValidationError{Issues: remaining}
	}
	if !removedVersionIssue {
		return err
	}
	if record == nil {
		return fmt.Errorf("imported UUID validation requires a record: %w", ErrInvalidRecord)
	}
	switch value := record.(type) {
	case *Project:
		if !value.ProjectID.IsImported() || !hasMigrationExtension(record) {
			return fmt.Errorf("migration Project identity must be UUIDv5: %w", ErrInvalidRecord)
		}
	default:
		id, ok := record.GetID()
		if !ok {
			return fmt.Errorf("migration-aware record must have a typed ID: %w", ErrInvalidRecord)
		}
		if id.IsImported() && !hasMigrationExtension(record) {
			return fmt.Errorf("imported UUIDs require %q provenance: %w", MigrationExtension, ErrInvalidRecord)
		}
		if !id.IsImported() && !id.IsNative() {
			return fmt.Errorf("migration-aware record identity is not UUIDv5 or UUIDv7: %w", ErrInvalidRecord)
		}
	}
	return nil
}
