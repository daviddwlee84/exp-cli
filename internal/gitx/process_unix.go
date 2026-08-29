//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcessCancellation(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return killProcessGroup(command) }
}

func watchProcessGroupCancellation(ctx context.Context, command *exec.Cmd) func() {
	completed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = killProcessGroup(command)
		close(completed)
	})
	return func() {
		if !stop() {
			<-completed
		}
	}
}

func killProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	directErr := command.Process.Kill()
	if directErr == nil || errors.Is(directErr, os.ErrProcessDone) {
		return err
	}
	return errors.Join(err, directErr)
}
