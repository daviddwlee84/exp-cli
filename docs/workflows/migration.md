# Harness-v0 Migration

Migration is an explicit, reviewable plan/apply protocol. It never silently
rewrites an older research tree.

## Build a read-only plan

```bash
exp migrate plan \
  --source path/to/harness-v0 \
  --output migration-plan.json \
  --json
```

The plan fingerprints source material, computes deterministic target identities,
and reports ambiguous fields as `needs_review`. Unknown source spans are
preserved rather than discarded.

## Resolve ambiguity

Copy only the reported `needs_review` keys into a resolution file, choose the
intended mapping, and rebuild the plan with `--resolutions`. Review the complete
result; a partially reviewed plan cannot be applied.

## Apply the exact plan

```bash
exp migrate apply --plan migration-plan.json --json
exp validate
exp render --check
```

Apply verifies the source fingerprint and uses no-clobber writes. If the source
changed after review, build a new plan instead of forcing the stale one.

The detailed field mapping and ambiguity rules live in
[Harness-v0 compatibility and migration](../design/harness-v0-migration.md).
