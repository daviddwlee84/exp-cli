# 研究心法

控制平面只有建立在可靠方法上才有價值。`exp` 讓下列 distinction 可 enforcement，但 record 與
provider 不能取代 scientific judgment。

## 將 governance 與 checkout convenience 分開

若 research history/access policy 需要跨多個 code/data repositories，優先使用 dedicated private
experiment repository。把每個 governed repository 或 immutable semantic subdirectory 表示成一般
Source。Local association 或 Git submodule 只是 checkout convenience，不是 canonical Source
identity。多個 Sources 是 explicit inputs；scientific record 不應含 host path。

當 code 與 research 確實共用 governance 時，embedded/monorepo Project 仍適合。Layout 不會改變
methodological rules。

## 從 decision 與 expected payoff 開始

Experiment 的正當性來自它能改善的 decision，不只是 novelty。先寫明 metric/unit、plausible
benefit、information value、resource cost、assumption，以及什麼結果會使方向成為 dead end。
真正 exploration 應留在 `explore` lane，不要偽裝成 exploitation。

## 用 Try 處理 bounded uncertainty

Try 詢問某方向是否值得 formalize。它記錄 goal、declared Sources、operational Attempts，以及
attributed conclusion 或 abandonment。有價值的 conclusion 可 adopt 成 Idea v2；本身不是
Candidate。

Dirty work 必須 opt-in、bounded，且 reproducible 到足以 inspect。Dirty SourceSnapshot 只有
Try-owned Attempt v3 合法，raw seed bytes 保存於 private cache，不進 Git。它是 exploratory
evidence：若方向重要，透過 formal Run clean rerun。不要把 local uncommitted state 洗成 production
candidate。

## Evidence 前先 register formal design

第一個 formal Attempt 前，記錄 falsifiable hypothesis、baseline、intended changes、comparability
requirements、success criteria 與 decision rule，再 lock design。由 result 驅動的 threshold/baseline
change 應成為 dated amendment 或 new Experiment，不得重寫 history。

Runtime v2 把 writable execution Source 與每個 read-only Source capture 成 clean full-object-ID
SourceSnapshots。這是 code/input identity，不是 scientific validity 證明。

## 只比較 comparable evidence

Dataset identity、preprocessing、metric、seed、stopping rule、runtime limit、hardware、dependency、
Source commit 與 read-only input 都可能 invalidate comparison。Run 是 intended evidence unit；
Attempt 是一次 operational execution。Retry infrastructure failure 不是新 scientific question，且
retry lineage 必須保留 execution identity。

Successful exit code、Pueue success、output hash、MLflow `FINISHED` 或 artifact URI 只證明 bounded
operational/provider fact。Artifact bytes 仍由 provider 擁有，不能只因保留 URI 就成為 canonical
evidence。

## 區分 negative、inconclusive、invalid 與 operational outcome

- **Refuted：**comparable included evidence 違反 registered hypothesis。
- **Inconclusive：**有 valid evidence，但不足以回答問題。
- **Invalid：**leakage、protocol violation、changed input 或 comparability failure 使 evidence 無法回答。
- **Operational failure：**process failure、timeout、cancellation、preemption 或 OOM；不是 scientific
  verdict。
- **Unknown：**execution 可能已啟動，但沒有 trustworthy terminal observation；不是 retry 許可。

Experiment closure 時 explicit include/exclude 每個 Run 並說明原因。Invalid evidence 不會自動成為
negative evidence。

## Reuse 前要求 typed ownership

能進入 promotion 的 evidence 具有具體 chain：

1. successful terminal formal Attempt v3，且只有 clean SourceSnapshots；
2. 其 Run 被 closed、concluded、supported Experiment included；
3. 在 registered scientific EvaluationSpec 下建立、並綁定 exact Attempt 的 Evaluation v2；
4. Candidate v2 複製 Attempt Source IDs、head commits 與 path sets。

MLflow 只有在 workload-owned run 證明 exact `exp.attempt_id` ownership 時，才能 corroborate
selected metrics；不能取代 Evaluation。Try、dirty snapshot、untyped Evaluation v1、standalone Git
commit 或 artifact location 都不能繞過此 chain。

## 保存 dead end 與 scope

記錄方向為何不該 retry，以及什麼條件改變後可 reconsider。Evidence-backed limit 成為 Finding；
recurring operational remedy 進 project pitfalls；future action 進 TODO/backlog。不要把 anecdote
promote 成 Finding，也不要把 scientific outcome 埋進 troubleshooting prose。

Forward relation 保存在單一 canonical owner，reverse view 由推導取得。新的 belief-changing Finding
會透過 revision/belief digest 使 dependent Plan stale；不要暗中 reuse 以 overturned assumption 排出的
ranking。

## 保留 branch 並測試 combination

Useful outcome 可建立多個 child Ideas。每個 branch 應 independent pricing 並保留 parent edge。
Independently successful Candidates 不假設 additive；multi-Candidate Release 需要自己的 supported
combination Experiment 與 passing Evaluation。

## Promotion 必須 human-gated

Scientific Evaluation 判斷 exact result 能否成為 Candidate；Promotion Evaluation 判斷 complete
validated Release 是否應取代某 target incumbent。使用前先 seal holdout protocol 與 finite budget，
不得 reuse holdout Evaluation，每筆 append-only Promotion 都要求具名 human。任何 autonomy mode、
provider、artifact registry、generated Champion manifest 或 read-only TUI 都不能核准 deployment。

探索可由 agent 明示 unreviewed conclusion 收尾；human adoption 與正式 evidence gates 仍分開。詳見[臨時探索](workflows/exploration.md)。
