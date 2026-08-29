package provider_test

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/provider"
)

func TestURISanitizationAndCanonicalValidation(t *testing.T) {
	const canary = "provider-uri-canary-8a31"
	policy, err := provider.NewRedactionPolicy(provider.DefaultMaxTextBytes, provider.DefaultMaxRawBytes, canary)
	if err != nil {
		t.Fatal(err)
	}
	raw := "https://user:" + canary + "@mlflow.example.invalid/api/" + canary + "?token=" + url.QueryEscape(canary) + "&view=active#route?access_token=" + url.QueryEscape(canary) + "&tab=metrics"
	safe, err := provider.SanitizeURIWithPolicy(raw, policy)
	if err != nil {
		t.Fatalf("SanitizeURIWithPolicy() error = %v", err)
	}
	if strings.Contains(safe, canary) || strings.Contains(safe, "user:") || strings.Contains(safe, "token=") || strings.Contains(safe, "access_token=") {
		t.Fatalf("sanitized URI leaked credentials: %q", safe)
	}
	parsedSafe, parseErr := url.Parse(safe)
	if parseErr != nil || !strings.Contains(safe, "view=active") || !strings.Contains(safe, "tab=metrics") || !strings.Contains(parsedSafe.Path, execx.Redacted) {
		t.Fatalf("sanitized URI lost safe structure or redaction marker: %q", safe)
	}

	validCanonical := "https://mlflow.example.invalid/#/experiments/7/runs/00000000000000000000000000000001"
	if err := provider.ValidateCanonicalURI(validCanonical); err != nil {
		t.Fatalf("safe routing fragment rejected: %v", err)
	}
	for _, unsafe := range []string{
		"https://user:password@example.invalid/run/1",
		"https://example.invalid/run/1?view=active",
		"https://example.invalid/#access_token=secret&view=active",
		"file:///tmp/artifact",
		"relative/path",
		" https://example.invalid/run/1",
		"https://example.invalid/" + execx.Redacted,
		"https://example.invalid/token=secret",
		"https://example.invalid/%0A",
	} {
		if err := provider.ValidateCanonicalURI(unsafe); err == nil {
			t.Errorf("ValidateCanonicalURI(%q) succeeded", unsafe)
		}
	}
}

func TestURIComponentsRecursivelyDecodeBeforeCredentialClassification(t *testing.T) {
	const canary = "nested-uri-secret-canary-41d2"
	for name, raw := range map[string]string{
		"path":     "https://example.invalid/access_token%253D" + canary,
		"opaque":   "urn:access_token%253D" + canary,
		"fragment": "https://example.invalid/#access_token%253D" + canary,
	} {
		t.Run(name, func(t *testing.T) {
			safe, err := provider.SanitizeURI(raw)
			if err != nil {
				t.Fatalf("SanitizeURI() error = %v", err)
			}
			if strings.Contains(safe, canary) {
				t.Fatalf("SanitizeURI() retained double-encoded credential payload: %q", safe)
			}
			if err := provider.ValidateCanonicalURI(raw); err == nil {
				t.Fatalf("ValidateCanonicalURI(%q) accepted double-encoded credentials", raw)
			}
		})
	}
}

func TestURISanitizationRedactsQueryKeysAndPreservesURLStructure(t *testing.T) {
	const canary = "provider-query-key-canary-6f21"
	policy, err := provider.NewRedactionPolicy(provider.DefaultMaxTextBytes, provider.DefaultMaxRawBytes, canary)
	if err != nil {
		t.Fatal(err)
	}
	raw := "https://example.invalid/run?" + url.QueryEscape(canary) + "=safe&token=drop-me#route?" + url.QueryEscape(canary) + "=fragment-safe&password=drop-me"
	safe, err := provider.SanitizeURIWithPolicy(raw, policy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(safe, canary) || strings.Contains(safe, "token=") || strings.Contains(safe, "password=") {
		t.Fatalf("sanitized URI leaked a query key or credential key: %q", safe)
	}
	parsed, err := url.Parse(safe)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "example.invalid" {
		t.Fatalf("sanitized URI is not structurally valid: %q, %v", safe, err)
	}
	if got := parsed.Query().Get(execx.Redacted); got != "safe" {
		t.Fatalf("sanitized query key/value = %q in %q", got, safe)
	}
	route, rawFragmentQuery, found := strings.Cut(parsed.Fragment, "?")
	if !found || route != "route" {
		t.Fatalf("sanitized fragment structure = %q", parsed.Fragment)
	}
	fragmentQuery, err := url.ParseQuery(rawFragmentQuery)
	if err != nil || fragmentQuery.Get(execx.Redacted) != "fragment-safe" {
		t.Fatalf("sanitized fragment query = %#v, %v", fragmentQuery, err)
	}
}

func TestExternalRefRejectsUnsafeCanonicalURIWithoutLeakingIt(t *testing.T) {
	const canary = "provider-reference-canary-b02d"
	unsafe := provider.ExternalRef{
		Role:       provider.ExternalRoleTracker,
		Provider:   provider.ProviderMLflow,
		Context:    "lab",
		NativeKind: "run",
		NativeID:   "00000000000000000000000000000001",
		URI:        "https://user:" + canary + "@mlflow.example.invalid/run/1?token=" + canary,
	}
	encoded, err := json.Marshal(unsafe)
	if err == nil || len(encoded) != 0 {
		t.Fatalf("json.Marshal(unsafe ref) = %q, %v", encoded, err)
	}
	if strings.Contains(err.Error(), canary) || strings.Contains(unsafe.String(), canary) {
		t.Fatalf("unsafe reference error rendering leaked canary: %v / %q", err, unsafe.String())
	}

	valid := unsafe
	valid.URI = "https://mlflow.example.invalid/#/runs/00000000000000000000000000000001"
	valid.Metadata = map[string]any{
		"mlflow.experiment_id": json.Number("7"),
		"mlflow.score_value":   json.Number("0.5"),
		"mlflow.scope": map[string]any{
			"workspace": "synthetic",
			"labels":    []any{"one", "two"},
		},
	}
	canonical, err := provider.NewExternalRef(valid)
	if err != nil {
		t.Fatalf("NewExternalRef() error = %v", err)
	}
	if err := canonical.ValidateWithRegistry(provider.CompiledRegistry()); err != nil {
		t.Fatalf("ValidateWithRegistry() error = %v", err)
	}
	if _, ok := canonical.Metadata["mlflow.experiment_id"].(int64); !ok {
		t.Fatalf("integer metadata normalized as %T, want int64", canonical.Metadata["mlflow.experiment_id"])
	}
	if _, ok := canonical.Metadata["mlflow.score_value"].(float64); !ok {
		t.Fatalf("floating metadata normalized as %T, want float64", canonical.Metadata["mlflow.score_value"])
	}
	canonical.Metadata["mlflow.experiment_id"] = "changed"
	canonical.Metadata["mlflow.scope"].(map[string]any)["workspace"] = "changed"
	if valid.Metadata["mlflow.experiment_id"] != json.Number("7") || valid.Metadata["mlflow.scope"].(map[string]any)["workspace"] != "synthetic" {
		t.Fatal("NewExternalRef() did not defensively copy structured metadata")
	}

	for name, metadata := range map[string]map[string]any{
		"token":             {"mlflow.token": canary},
		"auth value":        {"mlflow.auth_value": canary},
		"database password": {"mlflow.databasePassword": canary},
		"session id":        {"mlflow.session_id": canary},
		"oversized":         {"mlflow.note": strings.Repeat("x", provider.MaxExternalRefBytes)},
	} {
		t.Run(name, func(t *testing.T) {
			sensitiveMetadata := valid
			sensitiveMetadata.Metadata = metadata
			if _, err := json.Marshal(sensitiveMetadata); err == nil {
				t.Fatal("canonical ExternalRef accepted unsafe metadata")
			}
		})
	}

	unknown := valid
	unknown.Provider = "future-provider"
	unknown.Metadata = map[string]any{"future-provider.run_id": int64(9)}
	if _, err := json.Marshal(unknown); err != nil {
		t.Fatalf("canonical ExternalRef rejected a future provider: %v", err)
	}

	reserved := valid
	reserved.Provider = provider.ProviderUnknown
	reserved.Metadata = nil
	if _, err := json.Marshal(reserved); err == nil {
		t.Fatal("canonical ExternalRef accepted the reserved unknown provider")
	}

	wrongKnownRole := valid
	wrongKnownRole.Provider = provider.ProviderPueue
	wrongKnownRole.Metadata = map[string]any{"pueue.task_id": int64(9)}
	if _, err := json.Marshal(wrongKnownRole); err == nil {
		t.Fatal("canonical ExternalRef accepted a known provider/role mismatch")
	}
}

func TestPueueStateRemovesEveryNestedEnvsAndRedactsCanaries(t *testing.T) {
	const canary = "provider-pueue-canary-228e"
	policy, err := provider.NewRedactionPolicy(provider.DefaultMaxTextBytes, provider.DefaultMaxRawBytes, canary)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"tasks": []any{
			map[string]any{
				"id":   float64(1),
				"envs": map[string]any{"TOKEN": canary},
				"nested": map[string]any{
					"EnVs":  []any{canary},
					"state": "Done",
				},
			},
		},
		"password": canary,
		"message":  "literal " + canary,
	}
	safe, err := provider.SanitizePueueRawState(input, policy)
	if err != nil {
		t.Fatalf("SanitizePueueRawState() error = %v", err)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), `"envs"`) || strings.Contains(string(encoded), canary) {
		t.Fatalf("Pueue state crossed boundary unsafely: %s", encoded)
	}
	if !strings.Contains(string(encoded), execx.Redacted) || !strings.Contains(string(encoded), `"state":"Done"`) {
		t.Fatalf("Pueue state did not retain sanitized native data: %s", encoded)
	}
	// The sanitizer returns a copy and never mutates parser-owned input.
	originalTask := input["tasks"].([]any)[0].(map[string]any)
	if _, exists := originalTask["envs"]; !exists {
		t.Fatal("SanitizePueueRawState() mutated input")
	}

	raw := []byte(`{"outer":{"envs":{"SECRET":"` + canary + `"},"items":[{"envs":[1],"value":"` + canary + `"}]}}`)
	safeJSON, err := provider.SanitizePueueJSON(raw, policy)
	if err != nil {
		t.Fatalf("SanitizePueueJSON() error = %v", err)
	}
	if strings.Contains(strings.ToLower(string(safeJSON)), `"envs"`) || strings.Contains(string(safeJSON), canary) {
		t.Fatalf("SanitizePueueJSON() leaked: %s", safeJSON)
	}
	if _, err := provider.SanitizePueueJSON(append(raw, []byte(` {}`)...), policy); !errors.Is(err, provider.ErrUnsupportedData) {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func TestRawStateAndURIRedactionUseSharedCredentialNames(t *testing.T) {
	const canary = "provider-shared-classifier-canary-d17a"
	policy := provider.DefaultRedactionPolicy()
	raw := map[string]any{
		"auth_value":           canary,
		"databasePassword":     canary,
		"session_id":           canary,
		"authorization/cookie": canary,
		"token_count":          int64(12),
		"note":                 "token=[CLS]",
	}
	safe, err := provider.SanitizeRawState(raw, policy)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) || !strings.Contains(string(encoded), `"token_count":12`) || !strings.Contains(string(encoded), `"token=[CLS]"`) {
		t.Fatalf("shared raw-state classification = %s", encoded)
	}

	uri := "https://example.invalid/run?auth_value=" + canary + "&databasePassword=" + canary + "&session_id=" + canary + "&view=active"
	safeURI, err := provider.SanitizeURIWithPolicy(uri, policy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(safeURI, canary) || strings.Contains(safeURI, "auth_value") || strings.Contains(safeURI, "databasePassword") || strings.Contains(safeURI, "session_id") || !strings.Contains(safeURI, "view=active") {
		t.Fatalf("shared URI classification = %q", safeURI)
	}
}

func TestStructuralRedactionCoversHeadersArgvEnvironmentAndBounds(t *testing.T) {
	const canary = "provider-structure-canary-a191"
	policy, err := provider.NewRedactionPolicy(128, 512, canary)
	if err != nil {
		t.Fatal(err)
	}
	headers, err := provider.SanitizeHeaders(map[string][]string{
		"Authorization": {"Bearer " + canary},
		"X-Trace":       {"trace " + canary},
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	argv, err := provider.SanitizeArgv([]string{"status", "--token", canary, "two words"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := provider.SanitizeEnvironment(map[string]string{
		"MLFLOW_TRACKING_TOKEN": canary,
		"MODE":                  "value " + canary,
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal([]any{headers, argv, environment})
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("structural sanitizer leaked canary: %s", encoded)
	}
	if len(argv) != 4 || argv[3] != "two words" || headers["Authorization"][0] != execx.Redacted || environment["MLFLOW_TRACKING_TOKEN"] != execx.Redacted {
		t.Fatalf("structural shape changed unexpectedly: %s", encoded)
	}

	tiny, err := provider.NewRedactionPolicy(16, 32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SanitizeRawState(map[string]any{"value": strings.Repeat("x", 100)}, tiny); !errors.Is(err, provider.ErrRedactionLimit) {
		t.Fatalf("oversized raw state error = %v", err)
	}
}
