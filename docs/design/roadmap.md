# Implementation roadmap

The current release is the local research control-plane foundation. Milestones
below separate delivered behavior from deliberate future integrations. Commands
are added only when their behavior is functional.

## Delivered: canonical research foundation

- fixed `<git-root>/experiments` discovery and idempotent initialization;
- strict, versioned Markdown/TOML records with UUID identity, privacy checks,
  graph validation, deterministic projections, and stable JSON envelopes;
- linked-worktree ID reservations and Git-common locking;
- prepared multi-record create/replace/delete transactions with exact-byte
  journals, hash-checked roll-forward recovery, failure injection, and explicit
  `record recover`;
- local `record list/show/transaction`, `validate`, `render`, and `context`.

## Delivered: research queue and agent collaboration

- explicit `POLICY.md`, default-manual autonomy, controlled classification,
  cluster saturation data, and 80/20 exploit/explore shares;
- human or agent Ideas, parent Idea lineage, and atomic qualification into
  resource-priced Plan v2 records;
- named ResourcePools and ordered pool/lane Queue partitions;
- transparent expected-value scoring, global listwise advice, order-swapped
  adjacent pairwise battles, immutable audit records, and human-review fallback;
- fresh single-shot agent CLI profiles with strict JSON Schema output,
  environment allowlists, secret references, bounded output, and no SDK session.

## Delivered: local execution control plane

- strict `.exp/runtime.json` bindings from Pools/Plans to Pueue groups and exact
  workload argv/Git identity;
- local frontier inspection, one-shot daemon tick, continuous daemon loop,
  pause/resume, lease fencing, weighted fairness, and outbox recovery;
- SQLite operational state under the Git common directory, never canonical;
- sanitized Pueue status, audited private-worker submission, explicit cancel;
- durable worker terminal markers and replay-safe completion;
- isolated XDG Git worktrees and exact allowlisted experiment auto-commits,
  without merge or cleanup authority;
- read-only MLflow run verification; workloads own run creation and logging.

## Delivered: scientific closure and production boundary

- atomic Experiment closure, Plan completion, evidence dispositions, and
  Finding publication;
- revision-aware belief dependencies and stale-queue detection;
- immutable EvaluationSpecs and Evaluations;
- Candidate creation from supported evidence with full Git commit/ChangeSet;
- typed Release slots and mandatory evaluated combination evidence for
  multi-Candidate Releases;
- sealed promotion-purpose evaluation, append-only human Promotion chains, and
  derived Champion manifests.

## Delivered: compatibility and extension contracts

- explicit harness-v0 migration plan/apply with exact-byte archive,
  deterministic UUIDv5 identities, reviewed ambiguity resolutions, fingerprint
  revalidation, and recoverable root swap;
- provider-neutral `exp.search-adapter/v1` contract for idempotent Plan-scoped
  Study open/ask/tell/prune/observe;
- version-matched embedded skill and generated command reference;
- repository-local Go 1.26.4 pin through `mise.toml`.

## Next: harden unattended operation

Priority work should improve recovery and observability without weakening the
authority model:

- long-running daemon soak and crash tests around Pueue submit ambiguity,
  expired job leases, worker interruption, and provider restart;
- clearer bounded event/audit inspection for SQLite operations and outbox state;
- policy-level semantic distinction between `assisted` and `limited` beyond the
  shared explicit dispatch gate;
- richer queue saturation and budget-consumption feedback from completed work;
- first-class follow-up and combination Experiment creation, including a
  supported path from an agent-prepared exact commit into a new executable
  Plan/Attempt instead of hand-authored canonical records;
- explicit holdout-budget consumption accounting and immutable Release
  supersession ergonomics;
- migration fixtures from more real harness-v0 layouts;
- runtime Windows support only after process-tree and SQLite behavior is tested;
  AIX remains an explicit operational-store non-support target.

## Next: concrete Plan-scoped search

Implement an Optuna adapter only after the integration can prove:

1. supported Optuna/storage versions and safe capability probes;
2. durable idempotency for `open`, `ask`, `tell`, and `prune`;
3. recovery for timeout-after-provider-commit ambiguity;
4. secret-reference-only storage configuration;
5. multi-objective and trial-state mapping;
6. bounded, structurally sanitized observations.

Optuna remains subordinate to one Plan revision. It will not replace the global
Queue or allocate ResourcePools.

## Later provider capabilities

Add external operations one verified capability at a time:

- richer Pueue observation/cancellation reconciliation and bounded logs;
- MLflow artifact/registry reads only where the CLI has a stable safe surface;
- DVC artifact and queue reads, then narrowly scoped writes;
- named-site Slurm probes and scheduling with explicit environment export;
- notebook runners as workload entrypoints, not durable schedulers.

Every new mutation must declare effects, preserve argument boundaries, expose
reviewable identity, and avoid implicit installation, authentication, daemon
startup, or artifact download.

## Explicitly deferred

- automatic production deployment or rollback execution;
- agent-approved Promotion;
- a universal cloud scheduler or model registry abstraction;
- W&B, Kaggle, Ray, Kubernetes, Azure ML, Databricks, Modal, RunPod, or generic
  browser-session control;
- multiple `experiments/` roots in one repository or cross-repository graphs;
- dynamic Go plugins, a mandatory FTS index, or a TUI;
- raw telemetry/log mirroring and artifact-byte storage;
- assuming gains from independent Candidates combine without a dedicated
  Experiment and Evaluation.
