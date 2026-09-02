# 研究筆記

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

Notes 是本文件網站中公開、帶日期且用於探索的層次。當問題、工具行為、
比較結果或尚未完成的想法，還不足以成為規範研究記錄 (canonical research
record) 或已提交的設計決策時，可先記錄在這裡。

本區所有內容都會隨文件網站公開。請一律視為公開資訊。筆記是
**非規範資料 (non-canonical)**：它可以說明觀察到的內容或待調查事項，但不能
建立 Plan、Finding、Evaluation、Decision、provider state 或 production choice。

## 適合記錄的內容

- 對工具、provider version 或 integration surface 所做的 dated investigation；
- 仍需驗證或補充更廣泛 evidence 的 bounded observation；
- 尚未形成決策的 alternative、trade-off 與 question；
- upstream documentation、issue 與已消毒 provider identity 的連結；
- 之後可能轉成 Idea、TODO、backlog item 或 canonical record 的 follow-up topic。

請使用[工具探索](tool-explorations.md)作為 MLflow、Pueue、DVC、Optuna、Slurm
與 notebook 相關調查的長期索引。

## 發布與安全規則

| 規則 | 要求 |
|---|---|
| 公開 | 假設每一份已提交筆記都會顯示在 GitHub Pages。 |
| 帶日期 | 在檔名或標題中寫入觀察日期，並記錄最後審閱日期。 |
| 探索性 | 分開陳述 observation、hypothesis 與 open question；不得把推測當成目前行為。 |
| 有界 | 只引用支持筆記所需的小段內容，並盡可能連至 upstream source。 |
| 已消毒 | 只保留經審閱、非敏感的 identifier 與 summary。 |
| 非規範 | 連至權威記錄或系統；不可讓筆記成為同一事實的第二個 owner。 |

絕不可將下列內容提交至筆記：

- secret、credential、token、cookie、authorization header 或 private key；
- raw environment-variable map 或已解析的 secret value；
- unbounded log、trace、prompt、completion、stderr 或 provider response；
- artifact byte、model weight、dataset，或 implicit artifact download；
- host-specific private path，或含 userinfo／query secret 的 URI。

若某個 diagnostic 值得保存，請寫下有限且已消毒的摘要，並保留 upstream task、
run、commit 或 issue identity，讓讀者能在來源本身的 access control 下查閱。

## 建議的筆記格式

使用如 `2026-09-02-mlflow-artifact-reads.md` 的檔名。觀察日期與最後一次
編輯審閱日期應分開記錄。

```markdown
# 2026-09-02：簡短主題

- Status: exploring | validated | deferred
- Observed: 2026-09-02
- Last reviewed: 2026-09-02
- Context: tool version or named non-secret environment

## 問題

我們正在調查哪個決策或 integration boundary？

## 有界觀察

我們觀察到什麼？資料來自哪個公開或已消毒的來源？

## 影響

哪些事項可能改變？哪些仍未獲得證實？

## 後續行動

下一步要進行哪個具體 validation、Idea、TODO 或 design update？
```

## 將持久結果交給正確的 owner

| 結果 | 持久保存位置 |
|---|---|
| 需要 lineage 與 qualification 的研究方向 | canonical Idea |
| 已估價、可衡量且可進入優先排序的工作 | canonical Plan |
| 由已註冊 evidence 支持的 scoped belief | canonical Finding |
| 某人可完成的具體 action | project TODO |
| 尚未成熟到能成為 Idea 的粗略調查 | project backlog |
| 反覆出現的 symptom、cause 與 remedy | project pitfall |
| 未來工作必須遵守的 durable constraint | project invariant |
| Live task、run、artifact 或 registry state | upstream provider |
| Code history、branch state 與 integration | Git |

完整擁有權對照請見[記錄與權威](../reference/records-and-authority.md)。應優先
建立連結，而不是複製可能變動的 state。

## 相關參考資料

- [工具探索](tool-explorations.md)
- [設定與路徑](../reference/configuration.md)
- [Command Map](../reference/command-map.md)
- [記錄格式與 schema 版本](../design/record-format.md)
