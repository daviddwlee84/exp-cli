# 核心研究流程

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

## 1. 記錄方向，但不要過早承諾資源

用 Idea 保存值得延續的研究方向。它可以由人提出，也可以來自先前證據的 child
branch，但此時還不代表承諾使用運算資源。

```bash
exp idea add --title TITLE --summary TEXT --lane exploit --cluster optimizer
```

## 2. 評估並估價工作

只有在能說明預期效益、資訊價值、假設、依賴與 ResourcePool-hours 後，才把 Idea
轉成 Plan。人類可用 `idea qualify`；設定好的 Agent 可用 `idea develop` 提出完整
Plan，並在 schema validation 後選擇套用。

```bash
exp idea qualify IDEA --resource POOL:UNITS:HOURS [payoff flags]
exp idea develop IDEA --json
exp idea develop IDEA --apply --json
```

## 3. 排序精確的 revision

```bash
exp queue insert QUEUE PLAN --pool POOL
exp queue insert QUEUE PLAN --pool POOL --agent
```

數值路徑會產生穩定且透明的 score。`--agent` 會加入 listwise advice 與交換順序的
adjacent battles。若結果不一致或信心不足，Queue 保持不變並交給人類審查。Plan
或會改變 belief 的 Finding 更新後，既有 pins 會 stale；請 refresh、重新評估並再次
insert。

## 4. 登記並執行證據單位

Experiment 記錄 hypothesis、baseline、comparability specification、成功條件與
decision rule。Runs 描述預定證據；Attempts 記錄每次 operational execution。
啟用 daemon dispatch 前，先設定精確的 runtime boundary。

## 5. 以明確的證據 disposition 結束

`exp experiment close --input PATH` 會原子性記錄哪些 Runs 被納入或排除、完成 Plan，
並發布有範圍的 Findings。只要保存原因與適用範圍，無效或負面的結果仍然有價值。

## 6. 建立分支，不要重寫歷史

後續問題應建立 child Ideas。若要組合多個 Candidates，請登記 combination Experiment，
不要假設個別量到的效益可以相加。參考[從證據到上線](evidence-to-promotion.md)。
