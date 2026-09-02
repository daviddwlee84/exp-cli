# Provider 契約

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

## 邊界

Providers 公開已安裝上游工具的能力；它們不會成為研究 records 的權威來源。目前的實作
包含已稽核的 Pueue scheduler 操作、私有 direct worker envelope，以及唯讀的 MLflow run
驗證。預設的 `doctor` 仍只探測 executable 是否存在。沒有動態 Go plugin ABI，也沒有
通用的「experiment provider」interface。

一個 adapter 會宣告一份描述元 (descriptor)，且只實作它支援的角色：

| 角色 | 責任 | 初始方向 |
|---|---|---|
| Runner | 將 entrypoint 準備成 argument-array workload | 私有 direct worker 已實作；notebooks 稍後支援 |
| Scheduler | 提交、觀察並取消一個 Attempt；擁有 native dependencies | Pueue 已實作；Slurm/DVC 稍後支援 |
| Tracker | 解析/列出 provider 擁有的 telemetry 與 sweep state | MLflow selected-field verification 已實作 |
| ArtifactStore | stat/列出不可變的 artifact references | DVC/MLflow/object references；絕不隱式下載 |
| Registry | 取得/列出/解析 model resources 的 aliases | 僅在具體 API 已驗證後提供唯讀操作 |

一個 provider 可以實作多個角色，但每個操作只屬於一個角色與一項 capability。每個
Attempt 恰好只有一個 Scheduler owner。除非有明確且經審查的操作計畫指定 concurrency
與 cancellation 的歸屬，否則拒絕巢狀 schedulers。

一般消費者使用的 Google Colab browser sessions 沒有持久且通用的 control plane，因此
不受支援。未來的 adapter 必須以特定名稱指明已有文件的 enterprise service，例如適用的
Vertex/Colab Enterprise API；不會 scrape 消費者 UI。

## Descriptor 與 capability probing

Descriptor 是靜態的，包含：

```text
provider name
implemented roles
candidate binary names
known capability names
adapter contract version
```

探測 (probe) 會回傳：

```text
provider
configured context
resolved binary path (sanitized for display where necessary)
provider version
capabilities: capability -> support
observed_at
diagnostics
```

支援狀態採三態 (tri-state)：

```text
supported | unsupported | unknown
```

- `supported`：adapter 已驗證所需的 binary/version/feature contract。
- `unsupported`：adapter 確知此 provider/version 缺少該能力。
- `unknown`：discovery 或安全驗證無法得出結論。

缺少選用 binaries、無法連線的 daemons、無法取得的 accounting，以及未知版本，會使該
provider 降級；但不會讓本機 record commands 失敗。絕不會因樂觀解析而將 `unknown`
提升為 `supported`。

預設的 `exp doctor` 僅透過 `LookPath` 進行本機 executable discovery。它絕不執行第三方
`--version`，因為名義上唯讀的 flags 仍可能建立 configuration、telemetry 或 log state；
因此探索到的 versions 與 capabilities 會維持 `unknown`。目前 `--live` 不會進行任何額外
聯絡。只有 `provider pueue status`、`provider mlflow verify`、`daemon tick` 或
`daemon run` 等針對特定操作的 command 才會聯絡 provider。Probing 與 operations 絕不
安裝 package、啟動 daemon、遷移 provider database，或開啟 authentication。

## 操作計畫 (operation plans) 與 effects

叫用 (invocation) 前，adapter 會建立可審查的計畫：

```text
provider
context
role
capability
operation
executable
argv[]
cwd
environment names and sensitivity (never secret values)
timeout/output bounds
effects
sanitized diagnostics
```

Effects 是有版本的集合，且只能取自：

```text
local_read
remote_read
local_write
remote_write
executes_user_code
starts_service
credential_flow
destructive
sensitive_output
blocking
```

範例：

| 操作 | 必要 effects |
|---|---|
| 本機 binary version | `local_read` |
| `pueue status --json` | `remote_read`、`sensitive_output` |
| 遠端 MLflow run lookup | `remote_read`，需要 credentials 時另有 `credential_flow` |
| artifact download | `remote_read`、`local_write`，也可能有 `credential_flow` |
| Marimo/Jupyter execution | `executes_user_code`，通常另有 `local_write` |
| daemon startup | `starts_service`、`local_write` 或 `remote_write` |
| queue reset 或 garbage collection | `destructive`、`remote_write` |
| follow/wait | read effect 加上 `blocking` |

未來的 command 若提供 `--plan` 或 `--dry-run`，這些模式只能呈現目前已知的 plans 與
effects。它們不會進行 invocation、聯絡 remote 或 daemon、authentication、package
resolution、service startup、建立檔案、寫入 cache，或執行 user code。目前的 Pueue
cancellation 與 Promotion 使用明確的 confirmation flags，而不會佯裝成 dry-run
interfaces。

## 叫用契約

每個 subprocess 都透過單一注入、可感知 signal 的 `execx.Invoker`。Adapters 絕不自行
呼叫 `os/exec`。

Invoker 接受 executable 與 argument slice，而絕不接受串接完成的 shell command。它也
接收明確的 canonical cwd、environment specification、context cancellation、timeout、
stream/capture mode 與 byte limits，並精確保留 argument boundaries。Errors 與
diagnostics 只包含經結構化遮蔽 (structural redaction) 的 display arguments。

Provider protocol output 會受到大小限制、經過解析與遮蔽後才回傳。User workload output
可串流至 caller，但預設不會保存。Process-group ownership 是 Unix-specific：
cancellation、timeout 與正常 parent exit 都會終止產生的 Unix process group，讓一般
descendants 不會在 invocation 結束後繼續存活。Windows 僅使用 Go 的 direct-child
cancellation；終止 descendant tree 需要整合 Job Object，而本專案不宣稱具備此能力。
Runtime CI 目前會在 Linux 與 macOS 上執行；Windows 與 AIX jobs 是 cross-build checks，
不代表 runtime-test claims。

如果上游 scheduler 只接受 shell payload，adapter 必須使用單一經稽核的 escaping
implementation，或叫用保留 arguments 的私有 `exp` execution envelope。它不得將
titles、labels、native state、metric values 或 external text 插入 shell syntax。

任何 adapter 都不得隱式進行下列操作：

- 安裝或升級 binary 或 Python package；
- 叫用 `uvx`、`pip`、PEP 723 resolution 或其他 package resolver；
- 啟動 provider service 或 daemon；
- 發起 login、OAuth、browser、keychain 或 credential setup；
- 在 local-only commands 中進行 network contact；
- 下載 artifacts；
- 在 inspection 期間執行 notebook 或 user workload code。

## 解析 (parsing) 與正規化結果

解析優先順序是強制性的：

1. Native JSON。
2. Native CSV，或明確指定的固定 delimiters/fields。
3. 明確設定且具有版本的 SDK 或 REST implementation。
4. `raw_only`，搭配受限且已清理的 output 與一則 diagnostic。

絕不 scrape 美化過的 terminal tables。Adapter 不認識的 native enum value 會對應至正規化
(normalized) 的 `unknown`，同時保留已清理的 native token/reason，並發出 diagnostic。
未知的 terminal states 會採取封閉式失敗 (fail closed)。

Provider observation 包含：

```json
{
  "provider": "pueue",
  "context": "local-synthetic",
  "provider_version": "4.0.4",
  "capability": "scheduler.observe",
  "support": "supported",
  "source": "pueue status --json",
  "observed_at": "2026-08-20T10:05:00Z",
  "stale": false,
  "partial": false,
  "normalized_state": "succeeded",
  "native_state": "Done",
  "native_reason": "",
  "raw_only": false,
  "raw_state": {},
  "diagnostics": []
}
```

`raw_state` 是選用欄位、大小受限，且已經過結構化遮蔽。Observation data 不會只因為是
有效 JSON 就成為規範性資料。Cache entries 會帶有 source、observation time、completeness
與 freshness policy，並維持可拋棄性。

Machine commands 會以 CLI envelope 包裝資料：

```json
{
  "schema_version": "exp.cli/v1",
  "command": "context",
  "ok": true,
  "partial": false,
  "observed_at": "2026-08-20T10:05:00Z",
  "data": {},
  "diagnostics": []
}
```

Machine mode 絕不提示使用者，stdout 也不會輸出 warnings 或 progress。

## Refresh 語意 (semantics)

本機規範性讀取是預設行為。`plan list`、`validate`、`render`、`context`、
`daemon status` 與 `daemon frontier` 不會進行任何 provider invocation。只有透過
`provider` commands 或 daemon 的 `tick`/`run` 才會明確聯絡 provider；cached
observations 絕不會在無提示下 refresh。

未來若新增組合式 refresh interface，它必須明確指出精確的 provider
contexts/capabilities、保留 partial success 與各 provider diagnostics，且絕不能改變
Experiment verdict 或 evidence inclusion。Attempt state 只能透過明確的 reconciliation
與 canonical revision checks 推進。

## Environment 與 credential 處理

Configuration 儲存的是 environment-variable name、upstream profile、credential-file
selector 或 keychain selector 等參照，絕不儲存 secret value。

Invocation 從 executable discovery 與 upstream profile selection 所需的最小 allowlist
開始。Non-secret bindings 必須明確指定。Secret references 只會在 process start 前一刻
解析；operation rendering、diagnostics、caches、markers 與 canonical records 都無法取得
其值。應盡可能使用 provider profile/environment authentication，而非 argv。

遮蔽 (redaction) 採結構化方式，並在資料跨越 adapter boundary 前完成：

- 移除 URI userinfo；移除類似 credential 的 query parameters；其餘 query keys 與 values
  也會遮蔽；canonical record 只會收到移除 query 的 sanitized URI。
- Authorization headers、cookies、secret environment values 與已知 credential arguments，
  都會在建立 errors 或 output 前遭到替換。
- Capture 與 stream redactors 會以每個明確或由結構推斷的 sensitive argv value，以及每個
  已解析的 secret environment value 作為初始資料。
- Logs、traces、prompts/completions、stderr 與 raw provider state 都分別視為 sensitive，
  且受到大小限制。
- Secret canaries 絕不能出現在 stdout、stderr、JSON、diagnostics、cache、markers 或
  canonical records 中。

無法安全保留身分時，會拒絕不安全的輸入；禁止悄悄保留含有 credential 的原始字串。

## 必要的 provider guardrails

### Pueue

Pueue 4.x 的 `status --json` task objects 可能包含已擷取的 `envs`。必須在 parsing 回傳，
或 raw state 跨越 adapter boundary 前，遞迴移除整個 `envs` member。不能只遮蔽目前已知的
keys。由於 daemon 會保存 task environments，submissions 使用明確、不含 secret 且不屬於
credential-sensitive 的 environment allowlist。因此 Pueue 會拒絕 runtime `secret_env`；
workload 必須在啟動後，透過 broker 或 provider profile 取得 credentials。

已實作的 adapter 提供 sanitized status，以及只有在下列條件都成立時才能搭配 `--confirm`
執行的 exact-task cancel：由一個 canonical Attempt 指定 Pueue 負責 scheduling、其 reference
使用 local runtime context，且 live task ID、group 與 label 全都符合 configured route。
Submission 僅限已稽核的私有 `exp worker run` envelope。任意 titles、provider text 或 user
shell fragments 都不能成為 submitted command。Pool bindings 會選擇明確的 Pueue groups；
stable labels 支援 submit 狀態不明時的 outbox recovery。

### Slurm

絕不產生 `--export=ALL`。使用明確的 allowlist 或 site-approved profile。即使在本機叫用，
controller/accounting commands 也視為 remote reads。優先使用已驗證的 JSON support；否則
透過 `--parsable2 --noheader --format` 要求具名的 fixed fields。保留 cluster、array 與
step identity。缺少或延遲的 accounting 會產生 partial/unknown observations，而非猜測
terminal state。

### MLflow

在考慮明確的 SDK/REST capability 前，先解析 native JSON 與 stdout CSV。絕不隱式進入
isolated Python environment。以結構化方式遮蔽 tracking 與 artifact URIs 中的 userinfo、
query parameters、tokens 與 doctor output。要求 storage credentials 前，先判定是 proxy
還是 direct artifact access。Tracker 擁有的 trials 與 telemetry 留在 MLflow；canonical
records 只保留選定 evidence 與已清理的 references。

已實作的 integration 是唯讀的：workload 會自行建立並記錄自己的 run，接著
`exp provider mlflow verify --run-id ...` 只要求具名 metrics 與 expected tags。它不會建立
run、記錄 metric、上傳 artifact 或變更 registry state。只有在宣告的 `exp.attempt_id` tag
識別出成功的 canonical Attempt，且該 Attempt 的 Run 屬於該 Experiment、Candidate 的
Experiment，或 Release 的 combination/single-slot Experiment lineage 時，verified run 才
能以 sanitized identity 連結至 Evaluation。之後由 Evaluation 建立 Candidate 時，該 owner
必須等於 Candidate 所納入且成功的 backing Attempt。Verification 本身不是科學結論。

### DVC、notebooks 與後續系統

DVC capabilities 會在實際 binary/version 可用後，逐項操作進行 probe。Marimo 與 Jupyter
是 Runners/entrypoints，而非持久 schedulers；inspection 不得觸發 sandbox/package
resolution 或 notebook execution。W&B、Kaggle、Ray、Kubernetes 與 cloud control planes
需要各自獨立且經驗證的 contracts，不能從已安裝 libraries 或 prose references 推斷。

## Daemon 與操作狀態

`.exp/runtime.json` 是 canonical IDs 與 provider-native configuration 之間的嚴格 execution
binding。它包含 `exp.runtime/v1` schema、Pool 至 Pueue group/label bindings，以及 Plan 至
executable/argv/cwd/timeout/Git identity bindings。Allowed environment arrays 只能包含
non-secret names；由於 task environments 會被保存，Pueue runtime secret arrays 必須為空。
同一個 Pueue group 內的 label prefixes 必須兩兩 prefix-free，並為完整的 scoped dispatch
ID 保留足夠空間。每個 executable ChangeSet 都會排除實際選定的 runtime config path。

Daemon 的 SQLite database 位於
`<git-common-dir>/exp/runtime/v1/control.sqlite`，擁有 leases、fencing tokens、jobs、
submission outbox、provider observations、fairness counters、pause state，以及 payload
大小受限的 hash-chained event log。它對 hypotheses、conclusions、Findings、Evaluations、
Releases 或 Promotions 沒有任何權威。

每次 tick 會在准入前建立 Pueue snapshot、調和已知 Attempts，並僅透過 scoped stable
label 恢復此 canonical worktree 的 outbox entries；接著以宣告的 pool units 計入已恢復與
狀態未知的 nonterminal tasks，再填滿剩餘的 canonical capacity。只有 queued Queue
entries 或 active prepared Attempts 所需的 runtimes 會進行 Git verification；已進入
terminal Plans 的 obsolete entries 不會阻擋其他工作。私有 worker 會凍結大小受限的
result，並在將 job 標記為 finished 前發布持久 terminal marker。Scheduler 與 marker
observations 只能透過明確的 canonical transaction 推進 Attempt 的 operational state。
