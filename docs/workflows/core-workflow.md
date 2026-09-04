# Core Research Workflow

The core workflow begins with one resolved Project and Source, then chooses a
quick Try, a rigorous Experiment, or promotion. For the dedicated-repository
wizard and complete runtime/profile setup, start with
[Getting Started](../getting-started.md).

## 1. Resolve canonical Project, Source, config, and trust

```bash
SOURCE_REPO="$(git rev-parse --show-toplevel)"
EXP_REPO="$(dirname "$SOURCE_REPO")/$(basename "$SOURCE_REPO")-experiments"

exp --workspace "$EXP_REPO" --source app workspace status
exp --workspace "$EXP_REPO" source status app
exp --workspace "$EXP_REPO" --source app config show
exp --workspace "$EXP_REPO" --source app config explain
exp --workspace "$EXP_REPO" context
```

Canonical records normally live in the dedicated private experiment repository.
Each code/data repository or governed subdirectory is an ordinary canonical
Source; its local clone path is a private association. `--workspace` selects a
Project by UUID/path and `--source` selects a Source by key/ID/display code.
Never treat current-directory convenience, a submodule, config, or provider
catalog as Source authority.

Use `source add` to publish another Source, `source register` to repair a moved
clone, and `config trust` to approve execution-bearing repository configuration
at one exact digest. Configuration never redirects a resolved Project or Source.

## 2. Choose the evidence level

| Question | Route | First command | Required end state |
|---|---|---|---|
| Is this direction worth more work? | Quick exploratory Try | `exp try run` | Human finish/abandon; optional adoption into Idea v2 |
| Should constrained resources test this claim? | Rigorous Experiment | `exp idea add` | Priced Plan, Queue, clean formal Attempt v3, typed Evaluation |
| Should proven work reach a target? | Promotion | `exp candidate create` | Validated Release, sealed fresh holdout, named-human Promotion |

The terminal uses the same sequence: `exp guide quick`, then `exp guide research`,
then `exp guide promotion`.

## 3. Probe uncertainty with a managed native-worktree Try

A clean read-only Try needs only Git:

```bash
exp --workspace "$EXP_REPO" --source app \
  --workspace-backend native_git try run \
  --title "Inspect the clean Source snapshot" \
  --goal "Decide whether this direction merits a formal Experiment" \
  --timeout 5m \
  -- git status --short
```

For a bounded change, add narrow Source-relative `--allow` globs. Dirty Source
state is rejected unless explicitly captured:

```bash
exp --workspace "$EXP_REPO" --source app try run \
  --title "Inspect a captured local edit" \
  --goal "Decide whether to rerun the direction cleanly" \
  --dirty=capture \
  --allow 'src/**' \
  --timeout 5m \
  -- git diff --stat
```

The Try and Attempt v3 are canonical before execution; private job, worktree,
seed, and marker state remain local. Dirty seed bytes stay in XDG cache while
Git receives only bounded paths/summaries/digests. Inspect and conclude using the
returned typed ID:

```bash
TRY_ID='try_replace_with_returned_id'
exp --workspace "$EXP_REPO" try status "$TRY_ID"
exp --workspace "$EXP_REPO" try finish "$TRY_ID" \
  --summary "The direction merits a controlled clean study" \
  --no-results \
  --confirm
exp --workspace "$EXP_REPO" try adopt "$TRY_ID"
```

The last command opens the TTY classification/review wizard. `try resume` only
executes work proven unstarted or imports a verified marker; it never guesses.
A useful Try becomes an Idea, not a Candidate. A dirty Try must be rerun cleanly
through formal execution.

## 4. Capture, qualify, price, and rank a durable direction

Create an Idea only for a direction worth preserving. It may be human-authored,
a child of prior evidence, or produced by `try adopt`; it is not yet a promise
to spend compute.

```bash
exp --workspace "$EXP_REPO" idea add \
  --title "Evaluate cosine decay after warmup" \
  --summary "Measure late-stage optimizer stability" \
  --lane explore \
  --cluster optimizer
IDEA_ID='idea_replace_with_returned_id'

POOL_ID='pool_replace_with_returned_id'
exp --workspace "$EXP_REPO" idea qualify "$IDEA_ID" \
  --payoff-summary "Improve validation macro-F1" \
  --payoff-metric macro_f1 \
  --payoff-unit score \
  --probability 0.45 \
  --impact 0.02 \
  --information-value 0.005 \
  --resource "$POOL_ID":1:3
PLAN_ID='plan_replace_with_returned_id'

QUEUE_ID='queue_replace_with_returned_id'
exp --workspace "$EXP_REPO" queue insert "$QUEUE_ID" "$PLAN_ID" --pool "$POOL_ID"
```

A configured fresh agent may propose the complete Plan with `idea develop`, but
schema/revision/Policy/resource checks remain authoritative. Numeric Queue
insertion is transparent; `--agent` adds listwise advice and order-swapped
adjacent battles. Disagreement or low confidence leaves order unchanged. Plan
revision and Finding belief digest are pinned, so changed evidence makes an
entry stale rather than silently reranking it.

## 5. Dispatch one clean formal evidence unit

An Experiment locks hypothesis, baseline, comparability, success criteria, and
decision rule. A Run is intended evidence; an Attempt is one operational
execution.

For independent Sources, author `exp.runtime/v2` with one writable execution
Source, optional read-only Sources, full base/head commits, exact ChangeSets,
argv, expected outputs, and a Pool/Pueue route. Approve the raw runtime file
using an exact `runtime.dispatch` receipt. A value-free trusted MLflow profile
may be selected for optional observation:

```bash
exp --workspace "$EXP_REPO" config trust
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon frontier
exp --workspace "$EXP_REPO" policy autonomy assisted \
  --confirm-auto-experiment
exp --workspace "$EXP_REPO" --source app \
  --mlflow-profile formal-observer daemon run --interval 5s
```

`daemon frontier` is provider-free. `daemon tick`/`run` need Pueue; native Try
does not. Formal runtime v2 captures only clean exact SourceSnapshots and
passes explicit Project/root/scope authority to worker v2. Dirty, missing,
stale, or untrusted Source state blocks before scheduler execution. Process,
Pueue, or MLflow success is not a scientific conclusion.

See [Runtime Dispatch](runtime-dispatch.md) for the complete v2 JSON and recovery
protocol.

## 6. Close, evaluate, and package exact evidence

Close the Experiment with explicit included/excluded Run dispositions. Then bind
a scientific Evaluation v2 and Candidate v2 to the same successful clean formal
Attempt v3:

```bash
exp --workspace "$EXP_REPO" experiment close --input closure.json --json

EXPERIMENT_ID='exp_replace_with_returned_id'
ATTEMPT_ID='attempt_replace_with_returned_id'
SPEC_ID='evalspec_replace_with_returned_id'

exp --workspace "$EXP_REPO" evaluation create \
  --title "Scientific result" \
  --spec "$SPEC_ID" \
  --subject "$EXPERIMENT_ID" \
  --attempt "$ATTEMPT_ID" \
  --outcome passed \
  --metric macro_f1=0.834:score \
  --summary "Passed the registered threshold"
EVALUATION_ID='eval_replace_with_returned_id'

exp --workspace "$EXP_REPO" candidate create \
  --title "Evaluated Source change" \
  --experiment "$EXPERIMENT_ID" \
  --evaluation "$EVALUATION_ID" \
  --attempt "$ATTEMPT_ID"
```

Candidate v2 requires a supported conclusion that includes the Attempt's Run, a
passing scientific Evaluation v2 bound to that exact Attempt, and clean
SourceSnapshots. It copies exact Source IDs/head commits/ChangeSets. MLflow may
corroborate selected values and ownership, but telemetry/artifacts never replace
Evaluation.

## 7. Branch, promote, and inspect without rewriting history

Create child Ideas for follow-up questions. Independently successful Candidates
are not assumed additive; a multi-Candidate Release needs its own combination
Experiment and passing Evaluation. A validated Release can challenge one target
only through a sealed fresh non-reused holdout and a named human append-only
Promotion. See [Evidence to Promotion](evidence-to-promotion.md).

```bash
exp --workspace "$EXP_REPO" validate
exp --workspace "$EXP_REPO" render --check
exp --workspace "$EXP_REPO" context
exp --workspace "$EXP_REPO" ui
```

`exp ui`, generated projections, and Champion manifests are read-only/derived
views. They cannot merge, push, deploy, approve, or become another source of
truth. Large datasets, model/checkpoint bytes, artifact files, traces, and
unbounded logs remain in MLflow, DVC, or object storage; only bounded summaries,
digests, exact identities, and sanitized references enter Git.
