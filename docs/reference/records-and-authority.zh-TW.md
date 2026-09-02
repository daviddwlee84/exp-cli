# 記錄與權威

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

每一項事實只應有一個 owner。`exp` 將 scientific meaning 連至 code 與
provider-owned execution，但不會把每個系統都複製進同一個 database。Reverse
relationship 與 summary 會由 owner 推導，而不是另存成互相競爭的事實。

## Authority map

| Store 或系統 | 擁有的內容 | 無法建立的內容 | `exp` 的使用方式 |
|---|---|---|---|
| `<git-root>/experiments/` 下的 canonical record | Research policy、Idea、priced Plan、ResourcePool、ordered Queue、Experiment design/closure、intended Run、redacted Attempt、Evaluation protocol/result、Finding、Candidate、Release、Promotion 與 Decision。 | Live provider freshness、raw telemetry、code integration state 或 hidden daemon coordination。 | 驗證精確 schema/revision；透過 domain command 或 prepared transaction 修改；推導 reverse link 與 projection。 |
| Upstream provider | Provider-native live fact：Pueue task/group state；MLflow run telemetry、parameter、metric、trace、artifact 與 registry state；未來的 DVC、Slurm、Study 或 notebook state。 | Scientific verdict、canonical relationship、production Promotion 或目前 Git integration。 | 只讀取已實作的 bounded capability、消毒 observation，並在 canonical record 明確需要時保留 ExternalRef 或 selected fact。 |
| Git | Commit、tree、branch、linked worktree、ChangeSet 與 integration history。 | Process result 是否支持 hypothesis 或通過 Evaluation。 | 固定完整 base/head object ID 與 exact changed path。Agent 可建立 experiment commit；merge 與 cleanup 由人類擁有。 |
| 位於 `<git-common-dir>/exp/runtime/v1/control.sqlite` 的 private SQLite | Daemon lease、fencing token、job、submission outbox、fairness counter、pause state、provider observation 與 bounded event history。 | Hypothesis、conclusion、evidence disposition、Finding、Evaluation、Release 或 Promotion。 | 在 linked worktree 間協調與復原 local execution。絕不把 database row 視為 scientific authority。 |
| Generated view | 從 canonical record 推導的 deterministic `README.md`、`ROADMAP.md`、`LEDGER.md`、`DECISIONS.md`、Champion manifest 與其他 projection。 | 新事實或 relationship ownership。 | 使用 `exp render` 重新產生；用 `exp render --check` 檢查 drift。絕不讀回 projection 來重建 canonical state。 |
| 公開 Notes | Dated exploratory question、bounded observation、comparison 與 link。 | Canonical truth、live provider state 或 implementation commitment。 | 作為 non-canonical staging/learning layer；將 durable outcome 交給正確 owner。 |
| TODO | 某人可以完成的具體 action。 | 此 action 具 scientific justification 的 evidence，或 priced Queue entry。 | 連至相關 Idea、Plan、Finding、Decision、issue 或 code location，不複製其中可能變動的事實。 |
| Backlog | 尚不值得 canonical qualification 的粗略 question 或 investigation。 | Durable research lineage、priced resource、priority 或 Queue order。 | 當 origin、classification、cluster 與 lineage 變得重要時提升為 Idea；只有在 payoff/resource cost 明確後才 qualification 為 Plan。 |
| Pitfall | 值得再次查找的 recurring operational symptom、cause、diagnostic 與 remedy。 | 完整 Attempt history 或 general scientific conclusion。 | 連至相關 Attempt、provider issue 或 Finding；在此保存可重用的 troubleshooting lesson。 |
| Invariant | 未來 design/implementation 必須遵守的 durable constraint，例如 privacy 或 leakage prohibition。 | 從單一 narrow run 推定的結果，或會自動執行的 validator。 | 從 design、test 與 Decision 引用；需要時另行實作 enforcement。 |

Repository 的 project-knowledge convention 仍是 TODO、Backlog、Pitfall 與
Invariant storage 的 authority。`exp` 不得在未經明確 domain operation 的情況下，
暗中建立 competing store 或把這些項目轉成 canonical record。

## Canonical relationship ownership

一項 relationship 只儲存一次，並由宣告的 owner 持有。例如：

| Relationship | Canonical owner |
|---|---|
| Idea qualification 為 Plan | Idea |
| Plan 產生 Experiment | Plan |
| Queue 在 ResourcePool/lane 中排序 Plan | Queue |
| Run 屬於 Experiment | Run |
| Attempt 執行 Run | Attempt |
| Experiment 將 Run 納入或排除為 conclusion evidence | Experiment conclusion |
| Finding 削弱或推翻 Finding | New Finding |
| Candidate 封裝 Experiment、Evaluation、Git commit 與 ChangeSet | Candidate |
| Release 用 Candidate 填入 named slot | Release |
| Promotion 延續 target decision chain | New Promotion |
| Decision supersede Decision | New Decision |

Inventory scan 會計算 reverse relation。不要只為方便 navigation 就新增 inverse
field；應新增或重新產生 projection。完整 schema-level 清單請見
[記錄格式與 schema 版本](../design/record-format.md#relationship-ownership-summary)。

## 跨越 authority boundary

ExternalRef 是橋接，不代表 authority transfer。它可以保留已消毒的 provider、
context、native kind、native ID、optional query-free URI 與 observation time。它
不會複製 credential、raw environment、unbounded provider output 或 artifact byte，
也不宣稱 provider state 仍然 fresh。

Provider 或 worker observation 只有在通過明確 validation 與 canonical transaction
後，才能推進 canonical Attempt 的 operational state。即便如此：

- process 或 scheduler success 是 operational fact，不是 scientific verdict；
- MLflow run 為 `FINISHED` 且符合 requested tag，只代表 verification，不是
  Evaluation outcome；
- Git commit 是精確 code identity，本身不是 evidence；
- Agent recommendation 只是 advisory，除非 domain command 驗證並記錄結果；
- 只有具名人類能附加 production Promotion。

## Routing example

| 陳述 | 正確 owner |
|---|---|
| 「下週嘗試更大的 context window。」 | 在完成 qualification 與估價前，屬於 TODO 或 Backlog。 |
| 「這組 comparable evidence 支持此 scoped claim。」 | 連至 registered evidence 的 Finding。 |
| 「Pueue 回報 task 42 正在執行。」 | Pueue；可選擇將 bounded observation reconcile 至 Attempt。 |
| 「Commit `abc…` 包含已審閱的 implementation。」 | Git；Candidate 另外需要 supported evidence 與 passing Evaluation。 |
| 「Allocator fragmentation 反覆造成此 launch shape 失敗；請使用這項設定。」 | Pitfall，並連至相關 Attempt 或 Finding。 |
| 「Evaluation data 絕不可進入 training-time feature selection。」 | Invariant，並由 design/test 引用。 |
| 「停止此方向並重新配置 budget。」 | 以 Finding 為基礎的 Decision；具體 follow-up 也可另外成為 TODO。 |

當同一 statement 服務兩種 audience 時，請將事實保留在其 authority 中，並從另一個
store 建立連結。不要同步兩份可變動副本。

相關 workflow 請見[Architecture](../design/architecture.md)、
[Transactions](../design/transactions.md)與[研究筆記](../notes/index.md)。
