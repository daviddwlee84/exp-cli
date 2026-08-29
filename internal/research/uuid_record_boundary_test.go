package research_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestOrdinaryRecordEncodeAndDecodeRejectUUIDv5DespiteMigrationExtension(t *testing.T) {
	const imported = "74738ff5-5367-5958-9aee-98fffdcd1876"
	project := &research.Project{
		Schema:          research.SchemaProject,
		ProjectID:       research.UUID{UUID: uuid.MustParse(imported)},
		Name:            "Imported project",
		CreatedAt:       time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC),
		ExperimentsRoot: ".",
		Extensions: research.Extensions{
			research.MigrationExtension: {"fingerprint": "fixture"},
		},
	}
	if encoded, err := record.Encode(&record.Document{Record: project, Body: "\n# Imported project\n"}); err == nil || len(encoded) != 0 {
		t.Fatalf("ordinary Encode accepted UUIDv5: bytes=%d err=%v", len(encoded), err)
	}

	document := `+++
schema = "exp.project/v1"
project_id = "` + imported + `"
name = "Imported project"
created_at = 2026-08-29T12:00:00Z
experiments_root = "."

[extensions."io.github.daviddwlee84.exp-cli.harness-v0"]
fingerprint = "fixture"
+++

# Imported project
`
	if _, err := record.Decode([]byte(document)); err == nil {
		t.Fatal("ordinary Decode accepted UUIDv5")
	} else if !strings.Contains(err.Error(), "record.id_version") {
		t.Fatalf("Decode UUIDv5 error = %v", err)
	}
}
