# Context

`exp-cli` 目前把 canonical research records、private coordination state 與 executable source code 都視為同一個 Git repository：`project.DiscoverWithGit` 固定尋找 `<git-root>/experiments/PROJECT.md`，`record.Store` 以同一個 Git common directory 管理 locks/journals/ID reservations，`controlplane.Adapter`、`worker`、`experimentgit.Manager` 與 Candidate provenance 也都假設 canonical repo 等於 execution repo。這使短期實驗若直接放進 production workspace，容易污染 codebase；但把實驗移到另一個 repo 後，現有模型又無法可靠表達 source repo/subdir、clean/dirty snapshot、local clone mapping 或跨 repo 執行。

本次目標是完整交付四個階段：獨立 experiment workspace 與分層 config、快速 Try/dirty provenance、跨 repo daemon/worker/MLflow，以及更清楚的 wizard/help/completion/read-only TUI，同時保留既有 embedded projects、strict records、one-envelope JSON、redaction、transaction recovery 與 human promotion gates。

## 已確定的產品方向

- **新專案預設使用獨立的 private experiment Git repo**；它擁有 Project UUID、canonical records、transactions、SQLite/jobs/markers。production repo 只作為具名 Source binding。
- **不以 submodule 或 monorepo 作 identity model**。submodule 只可作共同 checkout 的便利；monorepo 只適合原本已有共同治理或既有 embedded project。Source binding/subdir 在三種排列下都使用同一套語意。
- Experiment repo 只保留小型 notebook/config/summary、selected facts、digests 與 sanitized refs；大型 dataset/model/log/artifact bytes 仍由 MLflow、DVC 或 object storage 擁有。
- Dirty source 只可用於快速 Try，保存 deterministic fingerprint 並標為 bounded；任何 Candidate/Release/Promotion 前都必須在 clean commit 上正式重跑。
- 完整四階段都在本分支交付，但每一階段必須能單獨通過 regression/integration tests。
- `exp ui` 為明示啟動、初版唯讀；bare `exp` 繼續顯示 help，避免破壞 scripting/TTY 相容性。

# Target authority and identity model

## Canonical authority

- Canonical experiment repo：Project、Source、Try、Experiment/Run/Attempt、Evaluation、Candidate/Release/Promotion records，以及其 Git history。
- Source repo：實際 source bytes、Git commits/worktrees 與 dirty working tree；它不擁有 research authority。
- User-local XDG state：canonical workspace path、Source clone paths、observed Git-common identities、trust receipts；絕對路徑不得進 canonical records。
- `.exp/runtime.json`：嚴格且 non-secret 的 execution contract，保持與一般 preference config 分離。
- MLflow/DVC/object storage：large artifacts 與 live telemetry authority；canonical records 只保存 bounded observations/digests/sanitized `ExternalRef`。

## Source binding：新增普通 canonical record，不升級 Project

新增 `exp.source/v1` / `KindSource`（typed `src_<uuidv7>`）與 `sources/` layout，而不是把 bindings 塞進 `exp.project/v2`。這可直接沿用一般 `record.Store.Transact`、CAS、recovery 與 linked-worktree ID reservation；Project UUID 仍可安全地 scope transaction journal，既有 Project v1 完全不必重寫。

`research.Source` 包含：

- `Common`（ID、title、timestamps、tags）；
- project-unique immutable `key`（CLI 使用的短 slug）；
- `kind = "git"`；
- immutable Git-root-relative POSIX `subdir`（預設 `.`）；
- append-only、去 credential、normalize 後的 remote locator hints；locator 只是 clone/fork 辨識提示，不是 identity；
- `state = active|retired` 與 optional `retired_at`；被 evidence 引用後不得刪除或改 key/subdir；
- extensions。

沒有可信 remote 的 local-only repo 可建立 Source，但 wizard/CLI 必須明示確認。既有 Attempt v1/v2、Candidate v1、runtime v1 沒有 Source 欄位，繼續精確解讀為「當時的 canonical workspace repo」，不製造虛構 ID。任何新外部執行都要求明確 Source record；若既有 embedded project 要使用新流程，可先以 `exp source add workspace --repo <canonical-root>` 建立正常 Source。

## Local association and invocation context

新增 `internal/workspace`，以一個 resolved `workspace.Context` 明確分開：

- canonical `*project.Info`；
- invocation directory；
- selected `research.Source` 與 resolved local clone/subdir；
- effective config 與逐欄 provenance/trust；
- Project UUID / Source ID / local association observation。

新增 private `$XDG_STATE_HOME/exp/associations/v1.json`（atomic write、0600）：

- Project UUID → canonical repo root/Git common dir；
- `(Project UUID, Source ID)` → local clone root/Git common dir、observed locator hints與時間；
- path 搬移以 `workspace register` / `source register` 修復，不把 host path 寫回 Git。

Resolver 順序：explicit `--workspace`/`--source` → current canonical project → longest containing registered Source/subdir → fail with actionable ambiguity/not-registered diagnostics。每次使用都重新 canonicalize、`gitx.DiscoverWithRunner`、檢查 Git common identity、Source subdir containment/no symlink escape，並在有 locator hints 時至少匹配一項；identity 衝突 fail closed。`cli.App` 新增 injectable resolver/config/association seams，`openProjectStore` 仍只把 canonical `project.Info` 交給既有 `record.NewStore`。

# Implementation plan

## Slice 1 — External workspace, Source records, scoped config

### 1.1 Canonical Source schema and services

- 在 `internal/research/kind.go`, `models.go`, validation/normalization/clone helpers新增 Source kind/schema/ID prefix/display code/state invariants。
- 在 `internal/record/layout.go`, codec/wire/frontmatter/inventory/projection 加入 `sources/`；保持 closed decoders，所有舊 schema 仍拒絕新欄位。
- 新增 `internal/source` service，實作 add/list/show/status/append-locator/retire；更新只能 append locator 或 active→retired，並以 exact revision transaction 驗證歷史引用。
- 新增 `exp source add|list|show|status|register|retire`；`source add` 先產生 pure plan，顯示 sanitized roots、Source key/subdir/locator、預期 revision/effects，confirm 後先 publish canonical Source，再寫 local association；後段失敗回報 `partial=true`，不回滾 canonical authority。

### 1.2 Dedicated workspace setup and discovery

- 保留 `project.DiscoverWithGit` 對 canonical repo 內固定 `experiments/PROJECT.md` 的安全契約；不要讓一般 config 任意重定向 `record.Store.Root`。
- 擴充 `exp init`：既有 flags/noninteractive path 維持 embedded behavior；TTY wizard 預設 dedicated repo，可 review 後建立/採用目標 Git repo、初始化 canonical project、建立首個 Source、寫 association。不得自動建立 submodule、remote、commit 或推送。
- 新增 `exp workspace register|status` 與 persistent `--workspace`, `--source` selectors；既有 `--start-dir` 保留為 invocation-start compatibility flag，不改成 canonical locator。
- `exp context --json` 新增 versioned workspace/source/config view，但保留既有 envelope `exp.cli/v1` 與舊 project fields。

### 1.3 `.exp-cli/config.toml` hierarchy

新增 `internal/config`，明確分離：

1. built-in defaults；
2. `$XDG_CONFIG_HOME/exp/config.toml`（user profiles/defaults）；
3. canonical repo root `.exp-cli/config.toml`；
4. Source repo root `.exp-cli/config.toml`；
5. 從 Source `subdir` 到 invocation cwd 的每層 `.exp-cli/config.toml`，root-to-leaf；
6. allowlisted `EXP_CLI_*` selectors；
7. CLI flags。

Config 必須先在 association/canonical identity 解析後載入，不能自行成為 Project/Source authority。規則：

- strict versioned TOML、unknown fields fail；bounded size/depth、no symlink、不越 Git root；同一檔只載入一次；cwd 不在 selected Source subdir 下 fail。
- scalar last-defined wins；arrays整體替換（明示空值可清除）；named profiles原子替換，禁止 executable/argv/env 從不同 layers 拼接；ordinary keyed tables逐 key merge。
- Project/Source selectors只能缺省或與已解析 identity 相同，衝突不能 last-wins。
- effective fields保留 `{value, source, layer, trusted}`，供 `exp config show|explain|path`、doctor、wizard、JSON/TUI 共用。
- `$XDG_CONFIG_HOME/exp/agents.toml` 保持 legacy input；`.exp/runtime.json` 繼續使用原 strict loader，之後由 runtime v2 演進，不混入 general config。

Repo/subdir config 可直接提供純 presentation/default data；任何 executable/argv、agent/MLflow profile selection/env binding/runtime path 等 execution-bearing內容都需 local trust receipt。新增 `$XDG_STATE_HOME/exp/trust/v1.json` 與 `exp config trust|revoke|list`，receipt 綁定 Project/Source context、canonical Git-common identity、config exact digest與 capability class；內容改變即失效。非 TTY approval 必須同時提供 expected digest 與 `--confirm`。本次不加入 shell hooks。

## Slice 2 — Fast Try, clean/dirty snapshots, promotion gate

### 2.1 Try and evidence schemas

- 新增 `exp.try/v1` / `KindTry`，layout 為 `t-<prefix>-<slug>/TRY.md` 與其 `attempts/`；state 為 `open|concluded|abandoned|adopted`，保存 goal、declared Sources、human conclusion、selected bounded result digests/ExternalRefs、adoption link。
- 新增 `exp.attempt/v3`：owner 為 `run` 或 `try`（exactly one），有一個 executable primary Source、可有多個 read-only Source snapshots；dispatch fields維持 all-or-none。舊 Attempt v1/v2 wire/encoder 完全保留。
- 新增 `exp.idea/v2.origin_try`；`try adopt` 在一個 transaction 中建立 Idea v2 並把 Try標為 adopted，後續仍走 Idea→Plan→clean Experiment/Run/Attempt。
- 新增 `exp.candidate/v2`：直接引用 backing Attempt，從該 clean formal Attempt 複製 `Source ID + head commit + exact change set`，不再只靠 repository-ambiguous SHA 搜尋。Candidate v1/舊 CLI flags只維持 legacy workspace path。
- Inventory 驗證 Source existence/retirement、Try/Attempt ownership、snapshot uniqueness、Candidate↔Attempt exact match；Try-backed、dirty、nonterminal 或非正式 Run-backed Attempt 一律不能建立 Candidate。Champion manifest v2輸出 Source ID、sanitized locator hints、commit/change set，永不輸出 local paths。

### 2.2 Deterministic snapshot capture

新增 `internal/sourcesnapshot`，重用 `gitx.Runner`、`pathx`、full object ID與exact change-set檢查：

- Clean snapshot：Source ID、subdir、object format、base/head full commit、sorted exact change set、capture time、framed digest、`reproducibility=exact`。正式 Run/daemon/Candidate 必須 clean；observational run可明示 `base == head` 且 empty change set（只在新 schema，不放寬 runtime v1）。
- Dirty snapshot只由 `exp try ... --dirty=capture` 明示啟用，不可由 nearest config 靜默開啟。以 porcelain-v2 `-z`、binary/full-index diff digest、tracked state、bounded sorted untracked regular-file mode/size/content hashes、submodule state與 policy version產生 framed digest；canonical record只保存 bounded summary/digests，原 patch/untracked bytes不進 records。
- 拒絕 symlink/device/socket、dirty submodule、rename ambiguity、TOCTOU、entry/file/total byte超限；opened-file identity與結束 re-stat必須一致。大檔改用 provider並記 sanitized content-addressed ref。
- 無 retained bytes 的 dirty snapshot標為 `bounded`，即使另有 exact provider bundle仍保持 dirty/non-promotable；正式 evidence一定 clean rerun。

### 2.3 Direct Try lifecycle

新增 `internal/tryflow`，復用 `record.Store.Transact`、`operation.Store`、`worker.Runner`、durable terminal markers及 `execx`（argv array，永不組 shell string）：

- `exp try run --title ... [--goal ...] [--source ID] [--dirty=capture] -- COMMAND ARG...`
- `exp try retry TRY -- COMMAND ARG...`
- `exp try finish TRY --summary ...`
- `exp try abandon TRY --reason ...`
- `exp try adopt TRY`
- `exp try list|show`

建立 Try/Attempt 後用 idempotency key建立/claim private direct job；canonical→operational 的 crash gap保留 planned Attempt並可由 `try status/retry` reconcile。執行前重新 capture/比對 snapshot，完成後先寫 durable marker，再更新 operation DB與 Attempt terminal state。process success不自動形成 conclusion；finish/adopt都要 human confirmation。Pueue/MLflow 缺失不阻止 direct Try。

## Slice 3 — Cross-repo runtime/worker/worktrees and MLflow profiles

### 3.1 Runtime v2 and controller split

- 在 `internal/controlplane/config.go` 加 `exp.runtime/v2`，每個 Plan runtime明示 primary Source ID、optional read-only Sources、Source-subdir-relative cwd、checkout、base/head/change set/expected outputs；v1保持 legacy canonical-workspace語意。
- 將 `controlplane.Adapter.RepositoryRoot` 拆成 canonical workspace與 Source/runtime resolver。`canonical()`只驗證 inventory/store屬 canonical repo；`verifyRuntimeGit` 對每個 Source的 resolved clone/worktree驗證 exact HEAD/ancestry/clean tree/change set。
- `controlplane.ScopeID`仍以 canonical checkout隔離 outbox/reconciliation，但 job payload另綁 Project UUID、Source IDs與snapshot digests，避免 path hash被誤當 portable identity。

### 3.2 Worker private schema/recovery

- 加入 worker job/workload/terminal/result v2 reader/writer；保留 v1 decoder以完成升級時已排隊工作。
- hidden `worker run` args明示 canonical workspace root、Project UUID、job ID與 fencing token；不可再由 Pueue execution cwd推測 Project。
- Worker從 canonical root開 operation store/marker root，驗證 Project UUID/scope/job claim，再對 Source snapshot重新驗證後才於 Source cwd啟動 workload。
- terminal markers/SQLite始終在 canonical Git common dir。已有 terminal marker的 replay不要求 Source仍存在；尚未開始且 Source無法 resolve 的 job保持 blocked/unknown且不盲目重試。Canonical transaction recovery不依賴 Source availability。
- SQLite初版沿用 versioned JSON payload，不因可由 payload取得的 Source欄位先加 index/schema migration。

### 3.3 External source worktrees

擴充 `experimentgit.Manager.Request` 加 Project UUID、Source ID/subdir；RepositoryRoot由 resolved Source提供。Managed path改為 `$XDG_DATA_HOME/exp/worktrees/<project>/<source>/<experiment>-<slug>`；allowed globs以 Source subdir為語意根並安全轉成 Git-root-relative path。保留 clean base、single child commit、exact allowlist、no merge/remove/unregister契約；不再一律把 source repo 的 `experiments/` 當 control metadata。

### 3.4 Named MLflow profiles and readiness

在 general config加入 named MLflow profiles：non-secret context slug、binary/timeout、child-env→parent-env name mapping、secret/required flags、default metric allowlist；不得存 tracking URI/token/password值、run ID或 raw artifact URI。選擇順序為 flag→subdir→Source root→canonical→user default；現有 `--allow-env/--secret-env/--mlflow-context` 保留為 highest-priority compatibility override。

`mlflow.Adapter` 維持 read-only selected-field contract；workload仍建立/log MLflow run，Attempt/Try只附 context、run ID、selected metrics/tags、sanitized URI與 observation time。擴充 provider readiness model為 installed/not-probed/ready/misconfigured/unsupported/unknown；`doctor`預設仍只 LookPath，`doctor --live`才做有 timeout、無 install/login/daemon-start 的明示 probe。

## Slice 4 — Wizard, help, completion, color, read-only TUI

### 4.1 Shared presentation contracts

- 在 `cli.App` 注入 TTY detection、color mode、prompter、terminal width與 TUI runner；所有 domain mutations仍走既有 services/transactions。
- 新增 `--color auto|always|never` semantic roles；先經 `safeHumanOutput`/redaction sanitize dynamic text，再由 renderer加 trusted ANSI。`--json`永不 ANSI/prompt；non-TTY human output維持 deterministic/plain；遵守 `NO_COLOR`、`TERM=dumb`。
- 參考 `dev-cli/internal/cli/prompt.go` 建小型 buffered/TTY-aware prompt abstraction；wizard一律 gather→pure validate→redacted effect plan→confirm→revalidate revision/config/snapshot→apply。Esc/Ctrl-C/EOF/decline皆為 zero mutation。
- Wizard覆蓋 dedicated `exp init`、source add/register、try run/finish/adopt、config trust。已提供完整 flags時不提示；JSON與non-TTY缺必要輸入時只回 usage error。

### 4.2 Discoverable help and completion

- Root/family help加入 ASCII-only flow：`Try → conclude → adopt Idea`、`Idea → Plan → Queue → Experiment → Evaluation`、`Candidate → Release → holdout → human Promotion`，並保持標準 `exp help <command>`。
- 新增 embedded `exp guide setup|quick|research|promotion|config`；由單一 map將 family help連到 concepts，測試 command/topic不漂移。
- 恢復 Cobra completion並提供 local-only dynamic candidates：record display codes/IDs、Source/Try、profiles、targets。Completion尊重 `--workspace/--source`，不 contact provider；未初始化/invalid config只退化為無 dynamic candidates，不污染 shell stderr。
- 所有新增 command同步 `approvedCommandReference`、embedded skill與 command-map生成檢查。

### 4.3 Explicit read-only `exp ui`

新增小型 callback-injected Bubble Tea TUI（不複製 `dev-cli` 的 dashboard state）：

- views：workflow/context、Sources/config resolution、open/recent Tries、queue frontier、active Attempts、Candidates/Champions、provider readiness；
- initial frame只讀 local canonical inventory/association/config，沒有 network/auth/daemon mutation；provider live probe只在明示 refresh時 lazy執行；
- 每個 async message帶 generation/identity/observed-at，reject stale results；失敗 refresh保留舊 snapshot並標 `STALE/PARTIAL`，成功 empty可清除舊 rows；
- missing optional tools顯示 disabled + reason/remediation，不隱藏、不使整個 UI退出；
- 初版無 mutation key bindings；任何後續 apply TUI 必須另有 persisted plan ID、authority fingerprint、revalidation與 effect ledger；
- narrow terminal、CJK/ANSI width、resize、quit/cancel、non-TTY refusal與 unsupported-platform fallback都有測試。新增 Charm dependencies只限 Bubble Tea/Bubbles/Lip Gloss/x/term/x/ansi 的實際使用面，並保住 Windows/AIX cross-build（必要時以 platform adapter/build-tag stub退化）。

# Backward compatibility and migration

- Project v1不變；existing embedded projects不需搬移、不需 Source record即可繼續使用所有舊命令。
- 所有舊 record/runtime/worker exact schema decoders保留 closed behavior；外部 Source只產生新 Attempt v3/Candidate v2/runtime v2/worker v2。
- Candidate v1保持 legacy same-repo SHA/change-set語意；新 external Candidate不允許走舊 flags造成 ambiguous provenance。
- `agents.toml`、runtime v1、`--start-dir`至少保留一個 major release，`config explain`標示 legacy來源；不自動批次重寫 inventory。
- 增加 `exp migrate runtime plan|apply`，把 v1 entries明示映射到已建立的 workspace Source，先顯示 diff、config digest與 expected revision再寫 v2。
- JSON envelope仍為 `exp.cli/v1`；每個新增 command只 version其 `data` payload。Warnings只到 stderr，partial publication如實標示。

# Critical files and reuse points

- Composition/discovery：`internal/cli/app.go` (`App` seams)、`internal/cli/project_commands.go` (`openProjectStore`)、`internal/project/discover.go`/`init.go`、`internal/gitx/*`、`internal/pathx/*`。
- Canonical model/storage：`internal/research/kind.go`, `models.go`, `control_models.go`, validation/normalization；`internal/record/layout.go`, codec/frontmatter/wire/inventory/store/transaction/projection。保留 `record.Store.Transact`、prepared journal與 exact revision checks。
- New packages：`internal/workspace`（association+resolver/context）、`internal/config`（layers/provenance/trust）、`internal/source`（binding lifecycle）、`internal/sourcesnapshot`（clean/dirty capture）、`internal/tryflow`（direct lifecycle）。
- Runtime：`internal/controlplane/config.go`, `adapter.go`, `git_verify.go`；`internal/operation/types.go`/SQLite store；`internal/worker/runner.go`, `git_verify.go`；`internal/cli/worker.go`, `daemon.go`；`internal/experimentgit/experimentgit.go`。
- Provider：`internal/provider/registry.go`/result types、`internal/mlflow/adapter.go`、`internal/execx/*`；沿用 tri-state support、sanitized refs、secret-env-name binding。
- UX：`internal/cli/root.go`, output/errors/renderers/command reference及新增 prompt/guide/completion files；新增 `internal/tui`，只透過 injected callbacks讀既有 view/service。
- Reference patterns：`dev-cli/internal/cli/color.go`, `prompt.go`, `tldr.go`；`internal/projectconfig/load.go`/`trust.go`；`internal/tui/readiness.go` 與 `internal/flowtui` 的 generation/revalidation設計，只抽契約不複製 domain model。

# Verification

## Unit/contract tests

- Source/Try/Idea v2/Attempt v3/Candidate v2 exact codec round trips、old-schema unknown-field rejection、ID/layout/reference/retirement/immutability。
- Config precedence、atomic profile replacement、identity conflict、per-field provenance、size/depth/symlink bounds、trust digest/capability失效。
- Association ambiguity/path move/wrong Git common dir/locator mismatch/local-only confirmation。
- Snapshot deterministic framing、clean empty change set、dirty path ordering、untracked bounds、symlink/submodule/TOCTOU拒絕。
- Candidate拒絕 dirty、Try-backed、nonterminal、Source mismatch；manifest不得洩漏 host paths。
- Runtime/worker v1/v2 compatibility、marker replay、fencing/scope/Project mismatch、missing Source blocked state。
- Color plain-equivalence、JSON no ANSI/no prompt、non-TTY behavior、wizard decline zero effects、completion quiet fallback、TUI stale generation/empty refresh/resize/quit/model tests與 Unix PTY smoke test。

## Multi-repository integration scenarios

以 temp Git repos建立 canonical A、Source B（含 subdir）、A/B linked worktrees與 optional Source C：

1. dedicated init後從 A、B root、B subdir解析同一 Project UUID；ambiguous/unregistered/moved clone fail或可 register修復；所有 canonical writes與SQLite只落 A。
2. clean direct Try、dirty direct Try、finish/adopt；dirty Try不能 Candidate，adopt後 clean formal rerun可建立 Candidate v2。
3. external experiment worktree在 B建立 exact branch/commit/allowlisted change set，A只記 Source-aware provenance。
4. Pueue cwd位於 B，但 hidden worker由明示 A定位 job/marker；B消失後完成 marker仍可 replay，未開始 job則 blocked。
5. MLflow profile只傳允許的 env values，canonical/JSON/log不出現 secret；MLflow/Pueue缺失時 direct Try和local UI仍可用。

## Repository checks

- `make skill-check`
- `make fmt-check`
- `make vet`
- `make test-race`
- `make test-build-portability`
- `make build` 並驗證 version injection
- `make docs-build`
- 另跑 focused real-Git integration與 PTY tests，最後確認 working tree只含預期檔案。

# Documentation and end-to-end acceptance

同步 README、English/zh-TW architecture/record-format/configuration/command-map/runtime/evidence/MLflow/Git-worktree/roadmap頁面、embedded skill與 command reference。新增雙語 dedicated-workspace/source-binding、quick-Try/dirty-boundary、repo-vs-submodule-vs-monorepo指南；明列 artifact authority、config precedence/trust、legacy embedded相容性與 optional-tool降級。

最終人工 acceptance flow：在 production repo B執行 dedicated init wizard建立 private experiment repo A → add/register B/subdir → `config explain`確認 layers/trust與 MLflow profile → dirty `try run`、finish、adopt → clean linked worktree正式 rerun → cross-repo daemon/worker記錄 Attempt → Evaluation/Candidate/manifest包含 Source-aware clean provenance → `exp ui`在 MLflow/Pueue缺失與存在兩種情況都能清楚顯示狀態而不改資料。