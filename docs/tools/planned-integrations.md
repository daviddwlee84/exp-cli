# Planned Integrations

This page records tools that `exp` can identify or has designed a contract for,
but does not yet operate. It is intentionally explicit so executable discovery
is not mistaken for a working integration.

## What discovery means

```bash
exp doctor
exp doctor --json
exp doctor --live
```

The compiled provider registry knows candidate binary names, roles, and
capability names. `doctor` uses local `LookPath`-style discovery only. It does
not invoke `--version`, contact a provider, authenticate, install anything, or
confirm capability support. `--live` currently adds an informational diagnostic
but performs no additional probe.

Consequently, a provider may be reported as `found` while every capability is
still `unknown`. Only a dedicated, reviewed operation may establish
`supported` or `unsupported` for the contract it exercises.

## Current planned inventory

| Tool | Current code-level boundary | Not implemented |
|---|---|---|
| DVC | Compiled descriptor for Runner, Scheduler, and ArtifactStore roles; local lookup of `dvc` | No command execution, pipeline scheduling, artifact stat/list, download, or remote credential flow |
| Slurm | Compiled Scheduler descriptor; local lookup of `squeue`, `sacct`, `sbatch`, and `scancel` | No submission, observation, cancellation, accounting reconciliation, or cluster context configuration |
| Optuna | Provider-neutral `exp.search-adapter/v1` contract for Plan-scoped Study open/ask/tell/prune/observe behavior | No concrete Optuna adapter, Python runtime, storage connection, package installation, service, or authentication |
| Marimo | Compiled Runner descriptor; local lookup of `marimo` | No notebook inspection, preparation, sandboxing, dependency resolution, or execution |
| Jupyter | Compiled Runner descriptor; local lookup of `jupyter` | No notebook inspection, kernel selection, preparation, sandboxing, dependency resolution, or execution |

## DVC

DVC is a possible Runner, Scheduler, and ArtifactStore boundary, but those roles
are metadata today. Any integration must be introduced one operation at a time
after a real binary/version contract is verified. Artifact references must
remain immutable and sanitized; inspection must never imply a download, cache
write, remote login, or pipeline execution.

Before implementation, document the exact native JSON or fixed-field output,
repository/remote context, credential source, effect set, output bounds, and
recovery behavior for each operation.

## Slurm

A future Slurm adapter must preserve cluster, job, array, and step identity.
Controller and accounting commands count as remote reads even when invoked from
the login node. It must never generate `--export=ALL`; environment forwarding
requires an explicit allowlist or a site-approved profile.

Prefer verified native JSON. Where it is unavailable, request named fixed
fields with `--parsable2 --noheader --format`. Missing or delayed accounting
must produce partial or `unknown` observations, never a guessed terminal state.
Nested scheduling also requires an explicit reviewed owner for concurrency and
cancellation.

## Optuna-like search

The internal Study contract is an integration boundary, not a runtime. A Study
is subordinate to one exact Plan revision and may choose parameters, record
trials, and prune work. It cannot own global Queue order, ResourcePool
allocation, scientific Findings, Releases, or Promotions.

A concrete adapter must use a reviewed versioned API or a strictly configured
sidecar, preserve idempotency across ambiguous provider commits, and keep the
complete external Study identity scoped to its Plan. `exp` must not invoke
`python`, `uvx`, `pip`, install Optuna, start a service, or open authentication
on the user's behalf.

See the [Search adapter contract](../design/search-adapter-contract.md).

## Marimo and Jupyter

Marimo and Jupyter are potential Runner entrypoints, not durable Schedulers.
Discovery must remain separate from inspection, and inspection must remain
separate from execution. Merely finding a binary or notebook file cannot
resolve packages, start a kernel, execute cells, create outputs, or modify
notebook metadata.

An implementation will need an exact executable and argument contract, working
directory, environment policy, timeout, output limits, sandbox expectations,
and a clear rule for generated files. Scheduling ownership must remain with the
Attempt's single configured Scheduler.

## Adding another tool

W&B, Kaggle, Ray, Kubernetes, cloud control planes, and other systems are not
inferred from installed libraries or prose references. Each requires its own
named role, capability, effects, identity model, redaction rules, and tested
failure semantics before it can move into the implemented section of the
[Tools overview](index.md).

## Future topics

- Record supported version ranges after each real adapter is implemented.
- Explore DVC immutable artifact lookup without implicit materialization.
- Design site-specific Slurm context and accounting profiles.
- Evaluate an Optuna sidecar transport and ambiguous-commit recovery tests.
- Define safe, reproducible notebook preparation without automatic execution.
