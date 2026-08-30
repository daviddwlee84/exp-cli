package harnessv0

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestPlanIsLosslessDeterministicAndRequiresExplicitReview(t *testing.T) {
	repository, _ := writeLegacyFixture(t)
	at := time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)
	first, err := BuildPlan(t.Context(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan(t.Context(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash != second.ContentHash || first.ProjectID != second.ProjectID {
		t.Fatalf("migration plan is not deterministic: %#v %#v", first, second)
	}
	if first.Applicable {
		t.Fatal("unresolved Plan/Inbox review unexpectedly applicable")
	}
	if len(first.UnknownSpans) == 0 {
		t.Fatal("expected exact unknown spans for legacy prose")
	}
	for _, file := range first.SourceFiles {
		var cursor int64
		for _, span := range file.Spans {
			if span.StartByte != cursor {
				t.Fatalf("%s span gap at %d", file.Path, cursor)
			}
			cursor = span.EndByte
		}
		if cursor != file.Bytes {
			t.Fatalf("%s spans cover %d of %d bytes", file.Path, cursor, file.Bytes)
		}
	}
	plan := rebuildWithResolutions(t, repository, at, first)
	if !plan.Applicable {
		t.Fatalf("explicitly resolved plan is not applicable: %#v", plan.Diagnostics)
	}
	if len(plan.CandidateFiles) < 7 {
		t.Fatalf("candidate file count = %d, want Project + records + projections", len(plan.CandidateFiles))
	}
	for _, candidate := range plan.CandidateFiles {
		if candidate.Path != record.ProjectFile {
			continue
		}
		content, decodeErr := base64.StdEncoding.DecodeString(candidate.ContentBase64)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if _, decodeErr = record.Decode(content); decodeErr == nil {
			t.Fatal("ordinary Decode authorized migration UUIDv5 bytes")
		}
		if _, decodeErr = record.DecodeImported(content); decodeErr != nil {
			t.Fatalf("privileged migration decode failed: %v", decodeErr)
		}
	}
	encoded, err := EncodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(bytes.NewReader(encoded))
	if err != nil || decoded.ContentHash != plan.ContentHash {
		t.Fatalf("round trip: plan=%v err=%v", decoded, err)
	}
}

func TestPlanHashDoesNotAuthorizeInjectedCandidate(t *testing.T) {
	repository, _ := writeLegacyFixture(t)
	at := time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)
	draft, err := BuildPlan(t.Context(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	plan := rebuildWithResolutions(t, repository, at, draft)
	plan.CandidateFiles[0].Path = "findings/injected.md"
	plan.ContentHash, err = ComputePlanHash(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlan(plan); err == nil {
		t.Fatal("self-hashed injected candidate was accepted")
	}
}

func TestPlanReportsRequiredHarnessAmbiguitiesWithoutRepair(t *testing.T) {
	repository, _ := writeLegacyFixture(t)
	root := filepath.Join(repository, "experiments")
	reportPath := filepath.Join(root, "001-alpha", "REPORT.md")
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	report = bytes.Replace(report, []byte("status: planned"), []byte("status: concluded-success"), 1)
	report = bytes.Replace(report, []byte("axis: validation window"), []byte("axis: validation window + seed"), 1)
	report = bytes.Replace(report, []byte("concluded:"), []byte("concluded: 2026-08-30"), 1)
	report = bytes.Replace(report, []byte("conclusion:"), []byte("conclusion: Supported."), 1)
	report = bytes.Replace(report, []byte("findings: [F-001]"), []byte("findings: [F-002]"), 1)
	report = append(report, []byte("\n## Conclusion\n\nSupported; distilled F-003.\n")...)
	if err := os.WriteFile(reportPath, report, 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := "# Findings Ledger\n\n## Active\n\n- **F-001** [2026-08-29] (#001) Alpha remains stable. → [evidence](missing/REPORT.md)\n\n## Overturned\n"
	if err := os.WriteFile(filepath.Join(root, "LEDGER.md"), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	roadmap := "# Experiment Roadmap\n\n## P1\n\n- [ ] **[S] Alpha stability** — already reported (payoff: decide; cat: research)\n"
	if err := os.WriteFile(filepath.Join(root, "ROADMAP.md"), []byte(roadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "EXECUTIVE-SUMMARY.md"), []byte("# Curated snapshot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "AGENTS.md"), []byte("Use experiment-knowledge-harness guidance.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(t.Context(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"finding.evidence_link": false, "relationship.report_ledger_drift": false,
		"report.finding_mismatch": false, "experiment.primary_factor": false,
		"report.provenance": false, "report.mlflow_absent": false,
		"source.curated_view": false, "source.skill_guidance": false,
		"roadmap.queued_completed_drift": false,
	}
	for _, diagnostic := range plan.Diagnostics {
		if _, found := wanted[diagnostic.Code]; found {
			wanted[diagnostic.Code] = true
		}
	}
	for code, found := range wanted {
		if !found {
			t.Errorf("missing ambiguity diagnostic %s: %#v", code, plan.Diagnostics)
		}
	}
}

func TestApplyPublishesArchiveAndCanonicalInventoryIdempotently(t *testing.T) {
	repository, common := writeLegacyFixture(t)
	at := time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)
	draft, err := BuildPlan(t.Context(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	plan := rebuildWithResolutions(t, repository, at, draft)
	legacyRoadmap, err := os.ReadFile(filepath.Join(repository, "experiments", "ROADMAP.md"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(t.Context(), ApplyRequest{RepositoryRoot: repository, GitCommonDir: common, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.AlreadyApplied {
		t.Fatalf("first apply = %#v", result)
	}
	root := filepath.Join(repository, "experiments")
	inventory, err := record.LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Valid() {
		t.Fatalf("migrated inventory invalid: %#v", inventory.Diagnostics)
	}
	findings := inventory.OfKind(research.KindFinding)
	if len(findings) != 1 {
		t.Fatalf("migrated findings = %d", len(findings))
	}
	findingID, _ := findings[0].ID()
	store := record.NewStore(root, common, record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
		return uuid.MustParse("01a01f00-0000-7001-8000-000000000001"), nil
	}))
	if _, err := store.CreatePlan(t.Context(), record.PlanInput{
		Title: "Follow imported evidence", Priority: research.PriorityP2, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Confirm imported evidence remains actionable", Metric: "confidence", Unit: "score"},
		Assumptions:    []research.ID{findingID}, Body: "\n# Follow imported evidence\n",
	}); err != nil {
		t.Fatalf("native Plan could not reference authenticated imported Finding: %v", err)
	}
	archived, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(plan.Archive.Path), "source", "ROADMAP.md"))
	if err != nil || !bytes.Equal(archived, legacyRoadmap) {
		t.Fatalf("archive mismatch: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "001-alpha", "runs.csv")); err != nil {
		t.Fatalf("non-source legacy artifact was not preserved: %v", err)
	}
	again, err := Apply(t.Context(), ApplyRequest{RepositoryRoot: repository, GitCommonDir: common, Plan: plan})
	if err != nil || !again.AlreadyApplied || again.Applied {
		t.Fatalf("idempotent apply = %#v err=%v", again, err)
	}
}

func TestApplyRecoversAfterSourceRename(t *testing.T) {
	repository, common := writeLegacyFixture(t)
	at := time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)
	draft, err := BuildPlan(t.Context(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	plan := rebuildWithResolutions(t, repository, at, draft)
	injected := errors.New("stop after source preservation")
	_, err = Apply(t.Context(), ApplyRequest{RepositoryRoot: repository, GitCommonDir: common, Plan: plan, Hook: func(stage ApplyStage) error {
		if stage == StageSourcePreserved {
			return injected
		}
		return nil
	}})
	if !errors.Is(err, injected) {
		t.Fatalf("injected apply error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repository, "experiments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source should be between prepared renames, got %v", err)
	}
	result, err := Apply(t.Context(), ApplyRequest{RepositoryRoot: repository, GitCommonDir: common, Plan: plan})
	if err != nil || !result.Applied {
		t.Fatalf("recovery apply = %#v err=%v", result, err)
	}
	if inventory, err := record.LoadInventory(filepath.Join(repository, "experiments")); err != nil || !inventory.Valid() {
		t.Fatalf("recovered inventory invalid: %#v err=%v", inventory, err)
	}
}

func TestApplyRefusesChangedSourceAndSymlink(t *testing.T) {
	repository, common := writeLegacyFixture(t)
	at := time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)
	draft, err := BuildPlan(t.Context(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	plan := rebuildWithResolutions(t, repository, at, draft)
	roadmap := filepath.Join(repository, "experiments", "ROADMAP.md")
	if err := os.WriteFile(roadmap, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(t.Context(), ApplyRequest{RepositoryRoot: repository, GitCommonDir: common, Plan: plan}); err == nil || !strings.Contains(err.Error(), "fingerprint changed") {
		t.Fatalf("changed source apply error = %v", err)
	}

	repository, _ = writeLegacyFixture(t)
	if err := os.Symlink("ROADMAP.md", filepath.Join(repository, "experiments", "alias.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPlan(context.Background(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: at}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink plan error = %v", err)
	}
}

func rebuildWithResolutions(t *testing.T, repository string, at time.Time, draft *Plan) *Plan {
	t.Helper()
	seen := map[string]bool{}
	var resolutions []Resolution
	for _, diagnostic := range draft.Diagnostics {
		if diagnostic.State != "needs_review" || diagnostic.Key == "" || seen[diagnostic.Key] {
			continue
		}
		seen[diagnostic.Key] = true
		action := "archive_only"
		if strings.HasPrefix(diagnostic.Key, "plan:") {
			action = "migrate"
		}
		resolutions = append(resolutions, Resolution{Key: diagnostic.Key, Action: action, Note: "reviewed in test"})
	}
	plan, err := BuildPlan(t.Context(), BuildRequest{RepositoryRoot: repository, SourceRoot: "experiments", GeneratedAt: at, Resolutions: ResolutionSet{SchemaVersion: ResolutionSchema, Resolutions: resolutions}})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func writeLegacyFixture(t *testing.T) (string, string) {
	t.Helper()
	repository := t.TempDir()
	git := exec.Command("git", "init", "--quiet")
	git.Dir = repository
	if output, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	common := filepath.Join(repository, ".git")
	root := filepath.Join(repository, "experiments")
	for _, directory := range []string{filepath.Join(root, "001-alpha")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"README.md":           "# Example Research\n\nCurated summary.\n",
		"ROADMAP.md":          "# Experiment Roadmap\n\n## P1\n\n- [ ] **[S] Verify alpha stability** — rerun split (payoff: decides go/no-go; cat: research; depends-on: F-001)\n\n## Done\n",
		"LEDGER.md":           "# Findings Ledger\n\n## Active\n\n- **F-001** [2026-08-29] (#001) Alpha remains stable on the held-out split. → [evidence](001-alpha/REPORT.md)\n\n## Overturned\n",
		"INBOX.md":            "# Inbox\n\n- Maybe compare a second data window.\n",
		"001-alpha/REPORT.md": "---\nid: 001\nslug: alpha\ntitle: Alpha stability\nstatus: planned\nquestion: Does alpha remain stable on the held-out split?\naxis: validation window\nbaseline: baseline-v1\nspec: heldout-v1\nstarted: 2026-08-29\nconcluded:\nconclusion:\nfindings: [F-001]\ntags: [alpha]\nrefs: []\nmlflow: \"\"\n---\n\n# #001 Alpha stability\n\n## Pre-registration\n\n- **Hypothesis**: Alpha remains positive on the held-out split.\n- **Success criteria**: Net alpha exceeds 1 bp/day.\n- **Decision rule**: if alpha exceeds the bar, continue; otherwise stop.\n",
		"001-alpha/runs.csv":  "run,alpha\na,1.2\n",
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
	return repository, common
}
