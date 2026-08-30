package searchadapter

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

// NormalizeObservation constructs a bounded, structurally redacted snapshot.
// Unknown upstream states fail closed while their sanitized native token is
// retained. Identity fields are never silently redacted because doing so would
// make later resume or tell/prune operations target a different Study/trial.
func NormalizeObservation(input Observation, policy provider.RedactionPolicy) (Observation, error) {
	out := input
	normalizationDiagnostics := make([]Diagnostic, 0, 3)
	if input.Study.Plan.IsZero() || input.Study.Plan.Kind() != research.KindPlan || !validName(input.Study.StudyKey) {
		return Observation{}, errors.New("observation Study scope is invalid")
	}
	if err := validateExactOpaqueWithPolicy(input.Study.PlanRevision, false, policy); err != nil {
		return Observation{}, errors.New("observation Plan revision is invalid")
	}
	external, err := NormalizeExternalStudyIdentity(input.Study.External, policy)
	if err != nil {
		return Observation{}, err
	}
	out.Study.External = external

	nativeState := input.NativeState
	if !input.State.valid() {
		if nativeState == "" {
			nativeState = string(input.State)
		}
		out.State = StudyStateUnknown
		out.Partial = true
		normalizationDiagnostics = append(normalizationDiagnostics, Diagnostic{
			Code: "unknown_study_state", Message: "upstream Study state was unknown and failed closed",
		})
	}
	out.NativeState, err = sanitizeDisplay(nativeState, policy)
	if err != nil {
		return Observation{}, err
	}

	out.Source, err = sanitizeDisplay(input.Source, policy)
	if err != nil {
		return Observation{}, err
	}
	if out.Source == "" {
		out.Source = "unknown"
		out.Partial = true
		normalizationDiagnostics = append(normalizationDiagnostics, Diagnostic{
			Code: "missing_observation_source", Message: "search observation source was missing",
		})
	}
	if input.ObservedAt.IsZero() {
		return Observation{}, errors.New("search observation time is required")
	}
	out.ObservedAt = input.ObservedAt.UTC()

	out.Trials = make([]TrialObservation, 0, len(input.Trials))
	seenTrials := make(map[string]struct{}, len(input.Trials))
	unknownTrialState := false
	for _, trial := range input.Trials {
		if !trial.State.valid() {
			unknownTrialState = true
			out.Partial = true
		}
		normalized, normalizeErr := normalizeTrialObservation(trial, policy)
		if normalizeErr != nil {
			return Observation{}, normalizeErr
		}
		if _, duplicate := seenTrials[normalized.Trial.TrialID]; duplicate {
			return Observation{}, fmt.Errorf("duplicate observed trial %q", normalized.Trial.TrialID)
		}
		seenTrials[normalized.Trial.TrialID] = struct{}{}
		out.Trials = append(out.Trials, normalized)
	}
	if unknownTrialState {
		normalizationDiagnostics = append(normalizationDiagnostics, Diagnostic{
			Code: "unknown_trial_state", Message: "one or more upstream trial states were unknown and failed closed",
		})
	}
	sort.Slice(out.Trials, func(i, j int) bool {
		left, right := out.Trials[i].Trial, out.Trials[j].Trial
		if left.Number != nil && right.Number != nil && *left.Number != *right.Number {
			return *left.Number < *right.Number
		}
		if left.Number != nil && right.Number == nil {
			return true
		}
		if left.Number == nil && right.Number != nil {
			return false
		}
		return left.TrialID < right.TrialID
	})

	if input.Metadata != nil {
		safe, sanitizeErr := provider.SanitizeRawState(input.Metadata, policy)
		if sanitizeErr != nil {
			return Observation{}, fmt.Errorf("sanitize search observation metadata: %w", sanitizeErr)
		}
		metadata, ok := safe.(map[string]any)
		if !ok {
			return Observation{}, errors.New("search observation metadata must be an object")
		}
		out.Metadata = metadata
	}

	out.Diagnostics = make([]Diagnostic, 0, len(input.Diagnostics)+len(normalizationDiagnostics))
	out.Diagnostics = append(out.Diagnostics, normalizationDiagnostics...)
	for _, diagnostic := range input.Diagnostics {
		safe, sanitizeErr := normalizeDiagnostic(diagnostic, policy)
		if sanitizeErr != nil {
			return Observation{}, sanitizeErr
		}
		out.Diagnostics = append(out.Diagnostics, safe)
	}
	sort.SliceStable(out.Diagnostics, func(i, j int) bool {
		if out.Diagnostics[i].Code != out.Diagnostics[j].Code {
			return out.Diagnostics[i].Code < out.Diagnostics[j].Code
		}
		return out.Diagnostics[i].Message < out.Diagnostics[j].Message
	})
	if out.Diagnostics == nil {
		out.Diagnostics = []Diagnostic{}
	}
	if out.Trials == nil {
		out.Trials = []TrialObservation{}
	}
	if err := out.Validate(); err != nil {
		return Observation{}, err
	}
	return out, nil
}

func normalizeTrialObservation(input TrialObservation, policy provider.RedactionPolicy) (TrialObservation, error) {
	out := input
	if err := validateExactOpaqueWithPolicy(input.Trial.TrialID, false, policy); err != nil {
		return TrialObservation{}, errors.New("observed trial id is invalid or sensitive")
	}
	if input.Trial.Number != nil {
		if *input.Trial.Number < 0 {
			return TrialObservation{}, errors.New("observed trial number cannot be negative")
		}
		number := *input.Trial.Number
		out.Trial.Number = &number
	}
	if !input.State.valid() {
		if input.NativeState == "" {
			out.NativeState = string(input.State)
		}
		out.State = TrialStateUnknown
	}
	var err error
	out.NativeState, err = sanitizeDisplay(out.NativeState, policy)
	if err != nil {
		return TrialObservation{}, err
	}
	out.Reason, err = sanitizeDisplay(input.Reason, policy)
	if err != nil {
		return TrialObservation{}, err
	}
	if input.Parameters != nil {
		if len(input.Parameters) > maxParameters {
			return TrialObservation{}, errors.New("observed trial parameters are oversized")
		}
		out.Parameters = make(map[string]Value, len(input.Parameters))
		for name, value := range input.Parameters {
			if !validName(name) {
				return TrialObservation{}, fmt.Errorf("observed trial parameter %q is invalid", name)
			}
			normalized, normalizeErr := normalizeObservedValue(value, policy)
			if normalizeErr != nil {
				return TrialObservation{}, fmt.Errorf("observed trial parameter %q: %w", name, normalizeErr)
			}
			out.Parameters[name] = normalized
		}
	}
	if err := validateMetrics(input.Values); err != nil {
		return TrialObservation{}, err
	}
	if input.Values != nil {
		out.Values = make(map[string]float64, len(input.Values))
		for name, value := range input.Values {
			out.Values[name] = value
		}
	}
	return out, nil
}

func normalizeObservedValue(input Value, policy provider.RedactionPolicy) (Value, error) {
	if err := input.validateWithPolicy(policy, true); err != nil {
		return Value{}, err
	}
	out := input
	if input.Kind == ValueString {
		safe, _, err := provider.SanitizeText(input.String, policy)
		if err != nil {
			return Value{}, err
		}
		out.String = safe
	}
	return out, nil
}

func normalizeDiagnostic(input Diagnostic, policy provider.RedactionPolicy) (Diagnostic, error) {
	code := input.Code
	if !validDiagnosticCode(code) {
		code = "adapter_diagnostic"
	}
	message, err := sanitizeDisplay(input.Message, policy)
	if err != nil {
		return Diagnostic{}, err
	}
	if message == "" {
		message = "search adapter diagnostic"
	}
	return Diagnostic{Code: code, Message: message}, nil
}

func sanitizeDisplay(value string, policy provider.RedactionPolicy) (string, error) {
	safe, _, err := provider.SanitizeText(value, policy)
	if err != nil {
		return "", err
	}
	safe = strings.Join(strings.Fields(safe), " ")
	if len(safe) > maxOpaqueBytes {
		safe = safe[:maxOpaqueBytes]
		for !utf8.ValidString(safe) {
			safe = safe[:len(safe)-1]
		}
	}
	return safe, nil
}

func (observation Observation) Validate() error {
	if err := observation.Study.Validate(); err != nil {
		return err
	}
	if !observation.State.valid() {
		return errors.New("search observation state is invalid")
	}
	if observation.Source == "" || observation.ObservedAt.IsZero() || !isUTC(observation.ObservedAt) {
		return errors.New("search observation provenance is incomplete")
	}
	if err := validateExactOpaque(observation.Source, true); err != nil {
		return errors.New("search observation source is unsafe")
	}
	if observation.NativeState != "" {
		if err := validateExactOpaque(observation.NativeState, true); err != nil {
			return errors.New("search observation native state is unsafe")
		}
	}
	seen := make(map[string]struct{}, len(observation.Trials))
	for _, trial := range observation.Trials {
		if err := trial.Trial.Validate(); err != nil {
			return err
		}
		if !trial.State.valid() {
			return errors.New("observed trial state is invalid")
		}
		if _, duplicate := seen[trial.Trial.TrialID]; duplicate {
			return errors.New("search observation has duplicate trials")
		}
		seen[trial.Trial.TrialID] = struct{}{}
		if trial.Parameters != nil {
			for name, value := range trial.Parameters {
				if !validName(name) || value.Validate() != nil {
					return errors.New("observed trial parameters are unsafe")
				}
			}
		}
		if err := validateMetrics(trial.Values); err != nil {
			return err
		}
		for _, value := range []string{trial.NativeState, trial.Reason} {
			if value != "" {
				if err := validateExactOpaque(value, true); err != nil {
					return errors.New("observed trial text is unsafe")
				}
			}
		}
	}
	if observation.Metadata != nil {
		safe, err := provider.SanitizeRawState(observation.Metadata, provider.DefaultRedactionPolicy())
		if err != nil || !reflect.DeepEqual(safe, observation.Metadata) {
			return errors.New("search observation metadata is not sanitized")
		}
	}
	for _, diagnostic := range observation.Diagnostics {
		if !validDiagnosticCode(diagnostic.Code) || diagnostic.Message == "" {
			return errors.New("search observation diagnostic is invalid")
		}
		if err := validateExactOpaque(diagnostic.Message, true); err != nil {
			return errors.New("search observation diagnostic is unsafe")
		}
	}
	return nil
}

func (state StudyState) valid() bool {
	return state == StudyStateActive || state == StudyStateComplete || state == StudyStateUnknown
}

func (state TrialState) valid() bool {
	switch state {
	case TrialStateWaiting, TrialStateRunning, TrialStateComplete, TrialStatePruned, TrialStateFailed, TrialStateUnknown:
		return true
	default:
		return false
	}
}

func validDiagnosticCode(value string) bool {
	if value == "" || len(value) > maxNameBytes {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '.') {
			return false
		}
	}
	return true
}
