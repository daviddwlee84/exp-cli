package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/harnessv0"
	"github.com/daviddwlee84/exp-cli/internal/record"
)

func TestMigratePlanAndApplyCommands(t *testing.T) {
	repository := writeCLILegacyFixture(t)
	app := NewApp(t.Context(), nil, nil, nil)
	app.Getwd = func() (string, error) { return repository, nil }
	app.Now = func() time.Time { return time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC) }
	draftPath := filepath.Join(repository, "draft-plan.json")
	draftInvocation := invokeCommand(t, app, "", "migrate", "plan", "--output", draftPath, "--json")
	requireCommandSuccess(t, draftInvocation)
	draftFile, err := os.Open(draftPath)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := harnessv0.DecodePlan(draftFile)
	_ = draftFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if draft.Applicable {
		t.Fatal("unreviewed legacy payoff unexpectedly applicable")
	}
	var resolutions []harnessv0.Resolution
	seen := map[string]bool{}
	for _, diagnostic := range draft.Diagnostics {
		if diagnostic.State == "needs_review" && diagnostic.Key != "" && !seen[diagnostic.Key] {
			seen[diagnostic.Key] = true
			resolutions = append(resolutions, harnessv0.Resolution{Key: diagnostic.Key, Action: "migrate", Note: "reviewed by CLI test"})
		}
	}
	resolutionPath := filepath.Join(repository, "resolutions.json")
	writeJSONTestFile(t, resolutionPath, harnessv0.ResolutionSet{SchemaVersion: harnessv0.ResolutionSchema, Resolutions: resolutions})
	planPath := filepath.Join(repository, "migration-plan.json")
	resolved := invokeCommand(t, app, "", "migrate", "plan", "--resolutions", resolutionPath, "--output", planPath, "--json")
	requireCommandSuccess(t, resolved)
	apply := invokeCommand(t, app, "", "migrate", "apply", "--plan", planPath, "--json")
	requireCommandSuccess(t, apply)
	validate := invokeCommand(t, app, "", "validate", "--json")
	requireCommandSuccess(t, validate)
	inventory, err := record.LoadInventory(filepath.Join(repository, "experiments"))
	if err != nil || !inventory.Valid() {
		t.Fatalf("migrated CLI inventory invalid: %#v err=%v", inventory, err)
	}
	again := invokeCommand(t, app, "", "migrate", "apply", "--plan", planPath, "--json")
	requireCommandSuccess(t, again)
	if !strings.Contains(again.stdout, `"already_applied":true`) {
		t.Fatalf("idempotent apply output = %s", again.stdout)
	}
}

func writeCLILegacyFixture(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	command := exec.Command("git", "init", "--quiet")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	root := filepath.Join(repository, "experiments")
	if err := os.MkdirAll(filepath.Join(root, "001-alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"README.md":           "# CLI migration fixture\n",
		"ROADMAP.md":          "# Roadmap\n\n## P1\n\n- [ ] **[S] Validate alpha** — held-out check (payoff: go/no-go; cat: research)\n",
		"001-alpha/REPORT.md": "---\nid: 001\nslug: alpha\ntitle: Alpha validation\nstatus: planned\nquestion: Does alpha survive validation?\naxis: validation window\nbaseline: baseline-v1\nspec: heldout-v1\nstarted: 2026-08-29\nconcluded:\nconclusion:\nfindings: []\ntags: [alpha]\nrefs: []\nmlflow: \"\"\n---\n\n- **Hypothesis**: Alpha remains positive.\n- **Success criteria**: Alpha exceeds 1 bp/day.\n- **Decision rule**: continue if positive; otherwise stop.\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repository
}

func writeJSONTestFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
