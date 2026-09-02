# 為什麼需要 exp

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

機器學習研究通常不缺實驗執行器與 tracking dashboard，真正缺少的是一個可長期
保存的答案：**下一個該跑什麼、為什麼值得付出成本，以及什麼證據能改變決策？**

## 最初的痛點

| 痛點 | 缺少控制平面時會發生什麼 |
|---|---|
| Ideas 增加得比運算資源快 | 最吵或最新的需求先跑，而不是價值最高的工作。 |
| Dashboard 變成研究記憶 | 可變的 telemetry 被誤認為可長期引用的結論。 |
| Process success 被當成 scientific success | Job 顯示綠燈就被採用，卻沒有檢查可比較性或預先登記的決策規則。 |
| 負面結果消失 | 團隊沒有保存失敗假設與適用範圍，因此不斷重走 dead end。 |
| Agent 產生工作的速度超過人類審查能力 | 自動化只增加 queue 壓力，卻隱藏工作為何被准入。 |
| 程式碼、證據與決策脫節 | Metric 無法追溯到精確的 ChangeSet、Attempt 與 evaluation protocol。 |
| 正式環境 gate 與實驗自動化混在一起 | 能派送研究工作，被誤解成也有權將結果上線。 |

## 解法的形狀

`exp` 在 Idea 與 provider-owned execution 之間加入一層小而明確的控制：

1. 先記錄 Idea，不假裝它已準備好執行。
2. 將它評估成 Plan，寫明預期效益、不確定性、資源成本、假設與依賴。
3. 在 ResourcePool 與 exploit/explore lane 中排序精確的 Plan revision。
4. 在看到證據前先登記 Experiment design。
5. 派送精確的 executable、argument vector、Git base/head 與 ChangeSet。
6. 讓 Pueue 與 MLflow 繼續掌握自己的 live state，只把去敏感化後的 identity
   連回研究 records。
7. 以納入／排除的證據、Findings 與明確的 follow-up Ideas 結束工作。
8. Promotion 必須另有已封存的 holdout 與具名人類核准。

## 為什麼使用普通檔案

Canonical meaning 位於 `experiments/` 下可審查的 Markdown 與 TOML。研究記錄因此
可以 diff、branch、search，也不依賴常駐服務。SQLite 被刻意限制在可重建的
operational state，例如 leases、jobs、outbox entries 與 fairness counters。

## exp 不是什麼

`exp` 不是 training framework、hyperparameter optimizer、artifact store、cluster
scheduler 或 experiment-tracking server。它協調這些系統之間的研究意義，但不奪取
它們的權責。現有整合範圍請參考[工具](tools/index.md)。
