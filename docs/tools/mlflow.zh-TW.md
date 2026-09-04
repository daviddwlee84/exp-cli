# MLflow

MLflow 仍是 workload-created run、metric、parameter、tag、trace、artifact location/bytes 與 registry
state 的 authority。`exp` 是 bounded read-only observer：絕不 create/mutate run、log telemetry、
download artifact、改 registry state，或把 run status 轉成 scientific verdict。

Canonical authorities 彼此分離：

- Attempt 記錄 redacted operational execution；只有 verification 後才可帶 sanitized MLflow
  ExternalRef；
- Evaluation 在 EvaluationSpec 下記錄 scientific metric outcome；
- Candidate/Release/Promotion enforcement 各自 typed evidence gates；
- artifact URI 是 provider identity，不是 artifact-byte/deployment authority。

## 已交付 entry points

| Entry point | MLflow 缺少或 assertion 失敗時的行為 |
|---|---|
| `exp provider mlflow verify` | Strict command failure；不建立 canonical record。 |
| `exp evaluation create --mlflow-run-id ...` | Immutable Evaluation 建立前 strict verification/lineage/metric failure。 |
| Direct Try 或 formal worker 選 profile，且 workload result 含 `mlflow_run_id` | Optional observation 變成 `unavailable`／`unverified`；successful process state 不變。只有 `verified` ownership 會匯入。 |

全部使用 installed `mlflow` binary 的 read-only `runs describe --run-id RUN_ID`。Binary 必須已可從
`PATH` 解析；`exp` 不 install MLflow、不進入 Python environment、不 start tracking server、不
interactive authenticate，也不 invocation shell。

## Named profiles

MLflow profile 已由 layered `exp.config/v1` 提供。Named profile 會由 higher-precedence config
atomically replace，不含 endpoint/credential value；只含 non-secret context、binary name、timeout、
environment **names 與 policy**，以及 default metric names。

```toml
schema = "exp.config/v1"

[defaults]
mlflow_profile = "observer"

[mlflow.profiles.observer]
context = "research"
binary = "mlflow"
timeout = "30s"
default_metrics = ["macro_f1", "validation_loss"]

[mlflow.profiles.observer.env.MLFLOW_TRACKING_URI]
from = "MLFLOW_TRACKING_URI"
secret = false
required = true

[mlflow.profiles.observer.env.MLFLOW_TRACKING_TOKEN]
from = "MLFLOW_TRACKING_TOKEN"
secret = true
required = true
```

Map key 與 `from` value 都是 variable name。其 value 只在 child start 前立即從 parent resolve，
絕不放入 config、worker output、record 或 receipt，且全部註冊供 redaction。Required variable 缺少
時，在 child process 前失敗。Serialized workload profile 禁止以 `EXP_` 開頭的 child name。

Profile precedence 先看 explicit root flag：

```bash
exp --mlflow-profile observer provider mlflow verify \
  --run-id <WORKLOAD_RUN_ID> \
  --tag 'exp.attempt_id=<ATTEMPT_ID>' \
  --json
```

沒有 flag 時由 `defaults.mlflow_profile` 提供。若 winning profile definition 來自 experiment/Source/
subdirectory repository file，即使 explicit name 仍要求 exact trust。Repository-selected default
同時要求 selector/profile definition trusted。應使用 `exp config explain`／`exp config trust`，不要把
value 複製到 command。

為 backward compatibility，`--mlflow-context`、`--allow-env`、`--secret-env` 建立 invocation-local
`compatibility` profile，binary 為 `mlflow`、timeout 30 秒。Named `--mlflow-profile` 不能與這些
flags 混用。`--allow-env NAME` 是 optional non-secret inheritance；`--secret-env NAME` 是從同名
parent variable 取得的 required secret inheritance；兩者都不接受 value。

## Explicit run verification

提供一個 explicit run ID，以及至少一個 effective assertion：exact `--metric`、exact
`--tag NAME=VALUE`，或 selected profile 的 default metric。

```bash
exp --mlflow-profile observer provider mlflow verify \
  --run-id <WORKLOAD_RUN_ID> \
  --metric macro_f1 \
  --tag 'exp.attempt_id=<ATTEMPT_ID>' \
  --json
```

只有下列條件全部成立才成功：

- returned run ID 完全等於 requested ID；
- status 完全等於 `FINISHED`；
- 每個 requested metric 存在；
- 每個 expected tag 存在且等於 supplied string。

Missing/mismatched assertion、其他 run ID、其他 status 都產生 sorted diagnostics 與 command
failure。`exp` 不判斷 metric value 是否 scientifically desirable。

### Selected-field boundary

只有 requested metric names/expected tag names 會跨 adapter boundary。Unrequested metrics/tags、
所有 parameters 與 unrelated raw output 都丟棄。Bounded result 可含 safe run ID、experiment ID、
status、diagnostics，以及 sanitized artifact URI。Canonical URI sanitization 會移除 userinfo 與完整
query component，拒絕 `file:`、unsafe host/path/fragment；無法安全保留時帶 diagnostic 省略 URI。
Safe routing fragment 可保留。系統不會沿 URI 讀取或下載 artifact。

Subprocess 只收到 deny-by-default minimal environment 加 profile late-bound names。Output 有 bounds、
只解析一個 JSON value，且不視為超過此 read-only operation 的 provider capability。

## Optional worker attachment

選擇 profile 的 workload 仍自行 create/log run。它可以在 assigned `EXP_RESULT_PATH` 的 bounded
JSON 中放 safe `mlflow_run_id` string。Process complete 後，worker 以 profile default metrics 加
required ownership tag 執行一次 read-only observation：

```text
exp.attempt_id = <the exact canonical Attempt ID>
```

Attachment state：

- 只有 run ID/status/default metrics 與 exact ownership tag 都 verified 時為 `verified`；
- syntactically invalid identity 或 assertion mismatch 為 `unverified`；
- invalid profile environment、provider/binary absence 或 invocation failure 為 `unavailable`。

Observation failure 不會把 otherwise successful workload 變成 failure。只有 valid `verified`
attachment 會轉成 Attempt ExternalRef。該 reference 記錄 selected profile/context、run/status/
experiment identity、selected metric values、`mlflow.owner_attempt`、observation time 與 sanitized
artifact URI；不含 profile value 或 artifact bytes。Replay durable worker marker 只在 reference
byte-equivalent 時匯入；conflicting run identity 會 fail closed。

### Direct Try 與 formal Pueue runtime

Direct Try 在本機執行，可使用帶 environment binding 的 profile。Selected profile name/context
會 pin 進 Attempt direct policy；retry 要求相同 effective config digest/profile identity。

Formal runtime v2 可選 value-free profile（context、binary、timeout、default metrics），但因 Pueue
會保存 task environment，任何 profile environment binding 都會被拒絕。需要 credential 的 formal
workload 必須在 start 後透過自行 reviewed broker 取得。Runtime v1 沒有 integrated named-profile
attachment route。

兩種 worker path 的 provider observation 都是 optional。Candidate v2 不要求 MLflow；但若其
Evaluation 或 backing Attempt 帶 MLflow owner claim，就必須指向 exact same formal Attempt。

## 將 MLflow 附加至 Evaluation

MLflow attachment 是 immutable Evaluation creation 的一部分，不是 later update。目前 formal
Candidate v2 path 應明確綁定 successful formal Attempt，讓 command 建立 Evaluation v2：

```bash
exp --mlflow-profile observer evaluation create \
  --title "Registered validation result" \
  --spec <EVALUATION_SPEC_ID> \
  --subject <EXPERIMENT_ID> \
  --attempt <FORMAL_ATTEMPT_ID> \
  --outcome passed \
  --metric 'macro_f1=0.913:score' \
  --metric 'validation_loss=0.204:loss' \
  --summary "Passed the sealed protocol" \
  --mlflow-run-id <WORKLOAD_RUN_ID> \
  --mlflow-tag 'exp.attempt_id=<FORMAL_ATTEMPT_ID>'
```

Command 要求：

1. ownership tag 可 parse 成此 Project 的 Attempt；
2. successful terminal Attempt 且有 canonical Run；
3. 該 Run Experiment 位於 Evaluation subject lineage；
4. 有 `--attempt` 時，subject 必須是 Experiment，Attempt 必須是該 Experiment 的 successful formal
   Attempt v3；
5. explicit Attempt 與 MLflow owner 相同；
6. 每個 supplied Evaluation numeric metric 與 MLflow 完全相等，沒有 tolerance；
7. name/unit/threshold-derived outcome 符合 EvaluationSpec。

Experiment subject 直接對應；Candidate subject 使用其 Experiment；Release subject 有設定時使用
combination Experiment，否則使用 slot Candidate lineage。但 Evaluation v2 本身只接受 Experiment
subject。省略 `--attempt` 會保留 Evaluation v1，即使 MLflow metadata 有 owner；此 record 無法
滿足 Candidate v2 typed Attempt gate。

全部通過後才 publish Evaluation transaction。MLflow verification 單獨不會建立 Finding、Candidate、
Release、Champion 或 Promotion。

## Artifact 與 promotion boundary

Verified artifact URI 是 navigation/provenance，不證明 bytes present、immutable、safe 或
production-ready。`exp` 不 hash、copy、cache、compare、register、alias、promote、delete 或 serve
MLflow artifact/model。Candidate v2 authority 來自 clean SourceSnapshots 加 typed Evaluation；
Promotion 來自 sealed holdout 與具名 human approval。MLflow state 不會觸發 automatic deployment/
rollback。

## 目前限制

- 不 create/log run，也不 mutation artifact/model registry。
- 沒有 artifact-byte store 或 automatic artifact download。
- 除 sanitized run observation 外，沒有 read-only registry capability。
- Formal Pueue runtime 不接受 environment-bound MLflow profile。
- 不把 sweep/trial/nested-run 解讀為 canonical Run/Attempt。
