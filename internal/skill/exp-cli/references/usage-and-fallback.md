# Human, agent, and read-only use

## Human orientation

For a new workspace, follow the same terminal sequence the docs and skill use:

```text
exp guide setup
exp init
exp guide workspaces
exp workspace status
exp source status
exp guide config
exp config show
exp config explain
```

Bare `exp init` in a TTY defaults to a reviewed dedicated private experiment
repository plus one initial Source. Complete explicit flags are required in
non-interactive/JSON mode. `source add` can publish/associate another repository
or immutable subdir; `source register` repairs only a host-local clone
association.

Choose the next flow deliberately:

| Need | Start | Continue |
|---|---|---|
| quick exploratory answer | `exp guide quick`, `exp try run` | status → finish/abandon → optional adopt |
| rigorous comparable evidence | `exp guide research`, `exp idea add` | qualify → Queue → runtime v2/daemon → Evaluation v2/Candidate v2 |
| production decision | `exp guide promotion`, `exp candidate create` | Release → sealed fresh holdout → named-human Promotion |

Use explicit flags, inspect proposed meaning, and review ordinary Markdown/TOML
committed to Git. Run `exp validate` before treating records as sound and
`exp render --check` when verifying generated project views.

## Doctor, completion, and UI

Default `exp doctor` performs local executable lookup only. `exp doctor --live`
explicitly performs bounded version and read-only local-service probes; it does
not log in, install, write config, start a service, or execute a workload.
Provider contact remains operation-specific: Pueue status/cancel and daemon
tick/run, MLflow verify/observation, and explicit readiness refresh.

Missing MLflow, Pueue, or `dev` does not hide commands and does not block a
native Try. Pueue is needed only for formal daemon dispatch; MLflow is optional
observation. `dev_cli` lifecycle capabilities remain unsupported until exact
schema-versioned path receipts exist; native Git remains the byte/inspection/
cleanup authority.

`exp completion bash|fish|powershell|zsh` emits standard shell completion.
Completion reads local canonical records, associations, and config only; it does
not probe providers or resolve environment values. `exp ui` is read-only on real
terminal stdin/stdout. Startup is local-only; explicit readiness refresh runs
bounded probes. No UI action mutates, trusts, executes, installs/logs in, starts
a service, opens an editor, or hands off a workspace. Use `context --json` for
machine-readable UI-equivalent summary.

## Agent use

Prefer machine contracts over terminal prose:

- add `--json` only to commands that advertise it;
- parse the complete `exp.cli/v1` envelope and check schema version, `ok`,
  `partial`, data, and diagnostics;
- keep stdout JSON-only and stderr separate;
- carry complete typed IDs and revisions returned by commands instead of
  extracting display codes;
- resolve Project/Source first; never let config, current-directory convenience,
  a provider, or an agent response choose canonical authority;
- inspect `config explain` and require exact-digest capability trust before any
  repository-selected execution-bearing value is used;
- treat `queue insert --agent` human-review output as a successful audit with no
  Queue mutation, not permission to pick one battle response;
- use `daemon frontier` before enabling dispatch and never change autonomy
  without explicit authorization;
- run `validate` after supported mutation and never reconstruct relationships
  from generated projections.

Command help and [commands.md](commands.md) are authoritative for syntax in this
build. If metadata and recollection disagree, stop and use the metadata.

## Agent profile example

Agent profiles remain separate user-managed `$XDG_CONFIG_HOME/exp/agents.toml`.
Configure a fresh CLI process, not a provider SDK session. Placeholders occupy a
whole argument. `research-agent` below is a user-supplied wrapper example.

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
```

Validate with `exp agent profiles`. Executable names resolve through `PATH`;
secret environment entries are names resolved only at process start.

## Layered MLflow profile example

A formal Pueue profile must be value-free because Pueue persists task
environments. Repository-defined selector/profile bytes require exact
`mlflow.profile` trust.

```toml
schema = "exp.config/v1"

[defaults]
mlflow_profile = "formal-observer"

[mlflow.profiles.formal-observer]
context = "research"
binary = "mlflow"
timeout = "30s"
default_metrics = ["macro_f1", "validation_loss"]
```

Inspect with `config show`/`config explain`, then use the `config trust` TTY
review or complete `--path`, `--digest`, `--capability mlflow.profile`, and
`--confirm` flags. Direct Try may use trusted environment-name bindings. Formal
runtime v2 rejects all profile environment bindings; the workload must obtain
credentials through its own broker.

The workload owns MLflow run creation/logging. exp reads selected values and
ownership only. Missing optional observation does not reverse process success;
a strict explicit verification or Evaluation attachment must satisfy all named
assertions.

## Runtime v2 contract example

`.exp/runtime.json` is strict project-local JSON, separate from layered config.
Replace each quoted identifier, full commit, absolute executable, and path with
reviewed values. The example intentionally contains no secret.

```json
{
  "schema_version": "exp.runtime/v2",
  "pools": {
    "pool_replace_with_full_id": {
      "pueue_group": "gpu",
      "label_prefix": "exp-gpu-"
    }
  },
  "plans": {
    "plan_replace_with_full_id": {
      "execution_source": "src_replace_with_full_id",
      "read_only_sources": [],
      "executable": "/absolute/path/to/project-runner",
      "argv": ["--config", "configs/cosine.toml"],
      "checkout": "managed_worktree",
      "cwd": ".",
      "timeout": "3h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "0000000000000000000000000000000000000000",
      "head_commit": "1111111111111111111111111111111111111111",
      "change_set": ["configs/cosine.toml"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
```

V2 names one writable execution Source and optional read-only Sources. Every
binding selects `main`, `registered_worktree`, or `managed_worktree`, pins full
base/head object IDs and an exact ChangeSet, and must capture clean state. A
no-change observation needs equal commits, an empty `change_set`, and explicit
`observational_no_change: true`.

Review `.exp/runtime.json`, then approve its raw exact digest for
`runtime.dispatch` with `config trust`. Use `daemon frontier` for provider-free
validation before an authorized `daemon tick`/`run`. Runtime v1 remains the
closed embedded-repository compatibility path and cannot describe external
Sources.

## Try and formal evidence boundaries

Direct Try publishes Try/Attempt intent before creating the private managed
native worktree/job. Dirty state is accepted only through bounded
`--dirty=capture`; raw seed bytes remain in private XDG cache while canonical
records contain summaries/paths/digests. Finish/abandon only after owned Attempts
are terminal. A concluded Try can be adopted into Idea v2 but never directly
supports Candidate creation.

Formal runtime v2 admits only clean exact SourceSnapshots. Candidate v2 requires
a supported Experiment conclusion including the Run, successful clean formal
Attempt v3, passing scientific Evaluation v2 bound to that same Attempt, and
exact snapshot equality. Process/Pueue/MLflow success alone is never enough.

## Storage boundary

Large datasets, model/checkpoint bytes, artifact files, traces, and unbounded
logs stay in MLflow, DVC, or object storage. Canonical Git records receive only
bounded summaries, selected metric values, cryptographic digests, exact Source/
commit identities, and sanitized refs. Never put credentials, raw environments,
host paths, artifact bytes, or unbounded provider output in Git.

## Manual read-only fallback

When `exp` is unavailable, a repository must remain understandable with ordinary
read-only file tools and Git:

1. Locate the exact Project marker at `<git-root>/experiments/PROJECT.md`; a
   dedicated Project may be separate from every Source repository.
2. Read strict TOML front matter and Markdown bodies without editing. Full typed
   IDs are identity; paths/display codes are navigation aids.
3. Resolve canonical Source records separately from host-local associations.
   Absence of the private association store does not alter canonical identity.
4. Read canonical records rather than rebuilding facts from generated
   `README.md`, `ROADMAP.md`, `LEDGER.md`, `DECISIONS.md`, Champion manifests, or
   UI output.
5. Keep Queue order, Experiment lifecycle/verdict, Run intent, Attempt state,
   Evaluation outcome, Candidate provenance, and Promotion decision separate.
6. Treat external references as provider identity only. Do not claim cached or
   committed provider state is live, and do not fetch large bytes implicitly.
7. Report malformed, missing, ambiguous, or stale material explicitly; do not
   repair it by hand.

Fallback is deliberately read-only. Do not author a record, reorder a Queue,
regenerate projections, allocate IDs, grant config/runtime trust, approve
Promotion, emulate a transaction, run a scheduler/provider, or execute legacy
scripts. Wait for a compatible `exp` binary or make a separately reviewed
repository change outside this skill.
