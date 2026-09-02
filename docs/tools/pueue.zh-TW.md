# Pueue

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

`exp` 使用 Pueue 4.x 作為已實作的本機排程器整合 (local scheduler integration)。
Pueue 擁有即時 task 與 group state；`exp` 提供 canonical scheduling intent、已清理的
observation、經稽核的 submission envelope，以及範圍嚴格受限的 cancellation。

Pueue process success 只屬於操作證據 (operational evidence)。經 reconciliation 後，
它可以推進 Attempt 的 operational state，但絕不會建立科學 Evaluation、Finding、
Candidate、Release 或 Promotion。

## 讀取已清理的 scheduler snapshot

```bash
exp provider pueue status
exp provider pueue status --json
```

此 command 必須在 `exp` project 內執行，並會聯絡已設定的 local Pueue daemon。它會讀取
native JSON、遞迴移除 task environment maps，且只回傳 normalized task 與 group data。
它不會公開 command strings、raw status objects 或 persisted environment values。

人類閱讀用 output 包含 group name、native group state、parallelism，以及每個 task 的
ID、group、normalized state 與 label。JSON 另外公開 priority、dependency IDs、native
state/reason、exit code，以及可取得時的 terminal timestamps 等安全欄位。

### 正規化 task states

| Pueue state 或 result | `exp` state |
|---|---|
| `Queued` | `queued` |
| `Stashed`, `Paused`, `Locked` | `blocked` |
| `Running`, `Starting` | `running` |
| `Done: Success` | `succeeded` |
| `Done: Failed` | `failed`，可取得時包含 exit code |
| `Done: Killed` | `cancelled` |
| `Done: DependencyFailed` | `dependency_failed` |
| 無法辨識的 shape 或 value | `unknown`，並附有大小受限的 native reason |

未知資料會以保守方式失敗 (fail closed)，不會被猜測成 terminal state。使用
`daemon status` 查看 private local controller state；該 command 不會聯絡 Pueue。
`daemon frontier` 也只讀本機資料，而 `daemon tick` 與 `daemon run` 會聯絡 Pueue
進行 reconciliation 與 admission。

## 取消單一且確實 owned 的 task

Cancellation 是明確的 provider mutation，必須提供 exact task ID 與 `--confirm`：

```bash
exp provider pueue cancel 42 --confirm

# 需要時指定非預設的 project-relative runtime contract。
exp provider pueue cancel 42 \
  --confirm \
  --config configs/exp-runtime.json \
  --json
```

預設 runtime contract 是 `.exp/runtime.json`。非負數 numeric task ID 與 confirmation
是必要條件，但並不足夠。呼叫 `pueue kill` 前，`exp` 會要求下列條件全部成立：

1. 目前 project 中恰好一筆 canonical Attempt 參照該 native Pueue task ID。
2. Attempt 將 scheduler ownership 指定給 `pueue`，且是具有 canonical pool 與
   dispatch route 的 v2 dispatch。
3. External reference context 是 local Pueue context。
4. Attempt 保存的 Pueue group 與 label 仍符合目前 runtime pool binding。
5. Live scheduler snapshot 中恰好一個 task 使用該 ID，且其 group 與 label 符合
   canonical route。

缺少、foreign、duplicated、stale 或 mismatched identity 都會被拒絕。`--confirm`
絕不會略過 identity check。Command 成功後，Pueue 擁有 cancellation request；後續
daemon reconciliation 會觀察 terminal scheduler state，並執行已授權的 canonical
transition。

## Daemon identity 與 dispatch safety

`.exp/runtime.json` 將每個 canonical ResourcePool 綁定至一個 Pueue group 與 stable
label prefix。同一 group 內的 prefixes 必須 pairwise prefix-free，且長度要能容納完整
dispatch ID。Dispatch ID 會納入 canonical checkout scope，因此共享 Git-common SQLite
store 的 linked worktrees 仍保有彼此分離的 recovery identities。

Daemon 會套用下列規則：

- admission 前先 snapshot Pueue，並將已觀察到的 nonterminal tasks 計入 canonical
  pool capacity；
- 聯絡 Pueue 前先寫入 submission outbox；
- 遇到 ambiguous submission 或 restart 時，只復原相同 canonical worktree scope 的
  outbox entries，並查找精確的 group/label route；
- 同一 route 對應到多個 live tasks 時會報錯，不會任意選擇；
- submission 僅限 private `exp worker run` envelope，必須使用乾淨的 absolute worker
  path 與 validated argument tokens；任意 shell fragments 與 record titles 都不能成為
  submitted command。

Pueue 會將 task environments 保存在 daemon state。因此 runtime `secret_env` 必須為空，
`allowed_env` 只能包含明確核准的非秘密 names。需要 credentials 的 workloads 必須在
啟動後，透過 workload-side broker 或 provider profile 取得。

完整 workload contract 請參閱 [執行與派送](../workflows/runtime-dispatch.md)；scheduler
effects 與 safety invariants 請參閱 [Provider 契約](../design/provider-contract.md)。

## 失敗處理指引

- 如果 `status` 無法連線，確認 compatible Pueue daemon 已經啟動；`exp` 絕不會隱式
  啟動它。
- 如果 cancel 回報沒有 canonical owner，請檢查 Attempt 的 scheduler external
  reference，不要透過 `exp` 取消未受追蹤的 native task。
- 如果 group 或 label identity 已改變，請協調 runtime contract 與 canonical Attempt
  route；不要改用另一個 task ID 繞過檢查。
- 如果 dispatch 拒絕 environment configuration，請從 persisted Pueue environment
  移除 secrets，並將 credential acquisition 移入 workload。

## 未來可探索主題

以下是預留的文件位置，並非目前支援的操作：

- group sizing、priority 與 queue-tuning runbooks；
- dependency、retry、pause 與 resume semantics；
- 具遮蔽機制的 bounded task-log 與 output inspection；
- shared 或 remote Pueue daemon deployment 與 authentication profiles；
- ambiguous submission 與 daemon replacement 的 operator recovery drills。
