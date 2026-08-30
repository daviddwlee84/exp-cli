# Human, agent, and read-only use

## Human use

Use explicit flags, inspect the proposed meaning, and review the ordinary Markdown committed to Git. Run `exp validate` before treating records as sound, and use `exp render --check` when verifying that generated project views match canonical records.

`exp doctor` is local-only by default, and current `--live` performs no extra
contact. Provider contact is operation-specific: Pueue status/cancel, MLflow
verify, and daemon tick/run. Use those commands only when that external read or
mutation is intended.

## Agent use

Prefer machine contracts over terminal prose:

- add `--json` to commands that advertise it;
- use `idea develop --apply` for a schema-validated agent proposal or
  `idea qualify` for an explicit human qualification; use `plan add --input -`
  only for the simpler v1 Plan input documented by that command;
- parse the complete JSON envelope and check its schema version, `ok`, `partial`, data, and diagnostics fields;
- keep stdout as JSON-only and treat stderr separately;
- use canonical typed IDs and revisions returned by the command instead of extracting display codes from human output;
- call `exp validate` after any supported mutation and never reconstruct relationships from generated projections;
- treat `queue insert --agent` human-review output as a successful audit with no
  Queue mutation, not as permission to choose one battle response manually;
- use `daemon frontier` before enabling dispatch, and never change Policy
  autonomy without explicit authorization.

Command help and [commands.md](commands.md) are authoritative for syntax in this build. If metadata and recollection disagree, stop and use the metadata.

## Agent profile example

Profiles default to `$XDG_CONFIG_HOME/exp/agents.toml`. Configure a fresh CLI
process, not a provider SDK session. Placeholders must occupy one whole argument.
The `research-agent` binary below is an example user-supplied wrapper.

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

Validate with `exp agent profiles`. The executable name is resolved through
`PATH`; secret environment entries are names resolved only at process start.

## Runtime contract example

`.exp/runtime.json` is non-canonical project-local configuration. Replace the
IDs, absolute executable, full commits, and paths with values returned by the
reviewed workflow.

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
      "argv": ["--config", "configs/trial.toml"],
      "checkout": "main",
      "cwd": ".",
      "timeout": "2h",
      "allowed_env": [],
      "secret_env": [],
      "base_commit": "0000000000000000000000000000000000000000",
      "head_commit": "1111111111111111111111111111111111111111",
      "change_set": ["configs/trial.toml"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
```

Use `exp daemon frontier` for a provider-free validation/read before any
dispatch-enabled tick.

Use `checkout: "registered_worktree"` to select the unique registered linked
worktree at `head_commit`. Pueue persists task environments, so runtime
`secret_env` must remain empty; retrieve credentials inside the workload from a
broker or configured provider profile. Label prefixes sharing a Pueue group
must be prefix-free, and the selected runtime config path cannot be part of a
Plan's `change_set`.

## Manual read-only fallback

When `exp` is unavailable, a repository must remain understandable with ordinary read-only file tools and Git:

1. Locate the fixed `<git-root>/experiments/PROJECT.md`; version 1 permits one root per Git repository.
2. Read strict TOML front matter and Markdown bodies without editing them. Treat full typed IDs as identity; paths and short display codes are navigation aids.
3. Read canonical records rather than rebuilding facts from `README.md`, `ROADMAP.md`, `LEDGER.md`, or `DECISIONS.md`. Those files are deterministic projections and may be stale.
4. Keep Queue order, Experiment lifecycle/verdict, Run evidence intent, Attempt
   operational state, Evaluation outcome, and Promotion decision separate. Do
   not infer missing inverse relationships, terminal state, or current Champion.
5. Treat external references as provider identity only. Do not claim that cached or committed provider state is live.
6. Report malformed, missing, ambiguous, or stale material explicitly. Do not repair it by hand.

The fallback is deliberately read-only. Do not hand-author an Idea/Plan, change
Queue order, update front matter, regenerate a projection, allocate an ID,
approve Promotion, or emulate a transaction. Wait for a compatible `exp`
binary or make an independently reviewed repository change outside this skill.

Never execute legacy experiment-harness scripts, another skill's helper scripts, notebook code, package installers, authentication flows, schedulers, or provider commands as part of fallback inspection.
