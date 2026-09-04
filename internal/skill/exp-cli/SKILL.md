---
name: exp-cli
description: >-
  Set up and operate exp's Git-native research workspaces, Sources, bounded
  Tries, rigorous experiment queue, evidence lifecycle, and human Promotion.
metadata:
  schema-version: "exp.skill/v1"
  skill-version: "1"
---

# exp-cli research control plane

Use `exp` to preserve the path from a bounded question to comparable evidence
and a reviewed production decision. Canonical research meaning belongs to
Git-backed records under `experiments/`; native Git, Pueue, MLflow/DVC/object
storage, and Plan-scoped search systems retain their own authority.

Read [the generated command reference](references/commands.md) before invoking
an unfamiliar command. It is generated from approved live CLI metadata; full
flags in `exp <command> --help` are authoritative. Use `exp guide` for conceptual
workflow help.

## Resolve authority before choosing a flow

The recommended layout is a dedicated private experiment repository plus
ordinary canonical Source records for code/data repositories or governed
subdirectories. A private XDG association locates each clone; host paths never
belong in canonical records.

For a new terminal user:

```text
exp init
exp workspace status
exp source status
exp config show
exp config explain
```

Bare `exp init` in a TTY defaults to the reviewed dedicated-repository wizard.
For non-interactive setup use complete `--dedicated-repo`, `--source-repo`,
`--source-key`, and `--confirm` flags; add `--create` only for a reviewed missing
or empty target and `--confirm-local-only` only when accepting no sanitized
remote locator. Use the explicit `exp init --name ...` path for embedded
compatibility.

Use `source add` to publish/associate another repository or immutable `subdir`,
and `source register` to repair a moved/recloned host association. Use
`--workspace` and `--source` whenever resolution is not unique. Configuration
loads only after authority resolves; repository-selected execution-bearing
fields are inert until `config trust` approves the exact current digest and
capability. When a Source is selected, repository-layer receipts bind that
Source ID, so explain and trust with the same selectors and invocation root that
execution will use. Editing the file invalidates that approval.

## Choose the right flow

| User's decision | Route | Start | Valid outcome |
|---|---|---|---|
| Is this direction worth formalizing? | Quick exploratory Try | `exp try run` | Human finish/abandon; optional `try adopt` into Idea v2 |
| Should constrained compute test this claim? | Rigorous Experiment | `exp idea add` | Priced Plan → Queue → clean Attempt v3 → Evaluation v2/Candidate v2 |
| Should validated evidence reach a target? | Promotion | `exp candidate create` | Typed Release → sealed fresh holdout → named-human Promotion |

Do not turn a quick Try into a shortcut around formal evidence, and do not turn
process/provider success into a scientific verdict.

## Run a bounded Try first when appropriate

A direct Try runs argv in a managed native Git worktree. Arguments after `--`
are not a shell command. Clean Source state is the default; `--dirty=capture`
must be literal, bounded, and Try-only. Narrow `--allow` globs are relative to
the Source semantic root; an empty allowlist is read-only.

```text
exp --workspace WORKSPACE --source SOURCE --workspace-backend native_git \
  try run --title TITLE --goal GOAL -- git status --short
exp --workspace WORKSPACE try status TRY
exp --workspace WORKSPACE try finish TRY --summary TEXT --no-results --confirm
```

Use `try resume` only for work proven unstarted or to import a verified durable
marker; use explicit `try reconcile` for uncertain execution. Never guess and
rerun uncertain work. `try cleanup` removes only verified manager-owned
worktrees/seed bundles and retains canonical records, branches, and markers.

A concluded Try may be adopted with `try adopt` through human review. It cannot
back a Candidate. A useful dirty Try must be rerun cleanly through the formal
queue.

## Build queue-ready rigorous work

1. Capture a human/agent Idea with origin, cluster, controlled classification,
   and parent Ideas when it follows existing evidence.
2. Qualify only after the Plan states measurable payoff, probability/impact,
   information/unblock value, risk, constrained ResourcePool-hours, assumptions,
   and revision-pinned Finding dependencies.
3. A human may use `idea qualify`; `idea develop` may ask one fresh CLI agent for
   the complete Plan and `--apply` only after schema/revision validation.
4. Initialize Policy and capacity explicitly. Policy defaults to `manual`; create
   Pool/Queue and insert the exact Plan. Prefer transparent numeric insertion;
   use `queue insert --agent` only when listwise advice/order-swapped battles add
   judgment.
5. Disagreement, abstention, low confidence, or tie review leaves incumbent Queue
   order unchanged. Never choose one battle response manually.
6. Changed belief evidence stales a queued Plan. Review it, use `plan refresh`
   with a complete utility reassessment, then `queue insert` again.

Read [methodology.md](references/methodology.md) before designing, closing, or
combining experiments.

## Configure runtime v2 and optional MLflow observation

For independent Sources, `.exp/runtime.json` must use the closed
`exp.runtime/v2` schema. It maps canonical Pools to Pueue groups and each Plan to
one writable execution Source plus optional read-only Sources, exact full
base/head commits and ChangeSets, managed/main/registered checkout selection,
argv, cwd, expected outputs, and non-sensitive environment names.

Review the raw runtime bytes and approve their exact digest for
`runtime.dispatch` with `config trust`. Formal v2 captures only clean exact
SourceSnapshots and passes explicit Project/root/scope authority into worker v2.
Dirty/missing/stale/untrusted state blocks before scheduler submission. Runtime
v1 remains only the embedded compatibility route.

A named MLflow profile is layered `exp.config/v1` policy. Repository-defined
profile selection/body requires exact `mlflow.profile` trust. Direct Try may use
an environment-bound profile; formal Pueue runtime rejects profile environment
bindings because Pueue persists task environments. Use a value-free formal
profile and let the workload obtain credentials through its own broker.

```text
exp --workspace WORKSPACE --source SOURCE config explain
exp --workspace WORKSPACE --source SOURCE config trust
exp --workspace WORKSPACE --source SOURCE \
  --mlflow-profile PROFILE daemon frontier
exp policy autonomy assisted --confirm-auto-experiment
exp --workspace WORKSPACE --source SOURCE \
  --mlflow-profile PROFILE daemon run --interval 5s
```

`daemon frontier` is provider-free. `daemon tick` and `daemon run` contact Pueue
and dispatch only in explicitly enabled modes. The workload creates/logs its
MLflow run; exp performs optional selected read-only observation. Never derive a
Finding, Evaluation, verdict, Candidate, Release, or Promotion merely from
scheduler, worker, output-hash, or tracker status.

## Preserve clean evidence and the human production gate

- Close an Experiment with explicit included/excluded Run dispositions. Record
  `invalid` when evidence cannot answer the registered question and
  `inconclusive` when valid evidence remains insufficient.
- Evaluation v2 must bind the exact successful clean formal Attempt v3 whose Run
  belongs to the concluded Experiment. Metrics must match the declared
  EvaluationSpec. MLflow may corroborate selected values/ownership only.
- Candidate v2 requires a supported conclusion including that Run, a passing
  scientific Evaluation v2 bound to the same Attempt, and clean snapshots. It
  copies exact Source IDs/head commits/ChangeSets—not checkout or artifact bytes.
- Compose a typed Release. More than one Candidate requires a separately
  evaluated combination Experiment; never add independent gains arithmetically.
- Create/seal the PromotionSpec before a fresh non-reused holdout Evaluation of
  the validated Release. Promotion always requires a named human plus explicit
  confirmation; no autonomy mode or agent can approve it.
- Treat append-only Promotion as authority. Champion manifests and `exp ui` are
  derived read-only views, never canonical input or automatic deployment.

See [records-and-project-knowledge.md](references/records-and-project-knowledge.md)
for record ownership and routing to TODO/backlog/pitfalls/invariants.

## Keep provider-owned bytes out of Git

Large datasets, checkpoints/models, artifact files, traces, and unbounded logs
remain in MLflow, DVC, or object storage. Canonical Git records may contain only
bounded summaries, selected values, cryptographic digests, exact Source/commit
identities, and sanitized refs. Never commit credentials, raw environments,
host-local paths, large artifact bytes, or unbounded provider output.

## Handle missing tools without hiding the flow

Default `exp doctor` performs local executable lookup only. `doctor --live`
explicitly runs bounded version/read-only local-service probes; it never logs in,
installs, writes config, starts a service, or executes a workload.

Missing MLflow, Pueue, or `dev` does not hide actions and does not block native
Try. Pueue is required only for formal daemon dispatch; MLflow is optional
observation. `dev_cli` prepare/open/handoff/retire remains unsupported until
`dev` exposes schema-versioned exact-path machine capability receipts. Trusted
fallback may choose `native_git`; native Git always owns byte verification,
inspection, and cleanup.

Read [external-tools.md](references/external-tools.md) before crossing into
Pueue, MLflow, Git, DVC, or another provider.

## Machine use, discovery, and recovery

Prefer `--json`, parse the complete `exp.cli/v1` envelope, and carry full typed
IDs/revisions. Never scrape human tables or reconstruct facts from generated
projections. `exp context` is the local resumable summary; `exp guide` explains
flow choices; shell completion reads local records/associations/config only; and
`exp ui` is read-only with explicit bounded readiness refresh.

Use domain commands for mutations. The public `record transaction` surface is
limited to low-risk Idea/ResourcePool edits and recovers through prepared
hash-checked journals. On interruption or a recovery diagnostic use
`record recover`; do not hand-edit split canonical state.

Harness-v0 migration is opt-in and fingerprinted: plan, resolve every
`needs_review`, then apply that exact plan. Never execute legacy scripts.
Optuna-like search remains scoped inside one Plan revision and never owns Queue,
Resources, Findings, Releases, or Promotions.

See [usage-and-fallback.md](references/usage-and-fallback.md) when the binary is
unavailable or for machine-output rules.
