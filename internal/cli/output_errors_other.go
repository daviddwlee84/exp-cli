//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package cli

func isBrokenPipeError(error) bool {
	return false
}
