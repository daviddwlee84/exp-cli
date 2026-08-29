//go:build aix

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

// AIX record locks are process-scoped: closing any descriptor for the same file
// releases the process's lock. Serialize before opening so a canceled waiter
// cannot invalidate another goroutine's active installer lock.
var aixInstallProcessGate = func() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
}()

type aixInstallLock struct {
	file    *os.File
	release func()
}

func acquireInstallLock(ctx context.Context, path string) (io.Closer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-aixInstallProcessGate:
	}
	releaseGate := func() { aixInstallProcessGate <- struct{}{} }
	file, err := openAIXInstallLockFile(path)
	if err != nil {
		releaseGate()
		return nil, err
	}
	request := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(0), Start: 0, Len: 1}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			releaseGate()
			return nil, err
		}
		err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &request)
		if err == nil {
			if err := validateAIXInstallLockFile(file, path); err != nil {
				_ = file.Close()
				releaseGate()
				return nil, err
			}
			return &aixInstallLock{file: file, release: releaseGate}, nil
		}
		if !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			_ = file.Close()
			releaseGate()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			releaseGate()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func openAIXInstallLockFile(path string) (*os.File, error) {
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
	closeWithError := func(base error) (*os.File, error) {
		return nil, errors.Join(base, file.Close())
	}
	if err := validateAIXInstallLockFile(file, path); err != nil {
		return closeWithError(err)
	}
	if created {
		if err := file.Chmod(0o600); err != nil {
			return closeWithError(err)
		}
	}
	info, err := file.Stat()
	if err != nil {
		return closeWithError(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		return closeWithError(fmt.Errorf("install lock path %s has mode %04o, want 0600", path, mode))
	}
	return file, nil
}

func validateAIXInstallLockFile(file *os.File, path string) error {
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

func (lock *aixInstallLock) Close() error {
	request := unix.Flock_t{Type: unix.F_UNLCK, Whence: int16(0), Start: 0, Len: 1}
	unlockErr := unix.FcntlFlock(lock.file.Fd(), unix.F_SETLK, &request)
	closeErr := lock.file.Close()
	lock.release()
	return errors.Join(unlockErr, closeErr)
}
