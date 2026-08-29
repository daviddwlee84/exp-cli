package record

import (
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestSchemaEncodeRejectsProgrammaticUUIDv4ProjectBeforeSerialization(t *testing.T) {
	project := &research.Project{
		Schema:          research.SchemaProject,
		ProjectID:       research.UUID{UUID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")},
		Name:            "Programmatic project",
		CreatedAt:       time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC),
		ExperimentsRoot: ".",
	}
	if _, err := Encode(&Document{Record: project, Body: "# Programmatic project\n"}); !hasEncodedIssue(err, "record.id_version") {
		t.Fatalf("Encode accepted UUIDv4 Project: %v", err)
	}
}

func hasEncodedIssue(err error, code string) bool {
	for _, issue := range research.IssuesFromError(err) {
		if issue.Code == code {
			return true
		}
	}
	return false
}
