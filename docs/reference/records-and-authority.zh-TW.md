# Records 與 Authority

每項事實只使用一個 owner。`exp` 把獨立 canonical experiment repository 中的 scientific
meaning，連到 Source Git repositories 與 provider-owned execution；不會把所有 system 複製進
同一個 database。Reverse relationship、Champion、generated page 與 TUI row 都由 authority
推導，不是競爭中的事實。

## Authority map

| Store 或 system | 擁有的內容 | 無法建立的內容 | `exp` 的使用方式 |
|---|---|---|---|
| 所選 experiment repository `experiments/` 下的 canonical records | Project/Policy、Sources、Ideas、Tries、priced Plans、ResourcePools、ordered Queues、Experiment designs/closures、Runs、redacted Attempts、Evaluation protocols/results、Findings、Candidates、Releases、Promotions、Decisions。 | Live provider freshness、local checkout location、config trust、raw telemetry、artifact bytes、Git integration 或 daemon coordination。 | 驗證 exact schema/revision/path/graph rules；在 experiment clone Git-common lock 下 mutation；推導 reverse link 與 projection。 |
| Canonical `Source` | Project-local Git Source ID/key、immutable `subdir`、sanitized locator history、active/retired state。 | Local path、current checkout bytes，或執行該 checkout config 的權限。 | 作為一般 record 解析；每個 Attempt v3 snapshot 與 Candidate v2 source 都反向連到它。 |
| XDG association store | Project UUID 到 canonical clone，以及 `(Project, Source)` 到 clone 的 host-local mapping，包含 Git-common filesystem identity 與 observed sanitized locators。 | Project/Source identity 或 scientific meaning。 | 每次使用都重新 discover/revalidate。Missing/stale/ambiguous mapping 會失敗或要求 explicit selector；canonical record 不會複製到 Source repository。 |
| Layered config 與 XDG trust store | Non-secret preference，加上依 Project/Source/config path/Git-common filesystem identity scoped 的 exact-digest capability approval。 | Canonical authority、另一個 Project/Source，或 file/clone identity 改變後的 approval。 | Authority 確定後才解析；依規則 merge；回報 leaf provenance；untrusted execution-bearing winner 會 fail closed。 |
| Source Git 與 native Git worktree | Commit、tree、branch、changed path、checkout registration、integration history。 | Outcome 是否支持 hypothesis 或通過 Evaluation。 | Capture full object ID 與 exact path；native Git verify/inspect/cleanup managed worktree。絕不 automatic merge/push。 |
| Pueue | Live task/group state 與 scheduler task identity。 | Scientific verdict、evidence inclusion 或 worker result。 | 只提交 audited worker argv；reconcile bounded observation；explicit cancel 要求 canonical ownership 與 live group/label 相符。 |
| MLflow | Workload-created run、metric、tag、parameter、trace、artifact、registry state。 | Evaluation outcome、沒有 ownership tag 的 canonical Attempt ownership、Candidate eligibility 或 Promotion。 | 只讀 requested field；只保留 sanitized verified ExternalRef。Artifact URI/bytes 仍由 provider 擁有。 |
| `<experiment-git-common-dir>/exp/runtime/v1/control.sqlite` 的 private SQLite | Lease、fencing token、job、submission outbox、fairness、pause state、provider observation、bounded event。 | Hypothesis、conclusion、evidence disposition、Finding、Evaluation、Release、Promotion。 | 協調與 recovery local work。它是 rebuildable operational state，不是 scientific authority。 |
| Worker terminal/result files | Durable exact process observation、frozen bounded result、output hash、optional verified MLflow observation。 | Scientific validity 或 Attempt/Evaluation 的替代品。 | 驗證 identity/fencing/digest，盡可能修復 SQLite，並透過 canonical CAS 匯入 selected fact。 |
| Dirty Source seed bundle | 重現單一 dirty Try snapshot 所需的 private bounded bytes。 | Canonical evidence、Candidate，或刪除 changed work 的權限。 | 依 digest authenticate，只 seed managed Try worktree，並只在 exact seeded cleanup 後刪除。 |
| Workspace provider registry | Invocation-local requested/actual provider 與 readiness/capability report。 | Source identity、canonical checkout proof 或 provider-local lifecycle authority。 | Native Git 是 mandatory authority。`dev_cli` 因沒有 exact machine receipt，目前所有 lifecycle capability fail closed；trusted fallback 可選 native Git。 |
| Generated page/manifest 與 `exp ui` | 由 canonical records 加 bounded local observations 產生的 deterministic presentation。 | 新事實、relationship ownership、mutation approval，或超過 observation time 的 freshness。 | 只能 regenerate/refresh。TUI 以 read-only 開啟 SQLite，沒有 mutation、login、install、service-start、editor 或 handoff action。 |
| Public Notes、TODO、Backlog、Pitfall、Invariant | 各 repository 定義的 prose purpose。 | 除非經 explicit domain operation promotion，否則不是 canonical research relationship。 | Link canonical ID，不同步 mutable copy。 |

建議預設使用 dedicated private experiment repository。Embedded/monorepo Project 仍支援 shared
governance 與 backward compatibility。Git submodule 只是 checkout convenience，不能取代 Source
或 association authority。詳見[Architecture](../design/architecture.md#recommended-repository-arrangement)。

## Canonical relationship ownership

Relationship 只存一次，位於 declared owner：

| Relationship | Canonical owner |
|---|---|
| Source 定義 Git meaning 與 subdirectory | Source |
| Try 宣告 Source set | Try |
| Try adopts Idea／Idea originates from Try | Try 與 Idea v2，以 atomic reciprocal edges 寫入 |
| Idea descends、merges 或 qualifies to Plan | Child/merged/qualified Idea |
| Plan assumes Finding、pins dependencies/resources、results in Experiment | Plan |
| Queue 在 ResourcePool/lane 中排序 Plan | Queue |
| Run 屬於 Experiment | Run |
| Attempt 屬於 Run 或 Try、retries Attempt、captures Sources | Attempt（Try/retry/Source 使用 v3） |
| Experiment includes/excludes Run 作為 conclusion evidence | Experiment conclusion |
| Evaluation 使用 spec/evaluates subject；formal Evaluation binds Attempt | Evaluation（v2 的 Attempt field） |
| Finding cites evidence 並 weakens/overturns Finding | New Finding |
| Candidate packages Experiment/Evaluation；v2 pins Attempt/Sources | Candidate |
| Release fills named slot 並 cites combination evidence | Release |
| Promotion 延續 target chain | New Promotion |
| Decision supersedes Decision | New Decision |

Inventory scan 計算 reverse relations，並 enforcement reciprocal Try/Idea、Run/Attempt location、
evaluation provenance、Candidate snapshot equality 與 Promotion-chain rules。不要為 navigation 加
inverse field；應 regenerate projection。完整 schema list 請見
[Record Format](../design/record-format.md#relationship-ownership-summary)。

## Try 與 formal authority

Try 是 canonical exploratory history，但不是 promotion-bearing evidence。Direct Try 建立由 Try
擁有的 Attempt v3，可帶 explicit bounded dirty SourceSnapshot。其 terminal result digests 可由人類
選入 conclusion，再把 conclusion adopt 成 Idea v2；但不能成為 Candidate。

Formal execution 建立 Run-owned Attempt。Runtime v2 對 writable execution Source 與每個 declared
read-only Source capture clean SourceSnapshot。能進入 promotion 的 chain 要求：

1. successful terminal formal Attempt v3，且只有 clean snapshots；
2. 其 Run 被 closed、concluded、supported Experiment included；
3. 該 Experiment 的 Evaluation v2 明確綁定同一 Attempt；
4. Candidate v2 的 Source IDs、head commits 與 path sets 精確等於該 Attempt snapshots。

Dirty Try 必須透過此 chain clean rerun。Process success、Git commit、output hash、MLflow
`FINISHED` 或 artifact URI 單獨都不滿足 scientific gate。

## 跨越 authority boundary

ExternalRef 是 bridge，不是 authority transfer。它可以保存 sanitized role、provider、context、
native kind/ID、query-free URI、observation time 與 provider-namespaced bounded metadata；絕不
攜帶 credential、raw environment、local checkout path、unbounded provider output 或 artifact
bytes，也不宣稱仍然 fresh。

Explicit formal worker authority flow 同樣是單向：

1. Pueue 為 worker-job v2 呼叫 hidden worker argv，其中包含 canonical root、Project UUID 與
   checkout-local scope。
2. Worker 重新 discover 該 exact Project，並在讀 payload bytes 前先檢查 metadata-only job
   authority/fencing。
3. Payload 必須符合 Project/scope/Attempt/execution Source 與所有 checkout/snapshot bindings；
   non-execution Sources 必須 read-only。
4. Native Git 在 spawn 前立即 revalidate snapshots，process success 後再驗證 read-only Sources。
5. Durable marker 只有經 validated revision-checked import，才能推進 canonical Attempt state。

Legacy worker-job v1 保留為 already-queued embedded runtime work 的獨立 exact path。Direct Try
worker-job v2 不能透過 hidden worker command 啟動；必須由 `exp try resume` 先重建
Source/config/worktree authority。

## Routing examples

| Statement | 正確 owner |
|---|---|
| 「此 repository/subdirectory 是 governed input。」 | Canonical Source；local clone path 另存 association |
| 「正式化前先試這個 bounded change。」 | Try 與 Try-owned Attempt；有價值時 adopt 成 Idea |
| 「此 comparable evidence 支持 scoped claim。」 | 連至 included Run evidence 的 Finding |
| 「Pueue 回報 task 42 running。」 | Pueue；可選擇將 bounded observation reconcile 至 Attempt |
| 「MLflow 在此 URI 有 artifact。」 | MLflow；最多保存 sanitized ExternalRef，Git 絕非 artifact authority |
| 「此 clean formal source state 通過 registered protocol。」 | 通過所有 lineage gate 後的 Evaluation v2 與 Candidate v2 |
| 「Merge 並 push experiment branch。」 | Human Git integration action；不是 automatic `exp` operation |
| 「停止方向並重新分配 budget。」 | Based on Findings 的 Decision；concrete follow-up 也可成 TODO |

同一 statement 服務兩種 audience 時，將 fact 保留於其 authority，其他位置只建立 link。不要同步
兩份 mutable copy。
