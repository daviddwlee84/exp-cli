# Implementation roadmap

Each milestone must deliver working behavior; command names are not added as inert stubs.

## Milestone 1: contracts and walking skeleton

Freeze the durable record, provider, privacy, projection, and future transaction contracts. Implement one local write/read/render path with no required provider.

Functional commands:

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

Required behavior:

- discover only the fixed `<git-root>/experiments` root; ignore out-of-scope nested `PROJECT.md` files and defer named or multiple roots;
- initialize idempotently without replacing an unrelated or harness-v0 tree;
- create UUIDv7 Plans through strict TOML/Markdown validation, common-Git-directory locking, expected revisions, and atomic single-record publication;
- render and byte-check deterministic README, roadmap, ledger, and decision projections;
- read/validate/context locally without network or provider calls;
- perform only `LookPath` executable discovery in default `doctor`; do not execute third-party `--version`, leaving versions and capabilities unknown until a future explicit adapter probe;
- provide stable versioned JSON without prompts or stdout contamination;
- embed/install/check a version-matched guidance skill without executing external skill scripts.

This milestone does **not** implement Runs, Attempts, lifecycle transitions, harness migration, provider reads, or the generalized transaction journal merely because their contracts and fixtures exist.

## Milestone 2: local vertical lifecycle

Close the full provider-free loop:

```text
plan start
experiment list|show|conclude
run add|wrap|attach|status
a finding add/weaken/overturn path
decision record|list|show
search
context for a selected experiment
```

Required foundation before those compound commands ship:

- prepared multi-record journal and idempotent hash-checked recovery;
- design digest lock on first Attempt and explicit amendments;
- Run as evidence unit and committed redacted Attempt records;
- direct-process durable start/terminal markers;
- explicit included/excluded Run dispositions at conclusion;
- transactional Plan/Experiment/Finding/Decision transitions;
- local resume context and complete stable JSON requests/responses.

Exit criterion: a human and an agent can complete `plan -> experiment -> run -> attempt -> conclusion -> finding -> decision/action -> context` without an external provider, and process success never changes scientific state implicitly.

## Milestone 3: harness-v0 compatibility and migration

Add read-only compatibility before writes:

```text
exp migrate plan
exp migrate apply
```

Also allow local list/show/search/validate/context over harness-v0 input where meaning is available. Migration must use deterministic UUIDv5, preserve `#NNN`/`F-NNN`, bodies, exact source bytes and unknown spans, require a fingerprinted reviewed plan, and retain ambiguity as `needs_review`. Test against a sanitized real-world tree; never execute legacy scripts or infer repairs.

## Milestone 4: provider reads

Add adapters one capability at a time, with explicit refresh only:

1. **Pueue 4.x**: status/groups/bounded logs; structurally remove `envs` before any result crosses the adapter boundary.
2. **MLflow 3.9**: native JSON/stdout CSV reads first; URI redaction; optional explicit SDK/REST only where CLI coverage is absent.
3. **DVC**: local discovery and verified repository reads after a real binary/version is available; otherwise `unsupported` or `raw_only`.
4. **Slurm**: named site/version probes, verified JSON or fixed `--parsable2` fields, accounting fallback, preserved native reasons, and no `--export=ALL`.

Provider absence remains nonfatal. Default context/status remains local. Caches are disposable dated observations.

## Milestone 5: controlled writes and runners

Only after read parsing, effects, and secret policy are stable:

- Pueue submit/wait/cancel through one durable Attempt owner;
- Marimo and Jupyter preparation/execution through direct process or a selected Scheduler;
- DVC queue/artifact operations capability-by-capability;
- Slurm submit/cancel after testing a real site;
- narrowly verified MLflow artifact/registry reads and, later, explicit writes.

Every mutation first exposes a reviewable argument-array plan and effect set. No adapter installs packages, starts services, authenticates, or downloads implicitly.

## Explicitly deferred

The roadmap does not currently promise:

- a TUI or mandatory FTS index;
- dynamic Go plugins;
- sweep or DAG orchestration;
- per-trial registration for tracker-owned sweeps;
- standardized metrics or `exp compare`;
- artifact transfer, garbage collection, or retention management;
- registry mutation in early provider milestones;
- W&B, Kaggle, Ray, Kubernetes, Azure ML, Databricks, Modal, RunPod, or general cloud adapters;
- control of consumer Colab browser sessions;
- multiple experiments roots in one Git repository;
- cross-repository knowledge graphs;
- automatic TODO/backlog/pitfall/invariant mutation;
- automatic scientific interpretation or generated verdicts.

A deferred item enters the roadmap only with a verified upstream control surface, explicit authority and effects, secret-safe fixtures, and a vertical user outcome that cannot be achieved cleanly with the upstream CLI itself.
