package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

type contextKey string

func TestRootInjectsContextStreamsAndProductThesis(t *testing.T) {
	ctx := context.WithValue(t.Context(), contextKey("request"), "test-value")
	var stdin, stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(ctx, &stdin, &stdout, &stderr)

	if !root.SilenceUsage || !root.SilenceErrors {
		t.Fatal("root must silence Cobra usage and errors")
	}
	if got := root.Context().Value(contextKey("request")); got != "test-value" {
		t.Fatalf("injected context value = %v", got)
	}
	if root.InOrStdin() != &stdin || root.OutOrStdout() != &stdout || root.ErrOrStderr() != &stderr {
		t.Fatal("root did not retain injected streams")
	}

	root.SetArgs([]string{"--help"})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext(--help) error = %v", err)
	}
	help := stdout.String()
	for _, phrase := range []string{
		"Git-native research control plane, not another tracker or scheduler",
		"delegated to the upstream tools",
		"walking-skeleton build",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help does not contain %q:\n%s", phrase, help)
		}
	}
}

func TestBareRootShowsOnlyApprovedFunctionalCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("Execute() code = %d, stderr = %q", code, stderr.String())
	}
	help := stdout.String()
	if !strings.Contains(help, "Usage:") || !strings.Contains(help, "Available Commands:") {
		t.Fatalf("bare root did not render command help:\n%s", help)
	}
	for _, command := range []string{"context", "doctor", "init", "plan", "render", "skill", "validate"} {
		if !strings.Contains(help, "  "+command) {
			t.Errorf("bare root help is missing %q:\n%s", command, help)
		}
	}
	for _, deferred := range []string{"experiment", "finding", "run", "provider", "migrate"} {
		if strings.Contains(help, "  "+deferred+" ") {
			t.Errorf("bare root advertises deferred command %q:\n%s", deferred, help)
		}
	}
}

func TestExecuteCentralizesErrorsOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, []string{"unknown"})
	if code != 1 {
		t.Fatalf("Execute() code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.HasPrefix(got, "exp: unknown command") {
		t.Fatalf("stderr = %q", got)
	}
	if strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("SilenceUsage failed: %q", stderr.String())
	}
}

func TestExecuteEmitsExactlyOneJSONEnvelopeForPreRunFailures(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		command string
		code    string
	}{
		{name: "unknown command", args: []string{"unknown", "--json"}, command: "exp", code: "command.invalid_usage"},
		{name: "positional argument", args: []string{"validate", "--json", "extra"}, command: "validate", code: "command.invalid_usage"},
		{name: "unknown sibling flag before JSON", args: []string{"validate", "--dir", "--json"}, command: "validate", code: "command.invalid_usage"},
		{name: "unknown flag", args: []string{"validate", "--json", "--bogus"}, command: "validate", code: "command.invalid_usage"},
		{name: "invalid JSON flag value", args: []string{"validate", "--json=maybe"}, command: "validate", code: "command.invalid_usage"},
		{name: "invalid payload flag value", args: []string{"plan", "add", "--json", "--payoff-estimate", "nope"}, command: "plan add", code: "command.invalid_usage"},
		{name: "handler failure is not duplicated", args: []string{"validate", "--json"}, command: "validate", code: "command.failed"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, testCase.args)
			if code != 1 {
				t.Fatalf("Execute() code = %d, want stable failure status 1; stdout=%q", code, stdout.String())
			}
			envelope := decodeEnvelope(t, stdout.String())
			if envelope.OK || envelope.Command != testCase.command || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != testCase.code {
				t.Fatalf("failure envelope = %#v", envelope)
			}
			if strings.Contains(stdout.String(), "Usage:") || strings.Count(stdout.String(), "\n") != 1 {
				t.Fatalf("machine stdout is not one pure envelope: %q", stdout.String())
			}
			if stderr.Len() == 0 {
				t.Fatal("terminal error was not reported to stderr")
			}
		})
	}
}
