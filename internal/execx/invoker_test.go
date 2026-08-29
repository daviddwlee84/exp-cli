package execx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

const helperEnvironment = "EXP_EXECX_HELPER_PROCESS"

func TestInvokerPreservesArgumentBoundaries(t *testing.T) {
	arguments := []string{"two words", "; echo not-a-shell", "$HOME", "", "line\nbreak", `quote"inside`}
	spec := helperSpec(t, "argv", arguments...)
	result, err := execx.NewInvoker().Invoke(t.Context(), spec)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	var got []string
	if err := json.Unmarshal([]byte(result.Stdout), &got); err != nil {
		t.Fatalf("decode helper argv %q: %v", result.Stdout, err)
	}
	if !reflect.DeepEqual(got, arguments) {
		t.Fatalf("helper argv = %#v, want %#v", got, arguments)
	}
}

func TestInvokerUsesOnlyAllowedAndExplicitEnvironment(t *testing.T) {
	t.Setenv("EXP_EXECX_ALLOWED", "inherited")
	t.Setenv("EXP_EXECX_BLOCKED", "must-not-cross")
	environment, err := execx.NewEnvironment(
		[]string{"EXP_EXECX_ALLOWED"},
		execx.Bind(helperEnvironment, "1"),
		execx.Bind("EXP_EXECX_BOUND", "explicit"),
	)
	if err != nil {
		t.Fatal(err)
	}
	spec := helperSpecWithEnvironment(t, environment, "environment")
	result, err := execx.NewInvoker().Invoke(t.Context(), spec)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got, want := result.Stdout, "inherited|explicit|"; got != want {
		t.Fatalf("helper environment = %q, want %q", got, want)
	}
}

func TestSecretReferenceResolvesOnlyForInvokeAndCannotLeak(t *testing.T) {
	const canary = "execx-secret-reference-canary-51b2"
	t.Setenv("EXP_EXECX_PARENT_SECRET", canary)
	environment, err := execx.NewEnvironment(nil,
		execx.Bind(helperEnvironment, "1"),
		execx.BindSecretFromEnv("EXP_EXECX_CHILD_SECRET", "EXP_EXECX_PARENT_SECRET"),
	)
	if err != nil {
		t.Fatal(err)
	}
	spec := helperSpecWithEnvironment(t, environment, "secret-environment")

	view, err := spec.SafeView()
	if err != nil {
		t.Fatalf("SafeView() error = %v", err)
	}
	if rendered := mustMarshal(t, view); strings.Contains(rendered, canary) || strings.Contains(rendered, "EXP_EXECX_PARENT_SECRET") {
		t.Fatalf("plan view leaked secret reference: %q", rendered)
	}

	result, err := execx.NewInvoker().Invoke(t.Context(), spec)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if strings.Contains(result.Stdout, canary) || result.Stdout != execx.Redacted {
		t.Fatalf("result leaked deferred secret: %q", result.Stdout)
	}
}

func TestSecretEnvironmentCanaryCannotLeakThroughFailureOrStream(t *testing.T) {
	const canary = "execx-secret-environment-failure-canary-726a"
	t.Setenv("EXP_EXECX_PARENT_FAILURE_SECRET", canary)
	environment, err := execx.NewEnvironment(nil,
		execx.Bind(helperEnvironment, "1"),
		execx.BindSecretFromEnv("EXP_EXECX_CHILD_SECRET", "EXP_EXECX_PARENT_FAILURE_SECRET"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	spec := helperSpecWithEnvironment(t, environment, "secret-environment-failure")
	spec.Output = execx.OutputPolicy{
		Mode:           execx.OutputStream,
		MaxStdoutBytes: 256,
		MaxStderrBytes: 256,
		Stdout:         &stdout,
		Stderr:         &stderr,
	}
	result, err := execx.NewInvoker().Invoke(t.Context(), spec)
	if err == nil {
		t.Fatal("Invoke() succeeded, want helper failure")
	}
	var invocationError *execx.Error
	if !errors.As(err, &invocationError) {
		t.Fatalf("Invoke() error = %T %v", err, err)
	}
	combined := strings.Join([]string{
		stdout.String(), stderr.String(), result.Stdout, result.Stderr,
		err.Error(), mustMarshal(t, result), mustMarshal(t, invocationError), mustMarshal(t, spec),
	}, "\n")
	if strings.Contains(combined, canary) || !strings.Contains(combined, execx.Redacted) {
		t.Fatalf("secret environment canary crossed an output/error/JSON/stream boundary: %q", combined)
	}
}

func TestInvokerRedactsAttachedAndBareCredentialArgumentsFromOutput(t *testing.T) {
	canaries := []string{
		"execx-attached-header-canary-71a2",
		"execx-attached-user-canary-82b3",
		"execx-bare-bearer-canary-93c4",
		"execx-bare-basic-canary-a4d5",
	}
	var stdout, stderr bytes.Buffer
	spec := helperSpec(t, "argv-failure",
		"-HAuthorization: Bearer "+canaries[0],
		"-ualice:"+canaries[1],
		"Bearer", canaries[2],
		"Basic "+canaries[3],
	)
	spec.Output = execx.OutputPolicy{
		Mode:           execx.OutputStream,
		MaxStdoutBytes: 4 << 10,
		MaxStderrBytes: 4 << 10,
		Stdout:         &stdout,
		Stderr:         &stderr,
	}
	result, err := execx.NewInvoker().Invoke(t.Context(), spec)
	if err == nil {
		t.Fatal("Invoke() succeeded, want helper failure")
	}
	combined := strings.Join([]string{stdout.String(), stderr.String(), result.Stdout, result.Stderr, err.Error(), mustMarshal(t, spec)}, "\n")
	for _, canary := range canaries {
		if strings.Contains(combined, canary) {
			t.Fatalf("inferred argv canary %q crossed capture/stream/error boundaries: %q", canary, combined)
		}
	}
	if !strings.Contains(combined, execx.Redacted) {
		t.Fatalf("redaction marker is absent: %q", combined)
	}
}

func TestInvokerRejectsInvalidUTF8SecretsBeforeStart(t *testing.T) {
	invalid := "secret-prefix\xffsecret-suffix"

	t.Run("argv", func(t *testing.T) {
		spec := helperSpec(t, "argv", "--auth-token", invalid)
		result, err := execx.NewInvoker().Invoke(t.Context(), spec)
		if err == nil || !errors.Is(err, execx.ErrCommandInvalid) || !result.StartedAt.IsZero() {
			t.Fatalf("Invoke() = %+v, %v; want pre-start validation failure", result, err)
		}
		if strings.Contains(err.Error(), "secret-prefix") || strings.Contains(err.Error(), "secret-suffix") {
			t.Fatalf("argv validation error leaked invalid secret fragments: %v", err)
		}
	})

	t.Run("environment source", func(t *testing.T) {
		environment, err := execx.NewEnvironment(nil, execx.BindSecretFromEnv("DATABASE_PASSWORD", "SECRET_SOURCE"))
		if err != nil {
			t.Fatal(err)
		}
		spec := helperSpecWithEnvironment(t, environment, "argv", "unused")
		invoker := execx.OSInvoker{LookupEnv: func(name string) (string, bool) {
			if name == "SECRET_SOURCE" {
				return invalid, true
			}
			return "", false
		}}
		result, err := invoker.Invoke(t.Context(), spec)
		if err == nil || !errors.Is(err, execx.ErrCommandInvalid) || !result.StartedAt.IsZero() {
			t.Fatalf("Invoke() = %+v, %v; want pre-start environment validation failure", result, err)
		}
		if strings.Contains(err.Error(), "secret-prefix") || strings.Contains(err.Error(), "secret-suffix") {
			t.Fatalf("environment validation error leaked invalid secret fragments: %v", err)
		}
	})
}

func TestInvokerClassifiesTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		spec := helperSpec(t, "sleep")
		spec.Timeout = 75 * time.Millisecond
		result, err := execx.NewInvoker().Invoke(t.Context(), spec)
		if err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Invoke() error = %v, want deadline exceeded", err)
		}
		var invocationError *execx.Error
		if !errors.As(err, &invocationError) || invocationError.Kind != execx.ErrorTimeout {
			t.Fatalf("Invoke() error type = %T (%v)", err, err)
		}
		if !result.TimedOut || result.Canceled {
			t.Fatalf("timeout result = %+v", result)
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		result, err := execx.NewInvoker().Invoke(ctx, helperSpec(t, "sleep"))
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("Invoke() error = %v, want context canceled", err)
		}
		var invocationError *execx.Error
		if !errors.As(err, &invocationError) || invocationError.Kind != execx.ErrorCanceled {
			t.Fatalf("Invoke() error type = %T (%v)", err, err)
		}
		if !result.Canceled || result.TimedOut {
			t.Fatalf("canceled result = %+v", result)
		}
	})
}

func TestInvokerBoundsCaptureAndStreaming(t *testing.T) {
	t.Run("capture", func(t *testing.T) {
		spec := helperSpec(t, "large-output")
		spec.Output.MaxStdoutBytes = 80
		spec.Output.MaxStderrBytes = 48
		result, err := execx.NewInvoker().Invoke(t.Context(), spec)
		if err != nil {
			t.Fatalf("Invoke() error = %v", err)
		}
		if !result.StdoutTruncated || !result.StderrTruncated {
			t.Fatalf("truncation flags = stdout %v, stderr %v", result.StdoutTruncated, result.StderrTruncated)
		}
		if len(result.Stdout) > 80 || len(result.Stderr) > 48 {
			t.Fatalf("bounded lengths = stdout %d, stderr %d", len(result.Stdout), len(result.Stderr))
		}
	})

	t.Run("stream", func(t *testing.T) {
		const canary = "execx-stream-canary-8c93"
		var stdout, stderr bytes.Buffer
		spec := helperSpec(t, "secret-failure", canary)
		spec.Redaction = execx.NewRedactor(canary)
		spec.Output = execx.OutputPolicy{
			Mode:           execx.OutputStream,
			MaxStdoutBytes: 256,
			MaxStderrBytes: 256,
			Stdout:         &stdout,
			Stderr:         &stderr,
		}
		result, err := execx.NewInvoker().Invoke(t.Context(), spec)
		if err == nil {
			t.Fatal("Invoke() succeeded, want nonzero exit")
		}
		var invocationError *execx.Error
		if !errors.As(err, &invocationError) || invocationError.Kind != execx.ErrorExit || result.ExitCode != 23 {
			t.Fatalf("Invoke() = %+v, %T %v", result, err, err)
		}
		combined := strings.Join([]string{
			stdout.String(), stderr.String(), result.Stdout, result.Stderr,
			err.Error(), mustMarshal(t, invocationError), mustMarshal(t, spec), fmt.Sprintf("%#v", spec),
		}, "\n")
		if strings.Contains(combined, canary) {
			t.Fatalf("secret canary survived an output/error/plan boundary: %q", combined)
		}
		if !strings.Contains(combined, execx.Redacted) {
			t.Fatalf("expected visible redaction marker: %q", combined)
		}
	})
}

func TestInvokerSeedsOutputRedactionFromEverySensitiveArgument(t *testing.T) {
	canaries := []string{
		"execx-structural-argv-canary-a",
		"execx-header-argv-canary-b",
		"execx-environment-argv-canary-c",
		"execx-explicit-argv-canary-d",
	}
	var stdout, stderr bytes.Buffer
	spec := helperSpec(t, "argv-failure",
		"--token", canaries[0],
		"--header", "Authorization: Bearer "+canaries[1], canaries[1],
		"--env", "PASSWORD="+canaries[2], canaries[2],
		"--opaque", canaries[3],
	)
	for index, argument := range spec.Argv {
		if argument == "--token" || argument == canaries[3] {
			spec.SensitiveArgs = append(spec.SensitiveArgs, index)
		}
	}
	spec.Output = execx.OutputPolicy{
		Mode:           execx.OutputStream,
		MaxStdoutBytes: 4 << 10,
		MaxStderrBytes: 4 << 10,
		Stdout:         &stdout,
		Stderr:         &stderr,
	}
	result, err := execx.NewInvoker().Invoke(t.Context(), spec)
	if err == nil {
		t.Fatal("Invoke() succeeded, want helper failure")
	}
	var invocationError *execx.Error
	if !errors.As(err, &invocationError) {
		t.Fatalf("Invoke() error = %T %v", err, err)
	}
	combined := strings.Join([]string{
		stdout.String(), stderr.String(), result.Stdout, result.Stderr,
		err.Error(), mustMarshal(t, result), mustMarshal(t, invocationError), mustMarshal(t, spec),
	}, "\n")
	for _, canary := range canaries {
		if strings.Contains(combined, canary) {
			t.Fatalf("sensitive argv canary %q survived output/error/JSON/stream boundaries: %q", canary, combined)
		}
	}
	if !strings.Contains(combined, execx.Redacted) {
		t.Fatalf("redaction marker is absent: %q", combined)
	}
}

func TestInvokerStreamsARedactedLineBeforeProcessExit(t *testing.T) {
	const canary = "execx-progressive-stream-canary-09e1"
	stream := &notifyingWriter{firstWrite: make(chan struct{})}
	spec := helperSpec(t, "delayed-stream", canary)
	spec.Redaction = execx.NewRedactor(canary)
	spec.Output = execx.OutputPolicy{
		Mode:           execx.OutputStream,
		MaxStdoutBytes: 256,
		MaxStderrBytes: 256,
		Stdout:         stream,
	}
	type outcome struct {
		result execx.Result
		err    error
	}
	completed := make(chan outcome, 1)
	go func() {
		result, err := execx.NewInvoker().Invoke(t.Context(), spec)
		completed <- outcome{result: result, err: err}
	}()

	select {
	case <-stream.firstWrite:
	case <-time.After(2 * time.Second):
		t.Fatal("stream received no line while child was running")
	}
	select {
	case early := <-completed:
		t.Fatalf("child exited before delayed stream completed: %+v, %v", early.result, early.err)
	default:
	}
	finished := <-completed
	if finished.err != nil {
		t.Fatalf("Invoke() error = %v", finished.err)
	}
	combined := stream.String() + finished.result.Stdout
	if strings.Contains(combined, canary) || !strings.Contains(combined, execx.Redacted) || !strings.Contains(combined, "done") {
		t.Fatalf("progressive stream was not safely complete: %q", combined)
	}
}

func TestInvokerRejectsImplicitOrUnboundedCommands(t *testing.T) {
	valid := helperSpec(t, "argv", "ok")
	for _, testCase := range []struct {
		name   string
		mutate func(*execx.CommandSpec)
	}{
		{name: "bare executable", mutate: func(spec *execx.CommandSpec) { spec.Executable = "go" }},
		{name: "relative cwd", mutate: func(spec *execx.CommandSpec) { spec.CWD = "." }},
		{name: "unclean cwd", mutate: func(spec *execx.CommandSpec) {
			spec.CWD += string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(spec.CWD)
		}},
		{name: "nul argument", mutate: func(spec *execx.CommandSpec) { spec.Argv = append(spec.Argv, "bad\x00arg") }},
		{name: "no stdout bound", mutate: func(spec *execx.CommandSpec) { spec.Output.MaxStdoutBytes = 0 }},
		{name: "oversized stderr bound", mutate: func(spec *execx.CommandSpec) { spec.Output.MaxStderrBytes = execx.MaxOutputLimit + 1 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			spec := valid
			spec.Argv = append([]string(nil), valid.Argv...)
			testCase.mutate(&spec)
			_, err := execx.NewInvoker().Invoke(t.Context(), spec)
			if err == nil || !errors.Is(err, execx.ErrCommandInvalid) {
				t.Fatalf("Invoke() error = %v, want ErrCommandInvalid", err)
			}
		})
	}
}

func TestInvokerFuncProvidesAnInjectedSeam(t *testing.T) {
	var got execx.CommandSpec
	fake := execx.InvokerFunc(func(_ context.Context, spec execx.CommandSpec) (execx.Result, error) {
		got = spec
		return execx.Result{Stdout: "synthetic", ExitCode: 0}, nil
	})
	spec := helperSpec(t, "argv", "one", "two words")
	result, err := fake.Invoke(t.Context(), spec)
	if err != nil || result.Stdout != "synthetic" || !reflect.DeepEqual(got.Argv, spec.Argv) {
		t.Fatalf("fake Invoke() = %+v, %v; argv %q", result, err, got.Argv)
	}
}

func helperSpec(t *testing.T, mode string, arguments ...string) execx.CommandSpec {
	t.Helper()
	environment, err := execx.NewEnvironment(nil, execx.Bind(helperEnvironment, "1"))
	if err != nil {
		t.Fatal(err)
	}
	return helperSpecWithEnvironment(t, environment, mode, arguments...)
}

func helperSpecWithEnvironment(t *testing.T, environment execx.Environment, mode string, arguments ...string) execx.CommandSpec {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	argv := []string{"-test.run=^TestExecxHelperProcess$", "--", mode}
	argv = append(argv, arguments...)
	return execx.CommandSpec{
		Executable:  filepath.Clean(executable),
		Argv:        argv,
		CWD:         filepath.Clean(t.TempDir()),
		Environment: environment,
		Timeout:     5 * time.Second,
		Output:      execx.DefaultOutputPolicy(execx.OutputCapture),
	}
}

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(encoded)
}

type notifyingWriter struct {
	mutex      sync.Mutex
	buffer     bytes.Buffer
	firstWrite chan struct{}
	once       sync.Once
}

func (w *notifyingWriter) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	written, err := w.buffer.Write(data)
	if written > 0 {
		w.once.Do(func() { close(w.firstWrite) })
	}
	return written, err
}

func (w *notifyingWriter) String() string {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.buffer.String()
}

// TestExecxHelperProcess is re-executed as a direct child by the tests above.
// Every mode exits explicitly so the testing package writes no extra output.
func TestExecxHelperProcess(t *testing.T) {
	if os.Getenv(helperEnvironment) != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(97)
	}
	mode := os.Args[separator+1]
	arguments := os.Args[separator+2:]
	switch mode {
	case "argv":
		_ = json.NewEncoder(os.Stdout).Encode(arguments)
	case "argv-failure":
		_ = json.NewEncoder(os.Stdout).Encode(arguments)
		_ = json.NewEncoder(os.Stderr).Encode(arguments)
		os.Exit(24)
	case "environment":
		_, _ = fmt.Fprintf(os.Stdout, "%s|%s|%s", os.Getenv("EXP_EXECX_ALLOWED"), os.Getenv("EXP_EXECX_BOUND"), os.Getenv("EXP_EXECX_BLOCKED"))
	case "secret-environment":
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("EXP_EXECX_CHILD_SECRET"))
	case "secret-environment-failure":
		_, _ = fmt.Fprintln(os.Stdout, os.Getenv("EXP_EXECX_CHILD_SECRET"))
		_, _ = fmt.Fprintln(os.Stderr, os.Getenv("EXP_EXECX_CHILD_SECRET"))
		os.Exit(25)
	case "sleep":
		time.Sleep(5 * time.Second)
	case "large-output":
		for index := 0; index < 100; index++ {
			_, _ = fmt.Fprintf(os.Stdout, "stdout-%03d-value\n", index)
			_, _ = fmt.Fprintf(os.Stderr, "stderr-%03d-value\n", index)
		}
	case "secret-failure":
		if len(arguments) != 1 {
			os.Exit(98)
		}
		_, _ = fmt.Fprintf(os.Stdout, "token=%s\n", arguments[0])
		_, _ = fmt.Fprintf(os.Stderr, "Authorization: Bearer %s\n", arguments[0])
		os.Exit(23)
	case "delayed-stream":
		if len(arguments) != 1 {
			os.Exit(98)
		}
		_, _ = fmt.Fprintf(os.Stdout, "token=%s\n", arguments[0])
		time.Sleep(300 * time.Millisecond)
		_, _ = fmt.Fprintln(os.Stdout, "done")
	default:
		os.Exit(99)
	}
	os.Exit(0)
}
