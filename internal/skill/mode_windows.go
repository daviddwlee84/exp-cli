//go:build windows

package skill

import (
	"io/fs"
	"os"
)

const windowsWritableFileMode fs.FileMode = 0o666

func expectedInstalledFileMode() fs.FileMode { return windowsWritableFileMode }

func installedFileModeCurrent(mode fs.FileMode) bool {
	// Windows exposes only the read-only attribute through permission bits. A
	// writable regular file reports write bits (normally 0666); a read-only file
	// reports 0444 and must be replaced rather than chmodded in place.
	return mode.Perm()&0o222 != 0
}

func installedDirectoryModeCurrent(fs.FileMode) bool {
	return true
}

func repairInstalledDirectory(_ *os.Root, _ string, _ fs.FileInfo) (bool, error) {
	// Windows does not represent POSIX directory permission bits. Directory mode
	// convergence is therefore intentionally a no-op on this platform.
	return false, nil
}
