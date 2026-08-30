package controlplane

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

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
