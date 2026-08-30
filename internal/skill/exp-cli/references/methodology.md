# Experiment methodology

## Start with expected payoff

An experiment is justified by a decision it could improve, not by novelty alone. Before creating a Plan, ask:

- What concrete choice would change for a positive, negative, or inconclusive result?
- Which metric expresses the benefit, and in what unit?
- What improvement is plausible, and what is the value of learning that the idea does not work?
- What engineering, compute, review, and opportunity cost will be spent?
- Which existing Findings or untested assumptions make the estimate fragile?
- What observation would make the work a dead end rather than motivate another trial?

Use an interval or omit a numeric estimate when precision would be false, but retain a stable metric and unit. Compare payoff and effort across Plans. “Interesting” without a decision, measurable benefit, or bounded learning value is a reason to clarify or defer.

For Queue admission, separate direct expected gain from information gain,
unblock value, downside, and constrained ResourcePool-hours. Classify genuine
exploration as `explore`; do not disguise it as exploitation merely to obtain a
higher rank. The default 80/20 allocation protects some discovery capacity
without allowing low-value exploration to consume every bottleneck.

## Register the design before evidence

A reviewable design states, before outcome inspection:

1. the question and falsifiable hypothesis;
2. the primary factor being changed and any secondary factors;
3. the baseline;
4. the comparability specification;
5. success criteria;
6. the decision rule.

Freeze that design before the first evidence-producing Attempt. Afterward, do not tune thresholds, swap the primary metric, remove inconvenient seeds, or redefine the baseline in place. New information can justify a dated amendment with a reason and a new design digest. Interpret evidence under the design that actually governed its collection.

Exploration is legitimate, but label it exploratory. A pattern discovered after looking at results is a candidate hypothesis for later confirmation, not a pre-registered prediction.

## Require comparable evidence

A comparison is meaningful only where the registered comparability specification holds. Check at least:

- dataset identity, version, split, filtering, and leakage controls;
- preprocessing, feature construction, tokenization, and augmentation;
- metric definition, aggregation, evaluation set, and evaluation timing;
- seed policy, sample size, confidence or variability treatment, and stopping rule;
- baseline code/configuration and all factors not intentionally changed;
- hardware, numerical precision, runtime limits, and dependency versions when they can affect the result.

A Run is an intended unit of evidence. An Attempt is one operational execution of that Run. Retrying an infrastructure failure does not create a new scientific question, and several materially different configurations should not be collapsed into one apparently replicated Run.

At conclusion, identify every cited Run as included or excluded. Exclusion needs a reason. Do not silently drop inconvenient evidence.

## Interpret negative and invalid outcomes correctly

A negative scientific result is useful when valid evidence fails the registered success or decision rule. Preserve it as a Finding with its scope and evidence; this prevents repeated dead ends and improves future payoff estimates.

Distinguish these cases:

- **Refuted:** comparable included evidence contradicts the registered hypothesis under the decision rule.
- **Inconclusive:** valid evidence exists but does not resolve the question with the registered precision or coverage.
- **Invalid:** the evidence cannot answer the question, for example because comparability failed, leakage was found, or the protocol was violated.
- **Operational failure:** an Attempt was blocked, failed, timed out, was preempted, or ran out of memory. This is not a scientific verdict.
- **Abandoned or superseded:** work closed without a scientific verdict because its value, assumptions, or design changed.

A successful exit code proves only that a process completed as observed. It does not prove that outputs are valid, comparable, or supportive. Conversely, a failed process may reveal an operational pitfall but cannot refute a model or research hypothesis.

## Preserve dead-end conditions

State why a path should not be retried and what would have to change before reconsideration. Route the information according to what it means:

- evidence-backed limits become scoped Findings;
- a decision to stop becomes an action-bearing Decision when that record type is available;
- recurring operational symptoms and remedies belong in the project pitfall store;
- non-negotiable methodological or system constraints belong in invariants;
- a next action belongs in TODO;
- an unpriced research direction belongs in backlog until it earns a Plan.

Do not upgrade an anecdote into a Finding or bury a scientific result in an operational troubleshooting note.

## Follow branches and test combinations

A useful result can create several follow-up Ideas. Preserve parent edges and
price each branch independently; do not rewrite the original Plan to make a new
question look preregistered. A negative or invalid branch can still improve the
Queue by reducing probability, limiting scope, or identifying a prerequisite.

Candidate improvements measured in isolation are not automatically additive.
When a downstream Release uses more than one Candidate, register a combination
Experiment with those Candidates as inputs and evaluate the combined behavior
under one comparable protocol. Pin that passing Evaluation separately as the
Release's combination evidence. Interaction failures are first-class evidence,
not reasons to cherry-pick the independent scores.

## Separate scientific and promotion evaluation

Scientific Evaluation establishes whether a result supports reuse as a
Candidate. Promotion Evaluation answers whether a complete Release should
replace an incumbent for one production target. Seal the promotion protocol and
finite holdout budget before creating the fresh holdout Evaluation; every
promotion metric needs a threshold and one Evaluation cannot be reused.
Production approval
remains a named human action even when experimental dispatch is automated.
