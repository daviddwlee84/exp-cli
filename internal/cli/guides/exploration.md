# Explore, save, and retrieve

Use `try start --title TITLE --goal GOAL` for continuing analysis. If there is
no codebase, add `--scratch` to create a small Git Source under `exp try root`.
Keep the returned Project and Try IDs. For a scratch Try, `source_path` is its
dedicated authoring repository. An existing Source still names its registered
checkout; use the Experiment workspace/agent flow for isolated codebase edits.

```text
storage show -> reuse or select a remembered profile
input bind NAME PATH -> private external data binding
try start -> try exec TRY -> results list -> try summarize/finish
               |                                  |
               +-> independent Attempt outputs     +-> history search --all
```

`try root PATH` remembers where scratch collections live. `storage add/use`
remembers local-directory, local MLflow/SQLite, or remote MLflow preferences.
`storage show --global` works without a Project. Local SQLite tracking needs the
full MLflow package in the configured Python; exp does not install dependencies.
Tracking metadata and artifact bytes are separate.

Run `try exec TRY --input NAME -- COMMAND ARG...`. Workloads read
`EXP_INPUT_NAME` and write to `EXP_OUTPUT_DIR`. Inputs are checked before/after
execution; each Attempt has its own output directory and binary identity.
Use `--dirty=capture` for bounded temporary code, or commit it before running.
New commands or parameters use another exec step; retry preserves identity.

Save a short description with `try summarize TRY --summary TEXT`. An authorized
agent can finish with `try finish TRY --author agent --summary TEXT`; this
records an unreviewed observation and selects saved manifests automatically.
Human adoption and formal Promotion retain their existing gates.

Use `history search WORDS --all`, optionally narrowing by `--source-key`,
`--version`, `--after`, `--before`, `--kind`, or `--state`. `results list` is local
and provider-free. `results compare` shows artifact/binary identities.
`results fetch/open ATTEMPT NAME` verifies the selected bytes. If multiple
Attempts produced a name, select the exact Attempt rather than guessing.

Failed or large transfers remain pending with local bytes retained. Review the
storage selection and size, then `results save ATTEMPT [--allow-large]`; never
rerun an uncertain workload to repair storage. Worktree cleanup retains artifacts.
Direct Tries do not reserve GPU/Pool capacity; use the formal Queue when needed.

`runner add/use` remembers a project CLI and optional `version --json` argv.
Version output supplies `version` and optional full lowercase `build_commit`.
Source commit and actual binary identity are recorded separately; `unknown`
agreement does not imply reproducibility. Temporary scripts remain supported.
