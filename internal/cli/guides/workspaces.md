# Resolve workspaces and Sources

A canonical Project can be embedded in a source repository or, preferably, live
in a dedicated repository. A Source record identifies one Git repository and
immutable governed subdir; a private association identifies an inspected clone
on this host.

```text
exp workspace status
exp source list
exp source status
exp source add
exp source register SOURCE --repo /path/to/clone
exp --workspace /path/to/research --source app context
```

Resolution uses explicit `--workspace`/`--source` first, then a physical embedded
Project marker, then the most-specific registered Source containing
`--start-dir`. Config and provider catalogs never redirect that authority.

`native_git` is the byte, inspection, and cleanup baseline. Optional `dev_cli`
prepare/open/handoff/retire is visible but unsupported until exact
schema-versioned path receipts exist; missing `dev` does not block native Try.
Completion reads local records/associations/config only and never probes a
provider.

Next: `exp guide config`; then choose `exp guide quick` for exploration or
`exp guide research` for governed formal evidence.
