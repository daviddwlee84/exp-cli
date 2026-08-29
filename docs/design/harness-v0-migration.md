# Harness-v0 compatibility and migration

## Scope and status

The unversioned experiment-knowledge-harness layout is named `harness-v0`. This document specifies future behavior: native read-only compatibility, its reader, and migration commands are not implemented by the walking skeleton and remain deferred until after the local lifecycle milestone.

When added, compatibility and migration will be implemented in Go. They will not execute the legacy parser, mutating scripts, provenance helpers, or job wrappers.

Recognized source surfaces are:

```text
ROADMAP.md
LEDGER.md
INBOX.md
<NNN>-<slug>/REPORT.md
README.md and optional executive summaries (views only)
```

A v0 tree is legacy input, not malformed v1. Read-only commands report parsed records, raw spans, and diagnostics without rewriting any byte.

## Lossless reader

The reader first captures each source file as bytes and a SHA-256 hash. Parsing produces nodes with source path, zero-based byte range, exact raw bytes, decoded fields, and diagnostics. Every byte belongs to exactly one of:

- a parsed field or record span;
- front-matter delimiter/formatting;
- a Markdown body retained byte-for-byte;
- an unknown span retained byte-for-byte.

Overlapping or unaccounted spans are a parser error. Unsupported YAML-like syntax, comments, unusual wrapping, custom sections, and malformed bullets remain unknown spans; the reader must not discard or normalize them. Invalid UTF-8 blocks semantic migration but remains reportable by hash and byte range.

The compatibility view may normalize values for querying, but it always retains source coordinates and exact raw text. It never treats generated README tables or graphs as authority.

## Deterministic migration identity

New native records use UUIDv7. The future migration engine uses UUIDv5 only, so repeating a plan for identical source bytes yields identical IDs.

The fixed harness-v0 namespace is:

```text
b2e8b68c-2de6-5291-885e-19f0efdfe218
```

It is UUIDv5 under the standard URL namespace for:

```text
https://github.com/daviddwlee84/exp-cli/migration/harness-v0
```

Compute the source-tree fingerprint as SHA-256 over:

1. ASCII `exp-harness-v0-tree` followed by NUL;
2. each regular source file in bytewise lexical order of its root-relative POSIX path;
3. for each file: unsigned 64-bit big-endian path-byte length, UTF-8 path bytes, unsigned 64-bit big-endian content length, then exact file bytes.

Symlinks, absolute paths, duplicate normalized paths, and files escaping the source root block migration.

Then compute:

```text
project UUID = UUIDv5(fixed namespace, "tree-sha256:" + lower_hex_fingerprint)
record UUID  = UUIDv5(project UUID, kind + NUL + stable_source_key)
```

Stable source keys are:

- Experiment: exact legacy alias, for example `#016`;
- Finding: exact legacy alias, for example `F-039`;
- Plan: `ROADMAP.md:<start-byte>:<end-byte>:<span-sha256>`;
- Decision: `<source-path>:<start-byte>:<end-byte>:<span-sha256>` when an explicit action-bearing decision is unambiguous.

The typed v1 prefix is added after UUID generation. The walking-skeleton validator rejects UUIDv5 regardless of extension presence. Only the future fingerprinted migration engine may accept a migrated record, after it recomputes the UUIDv5 from the reviewed source fingerprint and provenance; UUIDv5 is never used for ordinary creation or trusted merely because an extension is present.

## Mapping

### Project

The project UUID above becomes `PROJECT.md`’s `project_id`. The migration extension records source fingerprint, fixed namespace, reader version, and committed source archive path.

### Reports to Experiments

A v0 `REPORT.md` becomes one v1 Experiment. Preserve its directory by default; paths are navigation, not identity. Preserve `#NNN` in `legacy_aliases` and replace structured references with the canonical typed ID.

The Markdown bytes after the legacy front matter are the new body bytes without reflow, heading changes, or line-ending normalization during migration staging. Legacy front-matter raw bytes remain recoverable from the source archive.

Conservative status mapping is:

| v0 status | v1 mapping |
|---|---|
| `planned` | `lifecycle = "planned"` |
| `running` | `lifecycle = "active"` |
| `concluded-success` | closed/concluded/supported |
| `concluded-negative` | closed/concluded/refuted |
| `inconclusive` | closed/concluded/inconclusive |
| `superseded` | closed/superseded, but `needs_review` if the replacement is not explicit |

A mapped conclusion still must satisfy v1 requirements. Missing verdict meaning, inconsistent dates, absent evidence disposition, ambiguous factors, or a status/body disagreement is `needs_review`; migration does not invent values merely to pass validation.

Legacy `axis` text is preserved. A clearly single factor may map to `primary_factor`; multi-factor or ambiguous text remains in migration extension data and is `needs_review`. The migrator does not select a primary factor.

### Result rows and external references

Do not synthesize Runs or Attempts from result-table rows, commands in prose, MLflow strings, process status, or scheduler labels. Those sources do not establish intended evidence-unit or retry identity.

A legacy Finding may cite its source Experiment as coarse evidence without creating a fictional Run. Empty MLflow fields become absence. A parseable, sanitized external reference may become an ExternalRef; otherwise preserve its raw span in the migration extension and emit a diagnostic.

### Ledger to Findings

Each ledger entry becomes one standalone Finding. Preserve `F-NNN` in `legacy_aliases`, the complete statement and scope text, source/evidence text, and weaken/overturn meaning where unambiguous. The new Finding owns its evidence and belief-changing edges; reports do not receive inverse Finding lists.

If a v0 report lists Findings that the ledger does not attribute back, or the ledger attributes a Finding omitted by a report, retain both raw sources and report the mismatch. The standalone Finding edge is the sole v1 authority after review.

### Roadmap to Plans

Each syntactically recognized roadmap item becomes one standalone Plan. Preserve priority lane, effort, title, payoff text, category text, dependencies, completion syntax, and its exact source span. Map only exact `#NNN` and `F-NNN` references that resolve uniquely.

A matching title or prose reference does not prove a Plan resulted in an Experiment. Queued/completed disagreement remains `needs_review`. Payoff without a separable metric or unit remains raw migration data and requires review rather than a fabricated estimate.

### Decisions

Create a Decision only for an explicit action-bearing statement with unambiguous evidence links and span boundaries. A preregistered decision rule is part of Experiment design, not a final Decision. Narrative interpretations, summaries, and TODO-like prose remain preserved spans when intent is ambiguous.

### Inbox

Version 1 has no canonical Idea entity. `INBOX.md` is therefore never silently converted to Plans. Read-only compatibility exposes its parsed bullets as legacy items. Migration archives the file exactly, records each recognized item/span, and marks unresolved items `needs_review`. Adding a future Idea schema may provide a lossless canonical destination; until then, the archive is the retained source.

### Generated and curated views

Legacy README generated blocks are non-authoritative. A hand-maintained executive summary is a curated snapshot, not a source of duplicate Findings or Decisions. Both are archived exactly and may be linked from migration diagnostics; v1 projections are generated solely from canonical records.

## Source archive and unknown spans

Before replacing monolithic `ROADMAP.md`, `LEDGER.md`, or any report front matter, apply commits an exact, read-only source archive:

```text
legacy/harness-v0/<tree-fingerprint>/
├── manifest.toml
└── source/<original relative paths>
```

The manifest lists every path, byte length, SHA-256, and each parsed/unknown byte range. Archived file bytes must hash exactly to the migration plan. This directory is ignored by v1 canonical inventory and projection rendering but remains Git-tracked evidence of losslessness.

Each migrated record also carries a compact pointer under:

```toml
[extensions."io.github.daviddwlee84.exp-cli.harness-v0"]
source_path = "016-example/REPORT.md"
source_sha256 = "sha256:..."
start_byte = 0
end_byte = 1234
```

Unknown spans are not copied into free-form core fields. Their archive path, ranges, and hashes make every source byte recoverable. A migration plan fails if any byte is neither represented nor archived.

## Plan/apply protocol

`exp migrate plan` is read-only. It emits a versioned plan containing:

- reader and target schema versions;
- exact source-tree fingerprint and per-file hashes;
- deterministic Project and record ID mapping;
- every parsed mapping and relationship;
- every unknown span;
- diagnostics, including all `needs_review` items;
- candidate archive contents;
- candidate canonical files and deterministic projections;
- a reviewable unified diff.

A plan with unresolved `needs_review` is not applicable. Resolution is explicit plan input: select the intended mapping, provide missing required meaning, or retain material only in the archive. A resolution never edits source bytes and is included in the plan hash.

`exp migrate apply --plan <file>`:

1. verifies the plan schema and its own content hash;
2. re-fingerprints every source file and refuses any change, addition, deletion, or symlink substitution;
3. recomputes all UUIDv5 values and candidate hashes;
4. requires all review items to have explicit resolutions;
5. validates the complete v1 candidate inventory;
6. publishes archive and canonical changes through the prepared multi-record transaction protocol;
7. generates projections last;
8. reports exact old/new paths and revisions.

Apply never reparses and makes new choices behind the reviewed plan. Reapplying an already completed plan is an idempotent no-op only when every destination hash matches.

## Required ambiguity diagnostics

The sanitized real-tree migration fixture must preserve and report, without inferred repair:

- a Plan still queued although matching prose says work completed;
- broken relative evidence links;
- report/ledger inverse Finding drift;
- front-matter/body Finding mismatch;
- multi-factor legacy axes with no declared primary factor;
- dirty or incomplete provenance;
- empty MLflow values as valid absence;
- a curated executive summary as a snapshot;
- project-local skill drift as guidance only.

`needs_review` is a migration diagnostic state, not an Experiment lifecycle, verdict, evidence disposition, or Attempt state.
