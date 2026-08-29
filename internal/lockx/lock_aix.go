//go:build aix

package lockx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// AIX record locks are process-scoped: closing any descriptor for the same file
// can release the process's lock. Serialize before opening the lock file so a
// canceled waiter cannot invalidate another goroutine's active lock.
var aixProcessGate = func() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
}()

func acquireProcessGate(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-aixProcessGate:
		return func() { aixProcessGate <- struct{}{} }, nil
	}
}

type fileLock struct{ file *os.File }

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
	request := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(0), Start: 0, Len: 1}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &request)
		if err == nil {
			return &fileLock{file: file}, nil
		}
		if !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
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
	request := unix.Flock_t{Type: unix.F_UNLCK, Whence: int16(0), Start: 0, Len: 1}
	unlockErr := unix.FcntlFlock(lock.file.Fd(), unix.F_SETLK, &request)
	return errors.Join(unlockErr, lock.file.Close())
}
