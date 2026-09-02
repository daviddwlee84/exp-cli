# Agent CLI Profiles

`exp` integrates with user-managed agent executables as fresh CLI processes,
not as provider SDK sessions. Every request receives an explicit prompt and
JSON Schema and must return exactly one JSON value that satisfies that schema.
The output remains advisory until a domain command validates and records it.

## Configuration file

Profiles default to `$XDG_CONFIG_HOME/exp/agents.toml`. Supply `--config` to use
another file. The top-level schema must be exactly `exp.agents/v1`.

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

`roles` maps a domain role to a profile name. `--profile` can override that
mapping for one invocation. The executable is a binary name, not a path, and is
resolved through `PATH` immediately before use.

## Profile fields

| Field | Contract |
|---|---|
| `executable` | Required binary basename; paths and names containing separators are rejected |
| `args` | Required argument array; shell concatenation is never used |
| `timeout` | Positive Go duration; defaults to 10 minutes |
| `max_output_bytes` | Per-stream and final-output bound; defaults to 1 MiB and may not exceed the global invoker limit |
| `output` | `stdout_json` (default) or `output_file_json` |
| `stdin_prompt` | Whether the prompt is also sent on stdin; defaults to `true` |
| `allowed_env` | Explicit non-sensitive environment names added to the minimal process environment |
| `secret_env` | Sensitive environment names resolved only immediately before process start and structurally redacted |
| `reported_model` | Optional user-configured metadata; `exp` does not discover or verify the model |

Supported placeholders are `{prompt_file}`, `{schema_file}`, `{schema_json}`,
`{output_file}`, and `{cwd}`. A placeholder must occupy the whole argument.
`output_file_json` requires exactly one `{output_file}` argument; the placeholder
is forbidden in other output modes.

Credential-sensitive names must be listed under `secret_env`, not
`allowed_env`. Names cannot be duplicated across the two lists. A missing
required secret fails the invocation; secret values are excluded from rendered
commands, diagnostics, and accepted output.

## Validate profiles

```bash
exp agent profiles
exp agent profiles --config /absolute/path/to/agents.toml --json
```

This strictly decodes the TOML, rejects unknown fields, validates every role
reference and profile, and lists the normalized profile names. It does not run
the executables.

## Run one schema-constrained request

```bash
exp agent run \
  --role idea_planner \
  --prompt prompt.md \
  --schema response.schema.json \
  --cwd "$PWD" \
  --json
```

`--role` and `--schema` are required. `--prompt` defaults to `-` for stdin;
`--cwd` defaults to the current directory; `--profile` overrides the configured
role mapping. Prompt input is limited to 4 MiB and schema input to 1 MiB.
External JSON Schema resources are disabled.

For every request, `exp` creates a private temporary directory containing
bounded prompt, schema, and output files, starts a new process with preserved
argument boundaries, and removes the directory afterward. Output is accepted
only if it is one JSON document, remains within the configured bound, contains
no protected secret value, and validates against the supplied schema.

With `output_file_json`, `exp` also monitors that the pre-created regular output
file keeps the same identity and does not exceed its limit while the process is
running. With `stdout_json`, stdout is the JSON document. There is no persistent
agent session or hidden provider state between requests.

## Domain roles

The built-in workflows use profile roles rather than hard-coded vendors:

- `idea_planner` proposes a complete Plan for `idea develop`;
- `queue_advisor` ranks a complete Queue partition plus a challenger;
- `queue_battle` compares adjacent entries with presentation order swapped;
- `experiment_implementer` edits an isolated allowlisted Git worktree.

Queue advice remains an immutable audit input and uncertainty leaves the Queue
unchanged. In the experiment workflow, the verified committed Git diff is more
authoritative than the agent's self-reported paths. See
[Git and Worktrees](git-worktrees.md).

## Future topics

- Add tested example wrappers for commonly used agent CLIs.
- Document profile portability across local development and CI hosts.
- Explore explicit streaming modes while preserving one final schema result.
- Add guidance for rotating secret environment sources and auditing redaction.
