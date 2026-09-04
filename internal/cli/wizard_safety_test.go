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
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestGuidedSourceAddDefaultInvalidRepromptAndLocalOnly(t *testing.T) {
	t.Run("default remote plan", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git")
		enableDeterministicWizard(fixture.app)
		invocation := invokeCommand(t, fixture.app, "training\n"+fixture.source+"\nno\nyes\n",
			"--start-dir", fixture.canonical, "source", "add")
		requireHumanCommandSuccess(t, invocation)
		assertCanonicalSourceCount(t, fixture.canonical, 1)
		for _, section := range []string{"Summary", "Effects", "Paths", "Source", "Snapshot", "Optional-tool readiness"} {
			if !strings.Contains(invocation.stdout, section) {
				t.Fatalf("guided output omitted %q:\n%s", section, invocation.stdout)
			}
		}
	})

	t.Run("invalid key reprompts", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git")
		enableDeterministicWizard(fixture.app)
		invocation := invokeCommand(t, fixture.app, "Bad Key\nbad-key\n"+fixture.source+"\nno\nyes\n",
			"--start-dir", fixture.canonical, "source", "add")
		requireHumanCommandSuccess(t, invocation)
		if !strings.Contains(invocation.stdout, "Invalid:") || !strings.Contains(invocation.stdout, "bad-key") {
			t.Fatalf("invalid input was not explained and reprompted:\n%s", invocation.stdout)
		}
		assertCanonicalSourceCount(t, fixture.canonical, 1)
	})

	t.Run("local only exact confirmation", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "")
		enableDeterministicWizard(fixture.app)
		invocation := invokeCommand(t, fixture.app, "local\n"+fixture.source+"\nno\nLOCAL\n",
			"--start-dir", fixture.canonical, "source", "add")
		requireHumanCommandSuccess(t, invocation)
		if !strings.Contains(invocation.stdout, "Local only:") || !strings.Contains(invocation.stdout, "true") {
			t.Fatalf("local-only review was not visible:\n%s", invocation.stdout)
		}
		assertCanonicalSourceCount(t, fixture.canonical, 1)
	})
}

func TestGuidedSourceCancellationAndStalePlanHaveZeroEffects(t *testing.T) {
	t.Run("EOF", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git")
		enableDeterministicWizard(fixture.app)
		invocation := invokeCommand(t, fixture.app, "cancelled\n", "--start-dir", fixture.canonical, "source", "add")
		if !errors.Is(invocation.err, ErrPromptCanceled) {
			t.Fatalf("EOF error = %v", invocation.err)
		}
		assertCanonicalSourceCount(t, fixture.canonical, 0)
	})

	t.Run("decline", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git")
		enableDeterministicWizard(fixture.app)
		invocation := invokeCommand(t, fixture.app, "declined\n"+fixture.source+"\nno\nno\n",
			"--start-dir", fixture.canonical, "source", "add")
		if !errors.Is(invocation.err, ErrPromptCanceled) {
			t.Fatalf("decline error = %v", invocation.err)
		}
		assertCanonicalSourceCount(t, fixture.canonical, 0)
	})

	t.Run("Source snapshot changes", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git")
		fixture.app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
		fixture.app.Prompter = &safetyScriptedPrompter{
			answers: []string{"stale", fixture.source, "no", "yes"},
			before: func(request PromptRequest) {
				if strings.Contains(request.Label, "Apply Source add plan") {
					if err := os.WriteFile(filepath.Join(fixture.source, "changed-after-review.txt"), []byte("changed\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			},
		}
		invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "add")
		if !errors.Is(invocation.err, ErrGuidedPlanStale) {
			t.Fatalf("stale Source error = %v", invocation.err)
		}
		assertCanonicalSourceCount(t, fixture.canonical, 0)
	})
}

func TestGuidedSourceRegisterSuppliesMissingSource(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git")
	addFixtureSource(t, fixture, "production")
	projectID, err := research.ParseUUID(fixture.projectID)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := research.ParseIDForKind(fixture.sourceID, research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := fixture.app.Associations.RemoveSource(t.Context(), projectID, sourceID); err != nil || !removed {
		t.Fatalf("remove Source association: removed=%t err=%v", removed, err)
	}
	enableDeterministicWizard(fixture.app)
	invocation := invokeCommand(t, fixture.app, "\nyes\n", "--start-dir", fixture.canonical,
		"source", "register", "--repo", fixture.source)
	requireHumanCommandSuccess(t, invocation)
	if !strings.Contains(invocation.stdout, "Review Source registration plan") {
		t.Fatalf("Source registration plan missing:\n%s", invocation.stdout)
	}
	if _, err := fixture.app.Associations.LookupSource(t.Context(), projectID, sourceID); err != nil {
		t.Fatalf("guided Source registration was not published: %v", err)
	}
	if removed, err := fixture.app.Associations.RemoveSource(t.Context(), projectID, sourceID); err != nil || !removed {
		t.Fatalf("remove repaired Source association: removed=%t err=%v", removed, err)
	}
	reader := &countingEOFReader{}
	refused := invokeCommandWithReader(t, fixture.app, reader, "--start-dir", fixture.canonical,
		"source", "register", "--repo", fixture.source, "--json")
	if !errors.Is(refused.err, ErrInvalidUsage) || reader.calls != 0 {
		t.Fatalf("non-TTY missing Source error=%v reads=%d", refused.err, reader.calls)
	}
}

func TestGuidedInitDefaultsToSeparatePrivateRepository(t *testing.T) {
	base, _ := externalTestRoots(t)
	source := initExternalGitRepository(t, filepath.Join(base, "code"))
	externalGit(t, source, "remote", "add", "origin", "https://example.com/org/code.git")
	target := source + "-experiments"
	app := deterministicApp(t)
	enableDeterministicWizard(app)
	invocation := invokeCommand(t, app, "\n\n\n\n\nno\nCREATE\n", "--start-dir", source, "init")
	requireHumanCommandSuccess(t, invocation)

	if info, err := os.Stat(target); err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private dedicated target info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(target, "experiments", "PROJECT.md")); err != nil {
		t.Fatalf("dedicated Project missing: %v", err)
	}
	if got := strings.TrimSpace(externalGitOutput(t, target, "remote")); got != "" {
		t.Fatalf("wizard created remote %q", got)
	}
	if got := strings.TrimSpace(externalGitOutput(t, target, "rev-list", "--all")); got != "" {
		t.Fatalf("wizard created commit %q", got)
	}
	if _, err := os.Lstat(filepath.Join(target, ".gitmodules")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wizard created submodule metadata: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(source, "experiments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wizard wrote canonical files into Source: %v", err)
	}
}

func TestDedicatedTargetSymlinkDiagnosticsDoNotFormatNilErrors(t *testing.T) {
	base, _ := externalTestRoots(t)
	realParent := filepath.Join(base, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(base, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	_, err := inspectDedicatedTarget(t.Context(), deterministicApp(t), base, filepath.Join(linkedParent, "target"))
	if err == nil || !strings.Contains(err.Error(), "without symlink substitution") {
		t.Fatalf("symlink-substituted parent error = %v", err)
	}
	if strings.Contains(err.Error(), "%!") {
		t.Fatalf("symlink-substituted parent exposed a formatting artifact: %v", err)
	}

	realTarget := filepath.Join(realParent, "real-target")
	if err := os.Mkdir(realTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedTarget := filepath.Join(realParent, "linked-target")
	if err := os.Symlink(realTarget, linkedTarget); err != nil {
		t.Skipf("target symlink creation is unavailable: %v", err)
	}
	_, err = inspectDedicatedTarget(t.Context(), deterministicApp(t), base, linkedTarget)
	if err == nil || !strings.Contains(err.Error(), "missing or real directory") {
		t.Fatalf("symlink target error = %v", err)
	}
	if strings.Contains(err.Error(), "%!") {
		t.Fatalf("symlink target exposed a formatting artifact: %v", err)
	}
}

func TestDedicatedCreateExplicitFastPathCleanupAndNestedRefusal(t *testing.T) {
	t.Run("explicit create", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		target := filepath.Join(base, "private-exp")
		app := deterministicApp(t)
		reader := &countingEOFReader{}
		invocation := invokeCommandWithReader(t, app, reader,
			"--start-dir", source, "init", "--dedicated-repo", target, "--create",
			"--source-repo", source, "--source-key", "source", "--confirm", "--json")
		requireCommandSuccess(t, invocation)
		if reader.calls != 0 {
			t.Fatalf("explicit create prompted %d time(s)", reader.calls)
		}
		if _, err := os.Stat(filepath.Join(target, "experiments", "PROJECT.md")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("invalid Source causes no target effect", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		target := filepath.Join(base, "must-stay-missing")
		app := deterministicApp(t)
		invocation := invokeCommand(t, app, "", "--start-dir", base, "init",
			"--dedicated-repo", target, "--create", "--source-repo", filepath.Join(base, "missing-source"),
			"--source-key", "source", "--confirm", "--json")
		if invocation.err == nil {
			t.Fatal("invalid Source unexpectedly initialized a target")
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid Source created target: %v", err)
		}
	})

	t.Run("post-creation failure reports retained repository", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		target := filepath.Join(base, "retained-exp")
		app := deterministicApp(t)
		originalInitialize := app.InitializeProject
		failure := errors.New("injected Project initialization failure")
		app.InitializeProject = func(context.Context, project.InitRequest) (*project.Info, bool, error) {
			return nil, false, failure
		}
		invocation := invokeCommand(t, app, "", "--start-dir", source, "init",
			"--dedicated-repo", target, "--create", "--source-repo", source,
			"--source-key", "source", "--confirm", "--json")
		if !errors.Is(invocation.err, failure) {
			t.Fatalf("Project initialization failure = %v", invocation.err)
		}
		envelope := decodeEnvelope(t, invocation.stdout)
		if !envelope.Partial || !diagnosticCodePresent(envelope.Diagnostics, "init.repository_initialized") {
			t.Fatalf("retained repository envelope = %#v", envelope)
		}
		if info, err := os.Stat(filepath.Join(target, ".git")); err != nil || !info.IsDir() {
			t.Fatalf("confirmed repository was not retained: info=%v err=%v", info, err)
		}
		if _, err := os.Lstat(filepath.Join(target, "experiments")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed Project initialization published canonical files: %v", err)
		}
		app.InitializeProject = originalInitialize
		retried := invokeCommand(t, app, "", "--start-dir", source, "init",
			"--dedicated-repo", target, "--create", "--source-repo", source,
			"--source-key", "source", "--confirm", "--json")
		requireCommandSuccess(t, retried)
	})

	t.Run("guided explicit Source works from non-Git start", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		target := filepath.Join(base, "guided-exp")
		app := deterministicApp(t)
		enableDeterministicWizard(app)
		invocation := invokeCommand(t, app, "\n\nno\nCREATE\n",
			"--start-dir", base, "init", "--dedicated-repo", target, "--create", "--source-repo", source)
		requireHumanCommandSuccess(t, invocation)
		if _, err := os.Stat(filepath.Join(target, "experiments", "PROJECT.md")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("git init failure removes created directory", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		target := filepath.Join(base, "failed-exp")
		app := deterministicApp(t, "01a0b310-0000-7001-8000-000000000001")
		delegate := app.GitRunner
		app.GitRunner = gitx.RunnerFunc(func(ctx context.Context, directory string, arguments []string) (string, string, error) {
			if directory == target && len(arguments) == 2 && arguments[0] == "init" && arguments[1] == "--quiet" {
				return "", "injected init failure", errors.New("injected git init failure")
			}
			return delegate.Run(ctx, directory, arguments)
		})
		invocation := invokeCommand(t, app, "",
			"--start-dir", source, "init", "--dedicated-repo", target, "--create",
			"--source-repo", source, "--source-key", "source", "--confirm", "--json")
		if invocation.err == nil {
			t.Fatal("injected git init failure unexpectedly succeeded")
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed created target was not cleaned: %v", err)
		}
	})

	t.Run("existing empty target is restored after init failure", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		target := filepath.Join(base, "empty-exp")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		app := deterministicApp(t, "01a0b320-0000-7001-8000-000000000001")
		delegate := app.GitRunner
		app.GitRunner = gitx.RunnerFunc(func(ctx context.Context, directory string, arguments []string) (string, string, error) {
			if directory == target && len(arguments) == 2 && arguments[0] == "init" && arguments[1] == "--quiet" {
				if err := os.Mkdir(filepath.Join(target, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
				return "", "injected partial init", errors.New("injected partial git init")
			}
			return delegate.Run(ctx, directory, arguments)
		})
		invocation := invokeCommand(t, app, "",
			"--start-dir", source, "init", "--dedicated-repo", target, "--create",
			"--source-repo", source, "--source-key", "source", "--confirm", "--json")
		if invocation.err == nil {
			t.Fatal("injected partial git init unexpectedly succeeded")
		}
		entries, err := os.ReadDir(target)
		if err != nil || len(entries) != 0 {
			t.Fatalf("existing target not restored to empty: entries=%v err=%v", entries, err)
		}
	})

	t.Run("unexpected bytes preserve recovery state", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		target := filepath.Join(base, "recovery-exp")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		app := deterministicApp(t)
		delegate := app.GitRunner
		app.GitRunner = gitx.RunnerFunc(func(ctx context.Context, directory string, arguments []string) (string, string, error) {
			if directory == target && len(arguments) == 2 && arguments[0] == "init" && arguments[1] == "--quiet" {
				if err := os.Mkdir(filepath.Join(target, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "foreign.txt"), []byte("preserve\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return "", "injected foreign path", errors.New("injected partial git init")
			}
			return delegate.Run(ctx, directory, arguments)
		})
		invocation := invokeCommand(t, app, "",
			"--start-dir", source, "init", "--dedicated-repo", target, "--create",
			"--source-repo", source, "--source-key", "source", "--confirm", "--json")
		if invocation.err == nil {
			t.Fatal("unexpected-path git init failure unexpectedly succeeded")
		}
		envelope := decodeEnvelope(t, invocation.stdout)
		if !envelope.Partial || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "init.repository_cleanup_required" {
			t.Fatalf("recovery envelope = %#v", envelope)
		}
		if content, err := os.ReadFile(filepath.Join(target, "foreign.txt")); err != nil || string(content) != "preserve\n" {
			t.Fatalf("foreign recovery bytes changed: %q err=%v", content, err)
		}
		if info, err := os.Stat(filepath.Join(target, ".git")); err != nil || !info.IsDir() {
			t.Fatalf("partial .git was removed despite refused cleanup: info=%v err=%v", info, err)
		}
	})

	t.Run("target appearance after review is stale", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		target := filepath.Join(base, "raced-exp")
		app := deterministicApp(t)
		app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
		app.Prompter = &safetyScriptedPrompter{
			answers: []string{"no", "CREATE"},
			before: func(request PromptRequest) {
				if strings.Contains(request.Label, "Create dedicated repository") {
					if err := os.Mkdir(target, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			},
		}
		invocation := invokeCommand(t, app, "", "--start-dir", source, "init",
			"--dedicated-repo", target, "--create", "--source-repo", source, "--source-key", "source", "--name", "Raced")
		if !errors.Is(invocation.err, ErrGuidedPlanStale) {
			t.Fatalf("target race error = %v", invocation.err)
		}
		entries, err := os.ReadDir(target)
		if err != nil || len(entries) != 0 {
			t.Fatalf("stale target was mutated: entries=%v err=%v", entries, err)
		}
	})

	t.Run("nested missing target refused", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		target := filepath.Join(source, "private-exp")
		app := deterministicApp(t, "01a0b330-0000-7001-8000-000000000001")
		invocation := invokeCommand(t, app, "",
			"--start-dir", source, "init", "--dedicated-repo", target, "--create",
			"--source-repo", source, "--source-key", "source", "--confirm", "--json")
		if invocation.err == nil {
			t.Fatal("nested dedicated target unexpectedly succeeded")
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("nested target was created: %v", err)
		}
	})

	t.Run("nested exact Git target refused", func(t *testing.T) {
		base, _ := externalTestRoots(t)
		source := initExternalGitRepository(t, filepath.Join(base, "source"))
		externalGit(t, source, "remote", "add", "origin", "https://example.com/org/source.git")
		outer := initExternalGitRepository(t, filepath.Join(base, "outer"))
		target := initExternalGitRepository(t, filepath.Join(outer, "inner"))
		app := deterministicApp(t)
		invocation := invokeCommand(t, app, "",
			"--start-dir", source, "init", "--dedicated-repo", target,
			"--source-repo", source, "--source-key", "source", "--confirm", "--json")
		if invocation.err == nil {
			t.Fatal("nested exact Git target unexpectedly succeeded")
		}
		if _, err := os.Lstat(filepath.Join(target, "experiments")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("nested exact target was mutated: %v", err)
		}
	})
}

func TestDefaultPrompterRefusesBufferedStreamsWithoutInjectedCheck(t *testing.T) {
	reader := &countingEOFReader{}
	app := NewApp(t.Context(), reader, io.Discard, io.Discard)
	_, err := app.Prompter.Ask(t.Context(), PromptRequest{Label: "Must not read"})
	if !errors.Is(err, ErrInvalidUsage) || reader.calls != 0 {
		t.Fatalf("buffered prompt error=%v reads=%d", err, reader.calls)
	}
}

func diagnosticCodePresent(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func requireHumanCommandSuccess(t *testing.T, invocation commandInvocation) {
	t.Helper()
	if invocation.err != nil {
		t.Fatalf("command error = %v, stdout=%q stderr=%q", invocation.err, invocation.stdout, invocation.stderr)
	}
}

func enableDeterministicWizard(app *App) {
	app.IsInteractive = func(io.Reader, io.Writer) bool { return true }
}

type safetyScriptedPrompter struct {
	answers []string
	before  func(PromptRequest)
	index   int
}

func (prompter *safetyScriptedPrompter) Ask(_ context.Context, request PromptRequest) (string, error) {
	if prompter.before != nil {
		prompter.before(request)
	}
	if prompter.index >= len(prompter.answers) {
		return "", ErrPromptCanceled
	}
	answer := prompter.answers[prompter.index]
	prompter.index++
	return answer, nil
}
