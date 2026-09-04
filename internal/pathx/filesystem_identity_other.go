//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package pathx

import (
	"fmt"
	"io/fs"
	"os"
)

// DirectoryFilesystemIdentity reports unsupported platforms explicitly.
func DirectoryFilesystemIdentity(string) (string, error) {
	return "", fmt.Errorf("persistent directory identity is unsupported on this platform")
}

func protectPrivateOpenFile(_ *os.File, _ fs.FileMode, _ bool) error { return nil }

func checkPrivateOpenFile(file *os.File, want fs.FileMode, description string, singleLink bool) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm() != want.Perm() {
		return fmt.Errorf("%s mode is %s, want %04o", description, info.Mode(), want.Perm())
	}
	_ = singleLink
	return nil
}
