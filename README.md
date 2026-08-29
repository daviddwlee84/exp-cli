# exp

`exp` is a Git-native research control plane for keeping an experimental thread
reviewable and resumable. It is not another tracker or scheduler: execution,
queueing, telemetry, artifacts, registries, authentication, and notebook
runtimes remain delegated to the upstream tools that own them.

## Status: functional walking path

This milestone implements one local, provider-free path from project
initialization to a priced Plan and deterministic repository views:

```sh
exp init --name "Encoder study"

exp plan add \
  --title "Calibrate encoder learning rate" \
  --priority P1 \
  --effort S \
  --payoff-summary "Avoid regressions while reducing calibration runs" \
  --payoff-metric macro_f1 \
  --payoff-unit score \
  --payoff-estimate 0.03 \
  --tags encoder,optimization

exp plan list
exp validate
exp render --check
exp context
```

Every project command discovers the containing Git worktree without changing
process directories. For tests and automation, `--start-dir <path>` explicitly
sets where discovery begins; the current directory is the default.

`exp init` requires Git, creates the single v1 root at `<repo>/experiments`, and
writes `PROJECT.md` plus all four generated projections. Re-running it is
idempotent. It refuses an unrelated or harness-v0 `experiments/` tree rather
than overwriting it.

## Implemented commands

```text
exp [--start-dir <path>]
├── init [--name <name>] [--json]
├── doctor [--json] [--live]
├── plan
│   ├── add [human flags | --input -] [--json]
│   └── list [--json]
├── validate [--json]
├── render [--check] [--json]
├── context [--json]
└── skill
    ├── print
    ├── install [--dir <path>] [--link] [--json]
    └── check [--dir <path>] [--links] [--json]
```

Run `exp <command> --help` for the complete flag descriptions.

### Versioned Plan input

Agents can create a Plan without prompts by sending exactly one strict JSON
request to standard input. The raw request must be valid UTF-8 and field names
are matched by an exact, case-sensitive allowlist. Unknown or case-variant
fields, duplicate or semantic-duplicate fields, trailing JSON, wrong schema
versions, and mixing `--input -` with human payload flags are rejected. Input is
bounded, and SIGINT or SIGTERM cancels a blocked read even while stdin remains
open.

```sh
exp plan add --input - --json <<'JSON'
{
  "schema_version": "exp.request.plan-add/v1",
  "title": "Calibrate encoder learning rate",
  "body": "\n# Calibrate encoder learning rate\n\nTest one bounded change.\n",
  "priority": "P1",
  "effort": "S",
  "expected_payoff": {
    "summary": "Avoid regressions while reducing calibration runs",
    "metric": "macro_f1",
    "unit": "score",
    "estimate": 0.03
  },
  "tags": ["encoder", "optimization"],
  "assumptions": []
}
JSON
```

`body` is optional; when omitted, `exp` writes a minimal Markdown title
heading. An explicitly empty body is invalid. Assumptions accept canonical
Finding IDs or unambiguous display references and are checked against the
canonical inventory before publication.

A successful JSON result includes the full canonical Plan ID, display code,
repository-relative record path, revision, and projection-refresh result.
`plan list` reads canonical Plan records only; it never parses `ROADMAP.md`.

## Machine output

Every `--json` command writes exactly one JSON document to stdout. The only CLI
envelope field for the contract version is `schema_version`:

```json
{
  "schema_version": "exp.cli/v1",
  "command": "plan list",
  "ok": true,
  "partial": false,
  "observed_at": "2026-08-29T00:00:00Z",
  "data": {},
  "diagnostics": []
}
```

Expected validation and drift failures, as well as flag, argument, and usage
parsing failures, still emit one envelope with `ok: false` and return a nonzero
status. Arrays are emitted as `[]`, not `null`. `partial: true` means the
response contains usable incomplete data or reports changes already published
before a later failure; `publication.durability_uncertain` specifically means
rename completed but directory durability could not be confirmed. Human warnings
and terminal errors go to stderr; prompts and human prose never enter machine
stdout.

## Canonical records and generated views

`PROJECT.md` and the per-entity Markdown/TOML files are authoritative.
`README.md`, `ROADMAP.md`, `LEDGER.md`, and `DECISIONS.md` are deterministic
projections derived solely from a valid canonical inventory. Rendering uses
same-directory atomic publication and contains no timestamp, hostname,
absolute path, provider cache, or live state.

`exp render --check` compares exact bytes and never writes. Missing or stale
projections produce a precise nonzero result. Invalid record, relationship,
lifecycle, path, or privacy diagnostics block rendering. `exp validate`
reports those inventory diagnostics directly.

`exp context` is also local-only. It reports project identity, canonical record
counts, queued Plans with IDs/display codes/revisions, and explicitly returns
`provider_refresh: false` and `live_observations: false`.

## Doctor and optional tools

`exp doctor` uses the compiled provider registry and performs only local
executable lookup (`LookPath`) in this milestone. It never executes discovered
third-party binaries, including `--version` commands, because those commands may
create configuration, telemetry, or log state. A found binary is reported with
an unknown version and unknown capabilities until a provider-specific safe probe
is implemented. Missing optional providers never make doctor fail.

`--live` is reserved for future explicit probes. In this milestone it performs
no additional process execution, daemon or network contact, and emits an
informational `doctor.live_not_implemented` diagnostic.

## Embedded skill

`exp skill print` prints this build's embedded guidance without touching HOME.
`exp skill check` is read-only and returns nonzero for missing or drifted known
files (and, with `--links`, requested consumer-link drift). Only
`exp skill install` mutates the selected destination; `--link` links into
supported consumer directories only when their bases already exist and never
replaces a real user directory. If the default home directory cannot be resolved
to a clean absolute path, install/check fail rather than falling back beneath the
current working directory.

The embedded `references/commands.md` is generated and byte-checked from the
approved metadata for the actual Cobra command paths. Full option details
remain authoritative in `exp <command> --help`.

## Explicitly deferred

This build does not implement experiment lifecycle transitions, Runs or
Attempts, conclusions, Finding/Decision mutations, provider reads or writes,
submission/cancellation, artifact transfer, harness migration, sweep/DAG
orchestration, or automatic scientific interpretation. No placeholder commands
are exposed for those later milestones.

## Development

Go 1.26.4 or a compatible newer toolchain is required.

```sh
make fmt-check
make vet
make test-race
make build
./exp --version
```

CI performs format checks, vetting, race-enabled tests, and versioned builds on
Linux and macOS, plus compile-only Windows and AIX portability checks.

## License

Newly authored source code in this repository is available under the MIT
License; see [LICENSE](LICENSE). Independently maintained external tools and
agent-skill content retain their own licenses, and this repository does not
claim to license that content.
