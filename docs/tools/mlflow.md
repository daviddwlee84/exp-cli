# MLflow

MLflow remains authoritative for workload-created runs, metrics, parameters,
tags, traces, artifact locations/bytes, and registry state. The evaluation/observation adapter is a bounded
read-only observer; it does not create runs or download artifacts. The separate
opt-in exploration storage writer creates ownership-tagged runs, logs artifacts,
and verifies explicit retrieval. Neither changes registry state or turns run
status into a scientific verdict. See [Adhoc Exploration](../workflows/exploration.md).

The canonical authorities are separate:

- Attempt records the redacted operational execution and, only after verification,
  a sanitized MLflow ExternalRef;
- Evaluation records the scientific metric outcome under an EvaluationSpec;
- Candidate/Release/Promotion enforce their own typed evidence gates;
- an artifact URI is provider identity, not artifact-byte or deployment authority.

## Delivered entry points

| Entry point | Behavior when MLflow is absent or assertions fail |
|---|---|
| `exp provider mlflow verify` | Strict command failure; creates no canonical record. |
| `exp evaluation create --mlflow-run-id ...` | Strict verification/lineage/metric failure before immutable Evaluation creation. |
| Direct Try or formal worker with a selected profile and workload result `mlflow_run_id` | Optional observation becomes `unavailable` or `unverified`; successful process state remains successful. Only `verified` ownership is imported. |

All use the installed `mlflow` binary's read-only
`runs describe --run-id RUN_ID` command. The binary must already be resolvable
from `PATH`; `exp` does not install MLflow, enter a Python environment, start a
tracking server, authenticate interactively, or invoke a shell.

## Named profiles

MLflow profiles are delivered through layered `exp.config/v1`. A named profile
is replaced atomically by higher-precedence config and contains no endpoint or
credential value—only a non-secret context, binary name, timeout, environment
**names and policy**, and default metric names.

```toml
schema = "exp.config/v1"

[defaults]
mlflow_profile = "observer"

[mlflow.profiles.observer]
context = "research"
binary = "mlflow"
timeout = "30s"
default_metrics = ["macro_f1", "validation_loss"]

[mlflow.profiles.observer.env.MLFLOW_TRACKING_URI]
from = "MLFLOW_TRACKING_URI"
secret = false
required = true

[mlflow.profiles.observer.env.MLFLOW_TRACKING_TOKEN]
from = "MLFLOW_TRACKING_TOKEN"
secret = true
required = true
```

Both map keys and `from` values are variable names. Their values are resolved
from the parent only immediately before child start, are never put in config,
worker output, a record, or a receipt, and are all registered for redaction.
A required missing variable fails before that child process runs. Child names
beginning with `EXP_` are forbidden in serialized workload profiles.

Profiles are selected by explicit root flag first:

```bash
exp --mlflow-profile observer provider mlflow verify \
  --run-id <WORKLOAD_RUN_ID> \
  --tag 'exp.attempt_id=<ATTEMPT_ID>' \
  --json
```

Without the flag, `defaults.mlflow_profile` supplies the profile. An explicit
name still requires exact trust when the winning profile definition came from an
experiment/Source/subdirectory repository file. A repository-selected default
also requires trust on both selector and profile definition. Use `exp config
explain` and `exp config trust` rather than copying values into commands.

For backward compatibility, `--mlflow-context`, `--allow-env`, and
`--secret-env` construct an invocation-local profile named `compatibility` with
binary `mlflow` and a 30-second timeout. A named `--mlflow-profile` cannot be
mixed with those flags. `--allow-env NAME` is optional non-secret inheritance;
`--secret-env NAME` is required secret inheritance from the same parent name.
Neither accepts a value.

## Explicit run verification

Supply one explicit run ID and at least one effective assertion: an exact
`--metric`, an exact `--tag NAME=VALUE`, or a default metric from the selected
profile.

```bash
exp --mlflow-profile observer provider mlflow verify \
  --run-id <WORKLOAD_RUN_ID> \
  --metric macro_f1 \
  --tag 'exp.attempt_id=<ATTEMPT_ID>' \
  --json
```

Verification succeeds only when:

- the returned run ID exactly equals the requested ID;
- status is exactly `FINISHED`;
- every requested metric exists;
- every expected tag exists and equals the supplied string.

Missing/mismatched assertions, another run ID, and any other status are sorted
diagnostics and a command failure. `exp` does not decide whether a metric value
is scientifically desirable.

### Selected-field boundary

Only requested metric names and expected tag names cross the adapter boundary.
Unrequested metrics/tags, all parameters, and unrelated raw output are discarded.
The bounded result may include safe run ID, experiment ID, status, diagnostics,
and a sanitized artifact URI. Canonical URI sanitization removes userinfo and
the complete query component, rejects `file:` and unsafe host/path/fragment
material, and omits an unretainable URI with a diagnostic. Safe routing fragments
may remain. No artifact read or download follows the URI.

The subprocess receives a deny-by-default minimal environment plus the profile's
late-bound names. Output is bounded, parsed as one JSON value, and never treated
as a provider capability beyond this read-only operation.

## Optional worker attachment

A workload selected with a profile still owns run creation and logging. It may
place a safe `mlflow_run_id` string in the bounded JSON written to its assigned
`EXP_RESULT_PATH`. After process completion, the worker performs one read-only
observation using the profile's default metrics and the required ownership tag:

```text
exp.attempt_id = <the exact canonical Attempt ID>
```

The attachment state is:

- `verified` only when the run ID/status/default metrics and exact ownership tag
  verify;
- `unverified` for a syntactically invalid identity or assertion mismatch;
- `unavailable` for invalid profile environment, provider/binary absence, or
  invocation failure.

Observation failure does not turn an otherwise successful workload into failure.
Only a valid `verified` attachment is converted to an Attempt ExternalRef. That
reference records selected profile/context, run/status/experiment identity,
selected metric values, `mlflow.owner_attempt`, observation time, and a sanitized
artifact URI. It contains no profile values or artifact bytes. Replaying a
durable worker marker imports the same reference only when it is byte-equivalent;
a conflicting run identity fails closed.

### Direct Try versus formal Pueue runtime

Direct Try runs locally and may use a profile with environment bindings. The
selected profile name/context are pinned in the Attempt's direct policy, and
retry requires the same effective config digest and profile identity.

Formal runtime v2 may select a value-free profile (context, binary, timeout,
default metrics), but rejects **any** profile environment binding because Pueue
persists task environments. A formal workload requiring credentials must obtain
them after start through its own reviewed broker. Runtime v1 has no integrated
named-profile attachment route.

Provider observation remains optional in both worker paths. Candidate v2 does
not require MLflow, but if its Evaluation or backing Attempt carries an MLflow
owner claim, it must name the exact same formal Attempt.

## Attach MLflow to an Evaluation

MLflow attachment is part of immutable Evaluation creation, not a later update.
For the current formal Candidate v2 path, explicitly bind the successful formal
Attempt so the command creates Evaluation v2:

```bash
exp --mlflow-profile observer evaluation create \
  --title "Registered validation result" \
  --spec <EVALUATION_SPEC_ID> \
  --subject <EXPERIMENT_ID> \
  --attempt <FORMAL_ATTEMPT_ID> \
  --outcome passed \
  --metric 'macro_f1=0.913:score' \
  --metric 'validation_loss=0.204:loss' \
  --summary "Passed the sealed protocol" \
  --mlflow-run-id <WORKLOAD_RUN_ID> \
  --mlflow-tag 'exp.attempt_id=<FORMAL_ATTEMPT_ID>'
```

The command requires:

1. the ownership tag to parse as an Attempt in this Project;
2. a successful terminal Attempt with a canonical Run;
3. that Run's Experiment to be in the Evaluation subject's lineage;
4. when `--attempt` is present, an Experiment subject and a successful formal
   Attempt v3 for that same Experiment;
5. the explicit Attempt and MLflow owner to match;
6. every supplied Evaluation numeric metric to equal MLflow exactly—no tolerance;
7. names/units/threshold-derived outcome to match the EvaluationSpec.

An Experiment subject maps directly. A Candidate subject uses its Experiment.
A Release subject uses its combination Experiment when set, otherwise a slot
Candidate lineage. However, Evaluation v2 itself accepts only an Experiment
subject. Omitting `--attempt` preserves Evaluation v1 even when MLflow metadata
names an owner; such a record cannot satisfy Candidate v2's typed Attempt gate.

Only after all checks pass is the Evaluation transaction published. MLflow
verification alone never creates a Finding, Candidate, Release, Champion, or
Promotion.

## Artifact and promotion boundary

A verified artifact URI is useful navigation and provenance, not evidence that
bytes are present, immutable, safe, or production-ready. The observation adapter does not hash,
copy, cache, compare, register, alias, promote, delete, or serve MLflow artifacts
or models. Candidate v2 authority comes from clean SourceSnapshots plus typed
Evaluation; Promotion comes from a sealed holdout and named human approval.
There is no automatic deployment or rollback based on MLflow state.

## Remaining limits

- No run creation/logging or artifact/model-registry mutation.
- No artifact-byte store or automatic artifact download.
- No read-only registry capability beyond sanitized run observation.
- No environment-bound MLflow profile in formal Pueue runtime.
- No sweep/trial/nested-run interpretation as canonical Runs or Attempts.
