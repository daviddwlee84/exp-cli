# Harness-v0 相容性與遷移

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

## 範圍與狀態

未版本化的 experiment-knowledge-harness 配置命名為 `harness-v0`。
Go 無損讀取器 (lossless reader) 以及明確的 `migrate plan`／`migrate apply` 路徑均已實作。
對尚未遷移的 v0 tree 提供原生 list/show/search/context 相容性仍延後處理。此實作絕不執行
legacy parser、會造成 mutation 的 scripts、provenance helpers 或 job wrappers。

可辨識的來源介面 (source surface) 為：

```text
ROADMAP.md
LEDGER.md
INBOX.md
<NNN>-<slug>/REPORT.md
README.md and other root-level Markdown summaries (views only)
```

v0 tree 是 legacy input，而非格式錯誤的 v1。`migrate plan` 會回報解析出的 records、raw spans
與 diagnostics，不會重寫任何來源 byte。

## 無損讀取器

讀取器會先將每個來源檔案擷取為 bytes 及其 SHA-256 hash。解析後產生的 nodes 包含來源路徑、
以零為起點的 byte range、精確 raw bytes、decoded fields 與 diagnostics。每個 byte 都恰好屬於下列一項：

- 一個 parsed field 或 record span；
- front-matter delimiter／formatting；
- 逐 byte 保留的 Markdown body；
- 逐 byte 保留的 unknown span。

span 重疊或有未歸屬的 span 都屬於 parser error。不支援的 YAML-like syntax、comments、
不尋常的 wrapping、自訂 sections 與 malformed bullets 會保留為 unknown spans；讀取器不得捨棄或正規化它們。
無效 UTF-8 會阻止 semantic migration，但仍可透過 hash 與 byte range 回報。

migration plan 可以為了建構 candidate 而正規化 values，但一定會保留 source coordinates 與精確的 archive hashes。
它絕不將產生的 README tables 或 graphs 視為權威來源。

## 決定性的遷移 identity

新的原生 records 使用 UUIDv7。migration engine 僅使用 UUIDv5，因此對完全相同的來源 bytes 重複執行 plan，
會得到完全相同的 IDs。

固定的 harness-v0 namespace 為：

```text
b2e8b68c-2de6-5291-885e-19f0efdfe218
```

它是在標準 URL namespace 下，針對以下內容產生的 UUIDv5：

```text
https://github.com/daviddwlee84/exp-cli/migration/harness-v0
```

依下列內容計算 source-tree fingerprint 的 SHA-256：

1. ASCII `exp-harness-v0-tree`，後接 NUL；
2. 每個 regular source file，依其相對 root 的 POSIX path 進行 bytewise lexical 排序；
3. 每個檔案依序加入：unsigned 64-bit big-endian path-byte length、UTF-8 path bytes、unsigned 64-bit big-endian content length，最後是精確的 file bytes。

symlinks、absolute paths、重複的 normalized paths，以及逃逸 source root 的檔案都會阻止遷移。

接著計算：

```text
project UUID = UUIDv5(fixed namespace, "tree-sha256:" + lower_hex_fingerprint)
record UUID  = UUIDv5(project UUID, kind + NUL + stable_source_key)
```

穩定的 source keys 為：

- Experiment：精確的 legacy alias，例如 `#016`；
- Finding：精確的 legacy alias，例如 `F-039`；
- Plan：`ROADMAP.md:<start-byte>:<end-byte>:<span-sha256>`；
- Decision：當具有明確 action 的 decision 不含歧義時，使用 `<source-path>:<start-byte>:<end-byte>:<span-sha256>`。

具型別的 v1 prefix 會在 UUID 產生後才加入。無論是否存在 extension，一般的 `Decode`、`Encode`
與 creation validation 都會拒絕 UUIDv5。inventory loading 只有在重新計算 UUIDv5，並驗證 extension 的 file
與 span hashes 符合已提交 archive 後，才會接受 imported document；extension 本身永遠不會授權 UUIDv5。

## 對應方式

### Project

上述 project UUID 會成為 `PROJECT.md` 的 `project_id`。migration extension 會記錄 source fingerprint、
fixed namespace、reader version 與已提交的 source archive path。

### 從 Reports 到 Experiments

一份 v0 `REPORT.md` 會成為一個 v1 Experiment。預設保留其目錄；path 用於導覽，不是 identity。
在 `legacy_aliases` 中保留 `#NNN`，並將 structured references 替換成規範的 typed ID。

legacy front matter 後的 Markdown bytes 會直接成為新的 body bytes；migration staging 期間不進行 reflow、
heading change 或 line-ending normalization。legacy front-matter raw bytes 仍可從 source archive 復原。

保守的 status mapping 為：

| v0 status | v1 mapping |
|---|---|
| `planned` | `lifecycle = "planned"` |
| `running` | `lifecycle = "active"` |
| `concluded-success` | closed/concluded/supported |
| `concluded-negative` | closed/concluded/refuted |
| `inconclusive` | closed/concluded/inconclusive |
| `superseded` | closed/superseded；但若 replacement 不明確則為 `needs_review` |

完成 mapping 的 conclusion 仍必須符合 v1 requirements。若缺少 verdict meaning、dates 不一致、
缺少 evidence disposition、factors 含糊不清，或 status/body 不一致，皆標為 `needs_review`；
migration 不會只為了通過 validation 而捏造 values。因此，目前 migrator 對於沒有經審查 v1 Run evidence dispositions
的 concluded reports，要求使用 `archive_only`。

legacy `axis` text 會保留。明確只有單一 factor 時可 map 到 `primary_factor`；multi-factor 或含糊的 text
會保留於 migration extension data，並標為 `needs_review`。migrator 不會選擇 primary factor。

### Result rows 與 external references

不要從 result-table rows、prose 中的 commands、MLflow strings、process status 或 scheduler labels
合成 Runs 或 Attempts。這些來源無法確立預期的 evidence-unit 或 retry identity。

legacy Finding 可以引用其來源 Experiment 作為粗粒度證據，而不建立虛構的 Run。空的 MLflow fields 會成為缺省值。
可解析且經過清理的 external reference 可成為 ExternalRef；否則保留其在 migration extension 中的 raw span，
並發出 diagnostic。

### 從 Ledger 到 Findings

每筆 ledger entry 會成為一個獨立的 Finding。在 `legacy_aliases` 中保留 `F-NNN`，並保留完整的 statement
與 scope text、source/evidence text，以及意義明確的 weaken/overturn 關係。新的 Finding 擁有其 evidence
與會改變 belief 的 edges；reports 不會收到反向 Finding lists。

若 v0 report 列出的 Findings 未被 ledger 反向歸屬，或 ledger 歸屬了 report 未列出的 Finding，則保留兩邊的 raw sources
並回報 mismatch。審查後，獨立的 Finding edge 是唯一的 v1 權威來源。

### 從 Roadmap 到 Plans

每個語法上可辨識的 roadmap item 會成為一個獨立的 Plan。保留 priority lane、effort、title、payoff text、
category text、dependencies、completion syntax 及其精確 source span。只 map 能唯一解析的精確 `#NNN` 與 `F-NNN` references。

title 相符或 prose reference 並不能證明 Plan 產生了 Experiment。queued/completed 不一致仍標為 `needs_review`。
若 payoff 沒有可分離的 metric 或 unit，就保留為 raw migration data 並要求 review，而不是捏造 estimate。

### Decisions

只有在 statement 明確表示 action，且 evidence links 與 span boundaries 沒有歧義時，才建立 Decision。
預先登記的 decision rule 屬於 Experiment design，而非 final Decision。若 intent 含糊不清，narrative interpretations、
summaries 與 TODO-like prose 都保留為 spans。

### Inbox

目前的 migrator 絕不默默將 `INBOX.md` bullets 升級成 qualified Plans 或 canonical Ideas。v0 bullet 缺少
目前 Idea schema 所要求的受控 classification、origin、cluster 與其他語意。讀取器會精確封存該檔案，
記錄每個已辨識的 item/span，並標為 `needs_review`；支援的保守處理方式是 `archive_only`。
遷移後，人類可以建立新的 native Idea，並在其 Markdown rationale 中引用 archived span。

### Generated 與 curated views

legacy README generated blocks 不具權威性。手動維護的 executive summary 是 curated snapshot，
不是重複 Findings 或 Decisions 的來源。兩者都會精確封存，並可從 migration diagnostics 連結；
v1 projections 僅從 canonical records 產生。

## Source archive 與 unknown spans

在替換單體 `ROADMAP.md`、`LEDGER.md` 或任何 report front matter 前，apply 會提交一份精確、唯讀的 source archive：

```text
legacy/harness-v0/<tree-fingerprint>/
├── manifest.toml
└── source/<original relative paths>
```

manifest 會列出每個 path、byte length、SHA-256，以及每個 parsed/unknown byte range。archived file bytes
的 hash 必須與 migration plan 完全相符。此目錄會被 v1 canonical inventory 與 projection rendering 忽略，
但仍作為無損性的 Git-tracked evidence 保留。

每筆 migrated record 也會在下列位置帶有一個精簡 pointer：

```toml
[extensions."io.github.daviddwlee84.exp-cli.harness-v0"]
source_path = "016-example/REPORT.md"
source_sha256 = "sha256:..."
start_byte = 0
end_byte = 1234
```

unknown spans 不會被複製到 free-form core fields。其 archive path、ranges 與 hashes 使每個 source byte
都可復原。若有任何 byte 既未被表示也未被封存，migration plan 就會失敗。

## Plan/apply protocol

除非呼叫端明確提供 `--output`，否則 `exp migrate plan` 為唯讀。典型的 review flow 為：

```sh
exp migrate plan --output draft.json
# copy needs_review keys into a resolution file
exp migrate plan --resolutions resolutions.json --output reviewed.json
exp migrate apply --plan reviewed.json
```

resolution input 是 strict JSON：

```json
{
  "schema_version": "exp.migration-resolutions.harness-v0/v1",
  "resolutions": [
    {"key": "plan:ROADMAP.md:123:456:...", "action": "migrate"},
    {"key": "inbox:INBOX.md:10:42:...", "action": "archive_only"}
  ]
}
```

`migrate` 會核准 plan 中已顯示的精確 candidate；`archive_only` 會保留 bytes，而不建立 canonical record。
沒有有效保守 candidate 的 items（例如沒有 v1 evidence disposition 的 concluded report）只接受 `archive_only`。

版本化的 plan 包含：

- reader 與 target schema versions；
- 精確的 source-tree fingerprint 與 per-file hashes；
- 決定性的 Project 與 record ID mapping；
- 每個 parsed mapping 與 relationship；
- 每個 unknown span；
- diagnostics，包括所有 `needs_review` items；
- candidate archive contents；
- candidate canonical files 與 deterministic projections；
- 可供審查的 unified diff。

含有尚未解決 `needs_review` 的 plan 無法 apply。resolution 是明確的 plan input：選擇預期的 mapping、
提供缺少的必要語意，或僅將材料保留在 archive 中。resolution 絕不編輯 source bytes，且會納入 plan hash。

`exp migrate apply --plan <file>`：

1. 驗證 plan schema 及其自身的 content hash；
2. 重新 fingerprint 每個 source file，並拒絕任何 change、addition、deletion 或 symlink substitution；
3. 重新計算所有 UUIDv5 values 與 candidate hashes；
4. 要求所有 review items 都有明確 resolutions；
5. 驗證完整的 v1 candidate inventory；
6. 將完整 legacy tree 複製到 sibling stage，加入精確 archive 與 canonical files，驗證有 provenance 支持的 candidate inventory，
   並在 Git common directory 下持久發布 prepared journal；
7. 最後在 stage 中產生 projections，接著完成可復原的 two-rename root swap，同時將原始 root 保留為已驗證 backup；
8. 回報精確的 old/new paths 與 revisions。

apply 的重新讀取只用於重新 fingerprint 並驗證精確 spans；它絕不重新解析 semantic mappings，
也不會在已審查 plan 背後做出新選擇。在任一 rename 後 crash，都會從 prepared journal 繼續。
只有在新的 root、archive 與 inventory 完全相符後，才會移除 verified backup。只有當每個 destination hash
都相符時，重新 apply 已完成的 plan 才會成為 idempotent no-op。

migration 會以 v1-compatible records 保留 v0 meaning；不會默默啟用 autonomous dispatch。
審查 migrated inventory 後，必須明確初始化 `POLICY.md`、native ResourcePools 與 Queues，
才能確認新的 Plan v2 work 資格。

## 必要的歧義 diagnostics

經清理的 real-tree migration fixture 必須保留並回報下列情況，不得自行推斷修復：

- 即使相符 prose 表示工作已完成，Plan 仍處於 queued；
- 失效的 relative evidence links；
- report/ledger 反向 Finding drift；
- front-matter/body Finding mismatch；
- 沒有宣告 primary factor 的 multi-factor legacy axes；
- dirty 或 incomplete provenance；
- 空的 MLflow values 是有效的 absence；
- 作為 snapshot 的 curated executive summary；
- project-local skill drift 僅供 guidance。

`needs_review` 是 migration diagnostic state，不是 Experiment lifecycle、verdict、evidence disposition 或 Attempt state。
