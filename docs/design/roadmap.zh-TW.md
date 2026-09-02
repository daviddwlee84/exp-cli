# 實作藍圖

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

目前的 release 是本機研究控制平面 (local research control plane) 的基礎。以下里程碑 (milestone) 將已交付的行為與刻意保留到未來的 integration 分開列出。只有當 command 的行為確實可用時，才會加入該 command。

## 已交付：規範研究基礎

- 固定探索 `<git-root>/experiments`，並提供冪等初始化 (idempotent initialization)；
- 嚴格、具版本的 Markdown/TOML 記錄，包含 UUID identity、privacy check、graph validation、deterministic projection 與 stable JSON envelope；
- linked-worktree ID reservation 與 Git-common locking；
- 已準備的 multi-record create/replace/delete transaction，具備 exact-byte journal、經 hash 檢查的 roll-forward recovery、failure injection，以及明確的 `record recover`；
- 本機 `record list/show/transaction`、`validate`、`render` 與 `context`。

## 已交付：研究 Queue 與 Agent 協作

- 明確的 `POLICY.md`、預設為 manual 的 autonomy、受控 classification、cluster saturation data，以及 80/20 exploit/explore share；
- 由人類或 Agent 提出的 Idea、parent Idea lineage，以及以原子操作 qualification 成含資源價格的 Plan v2 記錄；
- 具名 ResourcePool 與有序的 pool/lane Queue partition；
- 透明的 expected-value scoring、global listwise advice、交換順序的 adjacent pairwise battle、不可變更的 audit record，以及 human-review fallback；
- 每次均為全新 single-shot 的 Agent CLI profile，具備嚴格 JSON Schema output、environment allowlist、secret reference、bounded output，且不使用 SDK session。

## 已交付：本機執行控制平面

- 嚴格的 `.exp/runtime.json` binding，將 Pool/Plan 對應至 Pueue group，以及精確的 workload argv/Git identity；
- 本機 frontier inspection、one-shot daemon tick、continuous daemon loop、pause/resume、lease fencing、weighted fairness 與 outbox recovery；
- Git common directory 下的 SQLite operational state，絕不屬於 canonical；
- 已消毒的 Pueue status、具 audit 的 private-worker submission，以及明確 cancel；
- durable worker terminal marker 與 replay-safe completion；
- 隔離的 XDG Git worktree 與精確 allowlisted experiment auto-commit，不具 merge 或 cleanup authority；
- 唯讀的 MLflow run verification；workload 擁有 run creation 與 logging。

## 已交付：科學結案與 production 邊界

- 以原子操作完成 Experiment closure、Plan completion、evidence disposition 與 Finding publication；
- 能感知 revision 的 belief dependency 與 stale-queue detection；
- 不可變更的 EvaluationSpec 與 Evaluation；
- 從 supported evidence 建立 Candidate，並帶有完整 Git commit/ChangeSet；
- typed Release slot，以及 multi-Candidate Release 強制要求經評估的 combination evidence；
- 已封存、用於 promotion 的 evaluation、append-only human Promotion chain，以及衍生的 Champion manifest。

## 已交付：相容性與 extension contract

- 明確的 harness-v0 migration plan/apply，具備 exact-byte archive、deterministic UUIDv5 identity、經審閱的 ambiguity resolution、fingerprint revalidation，以及可復原的 root swap；
- provider-neutral `exp.search-adapter/v1` contract，用於冪等的 Plan-scoped Study `open`/`ask`/`tell`/`prune`/`observe`；
- 版本相符的 embedded skill 與 generated command reference；
- 透過 `mise.toml` 在 repository local 固定 Go 1.26.4。

## 下一步：強化無人值守運作

優先工作應改善復原能力與可觀測性 (observability)，同時不削弱 authority model：

- 對長時間執行的 daemon 進行 soak test 與 crash test，涵蓋 Pueue submit ambiguity、expired job lease、worker interruption 與 provider restart；
- 為 SQLite operation 與 outbox state 提供更清楚、有限的 event/audit inspection；
- 在 policy 層級進一步區分 `assisted` 與 `limited` 的語意，而不只共用同一個 explicit dispatch gate；
- 根據已完成工作提供更豐富的 Queue saturation 與 budget-consumption feedback；
- first-class follow-up 與 combination Experiment creation，包括一條受支援的路徑，將 Agent 準備的精確 commit 轉成新的 executable Plan/Attempt，而不是手寫 canonical record；
- 明確的 holdout-budget consumption accounting，以及 immutable Release supersession 的易用操作；
- 納入更多實際 harness-v0 layout 的 migration fixture；
- 只有在 process-tree 與 SQLite 行為經過測試後，才支援 runtime Windows；AIX 仍明確不支援 operational store。

## 下一步：具體的 Plan-scoped search

只有當 integration 能證明以下項目後，才實作 Optuna adapter：

1. 支援的 Optuna/storage 版本與安全 capability probe；
2. `open`、`ask`、`tell` 與 `prune` 的 durable idempotency；
3. timeout-after-provider-commit ambiguity 的 recovery；
4. storage configuration 只能使用 secret reference；
5. multi-objective 與 trial-state mapping；
6. 有限且經結構消毒的 observation。

Optuna 始終從屬於單一 Plan revision。它不會取代 global Queue，也不會配置 ResourcePool。

## 後續的 provider capability

每次加入一項已驗證的 external operation capability：

- 更完整的 Pueue observation/cancellation reconciliation 與 bounded log；
- 只有在 CLI 具備 stable safe surface 時，才加入 MLflow artifact/registry read；
- DVC artifact 與 Queue read，之後再加入範圍狹窄的 write；
- 具名 site 的 Slurm probe 與 scheduling，並明確 export environment；
- notebook runner 作為 workload entrypoint，而不是 durable scheduler。

每一項新的 mutation 都必須宣告 effect、保留 argument boundary、揭露可供審閱的 identity，並避免 implicit installation、authentication、daemon startup 或 artifact download。

## 明確延後

- 自動 production deployment 或 rollback execution；
- 由 Agent 核准 Promotion；
- 通用 cloud scheduler 或 model registry abstraction；
- W&B、Kaggle、Ray、Kubernetes、Azure ML、Databricks、Modal、RunPod，或 generic browser-session control；
- 一個 repository 中存在多個 `experiments/` root，或 cross-repository graph；
- dynamic Go plugin、強制 FTS index 或 TUI；
- raw telemetry/log mirroring 與 artifact-byte storage；
- 在沒有專用 Experiment 與 Evaluation 的情況下，假設各個獨立 Candidate 的增益組合後仍成立。
