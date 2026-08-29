package safex

import (
	"slices"
	"strings"
	"testing"
)

func TestSensitiveNameRecognizesCredentialFormsWithoutNLPFalsePositives(t *testing.T) {
	for _, name := range []string{
		"auth_value",
		"databasePassword",
		"session_id",
		"authorization/cookie",
		"auth-token",
		"AWS_SECRET_ACCESS_KEY",
		"mlflow.refresh_token",
	} {
		if !SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = false", name)
		}
	}
	for _, name := range []string{
		"token_count",
		"max-tokens",
		"tokenizer",
		"monkey",
		"public_key",
		"authentication_method",
	} {
		if SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = true", name)
		}
	}
}

func TestTextClassificationRedactsCredentialsAndPreservesNLPProse(t *testing.T) {
	const canary = "safex-text-canary-9f31"
	unsafe := []string{
		"auth_value=" + canary,
		"databasePassword: " + canary,
		"session_id=" + canary,
		"access%255Ftoken=" + canary,
		"Authorization: Bearer " + canary,
		"Cookie: session=" + canary,
		"Bearer " + canary,
		"Basic Zm9vOmJhcg==",
		"run --auth-token " + canary,
		"-----BEGIN PRIVATE KEY-----\n" + canary,
	}
	for _, value := range unsafe {
		if !ContainsSecretText(value) {
			t.Errorf("ContainsSecretText(%q) = false", value)
		}
		redacted := NewRedactor().Text(value)
		if strings.Contains(redacted, canary) || redacted == value {
			t.Errorf("Text(%q) = %q", value, redacted)
		}
	}

	for _, value := range []string{
		"Compare token count across tokenizers.",
		"token=[CLS]",
		"Use token=[CLS].",
		"Token: count used by the tokenizer",
		"The access token parameter must never be committed.",
		"Compare authorization mechanisms and cookie classification.",
	} {
		if ContainsSecretText(value) {
			t.Errorf("ContainsSecretText(%q) = true", value)
		}
		if got := NewRedactor().Text(value); got != value {
			t.Errorf("Text(%q) = %q", value, got)
		}
	}
}

func TestArgvClassificationCoversAttachedSeparatedAndBareSchemes(t *testing.T) {
	const canary = "safex-argv-canary-4b28"
	tests := [][]string{
		{"curl", "-H", "Authorization: Bearer " + canary},
		{"curl", "-HCookie: session=" + canary},
		{"curl", "--header=Authorization: Bearer " + canary},
		{"curl", "-u", "alice:" + canary},
		{"curl", "-ualice:" + canary},
		{"curl", "--user=alice:" + canary},
		{"runner", "--auth-token", canary},
		{"runner", "--databasePassword=" + canary},
		{"runner", "Bearer", canary},
		{"runner", "Basic " + canary},
	}
	for _, argv := range tests {
		safe := NewRedactor().Argv(argv)
		if len(safe) != len(argv) {
			t.Fatalf("Argv(%q) changed argument count", argv)
		}
		if strings.Contains(strings.Join(safe, "\x00"), canary) {
			t.Errorf("Argv(%q) leaked canary as %q", argv, safe)
		}
		if len(SensitiveArgvIndexes(argv)) == 0 {
			t.Errorf("SensitiveArgvIndexes(%q) found no sensitive argument", argv)
		}
		if !slices.Contains(SensitiveArgvValues(argv), canary) {
			t.Errorf("SensitiveArgvValues(%q) omitted canary: %q", argv, SensitiveArgvValues(argv))
		}
	}

	benign := []string{"python", "train.py", "--max-tokens", "2048", "token=[CLS]"}
	if indexes := SensitiveArgvIndexes(benign); len(indexes) != 0 {
		t.Fatalf("benign argv classified sensitive at %v", indexes)
	}
	if got := NewRedactor().Argv(benign); !slices.Equal(got, benign) {
		t.Fatalf("benign argv changed: %q", got)
	}
}

func TestDiagnosticRedactsSensitiveStructuredIdentifiers(t *testing.T) {
	const canary = "safex-diagnostic-canary-5d72"
	for _, value := range []string{
		`directory sync failed: password=` + canary,
		`unknown field request.databasePassword`,
		`unknown field "auth_value=` + canary + `"`,
		`inspect /tmp/session_id/` + canary,
		"terminal\x1b]52;c;payload\a password=" + canary,
	} {
		safe := NewRedactor().Diagnostic(value)
		if strings.Contains(safe, canary) || strings.ContainsRune(safe, '\x1b') || !strings.Contains(safe, Redacted) {
			t.Errorf("Diagnostic(%q) = %q", value, safe)
		}
	}
}

func TestPathRedactsOnlyCredentialShapedComponents(t *testing.T) {
	const canary = "safex-path-canary-731c"
	redactor := NewRedactor()
	for _, testCase := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "safe path remains exact",
			input: "/tmp/research  project/token_count/public_key/repo",
			want:  "/tmp/research  project/token_count/public_key/repo",
		},
		{
			name:  "POSIX credential component",
			input: "/tmp/auth_value=" + canary + "/repo",
			want:  "/tmp/" + Redacted + "/repo",
		},
		{
			name:  "Windows credential component",
			input: `C:\Users\databasePassword=` + canary + `\repo`,
			want:  `C:\Users\` + Redacted + `\repo`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := redactor.Path(testCase.input); got != testCase.want {
				t.Fatalf("Path(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}
