# Harness-v0 遷移

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現。若無公認譯名，
    直接保留英文。程式碼、API 名、CLI flag、套件名與檔名一律不翻。

Migration 使用明確、可審查的 plan/apply protocol，絕不會默默重寫舊 research tree。

## 建立唯讀 plan

```bash
exp migrate plan \
  --source path/to/harness-v0 \
  --output migration-plan.json \
  --json
```

Plan 會 fingerprint source material、計算 deterministic target identities，並把模糊欄位
回報為 `needs_review`。Unknown source spans 會被保存，而不是丟棄。

## 解決模糊項目

只把回報的 `needs_review` keys 複製到 resolution file，選擇預期 mapping，再用
`--resolutions` 重建 plan。請審查完整結果；只完成部分審查的 plan 不能 apply。

## 套用精確 plan

```bash
exp migrate apply --plan migration-plan.json --json
exp validate
exp render --check
```

Apply 會驗證 source fingerprint，並採用 no-clobber writes。若 source 在審查後改變，
請重建 plan，不要強制套用 stale plan。

完整欄位 mapping 與 ambiguity rules 請見
[Harness-v0 相容與遷移](../design/harness-v0-migration.md)。
