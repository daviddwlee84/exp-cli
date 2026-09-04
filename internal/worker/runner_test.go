package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/mlflow"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
)

type fakeStore struct {
	finished       int
	state          operation.JobState
	result         json.RawMessage
	message        string
	mlflowRunID    string
	externalWrites int
}

func (store *fakeStore) FinishJob(_ context.Context, _ string, _ int64, state operation.JobState, result json.RawMessage, message string) (operation.Job, error) {
	store.finished++
	store.state = state
	store.result = append(json.RawMessage(nil), result...)
	store.message = message
	return operation.Job{State: state}, nil
}

func (store *fakeStore) SetJobExternalRefs(_ context.Context, _ string, _ int64, _ *int64, mlflowRunID string) error {
	store.externalWrites++
	store.mlflowRunID = mlflowRunID
	return nil
}

func TestRunnerPublishesMarkerBeforeFinishingJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	directory := t.TempDir()
	executable := filepath.Join(directory, "workload")
	script := "#!/bin/sh\nmkdir -p outputs\nprintf '%s' verified > outputs/metrics.json\nprintf '%s' '{\"metric\":0.91,\"mlflow_run_id\":\"abc\"}' > \"$EXP_RESULT_PATH\"\n"
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runWorkerGit(t, directory, "init", "--quiet")
	runWorkerGit(t, directory, "config", "user.name", "Worker Test")
	runWorkerGit(t, directory, "config", "user.email", "worker@example.invalid")
	runWorkerGit(t, directory, "add", "workload")
	runWorkerGit(t, directory, "commit", "--quiet", "-m", "fixture")
	head := strings.TrimSpace(runWorkerGit(t, directory, "rev-parse", "HEAD"))
	payload, err := json.Marshal(Workload{
		Schema: JobSchema, AttemptID: "att_1", Executable: executable, CWD: directory, Timeout: "20s",
		RepositoryRoot: directory, BaseCommit: head, HeadCommit: head, ChangeSet: []string{}, ExpectedOutputs: []string{"outputs/metrics.json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	markerRoot := filepath.Join(t.TempDir(), "markers")
	if err := os.Mkdir(markerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	markerRoot, err = filepath.EvalSymlinks(markerRoot)
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{Store: store, MarkerRoot: markerRoot, Clock: func() time.Time {
		now = now.Add(time.Second)
		return now
	}}
	job := operation.Job{JobInput: operation.JobInput{ID: "job_1", Payload: payload}, State: operation.JobRunning, FencingToken: 7}
	terminal, err := runner.Run(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	stored, decodeErr := DecodeJobResult(store.result)
	if terminal.State != operation.JobSucceeded || terminal.ResultSHA256 == "" || terminal.Outputs["outputs/metrics.json"] == "" || store.finished != 1 || decodeErr != nil || string(stored.Result) != `{"metric":0.91,"mlflow_run_id":"abc"}` || stored.Terminal.ResultSHA256 != terminal.ResultSHA256 {
		t.Fatalf("terminal=%#v store=%#v", terminal, store)
	}
	marker := filepath.Join(runner.MarkerRoot, jobArtifactBase("job_1")+".json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("terminal marker missing: %v", err)
	}
	// Replaying the same claimed job uses the durable marker to repair the
	// operational state in case the first FinishJob was interrupted.
	again, err := runner.Run(t.Context(), job)
	stored, decodeErr = DecodeJobResult(store.result)
	if err != nil || again.ResultSHA256 != terminal.ResultSHA256 || store.finished != 2 || decodeErr != nil || string(stored.Result) != `{"metric":0.91,"mlflow_run_id":"abc"}` {
		t.Fatalf("replay=%#v finished=%d err=%v", again, store.finished, err)
	}
	resultPath := filepath.Join(runner.MarkerRoot, jobArtifactBase("job_1")+".result.json")
	if err := os.Chmod(resultPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultPath, []byte(`{"metric":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(t.Context(), job); err == nil || store.finished != 2 {
		t.Fatalf("tampered replay finalized job: finished=%d err=%v", store.finished, err)
	}
}

func TestWorkerProfileRedactsSecretsAndReplaysVerifiedMLflowAttachment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	const secret = "PROFILE_RESULT_SECRET_CANARY_7b3a"
	const attemptID = "att_01a09000-0000-7202-8000-000000000202"
	t.Setenv("PARENT_MLFLOW_TOKEN", secret)

	sourceRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workloadExecutable := filepath.Join(sourceRoot, "workload")
	workloadScript := "#!/bin/sh\nprintf '%s\\n' \"$WORKLOAD_SECRET\"\nprintf '%s\\n' \"$WORKLOAD_SECRET\" >&2\nprintf '{\"leak\":\"%s\",\"token\":\"%s\",\"mlflow_run_id\":\"run-123\"}' \"$WORKLOAD_SECRET\" \"$WORKLOAD_SECRET\" > \"$EXP_RESULT_PATH\"\n"
	if err := os.WriteFile(workloadExecutable, []byte(workloadScript), 0o755); err != nil {
		t.Fatal(err)
	}
	runWorkerGit(t, sourceRoot, "init", "--quiet")
	runWorkerGit(t, sourceRoot, "config", "user.name", "Worker Test")
	runWorkerGit(t, sourceRoot, "config", "user.email", "worker@example.invalid")
	runWorkerGit(t, sourceRoot, "add", "workload")
	runWorkerGit(t, sourceRoot, "commit", "--quiet", "-m", "fixture")
	head := strings.TrimSpace(runWorkerGit(t, sourceRoot, "rev-parse", "HEAD"))
	common := strings.TrimSpace(runWorkerGit(t, sourceRoot, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	identity, err := pathx.DirectoryFilesystemIdentity(common)
	if err != nil {
		t.Fatal(err)
	}

	mlflowRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mlflowExecutable := filepath.Join(mlflowRoot, "mlflow-test")
	mlflowOutput := `{"info":{"run_id":"run-123","experiment_id":"7","status":"FINISHED","artifact_uri":"https://tracking.example.invalid/artifacts/run-123?token=hidden"},"data":{"metrics":{"score":0.91},"tags":{"exp.attempt_id":"` + attemptID + `","secret":"not-returned"}}}`
	if err := os.WriteFile(mlflowExecutable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 4, 6, 30, 0, 0, time.UTC)
	sourceID, err := research.ParseIDForKind("src_01a09000-0000-7101-8000-000000000101", research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := research.SourceSnapshot{
		Source: sourceID, Subdir: ".", PolicyVersion: sourcesnapshot.PolicyVersion,
		CapturedAt: now, GitObjectFormat: research.GitObjectSHA1,
		BaseCommit: head, HeadCommit: head, ChangeSet: []string{}, State: research.SourceSnapshotClean,
		Reproducibility: research.ReproducibilityExact,
	}
	snapshot.Digest, err = research.SourceSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	binding := SourceBinding{
		Checkout: "main", RepositoryRoot: sourceRoot, SemanticRoot: sourceRoot,
		RegisteredGitCommonDir: common, RegisteredGitCommonIdentity: identity,
		Snapshot: BindSourceSnapshot(snapshot),
	}
	profile := &mlflow.WorkloadProfile{
		Name: "training", Context: "local-test", Binary: "mlflow-test", Timeout: "2s",
		Environment: []mlflow.EnvironmentBinding{{
			Child: "WORKLOAD_SECRET", From: "PARENT_MLFLOW_TOKEN", Secret: true, Required: true,
		}},
		DefaultMetrics: []string{"score"},
	}
	workload := Workload{
		Schema: SourceJobSchema, AttemptID: attemptID,
		ProjectID: "01a01e50-0000-7001-8000-000000000001", CanonicalScope: "scope-profile-test",
		ExecutionSource: sourceID.String(), Sources: []SourceBinding{binding},
		Executable: workloadExecutable, Args: []string{}, CWD: sourceRoot, Timeout: "5s",
		AllowedEnv: []string{}, SecretEnv: []string{}, MLflow: profile,
		RepositoryRoot: sourceRoot, OutputRoot: sourceRoot,
		BaseCommit: head, HeadCommit: head, ChangeSet: []string{}, ExpectedOutputs: []string{},
	}
	workload.CheckoutIdentity = SourceCheckoutIdentity(workload.ProjectID, workload.CanonicalScope, workload.ExecutionSource, workload.Sources)
	payload, err := json.Marshal(workload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), secret) {
		t.Fatal("resolved secret entered the private job payload")
	}
	markerRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	runner := Runner{
		Store: store, MarkerRoot: markerRoot, ProjectID: workload.ProjectID, CanonicalScope: workload.CanonicalScope,
		MLflowInvoker: execx.InvokerFunc(func(_ context.Context, spec execx.CommandSpec) (execx.Result, error) {
			if spec.Executable != mlflowExecutable || !reflect.DeepEqual(spec.Argv, []string{"runs", "describe", "--run-id", "run-123"}) {
				t.Fatalf("unexpected MLflow invocation: %#v", spec)
			}
			return execx.Result{Stdout: mlflowOutput, ExitCode: 0}, nil
		}),
		LookupBinary: func(name string) (string, error) {
			if name == "mlflow-test" {
				return mlflowExecutable, nil
			}
			return "", errors.New("missing")
		},
		Clock: func() time.Time { return now },
	}
	job := operation.Job{
		JobInput: operation.JobInput{ID: "job-profile", SubjectID: attemptID, CanonicalScope: workload.CanonicalScope, Payload: payload},
		State:    operation.JobRunning, FencingToken: 17,
	}
	terminal, err := runner.Run(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.State != operation.JobSucceeded || terminal.MLflow == nil || terminal.MLflow.State != mlflow.ObservationVerified || terminal.MLflow.Tags[mlflow.AttemptOwnershipTag] != attemptID {
		t.Fatalf("terminal MLflow observation = %#v attachment=%#v", terminal, terminal.MLflow)
	}
	if store.mlflowRunID != "run-123" || store.externalWrites != 1 {
		t.Fatalf("operational MLflow identity = %q writes=%d", store.mlflowRunID, store.externalWrites)
	}
	stored, err := DecodeJobResult(store.result)
	if err != nil {
		t.Fatal(err)
	}
	markerBytes, err := json.Marshal(terminal)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(markerRoot, jobArtifactBase(job.ID)+".result.json")
	resultBytes, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]string{
		"stdout": terminal.Stdout, "stderr": terminal.Stderr, "job": string(payload),
		"stored job result": string(store.result), "workload result": string(stored.Result),
		"terminal marker": string(markerBytes), "durable result": string(resultBytes),
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("%s retained resolved secret: %q", label, value)
		}
	}
	if !strings.Contains(string(stored.Result), execx.Redacted) || !strings.Contains(terminal.Stdout, execx.Redacted) || !strings.Contains(terminal.Stderr, execx.Redacted) {
		t.Fatalf("redaction was not visible in selected safe outputs: terminal=%#v result=%s", terminal, stored.Result)
	}
	reference, err := terminal.MLflow.ExternalRef(attemptID)
	if err != nil || reference.NativeID != "run-123" || strings.Contains(reference.URI, "token=") {
		t.Fatalf("canonical MLflow reference = %#v, err=%v", reference, err)
	}

	replayed, err := runner.Run(t.Context(), job)
	if err != nil || replayed.MLflow == nil || replayed.MLflow.RunID != "run-123" || store.finished != 2 || store.externalWrites != 2 {
		t.Fatalf("marker replay = %#v store=%#v err=%v", replayed, store, err)
	}
}

func runWorkerGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func TestTerminalMarkerRepresentsWorstCaseEscapedStreams(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	terminal := Terminal{
		Schema: SourceTerminalSchema, JobID: "job-escaped", AttemptID: "att-escaped", FencingToken: 1,
		State: operation.JobSucceeded, StartedAt: now, EndedAt: now,
		Stdout: strings.Repeat("\"", maxTerminalStreamBytes), Stderr: strings.Repeat("\"", maxTerminalStreamBytes),
		Outputs: map[string]string{},
	}
	if err := writeTerminal(root, "escaped.json", terminal); err != nil {
		t.Fatalf("write worst-case escaped terminal: %v", err)
	}
	content, _, err := pathx.ReadBoundedRegularFile(t.Context(), root, "escaped.json", maxTerminalBytes)
	if err != nil || len(content) <= 64<<10 {
		t.Fatalf("escaped terminal size=%d err=%v", len(content), err)
	}
}

func TestDecodeWorkloadRejectsUnknownFields(t *testing.T) {
	_, err := decodeWorkload([]byte(`{"schema_version":"exp.worker-job/v1","attempt_id":"a","executable":"/bin/true","args":[],"cwd":"/tmp","surprise":true}`))
	if err == nil {
		t.Fatal("expected unknown field to fail")
	}
}

func TestWorkerJobV1RejectsSourceAwareFields(t *testing.T) {
	payload := []byte(`{"schema_version":"exp.worker-job/v1","attempt_id":"att_1","try_id":"try_1","executable":"/bin/true","args":[],"cwd":"/tmp","timeout":"","allowed_env":[],"secret_env":[],"base_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","change_set":[],"expected_outputs":[]}`)
	if _, err := decodeWorkload(payload); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("extended v1 payload error = %v", err)
	}
}

func TestRunnerTerminalizesPreflightFailureBeforeInvocation(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	markerRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(Workload{
		Schema: JobSchema, AttemptID: "att_preflight", Executable: "/bin/true", Args: []string{}, CWD: directory,
		Timeout: "", AllowedEnv: []string{}, SecretEnv: []string{}, RepositoryRoot: directory,
		BaseCommit: strings.Repeat("a", 40), HeadCommit: strings.Repeat("a", 40), ChangeSet: []string{}, ExpectedOutputs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	invoked := false
	store := &fakeStore{}
	runner := Runner{
		Store: store, MarkerRoot: markerRoot,
		Invoker: execx.InvokerFunc(func(context.Context, execx.CommandSpec) (execx.Result, error) {
			invoked = true
			return execx.Result{}, nil
		}),
		Preflight: func(context.Context, Workload) error { return errors.New("changed configuration") },
		Clock:     func() time.Time { return time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC) },
	}
	job := operation.Job{JobInput: operation.JobInput{ID: "job_preflight", SubjectID: "att_preflight", Payload: payload}, State: operation.JobRunning, FencingToken: 3}
	terminal, err := runner.Run(t.Context(), job)
	if err != nil || invoked || terminal.State != operation.JobFailed || terminal.ExitCode != -1 || store.finished != 1 {
		t.Fatalf("preflight terminal = %#v invoked=%v store=%#v err=%v", terminal, invoked, store, err)
	}
	loaded, found, err := LoadTerminal(t.Context(), markerRoot, job.ID)
	if err != nil || !found || loaded.FencingToken != job.FencingToken || loaded.AttemptID != "att_preflight" {
		t.Fatalf("load preflight terminal = %#v found=%v err=%v", loaded, found, err)
	}
}

func TestTerminalBindingRejectsStaleTokenAttemptAndState(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 15, 0, 0, time.UTC)
	terminal := Terminal{Schema: TerminalSchema, JobID: "job_binding", AttemptID: "att_binding", FencingToken: 4, State: operation.JobSucceeded, StartedAt: now, EndedAt: now}
	job := operation.Job{JobInput: operation.JobInput{ID: "job_binding", SubjectID: "att_binding"}, State: operation.JobSucceeded, FencingToken: 4}
	if err := ValidateTerminalForJob(terminal, job, "att_binding"); err != nil {
		t.Fatalf("valid marker binding: %v", err)
	}
	stale := job
	stale.FencingToken++
	if err := ValidateTerminalForJob(terminal, stale, "att_binding"); err == nil {
		t.Fatal("stale fencing token was accepted")
	}
	if err := ValidateTerminalForJob(terminal, job, "att_other"); err == nil {
		t.Fatal("wrong Attempt identity was accepted")
	}
	wrongState := job
	wrongState.State = operation.JobFailed
	if err := ValidateTerminalForJob(terminal, wrongState, "att_binding"); err == nil {
		t.Fatal("terminal job/marker state disagreement was accepted")
	}
}

func TestLoadTerminalPromotesDurableTemporaryMarker(t *testing.T) {
	markerRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jobID := "job_interrupted_marker"
	base := jobArtifactBase(jobID)
	if err := os.WriteFile(filepath.Join(markerRoot, base+".result.json"), nil, 0o400); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 1, 30, 0, 0, time.UTC)
	terminal := Terminal{Schema: TerminalSchema, JobID: jobID, AttemptID: "att_interrupted", FencingToken: 9, State: operation.JobSucceeded, ExitCode: 0, StartedAt: now, EndedAt: now}
	content, err := json.Marshal(terminal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerRoot, base+".json.tmp"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := LoadTerminal(t.Context(), markerRoot, jobID)
	if err != nil || !found || loaded.AttemptID != terminal.AttemptID {
		t.Fatalf("promoted marker = %#v found=%v err=%v", loaded, found, err)
	}
	if _, err := os.Stat(filepath.Join(markerRoot, base+".json")); err != nil {
		t.Fatalf("final marker missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(markerRoot, base+".json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary marker remains: %v", err)
	}
}

func TestLoadTerminalRejectsSymlinkedMarkerRoot(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "markers")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := LoadTerminal(t.Context(), link, "job-test"); err == nil {
		t.Fatal("symlinked marker root was accepted")
	}
}

func TestFormalWorkerV2ReplaysMarkerAfterSourceDisappears(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable path is Unix-specific")
	}
	sourceRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runWorkerGit(t, sourceRoot, "init", "--quiet")
	runWorkerGit(t, sourceRoot, "config", "user.name", "Worker Test")
	runWorkerGit(t, sourceRoot, "config", "user.email", "worker@example.invalid")
	if err := os.WriteFile(filepath.Join(sourceRoot, "input.txt"), []byte("input\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runWorkerGit(t, sourceRoot, "add", "input.txt")
	runWorkerGit(t, sourceRoot, "commit", "--quiet", "-m", "fixture")
	head := strings.TrimSpace(runWorkerGit(t, sourceRoot, "rev-parse", "HEAD"))
	common := strings.TrimSpace(runWorkerGit(t, sourceRoot, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	identity, err := pathx.DirectoryFilesystemIdentity(common)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := research.ParseIDForKind("src_01a09000-0000-7101-8000-000000000101", research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	snapshot := research.SourceSnapshot{
		Source: sourceID, Subdir: ".", PolicyVersion: sourcesnapshot.PolicyVersion,
		CapturedAt: now, GitObjectFormat: research.GitObjectSHA1,
		BaseCommit: head, HeadCommit: head, ChangeSet: []string{},
		State: research.SourceSnapshotClean, Reproducibility: research.ReproducibilityExact,
	}
	snapshot.Digest, err = research.SourceSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	binding := SourceBinding{
		Checkout: "main", RepositoryRoot: sourceRoot, SemanticRoot: sourceRoot,
		RegisteredGitCommonDir: common, RegisteredGitCommonIdentity: identity,
		ReadOnly: false, Snapshot: BindSourceSnapshot(snapshot),
	}
	projectID := "01a01e50-0000-7001-8000-000000000001"
	scope := "scope-formal-worker"
	workload := Workload{
		Schema: SourceJobSchema, AttemptID: "att_01a09000-0000-7202-8000-000000000202",
		ProjectID: projectID, CanonicalScope: scope, ExecutionSource: sourceID.String(),
		Sources: []SourceBinding{binding}, Executable: "/usr/bin/true", Args: []string{},
		CWD: sourceRoot, AllowedEnv: []string{}, SecretEnv: []string{}, RepositoryRoot: sourceRoot,
		OutputRoot: sourceRoot, BaseCommit: head, HeadCommit: head, ChangeSet: []string{}, ExpectedOutputs: []string{},
	}
	workload.CheckoutIdentity = SourceCheckoutIdentity(projectID, scope, sourceID.String(), workload.Sources)
	if err := verifyWorkloadGit(t.Context(), nil, nil, workload); err != nil {
		t.Fatalf("verify formal workload fixture: %v", err)
	}
	payload, err := json.Marshal(workload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeWorkload(payload)
	if err != nil {
		t.Fatalf("decode formal workload fixture: %v", err)
	}
	if err := verifyWorkloadGit(t.Context(), nil, nil, decoded); err != nil {
		t.Fatalf("verify decoded formal workload fixture: %v", err)
	}
	markerRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	runner := Runner{Store: store, MarkerRoot: markerRoot, ProjectID: projectID, CanonicalScope: scope, Clock: func() time.Time { return now }}
	job := operation.Job{
		JobInput: operation.JobInput{ID: "job-formal", SubjectID: workload.AttemptID, CanonicalScope: scope, Payload: payload},
		State:    operation.JobRunning, FencingToken: 11,
	}
	if _, _, err := workloadEnvironment(decoded, job, markerRoot); err != nil {
		t.Fatalf("prepare formal workload environment: %v", err)
	}
	terminal, err := runner.Run(t.Context(), job)
	if err != nil || terminal.Schema != SourceTerminalSchema || terminal.State != operation.JobSucceeded || store.finished != 1 {
		t.Fatalf("first formal run terminal=%#v store=%#v err=%v", terminal, store, err)
	}
	if err := os.RemoveAll(sourceRoot); err != nil {
		t.Fatal(err)
	}
	replayed, err := runner.Run(t.Context(), job)
	if err != nil || replayed.State != operation.JobSucceeded || store.finished != 2 || replayed.JobID != job.ID {
		t.Fatalf("marker replay terminal=%#v store=%#v err=%v", replayed, store, err)
	}
}
