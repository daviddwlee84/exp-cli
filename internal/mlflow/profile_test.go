package mlflow

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
)

func TestResolveProfileHonorsExplicitPrecedenceAndExactTrust(t *testing.T) {
	result := profileConfigResult()
	selected, err := ResolveProfile(result, "")
	if err != nil || selected == nil || selected.Name != "leaf" || selected.Origin != ProfileSubdir {
		t.Fatalf("default profile = %#v, err=%v", selected, err)
	}
	explicit, err := ResolveProfile(result, "user")
	if err != nil || explicit == nil || explicit.Name != "user" || explicit.Origin != ProfileExplicit {
		t.Fatalf("explicit profile = %#v, err=%v", explicit, err)
	}

	untrustedSelector := profileConfigResult()
	selector := untrustedSelector.Provenance["defaults.mlflow_profile"]
	selector.Trusted = false
	untrustedSelector.Provenance["defaults.mlflow_profile"] = selector
	if _, err := ResolveProfile(untrustedSelector, ""); !errors.Is(err, config.ErrUntrusted) {
		t.Fatalf("untrusted selector error = %v", err)
	}
	if explicit, err := ResolveProfile(untrustedSelector, "user"); err != nil || explicit == nil {
		t.Fatalf("explicit selection incorrectly depended on default selector trust: %#v, %v", explicit, err)
	}

	untrustedProfile := profileConfigResult()
	definition := untrustedProfile.Provenance["mlflow.profiles.leaf"]
	definition.Trusted = false
	untrustedProfile.Provenance["mlflow.profiles.leaf"] = definition
	if _, err := ResolveProfile(untrustedProfile, ""); !errors.Is(err, config.ErrUntrusted) {
		t.Fatalf("untrusted profile error = %v", err)
	}
}

func TestResolveProfileAllowsOptionalAbsenceAndRejectsMixedCompatibility(t *testing.T) {
	result := profileConfigResult()
	result.Effective.Defaults.MLflowProfile = ""
	if selected, err := ResolveProfile(result, ""); err != nil || selected != nil {
		t.Fatalf("optional profile = %#v, err=%v", selected, err)
	}
	if _, err := ResolveInvocation(result, InvocationOptions{Profile: "user", Context: "other", ContextSet: true}); !errors.Is(err, ErrAmbiguousSelection) {
		t.Fatalf("mixed context/profile error = %v", err)
	}
	if _, err := ResolveInvocation(result, InvocationOptions{Profile: "user", AllowEnv: []string{"MLFLOW_TRACKING_URI"}}); !errors.Is(err, ErrAmbiguousSelection) {
		t.Fatalf("mixed env/profile error = %v", err)
	}
	legacy, err := ResolveInvocation(result, InvocationOptions{Context: "legacy", ContextSet: true, SecretEnv: []string{"MLFLOW_TOKEN"}})
	if err != nil || legacy.Name != "compatibility" || legacy.Context != "legacy" || legacy.Origin != ProfileCompatibility || len(legacy.Environment) != 1 {
		t.Fatalf("compatibility profile = %#v, err=%v", legacy, err)
	}
}

func TestProfileEnvironmentBindsChildFromParentAndRedactsEveryValue(t *testing.T) {
	profile := WorkloadProfile{
		Name: "secure", Context: "training", Binary: "mlflow", Timeout: "2s",
		Environment: []EnvironmentBinding{
			{Child: "MLFLOW_TRACKING_TOKEN", From: "PARENT_MLFLOW_TOKEN", Secret: true, Required: true},
			{Child: "MLFLOW_TRACKING_URI", From: "PARENT_MLFLOW_URI", Required: false},
		},
		DefaultMetrics: []string{"accuracy"},
	}
	environment, err := profile.EnvironmentPolicy()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(environment)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(metadata)
	if strings.Contains(serialized, "PARENT_MLFLOW_TOKEN") || strings.Contains(serialized, "PARENT_MLFLOW_URI") || !strings.Contains(serialized, "MLFLOW_TRACKING_TOKEN") {
		t.Fatalf("environment metadata exposed a parent name or omitted child metadata: %s", serialized)
	}
	const token = "PROFILE_SECRET_CANARY_9f1e"
	const endpoint = "https://tracking.example.invalid/private-context"
	redactor, err := environment.Redactor(func(name string) (string, bool) {
		switch name {
		case "PARENT_MLFLOW_TOKEN":
			return token, true
		case "PARENT_MLFLOW_URI":
			return endpoint, true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	redacted := redactor.Text("token=" + token + " endpoint=" + endpoint)
	if strings.Contains(redacted, token) || strings.Contains(redacted, endpoint) {
		t.Fatalf("resolved profile values survived redaction: %q", redacted)
	}
	if _, err := environment.Redactor(func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("missing required parent environment was accepted")
	}
}

func TestAttachmentExternalRefRequiresExactAttemptOwnership(t *testing.T) {
	now := time.Date(2026, 9, 4, 6, 0, 0, 0, time.UTC)
	attempt := "att_01a09000-0000-7202-8000-000000000202"
	attachment := Attachment{
		Profile: "secure", Context: "training", RunID: "run-123", ExperimentID: "7", Status: "FINISHED",
		Metrics: map[string]float64{"accuracy": 0.9}, Tags: map[string]string{AttemptOwnershipTag: attempt},
		ObservedAt: now, State: ObservationVerified, Reasons: []string{},
	}
	reference, err := attachment.ExternalRef(attempt)
	if err != nil || reference.NativeID != "run-123" || reference.Context != "training" || reference.Metadata["mlflow.owner_attempt"] != attempt {
		t.Fatalf("external reference = %#v, err=%v", reference, err)
	}
	if _, err := attachment.ExternalRef("att_01a09000-0000-7202-8000-000000000999"); err == nil {
		t.Fatal("mismatched Attempt ownership was accepted")
	}
	attachment.Tags["output_dir"] = "/Users/alice/private-run"
	attachment.Tags["windows_output"] = `C:\\Users\\alice\\private-run`
	reference, err = attachment.ExternalRef(attempt)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	if _, retained := reference.Metadata["mlflow.tags"]; retained || strings.Contains(string(encoded), "/Users/alice") || strings.Contains(string(encoded), `C:\\Users\\alice`) {
		t.Fatalf("assertion tags leaked into canonical metadata: %s", encoded)
	}
}

func profileConfigResult() *config.Result {
	profile := func(contextName string) config.MLflowProfile {
		return config.MLflowProfile{
			Context: contextName, Binary: "mlflow", Timeout: 2 * time.Second,
			Env: map[string]config.EnvBinding{}, DefaultMetrics: []string{"accuracy"},
		}
	}
	return &config.Result{
		Effective: config.Effective{
			Defaults: config.Defaults{MLflowProfile: "leaf"},
			MLflow: config.MLflow{Profiles: map[string]config.MLflowProfile{
				"leaf": profile("leaf-context"), "user": profile("user-context"),
			}},
		},
		Provenance: map[string]config.Provenance{
			"defaults.mlflow_profile": {Layer: config.LayerSubdir, Trusted: true},
			"mlflow.profiles.leaf":    {Layer: config.LayerSubdir, Trusted: true},
			"mlflow.profiles.user":    {Layer: config.LayerUser, Trusted: true},
		},
	}
}
