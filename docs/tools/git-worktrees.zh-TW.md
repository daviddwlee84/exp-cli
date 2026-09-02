# Git 與 Worktrees

!!! note "Terminology rule (zh-TW pages)"
    技術名詞首次出現以「中文 (English original)」格式呈現，例：依賴注入
    (dependency injection)。**不自創翻譯**——若無公認譯名直接保留英文
    （如 `embedding`、`tokenizer`）。代碼、API 名、CLI flag、套件名、檔名一律不翻。

在 `exp` 架構中，Git 擁有原始碼歷史。`exp` 可以為 active Experiment 準備隔離的 linked
worktree，並建立一個精確 commit；但它絕不 merge 該 branch、移除 worktree，或變更由
人類擁有的 integration branch。

## 前置條件

Workspace commands 要求 Experiment 處於 active 狀態且設計已鎖定。若要修改已結束的
evidence，應建立 follow-up Experiment。來源 checkout 必須乾淨，且 `--base` 必須指定
完整、小寫的 Git object ID；symbolic revisions 與 abbreviated hashes 都會遭到拒絕。

必須提供至少一個可重複使用的 `--allow` glob。Globs 是 repository root-relative POSIX
paths，例如 `src/**` 或 `configs/*.toml`。它們定義 experiment 可以變更的完整範圍，
不是給 agent 的參考建議。

## 準備隔離工作區

```bash
EXPERIMENT_ID='exp_...'
BASE_COMMIT='0123456789abcdef0123456789abcdef01234567'

exp experiment workspace prepare "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**'
```

`prepare` 會驗證精確的 repository 與 base commit、要求乾淨的來源 checkout，並在
`exp/…` 下建立以完整 Experiment UUID 與長度受限 title slug 命名的 branch。Linked
worktree 儲存在來源 repository 外的 XDG data home 之下，並以 repository-specific
namespace 分隔。新 checkout 必須指向指定 base、位於預期 branch，且保持乾淨。

Command 會拒絕重複使用既有 path，也不會跟隨遭替換的 symlink。其 machine result 會指出
repository、worktree、base commit、branch 與 normalized allowlist。

## Commit 精確 change set

```bash
exp experiment workspace commit "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**' \
  --json
```

`commit` 會重新探索預期 worktree、確認它屬於相同的 Git common directory 與 branch，
並收集 tracked 和 untracked changes。接著會：

1. 拒絕空的 change set；
2. 拒絕 `.git`、`experiments/` 下的所有內容，以及 normalized allowlist 以外的任何 path；
3. 精確 stage 觀察到的 paths，並驗證 staged path set；
4. 建立一個以指定 base 為 parent 的 commit；
5. 驗證結果 checkout 為乾淨狀態，且 committed path set 仍然精確。

回傳的 ChangeSet 包含 `base_commit`、`head_commit`、`branch`、排序後的 `paths`，以及從
這些精確值衍生的 `sha256:` diff identity。如果 worktree 已包含預期的單一 commit，重複
叫用會驗證並回傳它，而不會再建立另一個 commit。

## 執行 code-edit agent workflow

```bash
exp experiment agent "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**' \
  --prompt implementation-notes.md \
  --json
```

這會組合 workspace preparation、一個全新的 `experiment_implementer` Agent CLI process，
以及相同的精確 commit validation。Committed Git diff 才是權威來源；如果 agent 回報的
`changed_paths` 不一致，`exp` 會回傳 diagnostic，而不會相信該 report。

Profile 設定請參閱 [Agent CLI profiles](agent-cli-profiles.md)。

## 在 runtime 使用 prepared worktree

如果 Plan 必須從 HEAD 等於 `head_commit` 的唯一 linked worktree 執行，請在
`.exp/runtime.json` 將 `checkout` 設為 `registered_worktree`。Config 儲存 Git identity，
而不是 host path。分派前，daemon 會檢查 repository identity、精確 HEAD、base ancestry、
committed ChangeSet，以及乾淨且可執行的 tree。

## 證據邊界

Commit 是準備完成的程式碼，不是執行證據 (execution evidence)。只有當 included Run
具有成功的 direct Attempt，且其 Git identity 與 ChangeSet 相符，再加上通過的 scientific
Evaluation 時，才有資格支援 Candidate。Experiment branch 是否以及如何整合，仍由人類
審查控制。

## 未來可探索主題

- 記錄經審查的 completed worktree 手動清理程序。
- 加入大型 allowlists 與 repository-specific path conventions 範例。
- 說明人類整合 experiment branch 時的 conflict handling。
- 探索不會削弱 exact-path validation 的 signed commits。
