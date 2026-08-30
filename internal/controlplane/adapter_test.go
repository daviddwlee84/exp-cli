package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/controller"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/worker"
	"github.com/google/uuid"
)

type canonicalFixture struct {
	repository string
	store      *record.Store
	adapter    Adapter
	poolID     research.ID
	planID     research.ID
	queueID    research.ID
	now        time.Time
}

func TestAdapterPreparesSubmitsAndReconcilesCanonicalAttempt(t *testing.T) {
	fixture := newCanonicalFixture(t, research.AutonomyAssisted)
	pools, err := fixture.adapter.Pools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 1 || pools[0].Name != fixture.poolID.String() || pools[0].Capacity != 2 || pools[0].NativeGroup != "gpu" {
		t.Fatalf("pools = %#v", pools)
	}
	frontier, err := fixture.adapter.Frontier(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(frontier) != 1 || !frontier[0].Configured || !frontier[0].Enabled || frontier[0].Plan != fixture.planID || frontier[0].Queue != fixture.queueID {
		t.Fatalf("frontier = %#v", frontier)
	}
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	if selection.ID != frontier[0].DispatchID || selection.Weight != 3 {
		t.Fatalf("selection = %#v", selection)
	}

	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Job.ID != jobID(selection.ID) || prepared.Job.SubjectID == "" || prepared.Job.Pool != fixture.poolID.String() || prepared.Worker != "/usr/local/bin/exp" || prepared.Label != "study-"+selection.ID {
		t.Fatalf("prepared = %#v", prepared)
	}
	recoveredSelection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil || recoveredSelection.ID != selection.ID {
		t.Fatalf("prepared dispatch recovery frontier = %#v, %v", recoveredSelection, err)
	}
	var workload worker.Workload
	if err := json.Unmarshal(prepared.Job.Payload, &workload); err != nil {
		t.Fatal(err)
	}
	if workload.Schema != worker.JobSchema || workload.AttemptID != prepared.Job.SubjectID || workload.Executable != "/bin/echo" || !reflect.DeepEqual(workload.Args, []string{"train"}) || workload.CWD != fixture.repository || workload.RepositoryRoot != fixture.repository || len(workload.SecretEnv) != 0 || !reflect.DeepEqual(workload.ExpectedOutputs, []string{"results/metrics.json"}) {
		t.Fatalf("workload = %#v", workload)
	}

	inventory := validInventory(t, fixture.store)
	planDocument, _ := inventory.ByID(fixture.planID)
	plan := planDocument.Record.(*research.Plan)
	if plan.State != research.PlanStarted || plan.ResultingExperiment.IsZero() {
		t.Fatalf("started Plan = %#v", plan)
	}
	queueDocument, _ := inventory.ByID(fixture.queueID)
	queue := queueDocument.Record.(*research.Queue)
	if queue.Revision != 2 || len(queue.Partitions) != 2 || queue.Partitions[0].Entries == nil || len(queue.Partitions[0].Entries) != 0 {
		t.Fatalf("dequeued Queue = %#v", queue)
	}
	attemptDocument := findAttemptByDispatch(inventory, selection.ID)
	if attemptDocument == nil {
		t.Fatal("prepared Attempt not found")
	}
	attempt := attemptDocument.Record.(*research.Attempt)
	if attempt.State != research.AttemptPlanned || attempt.Schema != research.SchemaAttemptV2 || attempt.Queue != fixture.queueID || attempt.Pool != fixture.poolID || attempt.QueueRevision != 1 || attempt.BaseCommit == "" || attempt.HeadCommit == "" || !reflect.DeepEqual(attempt.ChangeSet, []string{"train.py"}) {
		t.Fatalf("prepared Attempt = %#v", attempt)
	}
	if len(inventory.OfKind(research.KindExperiment)) != 1 || len(inventory.OfKind(research.KindRun)) != 1 || len(inventory.OfKind(research.KindAttempt)) != 1 {
		t.Fatalf("atomic creation counts: experiments=%d runs=%d attempts=%d", len(inventory.OfKind(research.KindExperiment)), len(inventory.OfKind(research.KindRun)), len(inventory.OfKind(research.KindAttempt)))
	}

	again, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if again.Job.ID != prepared.Job.ID || again.Job.SubjectID != prepared.Job.SubjectID || len(validInventory(t, fixture.store).OfKind(research.KindAttempt)) != 1 {
		t.Fatalf("idempotent prepare = %#v", again)
	}

	job := operationJob(prepared)
	if err := fixture.adapter.Submitted(t.Context(), selection, job, 42); err != nil {
		t.Fatal(err)
	}
	if err := fixture.adapter.Submitted(t.Context(), selection, job, 42); err != nil {
		t.Fatalf("idempotent Submitted: %v", err)
	}
	attempt = findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if attempt.State != research.AttemptQueued || len(attempt.ExternalRefs) != 1 || attempt.ExternalRefs[0].NativeID != "42" {
		t.Fatalf("submitted Attempt = %#v", attempt)
	}

	if err := fixture.adapter.Reconcile(t.Context(), controller.SchedulerSnapshot{Tasks: []controller.SchedulerTask{{ID: 42, Label: prepared.Label, Group: "gpu", State: "running"}}}); err != nil {
		t.Fatal(err)
	}
	attempt = findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if attempt.State != research.AttemptRunning || attempt.Terminal != nil {
		t.Fatalf("running Attempt = %#v", attempt)
	}
	if err := fixture.adapter.Reconcile(t.Context(), controller.SchedulerSnapshot{Tasks: []controller.SchedulerTask{{ID: 42, Label: prepared.Label, Group: "gpu", State: "succeeded"}}}); err != nil {
		t.Fatal(err)
	}
	inventory = validInventory(t, fixture.store)
	attempt = findAttemptByDispatch(inventory, selection.ID).Record.(*research.Attempt)
	if attempt.State != research.AttemptSucceeded || attempt.Terminal == nil || attempt.Terminal.Source != "pueue" {
		t.Fatalf("terminal Attempt = %#v", attempt)
	}
	started := fixture.now.Add(2 * time.Hour)
	ended := started.Add(2 * time.Minute)
	workerTerminal := worker.Terminal{
		Schema: worker.TerminalSchema, JobID: prepared.Job.ID, AttemptID: prepared.Job.SubjectID, FencingToken: 7,
		State: operation.JobSucceeded, ExitCode: 0, StartedAt: started, EndedAt: ended,
		ResultSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResultSize: 12,
		Outputs: map[string]string{"results/metrics.json": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}
	workerResult, _ := json.Marshal(worker.JobResult{Schema: worker.ResultSchema, Result: json.RawMessage(`{"metric":0.9}`), Terminal: workerTerminal})
	taskID := int64(42)
	terminalJob := operation.Job{JobInput: prepared.Job, State: operation.JobSucceeded, FencingToken: 7, PueueTaskID: &taskID, Result: workerResult}
	fixture.now = ended.Add(time.Minute)
	if err := fixture.adapter.Reconcile(t.Context(), controller.SchedulerSnapshot{Jobs: []operation.Job{terminalJob}}); err != nil {
		t.Fatal(err)
	}
	attempt = findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if attempt.State != research.AttemptSucceeded || attempt.Terminal == nil || attempt.Terminal.Source != "direct" || attempt.Terminal.StartedAt == nil || !attempt.Terminal.StartedAt.Equal(started) || !attempt.Terminal.EndedAt.Equal(ended) || attempt.Terminal.ExitCode == nil || *attempt.Terminal.ExitCode != 0 {
		t.Fatalf("worker terminal Attempt = %#v", attempt)
	}
	if attempt.Extensions[attemptExtension]["worker_result_sha256"] != workerTerminal.ResultSHA256 {
		t.Fatalf("worker terminal extension = %#v", attempt.Extensions[attemptExtension])
	}
	experimentDocument, _ := inventory.ByID(plan.ResultingExperiment)
	experiment := experimentDocument.Record.(*research.Experiment)
	if experiment.Lifecycle != research.LifecycleActive || experiment.Conclusion != nil || experiment.Verdict != "" {
		t.Fatalf("scheduler state became scientific authority: %#v", experiment)
	}
}

func TestAdapterRecoversSubmissionByStableLabel(t *testing.T) {
	fixture := newCanonicalFixture(t, research.AutonomyLimited)
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fixture.repository, filepath.FromSlash(DefaultConfigPath))); err != nil {
		t.Fatal(err)
	}
	if err := fixture.adapter.Reconcile(t.Context(), controller.SchedulerSnapshot{Tasks: []controller.SchedulerTask{{ID: 77, Label: prepared.Label, Group: "gpu", State: "queued"}}}); err != nil {
		t.Fatal(err)
	}
	attempt := findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if attempt.State != research.AttemptQueued || len(attempt.ExternalRefs) != 1 || attempt.ExternalRefs[0].NativeID != "77" {
		t.Fatalf("label-recovered Attempt = %#v", attempt)
	}
	if err := fixture.adapter.Reconcile(t.Context(), controller.SchedulerSnapshot{}); err != nil {
		t.Fatal(err)
	}
	attempt = findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if attempt.State != research.AttemptQueued {
		t.Fatalf("missing task inferred terminal state: %#v", attempt)
	}
}

func TestAdapterManualAndShadowExposeButNeverDispatch(t *testing.T) {
	for _, mode := range []research.AutonomyMode{research.AutonomyManual, research.AutonomyShadow} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newCanonicalFixture(t, mode)
			pools, err := fixture.adapter.Pools(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			frontier, err := fixture.adapter.Frontier(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(pools) != 1 || pools[0].Capacity != 0 || len(frontier) != 1 || frontier[0].Enabled {
				t.Fatalf("mode %s pools=%#v frontier=%#v", mode, pools, frontier)
			}
			if _, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit)); !errors.Is(err, controller.ErrNoWork) {
				t.Fatalf("mode %s Next error = %v", mode, err)
			}
			if len(validInventory(t, fixture.store).OfKind(research.KindAttempt)) != 0 {
				t.Fatal("non-autonomous mode created an Attempt")
			}
		})
	}
}

func TestDispatchRejectsRuntimeGitIdentityDrift(t *testing.T) {
	fixture := newCanonicalFixture(t, research.AutonomyAssisted)
	if err := os.WriteFile(filepath.Join(fixture.repository, "train.py"), []byte("print('dirty')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit)); err == nil || !strings.Contains(err.Error(), "uncommitted executable-tree") {
		t.Fatalf("dirty runtime error = %v", err)
	}
}

func TestDispatchIgnoresDroppedRuntimeGitDrift(t *testing.T) {
	fixture := newCanonicalFixture(t, research.AutonomyAssisted)
	droppedID := mustID(t, "plan_01a01e60-0000-7003-8000-000000000099", research.KindPlan)
	inventory := validInventory(t, fixture.store)
	queuedDocument, err := inventory.ByID(fixture.planID)
	if err != nil {
		t.Fatal(err)
	}
	dropped := research.Clone(queuedDocument.Record).(*research.Plan)
	dropped.ID = droppedID
	dropped.Title = "Historical dropped plan"
	dropped.State = research.PlanDropped
	dropped.ResultingExperiment = research.ID{}
	if _, err := fixture.store.Transact(t.Context(), record.TransactionRequest{Operation: "fixture.dropped-plan", Changes: []record.TransactionChange{{
		Operation: record.TransactionCreate, Document: &record.Document{Record: dropped, Body: "# Historical dropped plan\n"},
	}}}); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(fixture.repository, filepath.FromSlash(DefaultConfigPath))
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config RuntimeConfig
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	stale := config.Plans[fixture.planID.String()]
	stale.HeadCommit = strings.Repeat("f", 40)
	config.Plans[droppedID.String()] = stale
	content, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit)); err != nil {
		t.Fatalf("stale dropped runtime blocked live frontier: %v", err)
	}
}

func TestPreparedAttemptStillVerifiesRuntimeGitIdentity(t *testing.T) {
	fixture := newCanonicalFixture(t, research.AutonomyAssisted)
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.adapter.Prepare(t.Context(), selection); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.repository, "train.py"), []byte("print('dirty after prepare')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit)); err == nil || !strings.Contains(err.Error(), "uncommitted executable-tree") {
		t.Fatalf("prepared Attempt runtime drift error = %v", err)
	}
}

func TestDispatchCanUseRegisteredExperimentWorktreeByHead(t *testing.T) {
	fixture := newCanonicalFixture(t, research.AutonomyAssisted)
	configPath := filepath.Join(fixture.repository, filepath.FromSlash(DefaultConfigPath))
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config RuntimeConfig
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	plan := config.Plans[fixture.planID.String()]
	plan.Checkout = "registered_worktree"
	config.Plans[fixture.planID.String()] = plan
	content, _ = json.Marshal(config)
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(worktreeParent, "candidate-worktree")
	runGit(t, fixture.repository, "worktree", "add", "--detach", worktree, plan.HeadCommit)
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	var workload worker.Workload
	if err := json.Unmarshal(prepared.Job.Payload, &workload); err != nil {
		t.Fatal(err)
	}
	if workload.RepositoryRoot != worktree || workload.CWD != worktree {
		t.Fatalf("registered worktree workload = %#v", workload)
	}
}

func newCanonicalFixture(t *testing.T, autonomy research.AutonomyMode) canonicalFixture {
	t.Helper()
	repository := canonicalTemp(t)
	runGit(t, repository, "init", "--quiet")
	runGit(t, repository, "config", "user.email", "test@example.com")
	runGit(t, repository, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repository, "train.py"), []byte("print('baseline')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "train.py")
	runGit(t, repository, "commit", "--quiet", "-m", "baseline")
	baseCommit := runGit(t, repository, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repository, "train.py"), []byte("print('train')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "train.py")
	runGit(t, repository, "commit", "--quiet", "-m", "candidate")
	headCommit := runGit(t, repository, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(repository, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	projectID, err := research.ParseUUID("01a01e50-0000-7001-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	project := &research.Project{Schema: research.SchemaProject, ProjectID: projectID, Name: "Controller Test", CreatedAt: now, ExperimentsRoot: "."}
	writeDocument(t, filepath.Join(repository, "experiments", record.ProjectFile), &record.Document{Record: project, Body: "# Controller Test\n"})

	store := record.NewStore(filepath.Join(repository, "experiments"), filepath.Join(repository, ".git"), record.WithClock(func() time.Time { return now }))
	poolID := mustID(t, "pool_01a01e60-0000-7002-8000-000000000002", research.KindResourcePool)
	planID := mustID(t, "plan_01a01e60-0000-7003-8000-000000000003", research.KindPlan)
	queueID := mustID(t, "queue_01a01e60-0000-7004-8000-000000000004", research.KindQueue)
	policy := &research.Policy{
		Schema: research.SchemaPolicy, CreatedAt: now, UpdatedAt: now, Autonomy: autonomy,
		ExploitShare: .8, ExploreShare: .2, ScoreFormula: "utility-v1", TiePolicy: research.QueueTieKeepIncumbent,
		PromotionRequiresHuman: true,
		Taxonomy:               research.ClassificationTaxonomy{Domains: []string{"ml"}, Work: []string{"training"}, Methods: []string{"ablation"}, Components: []string{"encoder"}},
		ClusterSaturation:      research.ClusterSaturationPolicy{BudgetHours: 24, PlateauWindow: 5, MinimumImprovement: .01, MinimumProbability: .1},
	}
	pool := &research.ResourcePool{
		Common:  research.Common{Schema: research.SchemaResourcePool, ID: poolID, Title: "GPU", CreatedAt: now, UpdatedAt: now},
		Enabled: true, Capacity: 2, Unit: "gpu", Bottleneck: "gpu",
	}
	plan := &research.Plan{
		Common:   research.Common{Schema: research.SchemaPlanV2, ID: planID, Title: "Tune encoder", CreatedAt: now, UpdatedAt: now, Tags: []string{"encoder"}},
		Priority: research.PriorityP1, Effort: research.EffortS, State: research.PlanQueued,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve held-out score", Metric: "macro_f1", Unit: "score"},
		PrimaryCluster: "encoder", Classification: &research.Classification{
			Domain: "ml", Work: "training", Method: "ablation", Component: "encoder",
			Lane: research.LaneExploit, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman,
		},
		Resources: []research.ResourceNeed{{Pool: poolID, Units: 1, EstimatedHours: 3}},
		Utility:   &research.UtilityEstimate{Probability: .7, Impact: .1, InformationGain: .2, UnblockValue: .1, RiskPenalty: .05},
	}
	if _, err := store.Transact(t.Context(), record.TransactionRequest{Operation: "fixture.bootstrap", Changes: []record.TransactionChange{
		{Operation: record.TransactionCreate, Document: &record.Document{Record: policy, Body: "# Policy\n"}},
		{Operation: record.TransactionCreate, Document: &record.Document{Record: pool, Body: "# GPU\n"}},
		{Operation: record.TransactionCreate, Document: &record.Document{Record: plan, Body: "# Tune encoder\n"}},
	}}); err != nil {
		t.Fatal(err)
	}
	inventory := validInventory(t, store)
	planDocument, _ := inventory.ByID(planID)
	queue := &research.Queue{
		Common:   research.Common{Schema: research.SchemaQueue, ID: queueID, Title: "Main queue", CreatedAt: now, UpdatedAt: now},
		Revision: 1, Partitions: []research.QueuePartition{{Pool: poolID, Lane: research.LaneExploit, Entries: []research.QueueEntry{{
			Plan: planID, PlanRevision: planDocument.Revision, Score: 10, InsertedAt: now,
		}}}, {Pool: poolID, Lane: research.LaneExplore, Entries: []research.QueueEntry{}}},
	}
	if _, err := store.Transact(t.Context(), record.TransactionRequest{Operation: "fixture.queue", Changes: []record.TransactionChange{{
		Operation: record.TransactionCreate, Document: &record.Document{Record: queue, Body: "# Main queue\n"},
	}}}); err != nil {
		t.Fatal(err)
	}

	runtime := RuntimeConfig{
		Schema: RuntimeSchema,
		Pools:  map[string]PoolRuntime{poolID.String(): {PueueGroup: "gpu", LabelPrefix: "study-"}},
		Plans: map[string]PlanRuntime{planID.String(): {
			Executable: "/bin/echo", Argv: []string{"train"}, CWD: ".", Timeout: "5m",
			AllowedEnv: []string{"PATH"}, SecretEnv: []string{},
			BaseCommit: baseCommit, HeadCommit: headCommit,
			ChangeSet: []string{"train.py"}, ExpectedOutputs: []string{"results/metrics.json"},
		}},
	}
	configBytes, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, ".exp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(DefaultConfigPath)), configBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	generated := []string{
		"01a01e67-e340-7303-8000-000000000303",
		"01a01e68-cda0-7404-8000-000000000404",
		"01a01e69-b800-7505-8000-000000000505",
	}
	index := 0
	adapter := Adapter{
		Store: store, RepositoryRoot: repository, WorkerExecutable: "/usr/local/bin/exp", WorkerArgs: []string{"worker", "run", "--job"},
		Clock: func() time.Time { return now.Add(time.Hour) },
		GenerateUUID: func(time.Time) (uuid.UUID, error) {
			if index >= len(generated) {
				return uuid.Nil, errors.New("test UUID sequence exhausted")
			}
			value := uuid.MustParse(generated[index])
			index++
			return value, nil
		},
	}
	return canonicalFixture{repository: repository, store: store, adapter: adapter, poolID: poolID, planID: planID, queueID: queueID, now: now}
}

func operationJob(prepared controller.Prepared) operation.Job {
	return operation.Job{JobInput: prepared.Job}
}

func validInventory(t *testing.T, store *record.Store) *record.Inventory {
	t.Helper()
	inventory, err := store.Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Valid() {
		t.Fatalf("invalid inventory: %v", inventory.Error())
	}
	return inventory
}

func writeDocument(t *testing.T, path string, document *record.Document) {
	t.Helper()
	content, err := record.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustID(t *testing.T, value string, kind research.Kind) research.ID {
	t.Helper()
	id, err := research.ParseIDForKind(value, kind)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func canonicalTemp(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(context.Background(), "git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	} else {
		return strings.TrimSpace(string(output))
	}
	return ""
}
