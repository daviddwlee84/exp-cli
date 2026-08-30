// Package searchadapter defines the narrow boundary between one canonical Plan
// and a provider-owned parameter-search Study. Search adapters suggest and
// account for trials; they never own exp's global queue, resource allocation,
// scientific findings, releases, or promotions.
package searchadapter

import (
	"context"
	"errors"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const ContractVersion = "exp.search-adapter/v1"

var (
	// ErrUnsupportedCapability means the adapter positively does not implement
	// the requested contract capability. Unknown support should be reported by
	// Descriptor rather than guessed at invocation time.
	ErrUnsupportedCapability = errors.New("search adapter capability is unsupported")
	// ErrIdempotencyConflict means a mutation key was previously used for a
	// request with a different normalized payload.
	ErrIdempotencyConflict = errors.New("search adapter idempotency conflict")
)

// Capability is one operation in the versioned Study-search contract.
type Capability string

const (
	CapabilityStudyOpen    Capability = "study.open"
	CapabilityStudyResume  Capability = "study.resume"
	CapabilityTrialAsk     Capability = "trial.ask"
	CapabilityTrialTell    Capability = "trial.tell"
	CapabilityTrialPrune   Capability = "trial.prune"
	CapabilityStudyObserve Capability = "study.observe"
)

var allCapabilities = []Capability{
	CapabilityStudyOpen,
	CapabilityStudyResume,
	CapabilityTrialAsk,
	CapabilityTrialTell,
	CapabilityTrialPrune,
	CapabilityStudyObserve,
}

// AllCapabilities returns the closed v1 vocabulary in stable order.
func AllCapabilities() []Capability { return append([]Capability(nil), allCapabilities...) }

// Responsibility is an authority boundary, not an adapter feature.
type Responsibility string

const (
	ResponsibilityPlanStudySearch    Responsibility = "plan_study_search"
	ResponsibilityGlobalQueue        Responsibility = "global_queue"
	ResponsibilityResourceAllocation Responsibility = "resource_allocation"
	ResponsibilityAttemptScheduling  Responsibility = "attempt_scheduling"
	ResponsibilityExperimentClosure  Responsibility = "experiment_closure"
	ResponsibilityCanonicalFindings  Responsibility = "canonical_findings"
	ResponsibilityReleases           Responsibility = "releases"
	ResponsibilityPromotions         Responsibility = "promotions"
)

var forbiddenResponsibilities = []Responsibility{
	ResponsibilityGlobalQueue,
	ResponsibilityResourceAllocation,
	ResponsibilityAttemptScheduling,
	ResponsibilityExperimentClosure,
	ResponsibilityCanonicalFindings,
	ResponsibilityReleases,
	ResponsibilityPromotions,
}

// AuthorityBoundary is deliberately fixed by the contract. Adapters cannot
// expand their authority by declaring additional capabilities.
type AuthorityBoundary struct {
	Owns       []Responsibility `json:"owns"`
	DoesNotOwn []Responsibility `json:"does_not_own"`
}

// ContractBoundary returns a defensive copy of the v1 authority boundary.
func ContractBoundary() AuthorityBoundary {
	return AuthorityBoundary{
		Owns:       []Responsibility{ResponsibilityPlanStudySearch},
		DoesNotOwn: append([]Responsibility(nil), forbiddenResponsibilities...),
	}
}

// CapabilityReport uses the provider tri-state so an adapter never converts
// an unverified upstream feature into optimistic support.
type CapabilityReport struct {
	Capability Capability       `json:"capability"`
	Support    provider.Support `json:"support"`
	Reason     string           `json:"reason,omitempty"`
}

// Descriptor reports the adapter, upstream, contract, capabilities, and fixed
// ownership boundary. Version probes are explicit adapter operations; loading
// this Go package never installs software, performs authentication, or makes
// network contact.
type Descriptor struct {
	Name            string             `json:"name"`
	AdapterVersion  string             `json:"adapter_version"`
	UpstreamName    string             `json:"upstream_name"`
	UpstreamVersion string             `json:"upstream_version,omitempty"`
	ContractVersion string             `json:"contract_version"`
	Capabilities    []CapabilityReport `json:"capabilities"`
	Boundary        AuthorityBoundary  `json:"boundary"`
}

// Direction is one objective optimization direction.
type Direction string

const (
	DirectionMinimize Direction = "minimize"
	DirectionMaximize Direction = "maximize"
)

type Objective struct {
	Name      string    `json:"name"`
	Direction Direction `json:"direction"`
}

// Value is a tagged scalar used by categorical choices and trial suggestions.
// The tagged form preserves integer and boolean identity without accepting
// arbitrary provider-owned JSON objects.
type ValueKind string

const (
	ValueString  ValueKind = "string"
	ValueInteger ValueKind = "integer"
	ValueNumber  ValueKind = "number"
	ValueBoolean ValueKind = "boolean"
)

type Value struct {
	Kind    ValueKind `json:"kind"`
	String  string    `json:"string,omitempty"`
	Integer int64     `json:"integer,omitempty"`
	Number  float64   `json:"number,omitempty"`
	Boolean bool      `json:"boolean,omitempty"`
}

// Distribution is the provider-neutral v1 search-space vocabulary. Integer
// ranges use integral Low/High/Step values. Categorical ranges use Choices.
type DistributionKind string

const (
	DistributionFloat       DistributionKind = "float"
	DistributionInteger     DistributionKind = "integer"
	DistributionCategorical DistributionKind = "categorical"
)

type Distribution struct {
	Kind    DistributionKind `json:"kind"`
	Low     *float64         `json:"low,omitempty"`
	High    *float64         `json:"high,omitempty"`
	Step    *float64         `json:"step,omitempty"`
	Log     bool             `json:"log,omitempty"`
	Choices []Value          `json:"choices,omitempty"`
}

type SearchSpace map[string]Distribution

// StudySpec binds exactly one provider-owned Study to one canonical Plan
// revision. StudyKey permits an explicit name without making it global.
type StudySpec struct {
	Plan              research.ID `json:"plan_id"`
	PlanRevision      string      `json:"plan_revision"`
	StudyKey          string      `json:"study_key"`
	Objectives        []Objective `json:"objectives"`
	SearchSpace       SearchSpace `json:"search_space"`
	SearchSpaceDigest string      `json:"search_space_digest"`
}

// ExternalStudyIdentity is the complete provider-owned identity needed to
// resume a Study after process restart. Context names configured storage or an
// upstream profile; secret values and storage credentials never belong here.
type ExternalStudyIdentity struct {
	Adapter string `json:"adapter"`
	Context string `json:"context"`
	StudyID string `json:"study_id"`
	URI     string `json:"uri,omitempty"`
}

// StudyRef combines canonical scope with resumable external identity. Every
// operation after OpenStudy carries this complete value to prevent accidental
// cross-Plan trial mutation.
type StudyRef struct {
	Plan         research.ID           `json:"plan_id"`
	PlanRevision string                `json:"plan_revision"`
	StudyKey     string                `json:"study_key"`
	External     ExternalStudyIdentity `json:"external"`
}

type OpenStudyRequest struct {
	Spec           StudySpec              `json:"spec"`
	Resume         *ExternalStudyIdentity `json:"resume,omitempty"`
	IdempotencyKey string                 `json:"idempotency_key"`
}

type MutationReceipt struct {
	IdempotencyKey   string    `json:"idempotency_key"`
	ExternalMutation string    `json:"external_mutation,omitempty"`
	Replayed         bool      `json:"replayed"`
	AppliedAt        time.Time `json:"applied_at"`
}

type OpenStudyResult struct {
	Study   StudyRef        `json:"study"`
	Receipt MutationReceipt `json:"receipt"`
}

type TrialIdentity struct {
	TrialID string `json:"trial_id"`
	Number  *int64 `json:"number,omitempty"`
}

type AskRequest struct {
	Study          StudyRef `json:"study"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type AskResult struct {
	Study      StudyRef         `json:"study"`
	Trial      TrialIdentity    `json:"trial"`
	Parameters map[string]Value `json:"parameters"`
	Receipt    MutationReceipt  `json:"receipt"`
}

// TrialTerminalState contains only states reported via Tell. Pruned trials use
// Prune, keeping completion and pruning idempotency independent.
type TrialTerminalState string

const (
	TrialComplete TrialTerminalState = "complete"
	TrialFailed   TrialTerminalState = "failed"
)

type TellRequest struct {
	Study          StudyRef           `json:"study"`
	Trial          TrialIdentity      `json:"trial"`
	State          TrialTerminalState `json:"state"`
	Values         map[string]float64 `json:"values"`
	Reason         string             `json:"reason,omitempty"`
	IdempotencyKey string             `json:"idempotency_key"`
}

type TellResult struct {
	Study   StudyRef        `json:"study"`
	Trial   TrialIdentity   `json:"trial"`
	Receipt MutationReceipt `json:"receipt"`
}

type PruneRequest struct {
	Study          StudyRef           `json:"study"`
	Trial          TrialIdentity      `json:"trial"`
	Step           int64              `json:"step"`
	Values         map[string]float64 `json:"values,omitempty"`
	Reason         string             `json:"reason"`
	IdempotencyKey string             `json:"idempotency_key"`
}

type PruneResult struct {
	Study   StudyRef        `json:"study"`
	Trial   TrialIdentity   `json:"trial"`
	Receipt MutationReceipt `json:"receipt"`
}

type ObserveRequest struct {
	Study StudyRef `json:"study"`
}

type StudyState string

const (
	StudyStateActive   StudyState = "active"
	StudyStateComplete StudyState = "complete"
	StudyStateUnknown  StudyState = "unknown"
)

type TrialState string

const (
	TrialStateWaiting  TrialState = "waiting"
	TrialStateRunning  TrialState = "running"
	TrialStateComplete TrialState = "complete"
	TrialStatePruned   TrialState = "pruned"
	TrialStateFailed   TrialState = "failed"
	TrialStateUnknown  TrialState = "unknown"
)

type TrialObservation struct {
	Trial       TrialIdentity      `json:"trial"`
	State       TrialState         `json:"state"`
	NativeState string             `json:"native_state,omitempty"`
	Parameters  map[string]Value   `json:"parameters,omitempty"`
	Values      map[string]float64 `json:"values,omitempty"`
	Reason      string             `json:"reason,omitempty"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Observation is disposable provider state. NormalizeObservation must be
// applied before it crosses the adapter boundary or enters operational cache.
// It never establishes canonical evidence or a scientific conclusion.
type Observation struct {
	Study       StudyRef           `json:"study"`
	State       StudyState         `json:"state"`
	NativeState string             `json:"native_state,omitempty"`
	Trials      []TrialObservation `json:"trials"`
	Metadata    map[string]any     `json:"metadata,omitempty"`
	Source      string             `json:"source"`
	ObservedAt  time.Time          `json:"observed_at"`
	Partial     bool               `json:"partial"`
	Diagnostics []Diagnostic       `json:"diagnostics"`
}

// Adapter is intentionally smaller than an experiment provider. Its only
// mutable domain is a Study already scoped to one Plan.
type Adapter interface {
	Describe(context.Context) (Descriptor, error)
	OpenStudy(context.Context, OpenStudyRequest) (OpenStudyResult, error)
	Ask(context.Context, AskRequest) (AskResult, error)
	Tell(context.Context, TellRequest) (TellResult, error)
	Prune(context.Context, PruneRequest) (PruneResult, error)
	Observe(context.Context, ObserveRequest) (Observation, error)
}
