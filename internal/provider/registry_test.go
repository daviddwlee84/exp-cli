package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestCompiledProviderRolesMatchCanonicalSchemaMatrix(t *testing.T) {
	for _, descriptor := range provider.CompiledRegistry().List() {
		canonical, known := research.KnownProviderRoles(string(descriptor.Name))
		if !known {
			t.Errorf("compiled provider %q is absent from canonical provider-role matrix", descriptor.Name)
			continue
		}
		want := make(map[provider.Role]struct{}, len(canonical))
		for _, role := range canonical {
			providerRole := provider.Role(role)
			if role == research.ExternalArtifact {
				providerRole = provider.RoleArtifactStore
			}
			want[providerRole] = struct{}{}
		}
		got := make(map[provider.Role]struct{}, len(descriptor.Roles))
		for _, role := range descriptor.Roles {
			got[role] = struct{}{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("compiled provider %q roles = %v, canonical matrix = %v", descriptor.Name, descriptor.Roles, canonical)
		}
	}
}

func TestLocalDiscoveryUsesOnlyInjectedLookupAndSafeProbe(t *testing.T) {
	const canary = "provider-version-error-canary-c717"
	policy, err := provider.NewRedactionPolicy(provider.DefaultMaxTextBytes, provider.DefaultMaxRawBytes, canary)
	if err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Date(2026, time.August, 29, 17, 23, 0, 0, time.FixedZone("test", 3*60*60))
	present := map[string]string{
		"mlflow": "/synthetic/bin/mlflow",
		"pueue":  "/synthetic/bin/pueue",
	}
	var lookups []string
	lookup := func(name string) (string, error) {
		lookups = append(lookups, name)
		if path, ok := present[name]; ok {
			return path, nil
		}
		return "", errors.New("not found")
	}
	var probes []provider.ProviderName
	probe := func(_ context.Context, descriptor provider.Descriptor, path string) (provider.LocalVersionResult, error) {
		probes = append(probes, descriptor.Name)
		if path != present[string(descriptor.Name)] {
			return provider.LocalVersionResult{}, fmt.Errorf("unexpected path")
		}
		switch descriptor.Name {
		case provider.ProviderPueue:
			return provider.LocalVersionResult{
				Version: "pueue 4.0.4\n",
				Capabilities: map[provider.Capability]provider.Support{
					provider.CapabilitySchedulerObserve:     provider.SupportSupported,
					provider.CapabilitySchedulerSubmit:      provider.SupportUnsupported,
					provider.CapabilitySchedulerCancel:      provider.Support("future-native-value"),
					provider.Capability("scheduler.future"): provider.SupportSupported,
				},
			}, nil
		case provider.ProviderMLflow:
			return provider.LocalVersionResult{}, errors.New("probe failed: token=" + canary)
		default:
			return provider.LocalVersionResult{}, fmt.Errorf("unexpected provider")
		}
	}

	registry := provider.CompiledRegistry()
	results, err := registry.DiscoverLocal(t.Context(), provider.LocalDiscoveryOptions{
		Context:      "local-synthetic",
		Lookup:       lookup,
		VersionProbe: probe,
		Now:          func() time.Time { return fixedTime },
		Redaction:    policy,
	})
	if err != nil {
		t.Fatalf("DiscoverLocal() error = %v", err)
	}
	if len(results) != 7 {
		t.Fatalf("DiscoverLocal() returned %d results", len(results))
	}
	if got, want := probes, []provider.ProviderName{provider.ProviderMLflow, provider.ProviderPueue}; !reflect.DeepEqual(got, want) {
		t.Fatalf("version probes = %v, want %v", got, want)
	}

	// Descriptor and candidate ordering fully determines lookup order.
	wantLookups := []string{"dvc", "jupyter", "marimo", "mlflow", "pueue", "sacct", "sbatch", "scancel", "squeue"}
	if !reflect.DeepEqual(lookups, wantLookups) {
		t.Fatalf("lookups = %v, want %v", lookups, wantLookups)
	}
	for index, result := range results {
		if result.Provider != registry.Names()[index] {
			t.Errorf("result[%d].Provider = %q", index, result.Provider)
		}
		if result.ObservedAt.Location() != time.UTC || !result.ObservedAt.Equal(fixedTime) {
			t.Errorf("result[%d].ObservedAt = %v", index, result.ObservedAt)
		}
		if result.Capabilities == nil || result.Diagnostics == nil {
			t.Errorf("result[%d] has nil stable collections: %+v", index, result)
		}
	}

	pueue := findProbe(t, results, provider.ProviderPueue)
	if pueue.ProviderVersion != "pueue 4.0.4" {
		t.Fatalf("Pueue version = %q", pueue.ProviderVersion)
	}
	if got := pueue.SupportFor(provider.CapabilitySchedulerObserve); got != provider.SupportSupported {
		t.Fatalf("Pueue observe support = %q", got)
	}
	if got := pueue.SupportFor(provider.CapabilitySchedulerSubmit); got != provider.SupportUnsupported {
		t.Fatalf("Pueue submit support = %q", got)
	}
	if got := pueue.SupportFor(provider.CapabilitySchedulerCancel); got != provider.SupportUnknown {
		t.Fatalf("Pueue cancel support = %q", got)
	}
	if !hasDiagnostic(pueue.Diagnostics, "unknown_probe_capability") || !hasDiagnostic(pueue.Diagnostics, "unknown_support_value") {
		t.Fatalf("Pueue diagnostics = %+v", pueue.Diagnostics)
	}

	mlflow := findProbe(t, results, provider.ProviderMLflow)
	if mlflow.ProviderVersion != "" || mlflow.SupportFor(provider.CapabilityTrackerList) != provider.SupportUnknown {
		t.Fatalf("MLflow failure promoted support: %+v", mlflow)
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("discovery leaked version-probe canary: %s", encoded)
	}
	if !strings.Contains(string(encoded), "[REDACTED]") {
		t.Fatalf("discovery did not retain a redaction marker: %s", encoded)
	}
}

func TestFoundBinaryWithoutVersionProbeRemainsUnknown(t *testing.T) {
	registry := provider.CompiledRegistry()
	calls := 0
	results, err := registry.DiscoverLocal(t.Context(), provider.LocalDiscoveryOptions{
		Lookup: func(name string) (string, error) {
			calls++
			return "/synthetic/bin/" + name, nil
		},
		Now: func() time.Time { return time.Unix(123, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 6 { // direct needs no lookup; each other descriptor resolves its first sorted candidate.
		t.Fatalf("lookup calls = %d, want 6", calls)
	}
	for _, result := range results {
		for _, capability := range result.Capabilities {
			if capability.Support != provider.SupportUnknown {
				t.Fatalf("binary presence promoted %s/%s to %s", result.Provider, capability.Capability, capability.Support)
			}
		}
	}
}

func TestBinaryOverridesBypassLookupButRemainLocallyProbed(t *testing.T) {
	registry := provider.CompiledRegistry()
	var lookups []string
	var probedPath string
	results, err := registry.DiscoverLocal(t.Context(), provider.LocalDiscoveryOptions{
		Lookup: func(name string) (string, error) {
			lookups = append(lookups, name)
			return "", errors.New("missing")
		},
		BinaryOverrides: map[provider.ProviderName]string{
			provider.ProviderPueue: "/synthetic/override/pueue",
		},
		VersionProbe: func(_ context.Context, descriptor provider.Descriptor, path string) (provider.LocalVersionResult, error) {
			if descriptor.Name != provider.ProviderPueue {
				t.Fatalf("unexpected overridden probe for %q", descriptor.Name)
			}
			probedPath = path
			return provider.LocalVersionResult{
				Version: "4.0.4",
				Capabilities: map[provider.Capability]provider.Support{
					provider.CapabilitySchedulerObserve: provider.SupportSupported,
				},
			}, nil
		},
		Now: func() time.Time { return time.Unix(234, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if probedPath != "/synthetic/override/pueue" {
		t.Fatalf("version probe path = %q", probedPath)
	}
	for _, lookup := range lookups {
		if lookup == "pueue" {
			t.Fatal("explicit Pueue override still called executable lookup")
		}
	}
	pueue := findProbe(t, results, provider.ProviderPueue)
	if pueue.ResolvedBinaryPath != probedPath || pueue.SupportFor(provider.CapabilitySchedulerObserve) != provider.SupportSupported {
		t.Fatalf("overridden Pueue result = %+v", pueue)
	}

	invalid, err := registry.DiscoverLocal(t.Context(), provider.LocalDiscoveryOptions{
		Lookup: func(string) (string, error) { return "", errors.New("missing") },
		BinaryOverrides: map[provider.ProviderName]string{
			provider.ProviderPueue: "relative/pueue",
		},
		VersionProbe: func(context.Context, provider.Descriptor, string) (provider.LocalVersionResult, error) {
			t.Fatal("invalid override reached version probe")
			return provider.LocalVersionResult{}, nil
		},
		Now: func() time.Time { return time.Unix(235, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(findProbe(t, invalid, provider.ProviderPueue).Diagnostics, "invalid_binary_override") {
		t.Fatalf("invalid override diagnostics = %+v", findProbe(t, invalid, provider.ProviderPueue).Diagnostics)
	}

	if _, err := registry.DiscoverLocal(t.Context(), provider.LocalDiscoveryOptions{
		BinaryOverrides: map[provider.ProviderName]string{"future-provider": "/bin/future"},
	}); err == nil {
		t.Fatal("uncompiled provider binary override was accepted")
	}
}

func TestLocalDiscoveryIsDeterministicAndCancellationAware(t *testing.T) {
	registry := provider.CompiledRegistry()
	options := provider.LocalDiscoveryOptions{
		Lookup: func(string) (string, error) { return "", errors.New("missing") },
		Now:    func() time.Time { return time.Unix(456, 789).UTC() },
	}
	first, err := registry.DiscoverLocal(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.DiscoverLocal(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("discovery not deterministic\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	partial, err := registry.DiscoverLocal(ctx, options)
	if !errors.Is(err, context.Canceled) || len(partial) != 0 {
		t.Fatalf("canceled discovery = %d results, %v", len(partial), err)
	}
}

func TestLocalDiscoveryPropagatesParentCancellationFromFinalProbe(t *testing.T) {
	registry := provider.CompiledRegistry()
	lookup := func(name string) (string, error) {
		if name == "sacct" {
			return "/synthetic/bin/sacct", nil
		}
		return "", errors.New("missing")
	}

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		results, err := registry.DiscoverLocal(ctx, provider.LocalDiscoveryOptions{
			Lookup: lookup,
			VersionProbe: func(context.Context, provider.Descriptor, string) (provider.LocalVersionResult, error) {
				cancel()
				return provider.LocalVersionResult{}, context.Canceled
			},
		})
		if !errors.Is(err, context.Canceled) || len(results) != len(registry.List())-1 {
			t.Fatalf("final canceled probe = %d results, %v", len(results), err)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		results, err := registry.DiscoverLocal(ctx, provider.LocalDiscoveryOptions{
			Lookup: lookup,
			VersionProbe: func(ctx context.Context, _ provider.Descriptor, _ string) (provider.LocalVersionResult, error) {
				<-ctx.Done()
				return provider.LocalVersionResult{}, ctx.Err()
			},
		})
		if !errors.Is(err, context.DeadlineExceeded) || len(results) != len(registry.List())-1 {
			t.Fatalf("final timed-out probe = %d results, %v", len(results), err)
		}
	})
}

func TestLocalDiscoveryKeepsProviderLocalCancellationAsDiagnostic(t *testing.T) {
	registry, err := provider.NewRegistry(provider.Descriptor{
		Name:              provider.ProviderPueue,
		Roles:             []provider.Role{provider.RoleScheduler},
		CandidateBinaries: []string{"pueue"},
		Capabilities:      []provider.Capability{provider.CapabilitySchedulerObserve},
		ContractVersion:   provider.AdapterContractVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := registry.DiscoverLocal(t.Context(), provider.LocalDiscoveryOptions{
		Lookup: func(string) (string, error) { return "/synthetic/bin/pueue", nil },
		VersionProbe: func(context.Context, provider.Descriptor, string) (provider.LocalVersionResult, error) {
			return provider.LocalVersionResult{}, context.Canceled
		},
	})
	if err != nil || len(results) != 1 || !hasDiagnostic(results[0].Diagnostics, "version_probe_failed") {
		t.Fatalf("provider-local cancellation = %#v, %v", results, err)
	}
}

func findProbe(t *testing.T, results []provider.ProbeResult, name provider.ProviderName) provider.ProbeResult {
	t.Helper()
	for _, result := range results {
		if result.Provider == name {
			return result
		}
	}
	t.Fatalf("probe %q not found", name)
	return provider.ProbeResult{}
}

func hasDiagnostic(diagnostics []provider.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
