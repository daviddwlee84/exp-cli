# Evidence to Promotion

## Close the Experiment

The closure request names every Run and whether its evidence is included or
excluded. It records the scientific outcome, Findings, and follow-up Ideas in
one atomic transaction:

```bash
exp experiment close --input closure.json --json
```

An operationally successful Attempt is not automatically included evidence.
Comparability, protocol compliance, and the registered decision rule still
govern the scientific conclusion.

## Create immutable Evaluations

Create an EvaluationSpec for a comparable protocol, then evaluate a subject:

```bash
exp evaluation spec create \
  --title "Scientific validation" \
  --purpose scientific \
  --dataset validation-v3 \
  --protocol "fixed preprocessing and five seeds" \
  --metric macro_f1:score:max:0.82 \
  --pool POOL --budget-hours 4

exp evaluation create --spec SPEC --subject SUBJECT \
  --outcome passed --metric macro_f1=0.834:score
```

MLflow may supply verified telemetry, but the resulting Evaluation is the
immutable research statement. See the [MLflow guide](../tools/mlflow.md).

## Candidate and Release

A Candidate pins a passing scientific Evaluation to exact Git code and the
backing successful Attempt. A Release composes validated Candidates into named
slots for one target. Multiple Candidates require separate combination evidence
rather than an assumption that independent gains add together.

## Human-only Promotion

Create a sealed promotion specification before spending the finite holdout
budget. Each promotion Evaluation is fresh and cannot be reused.

```bash
exp promotion spec-create \
  --title "Encoder production gate" \
  --target encoder-prod --evaluation-spec SPEC \
  --holdout-budget-hours 2

exp promotion append \
  --title "Promote encoder release" \
  --target encoder-prod --spec PROMOTION_SPEC \
  --challenger RELEASE --evaluation EVALUATION \
  --outcome accepted --approved-by HUMAN --confirm
```

No autonomy mode bypasses `--approved-by` and `--confirm`. The Champion manifest
is derived from Promotion history; it is not a second source of truth.
