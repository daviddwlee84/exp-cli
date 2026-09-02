# MLflow

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

`exp` 將 MLflow 視為唯讀遙測來源 (read-only telemetry source)。Workload 自行建立
並記錄其 MLflow run；`exp` 可以驗證單一明確 run 的指定欄位，並在通過更嚴格的
lineage 檢查後，將已清理的參照附加至不可變的 Evaluation。

MLflow 仍是 runs、metrics、tags、parameters、artifacts 與 registry state 的權威來源。
Evaluation 則是規範性研究陳述 (canonical research statement)。

## 已實作功能

目前的 boundary 有兩個入口：

- `exp provider mlflow verify` 檢查單一 workload-owned run 上的精確 metrics 與
  expected tags。
- `exp evaluation create --mlflow-run-id ...` 重新執行驗證，且只有 canonical Attempt
  ownership 與 subject lineage 相符時才附加該 run。

此整合**不會**：

- 建立、啟動、終止或刪除 MLflow run；
- 記錄或更新 metrics、parameters、tags 或 artifacts；
- 註冊、轉換 stage、設定 alias 或刪除 model；
- 安裝 MLflow、隱式進入 Python environment，或開啟 authentication；
- 將 run success 轉換成科學結論。

`mlflow` executable 必須已存在於 `PATH`。Adapter 會從 project repository 執行
`mlflow runs describe --run-id RUN_ID`，並套用有大小限制、預設拒絕
(deny-by-default) 的 environment。

## 驗證 workload-owned run

每個 metric 都必須使用精確名稱要求；每個 tag 都必須寫成精確的 `NAME=VALUE`
assertion：

```bash
RUN_ID='0123456789abcdef0123456789abcdef'
ATTEMPT_ID='att_01a01e61-0000-7031-8000-000000000031'

exp provider mlflow verify \
  --run-id "$RUN_ID" \
  --metric macro_f1 \
  --metric validation_loss \
  --tag "exp.attempt_id=$ATTEMPT_ID" \
  --json
```

至少需要一個 `--metric` 或 `--tag`。只有下列條件全部成立，驗證才會成功：

- MLflow 回傳的 run ID 與要求的 ID 相同；
- run status 精確等於 `FINISHED`；
- 每個 requested metric 都存在；
- 每個 expected tag 都存在，且其值完全相符。

缺少 metric、缺少或不相符的 tag、不同的 run ID，以及任何非 `FINISHED` status，
都會產生 diagnostics 並使 command 失敗。驗證不會判斷 metric 在科學上是好是壞。

### 指定欄位與遮蔽邊界

只有 requested metric names 與 expected tag names 會跨越 adapter boundary。
未要求的 metrics 與 tags、所有 parameters，以及其他 raw MLflow fields 都會被丟棄。
Result 也包含有大小限制的 run metadata：run ID、experiment ID、status、verification
diagnostics，以及可安全保留時的已清理 artifact URI。

URI userinfo 會被移除；看似 credential 的 query data 會被移除或遮蔽；不安全或無法解析
的 artifact URI 會被省略並留下 diagnostic。需要穩定的 `exp.cli/v1` response envelope
時請使用 `--json`，不要 scrape 人類閱讀用 summary。

### Environment 與 credentials

MLflow subprocess 預設只繼承一小組 portable baseline。使用 `--allow-env` 明確加入
非秘密 configuration names；使用 `--secret-env` 從 parent environment 綁定必要
credentials：

```bash
export MLFLOW_TRACKING_URI='https://mlflow.example.test'
export MLFLOW_TRACKING_TOKEN='set-outside-shell-history'

exp provider mlflow verify \
  --run-id "$RUN_ID" \
  --metric macro_f1 \
  --allow-env MLFLOW_TRACKING_URI \
  --secret-env MLFLOW_TRACKING_TOKEN
```

`--allow-env NAME` 只用於額外的非秘密 variables。`--secret-env NAME` 要求 parent
process 中存在相同名稱的 variable；其值只會綁定至 MLflow subprocess，且不會出現在
rendered command 或 environment metadata。缺少必要 secret 時，系統會在 MLflow
執行前失敗。

## 將 run 附加至 Evaluation

附加 MLflow telemetry 是 `evaluation create` 的一部分，而不是對既有 Evaluation
進行另一項 mutation：

```bash
exp evaluation create \
  --title "Validation result" \
  --spec "$EVALUATION_SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --outcome passed \
  --metric 'macro_f1=0.913:score' \
  --summary "Passed the registered threshold" \
  --mlflow-run-id "$RUN_ID" \
  --mlflow-context local \
  --mlflow-tag "exp.attempt_id=$ATTEMPT_ID" \
  --allow-env MLFLOW_TRACKING_URI \
  --secret-env MLFLOW_TRACKING_TOKEN
```

`--mlflow-context` 是非秘密的 provider context name，預設值為 `default`。可以加入其他
`--mlflow-tag NAME=VALUE` assertions；建立 Evaluation 時若 tag name 重複會被拒絕。

### Ownership 與 lineage 檢查

每個 attachment 都必須包含：

```text
--mlflow-tag exp.attempt_id=<canonical-attempt-id>
```

指定的 Attempt 必須存在於目前 project、是成功的 terminal execution，並指向一筆
canonical Run。該 Run 的 Experiment 必須屬於 Evaluation subject：

- Experiment subject 必須就是同一個 Experiment；
- Candidate subject 必須參照該 Experiment；
- Release subject 必須透過 combination evidence 或受支援的 single-slot lineage
  包含該 Experiment。

來自其他 Experiment 的 run、未知 Attempt，或非成功的 Attempt 都不能附加。如果該
Evaluation 後續用來建立 Candidate，記錄的 MLflow owner Attempt 也必須等於該 Candidate
的 successful backing Attempt。

### 精確 metric 比對

進行 attachment 時，`exp` 會要求 Evaluation 的每個 `--metric NAME=VALUE:UNIT`
argument 所指定的 metric。每個 supplied numeric value 都必須與 MLflow 回傳值完全相等；
沒有 rounding 或 tolerance。Units 與 thresholds 由 EvaluationSpec 驗證，MLflow 只提供
numeric telemetry value。

只有 run verification、ownership、lineage 與 exact metric checks 全部通過後，`exp`
才會建立不可變的 Evaluation。其 external reference 會記錄已清理的 MLflow identity、
observation time、verified status、experiment ID、owner Attempt 與 owner subject。
Verification 本身絕不會建立 Evaluation、Finding、Candidate、Release 或 Promotion。

完整的科學流程請參閱 [從證據到 Promotion](../workflows/evidence-to-promotion.md)；共通
安全模型請參閱 [Provider 契約](../design/provider-contract.md)。

## 未來可探索主題

以下是預留的文件位置，並非目前支援的操作：

- named tracking-server profiles 與 context-specific authentication guidance；
- proxy 與 direct artifact-access topology 及其 credential boundaries；
- read-only history comparisons 與更完整的 metric diagnostics；
- sweeps、trials 與 nested runs 的 workload conventions；
- 經獨立審查的 read-only model-registry capability。
