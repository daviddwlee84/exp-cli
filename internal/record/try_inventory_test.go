package record

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestInventoryReservesOnlyExactTryNamespace(t *testing.T) {
	root := copyValidProjectForSchemaTest(t)
	legacy := filepath.Join(root, "t-legacy-notes")
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "notes.md"), []byte("legacy notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inventory, err := LoadInventory(root)
	if err != nil || !inventory.Valid() {
		t.Fatalf("legacy t-* tree was newly invalidated: %v, %v", err, inventory.Diagnostics)
	}

	reserved := filepath.Join(root, "t-deadbeef-fast")
	if err := os.Mkdir(reserved, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reserved, "notes.md"), []byte("not canonical\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inventory, err = LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnosticAt(inventory.Diagnostics, "t-deadbeef-fast/notes.md", "record.invalid_path") {
		t.Fatalf("exact Try namespace did not reject unrelated record: %v", inventory.Diagnostics)
	}
}

func TestInventoryAcceptsDirtyTryAttemptAndEnforcesOwnership(t *testing.T) {
	documents := tryInventoryDocuments(t)
	inventory := InventoryFromDocuments(t.TempDir(), documents)
	if !inventory.Valid() {
		t.Fatalf("valid Try inventory: %v", inventory.Diagnostics)
	}
	tryDocument := inventory.OfKind(research.KindTry)[0]
	attemptDocument := inventory.OfKind(research.KindAttempt)[0]
	location, found := inventory.Location(attemptDocument)
	if !found || location.TryDir != path.Dir(tryDocument.Path) || location.ExperimentDir != "" {
		t.Fatalf("Try Attempt location = %#v, %v", location, found)
	}

	closedWithPending := cloneDocuments(documents)
	for _, document := range closedWithPending {
		if value, ok := document.Record.(*research.Try); ok {
			value.State = research.TryConcluded
			value.UpdatedAt = value.UpdatedAt.Add(time.Minute)
			value.Conclusion = &research.TryConclusion{ConcludedAt: value.UpdatedAt, Summary: "Closed too early."}
		}
	}
	invalid := InventoryFromDocuments(t.TempDir(), closedWithPending)
	if !hasDiagnosticCode(invalid.Diagnostics, "try.nonterminal_attempt") {
		t.Fatalf("closed Try with pending Attempt validated: %v", invalid.Diagnostics)
	}

	wrongOwner := cloneDocuments(documents)
	for _, document := range wrongOwner {
		if document.Kind() == research.KindAttempt {
			id, _ := document.ID()
			document.Path = path.Join("e-deadbeef-unrelated", "attempts", id.String()+".md")
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), wrongOwner)
	if !hasDiagnosticCode(invalid.Diagnostics, "relationship.wrong_owner") {
		t.Fatalf("Try Attempt under Experiment path validated: %v", invalid.Diagnostics)
	}

	undeclared := cloneDocuments(documents)
	capturedAt := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	secondSource := testSourceDocument(t, "src_01a09000-0000-7405-8000-000000000405", "secondary", ".", capturedAt)
	undeclared = append(undeclared, secondSource)
	for _, document := range undeclared {
		if value, ok := document.Record.(*research.Attempt); ok {
			value.SourceSnapshots = append(value.SourceSnapshots, inventorySnapshot(t, secondSource.Record.(*research.Source).ID, ".", capturedAt, research.SourceSnapshotDirty))
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), undeclared)
	if !hasDiagnosticCode(invalid.Diagnostics, "try.source_mismatch") {
		t.Fatalf("Try Attempt used an undeclared Source: %v", invalid.Diagnostics)
	}

	optionalReadOnly := cloneDocuments(documents)
	optionalReadOnly = append(optionalReadOnly, secondSource.Clone())
	for _, document := range optionalReadOnly {
		if value, ok := document.Record.(*research.Try); ok {
			value.Sources = append(value.Sources, secondSource.Record.(*research.Source).ID)
		}
	}
	optionalInventory := InventoryFromDocuments(t.TempDir(), optionalReadOnly)
	if !optionalInventory.Valid() {
		t.Fatalf("declared optional read-only Source was made mandatory: %v", optionalInventory.Diagnostics)
	}
}

func TestInventoryCandidateV2RequiresExactCleanFormalAttempt(t *testing.T) {
	documents := formalCandidateDocuments(t)
	inventory := InventoryFromDocuments(t.TempDir(), documents)
	if !inventory.Valid() {
		t.Fatalf("valid Candidate v2 inventory: %v", inventory.Diagnostics)
	}

	ownerMismatch := cloneDocuments(documents)
	var evaluation *research.Evaluation
	var ownerAttemptDocument *Document
	for _, document := range ownerMismatch {
		switch value := document.Record.(type) {
		case *research.Evaluation:
			evaluation = value
		case *research.Attempt:
			ownerAttemptDocument = document
		}
	}
	foreignAttempt := ownerAttemptDocument.Clone()
	foreign := foreignAttempt.Record.(*research.Attempt)
	foreign.ID = mustControlID(t, "att_01a09000-0000-7509-8000-000000000509")
	foreign.Title = "Foreign telemetry owner"
	foreignAttempt.Path = path.Join(path.Dir(ownerAttemptDocument.Path), foreign.ID.String()+".md")
	ownerMismatch = append(ownerMismatch, foreignAttempt)
	observed := evaluation.EvaluatedAt
	evaluation.ExternalRefs = []research.ExternalRef{{
		Role: research.ExternalTracker, Provider: "mlflow", Context: "test", NativeKind: "run", NativeID: "foreign-run", ObservedAt: &observed,
		Metadata: map[string]any{"mlflow.verified": true, "mlflow.owner_attempt": foreign.ID.String(), "mlflow.owner_subject": evaluation.Subject.String()},
	}}
	withoutCandidate := make([]*Document, 0, len(ownerMismatch))
	for _, document := range ownerMismatch {
		if document.Kind() != research.KindCandidate {
			withoutCandidate = append(withoutCandidate, document)
		}
	}
	invalid := InventoryFromDocuments(t.TempDir(), withoutCandidate)
	if !hasDiagnosticCode(invalid.Diagnostics, "evaluation.mlflow_owner_attempt") {
		t.Fatalf("Evaluation v2 accepted telemetry owned by another Attempt: %v", invalid.Diagnostics)
	}
	invalid = InventoryFromDocuments(t.TempDir(), ownerMismatch)
	if !hasDiagnosticCode(invalid.Diagnostics, "candidate.mlflow_owner_attempt") {
		t.Fatalf("Candidate v2 accepted foreign Evaluation telemetry: %v", invalid.Diagnostics)
	}

	ownerSubjectMismatch := cloneDocuments(ownerMismatch)
	for _, document := range ownerSubjectMismatch {
		if value, ok := document.Record.(*research.Evaluation); ok {
			value.ExternalRefs[0].Metadata["mlflow.owner_subject"] = foreign.ID.String()
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), ownerSubjectMismatch)
	if !hasDiagnosticCode(invalid.Diagnostics, "evaluation.mlflow_owner_subject") {
		t.Fatalf("Evaluation accepted mismatched MLflow owner subject: %v", invalid.Diagnostics)
	}

	unbound := cloneDocuments(documents)
	for _, document := range unbound {
		if evaluation, ok := document.Record.(*research.Evaluation); ok {
			evaluation.Schema = research.SchemaEvaluation
			evaluation.Attempt = research.ID{}
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), unbound)
	if !hasDiagnosticCode(invalid.Diagnostics, "candidate.evaluation_attempt") {
		t.Fatalf("Candidate v2 accepted an unbound Evaluation: %v", invalid.Diagnostics)
	}

	mismatch := cloneDocuments(documents)
	for _, document := range mismatch {
		if candidate, ok := document.Record.(*research.Candidate); ok {
			candidate.Sources[0].HeadCommit = strings.Repeat("c", 40)
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), mismatch)
	if !hasDiagnosticCode(invalid.Diagnostics, "candidate.attempt_provenance") {
		t.Fatalf("mismatched Candidate source identity validated: %v", invalid.Diagnostics)
	}

	nonterminal := cloneDocuments(documents)
	for _, document := range nonterminal {
		if attempt, ok := document.Record.(*research.Attempt); ok {
			attempt.State = research.AttemptRunning
			attempt.Terminal = nil
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), nonterminal)
	if !hasDiagnosticCode(invalid.Diagnostics, "candidate.attempt_provenance") {
		t.Fatalf("nonterminal Candidate Attempt validated: %v", invalid.Diagnostics)
	}

	late := cloneDocuments(documents)
	var conclusionAt time.Time
	for _, document := range late {
		if experiment, ok := document.Record.(*research.Experiment); ok {
			conclusionAt = experiment.Conclusion.ConcludedAt
		}
	}
	lateAt := conclusionAt.Add(time.Minute)
	for _, document := range late {
		if attempt, ok := document.Record.(*research.Attempt); ok {
			attempt.CreatedAt, attempt.UpdatedAt = lateAt, lateAt
			attempt.SourceSnapshots[0].CapturedAt = lateAt
			attempt.SourceSnapshots[0].Digest, _ = research.SourceSnapshotDigest(attempt.SourceSnapshots[0])
			attempt.Terminal.ObservedAt, attempt.Terminal.EndedAt = lateAt, lateAt
			attempt.Terminal.StartedAt = &lateAt
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), late)
	if !hasDiagnosticCode(invalid.Diagnostics, "candidate.attempt_provenance") {
		t.Fatalf("post-conclusion Candidate Attempt validated: %v", invalid.Diagnostics)
	}

	ambiguous := cloneDocuments(documents)
	for _, document := range ambiguous {
		if source, ok := document.Record.(*research.Source); ok {
			duplicate := document.Clone()
			duplicateSource := duplicate.Record.(*research.Source)
			duplicateSource.Key = "production-copy"
			duplicateSource.Title = "Production copy"
			duplicate.Path = path.Join(SourcesDir, source.ID.String()+"-production-copy.md")
			ambiguous = append(ambiguous, duplicate)
			break
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), ambiguous)
	if !hasDiagnosticCode(invalid.Diagnostics, "reference.ambiguous") || !hasDiagnosticCode(invalid.Diagnostics, "record.duplicate_id") {
		t.Fatalf("repository-ambiguous Candidate Source validated: %v", invalid.Diagnostics)
	}

	tryBacked := cloneDocuments(documents)
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	var sourceID research.ID
	for _, document := range tryBacked {
		if source, ok := document.Record.(*research.Source); ok {
			sourceID = source.ID
		}
	}
	tryID := mustControlID(t, "try_01a09000-0000-7499-8000-000000000499")
	tryDocument := &Document{Record: &research.Try{
		Common:  research.Common{Schema: research.SchemaTry, ID: tryID, Title: "Exploratory owner", CreatedAt: now, UpdatedAt: now},
		State:   research.TryOpen,
		Goal:    "Explore only.",
		Sources: []research.ID{sourceID},
	}, Body: "# Try\n"}
	tryDocument.Path, _ = PathForNew(tryDocument.Record, nil)
	tryBacked = append(tryBacked, tryDocument)
	for _, document := range tryBacked {
		if attempt, ok := document.Record.(*research.Attempt); ok {
			attempt.Run = research.ID{}
			attempt.Try = tryID
			document.Path = path.Join(path.Dir(tryDocument.Path), "attempts", attempt.ID.String()+".md")
		}
	}
	invalid = InventoryFromDocuments(t.TempDir(), tryBacked)
	if !hasDiagnosticCode(invalid.Diagnostics, "candidate.attempt_provenance") {
		t.Fatalf("Try-backed Candidate Attempt validated: %v", invalid.Diagnostics)
	}
}

func TestInventoryRejectsMultipleRetrySuccessors(t *testing.T) {
	documents := tryInventoryDocuments(t)
	var predecessor *Document
	for _, document := range documents {
		if document.Kind() == research.KindAttempt {
			predecessor = document
			break
		}
	}
	if predecessor == nil {
		t.Fatal("fixture has no Attempt")
	}
	attempt := predecessor.Record.(*research.Attempt)
	exitCode := 1
	started := attempt.CreatedAt
	attempt.State = research.AttemptFailed
	attempt.Terminal = &research.Terminal{Source: "direct", ObservedAt: started, StartedAt: &started, EndedAt: started, ExitCode: &exitCode}
	for index, rawID := range []string{"att_01a09000-0000-7410-8000-000000000410", "att_01a09000-0000-7411-8000-000000000411"} {
		successor := predecessor.Clone()
		value := successor.Record.(*research.Attempt)
		value.ID = mustControlID(t, rawID)
		value.RetryOf = attempt.ID
		value.State = research.AttemptPlanned
		value.Terminal = nil
		value.CreatedAt = attempt.CreatedAt.Add(time.Duration(index+1) * time.Minute)
		value.UpdatedAt = value.CreatedAt
		value.SourceSnapshots[0].CapturedAt = value.CreatedAt
		value.SourceSnapshots[0].Digest, _ = research.SourceSnapshotDigest(value.SourceSnapshots[0])
		successor.Path = path.Join(path.Dir(documents[2].Path), "attempts", value.ID.String()+".md")
		documents = append(documents, successor)
	}
	inventory := InventoryFromDocuments(t.TempDir(), documents)
	if !hasDiagnosticCode(inventory.Diagnostics, "attempt.retry_duplicate") {
		t.Fatalf("multiple retry successors validated: %v", inventory.Diagnostics)
	}
}

func tryInventoryDocuments(t *testing.T) []*Document {
	t.Helper()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	project := testProjectDocument(t, now)
	source := testSourceDocument(t, "src_01a09000-0000-7401-8000-000000000401", "workspace", ".", now)
	tryID := mustControlID(t, "try_01a09000-0000-7402-8000-000000000402")
	tryDocument := &Document{Record: &research.Try{
		Common:  research.Common{Schema: research.SchemaTry, ID: tryID, Title: "Dirty quick check", CreatedAt: now, UpdatedAt: now},
		State:   research.TryOpen,
		Goal:    "Capture a bounded local modification.",
		Sources: []research.ID{source.Record.(*research.Source).ID},
	}, Body: "# Dirty quick check\n"}
	tryDocument.Path, _ = PathForNew(tryDocument.Record, nil)
	snapshot := inventorySnapshot(t, source.Record.(*research.Source).ID, ".", now, research.SourceSnapshotDirty)
	attemptID := mustControlID(t, "att_01a09000-0000-7403-8000-000000000403")
	attempt := &Document{Record: &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV3, ID: attemptID, Title: "Dirty attempt", CreatedAt: now, UpdatedAt: now},
		Try:    tryID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"go", "test", "./..."},
		ExecutionSource: snapshot.Source, SourceSnapshots: []research.SourceSnapshot{snapshot},
	}, Body: "# Dirty attempt\n", Path: path.Join(path.Dir(tryDocument.Path), "attempts", attemptID.String()+".md")}
	return []*Document{project, source, tryDocument, attempt}
}

func formalCandidateDocuments(t *testing.T) []*Document {
	t.Helper()
	created := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	attempted := created.Add(time.Minute)
	concluded := created.Add(2 * time.Minute)
	evaluated := created.Add(3 * time.Minute)
	candidateAt := created.Add(4 * time.Minute)
	project := testProjectDocument(t, created)
	source := testSourceDocument(t, "src_01a09000-0000-7501-8000-000000000501", "production", "services/api", created)
	poolID := mustControlID(t, "pool_01a09000-0000-7502-8000-000000000502")
	pool := &Document{Record: &research.ResourcePool{
		Common:  research.Common{Schema: research.SchemaResourcePool, ID: poolID, Title: "CPU", CreatedAt: created, UpdatedAt: created},
		Enabled: true, Capacity: 1, Unit: "cpu", Bottleneck: "cpu",
	}, Body: "# CPU\n"}
	pool.Path, _ = PathForNew(pool.Record, nil)

	experimentID := mustControlID(t, "exp_01a09000-0000-7503-8000-000000000503")
	runID := mustControlID(t, "run_01a09000-0000-7504-8000-000000000504")
	design := research.Design{
		Question: "Does the candidate improve score?", Hypothesis: "The candidate improves score.", Kind: research.ExperimentSingleFactor,
		PrimaryFactor: "implementation", SecondaryFactors: []string{}, Baseline: "main", ComparabilitySpec: "same evaluator",
		SuccessCriteria: []string{"score improves"}, DecisionRule: "accept on pass", DesignLockedAt: &created,
	}
	digest, err := research.DesignDigest(design)
	if err != nil {
		t.Fatal(err)
	}
	design.DesignDigest = digest
	experiment := &Document{Record: &research.Experiment{
		Common:     research.Common{Schema: research.SchemaExperiment, ID: experimentID, Title: "Formal study", CreatedAt: created, UpdatedAt: concluded},
		Lifecycle:  research.LifecycleClosed,
		Closure:    research.ClosureConcluded,
		Verdict:    research.VerdictSupported,
		Design:     design,
		Conclusion: &research.Conclusion{ConcludedAt: concluded, Summary: "The gate passed.", Evidence: []research.ConclusionEvidence{{Run: runID, Disposition: research.EvidenceIncluded, Reason: ""}}},
	}, Body: "# Formal study\n"}
	experiment.Path, _ = PathForNew(experiment.Record, nil)
	run := &Document{Record: &research.Run{
		Common:     research.Common{Schema: research.SchemaRun, ID: runID, Title: "Formal run", CreatedAt: created, UpdatedAt: created},
		Experiment: experimentID, Role: research.RunCandidate, Objective: "Measure the implementation.",
	}, Body: "# Formal run\n", Path: path.Join(path.Dir(experiment.Path), "runs", runID.String()+"-formal-run.md")}

	snapshot := inventorySnapshot(t, source.Record.(*research.Source).ID, "services/api", attempted, research.SourceSnapshotClean)
	attemptID := mustControlID(t, "att_01a09000-0000-7505-8000-000000000505")
	exitCode := 0
	attempt := &Document{Record: &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV3, ID: attemptID, Title: "Successful formal attempt", CreatedAt: attempted, UpdatedAt: attempted},
		Run:    runID, State: research.AttemptSucceeded, Runner: "direct", Scheduler: "direct", CWD: ".", Argv: []string{"go", "test", "./..."},
		ExecutionSource: snapshot.Source, SourceSnapshots: []research.SourceSnapshot{snapshot},
		Terminal: &research.Terminal{Source: "direct", ObservedAt: attempted, StartedAt: &attempted, EndedAt: attempted, ExitCode: &exitCode},
	}, Body: "# Successful formal attempt\n", Path: path.Join(path.Dir(experiment.Path), "attempts", attemptID.String()+".md")}

	specID := mustControlID(t, "evalspec_01a09000-0000-7506-8000-000000000506")
	spec := &Document{Record: &research.EvaluationSpec{
		Common:  research.Common{Schema: research.SchemaEvaluationSpec, ID: specID, Title: "Scientific gate", CreatedAt: created, UpdatedAt: created},
		Purpose: research.EvaluationScientific, Dataset: "validation", Protocol: "Run once.",
		Metrics: []research.MetricSpec{{Name: "score", Unit: "points", Direction: research.MetricMaximize}}, BudgetPool: poolID, BudgetHours: 1,
	}, Body: "# Scientific gate\n"}
	spec.Path, _ = PathForNew(spec.Record, nil)
	evaluationID := mustControlID(t, "eval_01a09000-0000-7507-8000-000000000507")
	evaluation := &Document{Record: &research.Evaluation{
		Common: research.Common{Schema: research.SchemaEvaluationV2, ID: evaluationID, Title: "Passing result", CreatedAt: evaluated, UpdatedAt: evaluated},
		Spec:   specID, Subject: experimentID, Attempt: attemptID, Outcome: research.EvaluationPassed, EvaluatedAt: evaluated,
		Metrics: []research.MetricValue{{Name: "score", Value: .9, Unit: "points"}}, Summary: "Passed.",
	}, Body: "# Passing result\n"}
	evaluation.Path, _ = PathForNew(evaluation.Record, nil)
	candidate := &Document{Record: &research.Candidate{
		Common:     research.Common{Schema: research.SchemaCandidateV2, ID: mustControlID(t, "cand_01a09000-0000-7508-8000-000000000508"), Title: "Formal candidate", CreatedAt: candidateAt, UpdatedAt: candidateAt},
		Experiment: experimentID, Evaluation: evaluationID, Attempt: attemptID,
		Sources: []research.CandidateSource{{Source: snapshot.Source, HeadCommit: snapshot.HeadCommit, ChangeSet: append([]string{}, snapshot.ChangeSet...)}},
	}, Body: "# Formal candidate\n"}
	candidate.Path, _ = PathForNew(candidate.Record, nil)
	return []*Document{project, source, pool, experiment, run, attempt, spec, evaluation, candidate}
}

func testProjectDocument(t *testing.T, now time.Time) *Document {
	t.Helper()
	projectID, err := research.ParseUUID("01a09000-0000-7000-8000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	return &Document{Path: ProjectFile, Body: "# Project\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: projectID, Name: "Try inventory", CreatedAt: now, ExperimentsRoot: ".",
	}}
}

func testSourceDocument(t *testing.T, id, key, subdir string, now time.Time) *Document {
	t.Helper()
	document := &Document{Record: &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: mustControlID(t, id), Title: key + " source", CreatedAt: now, UpdatedAt: now},
		Key:    key, Kind: research.SourceGit, Subdir: subdir, LocatorHints: []string{"https://github.com/example/" + key + ".git"}, State: research.SourceActive,
	}, Body: "# Source\n"}
	document.Path, _ = PathForNew(document.Record, nil)
	return document
}

func inventorySnapshot(t *testing.T, source research.ID, subdir string, now time.Time, state research.SourceSnapshotState) research.SourceSnapshot {
	t.Helper()
	snapshot := research.SourceSnapshot{
		Source: source, Subdir: subdir, PolicyVersion: "capture-v1", CapturedAt: now,
		GitObjectFormat: research.GitObjectSHA1,
		BaseCommit:      strings.Repeat("0", 40), HeadCommit: strings.Repeat("b", 40),
		ChangeSet: []string{"services/api/model.go"}, State: state, Reproducibility: research.ReproducibilityExact,
	}
	if state == research.SourceSnapshotDirty {
		snapshot.DirtyDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
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
