package record

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

// AtomicStage names a failure-injection boundary in single-file publication.
type AtomicStage string

const (
	StageTempCreate AtomicStage = "temp_create"
	StageTempWrite  AtomicStage = "temp_write"
	StageFileSync   AtomicStage = "file_sync"
	StageRename     AtomicStage = "rename"
	StageDirSync    AtomicStage = "directory_sync"
)

var (
	// ErrAtomicConflict means the destination no longer has the identity or exact
	// bytes inspected by the caller. The racing bytes are left authoritative.
	ErrAtomicConflict = errors.New("atomic destination conflict")
	// ErrAtomicCASUnsupported means the platform has no safe descriptor-relative
	// exchange primitive. Replacement fails closed rather than using a racy rename.
	ErrAtomicCASUnsupported = errors.New("atomic compare-and-swap replacement is unsupported on this platform")
)

// AtomicHook runs immediately before a named operation. It is nil in production.
type AtomicHook func(stage AtomicStage, destination string) error

// AtomicWriteOptions distinguishes create from content-checked replacement.
type AtomicWriteOptions struct {
	// Expected is nil for create. For replacement it is the previously inspected
	// destination identity, and ExpectedContent contains the exact bytes observed
	// with that identity. Both are rechecked around the atomic exchange.
	Expected        fs.FileInfo
	ExpectedContent []byte
	// Mode defaults to 0644. Local coordination callers may request 0600.
	Mode fs.FileMode
	Hook AtomicHook
	// Verify is called at each rooted publication boundary. It lets callers bind
	// an additional coordination-root identity to this write.
	Verify func() error
}

// PublicationError reports whether the requested new bytes remain published at
// the destination after the failing operation returned.
type PublicationError struct {
	Stage     AtomicStage
	Published bool
	Err       error
}

func (e *PublicationError) Error() string {
	if e.Published {
		return fmt.Sprintf("atomic publication reached rename but failed at %s: %v", e.Stage, e.Err)
	}
	return fmt.Sprintf("atomic publication failed at %s: %v", e.Stage, e.Err)
}
func (e *PublicationError) Unwrap() error { return e.Err }

// AtomicWrite publishes bytes relative to one opened canonical root. Temporary
// creation, destination inspection, publication, cleanup, and directory sync all
// use rooted handles, so swapping a pathname ancestor cannot redirect an
// operation to another tree. Create is an atomic no-clobber operation. Replace
// uses an atomic exchange and verifies the displaced inode and exact bytes,
// rolling the exchange back if the expected content changed.
func AtomicWrite(root, relative string, data []byte, options AtomicWriteOptions) error {
	if err := pathx.ValidateRelativePOSIX(relative, false); err != nil {
		return publicationError(StageTempCreate, false, fmt.Errorf("unsafe destination %q: %w", relative, err))
	}
	canonicalRoot, err := pathx.Canonical(root)
	if err != nil {
		return publicationError(StageTempCreate, false, err)
	}
	rootHandle, err := pathx.OpenCanonicalRootNoSymlinks(canonicalRoot)
	if err != nil {
		return publicationError(StageTempCreate, false, err)
	}
	defer rootHandle.Close()
	return atomicWriteRoot(rootHandle, canonicalRoot, relative, data, options, false)
}

// AtomicWriteRoot is AtomicWrite for callers that already hold a trusted root
// handle, such as the project lock and initialization receipt paths. Canonical
// replacements fail with ErrAtomicCASUnsupported when the platform cannot
// atomically exchange and verify the old and new files.
func AtomicWriteRoot(root *os.Root, relative string, data []byte, options AtomicWriteOptions) error {
	return atomicWriteRootEntry(root, relative, data, options, false)
}

// AtomicWriteDerivedRoot publishes rebuildable derived bytes through one trusted
// root. Unlike AtomicWriteRoot, replacement may use a guarded rooted rename on
// platforms without atomic exchange; callers must never use it for canonical
// records or other irreplaceable state.
func AtomicWriteDerivedRoot(root *os.Root, relative string, data []byte, options AtomicWriteOptions) error {
	return atomicWriteRootEntry(root, relative, data, options, true)
}

func atomicWriteRootEntry(root *os.Root, relative string, data []byte, options AtomicWriteOptions, derived bool) error {
	if root == nil {
		return publicationError(StageTempCreate, false, errors.New("nil atomic filesystem root"))
	}
	if err := pathx.ValidateRelativePOSIX(relative, false); err != nil {
		return publicationError(StageTempCreate, false, fmt.Errorf("unsafe destination %q: %w", relative, err))
	}
	return atomicWriteRoot(root, filepath.Clean(root.Name()), relative, data, options, derived)
}

func atomicWriteRoot(rootHandle *os.Root, canonicalRoot, relative string, data []byte, options AtomicWriteOptions, derived bool) error {
	parentRelative := path.Dir(relative)
	var err error
	var parent *os.Root
	if options.Expected == nil {
		parent, _, err = pathx.EnsureRootAtNoSymlinks(rootHandle, parentRelative, 0o755)
	} else {
		parent, err = pathx.OpenRootAtNoSymlinks(rootHandle, parentRelative)
	}
	if err != nil {
		return publicationError(StageTempCreate, false, err)
	}
	defer parent.Close()
	destination := path.Base(relative)
	verifyRoots := func() error {
		if baseErr := verifyOpenedRoots(canonicalRoot, rootHandle, parentRelative, parent); baseErr != nil {
			return baseErr
		}
		if options.Verify != nil {
			return options.Verify()
		}
		return nil
	}
	expectedContentKnown := !derived || options.ExpectedContent != nil
	verifyExpectedDestination := func() error {
		return verifyExpectedAt(parent, destination, options.Expected, options.ExpectedContent, expectedContentKnown)
	}

	if err := verifyExpectedDestination(); err != nil {
		return publicationError(StageTempCreate, false, err)
	}
	if err := runAtomicHook(options.Hook, StageTempCreate, relative); err != nil {
		return publicationError(StageTempCreate, false, err)
	}
	if err := verifyRoots(); err != nil {
		return publicationError(StageTempCreate, false, err)
	}

	mode := options.Mode.Perm()
	if mode == 0 {
		mode = 0o644
	}
	temporaryName, temporary, err := createAtomicTemporary(parent)
	if err != nil {
		return publicationError(StageTempCreate, false, err)
	}
	temporaryIdentity, err := temporary.Stat()
	if err != nil {
		_ = temporary.Close()
		return publicationError(StageTempCreate, false, err)
	}
	defer func() {
		_ = temporary.Close()
		_ = removeNamedFileIfSame(parent, temporaryName, temporaryIdentity)
	}()
	if err := verifyRoots(); err != nil {
		return publicationError(StageTempCreate, false, err)
	}
	if err := temporary.Chmod(mode); err != nil {
		return publicationError(StageTempCreate, false, err)
	}
	if err := runAtomicHook(options.Hook, StageTempWrite, relative); err != nil {
		return publicationError(StageTempWrite, false, err)
	}
	if err := verifyRoots(); err != nil {
		return publicationError(StageTempWrite, false, err)
	}
	if _, err := io.Copy(temporary, bytes.NewReader(data)); err != nil {
		return publicationError(StageTempWrite, false, err)
	}
	if err := runAtomicHook(options.Hook, StageFileSync, relative); err != nil {
		return publicationError(StageFileSync, false, err)
	}
	if err := verifyRoots(); err != nil {
		return publicationError(StageFileSync, false, err)
	}
	if err := temporary.Sync(); err != nil {
		return publicationError(StageFileSync, false, err)
	}
	if err := verifyOpenFileContent(temporary, temporaryIdentity, data); err != nil {
		return publicationError(StageFileSync, false, fmt.Errorf("verify publication temporary: %w", err))
	}

	// This is the intentional deterministic race boundary. Checks after the hook
	// prove that both the destination and the temporary pathname still identify
	// the objects validated by the caller and writer.
	if err := verifyExpectedDestination(); err != nil {
		return publicationError(StageRename, false, err)
	}
	if err := runAtomicHook(options.Hook, StageRename, relative); err != nil {
		return publicationError(StageRename, false, err)
	}
	if err := verifyRoots(); err != nil {
		return publicationError(StageRename, false, err)
	}
	if err := verifyExpectedDestination(); err != nil {
		return publicationError(StageRename, false, err)
	}
	if err := verifyTemporaryAt(parent, temporaryName, temporary, temporaryIdentity, data); err != nil {
		return publicationError(StageRename, false, err)
	}

	if options.Expected == nil {
		if err := parent.Link(temporaryName, destination); err != nil {
			return publicationError(StageRename, false, err)
		}
		if err := verifyDestinationAt(parent, destination, temporaryIdentity, data); err != nil {
			published, rollbackErr := rollbackLinkedCreate(parent, temporaryName, destination, temporaryIdentity, err)
			return publicationError(StageRename, published, rollbackErr)
		}
		if err := verifyRoots(); err != nil {
			published, rollbackErr := rollbackLinkedCreate(parent, temporaryName, destination, temporaryIdentity, err)
			return publicationError(StageRename, published, rollbackErr)
		}
		if err := removeNamedFileIfSame(parent, temporaryName, temporaryIdentity); err != nil {
			return publicationError(StageRename, true, fmt.Errorf("remove publication temporary: %w", err))
		}
	} else {
		var published bool
		var publishErr error
		if derived {
			published, publishErr = replaceAtomicDerived(parent, temporaryName, destination, temporaryIdentity, data, options.Expected, options.ExpectedContent, expectedContentKnown, verifyRoots)
		} else {
			published, publishErr = replaceAtomicCAS(parent, temporaryName, destination, temporaryIdentity, data, options.Expected, options.ExpectedContent, verifyRoots)
		}
		if publishErr != nil {
			return publicationError(StageRename, published, publishErr)
		}
	}

	if err := runAtomicHook(options.Hook, StageDirSync, relative); err != nil {
		return publicationError(StageDirSync, true, err)
	}
	if err := verifyRoots(); err != nil {
		return publicationError(StageDirSync, true, err)
	}
	if err := syncPublishedFile(parent, destination); err != nil {
		return publicationError(StageDirSync, true, err)
	}
	if err := syncDirectory(parent); err != nil {
		return publicationError(StageDirSync, true, err)
	}
	if err := verifyRoots(); err != nil {
		return publicationError(StageDirSync, true, err)
	}
	if err := verifyDestinationAt(parent, destination, temporaryIdentity, data); err != nil {
		return publicationError(StageDirSync, true, fmt.Errorf("published file changed before completion: %w", err))
	}
	return nil
}

func replaceAtomicCAS(parent *os.Root, temporary, destination string, temporaryIdentity fs.FileInfo, temporaryContent []byte, expected fs.FileInfo, expectedContent []byte, verifyRoot func() error) (bool, error) {
	directory, err := parent.Open(".")
	if err != nil {
		return false, err
	}
	defer directory.Close()
	if err := exchangeAtomic(directory, temporary, destination); err != nil {
		return false, err
	}

	rollback := func(cause error) (bool, error) {
		if rollbackErr := exchangeAtomic(directory, temporary, destination); rollbackErr != nil {
			return true, errors.Join(cause, fmt.Errorf("rollback atomic exchange: %w", rollbackErr))
		}
		cleanupErr := removeNamedFileIfSame(parent, temporary, temporaryIdentity)
		syncErr := syncDirectory(parent)
		if cleanupErr != nil || syncErr != nil {
			cause = errors.Join(cause, fmt.Errorf("durably clean rolled-back replacement: %w", errors.Join(cleanupErr, syncErr)))
		}
		return false, cause
	}
	if err := verifyDestinationAt(parent, destination, temporaryIdentity, temporaryContent); err != nil {
		return rollback(fmt.Errorf("temporary changed during replacement: %w", err))
	}
	if err := verifyDestinationAt(parent, temporary, expected, expectedContent); err != nil {
		return rollback(fmt.Errorf("destination changed during replacement: %w", err))
	}
	if err := verifyRoot(); err != nil {
		return rollback(err)
	}
	if err := removeNamedFileIfSame(parent, temporary, expected); err != nil {
		return true, fmt.Errorf("remove displaced destination: %w", err)
	}
	return true, nil
}

func replaceAtomicDerived(parent *os.Root, temporary, destination string, temporaryIdentity fs.FileInfo, temporaryContent []byte, expected fs.FileInfo, expectedContent []byte, expectedContentKnown bool, verifyRoot func() error) (bool, error) {
	backup, backupIdentity, err := linkAtomicBackup(parent, destination)
	if err != nil {
		return false, err
	}
	defer func() { _ = removeNamedFileIfSame(parent, backup, backupIdentity) }()
	if err := verifyExpectedAt(parent, backup, expected, expectedContent, expectedContentKnown); err != nil {
		return false, fmt.Errorf("verify derived replacement backup: %w", err)
	}
	if err := verifyExpectedAt(parent, destination, expected, expectedContent, expectedContentKnown); err != nil {
		return false, err
	}
	if err := verifyDestinationAt(parent, temporary, temporaryIdentity, temporaryContent); err != nil {
		return false, fmt.Errorf("verify derived publication temporary: %w", err)
	}
	if err := verifyRoot(); err != nil {
		return false, err
	}
	if err := parent.Rename(temporary, destination); err != nil {
		return false, err
	}

	rollback := func(cause error) (bool, error) {
		_, currentErr := parent.Lstat(destination)
		if currentErr != nil {
			return true, errors.Join(cause, fmt.Errorf("inspect derived destination for rollback: %w", currentErr))
		}
		if err := parent.Rename(destination, temporary); err != nil {
			return true, errors.Join(cause, fmt.Errorf("preserve failed derived publication: %w", err))
		}
		if err := parent.Rename(backup, destination); err != nil {
			return true, errors.Join(cause, fmt.Errorf("restore derived destination: %w", err))
		}
		if err := verifyExpectedAt(parent, destination, expected, expectedContent, expectedContentKnown); err != nil {
			return true, errors.Join(cause, fmt.Errorf("verify restored derived destination: %w", err))
		}
		if err := syncDirectory(parent); err != nil {
			return false, errors.Join(cause, fmt.Errorf("sync derived rollback: %w", err))
		}
		return false, cause
	}
	if err := verifyDestinationAt(parent, destination, temporaryIdentity, temporaryContent); err != nil {
		return rollback(fmt.Errorf("derived temporary changed during replacement: %w", err))
	}
	if err := verifyRoot(); err != nil {
		return rollback(err)
	}
	if err := removeNamedFileIfSame(parent, backup, expected); err != nil {
		return true, fmt.Errorf("remove derived replacement backup: %w", err)
	}
	return true, nil
}

func linkAtomicBackup(parent *os.Root, destination string) (string, fs.FileInfo, error) {
	for attempt := 0; attempt < 128; attempt++ {
		name, err := randomAtomicName()
		if err != nil {
			return "", nil, err
		}
		if err := parent.Link(destination, name); errors.Is(err, fs.ErrExist) {
			continue
		} else if err != nil {
			return "", nil, err
		}
		info, err := parent.Lstat(name)
		if err != nil {
			return "", nil, err
		}
		return name, info, nil
	}
	return "", nil, errors.New("allocate unique atomic backup name")
}

func verifyOpenedRoots(canonicalRoot string, root *os.Root, parentRelative string, parent *os.Root) error {
	if canonicalRoot != "" {
		if err := pathx.VerifyRootPath(canonicalRoot, root); err != nil {
			return fmt.Errorf("canonical root changed during publication: %w", err)
		}
	}
	if err := pathx.VerifyRootAt(root, parentRelative, parent); err != nil {
		return fmt.Errorf("destination parent changed during publication: %w", err)
	}
	return nil
}

func verifyExpectedAt(parent *os.Root, destination string, expected fs.FileInfo, expectedContent []byte, checkContent bool) error {
	if checkContent || expected == nil {
		return verifyDestinationAt(parent, destination, expected, expectedContent)
	}
	file, info, err := pathx.OpenRegularFileNoFollow(parent, destination)
	if err != nil {
		return fmt.Errorf("inspect replacement destination: %w", errors.Join(ErrAtomicConflict, err))
	}
	if closeErr := file.Close(); closeErr != nil {
		return closeErr
	}
	if !os.SameFile(expected, info) {
		return fmt.Errorf("replacement destination identity changed: %w", ErrAtomicConflict)
	}
	return nil
}

func verifyDestinationAt(parent *os.Root, destination string, expected fs.FileInfo, expectedContent []byte) error {
	info, err := parent.Lstat(destination)
	if expected == nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("destination %s already exists: %w", destination, fs.ErrExist)
	}
	if err != nil {
		return fmt.Errorf("replacement destination changed: %w", errors.Join(ErrAtomicConflict, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("replacement destination is not a regular non-symlink file: %w", ErrAtomicConflict)
	}
	if !os.SameFile(expected, info) {
		return fmt.Errorf("replacement destination identity changed: %w", ErrAtomicConflict)
	}
	content, openedInfo, err := pathx.ReadBoundedRegularFile(context.Background(), parent, destination, int64(len(expectedContent)))
	if err != nil {
		return fmt.Errorf("read replacement destination: %w", errors.Join(ErrAtomicConflict, err))
	}
	if !os.SameFile(expected, openedInfo) {
		return fmt.Errorf("replacement destination changed while opening: %w", ErrAtomicConflict)
	}
	if !bytes.Equal(content, expectedContent) {
		return fmt.Errorf("replacement destination content changed: %w", ErrAtomicConflict)
	}
	return nil
}

func verifyTemporaryAt(parent *os.Root, name string, file *os.File, expected fs.FileInfo, content []byte) error {
	if err := verifyOpenFileContent(file, expected, content); err != nil {
		return fmt.Errorf("temporary handle changed: %w", err)
	}
	actual, openedInfo, err := pathx.ReadBoundedRegularFile(context.Background(), parent, name, int64(len(content)))
	if err != nil {
		return fmt.Errorf("temporary pathname changed: %w", errors.Join(ErrAtomicConflict, err))
	}
	if !os.SameFile(expected, openedInfo) || !bytes.Equal(actual, content) {
		return fmt.Errorf("temporary pathname no longer identifies written bytes: %w", ErrAtomicConflict)
	}
	return nil
}

func verifyOpenFileContent(file *os.File, expected fs.FileInfo, content []byte) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return errors.Join(ErrAtomicConflict, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	actual, err := io.ReadAll(io.LimitReader(file, int64(len(content))+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, content) {
		return ErrAtomicConflict
	}
	return nil
}

func removeNamedFileIfSame(root *os.Root, name string, expected fs.FileInfo) error {
	if expected == nil {
		return errors.New("cannot remove file without expected identity")
	}
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(info, expected) {
		return fmt.Errorf("refuse to remove changed file %s: %w", name, ErrAtomicConflict)
	}
	return root.Remove(name)
}

func rollbackLinkedCreate(parent *os.Root, temporary, destination string, temporaryIdentity fs.FileInfo, cause error) (bool, error) {
	destinationInfo, destinationErr := parent.Lstat(destination)
	if errors.Is(destinationErr, fs.ErrNotExist) {
		return false, cause
	}
	if destinationErr != nil {
		return true, errors.Join(cause, fmt.Errorf("inspect linked publication during rollback: %w", destinationErr))
	}
	removable := os.SameFile(destinationInfo, temporaryIdentity)
	if temporaryInfo, err := parent.Lstat(temporary); err == nil && os.SameFile(destinationInfo, temporaryInfo) {
		removable = true
	}
	if !removable {
		return true, errors.Join(cause, errors.New("refuse to remove a concurrently replaced destination"))
	}
	if err := removeNamedFileIfSame(parent, destination, destinationInfo); err != nil {
		return true, errors.Join(cause, fmt.Errorf("roll back linked publication: %w", err))
	}
	if err := syncDirectory(parent); err != nil {
		return false, errors.Join(cause, fmt.Errorf("sync linked-publication rollback: %w", err))
	}
	return false, cause
}

func createAtomicTemporary(parent *os.Root) (string, *os.File, error) {
	for attempt := 0; attempt < 128; attempt++ {
		name, err := randomAtomicName()
		if err != nil {
			return "", nil, err
		}
		file, err := parent.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return name, file, nil
	}
	return "", nil, errors.New("allocate unique atomic temporary name")
}

func randomAtomicName() (string, error) {
	var random [16]byte
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return "", fmt.Errorf("generate temporary name: %w", err)
	}
	return ".exp-" + hex.EncodeToString(random[:]) + ".tmp", nil
}

// IsAtomicTempName reports whether base is owned by the single-file publisher.
// Callers may ignore these files on reads, but may remove them only while holding
// the project mutation/initialization lock.
func IsAtomicTempName(base string) bool {
	const prefix, suffix = ".exp-", ".tmp"
	if path.Base(base) != base || !bytes.HasPrefix([]byte(base), []byte(prefix)) || !bytes.HasSuffix([]byte(base), []byte(suffix)) {
		return false
	}
	randomPart := base[len(prefix) : len(base)-len(suffix)]
	if len(randomPart) == 32 {
		for _, character := range randomPart {
			if character < '0' || character > '9' {
				if character < 'a' || character > 'f' {
					return false
				}
			}
		}
		return true
	}
	// Go's former os.CreateTemp-based writer used the decimal rendering of a
	// uint32, so retain that exact 1-10 digit namespace for crash recovery.
	if len(randomPart) == 0 || len(randomPart) > 10 {
		return false
	}
	for _, character := range randomPart {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func publicationError(stage AtomicStage, published bool, err error) error {
	return &PublicationError{Stage: stage, Published: published, Err: err}
}

func runAtomicHook(hook AtomicHook, stage AtomicStage, destination string) error {
	if hook == nil {
		return nil
	}
	return hook(stage, destination)
}
