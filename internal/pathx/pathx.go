// Package pathx provides strict lexical and physical containment checks for
// canonical records and local coordination state.
package pathx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	ErrOutsideRoot  = errors.New("path is outside root")
	ErrRoot         = errors.New("path is the root itself")
	ErrTraversal    = errors.New("path contains parent traversal")
	ErrInvalidPath  = errors.New("invalid relative POSIX path")
	ErrSymlink      = errors.New("path contains a symlink")
	ErrNotRegular   = errors.New("path is not a regular file")
	ErrFileTooLarge = errors.New("file exceeds byte limit")
)

// ValidateRelativePOSIX requires clean repository-relative POSIX syntax.
func ValidateRelativePOSIX(value string, allowRoot bool) error {
	switch {
	case value == "":
		return fmt.Errorf("empty path: %w", ErrInvalidPath)
	case !utf8.ValidString(value) || strings.ContainsRune(value, '\x00'):
		return fmt.Errorf("path is not safe UTF-8: %w", ErrInvalidPath)
	case strings.Contains(value, "\\"):
		return fmt.Errorf("backslash separators are forbidden: %w", ErrInvalidPath)
	case strings.HasPrefix(value, "/") || driveQualified(value):
		return fmt.Errorf("absolute or drive-qualified path: %w", ErrInvalidPath)
	case strings.HasPrefix(strings.ToLower(value), "file:"):
		return fmt.Errorf("file URI is not a relative path: %w", ErrInvalidPath)
	case strings.HasPrefix(value, "~"):
		return fmt.Errorf("home shorthand is forbidden: %w", ErrInvalidPath)
	case value == ".":
		if allowRoot {
			return nil
		}
		return ErrRoot
	case strings.Contains(value, "//"):
		return fmt.Errorf("path has an empty component: %w", ErrInvalidPath)
	}
	for _, component := range strings.Split(value, "/") {
		switch component {
		case "..":
			return fmt.Errorf("%q: %w", value, ErrTraversal)
		case "", ".":
			return fmt.Errorf("%q is not clean: %w", value, ErrInvalidPath)
		}
	}
	if path.Clean(value) != value {
		return fmt.Errorf("%q is not clean: %w", value, ErrInvalidPath)
	}
	return nil
}

// Canonical resolves symlinks in an absolute path. For a missing destination it
// resolves the nearest existing ancestor and appends the missing suffix.
func Canonical(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("canonical path is empty: %w", ErrInvalidPath)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("make %q absolute: %w", value, err)
	}
	absolute = filepath.Clean(absolute)
	probe := absolute
	var missing []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(probe)
		if resolveErr == nil {
			slices.Reverse(missing)
			parts := append([]string{resolved}, missing...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if !errors.Is(resolveErr, fs.ErrNotExist) {
			return "", fmt.Errorf("resolve %q: %w", value, resolveErr)
		}
		if info, lstatErr := os.Lstat(probe); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(probe)
			if readErr != nil {
				return "", fmt.Errorf("read unresolved symlink %q: %w", probe, readErr)
			}
			if hasParentTraversal(target) {
				return "", fmt.Errorf("unresolved symlink %q target %q: %w", probe, target, ErrTraversal)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(probe), target)
			}
			resolved, targetErr := Canonical(target)
			if targetErr != nil {
				return "", fmt.Errorf("resolve target of %q: %w", probe, targetErr)
			}
			slices.Reverse(missing)
			parts := append([]string{resolved}, missing...)
			return filepath.Clean(filepath.Join(parts...)), nil
		} else if lstatErr != nil && !errors.Is(lstatErr, fs.ErrNotExist) {
			return "", fmt.Errorf("inspect unresolved path %q: %w", probe, lstatErr)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("resolve %q: no existing ancestor: %w", value, resolveErr)
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

// Contains reports whether candidate resolves to root or a descendant.
func Contains(root, candidate string) (bool, error) {
	canonicalRoot, err := Canonical(root)
	if err != nil {
		return false, err
	}
	canonicalCandidate, err := Canonical(candidate)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalCandidate)
	if err != nil {
		return false, err
	}
	return relative == "." || relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

// ResolveUnder validates relative syntax and returns its canonical physical path
// only if it remains inside root after resolving existing symlinks.
func ResolveUnder(root, relative string, allowRoot bool) (string, error) {
	if err := ValidateRelativePOSIX(relative, allowRoot); err != nil {
		return "", err
	}
	canonicalRoot, err := Canonical(root)
	if err != nil {
		return "", fmt.Errorf("canonicalize root: %w", err)
	}
	if relative == "." {
		return canonicalRoot, nil
	}
	lexical := filepath.Join(canonicalRoot, filepath.FromSlash(relative))
	canonicalCandidate, err := Canonical(lexical)
	if err != nil {
		return "", err
	}
	inside, err := Contains(canonicalRoot, canonicalCandidate)
	if err != nil {
		return "", err
	}
	if !inside || canonicalCandidate == canonicalRoot {
		return "", fmt.Errorf("%q escapes %q: %w", relative, root, ErrOutsideRoot)
	}
	return canonicalCandidate, nil
}

// ResolveUnderNoSymlinks is the write-path form: no existing component below
// root may be a symlink, even when that symlink would resolve back inside root.
func ResolveUnderNoSymlinks(root, relative string, allowRoot bool) (string, error) {
	if err := ValidateRelativePOSIX(relative, allowRoot); err != nil {
		return "", err
	}
	canonicalRoot, err := Canonical(root)
	if err != nil {
		return "", err
	}
	if relative == "." {
		return canonicalRoot, nil
	}
	current := canonicalRoot
	for _, component := range strings.Split(relative, "/") {
		current = filepath.Join(current, component)
		info, lstatErr := os.Lstat(current)
		if lstatErr != nil {
			if errors.Is(lstatErr, fs.ErrNotExist) {
				break
			}
			return "", fmt.Errorf("inspect %q: %w", current, lstatErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%q: %w", current, ErrSymlink)
		}
	}
	resolved, err := ResolveUnder(canonicalRoot, relative, allowRoot)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// RecheckNoSymlinks repeats the no-symlink traversal immediately before publish.
func RecheckNoSymlinks(root, relative string, allowRoot bool) error {
	_, err := ResolveUnderNoSymlinks(root, relative, allowRoot)
	return err
}

// OpenRootNoSymlinks resolves the supplied trust-root path once, then opens its
// canonical absolute path through a chain of directory handles. No component
// below that selected canonical root may later be followed as a symbolic link.
// Every opened handle is checked against the entry that selected it, and the
// returned Root stays attached to that directory if an ancestor is renamed.
func OpenRootNoSymlinks(value string) (*os.Root, error) {
	if value == "" {
		return nil, fmt.Errorf("root path is empty: %w", ErrInvalidPath)
	}
	absolute, err := Canonical(value)
	if err != nil {
		return nil, err
	}
	return OpenCanonicalRootNoSymlinks(absolute)
}

// OpenCanonicalRootNoSymlinks opens an already canonical absolute directory
// without resolving a symlink that may have been introduced since discovery.
func OpenCanonicalRootNoSymlinks(absolute string) (*os.Root, error) {
	if !filepath.IsAbs(absolute) || filepath.Clean(absolute) != absolute {
		return nil, fmt.Errorf("trusted root must be a clean absolute path: %w", ErrInvalidPath)
	}
	volume := filepath.VolumeName(absolute)
	volumeRoot := volume + string(filepath.Separator)
	if volume == "" {
		volumeRoot = string(filepath.Separator)
	}
	base, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, fmt.Errorf("open filesystem root %q: %w", volumeRoot, err)
	}
	relative, err := filepath.Rel(volumeRoot, absolute)
	if err != nil {
		_ = base.Close()
		return nil, fmt.Errorf("make root path relative to volume: %w", err)
	}
	if relative == "." {
		return base, nil
	}
	components := strings.FieldsFunc(relative, func(character rune) bool {
		return character <= 0xff && os.IsPathSeparator(uint8(character))
	})
	opened, err := openRootComponents(base, components, false, 0)
	_ = base.Close()
	if err != nil {
		return nil, fmt.Errorf("open root %q without symlinks: %w", absolute, err)
	}
	return opened, nil
}

// OpenRootAtNoSymlinks opens an existing relative directory beneath root while
// rejecting every symbolic-link component.
func OpenRootAtNoSymlinks(root *os.Root, relative string) (*os.Root, error) {
	if root == nil {
		return nil, errors.New("nil filesystem root")
	}
	if err := ValidateRelativePOSIX(relative, true); err != nil {
		return nil, err
	}
	components := []string(nil)
	if relative != "." {
		components = strings.Split(relative, "/")
	}
	return openRootComponents(root, components, false, 0)
}

// EnsureRootAtNoSymlinks opens or creates a relative directory beneath root.
// Newly created directories are chmodded through their open handles and their
// parent directory entries are synced before the function returns.
func EnsureRootAtNoSymlinks(root *os.Root, relative string, mode fs.FileMode) (*os.Root, bool, error) {
	if root == nil {
		return nil, false, errors.New("nil filesystem root")
	}
	if err := ValidateRelativePOSIX(relative, true); err != nil {
		return nil, false, err
	}
	components := []string(nil)
	if relative != "." {
		components = strings.Split(relative, "/")
	}
	opened, created, err := ensureRootComponents(root, components, mode)
	return opened, created, err
}

// VerifyRootPath confirms that expected is still the directory reached by the
// absolute path used to open it.
func VerifyRootPath(value string, expected *os.Root) error {
	if expected == nil {
		return errors.New("nil filesystem root")
	}
	// Root.Name records the canonical path selected when the handle was opened.
	// Reopening that path without another EvalSymlinks prevents a newly inserted
	// symlink from making an identity comparison appear to succeed.
	current, err := OpenCanonicalRootNoSymlinks(filepath.Clean(expected.Name()))
	if err != nil {
		return err
	}
	defer current.Close()
	currentInfo, err := current.Stat(".")
	if err != nil {
		return err
	}
	expectedInfo, err := expected.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(currentInfo, expectedInfo) {
		return fmt.Errorf("root path %q changed identity", value)
	}
	return nil
}

// VerifyRootAt confirms that expected is still reachable at relative beneath
// root. It detects directory replacement or retargeting after a handle was
// opened, without relying on a pathname for subsequent I/O.
func VerifyRootAt(root *os.Root, relative string, expected *os.Root) error {
	if root == nil || expected == nil {
		return errors.New("nil filesystem root")
	}
	current, err := OpenRootAtNoSymlinks(root, relative)
	if err != nil {
		return err
	}
	defer current.Close()
	currentInfo, err := current.Stat(".")
	if err != nil {
		return err
	}
	expectedInfo, err := expected.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(currentInfo, expectedInfo) {
		return fmt.Errorf("directory %q changed identity", relative)
	}
	return nil
}

// OpenRegularFileNoFollow opens one regular file relative to an already trusted
// root. It rejects a symbolic-link final component and verifies that the path,
// opened handle, and final path recheck all identify the same file. Unix builds
// additionally ask the kernel not to follow the final component while opening.
func OpenRegularFileNoFollow(root *os.Root, relative string) (*os.File, fs.FileInfo, error) {
	if root == nil {
		return nil, nil, errors.New("nil filesystem root")
	}
	if err := ValidateRelativePOSIX(relative, false); err != nil {
		return nil, nil, err
	}
	before, err := root.Lstat(relative)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%q: %w", relative, ErrSymlink)
	}
	if !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%q: %w", relative, ErrNotRegular)
	}
	file, err := openFileNoFollow(root, relative)
	if err != nil {
		return nil, nil, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("%q changed while opening: %w", relative, errors.Join(ErrNotRegular, statErr))
	}
	after, lstatErr := root.Lstat(relative)
	if lstatErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(opened, after) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("%q changed while opening: %w", relative, errors.Join(ErrNotRegular, lstatErr))
	}
	return file, opened, nil
}

// ReadBoundedRegularFile reads at most maxBytes from one identity-safe,
// non-symlink regular file. It rejects an oversized file before allocation when
// its metadata permits and always uses a maxBytes+1 sentinel read.
func ReadBoundedRegularFile(ctx context.Context, root *os.Root, relative string, maxBytes int64) ([]byte, fs.FileInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if maxBytes < 0 || maxBytes == int64(^uint64(0)>>1) {
		return nil, nil, fmt.Errorf("invalid byte limit %d", maxBytes)
	}
	file, info, err := OpenRegularFileNoFollow(root, relative)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	if info.Size() > maxBytes {
		return nil, nil, fmt.Errorf("%q is %d bytes; limit is %d: %w", relative, info.Size(), maxBytes, ErrFileTooLarge)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, nil, fmt.Errorf("%q exceeds %d bytes: %w", relative, maxBytes, ErrFileTooLarge)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	afterOpen, err := file.Stat()
	if err != nil || !os.SameFile(info, afterOpen) || afterOpen.Size() != int64(len(content)) || !afterOpen.ModTime().Equal(info.ModTime()) {
		return nil, nil, fmt.Errorf("%q changed while reading: %w", relative, errors.Join(ErrNotRegular, err))
	}
	afterPath, err := root.Lstat(relative)
	if err != nil || afterPath.Mode()&os.ModeSymlink != 0 || !afterPath.Mode().IsRegular() || !os.SameFile(info, afterPath) {
		return nil, nil, fmt.Errorf("%q changed while reading: %w", relative, errors.Join(ErrNotRegular, err))
	}
	return content, info, nil
}

func openRootComponents(base *os.Root, components []string, create bool, mode fs.FileMode) (*os.Root, error) {
	opened, _, err := walkRootComponents(base, components, create, mode)
	return opened, err
}

func ensureRootComponents(base *os.Root, components []string, mode fs.FileMode) (*os.Root, bool, error) {
	return walkRootComponents(base, components, true, mode)
}

func walkRootComponents(base *os.Root, components []string, create bool, mode fs.FileMode) (*os.Root, bool, error) {
	current, err := base.OpenRoot(".")
	if err != nil {
		return nil, false, err
	}
	createdAny := false
	for _, component := range components {
		info, statErr := current.Lstat(component)
		created := false
		if errors.Is(statErr, fs.ErrNotExist) && create {
			if mkdirErr := current.Mkdir(component, mode); mkdirErr != nil {
				_ = current.Close()
				return nil, createdAny, mkdirErr
			}
			created = true
			createdAny = true
			info, statErr = current.Lstat(component)
		}
		if statErr != nil {
			_ = current.Close()
			return nil, createdAny, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			_ = current.Close()
			return nil, createdAny, fmt.Errorf("directory component %q is a symlink: %w", component, ErrSymlink)
		}
		if !info.IsDir() {
			_ = current.Close()
			return nil, createdAny, fmt.Errorf("directory component %q is not a directory", component)
		}
		next, openErr := current.OpenRoot(component)
		if openErr != nil {
			_ = current.Close()
			return nil, createdAny, openErr
		}
		nextInfo, nextStatErr := next.Stat(".")
		if nextStatErr != nil || !os.SameFile(info, nextInfo) {
			_ = next.Close()
			_ = current.Close()
			if nextStatErr != nil {
				return nil, createdAny, nextStatErr
			}
			return nil, createdAny, fmt.Errorf("directory component %q changed identity", component)
		}
		pathInfo, pathStatErr := current.Lstat(component)
		if pathStatErr != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() || !os.SameFile(nextInfo, pathInfo) {
			_ = next.Close()
			_ = current.Close()
			if pathStatErr != nil {
				return nil, createdAny, pathStatErr
			}
			return nil, createdAny, fmt.Errorf("directory component %q changed identity or became a symlink: %w", component, ErrSymlink)
		}
		if created {
			directory, chmodErr := next.Open(".")
			if chmodErr == nil {
				chmodErr = directory.Chmod(mode)
				chmodErr = errors.Join(chmodErr, directory.Close())
			}
			if chmodErr != nil {
				_ = next.Close()
				_ = current.Close()
				return nil, createdAny, chmodErr
			}
			if syncErr := SyncRoot(next); syncErr != nil {
				_ = next.Close()
				_ = current.Close()
				return nil, createdAny, syncErr
			}
			if syncErr := SyncRoot(current); syncErr != nil {
				_ = next.Close()
				_ = current.Close()
				return nil, createdAny, syncErr
			}
		}
		_ = current.Close()
		current = next
	}
	return current, createdAny, nil
}

func driveQualified(value string) bool {
	return len(value) >= 2 && value[1] == ':' && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z'))
}

func hasParentTraversal(value string) bool {
	for _, component := range strings.FieldsFunc(value, func(character rune) bool { return character == '/' || character == '\\' }) {
		if component == ".." {
			return true
		}
	}
	return false
}
