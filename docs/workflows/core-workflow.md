# Core Research Workflow

## 1. Capture direction without overcommitting

Create an Idea for a durable research direction. It may be human-authored or a
child of earlier evidence, but it is not yet a promise to spend compute.

```bash
exp idea add --title TITLE --summary TEXT --lane exploit --cluster optimizer
```

## 2. Qualify and price the work

Turn the Idea into a Plan only after expected payoff, information value,
assumptions, dependencies, and ResourcePool-hours can be stated. A human can use
`idea qualify`; a configured agent can propose the complete Plan with
`idea develop`, optionally applying it after schema validation.

```bash
exp idea qualify IDEA --resource POOL:UNITS:HOURS [payoff flags]
exp idea develop IDEA --json
exp idea develop IDEA --apply --json
```

## 3. Rank an exact revision

```bash
exp queue insert QUEUE PLAN --pool POOL
exp queue insert QUEUE PLAN --pool POOL --agent
```

The numeric path produces a stable transparent score. `--agent` adds listwise
advice and order-swapped adjacent battles. Disagreement or low confidence leaves
the Queue unchanged for human review. A changed Plan or belief-changing Finding
makes pins stale; refresh, reassess, and insert again.

## 4. Register and execute evidence units

An Experiment records the hypothesis, baseline, comparability specification,
success criteria, and decision rule. Runs describe intended evidence; Attempts
record individual operational executions. Configure the exact runtime boundary
before daemon dispatch.

## 5. Close with explicit evidence dispositions

`exp experiment close --input PATH` records which Runs were included or
excluded, completes the Plan, and publishes scoped Findings atomically. Invalid
or negative results remain useful when their reason and scope are preserved.

## 6. Branch instead of rewriting history

Create child Ideas for follow-up questions. If several Candidates should be
combined, register a combination Experiment rather than assuming their measured
gains are additive. See [evidence to promotion](evidence-to-promotion.md).
