# 快速開始

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

本頁帶新使用者從一個 code repository 走到三種實用結果之一：bounded exploratory
Try、queued formal Experiment，或可進入 promotion workflow 的 clean Candidate。

## 需求與 optional tools

Setup 與 native Try 的必要工具：

- Git；
- 從本 source tree 安裝時，需要 Go 1.26.4（或 `go.mod` 宣告版本）與 Make。

依 operation 才需要的 optional tools：

- 只有 formal daemon dispatch 才需要 Pueue 4.x；
- 只有 explicit/profile-selected read-only run observation 才需要 MLflow CLI；
- `dev` 是 future optional workspace backend；其 lifecycle 目前 unsupported，
  `native_git` 永遠可用。

```bash
exp doctor
exp doctor --live
```

Default `doctor` 只做 local executable lookup。`--live` 明確執行 bounded version 與
read-only local-service probes；不 install、login、寫 config、start service 或 execute
workload。缺少 MLflow、Pueue 或 `dev` 時 action 仍可 discovery，也不會 block native
Try；只有真正需要該工具的 operation unavailable。

## 從原始碼安裝

```bash
git clone https://github.com/daviddwlee84/exp-cli.git
cd exp-cli
make install
exp --version
```

`make install` 寫入 `${PREFIX:-$HOME/.local}/bin/exp`，並 link embedded Agent
Skill。請確認該目錄已加入 `PATH`。

## 1. 建立 dedicated experiment repository

建議 layout 將 private canonical research history 與受研究的 Source repositories
分開。

### TTY wizard（建議）

進入要研究的 existing Git repository 後執行：

```bash
cd "$HOME/src/my-application"
exp init
```

在 terminal 上 bare `exp init` 預設選 `dedicated` flow。任何 write 前，會提出 sibling
repository、inspect initial Source，並 review Source subdirectory、locator risk、exact
effects 與 paths。不會建立 remote、commit、submodule 或 push。

### Explicit flags

以下 copy-paste-safe 形式由 current Git root 推導 sibling target：

```bash
SOURCE_REPO="$(git rev-parse --show-toplevel)"
EXP_REPO="$(dirname "$SOURCE_REPO")/$(basename "$SOURCE_REPO")-experiments"

exp init \
  --dedicated-repo "$EXP_REPO" \
  --create \
  --source-repo "$SOURCE_REPO" \
  --source-key app \
  --source-title "Application source" \
  --source-subdir . \
  --source-tag code \
  --name "Application research" \
  --confirm \
  --confirm-local-only
```

`--create` 只接受 missing/empty target（target 已在 rerun 前成為 exact Git root 時也
無害）。有 sanitized remote locator 可識別 Source 時可省略 `--confirm-local-only`；
local-only clone 保留此 flag 代表 explicit acknowledgement。Initialization 會 publish
Project v1/Source v1、register 兩筆 private local associations，並 refresh projections。

若 code/research 要共用同一 Git repository，請使用 explicit embedded compatibility
fast path：

```bash
exp init --name "Application research"
```

## 2. Register Sources 與 subdirectories

Dedicated initialization 後 initial Source 已存在。從任一 repository inspect 其 canonical
identity 與 host-local association：

```bash
exp --workspace "$EXP_REPO" source list
exp --workspace "$EXP_REPO" source show app
exp --workspace "$EXP_REPO" source status app
exp --workspace "$EXP_REPO" --source app workspace status
exp --start-dir "$SOURCE_REPO" context
```

Source immutable `subdir` 是 Git clone 內受 governance 的 semantic root；`.` 代表 Git
root。用 TTY wizard 新增另一個 repository/subdirectory：

```bash
exp --workspace "$EXP_REPO" source add
```

或使用完整 explicit flags：

```bash
DATA_REPO="$HOME/src/shared-data"

exp --workspace "$EXP_REPO" source add \
  --key data \
  --title "Shared data definitions" \
  --repo "$DATA_REPO" \
  --subdir datasets \
  --tag data \
  --confirm
```

Clone 沒有 sanitized remote 時加 `--confirm-local-only`。Clone moved/recreated 後，只
repair private association；canonical Source 不變：

```bash
exp --workspace "$EXP_REPO" source register data --repo "$DATA_REPO"
exp --workspace "$EXP_REPO" source status data
```

Canonical Source record 絕不包含 local clone path。

## 3. Inspect config 與 exact-digest trust

只有 Project/Source authority 解析後才載入 configuration。Inspect candidate paths、
effective non-secret values、winning provenance 與 local receipts：

```bash
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config path
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config show
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config explain
exp config list
```

Repository/Source/subdirectory 的 execution-bearing values 在 exact file digest 依 relevant
capability 核准前都是 inert。Terminal 上的 safe path 是 review wizard：

```bash
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config trust
```

Trust 會依 resolved authority context 評估。選擇 `app` 時，canonical repository layer
也會取得綁定 `app` 的 receipt；workspace-only approval 不會授權同一份 bytes 給所有
Sources。Automation 請使用 `config explain` 印出的 exact path/digest、保留相同 selectors，
而且只命名要核准的 capability：

```bash
CONFIG_PATH="$EXP_REPO/.exp-cli/config.toml"
CONFIG_DIGEST='sha256:replace_with_the_current_digest'

exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config trust \
  --path "$CONFIG_PATH" \
  --digest "$CONFIG_DIGEST" \
  --capability mlflow.profile \
  --confirm
```

任何 byte change 都要求新 receipt。`exp config list` 是 sanitized output；不洩漏
Git-common path 或 secret value。

## 4. 選擇 flow

| Decision | 使用 | 第一個 command | Promotion authority |
|---|---|---|---|
| 「這方向值得 formalize 嗎？」 | Quick exploratory Try | `exp try run` | 無；finish 後可選擇 adopt 成 Idea |
| 「Scarce compute 應測試此 registered claim 嗎？」 | Rigorous Experiment | `exp idea add`，再 qualify/queue | Clean Attempt v3 可支持 Evaluation v2/Candidate v2 |
| 「此 validated change 應進到 target 嗎？」 | Promotion | `exp candidate create`，再做 Release/holdout | 只有 sealed fresh holdout 加 named human |

Terminal guidance 使用相同順序：

```bash
exp guide setup
exp guide workspaces
exp guide config
exp guide quick
exp guide research
exp guide promotion
```

## 5. 在 managed native worktree 執行 clean/dirty Try

### Clean read-only Try

此 example 只需要 Git；fully explicit command 不 prompt：

```bash
exp --workspace "$EXP_REPO" \
  --source app \
  --workspace-backend native_git \
  try run \
  --title "Inspect the clean Source snapshot" \
  --goal "Confirm the registered checkout is ready for a controlled probe" \
  --timeout 5m \
  -- git status --short
```

Try/planned Attempt v3 在 execution 前先成為 canonical。`exp` 在 XDG data 下建立
managed native Git worktree、claim private job、直接 invoke argv，並匯入 bounded
terminal result。`--` 後的 argument 不經 shell。空 `--allow` set 使 workload changes
read-only。

Bounded code-changing probe 請加 narrow Source-relative allowlists：

```bash
exp --workspace "$EXP_REPO" --source app try run \
  --title "Probe cosine decay" \
  --goal "Decide whether the optimizer direction merits a formal Experiment" \
  --allow 'src/**' \
  --allow 'configs/**' \
  --timeout 20m \
  -- project-runner --config configs/probe.toml
```

### Explicit dirty capture

除非 literal 提供 `--dirty=capture`，dirty state 會被拒絕。若 intended local edit 位於
`src/`，以下 read-only inspection 會查看 captured replay：

```bash
exp --workspace "$EXP_REPO" --source app try run \
  --title "Inspect the captured local edit" \
  --goal "Decide whether the dirty direction deserves a clean rerun" \
  --dirty=capture \
  --allow 'src/**' \
  --timeout 5m \
  -- git diff --stat
```

Capture 只有 Try 可用且 bounded。Raw patch/untracked bytes 留在 authenticated private
XDG cache seed；canonical Git records 只含 dirty summary、sorted paths 與 digests。Dirty
Try 不能支持 Candidate v2。

### Inspect、recover、finish、adopt

使用 `try run` 回傳的 exact typed ID：

```bash
TRY_ID='try_replace_with_returned_id'

exp --workspace "$EXP_REPO" try status "$TRY_ID"
exp --workspace "$EXP_REPO" try resume "$TRY_ID"
```

`resume` 只能執行可證明 unstarted 的 Attempt，或匯入 verified durable marker；不會猜測
並重新 execution uncertain work。所有 owned Attempts terminal 後，選 owned results 或
explicit none 再 finish：

```bash
exp --workspace "$EXP_REPO" try finish "$TRY_ID" \
  --summary "The direction merits a registered clean study" \
  --no-results \
  --confirm
```

可執行 `exp try adopt "$TRY_ID"` 使用 TTY wizard，把 reviewed conclusion adopt 成 Idea
v2；或完整 explicit：

```bash
exp --workspace "$EXP_REPO" try adopt "$TRY_ID" \
  --title "Evaluate cosine decay cleanly" \
  --summary "Run the direction under a registered comparable protocol" \
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

Cleanup 是 separate operation；不移除 canonical records、retained branch 或 terminal
markers：

```bash
exp --workspace "$EXP_REPO" try cleanup "$TRY_ID" --confirm
```

## 6. Configure value-free MLflow profile

建立 canonical profile 供 optional read-only observation。Formal Pueue run 要求
**value-free** profile，因為 Pueue 會保存 task environment；workload 必須從自己的
broker 取得 credentials。

```bash
CONFIG_PATH="$EXP_REPO/.exp-cli/config.toml"
mkdir -p "$(dirname "$CONFIG_PATH")"
cat >"$CONFIG_PATH" <<'TOML'
schema = "exp.config/v1"

[defaults]
mlflow_profile = "formal-observer"

[mlflow.profiles.formal-observer]
context = "research"
binary = "mlflow"
timeout = "30s"
default_metrics = ["macro_f1", "validation_loss"]
TOML

exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config explain
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config trust
```

Review 後，在 trust wizard 選此 file 並核准 `mlflow.profile`。Direct Try 可改用帶
trusted environment-name bindings 的 profile；value 只在 process start 時解析，並會
redact。Formal runtime v2 拒絕這種 binding。缺少 MLflow 只會把 optional observation
記為 unavailable，不會撤銷 workload success。

## 7. Price 並 queue formal work

Policy 以 `manual` 開始，所以 setup 不會意外 dispatch：

```bash
exp --workspace "$EXP_REPO" policy init

exp --workspace "$EXP_REPO" pool add \
  --title "Local GPUs" \
  --capacity 2 \
  --unit gpu \
  --bottleneck accelerator

POOL_ID='pool_replace_with_returned_id'
exp --workspace "$EXP_REPO" queue create --pool "$POOL_ID"
QUEUE_ID='queue_replace_with_returned_id'

exp --workspace "$EXP_REPO" idea add \
  --title "Evaluate cosine decay after warmup" \
  --summary "Measure late-stage optimizer stability" \
  --lane explore \
  --cluster optimizer
IDEA_ID='idea_replace_with_returned_id'

exp --workspace "$EXP_REPO" idea qualify "$IDEA_ID" \
  --payoff-summary "Improve validation macro-F1" \
  --payoff-metric macro_f1 \
  --payoff-unit score \
  --probability 0.45 \
  --impact 0.02 \
  --information-value 0.005 \
  --resource "$POOL_ID":1:3
PLAN_ID='plan_replace_with_returned_id'

exp --workspace "$EXP_REPO" queue insert "$QUEUE_ID" "$PLAN_ID" --pool "$POOL_ID"
exp --workspace "$EXP_REPO" context
```

將每個 quoted placeholder 換成它上方 command 印出的 complete typed ID。Queue order
會 pin ranking 使用的 Plan revision/Finding beliefs。

## 8. Configure runtime v2 並執行 daemon

Runtime v2 指定一個 writable execution Source、optional read-only Sources、exact full
commits/ChangeSets、argv、expected outputs，以及 Pool-to-Pueue route。它是 strict JSON，
與 layered config 分離。

先取得 exact identities：

```bash
exp --workspace "$EXP_REPO" source show app
SOURCE_ID='src_replace_with_returned_id'
BASE_COMMIT='replace_with_full_lower_case_base_commit'
HEAD_COMMIT='replace_with_full_lower_case_head_commit'
RUNNER='/absolute/path/to/project-runner'
RUNTIME_PATH="$EXP_REPO/.exp/runtime.json"
mkdir -p "$(dirname "$RUNTIME_PATH")"
```

以下 example 描述一筆 committed change。寫入前，請替換 quoted IDs、commits、runner
與 exact Git-root-relative ChangeSet：

```bash
cat >"$RUNTIME_PATH" <<EOF
{
  "schema_version": "exp.runtime/v2",
  "pools": {
    "$POOL_ID": {
      "pueue_group": "gpu",
      "label_prefix": "exp-gpu-"
    }
  },
  "plans": {
    "$PLAN_ID": {
      "execution_source": "$SOURCE_ID",
      "read_only_sources": [],
      "executable": "$RUNNER",
      "argv": ["--config", "configs/cosine.toml"],
      "checkout": "managed_worktree",
      "cwd": ".",
      "timeout": "3h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "$BASE_COMMIT",
      "head_commit": "$HEAD_COMMIT",
      "change_set": ["configs/cosine.toml"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
EOF
```

No-change observation 要使用 equal base/head、empty `change_set`，並加
`"observational_no_change": true`。絕不可在 runtime JSON 放 credential。

Review raw runtime file digest，並以 `runtime.dispatch` trust。TTY wizard 會把
`.exp/runtime.json` 與 layered config 分開 discovery：

```bash
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config trust
exp --workspace "$EXP_REPO" config list
```

確認 named Pueue group 已存在後，先在不 contact Pueue 的情況下 validate canonical
frontier；再 explicit enable formal experiment dispatch，執行 one pass 或 continuous
daemon：

```bash
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon frontier

exp --workspace "$EXP_REPO" policy autonomy assisted \
  --confirm-auto-experiment

exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon tick

exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon run --interval 5s
```

用 terminal interrupt 停止 foreground `daemon run`。Runtime v2 只 admission clean exact
SourceSnapshots；dirty/missing/stale/untrusted state 在 scheduler execution 前就 block。
缺少 Pueue 會 block daemon dispatch，但不影響 native Try。

Optional `dev_cli` backend 可 discovery：

```bash
exp workspace backend list --probe
exp --workspace "$EXP_REPO" workspace backend status --probe
```

在 `dev` 提供 schema-versioned exact-path machine capability receipts 前，其
prepare/open/handoff/retire lifecycle 都 compiled unsupported。Actions 仍 visible；trusted
fallback 可選 `native_git`，且 native Git 保有 byte、inspection、cleanup authority。

## 9. 建立 Evaluation v2 與 Candidate v2

Formal Attempt success 後，先用 explicit included/excluded Run dispositions 與 supported
scientific conclusion closure Experiment：

```bash
exp --workspace "$EXP_REPO" experiment close --input closure.json --json

EXPERIMENT_ID='exp_replace_with_returned_id'
ATTEMPT_ID='attempt_replace_with_returned_id'

exp --workspace "$EXP_REPO" evaluation spec create \
  --title "Scientific validation" \
  --purpose scientific \
  --dataset validation-v3 \
  --protocol "fixed preprocessing and five seeds" \
  --metric macro_f1:score:maximize:0.82 \
  --pool "$POOL_ID" \
  --budget-hours 4
EVALUATION_SPEC_ID='evalspec_replace_with_returned_id'

exp --workspace "$EXP_REPO" evaluation create \
  --title "Scientific result" \
  --spec "$EVALUATION_SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --attempt "$ATTEMPT_ID" \
  --outcome passed \
  --metric macro_f1=0.834:score \
  --summary "Passed the registered scientific threshold"
EVALUATION_ID='eval_replace_with_returned_id'

exp --workspace "$EXP_REPO" candidate create \
  --title "Evaluated Source change" \
  --experiment "$EXPERIMENT_ID" \
  --evaluation "$EVALUATION_ID" \
  --attempt "$ATTEMPT_ID" \
  --json
```

只有 exact successful clean formal Attempt v3、且其 Run 屬於該 Experiment 時，提供
`--attempt` 才建立 Evaluation v2。Candidate v2 要求同一 Attempt、include 其 Run 的
supported conclusion、passing scientific Evaluation v2 與 clean snapshots；它複製 exact
Source IDs、head commits 與 ChangeSets。Optional MLflow attachment 可 corroborate selected
values：

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

MLflow 絕不取代 Evaluation。接著閱讀
[從 Evidence 到 Promotion](workflows/evidence-to-promotion.md)，完成 validated Release、
sealed fresh holdout 與 named-human production gate。

## 10. 用 context、help、completion 與 UI resume

```bash
exp --workspace "$EXP_REPO" context
exp guide
exp guide quick
exp --help
exp try --help
exp completion --help
```

在 current shell 暫時啟用 completion：

```bash
# Bash
source <(exp completion bash)

# Zsh（請在 zsh 中執行）
source <(exp completion zsh)
```

Completion 只讀 local records、associations 與 config；不 probe provider 或 resolve secret
value。它可完成 Source keys、typed record IDs、workspaces、profiles 與 guide topics。

開啟 local read-only terminal UI：

```bash
exp --workspace "$EXP_REPO" ui
```

Startup 只在 local 讀 canonical records、associations、effective config summaries 與
operation database。只有 explicit readiness tab/refresh 才執行 bounded live probes。UI
沒有 action 能 mutation record/trust、execute work、install/login、start service、open
editor 或 handoff workspace。UI 沒有 JSON mode；automation 請使用 `exp context --json`
與 command-specific envelopes。

## Large bytes 留在 owning systems

Large datasets、model checkpoints、artifact files、traces 與 unbounded logs 留在 MLflow、
DVC 或 object storage。Canonical Git history 只接收 bounded summaries、cryptographic
digests、exact Source/commit identities 與 sanitized provider references。Credentials、raw
environments、host-local paths、large bytes 與 unbounded provider output 絕不進 canonical
records。

下一步閱讀[核心研究流程](workflows/core-workflow.md)、
[執行與派送](workflows/runtime-dispatch.md)與[指令地圖](reference/command-map.md)。
