# Storage and transactions

## Status of this contract

The current walking-skeleton milestone implements receipt-backed project initialization, linked-worktree ID reservations, common-directory coordination, atomic no-clobber single-record creation, and canonical compare-and-swap replacement only where a safe atomic-exchange primitive is available. Canonical replacement fails closed on other platforms. Generated projections use their own rebuildable replacement path; they are not canonical transaction participants. The milestone does not claim a generalized multi-record transaction engine.

The prepared journal and recovery protocol below is the required design for the next lifecycle milestone, before `plan start`, `experiment conclude`, Finding belief transitions, or any other compound canonical mutation ships. Projections are derived and follow canonical publication; they are not transaction participants.

## Shared coordination

Version 1 discovers only `<git-root>/experiments`; named or multiple roots are deferred. Resolve the absolute Git common directory, not the current worktree’s `.git` indirection, and use:

```text
<git-common-dir>/exp/v1/
├── lock
├── project-receipt.json
├── reservations/
│   └── <typed-id>
├── transactions/
└── attempts/
```

All linked worktrees therefore serialize canonical writes through the same advisory lock. The coordination root and its subdirectories use mode `0700`, coordination files use `0600`, and canonical Markdown uses `0644`. Git-common coordination is private local state: it is not Git-tracked, is excluded from canonical inventory and projection input, and cannot by itself establish a Plan, Experiment, Run, Attempt, Finding, Decision, relationship, or scientific conclusion.

The lock covers:

- linked-worktree Project identity checks and initialization receipt reconciliation;
- inventory scans, reservation seeding, and ID allocation;
- expected-revision checks;
- candidate validation;
- canonical publication;
- journal recovery when the future journal protocol is implemented;
- projection refresh associated with the mutation.

Lock acquisition honors context cancellation and reports the owner metadata when safely available. A process must not break a lock merely because a PID appears absent; platform advisory locking is authoritative.

### Project initialization receipt

`project-receipt.json` has schema `exp.project-init-receipt/v1` and contains the exact encoded `PROJECT.md` bytes plus their SHA-256 hash. It is bounded to 1 MiB, written atomically, protected as a regular non-symlink private file, and shared by all linked worktrees.

The receipt has deliberately narrow authority. If no linked worktree contains the fixed `<git-root>/experiments/PROJECT.md`, a valid receipt is the recovery source for completing an interrupted first initialization; retrying does not generate a different project UUIDv7. If any linked worktree contains a canonical Project, all present canonical Project markers must agree on `project_id` and `created_at`; those canonical bytes are authoritative and repair a missing, stale, or safely replaceable corrupt receipt. Conflicting canonical identities block initialization. The receipt is neither a second Project record nor an input to ordinary inventory validation.

This ordering makes crashes idempotent. Failure after receipt publication but before `PROJECT.md` publication leaves a reusable initialization candidate. Failure after canonical publication leaves `PROJECT.md` authoritative, and the next initialization reconciles the receipt from it.

### Canonical ID reservations

`reservations/<typed-id>` is a regular non-symlink `0600` file whose complete content is `<typed-id>\n`. Before each canonical mutation, while holding the common lock, the writer loads valid inventories from every present linked worktree at its fixed `experiments` root, requires one Project identity, and creates any missing reservations for their record IDs. A newly allocated ID is reserved without clobber before its canonical record is published. An existing reservation is a collision even when that ID is absent from the current worktree.

Reservations are repository-local durable non-reuse authority, not canonical record authority. A reservation left after a crash or failed publication intentionally burns the ID; record deletion does not remove it. Missing reservations for records still visible in a linked worktree are rebuilt by the pre-mutation scan. A tombstone for an ID absent from every current linked-worktree inventory cannot be reconstructed by that scan, so the reservations directory is not a disposable cache and must not be wholesale rebuilt or cleared. Conversely, a reservation without a canonical Markdown record does not create evidence or participate in relationships, lifecycle validation, rendering, or revisions.

Ordinary record and relationship validation remains UUIDv7-only, including records carrying migration extensions. Reservation filenames do not authorize UUIDv5. Only the future fingerprinted migration engine may introduce deterministic UUIDv5 IDs after recomputing them from reviewed provenance; that engine must reserve those validated imported IDs under the same no-reuse rule.

## Current single-record write

The walking skeleton uses this complete sequence:

1. Discover the Git repository and fixed `<git-root>/experiments` root.
2. Resolve the Git common directory, acquire `exp/v1/lock`, and ensure private coordination directories have mode `0700`.
3. Reject any nonempty `transactions/` directory. The walking skeleton does not recover or interpret journals; recognition and roll-forward belong to the next-milestone protocol below.
4. Open the canonical root without following symlinks and clean only abandoned atomic temporary files.
5. Enumerate present linked worktrees, load each fixed-root inventory, require valid inventories with one Project identity, and seed missing `0600` reservations for every canonical typed ID. Recheck that registered missing worktrees did not appear during the operation.
6. Re-read the current worktree’s inventory while locked through the opened canonical root, applying the 8 MiB per-record bound and no-follow/identity checks.
7. For update, compute the current normalized revision and compare the caller’s expected revision. For create, verify the ID and target path are absent.
8. Build the complete candidate record in memory and validate schema, UUIDv7 identity, relationships, lifecycle, path containment, and privacy against the candidate inventory. Then, for create, reserve the typed ID without clobber before publishing the record; generated creates retry with a fresh UUIDv7 when reservation reports a collision.
9. Create a same-directory temporary file without following symlinks, mode it `0644`, write all bytes, and fsync it.
10. Recheck the opened roots, linked-worktree set, and destination identity and bytes.
11. For create, publish without clobbering an existing name. For canonical replacement, atomically exchange the temporary and destination files, verify the displaced identity and bytes, and roll back on mismatch; if no safe exchange primitive is available, fail without replacing the destination.
12. Fsync the published file and destination directory, then rebuild projections deterministically through the separate rooted generated-file replacement path.
13. Release the lock and return the new computed revision plus any projection diagnostic.

A crash before canonical publication leaves the old record authoritative. A crash after it leaves the new record authoritative; projections may be stale and `render --check` detects that. Projection failure does not roll back a successfully published canonical record and must be reported distinctly.

No process may validate, unlock, and then publish. No writer may write directly to a destination, use a cross-filesystem temp directory, or rely on rename without file and directory fsync.

## Next-milestone prepared journal

### Journal location and identity

A compound operation creates:

```text
<git-common-dir>/exp/v1/transactions/<transaction-uuid>/
├── journal.toml
└── staged/
    ├── 0000
    ├── 0001
    └── ...
```

The transaction ID is UUIDv7. `journal.toml` uses schema `exp.transaction/v1`, mode `0600`, and is itself published atomically. It contains:

```text
schema
transaction_id
project_id
operation
created_at
phase                 prepared | committed
entries[]
```

Each ordered entry contains:

```text
path                  clean experiments-root-relative POSIX path
operation             create | replace | delete
old_hash              sha256 digest or "absent"
new_hash              sha256 digest or "absent"
staged                 staged file name for create/replace
staged_hash            same value as new_hash for create/replace
```

Hashes cover exact publication bytes, not normalized record revisions. Paths may target canonical records only. A journal never stores secrets, absolute worktree paths, or projection entries.

### Prepare

While holding the common lock:

1. Recover older prepared journals first.
2. Seed reservations from all present same-project linked-worktree inventories, re-read every participating source, and verify expected normalized revisions.
3. Build the entire candidate canonical inventory in memory, including creates, replacements, and deletions.
4. Validate every candidate record, relationship, graph constraint, lifecycle rule, path, and privacy rule against that inventory.
5. Reserve every create ID without clobbering. A prepare failure after this point may burn an ID but cannot make it reusable; replacements and deletions retain their existing reservations.
6. Sort entries by path byte order so publication and tests are deterministic.
7. Write each new exact byte sequence to `staged/<index>`, fsync every staged file, and fsync `staged/`.
8. Record exact old/new SHA-256 hashes, write `journal.toml` with `phase = "prepared"`, fsync it, atomically publish it, and fsync the transaction directory and parent `transactions/` directory.

No canonical file changes before the prepared journal and all staged bytes are durable.

### Publish

Still holding the lock, process entries in journal order:

- **create**: require destination absent, then publish the hash-checked staged file without clobber;
- **replace**: require destination exact hash `old_hash`, then use the safe canonical compare-and-swap primitive and verify the displaced bytes; if that primitive is unavailable, fail closed;
- **delete**: require destination exact hash `old_hash`, then unlink it.

After each operation, fsync its parent directory. Re-read and hash the resulting destination (or confirm absence) before moving to the next entry. Progress need not be recorded because recovery derives it from destination hashes.

After every destination matches `new_hash`/`absent`, atomically replace the journal with `phase = "committed"` and fsync its directories. Canonical publication is then complete. Projections are regenerated last from the committed inventory.

Committed journals may be removed only after directory fsync; retaining them for bounded diagnostics is also safe. Cleanup policy must not affect correctness.

## Idempotent recovery

Every mutating command recovers prepared journals while holding the common lock and before reading its own candidate state.

For each entry:

1. Verify journal syntax/version, project identity, path containment, old/new hash forms, and staged file hashes.
2. Hash the current destination without following symlinks.
3. If it equals `new_hash` (or is absent for delete), that entry was already applied; continue.
4. If it equals `old_hash` (or is absent for create), apply the operation from its verified staged data, fsync, and verify the new state.
5. If it matches neither, stop recovery. Report the transaction, path, expected old/new hashes, and observed hash. Never overwrite the unrelated edit and never regenerate projections from a split canonical state.

Recovery always rolls forward; it does not guess how to reconstruct an old tree after some new files were published. Create/replace staged data remains available until commit. Delete recovery needs no removed bytes because it only rolls forward from a verified old hash.

When all entries match the new state, mark the journal committed exactly as normal publication would, then regenerate projections. Running recovery repeatedly produces the same result.

An unknown journal schema, missing staged file while the destination remains old, hash mismatch, unsafe path, or project mismatch blocks all mutation with a repair diagnostic. Read-only commands may report the condition but must not present a split tree as valid.

## Projections-last rule

`README.md`, `ROADMAP.md`, `LEDGER.md`, and `DECISIONS.md` never appear in journal entries. After canonical commit:

1. Render all four from one committed inventory snapshot.
2. Publish each through the separate rooted generated-file replacement path; its weaker replacement semantics are acceptable only because these outputs are rebuildable, and it is never used for canonical records.
3. If interrupted, leave canonical state committed and projections detectably stale.
4. `exp render` repairs them; `exp render --check` reports byte-level drift without writing.

This prevents a generated-file conflict from blocking or corrupting scientific state. Readers never use a projection as relationship or lifecycle input.

## Attempt markers

Local direct execution may write:

```text
<git-common-dir>/exp/v1/attempts/<attempt-id>/start.json
<git-common-dir>/exp/v1/attempts/<attempt-id>/terminal.json
```

Each file is bounded, secret-safe JSON and uses temp/fsync/rename publication. `start.json` records that execution began; `terminal.json` records a terminal operational observation. Its absence means `unknown`, even if a process is not found. Marker import is an explicit revision-checked mutation of the committed Attempt record. Markers are local coordination state, not scientific evidence and not substitutes for committed Attempt records.

## Required verification

Failure injection must cover temp creation, write, file fsync, journal publication, canonical create/CAS/unlink, directory fsync, commit marking, and projection rendering. After restart, the result must be the complete old single-record state, the complete new compound state, or a precise unrelated-edit conflict—never silent split state.

Linked-worktree tests must prove:

- independent UUIDv7 creates serialize and do not collide;
- updates to one record yield expected-revision conflict rather than last-writer-wins;
- compound operations share the common-directory lock;
- repeated recovery is idempotent;
- projection drift never changes canonical validation results.
