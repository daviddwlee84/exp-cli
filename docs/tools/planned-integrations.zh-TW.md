# 規劃中的整合

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

本頁記錄 `exp` 能辨識，或已為其設計契約，但尚未實際操作的工具。此界線刻意明確，
避免將 executable discovery 誤認為可運作的整合 (working integration)。

## Discovery 的意義

```bash
exp doctor
exp doctor --json
exp doctor --live
```

Compiled provider registry 知道 candidate binary names、roles 與 capability names。
`doctor` 只使用類似 `LookPath` 的本機 discovery。它不會叫用 `--version`、聯絡 provider、
進行 authentication、安裝任何項目，或確認 capability support。目前 `--live` 只會增加
一則資訊性 diagnostic，不會執行額外 probe。

因此，即使 provider 顯示為 `found`，其所有 capabilities 仍可能是 `unknown`。只有專用且
經過審查的操作，才能針對其實際執行的 contract 判定 `supported` 或 `unsupported`。

## 目前的規劃清單

| 工具 | 目前的程式碼邊界 | 尚未實作 |
|---|---|---|
| DVC | Runner、Scheduler 與 ArtifactStore roles 的 compiled descriptor；本機查找 `dvc` | 不會執行 commands、安排 pipeline、stat/list artifacts、下載，或進行 remote credential flow |
| Slurm | Compiled Scheduler descriptor；本機查找 `squeue`、`sacct`、`sbatch` 與 `scancel` | 沒有 submission、observation、cancellation、accounting reconciliation 或 cluster context configuration |
| Optuna | Provider-neutral `exp.search-adapter/v1` contract，涵蓋限定於 Plan 的 Study open/ask/tell/prune/observe 行為 | 沒有具體 Optuna adapter、Python runtime、storage connection、package installation、service 或 authentication |
| Marimo | Compiled Runner descriptor；本機查找 `marimo` | 沒有 notebook inspection、preparation、sandboxing、dependency resolution 或 execution |
| Jupyter | Compiled Runner descriptor；本機查找 `jupyter` | 沒有 notebook inspection、kernel selection、preparation、sandboxing、dependency resolution 或 execution |

## DVC

DVC 可能作為 Runner、Scheduler 與 ArtifactStore 的邊界，但目前這些 roles 只是 metadata。
任何整合都必須在驗證真實 binary/version contract 後，逐項操作導入。Artifact references
必須維持不可變且經過清理；inspection 絕不能暗示會進行 download、cache write、remote
login 或 pipeline execution。

實作前，必須為每個操作記錄精確的 native JSON 或 fixed-field output、repository/remote
context、credential source、effect set、output bounds 與 recovery behavior。

## Slurm

未來的 Slurm adapter 必須保留 cluster、job、array 與 step identity。即使從 login node
叫用，controller 與 accounting commands 仍算 remote reads。它絕不能產生
`--export=ALL`；轉送 environment 必須使用明確 allowlist 或 site-approved profile。

優先使用已驗證的 native JSON。無法取得時，透過 `--parsable2 --noheader --format` 要求
具名 fixed fields。缺少或延遲的 accounting 必須產生 partial 或 `unknown` observations，
絕不能猜測 terminal state。巢狀 scheduling 也必須明確指定並審查 concurrency 與
cancellation 的 owner。

## Optuna-like search

內部 Study contract 是整合邊界，而不是 runtime。Study 從屬於單一且精確的 Plan revision，
可以選擇 parameters、記錄 trials 並 prune work。它不能擁有全域 Queue order、
ResourcePool allocation、科學 Findings、Releases 或 Promotions。

具體 adapter 必須使用經審查且有版本的 API，或經嚴格設定的 sidecar；在 provider commit
狀態不明時仍要維持冪等性 (idempotency)，並將完整 external Study identity 限定於其 Plan。
`exp` 不得代替使用者叫用 `python`、`uvx`、`pip`、安裝 Optuna、啟動 service，或開啟
authentication。

請參閱 [Search adapter 契約](../design/search-adapter-contract.md)。

## Marimo 與 Jupyter

Marimo 與 Jupyter 是潛在 Runner entrypoints，而非持久的 Schedulers。Discovery 必須與
inspection 分離，inspection 也必須與 execution 分離。僅僅找到 binary 或 notebook file，
不能解析 packages、啟動 kernel、執行 cells、建立 outputs，或修改 notebook metadata。

實作時需要精確的 executable 與 argument contract、working directory、environment
policy、timeout、output limits、sandbox expectations，以及 generated files 的明確規則。
Scheduling ownership 必須維持由 Attempt 的單一 configured Scheduler 擁有。

## 新增其他工具

W&B、Kaggle、Ray、Kubernetes、cloud control planes 與其他系統，不會根據已安裝 libraries
或 prose references 自動推斷。每個系統都必須先定義自己的具名 role、capability、effects、
identity model、redaction rules 與經測試的 failure semantics，才能移至
[工具總覽](index.md)的已實作區域。

## 未來可探索主題

- 各真實 adapter 實作後，記錄其 supported version ranges。
- 探索不隱式 materialize 的 DVC immutable artifact lookup。
- 設計 site-specific Slurm context 與 accounting profiles。
- 評估 Optuna sidecar transport 與 ambiguous-commit recovery tests。
- 定義不會自動執行的安全、可重現 notebook preparation。
