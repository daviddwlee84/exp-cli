package workspacebackend

import (
	"errors"
	"fmt"
)

const (
	// DevCLIName reserves the optional local dev-cli integration. Current public
	// releases expose no schema-versioned, path-exact machine lifecycle contract,
	// so every mutating/open capability remains fail-closed and unsupported.
	DevCLIName = "dev_cli"
)

// Capability is one narrow workspace-provider operation.
type Capability string

const (
	CapabilityPrepare Capability = "workspace.prepare"
	CapabilityInspect Capability = "workspace.inspect"
	CapabilityCleanup Capability = "workspace.cleanup"
	CapabilityOpen    Capability = "workspace.open"
	CapabilityHandoff Capability = "workspace.handoff"
	CapabilityRetire  Capability = "workspace.retire"
)

// Support is fail-closed capability support.
type Support string

const (
	SupportSupported   Support = "supported"
	SupportUnsupported Support = "unsupported"
	SupportUnknown     Support = "unknown"
)

// CapabilityStatus reports one provider capability in stable order.
type CapabilityStatus struct {
	Capability Capability `json:"capability"`
	Support    Support    `json:"support"`
}

// Descriptor is immutable compiled provider metadata.
type Descriptor struct {
	Name         string             `json:"name"`
	BuiltIn      bool               `json:"built_in"`
	Binary       string             `json:"binary,omitempty"`
	Capabilities []CapabilityStatus `json:"capabilities"`
}

// ReadinessState is the closed local-readiness vocabulary.
type ReadinessState string

const (
	ReadinessBuiltIn            ReadinessState = "built-in"
	ReadinessMissing            ReadinessState = "missing"
	ReadinessInstalledNotProbed ReadinessState = "installed-not-probed"
	ReadinessReady              ReadinessState = "ready"
	ReadinessMisconfigured      ReadinessState = "misconfigured"
	ReadinessUnsupported        ReadinessState = "unsupported"
	ReadinessUnknown            ReadinessState = "unknown"

	// Compatibility names preserve the source API while emitting the shared
	// readiness vocabulary.
	ReadinessBuiltInNative ReadinessState = ReadinessBuiltIn
	ReadinessCompatible    ReadinessState = ReadinessReady
	ReadinessIncompatible  ReadinessState = ReadinessUnsupported
	ReadinessProbeFailed   ReadinessState = ReadinessUnknown
)

// Readiness contains no provider-local path or catalog identity. binaryPath is
// retained only as an invocation capability and is never serialized.
type Readiness struct {
	Provider     string             `json:"provider"`
	State        ReadinessState     `json:"state"`
	BinaryFound  bool               `json:"binary_found"`
	Probed       bool               `json:"probed"`
	Version      string             `json:"version,omitempty"`
	Capabilities []CapabilityStatus `json:"capabilities"`
	Reason       string             `json:"reason,omitempty"`

	binaryPath string
}

// SelectionOrigin identifies the winning selection tier.
type SelectionOrigin string

const (
	SelectionExplicit   SelectionOrigin = "explicit"
	SelectionRepository SelectionOrigin = "repository_config"
	SelectionUser       SelectionOrigin = "user_config"
	SelectionBuiltin    SelectionOrigin = "builtin"
)

// ProviderSelection is invocation-local policy. It is never canonical record
// data and contains no dev-cli task, catalog, or worktree identifier.
type ProviderSelection struct {
	Requested                string          `json:"requested"`
	Origin                   SelectionOrigin `json:"origin"`
	Trusted                  bool            `json:"trusted"`
	AllowUnavailableFallback bool            `json:"allow_unavailable_fallback"`
	AllowUnsupportedFallback bool            `json:"allow_unsupported_fallback"`
}

// Resolution reports the requested and actual provider for one capability.
type Resolution struct {
	Requested      string          `json:"requested"`
	Actual         string          `json:"actual"`
	Origin         SelectionOrigin `json:"origin"`
	Capability     Capability      `json:"capability"`
	Fallback       bool            `json:"fallback"`
	FallbackReason string          `json:"fallback_reason,omitempty"`
	Readiness      Readiness       `json:"readiness"`
}

var (
	ErrUnknownProvider       = errors.New("unknown workspace provider")
	ErrUnsupportedCapability = errors.New("workspace provider capability is unsupported")
	ErrProviderMissing       = errors.New("workspace provider binary is missing")
	ErrProviderMisconfigured = errors.New("workspace provider is misconfigured")
	ErrProviderIncompatible  = errors.New("workspace provider is incompatible")
	ErrProviderProbeFailed   = errors.New("workspace provider probe failed")
	ErrUntrustedSelection    = errors.New("workspace provider selection is untrusted")
	ErrOperationFailed       = errors.New("workspace provider operation failed")
	ErrPostcondition         = errors.New("workspace provider postcondition failed")
)

// UnsupportedCapabilityError is the typed fail-closed result returned by a
// provider that cannot implement an operation.
type UnsupportedCapabilityError struct {
	Provider   string
	Capability Capability
}

func (failure *UnsupportedCapabilityError) Error() string {
	if failure == nil {
		return ErrUnsupportedCapability.Error()
	}
	return fmt.Sprintf("workspace provider %q does not support %s", failure.Provider, failure.Capability)
}

func (failure *UnsupportedCapabilityError) Unwrap() error { return ErrUnsupportedCapability }

// ReadinessError preserves a typed, path-free readiness observation.
type ReadinessError struct {
	Readiness Readiness
	Err       error
}

func (failure *ReadinessError) Error() string {
	if failure == nil {
		return "workspace provider is not ready"
	}
	return fmt.Sprintf("workspace provider %q readiness is %s", failure.Readiness.Provider, failure.Readiness.State)
}

func (failure *ReadinessError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

// OperationError preserves cancellation/timeout classification while rendering
// no subprocess output, argv, executable, or local path.
type OperationError struct {
	Provider  string
	Operation Capability
	Err       error
}

func (failure *OperationError) Error() string {
	if failure == nil {
		return ErrOperationFailed.Error()
	}
	return fmt.Sprintf("workspace provider %q %s operation failed", failure.Provider, failure.Operation)
}

func (failure *OperationError) Unwrap() error {
	if failure == nil {
		return ErrOperationFailed
	}
	return errors.Join(ErrOperationFailed, failure.Err)
}

// PostconditionError reports only provider and operation; local paths remain
// inside the validator and never enter the rendered error.
type PostconditionError struct {
	Provider  string
	Operation Capability
	Err       error
}

func (failure *PostconditionError) Error() string {
	if failure == nil {
		return ErrPostcondition.Error()
	}
	return fmt.Sprintf("workspace provider %q failed exp-owned %s postcondition", failure.Provider, failure.Operation)
}

func (failure *PostconditionError) Unwrap() error {
	if failure == nil {
		return ErrPostcondition
	}
	return errors.Join(ErrPostcondition, failure.Err)
}

func nativeDescriptor() Descriptor {
	return Descriptor{
		Name: NativeGitName, BuiltIn: true,
		Capabilities: []CapabilityStatus{
			{Capability: CapabilityCleanup, Support: SupportSupported},
			{Capability: CapabilityHandoff, Support: SupportUnsupported},
			{Capability: CapabilityInspect, Support: SupportSupported},
			{Capability: CapabilityOpen, Support: SupportUnsupported},
			{Capability: CapabilityPrepare, Support: SupportSupported},
			{Capability: CapabilityRetire, Support: SupportSupported},
		},
	}
}

func devCLIDescriptor() Descriptor {
	return Descriptor{
		Name: DevCLIName, Binary: "dev",
		Capabilities: []CapabilityStatus{
			{Capability: CapabilityCleanup, Support: SupportUnsupported},
			{Capability: CapabilityHandoff, Support: SupportUnsupported},
			{Capability: CapabilityInspect, Support: SupportUnsupported},
			{Capability: CapabilityOpen, Support: SupportUnsupported},
			{Capability: CapabilityPrepare, Support: SupportUnsupported},
			{Capability: CapabilityRetire, Support: SupportUnsupported},
		},
	}
}

func capabilityStatuses(support Support, capabilities ...Capability) []CapabilityStatus {
	out := make([]CapabilityStatus, len(capabilities))
	for index, capability := range capabilities {
		out[index] = CapabilityStatus{Capability: capability, Support: support}
	}
	return out
}
