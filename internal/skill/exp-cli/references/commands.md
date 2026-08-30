<!-- generated from exp command metadata; do not edit -->

# Current `exp` commands

This reference contains only the command metadata supplied by this build's CLI layer. It is not a roadmap for deferred commands.

## `exp`

Use the Git-native research control plane.

```text
exp [--skill]
```

Options:

- `--skill` — print this build's embedded SKILL.md

## `exp agent`

Inspect or run configured fresh agent CLI profiles.

```text
exp agent
```

## `exp agent profiles`

Validate and list local agent CLI profiles.

```text
exp agent profiles [--config PATH] [--json]
```

Options:

- `--config` — set the agent profile TOML path
- `--json` — emit the versioned machine-readable envelope

## `exp agent run`

Run one fresh agent CLI with a strict JSON output contract.

```text
exp agent run --role ROLE --schema PATH [--prompt PATH|-] [--profile NAME] [--cwd DIR] [--config PATH] [--json]
```

Options:

- `--config` — set the agent profile TOML path
- `--cwd` — set the agent working directory
- `--json` — emit the versioned machine-readable envelope
- `--profile` — override the role profile
- `--prompt` — read the prompt from a file or stdin
- `--role` — select the configured role
- `--schema` — set the JSON Schema file

## `exp candidate`

Create scientifically validated, Git-addressed Candidates.

```text
exp candidate
```

## `exp candidate create`

Create a Candidate from supported evidence and an exact change set.

```text
exp candidate create --experiment ID --evaluation ID --git-commit SHA --change PATH [--json]
```

Options:

- `--change` — add an exact changed path
- `--evaluation` — select its passing scientific Evaluation
- `--experiment` — select the supported Experiment
- `--git-commit` — pin the full Git object ID
- `--json` — emit the versioned machine-readable envelope

## `exp champion`

Show champions derived from append-only Promotion chains.

```text
exp champion [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp champion manifest`

Render a deterministic downstream manifest from current champions.

```text
exp champion manifest [--target TARGET] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--target` — select one production target

## `exp context`

Show a local, resumable research summary without provider refresh.

```text
exp context [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp daemon`

Inspect or control the local orchestration daemon.

```text
exp daemon
```

## `exp daemon frontier`

Show canonical dispatch frontiers without contacting Pueue.

```text
exp daemon frontier [--config PATH] [--json]
```

Options:

- `--config` — set the project runtime contract path
- `--json` — emit the versioned machine-readable envelope

## `exp daemon pause`

Pause new dispatch while preserving reconciliation state.

```text
exp daemon pause [--reason TEXT] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--reason` — record a bounded human reason

## `exp daemon resume`

Resume eligible daemon dispatch.

```text
exp daemon resume [--reason TEXT] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--reason` — record a bounded human reason

## `exp daemon run`

Run the local reconcile and admission loop until cancelled.

```text
exp daemon run [--config PATH] [--holder ID] [--interval DURATION]
```

Options:

- `--config` — set the project runtime contract path
- `--holder` — override the lease holder ID
- `--interval` — set the reconcile interval

## `exp daemon status`

Show local daemon state without provider contact.

```text
exp daemon status [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp daemon tick`

Reconcile Pueue and fill available capacity once.

```text
exp daemon tick [--config PATH] [--holder ID] [--json]
```

Options:

- `--config` — set the project runtime contract path
- `--holder` — override the lease holder ID
- `--json` — emit the versioned machine-readable envelope

## `exp doctor`

Inspect local core and optional-tool capabilities.

```text
exp doctor [--json] [--live]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--live` — permit only the explicitly documented live probes

## `exp evaluation`

Define comparable protocols and record immutable results.

```text
exp evaluation
```

## `exp evaluation create`

Record one immutable Evaluation.

```text
exp evaluation create --spec ID --subject ID --outcome OUTCOME --metric VALUE [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--metric` — record a declared metric
- `--outcome` — set passed, failed, or invalid
- `--spec` — select the EvaluationSpec
- `--subject` — select the evaluated subject

## `exp evaluation spec`

Work with scientific and promotion EvaluationSpecs.

```text
exp evaluation spec
```

## `exp evaluation spec create`

Create a comparable EvaluationSpec.

```text
exp evaluation spec create --purpose PURPOSE --dataset NAME --protocol TEXT --metric SPEC --pool ID --budget-hours HOURS [--sealed] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--metric` — declare a metric contract
- `--pool` — select the budget pool
- `--purpose` — select scientific or promotion use
- `--sealed` — seal the protocol now

## `exp experiment`

Operate isolated experiment workspaces and scientific lifecycle.

```text
exp experiment
```

## `exp experiment agent`

Run a fresh code-edit agent in an isolated worktree and commit exact allowlisted changes.

```text
exp experiment agent EXPERIMENT --base SHA --allow GLOB [--prompt PATH|-] [--profile NAME] [--json]
```

Options:

- `--allow` — allow a changed path glob
- `--base` — pin the exact base commit
- `--json` — emit the versioned machine-readable envelope
- `--profile` — override the experiment_implementer profile
- `--prompt` — supply additional implementation instructions

## `exp experiment close`

Atomically conclude an Experiment, complete its Plan, and publish Findings.

```text
exp experiment close --input PATH|- [--json]
```

Options:

- `--input` — read the versioned closure request
- `--json` — emit the versioned machine-readable envelope

## `exp experiment workspace`

Prepare or commit an allowlisted experiment Git worktree.

```text
exp experiment workspace
```

## `exp experiment workspace commit`

Commit only the exact allowlisted experiment change set.

```text
exp experiment workspace commit EXPERIMENT --base SHA --allow GLOB [--json]
```

Options:

- `--allow` — allow a changed path glob
- `--base` — pin the exact base commit
- `--json` — emit the versioned machine-readable envelope

## `exp experiment workspace prepare`

Create an isolated experiment worktree at an exact base commit.

```text
exp experiment workspace prepare EXPERIMENT --base SHA --allow GLOB [--json]
```

Options:

- `--allow` — allow a changed path glob
- `--base` — pin the exact base commit
- `--json` — emit the versioned machine-readable envelope

## `exp idea`

Capture and qualify human or agent research ideas.

```text
exp idea
```

## `exp idea add`

Create an unqueued canonical Idea.

```text
exp idea add --title TITLE --summary TEXT [classification flags] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--lane` — classify exploit or explore
- `--summary` — state the proposed mechanism
- `--title` — set the Idea title

## `exp idea develop`

Ask one fresh agent for a queue-ready Plan proposal.

```text
exp idea develop IDEA [--profile NAME] [--apply] [--json]
```

Options:

- `--apply` — atomically qualify the validated proposal
- `--json` — emit the versioned machine-readable envelope
- `--profile` — override the idea_planner profile

## `exp idea list`

List canonical Ideas.

```text
exp idea list [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp idea qualify`

Atomically turn an Idea into a fully priced Plan.

```text
exp idea qualify IDEA --resource POOL:UNITS:HOURS [payoff and utility flags] [--json]
```

Options:

- `--impact` — estimate impact if successful
- `--json` — emit the versioned machine-readable envelope
- `--probability` — estimate probability of improvement
- `--resource` — price constrained pool use

## `exp init`

Initialize an idempotent v1 experiments root.

```text
exp init
```

## `exp migrate`

Plan or apply an explicit harness-v0 migration.

```text
exp migrate
```

## `exp migrate apply`

Apply one fully reviewed harness-v0 migration plan.

```text
exp migrate apply --plan PATH|- [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--plan` — read the exact reviewed migration plan

## `exp migrate plan`

Build a read-only, fingerprinted harness-v0 migration plan.

```text
exp migrate plan [--source DIR] [--resolutions PATH|-] [--output PATH|-] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--output` — write the complete no-clobber plan to a path or raw stdout
- `--resolutions` — read explicit needs_review resolutions from JSON
- `--source` — set the Git-root-relative harness-v0 source directory

## `exp plan`

Work with priced research Plans.

```text
exp plan
```

## `exp plan add`

Create one validated Plan from human flags or versioned JSON input.

```text
exp plan add [flags | --input -] [--json]
```

Options:

- `--input` — read the versioned Plan request from standard input (must be -)
- `--json` — emit the versioned machine-readable envelope

## `exp plan list`

List canonical Plans without contacting providers.

```text
exp plan list [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp plan refresh`

Repin current Finding beliefs and any canonical Queue revision.

```text
exp plan refresh PLAN [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp policy`

Configure canonical research autonomy and queue policy.

```text
exp policy
```

## `exp policy autonomy`

Change autonomy through the explicit auto-experiment gate.

```text
exp policy autonomy MODE [--confirm-auto-experiment] [--json]
```

Options:

- `--confirm-auto-experiment` — acknowledge assisted or limited dispatch
- `--expected-revision` — require the exact current Policy revision
- `--json` — emit the versioned machine-readable envelope

## `exp policy cluster-set`

Set cluster saturation thresholds or explicitly reopen a direction.

```text
exp policy cluster-set NAME --state STATE [threshold flags] [--json]
```

Options:

- `--budget-hours` — set the cluster budget
- `--expected-revision` — require the exact Policy revision
- `--json` — emit the versioned machine-readable envelope
- `--reopen-condition` — state evidence required to reopen
- `--state` — set open or saturated

## `exp policy init`

Create an explicit default-manual POLICY.md.

```text
exp policy init [taxonomy and allocation flags] [--json]
```

Options:

- `--autonomy` — set the autonomy mode
- `--confirm-auto-experiment` — explicitly enable assisted or limited dispatch
- `--exploit-share` — set the exploit allocation share
- `--explore-share` — set the explore allocation share
- `--json` — emit the versioned machine-readable envelope

## `exp policy show`

Show canonical autonomy and queue policy.

```text
exp policy show [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp pool`

Define constrained compute or human resource pools.

```text
exp pool
```

## `exp pool add`

Create a named ResourcePool.

```text
exp pool add --title TITLE --capacity N --unit UNIT --bottleneck SLUG [--json]
```

Options:

- `--capacity` — set concurrent capacity
- `--json` — emit the versioned machine-readable envelope
- `--title` — set the pool title
- `--unit` — name one capacity unit

## `exp pool list`

List canonical ResourcePools.

```text
exp pool list [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp promotion`

Seal and append human-only production promotion decisions.

```text
exp promotion
```

## `exp promotion append`

Append a human Promotion decision.

```text
exp promotion append --target TARGET --spec ID --challenger ID --evaluation ID --outcome OUTCOME --approved-by HUMAN --confirm [--json]
```

Options:

- `--approved-by` — identify the human approver
- `--challenger` — select the validated Release
- `--confirm` — confirm the exact production decision
- `--json` — emit the versioned machine-readable envelope
- `--outcome` — set accepted, rejected, or rolled_back
- `--target` — select the exact target

## `exp promotion spec-create`

Create a sealed human-gated PromotionSpec.

```text
exp promotion spec-create --target TARGET --evaluation-spec ID --holdout-budget-hours HOURS [--json]
```

Options:

- `--evaluation-spec` — select the sealed holdout protocol
- `--holdout-budget-hours` — bound holdout use
- `--json` — emit the versioned machine-readable envelope
- `--target` — select the production target

## `exp provider`

Run explicit audited reads or controls against supported tools.

```text
exp provider
```

## `exp provider mlflow`

Verify workload-owned MLflow runs without creating them.

```text
exp provider mlflow
```

## `exp provider mlflow verify`

Read only requested metrics and tags from an MLflow run.

```text
exp provider mlflow verify --run-id ID [--metric NAME] [--tag NAME=VALUE] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--metric` — request a metric
- `--run-id` — select the workload-owned run
- `--tag` — verify one expected tag

## `exp provider pueue`

Inspect or explicitly cancel local Pueue tasks.

```text
exp provider pueue
```

## `exp provider pueue cancel`

Explicitly cancel one exact Pueue task.

```text
exp provider pueue cancel TASK --confirm [--json]
```

Options:

- `--confirm` — confirm cancellation
- `--json` — emit the versioned machine-readable envelope

## `exp provider pueue status`

Read a sanitized Pueue scheduler snapshot.

```text
exp provider pueue status [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp queue`

Rank Plans across constrained pool and lane queues.

```text
exp queue
```

## `exp queue create`

Create exploit and explore partitions for ResourcePools.

```text
exp queue create --pool ID [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--pool` — add a ResourcePool

## `exp queue insert`

Score and insert a Plan with optional listwise advice and battles.

```text
exp queue insert QUEUE PLAN [--pool ID] [--agent] [--json]
```

Options:

- `--agent` — run listwise advice and order-swapped battles
- `--json` — emit the versioned machine-readable envelope
- `--pin` — human-pin or override cluster saturation
- `--pool` — select the constrained ResourcePool
- `--position` — override the provisional position
- `--score` — override the transparent score

## `exp queue list`

List canonical Queues.

```text
exp queue list [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp queue remove`

Remove a Plan with an exact Queue CAS.

```text
exp queue remove QUEUE PLAN [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp queue show`

Show ordered pool/lane entries and pinned revisions.

```text
exp queue show QUEUE [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp record`

Inspect or atomically apply canonical records.

```text
exp record
```

## `exp record list`

List Git-backed canonical records.

```text
exp record list [--kind KIND] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--kind` — filter by canonical kind

## `exp record recover`

Roll durable prepared transactions forward.

```text
exp record recover [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp record show`

Show one canonical record.

```text
exp record show REF [--raw|--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--raw` — emit normalized canonical Markdown

## `exp record transaction`

Apply a low-risk Idea/ResourcePool prepared transaction.

```text
exp record transaction --input PATH|- [--json]
```

Options:

- `--input` — read the transaction request
- `--json` — emit the versioned machine-readable envelope

## `exp release`

Assemble typed Candidate slots for downstream targets.

```text
exp release
```

## `exp release create`

Create a draft or atomically validated typed Release.

```text
exp release create --input PATH|- [--json]
```

Options:

- `--input` — read the versioned Release request
- `--json` — emit the versioned machine-readable envelope

## `exp render`

Render deterministic projections or check them without writing.

```text
exp render [--check]
```

Options:

- `--check` — report projection drift without writing

## `exp skill`

Inspect or manage the version-matched embedded guidance skill.

```text
exp skill print|install|check|sync
```

## `exp skill check`

Check installed files, compatibility, manifest hash, and consumer links without mutation.

```text
exp skill check
```

## `exp skill install`

Atomically install the embedded skill and safe consumer links.

```text
exp skill install
```

## `exp skill print`

Print this build's embedded SKILL.md.

```text
exp skill print
```

## `exp skill sync`

Synchronize the generated source-tree command reference for development.

```text
exp skill sync [--check]
```

Options:

- `--check` — report source-tree command-reference drift without writing

## `exp validate`

Validate canonical local records without provider calls.

```text
exp validate [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
