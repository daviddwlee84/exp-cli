package pueue

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

func TestParseStatusStripsEnvironmentAndNormalizesResults(t *testing.T) {
	input := []byte(`{
  "tasks": {
    "1": {"id":1,"group":"gpu","dependencies":[],"priority":7,"label":"exp:p:e1","envs":{"TOKEN":"secret"},"status":{"Running":{"start":"2026-08-30T00:00:00Z"}}},
    "2": {"id":2,"group":"gpu","dependencies":[1],"priority":0,"label":null,"status":{"Done":{"start":"2026-08-30T00:01:00Z","end":"2026-08-30T00:02:00Z","result":{"Failed":42}}}},
    "3": {"id":3,"group":"gpu","dependencies":[],"priority":0,"label":"x","status":{"Done":{"start":"2026-08-30T00:01:00Z","end":"2026-08-30T00:02:00Z","result":"DependencyFailed"}}}
  },
  "groups":{"gpu":{"status":"Running","parallel_tasks":1}}
}`)
	snapshot, err := ParseStatus(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tasks) != 3 || snapshot.Tasks[0].State != StateRunning || snapshot.Tasks[1].State != StateFailed || snapshot.Tasks[2].State != StateDependencyFailed {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Tasks[1].ExitCode == nil || *snapshot.Tasks[1].ExitCode != 42 {
		t.Fatalf("failed task = %#v", snapshot.Tasks[1])
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "envs") {
		t.Fatalf("sensitive environment crossed adapter: %s", encoded)
	}
}

func TestWorkerCommandOnlyAcceptsSafeEnvelopeTokens(t *testing.T) {
	worker := filepath.Join(string(filepath.Separator), "opt", "exp bin", "exp")
	command, err := WorkerCommand(worker, []string{"worker", "run", "--job", "job_123"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "'job_123'") || !strings.Contains(command, "'"+worker+"'") {
		t.Fatalf("command = %q", command)
	}
	if _, err := WorkerCommand(worker, []string{"$(touch /tmp/nope)"}); err == nil {
		t.Fatal("expected shell-bearing token to fail")
	}
}

func TestSubmitBuildsOnlyInternalWorkerEnvelope(t *testing.T) {
	directory := t.TempDir()
	worker := filepath.Join(directory, "exp")
	var captured execx.CommandSpec
	adapter := Adapter{
		LookupBinary: func(string) (string, error) { return filepath.Join(directory, "pueue"), nil },
		Invoker: execx.InvokerFunc(func(_ context.Context, spec execx.CommandSpec) (execx.Result, error) {
			captured = spec
			return execx.Result{Stdout: "17\n", ExitCode: 0, StartedAt: time.Now(), FinishedAt: time.Now()}, nil
		}),
	}
	environment, err := execx.MinimalEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	id, err := adapter.Submit(t.Context(), SubmitRequest{
		Group: "gpu0", Label: "exp:p1:att_123", Priority: 9, WorkingDir: directory,
		Worker: worker, WorkerArgs: []string{"worker", "run", "--job", "job_123"}, Environment: environment,
	})
	if err != nil || id != 17 {
		t.Fatalf("submit id=%d err=%v", id, err)
	}
	if captured.Executable != filepath.Join(directory, "pueue") || len(captured.Argv) == 0 || captured.Argv[0] != "add" {
		t.Fatalf("captured = %#v", captured)
	}
	command := captured.Argv[len(captured.Argv)-1]
	if strings.Contains(command, "$(") || !strings.Contains(command, "job_123") {
		t.Fatalf("worker envelope = %q", command)
	}
}
