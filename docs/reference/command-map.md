# Command Map

The current CLI exposes 77 visible command paths. This page groups every path
by the job it helps accomplish; it deliberately does not duplicate the full
generated command reference or every flag.

The running binary is the source of truth for syntax:

```bash
exp <command> --help
```

Use `--json` where the command offers it and consume the versioned envelope
instead of parsing human-readable output. For an exact build-matched inventory,
see the
[generated command reference](https://github.com/daviddwlee84/exp-cli/blob/main/internal/skill/exp-cli/references/commands.md).
Parent commands such as `exp queue` or `exp evaluation` primarily organize
their subcommands.

## Set up and inspect the project (6)

| Command | Use it to |
|---|---|
| `exp` | Enter the Git-native research control plane or print the embedded skill with `--skill`. |
| `exp init` | Initialize the fixed `experiments/` root idempotently. |
| `exp context` | Read a local resumable research summary without refreshing providers. |
| `exp doctor` | Discover built-in and optional local provider capabilities. Default behavior only looks for executables; current `--live` performs no provider contact. |
| `exp validate` | Validate canonical records and their graph without provider calls. |
| `exp render` | Generate deterministic projections or report projection drift with `--check`. |

## Run agents and manage embedded guidance (8)

| Command | Use it to |
|---|---|
| `exp agent` | Discover the profile inspection and direct agent-run commands. |
| `exp agent profiles` | Validate and list configured agent CLI profiles. |
| `exp agent run` | Run one fresh profile with a supplied JSON Schema output contract. |
| `exp skill` | Discover commands for the version-matched embedded guidance skill. |
| `exp skill check` | Check installed skill files, compatibility, hashes, and consumer links without mutation. |
| `exp skill install` | Atomically install the embedded skill and safe consumer links. |
| `exp skill print` | Print this build's embedded `SKILL.md`. |
| `exp skill sync` | Synchronize the generated source-tree command reference; use `--check` to inspect drift without writing. |

## Capture, qualify, and prioritize research (23)

### Ideas and Plans

| Command | Use it to |
|---|---|
| `exp idea` | Discover Idea capture and qualification commands. |
| `exp idea add` | Create an unqueued canonical Idea from a human or agent direction. |
| `exp idea develop` | Ask one fresh agent for a queue-ready Plan proposal and optionally apply it. |
| `exp idea list` | List canonical Ideas. |
| `exp idea qualify` | Atomically turn an Idea into a fully priced Plan. |
| `exp plan` | Discover commands for priced research Plans. |
| `exp plan add` | Create a validated Plan from flags or versioned JSON input. |
| `exp plan list` | List canonical Plans without provider contact. |
| `exp plan refresh` | Reassess utility, repin current Finding beliefs, and remove a stale Plan from its Queue before reranking. |

### Policy, resources, and Queues

| Command | Use it to |
|---|---|
| `exp policy` | Discover canonical autonomy and Queue-policy commands. |
| `exp policy autonomy` | Change autonomy through the explicit auto-experiment confirmation gate. |
| `exp policy cluster-set` | Set cluster saturation thresholds or explicitly reopen a direction. |
| `exp policy init` | Create the default-manual canonical `POLICY.md`. |
| `exp policy show` | Show the current canonical research policy. |
| `exp pool` | Discover ResourcePool commands. |
| `exp pool add` | Create a named constrained compute or human ResourcePool. |
| `exp pool list` | List canonical ResourcePools. |
| `exp queue` | Discover Plan ranking commands across Pool/lane frontiers. |
| `exp queue create` | Create exploit and explore partitions for selected ResourcePools. |
| `exp queue insert` | Score and insert a Plan, optionally using listwise advice and order-swapped battles. |
| `exp queue list` | List canonical Queues. |
| `exp queue remove` | Remove a Plan with an exact Queue compare-and-swap. |
| `exp queue show` | Inspect ordered Pool/lane entries and their pinned revisions. |

## Prepare code and operate local execution (19)

### Daemon control

| Command | Use it to |
|---|---|
| `exp daemon` | Discover local orchestration daemon commands. |
| `exp daemon frontier` | Inspect canonical dispatch frontiers without contacting Pueue. |
| `exp daemon pause` | Stop new dispatch while preserving reconciliation state. |
| `exp daemon resume` | Resume eligible dispatch. |
| `exp daemon run` | Reconcile and admit work continuously until cancelled. |
| `exp daemon status` | Read local daemon state without provider contact. |
| `exp daemon tick` | Perform one Pueue reconciliation and capacity-admission pass. |

### Experiment workspaces and closure

| Command | Use it to |
|---|---|
| `exp experiment` | Discover isolated workspace and scientific lifecycle commands. |
| `exp experiment agent` | Run the configured implementation agent in an isolated worktree and commit exact allowlisted changes. |
| `exp experiment close` | Atomically conclude an Experiment, complete its Plan, dispose evidence, and publish Findings. |
| `exp experiment workspace` | Discover workspace preparation and commit commands. |
| `exp experiment workspace commit` | Commit only the observed allowlisted ChangeSet in the experiment worktree. |
| `exp experiment workspace prepare` | Create an isolated experiment branch and linked worktree at an exact base commit. |

### Implemented provider operations

| Command | Use it to |
|---|---|
| `exp provider` | Discover explicit audited reads and controls for supported tools. |
| `exp provider mlflow` | Discover the read-only MLflow verification command. |
| `exp provider mlflow verify` | Verify requested metrics and expected tags from a workload-created MLflow run. |
| `exp provider pueue` | Discover supported Pueue reads and controls. |
| `exp provider pueue cancel` | Explicitly cancel one exact matching Pueue task after confirmation. |
| `exp provider pueue status` | Read a sanitized Pueue task and group snapshot. |

## Evaluate, package, and promote evidence (13)

| Command | Use it to |
|---|---|
| `exp evaluation` | Discover comparable protocol and immutable result commands. |
| `exp evaluation create` | Record one immutable Evaluation against a declared EvaluationSpec. |
| `exp evaluation spec` | Discover scientific and promotion EvaluationSpec commands. |
| `exp evaluation spec create` | Create a comparable metric protocol with a bounded ResourcePool budget. |
| `exp candidate` | Discover Candidate creation commands. |
| `exp candidate create` | Package supported evidence, a passing Evaluation, and an exact Git ChangeSet as a Candidate. |
| `exp release` | Discover typed Release composition commands. |
| `exp release create` | Create a draft or atomically validated Release from named Candidate slots. |
| `exp promotion` | Discover human-only production Promotion commands. |
| `exp promotion append` | Append a confirmed human Promotion outcome to the target's chain. |
| `exp promotion spec-create` | Create a sealed, bounded, human-gated PromotionSpec. |
| `exp champion` | Show current Champions derived from append-only Promotion chains. |
| `exp champion manifest` | Render a deterministic downstream manifest for current Champions. |

## Inspect records and migrate safely (8)

| Command | Use it to |
|---|---|
| `exp migrate` | Discover explicit harness-v0 migration commands. |
| `exp migrate apply` | Apply one fully reviewed and fingerprint-validated migration plan. |
| `exp migrate plan` | Build a read-only migration plan and surface ambiguities for review. |
| `exp record` | Discover canonical record inspection and transaction commands. |
| `exp record list` | List Git-backed canonical records, optionally by kind. |
| `exp record recover` | Roll durable prepared transactions forward from exact hashes. |
| `exp record show` | Resolve and show one canonical record as a view, JSON envelope, or normalized Markdown. |
| `exp record transaction` | Apply the supported low-risk prepared Idea/ResourcePool transaction. |

The section counts above total 77 command paths. If a command or flag is absent
from `exp <command> --help`, do not infer it from a roadmap, note, or older
documentation.
