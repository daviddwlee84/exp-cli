package research

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSchemaValidationRequiresExactNativeAndMigratedUUIDVersions(t *testing.T) {
	v4 := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	v5 := uuid.MustParse("74738ff5-5367-5958-9aee-98fffdcd1876")
	v7 := uuid.MustParse("01a01e66-f8e0-7202-8000-000000000202")
	nonRFCV7 := v7
	nonRFCV7[8] = nonRFCV7[8]&0x1f | 0xe0

	for name, project := range map[string]*Project{
		"native v4": func() *Project {
			value := schemaUUIDProject(t)
			value.ProjectID = UUID{UUID: v4}
			return value
		}(),
		"native non-RFC v7": func() *Project {
			value := schemaUUIDProject(t)
			value.ProjectID = UUID{UUID: nonRFCV7}
			return value
		}(),
		"migrated v4": func() *Project {
			value := schemaUUIDProject(t)
			value.ProjectID = UUID{UUID: v4}
			value.Extensions = Extensions{MigrationExtension: {"fingerprint": "fixture"}}
			return value
		}(),
		"migration extension with v5": func() *Project {
			value := schemaUUIDProject(t)
			value.ProjectID = UUID{UUID: v5}
			value.Extensions = Extensions{MigrationExtension: {"fingerprint": "fixture"}}
			return value
		}(),
	} {
		t.Run("project "+name, func(t *testing.T) {
			if err := Validate(project); !hasIssueCode(err, "record.id_version") {
				t.Fatalf("wrong Project UUID version validated: %v", err)
			}
		})
	}

	for name, plan := range map[string]*Plan{
		"native v4": func() *Plan {
			value := schemaUUIDPlan(t)
			value.ID = ID{kind: KindPlan, uuid: v4}
			return value
		}(),
		"native non-RFC v7": func() *Plan {
			value := schemaUUIDPlan(t)
			value.ID = ID{kind: KindPlan, uuid: nonRFCV7}
			return value
		}(),
		"migrated v4": func() *Plan {
			value := schemaUUIDPlan(t)
			value.ID = ID{kind: KindPlan, uuid: v4}
			value.Extensions = Extensions{MigrationExtension: {"fingerprint": "fixture"}}
			return value
		}(),
		"migration extension with v5": func() *Plan {
			value := schemaUUIDPlan(t)
			value.ID = ID{kind: KindPlan, uuid: v5}
			value.Extensions = Extensions{MigrationExtension: {"fingerprint": "fixture"}}
			return value
		}(),
	} {
		t.Run("record "+name, func(t *testing.T) {
			if err := Validate(plan); !hasIssueCode(err, "record.id_version") {
				t.Fatalf("wrong record UUID version validated: %v", err)
			}
		})
	}

	nativeProject := schemaUUIDProject(t)
	nativeProject.ProjectID = UUID{UUID: v7}
	if err := Validate(nativeProject); err != nil {
		t.Fatalf("native UUIDv7 Project rejected: %v", err)
	}
	extensionProject := schemaUUIDProject(t)
	extensionProject.ProjectID = UUID{UUID: v7}
	extensionProject.Extensions = Extensions{MigrationExtension: {"fingerprint": "fixture"}}
	if err := Validate(extensionProject); err != nil {
		t.Fatalf("UUIDv7 Project with migration metadata rejected: %v", err)
	}

	nativePlan := schemaUUIDPlan(t)
	nativePlan.ID = ID{kind: KindPlan, uuid: v7}
	if err := Validate(nativePlan); err != nil {
		t.Fatalf("native UUIDv7 record rejected: %v", err)
	}
	extensionPlan := schemaUUIDPlan(t)
	extensionPlan.ID = ID{kind: KindPlan, uuid: v7}
	extensionPlan.Extensions = Extensions{MigrationExtension: {"fingerprint": "fixture"}}
	if err := Validate(extensionPlan); err != nil {
		t.Fatalf("UUIDv7 record with migration metadata rejected: %v", err)
	}
}

func TestOrdinaryValidationRejectsUUIDv5References(t *testing.T) {
	plan := schemaUUIDPlan(t)
	plan.Assumptions = []ID{{kind: KindFinding, uuid: uuid.MustParse("74738ff5-5367-5958-9aee-98fffdcd1876")}}
	plan.Extensions = Extensions{MigrationExtension: {"fingerprint": "fixture"}}
	if err := Validate(plan); !hasIssueCode(err, "reference.id_version") {
		t.Fatalf("migration extension authorized a UUIDv5 reference: %v", err)
	}
}

func schemaUUIDProject(t *testing.T) *Project {
	t.Helper()
	id, err := ParseUUID("01a01e66-0e80-7101-8000-000000000101")
	if err != nil {
		t.Fatal(err)
	}
	return &Project{
		Schema: SchemaProject, ProjectID: id, Name: "Project",
		CreatedAt: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC), ExperimentsRoot: ".",
	}
}

func schemaUUIDPlan(t *testing.T) *Plan {
	t.Helper()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	return &Plan{
		Common: Common{
			Schema: SchemaPlan, ID: mustID(t, "plan_01a01e66-f8e0-7202-8000-000000000202"),
			Title: "Plan", CreatedAt: now, UpdatedAt: now,
		},
		Priority: PriorityP1, Effort: EffortS, State: PlanQueued,
		ExpectedPayoff: ExpectedPayoff{Summary: "Improve score", Metric: "macro_f1", Unit: "score"},
	}
}
