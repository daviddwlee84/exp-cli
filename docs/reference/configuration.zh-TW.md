# 設定與路徑

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

`exp` 將 user configuration、project-local execution binding、Git-backed
research record 與 private operational state 分開保存。維持這些位置的區隔，可以
避免本機 scheduler detail 變成 scientific authority，也能避免 host path 或
credential 洩漏至記錄。

## 路徑摘要

| 用途 | 預設位置 | Authority 與生命週期 |
|---|---|---|
| Canonical research record | `<git-root>/experiments/` | 由 Git 支持的 scientific/decision authority。 |
| Agent CLI profile | XDG 系統上的 `$XDG_CONFIG_HOME/exp/agents.toml`；實際透過 OS user config directory 解析 | 使用者管理、將 role 對應到 fresh agent CLI process 的設定。可用 `--config PATH` 覆寫。 |
| Runtime contract | `<git-root>/.exp/runtime.json` | 嚴格的 project-local `exp.runtime/v1` binding，將 canonical Pool/Plan ID 對應至 execution detail。Command 可用 `--config PATH` 選擇另一個安全的 repository-relative path。 |
| Experiment worktree | `$XDG_DATA_HOME/exp/worktrees/<project-namespace>/<short-id>-<slug>` | 位於 source repository 外的 local linked Git worktree。若未設定 `XDG_DATA_HOME`，Unix-style fallback 為 `~/.local/share`。 |
| Canonical coordination | `<git-common-dir>/exp/v1/` | 由 linked worktree 共用的 private lock、receipt、ID reservation、prepared transaction 與 Attempt marker。 |
| Daemon database | `<git-common-dir>/exp/runtime/v1/control.sqlite` | Private SQLite coordination state；非 canonical，也不受 Git 追蹤。 |

本機 command 顯示的 host path 是 operational detail。不得複製到 canonical Markdown
或公開筆記中。

## Agent CLI profile：`agents.toml`

Agent profile 將 role 對應到使用者管理的 executable。每個 request 都會啟動新
process；`exp` 不保留 provider SDK session 或隱藏的 conversational state。

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

重要限制：

- `executable` 是從 `PATH` 解析的 binary name，不是 path 或 shell command；
- `{prompt_file}`、`{schema_file}`、`{schema_json}`、`{output_file}` 與
  `{cwd}` 等 placeholder 必須占據完整 argument；
- output 必須恰為一個符合 supplied JSON Schema 的 JSON value；
- `allowed_env` 與 `secret_env` 只包含 variable name，絕不包含 value；
- output 與 diagnostic 會先經過 bounded/redacted 處理，再傳回呼叫端。

使用 `exp agent profiles [--config PATH]` 驗證並列出 profile。使用
`exp agent run --role ROLE --schema PATH [--config PATH]` 直接執行受 schema
約束的 invocation。

## Project runtime contract：`.exp/runtime.json`

Runtime file 是 strict JSON。Unknown field、無效 canonical ID、不安全 path、
ambiguous Pueue label prefix 或不相符的 Git identity 都會使 validation 失敗。

```json
{
  "schema_version": "exp.runtime/v1",
  "pools": {
    "pool_01a01e66-f8e0-7202-8000-000000000202": {
      "pueue_group": "gpu",
      "label_prefix": "exp-"
    }
  },
  "plans": {
    "plan_01a01e69-e340-7505-8000-000000000505": {
      "executable": "/opt/project/bin/train",
      "argv": ["--config", "configs/cosine.toml"],
      "checkout": "main",
      "cwd": ".",
      "timeout": "4h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "0000000000000000000000000000000000000000",
      "head_commit": "1111111111111111111111111111111111111111",
      "change_set": ["configs/cosine.toml", "src/train.go"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
```

Pool entry 將 canonical ResourcePool 綁定至 Pueue group 與 stable label prefix。
Plan entry 將一個 canonical Plan 綁定至 absolute executable、argument array、
repository-relative working directory、timeout、選定的 environment-variable name、
精確 Git base/head commit、精確 ChangeSet 與 expected output path。

`checkout` 預設為 `main`。設成 `registered_worktree` 時，系統會選擇 HEAD
等於 `head_commit` 的唯一 registered linked worktree；runtime file 不會持久化
worktree host path。目前的 Pueue route 會保存 task environment，因此 runtime
`secret_env` 必須保持為空。需要 credential 的 workload，必須在啟動後透過另行
審閱的 broker 或 provider profile 取得。

Executable 刻意要求為 absolute path，因此可能是 host-specific。團隊必須明確決定
`.exp/runtime.json` 是可攜且受追蹤，或由各 host 分別維護。無論採哪種方式，它都是
configuration，不是 canonical research record，而且選定的 runtime path 不得包含在
Plan ChangeSet 中。

## XDG 管理的 Experiment worktree

`exp experiment workspace prepare` 會在以下位置建立 linked worktree：

```text
<data-home>/exp/worktrees/<project-namespace>/<short-id>-<slug>
```

`<data-home>` 在有設定時為 `XDG_DATA_HOME`，否則在一般 Unix configuration
下為 `~/.local/share`。Project namespace 結合 repository-derived name 與
Git-common identity digest，因此不同 clone 不會在無提示下共用 workspace。該 path
必須是 source repository 外部、不含 symlink 的 absolute location。

`exp` 可以準備 branch，並建立一個精確的 allowlisted commit。它絕不 merge 該
branch、移除 worktree，或變更由人類擁有的 integration branch。

## Git-common operational data

同一 clone 中的所有 linked worktree 共用 absolute Git common directory。`exp`
將 cross-worktree coordination 保存於 `<git-common-dir>/exp/`：

```text
<git-common-dir>/exp/
├── v1/
│   ├── lock
│   ├── project-receipt.json
│   ├── reservations/
│   ├── transactions/
│   └── attempts/
└── runtime/v1/control.sqlite
```

`control.sqlite` 擁有 daemon lease、fencing token、job、submission outbox、
provider observation、fairness counter、pause state 與 bounded event history。
這些事實用來協調 execution，不能建立 hypothesis、evidence disposition、Finding、
Evaluation、Release 或 Promotion。

不要為了重設設定而自行編輯或清除此目錄樹。ID reservation 與 prepared transaction
具有 recovery semantics；請使用對應的 `exp` command。

## Workload environment contract

Private worker 會使用 minimal environment、明確允許的 name，以及三個
non-secret variable 啟動 configured workload：

| Variable | 意義 |
|---|---|
| `EXP_JOB_ID` | Private control-plane job identity。 |
| `EXP_ATTEMPT_ID` | 此次 execution 的 canonical Attempt identity。 |
| `EXP_RESULT_PATH` | Workload 可寫入一個 bounded valid JSON result 的 absolute private path。 |

Workload 必須將 `EXP_RESULT_PATH` 視為系統指派的 output path，不得自行選擇或
替換。Repository 中的 expected output 另由 `expected_outputs` 宣告，並在 process
成功後驗證與計算 hash。Process 成功或 result JSON 有效，都不構成 scientific verdict。

完整 behavioral boundary 請見[Runtime Dispatch](../workflows/runtime-dispatch.md)、
[Agents and Workspaces](../workflows/agents-and-workspaces.md)與
[Architecture](../design/architecture.md)。
