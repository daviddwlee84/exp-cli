//go:build windows

package lockx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func acquireProcessGate(context.Context) (func(), error) { return func() {}, nil }

type fileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func validatePlatformLockFile(file *os.File) error {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information); err != nil {
		return err
	}
	if information.NumberOfLinks != 1 {
		return fmt.Errorf("project lock file has %d hard links; want 1", information.NumberOfLinks)
	}
	return nil
}

func acquireFile(ctx context.Context, file *os.File) (*fileLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock := &fileLock{file: file}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.overlapped)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (lock *fileLock) Close() error {
	unlockErr := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, &lock.overlapped)
	return errors.Join(unlockErr, lock.file.Close())
}
