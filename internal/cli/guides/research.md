# Move from an Idea to rigorous evidence

Choose this route when constrained resources should test a registered claim:

```text
Idea v2 -> priced Plan v2 -> Pool/lane Queue
        -> runtime v2/Pueue -> Experiment -> Run -> clean Attempt v3
        -> supported conclusion -> Evaluation v2 -> Candidate v2
```

Start with `idea add` (or human-reviewed `try adopt`), qualify with explicit
payoff/resource needs, create Pool/Queue, and insert the Plan. `daemon frontier`
validates the canonical frontier without provider contact.

For independent Sources, author strict `.exp/runtime.json` v2 and exact-digest
trust it for `runtime.dispatch`. It names one writable execution Source, optional
read-only Sources, full commits/ChangeSets, argv, expected outputs, and Pueue
route. Formal dispatch accepts only clean exact SourceSnapshots. A selected
MLflow profile must be trusted and value-free for Pueue; MLflow observation is
optional and never a verdict.

```text
exp config trust
exp daemon frontier
exp policy autonomy assisted --confirm-auto-experiment
exp daemon tick
# or: exp daemon run --interval 5s
```

Closing an Experiment explicitly includes/excludes Runs and records the
scientific verdict. Evaluation v2 must bind the exact successful clean formal
Attempt v3; Candidate v2 requires the same Attempt and copies its Source IDs,
head commits, and ChangeSets.

Next: `exp guide promotion` after Candidate v2 exists. Use `exp context` or
read-only `exp ui` to inspect without granting authority.
