# Implementation Roadmap

The current release delivers a Git-native research control plane whose canonical
experiment repository can be independent from its Source repositories. This page
separates implemented behavior from remaining integrations and explicit
non-goals. A command is listed as delivered only when its current code path is
functional and tested.

## Delivered: canonical research and recovery

- fixed `experiments/PROJECT.md` discovery with idempotent Project v1
  initialization and cross-linked-worktree Project receipt reconciliation;
- strict Markdown/TOML records, UUID identity, privacy/path checks, graph and
  lifecycle validation, deterministic revisions/projections, and stable JSON
  envelopes;
- Source v1, Try v1, Idea v2, Attempt v3 SourceSnapshots/retry ownership,
  Evaluation v2 typed Attempt ownership, and Candidate v2 Source identity;
- linked-worktree ID reservations and Git-common locking;
- worktree-scoped `exp.transaction/v2` journals in `transactions-v2/`, exact-byte
  roll-forward recovery, and conditional backward reading of v1 journals;
- local `record list/show/transaction/recover`, `validate`, `render`, `context`,
  and exact harness-v0 plan/apply migration.

## Delivered: independent experiment repository and Sources

- dedicated private experiment repository as the guided initialization default,
  including safe adoption or explicit creation of a missing/empty Git target;
- no implicit remote, commit, submodule, or push during dedicated initialization;
- embedded/monorepo initialization retained for shared governance and legacy v1
  compatibility;
- ordinary canonical Git Source records with project-unique key, immutable
  subdir, sanitized locator history, active/retired lifecycle, and exact CAS
  updates;
- multiple Sources per Project and Source-aware Experiment workspace commands;
- private `exp.associations/v1` Project/Source mappings with clone/Git-common
  filesystem identity and locator revalidation;
- deterministic explicit/current-Project/most-specific-Source resolution with
  ambiguity and staleness reported rather than guessed;
- XDG user/state/cache/data paths with no host paths in canonical Source records.

## Delivered: layered config, trust, and profiles

- strict `exp.config/v1` hierarchy: built-ins, XDG user, canonical repository,
  Source root, then Source-subdir root-to-leaf files;
- documented scalar/array/backend/profile merge rules, combined digest, per-leaf
  provenance, and applied-layer audit;
- private `exp.trust/v1` receipts bound to exact bytes, capability, Project/
  Source/config scope, Git-common path, and filesystem identity;
- `config path/show/explain/trust/revoke/list`, including historical revocation
  and sanitized output;
- workspace backend profiles with `native_git` as mandatory correctness baseline;
- named MLflow profiles with binary/context/timeout/default metrics and
  environment names/policy only; explicit compatibility flags remain available;
- exact `runtime.dispatch` trust for runtime v2, separate from layered config.

## Delivered: bounded Try workflow

- `try run` publishes Try plus planned Attempt v3 before operational execution;
- direct argv execution in deterministic native Git worktrees with empty-by-default
  change allowlist and bounded timeout;
- clean capture or explicit Try-only `--dirty=capture` with full tracked patch,
  bounded untracked bytes, clean direct submodules, canonical dirty/snapshot
  digests, and private authenticated `exp.source-seed/v1` bundle;
- workspace preparation marker recovery and byte-exact seed round-trip checks;
- private lease/fencing job execution, heartbeat, bounded result file, redacted
  v2 streams, and durable terminal/result markers;
- conservative resume, marker/SQLite repair, explicit unknown reconciliation,
  one-successor retry lineage with unchanged Source/config/argv identity;
- human conclusion/abandonment, result ownership checks, atomic adoption as Idea
  v2, status, and partial-safe cleanup;
- native cleanup of verified clean or exactly seeded worktrees plus private seed,
  while retaining branch, commit, markers, operation rows, and canonical records.

## Delivered: Source-aware formal runtime

- closed `exp.runtime/v1` retained for embedded Attempt v2/worker v1 dispatch;
- separate strict `exp.runtime/v2` with one writable execution Source, multiple
  read-only Sources, `main`/`registered_worktree`/`managed_worktree` selection,
  explicit observational no-change, and Source-subdir-relative cwd/outputs;
- exact clean SourceSnapshot capture for every Source and formal Attempt v3;
- cross-repository canonical-before-operational creation of Experiment, Run,
  Attempt, Plan transition, and Queue removal;
- outbox submission revalidation that marks an unstarted job/Attempt blocked if
  Source authority disappears instead of submitting optimistically;
- worker-job/terminal/result v2 with explicit canonical root, Project UUID,
  checkout-local scope, metadata-only pre-payload authorization, fencing, private
  checkout identity, and post-success read-only Source verification;
- durable marker temporary promotion and database-independent replay without
  rerunning the workload;
- clean formal Candidate gate through Evaluation v2 and Candidate v2.

## Delivered: research queue, closure, and promotion

- default-manual Policy, controlled classification, cluster saturation, and
  80/20 exploit/explore allocation;
- Ideas, qualified resource-priced Plan v2 records, named ResourcePools, and
  globally unique ordered pool/lane Queue partitions;
- transparent scoring, listwise advice, order-swapped pairwise battles,
  immutable audit records, and human-review fallback;
- daemon frontier/tick/run/pause/resume, project lease fencing, weighted
  fairness, Pueue outbox recovery, sanitized status, and ownership-checked cancel;
- Experiment design locking/amendments/closure, explicit Run evidence
  dispositions, Findings and belief-staleness propagation;
- EvaluationSpecs/Evaluations, Candidate v1 compatibility and Candidate v2,
  typed Release slots, mandatory combination evidence, sealed promotion holdout,
  append-only human Promotion chains, and Champion manifest v1/v3.

## Delivered: provider and UI boundaries

- provider registry/discovery with bounded local probes and sanitized readiness;
- Pueue scheduling/control within its declared capabilities;
- read-only MLflow verification, exact Attempt ownership, selected metrics/tags,
  sanitized artifact URI, and optional worker observation that does not change
  workload success when unavailable;
- provider-neutral `exp.search-adapter/v1` interface contract (no concrete search
  backend yet);
- workspace-provider registry with requested/actual/fallback reporting;
- `exp ui` read-only Workflow, Workspace, Tries, Queue, Attempts, Candidates, and
  Readiness tabs; cancellable generation-fenced reads, read-only SQLite open, and
  explicit bounded probes only;
- version-matched embedded skill and generated command reference.

## Remaining: unattended-operation hardening

Priority hardening can improve observability without expanding authority:

- longer daemon/worker soak and crash tests around Pueue submit ambiguity,
  expired leases, provider restart, marker/result publication, and outbox repair;
- clearer bounded event/audit inspection and budget-consumption reporting;
- policy semantics that distinguish `assisted` from `limited` beyond the shared
  explicit dispatch gate;
- first-class ergonomic creation of follow-up/combination Experiments without
  weakening the existing typed gates;
- explicit holdout-budget consumption accounting and Release supersession
  ergonomics;
- additional real harness-v0 migration fixtures;
- broader runtime/process-tree verification on Windows; AIX deliberately keeps
  only canonical Git operations and reports the operational store unsupported.

## Remaining: optional workspace provider

`dev_cli` is discoverable but **all lifecycle capabilities fail closed** today:
prepare, inspect, cleanup, open, handoff, and retire are compiled unsupported.
There is no public schema-versioned, content-free capability response or exact
native-path machine receipt with verifiable occupancy. Human help/version/catalog
output is not authorization.

Until such a contract exists:

- no lifecycle `dev` subprocess is invoked;
- trusted selection may report an explicit fallback to `native_git`;
- native Git owns prepare, byte verification, inspect, cleanup, and retire;
- provider-local task/catalog/worktree IDs are never canonical authority.

A future capability can be enabled only after it returns an exact machine receipt
and native postconditions prove that the requested worktree was acted upon.

## Remaining: concrete Plan-scoped search and providers

The provider-neutral Study contract exists, but no concrete Optuna runtime does.
A future adapter must prove version/capability support, durable idempotency,
timeout-after-provider-commit recovery, secret-reference-only storage config,
trial-state mapping, and bounded sanitized observation. It remains subordinate
to one exact Plan revision and cannot replace Queue/ResourcePool authority.

Additional Pueue observations, MLflow registry/artifact reads, DVC, Slurm, and
notebook entrypoints remain capability-by-capability work. Each must declare
effects, preserve argv boundaries, avoid implicit installation/login/service
start/download, and keep provider state non-canonical unless explicitly imported.

## Explicit non-goals and true limits

The current scope does not provide:

- automatic Git merge, push, rebase, branch deletion, production deploy, or
  rollback execution;
- agent-, provider-, autonomy-, manifest-, or TUI-approved Promotion;
- a large artifact store, raw telemetry/log mirror, artifact-byte persistence,
  or automatic artifact/model download;
- direct Candidate/Promotion from a Try—especially a dirty Try; a clean formal
  rerun, typed Evaluation v2, and Candidate v2 are required;
- multiple canonical Project roots in one Git repository or canonical relations
  across different Project UUIDs (multiple external Sources inside one Project
  are delivered);
- a universal cloud scheduler/model registry, generic browser-session control,
  or dynamic Go plugin ABI;
- automatic scientific verdicts from process, scheduler, tracker, commit, or
  artifact state;
- execution of legacy harness scripts during migration.
