package execx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// DefaultWaitDelay bounds the os/exec pipe wait after cancellation. Supported
// Unix targets cancel the spawned process group; Windows currently retains
// os/exec's direct-child cancellation behavior.
const DefaultWaitDelay = time.Second

// Invoker is injected into every provider adapter. Implementations receive one
// explicit executable and argv and must never reinterpret them as shell text.
type Invoker interface {
	Invoke(context.Context, CommandSpec) (Result, error)
}

// InvokerFunc adapts a function into an Invoker for focused tests and narrow
// composition roots.
type InvokerFunc func(context.Context, CommandSpec) (Result, error)

// Invoke implements Invoker.
func (f InvokerFunc) Invoke(ctx context.Context, spec CommandSpec) (Result, error) {
	return f(ctx, spec)
}

// OSInvoker runs a direct child with os/exec. LookupEnv and Now are injectable;
// nil fields use os.LookupEnv and time.Now.
type OSInvoker struct {
	LookupEnv LookupEnv
	Now       func() time.Time
}

// NewInvoker returns the standard platform-aware subprocess implementation.
func NewInvoker() Invoker { return OSInvoker{} }

// Invoke validates spec, resolves its allowlisted environment immediately
// before start, and returns only bounded, sanitized output.
func (i OSInvoker) Invoke(ctx context.Context, spec CommandSpec) (Result, error) {
	result := Result{ExitCode: -1}
	if ctx == nil {
		return result, newError(ErrorInvalid, spec, result, "context is required")
	}
	if err := spec.Validate(); err != nil {
		return result, err
	}

	lookup := i.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	environment, environmentSecrets, err := spec.Environment.resolve(lookup)
	if err != nil {
		return result, newError(ErrorInvalid, spec, result, err.Error())
	}
	redactor := spec.effectiveRedactor(environmentSecrets...)

	runCtx := ctx
	cancel := func() {}
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}
	defer cancel()

	now := i.Now
	if now == nil {
		now = time.Now
	}
	stdout := newOutputCollector(spec.Output.MaxStdoutBytes, redactor, streamDestination(spec.Output.Mode, spec.Output.Stdout))
	stderr := newOutputCollector(spec.Output.MaxStderrBytes, redactor, streamDestination(spec.Output.Mode, spec.Output.Stderr))

	command := exec.CommandContext(runCtx, spec.Executable, spec.Argv...)
	configureProcessCancellation(command)
	command.Dir = spec.CWD
	command.Env = environment // a non-nil empty slice means inherit nothing
	command.Stdin = spec.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = DefaultWaitDelay

	result.StartedAt = now().UTC()
	startErr := command.Start()
	if startErr != nil {
		result.FinishedAt = now().UTC()
		result.Duration = nonNegativeDuration(result.StartedAt, result.FinishedAt)
		result.Stdout, result.StdoutTruncated, _ = stdout.finish()
		result.Stderr, result.StderrTruncated, _ = stderr.finish()
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
			return result, newError(ErrorTimeout, specWithRedactor(spec, redactor), result, "deadline exceeded before process start")
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			result.Canceled = true
			return result, newError(ErrorCanceled, specWithRedactor(spec, redactor), result, "context canceled before process start")
		}
		return result, newError(ErrorStart, specWithRedactor(spec, redactor), result, "process start failed")
	}

	waitErr := command.Wait()
	result.FinishedAt = now().UTC()
	result.Duration = nonNegativeDuration(result.StartedAt, result.FinishedAt)
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	var stdoutStreamErr, stderrStreamErr error
	result.Stdout, result.StdoutTruncated, stdoutStreamErr = stdout.finish()
	result.Stderr, result.StderrTruncated, stderrStreamErr = stderr.finish()

	safeSpec := specWithRedactor(spec, redactor)
	if waitErr == nil {
		if stdoutStreamErr != nil || stderrStreamErr != nil {
			return result, newError(ErrorOutput, safeSpec, result, "sanitized output stream failed")
		}
		return result, nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		return result, newError(ErrorTimeout, safeSpec, result, "command deadline exceeded")
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		result.Canceled = true
		return result, newError(ErrorCanceled, safeSpec, result, "command context canceled")
	}
	if command.ProcessState == nil {
		return result, newError(ErrorStart, safeSpec, result, "process start failed")
	}
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		return result, newError(ErrorOutput, safeSpec, result, "process output pipes did not close within the wait bound")
	}
	if result.ExitCode >= 0 {
		return result, newError(ErrorExit, safeSpec, result, "exit code "+strconv.Itoa(result.ExitCode))
	}
	return result, newError(ErrorExit, safeSpec, result, "process exited unsuccessfully")
}

func streamDestination(mode OutputMode, destination io.Writer) io.Writer {
	if mode != OutputStream {
		return nil
	}
	return destination
}

func specWithRedactor(spec CommandSpec, redactor Redactor) CommandSpec {
	spec.Redaction = redactor
	return spec
}

func nonNegativeDuration(started, finished time.Time) time.Duration {
	if finished.Before(started) {
		return 0
	}
	return finished.Sub(started)
}

type outputCollector struct {
	capture  *boundedCapture
	stream   *safeLineStream
	redactor Redactor
}

func newOutputCollector(limit int64, redactor Redactor, destination io.Writer) *outputCollector {
	collector := &outputCollector{capture: &boundedCapture{limit: limit}, redactor: redactor}
	if destination != nil {
		collector.stream = &safeLineStream{
			destination: destination,
			redactor:    redactor,
			limit:       limit,
		}
	}
	return collector
}

func (c *outputCollector) Write(data []byte) (int, error) {
	_, _ = c.capture.Write(data)
	if c.stream != nil {
		_, _ = c.stream.Write(data)
	}
	// Destination failures are recorded and reported after the process exits;
	// they do not turn into a child-side broken pipe or expose raw output.
	return len(data), nil
}

func (c *outputCollector) finish() (string, bool, error) {
	var streamErr error
	if c.stream != nil {
		streamErr = c.stream.Close()
	}
	value, truncated := c.capture.safe(c.redactor)
	if c.stream != nil && c.stream.truncated {
		truncated = true
	}
	return value, truncated, streamErr
}

type boundedCapture struct {
	buffer bytes.Buffer
	limit  int64
	seen   int64
}

func (c *boundedCapture) Write(data []byte) (int, error) {
	original := len(data)
	c.seen += int64(original)
	remaining := c.limit - int64(c.buffer.Len())
	if remaining > 0 {
		if int64(len(data)) > remaining {
			data = data[:remaining]
		}
		_, _ = c.buffer.Write(data)
	}
	return original, nil
}

func (c *boundedCapture) safe(redactor Redactor) (string, bool) {
	truncated := c.seen > c.limit
	raw := append([]byte(nil), c.buffer.Bytes()...)
	if truncated {
		// Never expose a partial final line: it may be the prefix of a secret
		// that crossed the byte boundary.
		if newline := bytes.LastIndexByte(raw, '\n'); newline >= 0 {
			raw = raw[:newline+1]
		} else {
			raw = nil
		}
	}
	value := redactor.Text(string(raw))
	if truncated {
		value += truncatedMarker
	}
	bounded, boundedAgain := BoundText(value, int(c.limit))
	return bounded, truncated || boundedAgain
}

type safeLineStream struct {
	destination io.Writer
	redactor    Redactor
	limit       int64
	seen        int64
	emitted     int64
	pending     []byte
	truncated   bool
	writeErr    error
	closed      bool
}

func (s *safeLineStream) Write(data []byte) (int, error) {
	original := len(data)
	if s.closed {
		return original, nil
	}
	s.seen += int64(original)
	remaining := s.limit - int64(len(s.pending)) - s.emitted
	if remaining < 0 {
		remaining = 0
	}
	if int64(len(data)) > remaining {
		data = data[:remaining]
		s.truncated = true
	}
	s.pending = append(s.pending, data...)
	for {
		newline := bytes.IndexByte(s.pending, '\n')
		if newline < 0 {
			break
		}
		line := string(s.pending[:newline+1])
		s.pending = s.pending[newline+1:]
		s.emit(s.redactor.Text(line))
	}
	if s.seen > s.limit {
		s.truncated = true
	}
	return original, nil
}

func (s *safeLineStream) Close() error {
	if s.closed {
		return s.writeErr
	}
	s.closed = true
	if !s.truncated && len(s.pending) > 0 {
		s.emit(s.redactor.Text(string(s.pending)))
	}
	s.pending = nil
	if s.truncated {
		s.emit(truncatedMarker)
	}
	return s.writeErr
}

func (s *safeLineStream) emit(value string) {
	if value == "" || s.writeErr != nil {
		return
	}
	remaining := s.limit - s.emitted
	if remaining <= 0 {
		s.truncated = true
		return
	}
	value, truncated := BoundText(value, int(remaining))
	if truncated {
		s.truncated = true
	}
	if value == "" {
		return
	}
	written, err := io.WriteString(s.destination, value)
	s.emitted += int64(written)
	if err != nil {
		s.writeErr = errors.New("sanitized output destination rejected data")
		return
	}
	if written != len(value) {
		s.writeErr = errors.New("sanitized output destination made a short write")
	}
}

// Ensure compile-time conformance for both injected forms.
var (
	_ Invoker = OSInvoker{}
	_ Invoker = InvokerFunc(nil)
)
