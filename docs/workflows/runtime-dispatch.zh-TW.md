# 執行與派送

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

`.exp/runtime.json` 是 project-local contract，負責把 canonical ResourcePools 與
Plans 連到 provider-owned execution。它會綁定：

- 每個 ResourcePool 對應的一個 Pueue group 與 stable label prefix；
- 每個 Plan 的 absolute executable、argument vector、working directory 與 timeout；
- 精確的 Git `base_commit`、`head_commit` 與 `change_set`；
- 明確允許且非秘密的 environment names；
- 必須產生的 output paths，其 SHA-256 digest 會被記錄。

若 workload 必須在 HEAD 等於 `head_commit` 的唯一 linked worktree 中執行，使用
`checkout: "registered_worktree"`。系統不會保存 host path。

## 派送前

```bash
exp daemon frontier
exp daemon status
exp policy autonomy assisted --confirm-auto-experiment
exp daemon tick
```

`frontier` 只讀本機資料，不接觸 Pueue。`tick` 執行一次 reconciliation 與 admission；
`run` 會重複執行直到被取消。Capacity 來自具名 ResourcePools，依 exploit/explore
權重公平分配；只有一個 lane 有合格工作時可借用閒置容量。

提交前，`exp` 會驗證 repository identity、乾淨的 executable tree、精確 HEAD、
base ancestry 與完全一致的 changed path set。Daemon 在 submission 前先寫 outbox，
並透過 worktree-scoped stable Pueue label 復原。

## Workload contract

Worker 會注入三個非秘密變數：

| 變數 | 意義 |
|---|---|
| `EXP_JOB_ID` | 私有 control-plane job identity |
| `EXP_ATTEMPT_ID` | Canonical Attempt identity |
| `EXP_RESULT_PATH` | Workload 必須寫入 bounded result JSON 的路徑 |

Workload 仍負責建立 MLflow run 與記錄資料。Pueue process success 只更新 operational
state，絕不會建立科學結論。

另見 [Pueue](../tools/pueue.md) 與詳細的
[架構說明](../design/architecture.md#execution-control-plane)。
