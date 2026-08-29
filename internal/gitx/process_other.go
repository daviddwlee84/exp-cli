//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package gitx

import (
	"context"
	"os/exec"
)

func configureProcessCancellation(*exec.Cmd) {}

func watchProcessGroupCancellation(context.Context, *exec.Cmd) func() {
	return func() {}
}
