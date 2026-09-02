# 記錄格式與 schema 版本

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

## 檔案封套

每一筆規範記錄 (canonical record) 都是一個 UTF-8 Markdown 檔案。檔案從第零個位元組開始，以僅含 `+++` 的行作為分隔符，包住 TOML 前置資料 (front matter)；其餘部分則是一般的 Markdown 內文。

```markdown
+++
schema = "exp.plan/v1"
id = "plan_01a01e66-f8e0-7202-8000-000000000202"
title = "Calibrate encoder learning rate"
created_at = 2026-08-20T09:01:00Z
updated_at = 2026-08-20T10:03:00Z
priority = "P1"
effort = "S"
state = "completed"
resulting_experiment = "exp_01a01e67-e340-7303-8000-000000000303"

[expected_payoff]
summary = "Avoid regressions while reducing calibration runs"
metric = "macro_f1"
unit = "score"
estimate = 0.03
+++

# Calibrate encoder learning rate

The body carries rationale and context that need not be query fields.
```

開頭分隔符、結尾分隔符與最後一個 LF 都是必須的。寫入器 (writer) 一律輸出 LF 行尾。前置資料必須能依 TOML 1.0 解析，且不得包含重複 key。

單一規範記錄檔案的上限為 8 MiB（8,388,608 位元組），包含前置資料與 Markdown 內文。磁碟驗證 (on-disk validation) 會透過一個已開啟的根目錄走訪保留的規範位置，只讀取一般檔案；在可用時採用核心 no-follow、開啟前／開啟時／開啟後的身分檢查，以及「上限再加一位元組」的哨兵讀取 (limit-plus-one sentinel read)。遇到符號連結 (symlink)、檔案類型或身分競態，以及過大的記錄時，系統會回報問題，而不會跟隨連結或解碼內容。

`schema` 會選擇一個精確解碼器 (exact decoder)：

```text
exp.project/v1
exp.policy/v1
exp.idea/v1
exp.resource-pool/v1
exp.queue/v1
exp.queue-advice/v1
exp.battle/v1
exp.plan/v1
exp.plan/v2
exp.experiment/v1
exp.experiment/v2
exp.run/v1
exp.attempt/v1
exp.attempt/v2
exp.evaluation-spec/v1
exp.evaluation/v1
exp.finding/v1
exp.candidate/v1
exp.release/v1
exp.promotion-spec/v1
exp.promotion/v1
exp.decision/v1
```

Plan、Experiment 與 Attempt 的 v1 decoder 維持精確且封閉。其 v2 decoder 分別加入含價格的 Queue 輸入、研究譜系／組合輸入，以及 dispatch／ChangeSet 識別；若 v1 記錄包含僅限 v2 的欄位，便會遭到拒絕。自主控制平面 (autonomous control plane) 的記錄及其擁有權規則，詳見 [autonomous-research-control-plane.md](autonomous-research-control-plane.md)。

已知 schema 會在每個已知 table 層級拒絕未知欄位。唯一開放的容器是 `extensions`。每個 extension 以小寫的反向 DNS namespace 為 key，並在不加以解讀的情況下遞迴保留：

```toml
[extensions."org.example.review"]
reviewed_by = "synthetic-reviewer"
```

像 `x_owner` 這樣的頂層廠商 key 並不是 extension，因此會遭到拒絕。若 extension value 無法用 TOML 表示，或違反大小／隱私限制，核心驗證器仍可拒絕該值。

## 識別碼與別名

新記錄使用小寫、帶型別的 RFC 9562 UUIDv7：

```text
plan_<uuidv7>
idea_<uuidv7>
pool_<uuidv7>
queue_<uuidv7>
advice_<uuidv7>
battle_<uuidv7>
exp_<uuidv7>
run_<uuidv7>
att_<uuidv7>
evalspec_<uuidv7>
eval_<uuidv7>
fnd_<uuidv7>
cand_<uuidv7>
rel_<uuidv7>
promspec_<uuidv7>
prom_<uuidv7>
dec_<uuidv7>
```

前綴必須與所選 schema 相符，而且每個關聯都儲存完整的帶型別 ID。ID 不可變更。建立記錄時，會先檢查完整的專案清冊 (project inventory)，確認尚無既有 ID，才進行發布。

`PROJECT.md` 使用不帶型別前綴的 `project_id` UUID，因為它是 namespace root，而不是跨種類記錄參照。新專案使用 UUIDv7。`POLICY.md` 是另一筆不含 ID 的特殊記錄，並由其固定規範路徑確保唯一性。

一般的原生建立只接受 UUIDv7，即使記錄帶有 `extensions."io.github.daviddwlee84.exp-cli.harness-v0"` 也是如此；extension 的存在不會授權使用 UUIDv5。明確採用指紋識別的 harness-v0 migrator，只有在重新計算識別碼，並以已提交的來源 archive 驗證經審閱的 provenance 之後，才會使用 deterministic UUIDv5。遷移後的記錄會在可用時保留舊版別名；遷移後的 Project 則使用重新計算的 UUIDv5 作為 `project_id`。隨機 UUIDv4、ULID、hash 與循序 ID 都不是規範 ID。請參閱 [harness-v0-migration.md](harness-v0-migration.md)。

顯示形式為 `<letter>-<prefix>`，使用 UUID 前八個大寫十六進位數字，且不含連字號。字母對應如下：`I` Idea、`O` Pool、`Q` Queue、`V` Advice、`B` Battle、`P` Plan、`E` Experiment、`R` Run、`A` Attempt、`S` EvaluationSpec、`N` Evaluation、`F` Finding、`C` Candidate、`L` Release、`T` PromotionSpec、`M` Promotion，以及 `D` Decision。若候選字串在同種類記錄中不唯一，便逐次多取一個十六進位數字。只有在無歧義時，才接受顯示代碼與唯一的帶型別 ID 前綴；它們絕不會作為關聯 key 持久化，並可能隨著專案成長而加長。

`legacy_aliases` 是遷移所使用的 optional array。Harness alias 的精確格式為：Experiment 使用 `#NNN`、Finding 使用 `F-NNN`，其中包含三個以上的十進位數字。別名解析會考慮型別，而且必須唯一。新的原生記錄不會配置循序別名。

## 共用欄位

除了 Project 與 Policy singleton 之外，每筆記錄都有：

| 欄位 | 要求 |
|---|---|
| `schema` | 必填，且必須是精確的 schema 字串 |
| `id` | 必填，使用上述帶型別 UUID |
| `title` | 必填，非空白單行字串 |
| `created_at` | 必填，RFC 3339 UTC TOML offset datetime |
| `updated_at` | 必填，RFC 3339 UTC TOML offset datetime，且不得早於建立時間 |
| `legacy_aliases` | Optional unique strings；僅供遷移使用 |
| `tags` | Optional、已排序且唯一的小寫 slug |
| `extensions` | Optional、帶 namespace 的 extension tables |

時間戳記 (timestamp) 使用 UTC，序列化時以 `Z` 表示。用來建模集合的 array 必須唯一，並依位元組詞彙順序序列化。具順序意義的科學資料，例如 amendment history 與 evidence disposition，會保留其語意順序。

## Project

`<git-root>/experiments/PROJECT.md` 標示 v1 唯一會探索的 root。

必填欄位為 `schema`、`project_id`、`name`、`created_at` 與 `experiments_root`。由於 `PROJECT.md` 位於 root 內，`experiments_root` 為 `.`。Optional 欄位只有 `extensions`。V1 不會搜尋其他 marker；它們是範圍外檔案，而不是 active root 或 discovery error。具名或多個 root 延後處理。

## Policy、Idea、ResourcePool 與 Queue

`POLICY.md` 是固定且不含 ID 的 `exp.policy/v1` singleton。它擁有 autonomy、exploit/explore share、score formula、tie policy、強制的人類 Promotion gate、受控 classification taxonomy、cluster saturation 預設值，以及 optional per-cluster state。原生 policy 初始化預設為 `manual` 與 0.8/0.2 share。

Idea 擁有 proposal state、summary、proposer、primary cluster、classification、parent Idea edge、resulting Plan edge，以及 optional merge target。有效 state 為 `proposed`、`developing`、`qualified`、`queued`、`dismissed` 與 `merged`。

ResourcePool 擁有 bottleneck 是否啟用、其整數 concurrent capacity、unit、bottleneck slug，以及 optional hourly cost。它不儲存 Pueue group；該 host/runtime binding 屬於 `.exp/runtime.json`。

Queue 擁有正整數 semantic revision、pause flag，以及依 ResourcePool 和 `exploit`／`explore` lane 為 key 的有序 `[[partitions]]`。每個 entry 儲存 Plan ID、精確 Plan revision、score、insertion time 與 pin flag。一個 Plan 在整個專案中只能出現一次。Partition 與 entry 的順序具有語意，絕不會正規化成集合。

QueueAdvice 是針對某個精確 Queue revision 的單次 listwise recommendation，不可變更；內容包括 candidate Plan、pool/lane、proposed position、完整 suggested order、score component、model/profile report、prompt digest 與 rationale。Battle 是不可變更的 adjacent comparison，記錄兩種 presentation order、resulting outcome、confidence 與 rationale。兩種記錄本身都不能改變 Queue order。

## Plan v1 與 v2

額外欄位：

| 欄位 | 要求 |
|---|---|
| `priority` | 必填：`P1`、`P2`、`P3` 或 `P?` |
| `effort` | 必填：`S`、`M`、`L` 或 `XL` |
| `state` | 必填：`queued`、`started`、`completed` 或 `dropped` |
| `assumptions` | Optional canonical Finding ID array |
| `resulting_experiment` | `started` 與 `completed` 時必填；其他情況禁止 |
| `expected_payoff.summary` | 必填，非空白陳述 |
| `expected_payoff.metric` | 必填，穩定的 metric slug |
| `expected_payoff.unit` | 必填，非空白 unit |
| `expected_payoff.estimate` | Optional，該 unit 下的有限數值 |

Plan 擁有 `resulting_experiment` 與 `assumptions`。Experiment 與 Finding 不儲存反向 Plan list。

`exp.plan/v2` 保留所有 v1 欄位，並額外要求用於 Queue 的 qualified control-plane context：

| 欄位 | 要求 |
|---|---|
| `idea` | Optional originating canonical Idea；由 Idea qualification 設定 |
| `primary_cluster` | 受控 cluster slug |
| `classification` | Domain、work、method、component、lane、risk、horizon 與 origin |
| `dependencies` | Finding ID，加上精確 revision 與目前 belief digest |
| `resources` | 一個以上的 ResourcePool、unit 與正 estimated hours |
| `utility` | Probability、impact、information gain、unblock value 與 risk penalty |

Belief digest 涵蓋被參照的 Finding revision，以及所有傳入的 `weakens`／`overturns` edge（包括來源 revision）。過期的 dependency 或已進入 Queue 的 Plan revision 會阻擋 dispatch。

## Experiment

額外的頂層欄位是 `lifecycle`、optional `closure`、optional `verdict`、`design`、optional ordered `amendments`、optional `closure_detail` 與 optional `conclusion`。

`design` 要求：

```text
question
hypothesis
kind                 single_factor | factorial | observational
                     replication | sweep | combination (v2 only)
primary_factor
secondary_factors    array of strings
baseline
comparability_spec
success_criteria     non-empty array of strings
decision_rule
```

`design_locked_at` 與 `design_digest` 必須同時不存在或同時存在。在註冊第一個 Attempt 前，兩者均為必填。計算 `design_digest` 時，使用恰好包含上述九個 design 欄位的 UTF-8 JSON 計算 SHA-256；object key 依位元組順序排序、array 保留原順序、JSON string 依一般規則跳脫，且不含無意義空白；輸出 `sha256:<64 lower-case hex>`。Lock timestamp 與 digest 本身不列入輸入。每筆 amendment 是一個 array-table item，包含 `amended_at`、`reason`、`previous_digest`、`new_digest` 與非空白 `changes` array。其 previous digest 必須等於前一個 design digest，且 amendment 必須嚴格依時間順序排列。

Lifecycle invariant：

| Lifecycle | 必填 | 禁止 |
|---|---|---|
| `planned` | design | `closure`、`verdict`、`closure_detail`、`conclusion` |
| `active` | design | `closure`、`verdict`、`closure_detail`、`conclusion` |
| `closed` + `concluded` | `closure`、`verdict`、`conclusion` | `closure_detail.superseded_by` |
| `closed` + `abandoned` | `closure`、`closure_detail.reason` | `verdict`、`conclusion`、`closure_detail.superseded_by` |
| `closed` + `superseded` | `closure`、`closure_detail.reason`、`closure_detail.superseded_by` | `verdict`、`conclusion` |

已得結論的 verdict 必須恰為 `supported`、`refuted`、`inconclusive` 或 `invalid` 之一。

`conclusion` 要求 `concluded_at`、`summary`，以及一筆以上的 `[[conclusion.evidence]]` entry。每個 entry 包含 `run`、`disposition`（`included` 或 `excluded`）與 `reason`。只有 included evidence 的 reason 可以為空白。每個 Run 都必須存在且屬於此 Experiment。Attempt 不是 evidence reference。

Experiment 只會透過 `closure_detail.superseded_by` 擁有 supersession edge。它不儲存 originating Plan、Run、Attempt、Finding 或 Decision。

`exp.experiment/v2` 加入 parent Experiment edge 與 `candidate_inputs`。Combination Experiment 使用 Candidate input，測試各自獨立評估過的變更在組合後是否仍然有效；這些 input 本身並不能證明 additivity。

## Run

額外欄位：

| 欄位 | 要求 |
|---|---|
| `experiment` | 必填 canonical Experiment ID；此 edge 由 Run 擁有 |
| `role` | 必填：`baseline`、`candidate`、`validation` 或 `batch` |
| `objective` | 必填，對預期 evidence 的非空白說明 |
| `config_digest` | Optional SHA-256 digest |
| `data_digest` | Optional SHA-256 digest |
| `seeds` | Optional ordered integer array |
| `expected_outputs` | Optional sorted array，內容為安全的 repository-relative POSIX path |

Run 不含 process state、Attempt list、evidence disposition 或 scientific verdict。一個 Run 可以有多個 Attempt；由 tracker 擁有的 sweep trial 可以只存在於 ExternalRef 之後。

## Attempt

額外欄位：

| 欄位 | 要求 |
|---|---|
| `run` | 必填 canonical Run ID；此 edge 由 Attempt 擁有 |
| `state` | 必填 operational state |
| `state_reason` | Optional sanitized provider/native reason |
| `runner` | 必填 provider name |
| `scheduler` | 必填 provider name；恰由一個 scheduler 擁有 Attempt |
| `cwd` | 必填安全的 repository-relative POSIX path；允許 `.` |
| `argv` | 必填非空白 argument array；不得使用 shell string |
| `external_refs` | Optional ExternalRef table array |
| `provenance` | Optional structured provenance table |
| `terminal` | 已知 terminal state 時必填；nonterminal state 時禁止 |

Operational state 定義於 [architecture.md](architecture.md)。已知 terminal state 為 `succeeded`、`failed`、`cancelled`、`timed_out`、`preempted` 與 `out_of_memory`。`unknown` 可以沒有 terminal record，而且絕不會授權自動重試。

Terminal table 包含 `source`、`observed_at`、optional `started_at`、必填 `ended_at`，以及 optional `exit_code` 與 `signal`。Timestamp 必須依序排列。禁止根據特定 state 推斷 exit code；provider evidence 必須明確證實如 `out_of_memory` 等 classification。

建議的 provenance key 為 `captured_at`、完整 `git_commit`、`git_dirty`、optional `dirty_digest`、`config_digest`、`data_digest`、`environment_digest`，以及 `reproducibility`（`exact`、`bounded`、`partial` 或 `unknown`）。Provenance value 必須能安全提交。

每個經明確註冊且已遮蔽敏感資訊的 Attempt 都是已提交的規範記錄，包括失敗的 Attempt。Git common directory 下的 local start/terminal marker 是 operational input，會匯入此記錄；它們不能取代此記錄。

`exp.attempt/v2` 額外固定 dispatch context：ResourcePool、Queue、Queue semantic revision、lane、stable dispatch ID、完整 Git base/head commit，以及精確 ChangeSet。這些欄位將 scientific Attempt 連接到一個已准入的 Queue frontier 與一個已審閱的 code candidate；Pueue task ID 與 live status 仍是 external observation。

## ExternalRef

每個 `[[external_refs]]` item 包含：

```text
role          runner | scheduler | tracker | artifact | registry
provider      lower-case provider slug
context       configured non-secret context name
native_kind   provider-native resource kind
native_id     provider-native immutable/scoped ID
uri           optional sanitized URI
observed_at   optional provider observation time
metadata      optional map whose keys are provider-namespaced
```

Reference 代表 identity，而不是宣稱 cached state 仍是最新。Live observation 會另外攜帶 source、provider version、capability、`observed_at`、`stale`、`partial`、diagnostic，以及有限且已消毒的 native state；除非刻意匯入為事實，否則它們都位於規範記錄之外。

## Finding

額外欄位為 `statement`、`scope`、一筆以上的 `[[evidence]]` entry、optional `weakens` 與 optional `overturns`。

- `statement` 與 `scope` 為必填非空白字串。
- 每個 evidence entry 包含 `kind`、`ref` 與 optional `detail`。原生 v1 Finding 使用 `kind = "run"` 搭配 canonical Run ID。若 v0 只識別到來源 report，遷移可使用 `kind = "experiment"` 搭配 canonical Experiment ID；這明確屬於粗粒度 evidence，不得因此合成 Run。
- 重複的 `(kind, ref)` evidence entry 無效。
- `weakens` 與 `overturns` 是 unique canonical Finding ID array。
- 同一 target 不得同時出現在兩個 array 中；self-edge 與 relation cycle 均無效。

Finding 擁有全部三類 relation。它不儲存 `active`、`weakened` 或 `overturned` status；projection 會從 incoming edge 推導該 status，並保留所有 historical record。

## Evaluation、Candidate 與 Release

EvaluationSpec 擁有 purpose（`scientific` 或 `promotion`）、凍結的 dataset 或 split identifier、protocol、一個以上的 metric contract、ResourcePool budget、有限 budget hours，以及 optional seal time。每個 metric 宣告 name、unit、direction 與 optional threshold。Promotion-purpose spec 必須 sealed，且每個 promotion metric 都要有 threshold，使 `passed`／`failed` 的結果具 deterministic 性質。

Evaluation 不可變更，並擁有其 EvaluationSpec 與 subject edge。Subject 為 Experiment、Candidate 或 Release。它記錄 `passed`、`failed` 或 `invalid`、evaluation time、完全依宣告的 metric value、summary，以及 optional sanitized external reference。Tracker state 不會整批複製。

Candidate 擁有其 concluded Experiment、通過的 scientific Evaluation、parent Candidate edge、完整 Git commit、精確 ChangeSet，以及 optional external reference。對 included Run 執行成功的 direct Attempt，必須符合該 Git identity 與 ChangeSet。Candidate 是可重複使用、經評估的結果，而不只是一個成功的 process。

Release 擁有 target、version、state（`draft`、`validated` 或 `retired`），以及對應至 Candidate 的 unique named slot（依 slot name 正規化）。它也可以擁有 combination Experiment、與之不同的 `combination_evaluation`，以及 Release-level Evaluation。若包含一個以上不同的 Candidate，就必須有 supported combination evidence；slot name 是專案慣例，可以表示 logic、strategy parameter、code 或 model。

## Promotion 與 Champion

PromotionSpec 擁有一個 target、一個 sealed promotion-purpose EvaluationSpec、有限 holdout hours、seal time，以及 human-approval requirement。

Promotion 僅能附加。它擁有 target、spec、challenger Release、optional incumbent Release、holdout Evaluation、outcome（`accepted`、`rejected` 或 `rolled_back`）、applied time、previous Promotion edge，以及具名 human approver。Holdout Evaluation 必須在 PromotionSpec seal 之後建立，而且不得由另一個 Promotion 重複使用。Accepted 與 rollback outcome 需要通過的 holdout；rollback 只能恢復由目前設定 champion 的 Promotion 所取代之 incumbent。EvaluationSpec、Run、Finding、Decision、QueueAdvice、Battle、Evaluation、Candidate、PromotionSpec、Promotion，以及 validated/retired Release 發布後均不可變更；後續知識應建立新的 linked record，而不是重寫 evidence。
每個 target 的目前 Champion 都由其有效 chain 推導；生成的 champion manifest 絕不是 canonical。

## Decision

額外欄位為 `statement`、`based_on`、`action`、`effective_at` 與 optional `supersedes`。

- `based_on` 是非空白且 unique canonical Finding ID array。
- `action` 是非空白、可安全提交的說明。其中的 repository path 應在內文中表示為 Markdown link，或以明確且安全的 path extension data 表示，不得使用 absolute host path。
- `supersedes` 是 unique canonical Decision ID array；self-edge 與 cycle 均無效。

Decision 擁有這些 relation。Active/superseded presentation 由 incoming `supersedes` edge 推導。

## 關聯擁有權摘要 { #relationship-ownership-summary }

| 關聯 | 唯一擁有者 |
|---|---|
| Idea 衍生自 Idea | Child Idea |
| Idea qualified 為 Plan | Idea |
| Idea merge 進 Idea | Merged Idea |
| Plan 假設 Finding | Plan |
| Plan 具有由 revision/belief 固定的 Finding dependency | Plan v2 |
| Plan 消耗 ResourcePool budget | Plan v2 |
| Plan 產生 Experiment | Plan |
| Queue 在 ResourcePool/lane 中排序 Plan | Queue |
| QueueAdvice/Battle 觀察 Queue revision | QueueAdvice/Battle |
| Experiment 衍生自 Experiment | Child Experiment v2 |
| Combination Experiment 消耗 Candidate | Experiment v2 |
| Run 屬於 Experiment | Run |
| Attempt 執行 Run | Attempt |
| Attempt 從 Queue/Pool/lane 獲准執行 | Attempt v2 |
| Experiment 將 Run 納入／排除為 conclusion evidence | Experiment conclusion |
| Evaluation 遵循 EvaluationSpec 並評估 subject | Evaluation |
| Finding 引用 Run（或粗粒度遷移的 Experiment） | Finding |
| Finding 削弱／推翻 Finding | New Finding |
| Candidate 封裝 Experiment/Evaluation 與 parent Candidate | Candidate |
| Release 以 Candidate 填入 named slot | Release |
| Release 引用 combination Experiment/Evaluation | Release |
| PromotionSpec 使用 sealed EvaluationSpec | PromotionSpec |
| Promotion 挑戰／引用 incumbent／評估／延續 chain | New Promotion |
| Decision 以 Finding 為依據 | Decision |
| Decision supersede Decision | New Decision |
| Experiment 被 Experiment supersede | Old Experiment closure |

反向關聯透過 inventory scan 計算。絕不能讀取生成的 projection 來重建 edge。

## 路徑、URI 與隱私

提交的 path 必須是乾淨的 repository-relative POSIX path。只有明確記載允許的地方可以使用 `.`。拒絕下列項目：

- POSIX、Windows drive、UNC 或 `file:` absolute path；
- `..`、空白 segment、NUL、反斜線分隔符，或透過 symlink 逃逸的 path；
- 解析至 Git worktree 外的 path；
- credential、home-directory shorthand，以及 host-specific temporary directory。

提交的 URI 必須能按結構解析，而且不得包含 userinfo 或 query component。Provider adapter 會將 identity 擷取到 `provider`、`context`、`native_kind` 與 `native_id`，再輸出 sanitized URI。遮蔽敏感資訊 (redaction) 發生於 validation、logging、diagnostic、caching 或 persistence 之前。含有 secret 的 URI input 會遭到拒絕；若沒有明確安全的值，系統不會只做部分遮蔽後儲存。

Canonical field 絕不包含 secret value、raw environment、authorization header、cookie value、private key 或 unbounded provider output。除非已宣告且經審閱的 provenance policy 需要某個非敏感值，否則會省略 hostname 與 user name。

## Revision

樂觀 revision (optimistic revision) 由計算取得，不會儲存：

```text
sha256:<lower-case hex of normalized record bytes>
```

正規化 (normalization) 具 deterministic 性質：

1. 解碼並驗證所選的 typed schema。
2. 依 schema 順序序列化 core field；依詞彙順序序列化 set array。
3. 依位元組詞彙順序，遞迴序列化 extension namespace 與 key。
4. 以 UTC RFC 3339 格式呈現 timestamp，digest 使用小寫。
5. 使用 LF delimiter 與 line ending。
6. 附加 Markdown 內文，不重新排列空白或變更 heading，並確保最後有一個 LF。

Path、file mode、生成的 projection 與 revision string 本身都不列入 hash input。未知且不帶 namespace 的 field 無法正規化，因此會在計算 revision 前失敗。

## 配置與 projection

Canonical path 為：

```text
PROJECT.md
POLICY.md
ideas/idea_<full-uuid>-<slug>.md
plans/plan_<full-uuid>-<slug>.md
resource-pools/pool_<full-uuid>-<slug>.md
queues/queue_<full-uuid>-<slug>.md
queue-advice/advice_<full-uuid>-<slug>.md
battles/battle_<full-uuid>-<slug>.md
evaluation-specs/evalspec_<full-uuid>-<slug>.md
evaluations/eval_<full-uuid>-<slug>.md
findings/fnd_<full-uuid>-<slug>.md
candidates/cand_<full-uuid>-<slug>.md
releases/rel_<full-uuid>-<slug>.md
promotion-specs/promspec_<full-uuid>-<slug>.md
promotions/prom_<full-uuid>-<slug>.md
decisions/dec_<full-uuid>-<slug>.md
e-<allocated-short-prefix>-<slug>/REPORT.md
e-<allocated-short-prefix>-<slug>/runs/run_<full-uuid>-<slug>.md
e-<allocated-short-prefix>-<slug>/attempts/att_<full-uuid>.md
```

Slug 由小寫 ASCII 字母／數字組成，並以單一連字號分隔。Path 絕不決定 identity；當 display prefix 需要加長時，既有 experiment directory 不會自動重新命名。

`README.md`、`ROADMAP.md`、`LEDGER.md` 與 `DECISIONS.md` 以以下內容開頭：

```markdown
<!-- Generated by exp render; DO NOT EDIT. -->
```

Rendering 不包含目前時間、hostname、absolute path 或 cache data。Record 先依 view 的明確 key，再依完整 canonical ID 排序：Plan 依 state、priority、ID；Experiment 依 lifecycle、ID；Finding 與 Decision 依 creation time、ID。Table cell 會將 `|` 跳脫為 `\|`，並將內嵌 newline 表示為 `<br>`。Link 使用 relative POSIX path。輸出使用 LF，並保留一個最終 newline。`exp render --check` 會比較精確位元組，絕不寫入檔案。
