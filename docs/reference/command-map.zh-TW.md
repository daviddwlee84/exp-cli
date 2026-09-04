# 指令地圖

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

目前 approved、generator-backed CLI metadata 有 **112 個 command paths**。本 authored
map 依 user task 分組，不重複每個 flag。

執行中的 binary 是 syntax authority：

```bash
exp <command> --help
```

Command 支援 `--json` 時請讀 versioned envelope，不要 parse human output。與 build
exact matching 的 inventory 是 generated
[`internal/skill/exp-cli/references/commands.md`](https://github.com/daviddwlee84/exp-cli/blob/main/internal/skill/exp-cli/references/commands.md)；
maintainer 只能透過 `exp skill sync` 更新。

## 先找下一個 command

| Need | Route | 開始 | 後續 |
|---|---|---|---|
| 建立或 reconnect Project/Source | Setup | `exp init` 或 `exp workspace status` | `source status` → `config show` |
| 回答 short bounded question | Quick exploratory Try | `exp try run` | `try status` → `try finish` → optional `try adopt` |
| 產生 governed comparable evidence | Rigorous Experiment | `exp idea add` | qualify → Queue → runtime v2/daemon → Evaluation v2/Candidate v2 |
| 把 validated evidence 移到 target | Promotion | `exp candidate create` | Release → sealed holdout → named-human Promotion |
| Safe resume/browse | Read-only | `exp context` | `exp guide`、completion 或 `exp ui` |

Conceptual terminal guides 與 standard Cobra utilities 刻意不計入 112 個
generator-backed domain paths：

```bash
exp guide [setup|workspaces|config|quick|research|promotion]
exp completion [bash|fish|powershell|zsh]
exp help [command]
```

Completion/guide rendering 只讀 local records/config，不 probe provider 或 resolve secret
value。

## 建立並 inspect Project（7）

| Command | 用途 |
|---|---|
| `exp` | 進入 control plane，或用 `--skill` print embedded skill。 |
| `exp init` | 初始化 embedded Project，或用 TTY/explicit dedicated-repository flow 建 initial Source。 |
| `exp context` | 不 refresh provider，讀取 local resumable summary。 |
| `exp ui` | 在 real terminal stdin/stdout 開啟 read-only local TUI；只有 explicit refresh 才做 readiness probes。沒有 JSON mode，請用 `context --json`。 |
| `exp doctor` | Discover core/optional capabilities。Default 只 executable lookup；`--live` 做 bounded version/read-only local-service probes，不 login、install、寫 config、start service 或 execute workload。 |
| `exp validate` | 不呼叫 provider，validate canonical records/graphs。 |
| `exp render` | Generate deterministic projections，或用 `--check` 回報 drift。 |

## Resolve workspace backends 與 associations（7）

| Command | 用途 |
|---|---|
| `exp workspace` | Discover canonical workspace association commands。 |
| `exp workspace register` | 在 current host register resolved Project clone。 |
| `exp workspace status` | 顯示 resolved Project/Source、associations 與 config trust summary。 |
| `exp workspace backend` | Discover workspace-provider selection/capability commands。 |
| `exp workspace backend list` | 列出 `native_git`/optional providers；`--probe` 做 bounded local checks。 |
| `exp workspace backend status` | 顯示 requested/actual preparation provider 與 fallback reason。 |
| `exp workspace backend handoff` | Request 透過 selected provider open 一個 exact managed Try Attempt；unsupported capability 仍 explicit。 |

`native_git` 是 correctness baseline。`dev_cli` 可 discovery，但在 `dev` 提供
schema-versioned exact-path machine capability receipts 前，其 prepare/open/handoff/retire
lifecycle unsupported。缺少 `dev` 不隱藏 action，也不 block native Try。

## 管理 canonical Sources 與 clone associations（8）

| Command | 用途 |
|---|---|
| `exp source` | Discover canonical Source/clone-association commands。 |
| `exp source add` | Review、publish、associate Source repository/subdirectory；missing fields 可用 TTY wizard。 |
| `exp source list` | 不接觸 provider，列出 canonical Sources。 |
| `exp source show` | 顯示一個 Source identity、state、subdir、path、revision。 |
| `exp source status` | Validate 一個或全部 Sources 的 host-local associations。 |
| `exp source register` | Register/repair moved/recloned Source clone association。 |
| `exp source append-locator` | 經 exact-revision CAS/confirmation append sanitized locator。 |
| `exp source retire` | 經 exact-revision CAS/confirmation retire active Source。 |

## Inspect config 與 trust（7）

| Command | 用途 |
|---|---|
| `exp config` | Discover layered config/exact-digest trust commands。 |
| `exp config path` | 顯示 candidate/applied user、canonical、Source、subdirectory config paths。 |
| `exp config show` | 顯示 effective non-secret values/safe provenance summary。 |
| `exp config explain` | Explain 全部 layers 或單一 effective field，含 digest/capability trust。 |
| `exp config trust` | 依 exact digest review/approve current applied config 或 runtime v2 file；missing fields 可用 TTY wizard。 |
| `exp config revoke` | Revoke current/deleted scope，可依 digest/capability narrow。 |
| `exp config list` | 列出 sanitized local trust receipts。 |

Repository-selected Source/workspace/agent/MLflow settings 在 winning exact digest trusted 前
都是 inert。選擇 Source 時，repository-layer receipt 會綁定該 Source ID，因此 explain/trust
應使用與 execution 相同的 authority context。Runtime v2 另要求 raw `.exp/runtime.json` 的
`runtime.dispatch` trust。

## 執行 agents 與管理 embedded guidance（8）

| Command | 用途 |
|---|---|
| `exp agent` | Discover profile inspection/direct fresh-agent execution。 |
| `exp agent profiles` | Validate/list configured `exp.agents/v1` profiles。 |
| `exp agent run` | 依 supplied JSON Schema output contract 執行一個 fresh profile。 |
| `exp skill` | Discover version-matched embedded-skill commands。 |
| `exp skill check` | 不 mutation，check installed files、compatibility、hashes、consumer links。 |
| `exp skill install` | Atomically install embedded skill/safe consumer links。 |
| `exp skill print` | Print 此 build embedded `SKILL.md`。 |
| `exp skill sync` | Generate source-tree `references/commands.md`；`--check` 只 verify exact drift。 |

## 執行並 conclude bounded Try（12）

| Command | 用途 |
|---|---|
| `exp try` | Discover bounded managed-native-worktree exploration。 |
| `exp try run` | Register/execute argv-only clean 或 explicit `--dirty=capture` Try；missing fields 可用 TTY wizard。 |
| `exp try retry` | 用相同 Source/argv identity 為 terminal Attempt 建 successor。 |
| `exp try resume` | 只 resume provably unstarted work 或 import verified durable marker。 |
| `exp try reconcile` | Import late evidence，或 confirmation 後 explicit abandon uncertain Attempt。 |
| `exp try finish` | 用 owned result digests/refs 或 explicit `--no-results` 記錄 reviewed human conclusion。 |
| `exp try abandon` | 記錄 abandoning open Try 的 confirmed human reason。 |
| `exp try adopt` | Atomically adopt concluded Try 成 Idea v2；missing classification 可用 TTY wizard。 |
| `exp try list` | 列出 canonical Tries/Attempt counts。 |
| `exp try show` | 顯示一個 Try canonical/local direct status。 |
| `exp try status` | 不 execute，inspect Attempts/jobs/markers/worktrees。 |
| `exp try cleanup` | 只移除 verified manager-owned worktrees/seed bundles；保留 canonical evidence/branch/markers。 |

缺少 MLflow、Pueue 或 `dev` 不 block native Try。Dirty capture 是 Try-only，不能直接
支持 Candidate v2。

## Capture、qualify、prioritize research（23）

### Ideas 與 Plans

| Command | 用途 |
|---|---|
| `exp idea` | Discover Idea capture/qualification commands。 |
| `exp idea add` | 建立 unqueued canonical Idea。 |
| `exp idea develop` | 請 fresh agent 提出 queue-ready Plan，可 optional apply。 |
| `exp idea list` | 列出 canonical Ideas。 |
| `exp idea qualify` | Atomically 把 Idea 變成 fully priced Plan。 |
| `exp plan` | Discover priced-Plan commands。 |
| `exp plan add` | 從 flags/versioned JSON 建 validated Plan。 |
| `exp plan list` | 不 contact provider，列出 canonical Plans。 |
| `exp plan refresh` | Reassess utility、repin current Finding beliefs，reranking 前移除 stale Plan。 |

### Policy、resources 與 Queue

| Command | 用途 |
|---|---|
| `exp policy` | Discover autonomy/Queue-policy commands。 |
| `exp policy autonomy` | 經 explicit auto-experiment confirmation gate change autonomy。 |
| `exp policy cluster-set` | Set saturation thresholds 或 explicit reopen direction。 |
| `exp policy init` | 建立 default-manual canonical `POLICY.md`。 |
| `exp policy show` | 顯示 current canonical research policy。 |
| `exp pool` | Discover ResourcePool commands。 |
| `exp pool add` | 建立 named constrained compute/human ResourcePool。 |
| `exp pool list` | 列出 canonical ResourcePools。 |
| `exp queue` | Discover Pool/lane frontiers 間的 Plan ranking。 |
| `exp queue create` | 為 selected Pools 建 exploit/explore partitions。 |
| `exp queue insert` | Score/insert Plan，可加 listwise advice/order-swapped battles。 |
| `exp queue list` | 列出 canonical Queues。 |
| `exp queue remove` | 用 exact Queue compare-and-swap 移除 Plan。 |
| `exp queue show` | Inspect ordered Pool/lane entries/pinned revisions。 |

## Prepare code 並操作 formal execution（19）

### Daemon（7）

| Command | 用途 |
|---|---|
| `exp daemon` | Discover local orchestration commands。 |
| `exp daemon frontier` | 不 contact Pueue，inspect canonical frontiers/runtime config。 |
| `exp daemon status` | 不 contact provider，讀 local daemon state。 |
| `exp daemon tick` | 執行一次 Pueue reconciliation/capacity-admission pass。 |
| `exp daemon run` | Continuous reconcile/admit，直到 cancel。 |
| `exp daemon pause` | Pause new dispatch，保留 reconciliation state。 |
| `exp daemon resume` | Resume eligible dispatch。 |

### Experiment workspaces 與 closure（6）

| Command | 用途 |
|---|---|
| `exp experiment` | Discover isolated workspace/scientific-lifecycle commands。 |
| `exp experiment agent` | 在 isolated worktree 執行 configured implementation agent，commit exact allowlisted changes。 |
| `exp experiment workspace` | Discover preparation/commit commands。 |
| `exp experiment workspace prepare` | 在 exact base commit 建 isolated worktree。 |
| `exp experiment workspace commit` | 只 commit observed allowlisted changes。 |
| `exp experiment close` | Atomically conclude Experiment、complete Plan、dispose evidence、publish Findings。 |

### Implemented provider operations（6）

| Command | 用途 |
|---|---|
| `exp provider` | Discover supported tools 的 explicit audited reads/controls。 |
| `exp provider mlflow` | Discover read-only MLflow verification。 |
| `exp provider mlflow verify` | Verify workload-created run 的 selected metrics/tags。 |
| `exp provider pueue` | Discover supported Pueue reads/controls。 |
| `exp provider pueue status` | 讀 sanitized task/group snapshot。 |
| `exp provider pueue cancel` | Confirmation 後 cancel one exact matching task。 |

Daemon dispatch 需要 Pueue；command visibility/native Try 不需要。MLflow 是 optional
observation；missing observation 不會成為 scientific verdict。

## Evaluate、package、promote evidence（13）

| Command | 用途 |
|---|---|
| `exp evaluation` | Discover comparable-protocol/immutable-result commands。 |
| `exp evaluation spec` | Discover scientific/promotion EvaluationSpec commands。 |
| `exp evaluation spec create` | 建立帶 bounded ResourcePool budget 的 comparable metric protocol。 |
| `exp evaluation create` | 記錄 immutable Evaluation；`--attempt` 把 Evaluation v2 綁 exact formal Attempt。 |
| `exp candidate` | Discover Candidate creation。 |
| `exp candidate create` | 從 clean supported Attempt-bound evidence 建 Candidate v2，或使用 explicit legacy v1 shape。 |
| `exp release` | Discover typed Release composition。 |
| `exp release create` | 從 named Candidate slots 建 draft/atomically validated Release。 |
| `exp promotion` | Discover human-only production Promotion commands。 |
| `exp promotion spec-create` | 建立 sealed/bounded/human-gated PromotionSpec。 |
| `exp promotion append` | 對單一 target chain append confirmed named-human outcome。 |
| `exp champion` | 顯示由 append-only Promotion chain derived Champions。 |
| `exp champion manifest` | Render deterministic downstream manifest。 |

## Inspect records 並 safe migrate（8）

| Command | 用途 |
|---|---|
| `exp migrate` | Discover explicit harness-v0 migration。 |
| `exp migrate plan` | 建 read-only fingerprinted plan，surface ambiguities。 |
| `exp migrate apply` | Apply fully reviewed/fingerprint-validated plan。 |
| `exp record` | Discover canonical record inspection/transaction commands。 |
| `exp record list` | 列出 Git-backed records，可依 kind filter。 |
| `exp record show` | Resolve/show record view、JSON envelope 或 normalized Markdown。 |
| `exp record transaction` | Apply supported low-risk prepared Idea/ResourcePool transaction。 |
| `exp record recover` | 從 exact hashes roll durable prepared transaction forward。 |

以上 section count 合計 **112** 個 approved paths。如果 `exp <command> --help` 或
generated `commands.md` 沒有某個 syntax，不可從 roadmap、note 或舊文件推定。

Large datasets、model/checkpoint/artifact bytes、traces 與 unbounded logs 留在 MLflow、
DVC 或 object storage。只有 bounded summaries、digests、exact identities 與 sanitized
references 進入 canonical Git records。
