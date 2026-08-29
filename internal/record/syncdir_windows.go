//go:build windows

package record

import "os"

// Windows has no portable equivalent to POSIX directory fsync. Flush the
// published file handle after linking so NTFS can persist file metadata and use
// this no-op only for the unavailable directory-handle flush.
func syncPublishedFile(root *os.Root, name string) error {
	file, err := root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectory(*os.Root) error { return nil }
