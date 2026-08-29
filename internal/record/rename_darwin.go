//go:build darwin

package record

import (
	"os"

	"golang.org/x/sys/unix"
)

func exchangeAtomic(directory *os.File, first, second string) error {
	fd := int(directory.Fd())
	return unix.RenameatxNp(fd, first, fd, second, unix.RENAME_SWAP)
}
