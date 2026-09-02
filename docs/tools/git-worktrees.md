# Git and Worktrees

Git owns source history in the `exp` architecture. `exp` can prepare an
isolated linked worktree for an active Experiment and create one exact commit,
but it never merges that branch, removes the worktree, or changes the
human-owned integration branch.

## Preconditions

Workspace commands require an active Experiment with a locked design. Create a
follow-up Experiment instead of modifying closed evidence. The source checkout
must be clean, and `--base` must name the complete lower-case Git object ID;
symbolic revisions and abbreviated hashes are rejected.

At least one repeatable `--allow` glob is required. Globs are root-relative
POSIX paths such as `src/**` or `configs/*.toml`. They define the complete area
the experiment may change; they are not a suggestion to the agent.

## Prepare an isolated workspace

```bash
EXPERIMENT_ID='exp_...'
BASE_COMMIT='0123456789abcdef0123456789abcdef01234567'

exp experiment workspace prepare "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**'
```

`prepare` verifies the exact repository and base commit, requires a clean
source checkout, and creates a branch named from the full Experiment UUID and a
bounded title slug under `exp/…`. The linked worktree is stored outside the
source repository beneath the XDG data home, separated by a repository-specific
namespace. The new checkout must point at the requested base, be on the expected
branch, and be clean.

The command refuses to reuse an existing path or follow a substituted symlink.
Its machine result identifies the repository, worktree, base commit, branch,
and normalized allowlist.

## Commit the exact change set

```bash
exp experiment workspace commit "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**' \
  --json
```

`commit` re-discovers the expected worktree, confirms that it belongs to the
same Git common directory and branch, and collects both tracked and untracked
changes. It then:

1. rejects an empty change set;
2. rejects `.git`, everything under `experiments/`, and any path outside the
   normalized allowlist;
3. stages exactly the observed paths and verifies the staged path set;
4. creates one commit whose parent is the requested base;
5. verifies that the resulting checkout is clean and the committed path set is
   still exact.

The returned ChangeSet contains `base_commit`, `head_commit`, `branch`, sorted
`paths`, and a `sha256:` diff identity derived from those exact values. If the
worktree already contains the expected single commit, a repeated call verifies
and returns it instead of creating another commit.

## Run the code-edit agent workflow

```bash
exp experiment agent "$EXPERIMENT_ID" \
  --base "$BASE_COMMIT" \
  --allow 'src/**' \
  --allow 'configs/**' \
  --prompt implementation-notes.md \
  --json
```

This combines workspace preparation, one fresh `experiment_implementer` Agent
CLI process, and the same exact commit validation. The committed Git diff is
authoritative; if the agent's reported `changed_paths` differ, `exp` returns a
diagnostic rather than trusting the report.

See [Agent CLI profiles](agent-cli-profiles.md) for profile configuration.

## Use a prepared worktree at runtime

In `.exp/runtime.json`, set `checkout` to `registered_worktree` when a Plan must
run from the unique linked worktree whose HEAD equals `head_commit`. The config
stores Git identity rather than a host path. Before dispatch, the daemon checks
repository identity, exact HEAD, base ancestry, committed ChangeSet, and a clean
executable tree.

## Evidence boundary

A commit is prepared code, not execution evidence. It becomes eligible to back
a Candidate only after an included Run has a successful direct Attempt whose
Git identity and ChangeSet match, plus a passing scientific Evaluation. Human
review still controls whether and how the experiment branch is integrated.

## Future topics

- Document a reviewed manual cleanup procedure for completed worktrees.
- Add examples for large allowlists and repository-specific path conventions.
- Explain conflict handling when humans integrate an experiment branch.
- Explore signed commits without weakening exact-path validation.
