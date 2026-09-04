package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/controller"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/lifecycle"
	"github.com/daviddwlee84/exp-cli/internal/mlflow"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/trust"
	"github.com/daviddwlee84/exp-cli/internal/worker"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
	"github.com/google/uuid"
)

type runtimeTrustAll struct{}

func (runtimeTrustAll) Check(context.Context, trust.Subject, string, trust.Capability) (bool, error) {
	return true, nil
}

type runtimeConfigLoaderFunc func(context.Context, config.Request) (*config.Result, error)

func (load runtimeConfigLoaderFunc) Load(ctx context.Context, request config.Request) (*config.Result, error) {
	return load(ctx, request)
}

func TestRuntimeV2PreparesFormalAttemptAcrossRepositories(t *testing.T) {
	fixture, sourceID, sourceRoot, base, head := newRuntimeV2Fixture(t, false)
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.CWD != fixture.repository || !prepared.QuotedWorkerArgs || prepared.Job.Profile != "source-runtime-v2" {
		t.Fatalf("prepared runtime v2 = %#v", prepared)
	}
	for _, expected := range []string{"--canonical-root", fixture.repository, "--project", "--scope"} {
		if !containsString(prepared.Args, expected) {
			t.Fatalf("worker args omit %q: %#v", expected, prepared.Args)
		}
	}
	var workload worker.Workload
	if err := json.Unmarshal(prepared.Job.Payload, &workload); err != nil {
		t.Fatal(err)
	}
	if workload.Schema != worker.SourceJobSchema || workload.ProjectID == "" || workload.CanonicalScope != prepared.Job.CanonicalScope ||
		workload.ExecutionSource != sourceID.String() || workload.RepositoryRoot != sourceRoot || workload.OutputRoot != filepath.Join(sourceRoot, "pkg") ||
		workload.CWD != filepath.Join(sourceRoot, "pkg") || len(workload.Sources) != 1 || workload.Sources[0].Snapshot.Digest == "" ||
		workload.Sources[0].Snapshot.BaseCommit != base || workload.Sources[0].Snapshot.HeadCommit != head ||
		!reflect.DeepEqual(workload.Sources[0].Snapshot.ChangeSet, []string{"pkg/train.py"}) || workload.CheckoutIdentity == "" {
		t.Fatalf("formal worker workload = %#v", workload)
	}
	if got := worker.SourceCheckoutIdentity(workload.ProjectID, workload.CanonicalScope, workload.ExecutionSource, workload.Sources); got != workload.CheckoutIdentity {
		t.Fatalf("checkout identity = %s, want %s", workload.CheckoutIdentity, got)
	}

	inventory := validInventory(t, fixture.store)
	document := findAttemptByDispatch(inventory, selection.ID)
	if document == nil {
		t.Fatal("Source-aware Attempt was not created")
	}
	attempt := document.Record.(*research.Attempt)
	if attempt.Schema != research.SchemaAttemptV3 || attempt.Run.IsZero() || !attempt.Try.IsZero() || attempt.ExecutionSource != sourceID ||
		len(attempt.SourceSnapshots) != 1 || attempt.SourceSnapshots[0].State != research.SourceSnapshotClean ||
		attempt.SourceSnapshots[0].BaseCommit != base || attempt.SourceSnapshots[0].HeadCommit != head ||
		attempt.Provenance == nil || attempt.Provenance.GitCommit != head || attempt.Provenance.GitDirty ||
		attempt.Provenance.ConfigDigest == "" || attempt.BaseCommit != "" || attempt.HeadCommit != "" || attempt.ChangeSet != nil {
		t.Fatalf("formal Attempt = %#v", attempt)
	}
	runDocument, err := inventory.ByID(attempt.Run)
	if err != nil {
		t.Fatal(err)
	}
	run := runDocument.Record.(*research.Run)
	if run.ConfigDigest != attempt.Provenance.ConfigDigest {
		t.Fatalf("Run/Attempt config digest mismatch: %#v %#v", run, attempt.Provenance)
	}
}

func TestRuntimeV2RequiresExplicitObservationalNoChange(t *testing.T) {
	fixture, _, _, _, head := newRuntimeV2Fixture(t, true)
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	attempt := findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if len(attempt.SourceSnapshots) != 1 || attempt.SourceSnapshots[0].BaseCommit != head || attempt.SourceSnapshots[0].HeadCommit != head || len(attempt.SourceSnapshots[0].ChangeSet) != 0 {
		t.Fatalf("observational snapshot = %#v; prepared=%#v", attempt.SourceSnapshots, prepared)
	}

	configPath := filepath.Join(fixture.repository, filepath.FromSlash(DefaultConfigPath))
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var runtime RuntimeConfigV2
	if err := json.Unmarshal(content, &runtime); err != nil {
		t.Fatal(err)
	}
	for key, route := range runtime.Plans {
		route.ObservationalNoChange = false
		runtime.Plans[key] = route
	}
	content, _ = json.Marshal(runtime)
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntimeV2(t.Context(), fixture.repository, DefaultConfigPath); err == nil || !strings.Contains(err.Error(), "explicit observational_no_change") {
		t.Fatalf("implicit empty change_set error = %v", err)
	}
}

func TestRuntimeV2SelectsRegisteredSourceWorktree(t *testing.T) {
	fixture, _, sourceRoot, _, head := newRuntimeV2Fixture(t, false)
	linkedParent := canonicalTemp(t)
	linked := filepath.Join(linkedParent, "registered source worktree")
	runGit(t, sourceRoot, "worktree", "add", "--detach", linked, head)
	updateRuntimeV2Route(t, fixture, func(route PlanRuntimeV2) PlanRuntimeV2 {
		route.Checkout = CheckoutRegisteredWorktree
		return route
	})
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
	if len(workload.Sources) != 1 || workload.Sources[0].Checkout != CheckoutRegisteredWorktree || workload.Sources[0].RepositoryRoot != linked || workload.CWD != filepath.Join(linked, "pkg") {
		t.Fatalf("registered Source worktree payload = %#v", workload)
	}
}

func TestRuntimeV2CreatesAndRecoversManagedSourceWorktree(t *testing.T) {
	fixture, _, sourceRoot, _, _ := newRuntimeV2Fixture(t, false)
	dataHome := canonicalTemp(t)
	fixture.adapter.Workspace = workspacebackend.NativeGit{DataHome: dataHome}
	updateRuntimeV2Route(t, fixture, func(route PlanRuntimeV2) PlanRuntimeV2 {
		route.Checkout = CheckoutManagedWorktree
		return route
	})
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	var first worker.Workload
	if err := json.Unmarshal(prepared.Job.Payload, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Sources) != 1 || first.Sources[0].Checkout != CheckoutManagedWorktree || first.Sources[0].RepositoryRoot == sourceRoot || !strings.HasPrefix(first.Sources[0].RepositoryRoot, dataHome+string(filepath.Separator)) {
		t.Fatalf("managed Source workload = %#v", first)
	}
	if _, err := os.Stat(first.Sources[0].RepositoryRoot); err != nil {
		t.Fatalf("managed Source worktree missing: %v", err)
	}
	again, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Job, prepared.Job) || again.CWD != prepared.CWD {
		t.Fatalf("managed Source recovery drifted: first=%#v again=%#v", prepared, again)
	}
	if _, err := os.Stat(first.Sources[0].RepositoryRoot); err != nil {
		t.Fatalf("managed Source worktree was implicitly removed: %v", err)
	}
}

type failingManagedCapture struct {
	delegate sourcesnapshot.Capturer
	prefix   string
	err      error
}

func (capture failingManagedCapture) CaptureClean(ctx context.Context, request sourcesnapshot.Request) (research.SourceSnapshot, error) {
	if strings.HasPrefix(request.RepositoryRoot, capture.prefix+string(filepath.Separator)) {
		return research.SourceSnapshot{}, capture.err
	}
	return capture.delegate.CaptureClean(ctx, request)
}

type failingDispatchStore struct {
	Store
	err error
}

func (store failingDispatchStore) Transact(ctx context.Context, request record.TransactionRequest) (*record.TransactionResult, error) {
	if request.Operation == "dispatch.prepare" {
		return nil, store.err
	}
	return store.Store.Transact(ctx, request)
}

func TestRuntimeV2RollsBackUnpublishedManagedWorktree(t *testing.T) {
	for _, boundary := range []string{"recapture", "transaction"} {
		t.Run(boundary, func(t *testing.T) {
			fixture, _, sourceRoot, _, _ := newRuntimeV2Fixture(t, false)
			dataHome := canonicalTemp(t)
			fixture.adapter.Workspace = workspacebackend.NativeGit{DataHome: dataHome}
			updateRuntimeV2Route(t, fixture, func(route PlanRuntimeV2) PlanRuntimeV2 {
				route.Checkout = CheckoutManagedWorktree
				return route
			})
			selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected post-workspace " + boundary + " failure")
			switch boundary {
			case "recapture":
				fixture.adapter.SourceCapturer = failingManagedCapture{delegate: sourcesnapshot.Capturer{}, prefix: dataHome, err: injected}
			case "transaction":
				fixture.adapter.Store = failingDispatchStore{Store: fixture.store, err: injected}
			}
			if _, err := fixture.adapter.Prepare(t.Context(), selection); !errors.Is(err, injected) {
				t.Fatalf("Prepare error = %v", err)
			}
			worktrees := runGit(t, sourceRoot, "worktree", "list", "--porcelain")
			branches := runGit(t, sourceRoot, "branch", "--list", "exp/*")
			if strings.Contains(worktrees, dataHome) || strings.TrimSpace(branches) != "" {
				t.Fatalf("unpublished managed workspace survived: worktrees=%q branches=%q", worktrees, branches)
			}
			if attempts := validInventory(t, fixture.store).OfKind(research.KindAttempt); len(attempts) != 0 {
				t.Fatalf("failed preparation published Attempts: %d", len(attempts))
			}
		})
	}
}

type failRepeatedRootCapture struct {
	delegate sourcesnapshot.Capturer
	root     string
	seen     int
	err      error
}

func (capture *failRepeatedRootCapture) CaptureClean(ctx context.Context, request sourcesnapshot.Request) (research.SourceSnapshot, error) {
	if request.RepositoryRoot == capture.root {
		capture.seen++
		if capture.seen > 1 {
			return research.SourceSnapshot{}, capture.err
		}
	}
	return capture.delegate.CaptureClean(ctx, request)
}

func TestRuntimeV2RollsBackManagedWorktreeWhenLaterSourceFails(t *testing.T) {
	fixture, _, sourceRoot, _, _ := newRuntimeV2Fixture(t, false)
	dataHome := canonicalTemp(t)
	fixture.adapter.Workspace = workspacebackend.NativeGit{DataHome: dataHome}
	readOnlyRoot := canonicalTemp(t)
	runGit(t, readOnlyRoot, "init", "--quiet")
	runGit(t, readOnlyRoot, "config", "user.email", "later@example.invalid")
	runGit(t, readOnlyRoot, "config", "user.name", "Later Source")
	if err := os.WriteFile(filepath.Join(readOnlyRoot, "data.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, readOnlyRoot, "add", "data.txt")
	runGit(t, readOnlyRoot, "commit", "--quiet", "-m", "later source")
	head := runGit(t, readOnlyRoot, "rev-parse", "HEAD")
	readOnlyID := mustID(t, "src_01a09000-0000-7101-8000-000000000112", research.KindSource)
	readOnlySource := &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: readOnlyID, Title: "Later source", CreatedAt: fixture.now, UpdatedAt: fixture.now},
		Key:    "later-source", Kind: research.SourceGit, Subdir: ".", LocatorHints: []string{}, State: research.SourceActive,
	}
	if _, err := fixture.store.Transact(t.Context(), record.TransactionRequest{Operation: "fixture.later-source", Changes: []record.TransactionChange{{
		Operation: record.TransactionCreate, Document: &record.Document{Record: readOnlySource, Body: "# Later source\n"},
	}}}); err != nil {
		t.Fatal(err)
	}
	info, err := project.Discover(t.Context(), fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.associations.RegisterSource(t.Context(), info.Project().ProjectID, readOnlyID, readOnlyRoot); err != nil {
		t.Fatal(err)
	}
	updateRuntimeV2Route(t, fixture, func(route PlanRuntimeV2) PlanRuntimeV2 {
		route.Checkout = CheckoutManagedWorktree
		route.ReadOnlySources = []ReadOnlySourceRuntime{{
			Source: readOnlyID.String(), Checkout: CheckoutMain, BaseCommit: head, HeadCommit: head,
			ChangeSet: []string{}, ObservationalNoChange: true,
		}}
		return route
	})
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected later Source failure")
	fixture.adapter.SourceCapturer = &failRepeatedRootCapture{delegate: sourcesnapshot.Capturer{}, root: readOnlyRoot, err: injected}
	if _, err := fixture.adapter.Prepare(t.Context(), selection); !errors.Is(err, injected) {
		t.Fatalf("Prepare error = %v", err)
	}
	worktrees := runGit(t, sourceRoot, "worktree", "list", "--porcelain")
	branches := runGit(t, sourceRoot, "branch", "--list", "exp/*")
	if strings.Contains(worktrees, dataHome) || strings.TrimSpace(branches) != "" {
		t.Fatalf("later Source failure orphaned workspace: worktrees=%q branches=%q", worktrees, branches)
	}
}

func TestRuntimeV2CapturesOptionalReadOnlySources(t *testing.T) {
	fixture, primaryID, _, _, _ := newRuntimeV2Fixture(t, false)
	readOnlyRoot := canonicalTemp(t)
	runGit(t, readOnlyRoot, "init", "--quiet")
	runGit(t, readOnlyRoot, "config", "user.email", "readonly@example.invalid")
	runGit(t, readOnlyRoot, "config", "user.name", "Read Only")
	if err := os.WriteFile(filepath.Join(readOnlyRoot, "data.txt"), []byte("fixed data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, readOnlyRoot, "add", "data.txt")
	runGit(t, readOnlyRoot, "commit", "--quiet", "-m", "read-only source")
	head := runGit(t, readOnlyRoot, "rev-parse", "HEAD")
	readOnlyID := mustID(t, "src_01a09000-0000-7101-8000-000000000102", research.KindSource)
	readOnlySource := &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: readOnlyID, Title: "Read-only data", CreatedAt: fixture.now, UpdatedAt: fixture.now},
		Key:    "read-only-data", Kind: research.SourceGit, Subdir: ".", LocatorHints: []string{}, State: research.SourceActive,
	}
	if _, err := fixture.store.Transact(t.Context(), record.TransactionRequest{Operation: "fixture.read-only-source", Changes: []record.TransactionChange{{
		Operation: record.TransactionCreate, Document: &record.Document{Record: readOnlySource, Body: "# Read-only data\n"},
	}}}); err != nil {
		t.Fatal(err)
	}
	info, err := project.Discover(t.Context(), fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.associations.RegisterSource(t.Context(), info.Project().ProjectID, readOnlyID, readOnlyRoot); err != nil {
		t.Fatal(err)
	}
	updateRuntimeV2Route(t, fixture, func(route PlanRuntimeV2) PlanRuntimeV2 {
		route.ReadOnlySources = []ReadOnlySourceRuntime{{
			Source: readOnlyID.String(), Checkout: CheckoutMain, BaseCommit: head, HeadCommit: head,
			ChangeSet: []string{}, ObservationalNoChange: true,
		}}
		return route
	})
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
	if len(workload.Sources) != 2 || workload.ExecutionSource != primaryID.String() {
		t.Fatalf("multi-Source workload = %#v", workload)
	}
	for _, binding := range workload.Sources {
		if binding.Snapshot.Source == primaryID.String() && binding.ReadOnly {
			t.Fatal("execution Source was marked read-only")
		}
		if binding.Snapshot.Source == readOnlyID.String() && (!binding.ReadOnly || binding.RepositoryRoot != readOnlyRoot) {
			t.Fatalf("read-only Source binding = %#v", binding)
		}
	}
	attempt := findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if len(attempt.SourceSnapshots) != 2 || attempt.SourceSnapshots[0].Source.String() >= attempt.SourceSnapshots[1].Source.String() {
		t.Fatalf("canonical SourceSnapshots are not complete and sorted: %#v", attempt.SourceSnapshots)
	}
}

func TestRuntimeV2FakePueueCrashRecoversRouteWithoutDuplicateAfterSourceRemoval(t *testing.T) {
	fixture, _, sourceRoot, _, _ := newRuntimeV2Fixture(t, false)
	info := fixture.adapter.CanonicalWorkspace
	now := fixture.now.Add(3 * time.Hour)
	operations, err := operation.Open(t.Context(), info.Repository.GitCommonDir, operation.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer operations.Close()
	environment, err := execx.MinimalEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	accepted := false
	label := ""
	group := "gpu"
	addCount := 0
	workerCommand := ""
	invoker := execx.InvokerFunc(func(_ context.Context, spec execx.CommandSpec) (execx.Result, error) {
		if len(spec.Argv) == 2 && spec.Argv[0] == "status" {
			tasks := map[string]any{}
			if accepted {
				tasks["17"] = map[string]any{
					"id": 17, "group": group, "dependencies": []int{}, "priority": 0,
					"label": label, "status": map[string]any{"Queued": map[string]any{}},
				}
			}
			content, _ := json.Marshal(map[string]any{"tasks": tasks, "groups": map[string]any{group: map[string]any{"status": "Running", "parallel_tasks": 2}}})
			return execx.Result{Stdout: string(content), ExitCode: 0}, nil
		}
		if len(spec.Argv) > 0 && spec.Argv[0] == "add" {
			addCount++
			for index := 0; index+1 < len(spec.Argv); index++ {
				switch spec.Argv[index] {
				case "--label":
					label = spec.Argv[index+1]
				case "--group":
					group = spec.Argv[index+1]
				}
			}
			workerCommand = spec.Argv[len(spec.Argv)-1]
			accepted = true
			return execx.Result{ExitCode: -1}, errors.New("transport lost after Pueue accepted task")
		}
		return execx.Result{ExitCode: -1}, errors.New("unexpected fake Pueue command")
	})
	scheduler := controller.PueueScheduler{
		Adapter:     pueue.Adapter{Invoker: invoker, LookupBinary: func(string) (string, error) { return filepath.Join(fixture.repository, "fake-pueue"), nil }},
		Environment: environment,
	}
	scope, err := ScopeID(fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	control := controller.Controller{
		ProjectID: info.Project().ProjectID.String(), Scope: scope, Holder: "runtime-v2-test",
		Canonical: fixture.adapter, Operational: operations, Scheduler: scheduler,
		Clock: func() time.Time { return now }, LeaseTTL: time.Minute,
		MarkerRoot: filepath.Join(info.Repository.GitCommonDir, "exp", "v1", "attempts"),
		NewID:      func(prefix string) string { return prefix + "-fake-pueue" },
	}
	first, firstErr := control.Tick(t.Context())
	if firstErr == nil || addCount != 1 || !accepted {
		pools, poolsErr := fixture.adapter.Pools(t.Context())
		frontier, frontierErr := fixture.adapter.Frontier(t.Context())
		t.Fatalf("first crash tick=%#v add_count=%d accepted=%t err=%v pools=%#v pools_err=%v frontier=%#v frontier_err=%v", first, addCount, accepted, firstErr, pools, poolsErr, frontier, frontierErr)
	}
	for _, token := range []string{"--canonical-root", "--project", "--scope", "--fencing-token"} {
		if !strings.Contains(workerCommand, token) {
			t.Fatalf("quoted worker command omits %s: %q", token, workerCommand)
		}
	}
	if err := os.RemoveAll(sourceRoot); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	result, err := control.Tick(t.Context())
	if err != nil || addCount != 1 || len(result.Recovered) != 1 {
		t.Fatalf("recovery tick=%#v add_count=%d err=%v", result, addCount, err)
	}
	jobs, err := operations.ListJobs(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].PueueTaskID == nil || *jobs[0].PueueTaskID != 17 {
		t.Fatalf("recovered jobs=%#v err=%v", jobs, err)
	}
	attempt := findAttemptByDispatch(validInventory(t, fixture.store), strings.TrimPrefix(jobs[0].ID, "job-"))
	if attempt == nil || attempt.Record.(*research.Attempt).State != research.AttemptQueued {
		t.Fatalf("recovered canonical Attempt = %#v", attempt)
	}
}

func TestRuntimeV2MarkerReconcilesCanonicalAttemptAfterSourceRemoval(t *testing.T) {
	fixture, _, sourceRoot, _, _ := newRuntimeV2Fixture(t, false)
	updateRuntimeV2Route(t, fixture, func(route PlanRuntimeV2) PlanRuntimeV2 {
		route.Executable = "/usr/bin/true"
		route.Argv = []string{"formal"}
		return route
	})
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	info := fixture.adapter.CanonicalWorkspace
	operations, err := operation.Open(t.Context(), info.Repository.GitCommonDir)
	if err != nil {
		t.Fatal(err)
	}
	defer operations.Close()
	job, _, err := operations.EnqueueJob(t.Context(), prepared.Job)
	if err != nil {
		t.Fatal(err)
	}
	job, err = operations.ClaimJobByID(t.Context(), job.ID, "pueue", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	markerRoot := filepath.Join(info.Repository.GitCommonDir, "exp", "v1", "attempts")
	if err := os.MkdirAll(markerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	terminal, err := (worker.Runner{
		Store: operations, Snapshots: sourcesnapshot.Capturer{}, MarkerRoot: markerRoot,
		ProjectID: info.Project().ProjectID.String(), CanonicalScope: prepared.Job.CanonicalScope,
		Clock: fixture.adapter.Clock,
	}).Run(t.Context(), job)
	if err != nil || terminal.State != operation.JobSucceeded {
		t.Fatalf("formal worker terminal=%#v err=%v", terminal, err)
	}
	if err := os.RemoveAll(sourceRoot); err != nil {
		t.Fatal(err)
	}
	if err := fixture.adapter.Reconcile(t.Context(), controller.SchedulerSnapshot{}); err != nil {
		t.Fatal(err)
	}
	attempt := findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if attempt.State != research.AttemptSucceeded || attempt.Terminal == nil || attempt.Terminal.Source != "direct" || attempt.Terminal.ExitCode == nil || *attempt.Terminal.ExitCode != 0 {
		t.Fatalf("marker-reconciled canonical Attempt = %#v", attempt)
	}
}

func TestRuntimeV2MovedSourceBlocksUnstartedDispatch(t *testing.T) {
	fixture, _, sourceRoot, _, _ := newRuntimeV2Fixture(t, false)
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	moved := sourceRoot + "-moved"
	if err := os.Rename(sourceRoot, moved); err != nil {
		t.Fatal(err)
	}
	job := operation.Job{JobInput: prepared.Job, State: operation.JobRunning, FencingToken: 9}
	if err := fixture.adapter.RevalidateSubmission(t.Context(), selection, job); !errors.Is(err, controller.ErrSubmissionBlocked) {
		t.Fatalf("moved Source revalidation error = %v", err)
	}
	job.State = operation.JobUnknown
	if err := fixture.adapter.SubmissionBlocked(t.Context(), selection, job); err != nil {
		t.Fatal(err)
	}
	attempt := findAttemptByDispatch(validInventory(t, fixture.store), selection.ID).Record.(*research.Attempt)
	if attempt.State != research.AttemptBlocked || attempt.Terminal != nil {
		t.Fatalf("blocked canonical Attempt = %#v", attempt)
	}
}

func TestRuntimeV2RejectsEnvironmentBoundMLflowProfileBeforePueueDispatch(t *testing.T) {
	fixture, _, _, _, _ := newRuntimeV2Fixture(t, false)
	base := fixture.adapter.ConfigLoader
	fixture.adapter.ConfigLoader = runtimeConfigLoaderFunc(func(ctx context.Context, request config.Request) (*config.Result, error) {
		result, err := base.Load(ctx, request)
		if err != nil {
			return nil, err
		}
		profile := result.Effective.MLflow.Profiles["training"]
		profile.Env = map[string]config.EnvBinding{
			"WORKLOAD_TOKEN": {From: "PARENT_MLFLOW_TOKEN", Secret: true, Required: true},
		}
		result.Effective.MLflow.Profiles["training"] = profile
		return result, nil
	})
	t.Setenv("PARENT_MLFLOW_TOKEN", "FORMAL_PROFILE_SECRET_CANARY")
	if _, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit)); err == nil || !strings.Contains(err.Error(), "environment-bound MLflow profiles are unsupported for Pueue") {
		t.Fatalf("environment-bound formal profile = %v", err)
	}
	if attempts := validInventory(t, fixture.store).OfKind(research.KindAttempt); len(attempts) != 0 {
		t.Fatalf("rejected profile published Attempts: %#v", attempts)
	}
}

func TestRuntimeV2FormalAttemptReachesEvaluationAndCandidateV2(t *testing.T) {
	fixture, sourceID, _, _, head := newRuntimeV2Fixture(t, false)
	selection, err := fixture.adapter.Next(t.Context(), fixture.poolID.String(), string(research.LaneExploit))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.adapter.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	var preparedWorkload worker.Workload
	if err := json.Unmarshal(prepared.Job.Payload, &preparedWorkload); err != nil {
		t.Fatal(err)
	}
	if preparedWorkload.MLflow == nil || preparedWorkload.MLflow.Name != "training" || preparedWorkload.MLflow.Context != "formal" || len(preparedWorkload.MLflow.DefaultMetrics) != 1 {
		t.Fatalf("formal MLflow workload binding = %#v", preparedWorkload.MLflow)
	}
	started := fixture.now.Add(time.Hour)
	ended := started.Add(time.Minute)
	terminal := worker.Terminal{
		Schema: worker.SourceTerminalSchema, JobID: prepared.Job.ID, AttemptID: prepared.Job.SubjectID,
		FencingToken: 7, State: operation.JobSucceeded, ExitCode: 0, StartedAt: started, EndedAt: ended,
		Outputs: map[string]string{},
		MLflow: &mlflow.Attachment{
			Profile: "training", Context: "formal", RunID: "formal-run-7", ExperimentID: "7", Status: "FINISHED",
			Metrics: map[string]float64{"score": 0.9}, Tags: map[string]string{mlflow.AttemptOwnershipTag: prepared.Job.SubjectID},
			ObservedAt: ended, State: mlflow.ObservationVerified, Reasons: []string{},
		},
	}
	resultPayload, err := json.Marshal(worker.JobResult{Schema: worker.SourceResultSchema, Result: json.RawMessage(`{}`), Terminal: terminal})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.adapter.Reconcile(t.Context(), controller.SchedulerSnapshot{Jobs: []operation.Job{{
		JobInput: prepared.Job, State: operation.JobSucceeded, FencingToken: 7, Result: resultPayload,
	}}}); err != nil {
		t.Fatal(err)
	}
	inventory := validInventory(t, fixture.store)
	attemptDocument := findAttemptByDispatch(inventory, selection.ID)
	attempt := attemptDocument.Record.(*research.Attempt)
	var trackerReference research.ExternalRef
	for _, reference := range attempt.ExternalRefs {
		if reference.Provider == "mlflow" && reference.Role == research.ExternalTracker {
			trackerReference = reference
		}
	}
	if trackerReference.NativeID != "formal-run-7" || trackerReference.Metadata["mlflow.owner_attempt"] != attempt.ID.String() {
		t.Fatalf("formal Attempt MLflow reference = %#v", trackerReference)
	}
	runDocument, err := inventory.ByID(attempt.Run)
	if err != nil {
		t.Fatal(err)
	}
	run := runDocument.Record.(*research.Run)
	experimentDocument, err := inventory.ByID(run.Experiment)
	if err != nil {
		t.Fatal(err)
	}
	planDocument, err := inventory.ByID(fixture.planID)
	if err != nil {
		t.Fatal(err)
	}
	current := ended.Add(time.Minute)
	threshold := 0.5
	specID := mustID(t, "evalspec_01a09000-0000-7303-8000-000000000303", research.KindEvaluationSpec)
	spec := &research.EvaluationSpec{
		Common:  research.Common{Schema: research.SchemaEvaluationSpec, ID: specID, Title: "Formal runtime evaluation", CreatedAt: current, UpdatedAt: current},
		Purpose: research.EvaluationScientific, Dataset: "held-out", Protocol: "fixed protocol",
		Metrics:    []research.MetricSpec{{Name: "score", Unit: "point", Direction: research.MetricMaximize, Threshold: &threshold}},
		BudgetPool: fixture.poolID, BudgetHours: 1, SealedAt: &current,
	}
	if _, err := fixture.store.Transact(t.Context(), record.TransactionRequest{Operation: "fixture.evaluation-spec", Changes: []record.TransactionChange{{
		Operation: record.TransactionCreate, Document: &record.Document{Record: spec, Body: "# Formal runtime evaluation\n"},
	}}}); err != nil {
		t.Fatal(err)
	}
	generated := []uuid.UUID{
		uuid.MustParse("01a09000-0000-7404-8000-000000000404"),
		uuid.MustParse("01a09000-0000-7505-8000-000000000505"),
	}
	generateIndex := 0
	service := lifecycle.New(fixture.store,
		lifecycle.WithClock(func() time.Time { return current }),
		lifecycle.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			value := generated[generateIndex]
			generateIndex++
			return value, nil
		}),
	)
	inventory = validInventory(t, fixture.store)
	attemptDocument, _ = inventory.ByID(attempt.ID)
	runDocument, _ = inventory.ByID(run.ID)
	experimentDocument, _ = inventory.ByID(run.Experiment)
	planDocument, _ = inventory.ByID(fixture.planID)
	closed, err := service.CloseExperiment(t.Context(), lifecycle.CloseExperimentRequest{
		Experiment: lifecycle.RevisionRef{ID: run.Experiment, Revision: experimentDocument.Revision},
		Plan:       lifecycle.RevisionRef{ID: fixture.planID, Revision: planDocument.Revision},
		Verdict:    research.VerdictSupported, Summary: "Formal Source runtime passed.",
		Evidence: []lifecycle.ConclusionEvidenceInput{{
			Run: lifecycle.RevisionRef{ID: run.ID, Revision: runDocument.Revision}, Disposition: research.EvidenceIncluded,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	current = current.Add(time.Minute)
	inventory = validInventory(t, fixture.store)
	specDocument, _ := inventory.ByID(specID)
	attemptDocument, _ = inventory.ByID(attempt.ID)
	evaluation, err := service.CreateEvaluation(t.Context(), lifecycle.CreateEvaluationRequest{
		Spec:    lifecycle.RevisionRef{ID: specID, Revision: specDocument.Revision},
		Subject: lifecycle.RevisionRef{ID: run.Experiment, Revision: closed.Experiment.Revision},
		Attempt: lifecycle.RevisionRef{ID: attempt.ID, Revision: attemptDocument.Revision},
		Data: lifecycle.EvaluationData{
			Title: "Formal runtime result", Outcome: research.EvaluationPassed,
			Metrics: []research.MetricValue{{Name: "score", Value: 0.9, Unit: "point"}},
			Summary: "The formal runtime passed.", ExternalRefs: []research.ExternalRef{trackerReference},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluationValue := evaluation.Evaluation.Record.(*research.Evaluation)
	if evaluationValue.Schema != research.SchemaEvaluationV2 || evaluationValue.Attempt != attempt.ID || len(evaluationValue.ExternalRefs) != 1 || evaluationValue.ExternalRefs[0].Metadata["mlflow.owner_attempt"] != attempt.ID.String() {
		t.Fatalf("exact Attempt-bound Evaluation v2 = %#v", evaluationValue)
	}
	current = current.Add(time.Minute)
	candidate, err := service.CreateCandidate(t.Context(), lifecycle.CreateCandidateRequest{
		Title: "Formal runtime candidate", Body: "# Formal runtime candidate\n",
		Experiment:                     lifecycle.RevisionRef{ID: run.Experiment, Revision: closed.Experiment.Revision},
		Evaluation:                     lifecycle.RevisionRef{ID: evaluation.Evaluation.Record.(*research.Evaluation).ID, Revision: evaluation.Evaluation.Revision},
		EvaluationSpecExpectedRevision: specDocument.Revision,
		Attempt:                        lifecycle.RevisionRef{ID: attempt.ID, Revision: attemptDocument.Revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := candidate.Candidate.Record.(*research.Candidate)
	if value.Schema != research.SchemaCandidateV2 || value.Attempt != attempt.ID || len(value.Sources) != 1 ||
		value.Sources[0].Source != sourceID || value.Sources[0].HeadCommit != head ||
		!reflect.DeepEqual(value.Sources[0].ChangeSet, attempt.SourceSnapshots[0].ChangeSet) {
		t.Fatalf("reachable Candidate v2 = %#v", value)
	}
}

func TestRuntimeV2DecoderIsClosedWithoutWideningV1(t *testing.T) {
	repository := canonicalTemp(t)
	if err := os.MkdirAll(filepath.Join(repository, ".exp"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repository, filepath.FromSlash(DefaultConfigPath))
	v2 := `{"schema_version":"exp.runtime/v2","pools":{},"plans":{},"surprise":true}`
	if err := os.WriteFile(path, []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntimeV2(t.Context(), repository, DefaultConfigPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("v2 unknown field error = %v", err)
	}
	v1 := `{"schema_version":"exp.runtime/v1","pools":{},"plans":{},"execution_source":"src_01a09000-0000-7101-8000-000000000101"}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntime(t.Context(), repository, DefaultConfigPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("v1 accepted v2 field: %v", err)
	}
}

func updateRuntimeV2Route(t *testing.T, fixture canonicalFixture, update func(PlanRuntimeV2) PlanRuntimeV2) {
	t.Helper()
	configPath := filepath.Join(fixture.repository, filepath.FromSlash(DefaultConfigPath))
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var runtime RuntimeConfigV2
	if err := json.Unmarshal(content, &runtime); err != nil {
		t.Fatal(err)
	}
	route := runtime.Plans[fixture.planID.String()]
	runtime.Plans[fixture.planID.String()] = update(route)
	content, err = json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newRuntimeV2Fixture(t *testing.T, observational bool) (canonicalFixture, research.ID, string, string, string) {
	t.Helper()
	fixture := newCanonicalFixture(t, research.AutonomyAssisted)
	info, err := project.Discover(t.Context(), fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := canonicalTemp(t)
	runGit(t, sourceRoot, "init", "--quiet")
	runGit(t, sourceRoot, "config", "user.email", "source@example.invalid")
	runGit(t, sourceRoot, "config", "user.name", "Source Test")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "pkg", "train.py"), []byte("print('base')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, sourceRoot, "add", "pkg/train.py")
	runGit(t, sourceRoot, "commit", "--quiet", "-m", "source base")
	base := runGit(t, sourceRoot, "rev-parse", "HEAD")
	head := base
	changeSet := []string{}
	if !observational {
		if err := os.WriteFile(filepath.Join(sourceRoot, "pkg", "train.py"), []byte("print('candidate')\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, sourceRoot, "add", "pkg/train.py")
		runGit(t, sourceRoot, "commit", "--quiet", "-m", "source candidate")
		head = runGit(t, sourceRoot, "rev-parse", "HEAD")
		changeSet = []string{"pkg/train.py"}
	} else {
		base = head
	}
	sourceID := mustID(t, "src_01a09000-0000-7101-8000-000000000101", research.KindSource)
	source := &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: sourceID, Title: "External training source", CreatedAt: fixture.now, UpdatedAt: fixture.now},
		Key:    "training", Kind: research.SourceGit, Subdir: "pkg", LocatorHints: []string{}, State: research.SourceActive,
	}
	if _, err := fixture.store.Transact(t.Context(), record.TransactionRequest{Operation: "fixture.source", Changes: []record.TransactionChange{{
		Operation: record.TransactionCreate, Document: &record.Document{Record: source, Body: "# External training source\n"},
	}}}); err != nil {
		t.Fatal(err)
	}
	associationPath := filepath.Join(canonicalTemp(t), "associations.json")
	associations := workspace.NewStore(workspace.WithPath(associationPath))
	if _, err := associations.RegisterProject(t.Context(), info); err != nil {
		t.Fatal(err)
	}
	if _, err := associations.RegisterSource(t.Context(), info.Project().ProjectID, sourceID, sourceRoot); err != nil {
		t.Fatal(err)
	}
	resolver := workspace.NewResolver(associations, workspace.WithoutConfig())
	userConfigRoot := canonicalTemp(t)
	userConfigPath := filepath.Join(userConfigRoot, "config.toml")
	userConfig := `schema = "exp.config/v1"
[defaults]
mlflow_profile = "training"
[mlflow.profiles.training]
context = "formal"
binary = "mlflow-test"
timeout = "2s"
default_metrics = ["score"]
`
	if err := os.WriteFile(userConfigPath, []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	loader := config.NewLoader(config.WithUserConfigPath(userConfigPath))
	runtime := RuntimeConfigV2{
		Schema: RuntimeSchemaV2,
		Pools:  map[string]PoolRuntime{fixture.poolID.String(): {PueueGroup: "gpu", LabelPrefix: "study-"}},
		Plans: map[string]PlanRuntimeV2{fixture.planID.String(): {
			ExecutionSource: sourceID.String(), ReadOnlySources: []ReadOnlySourceRuntime{},
			Executable: "/bin/echo", Argv: []string{"train"}, Checkout: CheckoutMain, CWD: ".", Timeout: "5m",
			AllowedEnv: []string{"PATH"}, SecretEnv: []string{}, BaseCommit: base, HeadCommit: head,
			ChangeSet: changeSet, ObservationalNoChange: observational, ExpectedOutputs: []string{},
		}},
	}
	content, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.repository, filepath.FromSlash(DefaultConfigPath)), content, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.adapter.CanonicalWorkspace = info
	fixture.adapter.SourceResolver = resolver
	fixture.adapter.ConfigLoader = loader
	fixture.adapter.RuntimeTrust = runtimeTrustAll{}
	fixture.adapter.SourceCapturer = sourcesnapshot.Capturer{}
	fixture.adapter.Workspace = workspacebackend.NativeGit{}
	fixture.associations = associations
	return fixture, sourceID, sourceRoot, base, head
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

var _ = time.Second
