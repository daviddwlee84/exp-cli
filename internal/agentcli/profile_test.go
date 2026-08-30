package agentcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

func TestConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.toml")
	content := `schema = "exp.agents/v1"
[roles]
ranker = "fake"
[profiles.fake]
executable = "fake-agent"
args = ["{output_file}"]
output = "output_file_json"
surprise = true
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestRunnerUsesArgumentArrayAndStrictJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	directory := t.TempDir()
	executable := filepath.Join(directory, "fake-agent")
	script := `#!/bin/sh
IFS= read -r prompt
[ "$prompt" = "rank these plans" ] || exit 9
printf '%s' '{"winner":"challenger","confidence":0.9}' > "$1"
`
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stdin := true
	runner := Runner{
		Config: Config{
			Schema: ConfigSchema,
			Roles:  map[string]string{"ranker": "fake"},
			Profiles: map[string]Profile{"fake": {
				Executable: "fake-agent", Args: []string{"{output_file}"}, Output: OutputFileJSON,
				StdinPrompt: &stdin, Timeout: "20s",
			}},
		},
		LookupBinary: func(name string) (string, error) { return executable, nil },
		TempRoot:     directory,
	}
	result, err := runner.Run(t.Context(), Request{
		Role: "ranker", Prompt: []byte("rank these plans"), CWD: directory,
		Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile != "fake" || string(result.Output) != `{"confidence":0.9,"winner":"challenger"}` {
		t.Fatalf("result = %#v", result)
	}
	if result.Command.Executable != executable || len(result.Command.Argv) != 1 {
		t.Fatalf("command = %#v", result.Command)
	}
}

func TestRunnerRejectsTrailingJSON(t *testing.T) {
	if _, err := normalizeJSONDocument([]byte(`{} {}`)); err == nil {
		t.Fatal("expected trailing JSON to fail")
	}
}

func TestRunnerValidatesOutputSchemaWithoutExternalLoads(t *testing.T) {
	schema := []byte(`{"type":"object","required":["winner"],"additionalProperties":false,"properties":{"winner":{"type":"string"}}}`)
	if err := validateJSONSchema(schema, []byte(`{"confidence":0.9}`)); err == nil {
		t.Fatal("schema-invalid agent output was accepted")
	}
	if err := validateJSONSchema([]byte(`{"$ref":"https://example.invalid/schema.json"}`), []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("external schema load error = %v", err)
	}
}

func TestRunnerCompilesSchemaBeforeAgentExecution(t *testing.T) {
	invoked := false
	runner := Runner{
		Config: Config{Schema: ConfigSchema, Roles: map[string]string{"ranker": "fake"}, Profiles: map[string]Profile{"fake": {
			Executable: "fake-agent", Args: []string{"{output_file}"}, Output: OutputFileJSON,
		}}},
		LookupBinary: func(string) (string, error) { return "/bin/false", nil },
		Invoker: execx.InvokerFunc(func(context.Context, execx.CommandSpec) (execx.Result, error) {
			invoked = true
			return execx.Result{}, errors.New("must not run")
		}),
	}
	_, err := runner.Run(t.Context(), Request{Role: "ranker", Prompt: []byte("rank"), CWD: t.TempDir(), Schema: json.RawMessage(`{"$ref":"https://example.invalid/schema.json"}`)})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("schema compile error = %v", err)
	}
	if invoked {
		t.Fatal("agent executed before invalid schema was rejected")
	}
}

func TestRunnerRejectsSecretFromOutputFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	directory := t.TempDir()
	executable := filepath.Join(directory, "secret-agent")
	script := `#!/bin/sh
printf '{"answer":"%s"}' "$EXP_AGENT_TEST_SECRET" > "$1"
`
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "canary-value-that-must-not-escape"
	runner := Runner{
		Config: Config{Schema: ConfigSchema, Roles: map[string]string{"ranker": "fake"}, Profiles: map[string]Profile{"fake": {
			Executable: "secret-agent", Args: []string{"{output_file}"}, Output: OutputFileJSON,
			SecretEnv: []string{"EXP_AGENT_TEST_SECRET"}, Timeout: "20s",
		}}},
		LookupBinary: func(string) (string, error) { return executable, nil },
		LookupEnv: func(name string) (string, bool) {
			if name == "EXP_AGENT_TEST_SECRET" {
				return secret, true
			}
			return os.LookupEnv(name)
		},
		TempRoot: directory,
	}
	result, err := runner.Run(t.Context(), Request{Role: "ranker", Prompt: []byte("rank"), CWD: directory, Schema: json.RawMessage(`{"type":"object"}`)})
	if err == nil || !strings.Contains(err.Error(), "protected secret") {
		t.Fatalf("secret output error = %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(string(result.Output), secret) || strings.Contains(result.Process.Stdout, secret) || strings.Contains(result.Process.Stderr, secret) {
		t.Fatal("secret escaped through agent result")
	}
}

func TestProfileRequiresWholeArgumentPlaceholders(t *testing.T) {
	profile := Profile{Executable: "codex", Args: []string{"--schema={schema_file}"}, Output: OutputStdoutJSON}
	if err := profile.Validate(); err == nil {
		t.Fatal("expected embedded placeholder to fail")
	}
}

func TestProfileRequiresSensitiveInheritedEnvironmentToUseSecretEnv(t *testing.T) {
	profile := Profile{Executable: "codex", Args: []string{"run"}, Output: OutputStdoutJSON, AllowedEnv: []string{"OPENAI_API_KEY"}}
	if err := profile.Validate(); err == nil || !strings.Contains(err.Error(), "secret_env") {
		t.Fatalf("sensitive allowed_env error = %v", err)
	}
}

func TestProfileOutputModeMatchesOutputFilePlaceholder(t *testing.T) {
	if err := (Profile{Executable: "agent", Args: []string{"run"}, Output: OutputFileJSON}).Validate(); err == nil {
		t.Fatal("file output without {output_file} was accepted")
	}
	if err := (Profile{Executable: "agent", Args: []string{"{output_file}"}, Output: OutputStdoutJSON}).Validate(); err == nil {
		t.Fatal("stdout output with {output_file} was accepted")
	}
}
