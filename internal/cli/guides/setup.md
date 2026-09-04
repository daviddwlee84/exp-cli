# Set up a research workspace

The recommended flow keeps canonical research history in a dedicated private Git
repository and registers the code/data repository as a Source.

```text
# TTY wizard: defaults to dedicated layout and reviews before writing.
exp init

# Then inspect the resolved authority and local associations.
exp workspace status
exp source status
```

For a non-interactive dedicated setup, provide `--dedicated-repo`,
`--source-repo`, `--source-key`, and `--confirm`. Add `--create` only for a
reviewed missing/empty target and `--confirm-local-only` only when accepting no
sanitized remote locator. Use `exp init --name "Project name"` only when the
embedded compatibility layout is intentional.

Canonical `PROJECT.md` and records under `experiments/` are scientific/decision
authority. Source clone paths remain in private host-local associations.

Next: `exp guide workspaces`, then `exp guide config`; when both are sound,
choose `exp guide quick` or `exp guide research`.
