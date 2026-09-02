# Research Principles

The control plane is useful only when the research method behind it is sound.
These principles define how `exp` expects work to be framed and interpreted.

## Start with a decision and expected payoff

An experiment is justified by a decision it could improve, not novelty alone.
State the metric and unit, plausible benefit, information value, resource cost,
assumptions, and what result would make the direction a dead end. Keep genuine
exploration in the `explore` lane instead of disguising it as exploitation.

## Register the design before evidence

Before the first evidence-producing Attempt, record the falsifiable hypothesis,
baseline, intended changes, comparability requirements, success criteria, and
decision rule. A result-driven threshold or baseline change belongs in a dated
amendment or a new Experiment—not a rewritten history.

## Compare only comparable evidence

Dataset identity, preprocessing, metrics, seeds, stopping rules, runtime limits,
hardware, and dependency versions can all invalidate a comparison. A Run is an
intended evidence unit; an Attempt is one operational execution. Retrying an
infrastructure failure is not a new scientific question.

## Separate negative, inconclusive, and invalid outcomes

- **Refuted:** comparable evidence contradicts the registered hypothesis.
- **Inconclusive:** valid evidence exists but does not resolve the question.
- **Invalid:** leakage, protocol violations, or failed comparability mean the
  evidence cannot answer the question.
- **Operational failure:** a process failed, timed out, was preempted, or ran
  out of memory. This is not a scientific verdict.

A successful exit code proves process completion, not scientific validity.

## Preserve dead ends and scope

Record why a direction should not be retried and what would need to change.
Evidence-backed limits become Findings; recurring operational remedies belong
in project pitfalls; future actions belong in TODO or backlog. Do not upgrade
an anecdote into a Finding or bury a scientific result in troubleshooting prose.

## Follow branches and test combinations

Useful outcomes may create several child Ideas. Price each branch independently
and keep its parent edges. Independently successful Candidates are not assumed
to be additive: a combined Release needs its own combination Experiment and
passing Evaluation.

## Keep promotion human-gated

Scientific Evaluation decides whether a result can become a Candidate.
Promotion Evaluation asks whether a complete Release should replace an
incumbent for one target. Seal the holdout protocol and budget first; production
approval remains a named human action regardless of experiment autonomy.
