//go:build darwin || linux

package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRunPTYFirstFrameAndQuitWhileLoadBlocks(t *testing.T) {
	if os.Getenv("EXP_UI_PTY_HELPER") == "1" {
		runPTYHelper(t)
		return
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script utility is unavailable for PTY smoke test")
	}
	marker := t.TempDir() + string(os.PathSeparator) + "loader-started"
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command(script, "-q", "/dev/null", os.Args[0], "-test.run=^TestRunPTYFirstFrameAndQuitWhileLoadBlocks$")
	case "linux":
		child := shellQuote(os.Args[0]) + " -test.run=^TestRunPTYFirstFrameAndQuitWhileLoadBlocks$"
		command = exec.Command(script, "-qefc", child, "/dev/null")
	default:
		t.Skip("PTY smoke wrapper is not configured for " + runtime.GOOS)
	}
	command.Env = append(os.Environ(),
		"EXP_UI_PTY_HELPER=1",
		"EXP_UI_PTY_MARKER="+marker,
		"TERM=xterm-256color",
		"NO_COLOR=1",
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = command.Stdout
	captured := newPTYCapture("Loading local canonical")
	readDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(captured, stdout)
		readDone <- copyErr
	}()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-captured.found:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("first frame did not appear while load blocked:\n%s", captured.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			t.Fatalf("loader did not enter blocking callback:\n%s", captured.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := io.WriteString(stdin, "q"); err != nil {
		_ = command.Process.Kill()
		t.Fatal(err)
	}
	_ = stdin.Close()
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			t.Fatalf("PTY helper did not quit cleanly: %v\n%s", err, captured.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("q did not quit while load was blocked:\n%s", captured.String())
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(captured.String(), "Loading local canonical") {
		t.Fatalf("captured frame omitted local loading state:\n%s", captured.String())
	}
}

func runPTYHelper(t *testing.T) {
	marker := os.Getenv("EXP_UI_PTY_MARKER")
	if err := unix.IoctlSetWinsize(int(os.Stdout.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 100}); err != nil {
		t.Fatalf("set PTY size: %v", err)
	}
	err := Run(context.Background(), os.Stdin, os.Stdout, Options{
		Workspace:  "pty-workspace",
		Monochrome: true,
		Callbacks: Callbacks{LoadLocal: func(ctx context.Context, request Request) Response {
			if marker != "" {
				if err := os.WriteFile(marker, []byte("started\n"), 0o600); err != nil {
					return Failure(request, err)
				}
			}
			<-ctx.Done()
			return Failure(request, ctx.Err())
		}},
	})
	if err != nil {
		t.Fatalf("run TUI helper: %v", err)
	}
}

type ptyCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	needle []byte
	found  chan struct{}
	once   sync.Once
}

func newPTYCapture(needle string) *ptyCapture {
	return &ptyCapture{needle: []byte(needle), found: make(chan struct{})}
}

func (capture *ptyCapture) Write(value []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	count, err := capture.buffer.Write(value)
	if bytes.Contains(capture.buffer.Bytes(), capture.needle) {
		capture.once.Do(func() { close(capture.found) })
	}
	return count, err
}

func (capture *ptyCapture) String() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.buffer.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
