package research

import "time"

const MigrationExtension = "io.github.daviddwlee84.exp-cli.harness-v0"

// Extensions is the only open schema container. Each top-level key is a
// lower-case reverse-DNS namespace and each value is a TOML table.
type Extensions map[string]map[string]any

// Common contains fields shared by every typed record. Project and the
// singleton Policy are intentionally ID-less special records.
type Common struct {
	Schema        Schema    `toml:"schema"`
	ID            ID        `toml:"id"`
	Title         string    `toml:"title"`
	CreatedAt     time.Time `toml:"created_at"`
	UpdatedAt     time.Time `toml:"updated_at"`
	LegacyAliases []string  `toml:"legacy_aliases,omitempty"`
	Tags          []string  `toml:"tags,omitempty"`
}

// Record is one typed front-matter value. Markdown bodies belong to record.Document.
type Record interface {
	GetSchema() Schema
	GetKind() Kind
	GetID() (ID, bool)
	GetCommon() *Common
	GetExtensions() Extensions
}

type Project struct {
	Schema          Schema     `toml:"schema"`
	ProjectID       UUID       `toml:"project_id"`
	Name            string     `toml:"name"`
	CreatedAt       time.Time  `toml:"created_at"`
	ExperimentsRoot string     `toml:"experiments_root"`
	Extensions      Extensions `toml:"extensions,omitempty"`
}

func (p *Project) GetSchema() Schema         { return p.Schema }
func (*Project) GetKind() Kind               { return KindProject }
func (*Project) GetID() (ID, bool)           { return ID{}, false }
func (*Project) GetCommon() *Common          { return nil }
func (p *Project) GetExtensions() Extensions { return p.Extensions }

type Priority string

const (
	PriorityP1      Priority = "P1"
	PriorityP2      Priority = "P2"
	PriorityP3      Priority = "P3"
	PriorityUnknown Priority = "P?"
)

type Effort string

const (
	EffortS  Effort = "S"
	EffortM  Effort = "M"
	EffortL  Effort = "L"
	EffortXL Effort = "XL"
)

type PlanState string

const (
	PlanQueued    PlanState = "queued"
	PlanStarted   PlanState = "started"
	PlanCompleted PlanState = "completed"
	PlanDropped   PlanState = "dropped"
)

type ExpectedPayoff struct {
	Summary  string   `toml:"summary"`
	Metric   string   `toml:"metric"`
	Unit     string   `toml:"unit"`
	Estimate *float64 `toml:"estimate,omitempty"`
}

type FindingDependency struct {
	Finding      ID     `toml:"finding"`
	Revision     string `toml:"revision"`
	BeliefDigest string `toml:"belief_digest"`
}

type ResourceNeed struct {
	Pool           ID      `toml:"pool"`
	Units          uint64  `toml:"units"`
	EstimatedHours float64 `toml:"estimated_hours"`
}

type UtilityEstimate struct {
	Probability     float64 `toml:"probability"`
	Impact          float64 `toml:"impact"`
	InformationGain float64 `toml:"information_gain"`
	UnblockValue    float64 `toml:"unblock_value"`
	RiskPenalty     float64 `toml:"risk_penalty"`
}

type Plan struct {
	Common
	Priority            Priority            `toml:"priority"`
	Effort              Effort              `toml:"effort"`
	State               PlanState           `toml:"state"`
	Assumptions         []ID                `toml:"assumptions,omitempty"`
	ResultingExperiment ID                  `toml:"resulting_experiment,omitempty"`
	ExpectedPayoff      ExpectedPayoff      `toml:"expected_payoff"`
	Idea                ID                  `toml:"idea,omitempty"`
	PrimaryCluster      string              `toml:"primary_cluster,omitempty"`
	Classification      *Classification     `toml:"classification,omitempty"`
	Dependencies        []FindingDependency `toml:"dependencies,omitempty"`
	Resources           []ResourceNeed      `toml:"resources,omitempty"`
	Utility             *UtilityEstimate    `toml:"utility,omitempty"`
	Extensions          Extensions          `toml:"extensions,omitempty"`
}

func (p *Plan) GetSchema() Schema         { return p.Schema }
func (*Plan) GetKind() Kind               { return KindPlan }
func (p *Plan) GetID() (ID, bool)         { return p.ID, !p.ID.IsZero() }
func (p *Plan) GetCommon() *Common        { return &p.Common }
func (p *Plan) GetExtensions() Extensions { return p.Extensions }

type ExperimentLifecycle string

const (
	LifecyclePlanned ExperimentLifecycle = "planned"
	LifecycleActive  ExperimentLifecycle = "active"
	LifecycleClosed  ExperimentLifecycle = "closed"
)

type ExperimentClosure string

const (
	ClosureConcluded  ExperimentClosure = "concluded"
	ClosureAbandoned  ExperimentClosure = "abandoned"
	ClosureSuperseded ExperimentClosure = "superseded"
)

type Verdict string

const (
	VerdictSupported    Verdict = "supported"
	VerdictRefuted      Verdict = "refuted"
	VerdictInconclusive Verdict = "inconclusive"
	VerdictInvalid      Verdict = "invalid"
)

type ExperimentKind string

const (
	ExperimentSingleFactor  ExperimentKind = "single_factor"
	ExperimentFactorial     ExperimentKind = "factorial"
	ExperimentObservational ExperimentKind = "observational"
	ExperimentReplication   ExperimentKind = "replication"
	ExperimentSweep         ExperimentKind = "sweep"
	ExperimentCombination   ExperimentKind = "combination"
)

type Design struct {
	Question          string         `toml:"question"`
	Hypothesis        string         `toml:"hypothesis"`
	Kind              ExperimentKind `toml:"kind"`
	PrimaryFactor     string         `toml:"primary_factor"`
	SecondaryFactors  []string       `toml:"secondary_factors"`
	Baseline          string         `toml:"baseline"`
	ComparabilitySpec string         `toml:"comparability_spec"`
	SuccessCriteria   []string       `toml:"success_criteria"`
	DecisionRule      string         `toml:"decision_rule"`
	DesignLockedAt    *time.Time     `toml:"design_locked_at,omitempty"`
	DesignDigest      string         `toml:"design_digest,omitempty"`
}

type Amendment struct {
	AmendedAt      time.Time `toml:"amended_at"`
	Reason         string    `toml:"reason"`
	PreviousDigest string    `toml:"previous_digest"`
	NewDigest      string    `toml:"new_digest"`
	Changes        []string  `toml:"changes"`
}

type ClosureDetail struct {
	Reason       string `toml:"reason,omitempty"`
	SupersededBy ID     `toml:"superseded_by,omitempty"`
}

type EvidenceDisposition string

const (
	EvidenceIncluded EvidenceDisposition = "included"
	EvidenceExcluded EvidenceDisposition = "excluded"
)

type ConclusionEvidence struct {
	Run         ID                  `toml:"run"`
	Disposition EvidenceDisposition `toml:"disposition"`
	Reason      string              `toml:"reason"`
}

type Conclusion struct {
	ConcludedAt time.Time            `toml:"concluded_at"`
	Summary     string               `toml:"summary"`
	Evidence    []ConclusionEvidence `toml:"evidence"`
}

type Experiment struct {
	Common
	Lifecycle       ExperimentLifecycle `toml:"lifecycle"`
	Closure         ExperimentClosure   `toml:"closure,omitempty"`
	Verdict         Verdict             `toml:"verdict,omitempty"`
	Design          Design              `toml:"design"`
	Amendments      []Amendment         `toml:"amendments,omitempty"`
	ClosureDetail   *ClosureDetail      `toml:"closure_detail,omitempty"`
	Conclusion      *Conclusion         `toml:"conclusion,omitempty"`
	Parents         []ID                `toml:"parents,omitempty"`
	CandidateInputs []ID                `toml:"candidate_inputs,omitempty"`
	Extensions      Extensions          `toml:"extensions,omitempty"`
}

func (e *Experiment) GetSchema() Schema         { return e.Schema }
func (*Experiment) GetKind() Kind               { return KindExperiment }
func (e *Experiment) GetID() (ID, bool)         { return e.ID, !e.ID.IsZero() }
func (e *Experiment) GetCommon() *Common        { return &e.Common }
func (e *Experiment) GetExtensions() Extensions { return e.Extensions }

type RunRole string

const (
	RunBaseline   RunRole = "baseline"
	RunCandidate  RunRole = "candidate"
	RunValidation RunRole = "validation"
	RunBatch      RunRole = "batch"
)

type Run struct {
	Common
	Experiment      ID         `toml:"experiment"`
	Role            RunRole    `toml:"role"`
	Objective       string     `toml:"objective"`
	ConfigDigest    string     `toml:"config_digest,omitempty"`
	DataDigest      string     `toml:"data_digest,omitempty"`
	Seeds           []int64    `toml:"seeds,omitempty"`
	ExpectedOutputs []string   `toml:"expected_outputs,omitempty"`
	Extensions      Extensions `toml:"extensions,omitempty"`
}

func (r *Run) GetSchema() Schema         { return r.Schema }
func (*Run) GetKind() Kind               { return KindRun }
func (r *Run) GetID() (ID, bool)         { return r.ID, !r.ID.IsZero() }
func (r *Run) GetCommon() *Common        { return &r.Common }
func (r *Run) GetExtensions() Extensions { return r.Extensions }

type AttemptState string

const (
	AttemptPlanned     AttemptState = "planned"
	AttemptQueued      AttemptState = "queued"
	AttemptBlocked     AttemptState = "blocked"
	AttemptStarting    AttemptState = "starting"
	AttemptRunning     AttemptState = "running"
	AttemptSucceeded   AttemptState = "succeeded"
	AttemptFailed      AttemptState = "failed"
	AttemptCancelled   AttemptState = "cancelled"
	AttemptTimedOut    AttemptState = "timed_out"
	AttemptPreempted   AttemptState = "preempted"
	AttemptOutOfMemory AttemptState = "out_of_memory"
	AttemptUnknown     AttemptState = "unknown"
)

type ExternalRole string

const (
	ExternalRunner    ExternalRole = "runner"
	ExternalScheduler ExternalRole = "scheduler"
	ExternalTracker   ExternalRole = "tracker"
	ExternalArtifact  ExternalRole = "artifact"
	ExternalRegistry  ExternalRole = "registry"
)

const reservedProviderUnknown = "unknown"

// knownProviderRoles is the cycle-free canonical schema matrix for providers
// compiled into this release. Unknown provider slugs remain valid so newer
// producers can be read by older binaries; known providers must claim only a
// role they implement. The literal "unknown" is a reserved normalized sentinel,
// not a valid canonical provider identity.
var knownProviderRoles = map[string][]ExternalRole{
	"direct":  {ExternalRunner, ExternalScheduler},
	"dvc":     {ExternalRunner, ExternalScheduler, ExternalArtifact},
	"jupyter": {ExternalRunner},
	"marimo":  {ExternalRunner},
	"mlflow":  {ExternalRunner, ExternalTracker, ExternalArtifact, ExternalRegistry},
	"pueue":   {ExternalScheduler},
	"slurm":   {ExternalScheduler},
}

// KnownProviderRoles returns a defensive copy of the roles for a provider
// compiled into this release. known is false for forward-compatible providers.
func KnownProviderRoles(provider string) (roles []ExternalRole, known bool) {
	roles, known = knownProviderRoles[provider]
	return append([]ExternalRole(nil), roles...), known
}

// KnownProviderSupportsRole reports compatibility only for compiled providers.
// Callers must accept syntactically valid unknown providers for forward compatibility.
func KnownProviderSupportsRole(provider string, role ExternalRole) (known, supported bool) {
	roles, known := knownProviderRoles[provider]
	if !known {
		return false, false
	}
	for _, candidate := range roles {
		if candidate == role {
			return true, true
		}
	}
	return true, false
}

// MaxExternalRefBytes bounds one canonical external reference, including metadata.
const MaxExternalRefBytes = 32 << 10

type ExternalRef struct {
	Role       ExternalRole   `toml:"role" json:"role"`
	Provider   string         `toml:"provider" json:"provider"`
	Context    string         `toml:"context" json:"context"`
	NativeKind string         `toml:"native_kind" json:"native_kind"`
	NativeID   string         `toml:"native_id" json:"native_id"`
	URI        string         `toml:"uri,omitempty" json:"uri,omitempty"`
	ObservedAt *time.Time     `toml:"observed_at,omitempty" json:"observed_at,omitempty"`
	Metadata   map[string]any `toml:"metadata,omitempty" json:"metadata,omitempty"`
}

type Reproducibility string

const (
	ReproducibilityExact   Reproducibility = "exact"
	ReproducibilityBounded Reproducibility = "bounded"
	ReproducibilityPartial Reproducibility = "partial"
	ReproducibilityUnknown Reproducibility = "unknown"
)

type Provenance struct {
	CapturedAt        time.Time       `toml:"captured_at"`
	GitCommit         string          `toml:"git_commit"`
	GitDirty          bool            `toml:"git_dirty"`
	DirtyDigest       string          `toml:"dirty_digest,omitempty"`
	ConfigDigest      string          `toml:"config_digest,omitempty"`
	DataDigest        string          `toml:"data_digest,omitempty"`
	EnvironmentDigest string          `toml:"environment_digest,omitempty"`
	Reproducibility   Reproducibility `toml:"reproducibility"`
}

type Terminal struct {
	Source     string     `toml:"source"`
	ObservedAt time.Time  `toml:"observed_at"`
	StartedAt  *time.Time `toml:"started_at,omitempty"`
	EndedAt    time.Time  `toml:"ended_at"`
	ExitCode   *int       `toml:"exit_code,omitempty"`
	Signal     string     `toml:"signal,omitempty"`
}

type Attempt struct {
	Common
	Run           ID            `toml:"run"`
	State         AttemptState  `toml:"state"`
	StateReason   string        `toml:"state_reason,omitempty"`
	Runner        string        `toml:"runner"`
	Scheduler     string        `toml:"scheduler"`
	CWD           string        `toml:"cwd"`
	Argv          []string      `toml:"argv"`
	ExternalRefs  []ExternalRef `toml:"external_refs,omitempty"`
	Provenance    *Provenance   `toml:"provenance,omitempty"`
	Terminal      *Terminal     `toml:"terminal,omitempty"`
	Pool          ID            `toml:"pool,omitempty"`
	Queue         ID            `toml:"queue,omitempty"`
	QueueRevision uint64        `toml:"queue_revision,omitempty"`
	Lane          ResearchLane  `toml:"lane,omitempty"`
	DispatchID    string        `toml:"dispatch_id,omitempty"`
	BaseCommit    string        `toml:"base_commit,omitempty"`
	HeadCommit    string        `toml:"head_commit,omitempty"`
	ChangeSet     []string      `toml:"change_set,omitempty"`
	Extensions    Extensions    `toml:"extensions,omitempty"`
}

func (a *Attempt) GetSchema() Schema         { return a.Schema }
func (*Attempt) GetKind() Kind               { return KindAttempt }
func (a *Attempt) GetID() (ID, bool)         { return a.ID, !a.ID.IsZero() }
func (a *Attempt) GetCommon() *Common        { return &a.Common }
func (a *Attempt) GetExtensions() Extensions { return a.Extensions }

type FindingEvidenceKind string

const (
	FindingEvidenceRun        FindingEvidenceKind = "run"
	FindingEvidenceExperiment FindingEvidenceKind = "experiment"
)

type FindingEvidence struct {
	Kind   FindingEvidenceKind `toml:"kind"`
	Ref    ID                  `toml:"ref"`
	Detail string              `toml:"detail,omitempty"`
}

type Finding struct {
	Common
	Statement  string            `toml:"statement"`
	Scope      string            `toml:"scope"`
	Weakens    []ID              `toml:"weakens,omitempty"`
	Overturns  []ID              `toml:"overturns,omitempty"`
	Evidence   []FindingEvidence `toml:"evidence"`
	Extensions Extensions        `toml:"extensions,omitempty"`
}

func (f *Finding) GetSchema() Schema         { return f.Schema }
func (*Finding) GetKind() Kind               { return KindFinding }
func (f *Finding) GetID() (ID, bool)         { return f.ID, !f.ID.IsZero() }
func (f *Finding) GetCommon() *Common        { return &f.Common }
func (f *Finding) GetExtensions() Extensions { return f.Extensions }

type Decision struct {
	Common
	Statement   string     `toml:"statement"`
	BasedOn     []ID       `toml:"based_on"`
	Action      string     `toml:"action"`
	EffectiveAt time.Time  `toml:"effective_at"`
	Supersedes  []ID       `toml:"supersedes,omitempty"`
	Extensions  Extensions `toml:"extensions,omitempty"`
}

func (d *Decision) GetSchema() Schema         { return d.Schema }
func (*Decision) GetKind() Kind               { return KindDecision }
func (d *Decision) GetID() (ID, bool)         { return d.ID, !d.ID.IsZero() }
func (d *Decision) GetCommon() *Common        { return &d.Common }
func (d *Decision) GetExtensions() Extensions { return d.Extensions }
