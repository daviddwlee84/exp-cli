# Architecture

## Product boundary

`exp` is a Git-native research control plane. It makes the path from a priced idea to a reviewable decision durable and resumable while delegating execution, queues, telemetry, artifacts, registries, authentication, and notebook runtimes to their existing owners.

The closed loop is:

```text
plan -> pre-registered experiment -> run -> attempt -> conclusion
     -> finding -> decision/action -> resume context
```

Process success is an operational fact, never a scientific verdict. Invalid evidence is not a refuted hypothesis.

## Authority

| Information | Authority | `exp` treatment |
|---|---|---|
| Plan, protocol, success criteria, decision rule | canonical Markdown records | Read and mutate under revision checks |
| Scientific conclusion and verdict | Experiment `REPORT.md` | Canonical |
| Intended evidence unit | Run record | Canonical |
| Registered execution and redacted provenance | Attempt record | Canonical; every explicit attempt is committed |
| Belief and belief-changing relation | Finding record | Canonical |
| Decision and intended action | Decision record | Canonical |
| Live process or queue state | process or scheduler | Explicitly refresh; cache only a dated observation |
| Metrics, traces, sweep trials | tracker | Store a sanitized reference, not a copy |
| Artifact and model bytes | artifact store or registry | Store a sanitized reference and digest when known |
| `README.md`, `ROADMAP.md`, `LEDGER.md`, `DECISIONS.md` | canonical records | Deterministic committed projections |
| Interrupted first-initialization identity | private Git-common `project-receipt.json`, only until a canonical `PROJECT.md` exists | Recover initialization; reconcile from canonical `PROJECT.md` thereafter |
| Used canonical record IDs | private Git-common `reservations/<typed-id>` | Prevent reuse across linked worktrees; never treat as a record |
| Search/provider cache | canonical records or provider | Disposable XDG cache |
| TODO, backlog, pitfall, invariant | project-knowledge harness | Discover and link; do not duplicate or mutate automatically |

A tracker-owned sweep remains one Run evidence unit or selected runs of record. Its individual trials are not automatically registered as Attempts.

## Canonical entities

- **Plan** prices proposed research with priority, effort, measurable payoff, assumptions, state, and an optional resulting Experiment.
- **Experiment** records the question, hypothesis, factors, baseline, comparability specification, success criteria, decision rule, amendments, conclusion, and scientific state.
- **Run** is an intended evidence unit or batch. It is not a retry or process invocation.
- **Attempt** is one execution or submission of a Run, including redacted invocation, provenance, external references, and operational state.
- **Finding** is a durable belief with evidence and optional `weakens` or `overturns` relations.
- **Decision** is an action-bearing interpretation with `based_on`, action, and optional `supersedes` relations.
- **ExternalRef** names provider-owned state by role, provider, context, native kind, native ID, and optional sanitized URI.
- **ResumeContext** is a derived join of records and dated provider observations. It is never canonical.

Each relationship has one owner. Reverse edges and all consolidated views are derived; see [record-format.md](record-format.md).

## Independent state axes

### Experiment state

```text
lifecycle: planned | active | closed
closure (closed only): concluded | abandoned | superseded
verdict (concluded only): supported | refuted | inconclusive | invalid
```

`invalid` means the available evidence cannot answer the question. `abandoned` and `superseded` close work without a scientific verdict.

The first registered Attempt requires a normalized design digest and locks the protocol. Later changes are dated amendments with a reason and a new digest; success criteria are never silently replaced after evidence exists.

At conclusion, every cited Run has one evidence disposition:

```text
included | excluded
```

An excluded Run requires a reason. Evidence disposition belongs to the Experiment conclusion, not to Run or Attempt.

### Attempt state

```text
planned | queued | blocked | starting | running | succeeded | failed
cancelled | timed_out | preempted | out_of_memory | unknown
```

Native state and reason remain separate. A missing durable terminal marker means `unknown`, not succeeded, failed, or safe to retry.

## Repository and storage boundary

Version 1 discovers only the fixed `<git-root>/experiments` root. It does not search the repository for `PROJECT.md`; a marker elsewhere, including nested `testdata/vendor/archive/PROJECT.md`, is an out-of-scope file rather than another active root. Named or multiple roots and cross-repository graphs are deferred.

```text
experiments/
├── PROJECT.md
├── README.md
├── ROADMAP.md
├── LEDGER.md
├── DECISIONS.md
├── plans/plan_<uuid>-<slug>.md
├── findings/fnd_<uuid>-<slug>.md
├── decisions/dec_<uuid>-<slug>.md
└── e-<short-id>-<slug>/
    ├── REPORT.md
    ├── runs/run_<uuid>-<slug>.md
    └── attempts/att_<uuid>.md
```

Paths aid navigation; IDs in front matter are identity. Canonical records are UTF-8 Markdown with strict TOML front matter and mode `0644`.

Coordination is shared by linked worktrees through the Git common directory:

```text
<git-common-dir>/exp/v1/
├── lock
├── project-receipt.json
├── reservations/
│   └── <typed-id>
├── transactions/
└── attempts/
```

The coordination root and its subdirectories are private mode `0700`; coordination files are `0600`. None of this state is Git-tracked, part of the canonical record inventory, or a substitute for a committed Markdown record in the fixed `<git-root>/experiments` root.

`project-receipt.json` stores the exact encoded `PROJECT.md` bytes and their hash so an interrupted first initialization reuses one project identity across linked worktrees. It is authoritative only while no linked worktree has a canonical `PROJECT.md`. Once a canonical Project exists, matching Project identity across linked worktrees is required, canonical bytes win, and a missing, stale, or safely replaceable corrupt receipt is rebuilt from them; conflicting canonical identities block initialization.

Each `reservations/<typed-id>` file contains that typed ID plus a newline. A reservation is a durable allocation tombstone: writers seed missing reservations from valid same-project inventories in every present linked worktree, then reserve a new ID before canonical publication. A reservation left by an interrupted or failed create intentionally burns the ID, and deleting a canonical record does not release it. Present canonical records can rebuild missing reservations, but reservations for records absent from every current linked-worktree inventory cannot be reconstructed by that scan and therefore must not be discarded. A reservation without a record conveys no scientific or lifecycle state.

Ordinary creation and validation accept only lower-case typed UUIDv7 IDs. The reservation namespace does not authorize UUIDv5; deterministic UUIDv5 remains reserved for the future fingerprinted, provenance-checked migration path described in [harness-v0-migration.md](harness-v0-migration.md).

Search and provider caches live only under `$XDG_CACHE_HOME/exp/<project-id>/` and may be deleted without loss. Provider contexts and binary overrides live in `$XDG_CONFIG_HOME/exp/config.toml`; configuration stores credential references or profile names, not credential values.

## Provider boundary

A compiled registry declares small role-specific adapter contracts: Runner, Scheduler, Tracker, ArtifactStore, and Registry. The walking skeleton performs executable-presence discovery only; adapter operations and version probes are deferred. Capabilities are `supported`, `unsupported`, or `unknown`; optional provider absence never blocks the local research core. All external programs run through one signal-aware argument-array invoker. Remote access, authentication, package resolution, installation, daemon startup, and user-code execution are never implicit. See [provider-contract.md](provider-contract.md).

## Current milestone

The first milestone is an RFC-backed walking skeleton. It implements receipt-backed recovery of first initialization, linked-worktree ID reservations, and atomic no-clobber single-record creation under the common-directory lock. Canonical replacement uses compare-and-swap only where the platform supplies the required safe atomic-exchange primitive and otherwise fails closed; generated projections use a separate rebuildable replacement path. The generalized prepared multi-record journal is specified in [transactions.md](transactions.md) for the next lifecycle milestone and must not be presented as current behavior.

The functional command surface is limited to:

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

Default `doctor` performs only `LookPath` executable discovery and never executes third-party `--version`; versions and capabilities remain `unknown` until a future explicit adapter probe. `--live` currently performs no additional contact, and `context` does not imply provider refresh. No nonfunctional command stubs are added.

## Non-goals

Version 1 does not:

- replace an executor, scheduler, tracker, artifact store, registry, or notebook runtime;
- infer scientific meaning from exit status or provider state;
- persist raw environments, unbounded logs, per-epoch telemetry, or artifact bytes;
- provide implicit install, auth, network, daemon, or package-manager behavior;
- scrape pretty terminal tables;
- orchestrate sweeps or DAGs;
- claim control of consumer Google Colab browser sessions;
- maintain a second TODO/backlog/pitfall store;
- provide a TUI, dynamic Go plugin ABI, FTS requirement, multiple roots, or a cross-repository graph.

The milestone sequence and explicit deferrals are in [roadmap.md](roadmap.md).
