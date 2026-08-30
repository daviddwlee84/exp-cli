package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/worker"
)

func TestDaemonStatusPauseResume(t *testing.T) {
	repository := newGitRepository(t)
	app := NewApp(t.Context(), nil, nil, nil)
	requireCommandSuccess(t, invokeCommand(t, app, "", "--start-dir", repository, "init", "--name", "Daemon Test", "--json"))

	missing := invokeCommand(t, app, "", "--start-dir", repository, "daemon", "status", "--json")
	requireCommandSuccess(t, missing)
	var before daemonStatusData
	decodeData(t, decodeEnvelope(t, missing.stdout), &before)
	if before.Initialized || before.Jobs == nil {
		t.Fatalf("before = %#v", before)
	}

	paused := invokeCommand(t, app, "", "--start-dir", repository, "daemon", "pause", "--reason", "maintenance", "--json")
	requireCommandSuccess(t, paused)
	status := invokeCommand(t, app, "", "--start-dir", repository, "daemon", "status", "--json")
	requireCommandSuccess(t, status)
	var after daemonStatusData
	decodeData(t, decodeEnvelope(t, status.stdout), &after)
	if !after.Initialized || !after.Runtime.Paused || after.Runtime.Reason != "maintenance" {
		t.Fatalf("after = %#v", after)
	}
	resumed := invokeCommand(t, app, "", "--start-dir", repository, "daemon", "resume", "--json")
	requireCommandSuccess(t, resumed)
}

func TestAgentProfilesAndRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	directory := t.TempDir()
	executable := filepath.Join(directory, "fake-agent")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nIFS= read -r prompt\nprintf '%s' '{\"ok\":true,\"prompt\":\"received\"}' > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "agents.toml")
	config := `schema = "exp.agents/v1"
[roles]
ranker = "fake"
[profiles.fake]
executable = "fake-agent"
args = ["{output_file}"]
timeout = "30s"
output = "output_file_json"
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(directory, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp(t.Context(), nil, nil, nil)
	app.BinaryLookup = func(name string) (string, error) { return executable, nil }
	profiles := invokeCommand(t, app, "", "agent", "profiles", "--config", configPath, "--json")
	requireCommandSuccess(t, profiles)
	run := invokeCommand(t, app, "rank queue\n", "agent", "run", "--config", configPath, "--role", "ranker", "--schema", schemaPath, "--prompt", "-", "--cwd", directory, "--json")
	requireCommandSuccess(t, run)
	var data agentRunData
	decodeData(t, decodeEnvelope(t, run.stdout), &data)
	if data.Profile != "fake" || !strings.Contains(string(data.Output), `"ok":true`) {
		t.Fatalf("agent run = %#v", data)
	}
}

func TestCanonicalIdeaQualificationAndQueueInsertion(t *testing.T) {
	repository := newGitRepository(t)
	app := NewApp(t.Context(), nil, nil, nil)
	requireCommandSuccess(t, invokeCommand(t, app, "", "--start-dir", repository, "init", "--name", "Control Plane", "--json"))
	requireCommandSuccess(t, invokeCommand(t, app, "", "--start-dir", repository, "policy", "init", "--json"))

	poolInvocation := invokeCommand(t, app, "", "--start-dir", repository, "pool", "add", "--title", "GPU", "--unit", "gpu", "--bottleneck", "gpu", "--json")
	requireCommandSuccess(t, poolInvocation)
	var poolData struct {
		Pool canonicalRecordView `json:"pool"`
	}
	decodeData(t, decodeEnvelope(t, poolInvocation.stdout), &poolData)

	queueInvocation := invokeCommand(t, app, "", "--start-dir", repository, "queue", "create", "--pool", poolData.Pool.ID, "--json")
	requireCommandSuccess(t, queueInvocation)
	var queueData struct {
		Queue canonicalRecordView `json:"queue"`
	}
	decodeData(t, decodeEnvelope(t, queueInvocation.stdout), &queueData)

	ideaInvocation := invokeCommand(t, app, "", "--start-dir", repository, "idea", "add",
		"--title", "Try a better encoder", "--summary", "Ablate encoder depth", "--lane", "exploit", "--json")
	requireCommandSuccess(t, ideaInvocation)
	var createdIdea ideaData
	decodeData(t, decodeEnvelope(t, ideaInvocation.stdout), &createdIdea)

	qualifiedInvocation := invokeCommand(t, app, "", "--start-dir", repository, "idea", "qualify", createdIdea.Idea.ID,
		"--payoff-summary", "Improve score", "--payoff-metric", "score", "--payoff-unit", "point",
		"--resource", poolData.Pool.ID+":1:2", "--probability", "0.6", "--impact", "1.5", "--json")
	requireCommandSuccess(t, qualifiedInvocation)
	var qualified ideaData
	decodeData(t, decodeEnvelope(t, qualifiedInvocation.stdout), &qualified)
	if qualified.Plan.ID == "" {
		t.Fatalf("qualified data = %#v", qualified)
	}

	inserted := invokeCommand(t, app, "", "--start-dir", repository, "queue", "insert", queueData.Queue.ID, qualified.Plan.ID,
		"--pool", poolData.Pool.ID, "--json")
	requireCommandSuccess(t, inserted)
	var insertData queueInsertData
	decodeData(t, decodeEnvelope(t, inserted.stdout), &insertData)
	if !insertData.Applied || insertData.Position != 0 {
		t.Fatalf("insert data = %#v", insertData)
	}

	shown := invokeCommand(t, app, "", "--start-dir", repository, "queue", "show", queueData.Queue.ID, "--json")
	requireCommandSuccess(t, shown)
	var view queueView
	decodeData(t, decodeEnvelope(t, shown.stdout), &view)
	entries := 0
	for _, partition := range view.Partitions {
		entries += len(partition.Entries)
	}
	if entries != 1 || view.Revision != 2 {
		t.Fatalf("queue view = %#v", view)
	}

	info, err := project.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	canonicalStore := record.NewStore(info.Root, info.Repository.GitCommonDir)
	inventory, err := canonicalStore.Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := research.ParseID(qualified.Plan.ID)
	planDocument, err := inventory.ByID(planID)
	if err != nil {
		t.Fatal(err)
	}
	planDocument.Body += "\nManual clarification that changes the normalized revision.\n"
	encoded, err := record.Encode(planDocument)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info.Root, filepath.FromSlash(planDocument.Path)), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err := canonicalStore.Inventory(t.Context())
	if err != nil || stale.Valid() {
		t.Fatalf("expected queue pin staleness, inventory=%#v err=%v", stale.Diagnostics, err)
	}
	refreshed := invokeCommand(t, app, "", "--start-dir", repository, "plan", "refresh", qualified.Plan.ID,
		"--probability", "0.5", "--impact", "0.2", "--information-gain", "0.3", "--unblock-value", "0.1", "--risk-penalty", "0.05", "--json")
	requireCommandSuccess(t, refreshed)
	validated := invokeCommand(t, app, "", "--start-dir", repository, "validate", "--json")
	requireCommandSuccess(t, validated)
}

func TestWorkerFailurePersistsTerminalAndReturnsCommandError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/bin/false fixture is Unix-only")
	}
	repository := newGitRepository(t)
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("worker fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"config", "user.name", "Worker Test"}, {"config", "user.email", "worker@example.invalid"}, {"add", "README.md"}, {"commit", "--quiet", "-m", "fixture"}} {
		command := exec.Command("git", arguments...)
		command.Dir = repository
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
	}
	app := NewApp(t.Context(), nil, nil, nil)
	requireCommandSuccess(t, invokeCommand(t, app, "", "--start-dir", repository, "init", "--name", "Worker failure", "--json"))
	info, err := project.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	store, err := operation.Open(t.Context(), info.Repository.GitCommonDir)
	if err != nil {
		t.Fatal(err)
	}
	headCommand := exec.Command("git", "rev-parse", "HEAD")
	headCommand.Dir = repository
	headBytes, err := headCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	payload, _ := json.Marshal(worker.Workload{Schema: worker.JobSchema, AttemptID: "att_test", Executable: "/bin/false", CWD: repository, RepositoryRoot: repository, BaseCommit: head, HeadCommit: head, ChangeSet: []string{}, ExpectedOutputs: []string{}})
	job, _, err := store.EnqueueJob(t.Context(), operation.JobInput{ID: "job-worker-failure", IdempotencyKey: "worker-failure", Kind: "run", Role: "execute", SubjectID: "att_test", Pool: "cpu", Lane: "exploit", Profile: "worker", Payload: payload, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.ClaimJobByID(t.Context(), job.ID, "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	invocation := invokeCommand(t, app, "", "--start-dir", repository, "worker", "run", "--job", job.ID, "--fencing-token", "1")
	if invocation.err == nil || !strings.Contains(invocation.err.Error(), "workload completed with state failed") || !strings.Contains(invocation.stdout, `"state":"failed"`) {
		t.Fatalf("worker invocation stdout=%q stderr=%q err=%v", invocation.stdout, invocation.stderr, invocation.err)
	}
	store, err = operation.Open(t.Context(), info.Repository.GitCommonDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	finished, err := store.GetJob(t.Context(), job.ID)
	if err != nil || finished.State != operation.JobFailed {
		t.Fatalf("finished job=%#v err=%v", finished, err)
	}
}
