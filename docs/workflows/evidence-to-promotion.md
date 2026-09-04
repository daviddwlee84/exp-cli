# Evidence to Promotion

Promotion is a typed chain, not a shortcut from process success:

```text
Try (optional exploration)
  -> Idea v2 -> qualified Plan v2 -> Queue
  -> Experiment -> Run -> clean formal Attempt v3
  -> supported Experiment conclusion -> Evaluation v2 -> Candidate v2
  -> validated Release (+ combination evidence when needed)
  -> sealed fresh holdout Evaluation -> named-human Promotion
  -> derived Champion manifest
```

Git commits, worker result JSON, output hashes, MLflow runs, and artifact URIs are
supporting identities/observations. None can skip a canonical gate.

Use a dedicated workspace explicitly in scripts:

```bash
EXP_REPO="$HOME/src/my-application-experiments"
```

## 1. Explore without granting promotion authority

Use `exp try run` for a bounded question. Clean or explicitly captured dirty
Try-owned Attempts can be concluded only after all are terminal. A useful Try is
adopted into Idea v2 with a named human:

```bash
TRY_ID='try_replace_with_returned_id'

exp --workspace "$EXP_REPO" try finish "$TRY_ID" \
  --summary "This merits a controlled clean study" \
  --no-results \
  --confirm

exp --workspace "$EXP_REPO" try adopt "$TRY_ID" \
  --title "Evaluate the controlled change" \
  --summary "Run the direction under the registered protocol" \
  --proposed-by 'human:alice' \
  --cluster optimizer \
  --domain modeling \
  --work training \
  --method optimization \
  --component scheduler \
  --lane explore \
  --risk medium \
  --horizon short \
  --origin hybrid \
  --confirm
```

A Try cannot back a Candidate. In particular, a dirty Try must be rerun through
the formal clean path after Idea qualification and Queue admission.

## 2. Dispatch a clean formal Attempt

Runtime v2 identifies one execution Source plus optional read-only Sources.
Every Source must be active, locally registered, exact at its declared base/head
and ChangeSet, and clean. Dispatch captures those SourceSnapshots into Attempt
v3, then passes explicit Project/root/scope authority to worker-job v2.

```bash
exp --workspace "$EXP_REPO" config trust
exp --workspace "$EXP_REPO" --source app daemon frontier
exp --workspace "$EXP_REPO" policy autonomy assisted \
  --confirm-auto-experiment
exp --workspace "$EXP_REPO" --source app daemon tick
# Or keep reconciling until interrupted:
exp --workspace "$EXP_REPO" --source app daemon run --interval 5s
```

The first command's TTY wizard can approve the reviewed `.exp/runtime.json`
exact digest for `runtime.dispatch`. `daemon frontier` does not contact Pueue;
`tick`/`run` do. The controller creates Experiment, Run, and planned Attempt
atomically with the Plan/Queue transition before Pueue submission.

A successful terminal Attempt is still only operational evidence. If a Source
is dirty, missing, changed, or untrusted, formal dispatch fails or blocks before
scheduler start; it does not capture or clean the checkout. Missing Pueue blocks
formal dispatch but does not affect native Try. MLflow remains optional.

## 3. Close the Experiment with evidence disposition

The closure request names every Run and says whether its evidence is included or
excluded. It records the scientific verdict, Plan completion, Findings, and
follow-up Ideas in one canonical transaction:

```bash
exp --workspace "$EXP_REPO" experiment close --input closure.json --json
```

Only Runs belonging to the Experiment are legal. Included Runs must have a
successful direct worker Attempt. Comparability, protocol compliance, and the
registered decision rule govern disposition; operational success does not
automatically include a Run. Use `supported`, `refuted`, `inconclusive`, or
`invalid` accurately.

Keep the returned identities:

```bash
EXPERIMENT_ID='exp_replace_with_returned_id'
ATTEMPT_ID='attempt_replace_with_returned_id'
POOL_ID='pool_replace_with_returned_id'
```

## 4. Create a typed scientific Evaluation

Create an EvaluationSpec before evaluating. Names, units, directions, optional
thresholds, dataset, protocol, ResourcePool, and finite budget form the
comparable contract:

```bash
exp --workspace "$EXP_REPO" evaluation spec create \
  --title "Scientific validation" \
  --purpose scientific \
  --dataset validation-v3 \
  --protocol "fixed preprocessing and five seeds" \
  --metric macro_f1:score:maximize:0.82 \
  --pool "$POOL_ID" \
  --budget-hours 4
EVALUATION_SPEC_ID='evalspec_replace_with_returned_id'
```

For Candidate v2, evaluate the concluded Experiment and explicitly bind the
exact successful formal Attempt v3:

```bash
exp --workspace "$EXP_REPO" evaluation create \
  --title "Scientific result" \
  --spec "$EVALUATION_SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --attempt "$ATTEMPT_ID" \
  --outcome passed \
  --metric macro_f1=0.834:score \
  --summary "Passed the registered scientific threshold"
EVALUATION_ID='eval_replace_with_returned_id'
```

This creates Evaluation v2 only when the Attempt is terminal/successful,
Run-owned, formal Attempt v3 and its Run belongs to the Experiment. Evaluation
time cannot precede the Attempt. Metrics must exactly cover the spec and the
outcome must agree with thresholds.

MLflow is optional corroboration. `--mlflow-run-id` additionally requires the
run to be `FINISHED`, selected values to match, and `exp.attempt_id` to identify
the same Attempt when `--attempt` is supplied:

```bash
MLFLOW_RUN_ID='replace_with_workload_owned_run_id'
exp --workspace "$EXP_REPO" --mlflow-profile formal-observer \
  evaluation create \
  --title "Scientific result with MLflow corroboration" \
  --spec "$EVALUATION_SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --attempt "$ATTEMPT_ID" \
  --outcome passed \
  --metric macro_f1=0.834:score \
  --mlflow-run-id "$MLFLOW_RUN_ID" \
  --mlflow-tag "exp.attempt_id=$ATTEMPT_ID" \
  --summary "Passed with exact Attempt ownership"
```

The resulting sanitized ExternalRef does not copy unrequested telemetry or
artifact bytes. Omitting `--attempt` deliberately creates Evaluation v1, even if
an MLflow owner exists; it cannot back Candidate v2.

## 5. Create Candidate v2 from the same Attempt

```bash
exp --workspace "$EXP_REPO" candidate create \
  --title "Evaluated Source change" \
  --experiment "$EXPERIMENT_ID" \
  --evaluation "$EVALUATION_ID" \
  --attempt "$ATTEMPT_ID" \
  --json
CANDIDATE_ID='cand_replace_with_returned_id'
```

Candidate v2 requires all of the following:

- the Experiment is closed, concluded, and `supported`;
- its conclusion includes the Attempt's Run;
- the Attempt is a successful terminal Run-owned Attempt v3 completed no later
  than conclusion;
- every Attempt SourceSnapshot is clean;
- the Evaluation is `passed`, uses a scientific spec, and is Evaluation v2 bound
  to exactly the same Attempt;
- any MLflow owner also names that Attempt.

The Candidate copies each Source ID, exact head commit, and sorted ChangeSet from
the Attempt. It does not copy checkout paths or artifact bytes. Candidate v1 is
a legacy path only: explicit `--legacy`, or the old `--git-commit` plus
`--change` shape, requires a matching successful included Attempt v2.

## 6. Assemble and validate a Release

A Release maps project-defined slot names to Candidates. Supply the strict
`exp.request.release-create/v1` document:

```bash
exp --workspace "$EXP_REPO" release create --input release.json --json
RELEASE_ID='rel_replace_with_returned_id'
```

A draft may be assembled for review. A validated Release needs a passing
Release-level Evaluation under a sealed promotion-purpose EvaluationSpec. If
more than one distinct Candidate fills the slots, the Release must also cite a
closed/supported combination Experiment v2 whose `candidate_inputs` exactly
match the slots and a passing scientific Evaluation of that combination. Gains
from separate Experiments are never assumed additive.

## 7. Seal and spend a fresh promotion holdout

First create and seal a promotion-purpose EvaluationSpec; every metric requires
a threshold:

```bash
exp --workspace "$EXP_REPO" evaluation spec create \
  --title "Production holdout protocol" \
  --purpose promotion \
  --dataset holdout-v1 \
  --protocol "sealed production comparison" \
  --metric macro_f1:score:maximize:0.83 \
  --pool "$POOL_ID" \
  --budget-hours 2 \
  --sealed
PROMOTION_EVALUATION_SPEC_ID='evalspec_replace_with_returned_id'

exp --workspace "$EXP_REPO" promotion spec-create \
  --title "Production gate" \
  --target encoder-prod \
  --evaluation-spec "$PROMOTION_EVALUATION_SPEC_ID" \
  --holdout-budget-hours 2
PROMOTION_SPEC_ID='promspec_replace_with_returned_id'
```

After the PromotionSpec seal, create a fresh Evaluation of the validated
challenger Release using that exact EvaluationSpec. This Release-subject result
is Evaluation v1 because Evaluation v2 is reserved for formal Experiment
Attempt ownership:

```bash
exp --workspace "$EXP_REPO" evaluation create \
  --title "Fresh promotion holdout" \
  --spec "$PROMOTION_EVALUATION_SPEC_ID" \
  --subject "$RELEASE_ID" \
  --outcome passed \
  --metric macro_f1=0.841:score \
  --summary "Challenger passed the sealed holdout"
HOLDOUT_EVALUATION_ID='eval_replace_with_returned_id'
```

The holdout must be evaluated strictly after the PromotionSpec was sealed and
cannot be consumed by another Promotion. The PromotionSpec holdout budget may
not exceed its EvaluationSpec budget.

## 8. Append the human production decision

```bash
exp --workspace "$EXP_REPO" promotion append \
  --title "Promote encoder release" \
  --target encoder-prod \
  --spec "$PROMOTION_SPEC_ID" \
  --challenger "$RELEASE_ID" \
  --evaluation "$HOLDOUT_EVALUATION_ID" \
  --outcome accepted \
  --approved-by 'human:alice' \
  --confirm

exp --workspace "$EXP_REPO" champion manifest --target encoder-prod
```

The challenger must be a validated Release for the same target. `accepted` and
`rolled_back` require a passing holdout; rollback may restore only the incumbent
displaced by the current champion-setting Promotion. Target chains cannot fork,
and the recorded incumbent must match the derived current Champion. No autonomy
mode or agent can supply human approval.

A Source-aware Champion manifest is a deployment input for a separate human
system, never canonical authority, an artifact store, a merge/push, or automatic
deployment instruction.

## Storage and review boundary

Large datasets, model/checkpoint bytes, artifact files, traces, and unbounded
logs remain in MLflow, DVC, or object storage. Git receives bounded summaries,
digests, selected metrics, exact Source/commit identities, and sanitized refs
only. Review the result locally without moving authority:

```bash
exp --workspace "$EXP_REPO" validate
exp --workspace "$EXP_REPO" context
exp --workspace "$EXP_REPO" ui
```
