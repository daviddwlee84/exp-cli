# MLflow

`exp` treats MLflow as a read-only telemetry source. The workload creates and
logs its own MLflow run; `exp` can verify selected fields from one explicit run
and, under stricter lineage checks, attach a sanitized reference to an
immutable Evaluation.

MLflow remains authoritative for runs, metrics, tags, parameters, artifacts,
and registry state. An Evaluation remains the canonical research statement.

## What is implemented

The implemented boundary has two entry points:

- `exp provider mlflow verify` checks exact metrics and expected tags on one
  workload-owned run.
- `exp evaluation create --mlflow-run-id ...` repeats verification and attaches
  the run only when its canonical Attempt ownership and subject lineage match.

The integration does **not**:

- create, start, terminate, or delete an MLflow run;
- log or update metrics, parameters, tags, or artifacts;
- register, transition, alias, or delete a model;
- install MLflow, enter an implicit Python environment, or open authentication;
- turn run success into a scientific verdict.

The `mlflow` executable must already be on `PATH`. The adapter invokes
`mlflow runs describe --run-id RUN_ID` from the project repository and applies
a bounded, deny-by-default environment.

## Verify a workload-owned run

Request every metric by exact name and every tag as an exact `NAME=VALUE`
assertion:

```bash
RUN_ID='0123456789abcdef0123456789abcdef'
ATTEMPT_ID='att_01a01e61-0000-7031-8000-000000000031'

exp provider mlflow verify \
  --run-id "$RUN_ID" \
  --metric macro_f1 \
  --metric validation_loss \
  --tag "exp.attempt_id=$ATTEMPT_ID" \
  --json
```

At least one `--metric` or `--tag` is required. Verification succeeds only
when all of these conditions hold:

- MLflow returns the same run ID that was requested;
- the run status is exactly `FINISHED`;
- every requested metric exists;
- every expected tag exists and its value is an exact string match.

Missing metrics, missing or mismatched tags, a different run ID, and any status
other than `FINISHED` produce diagnostics and a failed command. Verification
does not interpret whether a metric is scientifically good or bad.

### Selected-field and redaction boundary

Only requested metric names and expected tag names cross the adapter boundary.
Unrequested metrics and tags, all parameters, and other raw MLflow fields are
discarded. The result also includes bounded run metadata: run ID, experiment
ID, status, verification diagnostics, and a sanitized artifact URI when one can
be retained safely.

URI userinfo is removed, credential-like query data is removed or redacted,
and unsafe or unparseable artifact URIs are omitted with a diagnostic. Use
`--json` for the stable `exp.cli/v1` response envelope; do not scrape the human
summary.

### Environment and credentials

The MLflow subprocess inherits only a small portable baseline by default. Add
non-secret configuration names explicitly with `--allow-env`. Bind required
credentials from the parent environment with `--secret-env`:

```bash
export MLFLOW_TRACKING_URI='https://mlflow.example.test'
export MLFLOW_TRACKING_TOKEN='set-outside-shell-history'

exp provider mlflow verify \
  --run-id "$RUN_ID" \
  --metric macro_f1 \
  --allow-env MLFLOW_TRACKING_URI \
  --secret-env MLFLOW_TRACKING_TOKEN
```

`--allow-env NAME` is for additional non-secret variables. `--secret-env NAME`
requires the same variable name to exist in the parent process, binds its value
only for the MLflow subprocess, and keeps the value out of rendered command and
environment metadata. A missing required secret fails before MLflow runs.

## Attach a run to an Evaluation

Attaching MLflow telemetry is part of `evaluation create`, not a separate
mutation of an existing Evaluation:

```bash
exp evaluation create \
  --title "Validation result" \
  --spec "$EVALUATION_SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --outcome passed \
  --metric 'macro_f1=0.913:score' \
  --summary "Passed the registered threshold" \
  --mlflow-run-id "$RUN_ID" \
  --mlflow-context local \
  --mlflow-tag "exp.attempt_id=$ATTEMPT_ID" \
  --allow-env MLFLOW_TRACKING_URI \
  --secret-env MLFLOW_TRACKING_TOKEN
```

`--mlflow-context` is a non-secret name for the provider context and defaults
to `default`. Additional `--mlflow-tag NAME=VALUE` assertions may be supplied;
duplicate tag names are rejected during Evaluation creation.

### Ownership and lineage checks

Every attachment must include:

```text
--mlflow-tag exp.attempt_id=<canonical-attempt-id>
```

The named Attempt must exist in this project, be a successful terminal
execution, and point to a canonical Run. That Run's Experiment must belong to
the Evaluation subject:

- an Experiment subject must be that same Experiment;
- a Candidate subject must reference that Experiment;
- a Release subject must include the Experiment through its combination
  evidence or supported single-slot lineage.

A run from another Experiment, an unknown Attempt, or a non-successful Attempt
cannot be attached. If the Evaluation later backs a Candidate, the recorded
MLflow owner Attempt must also equal that Candidate's successful backing
Attempt.

### Exact metric match

For an attachment, `exp` requests every metric named by the Evaluation's
`--metric NAME=VALUE:UNIT` arguments. Each supplied numeric value must equal
the value returned by MLflow exactly; there is no rounding or tolerance. Units
and thresholds are validated against the EvaluationSpec, while MLflow supplies
only the numeric telemetry value.

Only after run verification, ownership, lineage, and exact metric checks pass
does `exp` create the immutable Evaluation. Its external reference records a
sanitized MLflow identity, observation time, verified status, experiment ID,
owner Attempt, and owner subject. Verification by itself never creates an
Evaluation, Finding, Candidate, Release, or Promotion.

See [Evidence to Promotion](../workflows/evidence-to-promotion.md) for the
larger scientific workflow and the [Provider contract](../design/provider-contract.md)
for the shared safety model.

## Future topics

These are reserved documentation areas, not supported operations today:

- named tracking-server profiles and context-specific authentication guidance;
- proxy versus direct artifact-access topology and credential boundaries;
- read-only history comparisons and richer metric diagnostics;
- workload conventions for sweeps, trials, and nested runs;
- a separately reviewed, read-only model-registry capability.
