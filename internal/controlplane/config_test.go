package controlplane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestRuntimeConfigIsStrictProjectLocalAndSecretReferenceOnly(t *testing.T) {
	repository := canonicalTemp(t)
	if err := os.MkdirAll(filepath.Join(repository, ".exp"), 0o700); err != nil {
		t.Fatal(err)
	}
	poolID := "pool_01a01e60-0000-7002-8000-000000000002"
	planID := "plan_01a01e60-0000-7003-8000-000000000003"
	valid := `{
  "schema_version":"exp.runtime/v1",
  "pools":{"` + poolID + `":{"pueue_group":"gpu","label_prefix":"study-"}},
  "plans":{"` + planID + `":{
    "executable":"/bin/echo","argv":["train"],"cwd":".",
    "allowed_env":["PATH"],"secret_env":[],
    "base_commit":"` + strings.Repeat("1", 40) + `","head_commit":"` + strings.Repeat("2", 40) + `",
    "change_set":["train.py"],"expected_outputs":[]
  }}
}`
	configPath := filepath.Join(repository, filepath.FromSlash(DefaultConfigPath))
	if err := os.WriteFile(configPath, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadRuntime(t.Context(), repository, DefaultConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.plans) != 1 || len(loaded.pools) != 1 {
		t.Fatalf("loaded runtime = %#v", loaded)
	}
	secretEnvironment := strings.Replace(valid, `"secret_env":[]`, `"secret_env":["TRACKER_TOKEN"]`, 1)
	sensitiveAllowed := strings.Replace(valid, `"allowed_env":["PATH"]`, `"allowed_env":["OPENAI_API_KEY"]`, 1)
	if err := os.WriteFile(configPath, []byte(sensitiveAllowed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntime(t.Context(), repository, DefaultConfigPath); err == nil || !strings.Contains(err.Error(), "credential-sensitive") {
		t.Fatalf("sensitive allowed_env error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(secretEnvironment), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntime(t.Context(), repository, DefaultConfigPath); err == nil || !strings.Contains(err.Error(), "scheduler persists") {
		t.Fatalf("secret Pueue environment error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}

	const canary = "runtime-secret-value-must-not-surface"
	unknown := `{"schema_version":"exp.runtime/v1","pools":{},"plans":{},"credential":"` + canary + `"}`
	if err := os.WriteFile(configPath, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = loadRuntime(t.Context(), repository, DefaultConfigPath)
	if err == nil || !strings.Contains(err.Error(), "unknown field") || strings.Contains(err.Error(), canary) {
		t.Fatalf("strict runtime config error = %v", err)
	}
	credentialArg := strings.Replace(valid, `"train"`, `"api_key=`+canary+`"`, 1)
	if err := os.WriteFile(configPath, []byte(credentialArg), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = loadRuntime(t.Context(), repository, DefaultConfigPath)
	if err == nil || !strings.Contains(err.Error(), "credential-bearing") || strings.Contains(err.Error(), canary) {
		t.Fatalf("secret-bearing runtime argv error = %v", err)
	}

	target := filepath.Join(repository, "runtime-target.json")
	if err := os.WriteFile(target, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := loadRuntime(t.Context(), repository, DefaultConfigPath); err == nil {
		t.Fatal("runtime config symlink was accepted")
	}
}

func TestRuntimeConfigRejectsCustomConfigPathInPlanChangeSet(t *testing.T) {
	repository := canonicalTemp(t)
	customConfigPath := "config/daemon-runtime.json"
	if err := os.MkdirAll(filepath.Join(repository, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	poolID := "pool_01a01e60-0000-7002-8000-000000000002"
	planID := "plan_01a01e60-0000-7003-8000-000000000003"
	config := `{
  "schema_version":"exp.runtime/v1",
  "pools":{"` + poolID + `":{"pueue_group":"gpu","label_prefix":"study-"}},
  "plans":{"` + planID + `":{
    "executable":"/bin/echo","argv":["train"],"cwd":".",
    "allowed_env":["PATH"],"secret_env":[],
    "base_commit":"` + strings.Repeat("1", 40) + `","head_commit":"` + strings.Repeat("2", 40) + `",
    "change_set":["train.py"],"expected_outputs":[]
  }}
}`
	absoluteConfigPath := filepath.Join(repository, filepath.FromSlash(customConfigPath))
	if err := os.WriteFile(absoluteConfigPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadRuntime(t.Context(), repository, customConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.configPath != customConfigPath {
		t.Fatalf("loaded config path = %q, want %q", loaded.configPath, customConfigPath)
	}
	plan, err := research.ParseIDForKind(planID, research.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.plans[plan].runtimeConfigPath != customConfigPath {
		t.Fatalf("plan runtime config path = %q, want %q", loaded.plans[plan].runtimeConfigPath, customConfigPath)
	}

	config = strings.Replace(config, `"change_set":["train.py"]`, `"change_set":["`+customConfigPath+`"]`, 1)
	if err := os.WriteFile(absoluteConfigPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntime(t.Context(), repository, customConfigPath); err == nil || !strings.Contains(err.Error(), "control metadata") {
		t.Fatalf("custom config change_set error = %v", err)
	}
}

func TestRuntimeConfigRequiresPrefixFreePoolRoutes(t *testing.T) {
	repository := canonicalTemp(t)
	if err := os.MkdirAll(filepath.Join(repository, ".exp"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{
  "schema_version":"exp.runtime/v1",
  "pools":{
    "pool_01a01e60-0000-7002-8000-000000000002":{"pueue_group":"gpu","label_prefix":"exp-"},
    "pool_01a01e60-0000-7002-8000-000000000003":{"pueue_group":"gpu","label_prefix":"exp-gpu-"}
  },
  "plans":{}
}`
	configPath := filepath.Join(repository, filepath.FromSlash(DefaultConfigPath))
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntime(t.Context(), repository, DefaultConfigPath); err == nil || !strings.Contains(err.Error(), "overlapping label prefixes") {
		t.Fatalf("overlapping route error = %v", err)
	}
}

func TestRuntimeConfigReservesDispatchIDSpaceInLabelPrefix(t *testing.T) {
	repository := canonicalTemp(t)
	if err := os.MkdirAll(filepath.Join(repository, ".exp"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(repository, filepath.FromSlash(DefaultConfigPath))
	config := func(prefix string) string {
		return `{
  "schema_version":"exp.runtime/v1",
  "pools":{"pool_01a01e60-0000-7002-8000-000000000002":{"pueue_group":"gpu","label_prefix":"` + prefix + `"}},
  "plans":{}
}`
	}
	boundary := strings.Repeat("a", maxPueueLabelPrefixBytes)
	if err := os.WriteFile(configPath, []byte(config(boundary)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntime(t.Context(), repository, DefaultConfigPath); err != nil {
		t.Fatalf("boundary label prefix rejected: %v", err)
	}
	if !validToken(boundary + strings.Repeat("d", generatedDispatchIDBytes)) {
		t.Fatal("boundary prefix plus dispatch ID must fit the Pueue label token")
	}

	tooLong := boundary + "a"
	if err := os.WriteFile(configPath, []byte(config(tooLong)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntime(t.Context(), repository, DefaultConfigPath); err == nil || !strings.Contains(err.Error(), "too long to append a dispatch ID") {
		t.Fatalf("oversized label prefix error = %v", err)
	}
}
