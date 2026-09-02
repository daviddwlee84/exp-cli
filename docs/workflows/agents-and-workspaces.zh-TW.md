# Agent 與工作區

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

`exp` 整合的是每次全新啟動的 CLI agent process，而不是 provider SDK session。
Profiles 位於 `$XDG_CONFIG_HOME/exp/agents.toml`，把 roles 對應到使用者管理的
executable。

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
```

Placeholder 必須占用完整 argument。每次 request 都啟動新 process，並且只能回傳
一個符合 supplied schema 的 JSON value。用 `exp agent profiles` 與
`exp agent run` 檢查或測試 profiles。

## 隔離的實作工作

```bash
exp experiment workspace prepare EXPERIMENT \
  --base BASE_COMMIT --allow 'src/**' --allow 'configs/**'

exp experiment agent EXPERIMENT \
  --base BASE_COMMIT --allow 'src/**' --allow 'configs/**' \
  --prompt implementation-notes.md
```

Workspace 是位於精確 base 的 linked Git worktree。Commit operation 只 stage 實際
觀察到且位於 allowlist 的 paths，拒絕 canonical `experiments/` records 與 Git
metadata，並產生一個精確的 experiment commit。它不會 merge 或移除 worktree；
整合仍由人類控制。

Agent output 在 domain command 驗證並記錄前都只是 advisory。程式碼 commit 不是
科學證據；沒有成功且被納入的 Attempt 與相符 Evaluation，就不能成為 Candidate。
