# 儲存與交易

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

## 此契約的狀態

本文件中的預備式多記錄日誌 (prepared multi-record journal) 與向前滾動復原協定
(roll-forward recovery protocol) 已實作。它們支援 Idea qualification、Queue mutation、
dispatch preparation、Experiment closure、Candidate/Release/Promotion operations、
harness migration coordination，以及公開、低風險的 `exp record transaction`／`exp record recover`
介面。公開 raw transactions 僅限 Idea 與 ResourcePool changes；scientific lifecycle records
必須使用其 domain services。

以 receipt 支援的 initialization、linked-worktree ID reservations 與 single-record publication 仍可使用。
在缺少必要的安全 compare-and-swap primitive 的平台上，canonical replacement 仍會以封閉方式失敗。
generated projections 使用自身可重建的 replacement path，且絕不作為 transaction participants。

## 共用協調

Version 1 僅探索 `<git-root>/experiments`；具名或多個 roots 延後處理。解析絕對 Git common directory，
而不是目前 worktree 的 `.git` indirection，並使用：

```text
<git-common-dir>/exp/v1/
├── lock
├── project-receipt.json
├── reservations/
│   └── <typed-id>
├── transactions/
└── attempts/
```

因此，所有 linked worktrees 都會透過同一個 advisory lock 序列化 canonical writes。coordination root
及其 subdirectories 使用 mode `0700`，coordination files 使用 `0600`，canonical Markdown 使用 `0644`。
Git-common coordination 是私有 local state：它不由 Git 追蹤、會從 canonical inventory 與 projection input 排除，
且自身無法建立 Plan、Experiment、Run、Attempt、Finding、Decision、relationship 或 scientific conclusion。

lock 涵蓋：

- linked-worktree Project identity checks 與 initialization receipt reconciliation；
- inventory scans、reservation seeding 與 ID allocation；
- expected-revision checks；
- candidate validation；
- canonical publication；
- 新 mutation 讀取自身 candidate state 前的 prepared-journal recovery；
- 與 mutation 相關的 projection refresh。

lock acquisition 會遵守 context cancellation，並在可安全取得時回報 owner metadata。process 不得只因 PID
看似不存在就破壞 lock；platform advisory locking 才是權威來源。

### Project initialization receipt

`project-receipt.json` 使用 schema `exp.project-init-receipt/v1`，包含精確 encoded `PROJECT.md` bytes
及其 SHA-256 hash。其大小上限為 1 MiB，以 atomic 方式寫入，受 regular non-symlink private file 保護，
並由所有 linked worktrees 共用。

receipt 的權威範圍刻意設得很窄。若沒有 linked worktree 包含固定的 `<git-root>/experiments/PROJECT.md`，
有效 receipt 就是完成中斷之首次 initialization 的 recovery source；重試不會產生不同的 project UUIDv7。
若任何 linked worktree 包含 canonical Project，所有存在的 canonical Project markers 都必須在 `project_id`
與 `created_at` 上一致；這些 canonical bytes 具有權威性，並會修復 missing、stale 或可安全替換的 corrupt receipt。
互相衝突的 canonical identities 會阻止 initialization。receipt 既不是第二筆 Project record，
也不是一般 inventory validation 的 input。

此順序讓 crashes 具有冪等性 (idempotency)。receipt publication 後、`PROJECT.md` publication 前失敗，
會留下可重用的 initialization candidate。canonical publication 後失敗，則以 `PROJECT.md` 為權威來源，
下一次 initialization 會依它協調 receipt。

### Canonical ID reservations

`reservations/<typed-id>` 是 regular non-symlink `0600` file，其完整內容為 `<typed-id>\n`。
每次 canonical mutation 前，writer 在持有 common lock 時，會從每個目前存在的 linked worktree 固定
`experiments` root 載入 valid inventories、要求唯一的 Project identity，並為其 record IDs 建立任何缺少的 reservations。
新配置的 ID 會在發布 canonical record 前，以不覆寫 (without clobber) 的方式 reservation。
即使該 ID 不在目前 worktree 中，只要 reservation 已存在就視為 collision。

Reservations 是 repository-local、持久且禁止重用的權威，不是 canonical record authority。
crash 或 publication failure 後留下的 reservation 會刻意耗用該 ID；刪除 record 也不會移除它。
對仍可在 linked worktree 中看到的 records，其 missing reservations 會由 pre-mutation scan 重建。
對所有目前 linked-worktree inventories 都不存在的 ID，其 tombstone 無法由該 scan 重建，因此 reservations directory
不是 disposable cache，不能整批 rebuild 或 clear。反過來，沒有 canonical Markdown record 的 reservation
也不會建立 evidence，或參與 relationships、lifecycle validation、rendering 或 revisions。

一般的 native record 與 relationship creation 仍然只能使用 UUIDv7，包括帶有 migration extensions 的 records。
reservation filenames 不會授權 UUIDv5。只有明確、以 fingerprint 驗證的 migration engine，
才能在依 reviewed provenance 重新計算後，引入 deterministic UUIDv5 IDs；它會依相同的 no-reuse rule
reservation 這些已驗證的 imported IDs。

## 單記錄寫入

single-record path 使用以下順序：

1. 探索 Git repository 與固定的 `<git-root>/experiments` root。
2. 解析 Git common directory、取得 `exp/v1/lock`，並確保 private coordination directories 使用 mode `0700`。
3. 在讀取 mutation 自己的 candidate state 前，復原任何 prepared transaction journal；conflict 或 unsupported journal 會阻止 publication。
4. 在不跟隨 symlinks 的情況下開啟 canonical root，且只清理已放棄的 atomic temporary files。
5. 列舉目前存在的 linked worktrees、載入每個 fixed-root inventory、要求 valid inventories 具有唯一的 Project identity，
   並為每個 canonical typed ID 補種 missing `0600` reservations。重新檢查 operation 期間已登記但缺少的 worktrees 沒有出現。
6. 在 lock 內透過已開啟的 canonical root 重新讀取目前 worktree 的 inventory，套用每筆 record 8 MiB 上限，
   並執行 no-follow/identity checks。
7. update 時，計算目前 normalized revision，並與 caller 的 expected revision 比較。create 時，驗證 ID 與 target path 均不存在。
8. 在 memory 中建立完整 candidate record，並針對 candidate inventory 驗證 schema、UUIDv7 identity、relationships、
   lifecycle、path containment 與 privacy。接著 create 時，在發布 record 前以不覆寫方式 reservation typed ID；
   generated creates 若遇到 reservation collision，則以新的 UUIDv7 重試。
9. 在相同 directory 中建立不跟隨 symlinks 的 temporary file，將 mode 設為 `0644`、寫入所有 bytes，並 fsync。
10. 重新檢查 opened roots、linked-worktree set，以及 destination identity 與 bytes。
11. create 時，在不覆寫 existing name 的情況下發布。canonical replacement 時，以 atomic 方式交換 temporary
    與 destination files、驗證 displaced identity 與 bytes，並在 mismatch 時 roll back；若沒有 safe exchange primitive，
    則在不替換 destination 的情況下失敗。
12. fsync published file 與 destination directory，接著透過獨立、以 root 為界的 generated-file replacement path，
    以 deterministic 方式重建 projections。
13. 釋放 lock，並回傳新的 computed revision 與任何 projection diagnostic。

canonical publication 前 crash 會讓舊 record 保持權威。publication 後 crash 則讓新 record 保持權威；
projections 可能已過時，而 `render --check` 會偵測到。projection failure 不會 roll back 已成功發布的 canonical record，
且必須分開回報。

任何 process 都不得先 validate、unlock，再 publish。任何 writer 都不得直接寫入 destination、使用 cross-filesystem
temp directory，或只依賴未對 file 與 directory 執行 fsync 的 rename。

## Prepared journal

公開的 machine request 是 strict JSON。`document` 包含完整的 canonical Markdown/TOML envelope；
replace/delete operations 要求精確的目前 normalized revision。

```json
{
  "schema_version": "exp.request.record-transaction/v1",
  "operation": "reviewed.batch-update",
  "changes": [
    {
      "operation": "replace",
      "document": "+++\nschema = \"exp.idea/v1\"\n...\n+++\n\n# Updated Idea\n",
      "expected_revision": "sha256:<normalized-record-revision>"
    }
  ]
}
```

使用 `exp record transaction --input request.json --json`。存在 domain command 時應優先使用，
因為它會建構並驗證 scientific transition，而不是要求 caller 自行撰寫 raw canonical documents。

### Journal location 與 identity

compound operation 會建立：

```text
<git-common-dir>/exp/v1/transactions/<transaction-uuid>/
├── journal.toml
└── staged/
    ├── 0000
    ├── 0001
    └── ...
```

transaction ID 是 UUIDv7。`journal.toml` 使用 schema `exp.transaction/v1` 與 mode `0600`，
自身也以 atomic 方式發布。它包含：

```text
schema
transaction_id
project_id
operation
created_at
phase                 prepared | committed
entries[]
```

每個 ordered entry 包含：

```text
path                  clean experiments-root-relative POSIX path
operation             create | replace | delete
old_hash              sha256 digest or "absent"
new_hash              sha256 digest or "absent"
staged                 staged file name for create/replace
staged_hash            same value as new_hash for create/replace
```

hashes 涵蓋精確的 publication bytes，而非 normalized record revisions。paths 只能指向 canonical records。
journal 絕不儲存 secrets、absolute worktree paths 或 projection entries。

### Prepare

持有 common lock 時：

1. 先復原較舊的 prepared journals。
2. 從所有目前存在、屬於同一 project 的 linked-worktree inventories 補種 reservations，重新讀取每個 participating source，
   並驗證 expected normalized revisions。
3. 在 memory 中建立完整的 candidate canonical inventory，包括 creates、replacements 與 deletions。
4. 對該 inventory 驗證每個 candidate record、relationship、graph constraint、lifecycle rule、path 與 privacy rule。
5. 以不覆寫方式 reservation 每個 create ID。此時之後若 prepare failure，可能會耗用 ID，但不能讓它可重用；
   replacements 與 deletions 保留其 existing reservations。
6. 依 path byte order 排序 entries，使 publication 與 tests 具決定性。
7. 將每個新的精確 byte sequence 寫入 `staged/<index>`、fsync 每個 staged file，並 fsync `staged/`。
8. 記錄精確 old/new SHA-256 hashes，以 `phase = "prepared"` 寫入 `journal.toml`，fsync 它、
   以 atomic 方式發布，並 fsync transaction directory 與 parent `transactions/` directory。

在 prepared journal 與所有 staged bytes 皆持久化前，不會變更任何 canonical file。

### Publish

仍持有 lock 時，依 journal order 處理 entries：

- **create**：要求 destination 不存在，接著以不覆寫方式發布經 hash 驗證的 staged file；
- **replace**：要求 destination 的精確 hash 為 `old_hash`，接著使用 safe canonical compare-and-swap primitive，
  並驗證 displaced bytes；若該 primitive 無法使用，則以封閉方式失敗；
- **delete**：要求 destination 的精確 hash 為 `old_hash`，接著 unlink。

每個 operation 後 fsync 其 parent directory。重新讀取並 hash 產生的 destination（或確認不存在）後，
才繼續下一個 entry。無須記錄 progress，因為 recovery 會從 destination hashes 推導。

commit 後，exp 只會為 local diagnostics 保留有界的 committed journal tail。journal publication 前留下的
UUID-scoped staging 不具持久權威，可在相同 common lock 內安全移除；unknown artifact 或已發布的 prepared journal
仍會以封閉方式失敗。

在每個 destination 都符合 `new_hash`／`absent` 後，以 atomic 方式將 journal 替換為 `phase = "committed"`，
並 fsync 其 directories。至此 canonical publication 完成，最後從 committed inventory 重新產生 projections。

只有在 directory fsync 後才能移除 committed journals；為 bounded diagnostics 保留它們也同樣安全。
cleanup policy 不得影響 correctness。

## 冪等復原

每個 mutating command 都會在持有 common lock 時、讀取其自身 candidate state 前，復原 prepared journals。

對每個 entry：

1. 驗證 journal syntax/version、project identity、path containment、old/new hash forms 與 staged file hashes。
2. 在不跟隨 symlinks 的情況下 hash 目前 destination。
3. 若等於 `new_hash`（或 delete 時不存在），表示該 entry 已套用；繼續處理。
4. 若等於 `old_hash`（或 create 時不存在），則從已驗證的 staged data 套用 operation、fsync，並驗證 new state。
5. 若兩者都不符合，停止 recovery。回報 transaction、path、expected old/new hashes 與 observed hash。
   絕不覆寫不相關的 edit，也絕不從 split canonical state 重新產生 projections。

recovery 永遠向前滾動；它不會在已發布部分新 files 後，猜測如何重建 old tree。create/replace staged data
會保留到 commit。delete recovery 不需要 removed bytes，因為它只會從 verified old hash 向前滾動。

當所有 entries 都符合 new state，便以一般 publication 完全相同的方式將 journal 標為 committed，
接著重新產生 projections。重複執行 recovery 會得到相同結果。

unknown journal schema、destination 仍舊時卻缺少 staged file、hash mismatch、unsafe path 或 project mismatch，
都會以 repair diagnostic 阻止所有 mutation。read-only commands 可以回報該狀況，但不得將 split tree 呈現為 valid。

## Projections-last 規則

`README.md`、`ROADMAP.md`、`LEDGER.md` 與 `DECISIONS.md` 絕不會出現在 journal entries 中。
canonical commit 後：

1. 從同一份 committed inventory snapshot 渲染全部四個檔案。
2. 透過獨立、以 root 為界的 generated-file replacement path 發布每個檔案；較弱的 replacement semantics
   只有在這些 outputs 可重建時才可接受，且不得用於 canonical records。
3. 若中斷，讓 canonical state 維持 committed，並讓 projections 處於可偵測的 stale 狀態。
4. `exp render` 會修復它們；`exp render --check` 會在不寫入的情況下回報 byte-level drift。

這能避免 generated-file conflict 阻止或破壞 scientific state。readers 絕不使用 projection 作為 relationship
或 lifecycle input。

## Attempt markers

private worker 會在完成其 SQLite job 前寫入一個 terminal marker：

```text
<git-common-dir>/exp/v1/attempts/job-<sha256-prefix-of-operational-job-id>.json
```

固定長度的 hash 可避免 attacker-controlled job IDs 出現在 filenames 中。有界且 secret-safe 的
`exp.worker-terminal/v1` JSON 包含 original job ID、canonical Attempt ID、fencing token、operational state、
process timing、exit code，以及選用的 result digest/size。publication 使用 private temporary、file fsync、
rename 與 directory fsync。相同 job/fencing claim 會回傳 existing marker，而不是再次執行 workload。
即使沒有找到 process，marker 不存在仍表示 `unknown`。reconciliation 會透過 revision-checked canonical Attempt mutation
匯入 observation；marker 既不是 scientific evidence，也不能取代 Attempt record。

## 必要驗證

failure injection 必須涵蓋 temp creation、write、file fsync、journal publication、canonical create/CAS/unlink、
directory fsync、commit marking 與 projection rendering。重新啟動後，結果必須是完整的舊 single-record state、
完整的新 compound state，或精確的不相關編輯 conflict——絕不能默默留下 split state。

linked-worktree tests 必須證明：

- 獨立的 UUIDv7 creates 會序列化且不會 collision；
- 同一 record 的 updates 會產生 expected-revision conflict，而不是 last-writer-wins；
- compound operations 共用 common-directory lock；
- 重複 recovery 具有冪等性；
- projection drift 絕不改變 canonical validation results。
