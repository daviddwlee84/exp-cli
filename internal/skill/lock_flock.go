//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type advisoryFileLock struct {
	file *os.File
}

func acquireInstallLock(ctx context.Context, path string) (io.Closer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	fd, err := unix.Open(path, flags|unix.O_CREAT|unix.O_EXCL, 0o600)
	created := err == nil
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Open(path, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open install lock %s", path)
	}
	closeWithError := func(base error) error {
		return errors.Join(base, file.Close())
	}
	if err := validateInstallLockFile(file, path); err != nil {
		return nil, closeWithError(err)
	}
	if created {
		if err := file.Chmod(0o600); err != nil {
			return nil, closeWithError(err)
		}
	}
	if err := validateInstallLockMode(file, path); err != nil {
		return nil, closeWithError(err)
	}

	lock := &advisoryFileLock{file: file}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			if err := errors.Join(validateInstallLockFile(file, path), validateInstallLockMode(file, path)); err != nil {
				_ = lock.Close()
				return nil, err
			}
			return lock, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
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

func validateInstallLockFile(file *os.File, path string) error {
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
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if uint64(stat.Nlink) != 1 {
		return fmt.Errorf("install lock path %s has %d hard links, want 1", path, stat.Nlink)
	}
	return nil
}

func validateInstallLockMode(file *os.File, path string) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		return fmt.Errorf("install lock path %s has mode %04o, want 0600", path, mode)
	}
	return nil
}

func (lock *advisoryFileLock) Close() error {
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	return errors.Join(unlockErr, lock.file.Close())
}
