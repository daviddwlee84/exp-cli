package mlflow

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/execx"
)

const DefaultTimeout = 30 * time.Second

var (
	ErrProfileNotFound    = errors.New("MLflow profile is not configured")
	ErrAmbiguousSelection = errors.New("MLflow profile and compatibility overrides cannot be combined")

	profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	profileEnvPattern  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	profileMetric      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

// ProfileOrigin identifies the winning selector tier without exposing a config
// path. Repository tiers are usable only after exact-digest capability trust.
type ProfileOrigin string

const (
	ProfileExplicit      ProfileOrigin = "explicit"
	ProfileSubdir        ProfileOrigin = "subdir"
	ProfileSource        ProfileOrigin = "source"
	ProfileCanonical     ProfileOrigin = "canonical"
	ProfileUser          ProfileOrigin = "user"
	ProfileCompatibility ProfileOrigin = "compatibility"
)

// EnvironmentBinding contains names and policy only. Parent values are resolved
// by execx immediately before a child process starts.
type EnvironmentBinding struct {
	Child    string `json:"child"`
	From     string `json:"from"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}

// WorkloadProfile is the value-free profile metadata safe to place in a private
// worker job and durable terminal marker. It intentionally cannot hold an
// endpoint, credential, run ID, artifact URI, or resolved environment value.
type WorkloadProfile struct {
	Name           string               `json:"name"`
	Context        string               `json:"context"`
	Binary         string               `json:"binary"`
	Timeout        string               `json:"timeout"`
	Environment    []EnvironmentBinding `json:"environment"`
	DefaultMetrics []string             `json:"default_metrics"`
}

// ResolvedProfile adds invocation-local selection provenance to a workload-safe
// profile. Origin is not required by the child and is not persisted there.
type ResolvedProfile struct {
	WorkloadProfile
	Origin ProfileOrigin `json:"origin"`
}

// InvocationOptions preserves the legacy environment/context flags as one
// explicit compatibility mode. ContextSet distinguishes an omitted flag from an
// explicitly supplied empty value.
type InvocationOptions struct {
	Profile    string
	Context    string
	ContextSet bool
	AllowEnv   []string
	SecretEnv  []string
}

// ResolveProfile selects an explicit profile or the already precedence-resolved
// config default. Config loading establishes user -> canonical -> Source ->
// root-to-leaf subdirectory precedence; this boundary enforces exact trust on
// the winning repository selector and atomic profile definition.
func ResolveProfile(result *config.Result, explicit string) (*ResolvedProfile, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" && !profileNamePattern.MatchString(explicit) {
		return nil, errors.New("MLflow profile name is invalid")
	}
	if result == nil {
		if explicit == "" {
			return nil, nil
		}
		return nil, ErrProfileNotFound
	}
	name := explicit
	origin := ProfileExplicit
	if name == "" {
		name = result.Effective.Defaults.MLflowProfile
		if name == "" {
			return nil, nil
		}
		provenance, found := result.SourceFor("defaults.mlflow_profile")
		if !found || !provenance.Trusted {
			return nil, fmt.Errorf("MLflow profile selector requires exact config trust: %w", config.ErrUntrusted)
		}
		origin = originForLayer(provenance.Layer)
	}
	profile, found := result.Effective.MLflow.Profiles[name]
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrProfileNotFound, name)
	}
	provenance, found := result.SourceFor("mlflow.profiles." + name)
	if !found || !provenance.Trusted {
		return nil, fmt.Errorf("MLflow profile definition requires exact config trust: %w", config.ErrUntrusted)
	}
	resolved := &ResolvedProfile{WorkloadProfile: workloadProfile(name, profile), Origin: origin}
	if err := resolved.WorkloadProfile.Validate(); err != nil {
		return nil, err
	}
	return resolved, nil
}

// ResolveInvocation applies compatibility overrides ahead of config defaults.
// An explicit named profile cannot be mixed with those overrides because doing
// so would make the source of binary/environment policy ambiguous.
func ResolveInvocation(result *config.Result, options InvocationOptions) (ResolvedProfile, error) {
	compatibility := options.ContextSet || len(options.AllowEnv) > 0 || len(options.SecretEnv) > 0
	if strings.TrimSpace(options.Profile) != "" && compatibility {
		return ResolvedProfile{}, ErrAmbiguousSelection
	}
	if !compatibility {
		profile, err := ResolveProfile(result, options.Profile)
		if err != nil {
			return ResolvedProfile{}, err
		}
		if profile != nil {
			return *profile, nil
		}
	}
	contextName := "default"
	if options.ContextSet {
		contextName = strings.TrimSpace(options.Context)
		if !profileNamePattern.MatchString(contextName) {
			return ResolvedProfile{}, errors.New("MLflow context must be a non-secret slug")
		}
	}
	bindings, err := compatibilityBindings(options.AllowEnv, options.SecretEnv)
	if err != nil {
		return ResolvedProfile{}, err
	}
	profile := ResolvedProfile{
		WorkloadProfile: WorkloadProfile{
			Name: "compatibility", Context: contextName, Binary: "mlflow", Timeout: DefaultTimeout.String(),
			Environment: bindings, DefaultMetrics: []string{},
		},
		Origin: ProfileCompatibility,
	}
	return profile, profile.WorkloadProfile.Validate()
}

func workloadProfile(name string, value config.MLflowProfile) WorkloadProfile {
	bindings := make([]EnvironmentBinding, 0, len(value.Env))
	for child, binding := range value.Env {
		bindings = append(bindings, EnvironmentBinding{
			Child: child, From: binding.From, Secret: binding.Secret, Required: binding.Required,
		})
	}
	sort.Slice(bindings, func(left, right int) bool { return bindings[left].Child < bindings[right].Child })
	metrics := append([]string{}, value.DefaultMetrics...)
	if metrics == nil {
		metrics = []string{}
	}
	return WorkloadProfile{
		Name: name, Context: value.Context, Binary: value.Binary, Timeout: value.Timeout.String(),
		Environment: bindings, DefaultMetrics: metrics,
	}
}

func compatibilityBindings(allowed, secret []string) ([]EnvironmentBinding, error) {
	seen := make(map[string]struct{}, len(allowed)+len(secret))
	bindings := make([]EnvironmentBinding, 0, len(allowed)+len(secret))
	add := func(name string, sensitive, required bool) error {
		if !profileEnvPattern.MatchString(name) {
			return errors.New("MLflow environment override contains an invalid name")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("MLflow environment override %s is ambiguous", name)
		}
		seen[name] = struct{}{}
		bindings = append(bindings, EnvironmentBinding{Child: name, From: name, Secret: sensitive, Required: required})
		return nil
	}
	for _, name := range allowed {
		if err := add(name, false, false); err != nil {
			return nil, err
		}
	}
	for _, name := range secret {
		if err := add(name, true, true); err != nil {
			return nil, err
		}
	}
	sort.Slice(bindings, func(left, right int) bool { return bindings[left].Child < bindings[right].Child })
	if bindings == nil {
		bindings = []EnvironmentBinding{}
	}
	return bindings, nil
}

func originForLayer(layer config.LayerKind) ProfileOrigin {
	switch layer {
	case config.LayerSubdir:
		return ProfileSubdir
	case config.LayerSource:
		return ProfileSource
	case config.LayerCanonical:
		return ProfileCanonical
	case config.LayerUser:
		return ProfileUser
	default:
		return ProfileUser
	}
}

// Validate independently checks serialized workload metadata before any values
// are resolved. The stricter config loader remains the source of profile policy.
func (profile WorkloadProfile) Validate() error {
	if !profileNamePattern.MatchString(profile.Name) || !profileNamePattern.MatchString(profile.Context) {
		return errors.New("MLflow workload profile identity is invalid")
	}
	if profile.Binary == "" || strings.ContainsAny(profile.Binary, "/\\\x00\r\n\t ") {
		return errors.New("MLflow workload profile binary is invalid")
	}
	timeout, err := time.ParseDuration(profile.Timeout)
	if err != nil || timeout <= 0 || timeout > 24*time.Hour {
		return errors.New("MLflow workload profile timeout is invalid")
	}
	previous := ""
	for _, binding := range profile.Environment {
		if !profileEnvPattern.MatchString(binding.Child) || !profileEnvPattern.MatchString(binding.From) {
			return errors.New("MLflow workload environment binding is invalid")
		}
		if previous != "" && binding.Child <= previous {
			return errors.New("MLflow workload environment bindings must be sorted and unique")
		}
		if strings.HasPrefix(binding.Child, "EXP_") {
			return errors.New("MLflow workload environment cannot override exp-owned variables")
		}
		previous = binding.Child
	}
	if len(profile.Environment) > 128 || len(profile.DefaultMetrics) > 256 {
		return errors.New("MLflow workload profile exceeds its entry limit")
	}
	seenMetrics := make(map[string]struct{}, len(profile.DefaultMetrics))
	for _, metric := range profile.DefaultMetrics {
		if !profileMetric.MatchString(metric) || strings.Contains(metric, "..") {
			return errors.New("MLflow workload profile contains an invalid metric")
		}
		if _, duplicate := seenMetrics[metric]; duplicate {
			return errors.New("MLflow workload profile contains a duplicate metric")
		}
		seenMetrics[metric] = struct{}{}
	}
	return nil
}

func (profile WorkloadProfile) TimeoutDuration() (time.Duration, error) {
	if err := profile.Validate(); err != nil {
		return 0, err
	}
	return time.ParseDuration(profile.Timeout)
}

// EnvironmentBindings builds private, late-resolved bindings. Every profile
// value is treated as sensitive for output/result redaction even when Secret is
// false; Secret remains policy metadata, not permission to persist a value.
func (profile WorkloadProfile) EnvironmentBindings() ([]execx.Binding, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	bindings := make([]execx.Binding, 0, len(profile.Environment))
	for _, binding := range profile.Environment {
		bindings = append(bindings, execx.BindFromEnv(binding.Child, binding.From, true, binding.Required))
	}
	return bindings, nil
}

// EnvironmentPolicy returns the complete deny-by-default observer environment.
func (profile WorkloadProfile) EnvironmentPolicy() (execx.Environment, error) {
	bindings, err := profile.EnvironmentBindings()
	if err != nil {
		return execx.Environment{}, err
	}
	return execx.MinimalEnvironment(bindings...)
}
