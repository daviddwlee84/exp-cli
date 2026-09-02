# Getting Started

## Requirements

- Git
- Go 1.26.4 or the version declared in `go.mod`
- Make for the repository build targets
- Pueue 4.x only when using daemon dispatch
- MLflow CLI only when verifying workload-owned runs

`exp doctor` reports which optional provider binaries are locally discoverable.
It does not contact providers by default.

## Install from source

```bash
git clone https://github.com/daviddwlee84/exp-cli.git
cd exp-cli
make install
exp --version
```

`make install` writes the binary to `${PREFIX:-$HOME/.local}/bin` and links the
embedded Agent Skill. Ensure that directory is on `PATH`.

## Initialize a research project

Run these commands inside an existing Git repository:

```bash
exp init --name "Encoder research"
exp policy init

exp pool add \
  --title "Local GPUs" \
  --capacity 2 \
  --unit gpu \
  --bottleneck accelerator
```

The first two commands create the canonical `experiments/` root and an explicit
manual policy. Keep the real ResourcePool ID printed by `pool add`:

```bash
POOL_ID='pool_...'
exp queue create --pool "$POOL_ID"
QUEUE_ID='queue_...'
```

## Capture and qualify an Idea

```bash
exp idea add \
  --title "Try cosine decay after warmup" \
  --summary "Reduce late-stage optimizer noise" \
  --lane exploit \
  --cluster optimizer

IDEA_ID='idea_...'
exp idea qualify "$IDEA_ID" \
  --payoff-summary "Improve validation macro-F1" \
  --payoff-metric macro_f1 \
  --payoff-unit score \
  --probability 0.45 \
  --impact 0.02 \
  --information-value 0.005 \
  --resource "$POOL_ID":1:3
```

Insert the returned Plan into the Queue and inspect the local context:

```bash
PLAN_ID='plan_...'
exp queue insert "$QUEUE_ID" "$PLAN_ID" --pool "$POOL_ID"
exp context
exp validate
```

Nothing dispatches while Policy is in `manual` or `shadow`. Continue with the
[core research workflow](workflows/core-workflow.md), or configure the exact
[runtime dispatch contract](workflows/runtime-dispatch.md) before enabling
assisted automation.

## Machine-readable use

Commands that advertise `--json` emit one versioned JSON envelope on stdout.
Keep stderr separate and use returned typed IDs rather than parsing human text.
See the [command map](reference/command-map.md) for the main command families.
