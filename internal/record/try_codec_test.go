package record

import (
	"bytes"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestLegacyV1FixtureNormalizedBytesRemainStable(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "v1", "valid-project")
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(filename) != ".md" {
			return nil
		}
		switch entry.Name() {
		case "README.md", "ROADMAP.md", "LEDGER.md", "DECISIONS.md":
			return nil
		}
		original, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		document, err := Decode(original)
		if err != nil {
			t.Fatalf("Decode %s: %v", filename, err)
		}
		encoded, err := Encode(document)
		if err != nil {
			t.Fatalf("Encode %s: %v", filename, err)
		}
		for _, forbidden := range []string{"origin_try", "execution_source", "source_snapshots", "adopted_idea"} {
			if bytes.Contains(encoded, []byte(forbidden)) {
				t.Fatalf("legacy fixture encoder leaked %q for %s", forbidden, filename)
			}
		}
		normalized, err := Decode(encoded)
		if err != nil {
			t.Fatalf("Decode normalized %s: %v", filename, err)
		}
		reencoded, err := Encode(normalized)
		if err != nil || !bytes.Equal(encoded, reencoded) {
			t.Fatalf("legacy normalized bytes changed for %s: %v", filename, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTryEvidenceSchemasRoundTripDeterministically(t *testing.T) {
	now := time.Date(2026, 9, 3, 13, 0, 0, 0, time.UTC)
	sourceID := mustControlID(t, "src_01a09000-0000-7101-8000-000000000101")
	tryID := mustControlID(t, "try_01a09000-0000-7102-8000-000000000102")
	ideaID := mustControlID(t, "idea_01a09000-0000-7103-8000-000000000103")
	attemptID := mustControlID(t, "att_01a09000-0000-7104-8000-000000000104")

	try := &research.Try{
		Common: research.Common{Schema: research.SchemaTry, ID: tryID, Title: "Quick check", CreatedAt: now, UpdatedAt: now},
		State:  research.TryOpen, Goal: "Check whether the direction merits formal study.", Sources: []research.ID{sourceID},
	}
	idea := &research.Idea{
		Common: research.Common{Schema: research.SchemaIdeaV2, ID: ideaID, Title: "Formalize check", CreatedAt: now, UpdatedAt: now},
		State:  research.IdeaProposed, Summary: "Turn the Try into a formal experiment.", ProposedBy: "human:david", PrimaryCluster: "encoder",
		Classification: research.Classification{Domain: "ml", Work: "training", Method: "ablation", Component: "encoder", Lane: research.LaneExplore, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman},
		OriginTry:      tryID,
	}
	snapshot := codecSnapshot(t, sourceID, now, research.SourceSnapshotDirty)
	attempt := &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV3, ID: attemptID, Title: "Quick execution", CreatedAt: now, UpdatedAt: now},
		Try:    tryID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"go", "test", "./..."},
		ExecutionSource: sourceID, SourceSnapshots: []research.SourceSnapshot{snapshot},
	}
	evaluationID := mustControlID(t, "eval_01a09000-0000-7107-8000-000000000107")
	experimentID := mustControlID(t, "exp_01a09000-0000-7106-8000-000000000106")
	evaluation := &research.Evaluation{
		Common: research.Common{Schema: research.SchemaEvaluationV2, ID: evaluationID, Title: "Attempt-bound result", CreatedAt: now, UpdatedAt: now},
		Spec:   mustControlID(t, "evalspec_01a09000-0000-7108-8000-000000000108"), Subject: experimentID, Attempt: attemptID,
		Outcome: research.EvaluationPassed, EvaluatedAt: now, Metrics: []research.MetricValue{{Name: "score", Value: 1, Unit: "points"}}, Summary: "Passed.",
	}
	candidate := &research.Candidate{
		Common:     research.Common{Schema: research.SchemaCandidateV2, ID: mustControlID(t, "cand_01a09000-0000-7105-8000-000000000105"), Title: "Formal candidate", CreatedAt: now, UpdatedAt: now},
		Experiment: experimentID,
		Evaluation: evaluationID,
		Attempt:    attemptID,
		Sources: []research.CandidateSource{{
			Source: sourceID, HeadCommit: strings.Repeat("b", 40), ChangeSet: []string{"internal/model.go"},
		}},
	}

	for name, value := range map[string]research.Record{"try": try, "idea-v2": idea, "attempt-v3": attempt, "evaluation-v2": evaluation, "candidate-v2": candidate} {
		t.Run(name, func(t *testing.T) {
			encoded, err := Encode(&Document{Record: value, Body: "# " + name + "\n"})
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode: %v\n%s", err, encoded)
			}
			again, err := Encode(decoded)
			if err != nil || !bytes.Equal(encoded, again) {
				t.Fatalf("non-deterministic round trip: %v\n%s\n%s", err, encoded, again)
			}
		})
	}
}

func TestOldSchemaDecodersRejectNewFieldsAndEncodersDoNotLeakThem(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC)
	sourceID := mustControlID(t, "src_01a09000-0000-7201-8000-000000000201")
	tryID := mustControlID(t, "try_01a09000-0000-7202-8000-000000000202")

	idea := &research.Idea{
		Common: research.Common{Schema: research.SchemaIdeaV2, ID: mustControlID(t, "idea_01a09000-0000-7203-8000-000000000203"), Title: "Adopt", CreatedAt: now, UpdatedAt: now},
		State:  research.IdeaProposed, Summary: "Adopt result.", ProposedBy: "human:david", PrimaryCluster: "encoder",
		Classification: research.Classification{Domain: "ml", Work: "training", Method: "ablation", Component: "encoder", Lane: research.LaneExplore, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman},
		OriginTry:      tryID,
	}
	ideaBytes := mustEncodeDocument(t, idea)
	assertUnknownField(t, bytes.Replace(ideaBytes, []byte(research.SchemaIdeaV2), []byte(research.SchemaIdea), 1))

	attempt := &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV3, ID: mustControlID(t, "att_01a09000-0000-7204-8000-000000000204"), Title: "Try attempt", CreatedAt: now, UpdatedAt: now},
		Try:    tryID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"true"},
		ExecutionSource: sourceID, SourceSnapshots: []research.SourceSnapshot{codecSnapshot(t, sourceID, now, research.SourceSnapshotDirty)},
	}
	attemptBytes := mustEncodeDocument(t, attempt)
	assertUnknownField(t, bytes.Replace(attemptBytes, []byte(research.SchemaAttemptV3), []byte(research.SchemaAttemptV2), 1))

	evaluation := &research.Evaluation{
		Common: research.Common{Schema: research.SchemaEvaluationV2, ID: mustControlID(t, "eval_01a09000-0000-7209-8000-000000000209"), Title: "Bound result", CreatedAt: now, UpdatedAt: now},
		Spec:   mustControlID(t, "evalspec_01a09000-0000-7212-8000-000000000212"), Subject: mustControlID(t, "exp_01a09000-0000-7213-8000-000000000213"), Attempt: attempt.ID,
		Outcome: research.EvaluationPassed, EvaluatedAt: now, Metrics: []research.MetricValue{{Name: "score", Value: 1, Unit: "points"}}, Summary: "Passed.",
	}
	evaluationBytes := mustEncodeDocument(t, evaluation)
	assertUnknownField(t, bytes.Replace(evaluationBytes, []byte(research.SchemaEvaluationV2), []byte(research.SchemaEvaluation), 1))

	candidate := &research.Candidate{
		Common:     research.Common{Schema: research.SchemaCandidateV2, ID: mustControlID(t, "cand_01a09000-0000-7205-8000-000000000205"), Title: "Candidate", CreatedAt: now, UpdatedAt: now},
		Experiment: mustControlID(t, "exp_01a09000-0000-7206-8000-000000000206"),
		Evaluation: mustControlID(t, "eval_01a09000-0000-7207-8000-000000000207"),
		Attempt:    attempt.Common.ID,
		Sources:    []research.CandidateSource{{Source: sourceID, HeadCommit: strings.Repeat("b", 40), ChangeSet: []string{}}},
	}
	candidateBytes := mustEncodeDocument(t, candidate)
	assertUnknownField(t, bytes.Replace(candidateBytes, []byte(research.SchemaCandidateV2), []byte(research.SchemaCandidate), 1))

	legacyAttempt := &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV2, ID: mustControlID(t, "att_01a09000-0000-7208-8000-000000000208"), Title: "Legacy attempt", CreatedAt: now, UpdatedAt: now},
		Run:    mustControlID(t, "run_01a09000-0000-7209-8000-000000000209"), State: research.AttemptPlanned,
		Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"true"},
		Pool: mustControlID(t, "pool_01a09000-0000-7210-8000-000000000210"), Queue: mustControlID(t, "queue_01a09000-0000-7211-8000-000000000211"),
		QueueRevision: 1, Lane: research.LaneExploit, DispatchID: "legacy-1",
		BaseCommit: strings.Repeat("0", 40), HeadCommit: strings.Repeat("a", 40), ChangeSet: []string{"model.go"},
	}
	legacyBytes := mustEncodeDocument(t, legacyAttempt)
	for _, forbidden := range []string{"execution_source", "source_snapshots", "try ="} {
		if bytes.Contains(legacyBytes, []byte(forbidden)) {
			t.Fatalf("legacy Attempt encoder leaked %q:\n%s", forbidden, legacyBytes)
		}
	}
}

func TestTryConclusionExternalRefMetadataRoundTrips(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 30, 0, 0, time.UTC)
	value := &research.Try{
		Common: research.Common{
			Schema:    research.SchemaTry,
			ID:        mustControlID(t, "try_01a09000-0000-7250-8000-000000000250"),
			Title:     "External evidence",
			CreatedAt: now,
			UpdatedAt: now.Add(time.Minute),
		},
		State:   research.TryConcluded,
		Goal:    "Preserve nested tracker metadata.",
		Sources: []research.ID{mustControlID(t, "src_01a09000-0000-7251-8000-000000000251")},
		Conclusion: &research.TryConclusion{
			ConcludedAt: now.Add(time.Minute),
			Summary:     "The external evidence is useful.",
			ExternalRefs: []research.ExternalRef{{
				Role: research.ExternalTracker, Provider: "mlflow", Context: "local",
				NativeKind: "run", NativeID: "run-1",
				Metadata: map[string]any{"mlflow.note": map[string]any{"nested": "bounded"}},
			}},
		},
	}
	encoded, err := Encode(&Document{Record: value, Body: "# External evidence\n"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode encoded Try: %v\n%s", err, encoded)
	}
	again, err := Encode(decoded)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("metadata round trip changed: %v\n%s\n%s", err, encoded, again)
	}
}

func TestTryCanonicalLayoutUsesFullUUIDAcrossPrefixCollision(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 45, 0, 0, time.UTC)
	first := &research.Try{Common: research.Common{Schema: research.SchemaTry, ID: mustControlID(t, "try_01a09000-0000-7252-8000-000000000252"), Title: "Same title", CreatedAt: now, UpdatedAt: now}}
	second := &research.Try{Common: research.Common{Schema: research.SchemaTry, ID: mustControlID(t, "try_01a09000-ffff-7253-8000-000000000253"), Title: "Same title", CreatedAt: now, UpdatedAt: now}}
	firstPath, firstErr := PathForNew(first, nil)
	secondPath, secondErr := PathForNew(second, nil)
	if firstErr != nil || secondErr != nil || firstPath == secondPath {
		t.Fatalf("colliding-prefix Try paths = %q, %q; errors %v, %v", firstPath, secondPath, firstErr, secondErr)
	}
	if !strings.Contains(firstPath, first.ID.UUIDHex()) || !strings.Contains(secondPath, second.ID.UUIDHex()) {
		t.Fatalf("Try paths do not carry full UUID identity: %q %q", firstPath, secondPath)
	}
}

func TestTryCanonicalLayoutAndNamespaceCompatibility(t *testing.T) {
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	tryID := mustControlID(t, "try_01a09000-0000-7301-8000-000000000301")
	value := &research.Try{
		Common: research.Common{Schema: research.SchemaTry, ID: tryID, Title: "Fast encoder check", CreatedAt: now, UpdatedAt: now},
		State:  research.TryOpen, Goal: "Bounded check.", Sources: []research.ID{mustControlID(t, "src_01a09000-0000-7302-8000-000000000302")},
	}
	tryPath, err := PathForNew(value, nil)
	if err != nil || tryPath != "t-01a09000000073018000000000000301-fast-encoder-check/TRY.md" {
		t.Fatalf("Try path = %q, %v", tryPath, err)
	}
	location, recognized, err := ClassifyPath(tryPath)
	if err != nil || !recognized || location.Kind != research.KindTry || location.TryDir != "t-01a09000000073018000000000000301-fast-encoder-check" {
		t.Fatalf("Try location = %#v, %v, %v", location, recognized, err)
	}
	attemptPath := path.Join(location.TryDir, "attempts", "att_01a09000-0000-7303-8000-000000000303.md")
	attemptLocation, recognized, err := ClassifyPath(attemptPath)
	if err != nil || !recognized || attemptLocation.Kind != research.KindAttempt || attemptLocation.TryDir != location.TryDir {
		t.Fatalf("Try Attempt location = %#v, %v, %v", attemptLocation, recognized, err)
	}
	if _, recognized, err := ClassifyPath("t-legacy-notes/notes.md"); err != nil || recognized {
		t.Fatalf("legacy t-* path became reserved: recognized=%v err=%v", recognized, err)
	}
	if _, recognized, err := ClassifyPath("t-01a09000000073018000000000000301-fast-encoder-check/runs/run_01a09000-0000-7304-8000-000000000304-run.md"); err == nil || !recognized {
		t.Fatalf("Try accepted a Run path: recognized=%v err=%v", recognized, err)
	}
}

func codecSnapshot(t *testing.T, source research.ID, now time.Time, state research.SourceSnapshotState) research.SourceSnapshot {
	t.Helper()
	snapshot := research.SourceSnapshot{
		Source: source, Subdir: ".", PolicyVersion: "capture-v1", CapturedAt: now,
		GitObjectFormat: research.GitObjectSHA1,
		BaseCommit:      strings.Repeat("0", 40), HeadCommit: strings.Repeat("b", 40),
		ChangeSet: []string{"internal/model.go"}, State: state, Reproducibility: research.ReproducibilityExact,
	}
	if state == research.SourceSnapshotDirty {
		snapshot.DirtyDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		snapshot.DirtySummary = "tracked_changes=1"
		snapshot.Reproducibility = research.ReproducibilityBounded
	}
	digest, err := research.SourceSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Digest = digest
	return snapshot
}

func mustEncodeDocument(t *testing.T, value research.Record) []byte {
	t.Helper()
	encoded, err := Encode(&Document{Record: value, Body: "# Record\n"})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func assertUnknownField(t *testing.T, data []byte) {
	t.Helper()
	_, err := Decode(data)
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != "record.unknown_field" {
		t.Fatalf("new field under old schema error = %v\n%s", err, data)
	}
}
