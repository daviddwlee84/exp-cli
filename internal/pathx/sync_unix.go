//go:build unix

package pathx

import (
	"os"

	"golang.org/x/sys/unix"
)

// SyncRoot durably flushes directory-entry changes made through root.
func SyncRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func openFileNoFollow(root *os.Root, relative string) (*os.File, error) {
	return root.OpenFile(relative, os.O_RDONLY|unix.O_NOFOLLOW, 0)
}
