# Runtime Dispatch

`.exp/runtime.json` is the project-local contract that connects canonical
ResourcePools and Plans to provider-owned execution. It binds:

- each ResourcePool to one Pueue group and stable label prefix;
- each Plan to an absolute executable, argument vector, working directory, and
  timeout;
- an exact Git `base_commit`, `head_commit`, and `change_set`;
- explicitly allowed non-secret environment names;
- required output paths whose SHA-256 digests will be recorded.

Use `checkout: "registered_worktree"` when the workload must run in the unique
linked worktree whose HEAD equals `head_commit`. Host paths are not persisted.

## Before dispatch

```bash
exp daemon frontier
exp daemon status
exp policy autonomy assisted --confirm-auto-experiment
exp daemon tick
```

`frontier` is local and does not contact Pueue. `tick` performs one
reconciliation and admission pass; `run` repeats until cancelled. Capacity is
drawn from named ResourcePools, with weighted exploit/explore fairness and idle
borrowing when only one lane has eligible work.

Before submission, `exp` verifies repository identity, a clean executable tree,
exact HEAD, base ancestry, and the exact changed path set. The daemon writes an
outbox before submission and recovers by a worktree-scoped stable Pueue label.

## Workload contract

The worker injects three non-secret variables:

| Variable | Meaning |
|---|---|
| `EXP_JOB_ID` | Private control-plane job identity |
| `EXP_ATTEMPT_ID` | Canonical Attempt identity |
| `EXP_RESULT_PATH` | Path where the workload must write its bounded result JSON |

The workload still owns MLflow run creation and logging. Pueue process success
updates operational state only; it never creates a scientific verdict.

See [Pueue](../tools/pueue.md) and the detailed
[architecture](../design/architecture.md#execution-control-plane).
