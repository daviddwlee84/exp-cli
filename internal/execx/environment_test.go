package execx_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

func TestEnvironmentMetadataIsStableAndValueFree(t *testing.T) {
	const canary = "execx-environment-canary-4a8f"
	environment, err := execx.NewEnvironment(
		[]string{"PATH", "LANG"},
		execx.Bind("MODE", "test"),
		execx.BindSecret("API_TOKEN", canary),
		execx.BindSecretFromEnv("TRACKER_PASSWORD", "EXP_TEST_PARENT_PASSWORD"),
	)
	if err != nil {
		t.Fatalf("NewEnvironment() error = %v", err)
	}

	variables := environment.Variables()
	gotNames := make([]string, len(variables))
	for index, variable := range variables {
		gotNames[index] = variable.Name
	}
	wantNames := []string{"API_TOKEN", "LANG", "MODE", "PATH", "TRACKER_PASSWORD"}
	if fmt.Sprint(gotNames) != fmt.Sprint(wantNames) {
		t.Fatalf("Variables() names = %v, want %v", gotNames, wantNames)
	}
	if !variables[0].Sensitive || variables[2].Sensitive || !variables[4].Sensitive {
		t.Fatalf("Variables() sensitivity = %+v", variables)
	}

	for _, rendered := range []string{
		environment.String(),
		fmt.Sprintf("%#v", environment),
		mustJSON(t, environment),
	} {
		if strings.Contains(rendered, canary) || strings.Contains(rendered, "EXP_TEST_PARENT_PASSWORD") {
			t.Fatalf("environment rendering leaked secret material: %q", rendered)
		}
	}
}

func TestEnvironmentValidationRejectsAmbiguousPolicy(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		allowed  []string
		bindings []execx.Binding
	}{
		{name: "invalid allowed name", allowed: []string{"BAD=NAME"}},
		{name: "duplicate allowed name", allowed: []string{"PATH", "PATH"}},
		{name: "invalid binding name", bindings: []execx.Binding{execx.Bind("BAD-NAME", "x")}},
		{name: "duplicate binding", bindings: []execx.Binding{execx.Bind("MODE", "a"), execx.Bind("MODE", "b")}},
		{name: "invalid source", bindings: []execx.Binding{execx.BindSecretFromEnv("TOKEN", "BAD=SOURCE")}},
		{name: "nul value", bindings: []execx.Binding{execx.Bind("MODE", "bad\x00value")}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := execx.NewEnvironment(testCase.allowed, testCase.bindings...); err == nil {
				t.Fatal("NewEnvironment() succeeded, want validation error")
			}
		})
	}
}

func TestEnvironmentRejectsInvalidUTF8LiteralValues(t *testing.T) {
	invalid := "secret-prefix\xffsecret-suffix"
	if _, err := execx.NewEnvironment(nil, execx.BindSecret("DATABASE_PASSWORD", invalid)); err == nil {
		t.Fatal("NewEnvironment accepted an invalid-UTF-8 secret value")
	} else if strings.Contains(err.Error(), "secret-prefix") || strings.Contains(err.Error(), "secret-suffix") {
		t.Fatalf("validation error leaked invalid secret fragments: %v", err)
	}
}

func TestRedactorHandlesKnownStructuresAndBoundaries(t *testing.T) {
	const canary = "execx-redaction-canary-17dd"
	redactor := execx.NewRedactor(canary)

	text := strings.Join([]string{
		"Authorization: Bearer " + canary,
		"Cookie: session=" + canary,
		"password=" + canary,
		"https://user:" + canary + "@example.invalid/path",
		"plain=" + canary,
	}, "\n")
	redacted := redactor.Text(text)
	if strings.Contains(redacted, canary) {
		t.Fatalf("Text() leaked canary: %q", redacted)
	}
	if !strings.Contains(redacted, execx.Redacted) {
		t.Fatalf("Text() did not mark redaction: %q", redacted)
	}

	argv := []string{
		"--label", "two words",
		"--token", canary,
		"--api-key=" + canary,
		"--header", "Authorization: Bearer " + canary,
		"--env", "PASSWORD=" + canary,
		"literal;$HOME",
	}
	safe := redactor.Argv(argv)
	if len(safe) != len(argv) {
		t.Fatalf("Argv() changed boundaries: got %d args, want %d", len(safe), len(argv))
	}
	if safe[1] != "two words" || safe[len(safe)-1] != "literal;$HOME" {
		t.Fatalf("Argv() changed non-sensitive arguments: %q", safe)
	}
	if strings.Contains(strings.Join(safe, "\x00"), canary) {
		t.Fatalf("Argv() leaked canary: %q", safe)
	}

	if execx.SensitiveName("MONKEY") {
		t.Fatal("SensitiveName(MONKEY) = true; matching must be token based")
	}
	for _, name := range []string{"Authorization", "AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "SLURM_JWT", "api-token", "client_secret"} {
		if !execx.SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = false", name)
		}
	}

	bounded, truncated := redactor.SafeText(strings.Repeat("x", 100)+canary, 32)
	if !truncated || len(bounded) > 32 || strings.Contains(bounded, canary) {
		t.Fatalf("SafeText() = %q, %v", bounded, truncated)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(encoded)
}
