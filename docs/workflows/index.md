# Workflows

Use the decision table before opening a detailed workflow. The three routes have
different evidence and authority; do not use a quick Try as a shortcut around a
formal Experiment or the human promotion gate.

## Choose the route

| Your next decision | Route | Start | Continue |
|---|---|---|---|
| Is a direction worth formalizing? | **Quick exploratory Try** | `exp guide quick` → `exp try run` | `try status` → `try finish` → optional `try adopt` |
| Should governed compute test a registered claim? | **Rigorous Experiment** | `exp guide research` → `exp idea add` | qualify → Queue → runtime v2/daemon → Evaluation v2 → Candidate v2 |
| Should validated evidence reach a target? | **Promotion** | `exp guide promotion` → `exp candidate create` | typed Release → sealed fresh holdout → named-human Promotion |

Before any route, establish the same context:

```bash
exp guide setup
exp guide workspaces
exp guide config
exp workspace status
exp source status
exp config show
```

## Detailed guides

| Guide | Use it when |
|---|---|
| [Core research workflow](core-workflow.md) | Choosing Try versus formal work, then moving from Sources through comparable evidence |
| [Agents and workspaces](agents-and-workspaces.md) | Asking a fresh CLI agent to plan or implement Source-aware isolated changes |
| [Runtime dispatch](runtime-dispatch.md) | Running a managed native-worktree Try or clean cross-repository formal runtime v2 |
| [Evidence to promotion](evidence-to-promotion.md) | Enforcing Attempt v3 → Evaluation v2 → Candidate v2 → human Promotion |
| [Migration and compatibility](migration.md) | Migrating harness-v0 or understanding exact Project/record/runtime/worker/journal compatibility |

Use `exp context` to resume, `exp guide` for conceptual terminal help,
`exp <command> --help` for syntax, shell completion for local IDs/Sources, and
`exp ui` for a read-only overview. Provider absence does not remove commands:
default `doctor` is local lookup, while `doctor --live` is an explicit bounded
read-only probe. Pueue is required only for formal daemon dispatch; MLflow is
optional observation; native Try does not require either or `dev`.

!!! warning "Authority does not move with convenience"
    A generated projection, provider dashboard, agent recommendation, or TUI
    view can inform a command, but it never becomes canonical merely because it
    is easy to read. Use the domain command and review the resulting records.

Large datasets, models, artifact files, and unbounded logs remain in MLflow,
DVC, or object storage. Only bounded summaries, digests, exact identities, and
sanitized references enter Git. Run `exp validate` after supported mutations
and `exp render --check` when a workflow depends on current generated views.
