package searchadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/searchadapter"
)

func TestDescriptorRequiresCompleteCapabilitiesAndFixedAuthority(t *testing.T) {
	descriptor := validDescriptor()
	if err := descriptor.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	missing := descriptor
	missing.Capabilities = missing.Capabilities[:len(missing.Capabilities)-1]
	if err := missing.Validate(); err == nil {
		t.Fatal("Validate() accepted a descriptor missing a capability report")
	}

	expanded := descriptor
	expanded.Boundary.Owns = append(expanded.Boundary.Owns, searchadapter.ResponsibilityGlobalQueue)
	if err := expanded.Validate(); err == nil {
		t.Fatal("Validate() let a search adapter claim the global queue")
	}

	reduced := descriptor
	reduced.Boundary.DoesNotOwn = reduced.Boundary.DoesNotOwn[:len(reduced.Boundary.DoesNotOwn)-1]
	if err := reduced.Validate(); err == nil {
		t.Fatal("Validate() accepted an incomplete forbidden-authority list")
	}
}

func TestStudySpecIsBoundToPlanAndSearchSpaceDigest(t *testing.T) {
	spec := validSpec(t)
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	wrongKind, err := research.ParseIDForKind("exp_01a01e66-f8e0-7202-8000-000000000203", research.KindExperiment)
	if err != nil {
		t.Fatal(err)
	}
	spec.Plan = wrongKind
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate() accepted an Experiment as Study scope")
	}

	spec = validSpec(t)
	spec.SearchSpace["learning_rate"] = searchadapter.Distribution{Kind: searchadapter.DistributionFloat, Low: ptr(0.01), High: ptr(0.1)}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate() accepted a stale search-space digest")
	}
}

func TestResultValidationRejectsScopeEscape(t *testing.T) {
	spec := validSpec(t)
	request := searchadapter.OpenStudyRequest{Spec: spec, IdempotencyKey: "open-0001"}
	result := searchadapter.OpenStudyResult{
		Study: studyFor(spec),
		Receipt: searchadapter.MutationReceipt{
			IdempotencyKey: request.IdempotencyKey,
			AppliedAt:      time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
		},
	}
	if err := result.ValidateFor(request); err != nil {
		t.Fatalf("ValidateFor() error = %v", err)
	}

	other := validSpec(t)
	other.StudyKey = "other-study"
	result.Study = studyFor(other)
	if err := result.ValidateFor(request); err == nil {
		t.Fatal("ValidateFor() accepted a Study returned for another scope")
	}
}

func TestIdempotencyTokenDetectsReplayAndConflict(t *testing.T) {
	request := searchadapter.AskRequest{Study: studyFor(validSpec(t)), IdempotencyKey: "ask-0001"}
	first, err := searchadapter.NewIdempotencyToken(request.IdempotencyKey, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := searchadapter.NewIdempotencyToken(request.IdempotencyKey, request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := first.CheckReplay(second)
	if err != nil || !replay {
		t.Fatalf("CheckReplay() = %v, %v; want true, nil", replay, err)
	}

	request.Study.StudyKey = "other-study"
	conflict, err := searchadapter.NewIdempotencyToken(request.IdempotencyKey, request)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err = first.CheckReplay(conflict); replay || !errors.Is(err, searchadapter.ErrIdempotencyConflict) {
		t.Fatalf("CheckReplay(conflict) = %v, %v", replay, err)
	}
}

func TestReferenceAdapterReplaysAskWithoutAllocatingAnotherTrial(t *testing.T) {
	adapter := newReferenceAdapter(t)
	request := searchadapter.AskRequest{Study: adapter.study, IdempotencyKey: "ask-0001"}

	first, err := adapter.Ask(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.Ask(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Trial.TrialID != second.Trial.TrialID || !second.Receipt.Replayed || adapter.allocated != 1 {
		t.Fatalf("idempotent Ask allocated=%d first=%+v second=%+v", adapter.allocated, first, second)
	}
	if err := second.ValidateFor(request); err != nil {
		t.Fatalf("ValidateFor() error = %v", err)
	}

	conflict := request
	conflict.Study.StudyKey = "changed-study"
	if _, err := adapter.Ask(context.Background(), conflict); !errors.Is(err, searchadapter.ErrIdempotencyConflict) {
		t.Fatalf("Ask(conflict) error = %v", err)
	}
}

func TestNormalizeObservationRedactsAndFailsUnknownStatesClosed(t *testing.T) {
	spec := validSpec(t)
	study := studyFor(spec)
	study.External.URI = "https://alice:secret-canary@example.test/studies/one?token=secret-canary&view=summary"
	number := int64(7)
	policy := provider.DefaultRedactionPolicy().WithSecrets("secret-canary")
	input := searchadapter.Observation{
		Study:       study,
		State:       searchadapter.StudyState("future-state"),
		NativeState: "FUTURE secret-canary",
		Source:      "reference adapter secret-canary",
		ObservedAt:  time.Date(2026, 8, 30, 18, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
		Trials: []searchadapter.TrialObservation{{
			Trial:       searchadapter.TrialIdentity{TrialID: "trial-7", Number: &number},
			State:       searchadapter.TrialState("PAUSED_BY_NEW_VERSION"),
			NativeState: "PAUSED_BY_NEW_VERSION",
			Parameters: map[string]searchadapter.Value{
				"optimizer": {Kind: searchadapter.ValueString, String: "adam-secret-canary"},
			},
			Values: map[string]float64{"loss": 0.25},
			Reason: "provider said secret-canary",
		}},
		Metadata: map[string]any{
			"token":  "secret-canary",
			"nested": map[string]any{"envs": map[string]any{"TOKEN": "secret-canary"}},
		},
		Diagnostics: []searchadapter.Diagnostic{{Code: "UPSTREAM BAD", Message: "secret-canary was returned"}},
	}

	out, err := searchadapter.NormalizeObservation(input, policy)
	if err != nil {
		t.Fatalf("NormalizeObservation() error = %v", err)
	}
	if out.State != searchadapter.StudyStateUnknown || out.Trials[0].State != searchadapter.TrialStateUnknown || !out.Partial {
		t.Fatalf("unknown states did not fail closed: %+v", out)
	}
	if out.ObservedAt.Location() != time.UTC {
		t.Fatalf("ObservedAt location = %v, want UTC", out.ObservedAt.Location())
	}
	if out.Study.External.URI != "https://example.test/studies/one?view=summary" {
		t.Fatalf("sanitized URI = %q", out.Study.External.URI)
	}
	if _, found := out.Metadata["nested"].(map[string]any)["envs"]; found {
		t.Fatal("NormalizeObservation() retained recursively nested envs")
	}
	encoded := mustJSON(t, out)
	if contains(encoded, "secret-canary") {
		t.Fatalf("normalized observation leaked secret: %s", encoded)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("normalized Validate() error = %v", err)
	}
}

func TestNormalizeObservationRejectsSensitiveResumableIdentity(t *testing.T) {
	input := searchadapter.Observation{
		Study:      studyFor(validSpec(t)),
		State:      searchadapter.StudyStateActive,
		Source:     "reference-adapter",
		ObservedAt: time.Now().UTC(),
	}
	input.Study.External.StudyID = "study-secret-canary"
	policy := provider.DefaultRedactionPolicy().WithSecrets("secret-canary")
	if _, err := searchadapter.NormalizeObservation(input, policy); err == nil {
		t.Fatal("NormalizeObservation() silently redacted a resumable Study id")
	}
}

func validDescriptor() searchadapter.Descriptor {
	reports := make([]searchadapter.CapabilityReport, 0, len(searchadapter.AllCapabilities()))
	for _, capability := range searchadapter.AllCapabilities() {
		reports = append(reports, searchadapter.CapabilityReport{Capability: capability, Support: provider.SupportSupported})
	}
	return searchadapter.Descriptor{
		Name: "reference", AdapterVersion: "1.0.0", UpstreamName: "optuna", UpstreamVersion: "4.5.0",
		ContractVersion: searchadapter.ContractVersion, Capabilities: reports, Boundary: searchadapter.ContractBoundary(),
	}
}

func validSpec(t *testing.T) searchadapter.StudySpec {
	t.Helper()
	plan, err := research.ParseIDForKind("plan_01a01e66-f8e0-7202-8000-000000000202", research.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	space := searchadapter.SearchSpace{
		"learning_rate": {Kind: searchadapter.DistributionFloat, Low: ptr(0.0001), High: ptr(0.1), Log: true},
		"optimizer": {Kind: searchadapter.DistributionCategorical, Choices: []searchadapter.Value{
			{Kind: searchadapter.ValueString, String: "adam"},
			{Kind: searchadapter.ValueString, String: "sgd"},
		}},
	}
	digest, err := searchadapter.DigestSearchSpace(space)
	if err != nil {
		t.Fatal(err)
	}
	return searchadapter.StudySpec{
		Plan: plan, PlanRevision: "0123456789abcdef0123456789abcdef01234567", StudyKey: "main-sweep",
		Objectives:  []searchadapter.Objective{{Name: "loss", Direction: searchadapter.DirectionMinimize}},
		SearchSpace: space, SearchSpaceDigest: digest,
	}
}

func studyFor(spec searchadapter.StudySpec) searchadapter.StudyRef {
	return searchadapter.StudyRef{
		Plan: spec.Plan, PlanRevision: spec.PlanRevision, StudyKey: spec.StudyKey,
		External: searchadapter.ExternalStudyIdentity{
			Adapter: "reference", Context: "local", StudyID: "study-0001", URI: "https://example.test/studies/one",
		},
	}
}

type referenceAdapter struct {
	study     searchadapter.StudyRef
	tokens    map[string]searchadapter.IdempotencyToken
	results   map[string]searchadapter.AskResult
	allocated int
}

var _ searchadapter.Adapter = (*referenceAdapter)(nil)

func newReferenceAdapter(t *testing.T) *referenceAdapter {
	return &referenceAdapter{
		study: studyFor(validSpec(t)), tokens: map[string]searchadapter.IdempotencyToken{}, results: map[string]searchadapter.AskResult{},
	}
}

func (adapter *referenceAdapter) Describe(context.Context) (searchadapter.Descriptor, error) {
	return validDescriptor(), nil
}

func (adapter *referenceAdapter) OpenStudy(context.Context, searchadapter.OpenStudyRequest) (searchadapter.OpenStudyResult, error) {
	return searchadapter.OpenStudyResult{}, searchadapter.ErrUnsupportedCapability
}

func (adapter *referenceAdapter) Ask(_ context.Context, request searchadapter.AskRequest) (searchadapter.AskResult, error) {
	token, err := searchadapter.NewIdempotencyToken(request.IdempotencyKey, request)
	if err != nil {
		return searchadapter.AskResult{}, err
	}
	if previous, found := adapter.tokens[request.IdempotencyKey]; found {
		replay, checkErr := previous.CheckReplay(token)
		if checkErr != nil {
			return searchadapter.AskResult{}, checkErr
		}
		if replay {
			result := adapter.results[request.IdempotencyKey]
			result.Receipt.Replayed = true
			return result, nil
		}
	}
	adapter.allocated++
	number := int64(adapter.allocated)
	result := searchadapter.AskResult{
		Study: request.Study, Trial: searchadapter.TrialIdentity{TrialID: "trial-0001", Number: &number},
		Parameters: map[string]searchadapter.Value{"learning_rate": {Kind: searchadapter.ValueNumber, Number: 0.01}},
		Receipt: searchadapter.MutationReceipt{
			IdempotencyKey: request.IdempotencyKey, AppliedAt: time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
		},
	}
	adapter.tokens[request.IdempotencyKey] = token
	adapter.results[request.IdempotencyKey] = result
	return result, nil
}

func (adapter *referenceAdapter) Tell(context.Context, searchadapter.TellRequest) (searchadapter.TellResult, error) {
	return searchadapter.TellResult{}, searchadapter.ErrUnsupportedCapability
}

func (adapter *referenceAdapter) Prune(context.Context, searchadapter.PruneRequest) (searchadapter.PruneResult, error) {
	return searchadapter.PruneResult{}, searchadapter.ErrUnsupportedCapability
}

func (adapter *referenceAdapter) Observe(context.Context, searchadapter.ObserveRequest) (searchadapter.Observation, error) {
	return searchadapter.Observation{}, searchadapter.ErrUnsupportedCapability
}

func ptr(value float64) *float64 { return &value }

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func contains(value, substring string) bool { return strings.Contains(value, substring) }
