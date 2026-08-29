// Package provider defines the compiled, role-specific boundary between exp's
// canonical research records and optional upstream tools. Descriptors and local
// probes expose capability metadata only; provider behavior is implemented one
// reviewed capability at a time.
package provider

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// AdapterContractVersion is the descriptor contract understood by this build.
const AdapterContractVersion = "exp.provider/v1"

// ProviderName is a compiled provider identifier.
type ProviderName string

const (
	ProviderUnknown ProviderName = "unknown"
	ProviderDirect  ProviderName = "direct"
	ProviderPueue   ProviderName = "pueue"
	ProviderMLflow  ProviderName = "mlflow"
	ProviderDVC     ProviderName = "dvc"
	ProviderSlurm   ProviderName = "slurm"
	ProviderMarimo  ProviderName = "marimo"
	ProviderJupyter ProviderName = "jupyter"
)

// Role is one narrow provider responsibility. Canonical ExternalRef roles are
// a distinct vocabulary because their artifact wire value is "artifact".
type Role string

const (
	RoleRunner        Role = "runner"
	RoleScheduler     Role = "scheduler"
	RoleTracker       Role = "tracker"
	RoleArtifactStore Role = "artifact_store"
	RoleRegistry      Role = "registry"
)

var allRoles = []Role{
	RoleRunner,
	RoleScheduler,
	RoleTracker,
	RoleArtifactStore,
	RoleRegistry,
}

// AllRoles returns the closed role vocabulary in stable contract order.
func AllRoles() []Role { return append([]Role(nil), allRoles...) }

// Valid reports whether r belongs to the v1 role vocabulary.
func (r Role) Valid() bool {
	for _, known := range allRoles {
		if r == known {
			return true
		}
	}
	return false
}

// Support is fail-closed capability support.
type Support string

const (
	SupportSupported   Support = "supported"
	SupportUnsupported Support = "unsupported"
	SupportUnknown     Support = "unknown"
)

// Valid reports whether s is one of the three support values.
func (s Support) Valid() bool {
	return s == SupportSupported || s == SupportUnsupported || s == SupportUnknown
}

// Canonical returns s when valid and unknown otherwise.
func (s Support) Canonical() Support {
	if s.Valid() {
		return s
	}
	return SupportUnknown
}

func (s Support) String() string { return string(s.Canonical()) }

// MarshalJSON prevents a native/unknown token from entering normalized output.
func (s Support) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON fails closed. Callers that need the original native token must
// retain it in a dedicated NativeSupport field before normalization.
func (s *Support) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed := Support(value)
	*s = parsed.Canonical()
	return nil
}

// Capability is a role-qualified operation name.
type Capability string

const (
	CapabilityRunnerPrepare    Capability = "runner.prepare"
	CapabilitySchedulerSubmit  Capability = "scheduler.submit"
	CapabilitySchedulerObserve Capability = "scheduler.observe"
	CapabilitySchedulerCancel  Capability = "scheduler.cancel"
	CapabilityTrackerResolve   Capability = "tracker.resolve"
	CapabilityTrackerList      Capability = "tracker.list"
	CapabilityArtifactStat     Capability = "artifact.stat"
	CapabilityArtifactList     Capability = "artifact.list"
	CapabilityRegistryGet      Capability = "registry.get"
	CapabilityRegistryList     Capability = "registry.list"
	CapabilityRegistryResolve  Capability = "registry.resolve"
)

// Role returns the capability's declared role and whether its syntax is valid.
func (c Capability) Role() (Role, bool) {
	prefix, operation, found := strings.Cut(string(c), ".")
	if !found || strings.Contains(operation, ".") || !validSlug(prefix) || !validSlug(operation) {
		return "", false
	}
	role := Role(prefix)
	if prefix == "artifact" {
		role = RoleArtifactStore
	}
	return role, role.Valid()
}

// Valid reports whether c is a syntactically valid v1 role capability.
func (c Capability) Valid() bool {
	_, ok := c.Role()
	return ok
}

// Descriptor is static compiled provider metadata. Slices are normalized and
// copied when inserted into a Registry.
type Descriptor struct {
	Name              ProviderName `json:"name"`
	Roles             []Role       `json:"roles"`
	CandidateBinaries []string     `json:"candidate_binaries"`
	Capabilities      []Capability `json:"capabilities"`
	ContractVersion   string       `json:"contract_version"`
}

// Validate verifies descriptor identity, closed roles, role-qualified unique
// capabilities, candidate binary basenames, and the exact adapter version.
func (d Descriptor) Validate() error {
	if !validSlug(string(d.Name)) || d.Name == ProviderUnknown {
		return fmt.Errorf("invalid provider name")
	}
	if d.ContractVersion != AdapterContractVersion {
		return fmt.Errorf("provider %q uses unsupported contract version", d.Name)
	}
	if len(d.Roles) == 0 {
		return fmt.Errorf("provider %q declares no roles", d.Name)
	}
	roles := make(map[Role]struct{}, len(d.Roles))
	for _, role := range d.Roles {
		if !role.Valid() {
			return fmt.Errorf("provider %q declares an invalid role", d.Name)
		}
		if _, duplicate := roles[role]; duplicate {
			return fmt.Errorf("provider %q declares a duplicate role", d.Name)
		}
		roles[role] = struct{}{}
	}
	if len(d.Capabilities) == 0 {
		return fmt.Errorf("provider %q declares no capabilities", d.Name)
	}
	capabilities := make(map[Capability]struct{}, len(d.Capabilities))
	for _, capability := range d.Capabilities {
		role, valid := capability.Role()
		if !valid {
			return fmt.Errorf("provider %q declares an invalid capability", d.Name)
		}
		if _, declared := roles[role]; !declared {
			return fmt.Errorf("provider %q capability %q has an undeclared role", d.Name, capability)
		}
		if _, duplicate := capabilities[capability]; duplicate {
			return fmt.Errorf("provider %q declares a duplicate capability", d.Name)
		}
		capabilities[capability] = struct{}{}
	}
	binaries := make(map[string]struct{}, len(d.CandidateBinaries))
	for _, binary := range d.CandidateBinaries {
		if !validBinaryName(binary) {
			return fmt.Errorf("provider %q declares an invalid candidate binary", d.Name)
		}
		if _, duplicate := binaries[binary]; duplicate {
			return fmt.Errorf("provider %q declares a duplicate candidate binary", d.Name)
		}
		binaries[binary] = struct{}{}
	}
	return nil
}

// HasRole reports whether the descriptor implements role.
func (d Descriptor) HasRole(role Role) bool {
	for _, candidate := range d.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

// HasCapability reports whether capability is declared by the descriptor.
func (d Descriptor) HasCapability(capability Capability) bool {
	for _, candidate := range d.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func (d Descriptor) normalized() Descriptor {
	out := d
	out.Roles = append([]Role(nil), d.Roles...)
	out.CandidateBinaries = append([]string(nil), d.CandidateBinaries...)
	out.Capabilities = append([]Capability(nil), d.Capabilities...)
	sort.Slice(out.Roles, func(i, j int) bool { return out.Roles[i] < out.Roles[j] })
	sort.Strings(out.CandidateBinaries)
	sort.Slice(out.Capabilities, func(i, j int) bool { return out.Capabilities[i] < out.Capabilities[j] })
	if out.Roles == nil {
		out.Roles = []Role{}
	}
	if out.CandidateBinaries == nil {
		out.CandidateBinaries = []string{}
	}
	if out.Capabilities == nil {
		out.Capabilities = []Capability{}
	}
	return out
}

func validBinaryName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return false
	}
	if strings.ContainsAny(name, "/\\\\\x00\r\n\t ") {
		return false
	}
	return true
}

func validSlug(value string) bool {
	if value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			previousHyphen = false
		case r == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return true
}
