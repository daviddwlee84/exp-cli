//go:build unix

package project

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func validateReceiptFile(file *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if uint64(stat.Nlink) != 1 {
		return fmt.Errorf("project initialization receipt has %d hard links; want 1", stat.Nlink)
	}
	if os.FileMode(stat.Mode).Perm() != 0o600 {
		if err := unix.Fchmod(int(file.Fd()), 0o600); err != nil {
			return fmt.Errorf("protect project initialization receipt: %w", err)
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync project initialization receipt mode: %w", err)
		}
	}
	return nil
}
