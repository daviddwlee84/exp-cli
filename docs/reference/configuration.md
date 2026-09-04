# Configuration and Paths

`exp` resolves canonical authority before it loads preferences. Canonical records,
local checkout associations, repository configuration, exact-digest trust,
runtime dispatch, provider profiles, and operational state have distinct schemas
and lifetimes. No config file can redirect a resolved Project or Source.

## Repository arrangement

The recommended default is a dedicated private experiment Git repository. Its
canonical root is still `experiments/`, while each code/data repository or
governed subdirectory is represented by an ordinary Source record. One Project
may have several Sources. A Git submodule is optional checkout convenience only;
it does not replace Source registration, local association validation, or
SourceSnapshot capture.

Embedded `experiments/` and monorepo layouts remain supported when code and
research share governance, and preserve existing Project v1 behavior. A Source's
immutable `subdir` selects its semantic root inside a clone; `.` selects the Git
root.

## Path summary and XDG rules

| Purpose | Default location | Authority and lifetime |
|---|---|---|
| Canonical records | `<experiment-git-root>/experiments/` | Git-backed scientific/decision authority for one Project. |
| User layered config | `$XDG_CONFIG_HOME/exp/config.toml` | Inherently trusted user preference layer; fallback `~/.config/exp/config.toml`. |
| Repository layered config | `<experiment-git-root>/.exp-cli/config.toml`, `<source-git-root>/.exp-cli/config.toml`, then subdirectory layers | Strict `exp.config/v1`; execution-bearing leaves require exact trust. |
| Project/Source associations | `$XDG_STATE_HOME/exp/associations/v1.json` | Private host-local paths and Git filesystem identities; fallback `~/.local/state/exp/associations/v1.json`. |
| Config trust receipts | `$XDG_STATE_HOME/exp/trust/v1.json` | Private exact-digest capability approvals; fallback `~/.local/state/exp/trust/v1.json`. |
| Dirty Source seed bundles | `$XDG_CACHE_HOME/exp/source-seeds/` | Disposable but authenticated private bytes for dirty Try replay; fallback `~/.cache/exp/source-seeds/`. |
| Managed worktrees | `$XDG_DATA_HOME/exp/worktrees/<project-id>/<source-id>/<owner-id>-<slug>` for identity-aware work | Local linked worktrees outside registered Source worktrees; fallback `~/.local/share`. |
| Agent CLI profiles | `$XDG_CONFIG_HOME/exp/agents.toml` | Separate `exp.agents/v1` execution loader; not merged into `exp.config/v1`. |
| Runtime contract | `<experiment-git-root>/.exp/runtime.json` | Separate closed `exp.runtime/v1` or `exp.runtime/v2` JSON decoder. |
| Canonical coordination | `<experiment-git-common-dir>/exp/v1/` | Private lock, Project receipt, reservations, transaction journals, workspace locks, and worker markers. |
| Operational database | `<experiment-git-common-dir>/exp/runtime/v1/control.sqlite` | Private leases/jobs/outbox/fairness/observations; never canonical. |

For config, state, and cache homes, an unset, empty, or relative XDG environment
value falls back through the absolute user home as required by the XDG base
specification. `XDG_DATA_HOME`, when supplied to worktree management, must itself
be a clean absolute path; unset uses `~/.local/share`. Private state directories
and files are verified as non-symlink `0700`/`0600` paths on supported Unix-like
systems. Windows creates protected owner-only DACLs through open handles and
rejects null, empty, inherited, or additional-principal ACLs. Read-only
inspection does not create, chmod, repair, or migrate missing/existing state.

Host paths in association/status output are operational details. Do not copy them
into canonical records, runtime files intended for another host, or public docs.

## Workspace and Source resolution

Root selectors are:

```text
--workspace <Project UUID or path>
--source <Source key, full ID, unique typed prefix, or display code>
```

Resolution is deterministic:

1. explicit `--workspace`, with optional explicit Source;
2. without explicit workspace, a physical Project marker in the invocation Git
   repository (the embedded compatibility path), with optional explicit Source;
3. otherwise, the most-specific validated registered Source containing the
   invocation directory.

An equally specific match is ambiguous. A physical Project and a containing
Source for a different Project are also ambiguous rather than silently choosing
one. An explicit Source outside Project/Source trees must match exactly one
registered Project. Every association is revalidated against canonical Project
UUID, Source ID, Git roots, Git-common filesystem identity, immutable subdir,
and sanitized remote-locator intersection. Retired Sources cannot be selected
for new work; historical resolution exists only for guarded cleanup.

`exp workspace register` records a canonical clone. `exp source add` atomically
publishes an ordinary Source, then registers the Project and Source clone; a
post-publication association failure is reported as partial and does not erase
the Source. `source register` repairs an association. `source append-locator`
and `source retire` require exact record revision plus confirmation.

## Layered `exp.config/v1`

Each file is strict TOML, at most 1 MiB, and rejects unknown fields. At most 64
layers and 32 Source-subdir-to-invocation directory levels are accepted. Files
that are the same filesystem object are applied once.

The exact low-to-high precedence is:

1. built-ins;
2. user config at `$XDG_CONFIG_HOME/exp/config.toml`;
3. canonical experiment-repository `.exp-cli/config.toml`;
4. selected Source clone-root `.exp-cli/config.toml`;
5. `.exp-cli/config.toml` at the Source's declared subdir and each directory down
   to the invocation directory, root to leaf.

Source and subdirectory layers exist only when a Source is selected and the
invocation is safely within its semantic root. Optional `[identity]` selectors
must equal the already resolved Project UUID and Source ID. They are assertions,
not redirects.

### Secure template

This template contains only public identifiers and environment **names**, never
resolved environment values or private host paths. Replace the angle-bracket
identifiers; omit `[identity]` where the file should be reusable across Projects.

```toml
schema = "exp.config/v1"

[identity]
project = "<PROJECT_UUID>"
source = "src_<SOURCE_UUID>"

[defaults]
source = "production"
workspace_backend = "native_git"
agent_profile = "research-agent"
mlflow_profile = "observer"

[ui]
color = "auto"

[workspace]
preferred_backends = ["native_git"]

[workspace.backends.native_git]
enabled = true
priority = 100

[workspace.backends.dev_cli]
enabled = false
priority = 10

[mlflow.profiles.observer]
context = "research"
binary = "mlflow"
timeout = "30s"
default_metrics = ["macro_f1", "validation_loss"]

[mlflow.profiles.observer.env.MLFLOW_TRACKING_URI]
from = "MLFLOW_TRACKING_URI"
secret = false
required = true

[mlflow.profiles.observer.env.MLFLOW_TRACKING_TOKEN]
from = "MLFLOW_TRACKING_TOKEN"
secret = true
required = true
```

The file does not expand environment variables. `from` names a parent variable
whose value is resolved immediately before the child process starts. Every
profile-provided value is redacted from process output/result handling even when
`secret = false`; `secret` is policy metadata, not permission to persist a value.
Child names beginning with `EXP_` are rejected in serialized MLflow workload
profiles so they cannot replace worker-owned variables.

### Merge semantics

- Scalar leaves use the highest applied value.
- `workspace.preferred_backends` replaces the complete array.
- `workspace.backends.<name>.enabled` and `.priority` merge independently by
  backend name. `native_git` must remain enabled as the correctness baseline.
- A named `mlflow.profiles.<name>` is replaced atomically by a higher layer; its
  fields/environment map/default metrics do not merge with the older definition.
- The built-in default is `workspace_backend = "native_git"`,
  `preferred_backends = ["native_git"]`, native enabled at priority 100,
  `ui.color = "auto"`, and no Source/agent/MLflow default.

Every effective leaf records winning source, layer kind, exact file digest,
trust status, and required capability. The combined config digest frames the
ordered exact layer digests and scopes; it is the value pinned by direct Try
Attempt provenance. `defaults.agent_profile` is currently policy/provenance data
only: agent execution still selects a role/profile from the separate
`exp.agents/v1` file or an explicit command `--profile`; the layered value is not
automatically passed to those commands. `exp config show` displays effective non-secret values;
`config explain [field]` displays winning provenance and all applied layers.
Overridden untrusted layers remain visible for audit but do not make the
effective summary untrusted when every winning execution-bearing leaf is trusted.

## Exact-digest trust

Built-ins and the user config are inherently trusted. Repository, Source, and
subdirectory files may always contribute pure identity assertions and UI color,
but these execution-bearing classes require a receipt:

```text
source.selection
workspace.backend
agent.profile
mlflow.profile
runtime.dispatch
```

A closed `exp.trust/v1` receipt binds:

- Project UUID and optional Source ID;
- canonical Git-common directory **and its filesystem identity**;
- Git-root-relative config scope;
- exact `sha256:` of the file bytes;
- sorted capability set and UTC approval time.

The receipt stores no command, config bytes, locator, environment value, or
credential. Editing the file changes its digest. Replacing the clone changes its
filesystem identity. Approval of a newer digest replaces overlapping capability
approval for older digests at the same logical scope, so reverting content does
not revive stale trust.

Trust uses the complete resolved authority context. When a Source is selected,
receipts for every repository layer—including the canonical experiment-repository
layer—bind that Source ID. Run `config explain` and `config trust` with the same
`--workspace`, `--source`, and invocation root (`--start-dir` when needed) that
execution will use. A workspace-only receipt does not implicitly approve those
bytes for every Source.

Review and approve the exact currently applied layer:

```bash
exp config explain
exp config trust \
  --path <CONFIG_PATH_FROM_EXPLAIN> \
  --digest sha256:<CURRENT_FILE_DIGEST> \
  --capability mlflow.profile \
  --confirm
```

Non-interactive or JSON trust requires `--path`, `--digest`, at least one
`--capability`, and `--confirm`. `config revoke --path ... --confirm` can revoke
a current or deleted scope; optional digest/capabilities narrow it. `config list`
returns sanitized receipts without Git-common paths. Repository execution
selection fails closed when the winning field is untrusted; it is never skipped
in favor of a lower layer.

Runtime v2 trust is selected through the same command, but `.exp/runtime.json`
is not an `exp.config/v1` layer. `config trust` recognizes its exact raw-file
digest only when its schema is `exp.runtime/v2`, and only the
`runtime.dispatch` capability is available. Runtime v1 deliberately does not use
that v2 receipt path.

## Workspace backend profiles

`--workspace-backend` has highest precedence; otherwise
`defaults.workspace_backend` is selected from effective config. Explicit
selection is invocation-local and trusted. Repository-derived selection and its
enablement/fallback leaves require `workspace.backend` trust.

`native_git` implements prepare, inspect, cleanup, and retire. `dev_cli` is
listed/discoverable, but every lifecycle capability currently reports
unsupported because its public CLI has no schema-versioned, exact-path machine
receipt. No `dev` lifecycle subprocess is invoked. An explicit `dev_cli` request,
or a trusted configured request whose fallback policy includes enabled
`native_git`, may resolve to native with an explicit fallback reason. Provider
priority is configuration metadata; it does not override the requested provider
or the native correctness checks.

Use:

```bash
exp workspace backend list --probe
exp workspace backend status --probe
```

Probes are local and bounded; they do not install, authenticate, start a service,
or contact a remote backend. Native Git always verifies prepared bytes, performs
inspection, and owns cleanup, regardless of requested provider.

## MLflow profiles

A named MLflow profile is an atomic config object with:

- project-safe profile name and non-secret context slug;
- binary **name** (not a path or command), resolved from `PATH`;
- positive timeout up to 24 hours;
- at most 128 child-to-parent environment-name bindings with
  `secret`/`required` policy;
- at most 256 default metric names.

`--mlflow-profile NAME` explicitly selects a trusted definition. Without it,
`defaults.mlflow_profile` selects a trusted profile. An explicit profile does not
bypass trust on a repository-defined profile body. Compatibility flags
`--mlflow-context`, `--allow-env`, and `--secret-env` construct the legacy
`mlflow`/30s profile and cannot be combined with a named profile.

Direct Try can pass an environment-bound profile to its local workload and then
perform optional read-only attachment observation. Formal runtime v2 over Pueue
rejects a profile with any environment bindings because the scheduler persists
task environments; use a workload-side credential broker. A value-free formal
profile may still specify context, binary, timeout, and default metrics. Provider
absence during optional worker observation is recorded as unavailable and does
not change successful process state. Explicit `provider mlflow verify` and an
Evaluation attachment remain strict: requested assertions must verify.

## Runtime contracts remain separate

`.exp/runtime.json` is strict JSON, not a layered config document. Both versions
reject unknown fields/trailing JSON and require `pools` and `plans` maps. The
following are templates: angle-bracket tokens must be replaced literally; no
variable interpolation occurs.

### Runtime v1: embedded compatibility

```json
{
  "schema_version": "exp.runtime/v1",
  "pools": {
    "pool_<POOL_UUID>": {
      "pueue_group": "gpu",
      "label_prefix": "exp-"
    }
  },
  "plans": {
    "plan_<PLAN_UUID>": {
      "executable": "/ABSOLUTE/PATH/TO/PROJECT_RUNNER",
      "argv": ["--config", "configs/run.toml"],
      "checkout": "main",
      "cwd": ".",
      "timeout": "4h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "0000000000000000000000000000000000000000",
      "head_commit": "1111111111111111111111111111111111111111",
      "change_set": ["configs/run.toml", "src/train.go"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
```

V1 executes in the experiment repository itself, accepts `main` or
`registered_worktree` (defaults to `main`/`.` if omitted), requires non-empty
argv and ChangeSet, and creates Attempt v2 plus worker job/terminal/result v1.
It is retained for embedded Projects; it cannot describe an external Source.

### Runtime v2: Source-aware formal execution

```json
{
  "schema_version": "exp.runtime/v2",
  "pools": {
    "pool_<POOL_UUID>": {
      "pueue_group": "gpu",
      "label_prefix": "exp-"
    }
  },
  "plans": {
    "plan_<PLAN_UUID>": {
      "execution_source": "src_<SOURCE_UUID>",
      "read_only_sources": [
        {
          "source": "src_<READ_ONLY_SOURCE_UUID>",
          "checkout": "main",
          "base_commit": "2222222222222222222222222222222222222222",
          "head_commit": "2222222222222222222222222222222222222222",
          "change_set": [],
          "observational_no_change": true
        }
      ],
      "executable": "/ABSOLUTE/PATH/TO/PROJECT_RUNNER",
      "argv": ["--config", "configs/run.toml"],
      "checkout": "managed_worktree",
      "cwd": ".",
      "timeout": "4h",
      "allowed_env": ["CUDA_VISIBLE_DEVICES"],
      "secret_env": [],
      "base_commit": "3333333333333333333333333333333333333333",
      "head_commit": "4444444444444444444444444444444444444444",
      "change_set": ["services/model/configs/run.toml"],
      "expected_outputs": ["outputs/metrics.json"]
    }
  }
}
```

V2 requires explicit `checkout` (`main`, `registered_worktree`, or
`managed_worktree`) for every Source. Each Source has same-format full base/head
object IDs and a required ChangeSet array. An empty ChangeSet is legal only with
`observational_no_change = true` and equal base/head; that flag is forbidden for
a non-empty set. `cwd` and expected outputs are relative to the execution
Source's declared subdir, while ChangeSet paths remain Git-root-relative.
Sources are unique, and non-execution bindings are read-only.

V2 resolves associations and layered config, requires exact runtime trust,
captures only clean SourceSnapshots, creates Attempt v3 and worker v2 envelopes,
and revalidates all identity before scheduler submission/spawn. `secret_env`
must be empty for Pueue, and credential-sensitive names are rejected from
`allowed_env`.

## Worker environment contract

Every worker supplies:

| Variable | Meaning |
|---|---|
| `EXP_JOB_ID` | Private job identity. |
| `EXP_ATTEMPT_ID` | Canonical Attempt identity. |
| `EXP_RESULT_PATH` | Exp-owned absolute private file for one bounded JSON result. |

A formal worker v2 additionally supplies:

| Variable | Meaning |
|---|---|
| `EXP_PROJECT_ID` | Explicit canonical Project UUID. |
| `EXP_CANONICAL_SCOPE` | Checkout-local canonical scope passed and verified by the worker. |
| `EXP_EXECUTION_SOURCE` | Canonical writable execution Source ID. |

The path value is operational and is never persisted in a canonical record.
The workload may write the assigned file but must not replace its identity.
Expected repository outputs are declared separately and hashed after process
success. Neither valid result JSON nor output hashes are scientific verdicts.

See [Runtime Dispatch](../workflows/runtime-dispatch.md),
[Git and Worktrees](../tools/git-worktrees.md), and
[MLflow](../tools/mlflow.md).
