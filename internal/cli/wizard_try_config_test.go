package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuidedTryRunDefaultDirtyReviewMissingToolsAndRedaction(t *testing.T) {
	t.Run("default and optional tool visibility", func(t *testing.T) {
		fixture := newDirectTryCLIFixture(t)
		enableDeterministicWizard(fixture.app)
		delegate := fixture.app.BinaryLookup
		fixture.app.BinaryLookup = func(name string) (string, error) {
			if name == "dev" || name == "mlflow" {
				return "", errors.New("injected optional tool missing")
			}
			return delegate(name)
		}
		invocation := invokeCommand(t, fixture.app, "Guided check\nGather bounded evidence\ntrue\nno\nRUN\n",
			"--start-dir", fixture.sourcePath, "try", "run")
		requireHumanCommandSuccess(t, invocation)
		for _, expected := range []string{"Review direct Try plan", "dev_cli workspace", "disabled", "MLflow observation", "Remediation:", " is succeeded"} {
			if !strings.Contains(invocation.stdout, expected) {
				t.Fatalf("guided Try output omitted %q:\n%s", expected, invocation.stdout)
			}
		}
	})

	t.Run("dirty capture is reviewed", func(t *testing.T) {
		fixture := newDirectTryCLIFixture(t)
		seed := filepath.Join(fixture.sourcePath, "dirty-seed.txt")
		if err := os.WriteFile(seed, []byte("private seed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		enableDeterministicWizard(fixture.app)
		input := "Dirty guided check\nReview bounded dirty evidence\ntrue\nno\nyes\ncapture\n\n\n\n\n\nRUN\n"
		invocation := invokeCommand(t, fixture.app, input, "--start-dir", fixture.sourcePath, "try", "run")
		requireHumanCommandSuccess(t, invocation)
		for _, expected := range []string{"Dirty capture: true", "Changed path:", "dirty-seed.txt", "Capture the reviewed bounded dirty Source state"} {
			if !strings.Contains(invocation.stdout, expected) {
				t.Fatalf("dirty review omitted %q:\n%s", expected, invocation.stdout)
			}
		}
		if content, err := os.ReadFile(seed); err != nil || string(content) != "private seed\n" {
			t.Fatalf("production dirty seed changed: %q, %v", content, err)
		}
	})

	t.Run("secret-like argv is redacted before decline", func(t *testing.T) {
		fixture := newDirectTryCLIFixture(t)
		enableDeterministicWizard(fixture.app)
		const canary = "WIZARD_ARGV_SECRET_91f4"
		input := "Redaction check\nReview argv redaction\ntrue --token " + canary + "\nno\nnot-run\n"
		invocation := invokeCommand(t, fixture.app, input, "--start-dir", fixture.sourcePath, "try", "run")
		if !errors.Is(invocation.err, ErrPromptCanceled) {
			t.Fatalf("declined Try error = %v", invocation.err)
		}
		if strings.Contains(invocation.stdout+invocation.stderr, canary) {
			t.Fatalf("guided Try leaked secret-like argv: stdout=%s stderr=%s", invocation.stdout, invocation.stderr)
		}
		status := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "list", "--json")
		requireCommandSuccess(t, status)
		var data tryStatusData
		decodeData(t, decodeEnvelope(t, status.stdout), &data)
		if len(data.Tries) != 0 {
			t.Fatalf("declined guided Try published state: %#v", data.Tries)
		}
	})

	t.Run("disabled backend is rejected before publication", func(t *testing.T) {
		fixture := newDirectTryCLIFixture(t)
		enableDeterministicWizard(fixture.app)
		delegate := fixture.app.BinaryLookup
		fixture.app.BinaryLookup = func(name string) (string, error) {
			if name == "dev" {
				return "", errors.New("injected dev CLI missing")
			}
			return delegate(name)
		}
		input := "Unavailable backend\nReject before publication\ntrue\nyes\n\n\ndev_cli\n\n\n\nRUN\n"
		invocation := invokeCommand(t, fixture.app, input, "--start-dir", fixture.sourcePath, "try", "run")
		if invocation.err == nil {
			t.Fatalf("disabled backend unexpectedly ran:\n%s", invocation.stdout)
		}
		status := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "list", "--json")
		requireCommandSuccess(t, status)
		var data tryStatusData
		decodeData(t, decodeEnvelope(t, status.stdout), &data)
		if len(data.Tries) != 0 {
			t.Fatalf("disabled backend published state before refusal: %#v; run error=%v", data.Tries, invocation.err)
		}
	})

	t.Run("dirty bytes changing after review are stale", func(t *testing.T) {
		fixture := newDirectTryCLIFixture(t)
		seed := filepath.Join(fixture.sourcePath, "byte-race.txt")
		if err := os.WriteFile(seed, []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
		fixture.app.Prompter = &safetyScriptedPrompter{
			answers: []string{"Byte race", "Detect changed dirty bytes", "true", "no", "yes", "capture", "", "native_git", "", "0", "", "RUN"},
			before: func(request PromptRequest) {
				if strings.Contains(request.Label, "Run this Try") {
					if err := os.WriteFile(seed, []byte("after!\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			},
		}
		invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "run")
		if !errors.Is(invocation.err, ErrGuidedPlanStale) {
			t.Fatalf("dirty byte race error = %v\n%s", invocation.err, invocation.stdout)
		}
		if !strings.Contains(invocation.stdout, "Dirty content digest") {
			t.Fatalf("dirty content identity was not reviewed:\n%s", invocation.stdout)
		}
		status := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "try", "list", "--json")
		requireCommandSuccess(t, status)
		var data tryStatusData
		decodeData(t, decodeEnvelope(t, status.stdout), &data)
		if len(data.Tries) != 0 {
			t.Fatalf("dirty byte race published state: %#v", data.Tries)
		}
	})
}

func TestGuidedTryFinishAndAdopt(t *testing.T) {
	fixture := newDirectTryCLIFixture(t)
	run := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Lifecycle seed", "--goal", "Produce bounded evidence", "--json", "--", "true")
	requireCommandSuccess(t, run)
	var execution tryExecutionData
	decodeData(t, decodeEnvelope(t, run.stdout), &execution)
	if execution.Try == nil || execution.Attempt == nil {
		t.Fatalf("seed Try result = %#v", execution)
	}

	enableDeterministicWizard(fixture.app)
	finishInput := "\nEvidence supports formalization.\n"
	if len(execution.Attempt.ResultDigests) > 0 {
		finishInput += "\n"
	}
	finishInput += "yes\n"
	finished := invokeCommand(t, fixture.app, finishInput, "--start-dir", fixture.sourcePath, "try", "finish")
	requireHumanCommandSuccess(t, finished)
	if !strings.Contains(finished.stdout, "Review Try conclusion plan") || !strings.Contains(finished.stdout, "Concluded Try") {
		t.Fatalf("guided finish output:\n%s", finished.stdout)
	}

	adoptInput := "\n\n\nhuman:test\nno\nADOPT\n"
	adopted := invokeCommand(t, fixture.app, adoptInput, "--start-dir", fixture.sourcePath, "try", "adopt")
	requireHumanCommandSuccess(t, adopted)
	for _, expected := range []string{"Review Try adoption plan", "Primary cluster: exploratory", "Origin: human", "Adopted Try"} {
		if !strings.Contains(adopted.stdout, expected) {
			t.Fatalf("guided adopt omitted %q:\n%s", expected, adopted.stdout)
		}
	}
}

func TestGuidedConfigTrustDefaultMissingToolRedactionAndStaleness(t *testing.T) {
	t.Run("default capabilities and missing optional tool", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "")
		const secret = "TRUST_WIZARD_SECRET_6a3d"
		t.Setenv("WIZARD_SECRET_ENV", secret)
		configPath := writeGuidedTrustConfig(t, fixture, "reviewer")
		delegate := fixture.app.BinaryLookup
		fixture.app.BinaryLookup = func(name string) (string, error) {
			if name == "missing-mlflow-observer" {
				return "", errors.New("injected optional observer missing")
			}
			return delegate(name)
		}
		enableDeterministicWizard(fixture.app)
		invocation := invokeCommand(t, fixture.app, "\nno\nTRUST\n", "--start-dir", fixture.canonical, "config", "trust")
		requireHumanCommandSuccess(t, invocation)
		for _, expected := range []string{"Review config trust plan", "agent.profile", "mlflow.profile", "missing-mlflow-observer", "disabled", "Remediation:"} {
			if !strings.Contains(invocation.stdout, expected) {
				t.Fatalf("guided trust omitted %q:\n%s", expected, invocation.stdout)
			}
		}
		if strings.Contains(invocation.stdout+invocation.stderr, secret) {
			t.Fatalf("guided trust leaked resolved environment value: stdout=%s stderr=%s", invocation.stdout, invocation.stderr)
		}
		if !strings.Contains(invocation.stdout, filepath.Base(configPath)) {
			t.Fatalf("guided trust did not identify selected config layer:\n%s", invocation.stdout)
		}
		receipts, err := fixture.app.TrustStore.List(t.Context())
		if err != nil || len(receipts) != 1 || len(receipts[0].Capabilities) != 2 {
			t.Fatalf("trust receipts = %#v, err=%v", receipts, err)
		}
	})

	t.Run("decline has no receipt", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "")
		writeGuidedTrustConfig(t, fixture, "reviewer")
		enableDeterministicWizard(fixture.app)
		invocation := invokeCommand(t, fixture.app, "\nno\nNOPE\n", "--start-dir", fixture.canonical, "config", "trust")
		if !errors.Is(invocation.err, ErrPromptCanceled) {
			t.Fatalf("trust decline error = %v", invocation.err)
		}
		receipts, err := fixture.app.TrustStore.List(t.Context())
		if err != nil || len(receipts) != 0 {
			t.Fatalf("declined trust receipts = %#v, err=%v", receipts, err)
		}
	})

	t.Run("config digest change has no receipt", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "")
		configPath := writeGuidedTrustConfig(t, fixture, "reviewer")
		fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
		fixture.app.Prompter = &safetyScriptedPrompter{
			answers: []string{configPath, "no", "TRUST"},
			before: func(request PromptRequest) {
				if strings.Contains(request.Label, "Trust this config") {
					content, err := os.ReadFile(configPath)
					if err != nil {
						t.Fatal(err)
					}
					updated := strings.Replace(string(content), `agent_profile = "reviewer"`, `agent_profile = "reviewer-two"`, 1)
					if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			},
		}
		invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "trust")
		if !errors.Is(invocation.err, ErrGuidedPlanStale) {
			t.Fatalf("stale config trust error = %v", invocation.err)
		}
		receipts, err := fixture.app.TrustStore.List(t.Context())
		if err != nil || len(receipts) != 0 {
			t.Fatalf("stale trust receipts = %#v, err=%v", receipts, err)
		}
	})

	t.Run("JSON refuses missing fields without reading stdin", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "")
		writeGuidedTrustConfig(t, fixture, "reviewer")
		fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
		reader := &countingEOFReader{}
		invocation := invokeCommandWithReader(t, fixture.app, reader, "--start-dir", fixture.canonical, "config", "trust", "--json")
		if !errors.Is(invocation.err, ErrInvalidUsage) || reader.calls != 0 {
			t.Fatalf("JSON trust error=%v reads=%d", invocation.err, reader.calls)
		}
		envelope := decodeEnvelope(t, invocation.stdout)
		if envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "command.invalid_usage" {
			t.Fatalf("JSON usage envelope = %#v", envelope)
		}
	})
}

func writeGuidedTrustConfig(t *testing.T, fixture *externalCLIFixture, agentProfile string) string {
	t.Helper()
	path := filepath.Join(fixture.canonical, ".exp-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `schema = "exp.config/v1"
[defaults]
agent_profile = "` + agentProfile + `"
mlflow_profile = "observer"
[mlflow.profiles.observer]
context = "local"
binary = "missing-mlflow-observer"
timeout = "30s"
[mlflow.profiles.observer.env.TOKEN]
from = "WIZARD_SECRET_ENV"
secret = true
required = true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
