# Agents and Workspaces

`exp` invokes a fresh CLI agent process for each request; it does not keep a
provider SDK session or hidden conversation. Agent output is advisory until a
domain service validates exact schema, identity, revision, and effects.

## Agent execution profiles

Executable role mappings remain in the separate user-managed
`$XDG_CONFIG_HOME/exp/agents.toml` (`exp.agents/v1`):

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

`executable` is a binary name, not a path/shell command. Placeholders occupy
complete argv elements. Every process receives bounded inputs/environment and
must return exactly one JSON value matching the supplied schema. Inspect with
`exp agent profiles`; test a role with `exp agent run`.

Layered `exp.config/v1` also exposes a trust-annotated
`defaults.agent_profile`, but current agent commands do not consume it. They use
the `exp.agents/v1` role mapping or explicit command `--profile`. Treat the
layered field as policy/provenance metadata until execution wiring is added.

## Source-aware isolated implementation

Canonical Experiment and code Source may be in different repositories. Resolve
both explicitly when needed:

```bash
EXP_REPO="$HOME/src/my-application-experiments"
SOURCE_KEY='app'
EXPERIMENT_ID='exp_replace_with_returned_id'
BASE_COMMIT='replace_with_full_lower_case_commit'

exp --workspace "$EXP_REPO" --source "$SOURCE_KEY" \
  experiment workspace prepare "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**'

exp --workspace "$EXP_REPO" --source "$SOURCE_KEY" \
  experiment agent "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**' \
  --prompt implementation-notes.md
```

The Source must be active and locally associated. Its immutable `subdir` defines
the agent cwd and the root for `--allow` globs; Git path identities remain
repository-root-relative. The Experiment must be active with locked design, and
the Source checkout/base must be clean and exact.

Native Git creates a deterministic XDG worktree/branch scoped by full Project,
Source, and Experiment IDs. Commit re-discovers it, rejects Git/canonical
metadata and paths outside Source subdir/allowlist, stages only observed paths,
and accepts exactly one single-parent commit. If the agent-reported path list
differs, the Git commit remains authoritative and a diagnostic is returned.

`--workspace-backend dev_cli` does not delegate lifecycle work today. The
optional provider lacks an exact schema-versioned path receipt, so prepare/open/
handoff/cleanup/retire are unsupported and trusted policy may explicitly fall
back to `native_git`. Native Git always verifies, inspects, and owns cleanup.

The formal workspace/branch is retained for human review. `exp` does not merge,
push, rebase, delete the branch, or resolve integration conflicts. Public
verified cleanup currently belongs to direct Try; formal workspace commands do
not automatically remove their worktree.

## Agent authority boundaries

- `idea develop` can propose and, with explicit `--apply`, create a validated
  Plan transaction; the model cannot bypass schema, revision, Policy, or resource
  checks.
- `queue insert --agent` records QueueAdvice and order-swapped Battles; uncertain
  output leaves Queue order unchanged.
- `experiment agent` may produce one exact allowlisted commit; it cannot create
  scientific evidence or integrate it.
- A commit becomes Candidate-eligible only after clean formal Attempt v3,
  included Run, Evaluation v2 bound to that Attempt, and Candidate v2 Source
  equality.
- No agent can approve Promotion, grant config trust, operate the read-only TUI,
  or turn MLflow/artifact state into a verdict.

See [Git and Worktrees](../tools/git-worktrees.md) and
[Runtime Dispatch](runtime-dispatch.md).
