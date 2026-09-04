package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
)

func TestColorModeSelectionUsesTerminalAndExplicitOverrides(t *testing.T) {
	var pipeLike bytes.Buffer
	if styleForWriter(&pipeLike, colorAuto).enabled {
		t.Fatal("auto color enabled for a non-file writer")
	}
	if !styleForWriter(&pipeLike, colorAlways).enabled {
		t.Fatal("explicit always did not color a non-file writer")
	}
	if styleForWriter(&pipeLike, colorNever).enabled {
		t.Fatal("never color enabled styling")
	}

	terminal := func(*os.File) bool { return true }
	if !styleForWriterWithTerminal(os.Stdout, colorAuto, terminal).enabled {
		t.Fatal("auto color did not enable for terminal output")
	}
	t.Setenv("NO_COLOR", "1")
	if styleForWriterWithTerminal(os.Stdout, colorAuto, terminal).enabled {
		t.Fatal("auto color ignored NO_COLOR")
	}
	if !styleForWriterWithTerminal(os.Stdout, colorAlways, terminal).enabled {
		t.Fatal("explicit always did not override NO_COLOR")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if styleForWriterWithTerminal(os.Stdout, colorAuto, terminal).enabled {
		t.Fatal("auto color ignored TERM=dumb")
	}
	if !styleForWriterWithTerminal(os.Stdout, colorAlways, terminal).enabled {
		t.Fatal("explicit always did not override TERM=dumb")
	}

	for _, mode := range []string{colorAuto, colorAlways, colorNever} {
		if err := validateColorMode(mode); err != nil {
			t.Errorf("validateColorMode(%q) = %v", mode, err)
		}
	}
	for _, mode := range []string{"", "rainbow"} {
		if err := validateColorMode(mode); err == nil {
			t.Errorf("invalid color mode %q was accepted", mode)
		}
	}
}

func TestSemanticRolesPreserveTheirExactPlainText(t *testing.T) {
	style := cliStyle{enabled: true}
	roles := []string{
		style.title("title"), style.header("header"), style.prompt("prompt"), style.label("label"),
		style.success("success"), style.warning("warning"), style.danger("danger"), style.review("review"),
		style.code("code"), style.domainState("succeeded", "succeeded"), style.readinessState("missing"),
	}
	for _, rendered := range roles {
		if !strings.Contains(rendered, "\x1b[") {
			t.Errorf("semantic role is not styled: %q", rendered)
		}
		plain := stripANSI(rendered)
		if plain == "" || strings.Contains(plain, "\x1b[") {
			t.Errorf("semantic role did not retain plain text: %q", rendered)
		}
	}
}

func TestColoredCommandOutputEqualsExactSanitizedPlainOutput(t *testing.T) {
	const canary = "semantic-color-secret-cf1b"
	human := "STATE  DETAIL\nqueued  password=" + canary + " \x1b[31muntrusted\x1b[0m\n"
	run := func(mode string) string {
		var output bytes.Buffer
		app := NewApp(t.Context(), nil, &output, io.Discard)
		app.colorMode = mode
		if err := commandSuccess(app, false, "test", struct{}{}, false, nil, human); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	plain := run(colorNever)
	colored := run(colorAlways)
	if strings.Contains(plain+colored, canary) || !strings.Contains(plain, "[REDACTED]") {
		t.Fatalf("color path bypassed redaction: plain=%q colored=%q", plain, colored)
	}
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain output retained untrusted ANSI: %q", plain)
	}
	if !strings.Contains(colored, ansiYellow+"queued") {
		t.Fatalf("domain state was not semantically colored: %q", colored)
	}
	if got := stripANSI(colored); got != plain {
		t.Fatalf("stripping color did not recover exact plain bytes\n got: %q\nwant: %q", got, plain)
	}
}

func TestCobraHelpColorMatchesPlainBytes(t *testing.T) {
	run := func(mode string) string {
		invocation := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", "--color", mode, "--help")
		if invocation.err != nil || invocation.stderr != "" {
			t.Fatalf("help mode %s: error=%v stderr=%q", mode, invocation.err, invocation.stderr)
		}
		return invocation.stdout
	}
	plain := run(colorNever)
	colored := run(colorAlways)
	if !strings.Contains(colored, ansiBold+ansiCyan+"Usage:") {
		t.Fatalf("help heading was not colored:\n%s", colored)
	}
	if got := stripANSI(colored); got != plain {
		t.Fatal("colored Cobra help changed its plain bytes")
	}
	automatic := run(colorAuto)
	if strings.Contains(automatic, "\x1b[") || automatic != plain {
		t.Fatal("auto color styled a non-terminal buffer")
	}
}

func TestDisplayWidthTableHandlesCJKCombiningAndANSI(t *testing.T) {
	table := newHumanTable("NAME", "STATE")
	table.Add("專案", "ready")
	table.Add("é", "missing")
	plain := mustRenderTable(table)
	colored := renderSemanticHuman(safeHumanOutput(plain), cliStyle{enabled: true})
	if got := stripANSI(colored); got != plain {
		t.Fatalf("colored table changed plain alignment\nplain:\n%s\ncolored:\n%s", plain, got)
	}
	lines := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("table lines = %d, want 3: %q", len(lines), plain)
	}
	for index, state := range []string{"ready", "missing"} {
		line := lines[index+1]
		column := strings.Index(line, state)
		if column < 0 || displayWidth(line[:column]) != 6 {
			t.Errorf("row %q state starts at display column %d, want 6", line, displayWidth(line[:max(column, 0)]))
		}
	}
	if displayWidth("é") != 1 || displayWidth("專案") != 4 {
		t.Fatalf("display widths: combining=%d CJK=%d", displayWidth("é"), displayWidth("專案"))
	}

	styled := cliStyle{enabled: true}.warning("é中文-alpha")
	truncated := truncateDisplay(styled, 6)
	if got := stripANSI(truncated); got != "é中文…" || displayWidth(truncated) != 6 {
		t.Fatalf("truncateDisplay = %q (plain=%q width=%d)", truncated, got, displayWidth(truncated))
	}
	if !strings.HasSuffix(truncated, ansiReset) {
		t.Fatalf("truncated ANSI text does not reset styling: %q", truncated)
	}
}

func TestHumanTableAndStyledOutputPreserveWriterFailures(t *testing.T) {
	sentinel := errors.New("writer failed")
	table := newHumanTable("A", "B")
	table.Add("one", "two")
	if err := table.Render(writerFunc(func([]byte) (int, error) { return 0, sentinel })); !errors.Is(err, sentinel) {
		t.Fatalf("table.Render() error = %v, want sentinel", err)
	}

	app := NewApp(t.Context(), nil, writerFunc(func([]byte) (int, error) { return 0, io.ErrClosedPipe }), io.Discard)
	app.colorMode = colorAlways
	if err := commandSuccess(app, false, "test", struct{}{}, false, nil, "ready\n"); err != nil {
		t.Fatalf("styled successful broken pipe became a command failure: %v", err)
	}
}

func TestWarningSanitizesBeforeStderrColor(t *testing.T) {
	const canary = "warning-secret-80e6"
	var stderr bytes.Buffer
	app := NewApp(t.Context(), nil, io.Discard, &stderr)
	app.colorMode = colorAlways
	if err := app.Warnf("password=%s", canary); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), canary) || !strings.Contains(stderr.String(), ansiYellow+"exp: warning:") {
		t.Fatalf("warning was leaked or unstyled: %q", stderr.String())
	}
	if got := stripANSI(stderr.String()); got != "exp: warning: password=[REDACTED]\n" {
		t.Fatalf("warning plain bytes = %q", got)
	}
	stderr.Reset()
	app.machineOutput = true
	if err := app.Warnf("state=%s", "untrusted"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "\x1b[") {
		t.Fatalf("machine-mode warning contains ANSI: %q", stderr.String())
	}
}

func TestExecuteColorsHumanStderrButNeverJSONFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, []string{"--color", "always", "pla"})
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "\x1b[") {
		t.Fatalf("colored failure: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	plainError := stripANSI(stderr.String())
	if !strings.Contains(plainError, `unknown command "pla"`) || !strings.Contains(plainError, "Did you mean this?") || !strings.Contains(plainError, `Run "exp --help"`) {
		t.Fatalf("colored failure lost recovery text: %q", plainError)
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, []string{"--color", "always", "pla", "--json"})
	if code != 1 {
		t.Fatalf("JSON failure code = %d", code)
	}
	if strings.Contains(stdout.String()+stderr.String(), "\x1b[") {
		t.Fatalf("JSON invocation received ANSI: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("JSON failure did not emit exactly one envelope: %q", stdout.String())
	}
	envelope := decodeEnvelope(t, stdout.String())
	if envelope.OK || envelope.SchemaVersion != MachineSchema {
		t.Fatalf("JSON failure envelope = %#v", envelope)
	}
}

func TestColorFlagRejectsEmptyValueEvenWhenHelpShortCircuitsHooks(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, []string{"--color=", "--help"})
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid --color value") {
		t.Fatalf("empty --color: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExecuteErrorColorUsesParsedFlagsNotConsumedRawValues(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		args     []string
		wantANSI bool
	}{
		{
			name:     "always token consumed by start-dir",
			args:     []string{"--color", "never", "validate", "--start-dir", "--color=always"},
			wantANSI: false,
		},
		{
			name:     "never token consumed by start-dir",
			args:     []string{"--color", "always", "validate", "--start-dir", "--color=never"},
			wantANSI: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, testCase.args)
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if got := strings.Contains(stderr.String(), "\x1b["); got != testCase.wantANSI {
				t.Fatalf("ANSI=%t, want %t: %q", got, testCase.wantANSI, stderr.String())
			}
		})
	}
}

func TestEffectiveConfigColorAppliesUnlessCLIOverridesIt(t *testing.T) {
	now := time.Date(2026, time.September, 4, 16, 0, 0, 0, time.UTC)
	run := func(args ...string) string {
		resolved, inventory, _ := newUITestSnapshot(t, now)
		effective := config.Builtins()
		effective.UI.Color = config.ColorAlways
		resolved.Config = &config.Result{Effective: effective}
		app := NewApp(t.Context(), nil, nil, nil)
		app.Now = func() time.Time { return now }
		app.Getwd = func() (string, error) { return resolved.InvocationDir, nil }
		app.ResolveWorkspace = &uiSnapshotResolver{resolved: resolved, inventory: inventory}
		invocation := invokeCommand(t, app, "", append(args, "config", "show")...)
		if invocation.err != nil || invocation.stderr != "" {
			t.Fatalf("%v: error=%v stderr=%q", args, invocation.err, invocation.stderr)
		}
		return invocation.stdout
	}

	configured := run()
	if !strings.Contains(configured, "\x1b[") || stripANSI(configured) == configured {
		t.Fatalf("effective ui.color=always was ignored: %q", configured)
	}
	overridden := run("--color", "never")
	if strings.Contains(overridden, "\x1b[") || stripANSI(configured) != overridden {
		t.Fatalf("explicit --color never did not override config: configured=%q overridden=%q", configured, overridden)
	}
}

func TestRawManifestBytesBypassColorRenderer(t *testing.T) {
	var output bytes.Buffer
	app := NewApp(t.Context(), nil, &output, io.Discard)
	app.colorMode = colorAlways
	encoded := []byte("{\"schema_version\":\"exp.champion-manifest/v3\",\"state\":\"ready\"}")
	if err := writeChampionManifestOutput(app, false, championManifest{}, nil, encoded); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), string(encoded)+"\n"; got != want || strings.Contains(got, "\x1b[") {
		t.Fatalf("raw manifest output = %q, want exact %q", got, want)
	}
}

func TestCanonicalMarkdownBypassesColorRenderer(t *testing.T) {
	repository := newGitRepository(t)
	app := deterministicApp(t, "01a09991-0000-7001-8000-000000000001")
	created := invokeCommand(t, app, "", "--start-dir", repository, "init", "--name", "Color Study", "--json")
	if created.err != nil {
		t.Fatalf("init: %v stderr=%q", created.err, created.stderr)
	}
	show := invokeCommand(t, app, "", "--color", "always", "--start-dir", repository, "record", "show", "PROJECT.md")
	if show.err != nil {
		t.Fatalf("record show: %v stderr=%q", show.err, show.stderr)
	}
	raw := invokeCommand(t, app, "", "--color", "always", "--start-dir", repository, "record", "show", "PROJECT.md", "--raw")
	if raw.err != nil {
		t.Fatalf("record show --raw: %v stderr=%q", raw.err, raw.stderr)
	}
	if strings.Contains(show.stdout+raw.stdout, "\x1b[") || show.stdout != raw.stdout {
		t.Fatalf("canonical Markdown was styled or changed: show=%q raw=%q", show.stdout, raw.stdout)
	}
}
