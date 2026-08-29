package record

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const coordinationRelative = "exp/v1"

func (store *Store) rejectTransactionArtifactsReadOnly() error {
	common, err := pathx.OpenCanonicalRootNoSymlinks(store.GitCommonDir)
	if err != nil {
		return fmt.Errorf("open Git common directory: %w", err)
	}
	defer common.Close()
	coordination, err := pathx.OpenRootAtNoSymlinks(common, "exp/v1")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Git-common coordination directory: %w", err)
	}
	defer coordination.Close()
	return rejectTransactionArtifacts(coordination)
}

// CheckTransactionArtifacts refuses nonempty unknown transaction state without
// creating or removing anything beneath the supplied coordination root.
func CheckTransactionArtifacts(coordination *os.Root) error {
	return rejectTransactionArtifacts(coordination)
}

func rejectTransactionArtifacts(coordination *os.Root) error {
	transactions, err := pathx.OpenRootAtNoSymlinks(coordination, "transactions")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect transaction directory: %w", err)
	}
	defer transactions.Close()
	directory, err := transactions.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if len(entries) != 0 {
		return fmt.Errorf("%w: found %d artifact(s) in Git-common transactions", ErrUnsupportedTransaction, len(entries))
	}
	return nil
}

func ensureCoordinationState(coordination *os.Root) error {
	for _, name := range []string{"transactions", "attempts", "reservations"} {
		directory, _, err := pathx.EnsureRootAtNoSymlinks(coordination, name, 0o700)
		if err != nil {
			return fmt.Errorf("open coordination directory %s: %w", name, err)
		}
		opened, openErr := directory.Open(".")
		if openErr == nil {
			openErr = opened.Chmod(0o700)
			openErr = errors.Join(openErr, opened.Close())
		}
		closeErr := directory.Close()
		if openErr != nil || closeErr != nil {
			return errors.Join(openErr, closeErr)
		}
	}
	return nil
}

func (store *Store) openCanonicalRoot() (*os.Root, error) {
	root, err := pathx.OpenCanonicalRootNoSymlinks(store.Root)
	if err != nil {
		return nil, fmt.Errorf("open canonical experiments root: %w", err)
	}
	if err := store.verifyCanonicalRoot(root); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func (store *Store) verifyCanonicalRoot(root *os.Root) error {
	if root == nil {
		return errors.New("canonical experiments root is not open")
	}
	if err := pathx.VerifyRootPath(store.Root, root); err != nil {
		return fmt.Errorf("canonical experiments root changed: %w", err)
	}
	if store.rootIdentity != nil {
		info, err := root.Stat(".")
		if err != nil {
			return err
		}
		if !os.SameFile(store.rootIdentity, info) {
			return errors.New("canonical experiments root changed identity")
		}
	}
	return nil
}

func readCanonicalFile(rootPath, relative string) ([]byte, fs.FileInfo, error) {
	canonicalRoot, err := pathx.Canonical(rootPath)
	if err != nil {
		return nil, nil, err
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonicalRoot)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	return readCanonicalFileRoot(context.Background(), root, relative)
}

func readCanonicalFileRoot(ctx context.Context, root *os.Root, relative string) ([]byte, fs.FileInfo, error) {
	if root == nil {
		return nil, nil, errors.New("nil canonical root")
	}
	parent, err := pathx.OpenRootAtNoSymlinks(root, path.Dir(relative))
	if err != nil {
		return nil, nil, err
	}
	defer parent.Close()
	content, info, err := pathx.ReadBoundedRegularFile(ctx, parent, path.Base(relative), MaxRecordBytes)
	if err != nil {
		if errors.Is(err, pathx.ErrFileTooLarge) {
			err = errors.Join(ErrRecordTooLarge, err)
		}
		return nil, nil, fmt.Errorf("read canonical file %s: %w", relative, err)
	}
	return content, info, nil
}

func (store *Store) seedCanonicalIDReservations(ctx context.Context, coordination *os.Root) error {
	repository, err := gitx.Discover(ctx, store.Root)
	if err != nil {
		return fmt.Errorf("discover repository for canonical ID reservations: %w", err)
	}
	if repository.GitCommonDir != store.GitCommonDir {
		return fmt.Errorf("Store Git common directory changed from %q to %q", store.GitCommonDir, repository.GitCommonDir)
	}
	currentInventory, err := LoadInventoryRoot(ctx, store.canonicalRoot, store.Root)
	if err != nil {
		return err
	}
	if !currentInventory.Valid() {
		return fmt.Errorf("current worktree inventory is invalid: %w", currentInventory.Error())
	}
	worktrees, err := gitx.Worktrees(ctx, repository.Root, gitx.ExecRunner{})
	if err != nil {
		return fmt.Errorf("enumerate worktrees for canonical ID reservations: %w", err)
	}
	store.missingWorktrees = append(store.missingWorktrees[:0], worktrees...)
	if err := gitx.VerifyMissingWorktrees(worktrees); err != nil {
		return err
	}
	ids := make(map[research.ID]struct{})
	for _, worktree := range worktrees {
		if worktree.Missing {
			continue
		}
		worktreeRoot, openErr := pathx.OpenCanonicalRootNoSymlinks(worktree.Root)
		if openErr != nil {
			return fmt.Errorf("open linked worktree %s: %w", worktree.Root, openErr)
		}
		experimentsRoot, openErr := pathx.OpenRootAtNoSymlinks(worktreeRoot, "experiments")
		if errors.Is(openErr, fs.ErrNotExist) {
			verifyErr := pathx.VerifyRootPath(worktree.Root, worktreeRoot)
			closeErr := worktreeRoot.Close()
			if verifyErr != nil || closeErr != nil {
				return fmt.Errorf("verify linked worktree %s: %w", worktree.Root, errors.Join(verifyErr, closeErr))
			}
			continue
		}
		if openErr != nil {
			_ = worktreeRoot.Close()
			return fmt.Errorf("open linked-worktree experiments root: %w", openErr)
		}
		if _, statErr := experimentsRoot.Lstat(ProjectFile); errors.Is(statErr, fs.ErrNotExist) {
			verifyErr := errors.Join(pathx.VerifyRootAt(worktreeRoot, "experiments", experimentsRoot), pathx.VerifyRootPath(worktree.Root, worktreeRoot))
			closeErr := errors.Join(experimentsRoot.Close(), worktreeRoot.Close())
			if verifyErr != nil || closeErr != nil {
				return fmt.Errorf("verify uninitialized linked worktree %s: %w", worktree.Root, errors.Join(verifyErr, closeErr))
			}
			continue
		} else if statErr != nil {
			_ = experimentsRoot.Close()
			_ = worktreeRoot.Close()
			return fmt.Errorf("inspect linked-worktree Project marker %s: %w", filepath.Join(worktree.Root, "experiments", ProjectFile), statErr)
		}
		rootPath := filepath.Join(worktree.Root, "experiments")
		inventory, loadErr := LoadInventoryRoot(ctx, experimentsRoot, rootPath)
		verifyErr := errors.Join(pathx.VerifyRootAt(worktreeRoot, "experiments", experimentsRoot), pathx.VerifyRootPath(worktree.Root, worktreeRoot))
		closeErr := errors.Join(experimentsRoot.Close(), worktreeRoot.Close())
		if loadErr != nil || verifyErr != nil || closeErr != nil {
			return fmt.Errorf("load linked-worktree inventory %s: %w", rootPath, errors.Join(loadErr, verifyErr, closeErr))
		}
		if !inventory.Valid() {
			return fmt.Errorf("linked-worktree inventory %s is invalid: %w", rootPath, inventory.Error())
		}
		if !sameProjectRecordIdentity(currentInventory.Project, inventory.Project) {
			return fmt.Errorf("%w: current %s, linked %s at %s", ErrProjectIdentityConflict, recordProjectIdentity(currentInventory.Project), recordProjectIdentity(inventory.Project), worktree.Root)
		}
		for _, document := range inventory.Documents {
			if id, ok := document.ID(); ok {
				ids[id] = struct{}{}
			}
		}
	}
	ordered := make([]research.ID, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].String() < ordered[right].String() })
	for _, id := range ordered {
		if err := ensureCanonicalIDReservation(coordination, id); err != nil {
			return err
		}
	}
	return gitx.VerifyMissingWorktrees(worktrees)
}

func sameProjectRecordIdentity(left, right *Document) bool {
	leftProject, leftOK := projectRecord(left)
	rightProject, rightOK := projectRecord(right)
	return leftOK && rightOK && leftProject.ProjectID == rightProject.ProjectID && leftProject.CreatedAt.Equal(rightProject.CreatedAt)
}

func recordProjectIdentity(document *Document) string {
	project, ok := projectRecord(document)
	if !ok {
		return "<invalid>"
	}
	return project.ProjectID.String() + "@" + project.CreatedAt.UTC().Format(time.RFC3339Nano)
}

func projectRecord(document *Document) (*research.Project, bool) {
	if document == nil {
		return nil, false
	}
	project, ok := document.Record.(*research.Project)
	return project, ok
}

func ensureCanonicalIDReservation(coordination *os.Root, id research.ID) error {
	reservations, err := pathx.OpenRootAtNoSymlinks(coordination, "reservations")
	if err != nil {
		return err
	}
	defer reservations.Close()
	name := id.String()
	info, err := reservations.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return reserveCanonicalID(coordination, id)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("canonical ID reservation %s is not a regular non-symlink file", id)
	}
	file, err := reservations.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return fmt.Errorf("inspect canonical ID reservation %s: %w", id, statErr)
	}
	if !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return fmt.Errorf("canonical ID reservation %s changed while opening", id)
	}
	if err := validatePrivateCoordinationFile(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("canonical ID reservation %s is unsafe: %w", id, err)
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if !bytes.Equal(content, []byte(id.String()+"\n")) {
		return fmt.Errorf("canonical ID reservation %s has invalid content", id)
	}
	return nil
}

func reserveCanonicalID(coordination *os.Root, id research.ID) error {
	if id.IsZero() {
		return errors.New("cannot reserve a zero canonical ID")
	}
	reservations, err := pathx.OpenRootAtNoSymlinks(coordination, "reservations")
	if err != nil {
		return err
	}
	defer reservations.Close()
	name := id.String()
	if err := pathx.ValidateRelativePOSIX(name, false); err != nil {
		return fmt.Errorf("unsafe reservation name: %w", err)
	}
	data := []byte(id.String() + "\n")
	err = AtomicWriteRoot(reservations, name, data, AtomicWriteOptions{
		Mode: 0o600,
		Hook: func(AtomicStage, string) error {
			if err := pathx.VerifyRootPath(reservations.Name(), reservations); err != nil {
				return fmt.Errorf("canonical ID reservation root changed: %w", err)
			}
			return nil
		},
	})
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("canonical ID %s is already reserved: %w", id, ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("reserve canonical ID %s: %w", id, err)
	}
	return nil
}
