package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/spf13/cobra"
)

type completionResolver struct {
	result   *workspace.Context
	err      error
	requests []workspace.ResolveRequest
}

func (resolver *completionResolver) Resolve(_ context.Context, request workspace.ResolveRequest) (*workspace.Context, error) {
	resolver.requests = append(resolver.requests, request)
	return resolver.result, resolver.err
}

type completionStore struct {
	inventory *record.Inventory
	err       error
}

func (store *completionStore) Inventory(context.Context) (*record.Inventory, error) {
	return store.inventory, store.err
}

func (*completionStore) Transact(context.Context, record.TransactionRequest) (*record.TransactionResult, error) {
	return nil, errors.New("completion must not mutate records")
}

func (*completionStore) Recover(context.Context) error {
	return errors.New("completion must not recover records")
}

type completionAssociationStore struct {
	AssociationStore
	associations workspace.Associations
	err          error
}

func (store *completionAssociationStore) List(context.Context) (workspace.Associations, error) {
	return store.associations, store.err
}

func TestCompletionDisplayCodesScaleAndResultsAreCapped(t *testing.T) {
	documents := make([]*record.Document, 2000)
	for index := range documents {
		id, err := research.ParseIDForKind(fmt.Sprintf("try_01a08691-0000-7003-8000-%012x", index+1), research.KindTry)
		if err != nil {
			t.Fatal(err)
		}
		documents[index] = &record.Document{Record: &research.Try{Common: research.Common{ID: id}}}
	}
	codes := completionDisplayCodes(documents)
	if len(codes) != len(documents) {
		t.Fatalf("display code count = %d, want %d", len(codes), len(documents))
	}
	seen := make(map[string]struct{}, len(codes))
	builder := newCompletionBuilder("")
	for _, code := range codes {
		if _, duplicate := seen[code]; duplicate {
			t.Fatalf("duplicate display code %q", code)
		}
		seen[code] = struct{}{}
		builder.add(code, "Try")
	}
	if len(builder.values()) != maxCompletionItems || !builder.truncated || builder.directive(nil)&cobra.ShellCompDirectiveError == 0 {
		t.Fatalf("completion cap = items %d truncated=%t directive=%v", len(builder.items), builder.truncated, builder.directive(nil))
	}
}

func TestCompletionScriptsUseRestoredPublicCommandWithoutANSI(t *testing.T) {
	for shell, marker := range map[string]string{
		"bash": "__start_exp",
		"zsh":  "#compdef exp",
		"fish": "complete -c exp",
	} {
		invocation := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", "--color", "always", "completion", shell)
		if invocation.err != nil || invocation.stderr != "" {
			t.Fatalf("completion %s: error=%v stderr=%q", shell, invocation.err, invocation.stderr)
		}
		if !strings.Contains(invocation.stdout, marker) {
			t.Errorf("completion %s is missing %q", shell, marker)
		}
		if strings.Contains(invocation.stdout, "\x1b[") {
			t.Errorf("completion %s protocol contains ANSI", shell)
		}
	}
}

func TestStaticCompletionIncludesColorGuidesAndWorkspaceBackends(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{[]string{"__complete", "--color", "a"}, "always"},
		{[]string{"__complete", "guide", "pro"}, "promotion"},
		{[]string{"__complete", "--workspace-backend", "n"}, "native_git"},
		{[]string{"__complete", "--workspace-backend", "d"}, "dev_cli"},
	} {
		invocation := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", testCase.args...)
		if invocation.err != nil || invocation.stderr != "" {
			t.Fatalf("%v: error=%v stderr=%q", testCase.args, invocation.err, invocation.stderr)
		}
		if !strings.Contains(invocation.stdout, testCase.want) || !strings.HasSuffix(invocation.stdout, ":4\n") {
			t.Errorf("%v: want %q and no-file directive, got %q", testCase.args, testCase.want, invocation.stdout)
		}
	}
}

func TestDynamicCompletionUsesParsedWorkspaceSourceAndStartDir(t *testing.T) {
	app, resolver := completionTestApp(t)
	app.Getwd = func() (string, error) { return "", errors.New("completion should use --start-dir") }

	source := invokeCommand(t, app, "", "__complete", "--start-dir", "/chosen/start", "--workspace", "chosen-workspace", "--source", "")
	if source.err != nil || source.stderr != "" {
		t.Fatalf("source completion: error=%v stderr=%q", source.err, source.stderr)
	}
	for _, want := range []string{"app\t", "U-01A08690\t", "src_01a08690-0000-7002-8000-000000000002\t"} {
		if !strings.Contains(source.stdout, want) {
			t.Errorf("source completion missing %q:\n%s", want, source.stdout)
		}
	}
	if len(resolver.requests) == 0 {
		t.Fatal("source completion did not resolve local workspace")
	}
	request := resolver.requests[len(resolver.requests)-1]
	if request.InvocationDir != "/chosen/start" || request.Workspace != "chosen-workspace" || request.Source != "" || !request.SkipConfig {
		t.Fatalf("source completion request = %#v", request)
	}

	resolver.requests = nil
	tryResult := invokeCommand(t, app, "", "__complete", "--start-dir", "/chosen/start", "--workspace", "chosen-workspace", "--source", "app", "try", "show", "")
	if tryResult.err != nil || tryResult.stderr != "" {
		t.Fatalf("Try completion: error=%v stderr=%q", tryResult.err, tryResult.stderr)
	}
	for _, want := range []string{"Y-01A08691\t", "try_01a08691-0000-7003-8000-000000000003\t"} {
		if !strings.Contains(tryResult.stdout, want) {
			t.Errorf("Try completion missing %q:\n%s", want, tryResult.stdout)
		}
	}
	request = resolver.requests[len(resolver.requests)-1]
	if request.Source != "app" || request.Workspace != "chosen-workspace" || request.InvocationDir != "/chosen/start" || !request.SkipConfig {
		t.Fatalf("Try completion ignored parsed selectors: %#v", request)
	}
}

func TestRecordQueuePoolAndTargetCompletionUsesLocalInventory(t *testing.T) {
	app, _ := completionTestApp(t)
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"__complete", "record", "show", ""}, []string{"U-01A08690\t", "Y-01A08691\t", "Q-01A08692\t"}},
		{[]string{"__complete", "queue", "insert", ""}, []string{"Q-01A08692\t"}},
		{[]string{"__complete", "queue", "insert", "Q-01A08692", ""}, []string{"P-01A08694\t"}},
		{[]string{"__complete", "queue", "create", "--pool", ""}, []string{"O-01A08693\t"}},
		{[]string{"__complete", "idea", "qualify", "I-00000000", "--resource", ""}, []string{"O-01A08693:\t"}},
		{[]string{"__complete", "promotion", "append", "--target", "pro"}, []string{"production\t"}},
	}
	for _, testCase := range cases {
		invocation := invokeCommand(t, app, "", testCase.args...)
		if invocation.err != nil || invocation.stderr != "" {
			t.Fatalf("%v: error=%v stderr=%q", testCase.args, invocation.err, invocation.stderr)
		}
		for _, want := range testCase.want {
			if !strings.Contains(invocation.stdout, want) {
				t.Errorf("%v missing %q:\n%s", testCase.args, want, invocation.stdout)
			}
		}
		if !strings.HasSuffix(invocation.stdout, ":4\n") {
			t.Errorf("%v did not disable file completion: %q", testCase.args, invocation.stdout)
		}
	}
}

func TestProfileCompletionRespectsParsedConfigWithoutLoadingSecrets(t *testing.T) {
	app, resolver := completionTestApp(t)
	loadedPath := ""
	app.ResolveAgentConfigPath = func() (string, error) { return "", errors.New("explicit --config was ignored") }
	app.LoadAgentConfig = func(path string) (agentcli.Config, error) {
		loadedPath = path
		return agentcli.Config{
			Roles: map[string]string{"idea_planner": "local"},
			Profiles: map[string]agentcli.Profile{
				"local": {Executable: "agent", ReportedModel: "model\tline\npassword=profile-secret"},
			},
		}, nil
	}
	agent := invokeCommand(t, app, "", "__complete", "idea", "develop", "--config", "/chosen/agents.toml", "--profile", "")
	if agent.err != nil || agent.stderr != "" || loadedPath != "/chosen/agents.toml" {
		t.Fatalf("agent profile completion: path=%q error=%v stderr=%q", loadedPath, agent.err, agent.stderr)
	}
	if !strings.Contains(agent.stdout, "local\tagent | model=model line password=[REDACTED]") || strings.Contains(agent.stdout, "profile-secret") {
		t.Fatalf("agent profile description was not sanitized: %q", agent.stdout)
	}

	resolver.requests = nil
	mlflow := invokeCommand(t, app, "", "__complete", "--start-dir", "/chosen/start", "--workspace", "chosen", "--source", "app", "--mlflow-profile", "")
	if mlflow.err != nil || mlflow.stderr != "" || !strings.Contains(mlflow.stdout, "tracking\tcontext=study | binary=mlflow") {
		t.Fatalf("MLflow completion: error=%v stderr=%q stdout=%q", mlflow.err, mlflow.stderr, mlflow.stdout)
	}
	request := resolver.requests[len(resolver.requests)-1]
	if request.InvocationDir != "/chosen/start" || request.Workspace != "chosen" || request.Source != "app" || request.SkipConfig {
		t.Fatalf("MLflow completion ignored parsed selectors/config: %#v", request)
	}
}

func TestCompletionDescriptionsCannotInjectProtocolFields(t *testing.T) {
	app, _ := completionTestApp(t)
	invocation := invokeCommand(t, app, "", "__complete", "try", "show", "")
	if invocation.err != nil || invocation.stderr != "" {
		t.Fatalf("completion error=%v stderr=%q", invocation.err, invocation.stderr)
	}
	if strings.Contains(invocation.stdout, "completion-secret") || !strings.Contains(invocation.stdout, "[REDACTED]") || strings.Contains(invocation.stdout, "\x1b[") {
		t.Fatalf("completion description bypassed safety: %q", invocation.stdout)
	}
	for _, line := range strings.Split(strings.TrimSuffix(invocation.stdout, "\n"), "\n") {
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.Count(line, "\t") != 1 {
			t.Errorf("candidate injected completion columns: %q", line)
		}
	}
}

func TestCompletionNoDescriptionProtocolStaysPlainAndQuiet(t *testing.T) {
	app, _ := completionTestApp(t)
	invocation := invokeCommand(t, app, "", "__completeNoDesc", "try", "show", "")
	if invocation.err != nil || invocation.stderr != "" {
		t.Fatalf("no-description completion: error=%v stderr=%q", invocation.err, invocation.stderr)
	}
	if !strings.Contains(invocation.stdout, "Y-01A08691\n") || strings.Contains(invocation.stdout, "\t") || strings.Contains(invocation.stdout, "\x1b[") {
		t.Fatalf("no-description completion protocol = %q", invocation.stdout)
	}
}

func TestCompletionDirectiveFilterForwardsOtherStderr(t *testing.T) {
	var output strings.Builder
	writer := completionDiagnosticWriter{destination: &output}
	directive := "Completion ended with directive: ShellCompDirectiveNoFileComp\n"
	if count, err := writer.Write([]byte(directive)); err != nil || count != len(directive) || output.Len() != 0 {
		t.Fatalf("directive filter = count %d error %v output %q", count, err, output.String())
	}
	message := "real completion failure\n"
	if count, err := writer.Write([]byte(message)); err != nil || count != len(message) || output.String() != message {
		t.Fatalf("diagnostic forwarding = count %d error %v output %q", count, err, output.String())
	}
}

func TestInvalidOrMissingLocalStateDegradesToNoCandidatesQuietly(t *testing.T) {
	app, resolver := completionTestApp(t)
	resolver.err = errors.New("local state unavailable")
	invocation := invokeCommand(t, app, "", "__complete", "try", "show", "")
	if invocation.err != nil || invocation.stderr != "" || invocation.stdout != ":4\n" {
		t.Fatalf("missing state completion = stdout %q stderr %q error %v", invocation.stdout, invocation.stderr, invocation.err)
	}

	resolver.err = nil
	app.NewTransactionalStore = func(*project.Info) (TransactionalRecordStore, error) {
		return &completionStore{inventory: &record.Inventory{Diagnostics: []record.Diagnostic{{Code: "invalid", Message: "bad"}}}}, nil
	}
	invocation = invokeCommand(t, app, "", "__complete", "record", "show", "")
	if invocation.err != nil || invocation.stderr != "" || invocation.stdout != ":4\n" {
		t.Fatalf("invalid inventory completion = stdout %q stderr %q error %v", invocation.stdout, invocation.stderr, invocation.err)
	}

	app.LoadAgentConfig = func(string) (agentcli.Config, error) { return agentcli.Config{}, errors.New("bad config") }
	invocation = invokeCommand(t, app, "", "__complete", "agent", "run", "--config", "/bad.toml", "--profile", "")
	if invocation.err != nil || invocation.stderr != "" || invocation.stdout != ":4\n" {
		t.Fatalf("invalid profile completion = stdout %q stderr %q error %v", invocation.stdout, invocation.stderr, invocation.err)
	}
}

func TestWorkspaceCompletionReadsOnlyLocalAssociationSnapshot(t *testing.T) {
	app, _ := completionTestApp(t)
	projectID, err := research.ParseUUID("01a08699-0000-7001-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	app.Associations = &completionAssociationStore{associations: workspace.Associations{
		Schema: workspace.AssociationSchema,
		Projects: []workspace.ProjectAssociation{{
			ProjectID: projectID,
			Root:      "/local/research",
		}},
	}}
	invocation := invokeCommand(t, app, "", "__complete", "--workspace", "")
	if invocation.err != nil || invocation.stderr != "" {
		t.Fatalf("workspace completion: error=%v stderr=%q", invocation.err, invocation.stderr)
	}
	for _, want := range []string{projectID.String() + "\t", "/local/research\t"} {
		if !strings.Contains(invocation.stdout, want) {
			t.Errorf("workspace completion missing %q: %q", want, invocation.stdout)
		}
	}
}

func completionTestApp(t *testing.T) (*App, *completionResolver) {
	t.Helper()
	ids := map[research.Kind]string{
		research.KindSource:       "src_01a08690-0000-7002-8000-000000000002",
		research.KindTry:          "try_01a08691-0000-7003-8000-000000000003",
		research.KindQueue:        "queue_01a08692-0000-7004-8000-000000000004",
		research.KindResourcePool: "pool_01a08693-0000-7005-8000-000000000005",
		research.KindPlan:         "plan_01a08694-0000-7006-8000-000000000006",
		research.KindRelease:      "rel_01a08695-0000-7007-8000-000000000007",
	}
	parsed := map[research.Kind]research.ID{}
	for kind, value := range ids {
		id, err := research.ParseIDForKind(value, kind)
		if err != nil {
			t.Fatalf("parse %s: %v", value, err)
		}
		parsed[kind] = id
	}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	common := func(kind research.Kind, title string) research.Common {
		schema, _ := kind.Schema()
		return research.Common{Schema: schema, ID: parsed[kind], Title: title, CreatedAt: now, UpdatedAt: now}
	}
	documents := []*record.Document{
		{Record: &research.Source{Common: common(research.KindSource, "Application"), Key: "app", Kind: research.SourceGit, Subdir: ".", State: research.SourceActive}},
		{Record: &research.Try{Common: common(research.KindTry, "Try\tname\npassword=completion-secret"), State: research.TryOpen}},
		{Record: &research.Queue{Common: common(research.KindQueue, "Research queue")}},
		{Record: &research.ResourcePool{Common: common(research.KindResourcePool, "GPU pool"), Enabled: true, Capacity: 1, Unit: "gpu", Bottleneck: "gpu"}},
		{Record: &research.Plan{Common: common(research.KindPlan, "Queued plan"), State: research.PlanQueued}},
		{Record: &research.Release{Common: common(research.KindRelease, "Production release"), Target: "production", State: research.ReleaseValidated}},
	}
	inventory := &record.Inventory{Documents: documents}
	effective := config.Builtins()
	effective.MLflow.Profiles["tracking"] = config.MLflowProfile{
		Context: "study", Binary: "mlflow", Timeout: 30 * time.Second, Env: map[string]config.EnvBinding{}, DefaultMetrics: []string{},
	}
	info := &project.Info{}
	resolver := &completionResolver{result: &workspace.Context{
		Project: info,
		Config:  &config.Result{Effective: effective},
	}}
	providers := &countingProviderRegistry{}
	binaryLookups := 0
	app := NewApp(t.Context(), nil, nil, nil)
	app.Getwd = func() (string, error) { return "/default/start", nil }
	app.ResolveWorkspace = resolver
	app.NewTransactionalStore = func(*project.Info) (TransactionalRecordStore, error) {
		return &completionStore{inventory: inventory}, nil
	}
	app.Registry = providers
	app.BinaryLookup = func(string) (string, error) {
		binaryLookups++
		return "", errors.New("completion must not look up provider binaries")
	}
	t.Cleanup(func() {
		if providers.calls != 0 || binaryLookups != 0 {
			t.Errorf("completion contacted providers: registry calls=%d binary lookups=%d", providers.calls, binaryLookups)
		}
	})
	return app, resolver
}

func Example_sanitizedCompletionDescription() {
	fmt.Println(sanitizeCompletionDescription("one\ttwo\nthree"))
	// Output: one two three
}
