# Migration 與 backward compatibility

需要區分兩種情況：

1. **Harness-v0 semantic migration**：透過 reviewed、fingerprinted plan/apply protocol，
   轉換較舊且 unversioned 的 research tree。
2. **Versioned compatibility**：精確讀取既有 Project/record/runtime/worker/journal schemas。
   系統不會只為了使用 independent-repository 與 Source-aware features，就重寫 valid v1/v2 history。

## Harness-v0：建立 read-only plan

```bash
exp migrate plan \
  --legacy-source path/to/harness-v0 \
  --output migration-plan.json \
  --json
```

`--source` 只在 `migrate plan` 作為 `--legacy-source` 的 compatibility alias。Migration 永遠
以 invocation Git repository 為 target，因此拒絕 `--workspace`；`migrate apply` 同時拒絕
`--workspace` 與 `--source`。Legacy source 預設為 `experiments`。

Plan 會 fingerprint 每個 source byte、計算 deterministic target identities、保留 unknown spans，
並把 ambiguous meaning 回報成 `needs_review`。只有提供 `--output` 時 plan 才會寫檔，且採
no-clobber creation。

## 解決 ambiguity 並 apply

只把回報的 `needs_review` keys 複製到 strict
`exp.migration-resolutions.harness-v0/v1` file。只有 shown candidate 有效時才能選
`migrate`；舊 material 無法安全建立 current record 時選 `archive_only`。重新建立並審閱完整
plan：

```bash
exp migrate plan \
  --legacy-source path/to/harness-v0 \
  --resolutions resolutions.json \
  --output reviewed-plan.json

exp migrate apply --plan reviewed-plan.json --json
exp validate
exp render --check
```

Apply 會驗證 plan hash 與 source fingerprint、重新計算 UUIDv5、驗證 complete candidate
inventory、保留 exact Git-tracked archive，並執行 recoverable root swap。任何 source change、
addition、deletion、symlink substitution、unresolved review item 或 candidate mismatch 都會 fail
closed。只有所有 expected destination hashes 相符時，reapply 才是 no-op。Migrator 絕不執行
legacy script、parser、provenance helper 或 job wrapper。

Exact field mapping 與 archive protocol 請見
[Harness-v0 compatibility and migration](../design/harness-v0-migration.md)。

## Versioned compatibility matrix

| Existing material | 精確目前行為 | New-feature path |
|---|---|---|
| `<git-root>/experiments/PROJECT.md` 的 embedded `exp.project/v1` | Physical Project discovery 優先，且不需要 local association state。Marker 與 `experiments_root = "."` 不變。 | 繼續 embedded use；需要從其他位置 selection 時執行 `workspace register`；或初始化另一個 dedicated Project。沒有 Project v2 conversion。 |
| Existing canonical records | 每個 persisted schema 使用其 closed decoder。Older schema 中的 unknown newer field 是 error；old record 不會 in-place upgrade。 | New Try adoption 寫 Idea v2；runtime v2/direct Try 寫 Attempt v3；formal explicit `--attempt` 寫 Evaluation v2；Candidate 預設 v2。 |
| 既有 `sources` path 或 `t-*` content | 只有 exact canonical Source filename 與 exact Try directory grammar 會被保留。Nonmatching legacy content 會忽略，initialization 不會重寫。 | 建立新 Source/Try 前明確處理 filesystem collision。 |
| Candidate v1 CLI workflow | 既有 `--git-commit` 加 `--change` invocation 仍推定 Candidate v1；也接受 explicit `--legacy`。 | 省略 legacy fields，提供 clean successful formal Attempt v3 以建立 Candidate v2。 |
| 使用 `exp.runtime/v1` 的 `.exp/runtime.json` | 由原 closed embedded-repository decoder 載入，建立 Attempt v2/worker-job v1；Source-aware field 會被拒絕。 | 另行 author/review `exp.runtime/v2`、register Sources，並為其 exact raw digest 核准 `runtime.dispatch`。不會 automatic JSON rewrite。 |
| 已 queued `exp.worker-job/v1` | 不帶 explicit authority flags 的 hidden worker invocation 只保留 exact legacy job shape。V1 job/terminal/result decoders 拒絕 v2-only fields。 | Runtime v2 dispatch worker-job/terminal/result v2，並一起傳 `--canonical-root`、`--project`、`--scope`。Direct Try v2 job 必須透過 `exp try resume`，不能直接呼叫 hidden worker。 |
| `transactions/` 中的 `exp.transaction/v1` journal | Committed journal 保留為 history。Prepared v1 journal 只有在不存在任何 linked-worktree metadata entry 時才能 recover；否則缺少 worktree authority 會 block recovery。 | 所有新 canonical transaction 都是 `transactions-v2/` 下帶 path-free worktree identity 的 `exp.transaction/v2`；journal 不重寫。 |
| Host-local association/trust file 缺少 | Legacy embedded use 繼續；absence 不改變 canonical identity。 | Cross-repository selection 建立 `exp.associations/v1`；execution-bearing repository config 要求 exact `exp.trust/v1` receipt。這些 local files 不跨 host copy，也不 commit。 |
| Champion manifest v1 consumer | 所有 slots 都使用 Candidate v1 時仍輸出 v1。 | 任何 Candidate v2 都選 Source-aware manifest v3；consumer 必須依 `schema_version` 分支。不輸出 v2 manifest。 |

## 從 embedded 移到 dedicated

沒有 command 會暗中移動 existing Project 或改變 Project UUID。若 governance 要求 dedicated
repository，請把它初始化成新的 canonical Project，再把舊 code repository 加成一般 Source。
舊 embedded Project 應依 repository policy 保留或 archive；不要把 records 複製到新 Project UUID，
卻假裝 typed relationships 沒有改變。Git submodule 可提供 checkout convenience，但不會執行
migration，也不能建立 Source identity。
