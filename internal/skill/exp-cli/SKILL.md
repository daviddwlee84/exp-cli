---
name: exp-cli
description: >-
  Operate exp's Git-native research queue and evidence lifecycle; use when
  developing human or agent Ideas, prioritizing scarce compute, dispatching
  experiments, recording Findings, composing Releases, or reviewing Promotion.
metadata:
  schema-version: "exp.skill/v1"
  skill-version: "1"
---

# exp-cli research control plane

Use `exp` to preserve the path from an Idea to evidence and a production
decision. Canonical research meaning belongs to Git-backed records under
`experiments/`; Pueue, MLflow, Git, and Plan-scoped search systems retain their
own operational authority.

Read [the generated command reference](references/commands.md) before invoking
an unfamiliar command. Full flags in `exp <command> --help` are authoritative.

## Preserve the safety gates

- Initialize Policy explicitly. It defaults to `manual` with 80/20
  exploit/explore allocation. `manual` and `shadow` never dispatch.
- Do not change to `assisted` or `limited` without the user's authorization and
  the command's `--confirm-auto-experiment` acknowledgement.
- No autonomy mode authorizes production Promotion. Promotion requires a sealed
  holdout Evaluation, a named human approver, and explicit confirmation.
- Treat agent output as advisory. It must pass the command's strict JSON schema
  and normal canonical validation before it can affect records.
- Keep operational success separate from scientific judgment. A succeeded task
  is not a supported hypothesis; a failed task is not a refuted hypothesis.

## Build queue-ready work

1. Capture a human or agent Idea with origin, cluster, controlled
   classification, and parent Ideas when it follows an existing branch.
2. Qualify it only after the question is falsifiable and the Plan has measurable
   payoff, probability/impact, information and unblock value, risk, constrained
   ResourcePool-hours, and revision-pinned Finding dependencies.
3. A human may qualify directly. `idea develop` may ask one fresh CLI agent for
   the missing Plan and `--apply` only after the proposal validates.
4. Insert the Plan into one ResourcePool/lane partition. Prefer the transparent
   score as the baseline; use `queue insert --agent` when listwise advice and
   adjacent battles add useful judgment.
5. If order-swapped battles disagree, abstain, lack confidence, or require tie
   review, leave the incumbent Queue unchanged and surface human review. Do not
   manually infer the intended order from one of the two answers.
6. When new belief-changing evidence stales a queued Plan, review it and use
   `plan refresh` with a complete new utility estimate. The transaction repins
   dependencies, removes the Plan from the Queue, and returns its Idea to
   `qualified`; run `queue insert` again so the revised Plan competes afresh.

Read [methodology.md](references/methodology.md) before designing, closing, or
combining experiments.

## Dispatch without moving authority

`.exp/runtime.json` binds canonical Pool and Plan IDs to a Pueue group and an
exact workload/Git contract. It may select the main checkout or the unique
registered worktree at `head_commit`. Pueue persists task environments, so the
runtime accepts only allowed non-secret names and requires empty `secret_env`;
use a workload-side credential broker. `daemon frontier` is local-only; `daemon tick` and `daemon run`
contact Pueue and dispatch only in explicitly enabled policy modes.

The daemon's SQLite state is private coordination, not research truth. Pueue
owns live tasks. The workload owns MLflow run creation and logging;
`provider mlflow verify` reads selected fields only. Never create a Finding,
Evaluation, verdict, Candidate, Release, or Promotion merely from scheduler,
worker, or tracker status.

For code-changing experiments, use the experiment workspace commands with a
full base commit and narrow allowlist. The agent may create the exact experiment
commit. It may not merge it, remove the worktree, or edit `experiments/` as part
of the code ChangeSet; the human owns integration.

## Close, combine, and promote

- Close an Experiment with explicit included/excluded Run dispositions. Record
  `invalid` when evidence cannot answer the registered question; use
  `inconclusive` when valid evidence remains insufficient.
- Publish Findings with scoped evidence and explicit `weakens`/`overturns`
  edges. New belief-changing evidence can stale dependent Plans and Queues.
- Create a Candidate only from a supported Experiment plus a passing scientific
  Evaluation and a successful direct Attempt for an included Run whose pinned
  full Git commit and exact ChangeSet match the Candidate.
- Compose a Release with typed project-specific slots. More than one Candidate
  requires a separately evaluated combination Experiment; never add independent
  improvements arithmetically.
- Treat the append-only Promotion chain as authority. Champion manifests are
  derived downstream views and are never edited or read back as canonical.
- Create the PromotionSpec before its fresh holdout Evaluation. A holdout that
  predates the seal or was consumed by another Promotion is not eligible.

See [records-and-project-knowledge.md](references/records-and-project-knowledge.md)
for record ownership and routing to TODO/backlog/pitfalls/invariants.

## Transactions, migration, and search

Use domain commands for scientific mutations. The public `record transaction`
surface accepts only low-risk Idea and ResourcePool edits; it requires exact
revisions and recovers through prepared hash-checked journals. On interruption or a recovery diagnostic, use
`record recover` and do not hand-edit a split canonical state.

Harness-v0 migration is opt-in: create and review a fingerprinted plan, resolve
every `needs_review` item, then apply that exact plan. Never execute legacy
scripts or silently convert ambiguous inbox prose.

Optuna-like search is scoped inside one Plan revision. It may suggest parameters
or prune trials through the Study adapter contract, but it never owns global
Queue order, ResourcePools, Findings, Releases, or Promotions. A concrete
Optuna adapter is not included in this build.

## Machine use and fallback

Agents should prefer `--json`, parse the entire `exp.cli/v1` envelope, and carry
complete typed IDs plus revisions. Never scrape human tables or reconstruct
facts from generated projections. See [usage-and-fallback.md](references/usage-and-fallback.md)
when the binary is unavailable, and [external-tools.md](references/external-tools.md)
before crossing into Pueue, MLflow, Git, or another provider.
