//go:build unix

package record

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func validatePrivateCoordinationFile(file *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if uint64(stat.Nlink) != 1 {
		return fmt.Errorf("coordination file has %d hard links; want 1", stat.Nlink)
	}
	if os.FileMode(stat.Mode).Perm() != 0o600 {
		return fmt.Errorf("coordination file mode is %04o; want 0600", os.FileMode(stat.Mode).Perm())
	}
	return nil
}
