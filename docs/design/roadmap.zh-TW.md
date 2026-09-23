# 實作藍圖

目前 release 已交付 Git-native research control plane；其 canonical experiment repository 可獨立於
Source repositories。本頁區分 implemented behavior、remaining integration 與 explicit non-goal。
只有 current code path functional/tested 的 command 才列為 delivered。

## 已交付：canonical research 與 recovery

- fixed `experiments/PROJECT.md` discovery、idempotent Project v1 initialization，以及跨 linked
  worktree 的 Project receipt reconciliation；
- strict Markdown/TOML records、UUID identity、privacy/path check、graph/lifecycle validation、
  deterministic revision/projection 與 stable JSON envelope；
- Source v1、Try v1、Idea v2、Attempt v3 SourceSnapshots/retry ownership、Evaluation v2 typed
  Attempt ownership、Candidate v2 Source identity；
- linked-worktree ID reservation 與 Git-common lock；
- `transactions-v2/` 中的 worktree-scoped `exp.transaction/v2` journal、exact-byte roll-forward
  recovery，以及 conditional backward reading v1 journal；
- local `record list/show/transaction/recover`、`validate`、`render`、`context` 與 exact harness-v0
  plan/apply migration。

## 已交付：independent experiment repository 與 Sources

- dedicated private experiment repository 作為 guided initialization default，可 safe adopt existing
  Git root，或 explicit create missing/empty target；
- dedicated initialization 不 implicit 建立 remote、commit、submodule 或 push；
- embedded/monorepo initialization 保留給 shared governance/legacy v1 compatibility；
- 一般 canonical Git Source record，具有 project-unique key、immutable subdir、sanitized locator
  history、active/retired lifecycle 與 exact CAS update；
- 每 Project 多個 Sources，以及 Source-aware Experiment workspace commands；
- private `exp.associations/v1` Project/Source mapping，帶 clone/Git-common filesystem identity 與
  locator revalidation；
- deterministic explicit/current-Project/most-specific-Source resolution，ambiguity/staleness 會回報而
  非猜測；
- XDG user/state/cache/data path，canonical Source record 不含 host path。

## 已交付：layered config、trust 與 profiles

- strict `exp.config/v1` hierarchy：built-in、XDG user、canonical repository、Source root、再加
  Source-subdir root-to-leaf files；
- documented scalar/array/backend/profile merge rule、combined digest、per-leaf provenance 與
  applied-layer audit；
- private `exp.trust/v1` receipt，綁定 exact bytes、capability、Project/Source/config scope、Git-common
  path/filesystem identity；
- `config path/show/explain/trust/revoke/list`，包含 historical revocation/sanitized output；
- workspace backend profile，`native_git` 是 mandatory correctness baseline；
- named MLflow profile，只有 binary/context/timeout/default metrics 與 environment names/policy；
  explicit compatibility flags 仍可用；
- runtime v2 exact `runtime.dispatch` trust，與 layered config 分離。

## 已交付：bounded Try workflow

- `try run` 在 operational execution 前 publish Try 加 planned Attempt v3；
- deterministic native Git worktree 中 direct argv execution，change allowlist 預設 empty，timeout bounded；
- clean capture 或 explicit Try-only `--dirty=capture`，包含 full tracked patch、bounded untracked
  bytes、clean direct submodule、canonical dirty/snapshot digest、private authenticated
  `exp.source-seed/v1` bundle；
- workspace preparation marker recovery 與 byte-exact seed round-trip checks；
- private lease/fencing job execution、heartbeat、bounded result file、redacted v2 streams、durable
  terminal/result markers；
- conservative resume、marker/SQLite repair、explicit unknown reconciliation，以及維持 Source/config/
  argv identity 的 one-successor retry lineage；
- human conclusion/abandonment、result ownership check、atomic adopt as Idea v2、status、partial-safe
  cleanup；
- native cleanup verified clean/exactly-seeded worktree 加 private seed，同時保留 branch、commit、marker、
  operation row 與 canonical record。

## 已交付：Source-aware formal runtime

- closed `exp.runtime/v1` 保留給 embedded Attempt v2/worker v1 dispatch；
- separate strict `exp.runtime/v2`，具有一個 writable execution Source、多個 read-only Sources、
  `main`/`registered_worktree`/`managed_worktree` selection、explicit observational no-change，以及
  Source-subdir-relative cwd/output；
- 每個 Source exact clean SourceSnapshot capture 與 formal Attempt v3；
- cross-repository canonical-before-operational Experiment/Run/Attempt creation、Plan transition、Queue
  removal；
- outbox submission revalidation；Source authority 消失時將 unstarted job/Attempt 標成 blocked，不會
  optimistic submit；
- worker-job/terminal/result v2，帶 explicit canonical root、Project UUID、checkout-local scope、
  metadata-only pre-payload authorization、fencing、private checkout identity，以及 post-success
  read-only Source verification；
- durable marker temporary promotion 與 database-independent replay，不重跑 workload；
- 透過 Evaluation v2/Candidate v2 的 clean formal Candidate gate。

## 已交付：research Queue、closure 與 promotion

- default-manual Policy、controlled classification、cluster saturation、80/20 exploit/explore allocation；
- Idea、qualified resource-priced Plan v2、named ResourcePool、globally unique ordered pool/lane Queue
  partition；
- transparent scoring、listwise advice、order-swapped pairwise battle、immutable audit record、human-review
  fallback；
- daemon frontier/tick/run/pause/resume、project lease fencing、weighted fairness、Pueue outbox recovery、
  sanitized status、ownership-checked cancel；
- Experiment design lock/amendment/closure、explicit Run evidence disposition、Finding/belief-staleness
  propagation；
- EvaluationSpec/Evaluation、Candidate v1 compatibility/Candidate v2、typed Release slot、mandatory
  combination evidence、sealed promotion holdout、append-only human Promotion chain、Champion manifest
  v1/v3。

## 已交付：provider 與 UI boundary

- provider registry/discovery，具 bounded local probe/sanitized readiness；
- Pueue scheduling/control 僅限 declared capabilities；
- read-only MLflow verification、exact Attempt ownership、selected metrics/tags、sanitized artifact URI，
  以及 unavailable 時不改變 workload success 的 optional worker observation；
- provider-neutral `exp.search-adapter/v1` interface contract（尚無 concrete search backend）；
- workspace-provider registry 與 requested/actual/fallback report；
- `exp ui` read-only Workflow、Workspace、Tries、Queue、Attempts、Candidates、Readiness tabs；
  cancellable generation-fenced read、read-only SQLite open、只有 explicit bounded probe；
- version-matched embedded skill/generated command reference。

## Remaining：unattended-operation hardening

優先 hardening 可改善 observability，但不擴大 authority：

- 更長 daemon/worker soak/crash test，涵蓋 Pueue submit ambiguity、expired lease、provider restart、
  marker/result publication、outbox repair；
- 更清楚的 bounded event/audit inspection 與 budget-consumption reporting；
- 在 shared explicit dispatch gate 外區分 `assisted`／`limited` 的 policy semantics；
- 提升 follow-up/combination Experiment creation ergonomics，但不削弱 typed gate；
- explicit holdout-budget consumption accounting 與 Release supersession ergonomics；
- 更多 real harness-v0 migration fixtures；
- Windows 上更完整 runtime/process-tree verification；AIX 刻意只保留 canonical Git operations，
  operational store 回報 unsupported。

## Remaining：optional workspace provider

`dev_cli` 可 discovery，但今日**所有 lifecycle capabilities 都 fail closed**：prepare、inspect、cleanup、
open、handoff、retire 均 compiled unsupported。Public CLI 沒有 schema-versioned content-free capability
response，也沒有 exact native-path machine receipt 加 verifiable occupancy。Human help/version/catalog
output 不是 authorization。

在此 contract 出現前：

- 不 invocation lifecycle `dev` subprocess；
- trusted selection 可 report explicit fallback 至 `native_git`；
- native Git 擁有 prepare、byte verification、inspect、cleanup、retire；
- provider-local task/catalog/worktree ID 絕不是 canonical authority。

未來 capability 只有在回傳 exact machine receipt，且 native postcondition 能證明 provider 作用於
requested worktree 後，才可 enable。

## Remaining：concrete Plan-scoped search 與 providers

Provider-neutral Study contract 已存在，但沒有 concrete Optuna runtime。Future adapter 必須證明
version/capability support、durable idempotency、timeout-after-provider-commit recovery、secret-reference-
only storage config、trial-state mapping、bounded sanitized observation。它從屬一個 exact Plan revision，
不能取代 Queue/ResourcePool authority。

Additional Pueue observation、MLflow registry/artifact read、DVC、Slurm、notebook entrypoint 仍需逐
capability 實作。每項都必須 declare effect、preserve argv boundary、避免 implicit install/login/service
start/download，且除非 explicit import，provider state 保持 non-canonical。

## Explicit non-goal 與 true limit

目前 scope 不提供：

- automatic Git merge、push、rebase、branch deletion、production deploy 或 rollback execution；
- agent/provider/autonomy/manifest/TUI-approved Promotion；
- 通用 cloud artifact service、raw telemetry/log mirror 或 implicit download；managed local／MLflow exploration artifacts 與 explicit verified retrieval 已支援；
- 從 Try（尤其 dirty Try）直接建立 Candidate/Promotion；必須 clean formal rerun、typed Evaluation v2
  與 Candidate v2；
- 同一 Git repository 的多個 canonical Project roots，或不同 Project UUID 間的 canonical relation
  （單一 Project 內多個 external Sources 已交付）；
- universal cloud scheduler/model registry、generic browser-session control、dynamic Go plugin ABI；
- 從 process、scheduler、tracker、commit 或 artifact state automatic scientific verdict；
- migration 期間執行 legacy harness scripts。

## 已交付：探索體驗

可配置 scratch collection、連續 Try steps、具名 input bindings、獨立 artifact outputs、local／MLflow writer、具作者標記的 agent conclusion、runner identity，以及 SQLite cross-Project history 已實作。詳見[臨時探索](../workflows/exploration.md)。

原生 Windows 發布目前暫緩；失敗證據與重新發布的驗收條件記錄於 [storage 與 release backlog](https://github.com/daviddwlee84/exp-cli/blob/main/backlog/windows-release-storage.md)。
