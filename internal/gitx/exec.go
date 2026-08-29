// Package gitx invokes the installed Git executable with explicit argument
// arrays. It never constructs or executes a shell command string.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// MaxGitOutputBytes bounds each Git stdout and stderr capture independently.
	MaxGitOutputBytes = 4 << 20
	gitWaitDelay      = time.Second
)

var (
	ErrNotRepository  = errors.New("not a Git working tree")
	ErrBareRepository = errors.New("bare Git repositories are unsupported")
	ErrOutputLimit    = errors.New("Git output exceeds byte limit")
)

// Runner is the narrow injected seam used by Git discovery tests.
type Runner interface {
	Run(ctx context.Context, dir string, args []string) (stdout string, stderr string, err error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(context.Context, string, []string) (string, string, error)

func (function RunnerFunc) Run(ctx context.Context, dir string, args []string) (string, string, error) {
	return function(ctx, dir, args)
}

// ExecRunner invokes an installed Git binary directly.
type ExecRunner struct{ Binary string }

func (runner ExecRunner) Run(ctx context.Context, dir string, args []string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	binary := runner.Binary
	if binary == "" {
		binary = "git"
	}
	command := exec.CommandContext(ctx, binary, append([]string(nil), args...)...)
	configureProcessCancellation(command)
	command.WaitDelay = gitWaitDelay
	command.Dir = dir
	command.Env = sanitizedGitEnvironment(os.Environ())
	stdout := &boundedBuffer{limit: MaxGitOutputBytes}
	stderr := &boundedBuffer{limit: MaxGitOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Start()
	if err == nil {
		stopCancellationWatch := watchProcessGroupCancellation(ctx, command)
		err = command.Wait()
		stopCancellationWatch()
	}
	if contextErr := ctx.Err(); contextErr != nil {
		err = errors.Join(contextErr, err)
	}
	if stdout.truncated || stderr.truncated {
		err = errors.Join(err, ErrOutputLimit)
	}
	return stdout.String(), stderr.String(), err
}

// Error preserves Git's argument array and stderr without shell re-rendering.
type Error struct {
	Dir    string
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	message := strings.TrimSpace(e.Stderr)
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), message)
}
func (e *Error) Unwrap() error { return e.Err }

func run(ctx context.Context, runner Runner, dir string, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	argumentCopy := append([]string(nil), args...)
	stdout, stderr, err := runner.Run(ctx, dir, argumentCopy)
	if err == nil {
		return trimGitTerminator(stdout), nil
	}
	if strings.Contains(strings.ToLower(stderr), "not a git repository") {
		return "", fmt.Errorf("%s: %w", dir, ErrNotRepository)
	}
	return "", &Error{Dir: dir, Args: argumentCopy, Stderr: stderr, Err: err}
}

func trimGitTerminator(output string) string {
	return strings.TrimSuffix(output, "\n")
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.buffer.Write(data)
	}
	if original > remaining {
		buffer.truncated = true
	}
	return original, nil
}

func (buffer *boundedBuffer) String() string { return buffer.buffer.String() }

func sanitizedGitEnvironment(environment []string) []string {
	blocked := map[string]struct{}{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
		"GIT_CEILING_DIRECTORIES":          {},
		"GIT_COMMON_DIR":                   {},
		"GIT_CONFIG":                       {},
		"GIT_CONFIG_COUNT":                 {},
		"GIT_CONFIG_GLOBAL":                {},
		"GIT_CONFIG_PARAMETERS":            {},
		"GIT_CONFIG_SYSTEM":                {},
		"GIT_DIR":                          {},
		"GIT_DISCOVERY_ACROSS_FILESYSTEM":  {},
		"GIT_GRAFT_FILE":                   {},
		"GIT_IMPLICIT_WORK_TREE":           {},
		"GIT_INDEX_FILE":                   {},
		"GIT_INTERNAL_SUPER_PREFIX":        {},
		"GIT_NAMESPACE":                    {},
		"GIT_NO_REPLACE_OBJECTS":           {},
		"GIT_OBJECT_DIRECTORY":             {},
		"GIT_PREFIX":                       {},
		"GIT_QUARANTINE_PATH":              {},
		"GIT_REPLACE_REF_BASE":             {},
		"GIT_SHALLOW_FILE":                 {},
		"GIT_WORK_TREE":                    {},
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		canonicalName := strings.ToUpper(name)
		if _, remove := blocked[canonicalName]; remove || strings.HasPrefix(canonicalName, "GIT_CONFIG_KEY_") || strings.HasPrefix(canonicalName, "GIT_CONFIG_VALUE_") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
