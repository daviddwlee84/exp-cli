# Adhoc work and durable results

Use this flow for plots, data analysis, and a continuing sequence of small
explorations. Keep the user's goal and existing storage preferences; ordinary
organization within an authorized task does not require another approval.

## Start and continue

- Resolve the current Project/Source. Use `try start --title ... --goal ...`;
  outside a codebase use `--scratch`. The latter creates a private collection
  and one small Git Source under the remembered `try root`.
- For a scratch Try, the returned `source_path` is its dedicated authoring repo.
  For an existing Source it identifies the registered checkout; it is not a new
  code-edit workspace. Use the existing Experiment workspace/agent flow for
  isolated codebase changes, or a deliberately registered development worktree.
  Capture meaningful analysis code in Git, or explicitly use `--dirty=capture`
  for bounded temporary code. Data and outputs stay outside these repos.
- Carry full Project, Try and Attempt IDs. Use `try exec TRY` for a changed
  step; `retry` means identical Source/argv/input identity. Never resolve an
  ambiguous result using an unrelated global latest run.
- `try exec` always has managed outputs; the one-shot compatibility command
  `try run` enables them with `--outputs`, `--storage`, `--runner`, or `--input`.

## Choose storage once, then remember it

Inspect `storage show` (or `--global`) and `input list`. Consider expected bytes,
free space, whether the result must be shared, and the user's chosen service.
When a suitable profile already exists, use it. If a choice is needed, ask for
the storage destination and whether to remember it globally or for the Project.
Persist the answer with `storage add` and `storage use`; conversation memory is
not the preference store. `try root PATH` similarly remembers the adhoc root.

Local directory storage needs no service. For local MLflow, prefer a
`mlflow-local` profile with SQLite metadata and a separate artifact directory;
do not put large files inside SQLite. A persistent tracking server is optional
for local SDK use. Remote MLflow uses its configured artifact store/proxy.
The configured Python must already have MLflow; do not silently install it or
change the user's Python environment. Credentials use environment references.

Use `input bind NAME PATH` for external CSV/data folders and `--input NAME` on
execution. Workloads read `EXP_INPUT_NAME` and write beneath `EXP_OUTPUT_DIR`.
Do not embed host paths in canonical argv, hard-code a shared output filename
outside that directory, or write plots into the Source worktree.

Preparation checks available space on supported platforms. Estimate expected
output size rather than asserting that an unknown-size workload is small.
The default MLflow transfer review threshold is 1 GiB; preferences can change
it. A pending transfer retains local objects. Review the actual size and use
`results save --allow-large` within the user's chosen storage authorization.
Never silently switch a local preference to remote upload.

## Finish and retrieve

Use `results describe ATTEMPT NAME --description TEXT` for useful artifact labels.
Inspect `results list` and execution state, write a brief summary stating the
question, observation, and useful outputs, and use `try summarize`. When the
bounded exploration is finished, use `try finish --author agent --summary ...`;
saved artifact manifests are selected automatically. This is explicitly an
unreviewed agent conclusion. It does not impersonate human review or qualify
formal evidence. Human adoption and Promotion retain their existing gates.

`results save ATTEMPT` repairs artifact publication without re-running code.
Never rerun an uncertain workload to repair storage. Use `history search` with
words, Source, version, date, tags/body content or artifact names; `--all`
includes registered adhoc collections. `results compare` shows exact artifact
and runner identities, not an automatic scientific verdict. `results fetch`
verifies downloaded bytes; `results open` explicitly launches the desktop
opener. In an agent response, provide the Try/Attempt identities, a short
finding, and a useful result path or retrieval command.

## Prefer a reusable CLI when the workflow matures

A project runner profile supplies argv and an optional version command. Its
JSON output has `version` and optional full lowercase `build_commit`.
Selected lockfiles can be hashed with `--environment-file`. Keep actual binary
identity separate from the Source commit: `unknown` or `mismatch` must remain
visible. A script is appropriate for temporary work; do not require packaging
every one-off plot into a production CLI.

Direct Tries isolate Git worktrees and output directories, not GPU allocation
or all external side effects. Use the existing formal Queue for constrained
compute. Native Git retains worktree identity/cleanup; dev-cli lifecycle remains
unsupported until its exact machine contract is available.

An explicit executable after `try exec TRY --` overrides the remembered runner. Empty argv or argv starting with an option uses the remembered runner; use `--runner PROFILE` explicitly when passing positional runner arguments.
