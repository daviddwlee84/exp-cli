# Pueue

`exp` uses Pueue 4.x as the implemented local scheduler integration. Pueue owns
live task and group state; `exp` contributes canonical scheduling intent,
sanitized observation, an audited submission envelope, and narrowly authorized
cancellation.

Pueue process success is operational evidence only. It can advance an
Attempt's operational state after reconciliation, but it never creates a
scientific Evaluation, Finding, Candidate, Release, or Promotion.

## Read a sanitized scheduler snapshot

```bash
exp provider pueue status
exp provider pueue status --json
```

The command must run inside an `exp` project and contacts the configured local
Pueue daemon. It reads native JSON, recursively removes task environment maps,
and returns only normalized task and group data. It does not expose command
strings, raw status objects, or persisted environment values.

Human output contains group name, native group state, parallelism, and each
task's ID, group, normalized state, and label. JSON additionally exposes safe
fields such as priority, dependency IDs, native state/reason, exit code, and
terminal timestamps when available.

### Normalized task states

| Pueue state or result | `exp` state |
|---|---|
| `Queued` | `queued` |
| `Stashed`, `Paused`, `Locked` | `blocked` |
| `Running`, `Starting` | `running` |
| `Done: Success` | `succeeded` |
| `Done: Failed` | `failed` with the exit code when present |
| `Done: Killed` | `cancelled` |
| `Done: DependencyFailed` | `dependency_failed` |
| Unrecognized shape or value | `unknown` with a bounded native reason |

Unknown data fails closed; it is not guessed into a terminal state. Use
`daemon status` for private local controller state. That command does not
contact Pueue. `daemon frontier` is also local-only, while `daemon tick` and
`daemon run` do contact Pueue for reconciliation and admission.

## Cancel exactly one owned task

Cancellation is an explicit provider mutation and requires the exact task ID
and `--confirm`:

```bash
exp provider pueue cancel 42 --confirm

# Use a non-default project-relative runtime contract when required.
exp provider pueue cancel 42 \
  --confirm \
  --config configs/exp-runtime.json \
  --json
```

The default runtime contract is `.exp/runtime.json`. A non-negative numeric
task ID and confirmation are necessary, but they are not sufficient. Before
calling `pueue kill`, `exp` requires all of the following:

1. Exactly one canonical Attempt in this project references that native Pueue
   task ID.
2. The Attempt assigns scheduler ownership to `pueue` and is a v2 dispatch with
   a canonical pool and dispatch route.
3. The external reference context is the local Pueue context.
4. The Attempt's captured Pueue group and label still match the current runtime
   pool binding.
5. The live scheduler snapshot contains exactly one task with that ID, and its
   group and label match the canonical route.

Missing, foreign, duplicated, stale, or mismatched identities are rejected.
`--confirm` never bypasses an identity check. After the command succeeds,
Pueue owns the cancellation request; a later daemon reconciliation observes
the terminal scheduler state and performs any authorized canonical transition.

## Daemon identity and dispatch safety

`.exp/runtime.json` binds each canonical ResourcePool to one Pueue group and a
stable label prefix. Within a group, prefixes must be pairwise prefix-free and
short enough for the full dispatch ID. The dispatch ID incorporates the
canonical checkout scope, so linked worktrees that share the Git-common SQLite
store retain separate recovery identities.

The daemon applies these rules:

- it snapshots Pueue before admission and accounts for observed nonterminal
  tasks against canonical pool capacity;
- it writes a submission outbox before contacting Pueue;
- after ambiguous submission or restart, it recovers only outbox entries from
  the same canonical worktree scope and looks up an exact group/label route;
- duplicate live tasks for one route are an error, not an arbitrary choice;
- submission is limited to the private `exp worker run` envelope with a clean
  absolute worker path and validated argument tokens; arbitrary shell fragments
  and record titles cannot become the submitted command.

Pueue persists task environments in daemon state. Consequently, runtime
`secret_env` must be empty, and `allowed_env` may contain only explicitly
approved non-secret names. Workloads that need credentials must obtain them
after startup through a workload-side broker or provider profile.

See [Runtime Dispatch](../workflows/runtime-dispatch.md) for the complete
workload contract and the [Provider contract](../design/provider-contract.md)
for scheduler effects and safety invariants.

## Failure guidance

- If `status` cannot connect, verify that the compatible Pueue daemon is
  already running; `exp` never starts it implicitly.
- If cancel reports no canonical owner, inspect the Attempt's scheduler
  external reference instead of cancelling an untracked native task through
  `exp`.
- If group or label identity changed, reconcile the runtime contract and
  canonical Attempt route. Do not work around the check with a different task
  ID.
- If dispatch rejects environment configuration, remove secrets from the
  persisted Pueue environment and move credential acquisition into the
  workload.

## Future topics

These are reserved documentation areas, not supported operations today:

- group sizing, priority, and queue-tuning runbooks;
- dependency, retry, pause, and resume semantics;
- bounded task-log and output inspection with redaction;
- shared or remote Pueue daemon deployment and authentication profiles;
- operator recovery drills for ambiguous submission and daemon replacement.
