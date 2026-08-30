# Research records and project knowledge

## Canonical exp concepts

Use one owner for each fact and derive reverse links rather than duplicating them.

| Concept | Store here when | Do not use it for |
|---|---|---|
| Policy | autonomy, taxonomy, queue allocation, tie, and human Promotion rules are project-wide | a one-off agent instruction or hidden daemon setting |
| Idea | a human/agent direction, branch, or merge needs durable qualification state | a priced queue entry or generic TODO |
| Plan | proposed research has priority, effort, measurable expected payoff, assumptions, and a state | a generic task or an unpriced idea |
| ResourcePool | a named bottleneck has finite concurrent capacity and cost | a provider-native Pueue task/group snapshot |
| Queue | a Plan's exact revision has semantic order within a Pool/lane | live scheduler priority or agent-only recommendation |
| QueueAdvice/Battle | listwise and order-swapped ranking judgment needs an immutable audit | canonical Queue order by itself |
| Experiment | a question has a registered hypothesis, baseline, comparability specification, success criteria, and decision rule | process status or ad hoc notebook notes |
| Run | an intended evidence unit or batch belongs to an Experiment | a process retry or every tracker-owned sweep trial |
| Attempt | one redacted execution/submission of a Run has operational state and provenance | a scientific verdict |
| EvaluationSpec/Evaluation | a comparable protocol and immutable measured outcome are required | raw telemetry or mutable provider dashboards |
| Finding | a durable, scoped belief is supported by explicit evidence | an unsupported hunch, action item, or raw metric dump |
| Candidate | a supported evaluated result is pinned to exact Git code/ChangeSet | any process that happened to succeed |
| Release | named downstream slots compose validated Candidates for one target | assuming independent gains are additive |
| Promotion | a sealed holdout and named human decision changes one target's incumbent | autonomous experiment dispatch |
| Decision | an interpretation selects an action based on Findings | the preregistered decision rule itself |
| External reference | provider-owned identity must be linked without copying its authority | credentials, raw environments, artifact bytes, or a claim of freshness |
| Resume context | a local derived summary joins records and dated observations | canonical state |

Use the domain command when one exists. Reverse relations, Queue frontiers,
Finding belief status, and Champions are derived from canonical owners; never
write the inverse edge into a second record or treat a generated view as input.

## Project-knowledge routing

The project's knowledge harness remains authoritative for its own stores:

- **TODO:** a concrete action someone can complete. An experiment may produce a TODO, but the action is not itself a Finding.
- **Backlog:** a rough question or investigation not yet worth a canonical
  Idea. Once origin, classification, cluster, and durable lineage matter,
  capture an Idea; qualify it to a Plan only after payoff and resource cost are
  explicit.
- **Pitfall:** a recurring operational symptom, cause, diagnostic, or remedy worth finding during future troubleshooting. Link relevant Attempts or Findings, but do not duplicate their canonical facts.
- **Invariant:** a durable constraint that future designs and implementations must obey, such as a leakage prohibition or privacy boundary. An invariant is not merely a result from one narrow dataset.

exp can discover or link these materials. It must not silently create a competing TODO/backlog/pitfall/invariant store or rewrite the existing one.

## Choosing between neighboring concepts

Ask what kind of claim is being made:

- “Try a larger context window next week” is a TODO or backlog item until priced; it is not a Finding.
- “Across the registered comparable Runs, a larger context window did not improve macro-F1” can be a scoped Finding.
- “CUDA allocator fragmentation repeatedly causes this launch shape to fail; use the recorded allocator setting” is a pitfall.
- “Evaluation data must never enter training-time feature selection” is an invariant.
- “Stop pursuing this configuration and allocate the budget to baseline hardening” is a Decision based on Findings.
- “The scheduler preempted Attempt A” is an operational fact. It says nothing by itself about the hypothesis.

When information serves two audiences, keep the fact in its authority and add a link from the other store rather than copying mutable text.
