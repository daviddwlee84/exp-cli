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
| [Git 與 linked worktrees](git-worktrees.md) | 已實作整合 | 準備隔離的 experiment branch/worktree，並 commit 精確的 allowlisted change set | Git 擁有程式碼歷史與整合 |
| [Pueue](pueue.md) | 已實作整合 | 已清理的 status、經身分檢查的 cancellation，以及 daemon 透過私有 worker envelope 提交工作 | Pueue 擁有即時 task 與 group 狀態 |
| [MLflow](mlflow.md) | 已實作唯讀整合 | 驗證單一 workload-created run 上指定的 metrics 與 tags，再將已清理的身分附加至 Evaluation | Workload 與 MLflow 擁有 run 建立、telemetry、artifacts 與 registry state |
| [Agent CLI profiles](agent-cli-profiles.md) | 已實作整合 | 驗證本機 profiles，並執行全新且受 schema 約束的 CLI process | 設定的 executable 擁有所有 external provider interaction；`exp` 不保存 session，agent output 仍只是建議 |
| Direct worker 與 SQLite control state | 內部實作 | 執行精確的 workload envelope；協調 leases、jobs、fencing、outbox recovery 與 fairness | 私有操作狀態絕不是科學權威 |
| [DVC 與 Slurm](planned-integrations.md) | 僅 discovery/contract | 本機 executable discovery 與 compiled capability metadata | 尚未整合任何 DVC 或 Slurm 操作 |
| [Marimo 與 Jupyter](planned-integrations.md) | 僅 discovery/contract | 本機 executable discovery 與 Runner descriptor metadata | 尚未整合 notebook inspection 或 execution |
| [Optuna-like search](planned-integrations.md) | 僅契約 | Provider-neutral `exp.search-adapter/v1` types 與 invariants | 不包含具體 Optuna runtime、package installation 或 service contact |

## 檢查本機可用性

```bash
exp doctor
exp doctor --json
```

`doctor` 只進行 executable lookup。它不會執行第三方 `--version` commands、聯絡 daemon
或 network、進行 authentication、安裝 package，或啟動 service。因此，在特定操作實際
驗證前，versions 與 capability support 都會維持 `unknown`。目前的 `--live` flag 只會回報
尚未實作 live probing，仍然僅執行本機 discovery。

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
