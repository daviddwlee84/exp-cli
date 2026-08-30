# External tools and independently managed skills

exp records research meaning and links to upstream-owned state. It does not absorb the responsibilities of these systems:

| Tool | Upstream authority | When separate guidance is relevant |
|---|---|---|
| DVC | data/artifact versions and DVC-native pipelines or queue state | inspecting or operating a DVC repository with its independently installed skill |
| MLflow | run telemetry, parameters, metrics, traces, artifacts, and registry state | querying or administering MLflow through independently managed guidance |
| Pueue | local daemon queue, groups, task state, and logs | managing the queue directly with its own skill or CLI documentation |
| Git | commits, branches, linked worktrees, and integration history | reviewing or merging an exact experiment ChangeSet |
| Optuna-like Study | ask/tell trials and pruning inside one Plan | operating a future explicitly configured concrete adapter |
| Slurm | cluster scheduling, accounting, allocation, and native reasons | working with the named site's policies and independently managed Slurm guidance |
| Marimo | reactive notebook document and runtime behavior | editing or running notebooks through independently managed Marimo guidance |

A reference in an exp record does not transfer authority or prove freshness. Keep only sanitized provider/context/native identity and observation metadata where the exp contract permits it. Never copy credentials, raw environments, unbounded logs, or artifact bytes into canonical research records.

## Implemented boundary

Default `exp doctor` checks only whether provider binaries are discoverable with
`LookPath`; it does not execute them. `--live` currently adds no contact.

Operation-specific commands implement a narrow boundary:

- Pueue status returns a sanitized task/group snapshot and recursively removes
  captured environment maps; cancel requires an exact task ID and confirmation.
- The daemon submits only the private `exp worker run` envelope. It does not
  accept arbitrary user shell text as a scheduler payload.
- MLflow verification reads only explicitly requested metrics/tags from a run
  the workload already created. It does not start or log a run.
- Experiment Git workspace commands create one allowlisted commit but never
  merge it or remove the worktree.

These adapters invoke configured binaries directly through exp's
argument-array execution boundary. They never execute scripts, templates, or
commands from an installed Agent Skill.

## Skill separation rule

This embedded skill may recommend that a human or agent consult a separately installed DVC, MLflow, Pueue, Slurm, or Marimo skill. It must never:

- source or execute that skill's scripts;
- copy its templates into the research tree;
- assume it is installed because the corresponding binary exists;
- install or update it implicitly;
- treat its prose as provider state;
- bypass exp's current command boundary by following future-looking examples.

Optuna is currently a provider-neutral Study contract only. Do not call Python,
install Optuna, or improvise an ask/tell sidecar. A future concrete adapter must
be explicitly configured and remain scoped to one Plan revision.

Switching to external guidance is a deliberate, separately visible action. Return to exp only with sanitized identity or evidence appropriate to the canonical research record.
