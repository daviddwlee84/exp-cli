# Command Map

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

目前 CLI 公開 77 個 visible command path。本頁依各 command 能協助完成的任務，
將所有 path 分組；刻意不重複完整 generated command reference 或每一個 flag。

執行中的 binary 是語法的權威來源：

```bash
exp <command> --help
```

Command 若提供 `--json`，請使用它並讀取 versioned envelope，不要解析
human-readable output。精確且與 build 相符的 inventory，請見
[generated command reference](https://github.com/daviddwlee84/exp-cli/blob/main/internal/skill/exp-cli/references/commands.md)。
`exp queue` 或 `exp evaluation` 等 parent command 主要用來組織 subcommand。

## 設定與檢查專案（6）

| Command | 用途 |
|---|---|
| `exp` | 進入 Git-native research control plane，或使用 `--skill` 輸出 embedded skill。 |
| `exp init` | 以冪等方式初始化固定的 `experiments/` root。 |
| `exp context` | 不 refresh provider，讀取 local resumable research summary。 |
| `exp doctor` | 探索 built-in 與 optional local provider capability。預設只尋找 executable；目前的 `--live` 不會接觸 provider。 |
| `exp validate` | 在不呼叫 provider 的情況下驗證 canonical record 與其 graph。 |
| `exp render` | 產生 deterministic projection，或用 `--check` 回報 projection drift。 |

## 執行 Agent 與管理 embedded guidance（8）

| Command | 用途 |
|---|---|
| `exp agent` | 查看 profile inspection 與 direct agent-run command。 |
| `exp agent profiles` | 驗證並列出 configured agent CLI profile。 |
| `exp agent run` | 使用 supplied JSON Schema output contract 執行一個 fresh profile。 |
| `exp skill` | 查看 version-matched embedded guidance skill 的 command。 |
| `exp skill check` | 不做 mutation，檢查 installed skill file、compatibility、hash 與 consumer link。 |
| `exp skill install` | 以原子操作安裝 embedded skill 與 safe consumer link。 |
| `exp skill print` | 輸出此 build 的 embedded `SKILL.md`。 |
| `exp skill sync` | 同步 generated source-tree command reference；使用 `--check` 可只檢查 drift 而不寫入。 |

## 收集、qualification 與研究優先排序（23）

### Idea 與 Plan

| Command | 用途 |
|---|---|
| `exp idea` | 查看 Idea capture 與 qualification command。 |
| `exp idea add` | 從人類或 Agent 提出的方向建立尚未進入 Queue 的 canonical Idea。 |
| `exp idea develop` | 請一個 fresh Agent 提出 queue-ready Plan，並可選擇套用。 |
| `exp idea list` | 列出 canonical Idea。 |
| `exp idea qualify` | 以原子操作將 Idea 轉成完整估價的 Plan。 |
| `exp plan` | 查看 priced research Plan command。 |
| `exp plan add` | 從 flag 或 versioned JSON input 建立 validated Plan。 |
| `exp plan list` | 不接觸 provider，列出 canonical Plan。 |
| `exp plan refresh` | 重新評估 utility、固定目前 Finding belief，並在重新排序前將 stale Plan 移出 Queue。 |

### Policy、resource 與 Queue

| Command | 用途 |
|---|---|
| `exp policy` | 查看 canonical autonomy 與 Queue-policy command。 |
| `exp policy autonomy` | 透過 explicit auto-experiment confirmation gate 變更 autonomy。 |
| `exp policy cluster-set` | 設定 cluster saturation threshold，或明確 reopen 一個方向。 |
| `exp policy init` | 建立預設為 manual 的 canonical `POLICY.md`。 |
| `exp policy show` | 顯示目前 canonical research policy。 |
| `exp pool` | 查看 ResourcePool command。 |
| `exp pool add` | 建立具名且受限制的 compute 或 human ResourcePool。 |
| `exp pool list` | 列出 canonical ResourcePool。 |
| `exp queue` | 查看跨 Pool/lane frontier 的 Plan ranking command。 |
| `exp queue create` | 為選定 ResourcePool 建立 exploit/explore partition。 |
| `exp queue insert` | 計分並插入 Plan，可選擇使用 listwise advice 與 order-swapped battle。 |
| `exp queue list` | 列出 canonical Queue。 |
| `exp queue remove` | 使用精確 Queue compare-and-swap 移除 Plan。 |
| `exp queue show` | 檢查 ordered Pool/lane entry 及其 pinned revision。 |

## 準備程式碼與操作本機執行（19）

### Daemon control

| Command | 用途 |
|---|---|
| `exp daemon` | 查看 local orchestration daemon command。 |
| `exp daemon frontier` | 不接觸 Pueue，檢查 canonical dispatch frontier。 |
| `exp daemon pause` | 停止新的 dispatch，同時保留 reconciliation state。 |
| `exp daemon resume` | 恢復 eligible dispatch。 |
| `exp daemon run` | 持續進行 reconciliation/admission，直到被取消。 |
| `exp daemon status` | 不接觸 provider，讀取 local daemon state。 |
| `exp daemon tick` | 執行一次 Pueue reconciliation 與 capacity-admission pass。 |

### Experiment workspace 與結案

| Command | 用途 |
|---|---|
| `exp experiment` | 查看 isolated workspace 與 scientific lifecycle command。 |
| `exp experiment agent` | 在 isolated worktree 中執行 configured implementation agent，並提交精確 allowlisted change。 |
| `exp experiment close` | 以原子操作結束 Experiment、完成 Plan、處置 evidence 並發布 Finding。 |
| `exp experiment workspace` | 查看 workspace preparation 與 commit command。 |
| `exp experiment workspace commit` | 只提交 experiment worktree 中實際觀察到且符合 allowlist 的 ChangeSet。 |
| `exp experiment workspace prepare` | 在精確 base commit 建立 isolated experiment branch 與 linked worktree。 |

### 已實作的 provider operation

| Command | 用途 |
|---|---|
| `exp provider` | 查看 supported tool 的 explicit audited read/control。 |
| `exp provider mlflow` | 查看 read-only MLflow verification command。 |
| `exp provider mlflow verify` | 驗證 workload-created MLflow run 中 requested metric 與 expected tag。 |
| `exp provider pueue` | 查看受支援的 Pueue read/control。 |
| `exp provider pueue cancel` | 經確認後，明確取消一個精確相符的 Pueue task。 |
| `exp provider pueue status` | 讀取 sanitized Pueue task/group snapshot。 |

## 評估、封裝與 Promotion evidence（13）

| Command | 用途 |
|---|---|
| `exp evaluation` | 查看 comparable protocol 與 immutable result command。 |
| `exp evaluation create` | 依 declared EvaluationSpec 記錄一筆 immutable Evaluation。 |
| `exp evaluation spec` | 查看 scientific/promotion EvaluationSpec command。 |
| `exp evaluation spec create` | 建立具有 bounded ResourcePool budget 的 comparable metric protocol。 |
| `exp candidate` | 查看 Candidate creation command。 |
| `exp candidate create` | 將 supported evidence、passing Evaluation 與精確 Git ChangeSet 封裝為 Candidate。 |
| `exp release` | 查看 typed Release composition command。 |
| `exp release create` | 從 named Candidate slot 建立 draft 或 atomically validated Release。 |
| `exp promotion` | 查看 human-only production Promotion command。 |
| `exp promotion append` | 將經確認的人類 Promotion outcome 附加至 target chain。 |
| `exp promotion spec-create` | 建立 sealed、bounded 且 human-gated 的 PromotionSpec。 |
| `exp champion` | 顯示從 append-only Promotion chain 推導的目前 Champion。 |
| `exp champion manifest` | 為目前 Champion 產生 deterministic downstream manifest。 |

## 檢查記錄並安全遷移（8）

| Command | 用途 |
|---|---|
| `exp migrate` | 查看 explicit harness-v0 migration command。 |
| `exp migrate apply` | 套用一份已完整審閱且通過 fingerprint validation 的 migration plan。 |
| `exp migrate plan` | 建立 read-only migration plan，並顯示需審閱的 ambiguity。 |
| `exp record` | 查看 canonical record inspection 與 transaction command。 |
| `exp record list` | 列出 Git-backed canonical record，並可依 kind 篩選。 |
| `exp record recover` | 依精確 hash 將 durable prepared transaction roll forward。 |
| `exp record show` | 解析並以 view、JSON envelope 或 normalized Markdown 顯示一筆 canonical record。 |
| `exp record transaction` | 套用目前支援的 low-risk prepared Idea/ResourcePool transaction。 |

以上各節合計 77 個 command path。如果 `exp <command> --help` 中不存在某個
command 或 flag，不可從 roadmap、note 或舊版文件推定它存在。
