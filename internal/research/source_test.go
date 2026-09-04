package research

import (
	"strings"
	"testing"
	"time"
)

func TestSourceKindIDSchemaAndDisplayMappings(t *testing.T) {
	id, err := ParseIDForKind("src_01a03000-0000-7001-8000-000000000001", KindSource)
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind() != KindSource || id.String() != "src_01a03000-0000-7001-8000-000000000001" || !id.IsNative() {
		t.Fatalf("Source ID = %#v", id)
	}
	if schema, err := KindSource.Schema(); err != nil || schema != SchemaSource {
		t.Fatalf("Source schema = %q, %v", schema, err)
	}
	if kind, err := KindForSchema(SchemaSource); err != nil || kind != KindSource {
		t.Fatalf("KindForSchema(Source) = %q, %v", kind, err)
	}
	if prefix, err := KindSource.IDPrefix(); err != nil || prefix != "src_" {
		t.Fatalf("Source ID prefix = %q, %v", prefix, err)
	}
	code, err := DisplayCode(id, []ReferenceCandidate{{ID: id}})
	if err != nil || code != "U-01A03000" {
		t.Fatalf("Source display code = %q, %v", code, err)
	}
	resolved, err := Resolve(strings.ToLower(code), KindSource, []ReferenceCandidate{{ID: id}})
	if err != nil || resolved != id {
		t.Fatalf("resolve Source display code = %s, %v", resolved, err)
	}
}

func TestSourceKeySubdirAndLocatorNormalization(t *testing.T) {
	key, err := NormalizeSourceKey("  Production__Workspace  ")
	if err != nil || key != "production-workspace" {
		t.Fatalf("normalized key = %q, %v", key, err)
	}
	if _, err := NormalizeSourceKey("資料庫"); err == nil {
		t.Fatal("non-ASCII-only key normalized to a valid empty key")
	}

	for input, want := range map[string]string{
		"":                    ".",
		".":                   ".",
		"./cmd//worker/":      "cmd/worker",
		"services/api/config": "services/api/config",
	} {
		got, err := NormalizeSourceSubdir(input)
		if err != nil || got != want {
			t.Errorf("NormalizeSourceSubdir(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"/private/repo", `C:\\repo`, "../repo", "src/../repo", "~/repo", "file:///repo", "token%253DCANARY"} {
		if _, err := NormalizeSourceSubdir(input); err == nil {
			t.Errorf("unsafe Source subdir %q normalized", input)
		}
	}

	locators := map[string]string{
		"HTTPS://GitHub.COM:443/Org/Repo.git/": "https://github.com/Org/Repo.git",
		"git@GitHub.COM:Org/Repo.git":          "ssh+scp://git@github.com/Org/Repo.git",
		"git@GitHub.COM:/Org/Repo.git":         "ssh://git@github.com/Org/Repo.git",
		"ssh://git@GitHub.COM:22/Org/Repo.git": "ssh://git@github.com/Org/Repo.git",
		"git://GitHub.COM/Org/Repo.git/":       "git://github.com/Org/Repo.git",
	}
	for input, want := range locators {
		got, err := NormalizeSourceLocator(input)
		if err != nil || got != want {
			t.Errorf("NormalizeSourceLocator(%q) = %q, %v; want %q", input, got, err, want)
		}
		if again, againErr := NormalizeSourceLocator(got); againErr != nil || again != got {
			t.Errorf("normalized Source locator is not idempotent: %q -> %q, %v", got, again, againErr)
		}
	}
	aliceRelative, err := NormalizeSourceLocator("alice@git.example:repos/app.git")
	if err != nil {
		t.Fatal(err)
	}
	for _, distinct := range []string{"ssh://mallory@git.example/repos/app.git", "ssh://alice@git.example/repos/app.git", "ssh://alice@git.example/~/repos/app.git"} {
		normalized, normalizeErr := NormalizeSourceLocator(distinct)
		if normalizeErr != nil {
			t.Fatal(normalizeErr)
		}
		if normalized == aliceRelative {
			t.Fatalf("distinct SSH identities collapsed: %q and %q", aliceRelative, normalized)
		}
	}
	for _, input := range []string{
		"/Users/alice/repo", `C:\\Users\\alice\\repo`, "../repo", "./repo", "file:///private/repo",
		"https://alice:CANARY@example.invalid/repo.git", "https://example.invalid/repo.git?token=CANARY",
		"git@example.invalid:repo.git#main", "ssh://git@example.invalid/repo.git?branch=main",
		"https://example.invalid/token=CANARY", "https://example.invalid/token%253DCANARY", "https://example.invalid", "urn:example:repo",
	} {
		_, err := NormalizeSourceLocator(input)
		if err == nil {
			t.Errorf("unsafe/non-remote Source locator %q normalized", input)
		} else if strings.Contains(err.Error(), "CANARY") {
			t.Errorf("locator diagnostic leaked credential material: %v", err)
		}
	}
}

func TestSourceValidationStateAndCanonicalForms(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	active := validSourceRecord(t, now)
	if err := Validate(active); err != nil {
		t.Fatalf("valid active Source: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Source)
		code   string
	}{
		{"noncanonical key", func(value *Source) { value.Key = "Production_Workspace" }, "source.key"},
		{"unsupported kind", func(value *Source) { value.Kind = "filesystem" }, "source.kind"},
		{"noncanonical subdir", func(value *Source) { value.Subdir = "./services/api" }, "source.subdir_normalized"},
		{"local subdir", func(value *Source) { value.Subdir = "/private/repo" }, "path.not_relative"},
		{"missing locator array", func(value *Source) { value.LocatorHints = nil }, "record.list_required"},
		{"duplicate locator", func(value *Source) { value.LocatorHints = append(value.LocatorHints, value.LocatorHints[0]) }, "record.set_duplicate"},
		{"forbidden locator component", func(value *Source) { value.LocatorHints[0] = "https://git@example.invalid/org/repo.git?branch=main" }, "source.locator_components"},
		{"active retired_at", func(value *Source) { value.RetiredAt = timePtr(now) }, "source.retired_at"},
		{"retired missing timestamp", func(value *Source) { value.State = SourceRetired }, "source.retired_at"},
		{"invalid state", func(value *Source) { value.State = "disabled" }, "source.state"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := Clone(active).(*Source)
			test.mutate(candidate)
			if err := Validate(candidate); !sourceIssue(err, test.code) {
				t.Fatalf("validation error = %v, want %s", err, test.code)
			}
		})
	}

	retired := Clone(active).(*Source)
	retired.State = SourceRetired
	retired.UpdatedAt = now.Add(time.Hour)
	retired.RetiredAt = timePtr(retired.UpdatedAt)
	if err := Validate(retired); err != nil {
		t.Fatalf("valid retired Source: %v", err)
	}
	before := Clone(retired).(*Source)
	Normalize(retired)
	if !retired.RetiredAt.Equal(before.RetiredAt.UTC()) {
		t.Fatalf("retired_at was not normalized to UTC: %s", retired.RetiredAt)
	}
}

func TestSourceCloneDeepCopiesMutableValues(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	source := validSourceRecord(t, now)
	source.State = SourceRetired
	source.RetiredAt = timePtr(now)
	source.Extensions = Extensions{"org.example.source": {"nested": map[string]any{"value": "original"}}}
	cloned := Clone(source).(*Source)
	cloned.LocatorHints[0] = "https://example.invalid/other/repo.git"
	*cloned.RetiredAt = cloned.RetiredAt.Add(time.Hour)
	cloned.Extensions["org.example.source"]["nested"].(map[string]any)["value"] = "changed"
	if source.LocatorHints[0] == cloned.LocatorHints[0] || source.RetiredAt.Equal(*cloned.RetiredAt) || source.Extensions["org.example.source"]["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("Source clone shares mutable state: source=%#v clone=%#v", source, cloned)
	}
}

func validSourceRecord(t *testing.T, now time.Time) *Source {
	t.Helper()
	id, err := ParseIDForKind("src_01a03000-0000-7001-8000-000000000001", KindSource)
	if err != nil {
		t.Fatal(err)
	}
	return &Source{
		Common: Common{Schema: SchemaSource, ID: id, Title: "Production workspace", CreatedAt: now, UpdatedAt: now},
		Key:    "production-workspace", Kind: SourceGit, Subdir: ".",
		LocatorHints: []string{"https://github.com/example/repo.git"}, State: SourceActive,
	}
}

func sourceIssue(err error, code string) bool {
	for _, issue := range IssuesFromError(err) {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func timePtr(value time.Time) *time.Time { return &value }
