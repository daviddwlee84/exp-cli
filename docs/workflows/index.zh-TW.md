# 工作流程

畫圖、連續 Try、儲存偏好與歷史結果查找，請先看[臨時探索](exploration.md)。

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

開啟 detailed workflow 前先使用 decision table。三條 route 具有不同 evidence/authority；
不可把 quick Try 當成繞過 formal Experiment 或 human promotion gate 的 shortcut。

## 選擇 route

| 下一個 decision | Route | 開始 | 後續 |
|---|---|---|---|
| 某方向值得 formalize 嗎？ | **Quick exploratory Try** | `exp guide quick` → `exp try run` | `try status` → `try finish` → optional `try adopt` |
| Governed compute 應測試 registered claim 嗎？ | **Rigorous Experiment** | `exp guide research` → `exp idea add` | qualify → Queue → runtime v2/daemon → Evaluation v2 → Candidate v2 |
| Validated evidence 應進入某 target 嗎？ | **Promotion** | `exp guide promotion` → `exp candidate create` | typed Release → sealed fresh holdout → named-human Promotion |

任何 route 前都建立相同 context：

```bash
exp guide setup
exp guide workspaces
exp guide config
exp workspace status
exp source status
exp config show
```

## Detailed guides

| 指南 | 適用時機 |
|---|---|
| [核心研究流程](core-workflow.md) | 選擇 Try/formal work，再從 Sources 走到 comparable evidence |
| [Agent 與工作區](agents-and-workspaces.md) | 請 fresh CLI agent 規劃或實作 Source-aware isolated changes |
| [執行與派送](runtime-dispatch.md) | 執行 managed native-worktree Try 或 clean cross-repository formal runtime v2 |
| [從 Evidence 到 Promotion](evidence-to-promotion.md) | Enforcement Attempt v3 → Evaluation v2 → Candidate v2 → human Promotion |
| [Migration 與 compatibility](migration.md) | Migrating harness-v0，或了解 exact Project/record/runtime/worker/journal compatibility |

Resume 使用 `exp context`；conceptual terminal help 使用 `exp guide`；syntax 使用
`exp <command> --help`；local IDs/Sources 使用 shell completion；read-only overview 使用
`exp ui`。Provider absence 不移除 commands：default `doctor` 只 local lookup，
`doctor --live` 才執行 explicit bounded read-only probe。只有 formal daemon dispatch 需要
Pueue；MLflow 是 optional observation；native Try 不需要這兩者或 `dev`。

!!! warning "方便不會轉移權責"
    Generated projection、provider dashboard、agent recommendation 或 TUI view 可以提供
    資訊，但不會因容易閱讀就成為 canonical。請使用 domain command 並 review resulting
    records。

Large datasets、models、artifact files 與 unbounded logs 留在 MLflow、DVC 或 object
storage。只有 bounded summaries、digests、exact identities 與 sanitized references 進入
Git。支援的 mutation 後執行 `exp validate`；workflow 依賴 current generated views 時執行
`exp render --check`。
