# 核心研究流程

Core workflow 從一個 resolved Project/Source 開始，再選 quick Try、rigorous Experiment
或 promotion。Dedicated-repository wizard 與完整 runtime/profile setup 請先看
[快速開始](../getting-started.md)。

## 1. Resolve canonical Project、Source、config 與 trust

```bash
SOURCE_REPO="$(git rev-parse --show-toplevel)"
EXP_REPO="$(dirname "$SOURCE_REPO")/$(basename "$SOURCE_REPO")-experiments"

exp --workspace "$EXP_REPO" --source app workspace status
exp --workspace "$EXP_REPO" source status app
exp --workspace "$EXP_REPO" --source app config show
exp --workspace "$EXP_REPO" --source app config explain
exp --workspace "$EXP_REPO" context
```

Canonical records 通常位於 dedicated private experiment repository。每個 code/data
repository 或 governed subdirectory 是一般 canonical Source；local clone path 是 private
association。`--workspace` 依 Project UUID/path selection，`--source` 依 key/ID/display code
selection。絕不可把 current-directory convenience、submodule、config 或 provider catalog
當成 Source authority。

用 `source add` publish 另一個 Source；用 `source register` repair moved clone；用
`config trust` 在單一 exact digest 核准 execution-bearing repository config。Configuration
不能 redirect 已 resolved Project/Source。

## 2. 選擇 evidence level

| Question | Route | 第一個 command | Required end state |
|---|---|---|---|
| 這方向值得更多工作嗎？ | Quick exploratory Try | `exp try run` | Human finish/abandon；optional adopt 成 Idea v2 |
| Constrained resources 應測試此 claim 嗎？ | Rigorous Experiment | `exp idea add` | Priced Plan、Queue、clean formal Attempt v3、typed Evaluation |
| Proven work 應到某 target 嗎？ | Promotion | `exp candidate create` | Validated Release、sealed fresh holdout、named-human Promotion |

Terminal 使用相同順序：`exp guide quick`，接著 `exp guide research`，再
`exp guide promotion`。

## 3. 用 managed native-worktree Try probe uncertainty

Clean read-only Try 只需要 Git：

```bash
exp --workspace "$EXP_REPO" --source app \
  --workspace-backend native_git try run \
  --title "Inspect the clean Source snapshot" \
  --goal "Decide whether this direction merits a formal Experiment" \
  --timeout 5m \
  -- git status --short
```

Bounded change 要加 narrow Source-relative `--allow` globs。Dirty Source state 除非
explicit capture，否則拒絕：

```bash
exp --workspace "$EXP_REPO" --source app try run \
  --title "Inspect a captured local edit" \
  --goal "Decide whether to rerun the direction cleanly" \
  --dirty=capture \
  --allow 'src/**' \
  --timeout 5m \
  -- git diff --stat
```

Try/Attempt v3 在 execution 前先成為 canonical；private job、worktree、seed、marker
state 保持 local。Dirty seed bytes 留在 XDG cache，Git 只接收 bounded
paths/summaries/digests。使用 returned typed ID inspect 並 conclude：

```bash
TRY_ID='try_replace_with_returned_id'
exp --workspace "$EXP_REPO" try status "$TRY_ID"
exp --workspace "$EXP_REPO" try finish "$TRY_ID" \
  --summary "The direction merits a controlled clean study" \
  --no-results \
  --confirm
exp --workspace "$EXP_REPO" try adopt "$TRY_ID"
```

最後一個 command 開啟 TTY classification/review wizard。`try resume` 只 execution
可證明 unstarted 的 work，或匯入 verified marker；不猜測。有價值的 Try 變成 Idea，
不是 Candidate。Dirty Try 必須經 formal execution clean rerun。

## 4. Capture、qualify、price、rank durable direction

只有值得 preservation 的方向才建立 Idea。它可以由 human author、是 prior evidence 的
child，或由 `try adopt` 產生；尚未承諾 compute。

```bash
exp --workspace "$EXP_REPO" idea add \
  --title "Evaluate cosine decay after warmup" \
  --summary "Measure late-stage optimizer stability" \
  --lane explore \
  --cluster optimizer
IDEA_ID='idea_replace_with_returned_id'

POOL_ID='pool_replace_with_returned_id'
exp --workspace "$EXP_REPO" idea qualify "$IDEA_ID" \
  --payoff-summary "Improve validation macro-F1" \
  --payoff-metric macro_f1 \
  --payoff-unit score \
  --probability 0.45 \
  --impact 0.02 \
  --information-value 0.005 \
  --resource "$POOL_ID":1:3
PLAN_ID='plan_replace_with_returned_id'

QUEUE_ID='queue_replace_with_returned_id'
exp --workspace "$EXP_REPO" queue insert "$QUEUE_ID" "$PLAN_ID" --pool "$POOL_ID"
```

Configured fresh agent 可用 `idea develop` propose 完整 Plan，但
schema/revision/Policy/resource checks 仍是 authority。Numeric Queue insertion
transparent；`--agent` 加 listwise advice/order-swapped adjacent battles。Disagreement 或
low confidence 不改 order。Plan revision/Finding belief digest 被 pin，因此 evidence
change 使 entry stale，不 silent rerank。

## 5. Dispatch 一個 clean formal evidence unit

Experiment lock hypothesis、baseline、comparability、success criteria 與 decision rule。
Run 是 intended evidence；Attempt 是一次 operational execution。

Independent Sources 應 author `exp.runtime/v2`：包含一個 writable execution Source、
optional read-only Sources、full base/head commits、exact ChangeSets、argv、expected
outputs 與 Pool/Pueue route。用 exact `runtime.dispatch` receipt 核准 raw runtime file。
可選 value-free trusted MLflow profile 做 optional observation：

```bash
exp --workspace "$EXP_REPO" config trust
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon frontier
exp --workspace "$EXP_REPO" policy autonomy assisted \
  --confirm-auto-experiment
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon run --interval 5s
```

`daemon frontier` provider-free。`daemon tick`/`run` 需要 Pueue；native Try 不需要。
Formal runtime v2 只 capture clean exact SourceSnapshots，並將 explicit
Project/root/scope authority 傳給 worker v2。Dirty、missing、stale 或 untrusted Source
state 在 scheduler execution 前就 block。Process、Pueue 或 MLflow success 不是
scientific conclusion。

完整 v2 JSON/recovery protocol 見[執行與派送](runtime-dispatch.md)。

## 6. Closure、evaluate、package exact evidence

用 explicit included/excluded Run dispositions closure Experiment；再把 scientific
Evaluation v2/Candidate v2 綁定同一 successful clean formal Attempt v3：

```bash
exp --workspace "$EXP_REPO" experiment close --input closure.json --json

EXPERIMENT_ID='exp_replace_with_returned_id'
ATTEMPT_ID='attempt_replace_with_returned_id'
SPEC_ID='evalspec_replace_with_returned_id'

exp --workspace "$EXP_REPO" evaluation create \
  --title "Scientific result" \
  --spec "$SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --attempt "$ATTEMPT_ID" \
  --outcome passed \
  --metric macro_f1=0.834:score \
  --summary "Passed the registered threshold"
EVALUATION_ID='eval_replace_with_returned_id'

exp --workspace "$EXP_REPO" candidate create \
  --title "Evaluated Source change" \
  --experiment "$EXPERIMENT_ID" \
  --evaluation "$EVALUATION_ID" \
  --attempt "$ATTEMPT_ID"
```

Candidate v2 要求 supported conclusion include Attempt Run、passing scientific
Evaluation v2 綁定 exact same Attempt，以及 clean SourceSnapshots；並複製 exact Source
IDs/head commits/ChangeSets。MLflow 可 corroborate selected values/ownership，但
telemetry/artifacts 不取代 Evaluation。

## 7. Branch、promote、inspect，不重寫 history

Follow-up question 建 child Ideas。Independently successful Candidates 不 assumed
additive；multi-Candidate Release 需要自己的 combination Experiment/passing Evaluation。
Validated Release 只能透過 sealed fresh non-reused holdout 與 named human append-only
Promotion 挑戰一個 target。詳見[從 Evidence 到 Promotion](evidence-to-promotion.md)。

```bash
exp --workspace "$EXP_REPO" validate
exp --workspace "$EXP_REPO" render --check
exp --workspace "$EXP_REPO" context
exp --workspace "$EXP_REPO" ui
```

`exp ui`、generated projections、Champion manifests 是 read-only/derived views；不能
merge、push、deploy、approve 或成為另一個 source of truth。Large datasets、
model/checkpoint bytes、artifact files、traces 與 unbounded logs 留在 MLflow、DVC 或
object storage；只有 bounded summaries、digests、exact identities 與 sanitized
references 進入 Git。
