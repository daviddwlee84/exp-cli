package record

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

// CleanupAtomicTemps removes only writer-owned regular `.exp-*.tmp` files.
// Callers must hold the project mutation or initialization lock. Unknown file
// types fail closed; symlinks are never followed or removed as owned state.
func CleanupAtomicTemps(rootPath string) error {
	canonicalRoot, err := pathx.Canonical(rootPath)
	if err != nil {
		return err
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonicalRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	return CleanupAtomicTempsRoot(root)
}

// CleanupAtomicTempsRoot is CleanupAtomicTemps for a caller that already holds
// a trusted root handle.
func CleanupAtomicTempsRoot(root *os.Root) error {
	if root == nil {
		return errors.New("nil atomic cleanup root")
	}
	var temporary []string
	err := fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if relative == "." || !IsAtomicTempName(path.Base(relative)) || !isAtomicWriterParent(path.Dir(relative)) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("atomic temporary %s is not a regular non-symlink file", relative)
		}
		temporary = append(temporary, relative)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(temporary)
	parents := make(map[string]struct{})
	for _, relative := range temporary {
		if err := root.Remove(relative); err != nil {
			return fmt.Errorf("remove atomic temporary %s: %w", relative, err)
		}
		parents[path.Dir(relative)] = struct{}{}
	}
	parentNames := make([]string, 0, len(parents))
	for parent := range parents {
		parentNames = append(parentNames, parent)
	}
	sort.Strings(parentNames)
	for _, parent := range parentNames {
		directory, err := pathx.OpenRootAtNoSymlinks(root, parent)
		if err != nil {
			return fmt.Errorf("open atomic temporary parent %s: %w", parent, err)
		}
		syncErr := pathx.SyncRoot(directory)
		closeErr := directory.Close()
		if syncErr != nil || closeErr != nil {
			return fmt.Errorf("sync atomic temporary parent %s: %w", parent, errors.Join(syncErr, closeErr))
		}
	}
	return nil
}

func isAtomicWriterParent(parent string) bool {
	if parent == "." || parent == PlansDir || parent == FindingsDir || parent == DecisionsDir {
		return true
	}
	components := strings.Split(parent, "/")
	if len(components) == 1 {
		return experimentDirPattern.MatchString(components[0])
	}
	return len(components) == 2 && experimentDirPattern.MatchString(components[0]) &&
		(components[1] == "runs" || components[1] == "attempts")
}
