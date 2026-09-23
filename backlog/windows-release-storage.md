# Windows release: canonical storage and native runtime blockers

Status: deferred by maintainer on 2026-09-23. Do not publish an exp Windows
release or include exp in the Windows installPersonalTools suite until this
work is explicitly resumed and the required acceptance passes.

## Decision and scope

The six-tool Windows rollout continues with dev-cli, translate, lazyclash,
lazypueue, lazychezmoi and lazymlflow. An exp archive that starts and supports
Scoop upgrades is insufficient: ordinary canonical record transitions must
preserve the same conflict, ownership, recovery and publication guarantees.
Keep existing manual exp installations untouched. No exp release tag was created.

## Reproduction and evidence

Baseline main: `e81b0a5ffa32aad73bd8ec4491c415297c6fc007`.
Investigated branch: `release/windows-personal-tools`; saved implementation at
`5458c5b` (includes packaging, upgrade entry point and experimental native fixes).
The branch is work in progress and has not been merged into main.

[Native full-suite run at f009e21](https://github.com/daviddwlee84/exp-cli/actions/runs/35871799807)
passed the focused Windows pathx/lockx/Scoop gate and then failed the full native
suite. Linux/macOS and release-contract gates passed. There were 133 top-level
failed tests in the captured full-suite output; this is a bounded observation,
not a complete inventory of unsupported Windows behavior.

The representative failure is:

```
atomic publication failed at rename: atomic compare-and-swap replacement is unsupported on this platform
```

`internal/record/atomic.go` deliberately requires descriptor-relative atomic
exchange for canonical replacement: verify displaced identity and exact bytes,
then roll back on conflict. Windows has no implementation of that primitive.
It is unsafe to substitute the rebuildable-derived-file rename path, ignore
ErrAtomicCASUnsupported, or skip all canonical mutation tests to publish.
Source retirement, source locator changes, Idea qualification, Try transitions
and transaction recovery reach this boundary.

Other observed failures, to triage after the storage design:

- SQLite file URIs interpret `C:` as an authority and try `//C:/...`.
  Inspect `internal/operation/store_sqlite.go` and
  `internal/exploration/history_sqlite.go`; the lazy repositories have a tested
  drive-aware file-URI helper that may be reusable.
- Directory `Sync` reports access denied in migration and private snapshot
  bundles. Distinguish unsupported directory flush semantics from real I/O
  failures and document the resulting durability contract.
- Derived skill replacements can fail on Windows sharing semantics while the
  staged handle is still open; do not weaken canonical replacement to fix this.
- Git-created fixture repositories still inherit CRLF conversion and path-length
  limits even though this source checkout has LF attributes. Preserve canonical
  LF records and test linked worktrees/long branch names explicitly.
- Several privacy assertions still compare Unix mode bits; use owner/DACL,
  hardlink and reparse evidence on Windows. Some retarget tests are prevented
  by Windows directory sharing rules and need equivalent native assertions.
- The CLI privacy-error canary, worker preflight and stored Git-common-identity
  fixture also failed; retain those checks and inspect each after root fixes.

## Work already available, not released

The saved branch has real Scoop ownership/current-junction checks, a private
process-exit helper, read-only status, failure/no-op distinction, replay refusal
and interruption/cancellation tests. It also adds exp upgrade delegation to
verified Homebrew, amd64/arm64 ZIP packaging and a required native Windows lane.
None of those establish full runtime support.

Native ACL work learned that os.Root metadata handles cannot directly set
WRITE_DAC. Reopening the held object needs an identity comparison before setting
security. Windows may encode an owner-only inheritable grant using more than
one ACE; validate every effective grant, not an assumed single-ACE encoding.
LockFileEx is mandatory, so the lock byte must sit outside bounded JSON owner
metadata if competing processes need to read diagnostics.

## Resume and acceptance

1. Reconcile the saved branch against current main; preserve user work and
   re-evaluate all experimental changes before reuse.
2. Design a Windows canonical publication transaction with equivalent race,
   interrupted-write and recovery guarantees, or explicitly redesign the public
   storage contract. Document the decision before implementation.
3. Run native adversarial atomic create/replace/conflict/recovery tests, including
   file/ancestor swaps, concurrent readers/writers, hardlinks/reparse points,
   open-handle sharing and ACL preservation. Do not make canonical publication
   silently best-effort.
4. Resolve the remaining SQLite, directory durability, Git fixture, privacy and
   process-lifecycle cases; complete native Windows vet/race/full-suite checks.
5. Validate version/help/completion and real isolated Scoop install, read-only
   check, upgrade/no-op, other-instance blocking, checksum failure, cancellation
   and interrupted helper results. ARM64 payload/header checks do not claim
   native ARM64 execution.
6. Only then select a new immutable version, publish Windows assets, add exp to
   the central Scoop registry and the Windows personal-tools selector, and run
   the expanded isolated dotfiles installation/reapply acceptance.

Historical source/module size and immutable-release rules remain in force.
