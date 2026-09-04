package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestPromptDefaultsValidationCancellationAndRedaction(t *testing.T) {
	t.Run("default display differs from stored fallback", func(t *testing.T) {
		const secret = "PROMPT_SECRET_CANARY_728d"
		var output strings.Builder
		app := NewApp(t.Context(), strings.NewReader("\n"), &output, io.Discard)
		app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
		value, err := app.Prompter.Ask(t.Context(), PromptRequest{
			Label: "Profile fallback", Default: secret, DefaultDisplay: "configured value",
		})
		if err != nil || value != secret {
			t.Fatalf("prompt value=%q err=%v", value, err)
		}
		if strings.Contains(output.String(), secret) || !strings.Contains(output.String(), "configured value") {
			t.Fatalf("prompt leaked stored fallback: %q", output.String())
		}
	})

	t.Run("invalid value reprompts", func(t *testing.T) {
		var output strings.Builder
		app := NewApp(t.Context(), strings.NewReader("bad\ngood\n"), &output, io.Discard)
		app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
		value, err := app.askValidated(t.Context(), PromptRequest{Label: "Value"}, func(value string) error {
			if value != "good" {
				return errors.New("use good")
			}
			return nil
		})
		if err != nil || value != "good" || strings.Count(output.String(), "Value") != 2 || !strings.Contains(output.String(), "Invalid") {
			t.Fatalf("reprompt value=%q err=%v output=%q", value, err, output.String())
		}
	})

	for name, input := range map[string]string{
		"EOF": "", "Escape": "\x1b", "Ctrl-C": "\x03", "Ctrl-D": "\x04",
	} {
		t.Run(name, func(t *testing.T) {
			app := NewApp(t.Context(), strings.NewReader(input), io.Discard, io.Discard)
			app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
			_, err := app.Prompter.Ask(t.Context(), PromptRequest{Label: "Cancel"})
			if !errors.Is(err, ErrPromptCanceled) {
				t.Fatalf("cancellation error = %v", err)
			}
		})
	}

	t.Run("decline uses cancellation type", func(t *testing.T) {
		app := NewApp(t.Context(), strings.NewReader("no\n"), io.Discard, io.Discard)
		app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
		if err := app.requireConfirmation(t.Context(), "Apply", "", ""); !errors.Is(err, ErrPromptCanceled) {
			t.Fatalf("decline error = %v", err)
		}
	})
}

func TestGuidedInitDefaultsToDedicatedPrivateRepository(t *testing.T) {
	base, _ := externalTestRoots(t)
	sourceRoot := initExternalGitRepository(t, filepath.Join(base, "model-source"))
	app := deterministicApp(t,
		"01a0a100-0000-7001-8000-000000000001",
		"01a0a100-0000-7002-8000-000000000002",
		"01a0a100-0000-7003-8000-000000000003",
	)
	app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
	invocation := invokeCommand(t, app, "\n\n\n\n\nno\nLOCAL\nCREATE\n", "--start-dir", sourceRoot, "init")
	if invocation.err != nil {
		t.Fatalf("guided init: %v\nstdout=%s\nstderr=%s", invocation.err, invocation.stdout, invocation.stderr)
	}
	target := filepath.Join(base, "model-source-experiments")
	info, err := project.Discover(t.Context(), target)
	if err != nil || info.Project() == nil {
		t.Fatalf("discover dedicated Project: info=%#v err=%v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(sourceRoot, "experiments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("guided init wrote canonical state into Source: %v", err)
	}
	if remotes := strings.TrimSpace(externalGitOutput(t, target, "remote")); remotes != "" {
		t.Fatalf("guided init created remote %q", remotes)
	}
	if commits := strings.TrimSpace(externalGitOutput(t, target, "rev-list", "--all")); commits != "" {
		t.Fatalf("guided init created commit %q", commits)
	}
	for _, section := range []string{"Summary", "Effects", "Paths", "Source", "Snapshot", "Optional-tool readiness"} {
		if !strings.Contains(invocation.stdout, section) {
			t.Fatalf("guided init omitted %s:\n%s", section, invocation.stdout)
		}
	}
}

func TestExplicitDedicatedCreateCleansFailedGitInitAndRefusesUnsafeTargets(t *testing.T) {
	base, _ := externalTestRoots(t)
	sourceRoot := initExternalGitRepository(t, filepath.Join(base, "source"))
	target := filepath.Join(base, "dedicated")
	app := deterministicApp(t, "01a0a200-0000-7001-8000-000000000001")
	original := app.GitRunner
	failure := errors.New("injected git init failure")
	app.GitRunner = gitx.RunnerFunc(func(ctx context.Context, directory string, arguments []string) (string, string, error) {
		if directory == target && len(arguments) == 2 && arguments[0] == "init" && arguments[1] == "--quiet" {
			return "", "credential=https://user:secret@example.invalid", failure
		}
		return original.Run(ctx, directory, arguments)
	})
	invocation := invokeCommand(t, app, "", "--start-dir", sourceRoot, "init",
		"--dedicated-repo", target, "--create", "--source-repo", sourceRoot, "--source-key", "source",
		"--confirm-local-only", "--confirm", "--json")
	if !errors.Is(invocation.err, failure) {
		t.Fatalf("git init failure = %v", invocation.err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed created target was not cleaned: %v", err)
	}
	if strings.Contains(invocation.stdout+invocation.stderr+invocation.err.Error(), "secret") {
		t.Fatalf("git init failure leaked credentials: %s%s%v", invocation.stdout, invocation.stderr, invocation.err)
	}

	nonempty := filepath.Join(base, "nonempty")
	if err := os.Mkdir(nonempty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonempty, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused := invokeCommand(t, app, "", "--start-dir", sourceRoot, "init",
		"--dedicated-repo", nonempty, "--create", "--source-repo", sourceRoot, "--source-key", "source",
		"--confirm-local-only", "--confirm", "--json")
	if refused.err == nil {
		t.Fatal("nonempty non-Git dedicated target was accepted")
	}
	if content, err := os.ReadFile(filepath.Join(nonempty, "keep.txt")); err != nil || string(content) != "keep" {
		t.Fatalf("unsafe target changed: %q err=%v", content, err)
	}

	nested := filepath.Join(sourceRoot, "nested-experiments")
	refused = invokeCommand(t, app, "", "--start-dir", sourceRoot, "init",
		"--dedicated-repo", nested, "--create", "--source-repo", sourceRoot, "--source-key", "source",
		"--confirm-local-only", "--confirm", "--json")
	if refused.err == nil {
		t.Fatal("nested missing dedicated target was accepted")
	}
	if _, err := os.Lstat(nested); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested refused target was created: %v", err)
	}
}

func TestGuidedSourceLocalOnlyCancellationAndConfirmation(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a0a300-0000-7001-8000-000000000001",
		"01a0a300-0000-7002-8000-000000000002",
		"01a0a300-0000-7003-8000-000000000003",
	)
	fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
	declined := invokeCommand(t, fixture.app, "local\n"+fixture.source+"\nno\nnot-local\n",
		"--start-dir", fixture.canonical, "source", "add")
	if !errors.Is(declined.err, ErrPromptCanceled) {
		t.Fatalf("local-only decline = %v", declined.err)
	}
	assertCanonicalSourceCount(t, fixture.canonical, 0)

	confirmed := invokeCommand(t, fixture.app, "local\n"+fixture.source+"\nno\nLOCAL\n",
		"--start-dir", fixture.canonical, "source", "add")
	if confirmed.err != nil {
		t.Fatalf("local-only confirmation: %v\n%s", confirmed.err, confirmed.stdout)
	}
	assertCanonicalSourceCount(t, fixture.canonical, 1)
	if !strings.Contains(confirmed.stdout, "Local only") || !strings.Contains(confirmed.stdout, "true") {
		t.Fatalf("local-only plan not visible: %s", confirmed.stdout)
	}
}

type scriptedPrompt struct {
	responses []string
	index     int
	before    func(PromptRequest, int)
}

func (prompt *scriptedPrompt) Ask(_ context.Context, request PromptRequest) (string, error) {
	index := prompt.index
	if prompt.before != nil {
		prompt.before(request, index)
	}
	if index >= len(prompt.responses) {
		return "", ErrPromptCanceled
	}
	prompt.index++
	return prompt.responses[index], nil
}

func TestGuidedSourceRejectsStalePlanBeforeEffects(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a0a400-0000-7001-8000-000000000001",
		"01a0a400-0000-7002-8000-000000000002",
	)
	fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
	fixture.app.Prompter = &scriptedPrompt{
		responses: []string{"guided", fixture.source, "no", "yes"},
		before: func(request PromptRequest, _ int) {
			if request.Label == "Apply Source add plan" {
				if err := os.WriteFile(filepath.Join(fixture.source, "raced.txt"), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		},
	}
	invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "add")
	if !errors.Is(invocation.err, ErrGuidedPlanStale) {
		t.Fatalf("stale Source plan = %v", invocation.err)
	}
	assertCanonicalSourceCount(t, fixture.canonical, 0)
}

func TestGuidedDirtyTryReviewShowsDisabledToolsAndCancellationHasNoEffects(t *testing.T) {
	fixture := newDirectTryCLIFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.sourcePath, "dirty-review.txt"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
	input := strings.Join([]string{
		"Dirty review", "Inspect bounded dirty state", "true", "no", "yes", "capture", "", "", "", "", "", "STOP", "",
	}, "\n")
	invocation := invokeCommand(t, fixture.app, input, "--start-dir", fixture.sourcePath, "try", "run")
	if !errors.Is(invocation.err, ErrPromptCanceled) {
		t.Fatalf("dirty Try cancellation = %v\n%s", invocation.err, invocation.stdout)
	}
	for _, text := range []string{"dirty-review.txt", "Dirty capture", "dev_cli workspace", "MLflow observation", "disabled"} {
		if !strings.Contains(invocation.stdout, text) {
			t.Fatalf("dirty Try review omitted %q:\n%s", text, invocation.stdout)
		}
	}
	info, err := project.Discover(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := record.NewStore(info.Root, info.Repository.GitCommonDir).Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tries := inventory.OfKind(research.KindTry); len(tries) != 0 {
		t.Fatalf("cancelled Try published %d records", len(tries))
	}
}

func TestGuidedTryFinishAndAdoptUseExistingServices(t *testing.T) {
	fixture := newDirectTryCLIFixture(t)
	run := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Guided lifecycle", "--goal", "Exercise human review", "--json", "--", "true")
	requireCommandSuccess(t, run)
	var execution tryExecutionData
	decodeData(t, decodeEnvelope(t, run.stdout), &execution)
	if execution.Try == nil {
		t.Fatal("Try run returned no Try")
	}
	fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
	finished := invokeCommand(t, fixture.app, "Evidence supports adoption.\nyes\n", "--start-dir", fixture.sourcePath,
		"try", "finish", execution.Try.ID)
	if finished.err != nil {
		t.Fatalf("guided finish: %v\n%s", finished.err, finished.stdout)
	}
	adopted := invokeCommand(t, fixture.app, "\n\nhuman:test\nno\nADOPT\n", "--start-dir", fixture.sourcePath,
		"try", "adopt", execution.Try.ID)
	if adopted.err != nil {
		t.Fatalf("guided adopt: %v\n%s", adopted.err, adopted.stdout)
	}
	if !strings.Contains(finished.stdout, "Review Try conclusion plan") || !strings.Contains(adopted.stdout, "Review Try adoption plan") {
		t.Fatalf("guided lifecycle plans missing:\nfinish=%s\nadopt=%s", finished.stdout, adopted.stdout)
	}
}

func TestGuidedConfigTrustDerivesDigestWithoutSecretFallbackLeak(t *testing.T) {
	const secret = "GUIDED_CONFIG_SECRET_CANARY_9ee2"
	fixture := newExternalCLIFixture(t, ".", "",
		"01a0a500-0000-7001-8000-000000000001",
	)
	t.Setenv("GUIDED_PARENT_SECRET", secret)
	configPath := filepath.Join(fixture.canonical, ".exp-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `schema = "exp.config/v1"
[defaults]
agent_profile = "reviewer"
mlflow_profile = "local"
[mlflow.profiles.local]
context = "local"
binary = "definitely-missing-mlflow"
timeout = "30s"
[mlflow.profiles.local.env.TOKEN]
from = "GUIDED_PARENT_SECRET"
secret = true
required = true
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
	invocation := invokeCommand(t, fixture.app, "\nno\nTRUST\n", "--start-dir", fixture.canonical, "config", "trust")
	if invocation.err != nil {
		t.Fatalf("guided config trust: %v\nstdout=%s\nstderr=%s", invocation.err, invocation.stdout, invocation.stderr)
	}
	if strings.Contains(invocation.stdout+invocation.stderr, secret) || strings.Contains(invocation.stdout, "GUIDED_PARENT_SECRET") {
		t.Fatalf("guided trust leaked env name/value: %s%s", invocation.stdout, invocation.stderr)
	}
	for _, text := range []string{"Config digest", "Trust state digest", "definitely-missing-mlflow", "disabled", "Remediation"} {
		if !strings.Contains(invocation.stdout, text) {
			t.Fatalf("guided trust omitted %q:\n%s", text, invocation.stdout)
		}
	}
}

func TestWizardNeverReadsJSONAndExplicitTryFastPathNeverPrompts(t *testing.T) {
	fixture := newDirectTryCLIFixture(t)
	fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }

	missingReader := &countingEOFReader{}
	missing := invokeCommandWithReader(t, fixture.app, missingReader, "--start-dir", fixture.sourcePath, "try", "finish", "--json")
	if missing.err == nil || missingReader.calls != 0 {
		t.Fatalf("JSON missing fields err=%v reads=%d", missing.err, missingReader.calls)
	}
	if envelope := decodeEnvelope(t, missing.stdout); envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "command.invalid_usage" {
		t.Fatalf("JSON usage envelope = %#v", envelope)
	}

	fastReader := &countingEOFReader{}
	fast := invokeCommandWithReader(t, fixture.app, fastReader, "--start-dir", fixture.sourcePath,
		"try", "run", "--title", "Explicit fast path", "--goal", "Never read stdin", "--json", "--", "true")
	if fast.err != nil || fastReader.calls != 0 {
		t.Fatalf("explicit Try fast path err=%v reads=%d stdout=%s", fast.err, fastReader.calls, fast.stdout)
	}
}
