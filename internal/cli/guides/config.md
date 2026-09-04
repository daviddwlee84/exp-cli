# Understand configuration and trust

Configuration layers after authority resolves:

```text
user -> canonical workspace -> Source clone -> invocation subdirectory
```

Inspect effective non-secret values and provenance:

```text
exp config path
exp config show
exp config explain
exp config list
```

Configuration never establishes Project/Source identity. Execution-bearing
repository values remain inert until `exp config trust` approves the exact
current file digest and capability; later byte changes require new approval.
When a Source is selected, repository-layer receipts bind that Source ID, so
explain and trust with the same selectors and invocation root that execution
will use. The TTY command gathers and reviews missing trust fields, while scripts
must provide complete path/digest/capability/confirm flags.

A named MLflow profile contains policy and environment-variable names, not
resolved values. Direct Try may use environment bindings; formal runtime v2 over
Pueue requires a value-free profile. The raw `.exp/runtime.json` v2 file is
separate and needs exact `runtime.dispatch` trust.

Default `exp doctor` is executable lookup only; `doctor --live` is an explicit
bounded version/read-only local-service probe. Missing optional tools does not
hide commands or block native Try.

Next: choose `exp guide quick` for exploration or `exp guide research` for a
priced Queue/Experiment.
