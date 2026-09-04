# Git 與 Worktrees

Git 擁有 source bytes、commit、branch、worktree registration 與 integration history。`exp` 把這些
facts 綁到 canonical Source/Attempt/Candidate records，但不會從 commit 推論 scientific meaning，
也絕不 automatic merge、push、deploy 或刪除 retained branch。

Canonical experiment repository 可以與所有 Source repository 分開。Source record 提供
Project-local identity 與 immutable `subdir`；host-local association 經 Git-common/locator validation
後識別 actual clone。Git submodule 只是 optional checkout convenience，不能取代任一層。

## Managed location 與 identity

Identity-aware worktree 位於所有 registered Source worktrees 外：

```text
$XDG_DATA_HOME/exp/worktrees/<project-uuid>/<source-id>/<owner-id>-<slug>
```

未設定 `XDG_DATA_HOME` 時使用 `~/.local/share`；提供時必須是 clean absolute path。Branch 為：

```text
exp/<project-uuid>/<source-id>/<owner-id>-<slug>
```

完整 Project、Source 與 owner IDs 讓 identity 不依賴 host path，也避免 linked worktrees 間
collision。依 caller 規則，owner 可為 Experiment、Try 或 Attempt。Legacy embedded Experiment
API 保留 repository-derived XDG namespace 與 `exp/<experiment-uuid-hex>-<slug>` branch，不會
reinterpret 既有 behavior。

Workspace operation 重新 discover exact Source root/Git common directory，要求 full lower-case
SHA-1/SHA-256 base object ID，拒絕 symbolic/abbreviated revision，且絕不 follow substituted symlink。
Managed path 必須在 Source repository 與其所有 registered worktrees 外。

## Formal Experiment workspace

Active 且 design locked 的 Experiment 可準備 code-edit workspace。若 selected Source，`--allow`
glob 相對其 declared subdir；沒有 Source 時，exact embedded v1 behavior 使用 experiment-repository-
root-relative glob。

```bash
exp --workspace <PROJECT> --source <SOURCE> \
  experiment workspace prepare <EXPERIMENT> \
  --base 0123456789abcdef0123456789abcdef01234567 \
  --allow 'src/**' \
  --allow 'configs/**'
```

Preparation 要求 selected source checkout 完全 clean、解析 exact base、以停用 Git hook 的
creation command 建立單一 branch/worktree，並驗證：

- exact repository/Git-common identity；
- expected branch 與位於 base 的 HEAD；
- clean new checkout；
- real non-symlink Source subdir；
- 不存在 forbidden canonical metadata path。

Workspace backend selector 會 resolve/report，但目前 preparation 一律由 native Git 執行。詳見
[Provider behavior](#workspace-provider-boundary)。

### Commit exact change set

```bash
exp --workspace <PROJECT> --source <SOURCE> \
  experiment workspace commit <EXPERIMENT> \
  --base 0123456789abcdef0123456789abcdef01234567 \
  --allow 'src/**' \
  --allow 'configs/**' \
  --json
```

Commit 重新 discover deterministic worktree，並驗證 branch、common Git identity、ancestry、
metadata 與 tracked/untracked path sets。接著：

1. 拒絕 empty change set；
2. 拒絕 `.git`、embedded 時的 canonical `experiments/` metadata、Source `subdir` 外所有 path，
   以及 normalized allowlist 外所有 path；
3. 只 stage observed paths，並驗證 staged set；
4. 建立一個 unsigned、no-hook/no-verify commit，其唯一 parent 是 exact requested base；
5. 驗證 checkout clean 且 committed path set 不變。

ChangeSet 回傳 base/head、branch、sorted paths 與 framed `sha256:` diff identity。若 worktree 已含
expected single-child commit，重複呼叫只 verify/return，不再 commit。Source-aware request 只有
Experiment owner 可 invocation 此 commit path。

### Code-edit agent

```bash
exp --workspace <PROJECT> --source <SOURCE> \
  experiment agent <EXPERIMENT> \
  --base 0123456789abcdef0123456789abcdef01234567 \
  --allow 'src/**' \
  --prompt implementation-notes.md \
  --json
```

此 flow 把相同 native preparation/commit verifier 與一個 fresh `experiment_implementer` CLI process
組合。Agent output 只是 advisory；若 `changed_paths` 與 actual commit 不同，`exp` 回報 mismatch，
以 Git diff 為 authority。Command 保留 worktree/branch，供 human review/integration。

Prepared commit 仍不是 execution evidence。只有 clean formal runtime 在 Attempt v3 capture 它、
Experiment include 其 Run、Evaluation v2 綁定該 Attempt、Candidate v2 複製 exact Source identities
之後，才可能進入 promotion。

## Runtime v2 checkouts

每個 formal runtime v2 Source 明確選擇：

| Checkout | 意義 |
|---|---|
| `main` | Registered clone current checkout 必須有 exact clean HEAD/ChangeSet。 |
| `registered_worktree` | 選擇 HEAD 等於 `head_commit` 的唯一 registered linked worktree；runtime config 不含 host path。 |
| `managed_worktree` | 在 captured clean SourceSnapshot 準備／重用 deterministic XDG worktree。 |

Worker contract 角度有一個 writable Source；每個 additional Source binding 為 read-only。Canonical
dispatch 前全部 clean capture，依 snapshot digest/private checkout identity 綁入 worker-job v2，
process start 前重新驗證。Successful process 後再檢查 read-only Source bytes。Missing、dirty、moved、
stale 或 mismatched Source 會 block submission，不會 fallback 到 unrelated checkout。

## Direct Try worktree

`exp try run` 一律使用 managed worktree 與 Attempt v3。沒有 `--allow` 時禁止新增 change。
Clean mode 只複製 committed state。Explicit `--dirty=capture` 可把 bounded pre-existing dirty state
seed 到 new worktree；絕不在 production checkout execution。

Dirty capture authenticate：

- full tracked binary patch 與 exact tracked paths；
- bounded regular untracked files、modes、sizes、hashes；
- clean direct submodule gitlinks/commits；
- Source、subdir、object format、base/head、policy、snapshot digest。

Rename/copy、hidden index flag、ignored/unmerged state、dirty/nested submodule、Source subdir 外 path、
symlink/special file、exceeded bound 都 fail closed。Clean submodule 會從 local objects materialize 成
unregistered shared clone，不會註冊成 production submodule 的 worktree。

依 Project/Source/owner/snapshot scoped 的 private `preparing` marker 讓 preparation 可 resume。若
crash 留下 incomplete manager-owned worktree 或 unadvanced branch，later prepare 只能移除 marker
authenticate 的 artifact 再 retry。Matching completed seed 只有 complete recapture 後才 reuse；
unmarked mismatch 絕不 overwrite。

## Inspection 與 cleanup

Native inspection 驗證 repository、Git-common identity、deterministic path、branch、base ancestry、
canonical metadata exclusion，以及 working/committed paths union 是否符合 exact path/glob；只 report，
不 mutation worktree。

`exp try cleanup TRY --confirm` 是 public managed cleanup flow：

- Attempt 必須 terminal 且 registered 為 direct Try；
- clean worktree 必須仍 clean、at base，兩次 inspection 都沒有 changed path；
- dirty seeded worktree 必須仍 at base，並在 guarded Git `--force` removal 前兩次 capture 都符合
  complete private seed；
- bundle deletion 前立即 reauthenticate；
- retired Source 只能為此 historical cleanup resolve；
- partial removal/bundle/publication failure 保持 retryable 並 truthful report。

Cleanup 只移除 verified worktree，以及 dirty Try 的 private seed；刻意**不**刪 branch/commit、
operation row、terminal/result marker 或 canonical Attempt。Formal Experiment workspace command 同樣
沒有 automatic cleanup。Human Git policy 決定 retained branch/worktree 何時 integration/prune。

## Workspace provider boundary

`native_git` built in，是 prepare、verify、inspect、cleanup、retire 的 correctness authority。
Registry 也列出 `dev_cli`，但目前所有 lifecycle capabilities—prepare、inspect、cleanup、open、
handoff、retire—都 compiled `unsupported`。Executable discovery 可以回報 installed/not probed；
explicit probe 回報 `machine-contract-unavailable`，不會 invocation help、version、inventory、login 或
任何 lifecycle command。

原因是 contract：目前 public `dev` output 沒有 schema-versioned content-free capability receipt，
也沒有 exact-path open/retire receipt 與 verifiable occupancy。`exp` 因此無法證明 provider 作用於
exact native worktree。Trusted selection 可在 configured 時 explicit fallback 到 native Git；resolution
回報 requested/actual provider 與 reason。Optional provider task/catalog ID 絕不成為 Source、worktree
或 cleanup authority，native postcondition 永遠是 final。

## 目前限制

- 不 automatic merge、push、rebase、deployment、rollback 或 branch deletion。
- 不執行 dirty formal run，也不從 dirty Try 建 Candidate；必須 clean rerun。
- Managed worktree contract 不提供 large artifact storage。
- 在 exact machine receipt 出現前，不使用 `dev_cli` lifecycle operation。
- Human integration retained branch 時，不 automatic resolve conflict。
