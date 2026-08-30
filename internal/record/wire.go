package record

import "github.com/daviddwlee84/exp-cli/internal/research"

// frontMatterValue selects an exact legacy encoder. The canonical in-memory
// structs carry additive v2 fields, but a v1 writer must not serialize even
// their zero values because the v1 decoder is intentionally closed.
func frontMatterValue(record research.Record) any {
	switch value := record.(type) {
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
		if value.Schema == research.SchemaAttempt {
			return &attemptV1{
				Common: value.Common, Run: value.Run, State: value.State,
				StateReason: value.StateReason, Runner: value.Runner,
				Scheduler: value.Scheduler, CWD: value.CWD, Argv: value.Argv,
				ExternalRefs: value.ExternalRefs, Provenance: value.Provenance,
				Terminal: value.Terminal, Extensions: value.Extensions,
			}
		}
	}
	return record
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
