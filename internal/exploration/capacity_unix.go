//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package exploration

import (
	"errors"
	"golang.org/x/sys/unix"
)

func CheckCapacity(dir string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return err
	}
	if uint64(stat.Bavail)*uint64(stat.Bsize) < 64<<20 {
		return errors.New("artifact storage has less than 64 MiB free; choose another storage profile")
	}
	return nil
}
