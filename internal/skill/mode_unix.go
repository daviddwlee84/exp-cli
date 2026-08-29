//go:build !windows

package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

func expectedInstalledFileMode() fs.FileMode { return installedFileMode }

func installedFileModeCurrent(mode fs.FileMode) bool {
	return mode.Perm() == installedFileMode
}

func installedDirectoryModeCurrent(mode fs.FileMode) bool {
	return mode.Perm() == installedDirMode
}

func repairInstalledDirectory(root *os.Root, name string, before fs.FileInfo) (changed bool, resultErr error) {
	if installedDirectoryModeCurrent(before.Mode()) {
		return false, nil
	}
	directory, err := root.Open(name)
	if err != nil {
		return false, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, directory.Close())
	}()
	opened, err := directory.Stat()
	if err != nil {
		return false, err
	}
	if !opened.IsDir() || !os.SameFile(before, opened) {
		return false, fmt.Errorf("directory identity changed before mode repair")
	}
	if err := directory.Chmod(installedDirMode); err != nil {
		return false, err
	}
	return true, nil
}
