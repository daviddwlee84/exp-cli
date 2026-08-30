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
	Path     string
	Expected string
	Actual   string
}

func (e *ConflictError) Error() string {
	target := e.ID.String()
	if target == "" {
		target = e.Path
	}
	return fmt.Sprintf("%s expected revision %q, current revision %q: %v", target, e.Expected, e.Actual, ErrConflict)
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
	transactionHook  TransactionHook
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
	root, err := store.openCanonicalRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := store.inspectTransactionArtifactsReadOnly(ctx, root); err != nil {
		return nil, err
	}
	inventory, err := LoadInventoryRoot(ctx, root, store.Root)
	if err != nil {
		return nil, err
	}
	inventory.boundRoot = nil
	if err := store.verifyCanonicalRoot(root); err != nil {
		return nil, err
	}
	if err := store.inspectTransactionArtifactsReadOnly(ctx, root); err != nil {
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
	return store.withLockedRoots(ctx, func() error {
		if err := CleanupAtomicTempsRoot(store.canonicalRoot); err != nil {
			return fmt.Errorf("clean abandoned atomic temporaries: %w", err)
		}
		if err := store.recoverPreparedTransactionsLocked(ctx); err != nil {
			return err
		}
		inventory, err := LoadInventoryRoot(ctx, store.canonicalRoot, store.Root)
		if err != nil {
			return err
		}
		inventory.boundVerify = func() error {
			return store.verifyMutationRoots()
		}
		defer func() {
			inventory.boundRoot = nil
			inventory.boundVerify = nil
		}()
		operationErr := operation(inventory)
		verificationErr := errors.Join(inventory.VerifySnapshot(ctx), store.verifyMutationRoots(), inspectTransactionJournalsReadOnly(ctx, store.coordinationRoot, store.canonicalRoot))
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
		candidateInventory := candidateInventoryForBase(store.Root, candidateDocuments, inventory)
		if !candidateInventory.Valid() {
			return &InventoryError{Diagnostics: append([]Diagnostic(nil), candidateInventory.Diagnostics...)}
		}
		content, normalized, err := encodeCandidateForBase(candidate, inventory)
		if err != nil {
			return err
		}
		normalized.Path = current.Path

		currentBytes, identity, err := readCanonicalFileRoot(ctx, store.canonicalRoot, current.Path)
		if err != nil {
			return err
		}
		reloaded, err := decodeCanonicalDocument(currentBytes)
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
	candidateInventory := candidateInventoryForBase(store.Root, candidateDocuments, inventory)
	if !candidateInventory.Valid() {
		return nil, &InventoryError{Diagnostics: append([]Diagnostic(nil), candidateInventory.Diagnostics...)}
	}
	content, normalized, err := encodeCandidateForBase(candidate, inventory)
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
	return store.withLockedRoots(ctx, func() error {
		if err := CleanupAtomicTempsRoot(store.canonicalRoot); err != nil {
			return fmt.Errorf("clean abandoned atomic temporaries: %w", err)
		}
		if err := store.recoverPreparedTransactionsLocked(ctx); err != nil {
			return err
		}
		if err := store.seedCanonicalIDReservations(ctx, store.coordinationRoot); err != nil {
			return err
		}
		if err := store.verifyMutationRoots(); err != nil {
			return err
		}
		return operation()
	})
}

// withLockedRoots is the single integration seam for ordinary writes,
// compound transactions, and recovery. Callers must hold store.mu.
func (store *Store) withLockedRoots(ctx context.Context, operation func() error) error {
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
	immutable := false
	switch current.Kind() {
	case research.KindQueueAdvice, research.KindBattle, research.KindEvaluation,
		research.KindCandidate, research.KindPromotionSpec, research.KindPromotion,
		research.KindFinding, research.KindDecision, research.KindEvaluationSpec,
		research.KindRun:
		immutable = true
	case research.KindRelease:
		immutable = true
	case research.KindPlan:
		state := current.Record.(*research.Plan).State
		immutable = state == research.PlanCompleted || state == research.PlanDropped
	}
	if immutable && (!reflect.DeepEqual(current.Record, replacement.Record) || current.Body != replacement.Body) {
		return fmt.Errorf("%s records are immutable once published in this state", current.Kind())
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
	if currentPlan, ok := current.Record.(*research.Plan); ok {
		replacementPlan, replacementOK := replacement.Record.(*research.Plan)
		if !replacementOK {
			return errors.New("Plan replacement has the wrong record type")
		}
		if err := validatePlanUpdate(currentPlan, replacementPlan); err != nil {
			return err
		}
	}
	if currentIdea, ok := current.Record.(*research.Idea); ok {
		replacementIdea, replacementOK := replacement.Record.(*research.Idea)
		if !replacementOK {
			return errors.New("Idea replacement has the wrong record type")
		}
		if err := validateIdeaUpdate(currentIdea, replacementIdea); err != nil {
			return err
		}
	}
	if currentAttempt, ok := current.Record.(*research.Attempt); ok {
		replacementAttempt, replacementOK := replacement.Record.(*research.Attempt)
		if !replacementOK {
			return errors.New("Attempt replacement has the wrong record type")
		}
		if terminalAttemptState(currentAttempt.State) && current.Body != replacement.Body {
			return errors.New("terminal Attempt body is immutable")
		}
		if err := validateAttemptUpdate(currentAttempt, replacementAttempt); err != nil {
			return err
		}
	}
	return nil
}

func validateIdeaUpdate(current, replacement *research.Idea) error {
	if !current.ResultingPlan.IsZero() && current.ResultingPlan != replacement.ResultingPlan {
		return errors.New("Idea resulting_plan is immutable once set")
	}
	if !current.MergedInto.IsZero() && current.MergedInto != replacement.MergedInto {
		return errors.New("Idea merged_into is immutable once set")
	}
	if current.State == research.IdeaDismissed || current.State == research.IdeaMerged {
		if !reflect.DeepEqual(current, replacement) {
			return errors.New("dismissed and merged Ideas are immutable")
		}
		return nil
	}
	allowed := current.State == replacement.State
	switch current.State {
	case research.IdeaProposed:
		allowed = allowed || replacement.State == research.IdeaDeveloping || replacement.State == research.IdeaQualified || replacement.State == research.IdeaDismissed || replacement.State == research.IdeaMerged
	case research.IdeaDeveloping:
		allowed = allowed || replacement.State == research.IdeaProposed || replacement.State == research.IdeaQualified || replacement.State == research.IdeaDismissed || replacement.State == research.IdeaMerged
	case research.IdeaQualified:
		allowed = allowed || replacement.State == research.IdeaQueued || replacement.State == research.IdeaDismissed
	case research.IdeaQueued:
		allowed = allowed || replacement.State == research.IdeaQualified || replacement.State == research.IdeaDismissed
	}
	if !allowed {
		return fmt.Errorf("Idea state cannot transition from %s to %s", current.State, replacement.State)
	}
	if current.State == research.IdeaQualified || current.State == research.IdeaQueued {
		left, right := *current, *replacement
		left.State, right.State = "", ""
		left.UpdatedAt, right.UpdatedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(left, right) {
			return errors.New("qualified Idea proposal and lineage fields are immutable")
		}
	}
	return nil
}

func validatePlanUpdate(current, replacement *research.Plan) error {
	if current.ResultingExperiment.IsZero() == false && current.ResultingExperiment != replacement.ResultingExperiment {
		return errors.New("Plan resulting_experiment is immutable once set")
	}
	allowed := current.State == replacement.State ||
		current.State == research.PlanQueued && (replacement.State == research.PlanStarted || replacement.State == research.PlanDropped) ||
		current.State == research.PlanStarted && (replacement.State == research.PlanCompleted || replacement.State == research.PlanDropped)
	if !allowed {
		return fmt.Errorf("Plan state cannot transition from %s to %s", current.State, replacement.State)
	}
	if current.State == research.PlanStarted {
		left, right := *current, *replacement
		left.State, right.State = "", ""
		left.UpdatedAt, right.UpdatedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(left, right) {
			return errors.New("started Plan preregistration fields are immutable")
		}
	}
	if current.State == research.PlanQueued && replacement.State != research.PlanQueued {
		left, right := *current, *replacement
		left.State, right.State = "", ""
		left.ResultingExperiment, right.ResultingExperiment = research.ID{}, research.ID{}
		left.UpdatedAt, right.UpdatedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(left, right) {
			return errors.New("starting or dropping a Plan cannot rewrite its preregistration fields")
		}
	}
	return nil
}

func validateAttemptUpdate(current, replacement *research.Attempt) error {
	terminalRefinement := current.Terminal != nil && current.Terminal.Source == "pueue" && replacement.Terminal != nil && replacement.Terminal.Source == "direct" && terminalAttemptState(current.State) && terminalAttemptState(replacement.State)
	immutableEqual := current.Title == replacement.Title && reflect.DeepEqual(current.LegacyAliases, replacement.LegacyAliases) && reflect.DeepEqual(current.Tags, replacement.Tags) &&
		current.Run == replacement.Run && current.Runner == replacement.Runner && current.Scheduler == replacement.Scheduler && current.CWD == replacement.CWD &&
		reflect.DeepEqual(current.Argv, replacement.Argv) && reflect.DeepEqual(current.Provenance, replacement.Provenance) && current.Pool == replacement.Pool &&
		current.Queue == replacement.Queue && current.QueueRevision == replacement.QueueRevision && current.Lane == replacement.Lane && current.DispatchID == replacement.DispatchID &&
		current.BaseCommit == replacement.BaseCommit && current.HeadCommit == replacement.HeadCommit && reflect.DeepEqual(current.ChangeSet, replacement.ChangeSet)
	if !immutableEqual {
		return errors.New("Attempt registration, execution, and Git identity fields are immutable")
	}
	if terminalAttemptState(current.State) && !terminalRefinement && current.StateReason != replacement.StateReason {
		return errors.New("terminal Attempt state_reason is immutable")
	}
	if len(replacement.ExternalRefs) < len(current.ExternalRefs) {
		return errors.New("Attempt external_refs are append-only")
	}
	for index := range current.ExternalRefs {
		if !reflect.DeepEqual(current.ExternalRefs[index], replacement.ExternalRefs[index]) {
			return fmt.Errorf("Attempt external_ref %d is immutable", index)
		}
	}
	for namespace, table := range current.Extensions {
		replacementTable, found := replacement.Extensions[namespace]
		if !found {
			return fmt.Errorf("Attempt extension namespace %s is append-only", namespace)
		}
		for key, value := range table {
			if !reflect.DeepEqual(value, replacementTable[key]) {
				return fmt.Errorf("Attempt extension %s.%s is immutable once recorded", namespace, key)
			}
		}
	}
	if !allowedAttemptTransition(current.State, replacement.State) && !terminalRefinement {
		return fmt.Errorf("Attempt state cannot transition from %s to %s", current.State, replacement.State)
	}
	if current.Terminal != nil && !reflect.DeepEqual(current.Terminal, replacement.Terminal) {
		if !terminalRefinement {
			return errors.New("Attempt terminal observation is immutable once recorded")
		}
	}
	return nil
}

func allowedAttemptTransition(current, replacement research.AttemptState) bool {
	if current == replacement {
		return true
	}
	if terminalAttemptState(current) {
		return false
	}
	switch current {
	case research.AttemptPlanned:
		return replacement == research.AttemptQueued || replacement == research.AttemptBlocked || replacement == research.AttemptStarting || replacement == research.AttemptRunning || replacement == research.AttemptUnknown || terminalAttemptState(replacement)
	case research.AttemptQueued:
		return replacement == research.AttemptBlocked || replacement == research.AttemptStarting || replacement == research.AttemptRunning || replacement == research.AttemptUnknown || terminalAttemptState(replacement)
	case research.AttemptBlocked:
		return replacement == research.AttemptPlanned || replacement == research.AttemptQueued || replacement == research.AttemptUnknown || replacement == research.AttemptCancelled
	case research.AttemptStarting:
		return replacement == research.AttemptRunning || replacement == research.AttemptUnknown || terminalAttemptState(replacement)
	case research.AttemptRunning, research.AttemptUnknown:
		return replacement == research.AttemptQueued || replacement == research.AttemptBlocked || replacement == research.AttemptStarting || replacement == research.AttemptRunning || replacement == research.AttemptUnknown || terminalAttemptState(replacement)
	default:
		return false
	}
}

func terminalAttemptState(state research.AttemptState) bool {
	switch state {
	case research.AttemptSucceeded, research.AttemptFailed, research.AttemptCancelled, research.AttemptTimedOut, research.AttemptPreempted, research.AttemptOutOfMemory:
		return true
	default:
		return false
	}
}

func validateExperimentUpdate(current, replacement *research.Experiment) error {
	if current.Title != replacement.Title || !reflect.DeepEqual(current.LegacyAliases, replacement.LegacyAliases) || !reflect.DeepEqual(current.Tags, replacement.Tags) ||
		!reflect.DeepEqual(current.Parents, replacement.Parents) || !reflect.DeepEqual(current.CandidateInputs, replacement.CandidateInputs) {
		return errors.New("Experiment registration and lineage fields are immutable")
	}
	if current.Lifecycle == research.LifecycleClosed {
		if !reflect.DeepEqual(current, replacement) {
			return errors.New("closed Experiment records are immutable")
		}
		return nil
	}
	if current.Lifecycle == research.LifecycleActive && replacement.Lifecycle == research.LifecyclePlanned {
		return errors.New("Experiment lifecycle cannot move from active back to planned")
	}
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
	document, err := decodeCanonicalDocument(content)
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
