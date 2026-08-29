package provider_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/provider"
)

func TestOperationPlanIsReviewableStableAndSecretFree(t *testing.T) {
	const canary = "provider-plan-canary-5cb7"
	environment, err := execx.NewEnvironment([]string{"PATH"},
		execx.Bind("MODE", "inspect"),
		execx.BindSecret("MLFLOW_TRACKING_TOKEN", canary),
	)
	if err != nil {
		t.Fatal(err)
	}
	command := execx.CommandSpec{
		Executable:  "/synthetic/bin/mlflow",
		Argv:        []string{"status", "--token", canary, "two words", "literal;$HOME"},
		CWD:         "/synthetic/repo",
		Environment: environment,
		Timeout:     5 * time.Second,
		Output: execx.OutputPolicy{
			Mode:           execx.OutputCapture,
			MaxStdoutBytes: 4096,
			MaxStderrBytes: 2048,
		},
		Redaction: execx.NewRedactor(canary),
	}
	effects, err := provider.NewEffectSet(provider.EffectSensitiveOutput, provider.EffectRemoteRead)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := provider.NewRedactionPolicy(provider.DefaultMaxTextBytes, provider.DefaultMaxRawBytes, canary)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := provider.NewOperationPlan(provider.OperationPlanRequest{
		Provider:   provider.ProviderMLflow,
		Context:    "lab",
		Role:       provider.RoleTracker,
		Capability: provider.CapabilityTrackerResolve,
		Operation:  "resolve",
		Command:    command,
		Effects:    effects,
		Diagnostics: []provider.Diagnostic{{
			Severity: provider.SeverityWarning,
			Code:     "synthetic_warning",
			Message:  "token=" + canary,
			Native:   canary,
		}},
		Redaction: policy,
	})
	if err != nil {
		t.Fatalf("NewOperationPlan() error = %v", err)
	}
	if plan.Schema != provider.OperationPlanSchema || plan.Timeout != "5s" || plan.Output.MaxStdoutBytes != 4096 {
		t.Fatalf("plan metadata = %+v", plan)
	}
	if len(plan.Argv) != len(command.Argv) || plan.Argv[3] != "two words" || plan.Argv[4] != "literal;$HOME" {
		t.Fatalf("plan argv boundaries = %#v", plan.Argv)
	}
	if got, want := plan.Effects.Values, []provider.Effect{provider.EffectRemoteRead, provider.EffectSensitiveOutput}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan effects = %v, want %v", got, want)
	}
	if plan.Environment == nil || plan.Diagnostics == nil {
		t.Fatalf("plan has nil stable collections: %+v", plan)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("json.Marshal(plan) error = %v", err)
	}
	combined := string(encoded) + "\n" + plan.String()
	if strings.Contains(combined, canary) {
		t.Fatalf("operation plan leaked canary: %s", combined)
	}
	if !strings.Contains(combined, execx.Redacted) {
		t.Fatalf("operation plan did not expose redaction markers: %s", combined)
	}

	// MarshalJSON reapplies the retained policy and deterministic set ordering
	// even after accidental mutation.
	plan.Argv = append(plan.Argv, "--password", canary)
	plan.Effects.Values[0], plan.Effects.Values[1] = plan.Effects.Values[1], plan.Effects.Values[0]
	for left, right := 0, len(plan.Environment)-1; left < right; left, right = left+1, right-1 {
		plan.Environment[left], plan.Environment[right] = plan.Environment[right], plan.Environment[left]
	}
	encoded, err = json.Marshal(plan)
	if err != nil {
		t.Fatalf("json.Marshal(mutated plan) error = %v", err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("mutated plan leaked canary: %s", encoded)
	}
	var rendered struct {
		Effects     provider.EffectSet          `json:"effects"`
		Environment []execx.EnvironmentVariable `json:"environment"`
	}
	if err := json.Unmarshal(encoded, &rendered); err != nil {
		t.Fatal(err)
	}
	if got, want := rendered.Effects.Values, []provider.Effect{provider.EffectRemoteRead, provider.EffectSensitiveOutput}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mutated plan effects serialized as %v", got)
	}
	for index := 1; index < len(rendered.Environment); index++ {
		if rendered.Environment[index].Name < rendered.Environment[index-1].Name {
			t.Fatalf("mutated plan environment is not sorted: %+v", rendered.Environment)
		}
	}
}

func TestOperationPlanRedactsAttachedShortOptions(t *testing.T) {
	const canary = "provider-attached-plan-canary-f327"
	environment, err := execx.NewEnvironment(nil)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := provider.NewEffectSet(provider.EffectRemoteRead, provider.EffectSensitiveOutput)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := provider.NewOperationPlan(provider.OperationPlanRequest{
		Provider:   provider.ProviderMLflow,
		Context:    "lab",
		Role:       provider.RoleTracker,
		Capability: provider.CapabilityTrackerResolve,
		Operation:  "resolve",
		Command: execx.CommandSpec{
			Executable:  "/synthetic/bin/mlflow",
			Argv:        []string{"-HAuthorization: Bearer " + canary, "-ualice:" + canary},
			CWD:         "/synthetic/repo",
			Environment: environment,
			Output:      execx.DefaultOutputPolicy(execx.OutputCapture),
		},
		Effects: effects,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(encoded) + plan.String()
	if strings.Contains(combined, canary) || !strings.Contains(combined, execx.Redacted) {
		t.Fatalf("operation plan leaked attached option: %s", combined)
	}
}

func TestObservationUnknownValuesFailClosedAndRetainOnlySafeNativeData(t *testing.T) {
	const canary = "provider-observation-canary-a5f4"
	policy, err := provider.NewRedactionPolicy(provider.DefaultMaxTextBytes, provider.DefaultMaxRawBytes, canary)
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, time.August, 29, 20, 30, 0, 0, time.FixedZone("test", -4*60*60))
	input := provider.Observation{
		Provider:        provider.ProviderPueue,
		Context:         "local",
		ProviderVersion: "4.0.4",
		Capability:      provider.CapabilitySchedulerObserve,
		Support:         provider.Support("optimistic"),
		Source:          "pueue status --json",
		ObservedAt:      observedAt,
		NormalizedState: provider.NormalizedState("Done"),
		NativeState:     "Done",
		NativeReason:    "token=" + canary,
		RawOnly:         true,
		RawState: map[string]any{
			"envs":  map[string]any{"TOKEN": canary},
			"state": "Done",
			"note":  canary,
		},
	}
	normalized, err := provider.CompiledRegistry().NormalizeObservation(input, policy)
	if err != nil {
		t.Fatalf("NormalizeObservation() error = %v", err)
	}
	if normalized.Support != provider.SupportUnknown || normalized.NormalizedState != provider.StateUnknown {
		t.Fatalf("unknown values did not fail closed: %+v", normalized)
	}
	if normalized.NativeSupport != "optimistic" || normalized.NativeState != "Done" {
		t.Fatalf("native values were not retained safely: %+v", normalized)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), `"envs"`) || strings.Contains(string(encoded), canary) {
		t.Fatalf("normalized observation leaked state: %s", encoded)
	}
	if !hasDiagnostic(normalized.Diagnostics, "unknown_support_value") || !hasDiagnostic(normalized.Diagnostics, "unknown_normalized_state") {
		t.Fatalf("normalized observation diagnostics = %+v", normalized.Diagnostics)
	}
}

func TestUnknownProviderCannotClaimSupportOrTerminalState(t *testing.T) {
	input := provider.Observation{
		Provider:        "future-provider",
		Context:         "local",
		Capability:      "scheduler.observe",
		Support:         provider.SupportSupported,
		Source:          "native inspection",
		ObservedAt:      time.Unix(100, 0),
		NormalizedState: provider.StateSucceeded,
		NativeState:     "Finished",
		RawState:        map[string]any{"native": "visible"},
	}
	normalized, err := provider.CompiledRegistry().NormalizeObservation(input, provider.DefaultRedactionPolicy())
	if err != nil {
		t.Fatalf("NormalizeObservation() error = %v", err)
	}
	if normalized.Provider != provider.ProviderUnknown || normalized.NativeProvider != "future-provider" {
		t.Fatalf("provider normalization = %+v", normalized)
	}
	if normalized.Support != provider.SupportUnknown || normalized.NormalizedState != provider.StateUnknown || normalized.Capability != "" {
		t.Fatalf("unknown provider retained normalized claims: %+v", normalized)
	}
	if normalized.NativeCapability != "scheduler.observe" || normalized.NativeState != "Finished" {
		t.Fatalf("unknown provider did not retain safe native values: %+v", normalized)
	}
}

func TestPueueRawStateGuardrailAppliesEvenWhenProviderIsUnknownToRegistry(t *testing.T) {
	direct, ok := provider.CompiledRegistry().Get(provider.ProviderDirect)
	if !ok {
		t.Fatal("compiled direct descriptor missing")
	}
	registry, err := provider.NewRegistry(direct)
	if err != nil {
		t.Fatal(err)
	}
	input := provider.Observation{
		Provider:        provider.ProviderPueue,
		Context:         "local",
		Capability:      provider.CapabilitySchedulerObserve,
		Support:         provider.SupportSupported,
		Source:          "native JSON",
		ObservedAt:      time.Unix(100, 0),
		NormalizedState: provider.StateSucceeded,
		RawState:        map[string]any{"envs": map[string]any{"TOKEN": "must-disappear"}, "state": "Done"},
	}
	normalized, err := registry.NormalizeObservation(input, provider.DefaultRedactionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(normalized.RawState)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), `"envs"`) {
		t.Fatalf("unknown-registry Pueue state retained envs: %s", encoded)
	}
}

func TestRawStateDropsEnvsDespiteProviderAndNativeProviderMismatch(t *testing.T) {
	input := provider.Observation{
		Provider:        provider.ProviderDirect,
		NativeProvider:  "pueue",
		Context:         "local",
		Capability:      provider.CapabilityRunnerPrepare,
		Support:         provider.SupportSupported,
		Source:          "native JSON",
		ObservedAt:      time.Unix(100, 0),
		NormalizedState: provider.StateRunning,
		RawState: map[string]any{
			"outer": map[string]any{"EnVs": map[string]any{"DATABASE_PASSWORD": "must-disappear"}},
			"state": "Running",
		},
	}
	normalized, err := provider.CompiledRegistry().NormalizeObservation(input, provider.DefaultRedactionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(normalized.RawState)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), `"envs"`) || strings.Contains(string(encoded), "must-disappear") {
		t.Fatalf("provider-label mismatch retained envs: %s", encoded)
	}
}

func TestUnverifiedCapabilityCannotClaimKnownState(t *testing.T) {
	input := provider.Observation{
		Provider:        provider.ProviderPueue,
		Context:         "local",
		Capability:      provider.CapabilitySchedulerObserve,
		Support:         provider.SupportUnknown,
		Source:          "bounded native JSON",
		ObservedAt:      time.Unix(101, 0),
		NormalizedState: provider.StateSucceeded,
		NativeState:     "Done",
	}
	normalized, err := provider.CompiledRegistry().NormalizeObservation(input, provider.DefaultRedactionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if normalized.NormalizedState != provider.StateUnknown || !hasDiagnostic(normalized.Diagnostics, "untrusted_normalized_state") {
		t.Fatalf("unverified state did not fail closed: %+v", normalized)
	}

	input.Support = provider.SupportSupported
	normalized, err = provider.CompiledRegistry().NormalizeObservation(input, provider.DefaultRedactionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if normalized.NormalizedState != provider.StateSucceeded {
		t.Fatalf("verified known state = %q", normalized.NormalizedState)
	}
}

func TestObservationResultOrderingAndNativeStateMapping(t *testing.T) {
	mapping := map[string]provider.NormalizedState{"Done": provider.StateSucceeded}
	state, native, diagnostic, err := provider.NormalizeNativeState("Done", mapping, provider.DefaultRedactionPolicy())
	if err != nil || state != provider.StateSucceeded || native != "Done" || diagnostic != nil {
		t.Fatalf("NormalizeNativeState(Done) = %q, %q, %+v, %v", state, native, diagnostic, err)
	}
	state, native, diagnostic, err = provider.NormalizeNativeState("Future", mapping, provider.DefaultRedactionPolicy())
	if err != nil || state != provider.StateUnknown || native != "Future" || diagnostic == nil {
		t.Fatalf("NormalizeNativeState(Future) = %q, %q, %+v, %v", state, native, diagnostic, err)
	}

	observations := []provider.Observation{
		{
			Provider: provider.ProviderPueue, Context: "z", Capability: provider.CapabilitySchedulerObserve,
			Support: provider.SupportSupported, Source: "b", ObservedAt: time.Unix(2, 0), NormalizedState: provider.StateRunning,
		},
		{
			Provider: provider.ProviderDVC, Context: "a", Capability: provider.CapabilityArtifactStat,
			Support: provider.SupportUnknown, Source: "a", ObservedAt: time.Unix(1, 0), NormalizedState: provider.StateUnknown,
		},
	}
	result, err := provider.CompiledRegistry().NormalizeObservations(observations, nil, provider.DefaultRedactionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if result.Observations[0].Provider != provider.ProviderDVC || result.Observations[1].Provider != provider.ProviderPueue {
		t.Fatalf("observation order = %+v", result.Observations)
	}
	if !result.ObservedAt.Equal(time.Unix(2, 0)) || result.Diagnostics == nil {
		t.Fatalf("observation result metadata = %+v", result)
	}
}
