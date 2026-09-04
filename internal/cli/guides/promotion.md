# Promote clean evidence with a human gate

Production progression is deliberately typed:

```text
clean Attempt v3 -> supported Experiment -> Evaluation v2 -> Candidate v2
Candidate slots + combination evidence when needed -> validated Release
sealed PromotionSpec + fresh holdout + named human -> Promotion -> Champion
```

Candidate v2 must bind the exact clean formal Attempt evaluated by Evaluation
v2 and copies its Source identities, head commits, and ChangeSets. A Release
assembles named Candidate slots for one target; multiple independent Candidates
need their own supported/evaluated combination Experiment.

Seal the promotion-purpose protocol before spending a fresh, non-reused holdout.
`exp promotion append` always requires a named human and explicit confirmation.
No agent or autonomy mode can approve production. `exp champion manifest` is a
derived deployment input, never canonical authority or an automatic merge/push.

Large datasets, models/checkpoints, artifact files, traces, and unbounded logs
stay in MLflow, DVC, or object storage. Git keeps only bounded summaries,
digests, exact identities, and sanitized references.

Next: `exp context` to resume or `exp ui` for a read-only overview; use
`exp <command> --help` for exact syntax.
