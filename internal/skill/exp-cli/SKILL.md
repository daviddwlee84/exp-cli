---
name: exp-cli
description: >-
  Plan and inspect Git-native ML, DL, NLP, and quantitative research with exp;
  use when pricing an experiment idea, pre-registering comparable evidence,
  validating research records, interpreting negative results, or resuming work.
metadata:
  schema-version: "exp.skill/v1"
  skill-version: "1"
---

# exp-cli research guidance

Use `exp` as a Git-native research control plane: it keeps the reasoning from a priced idea to evidence and a decision reviewable, while upstream systems remain authoritative for execution, queues, telemetry, artifacts, registries, credentials, and notebook runtimes.

## Current command boundary

This skill documents only the current walking-skeleton surface:

```text
exp init
exp doctor [--json] [--live]
exp plan add [flags | --input -] [--json]
exp plan list [--json]
exp validate [--json]
exp render [--check]
exp context [--json]
exp skill print|install|check
```

Do not claim that this build implements experiment lifecycle transitions, Runs, Attempts, conclusions, Findings, Decisions, provider reads or submissions, legacy migration, sweep orchestration, or artifact transfer. Those concepts define the research model and later direction; they are not hidden commands. Consult [the generated command reference](references/commands.md) rather than inventing syntax.

## Research judgment before mechanics

1. Ask what decision the experiment could change. Price the expected payoff with a named metric and unit, an estimate when defensible, effort, and assumptions. If no plausible decision has enough value to repay the work, recommend not running it.
2. State the question, hypothesis, primary factor, baseline, comparability requirements, success criteria, and decision rule before looking at outcome evidence. Once evidence collection begins, preserve that design; a later change is an explicit amendment, not a rewritten prediction.
3. Demand comparable evidence. Differences in data, split, preprocessing, metric definition, seed policy, compute regime, stopping rule, or evaluation timing can make a numerical comparison invalid.
4. Preserve negative information. Record a supported negative finding only when comparable included evidence answers the registered question. Record dead-end conditions and excluded evidence without turning an execution failure into a refuted hypothesis.
5. Keep the axes separate: an Attempt can succeed operationally while the hypothesis is refuted, or fail operationally while the scientific result remains unknown. `invalid` means the evidence cannot answer the question; it does not mean `refuted`.

Read [methodology.md](references/methodology.md) before designing or interpreting an experiment.

## Put information under the right authority

Use exp records for research meaning: priced Plans, registered designs, intended evidence units, operational Attempts, evidence-backed Findings, and action-bearing Decisions. Keep ordinary work actions in the project TODO, exploratory notes in its backlog, recurring troubleshooting knowledge in pitfalls, and durable non-negotiable constraints in invariants. Those stores are independently managed; exp may link to them but does not silently duplicate or mutate them.

See [records-and-project-knowledge.md](references/records-and-project-knowledge.md) for routing rules.

## Human, agent, and fallback use

Humans may use explicit flags and review ordinary Markdown. Agents should prefer each command's `--json` form and the versioned stdin request accepted by `exp plan add --input - --json`; never scrape human tables or mix warnings from stderr into JSON stdout. Validate before relying on a derived projection.

If the binary is unavailable, use the [manual read-only fallback](references/usage-and-fallback.md). Never approximate a mutation by hand, infer live provider state from a stale record, or run a legacy/helper script as a substitute for exp.

## External tools and skills

DVC, MLflow, Pueue, Slurm, and Marimo each retain their own authority and may have independently installed skills. This skill can tell an agent when that guidance is relevant, but it never invokes another skill's scripts or templates. Current exp commands do not perform provider operations. See [external-tools.md](references/external-tools.md).
