# Record format and schema versions

## File envelope

Every canonical record is one UTF-8 Markdown file. It begins at byte zero with TOML front matter delimited by lines containing only `+++`; the remainder is an ordinary Markdown body.

```markdown
+++
schema = "exp.plan/v1"
id = "plan_01a01e66-f8e0-7202-8000-000000000202"
title = "Calibrate encoder learning rate"
created_at = 2026-08-20T09:01:00Z
updated_at = 2026-08-20T10:03:00Z
priority = "P1"
effort = "S"
state = "completed"
resulting_experiment = "exp_01a01e67-e340-7303-8000-000000000303"

[expected_payoff]
summary = "Avoid regressions while reducing calibration runs"
metric = "macro_f1"
unit = "score"
estimate = 0.03
+++

# Calibrate encoder learning rate

The body carries rationale and context that need not be query fields.
```

The opening delimiter, closing delimiter, and a final LF are mandatory. Writers emit LF line endings. Front matter must parse as TOML 1.0 and may not contain duplicate keys.

A canonical record file is limited to 8 MiB (8,388,608 bytes), including front matter and Markdown body. On-disk validation traverses reserved canonical locations through one opened root and reads only regular files using kernel no-follow where available, pre/open/post identity checks, and a limit-plus-one sentinel read. Symlinks, file-type or identity races, and oversized records are reported rather than followed or decoded.

`schema` selects an exact decoder:

```text
exp.project/v1
exp.policy/v1
exp.idea/v1
exp.resource-pool/v1
exp.queue/v1
exp.queue-advice/v1
exp.battle/v1
exp.plan/v1
exp.plan/v2
exp.experiment/v1
exp.experiment/v2
exp.run/v1
exp.attempt/v1
exp.attempt/v2
exp.evaluation-spec/v1
exp.evaluation/v1
exp.finding/v1
exp.candidate/v1
exp.release/v1
exp.promotion-spec/v1
exp.promotion/v1
exp.decision/v1
```

Plan, Experiment, and Attempt v1 decoders remain exact and closed. Their v2
decoders add priced queue inputs, research lineage/combination inputs, and
dispatch/ChangeSet identity respectively; a v1 record containing a v2-only
field is rejected. The autonomous control-plane records and their ownership
rules are specified in
[autonomous-research-control-plane.md](autonomous-research-control-plane.md).

A known schema rejects unknown fields at every known table level. The sole open container is `extensions`. An extension is keyed by a lower-case reverse-DNS namespace and is preserved recursively without interpretation:

```toml
[extensions."org.example.review"]
reviewed_by = "synthetic-reviewer"
```

Top-level vendor keys such as `x_owner` are not extensions and are rejected. Core validators may still reject an extension value that cannot be represented by TOML or breaches size/privacy limits.

## Identity and aliases

New records use a lower-case typed RFC 9562 UUIDv7:

```text
plan_<uuidv7>
idea_<uuidv7>
pool_<uuidv7>
queue_<uuidv7>
advice_<uuidv7>
battle_<uuidv7>
exp_<uuidv7>
run_<uuidv7>
att_<uuidv7>
evalspec_<uuidv7>
eval_<uuidv7>
fnd_<uuidv7>
cand_<uuidv7>
rel_<uuidv7>
promspec_<uuidv7>
prom_<uuidv7>
dec_<uuidv7>
```

The prefix must match the selected schema and every relationship stores the complete typed ID. IDs are immutable. Creation checks the complete project inventory for an existing ID before publication.

`PROJECT.md` has a bare `project_id` UUID because it is the namespace root rather than a cross-kind record reference. A new project uses UUIDv7. `POLICY.md` is the other ID-less special record and is unique by its fixed canonical path.

Ordinary native creation accepts only UUIDv7, even when a record carries
`extensions."io.github.daviddwlee84.exp-cli.harness-v0"`; extension presence
does not authorize UUIDv5. The explicit fingerprinted harness-v0 migrator uses
deterministic UUIDv5 only after recomputing the identity and verifying reviewed
provenance against the committed source archive. Migrated records retain legacy
aliases where available; a migrated Project uses the recomputed UUIDv5 as
`project_id`. Random UUIDv4, ULID, hashes, and sequential IDs are not canonical
IDs. See [harness-v0-migration.md](harness-v0-migration.md).

The display form is `<letter>-<prefix>`, using the first eight upper-case
hexadecimal UUID digits without hyphens. Letters are `I` Idea, `O` Pool, `Q`
Queue, `V` Advice, `B` Battle, `P` Plan, `E` Experiment, `R` Run, `A` Attempt,
`S` EvaluationSpec, `N` Evaluation, `F` Finding, `C` Candidate, `L` Release,
`T` PromotionSpec, `M` Promotion, and `D` Decision. If the candidate is not
unique among records of the same kind, extend it one hexadecimal digit at a
time. Display codes and unique typed-ID prefixes are accepted only when
unambiguous; they are never persisted as relationship keys and may lengthen as
a project grows.

`legacy_aliases` is an optional array used by migration. Harness aliases have exact forms `#NNN` for Experiments and `F-NNN` for Findings, with three or more decimal digits. Alias resolution is type-aware and must be unique. New native records do not allocate sequential aliases.

## Common fields

Except for the Project and Policy singletons, every record has:

| Field | Requirement |
|---|---|
| `schema` | Required exact schema string |
| `id` | Required typed UUID as above |
| `title` | Required non-empty single-line string |
| `created_at` | Required RFC 3339 UTC TOML offset datetime |
| `updated_at` | Required RFC 3339 UTC TOML offset datetime, not before creation |
| `legacy_aliases` | Optional unique strings; migration only |
| `tags` | Optional sorted, unique lower-case slugs |
| `extensions` | Optional namespaced extension tables |

Timestamps are UTC and serialize with `Z`. Arrays that model sets are unique and serialize in bytewise lexical order. Ordered scientific data, such as amendment history and evidence disposition, retains its semantic order.

## Project

`<git-root>/experiments/PROJECT.md` marks the only root v1 discovers.

Required fields are `schema`, `project_id`, `name`, `created_at`, and `experiments_root`. `experiments_root` is `.` because `PROJECT.md` is inside the root. Optional fields are `extensions` only. V1 does not search for other markers; they are out-of-scope files, not active roots or discovery errors. Named or multiple roots are deferred.

## Policy, Idea, ResourcePool, and Queue

`POLICY.md` is the fixed ID-less `exp.policy/v1` singleton. It owns autonomy,
exploit/explore shares, score formula, tie policy, the mandatory human Promotion
gate, controlled classification taxonomy, cluster saturation defaults, and
optional per-cluster state. Native policy initialization defaults to `manual`
and 0.8/0.2 shares.

An Idea owns its proposal state, summary, proposer, primary cluster,
classification, parent Idea edges, resulting Plan edge, and optional merge
target. Valid states are `proposed`, `developing`, `qualified`, `queued`,
`dismissed`, and `merged`.

A ResourcePool owns whether a bottleneck is enabled, its integer concurrent
capacity, unit, bottleneck slug, and optional hourly cost. It does not store a
Pueue group; that host/runtime binding belongs to `.exp/runtime.json`.

A Queue owns a positive semantic revision, pause flag, and ordered
`[[partitions]]` keyed by ResourcePool and `exploit`/`explore` lane. Each entry
stores Plan ID, exact Plan revision, score, insertion time, and pin flag. A Plan
may appear only once across the project. Partition and entry order is semantic
and is never normalized as a set.

QueueAdvice is an immutable record of one listwise recommendation against an
exact Queue revision: candidate Plan, pool/lane, proposed position, complete
suggested order, score components, model/profile report, prompt digest, and
rationale. Battle is an immutable adjacent comparison with both presentation
orders, resulting outcome, confidence, and rationale. Neither record can mutate
Queue order by itself.

## Plan v1 and v2

Additional fields:

| Field | Requirement |
|---|---|
| `priority` | Required: `P1`, `P2`, `P3`, or `P?` |
| `effort` | Required: `S`, `M`, `L`, or `XL` |
| `state` | Required: `queued`, `started`, `completed`, or `dropped` |
| `assumptions` | Optional array of canonical Finding IDs |
| `resulting_experiment` | Required for `started` and `completed`; forbidden otherwise |
| `expected_payoff.summary` | Required non-empty statement |
| `expected_payoff.metric` | Required stable metric slug |
| `expected_payoff.unit` | Required non-empty unit |
| `expected_payoff.estimate` | Optional finite number in that unit |

A Plan owns `resulting_experiment` and `assumptions`. Experiments and Findings do not store inverse Plan lists.

`exp.plan/v2` retains every v1 field and additionally requires the qualified
control-plane context used for queueing:

| Field | Requirement |
|---|---|
| `idea` | Optional originating canonical Idea; set by Idea qualification |
| `primary_cluster` | Controlled cluster slug |
| `classification` | Domain, work, method, component, lane, risk, horizon, and origin |
| `dependencies` | Finding ID plus exact revision and current belief digest |
| `resources` | One or more ResourcePool, units, and positive estimated hours |
| `utility` | Probability, impact, information gain, unblock value, and risk penalty |

The belief digest covers the referenced Finding revision and all incoming
`weakens`/`overturns` edges including source revisions. A stale dependency or
queued Plan revision blocks dispatch.

## Experiment

Additional top-level fields are `lifecycle`, optional `closure`, optional `verdict`, `design`, optional ordered `amendments`, optional `closure_detail`, and optional `conclusion`.

`design` requires:

```text
question
hypothesis
kind                 single_factor | factorial | observational
                     replication | sweep | combination (v2 only)
primary_factor
secondary_factors    array of strings
baseline
comparability_spec
success_criteria     non-empty array of strings
decision_rule
```

`design_locked_at` and `design_digest` are either both absent or both present. They are required before the first Attempt can be registered. Compute `design_digest` as SHA-256 of UTF-8 JSON containing exactly the nine design fields listed above, with object keys sorted bytewise, arrays retaining order, JSON strings escaped normally, and no insignificant whitespace; emit `sha256:<64 lower-case hex>`. Lock timestamps and the digest itself are not input. An amendment is an array-table item containing `amended_at`, `reason`, `previous_digest`, `new_digest`, and a non-empty `changes` array. Its previous digest must equal the preceding design digest, and amendments are strictly chronological.

Lifecycle invariants:

| Lifecycle | Required | Forbidden |
|---|---|---|
| `planned` | design | `closure`, `verdict`, `closure_detail`, `conclusion` |
| `active` | design | `closure`, `verdict`, `closure_detail`, `conclusion` |
| `closed` + `concluded` | `closure`, `verdict`, `conclusion` | `closure_detail.superseded_by` |
| `closed` + `abandoned` | `closure`, `closure_detail.reason` | `verdict`, `conclusion`, `closure_detail.superseded_by` |
| `closed` + `superseded` | `closure`, `closure_detail.reason`, `closure_detail.superseded_by` | `verdict`, `conclusion` |

A concluded verdict is exactly one of `supported`, `refuted`, `inconclusive`, or `invalid`.

`conclusion` requires `concluded_at`, `summary`, and one or more `[[conclusion.evidence]]` entries. Each entry has `run`, `disposition` (`included` or `excluded`), and `reason`. The reason may be empty only for included evidence. Each Run must exist and belong to this Experiment. Attempts are not evidence references.

An Experiment owns a supersession edge only through `closure_detail.superseded_by`. It does not store its originating Plan, Runs, Attempts, Findings, or Decisions.

`exp.experiment/v2` adds parent Experiment edges and `candidate_inputs`. A
combination Experiment uses Candidate inputs to test whether independently
evaluated changes remain valid together; those inputs do not themselves prove
additivity.

## Run

Additional fields:

| Field | Requirement |
|---|---|
| `experiment` | Required canonical Experiment ID; the Run owns this edge |
| `role` | Required: `baseline`, `candidate`, `validation`, or `batch` |
| `objective` | Required non-empty description of the intended evidence |
| `config_digest` | Optional SHA-256 digest |
| `data_digest` | Optional SHA-256 digest |
| `seeds` | Optional ordered array of integers |
| `expected_outputs` | Optional sorted array of safe repository-relative POSIX paths |

A Run has no process state, Attempt list, evidence disposition, or scientific verdict. One Run may have several Attempts; tracker-owned sweep trials may remain solely behind an ExternalRef.

## Attempt

Additional fields:

| Field | Requirement |
|---|---|
| `run` | Required canonical Run ID; the Attempt owns this edge |
| `state` | Required operational state |
| `state_reason` | Optional sanitized provider/native reason |
| `runner` | Required provider name |
| `scheduler` | Required provider name; exactly one scheduler owns the Attempt |
| `cwd` | Required safe repository-relative POSIX path; `.` is allowed |
| `argv` | Required non-empty argument array; no shell string |
| `external_refs` | Optional array of ExternalRef tables |
| `provenance` | Optional structured provenance table |
| `terminal` | Required for known terminal states; forbidden for nonterminal states |

Operational states are those defined in [architecture.md](architecture.md). Known terminal states are `succeeded`, `failed`, `cancelled`, `timed_out`, `preempted`, and `out_of_memory`. `unknown` may have no terminal record and never authorizes automatic retry.

A terminal table contains `source`, `observed_at`, optional `started_at`, required `ended_at`, and optional `exit_code` and `signal`. Timestamps must be ordered. State-specific exit-code inference is forbidden; provider evidence must explicitly establish classifications such as `out_of_memory`.

Recommended provenance keys are `captured_at`, full `git_commit`, `git_dirty`, optional `dirty_digest`, `config_digest`, `data_digest`, `environment_digest`, and `reproducibility` (`exact`, `bounded`, `partial`, or `unknown`). Provenance values must be safe to commit.

Every explicitly registered, redacted Attempt is a canonical committed record, including failed Attempts. Local start/terminal markers under the Git common directory are operational inputs and are imported into this record; they do not replace it.

`exp.attempt/v2` additionally pins the dispatch context: ResourcePool, Queue,
Queue semantic revision, lane, stable dispatch ID, full Git base/head commits,
and the exact ChangeSet. These fields connect the scientific Attempt to one
admitted Queue frontier and one reviewed code candidate; Pueue task IDs and
live status remain external observations.

## ExternalRef

Each `[[external_refs]]` item has:

```text
role          runner | scheduler | tracker | artifact | registry
provider      lower-case provider slug
context       configured non-secret context name
native_kind   provider-native resource kind
native_id     provider-native immutable/scoped ID
uri           optional sanitized URI
observed_at   optional provider observation time
metadata      optional map whose keys are provider-namespaced
```

A reference is identity, not a claim that cached state is fresh. Live observations additionally carry source, provider version, capability, `observed_at`, `stale`, `partial`, diagnostics, and bounded sanitized native state outside canonical records unless deliberately imported as a fact.

## Finding

Additional fields are `statement`, `scope`, one or more `[[evidence]]` entries, optional `weakens`, and optional `overturns`.

- `statement` and `scope` are required non-empty strings.
- Each evidence entry has `kind`, `ref`, and optional `detail`. Native v1 Findings use `kind = "run"` with a canonical Run ID. Migration may use `kind = "experiment"` with a canonical Experiment ID when v0 identifies only a source report; this is explicitly coarse evidence and must not cause a Run to be synthesized.
- Duplicate `(kind, ref)` evidence entries are invalid.
- `weakens` and `overturns` are unique arrays of canonical Finding IDs.
- A target may not occur in both arrays; self-edges and relation cycles are invalid.

A Finding owns all three relation classes. It has no stored `active`, `weakened`, or `overturned` status: projections derive that status from incoming edges, retaining all historical records.

## Evaluation, Candidate, and Release

EvaluationSpec owns a purpose (`scientific` or `promotion`), frozen dataset or
split identifier, protocol, one or more metric contracts, a ResourcePool budget,
finite budget hours, and optional seal time. Each metric declares name, unit,
direction, and optional threshold. Promotion-purpose specs must be sealed and
every promotion metric has a threshold, so `passed`/`failed` is deterministic.

Evaluation is immutable and owns its EvaluationSpec and subject edges. The
subject is an Experiment, Candidate, or Release. It records `passed`, `failed`,
or `invalid`, evaluation time, exactly declared metric values, summary, and
optional sanitized external references. Tracker state is not copied wholesale.

Candidate owns its concluded Experiment, passing scientific Evaluation, parent
Candidate edges, full Git commit, exact ChangeSet, and optional external
references. A successful direct Attempt for an included Run must match that Git
identity and ChangeSet. It is a reusable evaluated result, not merely a
successful process.

Release owns a target, version, state (`draft`, `validated`, or `retired`), and
unique named slots mapping to Candidates (normalized by slot name). It may also
own a combination Experiment plus its distinct `combination_evaluation`, and a
Release-level Evaluation. More than one distinct Candidate
requires supported combination evidence; slot names are project conventions
and can represent logic, strategy parameters, code, or models.

## Promotion and Champion

PromotionSpec owns one target, a sealed promotion-purpose EvaluationSpec,
bounded holdout hours, seal time, and the human-approval requirement.

Promotion is append-only. It owns target, spec, challenger Release, optional
incumbent Release, holdout Evaluation, outcome (`accepted`, `rejected`, or
`rolled_back`), applied time, previous Promotion edge, and named human approver.
The holdout Evaluation must be created after the PromotionSpec seal and cannot
be reused by another Promotion. Accepted and rollback outcomes require a passed
holdout; rollback may restore only the incumbent displaced by the current
champion-setting Promotion. EvaluationSpec, Run, Finding, Decision,
QueueAdvice, Battle, Evaluation, Candidate, PromotionSpec, Promotion, and a
validated/retired Release are immutable once published; later knowledge uses
new linked records rather than rewriting evidence.
The current Champion is derived from the valid chain for each target; a
generated champion manifest is never canonical.

## Decision

Additional fields are `statement`, `based_on`, `action`, `effective_at`, and optional `supersedes`.

- `based_on` is a non-empty, unique array of canonical Finding IDs.
- `action` is a non-empty commit-safe description. Repository paths in it should be represented as Markdown links in the body or explicit safe path extension data, not absolute host paths.
- `supersedes` is a unique array of canonical Decision IDs; self-edges and cycles are invalid.

A Decision owns these relations. Active/superseded presentation is derived from incoming `supersedes` edges.

## Relationship ownership summary

| Relation | Sole owner |
|---|---|
| Idea descends from Idea | Child Idea |
| Idea qualifies to Plan | Idea |
| Idea merges into Idea | Merged Idea |
| Plan assumes Finding | Plan |
| Plan has revision/belief-pinned Finding dependency | Plan v2 |
| Plan consumes ResourcePool budget | Plan v2 |
| Plan results in Experiment | Plan |
| Queue orders Plan in ResourcePool/lane | Queue |
| QueueAdvice/Battle observes Queue revision | QueueAdvice/Battle |
| Experiment descends from Experiment | Child Experiment v2 |
| Combination Experiment consumes Candidate | Experiment v2 |
| Run belongs to Experiment | Run |
| Attempt executes Run | Attempt |
| Attempt was admitted from Queue/Pool/lane | Attempt v2 |
| Experiment includes/excludes Run as conclusion evidence | Experiment conclusion |
| Evaluation follows EvaluationSpec and evaluates subject | Evaluation |
| Finding cites Run (or a coarse migrated Experiment) | Finding |
| Finding weakens/overturns Finding | New Finding |
| Candidate packages Experiment/Evaluation and parent Candidates | Candidate |
| Release fills named slot from Candidate | Release |
| Release cites combination Experiment/Evaluation | Release |
| PromotionSpec uses sealed EvaluationSpec | PromotionSpec |
| Promotion challenges/incumbent/evaluates/continues chain | New Promotion |
| Decision is based on Finding | Decision |
| Decision supersedes Decision | New Decision |
| Experiment is superseded by Experiment | Old Experiment closure |

Reverse relations are computed by inventory scans. A generated projection must never be read to reconstruct an edge.

## Paths, URIs, and privacy

A committed path is a clean repository-relative POSIX path. `.` is permitted only where explicitly documented. Reject:

- POSIX, Windows-drive, UNC, or `file:` absolute paths;
- `..`, empty segments, NUL, backslash separators, or paths escaping through symlinks;
- paths resolving outside the Git worktree;
- credentials, home-directory shorthand, and host-specific temporary directories.

A committed URI must parse structurally and contain no userinfo or query component. Provider adapters extract identity into `provider`, `context`, `native_kind`, and `native_id`, then emit a sanitized URI. Redaction happens before validation, logging, diagnostics, caching, or persistence. Secret-bearing URI input is rejected rather than stored partially redacted without an explicit safe value.

Canonical fields never contain secret values, raw environments, authorization headers, cookie values, private keys, or unbounded provider output. Hostnames and user names are omitted unless a declared, reviewed provenance policy requires a non-sensitive value.

## Revisions

Optimistic revision is computed, not stored:

```text
sha256:<lower-case hex of normalized record bytes>
```

Normalization is deterministic:

1. Decode the selected typed schema and validate it.
2. Serialize core fields in schema order; serialize set arrays in lexical order.
3. Serialize extension namespaces and keys recursively in bytewise lexical order.
4. Render timestamps in UTC RFC 3339 form and digests in lower case.
5. Use LF delimiters and line endings.
6. Append the Markdown body without whitespace reflow or heading changes, ensuring one final LF.

The path, file mode, generated projections, and the revision string itself are not hash input. Unknown non-namespaced fields cannot be normalized and therefore fail before revision calculation.

## Layout and projections

Canonical paths are:

```text
PROJECT.md
POLICY.md
ideas/idea_<full-uuid>-<slug>.md
plans/plan_<full-uuid>-<slug>.md
resource-pools/pool_<full-uuid>-<slug>.md
queues/queue_<full-uuid>-<slug>.md
queue-advice/advice_<full-uuid>-<slug>.md
battles/battle_<full-uuid>-<slug>.md
evaluation-specs/evalspec_<full-uuid>-<slug>.md
evaluations/eval_<full-uuid>-<slug>.md
findings/fnd_<full-uuid>-<slug>.md
candidates/cand_<full-uuid>-<slug>.md
releases/rel_<full-uuid>-<slug>.md
promotion-specs/promspec_<full-uuid>-<slug>.md
promotions/prom_<full-uuid>-<slug>.md
decisions/dec_<full-uuid>-<slug>.md
e-<allocated-short-prefix>-<slug>/REPORT.md
e-<allocated-short-prefix>-<slug>/runs/run_<full-uuid>-<slug>.md
e-<allocated-short-prefix>-<slug>/attempts/att_<full-uuid>.md
```

Slugs are lower-case ASCII letters/digits separated by single hyphens. A path never determines identity, and an existing experiment directory is not automatically renamed when its display prefix would lengthen.

`README.md`, `ROADMAP.md`, `LEDGER.md`, and `DECISIONS.md` begin with:

```markdown
<!-- Generated by exp render; DO NOT EDIT. -->
```

Rendering contains no current time, hostname, absolute path, or cache data. Records sort by the view's explicit key and then complete canonical ID: Plans by state, priority, then ID; Experiments by lifecycle then ID; Findings and Decisions by creation time then ID. Table cells escape `|` as `\|` and embedded newlines as `<br>`. Links are relative POSIX paths. Output uses LF and one final newline. `exp render --check` compares exact bytes and never writes.
