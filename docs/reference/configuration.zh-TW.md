# 設定與路徑

`exp` 會先解析 canonical authority，再載入 preference。Canonical record、local checkout
association、repository configuration、exact-digest trust、runtime dispatch、provider profile 與
operational state 各有不同 schema 與 lifetime。任何 config file 都不能 redirect 已解析的
Project 或 Source。

## Repository arrangement

建議預設使用 dedicated private experiment Git repository。其 canonical root 仍是
`experiments/`；每個 code/data repository 或受治理 subdirectory 都由一般 Source record 表示。
一個 Project 可有多個 Sources。Git submodule 只是 optional checkout convenience，不能取代
Source registration、local association validation 或 SourceSnapshot capture。

當 code 與 research 共用 governance 時，embedded `experiments/` 與 monorepo layout 仍受支援，
並保留既有 Project v1 behavior。Source 的 immutable `subdir` 選擇 clone 內 semantic root；
`.` 代表 Git root。

## 路徑摘要與 XDG 規則

| 用途 | 預設位置 | Authority 與 lifetime |
|---|---|---|
| Canonical records | `<experiment-git-root>/experiments/` | 單一 Project 的 Git-backed scientific/decision authority。 |
| User layered config | `$XDG_CONFIG_HOME/exp/config.toml` | Inherently trusted user preference layer；fallback `~/.config/exp/config.toml`。 |
| Repository layered config | `<experiment-git-root>/.exp-cli/config.toml`、`<source-git-root>/.exp-cli/config.toml`，再加 subdirectory layers | Strict `exp.config/v1`；execution-bearing leaf 要求 exact trust。 |
| Project/Source associations | `$XDG_STATE_HOME/exp/associations/v1.json` | Private host-local path 與 Git filesystem identity；fallback `~/.local/state/exp/associations/v1.json`。 |
| Config trust receipts | `$XDG_STATE_HOME/exp/trust/v1.json` | Private exact-digest capability approval；fallback `~/.local/state/exp/trust/v1.json`。 |
| Dirty Source seed bundles | `$XDG_CACHE_HOME/exp/source-seeds/` | 可丟棄但 authenticated 的 private bytes，用於 dirty Try replay；fallback `~/.cache/exp/source-seeds/`。 |
| Managed worktrees | Identity-aware work 使用 `$XDG_DATA_HOME/exp/worktrees/<project-id>/<source-id>/<owner-id>-<slug>` | 位於 registered Source worktrees 外的 local linked worktree；fallback `~/.local/share`。 |
| Agent CLI profiles | `$XDG_CONFIG_HOME/exp/agents.toml` | Separate `exp.agents/v1` execution loader；不 merge 進 `exp.config/v1`。 |
| Runtime contract | `<experiment-git-root>/.exp/runtime.json` | Separate closed `exp.runtime/v1` 或 `exp.runtime/v2` JSON decoder。 |
| Canonical coordination | `<experiment-git-common-dir>/exp/v1/` | Private lock、Project receipt、reservation、transaction journal、workspace lock、worker marker。 |
| Operational database | `<experiment-git-common-dir>/exp/runtime/v1/control.sqlite` | Private lease/job/outbox/fairness/observation；絕非 canonical。 |

對 config、state 與 cache home，未設定、空值或 relative XDG environment value 都會依 XDG
base specification，透過 absolute user home fallback。Worktree management 若收到
`XDG_DATA_HOME`，該值本身必須是 clean absolute path；未設定時使用 `~/.local/share`。
支援的 Unix-like system 會把 private state directory/file 驗證為 non-symlink `0700`／`0600`
path。Windows 會透過 open handle 建立 protected owner-only DACL，並拒絕 null、empty、inherited
或包含其他 principal 的 ACL。Read-only inspection 不會建立、chmod、repair 或 migrate missing／
existing state。

Association/status output 中的 host path 是 operational detail。不要複製到 canonical record、
準備跨 host 使用的 runtime file 或 public docs。

## Workspace 與 Source resolution

Root selectors：

```text
--workspace <Project UUID or path>
--source <Source key, full ID, unique typed prefix, or display code>
```

Resolution 順序是 deterministic：

1. explicit `--workspace`，可再指定 explicit Source；
2. 沒有 explicit workspace 時，使用 invocation Git repository 中的 physical Project marker
   （embedded compatibility path），可再指定 explicit Source；
3. 否則使用包含 invocation directory、最 specific 且通過驗證的 registered Source。

Equally specific match 會 ambiguous。Physical Project 與屬於不同 Project 的 containing Source
同時存在時也會 ambiguous，不會 silent choose。Project/Source trees 外的 explicit Source 必須只
match 一個 registered Project。每個 association 每次使用都會重新驗證 canonical Project UUID、
Source ID、Git roots、Git-common filesystem identity、immutable subdir 與 sanitized remote-locator
intersection。Retired Source 不能用於 new work；historical resolution 只供 guarded cleanup。

`exp workspace register` 記錄 canonical clone。`exp source add` 先 atomically publish 一般 Source，
再 register Project 與 Source clone；publication 後 association failure 會回報 partial，不會刪除
Source。`source register` 修復 association。`source append-locator` 與 `source retire` 要求 exact
record revision 加 confirmation。

## Layered `exp.config/v1`

每個 file 都是 strict TOML，上限 1 MiB，unknown field 會被拒絕。最多套用 64 layers，
Source-subdir 到 invocation 最多 32 directory levels。同一 filesystem object 只套用一次。

精確 low-to-high precedence：

1. built-ins；
2. `$XDG_CONFIG_HOME/exp/config.toml` user config；
3. canonical experiment-repository `.exp-cli/config.toml`；
4. selected Source clone-root `.exp-cli/config.toml`；
5. 從 Source declared subdir 到 invocation directory、依 root-to-leaf 順序的每個
   `.exp-cli/config.toml`。

只有選擇 Source 且 invocation 安全位於其 semantic root 內，才會套用 Source/subdirectory
layers。Optional `[identity]` selector 必須等於已解析的 Project UUID 與 Source ID；它是 assertion，
不是 redirect。

### Secure template

此 template 只含 public identifier 與 environment **names**，沒有 resolved environment value 或
private host path。請替換 angle-bracket identifiers；若 file 要跨 Project reuse，省略 `[identity]`。

```toml
schema = "exp.config/v1"

[identity]
project = "<PROJECT_UUID>"
source = "src_<SOURCE_UUID>"

[defaults]
source = "production"
workspace_backend = "native_git"
agent_profile = "research-agent"
mlflow_profile = "observer"

[ui]
color = "auto"

[workspace]
preferred_backends = ["native_git"]

[workspace.backends.native_git]
enabled = true
priority = 100

[workspace.backends.dev_cli]
enabled = false
priority = 10

[mlflow.profiles.observer]
context = "research"
binary = "mlflow"
timeout = "30s"
default_metrics = ["macro_f1", "validation_loss"]

[mlflow.profiles.observer.env.MLFLOW_TRACKING_URI]
from = "MLFLOW_TRACKING_URI"
secret = false
required = true

[mlflow.profiles.observer.env.MLFLOW_TRACKING_TOKEN]
from = "MLFLOW_TRACKING_TOKEN"
secret = true
required = true
```

File 不會 expand environment variable。`from` 是 parent variable name，其 value 只在 child process
啟動前立即解析。Profile 提供的所有 value 在 process output/result handling 都會 redacted，即使
`secret = false`；`secret` 是 policy metadata，不是保存 value 的許可。Serialized MLflow
workload profile 會拒絕以 `EXP_` 開頭的 child name，避免覆寫 worker-owned variables。

### Merge semantics

- Scalar leaf 採最高 applied value。
- `workspace.preferred_backends` 替換完整 array。
- `workspace.backends.<name>.enabled` 與 `.priority` 依 backend name、各 field 獨立 merge；
  `native_git` 必須保持 enabled，作為 correctness baseline。
- 較高 layer 的 named `mlflow.profiles.<name>` 會 atomically replace；其 fields/environment
  map/default metrics 不與舊 definition merge。
- Built-in default 為 `workspace_backend = "native_git"`、
  `preferred_backends = ["native_git"]`、native enabled/priority 100、`ui.color = "auto"`，
  且沒有 Source/agent/MLflow default。

每個 effective leaf 都記錄 winning source、layer kind、exact file digest、trust status 與 required
capability。Combined config digest 會 frame ordered exact layer digests/scopes，是 direct Try Attempt
provenance 所 pin 的 value。`defaults.agent_profile` 目前只是 policy/provenance data：agent execution
仍從 separate `exp.agents/v1` file 的 role/profile 或 command explicit `--profile` 選擇；layered value
不會自動傳入這些 commands。`exp config show` 顯示 effective non-secret values；
`config explain [field]` 顯示 winning provenance 與所有 applied layers。Overridden untrusted layer
仍保留供 audit，但只要每個 winning execution-bearing leaf 都 trusted，就不會讓 effective summary
變成 untrusted。

## Exact-digest trust

Built-in 與 user config inherently trusted。Repository、Source、subdirectory file 永遠可以提供
pure identity assertion 與 UI color；下列 execution-bearing classes 要求 receipt：

```text
source.selection
workspace.backend
agent.profile
mlflow.profile
runtime.dispatch
```

Closed `exp.trust/v1` receipt 綁定：

- Project UUID 與 optional Source ID；
- canonical Git-common directory **及其 filesystem identity**；
- Git-root-relative config scope；
- exact file bytes `sha256:`；
- sorted capability set 與 UTC approval time。

Receipt 不保存 command、config bytes、locator、environment value 或 credential。Edit file 會改變
digest；replace clone 會改變 filesystem identity。同一 logical scope 核准 newer digest 時，會替換
older digests 中重疊的 capability approval，因此 revert content 不會 revive stale trust。

Trust 使用完整的 resolved authority context。選擇 Source 時，每個 repository layer（包含
canonical experiment-repository layer）的 receipt 都會綁定該 Source ID。執行 `config explain` 與
`config trust` 時，應使用後續 execution 相同的 `--workspace`、`--source` 與 invocation root
（必要時加 `--start-dir`）。Workspace-only receipt 不會隱含核准同一份 bytes 給所有 Sources。

Review 並核准 exact currently applied layer：

```bash
exp config explain
exp config trust \
  --path <CONFIG_PATH_FROM_EXPLAIN> \
  --digest sha256:<CURRENT_FILE_DIGEST> \
  --capability mlflow.profile \
  --confirm
```

Non-interactive/JSON trust 要求 `--path`、`--digest`、至少一個 `--capability` 與 `--confirm`。
`config revoke --path ... --confirm` 可 revoke current 或 deleted scope；optional digest/capabilities
可縮小範圍。`config list` 回傳 sanitized receipts，不含 Git-common paths。Winning field untrusted
時，repository execution selection 會 fail closed，不會略過它並使用 lower layer。

Runtime v2 trust 使用同一 command，但 `.exp/runtime.json` 不是 `exp.config/v1` layer。
`config trust` 只有在其 schema 是 `exp.runtime/v2` 時，才識別 exact raw-file digest，而且只能
核准 `runtime.dispatch`。Runtime v1 刻意不使用此 v2 receipt path。

## Workspace backend profiles

`--workspace-backend` precedence 最高；否則由 effective config 的
`defaults.workspace_backend` 選擇。Explicit selection 是 invocation-local 且 trusted。
Repository-derived selection 與 enablement/fallback leaves 要求 `workspace.backend` trust。

`native_git` 實作 prepare、inspect、cleanup、retire。`dev_cli` 可 list/discover，但其 public CLI
沒有 schema-versioned exact-path machine receipt，因此目前所有 lifecycle capability 都回報
unsupported，不會 invocation 任何 `dev` lifecycle subprocess。Explicit `dev_cli` request，或
fallback policy 包含 enabled `native_git` 的 trusted configured request，可以帶明確 fallback reason
解析成 native。Provider priority 是 config metadata；不會 override requested provider 或 native
correctness check。

```bash
exp workspace backend list --probe
exp workspace backend status --probe
```

Probe 只在本機 bounded 執行；不會 install、authenticate、start service 或 contact remote backend。
不論 requested provider，prepared byte verification、inspection 與 cleanup 永遠由 native Git 擁有。

## MLflow profiles

Named MLflow profile 是 atomic config object，包含：

- project-safe profile name 與 non-secret context slug；
- 從 `PATH` 解析的 binary **name**，不是 path/command；
- positive timeout，上限 24 hours；
- 最多 128 個 child-to-parent environment-name binding，帶 `secret`／`required` policy；
- 最多 256 個 default metric names。

`--mlflow-profile NAME` 明確選擇 trusted definition；缺省時由
`defaults.mlflow_profile` 選 trusted profile。Explicit profile 不會 bypass repository-defined profile
body 的 trust。Compatibility flags `--mlflow-context`、`--allow-env`、`--secret-env` 建立 legacy
`mlflow`/30s profile，不能與 named profile 混用。

Direct Try 可把 environment-bound profile 傳給 local workload，之後執行 optional read-only
attachment observation。Formal runtime v2 經 Pueue 執行時會拒絕任何帶 environment binding 的
profile，因 scheduler 會保存 task environment；應改用 workload-side credential broker。
Value-free formal profile 仍可指定 context、binary、timeout 與 default metrics。Optional worker
observation 遇到 provider absence 時記錄 unavailable，不會改變 successful process state。Explicit
`provider mlflow verify` 與 Evaluation attachment 維持 strict：requested assertions 必須通過。

## Runtime contract 保持分離

`.exp/runtime.json` 是 strict JSON，不是 layered config document。兩個版本都拒絕 unknown field／
trailing JSON，並要求 `pools` 與 `plans` maps。以下是 template：angle-bracket token 必須由人類
literal replace；不會 variable interpolation。

### Runtime v1：embedded compatibility

```json
{
  "schema_version": "exp.runtime/v1",
  "pools": {
    "pool_<POOL_UUID>": {
      "pueue_group": "gpu",
      "label_prefix": "exp-"
    }
  },
  "plans": {
    "plan_<PLAN_UUID>": {
      "executable": "/ABSOLUTE/PATH/TO/PROJECT_RUNNER",
      "argv": ["--config", "configs/run.toml"],
      "checkout": "main",
      "cwd": ".",
      "timeout": "4h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "0000000000000000000000000000000000000000",
      "head_commit": "1111111111111111111111111111111111111111",
      "change_set": ["configs/run.toml", "src/train.go"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
```

V1 在 experiment repository 本身 execution，接受 `main` 或 `registered_worktree`（缺省時預設
`main`／`.`），要求 non-empty argv/ChangeSet，並建立 Attempt v2 加 worker
job/terminal/result v1。它保留給 embedded Project，不能描述 external Source。

### Runtime v2：Source-aware formal execution

```json
{
  "schema_version": "exp.runtime/v2",
  "pools": {
    "pool_<POOL_UUID>": {
      "pueue_group": "gpu",
      "label_prefix": "exp-"
    }
  },
  "plans": {
    "plan_<PLAN_UUID>": {
      "execution_source": "src_<SOURCE_UUID>",
      "read_only_sources": [
        {
          "source": "src_<READ_ONLY_SOURCE_UUID>",
          "checkout": "main",
          "base_commit": "2222222222222222222222222222222222222222",
          "head_commit": "2222222222222222222222222222222222222222",
          "change_set": [],
          "observational_no_change": true
        }
      ],
      "executable": "/ABSOLUTE/PATH/TO/PROJECT_RUNNER",
      "argv": ["--config", "configs/run.toml"],
      "checkout": "managed_worktree",
      "cwd": ".",
      "timeout": "4h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "3333333333333333333333333333333333333333",
      "head_commit": "4444444444444444444444444444444444444444",
      "change_set": ["services/model/configs/run.toml"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
```

V2 要求每個 Source 明確 `checkout`（`main`、`registered_worktree` 或
`managed_worktree`）。每個 Source 具有同 object format 的 full base/head IDs 與 required
ChangeSet array。Empty ChangeSet 只有在 `observational_no_change = true` 且 base=head 時合法；
non-empty set 禁止該 flag。`cwd`/expected outputs 相對 execution Source declared subdir；
ChangeSet path 仍相對 Git root。Sources 必須 unique，non-execution bindings 為 read-only。

V2 解析 association/layered config、要求 exact runtime trust、只 capture clean SourceSnapshots、
建立 Attempt v3/worker v2 envelopes，並在 scheduler submission/spawn 前重新驗證所有 identity。
Pueue 下 `secret_env` 必須為空，`allowed_env` 會拒絕 credential-sensitive name。

## Worker environment contract

每個 worker 都提供：

| Variable | 意義 |
|---|---|
| `EXP_JOB_ID` | Private job identity。 |
| `EXP_ATTEMPT_ID` | Canonical Attempt identity。 |
| `EXP_RESULT_PATH` | Exp-owned absolute private file，用於一個 bounded JSON result。 |

Formal worker v2 額外提供：

| Variable | 意義 |
|---|---|
| `EXP_PROJECT_ID` | Explicit canonical Project UUID。 |
| `EXP_CANONICAL_SCOPE` | Worker 接收並驗證的 checkout-local canonical scope。 |
| `EXP_EXECUTION_SOURCE` | Canonical writable execution Source ID。 |

Path value 是 operational，不會保存於 canonical record。Workload 可寫 assigned file，但不得
replace 其 identity。Expected repository outputs 另行宣告，只在 process success 後 hash。Valid
result JSON 與 output hash 都不是 scientific verdict。

另見 [Runtime Dispatch](../workflows/runtime-dispatch.md)、
[Git and Worktrees](../tools/git-worktrees.md)與[MLflow](../tools/mlflow.md)。
