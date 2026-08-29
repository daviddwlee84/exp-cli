# Human, agent, and read-only use

## Human use

Use explicit flags, inspect the proposed meaning, and review the ordinary Markdown committed to Git. Run `exp validate` before treating records as sound, and use `exp render --check` when verifying that generated project views match canonical records.

`exp doctor` is local-only by default. Add `--live` only when the command help identifies an explicit live probe and that contact is intended. In the walking skeleton, optional tools are capabilities rather than requirements.

## Agent use

Prefer machine contracts over terminal prose:

- add `--json` to commands that advertise it;
- create a Plan through the versioned stdin request advertised by `exp plan add --input - --json` rather than guessing a flag or front-matter shape;
- parse the complete JSON envelope and check its schema version, `ok`, `partial`, data, and diagnostics fields;
- keep stdout as JSON-only and treat stderr separately;
- use canonical typed IDs and revisions returned by the command instead of extracting display codes from human output;
- call `exp validate` after any supported mutation and never reconstruct relationships from generated projections.

Command help and [commands.md](commands.md) are authoritative for syntax in this build. If metadata and recollection disagree, stop and use the metadata.

## Manual read-only fallback

When `exp` is unavailable, a repository must remain understandable with ordinary read-only file tools and Git:

1. Locate the experiments root by finding its `PROJECT.md`; version 1 permits one root per Git repository.
2. Read strict TOML front matter and Markdown bodies without editing them. Treat full typed IDs as identity; paths and short display codes are navigation aids.
3. Read canonical records rather than rebuilding facts from `README.md`, `ROADMAP.md`, `LEDGER.md`, or `DECISIONS.md`. Those files are deterministic projections and may be stale.
4. Keep Experiment lifecycle/verdict, Run evidence intent, and Attempt operational state separate. Do not infer missing inverse relationships or terminal state.
5. Treat external references as provider identity only. Do not claim that cached or committed provider state is live.
6. Report malformed, missing, ambiguous, or stale material explicitly. Do not repair it by hand.

The fallback is deliberately read-only. Do not hand-author a Plan, update front matter, regenerate a projection, allocate an ID, or emulate a transaction. Wait for a compatible exp binary or make an independently reviewed repository change outside this skill.

Never execute legacy experiment-harness scripts, another skill's helper scripts, notebook code, package installers, authentication flows, schedulers, or provider commands as part of fallback inspection.
