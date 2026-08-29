//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	gitCancellationHelperModeEnv      = "EXP_GIT_CANCELLATION_HELPER"
	gitCancellationHelperLeaderPIDEnv = "EXP_GIT_CANCELLATION_LEADER_PID"
	gitCancellationHelperPIDPathEnv   = "EXP_GIT_CANCELLATION_PID_PATH"
	gitCancellationHelperReadyPathEnv = "EXP_GIT_CANCELLATION_READY_PATH"
	gitCancellationHelperBeatPathEnv  = "EXP_GIT_CANCELLATION_BEAT_PATH"
)

func TestExecRunnerCancellationStopsUnixProcessGroupAfterLeaderExit(t *testing.T) {
	directory := t.TempDir()
	pidPath := filepath.Join(directory, "descendant.pid")
	readyPath := filepath.Join(directory, "leader-exited")
	heartbeatPath := filepath.Join(directory, "descendant.heartbeat")
	t.Setenv(gitCancellationHelperModeEnv, "leader")
	t.Setenv(gitCancellationHelperPIDPathEnv, pidPath)
	t.Setenv(gitCancellationHelperReadyPathEnv, readyPath)
	t.Setenv(gitCancellationHelperBeatPathEnv, heartbeatPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		_, _, runErr := (ExecRunner{Binary: os.Args[0]}).Run(ctx, directory, []string{"-test.run=^TestGitCancellationHelper$"})
		completed <- runErr
	}()

	t.Cleanup(func() { killGitCancellationHelper(pidPath) })
	pid := waitForGitDescendant(t, pidPath, readyPath, heartbeatPath)
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Git runner error = %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Git runner did not return after cancellation")
	}

	before := gitHeartbeatSize(t, heartbeatPath)
	time.Sleep(200 * time.Millisecond)
	after := gitHeartbeatSize(t, heartbeatPath)
	if after != before {
		t.Fatalf("Git descendant %d remained alive: heartbeat grew from %d to %d", pid, before, after)
	}
}

func TestGitCancellationHelper(t *testing.T) {
	switch os.Getenv(gitCancellationHelperModeEnv) {
	case "":
		return
	case "leader":
		command := exec.Command(os.Args[0], "-test.run=^TestGitCancellationHelper$")
		command.Env = setGitCancellationHelperEnv(os.Environ(), gitCancellationHelperModeEnv, "descendant")
		command.Env = setGitCancellationHelperEnv(command.Env, gitCancellationHelperLeaderPIDEnv, strconv.Itoa(os.Getpid()))
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "start cancellation helper descendant: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv(gitCancellationHelperPIDPathEnv), []byte(strconv.Itoa(command.Process.Pid)), 0o600); err != nil {
			_ = command.Process.Kill()
			_, _ = fmt.Fprintf(os.Stderr, "write cancellation helper PID: %v\n", err)
			os.Exit(2)
		}
		os.Exit(0)
	case "descendant":
		leaderPID, err := strconv.Atoi(os.Getenv(gitCancellationHelperLeaderPIDEnv))
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "parse cancellation helper leader PID: %v\n", err)
			os.Exit(2)
		}
		for os.Getppid() == leaderPID {
			time.Sleep(time.Millisecond)
		}
		if err := os.WriteFile(os.Getenv(gitCancellationHelperReadyPathEnv), []byte("ready"), 0o600); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "write cancellation helper readiness: %v\n", err)
			os.Exit(2)
		}
		heartbeat, err := os.OpenFile(os.Getenv(gitCancellationHelperBeatPathEnv), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "open cancellation helper heartbeat: %v\n", err)
			os.Exit(2)
		}
		defer heartbeat.Close()
		_, _ = fmt.Fprintln(os.Stdout, "descendant holds stdout open")
		_, _ = fmt.Fprintln(os.Stderr, "descendant holds stderr open")
		for {
			if _, err := heartbeat.Write([]byte("x")); err != nil {
				os.Exit(2)
			}
			time.Sleep(10 * time.Millisecond)
		}
	default:
		t.Fatalf("unknown cancellation helper mode %q", os.Getenv(gitCancellationHelperModeEnv))
	}
}

func waitForGitDescendant(t *testing.T, pidPath, readyPath, heartbeatPath string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pidBytes, pidErr := os.ReadFile(pidPath)
		_, readyErr := os.Stat(readyPath)
		heartbeat, heartbeatErr := os.Stat(heartbeatPath)
		if pidErr == nil && readyErr == nil && heartbeatErr == nil && heartbeat.Size() > 0 {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Git leader did not exit with its descendant alive")
	return 0
}

func gitHeartbeatSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func killGitCancellationHelper(pidPath string) {
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err == nil && pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func setGitCancellationHelperEnv(environment []string, name, value string) []string {
	prefix := name + "="
	result := append([]string(nil), environment...)
	for index, entry := range result {
		if strings.HasPrefix(entry, prefix) {
			result[index] = prefix + value
			return result
		}
	}
	return append(result, prefix+value)
}
