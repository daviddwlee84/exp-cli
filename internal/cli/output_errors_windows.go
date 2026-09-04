//go:build windows

package cli

import (
	"errors"
	"syscall"
)

func isBrokenPipeError(err error) bool {
	return errors.Is(err, syscall.ERROR_BROKEN_PIPE)
}
