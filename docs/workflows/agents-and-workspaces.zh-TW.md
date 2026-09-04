# Agent 與工作區

`exp` 每個 request 都 invocation fresh CLI agent process；不保留 provider SDK session 或 hidden
conversation。Agent output 在 domain service 驗證 exact schema、identity、revision、effect 前都是
advisory。

## Agent execution profiles

Executable role mapping 保留於 separate user-managed
`$XDG_CONFIG_HOME/exp/agents.toml`（`exp.agents/v1`）：

```toml
schema = "exp.agents/v1"

[roles]
idea_planner = "research-agent"
queue_advisor = "research-agent"
queue_battle = "research-agent"
experiment_implementer = "research-agent"

[profiles.research-agent]
executable = "research-agent"
args = ["--prompt", "{prompt_file}", "--schema", "{schema_file}", "--output", "{output_file}"]
timeout = "10m"
max_output_bytes = 1048576
output = "output_file_json"
stdin_prompt = false
allowed_env = []
secret_env = []
reported_model = "configured-outside-exp"
```

`executable` 是 binary name，不是 path/shell command。Placeholder 占完整 argv element。每個 process
接收 bounded input/environment，只能回傳一個符合 supplied schema 的 JSON value。用
`exp agent profiles` inspect；用 `exp agent run` 測試 role。

Layered `exp.config/v1` 也公開 trust-annotated `defaults.agent_profile`，但 current agent commands
不 consume；它們使用 `exp.agents/v1` role mapping 或 command explicit `--profile`。Execution wiring
加入前，layered field 只視為 policy/provenance metadata。

## Source-aware isolated implementation

Canonical Experiment/code Source 可在不同 repositories。需要時 explicit resolve 兩者：

```bash
EXP_REPO="$HOME/src/my-application-experiments"
SOURCE_KEY='app'
EXPERIMENT_ID='exp_replace_with_returned_id'
BASE_COMMIT='replace_with_full_lower_case_commit'

exp --workspace "$EXP_REPO" --source "$SOURCE_KEY" \
  experiment workspace prepare "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**'

exp --workspace "$EXP_REPO" --source "$SOURCE_KEY" \
  experiment agent "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**' \
  --prompt implementation-notes.md
```

Source 必須 active/locally associated。Immutable `subdir` 定義 agent cwd 與 `--allow` glob root；
Git path identity 仍相對 repository root。Experiment 必須 active/design locked，Source checkout/base
必須 clean/exact。

Native Git 建立依 full Project、Source、Experiment IDs scoped 的 deterministic XDG worktree/branch。
Commit 重新 discover，拒絕 Git/canonical metadata 與 Source subdir/allowlist 外 path，只 stage
observed paths，並接受 exact one single-parent commit。Agent-reported path list 若不同，Git commit
仍是 authority，並回傳 diagnostic。

`--workspace-backend dev_cli` 今日不 delegate lifecycle work。Optional provider 缺 exact schema-
versioned path receipt，因此 prepare/open/handoff/cleanup/retire unsupported；trusted policy 可 explicit
fallback 到 `native_git`。Native Git 一律 verify/inspect/own cleanup。

Formal workspace/branch 保留供 human review。`exp` 不 merge、push、rebase、delete branch 或 resolve
integration conflict。Public verified cleanup 目前屬於 direct Try；formal workspace commands 不
automatic remove worktree。

## Agent authority boundaries

- `idea develop` 可 propose，並在 explicit `--apply` 時建立 validated Plan transaction；model 不能
  bypass schema、revision、Policy、resource checks。
- `queue insert --agent` 記錄 QueueAdvice/order-swapped Battles；uncertain output 不改 Queue order。
- `experiment agent` 可建立一個 exact allowlisted commit；不能建立 scientific evidence/integrate。
- Commit 只有經 clean formal Attempt v3、included Run、綁定該 Attempt 的 Evaluation v2，以及
  Candidate v2 Source equality 後，才 Candidate-eligible。
- Agent 不能 approve Promotion、grant config trust、operate read-only TUI，或把 MLflow/artifact
  state 變成 verdict。

詳見 [Git and Worktrees](../tools/git-worktrees.md)與[Runtime Dispatch](runtime-dispatch.md)。
