package research

import "time"

// ResearchLane separates exploitation of known-good directions from
// exploration whose primary value may be information gain.
type ResearchLane string

const (
	LaneExploit ResearchLane = "exploit"
	LaneExplore ResearchLane = "explore"
)

type RiskClass string

const (
	RiskLow    RiskClass = "low"
	RiskMedium RiskClass = "medium"
	RiskHigh   RiskClass = "high"
)

type HorizonClass string

const (
	HorizonShort  HorizonClass = "short"
	HorizonMedium HorizonClass = "medium"
	HorizonLong   HorizonClass = "long"
)

type OriginClass string

const (
	OriginHuman    OriginClass = "human"
	OriginAgent    OriginClass = "agent"
	OriginHybrid   OriginClass = "hybrid"
	OriginImported OriginClass = "imported"
)

// Classification contains the controlled, cross-project dimensions used by
// queue policy. Free-form discovery labels continue to live in Common.Tags.
type Classification struct {
	Domain    string       `toml:"domain"`
	Work      string       `toml:"work"`
	Method    string       `toml:"method"`
	Component string       `toml:"component"`
	Lane      ResearchLane `toml:"lane"`
	Risk      RiskClass    `toml:"risk"`
	Horizon   HorizonClass `toml:"horizon"`
	Origin    OriginClass  `toml:"origin"`
}

type ClassificationTaxonomy struct {
	Domains    []string `toml:"domains"`
	Work       []string `toml:"work"`
	Methods    []string `toml:"methods"`
	Components []string `toml:"components"`
}

type AutonomyMode string

const (
	AutonomyManual   AutonomyMode = "manual"
	AutonomyShadow   AutonomyMode = "shadow"
	AutonomyAssisted AutonomyMode = "assisted"
	AutonomyLimited  AutonomyMode = "limited"
)

type QueueTiePolicy string

const (
	QueueTieKeepIncumbent QueueTiePolicy = "keep_incumbent"
	QueueTieHumanReview   QueueTiePolicy = "human_review"
)

type ClusterSaturationPolicy struct {
	BudgetHours        float64 `toml:"budget_hours"`
	PlateauWindow      uint64  `toml:"plateau_window"`
	MinimumImprovement float64 `toml:"minimum_improvement"`
	MinimumProbability float64 `toml:"minimum_probability"`
}

type ClusterState string

const (
	ClusterOpen      ClusterState = "open"
	ClusterSaturated ClusterState = "saturated"
)

type ClusterPolicy struct {
	Name               string       `toml:"name"`
	State              ClusterState `toml:"state"`
	BudgetHours        float64      `toml:"budget_hours"`
	PlateauWindow      uint64       `toml:"plateau_window"`
	MinimumImprovement float64      `toml:"minimum_improvement"`
	MinimumProbability float64      `toml:"minimum_probability"`
	ReopenCondition    string       `toml:"reopen_condition,omitempty"`
}

// Policy is the singleton, ID-less policy record at POLICY.md.
type Policy struct {
	Schema                 Schema                  `toml:"schema"`
	CreatedAt              time.Time               `toml:"created_at"`
	UpdatedAt              time.Time               `toml:"updated_at"`
	Autonomy               AutonomyMode            `toml:"autonomy"`
	ExploitShare           float64                 `toml:"exploit_share"`
	ExploreShare           float64                 `toml:"explore_share"`
	ScoreFormula           string                  `toml:"score_formula"`
	TiePolicy              QueueTiePolicy          `toml:"tie_policy"`
	PromotionRequiresHuman bool                    `toml:"promotion_requires_human"`
	Taxonomy               ClassificationTaxonomy  `toml:"taxonomy"`
	ClusterSaturation      ClusterSaturationPolicy `toml:"cluster_saturation"`
	Clusters               []ClusterPolicy         `toml:"clusters,omitempty"`
	Extensions             Extensions              `toml:"extensions,omitempty"`
}

func (p *Policy) GetSchema() Schema         { return p.Schema }
func (*Policy) GetKind() Kind               { return KindPolicy }
func (*Policy) GetID() (ID, bool)           { return ID{}, false }
func (*Policy) GetCommon() *Common          { return nil }
func (p *Policy) GetExtensions() Extensions { return p.Extensions }

type IdeaState string

const (
	IdeaProposed   IdeaState = "proposed"
	IdeaDeveloping IdeaState = "developing"
	IdeaQualified  IdeaState = "qualified"
	IdeaQueued     IdeaState = "queued"
	IdeaDismissed  IdeaState = "dismissed"
	IdeaMerged     IdeaState = "merged"
)

type Idea struct {
	Common
	State          IdeaState      `toml:"state"`
	Summary        string         `toml:"summary"`
	ProposedBy     string         `toml:"proposed_by"`
	PrimaryCluster string         `toml:"primary_cluster"`
	Classification Classification `toml:"classification"`
	Parents        []ID           `toml:"parents,omitempty"`
	ResultingPlan  ID             `toml:"resulting_plan,omitempty"`
	MergedInto     ID             `toml:"merged_into,omitempty"`
	Extensions     Extensions     `toml:"extensions,omitempty"`
}

func (i *Idea) GetSchema() Schema         { return i.Schema }
func (*Idea) GetKind() Kind               { return KindIdea }
func (i *Idea) GetID() (ID, bool)         { return i.ID, !i.ID.IsZero() }
func (i *Idea) GetCommon() *Common        { return &i.Common }
func (i *Idea) GetExtensions() Extensions { return i.Extensions }

type ResourcePool struct {
	Common
	Enabled     bool       `toml:"enabled"`
	Capacity    uint64     `toml:"capacity"`
	Unit        string     `toml:"unit"`
	Bottleneck  string     `toml:"bottleneck"`
	CostPerHour *float64   `toml:"cost_per_hour,omitempty"`
	Extensions  Extensions `toml:"extensions,omitempty"`
}

func (p *ResourcePool) GetSchema() Schema         { return p.Schema }
func (*ResourcePool) GetKind() Kind               { return KindResourcePool }
func (p *ResourcePool) GetID() (ID, bool)         { return p.ID, !p.ID.IsZero() }
func (p *ResourcePool) GetCommon() *Common        { return &p.Common }
func (p *ResourcePool) GetExtensions() Extensions { return p.Extensions }

type QueueEntry struct {
	Plan         ID        `toml:"plan"`
	PlanRevision string    `toml:"plan_revision"`
	Score        float64   `toml:"score"`
	InsertedAt   time.Time `toml:"inserted_at"`
	Pinned       bool      `toml:"pinned"`
}

type QueuePartition struct {
	Pool    ID           `toml:"pool"`
	Lane    ResearchLane `toml:"lane"`
	Entries []QueueEntry `toml:"entries"`
}

// Queue is the canonical authority for pool x lane ordering. Entry order is
// semantic and is therefore never normalized as a set.
type Queue struct {
	Common
	Revision   uint64           `toml:"revision"`
	Paused     bool             `toml:"paused"`
	Partitions []QueuePartition `toml:"partitions"`
	Extensions Extensions       `toml:"extensions,omitempty"`
}

func (q *Queue) GetSchema() Schema         { return q.Schema }
func (*Queue) GetKind() Kind               { return KindQueue }
func (q *Queue) GetID() (ID, bool)         { return q.ID, !q.ID.IsZero() }
func (q *Queue) GetCommon() *Common        { return &q.Common }
func (q *Queue) GetExtensions() Extensions { return q.Extensions }

type QueueScore struct {
	ExpectedUtility float64 `toml:"expected_utility"`
	InformationGain float64 `toml:"information_gain"`
	UnblockValue    float64 `toml:"unblock_value"`
	RiskPenalty     float64 `toml:"risk_penalty"`
	PoolHours       float64 `toml:"pool_hours"`
	Total           float64 `toml:"total"`
}

type QueueAdvice struct {
	Common
	Queue            ID           `toml:"queue"`
	QueueRevision    uint64       `toml:"queue_revision"`
	CandidatePlan    ID           `toml:"candidate_plan"`
	Pool             ID           `toml:"pool"`
	Lane             ResearchLane `toml:"lane"`
	ProposedPosition uint64       `toml:"proposed_position"`
	ListwiseOrder    []ID         `toml:"listwise_order"`
	Score            QueueScore   `toml:"score"`
	Model            string       `toml:"model"`
	PromptDigest     string       `toml:"prompt_digest"`
	Rationale        string       `toml:"rationale"`
	Extensions       Extensions   `toml:"extensions,omitempty"`
}

func (a *QueueAdvice) GetSchema() Schema         { return a.Schema }
func (*QueueAdvice) GetKind() Kind               { return KindQueueAdvice }
func (a *QueueAdvice) GetID() (ID, bool)         { return a.ID, !a.ID.IsZero() }
func (a *QueueAdvice) GetCommon() *Common        { return &a.Common }
func (a *QueueAdvice) GetExtensions() Extensions { return a.Extensions }

type BattleChoice string

const (
	BattleChooseCandidate BattleChoice = "candidate"
	BattleChooseIncumbent BattleChoice = "incumbent"
	BattleChooseTie       BattleChoice = "tie"
)

type BattleOutcome string

const (
	BattleCandidateWins BattleOutcome = "candidate_wins"
	BattleIncumbentWins BattleOutcome = "incumbent_wins"
	BattleTie           BattleOutcome = "tie"
	BattleHumanReview   BattleOutcome = "human_review"
)

type Battle struct {
	Common
	Queue         ID            `toml:"queue"`
	QueueRevision uint64        `toml:"queue_revision"`
	Advice        ID            `toml:"advice,omitempty"`
	CandidatePlan ID            `toml:"candidate_plan"`
	IncumbentPlan ID            `toml:"incumbent_plan"`
	Pool          ID            `toml:"pool"`
	Lane          ResearchLane  `toml:"lane"`
	OrderAB       BattleChoice  `toml:"order_ab"`
	OrderBA       BattleChoice  `toml:"order_ba"`
	Outcome       BattleOutcome `toml:"outcome"`
	Confidence    float64       `toml:"confidence"`
	Rationale     string        `toml:"rationale"`
	Extensions    Extensions    `toml:"extensions,omitempty"`
}

func (b *Battle) GetSchema() Schema         { return b.Schema }
func (*Battle) GetKind() Kind               { return KindBattle }
func (b *Battle) GetID() (ID, bool)         { return b.ID, !b.ID.IsZero() }
func (b *Battle) GetCommon() *Common        { return &b.Common }
func (b *Battle) GetExtensions() Extensions { return b.Extensions }

type MetricDirection string

const (
	MetricMaximize MetricDirection = "maximize"
	MetricMinimize MetricDirection = "minimize"
)

type MetricSpec struct {
	Name      string          `toml:"name"`
	Unit      string          `toml:"unit"`
	Direction MetricDirection `toml:"direction"`
	Threshold *float64        `toml:"threshold,omitempty"`
}

type EvaluationPurpose string

const (
	EvaluationScientific EvaluationPurpose = "scientific"
	EvaluationPromotion  EvaluationPurpose = "promotion"
)

type EvaluationSpec struct {
	Common
	Purpose     EvaluationPurpose `toml:"purpose"`
	Dataset     string            `toml:"dataset"`
	Protocol    string            `toml:"protocol"`
	Metrics     []MetricSpec      `toml:"metrics"`
	BudgetPool  ID                `toml:"budget_pool"`
	BudgetHours float64           `toml:"budget_hours"`
	SealedAt    *time.Time        `toml:"sealed_at,omitempty"`
	Extensions  Extensions        `toml:"extensions,omitempty"`
}

func (s *EvaluationSpec) GetSchema() Schema         { return s.Schema }
func (*EvaluationSpec) GetKind() Kind               { return KindEvaluationSpec }
func (s *EvaluationSpec) GetID() (ID, bool)         { return s.ID, !s.ID.IsZero() }
func (s *EvaluationSpec) GetCommon() *Common        { return &s.Common }
func (s *EvaluationSpec) GetExtensions() Extensions { return s.Extensions }

type EvaluationOutcome string

const (
	EvaluationPassed  EvaluationOutcome = "passed"
	EvaluationFailed  EvaluationOutcome = "failed"
	EvaluationInvalid EvaluationOutcome = "invalid"
)

type MetricValue struct {
	Name  string  `toml:"name"`
	Value float64 `toml:"value"`
	Unit  string  `toml:"unit"`
}

type Evaluation struct {
	Common
	Spec         ID                `toml:"spec"`
	Subject      ID                `toml:"subject"`
	Outcome      EvaluationOutcome `toml:"outcome"`
	EvaluatedAt  time.Time         `toml:"evaluated_at"`
	Metrics      []MetricValue     `toml:"metrics"`
	ExternalRefs []ExternalRef     `toml:"external_refs,omitempty"`
	Summary      string            `toml:"summary"`
	Extensions   Extensions        `toml:"extensions,omitempty"`
}

func (e *Evaluation) GetSchema() Schema         { return e.Schema }
func (*Evaluation) GetKind() Kind               { return KindEvaluation }
func (e *Evaluation) GetID() (ID, bool)         { return e.ID, !e.ID.IsZero() }
func (e *Evaluation) GetCommon() *Common        { return &e.Common }
func (e *Evaluation) GetExtensions() Extensions { return e.Extensions }

// Candidate is a validated result that can fill a typed Release slot.
type Candidate struct {
	Common
	Experiment   ID            `toml:"experiment"`
	Evaluation   ID            `toml:"evaluation"`
	Parents      []ID          `toml:"parents,omitempty"`
	GitCommit    string        `toml:"git_commit"`
	ChangeSet    []string      `toml:"change_set"`
	ExternalRefs []ExternalRef `toml:"external_refs,omitempty"`
	Extensions   Extensions    `toml:"extensions,omitempty"`
}

func (c *Candidate) GetSchema() Schema         { return c.Schema }
func (*Candidate) GetKind() Kind               { return KindCandidate }
func (c *Candidate) GetID() (ID, bool)         { return c.ID, !c.ID.IsZero() }
func (c *Candidate) GetCommon() *Common        { return &c.Common }
func (c *Candidate) GetExtensions() Extensions { return c.Extensions }

type ReleaseState string

const (
	ReleaseDraft     ReleaseState = "draft"
	ReleaseValidated ReleaseState = "validated"
	ReleaseRetired   ReleaseState = "retired"
)

type ReleaseSlot struct {
	Name      string `toml:"name"`
	Candidate ID     `toml:"candidate"`
}

type Release struct {
	Common
	Target                string        `toml:"target"`
	Version               string        `toml:"version"`
	State                 ReleaseState  `toml:"state"`
	Slots                 []ReleaseSlot `toml:"slots"`
	CombinationExperiment ID            `toml:"combination_experiment,omitempty"`
	CombinationEvaluation ID            `toml:"combination_evaluation,omitempty"`
	Evaluation            ID            `toml:"evaluation,omitempty"`
	Extensions            Extensions    `toml:"extensions,omitempty"`
}

func (r *Release) GetSchema() Schema         { return r.Schema }
func (*Release) GetKind() Kind               { return KindRelease }
func (r *Release) GetID() (ID, bool)         { return r.ID, !r.ID.IsZero() }
func (r *Release) GetCommon() *Common        { return &r.Common }
func (r *Release) GetExtensions() Extensions { return r.Extensions }

type PromotionSpec struct {
	Common
	Target                string     `toml:"target"`
	EvaluationSpec        ID         `toml:"evaluation_spec"`
	SealedAt              time.Time  `toml:"sealed_at"`
	HoldoutBudgetHours    float64    `toml:"holdout_budget_hours"`
	HumanApprovalRequired bool       `toml:"human_approval_required"`
	Extensions            Extensions `toml:"extensions,omitempty"`
}

func (s *PromotionSpec) GetSchema() Schema         { return s.Schema }
func (*PromotionSpec) GetKind() Kind               { return KindPromotionSpec }
func (s *PromotionSpec) GetID() (ID, bool)         { return s.ID, !s.ID.IsZero() }
func (s *PromotionSpec) GetCommon() *Common        { return &s.Common }
func (s *PromotionSpec) GetExtensions() Extensions { return s.Extensions }

type PromotionOutcome string

const (
	PromotionAccepted   PromotionOutcome = "accepted"
	PromotionRejected   PromotionOutcome = "rejected"
	PromotionRolledBack PromotionOutcome = "rolled_back"
)

type Promotion struct {
	Common
	Target     string           `toml:"target"`
	Spec       ID               `toml:"spec"`
	Challenger ID               `toml:"challenger"`
	Incumbent  ID               `toml:"incumbent,omitempty"`
	Evaluation ID               `toml:"evaluation"`
	Outcome    PromotionOutcome `toml:"outcome"`
	AppliedAt  time.Time        `toml:"applied_at"`
	Previous   ID               `toml:"previous,omitempty"`
	ApprovedBy string           `toml:"approved_by"`
	Extensions Extensions       `toml:"extensions,omitempty"`
}

func (p *Promotion) GetSchema() Schema         { return p.Schema }
func (*Promotion) GetKind() Kind               { return KindPromotion }
func (p *Promotion) GetID() (ID, bool)         { return p.ID, !p.ID.IsZero() }
func (p *Promotion) GetCommon() *Common        { return &p.Common }
func (p *Promotion) GetExtensions() Extensions { return p.Extensions }
