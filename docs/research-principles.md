# Research Principles

The control plane is useful only when the method behind it is sound. `exp` makes
these distinctions enforceable, but records and providers cannot substitute for
scientific judgment.

## Separate governance from checkout convenience

Prefer a dedicated private experiment repository when research history and
access policy should span several code/data repositories. Represent each governed
repository or immutable semantic subdirectory as an ordinary Source. A local
association or Git submodule is checkout convenience, not canonical Source
identity. Multiple Sources are explicit inputs; no host path belongs in a
scientific record.

Embedded/monorepo Projects remain appropriate when code and research truly share
governance. The methodological rules do not change with layout.

## Start with a decision and expected payoff

An Experiment is justified by a decision it could improve, not novelty alone.
State metric and unit, plausible benefit, information value, resource cost,
assumptions, and what result would make the direction a dead end. Keep genuine
exploration in the `explore` lane instead of disguising it as exploitation.

## Use Try for bounded uncertainty

A Try asks whether a direction deserves formalization. It records a goal,
declared Sources, operational Attempts, and an attributed conclusion or
abandonment. A useful conclusion can be adopted as Idea v2; it is not itself a
Candidate.

Dirty work must be opt-in, bounded, and reproducible enough to inspect. A dirty
SourceSnapshot is legal only for Try-owned Attempt v3 and keeps raw seed bytes in
private cache, not Git. It is exploratory evidence: if the direction matters,
rerun it cleanly through a formal Run. Do not launder local uncommitted state into
a production candidate.

## Register formal design before evidence

Before the first formal Attempt, record a falsifiable hypothesis, baseline,
intended changes, comparability requirements, success criteria, and decision
rule, then lock the design. A result-driven threshold or baseline change belongs
in a dated amendment or a new Experiment—not rewritten history.

Runtime v2 captures the writable execution Source and every read-only Source as
clean, full-object-ID SourceSnapshots. That is code/input identity, not proof of
scientific validity.

## Compare only comparable evidence

Dataset identity, preprocessing, metrics, seeds, stopping rules, runtime limits,
hardware, dependencies, Source commits, and read-only inputs can all invalidate
a comparison. A Run is the intended evidence unit; an Attempt is one operational
execution. Retrying an infrastructure failure is not a new scientific question,
and retry lineage must preserve the execution identity.

A successful exit code, Pueue success, output hash, MLflow `FINISHED`, or artifact
URI proves only a bounded operational/provider fact. Artifact bytes remain in
their provider and are not canonical evidence merely because a URI was retained.

## Separate negative, inconclusive, invalid, and operational outcomes

- **Refuted:** comparable included evidence contradicts the registered
  hypothesis.
- **Inconclusive:** valid evidence exists but does not resolve the question.
- **Invalid:** leakage, protocol violation, changed inputs, or failed
  comparability means the evidence cannot answer the question.
- **Operational failure:** process failure, timeout, cancellation, preemption, or
  out-of-memory. This is not a scientific verdict.
- **Unknown:** execution may have started but no trustworthy terminal observation
  exists. It is not permission to retry.

Explicitly include or exclude each Run at Experiment closure and state why.
Invalid evidence is not automatically negative evidence.

## Require typed ownership before reuse

Promotion-bearing evidence has a concrete chain:

1. successful terminal formal Attempt v3 with only clean SourceSnapshots;
2. its Run included by a closed, concluded, supported Experiment;
3. Evaluation v2 under the registered scientific EvaluationSpec, bound to that
   exact Attempt;
4. Candidate v2 copying the Attempt's Source IDs, head commits, and path sets.

MLflow can corroborate selected metrics only when its workload-owned run proves
exact `exp.attempt_id` ownership. It does not replace Evaluation. A Try, dirty
snapshot, untyped Evaluation v1, standalone Git commit, or artifact location
cannot bypass this chain.

## Preserve dead ends and scope

Record why a direction should not be retried and what would need to change.
Evidence-backed limits become Findings; recurring operational remedies belong in
project pitfalls; future actions belong in TODO or backlog. Do not promote an
anecdote into a Finding or bury a scientific outcome in troubleshooting prose.

Keep forward relations on one canonical owner and derive reverse views. New
belief-changing Findings stale dependent Plans through revision/belief digests;
do not silently reuse a ranking based on overturned assumptions.

## Follow branches and test combinations

Useful outcomes may create several child Ideas. Price each branch independently
and preserve parent edges. Independently successful Candidates are not assumed
to be additive: a multi-Candidate Release needs its own supported combination
Experiment and passing Evaluation.

## Keep promotion human-gated

Scientific Evaluation decides whether an exact result can become a Candidate.
Promotion Evaluation asks whether a complete validated Release should replace an
incumbent for one target. Seal the holdout protocol and finite budget before use,
never reuse the holdout Evaluation, and require a named human for every append-only
Promotion. No autonomy mode, provider, artifact registry, generated Champion
manifest, or read-only TUI can approve deployment.

Exploration can finish with an explicitly unreviewed agent conclusion. Human adoption and formal evidence gates remain separate; see [Adhoc Exploration](workflows/exploration.md).
