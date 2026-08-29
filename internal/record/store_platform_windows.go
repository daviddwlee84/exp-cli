//go:build windows

package record

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func validatePrivateCoordinationFile(file *os.File) error {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information); err != nil {
		return err
	}
	if information.NumberOfLinks != 1 {
		return fmt.Errorf("coordination file has %d hard links; want 1", information.NumberOfLinks)
	}
	return nil
}
