# 臨時探索、儲存與結果查找

Try 適合畫圖、檢查資料與連續探索。每次 `try exec` 都建立獨立 Attempt，保留 Source
snapshot、輸入 digest、輸出目錄與實際 runner 身分。執行成功不會自動成為正式研究結論。

## 從 codebase 或純資料開始

已初始化的 workspace 可直接開始：

```bash
exp try start --title "檢查回應時間" --goal "尋找值得測試的模式"
```

尚無 codebase 時，建立獨立的小型 Git Source：

```bash
exp try root
exp try root /Volumes/research/tries
exp try start --scratch --title "檢查回應時間" --goal "尋找值得測試的模式" --json
```

預設根目錄是 `$XDG_DATA_HOME/exp/tries`，未設定時為 `~/.local/share/exp/tries`。
`records/` 保存 canonical research records，`sources/` 為每個新探索建立獨立的小型 Git repo。
Agent 在回傳的 `source_path` 撰寫分析程式；初始 README 會 local commit，不建立 remote 或 push。
變更 `try root` 不搬動既有探索，舊的 Project association 仍可搜尋。

在 Git repository 外執行裸 `try start` 也會使用 scratch 流程；未註冊的 codebase 請先用
`exp init` 或明確選擇 `--scratch`。跨 session 保留回傳的 Project／Try ID，不依賴全域 latest。

## 外部資料與連續執行

```bash
exp --workspace PROJECT input bind sample /Volumes/data/response-times.csv
exp --workspace PROJECT try exec TRY --input sample --dirty=capture -- python3 plot.py
```

本機路徑只寫入 private settings。Workload 透過 `EXP_INPUT_SAMPLE` 讀資料，透過
`EXP_OUTPUT_DIR` 存圖表；也能讀取 `EXP_TRY_ID`、`EXP_ATTEMPT_ID`、`EXP_PROJECT_ID`。
檔案與目錄輸入均支援，執行前後會核對 digest。目錄最多 4096 個 regular files，拒絕 symlink
與 special files；較大集合請封裝或縮小輸入範圍。

分析程式可先 commit 再 clean 執行，也可明確使用 `--dirty=capture` 捕捉 bounded dirty state。
改程式、參數或資料後再次 `try exec TRY`；每一步建立新的 Attempt 與輸出目錄。
`try retry` 則保留先前 Source／argv／input 身分。並行輸出不互相覆蓋；Direct Try 不預留
GPU／Pool 配額，需要共享算力治理時使用正式 Queue。

既有 `try run` 保留相容行為；加上 `--outputs`、`--storage`、`--runner` 或 `--input` 即啟用
managed outputs。`try exec` 一律啟用。

## 記住儲存偏好

設定在 `$XDG_CONFIG_HOME/exp/exploration.json`，未設定時為 `~/.config/exp/exploration.json`，
與 repository execution trust 分開。Project preference 覆蓋 global default，當次明確 profile
再覆蓋兩者。實際執行使用 frozen private receipt，日後改偏好不會改寫舊 Attempt 的位置。

```bash
exp storage add disk --kind local --root /Volumes/research/artifacts
exp storage use disk --global
exp --workspace PROJECT storage show
exp storage add local-tracking --kind mlflow-local --root /Volumes/research/tracking
exp --workspace PROJECT storage use local-tracking
exp storage add team --kind mlflow-remote --root /Volumes/research/staging \
  --tracking-uri https://tracking.example.org --token-env RESEARCH_TRACKING_TOKEN
```

內建 `local` profile 使用 `$XDG_DATA_HOME/exp/artifacts`。儲存位置必須在 Source／canonical
repo 外；Git 只保存 bounded manifest、描述與 digest，檔案以 content-addressed objects 保存。
清理 worktree 不會刪除這些 objects 或 artifact receipts。

MLflow writer 使用已安裝的 Python SDK，可用 `--python` 選擇 interpreter；exp 不自行安裝
套件；SQLite 需要完整 mlflow package，mlflow-skinny 不包含此 backend。`mlflow-local` 的 `mlflow.db` 使用 SQLite 保存 tracking metadata，`mlflow-artifacts/`
保存檔案，SDK 本機操作不需要長駐 server。MLflow UI 可以稍後連接同一個 DB。
`mlflow-remote` 使用 server 的 artifact store；遠端 object storage 使用具 artifact proxy 的
server。Credential 在呼叫前由指定環境變數解析，不保存 value。

每個 Project／Try／Attempt 對應一個 run；保存重試依 ownership tags 找回同一個 run，多個
候選會明確失敗。此 writer 與既有唯讀 MLflow evaluation／observation adapter 分開。

執行前先估計產出大小，再選擇合適的既有 profile。支援的平台會檢查本機剩餘容量。
MLflow transfer 超過 profile 的 `large_bytes`（預設 1 GiB）時保留已驗證的本機副本並標記
pending，檢查後使用：

```bash
exp --workspace PROJECT results save ATTEMPT --allow-large
```

可用 `storage add --large-bytes` 調整門檻；零代表停用 transfer 門檻。此門檻不是執行程序的
磁碟 quota。上傳失敗不改變 workload exit status；用 `results save` 重試保存，不重跑分析。
Output 掃描最多 4096 個檔案，canonical manifest 最多 512 KiB；更大集合請封裝成 archive。

## 自動整理與找回結果

```bash
exp --workspace PROJECT try summarize TRY --summary "延遲可分為兩群"
exp --workspace PROJECT try finish TRY --author agent --summary "下一步分開分析冷啟動與暖啟動"
exp history search "延遲" --all
exp --workspace PROJECT history search --source-key app --version COMMIT_PREFIX
exp --workspace PROJECT results describe ATTEMPT plots/latency.png --description "依 request state 分組的延遲分布"
exp --workspace PROJECT results list TRY
exp --workspace PROJECT results compare ATTEMPT_A ATTEMPT_B
exp --workspace PROJECT results fetch ATTEMPT plots/latency.png
exp --workspace PROJECT results open ATTEMPT plots/latency.png
```

`summarize` 預設 agent authorship 並維持 Try open。`finish --author agent` 自動選取已保存的
artifact manifests，明示 agent conclusion 尚未經人類審閱，不再要求一次 human confirmation。
Human conclusion 維持原本確認流程，可用 `--saved-results` 省去手填 JSON。
Adoption 與正式 Promotion 維持原本人類審核要求。

History 可搜尋 title、goal、summary、正文、tags、runner version 與 artifact 名稱；可搭配
`--after`、`--before`、`--kind`、`--state`、`--source-key`、`--version`。
SQLite index 可重建，讀取的 canonical records 才是來源。無法讀取的 Project 會列入 skipped，
不以 stale cache 假裝成功。

`results list` 不呼叫 provider；fetch／open 要求明確名稱，多個 Attempt 產生同名檔案時必須
選定 Attempt。取回後核對 digest，再回傳保留副檔名的 cache 路徑，或指定 `--destination`。
新主機配置同名 storage profile 後可取回遠端 artifacts；local-only artifacts 仍需要原始檔案
或備份，單純 clone Git 只有 manifest。

## Codebase CLI 的實際版本

```bash
exp runner add app --version-arg app --version-arg version --version-arg=--json \
  --environment-file uv.lock -- app analyze
exp --workspace PROJECT runner use app
exp --workspace PROJECT try exec TRY -- --config configs/probe.toml
```

Runner 使用 argv，不解析 shell。Version command 回傳
`{"version":"1.2.0","build_commit":"完整小寫 Git object ID"}`。
實際 executable digest、version／build commit、指定 environment files 的 digest 都與 Source
snapshot 分開保存。Build／Source agreement 明示 match、mismatch 或 unknown；缺少版本資訊
不代表完整重現。準備後 binary 改變會阻擋執行。臨時 script 仍可作為合法入口。

`try exec TRY --` 後若明確給 executable，會覆蓋 remembered runner；空 argv 或以 option 開頭時使用 runner default。傳入 runner positional arguments 時明確指定 `--runner PROFILE`。
