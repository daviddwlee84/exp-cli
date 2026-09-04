# 工具

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

`exp` 協調研究工作，但不會取代執行程式碼、儲存遙測資料 (telemetry) 或擁有原始碼歷史
的工具。整合 (integration) 的範圍刻意維持狹窄：上游工具仍是其原生狀態的權威來源，
`exp` 只記錄所需的規範性研究決策與已清理參照。

## 整合狀態

本表的狀態標籤是文件契約的一部分。已編譯的描述元 (descriptor)，或 `exp doctor` 找到
某個 binary，都不代表對應操作已經實作。

| 工具或元件 | 狀態 | 目前可用功能 | 權威邊界 |
|---|---|---|---|
| [Git 與 linked worktrees](git-worktrees.md) | 已實作整合 | Source-aware isolated worktree、exact allowlisted formal commit、clean runtime snapshot、bounded dirty Try seeding、native inspection、verified Try cleanup | Native Git 擁有 bytes/lifecycle；human 擁有 integration/merge/push |
| Workspace backends | Native 已實作；`dev_cli` fail-closed | Requested/actual/fallback report 與 bounded discovery；native prepare/inspect/cleanup/retire | 沒有 exact machine receipt 就不授權 optional lifecycle action |
| [Pueue](pueue.md) | 已實作整合 | Sanitized status、identity-checked cancellation、daemon 經 private worker envelope submission | Pueue 擁有 live task/group state |
| [MLflow](mlflow.md) | 已實作 read-only integration | Trusted named profile、selected metric/tag verification、exact Attempt ownership、Evaluation attachment、optional worker observation | Workload/MLflow 擁有 run creation、telemetry、artifact、registry state |
| [Agent CLI profiles](agent-cli-profiles.md) | 已實作整合 | 驗證 separate `exp.agents/v1` profile，並執行 fresh schema-constrained CLI process | Configured executable 擁有 external interaction；`exp` 不保存 session，agent output 仍 advisory |
| Direct worker 與 SQLite control state | 內部實作 | V1/v2 exact workload envelope、explicit v2 Project/scope authority、durable marker replay、lease、job、fencing、outbox、fairness | Private operational state 絕不是 scientific authority |
| `exp ui` | 已實作 read-only TUI | Immutable local research views、read-only operation database、explicit bounded readiness probe | 沒有 key 會 mutation、trust、execute、install、login、start service、open editor 或 handoff |
| [DVC 與 Slurm](planned-integrations.md) | 僅 discovery/contract | 本機 executable discovery 與 compiled capability metadata | 尚未整合任何 DVC 或 Slurm 操作 |
| [Marimo 與 Jupyter](planned-integrations.md) | 僅 discovery/contract | 本機 executable discovery 與 Runner descriptor metadata | 尚未整合 notebook inspection 或 execution |
| [Optuna-like search](planned-integrations.md) | 僅契約 | Provider-neutral `exp.search-adapter/v1` types 與 invariants | 不包含具體 Optuna runtime、package installation 或 service contact |

## 檢查本機可用性

```bash
exp doctor
exp doctor --json
```

Default `doctor` 只執行 executable lookup，不執行 third-party command、不 contact daemon/network、
不 authenticate/install package/start service，因此 version/capability 尚未 verified。
`doctor --live` 是 explicit exception：它執行 bounded provider-specific version/read-only local
service probe 加 workspace-backend probe。仍不 login、install、寫 config、start service、執行
workload 或 canonical mutation；unsupported capability 仍是 unsupported。

Provider contact 一律針對特定操作明確發生。目前只有 Pueue status/cancel、MLflow verify，
或 daemon `tick`/`run` 會聯絡 provider；本機 record commands 與 `daemon frontier` 不會
暗中 refresh provider state。

## 共通安全規則

- Scheduler 或 process 成功是操作證據，而不是科學結論。
- Provider output 會受到大小限制，並在跨越 adapter boundary 前進行結構化遮蔽
  (structural redaction)。
- 規範性 records 絕不包含 raw environments、credentials、無界限 logs 或 artifact bytes。
- Adapter 絕不隱式安裝 packages、開啟 authentication、啟動 services、下載 artifacts，
  或執行 notebook code。
- Machine-readable commands 會回傳單一 `exp.cli/v1` envelope；不要 scrape 人類閱讀用
  tables。

完整的 role 與 effect model 請參閱 [Provider 契約](../design/provider-contract.md)。

## 未來可探索主題

- 隨整合成熟，加入針對各操作的 capability/version matrices。
- 記錄 shared Pueue、Slurm 與 tracking services 的 deployment profiles。
- 為已清理的 provider diagnostics 加入疑難排解頁面。
- 新增工具頁面，同時維持此處所述的權威邊界。
