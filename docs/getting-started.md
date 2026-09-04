# Getting Started

This page takes a new user from a code repository to one of three useful
outcomes: a bounded exploratory Try, a queued formal Experiment, or a clean
Candidate ready for the promotion workflow.

## Requirements and optional tools

Required for setup and native Try:

- Git;
- Go 1.26.4 (or the version declared in `go.mod`) and Make when installing from
  this source tree.

Optional, operation-specific tools:

- Pueue 4.x for formal daemon dispatch only;
- MLflow CLI for explicit or profile-selected read-only run observation;
- `dev` for a future optional workspace backend. Its lifecycle is currently
  unsupported; native Git is always available.

```bash
exp doctor
exp doctor --live
```

Default `doctor` performs local executable lookup only. `--live` explicitly runs
bounded version and read-only local-service probes; it never installs, logs in,
writes configuration, starts a service, or executes a workload. Missing MLflow,
Pueue, or `dev` remains visible and does not hide actions or block native Try.
Only an operation that actually needs a missing tool is unavailable.

## Install from source

```bash
git clone https://github.com/daviddwlee84/exp-cli.git
cd exp-cli
make install
exp --version
```

`make install` writes `${PREFIX:-$HOME/.local}/bin/exp` and links the embedded
Agent Skill. Ensure that directory is on `PATH`.

## 1. Create a dedicated experiment repository

The recommended layout separates private canonical research history from the
Source repositories it studies.

### TTY wizard (recommended)

Enter the existing Git repository you want to study and run:

```bash
cd "$HOME/src/my-application"
exp init
```

Bare `exp init` on a terminal defaults to the `dedicated` flow. It proposes a
sibling repository, inspects the initial Source, and reviews the Source
subdirectory, locator risk, exact effects, and paths before any write. It does
not create a remote, commit, submodule, or push.

### Explicit flags

This copy-paste-safe form derives a sibling target from the current Git root:

```bash
SOURCE_REPO="$(git rev-parse --show-toplevel)"
EXP_REPO="$(dirname "$SOURCE_REPO")/$(basename "$SOURCE_REPO")-experiments"

exp init \
  --dedicated-repo "$EXP_REPO" \
  --create \
  --source-repo "$SOURCE_REPO" \
  --source-key app \
  --source-title "Application source" \
  --source-subdir . \
  --source-tag code \
  --name "Application research" \
  --confirm \
  --confirm-local-only
```

`--create` is accepted only for a missing/empty target (or is harmless on a
rerun after that target became an exact Git root). Omit `--confirm-local-only`
when a sanitized remote locator identifies the Source; retaining it is an
explicit acknowledgement for a local-only clone. Initialization publishes
Project v1 and Source v1, registers both private local associations, and
refreshes projections.

To retain the compatibility layout in which code and research share one Git
repository, use the explicit embedded fast path instead:

```bash
exp init --name "Application research"
```

## 2. Register Sources and subdirectories

The initial Source already exists after dedicated initialization. Inspect its
canonical identity and host-local association from either repository:

```bash
exp --workspace "$EXP_REPO" source list
exp --workspace "$EXP_REPO" source show app
exp --workspace "$EXP_REPO" source status app
exp --workspace "$EXP_REPO" --source app workspace status
exp --start-dir "$SOURCE_REPO" context
```

A Source's immutable `subdir` is the governed semantic root inside its Git
clone; `.` means the Git root. Add another repository/subdirectory with either
the TTY wizard:

```bash
exp --workspace "$EXP_REPO" source add
```

or complete explicit flags:

```bash
DATA_REPO="$HOME/src/shared-data"

exp --workspace "$EXP_REPO" source add \
  --key data \
  --title "Shared data definitions" \
  --repo "$DATA_REPO" \
  --subdir datasets \
  --tag data \
  --confirm
```

Add `--confirm-local-only` if that clone has no sanitized remote. When a clone
moves or is recreated, repair only the private association—the canonical Source
does not change:

```bash
exp --workspace "$EXP_REPO" source register data --repo "$DATA_REPO"
exp --workspace "$EXP_REPO" source status data
```

Canonical Source records never contain the local clone path.

## 3. Inspect configuration and exact-digest trust

Configuration is loaded only after Project/Source authority resolves. Inspect
candidate paths, effective non-secret values, winning provenance, and local
receipts:

```bash
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config path
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config show
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config explain
exp config list
```

Repository/Source/subdirectory execution-bearing values are inert until the
exact file digest is approved for the relevant capability. On a terminal, the
safe path is the review wizard:

```bash
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config trust
```

Trust is evaluated in the resolved authority context. With `app` selected, even
the canonical repository layer receives an `app`-scoped receipt; workspace-only
approval does not authorize the same bytes for every Source. For automation, use
the exact path/digest printed by `config explain`, preserve the same selectors,
and name only the capability being approved:

```bash
CONFIG_PATH="$EXP_REPO/.exp-cli/config.toml"
CONFIG_DIGEST='sha256:replace_with_the_current_digest'

exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config trust \
  --path "$CONFIG_PATH" \
  --digest "$CONFIG_DIGEST" \
  --capability mlflow.profile \
  --confirm
```

A byte change requires a new receipt. `exp config list` is sanitized; it does
not reveal Git-common paths or secret values.

## 4. Choose the flow

| Decision | Use | First command | Promotion authority |
|---|---|---|---|
| “Is this direction worth formalizing?” | Quick exploratory Try | `exp try run` | None; finish, then optionally adopt into an Idea |
| “Should scarce compute test this registered claim?” | Rigorous Experiment | `exp idea add`, then qualify and queue | Clean Attempt v3 can support Evaluation v2/Candidate v2 |
| “Should this validated change reach a target?” | Promotion | `exp candidate create`, then Release/holdout | Sealed fresh holdout plus named human only |

Terminal guidance follows the same order:

```bash
exp guide setup
exp guide workspaces
exp guide config
exp guide quick
exp guide research
exp guide promotion
```

## 5. Run clean and dirty Try in a managed native worktree

### Clean, read-only Try

This example needs only Git. A fully explicit command does not prompt:

```bash
exp --workspace "$EXP_REPO" \
  --source app \
  --workspace-backend native_git \
  try run \
  --title "Inspect the clean Source snapshot" \
  --goal "Confirm the registered checkout is ready for a controlled probe" \
  --timeout 5m \
  -- git status --short
```

The Try and planned Attempt v3 become canonical before execution. `exp` creates
a managed native Git worktree under XDG data, claims a private job, invokes argv
directly, and imports a bounded terminal result. No shell interprets arguments
after `--`. An empty `--allow` set makes workload changes read-only.

For a bounded code-changing probe, add narrow Source-relative allowlists:

```bash
exp --workspace "$EXP_REPO" --source app try run \
  --title "Probe cosine decay" \
  --goal "Decide whether the optimizer direction merits a formal Experiment" \
  --allow 'src/**' \
  --allow 'configs/**' \
  --timeout 20m \
  -- project-runner --config configs/probe.toml
```

### Explicit dirty capture

Dirty state is rejected unless the literal `--dirty=capture` is supplied. If
an intended local edit is under `src/`, a read-only inspection of its captured
replay is:

```bash
exp --workspace "$EXP_REPO" --source app try run \
  --title "Inspect the captured local edit" \
  --goal "Decide whether the dirty direction deserves a clean rerun" \
  --dirty=capture \
  --allow 'src/**' \
  --timeout 5m \
  -- git diff --stat
```

Capture is Try-only and bounded. Raw patch/untracked bytes remain in an
authenticated private XDG cache seed; canonical Git records contain only the
dirty summary, sorted paths, and digests. A dirty Try cannot back Candidate v2.

### Inspect, recover, finish, and adopt

Use the exact typed ID returned by `try run`:

```bash
TRY_ID='try_replace_with_returned_id'

exp --workspace "$EXP_REPO" try status "$TRY_ID"
exp --workspace "$EXP_REPO" try resume "$TRY_ID"
```

`resume` may run only an Attempt proven unstarted or import a verified durable
marker; it never guesses and re-executes uncertain work. Finish after every
owned Attempt is terminal, selecting owned results or explicitly none:

```bash
exp --workspace "$EXP_REPO" try finish "$TRY_ID" \
  --summary "The direction merits a registered clean study" \
  --no-results \
  --confirm
```

Adopt the reviewed conclusion into Idea v2 either with `exp try adopt "$TRY_ID"`
and its TTY wizard or explicitly:

```bash
exp --workspace "$EXP_REPO" try adopt "$TRY_ID" \
  --title "Evaluate cosine decay cleanly" \
  --summary "Run the direction under a registered comparable protocol" \
  --proposed-by 'human:alice' \
  --cluster optimizer \
  --domain modeling \
  --work training \
  --method optimization \
  --component scheduler \
  --lane explore \
  --risk medium \
  --horizon short \
  --origin hybrid \
  --confirm
```

Cleanup is separate and never removes canonical records, the retained branch,
or terminal markers:

```bash
exp --workspace "$EXP_REPO" try cleanup "$TRY_ID" --confirm
```

## 6. Configure a value-free MLflow profile

Create a canonical profile for optional read-only observation. A formal Pueue
run requires a **value-free** profile because Pueue persists task environments;
the workload must obtain credentials from its own broker.

```bash
CONFIG_PATH="$EXP_REPO/.exp-cli/config.toml"
mkdir -p "$(dirname "$CONFIG_PATH")"
cat >"$CONFIG_PATH" <<'TOML'
schema = "exp.config/v1"

[defaults]
mlflow_profile = "formal-observer"

[mlflow.profiles.formal-observer]
context = "research"
binary = "mlflow"
timeout = "30s"
default_metrics = ["macro_f1", "validation_loss"]
TOML

exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config explain
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config trust
```

In the trust wizard, select this file and approve `mlflow.profile` after review.
Direct Try may instead use a trusted profile with environment-name bindings;
values are resolved only at process start and redacted. Formal runtime v2
rejects such bindings. Missing MLflow records optional observation as
unavailable and does not reverse workload success.

## 7. Price and queue formal work

Policy starts in `manual`, so setup cannot dispatch by accident:

```bash
exp --workspace "$EXP_REPO" policy init

exp --workspace "$EXP_REPO" pool add \
  --title "Local GPUs" \
  --capacity 2 \
  --unit gpu \
  --bottleneck accelerator

POOL_ID='pool_replace_with_returned_id'
exp --workspace "$EXP_REPO" queue create --pool "$POOL_ID"
QUEUE_ID='queue_replace_with_returned_id'

exp --workspace "$EXP_REPO" idea add \
  --title "Evaluate cosine decay after warmup" \
  --summary "Measure late-stage optimizer stability" \
  --lane explore \
  --cluster optimizer
IDEA_ID='idea_replace_with_returned_id'

exp --workspace "$EXP_REPO" idea qualify "$IDEA_ID" \
  --payoff-summary "Improve validation macro-F1" \
  --payoff-metric macro_f1 \
  --payoff-unit score \
  --probability 0.45 \
  --impact 0.02 \
  --information-value 0.005 \
  --resource "$POOL_ID":1:3
PLAN_ID='plan_replace_with_returned_id'

exp --workspace "$EXP_REPO" queue insert "$QUEUE_ID" "$PLAN_ID" --pool "$POOL_ID"
exp --workspace "$EXP_REPO" context
```

Replace each quoted placeholder with the complete typed ID printed immediately
above it. Queue order pins the Plan revision and Finding beliefs used to rank it.

## 8. Configure runtime v2 and run the daemon

Runtime v2 names one writable execution Source, optional read-only Sources, exact
full commits/ChangeSets, argv, expected outputs, and a Pool-to-Pueue route. It is
strict JSON, separate from layered config.

First obtain exact identities:

```bash
exp --workspace "$EXP_REPO" source show app
SOURCE_ID='src_replace_with_returned_id'
BASE_COMMIT='replace_with_full_lower_case_base_commit'
HEAD_COMMIT='replace_with_full_lower_case_head_commit'
RUNNER='/absolute/path/to/project-runner'
RUNTIME_PATH="$EXP_REPO/.exp/runtime.json"
mkdir -p "$(dirname "$RUNTIME_PATH")"
```

The following example describes one committed change. Replace the quoted IDs,
commits, runner, and exact Git-root-relative ChangeSet before writing it:

```bash
cat >"$RUNTIME_PATH" <<EOF
{
  "schema_version": "exp.runtime/v2",
  "pools": {
    "$POOL_ID": {
      "pueue_group": "gpu",
      "label_prefix": "exp-gpu-"
    }
  },
  "plans": {
    "$PLAN_ID": {
      "execution_source": "$SOURCE_ID",
      "read_only_sources": [],
      "executable": "$RUNNER",
      "argv": ["--config", "configs/cosine.toml"],
      "checkout": "managed_worktree",
      "cwd": ".",
      "timeout": "3h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "$BASE_COMMIT",
      "head_commit": "$HEAD_COMMIT",
      "change_set": ["configs/cosine.toml"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
EOF
```

For a no-change observation use equal base/head, an empty `change_set`, and
`"observational_no_change": true`. Never put credentials in runtime JSON.

Review and trust the raw runtime file digest for `runtime.dispatch`. The TTY
wizard discovers `.exp/runtime.json` separately from layered config:

```bash
exp --start-dir "$SOURCE_REPO" --workspace "$EXP_REPO" --source app config trust
exp --workspace "$EXP_REPO" config list
```

Ensure the named Pueue group exists, then validate the canonical frontier
without contacting Pueue. Explicitly enable formal experiment dispatch and run
one pass or the continuous daemon:

```bash
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon frontier

exp --workspace "$EXP_REPO" policy autonomy assisted \
  --confirm-auto-experiment

exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon tick

exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon run --interval 5s
```

Stop the foreground `daemon run` with the terminal interrupt. Runtime v2 admits
only clean, exact SourceSnapshots; dirty/missing/stale/untrusted state blocks
before scheduler execution. Missing Pueue blocks daemon dispatch but never
native Try.

The optional `dev_cli` backend is discoverable with:

```bash
exp workspace backend list --probe
exp --workspace "$EXP_REPO" workspace backend status --probe
```

Its prepare/open/handoff/retire lifecycle is compiled unsupported until `dev`
provides schema-versioned exact-path machine capability receipts. Actions remain
visible; trusted fallback may select `native_git`, which retains byte,
inspection, and cleanup authority.

## 9. Create Evaluation v2 and Candidate v2

After the formal Attempt succeeds, close the Experiment with explicit included/
excluded Run dispositions and a supported scientific conclusion:

```bash
exp --workspace "$EXP_REPO" experiment close --input closure.json --json

EXPERIMENT_ID='exp_replace_with_returned_id'
ATTEMPT_ID='attempt_replace_with_returned_id'

exp --workspace "$EXP_REPO" evaluation spec create \
  --title "Scientific validation" \
  --purpose scientific \
  --dataset validation-v3 \
  --protocol "fixed preprocessing and five seeds" \
  --metric macro_f1:score:maximize:0.82 \
  --pool "$POOL_ID" \
  --budget-hours 4
EVALUATION_SPEC_ID='evalspec_replace_with_returned_id'

exp --workspace "$EXP_REPO" evaluation create \
  --title "Scientific result" \
  --spec "$EVALUATION_SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --attempt "$ATTEMPT_ID" \
  --outcome passed \
  --metric macro_f1=0.834:score \
  --summary "Passed the registered scientific threshold"
EVALUATION_ID='eval_replace_with_returned_id'

exp --workspace "$EXP_REPO" candidate create \
  --title "Evaluated Source change" \
  --experiment "$EXPERIMENT_ID" \
  --evaluation "$EVALUATION_ID" \
  --attempt "$ATTEMPT_ID" \
  --json
```

Supplying `--attempt` creates Evaluation v2 only when it is the exact successful
clean formal Attempt v3 for a Run in that Experiment. Candidate v2 requires the
same Attempt, a supported conclusion that includes its Run, a passing scientific
Evaluation v2, and clean snapshots; it copies exact Source IDs, head commits, and
ChangeSets. An optional MLflow attachment may corroborate selected values:

```bash
MLFLOW_RUN_ID='replace_with_workload_owned_run_id'
exp --workspace "$EXP_REPO" --mlflow-profile formal-observer \
  evaluation create \
  --title "Scientific result with MLflow corroboration" \
  --spec "$EVALUATION_SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --attempt "$ATTEMPT_ID" \
  --outcome passed \
  --metric macro_f1=0.834:score \
  --mlflow-run-id "$MLFLOW_RUN_ID" \
  --mlflow-tag "exp.attempt_id=$ATTEMPT_ID" \
  --summary "Passed with exact Attempt ownership"
```

MLflow never replaces the Evaluation. Continue with
[Evidence to Promotion](workflows/evidence-to-promotion.md) for validated
Releases, a sealed fresh holdout, and the named-human production gate.

## 10. Resume with context, help, completion, and UI

```bash
exp --workspace "$EXP_REPO" context
exp guide
exp guide quick
exp --help
exp try --help
exp completion --help
```

For temporary completion in the current shell:

```bash
# Bash
source <(exp completion bash)

# Zsh (run in zsh instead)
source <(exp completion zsh)
```

Completion reads only local records, associations, and configuration; it does
not probe providers or resolve secret values. It completes Source keys, typed
record IDs, workspaces, profiles, and guide topics.

Open the local read-only terminal UI with:

```bash
exp --workspace "$EXP_REPO" ui
```

Startup reads canonical records, associations, effective config summaries, and
the operation database locally. Only an explicit readiness tab/refresh runs
bounded live probes. No UI action mutates records/trust, executes work,
installs/logs in, starts a service, opens an editor, or hands off a workspace.
There is no UI JSON mode; automation should use `exp context --json` and
command-specific envelopes.

## Keep large bytes in their owning systems

Large datasets, model checkpoints, artifact files, traces, and unbounded logs
stay in MLflow, DVC, or object storage. Canonical Git history receives only
bounded summaries, cryptographic digests, exact Source/commit identities, and
sanitized provider references. Credentials, raw environments, host-local paths,
large bytes, and unbounded provider output never enter canonical records.

Next read [Core Research Workflow](workflows/core-workflow.md),
[Runtime Dispatch](workflows/runtime-dispatch.md), and the
[Command Map](reference/command-map.md).
