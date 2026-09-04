# Runtime Dispatch

`exp` has two execution paths with different scientific authority:

- **Direct Try** is a bounded local exploration in one managed Source worktree.
  It may explicitly capture dirty state, but can never directly back a Candidate.
- **Formal dispatch** consumes a qualified Queue frontier and runs through Pueue.
  Runtime v2 can execute across independent Source repositories, but admits only
  clean, exact SourceSnapshots.

Both paths publish canonical Attempt intent before execution, keep jobs and host
paths private, and import terminal facts only after identity checks. Neither
process success nor provider state is a scientific verdict.

Use the terminal guides and local status before choosing a path:

```bash
exp guide quick
exp guide research
exp workspace status
exp source status
exp config show
exp doctor
```

Default `doctor` performs executable lookup only. `doctor --live` is an explicit
bounded version/read-only local-service probe. Missing MLflow, Pueue, or `dev`
does not hide commands and does not block a native Try; Pueue is required only
when formal `daemon tick`/`run` actually dispatches work.

## Resolve Project, Source, config, and trust

Every invocation first resolves one canonical Project. That Project normally
lives in a dedicated private experiment repository; existing embedded Projects
remain discoverable. `--workspace` selects the Project by UUID/path and
`--source` selects an ordinary Source by key/ID/prefix/display code. Otherwise a
physical Project marker or the most-specific registered Source may resolve the
context.

A Source association is host-local and is revalidated against Project/Source
identity, Git-common filesystem identity, immutable subdir, and sanitized
locator hints. Only after resolution does `exp` load layered `exp.config/v1`.
Execution-bearing repository winners require exact-digest trust. Runtime v2 has
its own required `runtime.dispatch` receipt for the raw `.exp/runtime.json`
digest. See [Configuration and Paths](../reference/configuration.md).

## Direct Try

A non-interactive clean Try can be started from a registered Source directory:

```bash
exp try run \
  --title "Probe a bounded change" \
  --goal "Decide whether the direction merits a formal Experiment" \
  --allow 'src/**' \
  --timeout 20m \
  -- project-runner --config configs/probe.toml
```

Arguments after `--` remain argv; no shell parses them. The executable must be a
PATH basename or a managed-worktree-relative path. No argument may be a POSIX,
drive, UNC, or `file:` absolute path, including common attached flag values such
as `--config=/host/path`; known workspace paths are also rejected. The Try's
`cwd` is relative to the Source's declared subdir. An empty `--allow` set makes
the managed worktree read-only. Dirty seed paths are baseline state, not implicit
write permissions: their existence, mode, size, and bytes must remain captured
unless an explicit user allow-glob matches them.

### Clean and dirty registration

Clean mode requires no tracked, untracked, dirty-submodule, or hidden-index
state. It records a clean `capture-v1` SourceSnapshot at the current full HEAD
with `base_commit == head_commit`, empty committed ChangeSet allowed, exact
reproducibility, and no seed bundle.

Dirty state must be requested literally:

```bash
exp try run \
  --title "Reproduce a local probe" \
  --goal "Bound the effect before a clean rerun" \
  --dirty=capture \
  --allow 'src/**' \
  -- project-runner --config configs/probe.toml
```

`--dirty` accepts only `capture`; dirty state is never inferred. Capture is
Try-only and bounded: it retains a full-index binary patch, regular untracked
file bytes/modes/hashes, final dirty-path existence/mode/size/digests, and clean
direct submodule commits in a private `exp.source-seed/v1` bundle under the XDG
cache. It rejects paths outside Source
`subdir`, rename/copy ambiguity, ignored/unmerged or hidden index state, dirty or
nested submodules, symlinks/special nodes, and configured hard limits. The
canonical Attempt contains only a bounded dirty summary, dirty/snapshot digests,
and sorted Git-root-relative paths—not raw bytes or host paths.

### Ordering and recovery

Direct execution progresses through explicit boundaries:

1. resolve and verify config/Source;
2. capture clean or dirty Source state;
3. atomically publish an open Try and planned Attempt v3, guarded by the Source
   revision;
4. refresh derived projections;
5. prepare/reuse the deterministic native Git worktree and verify exact seed;
6. enqueue and exact-ID claim one private `try-direct` job;
7. advance the canonical Attempt through queued/starting/running;
8. hold the workspace lock across final Source/config/worktree preflight and
   process spawn;
9. freeze the bounded result, publish the aggregate-bounded worker-terminal v2
   marker, finish the private job, and import terminal state/result digests only
   after a positive native workspace inspection;
10. refresh projections.

Canonical publication precedes the job/worktree, so a crash can leave a planned
Attempt with no execution. `exp try resume TRY` may resume that provably unstarted
Attempt, repair SQLite from a valid marker, or import a late marker. It never
re-invokes an uncertain execution. A running job with a live lease reports
in-progress; the default direct lease is two minutes and heartbeats every third
of that interval. An expired lease without a marker moves the Attempt to
`unknown`, bounding ordinary local crash-recovery latency.

`exp try reconcile TRY --attempt ATTEMPT --abandon-uncertain --reason TEXT
--confirm` first checks again for a marker/live lease and only then records a
human cancellation. `exp try retry TRY` creates a new Attempt only after the
latest is terminal; otherwise it delegates to resume. Retry preserves Try,
Source state, cwd, argv, execution Source, config digest, MLflow profile identity,
and one-successor `retry_of` lineage. Source/config drift blocks it.

A Try can finish/abandon only after every owned Attempt is terminal. Selected
result digests must have been produced by owned Attempts. `try adopt` requires a
human and atomically creates Idea v2. A dirty Try that appears useful still needs
a clean formal rerun.

### Cleanup

```bash
exp try status TRY
exp try cleanup TRY --confirm
```

Status is observation-only: it combines canonical records with optional jobs,
markers, and native worktree inspection without starting work or creating,
repairing, or migrating the operational database. Unfiltered `try list` and
`try status` return at most 100 Tries by default and accept `--limit 1..1000`
plus `--offset`; selecting one Try is not paginated. Cleanup visits
each terminal direct Attempt independently. Native Git removes a clean-at-base
worktree normally; for a dirty seed it verifies the complete seed twice before
the guarded force removal. Only then is the authenticated seed bundle deleted
and canonical `cleanup_completed` recorded. Changed/unverifiable worktrees are
refused. Cleanup never removes the canonical Try/Attempt, branch, commit,
operation row, or terminal/result marker, and can return a truthful partial
result.

## Formal dispatch

Inspect the canonical frontier without contacting Pueue, then run one or
continuous passes:

```bash
exp daemon frontier
exp daemon status
exp policy autonomy assisted --confirm-auto-experiment
exp daemon tick
# or: exp daemon run
```

`manual` and `shadow` expose frontiers but do not admit work. `assisted` and
`limited` permit dispatch after the explicit policy transition. Named
ResourcePools enforce capacity in units; weighted exploit/explore counters can
borrow idle capacity only when the other lane has no eligible work.

### Runtime v1 and v2 stay separate

`.exp/runtime.json` binds ResourcePool IDs to Pueue group/label namespaces and
Plan IDs to executable contracts. Both schemas are strict, non-secret JSON:

- `exp.runtime/v1` is the embedded compatibility route. It verifies one
  experiment repository's `main` or `registered_worktree`, top-level
  base/head/ChangeSet, and creates Attempt v2 plus worker-job v1.
- `exp.runtime/v2` names one writable execution Source and zero or more read-only
  Sources. Each selects `main`, `registered_worktree`, or `managed_worktree` and
  pins full base/head IDs plus exact ChangeSet. It creates Attempt v3 and
  worker-job v2.

V2 `cwd` and expected-output paths are relative to the execution Source's
canonical subdir; ChangeSets remain Source Git-root-relative. Empty ChangeSet is
allowed only as an explicit no-change observation with equal base/head. Every
Source must be active, canonically declared, registered locally, and unique.

Before a frontier can be prepared, runtime v2 rechecks the raw runtime digest
trust, effective execution-Source config, workspace-backend selection, Source
records/associations, exact object format/HEAD/base ancestry/ChangeSet, clean
status, and optional managed-worktree postcondition. It captures every Source as
a clean SourceSnapshot. Dirty state is a formal gate failure, not something
runtime v2 captures. Missing or changed state before recovered outbox submission
terminalizes the unstarted private job as `unknown` and marks the canonical
Attempt `blocked`; it is not sent to Pueue.

### Canonical-before-operational admission

For one exact Queue frontier, the controller atomically:

1. creates and locks an active Experiment design;
2. creates its intended Run;
3. creates planned Attempt v2 or v3 with Queue/Pool/lane/dispatch identity;
4. marks the Plan started and points it to the Experiment;
5. removes exactly that Queue entry and increments Queue revision.

Only afterward does it atomically enqueue/claim the private job and submission
outbox, then submit to Pueue through a fenced lease. Stable group/label identity
supports recovery without guessing from a task ID. A Pueue task reference is
attached only after accepted submission.

### Explicit worker authority in v2

Pueue receives argv, never a shell command. Runtime v2 worker argv includes:

```text
--canonical-root <exact experiment Git root>
--project <Project UUID>
--scope <checkout-local canonical scope>
--job <private job ID>
--fencing-token <positive token>
```

The hidden worker requires the first three together. It re-discovers the exact
Project/root, recomputes scope, opens that Project's operation store, reads only
metadata authority, and verifies job kind/role/state/fencing/scope before loading
the payload. The payload then must match Project, scope, Attempt, writable
execution Source, path-bound checkout identity, and every sorted Source binding.
All non-execution Sources must be read-only and all snapshots clean. This
prevents a Source checkout, Pueue working directory, or copied job payload from
choosing canonical authority.

Immediately before spawn, native Git and Source capture revalidate every binding.
After a successful process, read-only Sources are revalidated. The worker starts
with a minimal environment plus explicit non-sensitive names; Pueue routes reject
`secret_env` and credential-sensitive allowlist names. Formal v2 adds
`EXP_PROJECT_ID`, `EXP_CANONICAL_SCOPE`, and `EXP_EXECUTION_SOURCE` to the shared
`EXP_JOB_ID`, `EXP_ATTEMPT_ID`, and assigned `EXP_RESULT_PATH` variables.

### Marker and outbox recovery

The worker freezes the bounded result file, hashes expected regular outputs, and
publishes an fsynced terminal marker before updating SQLite. If publication
stops after the valid `.tmp` marker and frozen result, restart can verify and
promote it. A final marker can repair a running SQLite row or be imported after
database loss. Marker/job identity, fencing, schema, timing, result digest, and
optional MLflow ownership must match; otherwise recovery fails closed. Replay
never executes the workload again.

A missing marker does not prove non-execution. Scheduler state alone may advance
bounded operational state but never closes the Experiment or creates an
Evaluation. Unknown/ambiguous cases remain blocked or unknown until explicit
human review.

## Workspace and MLflow provider behavior

`native_git` is the mandatory workspace byte/lifecycle authority. Optional
`dev_cli` is discoverable, but prepare/inspect/cleanup/open/handoff/retire are
compiled unsupported because no schema-versioned exact-path machine receipt
exists. Trusted policy may explicitly fall back to native Git; no provider-local
ID becomes canonical. Native Git always performs final inspection and cleanup.

A trusted MLflow profile may be selected for read-only attachment observation.
Formal Pueue runtime accepts only profiles with no environment bindings; a
workload needing credentials must use its own broker. Inspect and trust the
profile, then carry the same explicit selection through validation and dispatch:

```bash
EXP_REPO="$HOME/src/my-application-experiments"
exp --workspace "$EXP_REPO" --source app config explain
exp --workspace "$EXP_REPO" --source app config trust
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon frontier
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon run --interval 5s
```

The workload creates/logs the run and may write `mlflow_run_id` in its bounded
result. Provider absence, missing assertions, or ownership mismatch produces an
unavailable/unverified optional observation and does not reverse process
success. Only a verified `exp.attempt_id` match becomes a sanitized Attempt
ExternalRef. Artifact URIs and bytes remain MLflow authority and do not become
Evaluation or Promotion.

Large datasets, model/checkpoint bytes, artifact files, traces, and unbounded
logs remain in MLflow, DVC, or object storage. Canonical Git records receive
only bounded summaries/digests, exact Source/commit identities, and sanitized
references—not those bytes, credentials, raw environments, or host paths.

## Formal evidence gate

A successful formal process still is not a Candidate. The promotion-bearing
chain requires the Experiment to include the Run and close supported, Evaluation
v2 to bind that exact successful clean Attempt v3, and Candidate v2 to copy the
Attempt's Source IDs/head commits/ChangeSets exactly. See
[Evidence to Promotion](evidence-to-promotion.md).

Resume with `exp context`; compare the conceptual routes with `exp guide`; use
`exp completion --help` for local Source/record completion; and open `exp ui`
for a read-only terminal view. None of these commands grants scientific or
promotion authority.
