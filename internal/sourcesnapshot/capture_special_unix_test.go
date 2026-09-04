//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package sourcesnapshot

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"golang.org/x/sys/unix"
)

func TestDirtyCaptureRejectsFIFOAndSocket(t *testing.T) {
	t.Run("fifo", func(t *testing.T) {
		repository, base, source := newCaptureRepository(t, ".")
		info, _ := gitx.Discover(t.Context(), repository)
		if err := unix.Mkfifo(filepath.Join(repository, "pipe"), 0o600); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
		if _, err := (Capturer{}).CaptureDirty(t.Context(), dirtyRequest(t, repository, info.GitCommonDir, base, source)); !errors.Is(err, pathx.ErrNotRegular) {
			t.Fatalf("FIFO capture error = %v", err)
		}
	})

	t.Run("socket", func(t *testing.T) {
		repository, base, source := newCaptureRepository(t, ".")
		info, _ := gitx.Discover(t.Context(), repository)
		shortPath, err := os.MkdirTemp("/tmp", "exp-sock-")
		if err != nil {
			t.Skipf("short socket path unavailable: %v", err)
		}
		if err := os.Remove(shortPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(repository, shortPath); err != nil {
			t.Skipf("short socket alias unavailable: %v", err)
		}
		defer os.Remove(shortPath)
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(shortPath, "socket"), Net: "unix"})
		if err != nil {
			t.Skipf("Unix sockets unavailable: %v", err)
		}
		defer listener.Close()
		if _, err := (Capturer{}).CaptureDirty(t.Context(), dirtyRequest(t, repository, info.GitCommonDir, base, source)); !errors.Is(err, pathx.ErrNotRegular) {
			t.Fatalf("socket capture error = %v", err)
		}
	})
}
