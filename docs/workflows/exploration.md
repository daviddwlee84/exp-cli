# Adhoc exploration, storage, and retrieval

Use a Try for a plot, data inspection, or a sequence of exploratory commands.
Each execution step has its own Attempt, Source snapshot, input identities,
output directory, and runner identity. A process exit status is an observation;
it does not create formal scientific evidence.

## Start with or without a codebase

In an initialized workspace with a selected Source:

```bash
exp try start --title "Inspect response times" --goal "Find patterns worth testing"
```

Without a codebase, create a small isolated Git Source:

```bash
exp try root                     # inspect the remembered collection root
exp try root /Volumes/research/tries
exp try start --scratch --title "Inspect response times" --goal "Find patterns worth testing" --json
```

The default collection is `$XDG_DATA_HOME/exp/tries` (otherwise
`~/.local/share/exp/tries`). `records/` is a dedicated research repository;
`sources/` contains one small Git repository per new scratch exploration.
The returned `source_path` is where the agent writes analysis code. The initial
README is committed locally; exp creates no remote or push. Changing `try root`
does not move old collections; registered Projects remain searchable.

Bare `try start` outside a Git repository also uses this scratch flow. Inside
an unregistered codebase, initialize its Source with `exp init` first, or choose
`--scratch` explicitly. Always carry the returned Project and Try IDs across
agent sessions; there is no global active/latest Try selector.

## Bind data and execute a step

Actual paths are stored only in private configuration. `input bind` inspects the
data; execution captures its current digest and verifies it before and after
running. Both regular files and directories are supported. Directory inspection
is bounded to 4096 regular files; package larger collections or select a smaller
input directory. Symlinks and special files are rejected.

```bash
exp --workspace PROJECT input bind sample /Volumes/data/response-times.csv
exp --workspace PROJECT try exec TRY --input sample --dirty=capture -- python3 plot.py
```

The workload receives `EXP_INPUT_SAMPLE`, `EXP_OUTPUT_DIR`, `EXP_TRY_ID`,
`EXP_ATTEMPT_ID`, and `EXP_PROJECT_ID`. For example:

```python
import os
from pathlib import Path

data = Path(os.environ["EXP_INPUT_SAMPLE"])
output = Path(os.environ["EXP_OUTPUT_DIR"])
# Read data; create a plot or report under output.
```

Commit analysis code before clean execution, or explicitly capture a bounded
dirty state for exploration. Use another `try exec TRY` for changed code,
parameters, or data; it creates a new Attempt. `try retry` retains the prior
Source/argv/input identity and allocates another independent output directory.
Concurrent steps never share output directories. Direct Tries do not reserve
Pool/GPU capacity; use the formal Queue for governed shared compute.

`try run` remains the one-command compatibility entry point. Add `--outputs`,
`--storage PROFILE`, `--runner PROFILE`, or `--input NAME` to enable managed
outputs. `try exec` always enables them.

## Remember storage preferences

Private settings live at `$XDG_CONFIG_HOME/exp/exploration.json`, otherwise
`~/.config/exp/exploration.json`. They are distinct from repository execution
trust. Defaults apply globally; a Project preference overrides its storage or
runner selection. An explicit invocation profile wins for that execution.

```bash
exp storage add disk --kind local --root /Volumes/research/artifacts
exp storage use disk --global
exp --workspace PROJECT storage show

exp storage add local-tracking --kind mlflow-local --root /Volumes/research/tracking
exp --workspace PROJECT storage use local-tracking

exp storage add team --kind mlflow-remote --root /Volumes/research/staging \
  --tracking-uri https://tracking.example.org --token-env RESEARCH_TRACKING_TOKEN
```

The built-in `local` profile uses `$XDG_DATA_HOME/exp/artifacts`. Artifact roots
must be outside the canonical and Source repositories. Local bytes are frozen
into content-addressed objects; Git contains only bounded manifests, descriptions
and digests. Worktree cleanup retains these objects and the artifact receipts.

The MLflow storage writer uses the installed Python SDK (`--python` selects its
executable). Install the full MLflow package (SQLite is not provided by mlflow-skinny) in that environment before choosing the profile;
exp does not install packages. `mlflow-local` uses `mlflow.db` in the configured
root for SQLite tracking metadata and `mlflow-artifacts/` for files. A persistent
tracking server is unnecessary for local SDK use; the MLflow UI can later use
that same database. `mlflow-remote` uses the configured server's artifact store;
use an artifact-proxy-enabled server for remote object storage. Credentials are
late-bound from the named environment variable.

One MLflow run belongs to one Project/Try/Attempt. Save recovery finds that same
run by ownership tags; duplicate matches fail explicitly. This storage writer
is separate from exp's existing read-only MLflow evaluation/observation adapter.

Estimate output size before execution and choose the remembered profile that
fits the task. Preparation checks local free space on supported platforms.
Remote/MLflow transfers above the profile's `large_bytes` threshold (default
1 GiB) stay pending with verified local objects until explicitly allowed:

```bash
exp --workspace PROJECT results save ATTEMPT --allow-large
```

Configure a different threshold with `storage add --large-bytes`; zero disables
this transfer threshold. It is not a process disk quota. A failed upload leaves
the Attempt's execution status intact and preserves local bytes. Retry
`results save`; do not rerun the workload to repair storage. Output scans are
bounded to 4096 files and canonical manifests to 512 KiB; use archives for
larger collections.

## Save summaries and find results

```bash
exp --workspace PROJECT try summarize TRY --summary "Tail latency has two clusters"
exp --workspace PROJECT try finish TRY --author agent \
  --summary "The plot suggests separating warm and cold requests"
exp history search "latency clusters" --all
exp --workspace PROJECT history search --source-key app --version COMMIT_PREFIX
exp --workspace PROJECT results describe ATTEMPT plots/latency.png --description "Latency distribution by request state"
exp --workspace PROJECT results list TRY
exp --workspace PROJECT results compare ATTEMPT_A ATTEMPT_B
exp --workspace PROJECT results fetch ATTEMPT plots/latency.png
exp --workspace PROJECT results open ATTEMPT plots/latency.png
```

`summarize` defaults to agent authorship and leaves the Try open. An explicit
`finish --author agent` selects saved artifact manifests automatically and
records an unreviewed agent conclusion without an additional human-confirmation
prompt. Human conclusions retain the existing confirmation flow; `--saved-results`
selects manifests without manual ExternalRef JSON. Adoption and formal Promotion
retain their human review requirements.

History searches titles, goals, summaries, body text, tags, runner versions and
artifact names. `--after`, `--before`, `--kind`, `--state`, `--source-key`, and
`--version` narrow the query. Its SQLite index is disposable and rebuilt from
selected canonical records; an unavailable registered Project is listed as
skipped, never silently represented by stale cached records.

`results list` is local and does not contact a provider. Fetch/open require an
exact name; if multiple Attempts produced it, select an Attempt explicitly.
Retrieval verifies the digest and returns a named cache file, or an explicit
`--destination`. A new host can fetch a remote artifact after configuring the
same storage profile name. Local-only artifacts require their original bytes or
a backup; a Git clone alone contains the manifest.

## Record the actual codebase CLI version

```bash
exp runner add app --version-arg app --version-arg version --version-arg=--json \
  --environment-file uv.lock -- app analyze
exp --workspace PROJECT runner use app
exp --workspace PROJECT try exec TRY -- --config configs/probe.toml
```

Runner arguments are argv elements, never shell text. The version command returns
`{"version":"1.2.0","build_commit":"FULL_LOWERCASE_GIT_OBJECT_ID"}`.
The actual executable digest, reported version/build commit and selected
environment-file digests are recorded separately from the Source snapshot.
Build/source agreement is `match`, `mismatch`, or `unknown`; missing version
information does not imply reproducibility. A changed executable blocks a
prepared invocation. Scripts remain valid for temporary analysis.

An explicit executable after `try exec TRY --` overrides the remembered runner. Empty argv or argv starting with an option uses the remembered runner; use `--runner PROFILE` explicitly when passing positional runner arguments.
