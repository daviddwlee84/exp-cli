# 從證據到上線

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

## 結束 Experiment

Closure request 會列出每個 Run，以及其證據被納入或排除。它在一個原子交易
(atomic transaction) 中記錄 scientific outcome、Findings 與 follow-up Ideas：

```bash
exp experiment close --input closure.json --json
```

Operationally successful Attempt 不會自動成為 included evidence。可比較性、protocol
compliance 與已登記的 decision rule 仍決定科學結論。

## 建立不可變的 Evaluations

先為可比較 protocol 建立 EvaluationSpec，再評估 subject：

```bash
exp evaluation spec create \
  --title "Scientific validation" \
  --purpose scientific \
  --dataset validation-v3 \
  --protocol "fixed preprocessing and five seeds" \
  --metric macro_f1:score:max:0.82 \
  --pool POOL --budget-hours 4

exp evaluation create --spec SPEC --subject SUBJECT \
  --outcome passed --metric macro_f1=0.834:score
```

MLflow 可以提供已驗證的 telemetry，但產生的 Evaluation 才是不可變的研究陳述。
參考 [MLflow 指南](../tools/mlflow.md)。

## Candidate 與 Release

Candidate 把通過的 scientific Evaluation 綁定到精確 Git code 與 backing successful
Attempt。Release 將已驗證 Candidates 組合成特定 target 的 named slots。多個
Candidates 需要獨立的 combination evidence，不能假設個別效益可以相加。

## 只能由人類進行的 Promotion

在使用有限 holdout budget 前先建立 sealed promotion specification。每個 promotion
Evaluation 都必須是新的，不能重複使用。

```bash
exp promotion spec-create \
  --title "Encoder production gate" \
  --target encoder-prod --evaluation-spec SPEC \
  --holdout-budget-hours 2

exp promotion append \
  --title "Promote encoder release" \
  --target encoder-prod --spec PROMOTION_SPEC \
  --challenger RELEASE --evaluation EVALUATION \
  --outcome accepted --approved-by HUMAN --confirm
```

任何 autonomy mode 都不能繞過 `--approved-by` 與 `--confirm`。Champion manifest
由 Promotion history 推導，不是第二個事實來源。
