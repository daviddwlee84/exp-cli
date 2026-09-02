# 研究心法

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

控制平面只有建立在可靠研究方法上才有價值。以下原則定義 `exp` 對研究工作的
描述方式與解讀方式。

## 從決策與預期效益開始

實驗的正當性來自它能改善的決策，而不只是新奇。先寫明 metric 與 unit、合理的
效益、資訊價值 (information value)、資源成本、假設，以及什麼結果會讓方向成為
dead end。真正的探索應留在 `explore` lane，不要為了提高排名而偽裝成 exploitation。

## 在看到證據前登記設計

第一次產生證據的 Attempt 前，記錄可證偽假設 (falsifiable hypothesis)、baseline、
預計改動、可比較性要求、成功條件與決策規則。看完結果才改 threshold 或 baseline，
應該成為附日期的 amendment 或新的 Experiment，而不是被重寫的歷史。

## 只比較真正可比較的證據

Dataset identity、preprocessing、metrics、seeds、stopping rules、runtime limits、
hardware 與 dependency versions 都可能使比較失效。Run 是預定的證據單位；Attempt
是一次 operational execution。重試基礎設施故障不會產生新的科學問題。

## 區分負面、無定論與無效結果

- **被反駁 (Refuted)：**可比較的證據違反已登記假設。
- **無定論 (Inconclusive)：**有有效證據，但不足以回答問題。
- **無效 (Invalid)：**資料洩漏、protocol violation 或 comparability failure 使證據
  無法回答問題。
- **執行失敗 (Operational failure)：**process failed、timeout、preempted 或 OOM；
  這不是科學結論。

成功的 exit code 只證明 process 結束，不證明科學上有效。

## 保存 dead end 與適用範圍

記錄某方向為何不該重試，以及什麼條件改變後才能重新考慮。有證據支持的限制成為
Finding；反覆發生的執行問題與解法進入 project pitfalls；未來行動進入 TODO 或
backlog。不要把 anecdote 升格成 Finding，也不要把科學結果埋進 troubleshooting。

## 保留分支並測試組合

有用結果可能產生多個 child Ideas。每個分支都應獨立估價並保留 parent edges。
個別成功的 Candidates 不代表效果可相加；組合成 Release 前，需要自己的 combination
Experiment 與通過的 Evaluation。

## Promotion 必須由人類把關

Scientific Evaluation 判斷結果能否成為 Candidate；Promotion Evaluation 判斷完整
Release 是否應取代特定 target 的 incumbent。先封存 holdout protocol 與 budget；
不論實驗自動化程度多高，正式環境核准仍是具名人類行動。
