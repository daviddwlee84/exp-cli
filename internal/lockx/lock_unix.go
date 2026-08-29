//go:build unix && !aix

package lockx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type fileLock struct{ file *os.File }

func acquireProcessGate(context.Context) (func(), error) { return func() {}, nil }

func validatePlatformLockFile(file *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if uint64(stat.Nlink) != 1 {
		return fmt.Errorf("project lock file has %d hard links; want 1", stat.Nlink)
	}
	return nil
}

func acquireFile(ctx context.Context, file *os.File) (*fileLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd := int(file.Fd())
	lock := &fileLock{file: file}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
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
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	return errors.Join(unlockErr, lock.file.Close())
}
