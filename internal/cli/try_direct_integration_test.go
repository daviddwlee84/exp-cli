package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/trust"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/worker"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

var directTryTestSlots = make(chan struct{}, 4)

func parallelDirectTry(t *testing.T) {
	t.Helper()
	if raceDetectorEnabled {
		switch t.Name() {
		case "TestDirectTryCleanAndDirtyTwoRepositoryLifecycle",
			"TestDirectTryDirtySeedIsNotImplicitlyWritable",
			"TestDirectTryUnknownCannotRetryUntilExplicitReconciliation",
			"TestDirectTryJSONRequiresExplicitFieldsAndRedactsArguments":
			return
		default:
			t.Skip("redundant direct CLI state permutation; focused seams run under -race")
		}
	}
	t.Parallel()
	directTryTestSlots <- struct{}{}
	t.Cleanup(func() { <-directTryTestSlots })
}

func TestDirectTryCleanAndDirtyTwoRepositoryLifecycle(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	fixture.app.RenderProjections = projection.Render

	touch, err := exec.LookPath("touch")
	if err != nil {
		t.Skip(err)
	}
	touch = filepath.Base(touch)
	clean := invokeCommand(t, fixture.app, "",
		"--start-dir", fixture.canonical, "--workspace", fixture.projectID, "--source", "production",
		"try", "run", "--title", "Clean direct check", "--goal", "Verify managed isolation",
		"--allow", "pkg/output.txt", "--json", "--", touch, "pkg/output.txt")
	if clean.err != nil {
		t.Fatalf("clean Try = %v\nstdout=%s\nstderr=%s", clean.err, clean.stdout, clean.stderr)
	}
	cleanEnvelope := decodeEnvelope(t, clean.stdout)
	if !cleanEnvelope.OK || cleanEnvelope.Partial || strings.Contains(clean.stdout, fixture.base) {
		t.Fatalf("clean envelope = %#v\n%s", cleanEnvelope, clean.stdout)
	}
	var cleanData tryExecutionData
	decodeData(t, cleanEnvelope, &cleanData)
	if cleanData.Attempt == nil || cleanData.Attempt.State != string(research.AttemptSucceeded) || cleanData.Try == nil {
		t.Fatalf("clean result = %#v", cleanData)
	}
	if _, err := os.Stat(filepath.Join(fixture.sourcePath, "output.txt")); !os.IsNotExist(err) {
		t.Fatalf("production checkout was changed: %v", err)
	}

	status := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "status", cleanData.Try.ID, "--json")
	requireCommandSuccess(t, status)
	var statusData tryStatusData
	decodeData(t, decodeEnvelope(t, status.stdout), &statusData)
	if len(statusData.Tries) != 1 || len(statusData.Tries[0].Attempts) != 1 || !statusData.Tries[0].Attempts[0].Runtime.WorkspacePresent {
		t.Fatalf("Try status = %#v", statusData)
	}

	finish := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "finish", cleanData.Try.ID, "--summary", "Isolation succeeded.", "--no-results", "--confirm", "--json")
	requireCommandSuccess(t, finish)
	finishAgain := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "finish", cleanData.Try.ID, "--summary", "Isolation succeeded.", "--no-results", "--confirm", "--json")
	requireCommandSuccess(t, finishAgain)
	adoptArgs := []string{
		"--start-dir", fixture.sourcePath, "try", "adopt", cleanData.Try.ID,
		"--title", "Formal isolation study", "--summary", "Validate isolation under a formal protocol.",
		"--proposed-by", "human:test", "--cluster", "isolation", "--domain", "systems",
		"--work", "validation", "--method", "controlled", "--component", "runtime",
		"--lane", "explore", "--risk", "low", "--horizon", "short", "--origin", "human",
		"--confirm", "--json",
	}
	adopted := invokeCommand(t, fixture.app, "", adoptArgs...)
	requireCommandSuccess(t, adopted)
	adoptedAgain := invokeCommand(t, fixture.app, "", adoptArgs...)
	requireCommandSuccess(t, adoptedAgain)
	var adoptedData, adoptedAgainData tryTransitionData
	decodeData(t, decodeEnvelope(t, adopted.stdout), &adoptedData)
	decodeData(t, decodeEnvelope(t, adoptedAgain.stdout), &adoptedAgainData)
	if adoptedData.Idea == nil || adoptedAgainData.Idea == nil || adoptedData.Idea.ID != adoptedAgainData.Idea.ID || adoptedAgainData.TransactionID != "" {
		t.Fatalf("idempotent adoption = first %#v second %#v", adoptedData, adoptedAgainData)
	}
	refusedCleanup := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "cleanup", cleanData.Try.ID, "--confirm", "--json")
	if refusedCleanup.err == nil {
		t.Fatalf("changed clean workspace cleanup unexpectedly succeeded: %s", refusedCleanup.stdout)
	}
	var refusedData tryCleanupData
	refusedEnvelope := decodeEnvelope(t, refusedCleanup.stdout)
	decodeData(t, refusedEnvelope, &refusedData)
	if !refusedEnvelope.Partial || len(refusedData.Items) != 1 || !refusedData.Items[0].Refused {
		t.Fatalf("cleanup refusal = envelope %#v data %#v", refusedEnvelope, refusedData)
	}

	if err := os.WriteFile(filepath.Join(fixture.sourcePath, "dirty.txt"), []byte("private seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testBinary, err := exec.LookPath("test")
	if err != nil {
		t.Skip(err)
	}
	testBinary = filepath.Base(testBinary)
	dirty := invokeCommand(t, fixture.app, "",
		"--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Dirty direct check", "--goal", "Verify private seed roundtrip",
		"--dirty=capture", "--json", "--", testBinary, "-f", "dirty.txt")
	if dirty.err != nil {
		t.Fatalf("dirty Try = %v\nstdout=%s\nstderr=%s", dirty.err, dirty.stdout, dirty.stderr)
	}
	var dirtyData tryExecutionData
	decodeData(t, decodeEnvelope(t, dirty.stdout), &dirtyData)
	if dirtyData.Attempt == nil || len(dirtyData.Attempt.SourceSnapshots) != 1 || dirtyData.Attempt.SourceSnapshots[0].State != string(research.SourceSnapshotDirty) {
		encoded, _ := json.Marshal(dirtyData)
		t.Fatalf("dirty result = %s", encoded)
	}
	if content, err := os.ReadFile(filepath.Join(fixture.sourcePath, "dirty.txt")); err != nil || string(content) != "private seed\n" {
		t.Fatalf("production dirty seed changed: %q, %v", content, err)
	}
	abandon := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "abandon", dirtyData.Try.ID, "--reason", "Bounded check complete.", "--confirm", "--json")
	requireCommandSuccess(t, abandon)
	fixture.app.Now = func() time.Time { return time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC) }
	retired := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical,
		"source", "retire", "production", "--expected-revision", fixture.revision, "--confirm", "--json")
	requireCommandSuccess(t, retired)
	cleanup := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "--workspace", fixture.projectID,
		"try", "cleanup", dirtyData.Try.ID, "--confirm", "--json")
	if cleanup.err != nil {
		t.Fatalf("dirty cleanup = %v\nstdout=%s\nstderr=%s", cleanup.err, cleanup.stdout, cleanup.stderr)
	}
	cleanupAgain := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "--workspace", fixture.projectID,
		"try", "cleanup", dirtyData.Try.ID, "--confirm", "--json")
	requireCommandSuccess(t, cleanupAgain)
}

func TestTryReadCommandsDoNotInitializeOperationalState(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	info, err := fixture.app.DiscoverProject(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	store := record.NewStore(info.Root, info.Repository.GitCommonDir)
	sourceID, err := research.ParseIDForKind(fixture.sourceID, research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	service := tryflow.New(store, tryflow.WithClock(fixture.app.clock), tryflow.WithUUIDGenerator(fixture.app.GenerateUUID))
	created, err := service.Create(t.Context(), tryflow.CreateRequest{
		Title: "Read-only status", Goal: "Observe without operational writes", Sources: []tryflow.RevisionRef{{ID: sourceID, Revision: fixture.revision}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(t.Context(), tryflow.CreateRequest{
		Title: "Read-only status page two", Goal: "Exercise bounded history", Sources: []tryflow.RevisionRef{{ID: sourceID, Revision: fixture.revision}},
	}); err != nil {
		t.Fatal(err)
	}
	database, err := operation.PathFor(info.Repository.GitCommonDir)
	if err != nil {
		t.Fatal(err)
	}
	tryID := created.Try.Record.(*research.Try).ID.String()
	for _, arguments := range [][]string{{"try", "list", "--json"}, {"try", "show", tryID, "--json"}, {"try", "status", tryID, "--json"}} {
		args := append([]string{"--start-dir", fixture.sourcePath}, arguments...)
		invocation := invokeCommand(t, fixture.app, "", args...)
		if invocation.err != nil {
			t.Fatalf("%v: %v\n%s", arguments, invocation.err, invocation.stdout)
		}
		if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%v initialized operational state: %v", arguments, err)
		}
	}
	paged := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "list", "--limit", "1", "--offset", "1", "--json")
	if paged.err != nil {
		t.Fatal(paged.err)
	}
	var page tryStatusData
	decodeData(t, decodeEnvelope(t, paged.stdout), &page)
	if page.Total != 2 || page.Limit != 1 || page.Offset != 1 || len(page.Tries) != 1 {
		t.Fatalf("Try status page = %#v", page)
	}
}

func TestDirectTryDirtySeedIsNotImplicitlyWritable(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	model := filepath.Join(fixture.sourcePath, "model.txt")
	if err := os.WriteFile(model, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalGit(t, fixture.source, "add", "--", ".")
	externalGit(t, fixture.source, "commit", "-qm", "add tracked model")
	if err := os.WriteFile(model, []byte("private seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	sh = filepath.Base(sh)
	mutate := []string{sh, "-c", "printf 'mutated\\n' > model.txt"}
	for _, scenario := range []struct {
		name   string
		script string
	}{
		{name: "overwrite", script: "printf 'mutated\\n' > model.txt"},
		{name: "delete", script: "rm -- model.txt"},
		{name: "reset", script: "git checkout -- model.txt"},
	} {
		blockedArgs := []string{
			"--start-dir", fixture.sourcePath, "try", "run", "--title", "Protected dirty seed " + scenario.name,
			"--goal", "Reject implicit writes", "--dirty=capture", "--json", "--", sh, "-c", scenario.script,
		}
		blocked := invokeCommand(t, fixture.app, "", blockedArgs...)
		if !errors.Is(blocked.err, tryflow.ErrCommandFailed) {
			t.Fatalf("dirty seed %s error = %v\n%s", scenario.name, blocked.err, blocked.stdout)
		}
		var blockedData tryExecutionData
		decodeData(t, decodeEnvelope(t, blocked.stdout), &blockedData)
		if blockedData.Attempt == nil || blockedData.Attempt.State != string(research.AttemptFailed) {
			t.Fatalf("dirty seed %s disposition = %#v", scenario.name, blockedData)
		}
		if content, err := os.ReadFile(model); err != nil || string(content) != "private seed\n" {
			t.Fatalf("production dirty seed changed after %s: %q, %v", scenario.name, content, err)
		}
	}

	allowedArgs := append([]string{
		"--start-dir", fixture.sourcePath, "try", "run", "--title", "Explicit dirty write",
		"--goal", "Permit one reviewed path", "--dirty=capture", "--allow", "pkg/model.txt", "--json", "--",
	}, mutate...)
	allowed := invokeCommand(t, fixture.app, "", allowedArgs...)
	if allowed.err != nil {
		t.Fatalf("explicit dirty seed write = %v\n%s", allowed.err, allowed.stdout)
	}
	var allowedData tryExecutionData
	decodeData(t, decodeEnvelope(t, allowed.stdout), &allowedData)
	if allowedData.Attempt == nil || allowedData.Attempt.State != string(research.AttemptSucceeded) {
		t.Fatalf("explicit dirty seed disposition = %#v", allowedData)
	}
}

func TestDirectTryMLflowProfileAttachesExactOwnedRunWithoutSecretLeak(t *testing.T) {
	fixture := newDirectTryCLIFixture(t)
	const secret = "DIRECT_PROFILE_SECRET_CANARY_41ad"
	t.Setenv("PARENT_MLFLOW_TOKEN", secret)
	writeDirectMLflowProfile(t, fixture, true)
	workload := filepath.Join(fixture.sourcePath, "mlflow-workload")
	script := "#!/bin/sh\nprintf '%s\\n' \"$WORKLOAD_SECRET\"\nprintf '{\"token\":\"%s\",\"mlflow_run_id\":\"direct-run-1\"}' \"$WORKLOAD_SECRET\" > \"$EXP_RESULT_PATH\"\n"
	if err := os.WriteFile(workload, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	externalGit(t, fixture.source, "add", "--", ".")
	externalGit(t, fixture.source, "commit", "-qm", "add MLflow workload")

	originalLookup := fixture.app.BinaryLookup
	mlflowBinary := filepath.Join(fixture.base, "tools", "mlflow-test")
	fixture.app.BinaryLookup = func(name string) (string, error) {
		if name == "mlflow-test" {
			return mlflowBinary, nil
		}
		return originalLookup(name)
	}
	delegate := execx.NewInvoker()
	fixture.app.Invoker = execx.InvokerFunc(func(ctx context.Context, spec execx.CommandSpec) (execx.Result, error) {
		if spec.Executable != mlflowBinary {
			return delegate.Invoke(ctx, spec)
		}
		info, err := project.Discover(ctx, fixture.canonical)
		if err != nil {
			t.Fatal(err)
		}
		inventory, err := record.NewStore(info.Root, info.Repository.GitCommonDir).Inventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		attempts := inventory.OfKind(research.KindAttempt)
		if len(attempts) != 1 {
			t.Fatalf("MLflow observer saw %d Attempts", len(attempts))
		}
		attemptID, _ := attempts[0].ID()
		output := fmt.Sprintf(`{"info":{"run_id":"direct-run-1","experiment_id":"9","status":"FINISHED","artifact_uri":"https://tracking.example.invalid/runs/direct-run-1?token=drop"},"data":{"metrics":{"score":0.8},"tags":{"exp.attempt_id":"%s","token":"not-selected"}}}`, attemptID)
		return execx.Result{Stdout: output, ExitCode: 0}, nil
	})

	invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"--mlflow-profile", "training", "try", "run", "--title", "Direct MLflow evidence",
		"--goal", "Attach exact workload-owned telemetry", "--json", "--", "./mlflow-workload")
	if invocation.err != nil {
		t.Fatalf("direct MLflow Try = %v\nstdout=%s\nstderr=%s", invocation.err, invocation.stdout, invocation.stderr)
	}
	if strings.Contains(invocation.stdout, secret) || strings.Contains(invocation.stderr, secret) {
		t.Fatalf("direct Try output leaked profile secret: stdout=%s stderr=%s", invocation.stdout, invocation.stderr)
	}
	var execution tryExecutionData
	decodeData(t, decodeEnvelope(t, invocation.stdout), &execution)
	if execution.Attempt == nil || execution.Attempt.State != string(research.AttemptSucceeded) || !strings.Contains(execution.Runtime.Stdout, execx.Redacted) {
		t.Fatalf("direct MLflow execution = %#v", execution)
	}
	info, err := project.Discover(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := record.NewStore(info.Root, info.Repository.GitCommonDir).Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	encodedRecords, err := json.Marshal(inventory.Documents)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedRecords), secret) {
		t.Fatalf("canonical records retained a resolved profile secret: %s", encodedRecords)
	}
	attemptDocument, err := inventory.Resolve(execution.Attempt.ID, research.KindAttempt)
	if err != nil {
		t.Fatal(err)
	}
	attempt := attemptDocument.Record.(*research.Attempt)
	var tracker *research.ExternalRef
	for index := range attempt.ExternalRefs {
		if attempt.ExternalRefs[index].Provider == "mlflow" {
			tracker = &attempt.ExternalRefs[index]
		}
	}
	if tracker == nil || tracker.NativeID != "direct-run-1" || tracker.Context != "direct" || tracker.Metadata["mlflow.owner_attempt"] != attempt.ID.String() || strings.Contains(tracker.URI, "token=") {
		t.Fatalf("direct Attempt MLflow reference = %#v", tracker)
	}
	operations, err := fixture.app.OpenOperational(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}
	defer operations.Close()
	jobs, err := operations.ListJobs(t.Context())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("direct jobs = %#v, err=%v", jobs, err)
	}
	encodedJob, err := json.Marshal(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedJob), secret) || jobs[0].MLflowRunID != "direct-run-1" {
		t.Fatalf("operational job leaked a secret or omitted run identity: %s", encodedJob)
	}
}

func TestDirectTryMissingMLflowObserverDoesNotBlockNativeExecution(t *testing.T) {
	fixture := newDirectTryCLIFixture(t)
	t.Setenv("PARENT_MLFLOW_TOKEN", "MISSING_PROVIDER_SECRET_CANARY")
	writeDirectMLflowProfile(t, fixture, true)
	workload := filepath.Join(fixture.sourcePath, "mlflow-workload")
	script := "#!/bin/sh\nprintf '{\"mlflow_run_id\":\"missing-run-1\"}' > \"$EXP_RESULT_PATH\"\n"
	if err := os.WriteFile(workload, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	externalGit(t, fixture.source, "add", "--", ".")
	externalGit(t, fixture.source, "commit", "-qm", "add optional MLflow workload")
	originalLookup := fixture.app.BinaryLookup
	fixture.app.BinaryLookup = func(name string) (string, error) {
		if name == "mlflow-test" || name == "pueue" {
			return "", errors.New("optional provider missing")
		}
		return originalLookup(name)
	}
	invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Optional MLflow evidence", "--goal", "Keep native execution independent",
		"--json", "--", "./mlflow-workload")
	if invocation.err != nil {
		t.Fatalf("missing MLflow blocked native Try: %v\n%s", invocation.err, invocation.stdout)
	}
	var execution tryExecutionData
	decodeData(t, decodeEnvelope(t, invocation.stdout), &execution)
	if execution.Attempt == nil || execution.Attempt.State != string(research.AttemptSucceeded) {
		t.Fatalf("missing MLflow direct execution = %#v", execution)
	}
}

func writeDirectMLflowProfile(t *testing.T, fixture *externalCLIFixture, required bool) {
	t.Helper()
	path := filepath.Join(fixture.home, "config", "exp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`schema = "exp.config/v1"
[defaults]
mlflow_profile = "training"
[mlflow.profiles.training]
context = "direct"
binary = "mlflow-test"
timeout = "2s"
default_metrics = ["score"]
[mlflow.profiles.training.env.WORKLOAD_SECRET]
from = "PARENT_MLFLOW_TOKEN"
secret = true
required = %t
`, required)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTryAdoptJSONPreservesPreparedTransactionRecovery(t *testing.T) {
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	run := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Adoption transaction", "--goal", "Preserve recovery identity", "--json", "--", trueBinary)
	requireCommandSuccess(t, run)
	var execution tryExecutionData
	decodeData(t, decodeEnvelope(t, run.stdout), &execution)
	finish := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "finish", execution.Try.ID, "--summary", "Ready for adoption.", "--no-results", "--confirm", "--json")
	requireCommandSuccess(t, finish)
	injected := errors.New("injected adoption Try CAS failure")
	fixture.app.NewTransactionalStore = func(info *project.Info) (TransactionalRecordStore, error) {
		return record.NewStore(info.Root, info.Repository.GitCommonDir, record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
			if stage == record.StageTransactionCanonicalCAS {
				return injected
			}
			return nil
		})), nil
	}
	adopted := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "adopt", execution.Try.ID,
		"--title", "Formal recovery study", "--summary", "Continue with controlled evidence.",
		"--proposed-by", "human:test", "--cluster", "recovery", "--domain", "systems",
		"--work", "validation", "--method", "controlled", "--component", "runtime",
		"--lane", "explore", "--risk", "low", "--horizon", "short", "--origin", "human",
		"--confirm", "--json")
	if !errors.Is(adopted.err, injected) {
		t.Fatalf("adoption transaction failure = %v\n%s", adopted.err, adopted.stdout)
	}
	envelope := decodeEnvelope(t, adopted.stdout)
	var data tryTransitionData
	decodeData(t, envelope, &data)
	if !envelope.Partial || data.Transaction == nil || data.TransactionID == "" || data.Transaction.TransactionID != data.TransactionID || !data.Transaction.RecoveryRequired || len(data.Transaction.Paths) != 2 {
		t.Fatalf("adoption recovery envelope = %#v data=%#v", envelope, data)
	}
}

func TestDirectTryCleanupResumesAcrossCanonicalMarkGap(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	completed := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Cleanup mark gap", "--goal", "Resume canonical cleanup marking", "--json", "--", trueBinary)
	requireCommandSuccess(t, completed)
	var execution tryExecutionData
	decodeData(t, decodeEnvelope(t, completed.stdout), &execution)
	injected := errors.New("injected cleanup canonical CAS failure")
	fail := true
	fixture.app.NewTransactionalStore = func(info *project.Info) (TransactionalRecordStore, error) {
		return record.NewStore(info.Root, info.Repository.GitCommonDir, record.WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if fail && stage == record.StageRename {
				return injected
			}
			return nil
		})), nil
	}
	first := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "cleanup", execution.Try.ID, "--confirm", "--json")
	if !errors.Is(first.err, injected) {
		t.Fatalf("cleanup mark gap = %v\n%s", first.err, first.stdout)
	}
	var firstData tryCleanupData
	firstEnvelope := decodeEnvelope(t, first.stdout)
	decodeData(t, firstEnvelope, &firstData)
	if !firstEnvelope.Partial || len(firstData.Items) != 1 || !firstData.Items[0].RemovedWorkspace {
		t.Fatalf("cleanup mark partial = %#v %#v", firstEnvelope, firstData)
	}
	fail = false
	second := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "cleanup", execution.Try.ID, "--confirm", "--json")
	requireCommandSuccess(t, second)
	var secondData tryCleanupData
	decodeData(t, decodeEnvelope(t, second.stdout), &secondData)
	if len(secondData.Items) != 1 || !secondData.Items[0].Attempt.CleanupCompleted || secondData.Items[0].Refused {
		t.Fatalf("resumed cleanup mark = %#v", secondData)
	}
}

func TestDirectTryFailureTimeoutRetryAndSafeCleanup(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	falseBinary, err := exec.LookPath("false")
	if err != nil {
		t.Skip(err)
	}
	falseBinary = filepath.Base(falseBinary)
	failed := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Expected failure", "--goal", "Record a nonzero exit", "--json", "--", falseBinary)
	if failed.err == nil {
		t.Fatalf("false command unexpectedly succeeded: %s", failed.stdout)
	}
	failedEnvelope := decodeEnvelope(t, failed.stdout)
	var failedData tryExecutionData
	decodeData(t, failedEnvelope, &failedData)
	if failedEnvelope.Partial || failedData.Attempt == nil || failedData.Attempt.State != string(research.AttemptFailed) || failedData.Attempt.Terminal == nil || failedData.Attempt.Terminal.ExitCode == nil || *failedData.Attempt.Terminal.ExitCode == 0 {
		t.Fatalf("failed direct result = envelope %#v data %#v", failedEnvelope, failedData)
	}

	unrelated := filepath.Join(fixture.base, "unrelated-invocation")
	if err := os.Mkdir(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	retried := invokeCommand(t, fixture.app, "", "--start-dir", unrelated, "--workspace", fixture.projectID, "--source", "production", "try", "retry", failedData.Try.ID, "--json")
	if retried.err == nil {
		t.Fatalf("retried false command unexpectedly succeeded: %s", retried.stdout)
	}
	var retriedData tryExecutionData
	decodeData(t, decodeEnvelope(t, retried.stdout), &retriedData)
	if !retriedData.RetryCreated || retriedData.Attempt == nil || retriedData.Attempt.ID == failedData.Attempt.ID || retriedData.Attempt.State != string(research.AttemptFailed) || retriedData.Attempt.ExecutionSource != failedData.Attempt.ExecutionSource ||
		len(retriedData.Attempt.SourceSnapshots) != 1 || retriedData.Attempt.SourceSnapshots[0].HeadCommit != failedData.Attempt.SourceSnapshots[0].HeadCommit {
		t.Fatalf("retry identity = first %#v retry %#v err=%v output=%s", failedData, retriedData, retried.err, retried.stdout)
	}
	abandoned := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "abandon", failedData.Try.ID, "--reason", "Expected failure captured.", "--confirm", "--json")
	requireCommandSuccess(t, abandoned)
	cleaned := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "cleanup", failedData.Try.ID, "--confirm", "--json")
	requireCommandSuccess(t, cleaned)
	var cleanedData tryCleanupData
	decodeData(t, decodeEnvelope(t, cleaned.stdout), &cleanedData)
	if len(cleanedData.Items) != 2 || !cleanedData.Items[0].RemovedWorkspace || !cleanedData.Items[1].RemovedWorkspace {
		t.Fatalf("failed retry cleanup = %#v", cleanedData)
	}

	sleepBinary, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	sleepBinary = filepath.Base(sleepBinary)
	timedOut := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Timeout check", "--goal", "Record a bounded timeout", "--timeout", "20ms", "--json", "--", sleepBinary, "1")
	if !errors.Is(timedOut.err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v\nstdout=%s", timedOut.err, timedOut.stdout)
	}
	var timeoutData tryExecutionData
	decodeData(t, decodeEnvelope(t, timedOut.stdout), &timeoutData)
	if timeoutData.Attempt == nil || timeoutData.Attempt.State != string(research.AttemptTimedOut) || timeoutData.Attempt.Terminal == nil {
		t.Fatalf("timeout result = %#v", timeoutData)
	}
}

func TestDirectTryPersistsAndExposesBoundedCommandStreams(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	echoBinary, err := exec.LookPath("echo")
	if err != nil {
		t.Skip(err)
	}
	echoBinary = filepath.Base(echoBinary)
	machine := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Stream evidence", "--goal", "Expose bounded evidence", "--json", "--", echoBinary, "observed-evidence")
	requireCommandSuccess(t, machine)
	var data tryExecutionData
	decodeData(t, decodeEnvelope(t, machine.stdout), &data)
	if data.Runtime.Stdout != "observed-evidence" || data.Runtime.StdoutTruncated || data.Runtime.Stderr != "" {
		t.Fatalf("captured stream data = %#v", data.Runtime)
	}
	human := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Human stream evidence", "--goal", "Show bounded evidence", "--", echoBinary, "human-evidence")
	if human.err != nil || !strings.Contains(human.stdout, "human-evidence") {
		t.Fatalf("human stream output = %v stdout=%q stderr=%q", human.err, human.stdout, human.stderr)
	}
}

func TestDirectTryProjectionFailureAfterClaimDoesNotStrandInvocation(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	originalRender := fixture.app.RenderProjections
	var renders atomic.Int32
	injected := errors.New("injected post-claim projection failure")
	fixture.app.RenderProjections = func(ctx context.Context, inventory *record.Inventory) (projection.Result, error) {
		if renders.Add(1) == 3 {
			return projection.Result{}, injected
		}
		return originalRender(ctx, inventory)
	}
	var invocations atomic.Int32
	delegate := execx.NewInvoker()
	fixture.app.Invoker = execx.InvokerFunc(func(ctx context.Context, spec execx.CommandSpec) (execx.Result, error) {
		invocations.Add(1)
		return delegate.Invoke(ctx, spec)
	})
	result := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Projection claim", "--goal", "Continue after projection failure", "--json", "--", trueBinary)
	if result.err != nil {
		t.Fatalf("post-claim projection failure stranded invocation: %v\n%s", result.err, result.stdout)
	}
	var data tryExecutionData
	decodeData(t, decodeEnvelope(t, result.stdout), &data)
	if invocations.Load() != 1 || data.Attempt == nil || data.Attempt.State != string(research.AttemptSucceeded) {
		t.Fatalf("projection recovery result = %#v invocations=%d renders=%d", data, invocations.Load(), renders.Load())
	}
}

func TestDirectTryResumeRepairsTerminalProjectionFailure(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	originalRender := fixture.app.RenderProjections
	var failTerminal atomic.Bool
	failTerminal.Store(true)
	injected := errors.New("injected terminal projection failure")
	fixture.app.RenderProjections = func(ctx context.Context, inventory *record.Inventory) (projection.Result, error) {
		for _, document := range inventory.OfKind(research.KindAttempt) {
			state := document.Record.(*research.Attempt).State
			if (state == research.AttemptSucceeded || state == research.AttemptFailed || state == research.AttemptCancelled || state == research.AttemptTimedOut) && failTerminal.CompareAndSwap(true, false) {
				return projection.Result{}, injected
			}
		}
		return originalRender(ctx, inventory)
	}
	partial := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Terminal projection", "--goal", "Repair derived views", "--json", "--", trueBinary)
	if !errors.Is(partial.err, injected) {
		t.Fatalf("terminal projection fixture = %v\n%s", partial.err, partial.stdout)
	}
	partialEnvelope := decodeEnvelope(t, partial.stdout)
	var partialData tryExecutionData
	decodeData(t, partialEnvelope, &partialData)
	if !partialEnvelope.Partial || !partialData.Recoverable || partialData.Stage != string(tryflow.StageProjection) || partialData.Attempt == nil || partialData.Attempt.State != string(research.AttemptSucceeded) {
		t.Fatalf("terminal projection partial = %#v %#v", partialEnvelope, partialData)
	}
	resumed := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "resume", partialData.Try.ID, "--json")
	requireCommandSuccess(t, resumed)
	var resumedData tryExecutionData
	decodeData(t, decodeEnvelope(t, resumed.stdout), &resumedData)
	if resumedData.Stage != string(tryflow.StageComplete) || resumedData.Attempt == nil || resumedData.Attempt.State != string(research.AttemptSucceeded) {
		t.Fatalf("terminal projection resume = %#v", resumedData)
	}
}

func TestDirectTryRecoversCanonicalOperationalGapWithoutDuplicateExecution(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	originalOpen := fixture.app.OpenOperational
	injected := errors.New("injected operation open failure")
	failOpen := true
	fixture.app.OpenOperational = func(ctx context.Context, info *project.Info) (OperationalStore, error) {
		if failOpen {
			return nil, injected
		}
		return originalOpen(ctx, info)
	}
	var invocations atomic.Int32
	delegate := execx.NewInvoker()
	fixture.app.Invoker = execx.InvokerFunc(func(ctx context.Context, spec execx.CommandSpec) (execx.Result, error) {
		invocations.Add(1)
		return delegate.Invoke(ctx, spec)
	})
	planned := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Operation gap", "--goal", "Resume the planned Attempt", "--json", "--", trueBinary)
	if !errors.Is(planned.err, injected) {
		t.Fatalf("operation gap = %v\nstdout=%s", planned.err, planned.stdout)
	}
	var plannedData tryExecutionData
	plannedEnvelope := decodeEnvelope(t, planned.stdout)
	decodeData(t, plannedEnvelope, &plannedData)
	if !plannedEnvelope.Partial || !plannedData.Recoverable || plannedData.Attempt == nil || plannedData.Attempt.State != string(research.AttemptPlanned) || invocations.Load() != 0 {
		t.Fatalf("planned gap = envelope %#v data %#v invocations=%d", plannedEnvelope, plannedData, invocations.Load())
	}
	failOpen = false
	resumed := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "--workspace", fixture.projectID, "--source", "production", "try", "resume", plannedData.Try.ID, "--json")
	requireCommandSuccess(t, resumed)
	var resumedData tryExecutionData
	decodeData(t, decodeEnvelope(t, resumed.stdout), &resumedData)
	if resumedData.Attempt == nil || resumedData.Attempt.ID != plannedData.Attempt.ID || resumedData.Attempt.State != string(research.AttemptSucceeded) || invocations.Load() != 1 {
		t.Fatalf("resumed gap = %#v invocations=%d", resumedData, invocations.Load())
	}
}

func TestDirectTryRepairsMarkerBeforeOperationalAndCanonicalTerminal(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	originalOpen := fixture.app.OpenOperational
	failFinish := true
	injected := errors.New("injected FinishJob failure")
	fixture.app.OpenOperational = func(ctx context.Context, info *project.Info) (OperationalStore, error) {
		store, openErr := originalOpen(ctx, info)
		if openErr != nil {
			return nil, openErr
		}
		return &finishFailureOperationalStore{OperationalStore: store, fail: &failFinish, err: injected}, nil
	}
	var invocations atomic.Int32
	delegate := execx.NewInvoker()
	fixture.app.Invoker = execx.InvokerFunc(func(ctx context.Context, spec execx.CommandSpec) (execx.Result, error) {
		invocations.Add(1)
		return delegate.Invoke(ctx, spec)
	})
	partial := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Marker recovery", "--goal", "Repair terminal ordering", "--json", "--", trueBinary)
	if !errors.Is(partial.err, injected) {
		t.Fatalf("marker gap = %v\nstdout=%s", partial.err, partial.stdout)
	}
	partialEnvelope := decodeEnvelope(t, partial.stdout)
	var partialData tryExecutionData
	decodeData(t, partialEnvelope, &partialData)
	if !partialEnvelope.Partial || partialData.Runtime.MarkerState != string(operation.JobSucceeded) || partialData.Attempt == nil || partialData.Attempt.State != string(research.AttemptRunning) || invocations.Load() != 1 {
		t.Fatalf("marker gap = envelope %#v data %#v invocations=%d", partialEnvelope, partialData, invocations.Load())
	}
	failFinish = false
	movedSource := fixture.source + "-moved-after-completion"
	if err := os.Rename(fixture.source, movedSource); err != nil {
		t.Fatal(err)
	}
	deferred := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "--workspace", fixture.projectID, "try", "resume", partialData.Try.ID, "--json")
	if deferred.err == nil {
		t.Fatalf("missing Source inspection imported success: %s", deferred.stdout)
	}
	var deferredData tryExecutionData
	deferredEnvelope := decodeEnvelope(t, deferred.stdout)
	decodeData(t, deferredEnvelope, &deferredData)
	if !deferredEnvelope.Partial || deferredData.Attempt == nil || deferredData.Attempt.State != string(research.AttemptRunning) || deferredData.Runtime.MarkerState != string(operation.JobSucceeded) || invocations.Load() != 1 {
		t.Fatalf("deferred marker recovery = envelope %#v data %#v invocations=%d", deferredEnvelope, deferredData, invocations.Load())
	}
	if err := os.Rename(movedSource, fixture.source); err != nil {
		t.Fatal(err)
	}
	recovered := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "--workspace", fixture.projectID, "try", "resume", partialData.Try.ID, "--json")
	requireCommandSuccess(t, recovered)
	var recoveredData tryExecutionData
	decodeData(t, decodeEnvelope(t, recovered.stdout), &recoveredData)
	if !recoveredData.Recovered || recoveredData.Attempt == nil || recoveredData.Attempt.State != string(research.AttemptSucceeded) || invocations.Load() != 1 {
		t.Fatalf("marker recovery = %#v invocations=%d", recoveredData, invocations.Load())
	}
}

func TestDirectTryTransientPostInvocationInspectionLeavesMarkerRecoverable(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	originalOpen := fixture.app.OpenOperational
	failFinish := true
	finishErr := errors.New("injected FinishJob failure")
	fixture.app.OpenOperational = func(ctx context.Context, info *project.Info) (OperationalStore, error) {
		store, openErr := originalOpen(ctx, info)
		if openErr != nil {
			return nil, openErr
		}
		return &finishFailureOperationalStore{OperationalStore: store, fail: &failFinish, err: finishErr}, nil
	}
	partial := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Inspection recovery", "--goal", "Keep successful marker pending", "--json", "--", trueBinary)
	if !errors.Is(partial.err, finishErr) {
		t.Fatalf("marker fixture = %v\n%s", partial.err, partial.stdout)
	}
	var partialData tryExecutionData
	decodeData(t, decodeEnvelope(t, partial.stdout), &partialData)
	failFinish = false
	var failInspect atomic.Bool
	failInspect.Store(true)
	inspectionErr := errors.New("transient Git lock failure")
	fixture.app.NewTryCoordinator = func() TryCoordinator {
		bundles := &sourcesnapshot.BundleStore{Root: filepath.Join(fixture.base, "cache", "source-seeds")}
		capturer := sourcesnapshot.Capturer{Git: fixture.app.GitRunner, Clock: fixture.app.clock, Bundles: bundles}
		native := workspacebackend.NativeGit{Git: fixture.app.GitRunner, Clock: fixture.app.clock, Bundles: bundles, DataHome: filepath.Join(fixture.base, "data")}
		backend := &transientInspectionBackend{Backend: native, fail: &failInspect, err: inspectionErr}
		return tryflow.NewDirect(tryflow.Dependencies{
			Resolver:  fixture.app.ResolveWorkspace,
			OpenStore: func(info *project.Info) (tryflow.Store, error) { return fixture.app.NewTransactionalStore(info) },
			OpenOperations: func(ctx context.Context, info *project.Info) (tryflow.OperationalStore, error) {
				return fixture.app.OpenOperational(ctx, info)
			},
			OpenOperationStatus: func(ctx context.Context, info *project.Info) (tryflow.OperationalStatusReader, error) {
				return fixture.app.OpenOperationalReadOnly(ctx, info)
			},
			Capturer: capturer, Bundles: bundles, Backend: backend, Git: fixture.app.GitRunner, Invoker: fixture.app.Invoker,
			LookupExecutable: fixture.app.BinaryLookup,
			Refresh: func(ctx context.Context, info *project.Info, store tryflow.Store) error {
				_, _, renderErr := renderFreshProjections(ctx, fixture.app, info, store)
				return renderErr
			},
			Clock: fixture.app.clock, GenerateUUID: fixture.app.GenerateUUID,
		})
	}
	deferred := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "resume", partialData.Try.ID, "--json")
	if !errors.Is(deferred.err, inspectionErr) {
		t.Fatalf("transient inspection should defer canonical terminal: %v\n%s", deferred.err, deferred.stdout)
	}
	var deferredData tryExecutionData
	decodeData(t, decodeEnvelope(t, deferred.stdout), &deferredData)
	if deferredData.Attempt == nil || deferredData.Attempt.State != string(research.AttemptRunning) || deferredData.Runtime.MarkerState != string(operation.JobSucceeded) {
		t.Fatalf("deferred marker state = %#v", deferredData)
	}
	recovered := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "resume", partialData.Try.ID, "--json")
	requireCommandSuccess(t, recovered)
	var recoveredData tryExecutionData
	decodeData(t, decodeEnvelope(t, recovered.stdout), &recoveredData)
	if recoveredData.Attempt == nil || recoveredData.Attempt.State != string(research.AttemptSucceeded) {
		t.Fatalf("recovered inspection marker = %#v", recoveredData)
	}
}

func TestDirectTryResumeReconcilesLateMarkerOnOlderAttempt(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	falseBinary, err := exec.LookPath("false")
	if err != nil {
		t.Skip(err)
	}
	falseBinary = filepath.Base(falseBinary)
	firstRun := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Late marker", "--goal", "Reconcile every Attempt", "--json", "--", falseBinary)
	if firstRun.err == nil {
		t.Fatalf("false command unexpectedly succeeded: %s", firstRun.stdout)
	}
	var firstData tryExecutionData
	decodeData(t, decodeEnvelope(t, firstRun.stdout), &firstData)
	secondRun := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "retry", firstData.Try.ID, "--json")
	if secondRun.err == nil {
		t.Fatalf("false retry unexpectedly succeeded: %s", secondRun.stdout)
	}
	var secondData tryExecutionData
	decodeData(t, decodeEnvelope(t, secondRun.stdout), &secondData)
	info, err := fixture.app.DiscoverProject(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	store, err := fixture.app.NewTransactionalStore(info)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := store.Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	firstID, _ := research.ParseID(firstData.Attempt.ID)
	secondID, _ := research.ParseID(secondData.Attempt.ID)
	firstDocument, _ := inventory.ByID(firstID)
	secondDocument, _ := inventory.ByID(secondID)
	late := firstDocument.Clone()
	lateAttempt := late.Record.(*research.Attempt)
	lateAttempt.State = research.AttemptUnknown
	lateAttempt.StateReason = "terminal marker arrived late"
	lateAttempt.Terminal = nil
	successor := secondDocument.Clone()
	successor.Record.(*research.Attempt).RetryOf = research.ID{}
	for _, document := range []*record.Document{late, successor} {
		encoded, encodeErr := record.Encode(document)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if writeErr := os.WriteFile(filepath.Join(info.Root, filepath.FromSlash(document.Path)), encoded, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	inventory, err = store.Inventory(t.Context())
	if err != nil || !inventory.Valid() {
		t.Fatalf("late-marker fixture inventory = %v, %v", inventory.Diagnostics, err)
	}
	resumed := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "resume", firstData.Try.ID, "--json")
	if !errors.Is(resumed.err, tryflow.ErrCommandFailed) {
		t.Fatalf("resume latest failed Attempt = %v\n%s", resumed.err, resumed.stdout)
	}
	inventory, err = store.Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := inventory.ByID(firstID)
	if err != nil || reconciled.Record.(*research.Attempt).State != research.AttemptFailed || reconciled.Record.(*research.Attempt).Terminal == nil {
		t.Fatalf("older marker remained unreconciled: %#v, %v", reconciled, err)
	}
}

func TestDirectTryCancellationPublishesAndRecoversMarkerWithoutReinvocation(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	fixture.app.Context = ctx
	started := make(chan struct{})
	var invocations atomic.Int32
	fixture.app.Invoker = execx.InvokerFunc(func(invokeContext context.Context, _ execx.CommandSpec) (execx.Result, error) {
		invocations.Add(1)
		now := time.Now().UTC()
		close(started)
		<-invokeContext.Done()
		return execx.Result{ExitCode: -1, StartedAt: now, FinishedAt: now, Canceled: true}, invokeContext.Err()
	})
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	completed := make(chan commandInvocation, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		fixture.app.In = strings.NewReader("")
		fixture.app.Out = &stdout
		fixture.app.Err = &stderr
		root := NewRootCommand(fixture.app)
		root.SetArgs([]string{"--start-dir", fixture.sourcePath, "try", "run", "--title", "Cancel check", "--goal", "Record cancellation", "--json", "--", trueBinary})
		runErr := root.ExecuteContext(ctx)
		completed <- commandInvocation{stdout: stdout.String(), stderr: stderr.String(), err: runErr}
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(30 * time.Second):
		t.Fatal("direct command did not start")
	}
	var partial commandInvocation
	select {
	case partial = <-completed:
	case <-time.After(30 * time.Second):
		t.Fatal("cancelled direct command did not return")
	}
	partialEnvelope := decodeEnvelope(t, partial.stdout)
	var partialData tryExecutionData
	decodeData(t, partialEnvelope, &partialData)
	if !partialEnvelope.Partial || partialData.Runtime.MarkerState == "" || partialData.Attempt == nil || invocations.Load() != 1 {
		t.Fatalf("cancel marker = error %v envelope %#v data %#v invocations=%d", partial.err, partialEnvelope, partialData, invocations.Load())
	}

	fixture.app.Context = t.Context()
	recovered := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "resume", partialData.Try.ID, "--json")
	if !errors.Is(recovered.err, context.Canceled) {
		t.Fatalf("recovered cancellation error = %v\nstdout=%s", recovered.err, recovered.stdout)
	}
	var recoveredData tryExecutionData
	recoveredEnvelope := decodeEnvelope(t, recovered.stdout)
	decodeData(t, recoveredEnvelope, &recoveredData)
	if recoveredEnvelope.Partial || !recoveredData.Recovered || recoveredData.Attempt == nil || recoveredData.Attempt.State != string(research.AttemptCancelled) || invocations.Load() != 1 {
		t.Fatalf("recovered cancellation = envelope %#v data %#v invocations=%d", recoveredEnvelope, recoveredData, invocations.Load())
	}
}

func TestDirectTryUnknownCannotRetryUntilExplicitReconciliation(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	fixture.app.Now = func() time.Time { return now }
	injected := errors.New("simulated process loss before invocation")
	fixture.app.NewTryCoordinator = func() TryCoordinator {
		bundles := &sourcesnapshot.BundleStore{}
		capturer := sourcesnapshot.Capturer{Git: fixture.app.GitRunner, Clock: fixture.app.clock, Bundles: bundles}
		backend := workspacebackend.NativeGit{Git: fixture.app.GitRunner, Clock: fixture.app.clock, Bundles: bundles}
		return tryflow.NewDirect(tryflow.Dependencies{
			Resolver:  fixture.app.ResolveWorkspace,
			OpenStore: func(info *project.Info) (tryflow.Store, error) { return fixture.app.NewTransactionalStore(info) },
			OpenOperations: func(ctx context.Context, info *project.Info) (tryflow.OperationalStore, error) {
				return fixture.app.OpenOperational(ctx, info)
			},
			OpenOperationStatus: func(ctx context.Context, info *project.Info) (tryflow.OperationalStatusReader, error) {
				return fixture.app.OpenOperationalReadOnly(ctx, info)
			},
			Capturer: capturer, Bundles: bundles, Backend: backend, Git: fixture.app.GitRunner, Invoker: fixture.app.Invoker,
			LookupExecutable: fixture.app.BinaryLookup,
			RunJob: func(context.Context, tryflow.OperationalStore, string, operation.Job, func(context.Context, worker.Workload) error) (worker.Terminal, error) {
				return worker.Terminal{}, injected
			},
			Refresh: func(ctx context.Context, info *project.Info, store tryflow.Store) error {
				_, _, renderErr := renderFreshProjections(ctx, fixture.app, info, store)
				return renderErr
			},
			Clock: fixture.app.clock, GenerateUUID: fixture.app.GenerateUUID, ClaimTTL: time.Second,
		})
	}
	crashed := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Uncertain execution", "--goal", "Require explicit disposition", "--json", "--", trueBinary)
	if !errors.Is(crashed.err, injected) {
		t.Fatalf("simulated process loss = %v\n%s", crashed.err, crashed.stdout)
	}
	var crashedData tryExecutionData
	decodeData(t, decodeEnvelope(t, crashed.stdout), &crashedData)
	if crashedData.Try == nil || crashedData.Attempt == nil || crashedData.Attempt.State != string(research.AttemptRunning) {
		t.Fatalf("crash-gap data = %#v", crashedData)
	}
	now = now.Add(2 * time.Second)
	firstRetry := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "retry", crashedData.Try.ID, "--json")
	if !errors.Is(firstRetry.err, tryflow.ErrExecutionUncertain) {
		t.Fatalf("expired retry error = %v\n%s", firstRetry.err, firstRetry.stdout)
	}
	var uncertainData tryExecutionData
	decodeData(t, decodeEnvelope(t, firstRetry.stdout), &uncertainData)
	if uncertainData.Attempt == nil || uncertainData.Attempt.ID != crashedData.Attempt.ID || uncertainData.Attempt.State != string(research.AttemptUnknown) {
		t.Fatalf("uncertain retry data = %#v", uncertainData)
	}
	secondRetry := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "retry", crashedData.Try.ID, "--json")
	if secondRetry.err == nil {
		t.Fatalf("unknown Attempt unexpectedly retried: %s", secondRetry.stdout)
	}
	status := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "status", crashedData.Try.ID, "--json")
	requireCommandSuccess(t, status)
	var statusData tryStatusData
	decodeData(t, decodeEnvelope(t, status.stdout), &statusData)
	if statusData.Tries[0].AttemptCount != 1 {
		t.Fatalf("unknown retry created another Attempt: %#v", statusData)
	}
	originalOpen := fixture.app.OpenOperational
	storeUnavailable := errors.New("injected operational store failure")
	fixture.app.OpenOperational = func(context.Context, *project.Info) (OperationalStore, error) {
		return nil, storeUnavailable
	}
	blockedReconcile := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "reconcile", crashedData.Try.ID, "--attempt", crashedData.Attempt.ID,
		"--abandon-uncertain", "--reason", "Execution ownership was lost.", "--confirm", "--json")
	if !errors.Is(blockedReconcile.err, storeUnavailable) {
		t.Fatalf("operational failure did not block reconciliation: %v\n%s", blockedReconcile.err, blockedReconcile.stdout)
	}
	fixture.app.OpenOperational = originalOpen
	info, err := fixture.app.DiscoverProject(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := record.NewStore(info.Root, info.Repository.GitCommonDir).Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unknownID, _ := research.ParseID(crashedData.Attempt.ID)
	if current, lookupErr := inventory.ByID(unknownID); lookupErr != nil || current.Record.(*research.Attempt).State != research.AttemptUnknown {
		t.Fatalf("failed reconciliation terminalized Attempt: %#v, %v", current, lookupErr)
	}

	queryFailure := errors.New("injected operational query failure")
	fixture.app.OpenOperational = func(ctx context.Context, info *project.Info) (OperationalStore, error) {
		store, err := originalOpen(ctx, info)
		if err != nil {
			return nil, err
		}
		return &getFailureOperationalStore{OperationalStore: store, err: queryFailure}, nil
	}
	queryBlocked := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "reconcile", crashedData.Try.ID, "--attempt", crashedData.Attempt.ID,
		"--abandon-uncertain", "--reason", "Execution ownership was lost.", "--confirm", "--json")
	if !errors.Is(queryBlocked.err, queryFailure) {
		t.Fatalf("query failure did not block reconciliation: %v\n%s", queryBlocked.err, queryBlocked.stdout)
	}

	fixture.app.OpenOperational = func(ctx context.Context, info *project.Info) (OperationalStore, error) {
		store, err := originalOpen(ctx, info)
		if err != nil {
			return nil, err
		}
		return &fencedFinishOperationalStore{OperationalStore: store}, nil
	}
	fenced := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "reconcile", crashedData.Try.ID, "--attempt", crashedData.Attempt.ID,
		"--abandon-uncertain", "--reason", "Execution ownership was lost.", "--confirm", "--json")
	if !errors.Is(fenced.err, operation.ErrFenced) || !errors.Is(fenced.err, tryflow.ErrExecutionUncertain) {
		t.Fatalf("fenced reconciliation did not fail closed: %v\n%s", fenced.err, fenced.stdout)
	}
	fixture.app.OpenOperational = originalOpen
	inventory, err = record.NewStore(info.Root, info.Repository.GitCommonDir).Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if current, lookupErr := inventory.ByID(unknownID); lookupErr != nil || current.Record.(*research.Attempt).State != research.AttemptUnknown {
		t.Fatalf("query/fence failure terminalized Attempt: %#v, %v", current, lookupErr)
	}

	reconciled := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "reconcile", crashedData.Try.ID, "--attempt", crashedData.Attempt.ID,
		"--abandon-uncertain", "--reason", "Execution ownership was lost.", "--confirm", "--json")
	if reconciled.err != nil {
		t.Fatalf("explicit reconciliation = %v\n%s", reconciled.err, reconciled.stdout)
	}
	var reconciledData tryExecutionData
	decodeData(t, decodeEnvelope(t, reconciled.stdout), &reconciledData)
	if reconciledData.Attempt == nil || reconciledData.Attempt.State != string(research.AttemptCancelled) {
		t.Fatalf("reconciled Attempt = %#v", reconciledData)
	}
}

func TestDirectTryRenewsShortLeaseWithUniqueInvocationHolder(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	fixture.app.Now = time.Now
	started, release := make(chan struct{}), make(chan struct{})
	delegate := execx.NewInvoker()
	fixture.app.Invoker = execx.InvokerFunc(func(ctx context.Context, spec execx.CommandSpec) (execx.Result, error) {
		close(started)
		<-release
		return delegate.Invoke(ctx, spec)
	})
	fixture.app.NewTryCoordinator = func() TryCoordinator {
		bundles := &sourcesnapshot.BundleStore{}
		capturer := sourcesnapshot.Capturer{Git: fixture.app.GitRunner, Clock: fixture.app.clock, Bundles: bundles}
		backend := workspacebackend.NativeGit{Git: fixture.app.GitRunner, Clock: fixture.app.clock, Bundles: bundles}
		return tryflow.NewDirect(tryflow.Dependencies{
			Resolver:  fixture.app.ResolveWorkspace,
			OpenStore: func(info *project.Info) (tryflow.Store, error) { return fixture.app.NewTransactionalStore(info) },
			OpenOperations: func(ctx context.Context, info *project.Info) (tryflow.OperationalStore, error) {
				return fixture.app.OpenOperational(ctx, info)
			},
			OpenOperationStatus: func(ctx context.Context, info *project.Info) (tryflow.OperationalStatusReader, error) {
				return fixture.app.OpenOperationalReadOnly(ctx, info)
			},
			Capturer: capturer, Bundles: bundles, Backend: backend, Git: fixture.app.GitRunner, Invoker: fixture.app.Invoker,
			LookupExecutable: fixture.app.BinaryLookup,
			Refresh: func(ctx context.Context, info *project.Info, store tryflow.Store) error {
				_, _, renderErr := renderFreshProjections(ctx, fixture.app, info, store)
				return renderErr
			},
			Clock: fixture.app.clock, GenerateUUID: fixture.app.GenerateUUID, ClaimTTL: 60 * time.Millisecond,
		})
	}
	firstApp, secondApp := *fixture.app, *fixture.app
	completed := make(chan commandInvocation, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		firstApp.In, firstApp.Out, firstApp.Err = strings.NewReader(""), &stdout, &stderr
		root := NewRootCommand(&firstApp)
		root.SetArgs([]string{"--start-dir", fixture.sourcePath, "try", "run", "--title", "Lease heartbeat", "--goal", "Renew active ownership", "--json", "--", trueBinary})
		completed <- commandInvocation{stdout: stdout.String(), stderr: stderr.String(), err: root.ExecuteContext(t.Context())}
	}()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("short-lease command did not start")
	}
	time.Sleep(180 * time.Millisecond)
	status := invokeCommand(t, &secondApp, "", "--start-dir", fixture.sourcePath, "try", "status", "--json")
	requireCommandSuccess(t, status)
	var statusData tryStatusData
	decodeData(t, decodeEnvelope(t, status.stdout), &statusData)
	runtime := statusData.Tries[0].Attempts[0].Runtime
	if !runtime.Active || runtime.LeaseExpired || runtime.JobID == "" {
		t.Fatalf("heartbeat did not retain active lease: %#v", runtime)
	}
	info, err := fixture.app.DiscoverProject(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := fixture.app.OpenOperational(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}
	job, err := operations.GetJob(t.Context(), runtime.JobID)
	_ = operations.Close()
	if err != nil || job.ClaimedBy == "exp-try-direct" || !strings.HasPrefix(job.ClaimedBy, "exp-try-direct-") {
		t.Fatalf("invocation holder = %q err=%v", job.ClaimedBy, err)
	}
	close(release)
	if finished := <-completed; finished.err != nil {
		t.Fatalf("short-lease execution failed: %v\n%s", finished.err, finished.stdout)
	}
}

func TestDirectTryRetryObservesLiveLeaseWithoutDuplicateExecution(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	started := make(chan struct{})
	release := make(chan struct{})
	var invocations atomic.Int32
	delegate := execx.NewInvoker()
	fixture.app.Invoker = execx.InvokerFunc(func(ctx context.Context, spec execx.CommandSpec) (execx.Result, error) {
		invocations.Add(1)
		close(started)
		<-release
		return delegate.Invoke(ctx, spec)
	})
	firstApp := *fixture.app
	secondApp := *fixture.app
	completed := make(chan commandInvocation, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		firstApp.In, firstApp.Out, firstApp.Err = strings.NewReader(""), &stdout, &stderr
		root := NewRootCommand(&firstApp)
		root.SetArgs([]string{"--start-dir", fixture.sourcePath, "try", "run", "--title", "Live lease", "--goal", "Prevent duplicate execution", "--json", "--", trueBinary})
		completed <- commandInvocation{stdout: stdout.String(), stderr: stderr.String(), err: root.ExecuteContext(t.Context())}
	}()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("direct command did not start")
	}
	status := invokeCommand(t, &secondApp, "", "--start-dir", fixture.sourcePath, "try", "status", "--json")
	requireCommandSuccess(t, status)
	var statusData tryStatusData
	decodeData(t, decodeEnvelope(t, status.stdout), &statusData)
	if len(statusData.Tries) != 1 || len(statusData.Tries[0].Attempts) != 1 || !statusData.Tries[0].Attempts[0].Runtime.Active || statusData.Tries[0].Attempts[0].Runtime.Uncertain {
		t.Fatalf("live status = %#v", statusData)
	}
	tryID := statusData.Tries[0].Try.ID
	retry := invokeCommand(t, &secondApp, "", "--start-dir", fixture.sourcePath, "try", "retry", tryID, "--json")
	if !errors.Is(retry.err, tryflow.ErrExecutionInProgress) || invocations.Load() != 1 {
		t.Fatalf("live retry = %v invocations=%d stdout=%s", retry.err, invocations.Load(), retry.stdout)
	}
	close(release)
	select {
	case finished := <-completed:
		if finished.err != nil {
			t.Fatalf("original execution failed: %v\n%s", finished.err, finished.stdout)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("original execution did not finish")
	}
	finalStatus := invokeCommand(t, &secondApp, "", "--start-dir", fixture.sourcePath, "try", "status", tryID, "--json")
	requireCommandSuccess(t, finalStatus)
	decodeData(t, decodeEnvelope(t, finalStatus.stdout), &statusData)
	if statusData.Tries[0].AttemptCount != 1 || invocations.Load() != 1 {
		t.Fatalf("retry duplicated Attempt: %#v invocations=%d", statusData, invocations.Load())
	}
}

func TestDirectTryJSONRequiresExplicitFieldsAndRedactsArguments(t *testing.T) {
	parallelDirectTry(t)
	fixture := newDirectTryCLIFixture(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	trueBinary = filepath.Base(trueBinary)
	missingGoal := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "No implicit prompt", "--json", "--", trueBinary)
	if missingGoal.err == nil {
		t.Fatal("missing explicit goal unexpectedly succeeded")
	}
	missingEnvelope := decodeEnvelope(t, missingGoal.stdout)
	if missingEnvelope.OK || missingEnvelope.Partial || missingGoal.stderr != "" {
		t.Fatalf("missing-goal contract = envelope %#v stderr=%q", missingEnvelope, missingGoal.stderr)
	}

	absoluteExecutable, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	for name, argv := range map[string][]string{
		"absolute executable":     {absoluteExecutable},
		"attached absolute value": {trueBinary, "--config=/etc/passwd"},
	} {
		rejectedArgs := append([]string{"--start-dir", fixture.sourcePath, "try", "run", "--title", "Host path rejection", "--goal", "Keep canonical argv portable", "--json", "--"}, argv...)
		rejected := invokeCommand(t, fixture.app, "", rejectedArgs...)
		if rejected.err == nil || decodeEnvelope(t, rejected.stdout).Partial {
			t.Fatalf("%s was accepted: %v\n%s", name, rejected.err, rejected.stdout)
		}
	}
	info, err := fixture.app.DiscoverProject(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := record.NewStore(info.Root, info.Repository.GitCommonDir).Inventory(t.Context())
	if err != nil || len(inventory.OfKind(research.KindAttempt)) != 0 {
		t.Fatalf("rejected host argv published Attempts: %d, %v", len(inventory.OfKind(research.KindAttempt)), err)
	}

	canary := "try-secret-canary-4871"
	secret := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Secret rejection", "--goal", "Reject credential arguments", "--json", "--", trueBinary, "--password="+canary)
	if secret.err == nil || strings.Contains(secret.stdout, canary) || strings.Contains(secret.stderr, canary) || strings.Contains(secret.err.Error(), canary) {
		t.Fatalf("secret argument contract = error=%v stdout=%s stderr=%s", secret.err, secret.stdout, secret.stderr)
	}
	secretEnvelope := decodeEnvelope(t, secret.stdout)
	if secretEnvelope.OK || secretEnvelope.Partial {
		t.Fatalf("secret envelope = %#v", secretEnvelope)
	}

	touchBinary, err := exec.LookPath("touch")
	if err != nil {
		t.Skip(err)
	}
	touchBinary = filepath.Base(touchBinary)
	hostTarget := filepath.Join(fixture.sourcePath, "escaped.txt")
	escaped := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Absolute rejection", "--goal", "Reject host paths", "--json", "--", touchBinary, hostTarget)
	if escaped.err == nil || decodeEnvelope(t, escaped.stdout).Partial {
		t.Fatalf("absolute argv was accepted: %v\n%s", escaped.err, escaped.stdout)
	}
	if _, err := os.Stat(hostTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected absolute argv changed production: %v", err)
	}

	missingExecutable := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Missing executable", "--goal", "Keep paths private", "--json", "--", "./does-not-exist")
	if missingExecutable.err == nil || strings.Contains(missingExecutable.stdout, fixture.base) || strings.Contains(missingExecutable.err.Error(), fixture.base) {
		t.Fatalf("executable error leaked a host path: err=%v stdout=%s", missingExecutable.err, missingExecutable.stdout)
	}

	if err := os.WriteFile(filepath.Join(fixture.sourcePath, "uncaptured.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uncaptured := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Dirty rejection", "--goal", "Require explicit capture", "--json", "--", trueBinary)
	if !errors.Is(uncaptured.err, sourcesnapshot.ErrDirtySource) {
		t.Fatalf("implicit dirty error = %v\nstdout=%s", uncaptured.err, uncaptured.stdout)
	}
	uncapturedEnvelope := decodeEnvelope(t, uncaptured.stdout)
	if uncapturedEnvelope.Partial || len(uncapturedEnvelope.Diagnostics) != 1 || uncapturedEnvelope.Diagnostics[0].Code != "try.dirty_capture_required" {
		t.Fatalf("implicit dirty envelope = %#v", uncapturedEnvelope)
	}
}

type transientInspectionBackend struct {
	workspacebackend.Backend
	fail *atomic.Bool
	err  error
}

func (backend *transientInspectionBackend) Inspect(ctx context.Context, request workspacebackend.PrepareRequest) (workspacebackend.Inspection, error) {
	if backend.fail != nil && backend.fail.CompareAndSwap(true, false) {
		return workspacebackend.Inspection{}, backend.err
	}
	return backend.Backend.Inspect(ctx, request)
}

type getFailureOperationalStore struct {
	OperationalStore
	err error
}

func (store *getFailureOperationalStore) GetJob(context.Context, string) (operation.Job, error) {
	return operation.Job{}, store.err
}

type fencedFinishOperationalStore struct {
	OperationalStore
}

func (store *fencedFinishOperationalStore) FinishJob(context.Context, string, int64, operation.JobState, json.RawMessage, string) (operation.Job, error) {
	return operation.Job{}, operation.ErrFenced
}

type finishFailureOperationalStore struct {
	OperationalStore
	fail *bool
	err  error
}

func (store *finishFailureOperationalStore) FinishJob(ctx context.Context, id string, token int64, state operation.JobState, result json.RawMessage, message string) (operation.Job, error) {
	if store.fail != nil && *store.fail {
		return operation.Job{}, store.err
	}
	return store.OperationalStore.FinishJob(ctx, id, token, state, result, message)
}

func newDirectTryCLIFixture(t *testing.T) *externalCLIFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base = filepath.Clean(base)
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical := initExternalGitRepository(t, filepath.Join(base, "canonical"))
	sourceRoot := initExternalGitRepository(t, filepath.Join(base, "source"))
	sourcePath := filepath.Join(sourceRoot, "services", "api", "pkg")
	if err := os.MkdirAll(sourcePath, 0o755); err != nil {
		t.Fatal(err)
	}
	externalGit(t, sourceRoot, "remote", "add", "origin", "https://example.com/org/source.git")
	app := deterministicApp(t, "01a0a000-0000-7001-8000-000000000001")
	app.Associations = workspace.NewStore(
		workspace.WithPath(filepath.Join(base, "state", "associations.json")),
		workspace.WithClock(app.clock), workspace.WithGitRunner(app.GitRunner),
	)
	app.TrustStore = trust.NewStore(trust.WithPath(filepath.Join(base, "state", "trust.json")), trust.WithClock(app.clock))
	app.ConfigLoader = config.NewLoader(
		config.WithUserConfigPath(filepath.Join(home, "config", "exp", "config.toml")),
		config.WithTrustChecker(app.TrustStore),
	)
	app.ResolveWorkspace = appWorkspaceResolver{app: app}
	info, _, err := app.InitializeProject(t.Context(), project.InitRequest{StartDir: canonical, Name: "External CLI"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Associations.RegisterProject(t.Context(), info); err != nil {
		t.Fatal(err)
	}
	app.GenerateUUID = research.DefaultUUIDGenerator
	if err := os.WriteFile(filepath.Join(sourceRoot, "README.md"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "input.txt"), []byte("clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalGit(t, sourceRoot, "config", "user.name", "Try Test")
	externalGit(t, sourceRoot, "config", "user.email", "try@example.invalid")
	externalGit(t, sourceRoot, "add", "--", ".")
	externalGit(t, sourceRoot, "commit", "-qm", "source baseline")
	store, err := app.NewTransactionalStore(info)
	if err != nil {
		t.Fatal(err)
	}
	sourceDocument, err := sourcepkg.New(store, sourcepkg.WithClock(app.clock), sourcepkg.WithUUIDGenerator(app.GenerateUUID)).Add(t.Context(), sourcepkg.AddRequest{
		Key: "production", Title: "Production", Subdir: "services/api", LocatorHints: []string{"https://example.com/org/source.git"}, Body: "# Production\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, _ := sourceDocument.ID()
	if _, err := app.Associations.RegisterSource(t.Context(), info.Project().ProjectID, sourceID, sourceRoot); err != nil {
		t.Fatal(err)
	}
	fixture := &externalCLIFixture{
		base: base, home: home, canonical: canonical, source: sourceRoot, sourcePath: sourcePath,
		app: app, projectID: info.Project().ProjectID.String(), sourceID: sourceID.String(), revision: sourceDocument.Revision,
	}
	app.NewTryCoordinator = func() TryCoordinator {
		bundles := &sourcesnapshot.BundleStore{Root: filepath.Join(base, "cache", "source-seeds")}
		capturer := sourcesnapshot.Capturer{Git: app.GitRunner, Clock: app.clock, Bundles: bundles}
		native := workspacebackend.NativeGit{Git: app.GitRunner, Clock: app.clock, Bundles: bundles, DataHome: filepath.Join(base, "data")}
		backend := app.WorkspaceRegistry.WithNative(native)
		return tryflow.NewDirect(tryflow.Dependencies{
			Resolver: app.ResolveWorkspace,
			OpenStore: func(info *project.Info) (tryflow.Store, error) {
				return app.NewTransactionalStore(info)
			},
			OpenOperations: func(ctx context.Context, info *project.Info) (tryflow.OperationalStore, error) {
				return app.OpenOperational(ctx, info)
			},
			OpenOperationStatus: func(ctx context.Context, info *project.Info) (tryflow.OperationalStatusReader, error) {
				return app.OpenOperationalReadOnly(ctx, info)
			},
			OperationalAvailable: operation.Available,
			Capturer:             capturer, Bundles: bundles, Backend: backend,
			Git: app.GitRunner, Invoker: app.Invoker, LookupExecutable: app.BinaryLookup,
			Refresh: func(ctx context.Context, info *project.Info, store tryflow.Store) error {
				_, _, err := renderFreshProjections(ctx, app, info, store)
				return err
			},
			Clock: app.clock, GenerateUUID: app.GenerateUUID,
		})
	}
	// State-machine cases exercise canonical/operational/worktree durability. Keep
	// one dedicated test on the real renderer and avoid rebuilding the same derived
	// views at every intermediate transition in all other cases.
	fixture.app.RenderProjections = func(context.Context, *record.Inventory) (projection.Result, error) {
		return projection.Result{Current: true, Written: []string{}, Unchanged: []string{}, Drifted: []string{}, Files: []projection.FileResult{}}, nil
	}
	return fixture
}
