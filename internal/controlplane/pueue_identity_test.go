package controlplane

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestScopeIDRemainsCheckoutLocalAcrossLinkedWorktrees(t *testing.T) {
	repository := canonicalTemp(t)
	runGit(t, repository, "init", "--quiet")
	runGit(t, repository, "config", "user.name", "Scope Test")
	runGit(t, repository, "config", "user.email", "scope@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "tracked"), []byte("scope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "tracked")
	runGit(t, repository, "commit", "--quiet", "-m", "scope")
	linked := filepath.Join(canonicalTemp(t), "linked")
	runGit(t, repository, "worktree", "add", "--detach", linked, "HEAD")
	mainScope, err := ScopeID(repository)
	if err != nil {
		t.Fatal(err)
	}
	linkedScope, err := ScopeID(linked)
	if err != nil {
		t.Fatal(err)
	}
	if mainScope == linkedScope || mainScope == "" || linkedScope == "" {
		t.Fatalf("checkout scopes collided: main=%s linked=%s", mainScope, linkedScope)
	}
}

func TestResolvePueueTaskIdentityAcceptsFormalAttemptV3RuntimeV2(t *testing.T) {
	repository := canonicalTemp(t)
	if err := os.Mkdir(filepath.Join(repository, ".exp"), 0o755); err != nil {
		t.Fatal(err)
	}
	poolID, err := research.ParseID("pool_01a01e61-0000-7021-8000-000000000021")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := research.ParseID("run_01a01e61-0000-7022-8000-000000000022")
	if err != nil {
		t.Fatal(err)
	}
	config := `{"schema_version":"exp.runtime/v2","pools":{"` + poolID.String() + `":{"pueue_group":"gpu","label_prefix":"study-"}},"plans":{}}`
	if err := os.WriteFile(filepath.Join(repository, DefaultConfigPath), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	attempt := &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV3}, Run: runID,
		Scheduler: "pueue", Pool: poolID, DispatchID: "dispatch-v3",
		Extensions: research.Extensions{attemptExtension: {"pueue_group": "gpu", "pueue_label": "study-dispatch-v3"}},
	}
	identity, err := ResolvePueueTaskIdentity(t.Context(), repository, DefaultConfigPath, attempt)
	if err != nil || identity.Group != "gpu" || identity.Label != "study-dispatch-v3" {
		t.Fatalf("v3 Pueue identity=%#v err=%v", identity, err)
	}
}

func TestResolvePueueTaskIdentityRequiresRuntimeAndCapturedRouteAgreement(t *testing.T) {
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".exp"), 0o755); err != nil {
		t.Fatal(err)
	}
	poolID, err := research.ParseID("pool_01a01e61-0000-7021-8000-000000000021")
	if err != nil {
		t.Fatal(err)
	}
	config := `{"schema_version":"exp.runtime/v1","pools":{"` + poolID.String() + `":{"pueue_group":"gpu","label_prefix":"study-"}},"plans":{}}`
	if err := os.WriteFile(filepath.Join(repository, DefaultConfigPath), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	attempt := &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV2}, Scheduler: "pueue", Pool: poolID, DispatchID: "dispatch-1",
		Extensions: research.Extensions{attemptExtension: {"pueue_group": "gpu", "pueue_label": "study-dispatch-1"}},
	}
	identity, err := ResolvePueueTaskIdentity(t.Context(), repository, DefaultConfigPath, attempt)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Context != LocalPueueContext || identity.Group != "gpu" || identity.Label != "study-dispatch-1" {
		t.Fatalf("identity = %#v", identity)
	}
	attempt.Extensions[attemptExtension]["pueue_label"] = "other"
	if _, err := ResolvePueueTaskIdentity(t.Context(), repository, DefaultConfigPath, attempt); err == nil {
		t.Fatal("captured route drift was accepted")
	}
	attempt.Extensions[attemptExtension]["pueue_label"] = "study-dispatch-1"
	attempt.Scheduler = "direct"
	if _, err := ResolvePueueTaskIdentity(t.Context(), repository, DefaultConfigPath, attempt); err == nil {
		t.Fatal("non-Pueue Attempt was accepted")
	}
}
