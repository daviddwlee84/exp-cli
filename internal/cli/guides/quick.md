# Run a bounded Try

For continuing adhoc work, external data, and saved plots, read `exp guide exploration`.

Choose a Try when the immediate decision is whether a direction deserves formal
research. It is not a Candidate shortcut.

```text
exp --workspace WORKSPACE --source SOURCE --workspace-backend native_git \
  try run --title "Check an idea" --goal "Learn whether X changes Y" \
  -- git status --short
exp --workspace WORKSPACE try status TRY
exp --workspace WORKSPACE try finish TRY \
  --summary "What changed" --no-results --confirm
```

A Try executes argv in a managed native Git worktree. Clean state is default;
`--dirty=capture` must be explicit and bounded. Raw dirty seed bytes stay in
private XDG cache, while Git receives summaries/paths/digests only. Missing
MLflow, Pueue, or `dev` does not block this native flow.

Use `try resume` only for provably unstarted work or verified marker import. Use
`try reconcile` for uncertain execution; never guess a terminal result. A
concluded useful direction can become Idea v2 through human-reviewed
`try adopt`, but a dirty or clean Try cannot directly back Candidate creation.

Next: `exp guide research` to price, queue, and rerun the direction as clean
formal evidence. Use `exp context` or read-only `exp ui` to resume/inspect.
