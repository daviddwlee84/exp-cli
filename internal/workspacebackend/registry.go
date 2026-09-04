package workspacebackend

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// Registry is the closed workspace-provider registry. Native Git is mandatory
// and remains the inspection/cleanup authority even when an optional provider
// performs a lifecycle handoff.
type Registry struct {
	native Backend
	dev    DevCLI
}

// NewRegistry constructs a registry with a mandatory native correctness
// baseline and one optional dev-cli adapter.
func NewRegistry(native Backend, dev DevCLI) (*Registry, error) {
	if native == nil {
		return nil, errors.New("native Git workspace backend is required")
	}
	return &Registry{native: native, dev: dev}, nil
}

// MustRegistry is the composition-root convenience for a statically valid
// native backend.
func MustRegistry(native Backend, dev DevCLI) *Registry {
	registry, err := NewRegistry(native, dev)
	if err != nil {
		panic(err)
	}
	return registry
}

// WithNative returns a copy using a request-specific native backend (for
// example, a Try-specific private seed store) while retaining dev-cli seams.
func (registry *Registry) WithNative(native Backend) *Registry {
	if registry == nil {
		return MustRegistry(native, DevCLI{})
	}
	return MustRegistry(native, registry.dev)
}

// Descriptors returns stable compiled capability metadata.
func (registry *Registry) Descriptors() []Descriptor {
	out := []Descriptor{nativeDescriptor(), devCLIDescriptor()}
	sort.Slice(out, func(left, right int) bool { return out[left].Name < out[right].Name })
	for index := range out {
		out[index].Capabilities = append([]CapabilityStatus(nil), out[index].Capabilities...)
	}
	return out
}

// Readiness reports one provider without exposing its executable path.
func (registry *Registry) Readiness(ctx context.Context, provider, cwd string, probe bool) (Readiness, error) {
	if registry == nil || registry.native == nil {
		return Readiness{}, errors.New("workspace provider registry is unavailable")
	}
	switch provider {
	case NativeGitName:
		return Readiness{
			Provider: NativeGitName, State: ReadinessBuiltInNative,
			Capabilities: append([]CapabilityStatus(nil), nativeDescriptor().Capabilities...),
		}, nil
	case DevCLIName:
		return registry.dev.Readiness(ctx, cwd, probe)
	default:
		return Readiness{}, fmt.Errorf("workspace provider %q: %w", provider, ErrUnknownProvider)
	}
}

// Statuses returns both providers in stable order. Optional probe failures are
// represented in their readiness row; caller cancellation still aborts.
func (registry *Registry) Statuses(ctx context.Context, cwd string, probe bool) ([]Readiness, error) {
	rows := make([]Readiness, 0, 2)
	for _, descriptor := range registry.Descriptors() {
		status, err := registry.Readiness(ctx, descriptor.Name, cwd, probe)
		if err != nil {
			if ctx != nil && ctx.Err() != nil {
				return rows, ctx.Err()
			}
			var readiness *ReadinessError
			if !errors.As(err, &readiness) {
				return rows, err
			}
		}
		rows = append(rows, publicReadiness(status))
	}
	return rows, nil
}

// Resolve determines the actual provider for one capability after a bounded
// live local probe. Native fallback is explicit in ProviderSelection and never
// masks caller cancellation.
func (registry *Registry) Resolve(ctx context.Context, selection ProviderSelection, capability Capability, cwd string) (Resolution, error) {
	return registry.resolve(ctx, selection, capability, cwd, true)
}

// Preview applies the same selection policy while optionally leaving an
// installed optional binary unprobed for side-effect-free status commands.
func (registry *Registry) Preview(ctx context.Context, selection ProviderSelection, capability Capability, cwd string, probe bool) (Resolution, error) {
	return registry.resolve(ctx, selection, capability, cwd, probe)
}

func (registry *Registry) resolve(ctx context.Context, selection ProviderSelection, capability Capability, cwd string, probe bool) (Resolution, error) {
	if registry == nil || registry.native == nil {
		return Resolution{}, errors.New("workspace provider registry is unavailable")
	}
	if selection.Requested == "" {
		selection = nativeSelection()
	}
	resolution := Resolution{
		Requested: selection.Requested, Actual: selection.Requested,
		Origin: selection.Origin, Capability: capability,
	}
	if !selection.Trusted {
		return resolution, ErrUntrustedSelection
	}
	if !knownProvider(selection.Requested) {
		return resolution, fmt.Errorf("workspace provider %q: %w", selection.Requested, ErrUnknownProvider)
	}
	if selection.Requested == NativeGitName {
		status, err := registry.Readiness(ctx, NativeGitName, cwd, false)
		resolution.Readiness = status
		if err != nil {
			return resolution, err
		}
		if supportFor(nativeDescriptor(), capability) != SupportSupported {
			return resolution, &UnsupportedCapabilityError{Provider: NativeGitName, Capability: capability}
		}
		return resolution, nil
	}

	support := supportFor(devCLIDescriptor(), capability)
	if support != SupportSupported {
		// Compiled capability support is authoritative and needs no subprocess
		// probe. Perform discovery only so status output remains informative.
		status, discoveryErr := registry.Readiness(ctx, DevCLIName, cwd, false)
		resolution.Readiness = status
		if discoveryErr != nil {
			return resolution, discoveryErr
		}
		if selection.AllowUnsupportedFallback {
			if supportFor(nativeDescriptor(), capability) != SupportSupported {
				return resolution, &UnsupportedCapabilityError{Provider: NativeGitName, Capability: capability}
			}
			resolution.Actual = NativeGitName
			resolution.Fallback = true
			resolution.FallbackReason = "capability-unsupported"
			return resolution, nil
		}
		return resolution, &UnsupportedCapabilityError{Provider: DevCLIName, Capability: capability}
	}

	status, probeErr := registry.Readiness(ctx, DevCLIName, cwd, probe)
	resolution.Readiness = status
	if ctx != nil && ctx.Err() != nil {
		return resolution, ctx.Err()
	}
	if probeErr != nil || status.State != ReadinessCompatible && !(status.State == ReadinessInstalledNotProbed && !probe) {
		if selection.AllowUnavailableFallback {
			if supportFor(nativeDescriptor(), capability) != SupportSupported {
				return resolution, &UnsupportedCapabilityError{Provider: NativeGitName, Capability: capability}
			}
			resolution.Actual = NativeGitName
			resolution.Fallback = true
			resolution.FallbackReason = readinessFallbackReason(status.State)
			return resolution, nil
		}
		if probeErr != nil {
			return resolution, probeErr
		}
		return resolution, readinessStateError(status)
	}

	return resolution, nil
}

// Prepare resolves capability policy and then invokes only the actual provider.
// With today's dev-cli contract the only successful actual provider is native.
func (registry *Registry) Prepare(ctx context.Context, request PrepareRequest) (Workspace, error) {
	resolution, err := registry.Resolve(ctx, request.Provider, CapabilityPrepare, request.RepositoryRoot)
	if err != nil {
		return Workspace{}, err
	}
	var workspace Workspace
	switch resolution.Actual {
	case NativeGitName:
		workspace, err = registry.native.Prepare(ctx, request)
	case DevCLIName:
		workspace, err = registry.dev.Prepare(ctx, request)
	default:
		return Workspace{}, ErrUnknownProvider
	}
	if err != nil {
		return workspace, err
	}
	inspection, inspectErr := registry.native.Inspect(ctx, request)
	if inspectErr != nil || workspace.Backend != resolution.Actual || !reflect.DeepEqual(workspace, inspection.Workspace) ||
		inspection.HeadCommit != request.Snapshot.HeadCommit {
		return workspace, &PostconditionError{Provider: resolution.Actual, Operation: CapabilityPrepare, Err: inspectErr}
	}
	return inspection.Workspace, nil
}

// RollbackPreparation delegates only to the native exact-identity authority.
func (registry *Registry) RollbackPreparation(ctx context.Context, request PrepareRequest) error {
	if registry == nil || registry.native == nil {
		return errors.New("native Git workspace backend is unavailable")
	}
	rollback, ok := registry.native.(PreparationRollbacker)
	if !ok {
		return errors.New("native Git workspace backend does not support preparation rollback")
	}
	return rollback.RollbackPreparation(ctx, request)
}

// VerifyPrepared always uses exp's native byte authority. Optional providers do
// not get to attest checkout content or substitute provider-local identity.
func (registry *Registry) VerifyPrepared(ctx context.Context, request PrepareRequest) (Workspace, error) {
	if registry == nil || registry.native == nil {
		return Workspace{}, errors.New("native Git workspace backend is unavailable")
	}
	return registry.native.VerifyPrepared(ctx, request)
}

// Inspect always uses exp's native Git authority. Provider-local catalogs and
// identifiers are never accepted as canonical workspace evidence.
func (registry *Registry) Inspect(ctx context.Context, request PrepareRequest) (Inspection, error) {
	if registry == nil || registry.native == nil {
		return Inspection{}, errors.New("native Git workspace backend is unavailable")
	}
	return registry.native.Inspect(ctx, request)
}

// CleanupOrHandoff keeps cleanup native. Handoff may use dev-cli only after its
// local probe and is revalidated through the native backend afterward.
func (registry *Registry) CleanupOrHandoff(ctx context.Context, request PrepareRequest, action FinalizeAction) (Inspection, error) {
	if registry == nil || registry.native == nil {
		return Inspection{}, errors.New("native Git workspace backend is unavailable")
	}
	if action != FinalizeHandoff {
		return registry.native.CleanupOrHandoff(ctx, request, action)
	}
	resolution, err := registry.Resolve(ctx, request.Provider, CapabilityHandoff, request.RepositoryRoot)
	if err != nil {
		return Inspection{}, err
	}
	if resolution.Actual == DevCLIName {
		return registry.dev.openWithReadiness(ctx, registry.native, request, resolution.Readiness)
	}
	inspection, err := registry.native.CleanupOrHandoff(ctx, request, FinalizeHandoff)
	if err == nil {
		inspection.HandoffProvider = NativeGitName
	}
	return inspection, err
}

// Retire explicitly delegates guarded clean-worktree retirement when selected;
// normal Backend cleanup remains native and preserves dirty-seed semantics.
func (registry *Registry) Retire(ctx context.Context, request PrepareRequest) (Inspection, error) {
	resolution, err := registry.Resolve(ctx, request.Provider, CapabilityRetire, request.RepositoryRoot)
	if err != nil {
		return Inspection{}, err
	}
	if resolution.Actual == NativeGitName {
		return registry.native.CleanupOrHandoff(ctx, request, FinalizeCleanup)
	}
	return registry.dev.retireWithReadiness(ctx, registry.native, request, resolution.Readiness)
}

func supportFor(descriptor Descriptor, capability Capability) Support {
	for _, candidate := range descriptor.Capabilities {
		if candidate.Capability == capability {
			return candidate.Support
		}
	}
	return SupportUnsupported
}

func publicReadiness(status Readiness) Readiness {
	status.binaryPath = ""
	status.Capabilities = append([]CapabilityStatus(nil), status.Capabilities...)
	return status
}

func readinessFallbackReason(state ReadinessState) string {
	switch state {
	case ReadinessMissing:
		return "provider-missing"
	case ReadinessMisconfigured:
		return "provider-misconfigured"
	case ReadinessUnsupported:
		return "provider-unsupported"
	case ReadinessUnknown:
		return "provider-unknown"
	default:
		return "provider-unavailable"
	}
}

var _ Backend = (*Registry)(nil)
