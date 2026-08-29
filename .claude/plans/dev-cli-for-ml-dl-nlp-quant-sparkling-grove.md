# Context

`exp-cli` is a greenfield CLI for ML/DL/NLP/quant research. Its purpose is to make the full research thread reviewable and resumable—plan, pre-registration, evidence, operational attempts, conclusion, findings, and decisions—while delegating execution, queueing, telemetry, artifacts, registries, authentication, and notebook runtimes to the upstream tools that already own them.

The design is grounded in two existing systems:

- `../dev-cli` supplies proven Go/Cobra patterns for thin commands, an injected `App`, external-binary adapters, capability-aware `doctor`, stable JSON, durable Markdown, optimistic revisions, locks/atomic writes, and a binary-owned embedded skill.
- `../agent-skills/skills/local/experiment-knowledge-harness` supplies the research methodology and legacy file contract, but its scripts are an executable specification rather than a production backend: runs are not first-class, YAML parsing is narrow, relationships can drift, IDs collide across branches, and compound writes are not transactional.

The intended outcome of this milestone is an RFC-backed walking skeleton, not broad provider support: freeze the durable contracts, prove one human/agent-safe write/read/render path, and leave a narrow seam for later Pueue, MLflow, DVC, Slurm, Marimo/Jupyter, W&B, and Kaggle adapters.

## Agreed foundation

- Product thesis: **a Git-native research control plane, not another tracker or scheduler**.
- Canonical IDs: typed UUIDv7 (`plan_…`, `exp_…`, `run_…`, `att_…`, `fnd_…`, `dec_…`) with collision-checked short display codes; legacy `#016`/`F-039` remain aliases only.
- Persistence: every explicitly registered, redacted attempt is committed; individual trials in a large tracker-owned sweep are not automatically registered.
- Scope: one Git repository has one v1 research root, default `<repo>/experiments`.
- Canonical records are one Markdown file per entity; `README.md`, `ROADMAP.md`, `LEDGER.md`, and `DECISIONS.md` are deterministic, rebuildable projections.
- External live state remains authoritative in its provider. Cached observations always carry source, `observed_at`, partial/stale status, and are disposable.
- Scientific state and operational state stay separate: process success never implies a supported hypothesis; invalid evidence never becomes a negative scientific result.

# Recommended architecture

## Authority and domain model

Define these canonical entities and single-owner relationships:

- `Plan`: priority, effort, expected payoff with units, assumptions, state, optional resulting experiment.
- `Experiment`: question/hypothesis, factors, baseline, comparability spec, success criteria, decision rule, lifecycle, amendments, conclusion, scientific verdict.
- `Run`: an intended evidence unit or batch, not a process retry; owns its experiment reference.
- `Attempt`: one operational execution/submission; owns its run reference, redacted invocation/provenance, and external references.
- `Finding`: a durable belief; owns evidence and `weakens`/`overturns` edges.
- `Decision`: an action-bearing interpretation; owns `based_on`, action, and supersession edges.
- `ExternalRef`: provider/context/native-kind/native-ID/sanitized URI, with optional namespaced metadata.
- `ResumeContext`: a derived view joining the above; never canonical.

Keep project-harness TODO/backlog/pitfalls authoritative in that harness. `exp` may discover, link, and search them but must not create a parallel store.

Use separate state axes:

- experiment lifecycle: `planned`, `active`, `closed`; closure: `concluded`, `abandoned`, `superseded`;
- scientific verdict for concluded work: `supported`, `refuted`, `inconclusive`, `invalid`;
- attempt state: `planned`, `queued`, `blocked`, `starting`, `running`, `succeeded`, `failed`, `cancelled`, `timed_out`, `preempted`, `out_of_memory`, `unknown`;
- evidence disposition at conclusion: every cited run is explicitly `included` or `excluded` with a reason.

The first attempt locks a normalized design digest. Later protocol changes require dated amendments; success criteria are never silently rewritten after evidence exists.

## On-disk v1

```text
experiments/
├── PROJECT.md                         # canonical root/schema record
├── README.md                          # generated overview/graph
├── ROADMAP.md                         # generated plan view
├── LEDGER.md                          # generated finding view
├── DECISIONS.md                       # generated decision view
├── plans/plan_<uuidv7>-<slug>.md
├── findings/fnd_<uuidv7>-<slug>.md
├── decisions/dec_<uuidv7>-<slug>.md
└── e-<short-id>-<slug>/
    ├── REPORT.md
    ├── runs/run_<uuidv7>-<slug>.md
    └── attempts/att_<uuidv7>.md
```

Use strict, schema-versioned TOML front matter plus ordinary Markdown bodies. Reject unknown fields for a known schema except namespaced extension data. Commit only repo-relative POSIX paths and sanitized references. Compute optimistic revisions from normalized content rather than storing a self-referential revision.

Put local coordination under the Git common directory so linked worktrees share it:

```text
<git-common-dir>/exp/v1/{lock,transactions/,attempts/}
```

Put only disposable search/provider caches under `$XDG_CACHE_HOME/exp/<project-id>/`; provider contexts and binary overrides live in `$XDG_CONFIG_HOME/exp/config.toml`, containing credential references/profile names rather than secret values.

## Provider boundary

Define a compiled registry of small role-specific capabilities rather than a universal “experiment provider” or dynamic Go plugin ABI:

- `Runner`: direct process first; Marimo/Jupyter/DVC/MLflow Project later.
- `Scheduler`: direct process first; Pueue, Slurm, and DVC Queue later.
- `Tracker`: MLflow first, W&B later; initially read-only.
- `ArtifactStore`: DVC/MLflow/object references; no implicit download.
- `Registry`: read/resolve only after a concrete provider API is verified.

Every adapter must provide binary discovery/override, local version and capability probing, a reviewable argument-array operation plan, declared effects, sanitized parsing, and graceful unavailable/unsupported/unknown results. Effects are versioned values: `local_read`, `remote_read`, `local_write`, `remote_write`, `executes_user_code`, `starts_service`, `credential_flow`, `destructive`, `sensitive_output`, and `blocking`.

All execution goes through one injected, signal-aware `execx.Invoker`; adapters never invoke a shell by string concatenation. `--plan`/`--dry-run` performs no remote contact, auth flow, package resolution, daemon start, file creation, or user-code execution. Parse native JSON first, then explicit CSV/fixed delimiters, then an explicitly configured SDK/REST adapter, otherwise expose `raw_only`; never scrape pretty tables.

Provider-specific guardrails are contract requirements even before adapters ship: remove Pueue `envs` before data crosses the adapter boundary, do not use Slurm `--export=ALL`, sanitize MLflow URI credentials/query parameters, bound logs/raw state, and fail closed on unknown terminal states.

# Milestone scope: RFC + walking skeleton

## 1. Bootstrap the Go CLI and output contract

Create:

- `go.mod`, `cmd/exp/main.go`
- `internal/cli/root.go`, `internal/cli/app.go`, `internal/cli/output.go`
- baseline `Makefile`, `.gitignore`, `LICENSE`, `README.md`, and CI workflow

Use module path `github.com/daviddwlee84/exp-cli`, Go/Cobra, and MIT for new code. Adapt—not import—patterns from:

- `../dev-cli/internal/cli/root.go`: `NewRootCommandWithIO`, centralized `Execute`, thin handlers, injected streams;
- `../dev-cli/internal/cli/app.go`: one composition root, while replacing `ctxOf() == context.Background()` with signal-aware cancellation;
- `../dev-cli/scripts/e2e.sh` and `.github/workflows/ci.yml`: isolated HOME/XDG E2E and `go test -race` matrix.

Define one versioned JSON envelope for every machine command: schema version, command, success/partial flags, observation time, data, and diagnostics. Human output and JSON rendering remain separate. Machine mode never prompts or emits warnings into stdout.

## 2. Freeze the RFCs and golden records before broad implementation

Create focused design documents under `docs/design/`:

- `architecture.md`: product thesis, authority table, non-goals, lifecycle, milestone roadmap;
- `record-format.md`: entities, TOML front matter, IDs/aliases, revisions, relationships, extensions, path/privacy rules;
- `provider-contract.md`: role interfaces, capability probing, effect vocabulary, refresh semantics, JSON observations, redaction/environment policy;
- `transactions.md`: Git-common-dir lock, candidate validation, prepared journal, hash-based roll-forward recovery, projections-last rule;
- `harness-v0-migration.md`: lossless mapping from REPORT/ROADMAP/LEDGER/INBOX, deterministic UUIDv5 IDs for migrated records, legacy aliases, fingerprinted plan/apply, and `needs_review` conflicts;
- `roadmap.md`: subsequent vertical MVP and provider order.

Add `testdata/v1/valid-project/` with one representative file of every canonical record kind and deterministic expected projections. Add malformed fixtures for unknown fields, bad references, lifecycle/verdict confusion, unsafe paths/URIs, and invalid attempt evidence. Keep the existing agent-skills files as reference only; do not execute their scripts or copy unlicensed prose/templates verbatim. Retain required attribution for any deliberately adapted `dev-cli` MIT code.

## 3. Implement the typed record/read model and durable single-record store

Create representative packages:

- `internal/research/`: typed IDs/references and Plan, Experiment, Run, Attempt, Finding, Decision models plus invariants;
- `internal/record/frontmatter.go`, `layout.go`, `revision.go`, `store.go`;
- `internal/project/discover.go`, `internal/gitx/`, `internal/pathx/`, `internal/lockx/`.

Adapt the verified mechanics from:

- `../dev-cli/internal/note/store.go`: strict TOML front matter, expected revisions, temp-file + fsync + rename, per-record diagnostics;
- `../dev-cli/internal/note/note.go`: content revisions;
- `../dev-cli/internal/lockx/lockx.go`: cross-platform advisory lock;
- `../dev-cli/internal/experiment/service.go`: durable facts separated from live observations and narrow injected test seams;
- `../dev-cli/internal/experiment/transitions.go`: plan/revalidate/intent/reconcile principles for the later transaction journal.

For this milestone, implement atomic single-record writes, project-wide common-dir locking, unique UUIDv7 creation, strict path containment, and deterministic inventory. Canonical committed records use `0644`; local state/caches use `0600`. Specify but defer the generalized multi-record transaction engine until compound lifecycle transitions are implemented.

## 4. Deliver one narrow human/agent walking path

Implement only these functional commands:

```text
exp init
exp doctor [--json] [--live]
exp plan add [flags | --input -] [--json]
exp plan list [--json]
exp validate [--json]
exp render [--check]
exp context [--json]
exp skill print|install|check
```

Behavior:

1. `exp init` requires/discovers Git, creates an idempotent v1 root and empty deterministic projections, and never overwrites an unrelated/legacy tree.
2. `exp plan add` proves the canonical write path: typed UUIDv7, strict Plan validation, common-dir lock, atomic write, revision in output, then projection refresh. It accepts human flags and a versioned JSON request on stdin; machine mode does not prompt.
3. `plan list`, `validate`, and `context` prove local parsing and stable human/agent projections with no provider/network calls.
4. `render` rebuilds README/ROADMAP/LEDGER/DECISIONS deterministically; `--check` detects drift without writing.
5. `doctor` performs local-only binary/version discovery by default and reports optional capabilities without requiring any provider. `--live` is reserved for explicit daemon/remote probes; no such probe is needed in this milestone.
6. `context` emits the local research summary and diagnostics. It establishes the future resume contract without pretending live provider state was refreshed.

Do not add nonfunctional command stubs. Reserve the remaining command tree in the RFC until its behavior is implemented.

## 5. Establish provider and execution seams without shipping provider behavior

Create:

- `internal/provider/provider.go`, `registry.go`, `effect.go`, `result.go`, `reference.go`, `redaction.go`
- `internal/execx/invoker.go`, `command.go`, `environment.go`

Implement descriptors and local discovery for Git/direct plus optional binaries (`pueue`, `mlflow`, `dvc`, Slurm clients, `marimo`, `jupyter`) so `doctor` can explain available/missing/unknown capabilities. Do not query daemons/remotes, install tools, import isolated Python packages, or expose submit/cancel/telemetry commands yet. Include structural redaction/canary tests now so later adapters cannot bypass the boundary.

Ground the later order in verified capabilities:

1. Pueue 4.x read adapter, immediately stripping `envs` and preserving sanitized native state.
2. MLflow 3.9 read adapter using native JSON/stdout CSV; explicit SDK/REST only where CLI coverage is absent.
3. DVC local read/probe after a real binary/version is available.
4. Slurm read adapter after testing a named site/version, with accounting fallback and fixed fields.
5. Controlled writes/runners only after read contracts and secret policy are stable.

W&B, Kaggle, Ray/Kubernetes/cloud platforms, and a named Colab Enterprise/Vertex control plane remain later additions. Consumer Colab browser sessions are explicitly unsupported rather than scraped.

## 6. Embed the version-matched skill

Create `internal/skill/skill.go` and `internal/skill/exp-cli/{SKILL.md,references/commands.md}`.

Adapt the binary-owned pattern from `../dev-cli/internal/skill/skill.go` and command-reference generation from `../dev-cli/internal/cli/skill.go`, adding lock + atomic install + manifest hash checks. Install to `~/.agents/skills/exp-cli` and link only into existing tool-specific locations without clobbering real directories.

The binary owns schemas, command syntax, mutations, templates, and generated references. The skill owns research judgment: expected-payoff questioning, pre-registration/anti-HARKing, comparability, negative evidence, operational failure vs scientific evidence, and routing among experiment records, TODO/backlog, pitfalls, and invariants. External DVC/MLflow/Pueue/Slurm/Marimo skills remain independently managed guidance; the built-in skill links to them conceptually, while runtime adapters call binaries directly and never execute those skills’ helper scripts.

# Explicitly deferred after this milestone

1. Full local vertical lifecycle: `plan start`, experiment design lock/amendment, runs, committed attempts, direct `run wrap`, durable terminal markers, transactional conclude, findings, decisions, and resume packet.
2. Generalized multi-record transaction journal and crash recovery, required before compound mutations.
3. Read-only `harness-v0` compatibility and fingerprinted migration plan/apply, tested against a sanitized OfflineAnalysis fixture without inferring repairs.
4. Pueue and MLflow read adapters, followed by DVC/Slurm.
5. Provider submissions/cancellation, Marimo/Jupyter runners, and artifact/registry operations.
6. TUI, FTS, dynamic plugins, sweep/DAG orchestration, standardized metric comparison, multiple roots, cross-repo knowledge graphs, and automatic TODO/pitfall mutation.

The next vertical MVP must close this loop before broad integrations: `plan → pre-registered experiment → run → attempt/marker → conclusion → finding → decision/action → context`.

# Verification

Run automated checks:

- `gofmt`/format check, `go vet ./...`, `go test -race ./...`, and `go build ./cmd/exp` on Linux/macOS.
- Unit/property tests for schema versions, Unicode TOML round-trips, UUIDv7/short-prefix resolution, unknown fields, relationship ownership, state separation, path containment, unsafe URI/argument/environment redaction, expected-revision conflicts, and deterministic ordering.
- Atomic-store failure injection around temp write, fsync, rename, and directory fsync.
- Fake-invoker tests proving `doctor` degrades on missing tools, default commands make no network/daemon calls, argument arrays are preserved, output is bounded, and secret canaries never appear in stdout/stderr/JSON/cache/records.
- Generated command/skill reference and projection drift checks in CI.

Run an isolated end-to-end test with temporary Git repository, HOME, and XDG directories:

1. `exp init`; rerun to prove idempotence and verify an existing unrelated/legacy root is refused.
2. Add a plan through human flags and another through stdin JSON; parse returned UUID/revision and verify no prompt/no stdout contamination.
3. List and render in human/JSON modes; manually stale a projection, verify `render --check` fails, render again, and verify deterministic clean output.
4. Run `validate` and `context --json`; delete all caches and confirm identical canonical results.
5. Create linked worktrees and concurrently add records; verify unique IDs, shared locking, and valid projections.
6. Run `doctor` with fake present/missing binaries and confirm optional tools never block core commands.
7. Install/check the embedded skill under the isolated HOME; verify unchanged reinstall is a no-op and real directories are never replaced.

Acceptance requires that the resulting tree remains understandable and valid using ordinary Markdown/Git, an agent can complete the walking path using only versioned JSON contracts, no provider is required, no cache is authoritative, and no secret can cross the persistence/output boundary.
