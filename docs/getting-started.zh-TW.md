# 快速開始

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

## 需求

- Git
- Go 1.26.4，或 `go.mod` 宣告的版本
- 用於 repository build targets 的 Make
- 只有使用 daemon dispatch 時才需要 Pueue 4.x
- 只有驗證 workload-owned runs 時才需要 MLflow CLI

`exp doctor` 會回報本機可找到哪些選用 provider binaries；預設不會接觸 provider。

## 從原始碼安裝

```bash
git clone https://github.com/daviddwlee84/exp-cli.git
cd exp-cli
make install
exp --version
```

`make install` 將 binary 寫入 `${PREFIX:-$HOME/.local}/bin`，並連結內嵌的 Agent
Skill。請確認該目錄已加入 `PATH`。

## 初始化研究專案

在既有 Git repository 中執行：

```bash
exp init --name "Encoder research"
exp policy init

exp pool add \
  --title "Local GPUs" \
  --capacity 2 \
  --unit gpu \
  --bottleneck accelerator
```

前兩個指令建立 canonical `experiments/` root 與明確的 manual policy。保存
`pool add` 印出的真實 ResourcePool ID：

```bash
POOL_ID='pool_...'
exp queue create --pool "$POOL_ID"
QUEUE_ID='queue_...'
```

## 記錄並評估 Idea

```bash
exp idea add \
  --title "Try cosine decay after warmup" \
  --summary "Reduce late-stage optimizer noise" \
  --lane exploit \
  --cluster optimizer

IDEA_ID='idea_...'
exp idea qualify "$IDEA_ID" \
  --payoff-summary "Improve validation macro-F1" \
  --payoff-metric macro_f1 \
  --payoff-unit score \
  --probability 0.45 \
  --impact 0.02 \
  --information-value 0.005 \
  --resource "$POOL_ID":1:3
```

把回傳的 Plan 放入 Queue，並檢查本機 context：

```bash
PLAN_ID='plan_...'
exp queue insert "$QUEUE_ID" "$PLAN_ID" --pool "$POOL_ID"
exp context
exp validate
```

Policy 位於 `manual` 或 `shadow` 時不會派送任何工作。接著可閱讀
[核心研究流程](workflows/core-workflow.md)，或先設定精確的
[runtime dispatch contract](workflows/runtime-dispatch.md)，再啟用 assisted automation。

## Machine-readable 使用方式

支援 `--json` 的指令會在 stdout 輸出一個具版本的 JSON envelope。請分開處理
stderr，並使用回傳的 typed IDs，不要解析人類可讀文字。主要 command families
請參考[指令地圖](reference/command-map.md)。
