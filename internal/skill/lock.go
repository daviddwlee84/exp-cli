package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type installDestination struct {
	requested string
	canonical string
	identity  fs.FileInfo
	created   bool
}

// resolveInstallDestination creates the requested root if necessary, resolves it
// once, and captures the identity that both locking and rooted I/O must use.
func resolveInstallDestination(dir, label string) (installDestination, error) {
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return installDestination{}, fmt.Errorf("make %s directory absolute: %w", label, err)
	}
	absoluteDir = filepath.Clean(absoluteDir)
	destination := installDestination{requested: absoluteDir}
	if _, statErr := os.Lstat(absoluteDir); errors.Is(statErr, fs.ErrNotExist) {
		destination.created = true
	} else if statErr != nil {
		return destination, fmt.Errorf("inspect requested %s directory: %w", label, statErr)
	}
	if err := os.MkdirAll(absoluteDir, installedDirMode); err != nil {
		return destination, fmt.Errorf("create %s directory for lock: %w", label, err)
	}
	canonicalDir, err := filepath.EvalSymlinks(absoluteDir)
	if err != nil {
		return destination, fmt.Errorf("canonicalize %s directory for lock: %w", label, err)
	}
	canonicalDir, err = filepath.Abs(canonicalDir)
	if err != nil {
		return destination, fmt.Errorf("make canonical %s directory absolute: %w", label, err)
	}
	canonicalDir = filepath.Clean(canonicalDir)
	destination.canonical = canonicalDir
	if filepath.Dir(canonicalDir) == canonicalDir {
		return destination, fmt.Errorf("refuse to use filesystem root as %s directory", label)
	}

	canonicalInfo, err := os.Lstat(canonicalDir)
	if err != nil {
		return destination, fmt.Errorf("inspect canonical %s directory: %w", label, err)
	}
	if canonicalInfo.Mode()&os.ModeSymlink != 0 || !canonicalInfo.IsDir() {
		return destination, fmt.Errorf("canonical %s destination is not a real directory", label)
	}
	destination.identity = canonicalInfo
	requestedInfo, err := os.Stat(absoluteDir)
	if err != nil {
		return destination, fmt.Errorf("inspect requested %s directory: %w", label, err)
	}
	if !requestedInfo.IsDir() || !os.SameFile(canonicalInfo, requestedInfo) {
		return destination, fmt.Errorf("requested %s destination changed during resolution", label)
	}
	return destination, nil
}

// withInstallLock keeps the lock beside the selected canonical directory and
// opens that exact directory after acquisition. Revalidation binds the opened
// root and the caller's original path to the identity used to choose the lock.
func withInstallLock(
	ctx context.Context,
	destination installDestination,
	label string,
	acquire func(context.Context, string) (io.Closer, error),
	operation func(*os.Root) error,
) (resultErr error) {
	if operation == nil {
		return fmt.Errorf("%s lock requires an operation", label)
	}
	if acquire == nil {
		acquire = acquireInstallLock
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	lockPath := filepath.Join(filepath.Dir(destination.canonical), "."+filepath.Base(destination.canonical)+".lock")
	lock, err := acquire(ctx, lockPath)
	if err != nil {
		return fmt.Errorf("acquire %s lock: %w", label, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeInstallLock(lock))
	}()

	root, err := os.OpenRoot(destination.canonical)
	if err != nil {
		return fmt.Errorf("open canonical %s destination %s: %w", label, destination.canonical, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	if err := revalidateInstallDestination(destination, root, label); err != nil {
		return err
	}
	operationErr := operation(root)
	identityErr := revalidateInstallDestination(destination, root, label)
	return errors.Join(operationErr, identityErr)
}

func revalidateInstallDestination(destination installDestination, root *os.Root, label string) error {
	openedInfo, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect opened %s destination: %w", label, err)
	}
	canonicalInfo, err := os.Lstat(destination.canonical)
	if err != nil {
		return fmt.Errorf("reinspect canonical %s destination: %w", label, err)
	}
	requestedInfo, err := os.Stat(destination.requested)
	if err != nil {
		return fmt.Errorf("reinspect requested %s destination: %w", label, err)
	}
	if canonicalInfo.Mode()&os.ModeSymlink != 0 || !canonicalInfo.IsDir() || !openedInfo.IsDir() || !requestedInfo.IsDir() {
		return fmt.Errorf("refuse changed or unsafe %s destination", label)
	}
	if !os.SameFile(destination.identity, canonicalInfo) ||
		!os.SameFile(destination.identity, openedInfo) ||
		!os.SameFile(destination.identity, requestedInfo) {
		return fmt.Errorf("refuse %s destination whose identity changed while waiting for the lock", label)
	}
	return nil
}

func closeInstallLock(lock io.Closer) error {
	if lock == nil {
		return nil
	}
	return lock.Close()
}
