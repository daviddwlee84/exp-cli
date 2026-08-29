//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package execx_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

func TestInvokerCancellationStopsUnixDescendants(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		timeout    time.Duration
		cancel     bool
		wantError  error
		resultFlag func(execx.Result) bool
	}{
		{name: "timeout", timeout: 350 * time.Millisecond, wantError: context.DeadlineExceeded, resultFlag: func(result execx.Result) bool { return result.TimedOut }},
		{name: "caller cancellation", timeout: 5 * time.Second, cancel: true, wantError: context.Canceled, resultFlag: func(result execx.Result) bool { return result.Canceled }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			pidPath := filepath.Join(directory, "descendant.pid")
			heartbeatPath := filepath.Join(directory, "descendant.heartbeat")
			shell, err := exec.LookPath("sh")
			if err != nil {
				t.Skip("POSIX shell is unavailable")
			}
			sleep, err := exec.LookPath("sleep")
			if err != nil {
				t.Skip("sleep is unavailable")
			}
			shell, err = filepath.Abs(shell)
			if err != nil {
				t.Fatal(err)
			}
			sleep, err = filepath.Abs(sleep)
			if err != nil {
				t.Fatal(err)
			}
			environment, err := execx.NewEnvironment(nil)
			if err != nil {
				t.Fatal(err)
			}
			// The outer shell starts an ordinary descendant shell that appends a
			// heartbeat until killed. All commands remain in the invoker-created
			// process group.
			script := `"$1" -c 'while :; do printf x >> "$1"; "$2" 0.05; done' child "$2" "$3" & child=$!; printf '%s' "$child" > "$4"; wait "$child"`
			spec := execx.CommandSpec{
				Executable:  filepath.Clean(shell),
				Argv:        []string{"-c", script, "parent", shell, heartbeatPath, sleep, pidPath},
				CWD:         filepath.Clean(directory),
				Environment: environment,
				Timeout:     testCase.timeout,
				Output:      execx.DefaultOutputPolicy(execx.OutputCapture),
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type outcome struct {
				result execx.Result
				err    error
			}
			completed := make(chan outcome, 1)
			go func() {
				result, invokeErr := execx.NewInvoker().Invoke(ctx, spec)
				completed <- outcome{result: result, err: invokeErr}
			}()

			pid := waitForDescendantPID(t, pidPath, heartbeatPath)
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			if testCase.cancel {
				cancel()
			}
			var finished outcome
			select {
			case finished = <-completed:
			case <-time.After(4 * time.Second):
				t.Fatal("Invoke did not return after cancellation")
			}
			if !errors.Is(finished.err, testCase.wantError) || !testCase.resultFlag(finished.result) {
				t.Fatalf("Invoke() = %+v, %v; want %v", finished.result, finished.err, testCase.wantError)
			}

			before := fileSize(t, heartbeatPath)
			time.Sleep(200 * time.Millisecond)
			after := fileSize(t, heartbeatPath)
			if after != before {
				t.Fatalf("descendant remained alive after cancellation: heartbeat grew from %d to %d bytes", before, after)
			}
		})
	}
}

func waitForDescendantPID(t *testing.T, pidPath, heartbeatPath string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pidBytes, pidErr := os.ReadFile(pidPath)
		heartbeat, heartbeatErr := os.Stat(heartbeatPath)
		if pidErr == nil && heartbeatErr == nil && heartbeat.Size() > 0 {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant did not start")
	return 0
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
