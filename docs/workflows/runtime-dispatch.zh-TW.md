# 執行與派送

`exp` 有兩條 scientific authority 不同的 execution path：

- **Direct Try**：在單一 managed Source worktree 中執行 bounded local exploration。可以明確
  capture dirty state，但絕不能直接支持 Candidate。
- **Formal dispatch**：consume qualified Queue frontier 並透過 Pueue 執行。Runtime v2 可跨
  independent Source repositories execution，但只 admission clean exact SourceSnapshots。

兩條路徑都先 publish canonical Attempt intent，再 execution；job/host path 保持 private，terminal
fact 只有經 identity check 才匯入。Process success 與 provider state 都不是 scientific verdict。

選擇 path 前，先用 terminal guides 與 local status：

```bash
exp guide quick
exp guide research
exp workspace status
exp source status
exp config show
exp doctor
```

Default `doctor` 只做 executable lookup；`doctor --live` 才執行 explicit bounded
version/read-only local-service probe。缺少 MLflow、Pueue 或 `dev` 不會隱藏 commands，也不
block native Try；只有 formal `daemon tick`/`run` 真正 dispatch 時才需要 Pueue。

## 解析 Project、Source、config 與 trust

每個 invocation 先解析一個 canonical Project。該 Project 一般位於 dedicated private experiment
repository；既有 embedded Project 仍可 discovery。`--workspace` 依 UUID/path 選 Project，
`--source` 依 key/ID/prefix/display code 選一般 Source。缺省時由 physical Project marker 或最
specific registered Source 解析 context。

Source association 是 host-local；每次使用都依 Project/Source identity、Git-common filesystem
identity、immutable subdir 與 sanitized locator hints 重新驗證。Resolution 完成後才載入 layered
`exp.config/v1`。Repository 中 winning execution-bearing field 要求 exact-digest trust。
Runtime v2 還要求 raw `.exp/runtime.json` digest 的 `runtime.dispatch` receipt。詳見
[Configuration and Paths](../reference/configuration.md)。

## Direct Try

可從 registered Source directory 啟動 non-interactive clean Try：

```bash
exp try run \
  --title "Probe a bounded change" \
  --goal "Decide whether the direction merits a formal Experiment" \
  --allow 'src/**' \
  --timeout 20m \
  -- project-runner --config configs/probe.toml
```

`--` 後內容保持 argv；不經 shell。Executable 必須是 PATH basename 或 managed-worktree-relative
path。任何 argument 都不得是 POSIX、drive、UNC 或 `file:` absolute path；常見 attached flag value
（例如 `--config=/host/path`）也在此限制內，且不得嵌入 known workspace path。Try `cwd` 相對
Source declared subdir。空 `--allow` set 使 managed worktree read-only。Dirty seed path 是 baseline
state，不是 implicit write permission；除非 explicit user allow-glob match，否則其 existence、mode、
size 與 bytes 都必須保持 captured state。

### Clean 與 dirty registration

Clean mode 要求不存在 tracked、untracked、dirty-submodule 或 hidden-index state。它在目前 full
HEAD 記錄 clean `capture-v1` SourceSnapshot；`base_commit == head_commit`、允許 empty committed
ChangeSet、reproducibility 為 exact，且沒有 seed bundle。

Dirty state 必須 literal request：

```bash
exp try run \
  --title "Reproduce a local probe" \
  --goal "Bound the effect before a clean rerun" \
  --dirty=capture \
  --allow 'src/**' \
  -- project-runner --config configs/probe.toml
```

`--dirty` 只接受 `capture`，絕不 inferred。Capture 僅供 Try 使用且有 bounds：full-index binary
patch、regular untracked file bytes/modes/hashes、每個 dirty path 最終 existence/mode/size/digest，
以及 clean direct submodule commits，都保存於 XDG cache 中的 private `exp.source-seed/v1`
bundle。系統拒絕 Source `subdir` 外的 path、rename/copy
ambiguity、ignored/unmerged/hidden index state、dirty/nested submodule、symlink/special node，以及
超過 hard limits 的內容。Canonical Attempt 只含 bounded dirty summary、dirty/snapshot digests 與
sorted Git-root-relative paths，不含 raw bytes/host paths。

### Ordering 與 recovery

Direct execution 經下列明確 boundary：

1. resolve 並 verify config/Source；
2. capture clean 或 dirty Source state；
3. 在 Source revision guard 下，atomically publish open Try 與 planned Attempt v3；
4. refresh derived projections；
5. prepare/reuse deterministic native Git worktree 並 verify exact seed；
6. enqueue 並依 exact ID claim 一個 private `try-direct` job；
7. 推進 canonical Attempt queued/starting/running state；
8. 在 final Source/config/worktree preflight 與 process spawn 期間持有 workspace lock；
9. freeze bounded result、publish aggregate-bounded worker-terminal v2 marker、finish private job，
   並只在 positive native workspace inspection 後把 terminal state/result digests 匯入 Attempt；
10. refresh projections。

Canonical publication 在 job/worktree 前，所以 crash 可留下 planned Attempt 而未 execution。
`exp try resume TRY` 可以 resume 可證明尚未啟動的 Attempt、從 valid marker 修復 SQLite，或匯入
late marker；絕不重新 invocation uncertain execution。帶 live lease 的 running job 回報
in-progress。Default direct lease 是兩分鐘，並每三分之一 interval heartbeat；expired lease 且沒有
marker 時，Attempt 變成 `unknown`，因此一般 local crash recovery latency 有明確上限。

`exp try reconcile TRY --attempt ATTEMPT --abandon-uncertain --reason TEXT --confirm` 會再次檢查
marker/live lease，之後才記錄 human cancellation。`exp try retry TRY` 只有最新 Attempt terminal
後才建立新 Attempt；否則 delegation 至 resume。Retry 保留 Try、Source state、cwd、argv、execution
Source、config digest、MLflow profile identity 與 one-successor `retry_of` lineage；Source/config
drift 會 block。

只有全部 owned Attempts terminal 後，Try 才能 finish/abandon。Selected result digest 必須由 owned
Attempt 產生。`try adopt` 要求人類，並 atomically create Idea v2。看似有價值的 dirty Try 仍需
clean formal rerun。

### Cleanup

```bash
exp try status TRY
exp try cleanup TRY --confirm
```

Status 只 observation：組合 canonical record 與 optional job、marker、native worktree inspection，
不會啟動工作，也不會建立、repair 或 migrate operational database。未指定 Try 的 `try list` 與
`try status` 預設最多回傳 100 個 Try，可用 `--limit 1..1000` 與 `--offset`；explicit single-Try
selection 不分頁。Cleanup 獨立處理每個 terminal direct Attempt。Native Git 正常移除 clean-at-base
worktree；dirty seed 則在 guarded force removal 前 verify 完整 seed 兩次。之後才刪 authenticated
seed bundle，並在 canonical record 標示 `cleanup_completed`。Changed/unverifiable worktree 會被
拒絕。Cleanup 絕不移除 canonical Try/Attempt、branch、commit、operation row 或 terminal/result
marker，也可能回傳 truthful partial result。

## Formal dispatch

先在不接觸 Pueue 的情況下 inspect canonical frontier，再執行一次或持續 pass：

```bash
exp daemon frontier
exp daemon status
exp policy autonomy assisted --confirm-auto-experiment
exp daemon tick
# 或：exp daemon run
```

`manual`／`shadow` 顯示 frontier 但不 admission；`assisted`／`limited` 在 explicit policy
transition 後允許 dispatch。Named ResourcePool 以 units enforcement capacity；只有另一 lane 沒有
eligible work 時，weighted exploit/explore counter 才可 borrow idle capacity。

### Runtime v1 與 v2 保持分離

`.exp/runtime.json` 將 ResourcePool ID 綁定 Pueue group/label namespace，並把 Plan ID 綁到
executable contract。兩個 schema 都是 strict non-secret JSON：

- `exp.runtime/v1` 是 embedded compatibility route。它驗證單一 experiment repository 的
  `main` 或 `registered_worktree`、top-level base/head/ChangeSet，並建立 Attempt v2 加
  worker-job v1。
- `exp.runtime/v2` 指定一個 writable execution Source 與零個以上 read-only Sources。每個選
  `main`、`registered_worktree` 或 `managed_worktree`，並 pin full base/head IDs 與 exact
  ChangeSet；建立 Attempt v3 與 worker-job v2。

V2 `cwd`/expected-output path 相對 execution Source canonical subdir；ChangeSet 仍相對 Source Git
root。Empty ChangeSet 只有在 base=head 的 explicit no-change observation 才合法。每個 Source
必須 active、canonically declared、locally registered 且 unique。

Frontier prepare 前，runtime v2 重新檢查 raw runtime digest trust、effective execution-Source
config、workspace-backend selection、Source record/association、exact object format/HEAD/base
ancestry/ChangeSet、clean status 與 optional managed-worktree postcondition。每個 Source 都 capture
成 clean SourceSnapshot。Dirty state 是 formal gate failure，不是 runtime v2 capture mode。
Recovered outbox submission 前若 state missing/changed，unstarted private job 會 terminalize 成
`unknown`，canonical Attempt 標成 `blocked`，不送 Pueue。

### Canonical-before-operational admission

對單一 exact Queue frontier，controller atomically：

1. 建立並 lock active Experiment design；
2. 建立 intended Run；
3. 建立帶 Queue/Pool/lane/dispatch identity 的 planned Attempt v2 或 v3；
4. 把 Plan 標為 started 並指向 Experiment；
5. 精確移除該 Queue entry 並增加 Queue revision。

之後才 atomically enqueue/claim private job 與 submission outbox，再經 fenced lease 向 Pueue
submit。Stable group/label identity 支援 recovery，不必從 task ID 猜測。只有 submission accepted
後才 attach Pueue task reference。

### V2 explicit worker authority

Pueue 接收 argv，絕不接 shell command。Runtime v2 worker argv 包含：

```text
--canonical-root <exact experiment Git root>
--project <Project UUID>
--scope <checkout-local canonical scope>
--job <private job ID>
--fencing-token <positive token>
```

Hidden worker 要求前三項一起出現。它重新 discover exact Project/root、重新計算 scope、開啟該
Project operation store；先只讀 metadata authority 並驗證 job kind/role/state/fencing/scope，之後
才 load payload。Payload 必須符合 Project、scope、Attempt、writable execution Source、path-bound
checkout identity 與每個 sorted Source binding。所有 non-execution Sources 必須 read-only，所有
snapshots 必須 clean。Source checkout、Pueue working directory 或 copied job payload 因此不能選擇
canonical authority。

Spawn 前，native Git/Source capture 立即 revalidate 每個 binding；process success 後再次驗證
read-only Sources。Worker 使用 minimal environment 加 explicit non-sensitive names；Pueue route
拒絕 `secret_env` 與 credential-sensitive allowlist name。Formal v2 除共用的 `EXP_JOB_ID`、
`EXP_ATTEMPT_ID`、assigned `EXP_RESULT_PATH` 外，還提供 `EXP_PROJECT_ID`、
`EXP_CANONICAL_SCOPE`、`EXP_EXECUTION_SOURCE`。

### Marker 與 outbox recovery

Worker freeze bounded result file、hash expected regular outputs，並在 update SQLite 前 publish fsynced
terminal marker。若 publication 停在 valid `.tmp` marker 加 frozen result，restart 可 verify/promote。
Final marker 可修復 running SQLite row，或在 database loss 後匯入。Marker/job identity、fencing、
schema、timing、result digest 與 optional MLflow ownership 必須相符，否則 recovery fail closed。
Replay 絕不重跑 workload。

Missing marker 不證明未 execution。Scheduler state 單獨只能推進 bounded operational state，絕不
closure Experiment 或建立 Evaluation。Unknown/ambiguous case 保持 blocked/unknown，等待 explicit
human review。

## Workspace 與 MLflow provider behavior

`native_git` 是 mandatory workspace byte/lifecycle authority。Optional `dev_cli` 可 discovery，但
prepare/inspect/cleanup/open/handoff/retire 都 compiled unsupported，因為沒有 schema-versioned
exact-path machine receipt。Trusted policy 可 explicit fallback 到 native Git；provider-local ID 不會
變成 canonical。不論 requested provider，final inspection/cleanup 都由 native Git 執行。

可選 trusted MLflow profile 做 read-only attachment observation。Formal Pueue runtime 只接受沒有
environment binding 的 profile；需要 credential 的 workload 應自行使用 broker。先 inspect/trust
profile，再把相同 explicit selection 帶到 validation/dispatch：

```bash
EXP_REPO="$HOME/src/my-application-experiments"
exp --workspace "$EXP_REPO" --source app config explain
exp --workspace "$EXP_REPO" --source app config trust
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon frontier
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon run --interval 5s
```

Workload 建立／logging run，並可在 bounded result 寫 `mlflow_run_id`。Provider absence、missing
assertion 或 ownership mismatch 只產生 unavailable/unverified optional observation，不會撤銷
process success。只有 verified `exp.attempt_id` match 才成為 sanitized Attempt ExternalRef。
Artifact URI/bytes 仍由 MLflow 擁有，不會成為 Evaluation 或 Promotion。

Large datasets、model/checkpoint bytes、artifact files、traces 與 unbounded logs 留在 MLflow、DVC
或 object storage。Canonical Git records 只接收 bounded summaries/digests、exact Source/commit
identities 與 sanitized references，不接收那些 bytes、credentials、raw environments 或 host paths。

## Formal evidence gate

Successful formal process 仍不是 Candidate。Promotion-bearing chain 要求 Experiment include 該 Run 並
以 supported closure；Evaluation v2 綁定 exact successful clean Attempt v3；Candidate v2 精確複製
Attempt Source IDs/head commits/ChangeSets。詳見
[Evidence to Promotion](evidence-to-promotion.md)。

Resume 使用 `exp context`；比較 conceptual routes 使用 `exp guide`；local Source/record completion
見 `exp completion --help`；read-only terminal view 使用 `exp ui`。這些 commands 都不授予
scientific/promotion authority。
