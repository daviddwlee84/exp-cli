# Agent CLI Profiles

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

`exp` 以全新的 CLI processes 整合使用者管理的 agent executables，而不是 provider SDK
sessions。每個 request 都會收到明確的 prompt 與 JSON Schema，且必須只回傳一個符合該
schema 的 JSON value。Domain command 驗證並記錄 output 前，它都只是建議。

## 設定檔

Profiles 預設位於 `$XDG_CONFIG_HOME/exp/agents.toml`。可用 `--config` 指定其他檔案。
Top-level schema 必須精確為 `exp.agents/v1`。

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

`roles` 將 domain role 對應至 profile name。`--profile` 可以在單次 invocation 中覆寫該
mapping。Executable 必須是 binary name 而非 path，並在使用前一刻透過 `PATH` 解析。

## Profile 欄位

| 欄位 | 契約 |
|---|---|
| `executable` | 必填的 binary basename；拒絕 path 與包含 separators 的名稱 |
| `args` | 必填的 argument array；絕不使用 shell concatenation |
| `timeout` | 正值的 Go duration；預設為 10 分鐘 |
| `max_output_bytes` | 每個 stream 與 final output 的大小限制；預設 1 MiB，且不能超過 global invoker limit |
| `output` | `stdout_json`（預設）或 `output_file_json` |
| `stdin_prompt` | 是否也透過 stdin 傳送 prompt；預設為 `true` |
| `allowed_env` | 加入 minimal process environment 的明確 non-sensitive environment names |
| `secret_env` | 只在 process start 前一刻解析，且會結構化遮蔽的 sensitive environment names |
| `reported_model` | 選用的使用者設定 metadata；`exp` 不會探索或驗證 model |

支援的 placeholders 為 `{prompt_file}`、`{schema_file}`、`{schema_json}`、
`{output_file}` 與 `{cwd}`。Placeholder 必須占據完整 argument。`output_file_json` 必須恰好
包含一個 `{output_file}` argument；其他 output modes 禁止使用該 placeholder。

Credential-sensitive names 必須列在 `secret_env`，而非 `allowed_env`。同一名稱不能在兩份
lists 間重複。缺少必要 secret 會使 invocation 失敗；rendered commands、diagnostics 與
accepted output 都不會包含 secret values。

## 驗證 profiles

```bash
exp agent profiles
exp agent profiles --config /absolute/path/to/agents.toml --json
```

這會嚴格 decode TOML、拒絕 unknown fields、驗證每個 role reference 與 profile，並列出
normalized profile names；不會執行 executables。

## 執行單次 schema-constrained request

```bash
exp agent run \
  --role idea_planner \
  --prompt prompt.md \
  --schema response.schema.json \
  --cwd "$PWD" \
  --json
```

`--role` 與 `--schema` 為必填。`--prompt` 預設為 `-`，表示從 stdin 讀取；`--cwd` 預設為
目前 directory；`--profile` 可覆寫設定的 role mapping。Prompt input 上限為 4 MiB，
schema input 上限為 1 MiB。External JSON Schema resources 已停用。

每次 request 時，`exp` 都會建立私有 temporary directory，其中包含大小受限的 prompt、
schema 與 output files；接著以保留 argument boundaries 的方式啟動全新 process，完成後
移除該 directory。只有符合下列條件的 output 才會接受：只有一份 JSON document、不超過
設定的大小限制、不含受保護 secret value，且通過 supplied schema 驗證。

使用 `output_file_json` 時，`exp` 還會在 process 執行期間監控預先建立的 regular output
file，確保其 identity 不變且大小不超限。使用 `stdout_json` 時，stdout 就是 JSON
document。Requests 之間不存在 persistent agent session 或 hidden provider state。

## Domain roles

內建 workflows 透過 profile roles 運作，而不 hard-code vendors：

- `idea_planner` 為 `idea develop` 提出完整 Plan；
- `queue_advisor` 排序完整 Queue partition 加上一個 challenger；
- `queue_battle` 以對調 presentation order 的方式比較相鄰 entries；
- `experiment_implementer` 編輯隔離且受 allowlist 限制的 Git worktree。

Queue advice 仍是不可變的 audit input；存在不確定性時，Queue 維持不變。在 experiment
workflow 中，已驗證的 committed Git diff 比 agent 自行回報的 paths 更具權威。請參閱
[Git 與 Worktrees](git-worktrees.md)。

## 未來可探索主題

- 為常用 agent CLIs 加入經測試的 example wrappers。
- 記錄 profiles 在 local development 與 CI hosts 間的 portability。
- 探索 explicit streaming modes，同時維持單一 final schema result。
- 加入 secret environment sources rotation 與 redaction audit 指南。
