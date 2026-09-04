# Records and Authority

Use one owner for each fact. `exp` links scientific meaning in an independent
canonical experiment repository to Source Git repositories and provider-owned
execution; it does not copy every system into one database. Reverse relationships,
Champions, generated pages, and TUI rows are derived rather than competing facts.

## Authority map

| Store or system | What it owns | What it cannot establish | How `exp` uses it |
|---|---|---|---|
| Canonical records under the selected experiment repository's `experiments/` | Project/Policy, Sources, Ideas, Tries, priced Plans, ResourcePools, ordered Queues, Experiment designs/closures, Runs, redacted Attempts, Evaluation protocols/results, Findings, Candidates, Releases, Promotions, Decisions. | Live provider freshness, local checkout location, config trust, raw telemetry, artifact bytes, Git integration, or daemon coordination. | Validate exact schema/revision/path/graph rules; mutate under the experiment clone's Git-common lock; derive reverse links and projections. |
| Canonical `Source` | Project-local Git Source ID/key, immutable `subdir`, sanitized locator history, active/retired state. | A local path, current checkout bytes, or permission to execute config from that checkout. | Resolve as an ordinary record. Bind every Attempt v3 snapshot and Candidate v2 source back to it. |
| XDG association store | Host-local mapping from Project UUID to canonical clone and `(Project, Source)` to a clone, including Git-common filesystem identity and observed sanitized locators. | Project/Source identity or scientific meaning. | Re-discover and revalidate on each use. Missing/stale/ambiguous mappings fail or require explicit selectors; no canonical record is copied to a Source repository. |
| Layered config and XDG trust store | Non-secret preferences plus exact-digest capability approval scoped to Project/Source/config path/Git-common filesystem identity. | Canonical authority, a different Project/Source, or approval after file/clone identity changes. | Resolve after authority, merge by defined rules, report leaf provenance, and fail closed for untrusted execution-bearing winners. |
| Source Git and native Git worktrees | Commits, trees, branches, changed paths, checkout registration, and integration history. | Whether an outcome supports a hypothesis or passes an Evaluation. | Capture full object IDs and exact paths; native Git verifies/inspects/cleans managed worktrees. No automatic merge or push. |
| Pueue | Live task/group state and scheduler task identity. | Scientific verdict, evidence inclusion, or worker result. | Submit only audited worker argv; reconcile bounded observations; explicit cancel requires canonical ownership and live group/label match. |
| MLflow | Workload-created runs, metrics, tags, parameters, traces, artifacts, and registry state. | Evaluation outcome, canonical Attempt ownership without the ownership tag, Candidate eligibility, or Promotion. | Read requested fields; keep only sanitized verified ExternalRefs. Artifact URI/bytes remain provider-owned. |
| Private SQLite at `<experiment-git-common-dir>/exp/runtime/v1/control.sqlite` | Leases, fencing tokens, jobs, submission outbox, fairness, pause state, provider observations, bounded events. | Hypothesis, conclusion, evidence disposition, Finding, Evaluation, Release, Promotion. | Coordinate and recover local work. It is rebuildable operational state, not scientific authority. |
| Worker terminal/result files | Durable exact process observation, frozen bounded result, output hashes, optional verified MLflow observation. | Scientific validity or a replacement for Attempt/Evaluation. | Validate identity/fencing/digests, repair SQLite when possible, and import selected facts through canonical CAS. |
| Dirty Source seed bundle | Private bounded bytes needed to reproduce one dirty Try snapshot. | Canonical evidence, a Candidate, or permission to delete changed work. | Authenticate by digest, seed only the managed Try worktree, and delete only after exact seeded cleanup. |
| Workspace provider registry | Invocation-local requested/actual provider and readiness/capability report. | Source identity, canonical checkout proof, or provider-local lifecycle authority. | Native Git is mandatory authority. `dev_cli` lifecycle capabilities currently fail closed because no exact machine receipt exists; trusted fallback may select native Git. |
| Generated pages/manifests and `exp ui` | Deterministic presentation from canonical records plus bounded local observations. | New facts, relationship ownership, mutation approval, or freshness beyond observation time. | Regenerate/refresh only. The TUI opens SQLite read-only and has no mutation, login, install, service-start, editor, or handoff action. |
| Public Notes, TODO, Backlog, Pitfall, Invariant | Their repository-defined prose purposes. | Canonical research relationships unless explicitly promoted through a domain operation. | Link to canonical IDs rather than synchronizing mutable copies. |

A dedicated private experiment repository is the recommended default. An
embedded/monorepo Project remains supported for shared governance and backward
compatibility. A Git submodule is checkout convenience only; it does not replace
Source or association authority. See [Architecture](../design/architecture.md#recommended-repository-arrangement).

## Canonical relationship ownership

A relationship is stored once on its declared owner:

| Relationship | Canonical owner |
|---|---|
| Source defines Git meaning and subdirectory | Source |
| Try declares Source set | Try |
| Try adopts Idea / Idea originates from Try | Try and Idea v2, written atomically as reciprocal edges |
| Idea descends, merges, or qualifies to Plan | Child/merged/qualified Idea |
| Plan assumes Finding, pins dependencies/resources, and results in Experiment | Plan |
| Queue orders Plan in ResourcePool/lane | Queue |
| Run belongs to Experiment | Run |
| Attempt belongs to Run or Try; retries Attempt; captures Sources | Attempt (v3 for Try/retry/Source) |
| Experiment includes/excludes Run as conclusion evidence | Experiment conclusion |
| Evaluation uses spec/evaluates subject; formal Evaluation binds Attempt | Evaluation (Attempt field in v2) |
| Finding cites evidence and weakens/overturns Finding | New Finding |
| Candidate packages Experiment/Evaluation; v2 pins Attempt/Sources | Candidate |
| Release fills a named slot and cites combination evidence | Release |
| Promotion continues the target chain | New Promotion |
| Decision supersedes Decision | New Decision |

Inventory scans compute reverse relations and enforce reciprocal Try/Idea,
Run/Attempt location, evaluation provenance, Candidate snapshot equality, and
Promotion-chain rules. Do not add inverse fields for navigation; regenerate a
projection instead. The complete schema list is in
[Record Format](../design/record-format.md#relationship-ownership-summary).

## Try versus formal authority

A Try is canonical exploratory history, but not promotion-bearing evidence.
Direct Try creates Attempt v3 owned by the Try, possibly with an explicitly
bounded dirty SourceSnapshot. Its terminal result digests can be selected into a
human conclusion, and that conclusion can be adopted as Idea v2. It cannot
become a Candidate.

Formal execution creates a Run-owned Attempt. Runtime v2 captures a clean
SourceSnapshot for the writable execution Source and every declared read-only
Source. A promotion-eligible chain requires:

1. successful terminal formal Attempt v3 with only clean snapshots;
2. its Run included in a closed, concluded, supported Experiment;
3. Evaluation v2 of that Experiment explicitly bound to the same Attempt;
4. Candidate v2 whose Source IDs, head commits, and path sets exactly equal that
   Attempt's snapshots.

A dirty Try must be rerun cleanly through this chain. Process success, a Git
commit, output hash, MLflow `FINISHED`, or an artifact URI alone satisfies none
of these scientific gates.

## Crossing an authority boundary

An ExternalRef is a bridge, not a transfer of authority. It may retain a
sanitized role, provider, context, native kind/ID, query-free URI, observation
time, and provider-namespaced bounded metadata. It never carries credentials,
raw environments, local checkout paths, unbounded provider output, or artifact
bytes, and never claims continued freshness.

The explicit formal worker authority flow is similarly one-way:

1. Pueue invokes hidden worker argv containing canonical root, Project UUID, and
   checkout-local scope for worker-job v2.
2. The worker re-discovers that exact Project and checks metadata-only job
   authority/fencing before reading payload bytes.
3. The payload must match Project/scope/Attempt/execution Source and every
   checkout/snapshot binding; non-execution Sources are read-only.
4. Native Git revalidates snapshots immediately before spawn and read-only
   Sources after success.
5. A durable marker can advance canonical Attempt state only through validated,
   revision-checked import.

Legacy worker-job v1 remains a separate exact path for already queued embedded
runtime work. Direct Try worker-job v2 cannot be launched through the hidden
worker command; `exp try resume` must reconstruct Source/config/worktree
authority first.

## Routing examples

| Statement | Correct owner |
|---|---|
| “This repository/subdirectory is a governed input.” | canonical Source; local clone path separately in associations |
| “Try this bounded change before formalizing it.” | Try and Try-owned Attempt; adopt as Idea if useful |
| “This comparable evidence supports the scoped claim.” | Finding linked to included Run evidence |
| “Pueue reports task 42 as running.” | Pueue; optionally a bounded observation reconciled to Attempt |
| “MLflow has an artifact at this URI.” | MLflow; at most a sanitized ExternalRef, never artifact authority in Git |
| “This clean formal source state passed the registered protocol.” | Evaluation v2 and Candidate v2 after all lineage gates |
| “Merge and push the experiment branch.” | Human Git integration action; not an automatic `exp` operation |
| “Stop this direction and reallocate budget.” | Decision based on Findings; a concrete follow-up may also be TODO |

When one statement serves two audiences, keep the fact with its authority and
link to it elsewhere. Do not synchronize two mutable copies.
