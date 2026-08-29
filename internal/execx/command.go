// Package execx provides the only operating-system subprocess boundary used by
// provider adapters. It accepts an executable and argv directly; it never
// constructs or evaluates a shell command string.
package execx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultOutputLimit is the bounded capture size used by
	// DefaultOutputPolicy.
	DefaultOutputLimit int64 = 1 << 20
	// MaxOutputLimit prevents a mistaken policy from allocating unbounded
	// in-memory capture. Streaming is also bounded by this value.
	MaxOutputLimit int64 = 64 << 20
	// MaxCommandViewBytes bounds serialized command plans and error identities.
	MaxCommandViewBytes = 64 << 10
	// MaxErrorBytes bounds Error.Error output.
	MaxErrorBytes = 16 << 10
)

// OutputMode controls whether sanitized output is only captured or is also
// forwarded incrementally to caller-provided writers.
type OutputMode string

const (
	OutputCapture OutputMode = "capture"
	OutputStream  OutputMode = "stream"
)

// OutputPolicy requires independent stdout and stderr byte bounds. Stream mode
// still returns a bounded sanitized capture while forwarding complete,
// sanitized lines to the configured writers.
type OutputPolicy struct {
	Mode           OutputMode
	MaxStdoutBytes int64
	MaxStderrBytes int64
	Stdout         io.Writer
	Stderr         io.Writer
}

// DefaultOutputPolicy creates a bounded policy for mode.
func DefaultOutputPolicy(mode OutputMode) OutputPolicy {
	return OutputPolicy{
		Mode:           mode,
		MaxStdoutBytes: DefaultOutputLimit,
		MaxStderrBytes: DefaultOutputLimit,
	}
}

// OutputView is value-free plan metadata for an OutputPolicy.
type OutputView struct {
	Mode           OutputMode `json:"mode"`
	MaxStdoutBytes int64      `json:"max_stdout_bytes"`
	MaxStderrBytes int64      `json:"max_stderr_bytes"`
}

// CommandSpec is one explicit subprocess invocation. Executable and CWD must
// be clean absolute paths. Argv is passed directly to os/exec with every
// boundary preserved. A zero Timeout means the caller's context alone owns the
// lifetime; a positive Timeout adds a child deadline.
//
// All fields are excluded from default JSON encoding. MarshalJSON emits only a
// sanitized CommandView so raw argv and environment values cannot be encoded
// accidentally.
type CommandSpec struct {
	Executable    string        `json:"-"`
	Argv          []string      `json:"-"`
	CWD           string        `json:"-"`
	Environment   Environment   `json:"-"`
	Timeout       time.Duration `json:"-"`
	Output        OutputPolicy  `json:"-"`
	Stdin         io.Reader     `json:"-"`
	Redaction     Redactor      `json:"-"`
	SensitiveArgs []int         `json:"-"`
}

// CommandView is the only renderable representation of a CommandSpec.
type CommandView struct {
	Executable  string                `json:"executable"`
	Argv        []string              `json:"argv"`
	CWD         string                `json:"cwd"`
	Environment []EnvironmentVariable `json:"environment"`
	Timeout     string                `json:"timeout"`
	Output      OutputView            `json:"output"`
}

// Validate rejects implicit executable lookup, relative/unclean working
// directories, NUL-bearing arguments or environment values, invalid bounds,
// and invalid sensitive indexes.
func (s CommandSpec) Validate() error {
	if reason := s.validationReason(); reason != "" {
		return newError(ErrorInvalid, s, Result{ExitCode: -1}, reason)
	}
	return nil
}

func (s CommandSpec) validationReason() string {
	if s.Executable == "" {
		return "executable is required"
	}
	if !utf8.ValidString(s.Executable) || strings.IndexByte(s.Executable, 0) >= 0 || strings.ContainsAny(s.Executable, "\r\n") {
		return "executable path contains invalid text"
	}
	if !filepath.IsAbs(s.Executable) || filepath.Clean(s.Executable) != s.Executable {
		return "executable must be a clean absolute path"
	}
	if s.CWD == "" {
		return "cwd is required"
	}
	if !utf8.ValidString(s.CWD) || strings.IndexByte(s.CWD, 0) >= 0 || strings.ContainsAny(s.CWD, "\r\n") {
		return "cwd contains invalid text"
	}
	if !filepath.IsAbs(s.CWD) || filepath.Clean(s.CWD) != s.CWD {
		return "cwd must be a clean absolute path"
	}
	for _, argument := range s.Argv {
		if !utf8.ValidString(argument) {
			return "argv contains invalid UTF-8"
		}
		if strings.IndexByte(argument, 0) >= 0 {
			return "argv contains NUL"
		}
	}
	if s.Timeout < 0 {
		return "timeout cannot be negative"
	}
	if err := s.Environment.validate(); err != nil {
		return NewRedactor().Text(err.Error())
	}
	if s.Output.Mode != OutputCapture && s.Output.Mode != OutputStream {
		return "output mode must be capture or stream"
	}
	if s.Output.MaxStdoutBytes <= 0 || s.Output.MaxStdoutBytes > MaxOutputLimit {
		return "stdout byte bound is outside the allowed range"
	}
	if s.Output.MaxStderrBytes <= 0 || s.Output.MaxStderrBytes > MaxOutputLimit {
		return "stderr byte bound is outside the allowed range"
	}
	seen := make(map[int]struct{}, len(s.SensitiveArgs))
	for _, index := range s.SensitiveArgs {
		if index < 0 || index >= len(s.Argv) {
			return "sensitive argv index is outside argv"
		}
		if _, duplicate := seen[index]; duplicate {
			return "sensitive argv index is duplicated"
		}
		seen[index] = struct{}{}
	}
	return ""
}

func (s CommandSpec) effectiveRedactor(additionalSecrets ...string) Redactor {
	secrets := append([]string(nil), s.Environment.knownSecretValues()...)
	secrets = append(secrets, sensitiveArgvValues(s.Argv, s.SensitiveArgs...)...)
	secrets = append(secrets, additionalSecrets...)
	return s.Redaction.WithSecrets(secrets...)
}

// SafeView creates stable plan metadata. Literal secret bindings and explicit
// redaction canaries are applied before argv is returned. Secret references are
// not resolved while rendering a plan.
func (s CommandSpec) SafeView() (CommandView, error) {
	if err := s.Validate(); err != nil {
		return CommandView{}, err
	}
	redactor := s.effectiveRedactor()
	executable, _ := redactor.SafeText(s.Executable, MaxCommandViewBytes)
	cwd, _ := redactor.SafeText(s.CWD, MaxCommandViewBytes)
	argv := redactor.Argv(s.Argv, s.SensitiveArgs...)
	for index := range argv {
		argv[index], _ = BoundText(argv[index], MaxCommandViewBytes)
	}
	timeout := "none"
	if s.Timeout > 0 {
		timeout = s.Timeout.String()
	}
	view := CommandView{
		Executable:  executable,
		Argv:        argv,
		CWD:         cwd,
		Environment: s.Environment.Variables(),
		Timeout:     timeout,
		Output: OutputView{
			Mode:           s.Output.Mode,
			MaxStdoutBytes: s.Output.MaxStdoutBytes,
			MaxStderrBytes: s.Output.MaxStderrBytes,
		},
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return CommandView{}, newError(ErrorInvalid, s, Result{ExitCode: -1}, "cannot encode safe command view")
	}
	if len(encoded) > MaxCommandViewBytes {
		return CommandView{}, newError(ErrorInvalid, s, Result{ExitCode: -1}, "safe command view exceeds byte bound")
	}
	return view, nil
}

// MarshalJSON guarantees that encoding a CommandSpec cannot expose raw argv or
// environment values.
func (s CommandSpec) MarshalJSON() ([]byte, error) {
	view, err := s.SafeView()
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// String renders only the sanitized command identity.
func (s CommandSpec) String() string {
	view, err := s.SafeView()
	if err != nil {
		return err.Error()
	}
	return formatCommand(view.Executable, view.Argv)
}

// GoString renders only the sanitized command identity.
func (s CommandSpec) GoString() string { return s.String() }

// Result is a bounded, sanitized subprocess result. ExitCode is -1 when no
// portable numeric code is available. Cancellation terminates the spawned
// process group on supported Unix targets; Windows currently terminates only the
// direct child because no Job Object integration is implemented.
type Result struct {
	Stdout          string        `json:"stdout"`
	Stderr          string        `json:"stderr"`
	ExitCode        int           `json:"exit_code"`
	StartedAt       time.Time     `json:"started_at"`
	FinishedAt      time.Time     `json:"finished_at"`
	Duration        time.Duration `json:"duration"`
	StdoutTruncated bool          `json:"stdout_truncated"`
	StderrTruncated bool          `json:"stderr_truncated"`
	Canceled        bool          `json:"canceled"`
	TimedOut        bool          `json:"timed_out"`
}

// ErrorKind classifies a safe invocation failure.
type ErrorKind string

const (
	ErrorInvalid  ErrorKind = "invalid"
	ErrorStart    ErrorKind = "start"
	ErrorExit     ErrorKind = "exit"
	ErrorCanceled ErrorKind = "canceled"
	ErrorTimeout  ErrorKind = "timeout"
	ErrorOutput   ErrorKind = "output"
)

var (
	ErrCommandInvalid  = errors.New("invalid command specification")
	ErrCommandStart    = errors.New("command could not start")
	ErrCommandExit     = errors.New("command exited unsuccessfully")
	ErrCommandCanceled = errors.New("command canceled")
	ErrCommandTimeout  = errors.New("command timed out")
	ErrCommandOutput   = errors.New("command output failed")
)

// Error contains only sanitized command and output data. The underlying OS
// error is intentionally not retained because it may embed raw executable,
// argv, or environment text.
type Error struct {
	Kind       ErrorKind `json:"kind"`
	Executable string    `json:"executable"`
	Argv       []string  `json:"argv"`
	CWD        string    `json:"cwd"`
	Result     Result    `json:"result"`
	Reason     string    `json:"reason"`
}

type errorJSON Error

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	safe := e.safeCopy()
	message := string(safe.Kind) + " command " + formatCommand(safe.Executable, safe.Argv)
	if safe.Reason != "" {
		message += ": " + safe.Reason
	}
	if detail := strings.TrimSpace(safe.Result.Stderr); detail != "" && detail != safe.Reason {
		message += ": " + detail
	}
	message, _ = BoundText(message, MaxErrorBytes)
	return message
}

// GoString prevents debug formatting from bypassing the redaction boundary.
func (e *Error) GoString() string { return e.Error() }

// MarshalJSON reapplies structural redaction to guard against mutation or
// manually constructed Error values.
func (e Error) MarshalJSON() ([]byte, error) {
	safe := (&e).safeCopy()
	return json.Marshal(errorJSON(safe))
}

func (e *Error) safeCopy() Error {
	redactor := NewRedactor(sensitiveArgvValues(e.Argv)...)
	safe := *e
	safe.Executable, _ = redactor.SafeText(e.Executable, MaxCommandViewBytes)
	safe.CWD, _ = redactor.SafeText(e.CWD, MaxCommandViewBytes)
	safe.Argv = redactor.Argv(e.Argv)
	for index := range safe.Argv {
		safe.Argv[index], _ = BoundText(safe.Argv[index], MaxCommandViewBytes)
	}
	safe.Reason, _ = redactor.SafeText(e.Reason, MaxErrorBytes)
	safe.Result.Stdout, safe.Result.StdoutTruncated = redactResultText(redactor, e.Result.Stdout, e.Result.StdoutTruncated)
	safe.Result.Stderr, safe.Result.StderrTruncated = redactResultText(redactor, e.Result.Stderr, e.Result.StderrTruncated)
	return safe
}

func redactResultText(redactor Redactor, value string, alreadyTruncated bool) (string, bool) {
	safe, truncated := redactor.SafeText(value, int(MaxOutputLimit))
	return safe, alreadyTruncated || truncated
}

// Unwrap exposes only a stable safe sentinel.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	switch e.Kind {
	case ErrorInvalid:
		return ErrCommandInvalid
	case ErrorStart:
		return ErrCommandStart
	case ErrorExit:
		return ErrCommandExit
	case ErrorCanceled:
		return ErrCommandCanceled
	case ErrorTimeout:
		return ErrCommandTimeout
	case ErrorOutput:
		return ErrCommandOutput
	default:
		return nil
	}
}

// Is also supports the conventional context cancellation sentinels.
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	if target == context.Canceled && e.Kind == ErrorCanceled {
		return true
	}
	if target == context.DeadlineExceeded && e.Kind == ErrorTimeout {
		return true
	}
	return errors.Is(e.Unwrap(), target)
}

func newError(kind ErrorKind, spec CommandSpec, result Result, reason string) *Error {
	redactor := spec.effectiveRedactor()
	executable, _ := redactor.SafeText(spec.Executable, MaxCommandViewBytes)
	cwd, _ := redactor.SafeText(spec.CWD, MaxCommandViewBytes)
	argv := redactor.Argv(spec.Argv, spec.SensitiveArgs...)
	for index := range argv {
		argv[index], _ = BoundText(argv[index], MaxCommandViewBytes)
	}
	reason, _ = redactor.SafeText(reason, MaxErrorBytes)
	return &Error{
		Kind:       kind,
		Executable: executable,
		Argv:       argv,
		CWD:        cwd,
		Result:     result,
		Reason:     reason,
	}
}

func formatCommand(executable string, argv []string) string {
	parts := make([]string, 0, len(argv)+1)
	parts = append(parts, strconv.Quote(executable))
	for _, argument := range argv {
		parts = append(parts, strconv.Quote(argument))
	}
	return strings.Join(parts, " ")
}
