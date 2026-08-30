// Package agentcli runs configured coding/research agent CLIs through exp's
// existing argument-array subprocess boundary. It deliberately has no SDK or
// provider-specific session model.
package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	ConfigSchema       = "exp.agents/v1"
	DefaultOutputLimit = 1 << 20
)

type OutputMode string

const (
	OutputStdoutJSON OutputMode = "stdout_json"
	OutputFileJSON   OutputMode = "output_file_json"
)

type Config struct {
	Schema   string             `toml:"schema"`
	Roles    map[string]string  `toml:"roles"`
	Profiles map[string]Profile `toml:"profiles"`
}

type Profile struct {
	Executable     string     `toml:"executable"`
	Args           []string   `toml:"args"`
	Timeout        string     `toml:"timeout"`
	MaxOutputBytes int64      `toml:"max_output_bytes"`
	Output         OutputMode `toml:"output"`
	StdinPrompt    *bool      `toml:"stdin_prompt"`
	AllowedEnv     []string   `toml:"allowed_env"`
	SecretEnv      []string   `toml:"secret_env"`
	ReportedModel  string     `toml:"reported_model"`
}

type Request struct {
	Role       string
	Profile    string
	Prompt     []byte
	Schema     json.RawMessage
	CWD        string
	ExtraEnv   []execx.Binding
	OutputMode execx.OutputMode
	Stdout     io.Writer
	Stderr     io.Writer
}

type Result struct {
	Profile       string            `json:"profile"`
	ReportedModel string            `json:"reported_model,omitempty"`
	Output        json.RawMessage   `json:"output"`
	Command       execx.CommandView `json:"command"`
	Process       execx.Result      `json:"process"`
}

type Runner struct {
	Config       Config
	Invoker      execx.Invoker
	LookupBinary func(string) (string, error)
	LookupEnv    execx.LookupEnv
	TempRoot     string
}

func Load(path string) (Config, error) {
	var config Config
	metadata, err := toml.DecodeFile(path, &config)
	if err != nil {
		return Config{}, fmt.Errorf("decode agent profiles: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		return Config{}, fmt.Errorf("agent profiles contain unknown field %q", undecoded[0].String())
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	if config.Schema != ConfigSchema {
		return fmt.Errorf("agent config schema must be %q", ConfigSchema)
	}
	if len(config.Profiles) == 0 {
		return errors.New("agent config defines no profiles")
	}
	for name, profile := range config.Profiles {
		if !validName(name) {
			return fmt.Errorf("invalid agent profile name %q", name)
		}
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("agent profile %s: %w", name, err)
		}
	}
	for role, profile := range config.Roles {
		if !validName(role) {
			return fmt.Errorf("invalid agent role %q", role)
		}
		if _, found := config.Profiles[profile]; !found {
			return fmt.Errorf("agent role %s references unknown profile %q", role, profile)
		}
	}
	return nil
}

func (profile Profile) Validate() error {
	if profile.Executable == "" || filepath.Base(profile.Executable) != profile.Executable || strings.ContainsAny(profile.Executable, `/\\`) {
		return errors.New("executable must be a binary name, not a path")
	}
	if len(profile.Args) == 0 {
		return errors.New("args must not be empty")
	}
	outputPlaceholders := 0
	for _, argument := range profile.Args {
		if !utf8.ValidString(argument) || strings.ContainsRune(argument, 0) {
			return errors.New("args contain invalid text")
		}
		if strings.Contains(argument, "{") || strings.Contains(argument, "}") {
			if _, ok := allowedPlaceholders[argument]; !ok {
				return fmt.Errorf("unsupported placeholder %q; placeholders must occupy a whole argument", argument)
			}
		}
		if argument == "{output_file}" {
			outputPlaceholders++
		}
	}
	if profile.Timeout != "" {
		value, err := time.ParseDuration(profile.Timeout)
		if err != nil || value <= 0 {
			return errors.New("timeout must be a positive duration")
		}
	}
	if profile.MaxOutputBytes < 0 || profile.MaxOutputBytes > execx.MaxOutputLimit {
		return fmt.Errorf("max_output_bytes is outside 1..%d", execx.MaxOutputLimit)
	}
	if profile.Output == "" {
		profile.Output = OutputStdoutJSON
	}
	if profile.Output != OutputStdoutJSON && profile.Output != OutputFileJSON {
		return fmt.Errorf("unsupported output mode %q", profile.Output)
	}
	if profile.Output == OutputFileJSON && outputPlaceholders != 1 {
		return errors.New("output_file_json requires exactly one {output_file} argument")
	}
	if profile.Output != OutputFileJSON && outputPlaceholders != 0 {
		return errors.New("{output_file} is only valid with output_file_json")
	}
	seen := map[string]struct{}{}
	for _, name := range profile.AllowedEnv {
		if execx.SensitiveName(name) {
			return fmt.Errorf("credential-sensitive environment variable %s must use secret_env", name)
		}
	}
	for _, name := range append(append([]string{}, profile.AllowedEnv...), profile.SecretEnv...) {
		if !validEnvironmentName(name) {
			return errors.New("invalid environment variable name")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("environment variable %s is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

var allowedPlaceholders = map[string]struct{}{
	"{prompt_file}": {},
	"{schema_file}": {},
	"{schema_json}": {},
	"{output_file}": {},
	"{cwd}":         {},
}

func (runner Runner) Run(ctx context.Context, request Request) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("agent context is required")
	}
	if err := runner.Config.Validate(); err != nil {
		return Result{}, err
	}
	profileName := request.Profile
	if profileName == "" {
		profileName = runner.Config.Roles[request.Role]
	}
	profile, found := runner.Config.Profiles[profileName]
	if !found {
		return Result{}, fmt.Errorf("agent role %q has no configured profile", request.Role)
	}
	if profile.Output == OutputFileJSON {
		for _, binding := range request.ExtraEnv {
			if binding.Sensitive() {
				return Result{}, errors.New("sensitive request environment bindings are unsupported with output_file_json")
			}
		}
	}
	if !filepath.IsAbs(request.CWD) || filepath.Clean(request.CWD) != request.CWD {
		return Result{}, errors.New("agent cwd must be a clean absolute path")
	}
	if len(request.Prompt) == 0 || len(request.Prompt) > 4<<20 || !utf8.Valid(request.Prompt) {
		return Result{}, errors.New("agent prompt is empty, oversized, or invalid UTF-8")
	}
	schema, err := normalizeJSONObject(request.Schema)
	if err != nil {
		return Result{}, fmt.Errorf("agent output schema: %w", err)
	}
	compiledSchema, err := compileJSONSchema(schema)
	if err != nil {
		return Result{}, fmt.Errorf("compile agent output schema: %w", err)
	}
	lookupEnv := runner.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	secretValues := make([]string, 0, len(profile.SecretEnv))
	for _, name := range profile.SecretEnv {
		value, ok := lookupEnv(name)
		if !ok {
			return Result{}, fmt.Errorf("required agent secret environment source is not set")
		}
		if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return Result{}, errors.New("agent secret environment value is invalid")
		}
		if value != "" {
			secretValues = append(secretValues, value)
		}
	}

	lookup := runner.LookupBinary
	if lookup == nil {
		lookup = exec.LookPath
	}
	executable, err := lookup(profile.Executable)
	if err != nil {
		return Result{}, fmt.Errorf("resolve agent executable %s: %w", profile.Executable, err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return Result{}, err
	}
	executable = filepath.Clean(executable)

	temporary, err := os.MkdirTemp(runner.TempRoot, "exp-agent-")
	if err != nil {
		return Result{}, fmt.Errorf("create private agent workspace: %w", err)
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o700); err != nil {
		return Result{}, err
	}
	promptPath := filepath.Join(temporary, "prompt.txt")
	schemaPath := filepath.Join(temporary, "schema.json")
	outputPath := filepath.Join(temporary, "output.json")
	if err := os.WriteFile(promptPath, request.Prompt, 0o600); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(schemaPath, schema, 0o600); err != nil {
		return Result{}, err
	}
	outputFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("prepare bounded agent output file: %w", err)
	}
	outputIdentity, statErr := outputFile.Stat()
	closeErr := outputFile.Close()
	if statErr != nil || closeErr != nil {
		return Result{}, errors.Join(statErr, closeErr)
	}

	replacements := map[string]string{
		"{prompt_file}": promptPath,
		"{schema_file}": schemaPath,
		"{schema_json}": string(schema),
		"{output_file}": outputPath,
		"{cwd}":         request.CWD,
	}
	argv := make([]string, len(profile.Args))
	for index, argument := range profile.Args {
		if replacement, ok := replacements[argument]; ok {
			argv[index] = replacement
		} else {
			argv[index] = argument
		}
	}
	allowed := append(execx.MinimalAllowlist(), profile.AllowedEnv...)
	bindings := append([]execx.Binding(nil), request.ExtraEnv...)
	for _, name := range profile.SecretEnv {
		bindings = append(bindings, execx.BindSecretFromEnv(name, name))
	}
	environment, err := execx.NewEnvironment(uniqueStrings(allowed), bindings...)
	if err != nil {
		return Result{}, err
	}
	timeout := 10 * time.Minute
	if profile.Timeout != "" {
		timeout, _ = time.ParseDuration(profile.Timeout)
	}
	limit := profile.MaxOutputBytes
	if limit == 0 {
		limit = DefaultOutputLimit
	}
	mode := request.OutputMode
	if mode == "" {
		mode = execx.OutputCapture
	}
	stdinPrompt := true
	if profile.StdinPrompt != nil {
		stdinPrompt = *profile.StdinPrompt
	}
	var stdin io.Reader
	if stdinPrompt {
		stdin = bytes.NewReader(request.Prompt)
	}
	spec := execx.CommandSpec{
		Executable:  executable,
		Argv:        argv,
		CWD:         request.CWD,
		Environment: environment,
		Timeout:     timeout,
		Output: execx.OutputPolicy{
			Mode: mode, MaxStdoutBytes: limit, MaxStderrBytes: limit,
			Stdout: request.Stdout, Stderr: request.Stderr,
		},
		Stdin:     stdin,
		Redaction: execx.NewRedactor().WithSecrets(append([]string{string(request.Prompt), string(schema)}, secretValues...)...),
	}
	view, err := spec.SafeView()
	if err != nil {
		return Result{}, err
	}
	invoker := runner.Invoker
	if invoker == nil {
		invoker = execx.OSInvoker{LookupEnv: lookupEnv}
	}
	invokeCtx, cancelInvoke := context.WithCancel(ctx)
	var outputMonitor <-chan error
	if profile.Output == OutputFileJSON {
		outputMonitor = monitorBoundedOutputFile(invokeCtx, outputPath, outputIdentity, limit, cancelInvoke)
	}
	process, invokeErr := invoker.Invoke(invokeCtx, spec)
	cancelInvoke()
	if outputMonitor != nil {
		if monitorErr := <-outputMonitor; monitorErr != nil && invokeErr == nil {
			invokeErr = monitorErr
		}
	}
	if invokeErr != nil {
		return Result{Profile: profileName, ReportedModel: profile.ReportedModel, Command: view, Process: process}, invokeErr
	}
	var raw []byte
	if profile.Output == OutputFileJSON {
		raw, err = readBoundedOutputFile(outputPath, outputIdentity, limit)
		if err != nil {
			return Result{Profile: profileName, ReportedModel: profile.ReportedModel, Command: view, Process: process}, fmt.Errorf("read agent output file: %w", err)
		}
	} else {
		raw = []byte(process.Stdout)
	}
	output, err := normalizeJSONDocument(raw)
	if err != nil {
		return Result{Profile: profileName, ReportedModel: profile.ReportedModel, Command: view, Process: process}, fmt.Errorf("parse agent output: %w", err)
	}
	if outputContainsSecret(output, secretValues) {
		return Result{Profile: profileName, ReportedModel: profile.ReportedModel, Command: view, Process: process}, errors.New("agent output contains protected secret material")
	}
	if err := validateCompiledJSONSchema(compiledSchema, output); err != nil {
		return Result{Profile: profileName, ReportedModel: profile.ReportedModel, Output: output, Command: view, Process: process}, fmt.Errorf("agent output does not satisfy JSON Schema: %w", err)
	}
	return Result{Profile: profileName, ReportedModel: profile.ReportedModel, Output: output, Command: view, Process: process}, nil
}

type denySchemaLoader struct{}

func (denySchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external JSON Schema resource %q is disabled", url)
}

func validateJSONSchema(schema, output []byte) error {
	compiled, err := compileJSONSchema(schema)
	if err != nil {
		return err
	}
	return validateCompiledJSONSchema(compiled, output)
}

func compileJSONSchema(schema []byte) (*jsonschema.Schema, error) {
	var schemaDocument any
	if err := json.Unmarshal(schema, &schemaDocument); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(denySchemaLoader{})
	const location = "urn:exp:agent-output-schema"
	if err := compiler.AddResource(location, schemaDocument); err != nil {
		return nil, err
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return nil, err
	}
	return compiled, nil
}

func validateCompiledJSONSchema(compiled *jsonschema.Schema, output []byte) error {
	var value any
	if err := json.Unmarshal(output, &value); err != nil {
		return err
	}
	return compiled.Validate(value)
}

func monitorBoundedOutputFile(ctx context.Context, path string, expected os.FileInfo, limit int64, cancel context.CancelFunc) <-chan error {
	result := make(chan error, 1)
	go func() {
		defer close(result)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				result <- nil
				return
			case <-ticker.C:
				info, err := os.Lstat(path)
				if err != nil || expected == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(expected, info) {
					cancel()
					result <- errors.New("agent output file identity changed or is not a regular file")
					return
				}
				if info.Size() > limit {
					cancel()
					result <- fmt.Errorf("agent output exceeds %d bytes", limit)
					return
				}
			}
		}
	}()
	return result
}

func outputContainsSecret(output []byte, secrets []string) bool {
	if len(secrets) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(output, &value) != nil {
		return true
	}
	var contains func(any) bool
	contains = func(item any) bool {
		switch typed := item.(type) {
		case string:
			for _, secret := range secrets {
				if secret != "" && strings.Contains(typed, secret) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if contains(child) {
					return true
				}
			}
		case map[string]any:
			for key, child := range typed {
				if contains(key) || contains(child) {
					return true
				}
			}
		}
		return false
	}
	return contains(value)
}

func readBoundedOutputFile(path string, expected os.FileInfo, limit int64) ([]byte, error) {
	directory, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	content, info, err := pathx.ReadBoundedRegularFile(context.Background(), directory, filepath.Base(path), limit)
	if err != nil {
		return nil, err
	}
	if expected == nil || !os.SameFile(expected, info) {
		return nil, errors.New("agent output file identity changed or is not a regular file")
	}
	return content, nil
}

func normalizeJSONObject(value []byte) ([]byte, error) {
	normalized, err := normalizeJSONDocument(value)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(normalized, &object); err != nil || object == nil {
		return nil, errors.New("value must be a JSON object")
	}
	return normalized, nil
}

func normalizeJSONDocument(value []byte) (json.RawMessage, error) {
	if len(value) == 0 || len(value) > 4<<20 || !utf8.Valid(value) {
		return nil, errors.New("JSON document is empty, oversized, or invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("JSON document contains trailing data")
		}
		return nil, err
	}
	return json.Marshal(decoded)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validName(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func validEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if !(character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}
