package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/operation"
)

type fakeStore struct {
	finished int
	state    operation.JobState
	result   json.RawMessage
}

func (store *fakeStore) FinishJob(_ context.Context, _ string, _ int64, state operation.JobState, result json.RawMessage, _ string) (operation.Job, error) {
	store.finished++
	store.state = state
	store.result = append(json.RawMessage(nil), result...)
	return operation.Job{State: state}, nil
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

func TestDecodeWorkloadRejectsUnknownFields(t *testing.T) {
	_, err := decodeWorkload([]byte(`{"schema_version":"exp.worker-job/v1","attempt_id":"a","executable":"/bin/true","args":[],"cwd":"/tmp","surprise":true}`))
	if err == nil {
		t.Fatal("expected unknown field to fail")
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
