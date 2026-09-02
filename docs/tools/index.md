# Tools

`exp` coordinates research work without replacing the tools that execute code,
store telemetry, or own source history. An integration is deliberately narrow:
the upstream tool remains authoritative for its native state, while `exp`
records only the canonical research decisions and sanitized references it needs.

## Integration status

The status labels in this table are part of the documentation contract. A
compiled descriptor or a binary found by `exp doctor` does not mean that an
operation is implemented.

| Tool or component | Status | What works today | Authority boundary |
|---|---|---|---|
| [Git and linked worktrees](git-worktrees.md) | Implemented integration | Prepare an isolated experiment branch/worktree and commit an exact allowlisted change set | Git owns code history and integration |
| [Pueue](pueue.md) | Implemented integration | Sanitized status, identity-checked cancellation, and daemon submission through the private worker envelope | Pueue owns live task and group state |
| [MLflow](mlflow.md) | Implemented read-only integration | Verify requested metrics and tags on one workload-created run, then attach a sanitized identity to an Evaluation | The workload and MLflow own run creation, telemetry, artifacts, and registry state |
| [Agent CLI profiles](agent-cli-profiles.md) | Implemented integration | Validate local profiles and run a fresh schema-constrained CLI process | The configured executable owns any external provider interaction; `exp` persists no session and agent output remains advisory |
| Direct worker and SQLite control state | Internal implementation | Execute an exact workload envelope; coordinate leases, jobs, fencing, outbox recovery, and fairness | Private operational state is never scientific authority |
| [DVC and Slurm](planned-integrations.md) | Discovery/contract only | Local executable discovery and compiled capability metadata | No DVC or Slurm operation is integrated |
| [Marimo and Jupyter](planned-integrations.md) | Discovery/contract only | Local executable discovery and Runner descriptor metadata | No notebook inspection or execution is integrated |
| [Optuna-like search](planned-integrations.md) | Contract only | Provider-neutral `exp.search-adapter/v1` types and invariants | No concrete Optuna runtime, package installation, or service contact is included |

## Inspect local availability

```bash
exp doctor
exp doctor --json
```

`doctor` performs executable lookup only. It does not run third-party
`--version` commands, contact a daemon or network, authenticate, install a
package, or start a service. Versions and capability support therefore remain
`unknown` unless a specific operation verifies them. The current `--live` flag
reports that live probing is not implemented and still performs only local
discovery.

Provider contact is always operation-specific. Today that means Pueue
status/cancel, MLflow verify, or daemon `tick`/`run`; local record commands and
`daemon frontier` do not silently refresh provider state.

## Shared safety rules

- Scheduler or process success is operational evidence, not a scientific
  verdict.
- Provider output is bounded and structurally redacted before it crosses the
  adapter boundary.
- Canonical records never contain raw environments, credentials, unbounded
  logs, or artifact bytes.
- An adapter never installs packages, opens authentication, starts services,
  downloads artifacts, or executes notebook code implicitly.
- Machine-readable commands return one `exp.cli/v1` envelope; do not scrape
  human tables.

For the full role and effect model, see the
[Provider contract](../design/provider-contract.md).

## Future topics

- Add operation-specific capability/version matrices as integrations mature.
- Document deployment profiles for shared Pueue, Slurm, and tracking services.
- Add troubleshooting pages for sanitized provider diagnostics.
- Add new tool pages without changing the authority boundary described here.
