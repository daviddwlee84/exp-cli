// Package localstate provides rooted, private, atomic JSON-state file I/O.
// Read paths are non-mutating; update paths serialize through lockx and reuse
// record's recoverable derived-file publisher.
package localstate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/lockx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
)

// Snapshot binds exact bytes to the regular-file identity observed while they
// were read. It is suitable for record.AtomicWriteDerivedRoot replacement.
type Snapshot struct {
	Data []byte
	Info fs.FileInfo
}

// StateHome resolves XDG_STATE_HOME, ignoring a relative value as required by
// the XDG base-directory specification. The fallback is ~/.local/state.
func StateHome() (string, error) {
	return xdgHome("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// ConfigHome resolves XDG_CONFIG_HOME, ignoring a relative value as required by
// the XDG base-directory specification. The fallback is ~/.config.
func ConfigHome() (string, error) {
	return xdgHome("XDG_CONFIG_HOME", ".config")
}

// CacheHome resolves XDG_CACHE_HOME, ignoring a relative value as required by
// the XDG base-directory specification. The fallback is ~/.cache. Disposable,
// host-private source seed bundles use this root by default.
func CacheHome() (string, error) {
	return xdgHome("XDG_CACHE_HOME", ".cache")
}

func xdgHome(environment, fallback string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(environment)); value != "" && filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for %s: %w", environment, err)
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("user home for %s is not absolute", environment)
	}
	return filepath.Clean(filepath.Join(home, fallback)), nil
}

// Read reads one optional private state file without creating its parent or a
// lock file. A missing parent or destination returns (nil, nil).
func Read(ctx context.Context, filename string, maxBytes int64) (*Snapshot, error) {
	parent, base, err := split(filename)
	if err != nil {
		return nil, err
	}
	canonicalParent, err := pathx.Canonical(parent)
	if err != nil {
		return nil, fmt.Errorf("canonicalize private state directory: %w", err)
	}
	parentInfo, err := os.Lstat(canonicalParent)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect private state directory: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return nil, errors.New("private state parent is not a real directory")
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonicalParent)
	if err != nil {
		return nil, fmt.Errorf("open private state directory: %w", err)
	}
	defer root.Close()
	if _, err := pathx.CheckPrivateRoot(root, 0o700, "private state directory"); err != nil {
		return nil, err
	}
	return ReadRoot(ctx, root, base, maxBytes)
}

// ReadRoot reads one optional private file relative to an already trusted root.
func ReadRoot(ctx context.Context, root *os.Root, name string, maxBytes int64) (*Snapshot, error) {
	if root == nil {
		return nil, errors.New("private state root is nil")
	}
	if err := pathx.ValidateRelativePOSIX(name, false); err != nil || filepath.Base(name) != name {
		return nil, fmt.Errorf("invalid private state filename %q: %w", name, err)
	}
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect private state file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("private state file is not a regular non-symlink file")
	}
	checkedInfo, err := pathx.CheckPrivateFile(root, name, 0o600, "private state file")
	if err != nil {
		return nil, fmt.Errorf("validate private state file: %w", err)
	}
	if !os.SameFile(info, checkedInfo) {
		return nil, fmt.Errorf("private state file changed during validation: %w", record.ErrAtomicConflict)
	}
	content, openedInfo, err := pathx.ReadBoundedRegularFile(ctx, root, name, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("read private state file: %w", err)
	}
	finalInfo, finalErr := pathx.CheckPrivateFile(root, name, 0o600, "private state file")
	if finalErr != nil || !os.SameFile(info, openedInfo) || !os.SameFile(openedInfo, finalInfo) {
		return nil, fmt.Errorf("private state file changed while opening: %w", errors.Join(record.ErrAtomicConflict, finalErr))
	}
	return &Snapshot{Data: append([]byte(nil), content...), Info: openedInfo}, nil
}

// WithLockedFile creates and protects the destination directory, serializes the
// operation through its rooted lock, and removes only abandoned record-writer
// temporaries before invoking operation.
func WithLockedFile(ctx context.Context, filename string, operation func(*os.Root, string) error) error {
	if operation == nil {
		return errors.New("private state operation is nil")
	}
	parent, base, err := split(filename)
	if err != nil {
		return err
	}
	canonicalParent, err := pathx.Canonical(parent)
	if err != nil {
		return fmt.Errorf("canonicalize private state directory: %w", err)
	}
	ancestor, relative, err := existingAncestor(canonicalParent)
	if err != nil {
		return err
	}
	return lockx.WithTrustedRoot(ctx, ancestor, relative, func(root *os.Root) error {
		if _, err := pathx.CheckPrivateRoot(root, 0o700, "private state directory"); err != nil {
			return err
		}
		if err := record.CleanupAtomicTempsRoot(root); err != nil {
			return fmt.Errorf("recover private state temporaries: %w", err)
		}
		return operation(root, base)
	})
}

// WriteRoot atomically creates or replaces a private file. Callers must hold the
// corresponding WithLockedFile lock and pass the snapshot read under that lock.
func WriteRoot(root *os.Root, name string, data []byte, previous *Snapshot, hook record.AtomicHook) error {
	options := record.AtomicWriteOptions{Mode: 0o600, Hook: hook}
	if previous != nil {
		options.Expected = previous.Info
		options.ExpectedContent = append([]byte(nil), previous.Data...)
	}
	if err := record.AtomicWriteDerivedRoot(root, name, data, options); err != nil {
		return fmt.Errorf("publish private state file: %w", err)
	}
	return nil
}

// CanonicalPath returns the path actually used after canonicalizing only the
// parent trust root. The final component is intentionally never followed.
func CanonicalPath(filename string) (string, error) {
	parent, base, err := split(filename)
	if err != nil {
		return "", err
	}
	canonicalParent, err := pathx.Canonical(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(canonicalParent, base), nil
}

func split(filename string) (string, string, error) {
	if filename == "" || !filepath.IsAbs(filename) || filepath.Clean(filename) != filename {
		return "", "", errors.New("private state path must be a clean absolute path")
	}
	base := filepath.Base(filename)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", "", errors.New("private state path must name a file")
	}
	if err := pathx.ValidateRelativePOSIX(filepath.ToSlash(base), false); err != nil {
		return "", "", fmt.Errorf("invalid private state filename: %w", err)
	}
	return filepath.Dir(filename), base, nil
}

func existingAncestor(target string) (string, string, error) {
	ancestor := target
	for {
		info, err := os.Lstat(ancestor)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", "", errors.New("private state ancestor is not a real directory")
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", fmt.Errorf("inspect private state ancestor: %w", err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", "", errors.New("private state path has no existing directory ancestor")
		}
		ancestor = parent
	}
	relative, err := filepath.Rel(ancestor, target)
	if err != nil {
		return "", "", err
	}
	relative = filepath.ToSlash(relative)
	if relative == "" {
		relative = "."
	}
	if err := pathx.ValidateRelativePOSIX(relative, true); err != nil {
		return "", "", fmt.Errorf("private state directory is outside its ancestor: %w", err)
	}
	return ancestor, relative, nil
}
