//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris && !windows

package cli

func isBrokenPipeError(error) bool {
	return false
}
