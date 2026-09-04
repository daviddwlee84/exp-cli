package research

import (
	"strings"
	"testing"
	"time"
)

func TestTryKindIdentityAndDisplayCode(t *testing.T) {
	if schema, err := KindTry.Schema(); err != nil || schema != SchemaTry {
		t.Fatalf("KindTry.Schema = %q, %v", schema, err)
	}
	id := mustID(t, "try_01a09000-0000-7001-8000-000000000001")
	if id.Kind() != KindTry {
		t.Fatalf("Try ID kind = %s", id.Kind())
	}
	code, err := DisplayCode(id, []ReferenceCandidate{{ID: id}})
	if err != nil || code != "Y-01A09000" {
		t.Fatalf("Try DisplayCode = %q, %v", code, err)
	}
	resolved, err := Resolve(code, KindTry, []ReferenceCandidate{{ID: id}})
	if err != nil || resolved != id {
		t.Fatalf("Resolve Try display = %s, %v", resolved, err)
	}
}

func TestTryLifecycleValidationRequiresExactStateFields(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	value := validTry(t, now)
	if err := Validate(value); err != nil {
		t.Fatalf("valid open Try: %v", err)
	}

	concluded := Clone(value).(*Try)
	concluded.State = TryConcluded
	concluded.UpdatedAt = now.Add(time.Minute)
	concluded.Conclusion = &TryConclusion{
		ConcludedAt: concluded.UpdatedAt,
		Summary:     "The smaller configuration merits a formal experiment.",
		ResultDigests: []string{
			"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	if err := Validate(concluded); err != nil {
		t.Fatalf("valid concluded Try: %v", err)
	}

	adopted := Clone(concluded).(*Try)
	adopted.State = TryAdopted
	adopted.UpdatedAt = concluded.UpdatedAt.Add(time.Minute)
	adopted.AdoptedIdea = mustID(t, "idea_01a09000-0000-7003-8000-000000000003")
	if err := Validate(adopted); err != nil {
		t.Fatalf("valid adopted Try: %v", err)
	}

	bad := Clone(value).(*Try)
	bad.State = TryAdopted
	if err := Validate(bad); !hasIssueCode(err, "try.lifecycle_fields") {
		t.Fatalf("adopted Try without conclusion/link validated: %v", err)
	}
	bad = Clone(value).(*Try)
	bad.Goal = strings.Repeat("g", MaxTryGoalBytes+1)
	if err := Validate(bad); !hasIssueCode(err, "try.goal_size") {
		t.Fatalf("oversized Try goal validated: %v", err)
	}
}

func TestAttemptV3ValidatesOwnersSnapshotsAndDispatch(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	attempt := validAttemptV3(t, now)
	if err := Validate(attempt); err != nil {
		t.Fatalf("valid clean Attempt v3: %v", err)
	}
	observational := Clone(attempt).(*Attempt)
	observational.SourceSnapshots[0].BaseCommit = observational.SourceSnapshots[0].HeadCommit
	observational.SourceSnapshots[0].ChangeSet = []string{}
	observational.SourceSnapshots[0].Digest, _ = SourceSnapshotDigest(observational.SourceSnapshots[0])
	if err := Validate(observational); err != nil {
		t.Fatalf("clean observational SourceSnapshot rejected: %v", err)
	}
	multiple := Clone(attempt).(*Attempt)
	readOnly := multiple.SourceSnapshots[0]
	readOnly.Source = mustID(t, "src_01a09000-0000-7003-8000-000000000003")
	readOnly.Digest, _ = SourceSnapshotDigest(readOnly)
	multiple.SourceSnapshots = append(multiple.SourceSnapshots, readOnly)
	if err := Validate(multiple); err != nil {
		t.Fatalf("additional read-only SourceSnapshot rejected: %v", err)
	}
	duplicate := Clone(attempt).(*Attempt)
	duplicate.SourceSnapshots = append(duplicate.SourceSnapshots, duplicate.SourceSnapshots[0])
	if err := Validate(duplicate); !hasIssueCode(err, "reference.duplicate") {
		t.Fatalf("duplicate SourceSnapshot validated: %v", err)
	}
	withProvenance := Clone(attempt).(*Attempt)
	withProvenance.Provenance = &Provenance{
		CapturedAt: now, GitCommit: withProvenance.SourceSnapshots[0].HeadCommit,
		GitDirty: false, Reproducibility: ReproducibilityExact,
	}
	if err := Validate(withProvenance); err != nil {
		t.Fatalf("matching v3 provenance rejected: %v", err)
	}
	withProvenance.Provenance.GitCommit = strings.Repeat("c", 40)
	if err := Validate(withProvenance); !hasIssueCode(err, "attempt.provenance_source") {
		t.Fatalf("contradictory v3 provenance validated: %v", err)
	}

	both := Clone(attempt).(*Attempt)
	both.Try = mustID(t, "try_01a09000-0000-7004-8000-000000000004")
	if err := Validate(both); !hasIssueCode(err, "attempt.owner") {
		t.Fatalf("Attempt with two owners validated: %v", err)
	}

	dirtyFormal := Clone(attempt).(*Attempt)
	makeDirtySnapshot(t, &dirtyFormal.SourceSnapshots[0])
	if err := Validate(dirtyFormal); !hasIssueCode(err, "attempt.dirty_owner") {
		t.Fatalf("dirty Run-backed Attempt validated: %v", err)
	}

	dirtyTry := Clone(dirtyFormal).(*Attempt)
	dirtyTry.Run = ID{}
	dirtyTry.Try = mustID(t, "try_01a09000-0000-7004-8000-000000000004")
	if err := Validate(dirtyTry); err != nil {
		t.Fatalf("dirty Try-backed Attempt rejected: %v", err)
	}
	absoluteTry := Clone(dirtyTry).(*Attempt)
	absoluteTry.Argv = []string{"/usr/bin/true", "--config=/etc/passwd"}
	if err := Validate(absoluteTry); !hasIssueCode(err, "privacy.host_path") {
		t.Fatalf("host-absolute direct Try argv validated: %v", err)
	}

	partialDispatch := Clone(attempt).(*Attempt)
	partialDispatch.DispatchID = "dispatch-1"
	if err := Validate(partialDispatch); !hasIssueCode(err, "attempt.dispatch") {
		t.Fatalf("partial v3 dispatch validated: %v", err)
	}

	badDigest := Clone(attempt).(*Attempt)
	badDigest.SourceSnapshots[0].Digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := Validate(badDigest); !hasIssueCode(err, "source_snapshot.digest_mismatch") {
		t.Fatalf("mismatched SourceSnapshot digest validated: %v", err)
	}
}

func TestCandidateV2AndIdeaV2VersionGates(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	sourceID := mustID(t, "src_01a09000-0000-7002-8000-000000000002")
	candidate := &Candidate{
		Common: Common{
			Schema: SchemaCandidateV2,
			ID:     mustID(t, "cand_01a09000-0000-7005-8000-000000000005"),
			Title:  "Formal candidate", CreatedAt: now, UpdatedAt: now,
		},
		Experiment: mustID(t, "exp_01a09000-0000-7006-8000-000000000006"),
		Evaluation: mustID(t, "eval_01a09000-0000-7007-8000-000000000007"),
		Attempt:    mustID(t, "att_01a09000-0000-7008-8000-000000000008"),
		Sources: []CandidateSource{{
			Source: sourceID, HeadCommit: strings.Repeat("a", 40), ChangeSet: []string{},
		}},
	}
	if err := Validate(candidate); err != nil {
		t.Fatalf("valid Candidate v2: %v", err)
	}
	legacyCandidate := Clone(candidate).(*Candidate)
	legacyCandidate.Schema = SchemaCandidate
	if err := Validate(legacyCandidate); !hasIssueCode(err, "record.schema_field") {
		t.Fatalf("Candidate v1 accepted v2 fields: %v", err)
	}

	origin := mustID(t, "try_01a09000-0000-7009-8000-000000000009")
	idea := validIdeaV2(t, now)
	idea.OriginTry = origin
	if err := Validate(idea); err != nil {
		t.Fatalf("valid Idea v2 origin_try: %v", err)
	}
	autonomous := Clone(idea).(*Idea)
	autonomous.ProposedBy = "agent:auto"
	if err := Validate(autonomous); !hasIssueCode(err, "idea.human_adoption") {
		t.Fatalf("hand-edited autonomous Try adoption validated: %v", err)
	}
	idea.Schema = SchemaIdea
	if err := Validate(idea); !hasIssueCode(err, "record.schema_field") {
		t.Fatalf("Idea v1 accepted origin_try: %v", err)
	}
}

func validTry(t *testing.T, now time.Time) *Try {
	t.Helper()
	return &Try{
		Common: Common{
			Schema: SchemaTry,
			ID:     mustID(t, "try_01a09000-0000-7001-8000-000000000001"),
			Title:  "Quick encoder check", CreatedAt: now, UpdatedAt: now,
		},
		State: TryOpen, Goal: "Determine whether a smaller encoder is worth formal study.",
		Sources: []ID{mustID(t, "src_01a09000-0000-7002-8000-000000000002")},
	}
}

func validAttemptV3(t *testing.T, now time.Time) *Attempt {
	t.Helper()
	sourceID := mustID(t, "src_01a09000-0000-7002-8000-000000000002")
	snapshot := SourceSnapshot{
		Source: sourceID, Subdir: ".", PolicyVersion: "capture-v1", CapturedAt: now,
		GitObjectFormat: GitObjectSHA1,
		BaseCommit:      strings.Repeat("0", 40), HeadCommit: strings.Repeat("a", 40),
		ChangeSet: []string{"internal/model.go"}, State: SourceSnapshotClean,
		Reproducibility: ReproducibilityExact,
	}
	digest, err := SourceSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Digest = digest
	return &Attempt{
		Common: Common{
			Schema: SchemaAttemptV3,
			ID:     mustID(t, "att_01a09000-0000-7010-8000-000000000010"),
			Title:  "Formal execution", CreatedAt: now, UpdatedAt: now,
		},
		Run:   mustID(t, "run_01a09000-0000-7011-8000-000000000011"),
		State: AttemptPlanned, Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"go", "test", "./..."},
		ExecutionSource: sourceID, SourceSnapshots: []SourceSnapshot{snapshot},
	}
}

func makeDirtySnapshot(t *testing.T, snapshot *SourceSnapshot) {
	t.Helper()
	snapshot.State = SourceSnapshotDirty
	snapshot.DirtyDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	snapshot.DirtySummary = "tracked_changes=1; untracked_files=0"
	snapshot.Reproducibility = ReproducibilityBounded
	digest, err := SourceSnapshotDigest(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Digest = digest
}

func validIdeaV2(t *testing.T, now time.Time) *Idea {
	t.Helper()
	return &Idea{
		Common: Common{
			Schema: SchemaIdeaV2,
			ID:     mustID(t, "idea_01a09000-0000-7012-8000-000000000012"),
			Title:  "Formalize encoder study", CreatedAt: now, UpdatedAt: now,
		},
		State: IdeaProposed, Summary: "Formalize the promising Try.", ProposedBy: "human:david",
		PrimaryCluster: "encoder",
		Classification: Classification{
			Domain: "ml", Work: "training", Method: "ablation", Component: "encoder",
			Lane: LaneExplore, Risk: RiskLow, Horizon: HorizonShort, Origin: OriginHuman,
		},
	}
}
