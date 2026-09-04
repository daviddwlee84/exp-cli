# 自主研究控制平面

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

## 閉環

控制平面 (control plane) 將線性的 v1 證據模型擴展為持久的研究有向無環圖
(research DAG)，同時維持規範 Markdown (canonical Markdown) 作為唯一的科學權威來源。

```mermaid
flowchart LR
  S[Source] --> T[Bounded Try]
  T -->|human adoption| I[人類或 agent Idea]
  S --> A
  I --> P[已確認資格的 Plan]
  P --> Q[Pool x lane Queue]
  Q --> A[Clean formal Attempt]
  A --> E[Typed Evaluation]
  E --> F[Finding]
  F --> I
  E --> C[Candidate]
  C --> X[組合 Experiment]
  C --> R[具型別的 Release]
  X --> R
  R --> S[密封的 Promotion Evaluation]
  S --> M[人類 Promotion]
  M --> H[衍生的 Champion]
```

Idea、Experiment 與 Candidate 的父級邊 (parent edge) 讓分支能被追蹤、合併或放棄，
而無須重寫歷史。反向邊與整合檢視皆為投影 (projection)；每個規範關係仍然恰好只有一個擁有者。

## 規範記錄

`POLICY.md` 是不含 ID 的特殊單例 (singleton)。它儲存自主程度、利用／探索比例、
Queue 公式版本、平手行為、受控分類詞彙、叢集飽和門檻，以及強制的人類 Promotion 關卡。

Policy 以 `manual` 模式建立。`manual` 與 `shadow` 會揭露規範前緣
(canonical frontier)，但不授予派送權限。`assisted` 與 `limited` 只有在呼叫端提供明確的
`--confirm-auto-experiment` 確認後，才會啟用 Experiment 派送。Promotion 是另一道獨立的權限邊界，
在所有模式中都仍然只能由人類執行。

新增項目使用 typed UUIDv7 ID：

| Record | Authority |
|---|---|
| Source | Project-local Git identity、immutable subdir、sanitized locator、lifecycle |
| Try | Bounded goal、declared Sources、human conclusion/abandonment/adoption |
| Idea | Human/agent proposal、qualification state、origin、cluster、parent Ideas；v2 可有 `origin_try` |
| ResourcePool | Bounded bottleneck、capacity、unit、optional cost |
| Queue | 依 ResourcePool/exploit-explore lane partition 的 ordered Plan entries |
| QueueAdvice | 針對 exact Queue revision 的 immutable listwise ranking suggestion |
| Battle | Immutable order-swapped pairwise comparison/confidence |
| EvaluationSpec | Metric、direction、protocol、resource budget、optional seal |
| Evaluation | Immutable measured outcome；v2 對 Experiment subject 額外擁有一個 formal Attempt |
| Candidate | Evaluated Experiment result/parents；v2 pins clean formal Attempt/Source commits/paths |
| Release | Target 加由 Candidates 填入的 typed slots |
| PromotionSpec | Sealed holdout protocol 與 mandatory human approval policy |
| Promotion | Link previous Promotion 的 append-only challenger/incumbent decision |

自由形式的探索標籤仍保留在 `tags` 中。Queue policy 使用受控的 `domain`、`work`、
`method`、`component`、`lane`、`risk`、`horizon` 與 `origin` 分類欄位，外加一個主要叢集。

## Queue 權威與過時工作

Queue 分區由 `(resource_pool, lane)` 識別。項目順序具有語意；同一個 Plan 在 Queue 中只能出現一次，
而每個項目都會固定排名時使用的正規化 Plan revision。Queue mutation 會遞增其正整數 revision；
Advice 與 Battle 記錄則保留它們當時觀察到的 revision。

Plan v2 dependency 同時固定 Finding 記錄的 revision 與 belief digest。belief digest 涵蓋目標 revision，
以及所有傳入的 `weakens`／`overturns` 邊，包括來源 Finding 的 revisions。因此，即使目標 Finding 檔案本身
沒有變更，新增或改變會影響 belief 的證據，仍會使相依的 Plans 與 Queue 項目過時。過時的 Plans 是無效 inventory，
不得派送。

重新整理過時工作並非盲目確認：呼叫端要提供一組完整的新 utility estimate；transaction 會重新固定 beliefs、
將 Plan 從其 Queue 移除，並使其 Idea 回到 qualified 狀態。之後再次插入 Queue 時，必須重新評分並進行 battle。
已開始／完成的 Plans 保留其歷史 pins，不會因執行後發現的新證據而變成過時。

Queue Advice 是 listwise 且暫定的。插入時可再與相鄰的現有項目比較兩次，並交換呈現順序。若發生 abstention、
低信心回應、順序不一致，或 policy 規定的平手，就必須交由人類審查；Advice 與 Battle audit 會保留，
但 Queue 順序維持不變。其他穩定的平手則讓原有項目維持在前。

透明的暫定分數會結合預期效用、資訊增益、解除阻塞價值、下行風險、受限 pool-hours，以及有上限的 aging bonus。
具名 ResourcePools 是硬性的容量邊界。daemon 會隨時間以 Policy 預設的 80/20 exploit/explore 比例為目標；
若只有其中一個 lane 有符合資格的工作，則借用閒置容量。
每個自主 Plan 恰好使用一項 ResourcePool need；在原子的 multi-pool admission 可用之前，耦合資源以 composite pool 表示。

## 執行與評估

Experiment v2 新增 replication、sweep、combination design、multi-parent lineage 與 explicit Candidate
inputs。Attempt v2 是 closed legacy formal dispatch shape，含 Pool/Queue/lane/dispatch identity 與
top-level Git base/head/ChangeSet。Attempt v3 改為恰好擁有一個 Run 或 Try，並帶 execution Source
與 sorted SourceSnapshots；formal dispatch fields 全有或全無，只有 Try-owned v3 可 dirty/use
`retry_of`。每個 older schema 都保留 exact closed decoder。

`.exp/runtime.json` 是 operational config，不是 canonical record。Runtime v1 保留 embedded
Pool/Plan/Pueue/Git contract。Runtime v2 要求 exact `runtime.dispatch` trust，透過 host-local
association 解析 canonical Sources，把一個 writable 與 optional read-only Sources 綁到 explicit
checkouts，且只 capture clean SourceSnapshots。Pool label prefix 仍 prefix-free，environment array
只含 names，Pueue secret environment array 必須 empty。

Daemon 使用 private SQLite lease、fencing、job、fairness、outbox；Pueue 擁有 live task。
Worker-job v2 經 explicit canonical root、Project UUID、checkout-local scope invocation；worker 先
驗證 metadata authority 再讀 payload，之後驗證 private checkout/snapshot identity。它 freeze
bounded result，並在 SQLite 前 publish durable v2 terminal marker；valid marker（包括 recoverable
temporary 加 exact frozen result）可 replay 而不重跑。Missing evidence 保持 unknown/blocked。

Code-editing agent 使用位於 exact clean base 的 Source-aware linked worktree。`exp` 只 commit observed
allowlisted path，絕不 merge/push 或授予 integration authority。Dirty Try 可使用 private bounded
seed/native verified cleanup，但不能支持 Candidate。Candidate v2 要求 included Run 的 successful
clean formal Attempt v3、綁定同一 Attempt 的 Evaluation v2，並從其 snapshots 精確複製 Source
IDs/head commits/ChangeSets。

Optuna 或其他 search backend 可在單一 Plan 內擁有 ask/tell trials 與 pruning。它不擁有全域 Idea queue、
跨 Plan 資源分配、Findings、Releases 或 Promotions。

跨越多筆記錄的科學 mutation 使用 prepared transaction。在第一次規範 rename 前，完整 candidate inventory
與精確 bytes 都會先持久化；重新啟動後的 recovery 會依 old/new hashes 向前滾動，並拒絕覆寫不相關的編輯。

## Releases 與 champions

Release slots 依 project convention 命名並具型別（單體系統使用 `main`，或例如 `signal`、`risk`、
`portfolio` 與 `execution`）。組合多個 Candidate 時，必須有另外評估過且支援該組合的 combination Experiment，
並將其通過的科學 Evaluation 與 Release 範圍的 production Evaluation 分開儲存。控制平面絕不假設獨立量測的增益可以相加。

Promotion 使用密封的 promotion-purpose EvaluationSpec 與有限的 holdout budget。每個 target 的 Promotion records
會形成一條僅可附加的 chain。accepted 與 rollback 項目都需要通過且新鮮的 holdout，以及一位人類 approver；
rollback 只能還原被目前 champion-setting 項目取代的 incumbent。現行 Champion 從該 chain 衍生，並可渲染供下游使用；
產生的 manifest 不會被讀回作為權威來源。

## 規範目錄配置

新的具型別記錄使用 `experiments/` 下保留的扁平目錄：

```text
POLICY.md
sources/
ideas/
resource-pools/
queues/
queue-advice/
battles/
evaluation-specs/
evaluations/
candidates/
releases/
promotion-specs/
promotions/
```

Try record 使用 `t-<full-uuid-hex>-<slug>/TRY.md`，owned Attempts 位於下層 `attempts/`。
既有 Plan、Experiment、Run、Attempt、Finding、Decision paths 不變。Source recognition 只保留
exact canonical filename，因此 unrelated legacy `sources` content 不會 reinterpret。
