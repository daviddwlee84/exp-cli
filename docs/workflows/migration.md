# Migration and backward compatibility

There are two different cases:

1. **Harness-v0 semantic migration** converts the older unversioned research
   tree through a reviewed, fingerprinted plan/apply protocol.
2. **Versioned compatibility** reads existing Project/record/runtime/worker/
   journal schemas exactly. It does not rewrite valid v1/v2 history merely to
   use the new independent-repository and Source-aware features.

## Harness-v0: build a read-only plan

```bash
exp migrate plan \
  --legacy-source path/to/harness-v0 \
  --output migration-plan.json \
  --json
```

`--source` is accepted only as the migrate-plan compatibility alias for
`--legacy-source`. Migration always targets the invocation Git repository and
rejects `--workspace`; `migrate apply` rejects both `--workspace` and `--source`.
The default legacy source is `experiments`.

The plan fingerprints every source byte, computes deterministic target
identities, preserves unknown spans, and reports ambiguous meaning as
`needs_review`. Supplying `--output` is the only plan-side write; output uses
no-clobber creation.

## Resolve ambiguity and apply

Copy only the reported `needs_review` keys into a strict
`exp.migration-resolutions.harness-v0/v1` file. Choose `migrate` only when the
shown candidate is valid; choose `archive_only` when the old material cannot
safely establish a current record. Rebuild and review the complete plan:

```bash
exp migrate plan \
  --legacy-source path/to/harness-v0 \
  --resolutions resolutions.json \
  --output reviewed-plan.json

exp migrate apply --plan reviewed-plan.json --json
exp validate
exp render --check
```

Apply verifies the plan hash and source fingerprint, recomputes UUIDv5 values,
validates the complete candidate inventory, preserves an exact Git-tracked
archive, and performs its recoverable root swap. Any source change, addition,
deletion, symlink substitution, unresolved review item, or candidate mismatch
fails closed. Reapplying is a no-op only when all expected destination hashes
match. The migrator never executes legacy scripts, parsers, provenance helpers,
or job wrappers.

See [Harness-v0 compatibility and migration](../design/harness-v0-migration.md)
for the exact field mapping and archive protocol.

## Versioned compatibility matrix

| Existing material | Exact current behavior | New-feature path |
|---|---|---|
| Embedded `exp.project/v1` at `<git-root>/experiments/PROJECT.md` | Physical Project discovery takes precedence and does not require local association state. The marker and `experiments_root = "."` remain unchanged. | Continue embedded use, run `workspace register` for selection from elsewhere, or initialize a separate dedicated Project. There is no Project v2 conversion. |
| Existing canonical records | Each persisted schema uses its closed decoder. Unknown newer fields in an older schema are errors. Old records are not upgraded in place. | New Try adoption writes Idea v2; runtime v2/direct Try write Attempt v3; explicit formal `--attempt` writes Evaluation v2; Candidate defaults to v2. |
| Pre-existing `sources` path or `t-*` content | Only exact canonical Source filenames and exact Try directory grammar are reserved. Nonmatching legacy content is ignored and initialization does not rewrite it. | Resolve a filesystem collision explicitly before creating a new Source or Try. |
| Candidate v1 CLI workflow | Existing `--git-commit` plus `--change` invocations still infer Candidate v1; explicit `--legacy` is also accepted. | Omit legacy fields and provide a clean successful formal Attempt v3 for Candidate v2. |
| `.exp/runtime.json` with `exp.runtime/v1` | Loaded by its original closed embedded-repository decoder and creates Attempt v2/worker-job v1. Source-aware fields are rejected. | Author a separately reviewed `exp.runtime/v2`, register Sources, and approve its exact raw digest for `runtime.dispatch`. No automatic JSON rewrite occurs. |
| Queued `exp.worker-job/v1` | Hidden worker invocation without explicit authority flags remains available only for the exact legacy job shape. V1 job/terminal/result decoders reject v2-only fields. | Runtime v2 dispatches worker-job/terminal/result v2 and passes `--canonical-root`, `--project`, and `--scope` together. Direct Try v2 jobs must be resumed through `exp try resume`, not the hidden worker command. |
| `exp.transaction/v1` journal in `transactions/` | Committed journals remain historical. A prepared v1 journal can recover only if no linked-worktree metadata entries exist; otherwise missing worktree authority blocks recovery. | Every new canonical transaction is `exp.transaction/v2` under `transactions-v2/`, with a path-free worktree identity. Journals are not rewritten. |
| Host-local association/trust files absent | Legacy embedded use continues. Absence does not change canonical identity. | Cross-repository selection creates `exp.associations/v1`; execution-bearing repository config requires exact `exp.trust/v1` receipts. These local files are not copied between hosts or committed. |
| Champion manifest v1 consumer | V1 remains emitted while every slot uses Candidate v1. | Any Candidate v2 selects Source-aware manifest v3; consumers must branch on `schema_version`. No v2 manifest is emitted. |

## Moving from embedded to dedicated

There is no command that silently moves an existing Project or changes its
Project UUID. If governance calls for a dedicated repository, initialize it as a
new canonical Project and add the old code repository as an ordinary Source.
Keep or archive the old embedded Project according to repository policy; do not
copy records under a new Project UUID and pretend their typed relationships are
unchanged. A Git submodule may make either checkout convenient, but it does not
perform migration or establish Source identity.
