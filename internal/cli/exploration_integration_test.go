package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/exploration"
)

func explorationTestHomes(t *testing.T) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(name, filepath.Join(root, name))
	}
}

func TestExplorationStartExecuteRetrieveAndAgentFinish(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("full Git lifecycle runs without -race; exploration concurrency and record transitions have focused race tests")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	explorationTestHomes(t)
	fixture := newDirectTryCLIFixture(t)
	invoke := func(args ...string) commandInvocation {
		return invokeCommand(t, fixture.app, "", append([]string{"--workspace", fixture.projectID, "--start-dir", fixture.sourcePath}, args...)...)
	}
	start := invoke("try", "start", "--title", "Plot exploration", "--goal", "Find a useful trend", "--json")
	requireCommandSuccess(t, start)
	var started struct {
		Try canonicalRecordView `json:"try"`
	}
	decodeData(t, decodeEnvelope(t, start.stdout), &started)
	data := filepath.Join(fixture.base, "outside.csv")
	if err := os.WriteFile(data, []byte("sample-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireCommandSuccess(t, invoke("input", "bind", "sample", data, "--json"))
	code := `import os,pathlib; data=pathlib.Path(os.environ['EXP_INPUT_SAMPLE']).read_text(); pathlib.Path(os.environ['EXP_OUTPUT_DIR'],'plot.txt').write_text(data)`
	run := invoke("try", "exec", started.Try.ID, "--input", "sample", "--json", "--", "python3", "-c", code)
	requireCommandSuccess(t, run)
	var executed tryExecutionData
	decodeData(t, decodeEnvelope(t, run.stdout), &executed)
	if executed.Attempt == nil || executed.Attempt.Exploration == nil || len(executed.Attempt.Exploration.Artifacts) != 1 || executed.Attempt.Exploration.Runner == nil {
		t.Fatalf("missing execution provenance: %s", run.stdout)
	}
	if strings.Contains(run.stdout, data) {
		t.Fatal("host input path leaked into execution JSON")
	}
	list := invoke("results", "list", started.Try.ID, "--json")
	requireCommandSuccess(t, list)
	var artifacts []artifactView
	decodeData(t, decodeEnvelope(t, list.stdout), &artifacts)
	if len(artifacts) != 1 || artifacts[0].Availability != "local" {
		t.Fatalf("artifacts %s", list.stdout)
	}
	fetch := invoke("results", "fetch", started.Try.ID, "plot.txt", "--json")
	requireCommandSuccess(t, fetch)
	var fetched struct {
		Path string `json:"path"`
	}
	decodeData(t, decodeEnvelope(t, fetch.stdout), &fetched)
	contents, err := os.ReadFile(fetched.Path)
	if err != nil || string(contents) != "sample-data" {
		t.Fatalf("fetch %q %v", contents, err)
	}
	requireCommandSuccess(t, invoke("try", "summarize", started.Try.ID, "--summary", "A useful trend in the sampled rows", "--json"))
	search := invoke("history", "search", "sampled", "--json")
	requireCommandSuccess(t, search)
	if !strings.Contains(search.stdout, started.Try.ID) {
		t.Fatalf("history did not find summary: %s", search.stdout)
	}
	requireCommandSuccess(t, invoke("results", "describe", executed.Attempt.ID, "plot.txt", "--description", "The first sampled trend", "--json"))
	second := invoke("try", "exec", started.Try.ID, "--input", "sample", "--json", "--", "python3", "-c", strings.Replace(code, "write_text(data)", "write_text(data + '-second')", 1))
	requireCommandSuccess(t, second)
	var secondExecution tryExecutionData
	decodeData(t, decodeEnvelope(t, second.stdout), &secondExecution)
	if secondExecution.Attempt.ID == executed.Attempt.ID || secondExecution.Attempt.Exploration.Artifacts[0].Digest == executed.Attempt.Exploration.Artifacts[0].Digest {
		t.Fatal("new step reused its predecessor's identity or output")
	}
	ambiguous := invoke("results", "fetch", started.Try.ID, "plot.txt", "--json")
	if ambiguous.err == nil || decodeEnvelope(t, ambiguous.stdout).OK {
		t.Fatal("ambiguous artifact name silently selected an Attempt")
	}
	finish := invoke("try", "finish", started.Try.ID, "--author", "agent", "--summary", "The plot suggests a direction to explore", "--json")
	requireCommandSuccess(t, finish)
	if !strings.Contains(finish.stdout, `"conclusion_author": "agent"`) && !strings.Contains(finish.stdout, `"conclusion_author":"agent"`) {
		t.Fatalf("agent attribution lost: %s", finish.stdout)
	}
	requireCommandSuccess(t, invoke("try", "cleanup", started.Try.ID, "--confirm", "--json"))
	requireCommandSuccess(t, invoke("results", "fetch", executed.Attempt.ID, "plot.txt", "--json"))
	requireCommandSuccess(t, invoke("validate", "--json"))
}

func TestScratchCollectionAndRememberedRoot(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("scratch Git lifecycle runs without -race; private state locking has focused race tests")
	}
	explorationTestHomes(t)
	fixture := newDirectTryCLIFixture(t)
	root := filepath.Join(fixture.base, "scratch")
	configure := invokeCommand(t, fixture.app, "", "try", "root", root, "--json")
	requireCommandSuccess(t, configure)
	var firstSource string
	for i := 0; i < 2; i++ {
		start := invokeCommand(t, fixture.app, "", "--start-dir", fixture.base, "try", "start", "--scratch", "--title", "Adhoc plot", "--goal", "Understand sample", "--json")
		requireCommandSuccess(t, start)
		var result struct {
			SourcePath string              `json:"source_path"`
			Project    string              `json:"project"`
			Try        canonicalRecordView `json:"try"`
		}
		decodeData(t, decodeEnvelope(t, start.stdout), &result)
		if !strings.HasPrefix(result.SourcePath, root) || result.Try.ID == "" {
			t.Fatalf("scratch: %s", start.stdout)
		}
		if i == 0 {
			firstSource = result.SourcePath
		} else if firstSource == result.SourcePath {
			t.Fatal("two explorations share a writable Source")
		}
	}
	s, err := exploration.Load(t.Context(), "")
	if err != nil || s.TriesRoot != root {
		t.Fatalf("root not persisted: %#v %v", s, err)
	}
	search := invokeCommand(t, fixture.app, "", "history", "search", "Adhoc", "--all", "--json")
	requireCommandSuccess(t, search)
	var result struct {
		Results []json.RawMessage `json:"results"`
	}
	decodeData(t, decodeEnvelope(t, search.stdout), &result)
	if len(result.Results) < 2 {
		t.Fatalf("cross-project search missed scratch history: %s", search.stdout)
	}
}
