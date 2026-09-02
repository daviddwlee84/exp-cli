# Records and Authority

Use one owner for each fact. `exp` links scientific meaning to code and
provider-owned execution, but it does not copy every system into one database.
Reverse relationships and summaries are derived from their owners rather than
stored as competing facts.

## Authority map

| Store or system | What it owns | What it cannot establish | How `exp` uses it |
|---|---|---|---|
| Canonical records under `<git-root>/experiments/` | Research policy, Ideas, priced Plans, ResourcePools, ordered Queues, Experiment design and closure, intended Runs, redacted Attempts, Evaluation protocols/results, Findings, Candidates, Releases, Promotions, and Decisions. | Live provider freshness, raw telemetry, code integration state, or hidden daemon coordination. | Validate exact schemas and revisions; mutate through domain commands or prepared transactions; derive reverse links and projections. |
| Upstream providers | Provider-native live facts: Pueue task/group state; MLflow run telemetry, parameters, metrics, traces, artifacts, and registry state; future DVC, Slurm, Study, or notebook state. | A scientific verdict, canonical relationship, production Promotion, or current Git integration. | Read only implemented bounded capabilities, sanitize observations, and retain an ExternalRef or selected fact when a canonical record explicitly needs it. |
| Git | Commits, trees, branches, linked worktrees, ChangeSets, and integration history. | Whether a process result supports a hypothesis or passes an Evaluation. | Pin full base/head object IDs and exact changed paths. An agent may create an experiment commit; a human owns merge and cleanup. |
| Private SQLite at `<git-common-dir>/exp/runtime/v1/control.sqlite` | Daemon leases, fencing tokens, jobs, submission outbox, fairness counters, pause state, provider observations, and bounded event history. | Hypotheses, conclusions, evidence disposition, Findings, Evaluations, Releases, or Promotions. | Coordinate and recover local execution across linked worktrees. Never treat a database row as scientific authority. |
| Generated views | Deterministic `README.md`, `ROADMAP.md`, `LEDGER.md`, `DECISIONS.md`, Champion manifests, and other projections derived from canonical records. | New facts or relationship ownership. | Regenerate with `exp render`; check drift with `exp render --check`. Never read a projection back to reconstruct canonical state. |
| Public Notes | Dated exploratory questions, bounded observations, comparisons, and links. | Canonical truth, live provider state, or an implementation commitment. | Use as a non-canonical staging and learning layer; route durable outcomes to the correct owner. |
| TODO | A concrete action that someone can complete. | Evidence that the action is scientifically justified or a priced Queue entry. | Link to the relevant Idea, Plan, Finding, Decision, issue, or code location without duplicating its mutable facts. |
| Backlog | A rough question or investigation not yet worth canonical qualification. | Durable research lineage, priced resources, priority, or Queue order. | Promote to an Idea when origin, classification, cluster, and lineage matter; qualify to a Plan only after payoff and resource cost are explicit. |
| Pitfall | A recurring operational symptom, cause, diagnostic, and remedy worth finding again. | The complete Attempt history or a general scientific conclusion. | Link relevant Attempts, provider issues, or Findings; keep the reusable troubleshooting lesson here. |
| Invariant | A durable constraint future designs and implementations must obey, such as a privacy or leakage prohibition. | A result inferred from one narrow run or an automatically enforced validator. | Reference it from designs, tests, and Decisions; implement enforcement separately where required. |

The repository's project-knowledge conventions remain authoritative for TODO,
Backlog, Pitfall, and Invariant storage. `exp` must not silently create a
competing store or convert those items into canonical records without an
explicit domain operation.

## Canonical relationship ownership

A relationship is stored once, on its declared owner. Examples:

| Relationship | Canonical owner |
|---|---|
| Idea qualifies to Plan | Idea |
| Plan results in Experiment | Plan |
| Queue orders a Plan in a ResourcePool/lane | Queue |
| Run belongs to Experiment | Run |
| Attempt executes Run | Attempt |
| Experiment includes or excludes Run as conclusion evidence | Experiment conclusion |
| Finding weakens or overturns Finding | New Finding |
| Candidate packages Experiment, Evaluation, Git commit, and ChangeSet | Candidate |
| Release fills a named slot from Candidate | Release |
| Promotion continues the target's decision chain | New Promotion |
| Decision supersedes Decision | New Decision |

Inventory scans compute reverse relations. Do not add an inverse field merely
to make navigation easier; add or regenerate a projection instead. The full
schema-level list is in
[Record Format and Schema Versions](../design/record-format.md#relationship-ownership-summary).

## Crossing an authority boundary

An ExternalRef is a bridge, not a transfer of authority. It may retain a
sanitized provider, context, native kind, native ID, optional query-free URI,
and observation time. It does not copy credentials, raw environments,
unbounded provider output, or artifact bytes, and it does not claim that the
provider state remains fresh.

Provider or worker observations can advance the operational state of a
canonical Attempt only through explicit validation and a canonical
transaction. Even then:

- process or scheduler success is an operational fact, not a scientific
  verdict;
- an MLflow run being `FINISHED` and matching requested tags is verification,
  not an Evaluation outcome;
- a Git commit is exact code identity, not evidence by itself;
- an agent recommendation is advisory unless a domain command validates and
  records the resulting state;
- only a named human can append a production Promotion.

## Routing examples

| Statement | Correct owner |
|---|---|
| “Try a larger context window next week.” | TODO or Backlog until it is qualified and priced. |
| “This comparable evidence supports the scoped claim.” | Finding linked to registered evidence. |
| “Pueue reports task 42 as running.” | Pueue; optionally a bounded observation reconciled to an Attempt. |
| “Commit `abc…` contains the reviewed implementation.” | Git; a Candidate additionally requires supported evidence and a passing Evaluation. |
| “Allocator fragmentation repeatedly causes this launch shape to fail; use this setting.” | Pitfall, linked to relevant Attempts or Findings. |
| “Evaluation data must never enter training-time feature selection.” | Invariant, referenced by designs and tests. |
| “Stop this direction and reallocate the budget.” | Decision based on Findings; any concrete follow-up can also be a TODO. |

When one statement serves two audiences, keep the fact with its authority and
link to it from the other store. Do not synchronize two mutable copies.

See [Architecture](../design/architecture.md),
[Transactions](../design/transactions.md), and
[Research Notes](../notes/index.md) for the surrounding workflows.
