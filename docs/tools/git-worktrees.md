# Git and Worktrees

Git owns source bytes, commits, branches, worktree registration, and integration
history. `exp` binds those facts to canonical Source/Attempt/Candidate records,
but never infers scientific meaning from a commit and never automatically
merges, pushes, deploys, or deletes a retained branch.

The canonical experiment repository may be separate from every Source repository.
A Source record supplies project-local identity and immutable `subdir`; a
host-local association identifies the actual clone after Git-common and locator
validation. A Git submodule is optional checkout convenience only and does not
replace either layer.

## Managed location and identity

Identity-aware worktrees live outside every registered Source worktree under:

```text
$XDG_DATA_HOME/exp/worktrees/<project-uuid>/<source-id>/<owner-id>-<slug>
```

Unset `XDG_DATA_HOME` uses `~/.local/share`; a supplied value must be a clean
absolute path. The branch is:

```text
exp/<project-uuid>/<source-id>/<owner-id>-<slug>
```

The complete Project, Source, and owner IDs make identity independent of a host
path and collision-resistant across linked worktrees. An owner may be an
Experiment, Try, or Attempt as permitted by the caller. The legacy embedded
Experiment API retains its repository-derived XDG namespace and
`exp/<experiment-uuid-hex>-<slug>` branch so existing behavior is not
reinterpreted.

Workspace operations re-discover the exact Source root and Git common directory,
require a full lower-case SHA-1 or SHA-256 base object ID, reject symbolic or
abbreviated revisions, and never follow a substituted symlink. Managed paths
must be outside the Source repository and all of its registered worktrees.

## Formal Experiment workspace

An active Experiment with a locked design can prepare a code-edit workspace. If
a Source is selected, `--allow` globs are relative to its declared subdir; without
a Source, the exact embedded v1 behavior uses experiment-repository-root-relative
globs.

```bash
exp --workspace <PROJECT> --source <SOURCE> \
  experiment workspace prepare <EXPERIMENT> \
  --base 0123456789abcdef0123456789abcdef01234567 \
  --allow 'src/**' \
  --allow 'configs/**'
```

Preparation requires the selected source checkout to be completely clean,
resolves the exact base, creates one branch/worktree with Git hooks disabled for
the creation command, and verifies:

- exact repository/Git-common identity;
- expected branch and HEAD at base;
- clean new checkout;
- real non-symlink Source subdir;
- no forbidden canonical metadata path.

The workspace backend selector is resolved and reported, but current preparation
always executes through native Git. See [Provider behavior](#workspace-provider-boundary).

### Commit the exact change set

```bash
exp --workspace <PROJECT> --source <SOURCE> \
  experiment workspace commit <EXPERIMENT> \
  --base 0123456789abcdef0123456789abcdef01234567 \
  --allow 'src/**' \
  --allow 'configs/**' \
  --json
```

Commit re-discovers the deterministic worktree and validates branch, common Git
identity, ancestry, metadata, and both tracked/untracked path sets. It then:

1. rejects an empty change set;
2. rejects `.git`, canonical `experiments/` metadata when embedded, every path
   outside Source `subdir`, and every path outside the normalized allowlist;
3. stages exactly the observed paths and verifies the staged set;
4. creates one unsigned, no-hook/no-verify commit whose sole parent is the exact
   requested base;
5. verifies the checkout is clean and the committed path set is unchanged.

The ChangeSet returns base/head, branch, sorted paths, and a framed `sha256:` diff
identity. If the worktree already contains the expected single child commit, a
repeat verifies and returns it rather than committing again. For a Source-aware
request, only an Experiment owner may invoke this commit path.

### Code-edit agent

```bash
exp --workspace <PROJECT> --source <SOURCE> \
  experiment agent <EXPERIMENT> \
  --base 0123456789abcdef0123456789abcdef01234567 \
  --allow 'src/**' \
  --prompt implementation-notes.md \
  --json
```

This composes the same native preparation and commit verifier with one fresh
`experiment_implementer` CLI process. Agent output is advisory. If its
`changed_paths` differs from the actual commit, `exp` reports the mismatch and
keeps the Git diff authoritative. The command leaves the worktree and branch for
human review and integration.

A prepared commit is still not execution evidence. It is promotion-eligible only
after clean formal runtime captures it in Attempt v3, the Experiment includes
its Run, Evaluation v2 binds that Attempt, and Candidate v2 copies its exact
Source identities.

## Runtime v2 checkouts

Each formal runtime v2 Source explicitly chooses:

| Checkout | Meaning |
|---|---|
| `main` | The registered clone's current checkout must have the exact clean HEAD/ChangeSet. |
| `registered_worktree` | Select the unique registered linked worktree whose HEAD equals `head_commit`; no host path enters runtime config. |
| `managed_worktree` | Prepare/reuse the deterministic XDG worktree at the captured clean SourceSnapshot. |

One Source is writable from the worker contract's perspective; every additional
Source binding is read-only. All are captured clean before canonical dispatch,
bound into worker-job v2 by snapshot digest and private checkout identity, and
revalidated before process start. Read-only Source bytes are checked again after
a successful process. A missing, dirty, moved, stale, or mismatched Source blocks
submission rather than falling back to an unrelated checkout.

## Direct Try worktree

`exp try run` always uses a managed worktree and Attempt v3. With no `--allow`,
new changes are forbidden. Clean mode copies only committed state. Explicit
`--dirty=capture` may seed bounded pre-existing dirty state into the new
worktree; it never runs in the production checkout.

Dirty capture authenticates:

- the full tracked binary patch and exact tracked paths;
- bounded regular untracked files, modes, sizes, and hashes;
- clean direct submodule gitlinks/commits;
- Source, subdir, object format, base/head, policy, and snapshot digest.

Renames/copies, hidden index flags, ignored/unmerged state, dirty or nested
submodules, paths outside Source subdir, symlinks/special files, and exceeded
bounds fail closed. Clean submodules are materialized from local objects into
unregistered shared clones, not registered as worktrees of the production
submodule.

A private `preparing` marker scoped by Project/Source/owner/snapshot makes
preparation resumable. If a crash leaves an incomplete manager-owned worktree or
unadvanced branch, a later prepare may remove only the artifact authenticated by
that marker, then retry. A matching completed seed is reused only after complete
recapture; an unmarked mismatch is never overwritten.

## Inspection and cleanup

Native inspection verifies repository, Git-common identity, deterministic path,
branch, base ancestry, canonical metadata exclusions, and the union of working
and committed paths against exact paths/globs. It reports state without mutating
the worktree.

`exp try cleanup TRY --confirm` is the public managed cleanup flow:

- the Attempt must be terminal and registered as a direct Try;
- a clean worktree must still be clean, at base, and have no changed path on two
  inspections;
- a dirty seeded worktree must still be at base and match the complete private
  seed on two captures before Git's guarded `--force` removal is allowed;
- the bundle is reauthenticated immediately before deletion;
- a retired Source may be resolved only for this historical cleanup;
- partial removal/bundle/publication failures remain retryable and are reported
  truthfully.

Cleanup removes only the verified worktree and, for dirty Try, the private seed.
It deliberately does **not** delete the branch or commit, operation row,
terminal/result marker, or canonical Attempt. Formal Experiment workspace
commands likewise do not expose automatic cleanup. Human Git policy decides
when retained branches/worktrees are integrated or pruned.

## Workspace provider boundary

`native_git` is built in and is the correctness authority for prepare, verify,
inspect, cleanup, and retire. The registry also lists `dev_cli`, but all current
lifecycle capabilities—prepare, inspect, cleanup, open, handoff, retire—are
compiled `unsupported`. Executable discovery can report installed/not probed;
an explicit probe reports `machine-contract-unavailable` without invoking help,
version, inventory, login, or any lifecycle command.

The reason is contractual: current public `dev` output does not provide a
schema-versioned, content-free capability receipt or an exact-path open/retire
receipt with verifiable occupancy. `exp` therefore cannot prove that a provider
acted on the exact native worktree. A trusted selection may explicitly fall back
to native Git when configured; the resolution reports requested/actual provider
and reason. Optional provider task/catalog IDs never become Source, worktree, or
cleanup authority, and native postconditions are always final.

## Remaining limits

- No automatic merge, push, rebase, deployment, rollback, or branch deletion.
- No dirty formal run and no Candidate from a dirty Try; rerun cleanly.
- No large artifact storage in a managed worktree contract.
- No `dev_cli` lifecycle operation until an exact machine receipt exists.
- No automatic conflict resolution when a human integrates a retained branch.
