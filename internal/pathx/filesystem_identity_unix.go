//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package pathx

import (
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// DirectoryFilesystemIdentity returns a stable local filesystem identifier for a directory.
func DirectoryFilesystemIdentity(path string) (string, error) {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return "", err
	}
	return fmt.Sprintf("unix:%x:%x", uint64(stat.Dev), uint64(stat.Ino)), nil
}

func protectPrivateOpenFile(_ *os.File, _ fs.FileMode, _ bool) error { return nil }

func checkPrivateOpenFile(file *os.File, want fs.FileMode, description string, singleLink bool) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	mode := os.FileMode(stat.Mode)
	if mode.Perm() != want.Perm() || mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("%s mode is %s, want %04o", description, mode, want.Perm())
	}
	if singleLink && uint64(stat.Nlink) != 1 {
		return fmt.Errorf("%s has %d hard links; want 1", description, stat.Nlink)
	}
	return nil
}
