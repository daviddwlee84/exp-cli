package record

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/lockx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

var (
	ErrConflict                = errors.New("record revision conflict")
	ErrAlreadyExists           = errors.New("record already exists")
	ErrCollision               = errors.New("unable to allocate a unique record ID")
	ErrInvalidInventory        = errors.New("canonical inventory is invalid")
	ErrUnsupportedTransaction  = errors.New("transaction journal artifacts block canonical access")
	ErrProjectIdentityConflict = errors.New("linked worktree has a conflicting Project identity")
	ErrInvalidBody             = errors.New("invalid Markdown body")
)

// ConflictError carries both optimistic revisions without exposing file content.
type ConflictError struct {
	ID       research.ID
	Expected string
	Actual   string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s expected revision %q, current revision %q: %v", e.ID, e.Expected, e.Actual, ErrConflict)
}
func (e *ConflictError) Unwrap() error { return ErrConflict }

// InventoryError blocks mutation while preserving every file diagnostic.
type InventoryError struct{ Diagnostics []Diagnostic }

func (e *InventoryError) Error() string {
	return fmt.Sprintf("%v (%d diagnostic(s))", ErrInvalidInventory, len(e.Diagnostics))
}
func (e *InventoryError) Unwrap() error { return ErrInvalidInventory }

// StoreOption injects deterministic creation and failure boundaries.
type StoreOption func(*Store)

func WithClock(clock func() time.Time) StoreOption {
	return func(store *Store) { store.clock = clock }
}

func WithUUIDGenerator(generator research.UUIDGenerator) StoreOption {
	return func(store *Store) { store.generate = generator }
}

func WithAtomicHook(hook AtomicHook) StoreOption {
	return func(store *Store) { store.atomicHook = hook }
}

func WithCollisionLimit(limit int) StoreOption {
	return func(store *Store) { store.collisionLimit = limit }
}

// Store owns one experiments root and coordinates writes through GitCommonDir.
type Store struct {
	Root         string
	GitCommonDir string

	clock            func() time.Time
	generate         research.UUIDGenerator
	atomicHook       AtomicHook
	collisionLimit   int
	coordinationRoot *os.Root
	canonicalRoot    *os.Root
	rootIdentity     fs.FileInfo
	missingWorktrees []gitx.Worktree
	mu               sync.Mutex
}

func NewStore(root, gitCommonDir string, options ...StoreOption) *Store {
	if canonical, err := pathx.Canonical(root); err == nil {
		root = canonical
	}
	if canonical, err := pathx.Canonical(gitCommonDir); err == nil {
		gitCommonDir = canonical
	}
	store := &Store{
		Root:           root,
		GitCommonDir:   gitCommonDir,
		clock:          time.Now,
		generate:       research.DefaultUUIDGenerator,
		collisionLimit: 128,
	}
	if info, err := os.Lstat(root); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		store.rootIdentity = info
	}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	if store.clock == nil {
		store.clock = time.Now
	}
	if store.generate == nil {
		store.generate = research.DefaultUUIDGenerator
	}
	if store.collisionLimit <= 0 {
		store.collisionLimit = 128
	}
	return store
}

func (store *Store) CoordinationDir() string {
	return filepath.Join(store.GitCommonDir, "exp", "v1")
}

// Inventory performs a read-only scan and never creates coordination state.
func (store *Store) Inventory(ctx context.Context) (*Inventory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.rejectTransactionArtifactsReadOnly(); err != nil {
		return nil, err
	}
	root, err := store.openCanonicalRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	inventory, err := LoadInventoryRoot(ctx, root, store.Root)
	if err != nil {
		return nil, err
	}
	inventory.boundRoot = nil
	if err := store.verifyCanonicalRoot(root); err != nil {
		return nil, err
	}
	if err := store.rejectTransactionArtifactsReadOnly(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return inventory, nil
}

// WithInventorySnapshot holds the project-wide mutation lock and one opened
// canonical root while operation inspects or refreshes derived views. The same
// canonical snapshot is verified before the lock is released.
func (store *Store) WithInventorySnapshot(ctx context.Context, operation func(*Inventory) error) error {
	if operation == nil {
		return errors.New("inventory snapshot operation is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return lockx.WithTrustedRoot(ctx, store.GitCommonDir, coordinationRelative, func(coordination *os.Root) error {
		if err := rejectTransactionArtifacts(coordination); err != nil {
			return err
		}
		root, err := store.openCanonicalRoot()
		if err != nil {
			return err
		}
		defer root.Close()
		inventory, err := LoadInventoryRoot(ctx, root, store.Root)
		if err != nil {
			return err
		}
		inventory.boundVerify = func() error {
			return errors.Join(store.verifyCanonicalRoot(root), pathx.VerifyRootPath(store.CoordinationDir(), coordination))
		}
		defer func() {
			inventory.boundRoot = nil
			inventory.boundVerify = nil
		}()
		operationErr := operation(inventory)
		verificationErr := errors.Join(inventory.VerifySnapshot(ctx), store.verifyCanonicalRoot(root), rejectTransactionArtifacts(coordination))
		if verificationErr != nil {
			verificationErr = fmt.Errorf("canonical inventory changed during snapshot operation: %w", verificationErr)
		}
		return errors.Join(operationErr, verificationErr)
	})
}

// Create publishes an explicitly identified non-Project record.
func (store *Store) Create(ctx context.Context, input *Document) (*Document, error) {
	if input == nil || input.Record == nil || input.Kind() == research.KindProject {
		return nil, fmt.Errorf("create requires one identified non-Project document")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var result *Document
	err := store.withMutationLock(ctx, func() error {
		inventory, err := store.validInventoryLocked(ctx)
		if err != nil {
			return err
		}
		id, ok := input.ID()
		if !ok {
			return fmt.Errorf("create %s: typed ID is required", input.Kind())
		}
		if documents := inventory.byID[id]; len(documents) != 0 {
			return fmt.Errorf("%s: %w", id, ErrAlreadyExists)
		}
		candidate := input.Clone()
		candidate.Path, err = PathForNew(candidate.Record, inventory)
		if err != nil {
			return err
		}
		result, err = store.createLocked(inventory, candidate)
		return err
	})
	return result, err
}

// Update replaces one record at its existing path after an exact normalized
// expected-revision check. Identity, kind, schema, and created_at are immutable.
func (store *Store) Update(ctx context.Context, replacement *Document, expectedRevision string) (*Document, error) {
	if replacement == nil || replacement.Record == nil || replacement.Kind() == research.KindProject {
		return nil, fmt.Errorf("update requires one non-Project document")
	}
	if !ValidRevision(expectedRevision) {
		return nil, fmt.Errorf("expected revision is required and must be canonical: %w", ErrConflict)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var result *Document
	err := store.withMutationLock(ctx, func() error {
		inventory, err := store.validInventoryLocked(ctx)
		if err != nil {
			return err
		}
		id, ok := replacement.ID()
		if !ok {
			return errors.New("replacement has no typed ID")
		}
		current, err := inventory.ByID(id)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return &ConflictError{ID: id, Expected: expectedRevision, Actual: current.Revision}
		}
		if err := validateImmutableUpdate(current, replacement); err != nil {
			return err
		}
		candidate := replacement.Clone()
		candidate.Path = current.Path
		candidateDocuments := replaceDocument(inventory.Documents, id, candidate)
		candidateInventory := InventoryFromDocuments(store.Root, candidateDocuments)
		if !candidateInventory.Valid() {
			return &InventoryError{Diagnostics: append([]Diagnostic(nil), candidateInventory.Diagnostics...)}
		}
		content, err := Encode(candidate)
		if err != nil {
			return err
		}
		if err := ValidateRecordSize(content); err != nil {
			return err
		}
		normalized, err := Decode(content)
		if err != nil {
			return err
		}
		normalized.Path = current.Path

		currentBytes, identity, err := readCanonicalFileRoot(ctx, store.canonicalRoot, current.Path)
		if err != nil {
			return err
		}
		reloaded, err := Decode(currentBytes)
		if err != nil {
			return fmt.Errorf("re-read current record: %w", err)
		}
		if reloaded.Revision != expectedRevision {
			return &ConflictError{ID: id, Expected: expectedRevision, Actual: reloaded.Revision}
		}
		writeErr := AtomicWriteRoot(store.canonicalRoot, current.Path, content, AtomicWriteOptions{
			Expected:        identity,
			ExpectedContent: currentBytes,
			Hook:            store.lockBoundAtomicHook,
			Verify:          store.verifyMutationRoots,
		})
		if writeErr != nil {
			if publicationSucceeded(writeErr) {
				result = normalized
				return writeErr
			}
			if errors.Is(writeErr, ErrAtomicConflict) {
				actual := observedRevision(store.Root, current.Path)
				return errors.Join(&ConflictError{ID: id, Expected: expectedRevision, Actual: actual}, writeErr)
			}
			return writeErr
		}
		result = normalized
		return nil
	})
	return result, err
}

func (store *Store) createLocked(inventory *Inventory, candidate *Document) (*Document, error) {
	candidateDocuments := append(cloneDocuments(inventory.Documents), candidate)
	candidateInventory := InventoryFromDocuments(store.Root, candidateDocuments)
	if !candidateInventory.Valid() {
		return nil, &InventoryError{Diagnostics: append([]Diagnostic(nil), candidateInventory.Diagnostics...)}
	}
	content, err := Encode(candidate)
	if err != nil {
		return nil, err
	}
	if err := ValidateRecordSize(content); err != nil {
		return nil, err
	}
	normalized, err := Decode(content)
	if err != nil {
		return nil, err
	}
	normalized.Path = candidate.Path
	id, ok := normalized.ID()
	if !ok {
		return nil, errors.New("created record has no canonical ID")
	}
	if err := reserveCanonicalID(store.coordinationRoot, id); err != nil {
		return nil, err
	}
	writeErr := AtomicWriteRoot(store.canonicalRoot, candidate.Path, content, AtomicWriteOptions{
		Hook:   store.lockBoundAtomicHook,
		Verify: store.verifyMutationRoots,
	})
	if writeErr != nil {
		if publicationSucceeded(writeErr) {
			return normalized, writeErr
		}
		if errors.Is(writeErr, fs.ErrExist) {
			return nil, fmt.Errorf("%s: %w", id, ErrAlreadyExists)
		}
		return nil, writeErr
	}
	return normalized, nil
}

func (store *Store) withMutationLock(ctx context.Context, operation func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if store.Root == "" || store.GitCommonDir == "" {
		return errors.New("store requires experiments root and Git common directory")
	}
	return lockx.WithTrustedRoot(ctx, store.GitCommonDir, coordinationRelative, func(coordination *os.Root) error {
		if err := ensureCoordinationState(coordination); err != nil {
			return err
		}
		if err := rejectTransactionArtifacts(coordination); err != nil {
			return err
		}
		canonicalRoot, err := store.openCanonicalRoot()
		if err != nil {
			return fmt.Errorf("open canonical experiments root: %w", err)
		}
		defer canonicalRoot.Close()
		store.coordinationRoot = coordination
		store.canonicalRoot = canonicalRoot
		defer func() {
			store.coordinationRoot = nil
			store.canonicalRoot = nil
			store.missingWorktrees = nil
		}()
		if err := CleanupAtomicTempsRoot(canonicalRoot); err != nil {
			return fmt.Errorf("clean abandoned atomic temporaries: %w", err)
		}
		if err := store.seedCanonicalIDReservations(ctx, coordination); err != nil {
			return err
		}
		if err := store.verifyMutationRoots(); err != nil {
			return err
		}
		return operation()
	})
}

func (store *Store) lockBoundAtomicHook(stage AtomicStage, destination string) error {
	if store.atomicHook != nil {
		if err := store.atomicHook(stage, destination); err != nil {
			return err
		}
	}
	return store.verifyMutationRoots()
}

func (store *Store) verifyMutationRoots() error {
	if store.coordinationRoot == nil || store.canonicalRoot == nil {
		return errors.New("canonical publication requires opened project and lock roots")
	}
	if err := pathx.VerifyRootPath(store.CoordinationDir(), store.coordinationRoot); err != nil {
		return fmt.Errorf("project lock root changed before canonical publication: %w", err)
	}
	if err := store.verifyCanonicalRoot(store.canonicalRoot); err != nil {
		return fmt.Errorf("canonical experiments root changed before publication: %w", err)
	}
	if err := gitx.VerifyMissingWorktrees(store.missingWorktrees); err != nil {
		return fmt.Errorf("linked worktree set changed before publication: %w", err)
	}
	return nil
}

func (store *Store) validInventoryLocked(ctx context.Context) (*Inventory, error) {
	if err := store.verifyMutationRoots(); err != nil {
		return nil, err
	}
	inventory, err := LoadInventoryRoot(ctx, store.canonicalRoot, store.Root)
	if err != nil {
		return nil, err
	}
	if err := store.verifyMutationRoots(); err != nil {
		return nil, err
	}
	if !inventory.Valid() {
		return nil, &InventoryError{Diagnostics: append([]Diagnostic(nil), inventory.Diagnostics...)}
	}
	return inventory, nil
}

func validateImmutableUpdate(current, replacement *Document) error {
	currentID, currentOK := current.ID()
	replacementID, replacementOK := replacement.ID()
	if !currentOK || !replacementOK || currentID != replacementID {
		return fmt.Errorf("record ID is immutable")
	}
	if current.Kind() != replacement.Kind() || current.Record.GetSchema() != replacement.Record.GetSchema() {
		return fmt.Errorf("record kind and schema are immutable")
	}
	currentCommon, replacementCommon := current.Record.GetCommon(), replacement.Record.GetCommon()
	if currentCommon == nil || replacementCommon == nil || !currentCommon.CreatedAt.Equal(replacementCommon.CreatedAt) {
		return fmt.Errorf("created_at is immutable")
	}
	if replacementCommon.UpdatedAt.Before(currentCommon.UpdatedAt) {
		return fmt.Errorf("updated_at cannot move backwards")
	}
	if currentExperiment, ok := current.Record.(*research.Experiment); ok {
		replacementExperiment, replacementOK := replacement.Record.(*research.Experiment)
		if !replacementOK {
			return errors.New("Experiment replacement has the wrong record type")
		}
		if err := validateExperimentUpdate(currentExperiment, replacementExperiment); err != nil {
			return err
		}
	}
	return nil
}

func validateExperimentUpdate(current, replacement *research.Experiment) error {
	if len(replacement.Amendments) < len(current.Amendments) {
		return errors.New("Experiment amendments are append-only")
	}
	for index := range current.Amendments {
		if !reflect.DeepEqual(current.Amendments[index], replacement.Amendments[index]) {
			return fmt.Errorf("Experiment amendment %d is immutable", index)
		}
	}
	if current.Design.DesignLockedAt == nil {
		return nil
	}
	if replacement.Design.DesignLockedAt == nil || !replacement.Design.DesignLockedAt.Equal(*current.Design.DesignLockedAt) {
		return errors.New("Experiment design_locked_at is immutable once set")
	}
	currentDigest, err := research.DesignDigest(current.Design)
	if err != nil {
		return fmt.Errorf("compute current Experiment design digest: %w", err)
	}
	replacementDigest, err := research.DesignDigest(replacement.Design)
	if err != nil {
		return fmt.Errorf("compute replacement Experiment design digest: %w", err)
	}
	appended := replacement.Amendments[len(current.Amendments):]
	if currentDigest != replacementDigest && len(appended) == 0 {
		return errors.New("a locked Experiment design change requires an appended amendment")
	}
	previous := current.Design.DesignDigest
	for index, amendment := range appended {
		if amendment.PreviousDigest != previous {
			return fmt.Errorf("appended Experiment amendment %d does not continue current digest %s", len(current.Amendments)+index, previous)
		}
		previous = amendment.NewDigest
	}
	if len(appended) > 0 && previous != replacement.Design.DesignDigest {
		return errors.New("appended Experiment amendments do not end at the replacement design digest")
	}
	return nil
}

func observedRevision(root, relative string) string {
	content, _, err := readCanonicalFile(root, relative)
	if err != nil {
		return "changed"
	}
	document, err := Decode(content)
	if err != nil || document.Revision == "" {
		return "changed"
	}
	return document.Revision
}

func replaceDocument(documents []*Document, id research.ID, replacement *Document) []*Document {
	out := make([]*Document, 0, len(documents))
	for _, document := range documents {
		documentID, ok := document.ID()
		if ok && documentID == id {
			out = append(out, replacement)
		} else {
			out = append(out, document)
		}
	}
	return out
}

func cloneDocuments(documents []*Document) []*Document {
	out := make([]*Document, len(documents))
	for index, document := range documents {
		out[index] = document.Clone()
	}
	return out
}

func publicationSucceeded(err error) bool {
	var publication *PublicationError
	return errors.As(err, &publication) && publication.Published
}
