// Package lockx provides the project-wide advisory lock shared by every linked
// worktree through the Git common directory.
package lockx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

const lockName = "lock"

// Owner is bounded, non-authoritative diagnostic metadata written only after
// the platform advisory lock has been acquired.
type Owner struct {
	PID        int       `json:"pid"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// AcquireError reports context cancellation together with safely readable
// owner metadata. Callers must never use PID metadata to break a lock.
type AcquireError struct {
	Dir   string
	Owner *Owner
	Err   error
}

func (e *AcquireError) Error() string {
	if e.Owner == nil {
		return fmt.Sprintf("acquire project lock %s: %v", e.Dir, e.Err)
	}
	return fmt.Sprintf("acquire project lock %s: %v (owner pid %d since %s)", e.Dir, e.Err, e.Owner.PID, e.Owner.AcquiredAt.UTC().Format(time.RFC3339Nano))
}
func (e *AcquireError) Unwrap() error { return e.Err }

// WithTrustedRoot creates and locks relativeDir beneath an already canonical
// absolute trustedRoot without following any symbolic-link component. The
// operation receives the opened lock-directory root and must keep local-state
// I/O relative to it.
func WithTrustedRoot(ctx context.Context, trustedRoot, relativeDir string, operation func(*os.Root) error) (err error) {
	if operation == nil {
		return errors.New("project lock requires an operation")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if trustedRoot == "" {
		return errors.New("trusted project lock root is empty")
	}
	if err := pathx.ValidateRelativePOSIX(relativeDir, true); err != nil {
		return fmt.Errorf("invalid project lock directory: %w", err)
	}
	canonicalTrustedRoot, err := pathx.Canonical(trustedRoot)
	if err != nil {
		return fmt.Errorf("canonicalize trusted project lock root: %w", err)
	}
	trustedRoot = canonicalTrustedRoot
	trusted, err := pathx.OpenCanonicalRootNoSymlinks(trustedRoot)
	if err != nil {
		return fmt.Errorf("open trusted project lock root: %w", err)
	}
	defer trusted.Close()
	lockDir, _, err := pathx.EnsureRootAtNoSymlinks(trusted, relativeDir, 0o700)
	if err != nil {
		return fmt.Errorf("open project lock directory: %w", err)
	}
	defer lockDir.Close()
	if err := chmodRoot(lockDir, 0o700); err != nil {
		return fmt.Errorf("protect project lock directory: %w", err)
	}
	if err := verifyLockRoots(trustedRoot, trusted, relativeDir, lockDir); err != nil {
		return err
	}
	leaveProcessGate, err := acquireProcessGate(ctx)
	if err != nil {
		return &AcquireError{Dir: filepath.Join(trustedRoot, filepath.FromSlash(relativeDir)), Err: err}
	}
	defer leaveProcessGate()

	file, err := openLockFile(lockDir)
	if err != nil {
		return fmt.Errorf("open project lock file: %w", err)
	}
	ownedByLock := false
	defer func() {
		if !ownedByLock {
			_ = file.Close()
		}
	}()
	if err := validateLockFile(lockDir, file); err != nil {
		return err
	}
	lock, err := acquireFile(ctx, file)
	if err != nil {
		owner := readOwnerFile(file)
		return &AcquireError{Dir: filepath.Join(trustedRoot, filepath.FromSlash(relativeDir)), Owner: owner, Err: err}
	}
	ownedByLock = true
	defer func() {
		if releaseErr := lock.Close(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release project lock: %w", releaseErr))
		}
	}()
	if err := validateLockFile(lockDir, file); err != nil {
		return err
	}
	if err := verifyLockRoots(trustedRoot, trusted, relativeDir, lockDir); err != nil {
		return err
	}
	if err := writeOwner(file); err != nil {
		return fmt.Errorf("write project lock owner metadata: %w", err)
	}
	if err := errors.Join(validateLockFile(lockDir, file), verifyLockRoots(trustedRoot, trusted, relativeDir, lockDir)); err != nil {
		return fmt.Errorf("project lock identity changed before operation: %w", err)
	}
	operationErr := operation(lockDir)
	verificationErr := errors.Join(
		verifyLockRoots(trustedRoot, trusted, relativeDir, lockDir),
		validateLockFile(lockDir, file),
	)
	if verificationErr != nil {
		verificationErr = fmt.Errorf("project lock identity changed during operation: %w", verificationErr)
	}
	return errors.Join(operationErr, verificationErr)
}

// WithDir is a compatibility convenience for callers without a separately
// established trust root. Security-sensitive project callers should use
// WithTrustedRoot with the canonical Git common directory.
func WithDir(ctx context.Context, dir string, operation func() error) error {
	if operation == nil {
		return errors.New("project lock requires an operation")
	}
	if dir == "" {
		return errors.New("project lock directory is empty")
	}
	canonical, err := pathx.Canonical(dir)
	if err != nil {
		return fmt.Errorf("canonicalize project lock directory: %w", err)
	}
	ancestor := canonical
	for {
		info, statErr := os.Lstat(ancestor)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("project lock ancestor must be a real directory")
			}
			break
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("inspect project lock ancestor: %w", statErr)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("find existing project lock ancestor: %w", statErr)
		}
		ancestor = parent
	}
	relative, err := filepath.Rel(ancestor, canonical)
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	return WithTrustedRoot(ctx, ancestor, relative, func(*os.Root) error { return operation() })
}

func verifyLockRoots(trustedPath string, trusted *os.Root, relative string, lockDir *os.Root) error {
	if err := pathx.VerifyRootPath(trustedPath, trusted); err != nil {
		return fmt.Errorf("trusted project lock root changed: %w", err)
	}
	if err := pathx.VerifyRootAt(trusted, relative, lockDir); err != nil {
		return fmt.Errorf("project lock directory changed: %w", err)
	}
	return nil
}

func chmodRoot(root *os.Root, mode fs.FileMode) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Chmod(mode)
}

func openLockFile(root *os.Root) (*os.File, error) {
	for attempt := 0; attempt < 16; attempt++ {
		info, err := root.Lstat(lockName)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return nil, errors.New("project lock file must be a regular non-symlink file")
			}
			return root.OpenFile(lockName, os.O_RDWR, 0)
		case errors.Is(err, fs.ErrNotExist):
			file, createErr := root.OpenFile(lockName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if errors.Is(createErr, fs.ErrExist) {
				continue
			}
			return file, createErr
		default:
			return nil, err
		}
	}
	return nil, errors.New("project lock file changed repeatedly while opening")
}

func validateLockFile(root *os.Root, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect project lock file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("project lock file must be regular")
	}
	if err := validatePlatformLockFile(file); err != nil {
		return fmt.Errorf("validate project lock file: %w", err)
	}
	pathInfo, err := root.Lstat(lockName)
	if err != nil {
		return fmt.Errorf("recheck project lock file: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(info, pathInfo) {
		return errors.New("project lock file changed identity")
	}
	return nil
}

func writeOwner(file *os.File) error {
	owner := Owner{PID: os.Getpid(), AcquiredAt: time.Now().UTC()}
	data, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func readOwnerFile(file *os.File) *Owner {
	if file == nil {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return nil
	}
	var owner Owner
	if json.Unmarshal(data, &owner) != nil || owner.PID <= 0 || owner.AcquiredAt.IsZero() {
		return nil
	}
	owner.AcquiredAt = owner.AcquiredAt.UTC()
	return &owner
}
