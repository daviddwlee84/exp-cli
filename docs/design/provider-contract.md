# Provider contract

## Boundary

Providers expose capabilities of installed upstream tools; they do not become
authorities for research records. The current implementation includes audited
Pueue scheduler operations, a private direct worker envelope, and read-only
MLflow run verification. Default `doctor` remains executable-presence discovery
only. There is no dynamic Go plugin ABI and no universal “experiment provider”
interface.

An adapter declares one descriptor and implements only the roles it supports:

| Role | Responsibility | Initial direction |
|---|---|---|
| Runner | Prepare an entrypoint as an argument-array workload | Private direct worker implemented; notebooks later |
| Scheduler | Submit, observe, and cancel one Attempt; own native dependencies | Pueue implemented; Slurm/DVC later |
| Tracker | Resolve/list provider-owned telemetry and sweep state | MLflow selected-field verification implemented |
| ArtifactStore | Stat/list immutable artifact references | DVC/MLflow/object references; never implicit download |
| Registry | Get/list/resolve aliases for model resources | Read-only only after a concrete API is verified |

A provider may implement several roles, but every operation belongs to one role and one capability. Every Attempt has exactly one Scheduler owner. Nested schedulers are rejected unless an explicit reviewed operation plan assigns concurrency and cancellation ownership.

Consumer Google Colab browser sessions have no durable general control plane and are not supported. A future adapter requires a specifically named, documented enterprise service such as an applicable Vertex/Colab Enterprise API; the consumer UI will not be scraped.

## Descriptor and capability probing

A descriptor is static and contains:

```text
provider name
implemented roles
candidate binary names
known capability names
adapter contract version
```

A probe returns:

```text
provider
configured context
resolved binary path (sanitized for display where necessary)
provider version
capabilities: capability -> support
observed_at
diagnostics
```

Support is tri-state:

```text
supported | unsupported | unknown
```

- `supported`: the adapter verified the required binary/version/feature contract.
- `unsupported`: the adapter positively knows this provider/version lacks it.
- `unknown`: discovery or safe verification was inconclusive.

Missing optional binaries, unreachable daemons, unavailable accounting, and unknown versions degrade that provider; they do not fail local record commands. Unknown is never promoted to supported by optimistic parsing.

Default `exp doctor` performs only local executable discovery with `LookPath`.
It never executes third-party `--version` because nominally read-only flags may
still create configuration, telemetry, or log state; discovered versions and
capabilities therefore remain `unknown`. `--live` currently performs no
additional contact. Provider contact occurs only through an operation-specific
command such as `provider pueue status`, `provider mlflow verify`, `daemon tick`,
or `daemon run`. Probing and operations never install a package, start a daemon,
migrate a provider database, or open authentication.

## Operation plans and effects

Before invocation, an adapter constructs a reviewable plan:

```text
provider
context
role
capability
operation
executable
argv[]
cwd
environment names and sensitivity (never secret values)
timeout/output bounds
effects
sanitized diagnostics
```

Effects are a versioned set drawn only from:

```text
local_read
remote_read
local_write
remote_write
executes_user_code
starts_service
credential_flow
destructive
sensitive_output
blocking
```

Examples:

| Operation | Required effects |
|---|---|
| local binary version | `local_read` |
| `pueue status --json` | `remote_read`, `sensitive_output` |
| remote MLflow run lookup | `remote_read`, `credential_flow` when credentials are needed |
| artifact download | `remote_read`, `local_write`, and possibly `credential_flow` |
| Marimo/Jupyter execution | `executes_user_code`, normally `local_write` |
| daemon startup | `starts_service`, `local_write` or `remote_write` |
| queue reset or garbage collection | `destructive`, `remote_write` |
| follow/wait | read effect plus `blocking` |

Where a future command exposes `--plan` or `--dry-run`, those modes must render
currently known plans and effects only. They perform no invocation, remote or
daemon contact, authentication, package resolution, service startup, file
creation, cache write, or user-code execution. Current Pueue cancellation and
Promotion use explicit confirmation flags instead of pretending to be dry-run
interfaces.

## Invocation contract

Every subprocess goes through one injected, signal-aware `execx.Invoker`. Adapters never call `os/exec` themselves.

The invoker accepts an executable and an argument slice, never a concatenated shell command. It also receives an explicit canonical cwd, an environment specification, context cancellation, timeout, stream/capture mode, and byte limits. It preserves argument boundaries exactly. Errors and diagnostics contain only structurally redacted display arguments.

Provider protocol output is bounded, parsed, redacted, and then returned. User
workload output may stream to the caller but is not persisted by default.
Process-group ownership is Unix-specific: cancellation, timeout, and normal
parent exit terminate the spawned Unix process group so ordinary descendants
do not outlive the invocation. Windows uses Go's direct-child cancellation
only; descendant-tree termination would require Job Object integration and is
not claimed. Runtime CI currently exercises Linux and macOS; Windows and AIX
jobs are cross-build checks, not runtime-test claims.

If an upstream scheduler accepts only a shell payload, the adapter must use one audited escaping implementation or invoke a private argument-preserving `exp` execution envelope. It must not interpolate titles, labels, native state, metric values, or external text into shell syntax.

No adapter may implicitly:

- install or upgrade a binary or Python package;
- invoke `uvx`, `pip`, PEP 723 resolution, or another package resolver;
- start a provider service or daemon;
- initiate login, OAuth, browser, keychain, or credential setup;
- make network contact during local-only commands;
- download artifacts;
- execute notebook or user workload code during inspection.

## Parsing and normalized results

Parsing priority is mandatory:

1. Native JSON.
2. Native CSV or explicitly requested fixed delimiters/fields.
3. An explicitly configured, versioned SDK or REST implementation.
4. `raw_only` with bounded, sanitized output and a diagnostic.

Pretty terminal tables are never scraped. A native enum value unknown to the adapter maps to normalized `unknown`, retains the sanitized native token/reason, and emits a diagnostic. Unknown terminal states fail closed.

A provider observation contains:

```json
{
  "provider": "pueue",
  "context": "local-synthetic",
  "provider_version": "4.0.4",
  "capability": "scheduler.observe",
  "support": "supported",
  "source": "pueue status --json",
  "observed_at": "2026-08-20T10:05:00Z",
  "stale": false,
  "partial": false,
  "normalized_state": "succeeded",
  "native_state": "Done",
  "native_reason": "",
  "raw_only": false,
  "raw_state": {},
  "diagnostics": []
}
```

`raw_state` is optional, bounded, and already structurally redacted. Observation data is not canonical merely because it is valid JSON. Cache entries carry their source, observation time, completeness, and freshness policy and remain disposable.

Machine commands wrap data in the CLI envelope:

```json
{
  "schema_version": "exp.cli/v1",
  "command": "context",
  "ok": true,
  "partial": false,
  "observed_at": "2026-08-20T10:05:00Z",
  "data": {},
  "diagnostics": []
}
```

Machine mode never prompts and emits no warnings or progress on stdout.

## Refresh semantics

Local canonical reads are the default. `plan list`, `validate`, `render`,
`context`, `daemon status`, and `daemon frontier` make zero provider
invocations. Contact is explicit through the `provider` commands or daemon
`tick`/`run`; cached observations are never silently refreshed.

If a future combined refresh interface is added, it must name exact provider
contexts/capabilities, preserve partial success with per-provider diagnostics,
and never change an Experiment verdict or evidence inclusion. Attempt state may
advance only through explicit reconciliation and canonical revision checks.

## Environment and credential handling

Configuration stores references such as an environment-variable name, upstream profile, credential-file selector, or keychain selector. It never stores the secret value.

Invocation starts from a minimal allowlist needed for executable discovery and upstream profile selection. Non-secret bindings are explicit. Secret references resolve only immediately before process start and their values are unavailable to operation rendering, diagnostics, caches, markers, and canonical records. Where possible, use provider profile/environment authentication rather than argv.

Redaction is structural and occurs before data crosses the adapter boundary:

- URI userinfo is removed; credential-like query parameters are removed; remaining query keys and values are redacted; a canonical record receives a query-free sanitized URI.
- Authorization headers, cookies, secret environment values, and known credential arguments are replaced before errors or output are built.
- Capture and stream redactors are seeded with every explicit or structurally inferred sensitive argv value and every resolved secret environment value.
- Logs, traces, prompts/completions, stderr, and raw provider state are independently sensitive and bounded.
- Secret canaries must never occur in stdout, stderr, JSON, diagnostics, cache, markers, or canonical records.

Unsafe input is rejected when safe identity cannot be preserved; silently retaining a credential-bearing raw string is forbidden.

## Required provider guardrails

### Pueue

Pueue 4.x `status --json` task objects may contain captured `envs`. Remove the
entire `envs` member recursively before parsing returns or raw state crosses the
adapter boundary. Do not merely mask keys known today. Submissions use an
explicit non-secret, non-credential-sensitive environment allowlist because the daemon persists task
environments. Runtime `secret_env` is therefore rejected for Pueue; a workload
must obtain credentials through a broker or provider profile after it starts.

The implemented adapter provides sanitized status and exact-task cancel with
`--confirm` only when one canonical Attempt assigns scheduling to Pueue, its
reference uses the local runtime context, and the live task ID, group, and label
all match the configured route. Submission is limited to the audited private
`exp worker run` envelope. Arbitrary titles, provider text, or user shell
fragments cannot become the submitted command. Pool bindings select explicit
Pueue groups; stable labels support outbox recovery after submit ambiguity.

### Slurm

Never generate `--export=ALL`. Use an explicit allowlist or site-approved profile. Treat controller/accounting commands as remote reads even when invoked locally. Prefer verified JSON support; otherwise request named fixed fields with `--parsable2 --noheader --format`. Preserve cluster, array, and step identity. Missing or delayed accounting produces partial/unknown observations, not guessed terminal state.

### MLflow

Parse native JSON and stdout CSV before considering an explicit SDK/REST capability. Never enter an isolated Python environment implicitly. Structurally redact userinfo, query parameters, tokens, and doctor output from tracking and artifact URIs. Determine proxy versus direct artifact access before requesting storage credentials. Tracker-owned trials and telemetry remain in MLflow; canonical records keep only selected evidence and sanitized references.

The implemented integration is read-only: a workload creates and logs its own
run, then `exp provider mlflow verify --run-id ...` requests only named metrics
and expected tags. It does not create a run, log a metric, upload an artifact,
or mutate registry state. A verified run may be linked from an Evaluation by
sanitized identity only when the asserted `exp.attempt_id` tag identifies a
successful canonical Attempt whose Run belongs to that Experiment, Candidate's
Experiment, or the Release's combination/single-slot Experiment lineage. When
an Evaluation later creates a Candidate, that owner must equal the Candidate's
included successful backing Attempt. Verification is not itself a scientific
verdict.

### DVC, notebooks, and later systems

DVC capabilities are probed one operation at a time after a real binary/version is available. Marimo and Jupyter are Runners/entrypoints, not durable schedulers; inspection must not trigger sandbox/package resolution or notebook execution. W&B, Kaggle, Ray, Kubernetes, and cloud control planes require separate verified contracts and are not inferred from installed libraries or prose references.

## Daemon and operational state

`.exp/runtime.json` is the strict execution binding between canonical IDs and
provider-native configuration. It contains an `exp.runtime/v1` schema, Pool to
Pueue group/label bindings, and Plan to executable/argv/cwd/timeout/Git identity
bindings. Allowed environment arrays contain non-secret names only; Pueue
runtime secret arrays must be empty because task environments are persisted.
Label prefixes in one Pueue group are pairwise prefix-free and reserve enough
space for the complete scoped dispatch ID. The actual selected runtime config
path is excluded from every executable ChangeSet.

The daemon's SQLite database at
`<git-common-dir>/exp/runtime/v1/control.sqlite` owns leases, fencing tokens,
jobs, submission outbox, provider observations, fairness counters, pause state,
and a hash-chained event log with bounded payloads. It has no authority over hypotheses, conclusions,
Findings, Evaluations, Releases, or Promotions.

One tick snapshots Pueue before admission, reconciles known Attempts, recovers
only this canonical worktree's outbox entries by scoped stable label, accounts for recovered and unknown nonterminal
tasks in declared pool units, then fills remaining canonical capacity.
Only runtimes needed by queued Queue entries or active prepared Attempts undergo
Git verification; obsolete entries for terminal Plans do not block other work.
The private worker freezes the bounded result and publishes a durable terminal
marker before marking the job finished. Scheduler and marker observations can advance an Attempt's operational
state only through an explicit canonical transaction.
