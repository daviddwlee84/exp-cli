// Package config loads strict, identity-scoped exp.config/v1 preferences after
// workspace resolution. Configuration cannot establish canonical authority.
package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/trust"
)

const (
	Schema                = "exp.config/v1"
	DirectoryName         = ".exp-cli"
	Filename              = "config.toml"
	RelativePath          = ".exp-cli/config.toml"
	MaxFileBytes    int64 = 1 << 20
	MaxLayers             = 64
	DefaultMaxDepth       = 32
)

var (
	ErrIdentityConflict = errors.New("config identity selector conflicts with resolved context")
	ErrTooDeep          = errors.New("config directory hierarchy exceeds depth limit")
	ErrUntrusted        = trust.ErrUntrusted
	namePattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	environmentPattern  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	metricPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

// LayerKind names a precedence tier from lowest to highest.
type LayerKind string

const (
	LayerBuiltin   LayerKind = "builtin"
	LayerUser      LayerKind = "user"
	LayerCanonical LayerKind = "canonical"
	LayerSource    LayerKind = "source"
	LayerSubdir    LayerKind = "subdir"
)

// ColorMode is pure presentation configuration.
type ColorMode string

const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// Defaults contains default selectors; it never changes resolved canonical
// Project identity.
type Defaults struct {
	Source           string `json:"source,omitempty"`
	WorkspaceBackend string `json:"workspace_backend"`
	AgentProfile     string `json:"agent_profile,omitempty"`
	MLflowProfile    string `json:"mlflow_profile,omitempty"`
}

type UI struct {
	Color ColorMode `json:"color"`
}

// EnvBinding maps a child variable name (the map key) to a parent environment
// variable name. Values are resolved only by later execution code and are never
// stored in this configuration model.
type EnvBinding struct {
	From     string `toml:"from" json:"from"`
	Secret   bool   `toml:"secret" json:"secret"`
	Required bool   `toml:"required" json:"required"`
}

// MLflowProfile is replaced atomically by name at every layer.
type MLflowProfile struct {
	Context        string                `json:"context"`
	Binary         string                `json:"binary"`
	Timeout        time.Duration         `json:"timeout"`
	Env            map[string]EnvBinding `json:"env"`
	DefaultMetrics []string              `json:"default_metrics"`
}

type MLflow struct {
	Profiles map[string]MLflowProfile `json:"profiles"`
}

// BackendPreference is an ordinary keyed table: fields merge independently by
// backend name rather than replacing the complete map entry.
type BackendPreference struct {
	Enabled  bool `json:"enabled"`
	Priority int  `json:"priority"`
}

type Workspace struct {
	PreferredBackends []string                     `json:"preferred_backends"`
	Backends          map[string]BackendPreference `json:"backends"`
}

// Effective is the fully merged, validated configuration.
type Effective struct {
	Defaults  Defaults  `json:"defaults"`
	UI        UI        `json:"ui"`
	MLflow    MLflow    `json:"mlflow"`
	Workspace Workspace `json:"workspace"`
}

// Provenance identifies the exact winning layer for one effective leaf.
type Provenance struct {
	Source     string           `json:"source"`
	Layer      LayerKind        `json:"layer"`
	Digest     string           `json:"digest"`
	Trusted    bool             `json:"trusted"`
	Capability trust.Capability `json:"capability,omitempty"`
}

// Layer describes one actually applied file (or the built-in layer).
type Layer struct {
	Kind         LayerKind          `json:"kind"`
	Source       string             `json:"source"`
	Digest       string             `json:"digest"`
	Trusted      bool               `json:"trusted"`
	Capabilities []trust.Capability `json:"capabilities"`
	TrustSubject *trust.Subject     `json:"trust_subject,omitempty"`
	Fields       []string           `json:"fields"`
}

// LegacyPath exposes an existing separate loader path without reading or
// rewriting that format.
type LegacyPath struct {
	Path    string    `json:"path"`
	Source  string    `json:"source"`
	Layer   LayerKind `json:"layer"`
	Trusted bool      `json:"trusted"`
	Legacy  bool      `json:"legacy"`
}

type LegacyPaths struct {
	Agents  LegacyPath `json:"agents"`
	Runtime LegacyPath `json:"runtime"`
}

// Result contains effective values, exact ordered layer metadata, and leaf
// provenance. Digest fingerprints the ordered exact layer bytes.
type Result struct {
	Effective  Effective             `json:"effective"`
	Layers     []Layer               `json:"layers"`
	Provenance map[string]Provenance `json:"provenance"`
	Digest     string                `json:"digest"`
	Legacy     LegacyPaths           `json:"legacy"`
}

func (result Result) SourceFor(field string) (Provenance, bool) {
	value, found := result.Provenance[field]
	return value, found
}

// RequireTrusted rejects any listed effective field whose execution-bearing
// provenance is not approved. Pure fields and built-ins are already trusted.
func (result Result) RequireTrusted(fields ...string) error {
	for _, field := range fields {
		provenance, found := result.Provenance[field]
		if !found {
			return fmt.Errorf("configuration field %s has no provenance", field)
		}
		if !provenance.Trusted {
			return fmt.Errorf("configuration field %s from %s requires %s: %w", field, provenance.Source, provenance.Capability, ErrUntrusted)
		}
	}
	return nil
}

// Request is populated from an already resolved workspace context. Redundant
// explicit identity fields are accepted for focused tests and must agree with
// Project/Source when both forms are supplied.
type Request struct {
	Project *project.Info

	ProjectID             research.UUID
	CanonicalRoot         string
	CanonicalGitCommonDir string

	Source             *research.Source
	SourceID           research.ID
	SourceRoot         string
	SourceGitCommonDir string
	SourceSubdir       string
	InvocationDir      string

	UserConfigPath    string
	AgentConfigPath   string
	RuntimeConfigPath string
}

// TrustChecker is the only trust-store surface needed while annotating fields.
type TrustChecker interface {
	Check(context.Context, trust.Subject, string, trust.Capability) (bool, error)
}

// Builtins returns a deep, deterministic default configuration.
func Builtins() Effective {
	return Effective{
		Defaults: Defaults{WorkspaceBackend: "native_git"},
		UI:       UI{Color: ColorAuto},
		MLflow:   MLflow{Profiles: map[string]MLflowProfile{}},
		Workspace: Workspace{
			PreferredBackends: []string{"native_git"},
			Backends: map[string]BackendPreference{
				"native_git": {Enabled: true, Priority: 100},
			},
		},
	}
}

func cloneEffective(value Effective) Effective {
	out := value
	out.MLflow.Profiles = make(map[string]MLflowProfile, len(value.MLflow.Profiles))
	for name, profile := range value.MLflow.Profiles {
		profile.Env = cloneEnv(profile.Env)
		profile.DefaultMetrics = append([]string{}, profile.DefaultMetrics...)
		out.MLflow.Profiles[name] = profile
	}
	out.Workspace.PreferredBackends = append([]string{}, value.Workspace.PreferredBackends...)
	out.Workspace.Backends = make(map[string]BackendPreference, len(value.Workspace.Backends))
	for name, backend := range value.Workspace.Backends {
		out.Workspace.Backends[name] = backend
	}
	return out
}

func cloneEnv(value map[string]EnvBinding) map[string]EnvBinding {
	out := make(map[string]EnvBinding, len(value))
	for name, binding := range value {
		out[name] = binding
	}
	return out
}

func validName(value string) bool { return namePattern.MatchString(value) }

func validEnvironmentName(value string) bool { return environmentPattern.MatchString(value) }

func validateEffective(value Effective) error {
	if value.Defaults.Source != "" {
		if id, err := research.ParseIDForKind(value.Defaults.Source, research.KindSource); err != nil || id.IsZero() {
			normalized, normalizeErr := research.NormalizeSourceKey(value.Defaults.Source)
			if normalizeErr != nil || normalized != value.Defaults.Source {
				return errors.New("defaults.source must be a canonical Source key or complete typed Source ID")
			}
		}
	}
	for field, selected := range map[string]string{
		"defaults.workspace_backend": value.Defaults.WorkspaceBackend,
		"defaults.agent_profile":     value.Defaults.AgentProfile,
		"defaults.mlflow_profile":    value.Defaults.MLflowProfile,
	} {
		if selected != "" && !validName(selected) {
			return fmt.Errorf("%s has an invalid name", field)
		}
	}
	if value.Defaults.WorkspaceBackend == "" {
		return errors.New("defaults.workspace_backend must not be empty")
	}
	if backend, found := value.Workspace.Backends[value.Defaults.WorkspaceBackend]; !found || !backend.Enabled {
		return fmt.Errorf("defaults.workspace_backend %q is not enabled in workspace.backends", value.Defaults.WorkspaceBackend)
	}
	if native, found := value.Workspace.Backends["native_git"]; !found || !native.Enabled {
		return errors.New("workspace.backends.native_git must remain enabled as the correctness baseline")
	}
	if value.Defaults.MLflowProfile != "" {
		if _, found := value.MLflow.Profiles[value.Defaults.MLflowProfile]; !found {
			return fmt.Errorf("defaults.mlflow_profile references unknown profile %q", value.Defaults.MLflowProfile)
		}
	}
	switch value.UI.Color {
	case ColorAuto, ColorAlways, ColorNever:
	default:
		return fmt.Errorf("ui.color must be auto, always, or never")
	}
	seenBackends := map[string]struct{}{}
	for _, name := range value.Workspace.PreferredBackends {
		if !validName(name) {
			return fmt.Errorf("workspace.preferred_backends contains invalid name %q", name)
		}
		if _, duplicate := seenBackends[name]; duplicate {
			return fmt.Errorf("workspace.preferred_backends contains duplicate %q", name)
		}
		seenBackends[name] = struct{}{}
	}
	for name, backend := range value.Workspace.Backends {
		if !validName(name) {
			return fmt.Errorf("workspace.backends contains invalid name %q", name)
		}
		if backend.Priority < -1000000 || backend.Priority > 1000000 {
			return fmt.Errorf("workspace.backends.%s.priority is outside the supported range", name)
		}
	}
	for name, profile := range value.MLflow.Profiles {
		if !validName(name) {
			return fmt.Errorf("invalid MLflow profile name %q", name)
		}
		if err := validateMLflowProfile(profile); err != nil {
			return fmt.Errorf("MLflow profile %s: %w", name, err)
		}
	}
	return nil
}

func validateMLflowProfile(profile MLflowProfile) error {
	if !validName(profile.Context) {
		return errors.New("context must be a non-secret slug")
	}
	if !validatePlainText(profile.Binary) || filepath.Base(profile.Binary) != profile.Binary || strings.ContainsAny(profile.Binary, `/\\`) || strings.IndexFunc(profile.Binary, unicode.IsSpace) >= 0 {
		return errors.New("binary must be a binary name, not a path or command")
	}
	if profile.Timeout <= 0 || profile.Timeout > 24*time.Hour {
		return errors.New("timeout must be positive and no longer than 24h")
	}
	for child, binding := range profile.Env {
		if !validEnvironmentName(child) || !validEnvironmentName(binding.From) {
			return errors.New("env bindings must contain valid upper-case environment names")
		}
		if strings.HasPrefix(child, "EXP_") {
			return errors.New("env bindings cannot override exp-owned variables")
		}
	}
	if len(profile.Env) > 128 {
		return errors.New("env bindings exceed the limit of 128")
	}
	if len(profile.DefaultMetrics) > 256 {
		return errors.New("default_metrics exceeds the limit of 256")
	}
	seen := map[string]struct{}{}
	for _, metric := range profile.DefaultMetrics {
		if !metricPattern.MatchString(metric) || strings.Contains(metric, "..") {
			return fmt.Errorf("invalid default metric name %q", metric)
		}
		if _, duplicate := seen[metric]; duplicate {
			return fmt.Errorf("duplicate default metric %q", metric)
		}
		seen[metric] = struct{}{}
	}
	return nil
}

func validatePlainText(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func sortedCapabilities(values map[trust.Capability]bool) []trust.Capability {
	out := make([]trust.Capability, 0, len(values))
	for capability := range values {
		out = append(out, capability)
	}
	sort.Slice(out, func(left, right int) bool { return out[left] < out[right] })
	return out
}
