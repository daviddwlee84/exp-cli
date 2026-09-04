# 從 Evidence 到 Promotion

Promotion 是 typed chain，不是 process success 的 shortcut：

```text
Try（optional exploration）
  -> Idea v2 -> qualified Plan v2 -> Queue
  -> Experiment -> Run -> clean formal Attempt v3
  -> supported Experiment conclusion -> Evaluation v2 -> Candidate v2
  -> validated Release（需要時加 combination evidence）
  -> sealed fresh holdout Evaluation -> named-human Promotion
  -> derived Champion manifest
```

Git commit、worker result JSON、output hash、MLflow run 與 artifact URI 都是 supporting
identities/observations；不能跳過 canonical gate。

Script 應 explicit 使用 dedicated workspace：

```bash
EXP_REPO="$HOME/src/my-application-experiments"
```

## 1. Explore，但不授予 promotion authority

用 `exp try run` 處理 bounded question。Clean 或 explicit captured dirty Try-owned
Attempts 只有在全部 terminal 後才能 conclude。有價值的 Try 由具名人類 adopt 成 Idea
v2：

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

Try 不能支持 Candidate。尤其 dirty Try 必須在 Idea qualification/Queue admission 後，
經 formal clean path 重跑。

## 2. Dispatch clean formal Attempt

Runtime v2 識別一個 execution Source 加 optional read-only Sources。每個 Source 必須
active、locally registered、精確符合 declared base/head/ChangeSet，且 clean。Dispatch
將 SourceSnapshots capture 進 Attempt v3，再把 explicit Project/root/scope authority 傳給
worker-job v2。

```bash
exp --workspace "$EXP_REPO" config trust
exp --workspace "$EXP_REPO" --source app daemon frontier
exp --workspace "$EXP_REPO" policy autonomy assisted \
  --confirm-auto-experiment
exp --workspace "$EXP_REPO" --source app daemon tick
# 或持續 reconcile，直到 terminal interrupt：
exp --workspace "$EXP_REPO" --source app daemon run --interval 5s
```

第一個 command 的 TTY wizard 可把 reviewed `.exp/runtime.json` exact digest 依
`runtime.dispatch` 核准。`daemon frontier` 不 contact Pueue；`tick`/`run` 會 contact。
Controller 在 Pueue submission 前，先用同一 transaction 建立 Experiment、Run、planned
Attempt，並完成 Plan/Queue transition。

Successful terminal Attempt 仍只是 operational evidence。Source 若 dirty、missing、
changed 或 untrusted，formal dispatch 在 scheduler start 前 fail/block；不 capture 或 clean
checkout。缺少 Pueue 會 block formal dispatch，但不影響 native Try；MLflow 仍 optional。

## 3. 以 evidence disposition closure Experiment

Closure request 列出每個 Run，並說明 evidence include/exclude。它在同一 canonical
transaction 記錄 scientific verdict、Plan completion、Findings 與 follow-up Ideas：

```bash
exp --workspace "$EXP_REPO" experiment close --input closure.json --json
```

只有屬於該 Experiment 的 Run 合法。Included Run 必須有 successful direct worker
Attempt。Comparability、protocol compliance 與 registered decision rule 決定 disposition；
operational success 不 automatic include Run。應準確使用 `supported`、`refuted`、
`inconclusive` 或 `invalid`。

保存 returned identities：

```bash
EXPERIMENT_ID='exp_replace_with_returned_id'
ATTEMPT_ID='attempt_replace_with_returned_id'
POOL_ID='pool_replace_with_returned_id'
```

## 4. 建立 typed scientific Evaluation

Evaluation 前先建立 EvaluationSpec。Name、unit、direction、optional threshold、dataset、
protocol、ResourcePool 與 finite budget 構成 comparable contract：

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

Candidate v2 要 evaluate concluded Experiment，並 explicit bind exact successful formal
Attempt v3：

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

只有 Attempt 是 terminal/successful、Run-owned formal Attempt v3，且其 Run 屬於
Experiment 時才建立 Evaluation v2。Evaluation time 不得早於 Attempt；metrics 必須
exactly cover spec，outcome 必須符合 thresholds。

MLflow 是 optional corroboration。`--mlflow-run-id` 額外要求 run `FINISHED`、selected
values match，且有 `--attempt` 時 `exp.attempt_id` 識別同一 Attempt：

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

Resulting sanitized ExternalRef 不 copy unrequested telemetry/artifact bytes。省略
`--attempt` 會刻意建立 Evaluation v1，即使 MLflow owner 存在；不能支持 Candidate v2。

## 5. 從同一 Attempt 建立 Candidate v2

```bash
exp --workspace "$EXP_REPO" candidate create \
  --title "Evaluated Source change" \
  --experiment "$EXPERIMENT_ID" \
  --evaluation "$EVALUATION_ID" \
  --attempt "$ATTEMPT_ID" \
  --json
CANDIDATE_ID='cand_replace_with_returned_id'
```

Candidate v2 要求：

- Experiment closed、concluded、`supported`；
- conclusion includes Attempt Run；
- Attempt 是 successful terminal Run-owned Attempt v3，且完成時間不晚於 conclusion；
- 每個 Attempt SourceSnapshot 都 clean；
- Evaluation `passed`、使用 scientific spec，且 Evaluation v2 綁定 exact same Attempt；
- 任何 MLflow owner 也指向該 Attempt。

Candidate 從 Attempt 複製每個 Source ID、exact head commit 與 sorted ChangeSet；不 copy
checkout path/artifact bytes。Candidate v1 只保留為 legacy path：explicit `--legacy`，或
old `--git-commit` 加 `--change` shape，要求 matching successful included Attempt v2。

## 6. Assemble 並 validate Release

Release 將 project-defined slot name 對應 Candidates。提供 strict
`exp.request.release-create/v1` document：

```bash
exp --workspace "$EXP_REPO" release create --input release.json --json
RELEASE_ID='rel_replace_with_returned_id'
```

Draft 可供 review；validated Release 要求在 sealed promotion-purpose EvaluationSpec 下
通過 Release-level Evaluation。如果 slots 有一個以上 distinct Candidate，Release 還必須
引用 closed/supported combination Experiment v2；其 `candidate_inputs` exactly match slots，
並有該 combination 的 passing scientific Evaluation。不同 Experiments 的 gain 不 assumed
additive。

## 7. Seal 並使用 fresh promotion holdout

先建立並 seal promotion-purpose EvaluationSpec；每個 metric 都要求 threshold：

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

PromotionSpec seal 後，使用 exact EvaluationSpec 對 validated challenger Release 建立
fresh Evaluation。此 Release-subject result 是 Evaluation v1，因 Evaluation v2 專供
formal Experiment Attempt ownership：

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

Holdout evaluation time 必須 strictly after PromotionSpec seal，且不能被另一個 Promotion
consume。PromotionSpec holdout budget 不得超過 EvaluationSpec budget。

## 8. Append human production decision

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

Challenger 必須是相同 target 的 validated Release。`accepted`/`rolled_back` 要求 passing
holdout；rollback 只能 restore 被 current champion-setting Promotion displacement 的
incumbent。Target chain 不能 fork，recorded incumbent 必須符合 derived current Champion。
任何 autonomy mode/agent 都不能提供 human approval。

Source-aware Champion manifest 是給另一個 human system 的 deployment input；絕非
canonical authority、artifact store、merge/push 或 automatic deployment instruction。

## Storage 與 review boundary

Large datasets、model/checkpoint bytes、artifact files、traces 與 unbounded logs 留在
MLflow、DVC 或 object storage。Git 只接收 bounded summaries、digests、selected metrics、
exact Source/commit identities 與 sanitized refs。可在不移動 authority 下 local review：

```bash
exp --workspace "$EXP_REPO" validate
exp --workspace "$EXP_REPO" context
exp --workspace "$EXP_REPO" ui
```
