package searchadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	maxNameBytes       = 128
	maxOpaqueBytes     = 512
	maxParameters      = 256
	maxObjectives      = 16
	maxCategoricalSize = 4096
)

// IdempotencyToken is the durable value an adapter stores with a mutation
// result. CheckReplay distinguishes a safe replay from key reuse with a
// different request. The adapter is responsible for persisting both the token
// and the original result in its own transaction.
type IdempotencyToken struct {
	Key           string `json:"key"`
	RequestDigest string `json:"request_digest"`
}

// NewIdempotencyToken creates a deterministic digest of a validated mutation
// request. JSON struct fields and map keys have deterministic Go encodings;
// unsupported values such as NaN fail instead of producing an unstable token.
func NewIdempotencyToken(key string, request any) (IdempotencyToken, error) {
	if err := validateIdempotencyKey(key); err != nil {
		return IdempotencyToken{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return IdempotencyToken{}, fmt.Errorf("encode idempotent request: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return IdempotencyToken{Key: key, RequestDigest: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

// CheckReplay reports whether next is an exact replay. Different keys are not
// replays; the same key with different content fails closed.
func (token IdempotencyToken) CheckReplay(next IdempotencyToken) (bool, error) {
	if err := token.Validate(); err != nil {
		return false, err
	}
	if err := next.Validate(); err != nil {
		return false, err
	}
	if token.Key != next.Key {
		return false, nil
	}
	if token.RequestDigest != next.RequestDigest {
		return false, ErrIdempotencyConflict
	}
	return true, nil
}

func (token IdempotencyToken) Validate() error {
	if err := validateIdempotencyKey(token.Key); err != nil {
		return err
	}
	if !validSHA256(token.RequestDigest) {
		return errors.New("idempotency request digest must be a sha256 digest")
	}
	return nil
}

func (descriptor Descriptor) Validate() error {
	if !validName(descriptor.Name) {
		return errors.New("search adapter name is invalid")
	}
	if err := validateExactOpaque(descriptor.AdapterVersion, false); err != nil {
		return errors.New("search adapter version is invalid")
	}
	if !validName(descriptor.UpstreamName) {
		return errors.New("search adapter upstream name is invalid")
	}
	if descriptor.UpstreamVersion != "" {
		if err := validateExactOpaque(descriptor.UpstreamVersion, false); err != nil {
			return errors.New("search adapter upstream version is invalid")
		}
	}
	if descriptor.ContractVersion != ContractVersion {
		return fmt.Errorf("search adapter contract must be %q", ContractVersion)
	}
	if err := validateCapabilities(descriptor.Capabilities); err != nil {
		return err
	}
	if err := validateBoundary(descriptor.Boundary); err != nil {
		return err
	}
	return nil
}

func validateCapabilities(reports []CapabilityReport) error {
	if len(reports) != len(allCapabilities) {
		return errors.New("search adapter must report every contract capability exactly once")
	}
	expected := make(map[Capability]struct{}, len(allCapabilities))
	for _, capability := range allCapabilities {
		expected[capability] = struct{}{}
	}
	seen := make(map[Capability]struct{}, len(reports))
	for _, report := range reports {
		if _, known := expected[report.Capability]; !known {
			return fmt.Errorf("unknown search adapter capability %q", report.Capability)
		}
		if _, duplicate := seen[report.Capability]; duplicate {
			return fmt.Errorf("duplicate search adapter capability %q", report.Capability)
		}
		if !report.Support.Valid() {
			return fmt.Errorf("capability %q has invalid support", report.Capability)
		}
		if report.Reason != "" {
			if err := validateExactOpaque(report.Reason, true); err != nil {
				return fmt.Errorf("capability %q has unsafe reason", report.Capability)
			}
		}
		seen[report.Capability] = struct{}{}
	}
	return nil
}

func validateBoundary(boundary AuthorityBoundary) error {
	expected := ContractBoundary()
	if !sameResponsibilitySet(boundary.Owns, expected.Owns) || !sameResponsibilitySet(boundary.DoesNotOwn, expected.DoesNotOwn) {
		return errors.New("search adapter authority boundary differs from the compiled contract")
	}
	return nil
}

func sameResponsibilitySet(left, right []Responsibility) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[Responsibility]struct{}, len(left))
	for _, value := range left {
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, found := seen[value]; !found {
			return false
		}
	}
	return true
}

func (value Value) Validate() error {
	return value.validateWithPolicy(provider.DefaultRedactionPolicy(), false)
}

func (value Value) validateWithPolicy(policy provider.RedactionPolicy, redact bool) error {
	switch value.Kind {
	case ValueString:
		if value.Integer != 0 || value.Number != 0 || value.Boolean {
			return errors.New("string search value has fields for another kind")
		}
		safe, truncated, err := provider.SanitizeText(value.String, policy)
		if err != nil || truncated {
			return errors.New("string search value exceeds safety bounds")
		}
		if !redact && safe != value.String {
			return errors.New("string search value contains sensitive data")
		}
		if strings.ContainsAny(safe, "\x00\r\n") || !utf8.ValidString(safe) {
			return errors.New("string search value contains controls or invalid UTF-8")
		}
	case ValueInteger:
		if value.String != "" || value.Number != 0 || value.Boolean {
			return errors.New("integer search value has fields for another kind")
		}
	case ValueNumber:
		if value.String != "" || value.Integer != 0 || value.Boolean || !finite(value.Number) {
			return errors.New("number search value is invalid")
		}
	case ValueBoolean:
		if value.String != "" || value.Integer != 0 || value.Number != 0 {
			return errors.New("boolean search value has fields for another kind")
		}
	default:
		return errors.New("search value kind is invalid")
	}
	return nil
}

func (distribution Distribution) Validate() error {
	switch distribution.Kind {
	case DistributionFloat, DistributionInteger:
		if distribution.Low == nil || distribution.High == nil || len(distribution.Choices) != 0 {
			return errors.New("range distribution requires low/high and forbids choices")
		}
		if !finite(*distribution.Low) || !finite(*distribution.High) || *distribution.Low > *distribution.High {
			return errors.New("range distribution bounds are invalid")
		}
		if distribution.Kind == DistributionInteger && (!integral(*distribution.Low) || !integral(*distribution.High)) {
			return errors.New("integer distribution bounds must be integral")
		}
		if distribution.Step != nil {
			if !finite(*distribution.Step) || *distribution.Step <= 0 {
				return errors.New("range distribution step must be positive and finite")
			}
			if distribution.Kind == DistributionInteger && !integral(*distribution.Step) {
				return errors.New("integer distribution step must be integral")
			}
			if distribution.Log {
				return errors.New("log range distribution cannot also declare step")
			}
		}
		if distribution.Log && *distribution.Low <= 0 {
			return errors.New("log range distribution requires a positive lower bound")
		}
	case DistributionCategorical:
		if distribution.Low != nil || distribution.High != nil || distribution.Step != nil || distribution.Log {
			return errors.New("categorical distribution forbids range fields")
		}
		if len(distribution.Choices) == 0 || len(distribution.Choices) > maxCategoricalSize {
			return errors.New("categorical distribution choices are empty or oversized")
		}
		seen := make(map[string]struct{}, len(distribution.Choices))
		for _, choice := range distribution.Choices {
			if err := choice.Validate(); err != nil {
				return fmt.Errorf("categorical choice: %w", err)
			}
			encoded, _ := json.Marshal(choice)
			key := string(encoded)
			if _, duplicate := seen[key]; duplicate {
				return errors.New("categorical distribution contains duplicate choices")
			}
			seen[key] = struct{}{}
		}
	default:
		return errors.New("search distribution kind is invalid")
	}
	return nil
}

func (space SearchSpace) Validate() error {
	if len(space) == 0 || len(space) > maxParameters {
		return errors.New("search space is empty or oversized")
	}
	for name, distribution := range space {
		if !validName(name) {
			return fmt.Errorf("search parameter name %q is invalid", name)
		}
		if err := distribution.Validate(); err != nil {
			return fmt.Errorf("search parameter %q: %w", name, err)
		}
	}
	return nil
}

// DigestSearchSpace validates and hashes a search space using deterministic
// JSON map-key ordering.
func DigestSearchSpace(space SearchSpace) (string, error) {
	if err := space.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(space)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (spec StudySpec) Validate() error {
	if spec.Plan.IsZero() || spec.Plan.Kind() != research.KindPlan {
		return errors.New("search Study must be scoped to one canonical Plan")
	}
	if err := validateExactOpaque(spec.PlanRevision, false); err != nil {
		return errors.New("search Study Plan revision is invalid")
	}
	if !validName(spec.StudyKey) {
		return errors.New("search Study key is invalid")
	}
	if len(spec.Objectives) == 0 || len(spec.Objectives) > maxObjectives {
		return errors.New("search Study objectives are empty or oversized")
	}
	seen := make(map[string]struct{}, len(spec.Objectives))
	for _, objective := range spec.Objectives {
		if !validName(objective.Name) {
			return fmt.Errorf("search objective %q is invalid", objective.Name)
		}
		if objective.Direction != DirectionMinimize && objective.Direction != DirectionMaximize {
			return fmt.Errorf("search objective %q has invalid direction", objective.Name)
		}
		if _, duplicate := seen[objective.Name]; duplicate {
			return fmt.Errorf("search objective %q is duplicated", objective.Name)
		}
		seen[objective.Name] = struct{}{}
	}
	digest, err := DigestSearchSpace(spec.SearchSpace)
	if err != nil {
		return err
	}
	if spec.SearchSpaceDigest != digest {
		return errors.New("search Study search-space digest does not match its content")
	}
	return nil
}

// NormalizeExternalStudyIdentity strips credentials from the display URI and
// rejects any identity field whose safe representation would differ. Context
// must resolve credentials out of band; it is never a connection string.
func NormalizeExternalStudyIdentity(input ExternalStudyIdentity, policy provider.RedactionPolicy) (ExternalStudyIdentity, error) {
	if !validName(input.Adapter) || !validName(input.Context) {
		return ExternalStudyIdentity{}, errors.New("external Study adapter or context is invalid")
	}
	if err := validateExactOpaqueWithPolicy(input.StudyID, false, policy); err != nil {
		return ExternalStudyIdentity{}, errors.New("external Study id is invalid or sensitive")
	}
	out := input
	if input.URI != "" {
		safe, err := provider.SanitizeURIWithPolicy(input.URI, policy)
		if err != nil {
			return ExternalStudyIdentity{}, errors.New("external Study URI is unsafe")
		}
		out.URI = safe
	}
	return out, nil
}

func (identity ExternalStudyIdentity) Validate() error {
	normalized, err := NormalizeExternalStudyIdentity(identity, provider.DefaultRedactionPolicy())
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(identity, normalized) {
		return errors.New("external Study identity is not sanitized")
	}
	return nil
}

func (study StudyRef) Validate() error {
	if study.Plan.IsZero() || study.Plan.Kind() != research.KindPlan {
		return errors.New("Study reference must name one canonical Plan")
	}
	if err := validateExactOpaque(study.PlanRevision, false); err != nil {
		return errors.New("Study reference Plan revision is invalid")
	}
	if !validName(study.StudyKey) {
		return errors.New("Study reference key is invalid")
	}
	return study.External.Validate()
}

func (request OpenStudyRequest) Validate() error {
	if err := request.Spec.Validate(); err != nil {
		return err
	}
	if err := validateIdempotencyKey(request.IdempotencyKey); err != nil {
		return err
	}
	if request.Resume != nil {
		if err := request.Resume.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (result OpenStudyResult) ValidateFor(request OpenStudyRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := result.Study.Validate(); err != nil {
		return err
	}
	if !sameSpecScope(result.Study, request.Spec) {
		return errors.New("opened Study escaped its canonical Plan scope")
	}
	if request.Resume != nil && !reflect.DeepEqual(result.Study.External, *request.Resume) {
		return errors.New("resumed Study returned a different external identity")
	}
	return result.Receipt.validateFor(request.IdempotencyKey)
}

func (trial TrialIdentity) Validate() error {
	if err := validateExactOpaque(trial.TrialID, false); err != nil {
		return errors.New("external trial id is invalid or sensitive")
	}
	if trial.Number != nil && *trial.Number < 0 {
		return errors.New("external trial number cannot be negative")
	}
	return nil
}

func (request AskRequest) Validate() error {
	if err := request.Study.Validate(); err != nil {
		return err
	}
	return validateIdempotencyKey(request.IdempotencyKey)
}

func (result AskResult) ValidateFor(request AskRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(result.Study, request.Study) {
		return errors.New("trial suggestion escaped its Study scope")
	}
	if err := result.Trial.Validate(); err != nil {
		return err
	}
	if err := validateParameters(result.Parameters, provider.DefaultRedactionPolicy(), false); err != nil {
		return err
	}
	return result.Receipt.validateFor(request.IdempotencyKey)
}

func (request TellRequest) Validate() error {
	if err := request.Study.Validate(); err != nil {
		return err
	}
	if err := request.Trial.Validate(); err != nil {
		return err
	}
	if request.State != TrialComplete && request.State != TrialFailed {
		return errors.New("tell state must be complete or failed")
	}
	if request.State == TrialComplete && len(request.Values) == 0 {
		return errors.New("completed trial must report at least one objective value")
	}
	if err := validateMetrics(request.Values); err != nil {
		return err
	}
	if request.Reason != "" {
		if err := validateExactOpaque(request.Reason, true); err != nil {
			return errors.New("tell reason is unsafe")
		}
	}
	return validateIdempotencyKey(request.IdempotencyKey)
}

func (result TellResult) ValidateFor(request TellRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(result.Study, request.Study) || !sameTrial(result.Trial, request.Trial) {
		return errors.New("tell result escaped its Study or trial scope")
	}
	return result.Receipt.validateFor(request.IdempotencyKey)
}

func (request PruneRequest) Validate() error {
	if err := request.Study.Validate(); err != nil {
		return err
	}
	if err := request.Trial.Validate(); err != nil {
		return err
	}
	if request.Step < 0 {
		return errors.New("prune step cannot be negative")
	}
	if err := validateMetrics(request.Values); err != nil {
		return err
	}
	if request.Reason == "" {
		return errors.New("prune reason is required")
	}
	if err := validateExactOpaque(request.Reason, true); err != nil {
		return errors.New("prune reason is unsafe")
	}
	return validateIdempotencyKey(request.IdempotencyKey)
}

func (result PruneResult) ValidateFor(request PruneRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(result.Study, request.Study) || !sameTrial(result.Trial, request.Trial) {
		return errors.New("prune result escaped its Study or trial scope")
	}
	return result.Receipt.validateFor(request.IdempotencyKey)
}

func (request ObserveRequest) Validate() error { return request.Study.Validate() }

func (receipt MutationReceipt) validateFor(key string) error {
	if receipt.IdempotencyKey != key {
		return errors.New("mutation receipt has the wrong idempotency key")
	}
	if receipt.AppliedAt.IsZero() || !isUTC(receipt.AppliedAt) {
		return errors.New("mutation receipt time must be non-zero UTC")
	}
	if receipt.ExternalMutation != "" {
		if err := validateExactOpaque(receipt.ExternalMutation, false); err != nil {
			return errors.New("external mutation id is invalid or sensitive")
		}
	}
	return nil
}

func sameSpecScope(study StudyRef, spec StudySpec) bool {
	return study.Plan == spec.Plan && study.PlanRevision == spec.PlanRevision && study.StudyKey == spec.StudyKey
}

func sameTrial(left, right TrialIdentity) bool {
	return reflect.DeepEqual(left, right)
}

func validateParameters(parameters map[string]Value, policy provider.RedactionPolicy, redact bool) error {
	if len(parameters) == 0 || len(parameters) > maxParameters {
		return errors.New("trial parameters are empty or oversized")
	}
	for name, value := range parameters {
		if !validName(name) {
			return fmt.Errorf("trial parameter name %q is invalid", name)
		}
		if err := value.validateWithPolicy(policy, redact); err != nil {
			return fmt.Errorf("trial parameter %q: %w", name, err)
		}
	}
	return nil
}

func validateMetrics(values map[string]float64) error {
	if len(values) > maxObjectives {
		return errors.New("objective values are oversized")
	}
	for name, value := range values {
		if !validName(name) || !finite(value) {
			return fmt.Errorf("objective value %q is invalid", name)
		}
	}
	return nil
}

func validateIdempotencyKey(key string) error {
	if err := validateExactOpaque(key, false); err != nil {
		return errors.New("idempotency key is invalid or sensitive")
	}
	return nil
}

func validateExactOpaque(value string, allowSpaces bool) error {
	return validateExactOpaqueWithPolicy(value, allowSpaces, provider.DefaultRedactionPolicy())
}

func validateExactOpaqueWithPolicy(value string, allowSpaces bool, policy provider.RedactionPolicy) error {
	if value == "" || len(value) > maxOpaqueBytes || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("invalid opaque text")
	}
	if !allowSpaces && strings.ContainsAny(value, " \t") {
		return errors.New("opaque identifier contains whitespace")
	}
	if value != strings.TrimSpace(value) {
		return errors.New("opaque text has surrounding whitespace")
	}
	safe, truncated, err := provider.SanitizeText(value, policy)
	if err != nil || truncated || safe != value {
		return errors.New("opaque text is sensitive or oversized")
	}
	return nil
}

func validName(value string) bool {
	if value == "" || len(value) > maxNameBytes || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._:-", character)) {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}

func finite(value float64) bool   { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func integral(value float64) bool { return finite(value) && math.Trunc(value) == value }

func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}
