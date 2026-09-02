# Configuration and Paths

`exp` separates user configuration, project-local execution bindings,
Git-backed research records, and private operational state. Keeping those
locations distinct prevents a local scheduler detail from becoming scientific
authority and prevents host paths or credentials from leaking into records.

## Path summary

| Purpose | Default location | Authority and lifetime |
|---|---|---|
| Canonical research records | `<git-root>/experiments/` | Git-backed scientific and decision authority. |
| Agent CLI profiles | `$XDG_CONFIG_HOME/exp/agents.toml` on XDG systems; resolved through the OS user config directory | User-managed mapping from roles to fresh agent CLI processes. Override with `--config PATH`. |
| Runtime contract | `<git-root>/.exp/runtime.json` | Strict project-local `exp.runtime/v1` binding from canonical Pool/Plan IDs to execution details. A command may select another safe repository-relative path with `--config PATH`. |
| Experiment worktrees | `$XDG_DATA_HOME/exp/worktrees/<project-namespace>/<short-id>-<slug>` | Local linked Git worktrees outside the source repository. If `XDG_DATA_HOME` is unset, the Unix-style fallback is `~/.local/share`. |
| Canonical coordination | `<git-common-dir>/exp/v1/` | Private locks, receipts, ID reservations, prepared transactions, and Attempt markers shared by linked worktrees. |
| Daemon database | `<git-common-dir>/exp/runtime/v1/control.sqlite` | Private SQLite coordination state. It is not canonical and is not Git-tracked. |

Host paths shown by local commands are operational details. Do not copy them
into canonical Markdown or public notes.

## Agent CLI profiles: `agents.toml`

Agent profiles map a role to a user-managed executable. Every request starts a
new process; `exp` does not keep a provider SDK session or hidden conversational
state.

```toml
schema = "exp.agents/v1"

[roles]
idea_planner = "research-agent"
queue_advisor = "research-agent"
queue_battle = "research-agent"
experiment_implementer = "research-agent"

[profiles.research-agent]
executable = "research-agent"
args = ["--prompt", "{prompt_file}", "--schema", "{schema_file}", "--output", "{output_file}"]
timeout = "10m"
max_output_bytes = 1048576
output = "output_file_json"
stdin_prompt = false
allowed_env = []
secret_env = []
reported_model = "configured-outside-exp"
```

Important constraints:

- `executable` is a binary name resolved from `PATH`, not a path or shell
  command;
- placeholders such as `{prompt_file}`, `{schema_file}`, `{schema_json}`,
  `{output_file}`, and `{cwd}` occupy a complete argument;
- output is exactly one JSON value that must match the supplied JSON Schema;
- `allowed_env` and `secret_env` contain variable names, never values;
- output and diagnostics are bounded and redacted before they are returned.

Use `exp agent profiles [--config PATH]` to validate and list profiles. Use
`exp agent run --role ROLE --schema PATH [--config PATH]` for a direct
schema-constrained invocation.

## Project runtime contract: `.exp/runtime.json`

The runtime file is strict JSON. Unknown fields, invalid canonical IDs,
unsafe paths, ambiguous Pueue label prefixes, or mismatched Git identities
cause validation to fail.

```json
{
  "schema_version": "exp.runtime/v1",
  "pools": {
    "pool_01a01e66-f8e0-7202-8000-000000000202": {
      "pueue_group": "gpu",
      "label_prefix": "exp-"
    }
  },
  "plans": {
    "plan_01a01e69-e340-7505-8000-000000000505": {
      "executable": "/opt/project/bin/train",
      "argv": ["--config", "configs/cosine.toml"],
      "checkout": "main",
      "cwd": ".",
      "timeout": "4h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "0000000000000000000000000000000000000000",
      "head_commit": "1111111111111111111111111111111111111111",
      "change_set": ["configs/cosine.toml", "src/train.go"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
```

Pool entries bind a canonical ResourcePool to a Pueue group and stable label
prefix. Plan entries bind one canonical Plan to an absolute executable, an
argument array, a repository-relative working directory, timeout, selected
environment-variable names, exact Git base/head commits, exact ChangeSet, and
expected output paths.

`checkout` defaults to `main`. Set it to `registered_worktree` to select the
unique registered linked worktree whose HEAD is `head_commit`; no worktree host
path is persisted in the runtime file. The current Pueue route persists task
environments, so runtime `secret_env` must remain empty. A workload that needs
credentials must obtain them after start through a separately reviewed broker
or provider profile.

The executable is intentionally an absolute path and may be host-specific.
Teams must decide deliberately whether `.exp/runtime.json` is portable and
tracked or is maintained per host. In either case it is configuration, not a
canonical research record, and its selected path cannot be included in the
Plan's ChangeSet.

## XDG-managed experiment worktrees

`exp experiment workspace prepare` creates a linked worktree under:

```text
<data-home>/exp/worktrees/<project-namespace>/<short-id>-<slug>
```

`<data-home>` is `XDG_DATA_HOME` when set, otherwise `~/.local/share` on the
usual Unix configuration. The project namespace combines a repository-derived
name with a digest of Git-common identity, so different clones do not silently
share a workspace. The path must be an absolute, non-symlink location outside
the source repository.

`exp` can prepare the branch and create one exact allowlisted commit. It never
merges that branch, removes the worktree, or changes the human-owned integration
branch.

## Git-common operational data

Every linked worktree in one clone shares the absolute Git common directory.
`exp` stores cross-worktree coordination below `<git-common-dir>/exp/`:

```text
<git-common-dir>/exp/
├── v1/
│   ├── lock
│   ├── project-receipt.json
│   ├── reservations/
│   ├── transactions/
│   └── attempts/
└── runtime/v1/control.sqlite
```

`control.sqlite` owns daemon leases, fencing tokens, jobs, the submission
outbox, provider observations, fairness counters, pause state, and bounded
event history. These facts coordinate execution. They cannot establish a
hypothesis, evidence disposition, Finding, Evaluation, Release, or Promotion.

Do not edit or clear this tree as a configuration reset. ID reservations and
prepared transactions have recovery semantics; use the relevant `exp` command
instead.

## Workload environment contract

The private worker starts the configured workload with a minimal environment,
the explicitly allowed names, and three non-secret variables:

| Variable | Meaning |
|---|---|
| `EXP_JOB_ID` | Private control-plane job identity. |
| `EXP_ATTEMPT_ID` | Canonical Attempt identity for this execution. |
| `EXP_RESULT_PATH` | Absolute private path where the workload may write one bounded valid JSON result. |

The workload must treat `EXP_RESULT_PATH` as the assigned output path rather
than choose or replace it. Expected repository outputs are declared separately
in `expected_outputs` and are verified and hashed after a successful process.
Neither a successful process nor a valid result JSON is a scientific verdict.

See [Runtime Dispatch](../workflows/runtime-dispatch.md),
[Agents and Workspaces](../workflows/agents-and-workspaces.md), and
[Architecture](../design/architecture.md) for the full behavioral boundary.
