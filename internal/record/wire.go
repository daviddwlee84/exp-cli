package record

import (
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

// frontMatterValue selects an exact legacy encoder. The canonical in-memory
// structs carry additive v2 fields, but a v1 writer must not serialize even
// their zero values because the v1 decoder is intentionally closed.
func frontMatterValue(record research.Record) any {
	switch value := record.(type) {
	case *research.Idea:
		if value.Schema == research.SchemaIdea {
			return &ideaV1{
				Common: value.Common, State: value.State, Summary: value.Summary,
				ProposedBy: value.ProposedBy, PrimaryCluster: value.PrimaryCluster,
				Classification: value.Classification, Parents: value.Parents,
				ResultingPlan: value.ResultingPlan, MergedInto: value.MergedInto,
				Extensions: value.Extensions,
			}
		}
	case *research.Plan:
		if value.Schema == research.SchemaPlan {
			return &planV1{
				Common: value.Common, Priority: value.Priority, Effort: value.Effort,
				State: value.State, Assumptions: value.Assumptions,
				ResultingExperiment: value.ResultingExperiment,
				ExpectedPayoff:      value.ExpectedPayoff, Extensions: value.Extensions,
			}
		}
	case *research.Experiment:
		if value.Schema == research.SchemaExperiment {
			return &experimentV1{
				Common: value.Common, Lifecycle: value.Lifecycle, Closure: value.Closure,
				Verdict: value.Verdict, Design: value.Design, Amendments: value.Amendments,
				ClosureDetail: value.ClosureDetail, Conclusion: value.Conclusion,
				Extensions: value.Extensions,
			}
		}
	case *research.Attempt:
		switch value.Schema {
		case research.SchemaAttempt:
			return &attemptV1{
				Common: value.Common, Run: value.Run, State: value.State,
				StateReason: value.StateReason, Runner: value.Runner,
				Scheduler: value.Scheduler, CWD: value.CWD, Argv: value.Argv,
				ExternalRefs: value.ExternalRefs, Provenance: value.Provenance,
				Terminal: value.Terminal, Extensions: value.Extensions,
			}
		case research.SchemaAttemptV2:
			return &attemptV2{
				Common: value.Common, Run: value.Run, State: value.State,
				StateReason: value.StateReason, Runner: value.Runner,
				Scheduler: value.Scheduler, CWD: value.CWD, Argv: value.Argv,
				ExternalRefs: value.ExternalRefs, Provenance: value.Provenance,
				Terminal: value.Terminal, Pool: value.Pool, Queue: value.Queue,
				QueueRevision: value.QueueRevision, Lane: value.Lane,
				DispatchID: value.DispatchID, BaseCommit: value.BaseCommit,
				HeadCommit: value.HeadCommit, ChangeSet: value.ChangeSet,
				Extensions: value.Extensions,
			}
		}
	case *research.Evaluation:
		switch value.Schema {
		case research.SchemaEvaluation:
			return &evaluationV1{
				Common: value.Common, Spec: value.Spec, Subject: value.Subject,
				Outcome: value.Outcome, EvaluatedAt: value.EvaluatedAt, Metrics: value.Metrics,
				ExternalRefs: value.ExternalRefs, Summary: value.Summary, Extensions: value.Extensions,
			}
		case research.SchemaEvaluationV2:
			return &evaluationV2{
				Common: value.Common, Spec: value.Spec, Subject: value.Subject, Attempt: value.Attempt,
				Outcome: value.Outcome, EvaluatedAt: value.EvaluatedAt, Metrics: value.Metrics,
				ExternalRefs: value.ExternalRefs, Summary: value.Summary, Extensions: value.Extensions,
			}
		}
	case *research.Candidate:
		switch value.Schema {
		case research.SchemaCandidate:
			return &candidateV1{
				Common: value.Common, Experiment: value.Experiment,
				Evaluation: value.Evaluation, Parents: value.Parents,
				GitCommit: value.GitCommit, ChangeSet: value.ChangeSet,
				ExternalRefs: value.ExternalRefs, Extensions: value.Extensions,
			}
		case research.SchemaCandidateV2:
			return &candidateV2{
				Common: value.Common, Experiment: value.Experiment,
				Evaluation: value.Evaluation, Parents: value.Parents,
				Attempt: value.Attempt, Sources: value.Sources,
				ExternalRefs: value.ExternalRefs, Extensions: value.Extensions,
			}
		}
	}
	return record
}

type ideaV1 struct {
	research.Common
	State          research.IdeaState      `toml:"state"`
	Summary        string                  `toml:"summary"`
	ProposedBy     string                  `toml:"proposed_by"`
	PrimaryCluster string                  `toml:"primary_cluster"`
	Classification research.Classification `toml:"classification"`
	Parents        []research.ID           `toml:"parents,omitempty"`
	ResultingPlan  research.ID             `toml:"resulting_plan,omitempty"`
	MergedInto     research.ID             `toml:"merged_into,omitempty"`
	Extensions     research.Extensions     `toml:"extensions,omitempty"`
}

type planV1 struct {
	research.Common
	Priority            research.Priority       `toml:"priority"`
	Effort              research.Effort         `toml:"effort"`
	State               research.PlanState      `toml:"state"`
	Assumptions         []research.ID           `toml:"assumptions,omitempty"`
	ResultingExperiment research.ID             `toml:"resulting_experiment,omitempty"`
	ExpectedPayoff      research.ExpectedPayoff `toml:"expected_payoff"`
	Extensions          research.Extensions     `toml:"extensions,omitempty"`
}

type experimentV1 struct {
	research.Common
	Lifecycle     research.ExperimentLifecycle `toml:"lifecycle"`
	Closure       research.ExperimentClosure   `toml:"closure,omitempty"`
	Verdict       research.Verdict             `toml:"verdict,omitempty"`
	Design        research.Design              `toml:"design"`
	Amendments    []research.Amendment         `toml:"amendments,omitempty"`
	ClosureDetail *research.ClosureDetail      `toml:"closure_detail,omitempty"`
	Conclusion    *research.Conclusion         `toml:"conclusion,omitempty"`
	Extensions    research.Extensions          `toml:"extensions,omitempty"`
}

type attemptV1 struct {
	research.Common
	Run          research.ID            `toml:"run"`
	State        research.AttemptState  `toml:"state"`
	StateReason  string                 `toml:"state_reason,omitempty"`
	Runner       string                 `toml:"runner"`
	Scheduler    string                 `toml:"scheduler"`
	CWD          string                 `toml:"cwd"`
	Argv         []string               `toml:"argv"`
	ExternalRefs []research.ExternalRef `toml:"external_refs,omitempty"`
	Provenance   *research.Provenance   `toml:"provenance,omitempty"`
	Terminal     *research.Terminal     `toml:"terminal,omitempty"`
	Extensions   research.Extensions    `toml:"extensions,omitempty"`
}

type attemptV2 struct {
	research.Common
	Run           research.ID            `toml:"run"`
	State         research.AttemptState  `toml:"state"`
	StateReason   string                 `toml:"state_reason,omitempty"`
	Runner        string                 `toml:"runner"`
	Scheduler     string                 `toml:"scheduler"`
	CWD           string                 `toml:"cwd"`
	Argv          []string               `toml:"argv"`
	ExternalRefs  []research.ExternalRef `toml:"external_refs,omitempty"`
	Provenance    *research.Provenance   `toml:"provenance,omitempty"`
	Terminal      *research.Terminal     `toml:"terminal,omitempty"`
	Pool          research.ID            `toml:"pool,omitempty"`
	Queue         research.ID            `toml:"queue,omitempty"`
	QueueRevision uint64                 `toml:"queue_revision,omitempty"`
	Lane          research.ResearchLane  `toml:"lane,omitempty"`
	DispatchID    string                 `toml:"dispatch_id,omitempty"`
	BaseCommit    string                 `toml:"base_commit,omitempty"`
	HeadCommit    string                 `toml:"head_commit,omitempty"`
	ChangeSet     []string               `toml:"change_set,omitempty"`
	Extensions    research.Extensions    `toml:"extensions,omitempty"`
}

type evaluationV1 struct {
	research.Common
	Spec         research.ID                `toml:"spec"`
	Subject      research.ID                `toml:"subject"`
	Outcome      research.EvaluationOutcome `toml:"outcome"`
	EvaluatedAt  time.Time                  `toml:"evaluated_at"`
	Metrics      []research.MetricValue     `toml:"metrics"`
	ExternalRefs []research.ExternalRef     `toml:"external_refs,omitempty"`
	Summary      string                     `toml:"summary"`
	Extensions   research.Extensions        `toml:"extensions,omitempty"`
}

type evaluationV2 struct {
	research.Common
	Spec         research.ID                `toml:"spec"`
	Subject      research.ID                `toml:"subject"`
	Attempt      research.ID                `toml:"attempt"`
	Outcome      research.EvaluationOutcome `toml:"outcome"`
	EvaluatedAt  time.Time                  `toml:"evaluated_at"`
	Metrics      []research.MetricValue     `toml:"metrics"`
	ExternalRefs []research.ExternalRef     `toml:"external_refs,omitempty"`
	Summary      string                     `toml:"summary"`
	Extensions   research.Extensions        `toml:"extensions,omitempty"`
}

type candidateV1 struct {
	research.Common
	Experiment   research.ID            `toml:"experiment"`
	Evaluation   research.ID            `toml:"evaluation"`
	Parents      []research.ID          `toml:"parents,omitempty"`
	GitCommit    string                 `toml:"git_commit"`
	ChangeSet    []string               `toml:"change_set"`
	ExternalRefs []research.ExternalRef `toml:"external_refs,omitempty"`
	Extensions   research.Extensions    `toml:"extensions,omitempty"`
}

type candidateV2 struct {
	research.Common
	Experiment   research.ID                `toml:"experiment"`
	Evaluation   research.ID                `toml:"evaluation"`
	Parents      []research.ID              `toml:"parents,omitempty"`
	Attempt      research.ID                `toml:"attempt"`
	Sources      []research.CandidateSource `toml:"sources"`
	ExternalRefs []research.ExternalRef     `toml:"external_refs,omitempty"`
	Extensions   research.Extensions        `toml:"extensions,omitempty"`
}
