# Agents and Workspaces

`exp` integrates with fresh CLI agent processes rather than provider SDK
sessions. Profiles live at `$XDG_CONFIG_HOME/exp/agents.toml` and map roles to a
user-managed executable.

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

Placeholders occupy complete arguments. Each request starts a new process and
must return exactly one JSON value matching the supplied schema. Inspect or test
profiles with `exp agent profiles` and `exp agent run`.

## Isolated implementation

```bash
exp experiment workspace prepare EXPERIMENT \
  --base BASE_COMMIT --allow 'src/**' --allow 'configs/**'

exp experiment agent EXPERIMENT \
  --base BASE_COMMIT --allow 'src/**' --allow 'configs/**' \
  --prompt implementation-notes.md
```

The workspace is a linked Git worktree at an exact base. Commit operations stage
only observed allowlisted paths, reject canonical `experiments/` records and Git
metadata, and produce one exact experiment commit. They do not merge or remove
the worktree; integration remains a human-controlled action.

Agent output is advisory until a domain command validates and records it. A code
commit is not scientific evidence and cannot become a Candidate without a
successful included Attempt and matching Evaluation.
