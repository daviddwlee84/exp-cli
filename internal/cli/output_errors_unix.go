//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package cli

import (
	"errors"
	"syscall"
)

func isBrokenPipeError(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}
