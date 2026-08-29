//go:build windows

package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

type windowsInstallLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireInstallLock(ctx context.Context, path string) (io.Closer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openWindowsInstallLockFile(path)
	if err != nil {
		return nil, err
	}
	lock := &windowsInstallLock{file: file}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.overlapped)
		if err == nil {
			if err := validateWindowsInstallLockFile(file, path); err != nil {
				_ = lock.Close()
				return nil, err
			}
			return lock, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_IO_PENDING) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func openWindowsInstallLockFile(path string) (*os.File, error) {
	for attempt := 0; attempt < 16; attempt++ {
		info, err := os.Lstat(path)
		created := false
		var file *os.File
		switch {
		case errors.Is(err, fs.ErrNotExist):
			file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			created = err == nil
		case err != nil:
			return nil, err
		case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular():
			return nil, fmt.Errorf("install lock path %s is not a regular non-symlink file", path)
		default:
			file, err = os.OpenFile(path, os.O_RDWR, 0)
		}
		if err != nil {
			return nil, err
		}
		closeWithError := func(base error) (*os.File, error) {
			return nil, errors.Join(base, file.Close())
		}
		if err := validateWindowsInstallLockFile(file, path); err != nil {
			return closeWithError(err)
		}
		if created {
			if err := file.Chmod(0o600); err != nil {
				return closeWithError(err)
			}
		}
		return file, nil
	}
	return nil, fmt.Errorf("install lock path %s changed repeatedly while opening", path)
}

func validateWindowsInstallLockFile(file *os.File, path string) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(opened, pathInfo) {
		return fmt.Errorf("install lock path %s is not a stable regular file", path)
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information); err != nil {
		return err
	}
	if information.NumberOfLinks != 1 {
		return fmt.Errorf("install lock path %s has %d hard links, want 1", path, information.NumberOfLinks)
	}
	return nil
}

func (lock *windowsInstallLock) Close() error {
	unlockErr := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, &lock.overlapped)
	return errors.Join(unlockErr, lock.file.Close())
}
