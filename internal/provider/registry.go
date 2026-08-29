package provider

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

// BinaryLookup is the only executable-discovery primitive used by local
// discovery. The default is exec.LookPath; tests and command wiring can inject
// a side-effect-free lookup.
type BinaryLookup func(string) (string, error)

// LocalVersionResult is returned by a caller-supplied, explicitly local-safe
// probe. The registry itself never chooses argv or invokes a process.
type LocalVersionResult struct {
	Version      string                 `json:"version,omitempty"`
	Capabilities map[Capability]Support `json:"capabilities,omitempty"`
	Diagnostics  []Diagnostic           `json:"diagnostics,omitempty"`
}

// LocalVersionProbe may inspect only the resolved local executable/version.
// Implementations must not contact a daemon or network, install/upgrade
// anything, start a service, execute user code, or initiate authentication.
type LocalVersionProbe func(context.Context, Descriptor, string) (LocalVersionResult, error)

// LocalDiscoveryOptions defines a strictly local registry observation.
type LocalDiscoveryOptions struct {
	Context         ContextName
	Lookup          BinaryLookup
	BinaryOverrides map[ProviderName]string
	VersionProbe    LocalVersionProbe
	Now             func() time.Time
	Redaction       RedactionPolicy
}

// Registry is an immutable, compiled descriptor index. Its methods return
// defensive copies.
type Registry struct {
	ordered []Descriptor
	byName  map[ProviderName]Descriptor
}

// NewRegistry validates, copies, normalizes, and stably sorts descriptors.
func NewRegistry(descriptors ...Descriptor) (*Registry, error) {
	registry := &Registry{byName: make(map[ProviderName]Descriptor, len(descriptors))}
	for _, descriptor := range descriptors {
		if err := descriptor.Validate(); err != nil {
			return nil, err
		}
		descriptor = descriptor.normalized()
		if _, duplicate := registry.byName[descriptor.Name]; duplicate {
			return nil, fmt.Errorf("duplicate provider descriptor %q", descriptor.Name)
		}
		registry.byName[descriptor.Name] = descriptor
		registry.ordered = append(registry.ordered, descriptor)
	}
	sort.Slice(registry.ordered, func(i, j int) bool {
		return registry.ordered[i].Name < registry.ordered[j].Name
	})
	return registry, nil
}

// CompiledRegistry returns the seven provider descriptors approved for this
// milestone. It contains metadata and local discovery only, not provider
// operations or daemon/remote probes.
func CompiledRegistry() *Registry {
	registry, err := NewRegistry(compiledDescriptors()...)
	if err != nil {
		panic("invalid compiled provider registry: " + err.Error())
	}
	return registry
}

// List returns stable name order and defensive descriptor copies.
func (r *Registry) List() []Descriptor {
	if r == nil {
		return []Descriptor{}
	}
	out := make([]Descriptor, len(r.ordered))
	for index, descriptor := range r.ordered {
		out[index] = descriptor.normalized()
	}
	return out
}

// Get returns one defensive descriptor copy.
func (r *Registry) Get(name ProviderName) (Descriptor, bool) {
	if r == nil {
		return Descriptor{}, false
	}
	descriptor, ok := r.byName[name]
	if !ok {
		return Descriptor{}, false
	}
	return descriptor.normalized(), true
}

// Names returns compiled provider names in stable bytewise order.
func (r *Registry) Names() []ProviderName {
	descriptors := r.List()
	names := make([]ProviderName, len(descriptors))
	for index, descriptor := range descriptors {
		names[index] = descriptor.Name
	}
	return names
}

// DiscoverLocal performs executable lookup and, only when explicitly supplied,
// a caller-owned local-safe version probe. Missing optional binaries and probe
// failures become unknown support plus diagnostics, not a registry error.
func (r *Registry) DiscoverLocal(ctx context.Context, options LocalDiscoveryOptions) ([]ProbeResult, error) {
	if r == nil {
		return nil, fmt.Errorf("provider registry is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	contextName := options.Context
	if contextName == "" {
		contextName = ContextName("local")
	}
	if !validSlug(string(contextName)) {
		return nil, fmt.Errorf("local discovery context is invalid")
	}
	policy, err := options.Redaction.effective()
	if err != nil {
		return nil, err
	}
	overrideNames := make([]ProviderName, 0, len(options.BinaryOverrides))
	for name := range options.BinaryOverrides {
		overrideNames = append(overrideNames, name)
	}
	sort.Slice(overrideNames, func(i, j int) bool { return overrideNames[i] < overrideNames[j] })
	for _, name := range overrideNames {
		if _, compiled := r.Get(name); !compiled {
			return nil, fmt.Errorf("binary override names an uncompiled provider")
		}
	}
	lookup := options.Lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	observedAt := now().UTC()
	if observedAt.IsZero() {
		return nil, fmt.Errorf("local discovery clock returned zero time")
	}

	results := make([]ProbeResult, 0, len(r.ordered))
	for _, descriptor := range r.ordered {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		override, hasOverride := options.BinaryOverrides[descriptor.Name]
		result := r.discoverDescriptor(ctx, descriptor, contextName, observedAt, lookup, override, hasOverride, options.VersionProbe, policy)
		// Optional discovery failures are diagnostics only while the parent is
		// live. Caller cancellation and deadlines always win, including while the
		// final descriptor is being discovered.
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if err := result.Validate(descriptor); err != nil {
			return results, fmt.Errorf("normalize local provider probe: %w", err)
		}
		results = append(results, result)
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

func (r *Registry) discoverDescriptor(
	ctx context.Context,
	descriptor Descriptor,
	contextName ContextName,
	observedAt time.Time,
	lookup BinaryLookup,
	binaryOverride string,
	hasBinaryOverride bool,
	versionProbe LocalVersionProbe,
	policy RedactionPolicy,
) ProbeResult {
	result := ProbeResult{
		Provider:     descriptor.Name,
		Context:      contextName,
		ObservedAt:   observedAt,
		Capabilities: make([]CapabilityResult, len(descriptor.Capabilities)),
		Diagnostics:  []Diagnostic{},
	}
	for index, capability := range descriptor.Capabilities {
		result.Capabilities[index] = CapabilityResult{Capability: capability, Support: SupportUnknown}
	}

	if len(descriptor.CandidateBinaries) == 0 {
		if hasBinaryOverride {
			result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
				SeverityWarning,
				"binary_override_ignored",
				"built-in provider does not accept a binary override",
				"",
				policy,
			))
		}
		result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
			SeverityInfo,
			"binary_not_required",
			"provider is built in; operation support is not probed in this milestone",
			"",
			policy,
		))
		return normalizedProbeResult(result)
	}

	resolved := ""
	if hasBinaryOverride {
		if binaryOverride != "" && filepath.IsAbs(binaryOverride) && filepath.Clean(binaryOverride) == binaryOverride && !hasControl(binaryOverride) {
			resolved = binaryOverride
		}
		if resolved == "" {
			result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
				SeverityWarning,
				"invalid_binary_override",
				"explicit provider binary override is not a clean absolute path; capabilities remain unknown",
				"",
				policy,
			))
			return normalizedProbeResult(result)
		}
	} else {
		for _, candidate := range descriptor.CandidateBinaries {
			path, err := lookup(candidate)
			if err != nil || path == "" {
				continue
			}
			if !filepath.IsAbs(path) {
				path, err = filepath.Abs(path)
				if err != nil {
					continue
				}
			}
			path = filepath.Clean(path)
			if hasControl(path) {
				continue
			}
			resolved = path
			break
		}
		if resolved == "" {
			result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
				SeverityWarning,
				"optional_binary_missing",
				"optional provider binary is not available; capabilities remain unknown",
				strings.Join(descriptor.CandidateBinaries, ","),
				policy,
			))
			return normalizedProbeResult(result)
		}
	}
	resolvedDisplay, sanitizeErr := sanitizeTextStrict(resolved, policy)
	if sanitizeErr != nil {
		result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
			SeverityWarning,
			"binary_path_omitted",
			"resolved provider binary path exceeded the redaction boundary; capabilities remain unknown",
			"",
			policy,
		))
		return normalizedProbeResult(result)
	}
	result.ResolvedBinaryPath = resolvedDisplay

	if versionProbe == nil {
		result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
			SeverityInfo,
			"version_probe_not_configured",
			"binary was found but no caller-supplied local-safe version probe was configured",
			"",
			policy,
		))
		return normalizedProbeResult(result)
	}

	versionResult, err := versionProbe(ctx, descriptor.normalized(), resolved)
	if err != nil {
		native, sanitizeErr := sanitizeSingleLine(err.Error(), policy)
		if sanitizeErr != nil {
			native = "version probe error was omitted by redaction policy"
		}
		result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
			SeverityWarning,
			"version_probe_failed",
			"local-safe version probe was inconclusive; capabilities remain unknown",
			native,
			policy,
		))
		return normalizedProbeResult(result)
	}

	if versionResult.Version != "" {
		version, sanitizeErr := sanitizeSingleLine(versionResult.Version, policy)
		if sanitizeErr == nil {
			result.ProviderVersion = version
		} else {
			result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
				SeverityWarning,
				"version_omitted",
				"provider version exceeded the redaction boundary",
				"",
				policy,
			))
		}
	}

	knownIndexes := make(map[Capability]int, len(result.Capabilities))
	for index, capability := range result.Capabilities {
		knownIndexes[capability.Capability] = index
	}
	provided := make([]Capability, 0, len(versionResult.Capabilities))
	for capability := range versionResult.Capabilities {
		provided = append(provided, capability)
	}
	sort.Slice(provided, func(i, j int) bool { return provided[i] < provided[j] })
	for _, capability := range provided {
		support := versionResult.Capabilities[capability]
		index, known := knownIndexes[capability]
		if !known {
			result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
				SeverityWarning,
				"unknown_probe_capability",
				"local-safe version probe returned an undeclared capability",
				string(capability),
				policy,
			))
			continue
		}
		if !support.Valid() {
			native, sanitizeErr := sanitizeSingleLine(string(support), policy)
			if sanitizeErr != nil {
				native = ""
			}
			result.Capabilities[index].Support = SupportUnknown
			result.Capabilities[index].NativeSupport = native
			result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
				SeverityWarning,
				"unknown_support_value",
				"local-safe version probe returned an unknown support value",
				native,
				policy,
			))
			continue
		}
		result.Capabilities[index].Support = support
	}

	for _, diagnostic := range versionResult.Diagnostics {
		safe, sanitizeErr := sanitizeDiagnostic(diagnostic, policy)
		if sanitizeErr != nil {
			result.Diagnostics = append(result.Diagnostics, mustDiagnostic(
				SeverityWarning,
				"diagnostic_omitted",
				"a provider diagnostic exceeded the redaction boundary",
				"",
				policy,
			))
			continue
		}
		result.Diagnostics = append(result.Diagnostics, safe)
	}
	return normalizedProbeResult(result)
}

func normalizedProbeResult(result ProbeResult) ProbeResult {
	sort.Slice(result.Capabilities, func(i, j int) bool {
		return result.Capabilities[i].Capability < result.Capabilities[j].Capability
	})
	sortDiagnostics(result.Diagnostics)
	if result.Capabilities == nil {
		result.Capabilities = []CapabilityResult{}
	}
	if result.Diagnostics == nil {
		result.Diagnostics = []Diagnostic{}
	}
	return result
}

func mustDiagnostic(severity DiagnosticSeverity, code, message, native string, policy RedactionPolicy) Diagnostic {
	diagnostic, err := NewDiagnostic(severity, code, message, native, policy)
	if err == nil {
		return diagnostic
	}
	return Diagnostic{
		Severity: SeverityWarning,
		Code:     "diagnostic_omitted",
		Message:  "provider diagnostic was omitted by redaction policy",
	}
}

func compiledRoles(name ProviderName) []Role {
	canonical, known := research.KnownProviderRoles(string(name))
	if !known {
		panic("compiled provider is absent from canonical provider-role matrix: " + string(name))
	}
	roles := make([]Role, 0, len(canonical))
	for _, canonicalRole := range canonical {
		role := Role(canonicalRole)
		if canonicalRole == research.ExternalArtifact {
			role = RoleArtifactStore
		}
		if !role.Valid() {
			panic("canonical provider-role matrix contains an unsupported role: " + string(canonicalRole))
		}
		roles = append(roles, role)
	}
	return roles
}

func compiledDescriptors() []Descriptor {
	return []Descriptor{
		{
			Name:            ProviderDirect,
			Roles:           compiledRoles(ProviderDirect),
			Capabilities:    []Capability{CapabilityRunnerPrepare, CapabilitySchedulerSubmit, CapabilitySchedulerObserve, CapabilitySchedulerCancel},
			ContractVersion: AdapterContractVersion,
		},
		{
			Name:              ProviderPueue,
			Roles:             compiledRoles(ProviderPueue),
			CandidateBinaries: []string{"pueue"},
			Capabilities:      []Capability{CapabilitySchedulerSubmit, CapabilitySchedulerObserve, CapabilitySchedulerCancel},
			ContractVersion:   AdapterContractVersion,
		},
		{
			Name:              ProviderMLflow,
			Roles:             compiledRoles(ProviderMLflow),
			CandidateBinaries: []string{"mlflow"},
			Capabilities: []Capability{
				CapabilityRunnerPrepare,
				CapabilityTrackerResolve,
				CapabilityTrackerList,
				CapabilityArtifactStat,
				CapabilityArtifactList,
				CapabilityRegistryGet,
				CapabilityRegistryList,
				CapabilityRegistryResolve,
			},
			ContractVersion: AdapterContractVersion,
		},
		{
			Name:              ProviderDVC,
			Roles:             compiledRoles(ProviderDVC),
			CandidateBinaries: []string{"dvc"},
			Capabilities: []Capability{
				CapabilityRunnerPrepare,
				CapabilitySchedulerSubmit,
				CapabilitySchedulerObserve,
				CapabilitySchedulerCancel,
				CapabilityArtifactStat,
				CapabilityArtifactList,
			},
			ContractVersion: AdapterContractVersion,
		},
		{
			Name:              ProviderSlurm,
			Roles:             compiledRoles(ProviderSlurm),
			CandidateBinaries: []string{"squeue", "sacct", "sbatch", "scancel"},
			Capabilities:      []Capability{CapabilitySchedulerSubmit, CapabilitySchedulerObserve, CapabilitySchedulerCancel},
			ContractVersion:   AdapterContractVersion,
		},
		{
			Name:              ProviderMarimo,
			Roles:             compiledRoles(ProviderMarimo),
			CandidateBinaries: []string{"marimo"},
			Capabilities:      []Capability{CapabilityRunnerPrepare},
			ContractVersion:   AdapterContractVersion,
		},
		{
			Name:              ProviderJupyter,
			Roles:             compiledRoles(ProviderJupyter),
			CandidateBinaries: []string{"jupyter"},
			Capabilities:      []Capability{CapabilityRunnerPrepare},
			ContractVersion:   AdapterContractVersion,
		},
	}
}
