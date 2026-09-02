# 工作流程

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

這些指南把 `exp` 指令連回它們代表的研究決策。先閱讀核心循環，再依目前操作的
邊界選擇專門指南。

| 指南 | 適用時機 |
|---|---|
| [核心研究流程](core-workflow.md) | 從 Idea 前進到已評估、已排隊並完成 evaluation 的結果 |
| [Agent 與工作區](agents-and-workspaces.md) | 請 CLI agent 規劃或實作隔離的程式碼變更 |
| [執行與派送](runtime-dispatch.md) | 將 Plan 綁定 Pueue、Git、argv、outputs 與 capacity |
| [從證據到上線](evidence-to-promotion.md) | 結束 Experiment，或建立 Candidate、Release、Promotion |
| [Harness-v0 遷移](migration.md) | 透過明確的 plan/apply 流程匯入舊 research harness |

!!! warning "方便不會轉移權責"
    產生式 projection、provider dashboard 或 agent recommendation 可以提供資訊，
    但不會因為容易閱讀就自動成為 canonical。請使用 domain command 並審查產生的
    records。

支援的 mutation 後執行 `exp validate`；當 workflow 依賴產生式 project views 時，
執行 `exp render --check`。
