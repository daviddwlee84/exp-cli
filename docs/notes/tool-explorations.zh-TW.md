# 工具探索

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

本頁為接觸研究工作流程的工具保留持久的調查位置。各項目描述問題與目前邊界，
不代表 integration 承諾。若一項調查需要 command、特定版本 observation 或較長的
比較，請建立獨立的 dated note。

所有探索筆記都遵守[發布與安全規則](index.md)：內容公開、有限、已消毒且
non-canonical。

## 如何維護探索項目

使用下列其中一種 status：

| Status | 意義 |
|---|---|
| `backlog` | 問題值得保留，但目前沒有進行中的調查。 |
| `exploring` | 正在針對具名問題與 context 收集 evidence。 |
| `validated` | 有限結論已重現並連至 evidence；它本身仍不會改變 `exp` 行為。 |
| `deferred` | 工作已刻意延後，並明確記錄 prerequisite。 |

每份 dated exploration 都應標明觀察到的 tool version 或具名 non-secret context，
區分 documented behavior 與 direct observation，並以接下來所需的 decision 或
validation 作結。不可只因 `exp doctor` 找到 binary 就推定已支援該工具：預設的
doctor 只做本機 executable discovery，目前的 `--live` 也不會接觸 provider。

## Topic backlog

| 工具 | 目前的 `exp` 邊界 | 保留的探索主題 |
|---|---|---|
| MLflow | 已實作唯讀的 `exp provider mlflow verify`。Workload 建立並記錄 run；驗證需要明確 run ID、requested metric 或 expected tag、`FINISHED` run，以及 sanitized output。 | 安全的 artifact／registry read surface；tracking/artifact URI redaction；proxy 與 direct artifact access 的區分；Evaluation attachment 與 Attempt lineage；支援的 CLI version 與 bounded failure output。 |
| Pueue | 已實作 sanitized status、經確認的 exact-task cancel，以及 daemon 提交 private `exp worker run` envelope。Captured environment map 與 raw command string 不會越過 adapter boundary。 | Bounded log access；更完整的 observation/cancellation reconciliation；group 與 dependency semantics；submit-ambiguity recovery；Pueue 4.x compatibility 與 Windows limitation。 |
| DVC | 只有 binary discovery 與 provider-contract role；尚未實作 DVC operation command。 | Version/capability probe；不 implicit download 的 artifact stat/list；DVC queue read；只有在 effect 與 recovery 都明確後才加入 narrowly scoped write；repository/remote identity redaction。 |
| Optuna | `exp.search-adapter/v1` 是 provider-neutral、Plan-scoped contract。目前沒有 concrete Optuna adapter、Python environment、storage connection 或 implicit installation。 | 支援的 Optuna/storage version；冪等 (idempotent) `open`/`ask`/`tell`/`prune`/`observe`；timeout-after-commit ambiguity；multi-objective 與 trial-state mapping；secret-reference-only storage；bounded sidecar transport。 |
| Slurm | 可以探索 candidate binary，但尚未實作 submit/observe/cancel operation。 | Named-site policy 與 version matrix；JSON 或 fixed-field parser；cluster/array/step identity；delayed accounting；絕不使用 `--export=ALL` 的 explicit environment export；safe cancellation 與 recovery。 |
| Marimo 與 Jupyter | 可以探索 candidate notebook binary。它們是未來的 workload entrypoint，不是 durable scheduler；inspection 不得執行 notebook code。 | Explicit runner argv；kernel/environment identity；package-resolution boundary；bounded output；reproducibility capture；將 notebook execution 對應至 Run/Attempt，且不把 notebook state 複製進 canonical record。 |

## 建議的 dated exploration template

```markdown
# YYYY-MM-DD：工具 — 問題

- Status: backlog | exploring | validated | deferred
- Tool/version:
- Context:
- Last reviewed:

## 要支持的決策

說明此探索可能影響的具體選擇。

## 文件化契約

連結 upstream documentation，並只記錄相關的 bounded claim。

## 安全觀察

記錄已消毒的 command、選定 output field 與可重現 result。
不得貼上 raw environment、完整 log、credential 或 artifact byte。

## 缺口與 failure mode

分開記錄 missing capability、ambiguous provider state 與 unsafe behavior。

## 下一項驗證

列出所需的 test、fixture、design change、Idea 或 implementation prerequisite。
```

## 探索何時畢業

Validated note 可以支持 design update 或 implementation Plan，但筆記不會啟用
capability。只有當 provider contract、command behavior、redaction、recovery、test
與 generated command metadata 一致時，integration 才算交付。狀態改變時，請更新
[實作藍圖](../design/roadmap.md)。

Authority boundary 請見[記錄與權威](../reference/records-and-authority.md)與
[Provider Contract](../design/provider-contract.md)。
