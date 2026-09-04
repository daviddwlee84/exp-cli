# External tools and independently managed skills

exp records research meaning and links to upstream-owned state. It does not
absorb these systems' responsibilities:

| Tool | Upstream authority | When separate guidance is relevant |
|---|---|---|
| Native Git | commits, branches, linked-worktree bytes/lifecycle, and integration history | reviewing or merging an exact Source/Candidate ChangeSet |
| Pueue 4.x | local daemon queue, groups, task state, and logs | operating formal dispatch or managing the queue directly |
| MLflow | run parameters/metrics/traces/artifacts and registry state | querying/administering workload-owned tracking state |
| DVC | large data/artifact versions and DVC-native pipelines/queue state | operating a DVC repository without copying its bytes into exp |
| Object storage | large datasets, model/checkpoint/artifact/log bytes | retaining and governing bytes referenced only by sanitized identity/digest |
| Optional `dev` CLI | a potential future workspace lifecycle provider | only after exact schema-versioned machine capability receipts exist |
| Optuna-like Study | ask/tell trials and pruning inside one Plan | operating a future explicitly configured adapter |
| Slurm | cluster scheduling/accounting/allocation/site-native reasons | working under the named site's policies |
| Marimo/Jupyter | notebook document/runtime behavior | editing or running notebooks through separate guidance |

A reference in an exp record does not transfer authority or prove freshness.
Large datasets, models/checkpoints, artifacts, traces, and unbounded logs remain
in MLflow, DVC, or object storage. exp may retain only bounded summaries,
selected values, cryptographic digests, exact native identities, and sanitized
refs. Never copy credentials, raw environments, host paths, artifact bytes, or
unbounded provider output into canonical Git records.

## Discovery does not grant capability

Default `exp doctor` uses local executable lookup only and does not execute the
binary or contact a service. `exp doctor --live` is a separate explicit bounded
probe: it may run short version commands and read-only local-service checks, but
never installs, logs in, writes config, starts a service, or executes a workload.
`exp workspace backend list --probe` follows the same local/bounded rule for
workspace providers.

Missing MLflow, Pueue, or `dev` remains visible in command help and readiness
output. It does not hide actions and does not block direct native Try:

- Pueue is required only when `daemon tick`/`run` performs formal dispatch;
- MLflow is optional selected observation unless the user explicitly requests a
  strict `provider mlflow verify` or Evaluation attachment;
- `dev_cli` prepare/open/handoff/retire remains compiled unsupported until the
  public `dev` CLI provides schema-versioned exact-path machine receipts;
- native Git remains the byte, final inspection, and cleanup authority, including
  when trusted configuration permits a fallback.

Do not infer capability from executable presence or a parseable version alone.
Use the command's compiled descriptor and bounded readiness result.

## Implemented operation boundary

- Direct Try uses a managed native Git worktree, argv-only execution, bounded
  result, and durable marker. It needs none of Pueue, MLflow, or `dev`.
- Pueue status returns a sanitized task/group snapshot and removes captured
  environment maps recursively; cancel needs an exact task ID and confirmation.
- The formal daemon submits only the private `exp worker run` envelope. It does
  not accept arbitrary shell text as scheduler payload.
- MLflow verification reads only requested metrics/tags from a run the workload
  created. It does not start or log a run. Optional worker observation can be
  unavailable without changing successful process state.
- Experiment Git workspace commands create one allowlisted commit but never
  merge it or remove the retained formal worktree.
- `exp ui` is read-only. Startup is local-only; readiness refresh is explicit and
  bounded. It has no mutation, trust, execution, install/login, service-start,
  editor, or handoff action.

Adapters invoke configured binaries directly through exp's argv execution
boundary. They never execute scripts, templates, or commands from an installed
Agent Skill.

## Skill separation rule

This embedded skill may recommend consulting separately installed DVC, MLflow,
Pueue, Slurm, Git, or notebook guidance. It must never:

- source or execute another skill's scripts;
- copy its templates into the canonical research tree;
- assume a skill exists because its binary exists;
- install or update it implicitly;
- treat its prose as provider state;
- fetch large provider-owned bytes merely to summarize a reference;
- bypass exp's current command boundary with future-looking examples.

Optuna is currently a provider-neutral Study contract only. Do not invoke
Python, install Optuna, or improvise an ask/tell sidecar. A future concrete
adapter must be explicitly configured and remain scoped to one Plan revision.

Switching to external guidance is a separate visible action. Return to exp only
with bounded sanitized identity/evidence appropriate to the canonical record.
