//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package execx

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessCancellation places the child in a new process group and
// replaces CommandContext's direct-child kill with a group kill. Ordinary
// descendants therefore cannot outlive cancellation or a CommandSpec timeout.
func configureProcessCancellation(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
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
		// Preserve the group-level failure, but still make a best effort to stop
		// the direct child rather than leave the command itself running.
		directErr := command.Process.Kill()
		if directErr == nil || errors.Is(directErr, os.ErrProcessDone) {
			return err
		}
		return errors.Join(err, directErr)
	}
}
