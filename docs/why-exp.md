# Why exp

Machine-learning research often has plenty of experiment runners and tracking
dashboards, but no durable answer to a harder question: **what should run next,
why is it worth the cost, and what evidence may change a decision?**

## The original pain points

| Pain point | What goes wrong without a control plane |
|---|---|
| Ideas arrive faster than compute | The loudest or newest request wins instead of the highest-value work. |
| A dashboard becomes the research memory | Mutable telemetry is mistaken for a durable conclusion. |
| Process success is treated as scientific success | A green job is promoted without checking comparability or a registered decision rule. |
| Negative results disappear | Teams repeat dead ends because failed hypotheses and their scope are not preserved. |
| Agents can generate more work than people can review | Automation increases queue pressure and hides why a task was admitted. |
| Code, evidence, and decisions drift apart | A metric cannot be tied back to an exact ChangeSet, Attempt, and evaluation protocol. |
| Production gates blur into experiment automation | Permission to dispatch research is accidentally treated as permission to ship it. |

## The solution shape

`exp` introduces a small, explicit layer between ideas and provider-owned
execution:

1. Capture an Idea without pretending it is ready to run.
2. Qualify it into a Plan with expected payoff, uncertainty, resource cost,
   assumptions, and dependencies.
3. Rank an exact Plan revision inside a ResourcePool and exploit/explore lane.
4. Register the Experiment design before evidence is observed.
5. Dispatch an exact executable, argument vector, Git base/head, and ChangeSet.
6. Keep Pueue and MLflow authoritative for their own live state while linking
   sanitized identities into research records.
7. Close work with included and excluded evidence, Findings, and explicit
   follow-up Ideas.
8. Require a separate sealed holdout and named human for Promotion.

## Why ordinary files

Canonical meaning lives in reviewable Markdown and TOML under `experiments/`.
That makes the research record diffable, branchable, searchable, and available
without a service. SQLite is deliberately limited to recoverable operational
state such as leases, jobs, outbox entries, and fairness counters.

## What exp is not

`exp` is not a training framework, hyperparameter optimizer, artifact store,
cluster scheduler, or experiment-tracking server. It coordinates research
meaning across those systems without absorbing their authority. See
[Tools](tools/index.md) for the current integration boundary.
