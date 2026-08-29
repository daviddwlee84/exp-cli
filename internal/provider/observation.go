package provider

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

// NormalizeObservation applies the compiled registry and redaction boundary to
// disposable provider state. Unknown provider/capability/support/state values
// move to sanitized Native fields while normalized support and state fail
// closed.
func (r *Registry) NormalizeObservation(input Observation, policy RedactionPolicy) (Observation, error) {
	if r == nil {
		return Observation{}, fmt.Errorf("provider registry is required")
	}
	effective, err := policy.effective()
	if err != nil {
		return Observation{}, err
	}
	out := input
	out.Diagnostics = make([]Diagnostic, 0, len(input.Diagnostics)+4)

	descriptor, knownProvider := r.Get(input.Provider)
	if !knownProvider {
		raw := input.NativeProvider
		if raw == "" {
			raw = string(input.Provider)
		}
		out.NativeProvider, err = sanitizeSingleLine(raw, effective)
		if err != nil {
			return Observation{}, err
		}
		out.Provider = ProviderUnknown
		out.Support = SupportUnknown
		out.NormalizedState = StateUnknown
		out.Diagnostics = append(out.Diagnostics, mustDiagnostic(
			SeverityWarning,
			"unknown_provider",
			"observation names a provider not compiled into this registry",
			out.NativeProvider,
			effective,
		))
	} else {
		out.Provider = descriptor.Name
		out.NativeProvider, err = sanitizeSingleLine(input.NativeProvider, effective)
		if err != nil {
			return Observation{}, err
		}
	}

	if validSlug(string(input.Context)) {
		out.Context = input.Context
		out.NativeContext, err = sanitizeSingleLine(input.NativeContext, effective)
	} else {
		raw := input.NativeContext
		if raw == "" {
			raw = string(input.Context)
		}
		out.Context = ContextName("unknown")
		out.NativeContext, err = sanitizeSingleLine(raw, effective)
		if err != nil {
			return Observation{}, err
		}
		out.Diagnostics = append(out.Diagnostics, mustDiagnostic(
			SeverityWarning,
			"unknown_context",
			"observation context was invalid and failed closed",
			out.NativeContext,
			effective,
		))
	}

	out.ProviderVersion, err = sanitizeSingleLine(input.ProviderVersion, effective)
	if err != nil {
		return Observation{}, err
	}
	out.ProviderVersion = strings.Join(strings.Fields(out.ProviderVersion), " ")

	knownCapability := knownProvider && descriptor.HasCapability(input.Capability)
	if knownCapability {
		out.Capability = input.Capability
		out.NativeCapability, err = sanitizeSingleLine(input.NativeCapability, effective)
	} else {
		raw := input.NativeCapability
		if raw == "" {
			raw = string(input.Capability)
		}
		out.Capability = ""
		out.NativeCapability, err = sanitizeSingleLine(raw, effective)
		if err != nil {
			return Observation{}, err
		}
		out.Support = SupportUnknown
		out.Diagnostics = append(out.Diagnostics, mustDiagnostic(
			SeverityWarning,
			"unknown_capability",
			"observation capability is not declared by the compiled provider",
			out.NativeCapability,
			effective,
		))
	}

	if knownProvider && knownCapability && input.Support.Valid() {
		out.Support = input.Support
		out.NativeSupport, err = sanitizeSingleLine(input.NativeSupport, effective)
	} else if knownProvider && knownCapability {
		raw := input.NativeSupport
		if raw == "" {
			raw = string(input.Support)
		}
		out.Support = SupportUnknown
		out.NativeSupport, err = sanitizeSingleLine(raw, effective)
		if err != nil {
			return Observation{}, err
		}
		out.Diagnostics = append(out.Diagnostics, mustDiagnostic(
			SeverityWarning,
			"unknown_support_value",
			"observation support value was unknown and failed closed",
			out.NativeSupport,
			effective,
		))
	} else {
		out.Support = SupportUnknown
		out.NativeSupport, err = sanitizeSingleLine(input.NativeSupport, effective)
	}
	if err != nil {
		return Observation{}, err
	}

	rawState := input.NativeState
	if rawState == "" && input.NormalizedState != StateUnknown {
		rawState = string(input.NormalizedState)
	}
	out.NativeState, err = sanitizeSingleLine(rawState, effective)
	if err != nil {
		return Observation{}, err
	}
	stateTrusted := knownProvider && knownCapability && out.Support == SupportSupported
	switch {
	case stateTrusted && input.NormalizedState.Valid():
		out.NormalizedState = input.NormalizedState
	case input.NormalizedState == StateUnknown:
		out.NormalizedState = StateUnknown
	default:
		out.NormalizedState = StateUnknown
		code := "untrusted_normalized_state"
		message := "observation state was not supported by a verified capability and failed closed"
		if !input.NormalizedState.Valid() {
			code = "unknown_normalized_state"
			message = "observation state was unknown and failed closed"
		}
		out.Diagnostics = append(out.Diagnostics, mustDiagnostic(
			SeverityWarning,
			code,
			message,
			out.NativeState,
			effective,
		))
	}
	out.NativeReason, err = sanitizeSingleLine(input.NativeReason, effective)
	if err != nil {
		return Observation{}, err
	}
	out.Source, err = sanitizeSingleLine(input.Source, effective)
	if err != nil {
		return Observation{}, err
	}
	if out.Source == "" {
		out.Source = "unknown"
		out.Partial = true
		out.Diagnostics = append(out.Diagnostics, mustDiagnostic(
			SeverityWarning,
			"missing_observation_source",
			"observation source was missing",
			"",
			effective,
		))
	}
	if input.ObservedAt.IsZero() {
		return Observation{}, fmt.Errorf("observation time is required")
	}
	out.ObservedAt = input.ObservedAt.UTC()

	if input.RawState != nil {
		// envs is removed universally because both Provider and NativeProvider are
		// untrusted labels at this boundary.
		out.RawState, err = SanitizeProviderRawState(input.Provider, input.RawState, effective)
		if err != nil {
			return Observation{}, err
		}
	}
	for _, diagnostic := range input.Diagnostics {
		safe, sanitizeErr := sanitizeDiagnostic(diagnostic, effective)
		if sanitizeErr != nil {
			return Observation{}, sanitizeErr
		}
		out.Diagnostics = append(out.Diagnostics, safe)
	}
	if !knownProvider {
		out.Capability = ""
		if out.NativeCapability == "" {
			out.NativeCapability, err = sanitizeSingleLine(string(input.Capability), effective)
			if err != nil {
				return Observation{}, err
			}
		}
		out.Support = SupportUnknown
		out.NormalizedState = StateUnknown
	}
	sortDiagnostics(out.Diagnostics)
	if out.Diagnostics == nil {
		out.Diagnostics = []Diagnostic{}
	}
	if err := out.Validate(r); err != nil {
		return Observation{}, err
	}
	return out, nil
}

// Validate checks that an Observation has already crossed the normalization
// and structural-redaction boundary.
func (o Observation) Validate(registry *Registry) error {
	if registry == nil {
		return fmt.Errorf("provider registry is required")
	}
	if !validSlug(string(o.Context)) || o.ObservedAt.IsZero() || o.Source == "" {
		return fmt.Errorf("observation identity is incomplete")
	}
	if o.Provider == ProviderUnknown {
		if o.NativeProvider == "" || o.Support != SupportUnknown || o.NormalizedState != StateUnknown || o.Capability != "" {
			return fmt.Errorf("unknown provider observation did not fail closed")
		}
	} else {
		descriptor, ok := registry.Get(o.Provider)
		if !ok {
			return fmt.Errorf("observation provider is not compiled")
		}
		if o.Capability == "" {
			if o.Support != SupportUnknown || o.NormalizedState != StateUnknown {
				return fmt.Errorf("unknown capability observation did not fail closed")
			}
		} else if !descriptor.HasCapability(o.Capability) {
			return fmt.Errorf("observation capability is not compiled")
		}
		if o.Support != SupportSupported && o.NormalizedState != StateUnknown {
			return fmt.Errorf("unverified capability observation did not fail closed")
		}
	}
	if !o.Support.Valid() || !o.NormalizedState.Valid() {
		return fmt.Errorf("observation normalized values are invalid")
	}
	for _, value := range []string{
		o.NativeProvider,
		o.NativeContext,
		o.ProviderVersion,
		o.NativeCapability,
		o.NativeSupport,
		o.Source,
		o.NativeState,
		o.NativeReason,
	} {
		if len(value) > DefaultMaxTextBytes || hasControl(value) || execx.NewRedactor().Text(value) != value {
			return fmt.Errorf("observation contains unsafe text")
		}
	}
	for _, diagnostic := range o.Diagnostics {
		if err := diagnostic.Validate(); err != nil {
			return err
		}
	}
	if o.RawState != nil {
		safe, err := SanitizeProviderRawState(o.Provider, o.RawState, DefaultRedactionPolicy())
		if err != nil {
			return err
		}
		originalJSON, err := json.Marshal(o.RawState)
		if err != nil {
			return ErrUnsupportedData
		}
		safeJSON, err := json.Marshal(safe)
		if err != nil || string(originalJSON) != string(safeJSON) {
			return fmt.Errorf("observation raw state is not structurally sanitized")
		}
	}
	return nil
}

// NormalizeObservations returns stable provider/context/capability/source order
// and a merged, sanitized diagnostic set.
func (r *Registry) NormalizeObservations(inputs []Observation, diagnostics []Diagnostic, policy RedactionPolicy) (ObservationResult, error) {
	effective, err := policy.effective()
	if err != nil {
		return ObservationResult{}, err
	}
	result := ObservationResult{
		Observations: make([]Observation, 0, len(inputs)),
		Diagnostics:  make([]Diagnostic, 0, len(diagnostics)),
	}
	for _, input := range inputs {
		observation, err := r.NormalizeObservation(input, effective)
		if err != nil {
			return ObservationResult{}, err
		}
		result.Observations = append(result.Observations, observation)
		result.Partial = result.Partial || observation.Partial
		if observation.ObservedAt.After(result.ObservedAt) {
			result.ObservedAt = observation.ObservedAt
		}
	}
	for _, diagnostic := range diagnostics {
		safe, err := sanitizeDiagnostic(diagnostic, effective)
		if err != nil {
			return ObservationResult{}, err
		}
		result.Diagnostics = append(result.Diagnostics, safe)
	}
	sort.SliceStable(result.Observations, func(i, j int) bool {
		left, right := result.Observations[i], result.Observations[j]
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		if left.Context != right.Context {
			return left.Context < right.Context
		}
		if left.Capability != right.Capability {
			return left.Capability < right.Capability
		}
		if !left.ObservedAt.Equal(right.ObservedAt) {
			return left.ObservedAt.Before(right.ObservedAt)
		}
		return left.Source < right.Source
	})
	sortDiagnostics(result.Diagnostics)
	if result.Observations == nil {
		result.Observations = []Observation{}
	}
	if result.Diagnostics == nil {
		result.Diagnostics = []Diagnostic{}
	}
	return result, nil
}

// StableObservedAt returns the latest timestamp in observations without using
// wall-clock time. It is useful to compose deterministic command envelopes.
func StableObservedAt(observations []Observation) time.Time {
	var latest time.Time
	for _, observation := range observations {
		if observation.ObservedAt.After(latest) {
			latest = observation.ObservedAt
		}
	}
	return latest
}
