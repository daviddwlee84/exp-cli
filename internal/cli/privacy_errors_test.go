package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/skill"
)

func TestHumanValidateDiagnosticsUseJSONSafetyBoundary(t *testing.T) {
	const canary = "cli-human-validate-canary-84af"
	inventory := &record.Inventory{Diagnostics: []record.Diagnostic{{
		Path:    "/tmp/auth_value/" + canary,
		Code:    "record.unknown_field",
		Message: `unknown field "session_id" with password=` + canary,
	}}}
	human := renderValidationHuman(inventory)
	machine := safeDiagnostics(convertRecordDiagnostics(inventory.Diagnostics))
	if strings.Contains(human, canary) || !strings.Contains(human, "[REDACTED]") {
		t.Fatalf("human validation diagnostic leaked: %q", human)
	}
	if len(machine) != 1 || !strings.Contains(machine[0].Path, "[REDACTED]") || !strings.Contains(machine[0].Message, "[REDACTED]") {
		t.Fatalf("machine validation diagnostic was not equivalently sanitized: %#v", machine)
	}
}

func TestHumanOutputAndRootStderrUseCommonDiagnosticRedaction(t *testing.T) {
	const canary = "cli-human-output-canary-a238"
	var output bytes.Buffer
	app := NewApp(t.Context(), nil, &output, nil)
	if err := commandSuccess(app, false, "test", struct{}{}, false, nil, "Result at /tmp/session_id/"+canary+"\n"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), canary) || !strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("central human output leaked: %q", output.String())
	}

	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, []string{"/tmp/auth_value/" + canary})
	if code != 1 {
		t.Fatalf("Execute() code = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), canary) || !strings.Contains(stderr.String(), "[REDACTED]") {
		t.Fatalf("root stderr leaked: %q", stderr.String())
	}
}

func TestCLIErrorBoundariesRedactUnknownJSONFieldsAndStartDirectories(t *testing.T) {
	const canary = "cli-boundary-canary-6d91"

	t.Run("unknown JSON field", func(t *testing.T) {
		request := `{"schema_version":"exp.request.plan-add/v1","title":"Plan","priority":"P1","effort":"S","expected_payoff":{"summary":"Gain","metric":"score","unit":"score"},"auth_value=` + canary + `":true}`
		invocation := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), request, "plan", "add", "--input", "-", "--json")
		if invocation.err == nil {
			t.Fatal("request with unknown field succeeded")
		}
		assertSafeSingleFailureEnvelope(t, invocation, canary)
	})

	t.Run("untrusted start directory", func(t *testing.T) {
		sentinel := errors.New("discover failed: auth_value=" + canary)
		app := NewApp(t.Context(), nil, nil, nil)
		app.DiscoverProject = func(_ context.Context, start string) (*project.Info, error) {
			return nil, fmt.Errorf("inspect %s: %w", start, sentinel)
		}
		invocation := invokeCommand(t, app, "", "--start-dir", "/tmp/session_id/"+canary, "validate", "--json")
		if !errors.Is(invocation.err, sentinel) {
			t.Fatalf("returned error lost wrapped identity: %v", invocation.err)
		}
		assertSafeSingleFailureEnvelope(t, invocation, canary)
	})
}

func TestExecuteRedactsPreRunErrorsAndKeepsOneEnvelope(t *testing.T) {
	const canary = "cli-prerun-canary-719a"
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, []string{"validate", "--json=auth_value=" + canary})
	if code != 1 {
		t.Fatalf("Execute() code = %d, want 1", code)
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, canary) || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("pre-run error was not safely redacted: %q", combined)
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("machine stdout is not exactly one envelope: %q", stdout.String())
	}
	envelope := decodeEnvelope(t, stdout.String())
	if envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "command.invalid_usage" {
		t.Fatalf("pre-run envelope = %#v", envelope)
	}
}

func TestSkillPublicationFailureMapsToDurabilityUncertain(t *testing.T) {
	const canary = "cli-skill-publication-canary-c438"
	cause := errors.New("directory sync failed: password=" + canary)
	publication := &skill.PublicationError{
		Path:      "/tmp/auth_value/" + canary,
		Published: true,
		Err:       cause,
	}
	diagnostics := diagnosticsForError(publication)
	if len(diagnostics) != 1 || diagnostics[0].Code != "publication.durability_uncertain" {
		t.Fatalf("publication diagnostics = %#v", diagnostics)
	}
	safeErr := safeCLIError(publication)
	combined := diagnostics[0].Message + diagnostics[0].Path + safeErr.Error() + fmt.Sprintf("%#v", safeErr)
	if strings.Contains(combined, canary) || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("publication diagnostics leaked canary: %q", combined)
	}
	if !errors.Is(safeCLIError(publication), cause) {
		t.Fatal("safe CLI error lost the publication cause")
	}
}

func TestOuterSkillPublicationErrorTakesPrecedenceOverNestedRecordError(t *testing.T) {
	nested := &record.PublicationError{
		Stage:     record.StageDirSync,
		Published: true,
		Err:       errors.New("nested publication failure"),
	}
	outer := &skill.PublicationError{
		Path:      "references/methodology.md",
		Published: true,
		Err:       nested,
	}
	published, subject, path := publishedFailure(outer)
	if !published || subject != "skill" || path != outer.Path {
		t.Fatalf("publishedFailure() = %t, %q, %q", published, subject, path)
	}
	diagnostics := diagnosticsForError(outer)
	if len(diagnostics) != 1 || diagnostics[0].Path != outer.Path || !strings.Contains(diagnostics[0].Message, "skill bytes") {
		t.Fatalf("nested skill publication diagnostics = %#v", diagnostics)
	}
}

func TestSkillPublicationErrorUsesDurabilityDiagnosticInCommandEnvelope(t *testing.T) {
	const canary = "cli-skill-envelope-canary-d529"
	app := NewApp(t.Context(), nil, nil, nil)
	app.ResolveDefaultSkillDir = func() (string, error) { return "/tmp/exp-skill", nil }
	app.InstallSkill = func(context.Context, string, bool) (skill.InstallResult, error) {
		return skill.InstallResult{Dir: "/tmp/exp-skill", Changed: true, Written: []string{"SKILL.md"}}, &skill.PublicationError{
			Path:      "/tmp/auth_value/" + canary,
			Published: true,
			Err:       errors.New("sync failed: databasePassword=" + canary),
		}
	}
	invocation := invokeCommand(t, app, "", "skill", "install", "--json")
	if invocation.err == nil {
		t.Fatal("skill publication failure unexpectedly succeeded")
	}
	assertSafeSingleFailureEnvelope(t, invocation, canary)
	envelope := decodeEnvelope(t, invocation.stdout)
	if !envelope.Partial || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "publication.durability_uncertain" {
		t.Fatalf("skill publication envelope = %#v", envelope)
	}
}

func assertSafeSingleFailureEnvelope(t *testing.T, invocation commandInvocation, canary string) {
	t.Helper()
	combined := invocation.stdout + invocation.stderr + invocation.err.Error()
	if strings.Contains(combined, canary) || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("CLI boundary leaked canary: %q", combined)
	}
	if strings.Count(invocation.stdout, "\n") != 1 {
		t.Fatalf("machine stdout is not exactly one envelope: %q", invocation.stdout)
	}
	envelope := decodeEnvelope(t, invocation.stdout)
	if envelope.OK || len(envelope.Diagnostics) == 0 {
		t.Fatalf("failure envelope = %#v", envelope)
	}
}
