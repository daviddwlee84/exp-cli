package provider_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/provider"
)

func TestCompiledRegistryIsCompleteStableAndDefensive(t *testing.T) {
	registry := provider.CompiledRegistry()
	wantNames := []provider.ProviderName{
		provider.ProviderDirect,
		provider.ProviderDVC,
		provider.ProviderJupyter,
		provider.ProviderMarimo,
		provider.ProviderMLflow,
		provider.ProviderPueue,
		provider.ProviderSlurm,
	}
	if got := registry.Names(); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("Names() = %v, want %v", got, wantNames)
	}

	descriptors := registry.List()
	if len(descriptors) != len(wantNames) {
		t.Fatalf("List() returned %d descriptors", len(descriptors))
	}
	for _, descriptor := range descriptors {
		if err := descriptor.Validate(); err != nil {
			t.Errorf("descriptor %q invalid: %v", descriptor.Name, err)
		}
		if !sort.SliceIsSorted(descriptor.Roles, func(i, j int) bool { return descriptor.Roles[i] < descriptor.Roles[j] }) {
			t.Errorf("descriptor %q roles not sorted: %v", descriptor.Name, descriptor.Roles)
		}
		if !sort.StringsAreSorted(descriptor.CandidateBinaries) {
			t.Errorf("descriptor %q binaries not sorted: %v", descriptor.Name, descriptor.CandidateBinaries)
		}
		if !sort.SliceIsSorted(descriptor.Capabilities, func(i, j int) bool { return descriptor.Capabilities[i] < descriptor.Capabilities[j] }) {
			t.Errorf("descriptor %q capabilities not sorted: %v", descriptor.Name, descriptor.Capabilities)
		}
	}

	// Returned slices are copies; command code cannot corrupt the compiled set.
	descriptors[0].Roles[0] = provider.Role("corrupted")
	descriptors[0].Capabilities[0] = provider.Capability("corrupted.value")
	descriptors[0].CandidateBinaries = append(descriptors[0].CandidateBinaries, "corrupted")
	again, ok := registry.Get(wantNames[0])
	if !ok || !again.Roles[0].Valid() || !again.Capabilities[0].Valid() {
		t.Fatalf("registry was mutated through List(): %+v", again)
	}
}

func TestDescriptorValidationRejectsMalformedContracts(t *testing.T) {
	valid := provider.Descriptor{
		Name:              "synthetic",
		Roles:             []provider.Role{provider.RoleScheduler},
		CandidateBinaries: []string{"synthetic"},
		Capabilities:      []provider.Capability{provider.CapabilitySchedulerObserve},
		ContractVersion:   provider.AdapterContractVersion,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid descriptor error = %v", err)
	}

	for _, testCase := range []struct {
		name   string
		mutate func(*provider.Descriptor)
	}{
		{name: "reserved provider", mutate: func(value *provider.Descriptor) { value.Name = provider.ProviderUnknown }},
		{name: "upper-case provider", mutate: func(value *provider.Descriptor) { value.Name = "Synthetic" }},
		{name: "wrong version", mutate: func(value *provider.Descriptor) { value.ContractVersion = "exp.provider/v2" }},
		{name: "no roles", mutate: func(value *provider.Descriptor) { value.Roles = nil }},
		{name: "duplicate role", mutate: func(value *provider.Descriptor) { value.Roles = append(value.Roles, provider.RoleScheduler) }},
		{name: "unknown role", mutate: func(value *provider.Descriptor) { value.Roles = []provider.Role{"everything"} }},
		{name: "no capabilities", mutate: func(value *provider.Descriptor) { value.Capabilities = nil }},
		{name: "capability role mismatch", mutate: func(value *provider.Descriptor) {
			value.Capabilities = []provider.Capability{provider.CapabilityTrackerList}
		}},
		{name: "duplicate capability", mutate: func(value *provider.Descriptor) {
			value.Capabilities = append(value.Capabilities, provider.CapabilitySchedulerObserve)
		}},
		{name: "binary path instead of name", mutate: func(value *provider.Descriptor) { value.CandidateBinaries = []string{"/usr/bin/synthetic"} }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := valid
			candidate.Roles = append([]provider.Role(nil), valid.Roles...)
			candidate.Capabilities = append([]provider.Capability(nil), valid.Capabilities...)
			candidate.CandidateBinaries = append([]string(nil), valid.CandidateBinaries...)
			testCase.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() succeeded, want error")
			}
		})
	}

	if _, err := provider.NewRegistry(valid, valid); err == nil {
		t.Fatal("NewRegistry() accepted duplicate names")
	}
}

func TestProviderAndExternalArtifactRolesRemainDistinct(t *testing.T) {
	if provider.RoleArtifactStore != "artifact_store" || provider.ExternalRoleArtifact != "artifact" {
		t.Fatalf("artifact role vocabulary = provider %q, external %q", provider.RoleArtifactStore, provider.ExternalRoleArtifact)
	}
	role, ok := provider.CapabilityArtifactStat.Role()
	if !ok || role != provider.RoleArtifactStore {
		t.Fatalf("artifact capability role = %q, %v", role, ok)
	}
	role, ok = provider.ExternalRoleArtifact.ProviderRole()
	if !ok || role != provider.RoleArtifactStore {
		t.Fatalf("external artifact provider role = %q, %v", role, ok)
	}
}

func TestEffectVocabularyIsExactVersionedAndStable(t *testing.T) {
	want := []provider.Effect{
		"local_read",
		"remote_read",
		"local_write",
		"remote_write",
		"executes_user_code",
		"starts_service",
		"credential_flow",
		"destructive",
		"sensitive_output",
		"blocking",
	}
	if got := provider.AllEffects(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllEffects() = %v, want %v", got, want)
	}
	if provider.EffectVocabularyVersion != "exp.effect/v1" {
		t.Fatalf("EffectVocabularyVersion = %q", provider.EffectVocabularyVersion)
	}

	reversed := append([]provider.Effect(nil), want...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	set, err := provider.NewEffectSet(reversed...)
	if err != nil {
		t.Fatalf("NewEffectSet() error = %v", err)
	}
	if !reflect.DeepEqual(set.Values, want) || set.Version != provider.EffectVocabularyVersion {
		t.Fatalf("effect set = %+v", set)
	}

	if _, err := provider.NewEffectSet(provider.Effect("network_magic")); err == nil {
		t.Fatal("NewEffectSet() accepted unknown effect")
	}
	if _, err := provider.NewEffectSet(provider.EffectLocalRead, provider.EffectLocalRead); err == nil {
		t.Fatal("NewEffectSet() accepted duplicate effect")
	}
	badVersion := set
	badVersion.Version = "exp.effect/v2"
	if err := badVersion.Validate(); err == nil {
		t.Fatal("EffectSet.Validate() accepted wrong version")
	}
}

func TestSupportAndStateFailClosedDuringJSONEncoding(t *testing.T) {
	encoded, err := json.Marshal(struct {
		Support provider.Support         `json:"support"`
		State   provider.NormalizedState `json:"state"`
	}{Support: provider.Support("optimistic"), State: provider.NormalizedState("Done")})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"support":"unknown","state":"unknown"}`; got != want {
		t.Fatalf("fail-closed JSON = %s, want %s", got, want)
	}
	if provider.StateUnknown.Terminal() || !provider.StateSucceeded.Terminal() {
		t.Fatalf("terminal classification is not fail closed")
	}

	roles := provider.AllRoles()
	roles[0] = "corrupt"
	if provider.AllRoles()[0] == "corrupt" {
		t.Fatal("AllRoles() did not return a copy")
	}
	if got := fmt.Sprint(provider.AllNormalizedStates()); got == "" {
		t.Fatal("AllNormalizedStates() returned no values")
	}
}
