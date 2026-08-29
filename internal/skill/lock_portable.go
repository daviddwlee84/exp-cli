//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !windows

package skill

import (
	"context"
	"fmt"
	"io"
	"runtime"
)

// Unknown platforms fail closed rather than using a create/remove lock that can
// permanently wedge after process death. Supported Unix, AIX, and Windows builds
// provide kernel-released advisory locks in platform files.
func acquireInstallLock(ctx context.Context, _ string) (io.Closer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("cross-process skill installation locks are unsupported on %s", runtime.GOOS)
}
