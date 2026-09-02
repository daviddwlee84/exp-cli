# Research Notes

Notes are the public, dated, exploratory layer of this documentation. They are
useful for recording questions, tool behavior, comparisons, and incomplete
ideas before those ideas deserve a canonical research record or a committed
design decision.

Everything under this section is published with the documentation site. Treat
it as public information. A note is **non-canonical**: it can explain what was
observed or what should be investigated, but it cannot establish a Plan,
Finding, Evaluation, Decision, provider state, or production choice.

## What belongs here

- a dated investigation into a tool, provider version, or integration surface;
- a bounded observation that still needs validation or broader evidence;
- alternatives, trade-offs, and questions that are not yet decisions;
- links to upstream documentation, issues, and sanitized provider identities;
- follow-up topics that may later become an Idea, TODO, backlog item, or
  canonical record.

Use [Tool Explorations](tool-explorations.md) as the standing index for MLflow,
Pueue, DVC, Optuna, Slurm, and notebook-related investigations.

## Publication and safety rules

| Rule | Requirement |
|---|---|
| Public | Assume every committed note will be visible on GitHub Pages. |
| Dated | Put the observation date in the filename or title and record when it was last reviewed. |
| Exploratory | Separate observations, hypotheses, and open questions; do not present guesses as current behavior. |
| Bounded | Quote only the small excerpt needed to support the note and link to the upstream source when possible. |
| Sanitized | Keep only reviewed, non-sensitive identifiers and summaries. |
| Non-canonical | Link to the authoritative record or system; never make the note a second owner of the fact. |

Never commit any of the following to a note:

- secrets, credentials, tokens, cookies, authorization headers, or private
  keys;
- raw environment-variable maps or resolved secret values;
- unbounded logs, traces, prompts, completions, stderr, or provider responses;
- artifact bytes, model weights, datasets, or implicit artifact downloads;
- host-specific private paths or URIs containing userinfo or query secrets.

When a diagnostic matters, write a bounded, sanitized summary and retain the
upstream task, run, commit, or issue identity needed to inspect the source under
its own access controls.

## Suggested note shape

Use a filename such as `2026-09-02-mlflow-artifact-reads.md`. Keep the date of
the observation distinct from the date of the latest editorial review.

```markdown
# 2026-09-02: Short topic

- Status: exploring | validated | deferred
- Observed: 2026-09-02
- Last reviewed: 2026-09-02
- Context: tool version or named non-secret environment

## Question

What decision or integration boundary are we investigating?

## Bounded observations

What did we observe, and from which public or sanitized source?

## Implications

What might change, and what remains unproven?

## Follow-ups

Which concrete validation, Idea, TODO, or design update comes next?
```

## Route durable outcomes to their owner

| Outcome | Durable home |
|---|---|
| A research direction that needs lineage and qualification | canonical Idea |
| Priced, measurable work ready for prioritization | canonical Plan |
| A scoped belief supported by registered evidence | canonical Finding |
| A concrete action someone can complete | project TODO |
| A rough investigation not ready to become an Idea | project backlog |
| A recurring symptom, cause, and remedy | project pitfall |
| A durable constraint future work must obey | project invariant |
| Live task, run, artifact, or registry state | the upstream provider |
| Code history, branch state, and integration | Git |

See [Records and Authority](../reference/records-and-authority.md) for the full
ownership map. Prefer links over copied mutable state.

## Related reference

- [Tool Explorations](tool-explorations.md)
- [Configuration and Paths](../reference/configuration.md)
- [Command Map](../reference/command-map.md)
- [Record Format and Schema Versions](../design/record-format.md)
