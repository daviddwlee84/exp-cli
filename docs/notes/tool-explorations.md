# Tool Explorations

This page reserves durable places for investigating tools that touch the
research workflow. Entries describe questions and current boundaries; they do
not promise an integration. Create a separate dated note when an investigation
needs commands, version-specific observations, or a longer comparison.

All exploration notes follow the [publication and safety rules](index.md):
they are public, bounded, sanitized, and non-canonical.

## How to maintain an exploration

Use one of these statuses:

| Status | Meaning |
|---|---|
| `backlog` | The question is worth retaining, but no investigation is active. |
| `exploring` | Evidence is being gathered for a named question and context. |
| `validated` | The bounded conclusion has been reproduced and linked to evidence. It still does not change `exp` behavior by itself. |
| `deferred` | The work is intentionally postponed with a stated prerequisite. |

Each dated exploration should identify the observed tool version or named
non-secret context, distinguish documented behavior from direct observation,
and end with the decision or validation needed next. Never infer support merely
because `exp doctor` finds a binary: default doctor uses local executable
discovery only, and the current `--live` option performs no provider contact.

## Topic backlog

| Tool | Current `exp` boundary | Reserved exploration topics |
|---|---|---|
| MLflow | Implemented read-only `exp provider mlflow verify`. The workload creates and logs the run; verification requires an explicit run ID, requested metrics or expected tags, a `FINISHED` run, and sanitized output. | Safe artifact and registry read surfaces; tracking/artifact URI redaction; proxy versus direct artifact access; Evaluation attachment and Attempt lineage; supported CLI versions and bounded failure output. |
| Pueue | Implemented sanitized status, confirmed exact-task cancel, and daemon submission of the private `exp worker run` envelope. Captured environment maps and raw command strings do not cross the adapter boundary. | Bounded log access; richer observation and cancellation reconciliation; group and dependency semantics; submit-ambiguity recovery; Pueue 4.x compatibility and Windows limitations. |
| DVC | Binary discovery and provider-contract roles only; no DVC operation command is implemented. | Version and capability probes; artifact stat/list without implicit download; DVC queue reads; narrowly scoped writes only after effects and recovery are explicit; repository and remote identity redaction. |
| Optuna | `exp.search-adapter/v1` is a provider-neutral, Plan-scoped contract. There is no concrete Optuna adapter, Python environment, storage connection, or implicit installation. | Supported Optuna/storage versions; idempotent `open`/`ask`/`tell`/`prune`/`observe`; timeout-after-commit ambiguity; multi-objective and trial-state mapping; secret-reference-only storage; bounded sidecar transport. |
| Slurm | Candidate binaries can be discovered, but submit/observe/cancel operations are not implemented. | Named-site policy and version matrix; JSON or fixed-field parsers; cluster/array/step identity; delayed accounting; explicit environment export that never uses `--export=ALL`; safe cancellation and recovery. |
| Marimo and Jupyter | Candidate notebook binaries can be discovered. They are future workload entrypoints, not durable schedulers, and inspection must not execute notebook code. | Explicit runner argv; kernel and environment identity; package-resolution boundaries; bounded outputs; reproducibility capture; mapping a notebook execution to Run/Attempt without copying notebook state into canonical records. |

## Suggested dated exploration template

```markdown
# YYYY-MM-DD: Tool — question

- Status: backlog | exploring | validated | deferred
- Tool/version:
- Context:
- Last reviewed:

## Decision to support

State the concrete choice this exploration may inform.

## Documented contract

Link the upstream documentation and record only the relevant bounded claim.

## Safe observations

Record sanitized commands, selected output fields, and reproducible results.
Do not paste raw environments, full logs, credentials, or artifact bytes.

## Gaps and failure modes

Separate missing capability, ambiguous provider state, and unsafe behavior.

## Next validation

Name the test, fixture, design change, Idea, or implementation prerequisite.
```

## When an exploration graduates

A validated note may justify a design update or implementation Plan, but the
note does not activate a capability. An integration is delivered only when its
provider contract, command behavior, redaction, recovery, tests, and generated
command metadata agree. Update the [implementation roadmap](../design/roadmap.md)
when that state changes.

For ownership boundaries, see
[Records and Authority](../reference/records-and-authority.md) and the
[Provider Contract](../design/provider-contract.md).
