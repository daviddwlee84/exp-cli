<!-- generated from exp command metadata; do not edit -->

# Current `exp` commands

This reference contains only the command metadata supplied by this build's CLI layer. It is not a roadmap for deferred commands.

## `exp`

Use the Git-native research control plane.

```text
exp [--skill] [--start-dir DIR] [--workspace PROJECT|PATH] [--source SOURCE] [--workspace-backend BACKEND] [--mlflow-profile PROFILE]
```

Options:

- `--mlflow-profile` — select one trusted named MLflow profile
- `--skill` — print this build's embedded SKILL.md
- `--source` — select a canonical Source
- `--start-dir` — set the physical invocation directory
- `--workspace` — select a canonical workspace by Project UUID or path
- `--workspace-backend` — override native_git or dev_cli for this invocation

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

Create Candidate v2 from a clean formal Attempt, or infer the compatible Candidate v1 flag shape.

```text
exp candidate create --title TITLE --experiment ID --evaluation ID --attempt ATTEMPT [--json] | exp candidate create --title TITLE --experiment ID --evaluation ID --git-commit SHA --change PATH [--legacy] [--json]
```

Options:

- `--attempt` — select the clean successful formal Attempt
- `--change` — add a legacy exact changed path
- `--evaluation` — select its passing scientific Evaluation
- `--experiment` — select the supported Experiment
- `--git-commit` — pin the legacy full Git object ID
- `--json` — emit the versioned machine-readable envelope
- `--legacy` — explicitly select Candidate v1 (legacy fields also infer it)
- `--title` — set the Candidate title

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

## `exp config`

Inspect layered configuration and manage exact-digest trust.

```text
exp config
```

## `exp config explain`

Explain layer and trust provenance for effective fields.

```text
exp config explain [FIELD] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp config list`

List sanitized local trust receipts.

```text
exp config list [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp config path`

Show candidate and applied configuration paths.

```text
exp config path [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp config revoke`

Revoke trust for a current or deleted config scope.

```text
exp config revoke --path PATH [--digest SHA256] [--capability CAPABILITY] --confirm [--json]
```

Options:

- `--capability` — limit revocation to a capability
- `--confirm` — confirm without prompting
- `--digest` — limit revocation to one digest
- `--json` — emit the versioned machine-readable envelope
- `--path` — select a current or deleted repository config

## `exp config show`

Show effective non-secret configuration and safe provenance.

```text
exp config show [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp config trust`

Approve current execution-bearing config by exact digest, with a TTY review wizard for missing fields.

```text
exp config trust [--path PATH --digest SHA256 --capability CAPABILITY --confirm] [--json]
```

Options:

- `--capability` — approve an execution-bearing capability
- `--confirm` — confirm without prompting
- `--digest` — require its exact current digest
- `--json` — emit the versioned machine-readable envelope
- `--path` — select one applied repository config

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
- `--live` — run short version and explicit read-only local/service probes

## `exp evaluation`

Define comparable protocols and record immutable results.

```text
exp evaluation
```

## `exp evaluation create`

Record one immutable Evaluation with optional exact MLflow ownership verification.

```text
exp evaluation create --title TITLE --spec ID --subject ID [--attempt ATTEMPT] --outcome OUTCOME --metric VALUE --summary TEXT [--mlflow-run-id ID --mlflow-tag exp.attempt_id=ATTEMPT] [--json]
```

Options:

- `--allow-env` — compatibility-mode non-secret parent binding
- `--attempt` — bind an Experiment Evaluation to its exact formal Attempt
- `--json` — emit the versioned machine-readable envelope
- `--metric` — record a declared metric
- `--mlflow-context` — override context only in compatibility mode
- `--mlflow-run-id` — select one workload-owned MLflow run
- `--mlflow-tag` — assert one selected MLflow tag
- `--outcome` — set passed, failed, or invalid
- `--secret-env` — compatibility-mode required secret binding
- `--spec` — select the EvaluationSpec
- `--subject` — select the evaluated subject
- `--summary` — summarize the bounded result
- `--title` — set the Evaluation title

## `exp evaluation spec`

Work with scientific and promotion EvaluationSpecs.

```text
exp evaluation spec
```

## `exp evaluation spec create`

Create a comparable EvaluationSpec.

```text
exp evaluation spec create --title TITLE --purpose PURPOSE --dataset NAME --protocol TEXT --metric SPEC --pool ID --budget-hours HOURS [--sealed] [--json]
```

Options:

- `--budget-hours` — set the finite evaluation budget
- `--dataset` — identify the frozen dataset or split
- `--json` — emit the versioned machine-readable envelope
- `--metric` — declare a metric contract
- `--pool` — select the budget pool
- `--protocol` — describe the comparable protocol
- `--purpose` — select scientific or promotion use
- `--sealed` — seal the protocol now
- `--title` — set the EvaluationSpec title

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

## `exp history`

Search descriptions and evidence across canonical records.

```text
exp history
```

## `exp history search`

Search canonical descriptions, tags, versions and artifact names using a rebuildable SQLite cache.

```text
exp history search [WORDS...] [--all] [--kind KIND] [--source-key SOURCE] [--version VERSION] [--state STATE] [--after DATE] [--before DATE] [--limit N] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

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

Initialize a project; the TTY wizard defaults to a separate private experiment repository.

```text
exp init [--name NAME] [--dedicated-repo DIR [--create] --source-repo DIR --source-key KEY --confirm] [--json]
```

Options:

- `--confirm` — confirm the dedicated plan
- `--confirm-local-only` — confirm an initial Source without a remote locator
- `--create` — git-init only a missing or empty dedicated target
- `--dedicated-repo` — adopt an exact dedicated Git repository root
- `--json` — emit the versioned machine-readable envelope
- `--name` — set the project name
- `--source-key` — set the initial Source key
- `--source-locator` — add a sanitized locator hint
- `--source-repo` — bind an existing Source clone
- `--source-subdir` — bind a Source subdirectory
- `--source-tag` — add an initial Source tag
- `--source-title` — set the initial Source title

## `exp input`

Bind external inputs without copying data into Git.

```text
exp input
```

## `exp input bind`

Remember a private external data binding and inspect its content identity.

```text
exp input bind NAME PATH [--global] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp input list`

Inspect effective host-local input bindings.

```text
exp input list [--global] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

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
exp migrate plan [--legacy-source DIR | --source DIR] [--resolutions PATH|-] [--output PATH|-] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--legacy-source` — set the Git-root-relative harness-v0 source directory
- `--output` — write the complete no-clobber plan to a path or raw stdout
- `--resolutions` — read explicit needs_review resolutions from JSON
- `--source` — compatibility alias for --legacy-source on migrate plan

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
exp promotion append --title TITLE --target TARGET --spec ID --challenger ID --evaluation ID --outcome OUTCOME --approved-by HUMAN --confirm [--json]
```

Options:

- `--approved-by` — identify the human approver
- `--challenger` — select the validated Release
- `--confirm` — confirm the exact production decision
- `--evaluation` — select the sealed holdout Evaluation
- `--json` — emit the versioned machine-readable envelope
- `--outcome` — set accepted, rejected, or rolled_back
- `--spec` — select the PromotionSpec
- `--target` — select the exact target
- `--title` — set the Promotion event title

## `exp promotion spec-create`

Create a sealed human-gated PromotionSpec.

```text
exp promotion spec-create --title TITLE --target TARGET --evaluation-spec ID --holdout-budget-hours HOURS [--json]
```

Options:

- `--evaluation-spec` — select the sealed holdout protocol
- `--holdout-budget-hours` — bound holdout use
- `--json` — emit the versioned machine-readable envelope
- `--target` — select the production target
- `--title` — set the PromotionSpec title

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

Read only selected metrics and tags through a trusted profile or explicit compatibility bindings.

```text
exp provider mlflow verify --run-id ID [--metric NAME] [--tag NAME=VALUE] [--mlflow-profile PROFILE | --mlflow-context CONTEXT --allow-env NAME --secret-env NAME] [--json]
```

Options:

- `--allow-env` — compatibility-mode non-secret parent binding
- `--json` — emit the versioned machine-readable envelope
- `--metric` — request a metric (profile defaults apply when omitted)
- `--mlflow-context` — override context only in compatibility mode
- `--run-id` — select the workload-owned run
- `--secret-env` — compatibility-mode required secret binding
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
exp render [--check] [--json]
```

Options:

- `--check` — report projection drift without writing
- `--json` — emit the versioned machine-readable envelope

## `exp results`

Locate and retrieve artifacts using exact Try and Attempt ownership.

```text
exp results
```

## `exp results compare`

Compare named outputs and the exact runner identities that produced them.

```text
exp results compare TRY|ATTEMPT TRY|ATTEMPT... [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp results describe`

Annotate one artifact without changing its content identity.

```text
exp results describe ATTEMPT NAME --description TEXT [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp results fetch`

Retrieve one unambiguous named artifact and verify its digest.

```text
exp results fetch TRY|ATTEMPT NAME [--destination PATH] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp results list`

List artifact identities and provider-free local availability.

```text
exp results list TRY|ATTEMPT [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp results open`

Retrieve and verify one named artifact, then invoke the desktop file opener.

```text
exp results open TRY|ATTEMPT NAME [--destination PATH] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp results save`

Retry pending artifact publication without re-executing the workload.

```text
exp results save TRY|ATTEMPT [--allow-large] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp runner`

Configure portable project runner commands and version identity.

```text
exp runner
```

## `exp runner add`

Remember project CLI argv and optional JSON version and lockfile probes.

```text
exp runner add NAME [--version-arg ARG] [--environment-file PATH] [--json] -- COMMAND [ARG...]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp runner list`

List configured runner profiles.

```text
exp runner list [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp runner use`

Remember the current Project or global runner preference.

```text
exp runner use NAME [--global] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp skill`

Inspect or manage the version-matched embedded guidance skill.

```text
exp skill print|install|check|sync
```

## `exp skill check`

Check installed files, compatibility, manifest hash, and consumer links without mutation.

```text
exp skill check [--dir DIR] [--links] [--json]
```

Options:

- `--dir` — set the skill destination
- `--json` — emit the versioned machine-readable envelope
- `--links` — also check supported consumer links

## `exp skill install`

Atomically install the embedded skill and safe consumer links.

```text
exp skill install [--dir DIR] [--link] [--json]
```

Options:

- `--dir` — set the skill destination
- `--json` — emit the versioned machine-readable envelope
- `--link` — create safe supported consumer links

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

## `exp source`

Manage canonical Git Source bindings and local clone associations.

```text
exp source
```

## `exp source add`

Publish and associate a planned Source; a TTY wizard can gather missing fields.

```text
exp source add [--key KEY --repo DIR] [--title TITLE] [--subdir DIR] [--locator URL] [--tag TAG] [--confirm] [--confirm-local-only] [--json]
```

Options:

- `--confirm` — confirm canonical and local effects
- `--confirm-local-only` — confirm a clone with no remote locator
- `--json` — emit the versioned machine-readable envelope
- `--key` — set the Source key
- `--locator` — add a sanitized locator hint
- `--repo` — inspect an existing Git clone
- `--subdir` — bind a Git-root-relative subdirectory
- `--tag` — add a Source tag
- `--title` — set the Source title

## `exp source append-locator`

Append a sanitized Source locator through exact-revision CAS.

```text
exp source append-locator [SOURCE] --locator URL --expected-revision SHA256 --confirm [--json]
```

Options:

- `--confirm` — confirm the locator update
- `--expected-revision` — require the exact Source revision
- `--json` — emit the versioned machine-readable envelope
- `--locator` — append a sanitized locator hint

## `exp source list`

List canonical Sources without provider access.

```text
exp source list [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp source register`

Register or repair a Source clone association, with TTY review when fields are missing.

```text
exp source register [SOURCE] [--repo DIR] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--repo` — inspect and register an existing Git clone

## `exp source retire`

Retire an active Source through exact-revision CAS.

```text
exp source retire [SOURCE] --expected-revision SHA256 --confirm [--json]
```

Options:

- `--confirm` — confirm retirement
- `--expected-revision` — require the exact Source revision
- `--json` — emit the versioned machine-readable envelope

## `exp source show`

Show one canonical Source.

```text
exp source show [SOURCE] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp source status`

Validate local associations for canonical Sources.

```text
exp source status [SOURCE] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp storage`

Configure host-local artifact storage preferences.

```text
exp storage
```

## `exp storage add`

Remember storage routing; MLflow metadata and artifact bytes use separate stores.

```text
exp storage add NAME --root DIR [--kind local|mlflow-local|mlflow-remote] [--tracking-uri URI] [--python BINARY] [--token-env NAME] [--large-bytes N] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp storage show`

Inspect private storage profiles, effective preference and its origin.

```text
exp storage show [--global] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp storage use`

Persist the storage default for the current Project or globally.

```text
exp storage use NAME [--global] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp try`

Run bounded exploratory work in managed native Git worktrees.

```text
exp try
```

## `exp try abandon`

Record a human reason for abandoning an open Try.

```text
exp try abandon TRY --reason TEXT --confirm [--json]
```

Options:

- `--confirm` — confirm without prompting
- `--json` — emit the versioned machine-readable envelope
- `--reason` — record the abandonment reason

## `exp try adopt`

Atomically adopt a reviewed concluded Try as an Idea v2; a TTY wizard supplies safe classification defaults.

```text
exp try adopt [TRY] [--title TITLE --summary TEXT --proposed-by HUMAN] [classification flags] [--confirm] [--json]
```

Options:

- `--body` — set optional Idea Markdown detail
- `--cluster` — set the primary cluster
- `--component` — set the component slug
- `--confirm` — confirm a fully explicit adoption without prompting
- `--domain` — set the domain slug
- `--horizon` — set short, medium, or long horizon
- `--json` — emit the versioned machine-readable envelope without prompting
- `--lane` — classify exploit or explore
- `--method` — set the method slug
- `--origin` — set human or hybrid origin
- `--parent` — link a parent Idea
- `--proposed-by` — identify the confirming human
- `--risk` — set low, medium, or high risk
- `--summary` — state the formal direction
- `--tags` — set canonical Idea tags
- `--title` — set the Idea title
- `--work` — set the work-class slug

## `exp try cleanup`

Remove only verified manager-owned worktrees and seed bundles.

```text
exp try cleanup TRY --confirm [--json]
```

Options:

- `--confirm` — confirm safe local cleanup
- `--json` — emit the versioned machine-readable envelope

## `exp try exec`

Append a new Attempt with independent managed outputs, input identities and runner provenance.

```text
exp try exec TRY [--storage PROFILE] [--runner PROFILE] [--input NAME] [--dirty=capture] [--allow GLOB] [--json] -- [COMMAND [ARG...]]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp try finish`

Conclude a Try with attributed observations; agent conclusions remain explicitly unreviewed.

```text
exp try finish TRY --summary TEXT [--author human|agent] [--saved-results|--result-digest SHA256|--external-ref JSON|--no-results] [--confirm] [--json]
```

Options:

- `--confirm` — confirm without prompting
- `--external-ref` — select an ExternalRef
- `--json` — emit the versioned machine-readable envelope
- `--no-results` — explicitly select no results
- `--result-digest` — select a result digest
- `--summary` — record the human conclusion

## `exp try list`

List a bounded page of canonical Tries and Attempt counts.

```text
exp try list [--limit N] [--offset N] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--limit` — limit rows to 1..1000
- `--offset` — skip rows before this page

## `exp try reconcile`

Import late evidence or explicitly abandon one uncertain Attempt.

```text
exp try reconcile TRY --attempt ATTEMPT --abandon-uncertain --reason TEXT --confirm [--json]
```

Options:

- `--abandon-uncertain` — record a durable terminal disposition when no marker or live lease exists
- `--attempt` — select the exact uncertain Attempt
- `--confirm` — confirm without prompting
- `--json` — emit the versioned machine-readable envelope
- `--reason` — record the human reconciliation reason

## `exp try resume`

Resume provably unstarted work or import a durable marker.

```text
exp try resume TRY [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp try retry`

Retry a terminal Attempt with identical Source and argv identity.

```text
exp try retry TRY [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp try root`

Inspect or set the adhoc collection root without moving previous explorations.

```text
exp try root [PATH] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp try run`

Run a bounded Try; opt into managed artifact outputs or use try start/exec for continuing exploration.

```text
exp try run --title TITLE --goal GOAL [--outputs] [--storage PROFILE] [--runner PROFILE] [--input NAME] [--dirty=capture] [--allow GLOB] [--json] -- COMMAND [ARG...]
```

Options:

- `--allow` — allow a Source-relative changed path glob
- `--body` — set optional canonical Markdown detail
- `--dirty` — explicitly capture dirty Source state
- `--goal` — state the bounded goal
- `--json` — emit the versioned machine-readable envelope without prompting
- `--tags` — set canonical Try tags
- `--timeout` — bound command runtime
- `--title` — set the Try title

## `exp try show`

Show one Try with canonical and local direct status.

```text
exp try show TRY [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp try start`

Create an open Try and optionally an isolated small Git Source for adhoc work.

```text
exp try start --title TITLE --goal GOAL [--scratch] [--tags TAGS] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp try status`

Inspect a bounded page of Attempts, jobs, markers, and worktrees without executing.

```text
exp try status [TRY] [--limit N] [--offset N] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--limit` — limit unfiltered rows to 1..1000
- `--offset` — skip unfiltered rows before this page

## `exp try summarize`

Save an attributed working summary without changing the Try lifecycle.

```text
exp try summarize TRY --summary TEXT [--author agent|human] [--json]
```

Options:

- `--json` — emit one exp.cli/v1 machine-readable JSON envelope

## `exp ui`

Open the read-only local research TUI on real terminal stdin and stdout.

```text
exp ui
```

## `exp validate`

Validate canonical local records without provider calls.

```text
exp validate [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp workspace`

Register or inspect canonical workspace associations.

```text
exp workspace
```

## `exp workspace backend`

Inspect workspace provider capabilities and selection.

```text
exp workspace backend
```

## `exp workspace backend handoff`

Open one exact managed Try Attempt through the selected provider.

```text
exp workspace backend handoff TRY --attempt ATTEMPT [--workspace-backend BACKEND] [--json]
```

Options:

- `--attempt` — select the exact canonical Attempt
- `--json` — emit the versioned machine-readable envelope
- `--workspace-backend` — override native_git or dev_cli for this invocation

## `exp workspace backend list`

List built-in and optional workspace providers.

```text
exp workspace backend list [--probe] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--probe` — run bounded local compatibility probes

## `exp workspace backend status`

Show requested and actual workspace preparation providers.

```text
exp workspace backend status [--probe] [--workspace-backend BACKEND] [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--probe` — run the bounded local compatibility probe
- `--workspace-backend` — override native_git or dev_cli for this invocation

## `exp workspace register`

Register the resolved canonical workspace on this host.

```text
exp workspace register [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp workspace status`

Show resolved workspace, Source, association, and config status.

```text
exp workspace status [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
