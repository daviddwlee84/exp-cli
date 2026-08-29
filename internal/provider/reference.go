package provider

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/safex"
)

const MaxExternalRefBytes = research.MaxExternalRefBytes

// ContextName is a configured, non-secret provider context identifier.
type ContextName string

// NativeKind identifies a provider-owned resource kind.
type NativeKind string

// NativeID is an immutable or provider-scoped resource identity.
type NativeID string

// ExternalRole is the canonical reference-owner vocabulary.
type ExternalRole string

const (
	ExternalRoleRunner    ExternalRole = "runner"
	ExternalRoleScheduler ExternalRole = "scheduler"
	ExternalRoleTracker   ExternalRole = "tracker"
	ExternalRoleArtifact  ExternalRole = "artifact"
	ExternalRoleRegistry  ExternalRole = "registry"
)

// Valid reports whether role belongs to the canonical reference vocabulary.
func (r ExternalRole) Valid() bool {
	_, valid := r.ProviderRole()
	return valid
}

// ProviderRole maps a canonical reference owner to its provider role.
func (r ExternalRole) ProviderRole() (Role, bool) {
	switch r {
	case ExternalRoleRunner:
		return RoleRunner, true
	case ExternalRoleScheduler:
		return RoleScheduler, true
	case ExternalRoleTracker:
		return RoleTracker, true
	case ExternalRoleArtifact:
		return RoleArtifactStore, true
	case ExternalRoleRegistry:
		return RoleRegistry, true
	default:
		return "", false
	}
}

// ExternalRef is the typed canonical identity of provider-owned state. URI is
// optional but, when present, must already be canonical-safe; validation never
// silently stores a partially redacted credential-bearing input.
type ExternalRef struct {
	Role       ExternalRole   `json:"role"`
	Provider   ProviderName   `json:"provider"`
	Context    ContextName    `json:"context"`
	NativeKind NativeKind     `json:"native_kind"`
	NativeID   NativeID       `json:"native_id"`
	URI        string         `json:"uri,omitempty"`
	ObservedAt *time.Time     `json:"observed_at,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type externalRefJSON ExternalRef

// NewExternalRef validates and defensively copies a canonical reference.
func NewExternalRef(reference ExternalRef) (ExternalRef, error) {
	out, err := reference.normalizedCopy()
	if err != nil {
		return ExternalRef{}, err
	}
	if err := out.Validate(); err != nil {
		return ExternalRef{}, err
	}
	return out, nil
}

// Validate enforces typed identity, canonical URI privacy, UTC observation
// time, provider-namespaced metadata, byte limits, and known provider roles.
// Syntactically valid future providers remain forward compatible.
func (r ExternalRef) Validate() error {
	return r.ValidateWithRegistry(CompiledRegistry())
}

func (r ExternalRef) validateStructure() error {
	if !r.Role.Valid() {
		return fmt.Errorf("external reference has invalid role")
	}
	if !validSlug(string(r.Provider)) || r.Provider == ProviderUnknown {
		return fmt.Errorf("external reference has invalid provider")
	}
	if known, supported := research.KnownProviderSupportsRole(string(r.Provider), research.ExternalRole(r.Role)); known && !supported {
		return fmt.Errorf("external reference role is not supported by the known provider")
	}
	if !validSlug(string(r.Context)) {
		return fmt.Errorf("external reference has invalid context")
	}
	if !validSlug(string(r.NativeKind)) {
		return fmt.Errorf("external reference has invalid native kind")
	}
	if err := validateCanonicalText(string(r.NativeID)); err != nil {
		return fmt.Errorf("external reference has invalid native id")
	}
	if err := ValidateCanonicalURI(r.URI); err != nil {
		return err
	}
	if r.ObservedAt != nil {
		_, offset := r.ObservedAt.Zone()
		if r.ObservedAt.IsZero() || offset != 0 {
			return fmt.Errorf("external reference observed_at must be non-zero UTC")
		}
	}
	for key := range r.Metadata {
		if !research.ValidExternalRefMetadataKey(string(r.Provider), key) {
			return fmt.Errorf("external reference metadata key is not provider-namespaced")
		}
		if safex.SensitiveName(key) {
			return fmt.Errorf("external reference metadata contains a credential-bearing key")
		}
	}
	normalized, err := r.normalizedCopy()
	if err != nil {
		return err
	}
	if r.Metadata != nil {
		safeMetadata, err := sanitizeExternalRefMetadata(normalized.Metadata, externalRefRedactionPolicy())
		if err != nil {
			return fmt.Errorf("external reference metadata is unsafe")
		}
		if !reflect.DeepEqual(normalized.Metadata, safeMetadata) {
			return fmt.Errorf("external reference metadata is not structurally sanitized")
		}
	}
	encoded, err := json.Marshal(externalRefJSON(normalized))
	if err != nil || len(encoded) > MaxExternalRefBytes {
		return fmt.Errorf("external reference exceeds byte bound")
	}
	return nil
}

func (r ExternalRef) normalizedCopy() (ExternalRef, error) {
	out := r
	if r.ObservedAt != nil {
		observed := *r.ObservedAt
		out.ObservedAt = &observed
	}
	if r.Metadata == nil {
		return out, nil
	}
	normalized, err := normalizeJSONValue(r.Metadata)
	if err != nil {
		return ExternalRef{}, fmt.Errorf("external reference metadata is not JSON-compatible")
	}
	metadata, ok := normalized.(map[string]any)
	if !ok {
		return ExternalRef{}, fmt.Errorf("external reference metadata is not an object")
	}
	if err := validateExternalMetadataShape(metadata, 0); err != nil {
		return ExternalRef{}, err
	}
	out.Metadata = metadata
	return out, nil
}

func validateExternalMetadataShape(value any, depth int) error {
	if depth > 32 {
		return fmt.Errorf("external reference metadata exceeds maximum nesting depth")
	}
	switch typed := value.(type) {
	case nil:
		return fmt.Errorf("external reference metadata contains null")
	case bool, string, int64, float64:
		return nil
	case []any:
		for _, item := range typed {
			if err := validateExternalMetadataShape(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, item := range typed {
			if err := validateExternalMetadataShape(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("external reference metadata is not in the JSON/TOML common subset")
	}
}

func externalRefRedactionPolicy() RedactionPolicy {
	return RedactionPolicy{MaxTextBytes: MaxExternalRefBytes, MaxRawBytes: MaxExternalRefBytes}
}

// ValidateWithRegistry enforces known provider roles while preserving
// syntactically valid future providers that are absent from the registry.
func (r ExternalRef) ValidateWithRegistry(registry *Registry) error {
	if err := r.validateStructure(); err != nil {
		return err
	}
	if registry == nil {
		return fmt.Errorf("provider registry is required")
	}
	descriptor, compiled := registry.Get(r.Provider)
	providerRole, valid := r.Role.ProviderRole()
	if !valid {
		return fmt.Errorf("external reference has invalid role")
	}
	if compiled {
		if !descriptor.HasRole(providerRole) {
			return fmt.Errorf("external reference role is not declared by provider")
		}
		return nil
	}
	if known, _ := research.KnownProviderSupportsRole(string(r.Provider), research.ExternalRole(r.Role)); known {
		return fmt.Errorf("external reference known provider is absent from registry")
	}
	return nil
}

// MarshalJSON refuses unsafe canonical references rather than serializing raw
// credential-bearing data.
func (r ExternalRef) MarshalJSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	normalized, err := r.normalizedCopy()
	if err != nil {
		return nil, err
	}
	return json.Marshal(externalRefJSON(normalized))
}

// String returns canonical JSON or a generic validation marker; it never falls
// back to raw struct formatting.
func (r ExternalRef) String() string {
	encoded, err := r.MarshalJSON()
	if err != nil {
		return "<invalid-external-reference>"
	}
	return string(encoded)
}

// GoString follows String to avoid exposing an invalid raw URI through %#v.
func (r ExternalRef) GoString() string { return r.String() }

func validateCanonicalText(value string) error {
	if value == "" || value != strings.TrimSpace(value) || hasControl(value) || len(value) > DefaultMaxTextBytes {
		return fmt.Errorf("invalid canonical text")
	}
	if strings.Contains(value, execx.Redacted) || execx.NewRedactor().Text(value) != value {
		return fmt.Errorf("credential-like canonical text")
	}
	return nil
}
