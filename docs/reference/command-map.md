# Command Map

The current approved, generator-backed CLI metadata contains **136 command paths**. This authored map groups those paths by user task; it does not duplicate
every flag.

The running binary is authoritative for syntax:

```bash
exp <command> --help
```

Use `--json` where advertised and consume the versioned envelope rather than
parsing human output. The exact build-matched inventory is the generated
[`internal/skill/exp-cli/references/commands.md`](https://github.com/daviddwlee84/exp-cli/blob/main/internal/skill/exp-cli/references/commands.md),
which maintainers update only through `exp skill sync`.

## Find the next command first

| Need | Route | Start | Then |
|---|---|---|---|
| Create or reconnect a Project/Source | Setup | `exp init` or `exp workspace status` | `source status` → `config show` |
| Answer a short bounded question | Quick exploratory Try | `exp try run` | `try status` → `try finish` → optional `try adopt` |
| Produce governed comparable evidence | Rigorous Experiment | `exp idea add` | qualify → Queue → runtime v2/daemon → Evaluation v2/Candidate v2 |
| Move validated evidence to a target | Promotion | `exp candidate create` | Release → sealed holdout → named-human Promotion |
| Resume or browse safely | Read-only | `exp context` | `exp guide`, completion, or `exp ui` |

Conceptual terminal guides and standard Cobra utilities are intentionally outside
the 136 generator-backed domain paths:

```bash
exp guide [setup|workspaces|config|quick|research|promotion]
exp completion [bash|fish|powershell|zsh]
exp help [command]
```

Completion and guide rendering read local records/config only and never probe a
provider or resolve secret values.

## Establish and inspect the Project (7)

| Command | Use it to |
|---|---|
| `exp` | Enter the control plane or print the embedded skill with `--skill`. |
| `exp init` | Initialize an embedded Project or use the TTY/explicit dedicated-repository flow with an initial Source. |
| `exp context` | Read a local resumable summary without provider refresh. |
| `exp ui` | Open the read-only local TUI on real terminal stdin/stdout; readiness probes occur only after explicit refresh. There is no JSON mode—use `context --json`. |
| `exp doctor` | Discover core/optional capabilities. Default is executable lookup only; `--live` runs bounded version/read-only local-service probes with no login, install, config write, service start, or workload. |
| `exp validate` | Validate canonical records and graphs without provider calls. |
| `exp render` | Generate deterministic projections or report drift with `--check`. |

## Resolve workspace backends and associations (7)

| Command | Use it to |
|---|---|
| `exp workspace` | Discover canonical workspace association commands. |
| `exp workspace register` | Register the resolved Project clone on this host. |
| `exp workspace status` | Show resolved Project/Source, associations, and config trust summary. |
| `exp workspace backend` | Discover workspace-provider selection/capability commands. |
| `exp workspace backend list` | List `native_git` and optional providers; `--probe` performs bounded local checks. |
| `exp workspace backend status` | Show requested versus actual preparation provider and fallback reason. |
| `exp workspace backend handoff` | Request opening one exact managed Try Attempt through a selected provider; unsupported capability remains explicit. |

`native_git` is the correctness baseline. `dev_cli` is discoverable, but its
prepare/open/handoff/retire lifecycle is unsupported until `dev` supplies
schema-versioned exact-path machine capability receipts. Missing `dev` does not
hide the action or block native Try.

## Manage canonical Sources and clone associations (8)

| Command | Use it to |
|---|---|
| `exp source` | Discover canonical Source/clone-association commands. |
| `exp source add` | Review, publish, and associate a Source repository/subdirectory; missing fields may use a TTY wizard. |
| `exp source list` | List canonical Sources without provider access. |
| `exp source show` | Show one Source identity, state, subdir, path, and revision. |
| `exp source status` | Validate host-local associations for one or all Sources. |
| `exp source register` | Register or repair a moved/recloned Source clone association. |
| `exp source append-locator` | Append a sanitized locator through exact-revision CAS and confirmation. |
| `exp source retire` | Retire an active Source through exact-revision CAS and confirmation. |

## Inspect configuration and trust (7)

| Command | Use it to |
|---|---|
| `exp config` | Discover layered config and exact-digest trust commands. |
| `exp config path` | Show candidate/applied user, canonical, Source, and subdirectory config paths. |
| `exp config show` | Show effective non-secret values and safe provenance summary. |
| `exp config explain` | Explain all layers or one effective field, including digest/capability trust. |
| `exp config trust` | Review/approve a current applied config or runtime v2 file by exact digest; missing fields may use a TTY wizard. |
| `exp config revoke` | Revoke a current/deleted scope, optionally narrowed by digest/capability. |
| `exp config list` | List sanitized local trust receipts. |

Repository-selected Source/workspace/agent/MLflow settings are inert until their
winning exact digest is trusted. When a Source is selected, repository-layer
receipts bind that Source ID, so explain and trust with the same authority context
used for execution. Runtime v2 separately requires `runtime.dispatch` trust for
raw `.exp/runtime.json` bytes.

## Run agents and manage embedded guidance (8)

| Command | Use it to |
|---|---|
| `exp agent` | Discover profile inspection and direct fresh-agent execution. |
| `exp agent profiles` | Validate/list configured `exp.agents/v1` profiles. |
| `exp agent run` | Run one fresh profile under a supplied JSON Schema output contract. |
| `exp skill` | Discover version-matched embedded-skill commands. |
| `exp skill check` | Check installed files, compatibility, hashes, and consumer links without mutation. |
| `exp skill install` | Atomically install the embedded skill and safe consumer links. |
| `exp skill print` | Print this build's embedded `SKILL.md`. |
| `exp skill sync` | Generate source-tree `references/commands.md`; `--check` verifies exact drift without writing. |

## Run and conclude a bounded Try (12)

| Command | Use it to |
|---|---|
| `exp try` | Discover bounded managed-native-worktree exploration. |
| `exp try run` | Register and execute one argv-only clean or explicit `--dirty=capture` Try; missing fields may use a TTY wizard. |
| `exp try retry` | Create a successor for a terminal Attempt with identical Source/argv identity. |
| `exp try resume` | Resume only provably unstarted work or import a verified durable marker. |
| `exp try reconcile` | Import late evidence or explicitly abandon one uncertain Attempt after confirmation. |
| `exp try finish` | Record a reviewed human conclusion with owned result digests/refs or explicit `--no-results`. |
| `exp try abandon` | Record a confirmed human reason for abandoning an open Try. |
| `exp try adopt` | Atomically adopt a concluded Try as Idea v2; missing classification may use a TTY wizard. |
| `exp try list` | List canonical Tries and Attempt counts. |
| `exp try show` | Show one Try with canonical and local direct status. |
| `exp try status` | Inspect Attempts/jobs/markers/worktrees without executing. |
| `exp try cleanup` | Remove only verified manager-owned worktrees and seed bundles; retain canonical evidence/branch/markers. |

Missing MLflow, Pueue, or `dev` does not block native Try. Dirty capture is
Try-only and cannot directly support Candidate v2.

## Capture, qualify, and prioritize research (23)

### Ideas and Plans

| Command | Use it to |
|---|---|
| `exp idea` | Discover Idea capture/qualification commands. |
| `exp idea add` | Create an unqueued canonical Idea. |
| `exp idea develop` | Ask one fresh agent for a queue-ready Plan and optionally apply it. |
| `exp idea list` | List canonical Ideas. |
| `exp idea qualify` | Atomically turn an Idea into a fully priced Plan. |
| `exp plan` | Discover priced-Plan commands. |
| `exp plan add` | Create a validated Plan from flags or versioned JSON input. |
| `exp plan list` | List canonical Plans without provider contact. |
| `exp plan refresh` | Reassess utility, repin current Finding beliefs, and remove a stale Plan from Queue before reranking. |

### Policy, resources, and Queue

| Command | Use it to |
|---|---|
| `exp policy` | Discover autonomy/Queue-policy commands. |
| `exp policy autonomy` | Change autonomy through the explicit auto-experiment confirmation gate. |
| `exp policy cluster-set` | Set saturation thresholds or explicitly reopen a direction. |
| `exp policy init` | Create default-manual canonical `POLICY.md`. |
| `exp policy show` | Show current canonical research policy. |
| `exp pool` | Discover ResourcePool commands. |
| `exp pool add` | Create a named constrained compute/human ResourcePool. |
| `exp pool list` | List canonical ResourcePools. |
| `exp queue` | Discover Plan ranking across Pool/lane frontiers. |
| `exp queue create` | Create exploit/explore partitions for selected Pools. |
| `exp queue insert` | Score/insert a Plan, optionally adding listwise advice and order-swapped battles. |
| `exp queue list` | List canonical Queues. |
| `exp queue remove` | Remove a Plan with exact Queue compare-and-swap. |
| `exp queue show` | Inspect ordered Pool/lane entries and pinned revisions. |

## Prepare code and operate formal execution (19)

### Daemon (7)

| Command | Use it to |
|---|---|
| `exp daemon` | Discover local orchestration commands. |
| `exp daemon frontier` | Inspect canonical frontiers/runtime configuration without contacting Pueue. |
| `exp daemon status` | Read local daemon state without provider contact. |
| `exp daemon tick` | Perform one Pueue reconciliation/capacity-admission pass. |
| `exp daemon run` | Reconcile/admit continuously until cancelled. |
| `exp daemon pause` | Pause new dispatch while preserving reconciliation state. |
| `exp daemon resume` | Resume eligible dispatch. |

### Experiment workspaces and closure (6)

| Command | Use it to |
|---|---|
| `exp experiment` | Discover isolated workspace and scientific-lifecycle commands. |
| `exp experiment agent` | Run the configured implementation agent in an isolated worktree and commit exact allowlisted changes. |
| `exp experiment workspace` | Discover preparation/commit commands. |
| `exp experiment workspace prepare` | Create an isolated worktree at an exact base commit. |
| `exp experiment workspace commit` | Commit only observed allowlisted changes. |
| `exp experiment close` | Atomically conclude an Experiment, complete its Plan, dispose evidence, and publish Findings. |

### Implemented provider operations (6)

| Command | Use it to |
|---|---|
| `exp provider` | Discover explicit audited reads/controls for supported tools. |
| `exp provider mlflow` | Discover read-only MLflow verification. |
| `exp provider mlflow verify` | Verify selected metrics/tags from a workload-created run. |
| `exp provider pueue` | Discover supported Pueue reads/controls. |
| `exp provider pueue status` | Read a sanitized task/group snapshot. |
| `exp provider pueue cancel` | Cancel one exact matching task after confirmation. |

Pueue is required for daemon dispatch, not command visibility or native Try.
MLflow remains optional observation; missing observation never becomes a
scientific verdict.

## Evaluate, package, and promote evidence (13)

| Command | Use it to |
|---|---|
| `exp evaluation` | Discover comparable-protocol and immutable-result commands. |
| `exp evaluation spec` | Discover scientific/promotion EvaluationSpec commands. |
| `exp evaluation spec create` | Create a comparable metric protocol with bounded ResourcePool budget. |
| `exp evaluation create` | Record an immutable Evaluation; `--attempt` binds Evaluation v2 to an exact formal Attempt. |
| `exp candidate` | Discover Candidate creation. |
| `exp candidate create` | Create Candidate v2 from clean supported Attempt-bound evidence, or use the explicit legacy v1 shape. |
| `exp release` | Discover typed Release composition. |
| `exp release create` | Create a draft or atomically validated Release from named Candidate slots. |
| `exp promotion` | Discover human-only production Promotion commands. |
| `exp promotion spec-create` | Create a sealed, bounded, human-gated PromotionSpec. |
| `exp promotion append` | Append a confirmed named-human outcome to one target chain. |
| `exp champion` | Show Champions derived from append-only Promotion chains. |
| `exp champion manifest` | Render a deterministic downstream manifest. |

## Inspect records and migrate safely (8)

| Command | Use it to |
|---|---|
| `exp migrate` | Discover explicit harness-v0 migration. |
| `exp migrate plan` | Build a read-only fingerprinted plan and surface ambiguities. |
| `exp migrate apply` | Apply one fully reviewed/fingerprint-validated plan. |
| `exp record` | Discover canonical record inspection/transaction commands. |
| `exp record list` | List Git-backed records, optionally by kind. |
| `exp record show` | Resolve/show one record as view, JSON envelope, or normalized Markdown. |
| `exp record transaction` | Apply the supported low-risk prepared Idea/ResourcePool transaction. |
| `exp record recover` | Roll durable prepared transactions forward from exact hashes. |

The original families plus the exploration commands below total **136** approved paths. If syntax is absent from
`exp <command> --help` or generated `commands.md`, do not infer it from a roadmap,
note, or older documentation.

Large datasets, model/checkpoint/artifact bytes, traces, and unbounded logs stay
in MLflow, DVC, or object storage. Only bounded summaries, digests, exact
identities, and sanitized references enter canonical Git records.

## Exploration and artifacts

`try root/start/exec/summarize`, `storage add/use/show`, `input bind/list`, `runner add/use/list`, `history search`, `results list/compare/describe/save/fetch/open`. [Workflow and complete examples](../workflows/exploration.md).
