package record

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

// TransactionOperation is one canonical mutation recorded in a prepared
// transaction journal.
type TransactionOperation string

const (
	TransactionCreate  TransactionOperation = "create"
	TransactionReplace TransactionOperation = "replace"
	TransactionDelete  TransactionOperation = "delete"
)

// TransactionStage names a durable transaction failure-injection boundary.
// Hooks run before the named operation and are intended only for tests.
type TransactionStage string

const (
	StageTransactionDirectoryCreate TransactionStage = "transaction_directory_create"
	StageTransactionTempCreate      TransactionStage = "staged_temp_create"
	StageTransactionTempWrite       TransactionStage = "staged_temp_write"
	StageTransactionFileSync        TransactionStage = "staged_file_sync"
	StageTransactionStagedDirSync   TransactionStage = "staged_directory_sync"
	StageTransactionJournalPublish  TransactionStage = "journal_publish"
	StageTransactionJournalDirSync  TransactionStage = "journal_directory_sync"
	StageTransactionCanonicalCreate TransactionStage = "canonical_create"
	StageTransactionCanonicalCAS    TransactionStage = "canonical_compare_and_swap"
	StageTransactionCanonicalDelete TransactionStage = "canonical_unlink"
	StageTransactionCanonicalSync   TransactionStage = "canonical_directory_sync"
	StageTransactionCommitMark      TransactionStage = "commit_mark"
)

// TransactionHook receives the transaction ID and the canonical path (or local
// journal path for preparation stages) associated with a boundary.
type TransactionHook func(stage TransactionStage, transactionID, relative string) error

// WithTransactionHook installs deterministic transaction failure injection.
func WithTransactionHook(hook TransactionHook) StoreOption {
	return func(store *Store) { store.transactionHook = hook }
}

// TransactionChange describes one member of a compound canonical mutation.
//
// Create and replace require Document. Delete requires ID for ordinary records
// or Path="POLICY.md" for the ID-less Policy singleton. Replace and delete
// require ExpectedRevision. Project records cannot participate because their
// identity scopes the journal itself.
type TransactionChange struct {
	Operation        TransactionOperation
	Document         *Document
	ID               research.ID
	Path             string
	ExpectedRevision string
}

// TransactionRequest is the service-layer input for one prepared transaction.
// Operation is a stable, non-secret machine label such as "plan.start".
type TransactionRequest struct {
	Operation string
	Changes   []TransactionChange
	// AllowStale is reserved for evidence publication that legitimately makes
	// queued work stale. The final inventory may contain only the explicit
	// repairable stale diagnostics; every other invariant still fails closed.
	AllowStale bool
}

// TransactionResult identifies a durably prepared transaction and returns the
// normalized create/replace documents. Deletes have no result document.
type TransactionResult struct {
	TransactionID string
	Documents     []*Document
}

var (
	ErrInvalidTransaction          = errors.New("invalid canonical transaction")
	ErrTransactionRecoveryRequired = errors.New("prepared canonical transaction requires recovery")
	ErrTransactionConflict         = errors.New("canonical transaction recovery conflict")
)

// TransactionConflictError means a destination is neither the exact old state
// nor the exact new state recorded by a prepared journal. Recovery never
// overwrites that unrelated edit.
type TransactionConflictError struct {
	TransactionID string
	Path          string
	OldHash       string
	NewHash       string
	ObservedHash  string
}

func (e *TransactionConflictError) Error() string {
	return fmt.Sprintf("transaction %s path %s has %s; expected old %s or new %s: %v", e.TransactionID, e.Path, e.ObservedHash, e.OldHash, e.NewHash, ErrTransactionConflict)
}

func (e *TransactionConflictError) Unwrap() error { return ErrTransactionConflict }

type preparedTransactionEntry struct {
	journal  transactionJournalEntry
	data     []byte
	document *Document
}

type preparedTransaction struct {
	journal      transactionJournal
	journalBytes []byte
	entries      []preparedTransactionEntry
	documents    []*Document
}

var transactionOperationPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

// Transact validates a complete candidate inventory, durably prepares exact
// publication bytes, and then rolls the transaction forward under the shared
// Git-common mutation lock. Projections are deliberately outside this API.
func (store *Store) Transact(ctx context.Context, request TransactionRequest) (*TransactionResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	var result *TransactionResult
	err := store.withMutationLock(ctx, func() error {
		inventory, err := store.transactionInventoryLocked(ctx)
		if err != nil {
			return err
		}
		prepared, durable, err := store.prepareTransactionLocked(ctx, inventory, request)
		if prepared != nil && durable {
			result = &TransactionResult{TransactionID: prepared.journal.TransactionID, Documents: cloneDocuments(prepared.documents)}
		}
		if err != nil {
			return err
		}
		if prepared == nil {
			return errors.New("transaction preparation returned no journal")
		}
		result = &TransactionResult{TransactionID: prepared.journal.TransactionID, Documents: cloneDocuments(prepared.documents)}
		// Re-open and verify the durable journal plus every staged byte before
		// canonical publication. The in-memory candidate is never treated as a
		// substitute for the crash-recovery source of truth.
		return store.recoverPreparedTransactionsLocked(ctx)
	})
	return result, err
}

func (store *Store) transactionInventoryLocked(ctx context.Context) (*Inventory, error) {
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
	for _, diagnostic := range inventory.Diagnostics {
		switch diagnostic.Code {
		case "plan.dependency_stale", "plan.belief_stale", "queue.plan_stale", "queue.cluster_saturated":
			// A prepared transaction may repair these explicit stale-state
			// diagnostics, but its complete candidate inventory must be valid.
		default:
			return nil, &InventoryError{Diagnostics: append([]Diagnostic(nil), inventory.Diagnostics...)}
		}
	}
	return inventory, nil
}

// Recover rolls every known prepared journal forward under the same common lock
// used by ordinary Store mutations. It is safe to call repeatedly.
func (store *Store) Recover(ctx context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.withLockedRoots(ctx, func() error {
		if err := CleanupAtomicTempsRoot(store.canonicalRoot); err != nil {
			return fmt.Errorf("clean abandoned atomic temporaries: %w", err)
		}
		return store.recoverPreparedTransactionsLocked(ctx)
	})
}

func (store *Store) prepareTransactionLocked(ctx context.Context, inventory *Inventory, request TransactionRequest) (*preparedTransaction, bool, error) {
	if !transactionOperationPattern.MatchString(request.Operation) || len(request.Operation) > 64 {
		return nil, false, fmt.Errorf("operation %q must be a lower-case non-secret machine label of at most 64 bytes: %w", request.Operation, ErrInvalidTransaction)
	}
	if len(request.Changes) == 0 {
		return nil, false, fmt.Errorf("transaction requires at least one change: %w", ErrInvalidTransaction)
	}
	if len(request.Changes) > maxTransactionEntries {
		return nil, false, fmt.Errorf("transaction has %d entries; limit is %d: %w", len(request.Changes), maxTransactionEntries, ErrInvalidTransaction)
	}

	projectID, err := inventoryProjectID(inventory)
	if err != nil {
		return nil, false, err
	}
	working := cloneDocuments(inventory.Documents)
	seenIDs := make(map[research.ID]struct{}, len(request.Changes))
	seenPaths := make(map[string]struct{}, len(request.Changes))
	createdIDs := make([]research.ID, 0, len(request.Changes))
	entries := make([]preparedTransactionEntry, 0, len(request.Changes))
	results := make([]*Document, 0, len(request.Changes))

	for index, change := range request.Changes {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		workingInventory := candidateInventoryForBase(store.Root, working, inventory)
		entry, normalized, id, err := store.prepareTransactionChange(ctx, inventory, workingInventory, change)
		if err != nil {
			return nil, false, fmt.Errorf("change %d: %w", index, err)
		}
		if !id.IsZero() {
			if _, duplicate := seenIDs[id]; duplicate {
				return nil, false, fmt.Errorf("change %d repeats record %s: %w", index, id, ErrInvalidTransaction)
			}
			seenIDs[id] = struct{}{}
		}
		if _, duplicate := seenPaths[entry.journal.Path]; duplicate {
			return nil, false, fmt.Errorf("change %d repeats path %s: %w", index, entry.journal.Path, ErrInvalidTransaction)
		}
		seenPaths[entry.journal.Path] = struct{}{}

		switch change.Operation {
		case TransactionCreate:
			working = append(working, normalized)
			if !id.IsZero() {
				createdIDs = append(createdIDs, id)
			}
			results = append(results, normalized)
		case TransactionReplace:
			working = replaceTransactionDocument(working, id, entry.journal.Path, normalized)
			results = append(results, normalized)
		case TransactionDelete:
			working = removeTransactionDocument(working, id, entry.journal.Path)
		}
		entries = append(entries, entry)
	}

	candidate := candidateInventoryForBase(store.Root, working, inventory)
	if !candidate.Valid() && !(request.AllowStale && onlyRepairableStaleness(candidate)) {
		return nil, false, &InventoryError{Diagnostics: append([]Diagnostic(nil), candidate.Diagnostics...)}
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].journal.Path < entries[right].journal.Path })
	sort.Slice(createdIDs, func(left, right int) bool { return createdIDs[left].String() < createdIDs[right].String() })
	for _, id := range createdIDs {
		if err := reserveCanonicalID(store.coordinationRoot, id); err != nil {
			return nil, false, err
		}
	}

	now := store.clock().UTC()
	transactionID, transactionRoot, stagedRoot, err := store.createTransactionRoots(now)
	if err != nil {
		return nil, false, err
	}
	journal := transactionJournal{
		Schema:        transactionSchema,
		TransactionID: transactionID,
		ProjectID:     projectID,
		Operation:     request.Operation,
		CreatedAt:     now,
		Phase:         transactionPhasePrepared,
		Entries:       make([]transactionJournalEntry, len(entries)),
	}
	for index := range entries {
		staged := ""
		if entries[index].journal.Operation != TransactionDelete {
			staged = fmt.Sprintf("%04d", index)
		}
		entries[index].journal.Staged = staged
		journal.Entries[index] = entries[index].journal
	}
	prepared := &preparedTransaction{journal: journal, entries: entries, documents: results}

	durable, persistErr := store.persistPreparedTransaction(transactionRoot, stagedRoot, prepared)
	closeErr := errors.Join(stagedRoot.Close(), transactionRoot.Close())
	if persistErr != nil || closeErr != nil {
		return prepared, durable, errors.Join(persistErr, closeErr)
	}
	return prepared, true, nil
}

func onlyRepairableStaleness(inventory *Inventory) bool {
	if inventory == nil || len(inventory.Diagnostics) == 0 {
		return false
	}
	for _, diagnostic := range inventory.Diagnostics {
		switch diagnostic.Code {
		case "plan.dependency_stale", "plan.belief_stale", "queue.plan_stale", "queue.cluster_saturated":
		default:
			return false
		}
	}
	return true
}

func (store *Store) prepareTransactionChange(ctx context.Context, base, working *Inventory, change TransactionChange) (preparedTransactionEntry, *Document, research.ID, error) {
	var empty preparedTransactionEntry
	switch change.Operation {
	case TransactionCreate:
		if change.Document == nil || change.Document.Record == nil || change.Document.Kind() == research.KindProject {
			return empty, nil, research.ID{}, fmt.Errorf("create requires one non-Project document: %w", ErrInvalidTransaction)
		}
		if change.ExpectedRevision != "" || !change.ID.IsZero() || change.Path != "" {
			return empty, nil, research.ID{}, fmt.Errorf("create does not accept ID, Path, or expected revision outside Document: %w", ErrInvalidTransaction)
		}
		id, ok := change.Document.ID()
		if !ok && change.Document.Kind() != research.KindPolicy {
			return empty, nil, research.ID{}, fmt.Errorf("create document has no typed ID: %w", ErrInvalidTransaction)
		}
		if ok && len(working.byID[id]) != 0 {
			return empty, nil, id, fmt.Errorf("%s: %w", id, ErrAlreadyExists)
		}
		if change.Document.Kind() == research.KindPolicy && working.Policy != nil {
			return empty, nil, id, fmt.Errorf("%s: %w", PolicyFile, ErrAlreadyExists)
		}
		candidate := change.Document.Clone()
		var err error
		candidate.Path, err = PathForNew(candidate.Record, working)
		if err != nil {
			return empty, nil, id, err
		}
		observed, _, _, err := canonicalDestinationState(ctx, store.canonicalRoot, candidate.Path)
		if err != nil {
			return empty, nil, id, err
		}
		if observed != absentHash {
			return empty, nil, id, fmt.Errorf("canonical destination %s already exists: %w", candidate.Path, ErrAlreadyExists)
		}
		content, normalized, err := normalizedTransactionDocument(candidate, working)
		if err != nil {
			return empty, nil, id, err
		}
		return preparedTransactionEntry{
			journal: transactionJournalEntry{Path: candidate.Path, Operation: TransactionCreate, OldHash: absentHash, NewHash: exactHash(content), StagedHash: exactHash(content)},
			data:    content, document: normalized,
		}, normalized, id, nil

	case TransactionReplace:
		if change.Document == nil || change.Document.Record == nil || change.Document.Kind() == research.KindProject || !change.ID.IsZero() || change.Path != "" {
			return empty, nil, research.ID{}, fmt.Errorf("replace requires one non-Project Document and no separate ID or Path: %w", ErrInvalidTransaction)
		}
		if !ValidRevision(change.ExpectedRevision) {
			return empty, nil, research.ID{}, fmt.Errorf("replace expected revision is required: %w", ErrConflict)
		}
		id, ok := change.Document.ID()
		if !ok && change.Document.Kind() != research.KindPolicy {
			return empty, nil, research.ID{}, fmt.Errorf("replacement has no typed ID: %w", ErrInvalidTransaction)
		}
		var current *Document
		var err error
		if change.Document.Kind() == research.KindPolicy {
			current = base.Policy
			if current == nil {
				return empty, nil, id, fmt.Errorf("Policy does not exist: %w", ErrInvalidTransaction)
			}
		} else {
			current, err = base.ByID(id)
			if err != nil {
				return empty, nil, id, err
			}
		}
		if current.Revision != change.ExpectedRevision {
			return empty, nil, id, &ConflictError{ID: id, Path: current.Path, Expected: change.ExpectedRevision, Actual: current.Revision}
		}
		if err := validateTransactionReplacement(current, change.Document); err != nil {
			return empty, nil, id, err
		}
		candidate := change.Document.Clone()
		candidate.Path = current.Path
		content, normalized, err := normalizedTransactionDocument(candidate, working)
		if err != nil {
			return empty, nil, id, err
		}
		currentBytes, _, err := readCanonicalFileRoot(ctx, store.canonicalRoot, current.Path)
		if err != nil {
			return empty, nil, id, err
		}
		reloaded, err := decodeCanonicalDocument(currentBytes)
		if err != nil {
			return empty, nil, id, fmt.Errorf("re-read current record: %w", err)
		}
		if reloaded.Revision != change.ExpectedRevision {
			return empty, nil, id, &ConflictError{ID: id, Path: current.Path, Expected: change.ExpectedRevision, Actual: reloaded.Revision}
		}
		return preparedTransactionEntry{
			journal: transactionJournalEntry{Path: current.Path, Operation: TransactionReplace, OldHash: exactHash(currentBytes), NewHash: exactHash(content), StagedHash: exactHash(content)},
			data:    content, document: normalized,
		}, normalized, id, nil

	case TransactionDelete:
		policyDelete := change.ID.IsZero() && change.Path == PolicyFile
		if change.Document != nil || (!policyDelete && (change.ID.IsZero() || change.Path != "")) {
			return empty, nil, research.ID{}, fmt.Errorf("delete requires a typed ID or Path=%q and no Document: %w", PolicyFile, ErrInvalidTransaction)
		}
		if !ValidRevision(change.ExpectedRevision) {
			return empty, nil, change.ID, fmt.Errorf("delete expected revision is required: %w", ErrConflict)
		}
		var current *Document
		var err error
		if policyDelete {
			current = base.Policy
			if current == nil {
				return empty, nil, change.ID, fmt.Errorf("Policy does not exist: %w", ErrInvalidTransaction)
			}
		} else {
			current, err = base.ByID(change.ID)
			if err != nil {
				return empty, nil, change.ID, err
			}
		}
		if current.Kind() == research.KindProject {
			return empty, nil, change.ID, fmt.Errorf("Project cannot participate in a transaction: %w", ErrInvalidTransaction)
		}
		if immutableTransactionDelete(current) {
			return empty, nil, change.ID, fmt.Errorf("%s records are immutable audit evidence and cannot be deleted: %w", current.Kind(), ErrInvalidTransaction)
		}
		if current.Revision != change.ExpectedRevision {
			return empty, nil, change.ID, &ConflictError{ID: change.ID, Path: current.Path, Expected: change.ExpectedRevision, Actual: current.Revision}
		}
		currentBytes, _, err := readCanonicalFileRoot(ctx, store.canonicalRoot, current.Path)
		if err != nil {
			return empty, nil, change.ID, err
		}
		reloaded, err := decodeCanonicalDocument(currentBytes)
		if err != nil {
			return empty, nil, change.ID, fmt.Errorf("re-read current record: %w", err)
		}
		if reloaded.Revision != change.ExpectedRevision {
			return empty, nil, change.ID, &ConflictError{ID: change.ID, Path: current.Path, Expected: change.ExpectedRevision, Actual: reloaded.Revision}
		}
		return preparedTransactionEntry{
			journal: transactionJournalEntry{Path: current.Path, Operation: TransactionDelete, OldHash: exactHash(currentBytes), NewHash: absentHash, StagedHash: absentHash},
		}, nil, change.ID, nil
	default:
		return empty, nil, research.ID{}, fmt.Errorf("unknown change operation %q: %w", change.Operation, ErrInvalidTransaction)
	}
}

func immutableTransactionDelete(document *Document) bool {
	if document == nil || document.Record == nil {
		return false
	}
	switch document.Kind() {
	case research.KindQueueAdvice, research.KindBattle, research.KindPlan, research.KindRun,
		research.KindAttempt, research.KindEvaluationSpec, research.KindEvaluation, research.KindFinding,
		research.KindCandidate, research.KindPromotionSpec, research.KindPromotion,
		research.KindDecision:
		return true
	case research.KindRelease:
		return true
	case research.KindExperiment:
		return document.Record.(*research.Experiment).Lifecycle == research.LifecycleClosed
	default:
		return false
	}
}

func normalizedTransactionDocument(candidate *Document, inventory *Inventory) ([]byte, *Document, error) {
	content, normalized, err := encodeCandidateForBase(candidate, inventory)
	if err != nil {
		return nil, nil, err
	}
	normalized.Path = candidate.Path
	return content, normalized, nil
}

func inventoryProjectID(inventory *Inventory) (research.UUID, error) {
	if inventory == nil || inventory.Project == nil {
		return research.UUID{}, fmt.Errorf("transaction requires one canonical Project: %w", ErrInvalidTransaction)
	}
	project, ok := inventory.Project.Record.(*research.Project)
	if !ok || project.ProjectID.IsZero() {
		return research.UUID{}, fmt.Errorf("transaction Project identity is invalid: %w", ErrInvalidTransaction)
	}
	return project.ProjectID, nil
}

func removeTransactionDocument(documents []*Document, id research.ID, relative string) []*Document {
	out := make([]*Document, 0, len(documents)-1)
	for _, document := range documents {
		documentID, ok := document.ID()
		if (ok && !id.IsZero() && documentID == id) || (id.IsZero() && document.Path == relative) {
			continue
		}
		out = append(out, document)
	}
	return out
}

func replaceTransactionDocument(documents []*Document, id research.ID, relative string, replacement *Document) []*Document {
	out := make([]*Document, 0, len(documents))
	for _, document := range documents {
		documentID, ok := document.ID()
		if (ok && !id.IsZero() && documentID == id) || (id.IsZero() && document.Path == relative) {
			out = append(out, replacement)
			continue
		}
		out = append(out, document)
	}
	return out
}

func validateTransactionReplacement(current, replacement *Document) error {
	if current != nil && current.Kind() == research.KindPolicy {
		currentPolicy, currentOK := current.Record.(*research.Policy)
		replacementPolicy, replacementOK := replacement.Record.(*research.Policy)
		if !currentOK || !replacementOK || currentPolicy.Schema != replacementPolicy.Schema {
			return errors.New("Policy schema is immutable")
		}
		if !currentPolicy.CreatedAt.Equal(replacementPolicy.CreatedAt) {
			return errors.New("Policy created_at is immutable")
		}
		if replacementPolicy.UpdatedAt.Before(currentPolicy.UpdatedAt) {
			return errors.New("Policy updated_at cannot move backwards")
		}
		return nil
	}
	return validateImmutableUpdate(current, replacement)
}

func (store *Store) runTransactionHook(stage TransactionStage, transactionID, relative string) error {
	if store.transactionHook != nil {
		if err := store.transactionHook(stage, transactionID, relative); err != nil {
			return err
		}
	}
	return store.verifyMutationRoots()
}

func transactionAtomicHook(store *Store, transactionID, relative string) AtomicHook {
	return func(stage AtomicStage, _ string) error {
		mapped := StageTransactionTempCreate
		switch stage {
		case StageTempCreate:
			mapped = StageTransactionTempCreate
		case StageTempWrite:
			mapped = StageTransactionTempWrite
		case StageFileSync:
			mapped = StageTransactionFileSync
		case StageDirSync:
			mapped = StageTransactionStagedDirSync
		case StageRename:
			return store.verifyMutationRoots()
		}
		return store.runTransactionHook(mapped, transactionID, relative)
	}
}

func validateCanonicalTransactionPath(relative string) error {
	if err := pathx.ValidateRelativePOSIX(relative, false); err != nil {
		return err
	}
	location, recognized, err := ClassifyPath(relative)
	if err != nil {
		return err
	}
	if !recognized || location.Kind == research.KindProject {
		return fmt.Errorf("%s is not a mutable canonical record path", relative)
	}
	if _, generated := generatedProjectionNames[path.Clean(relative)]; generated {
		return fmt.Errorf("projection %s cannot participate in a canonical transaction", relative)
	}
	return nil
}

func verifyStagedDocument(relative string, data []byte) (*Document, error) {
	if err := ValidateRecordSize(data); err != nil {
		return nil, err
	}
	document, err := Decode(data)
	encode := Encode
	if err != nil {
		document, err = DecodeImported(data)
		encode = EncodeImported
	}
	if err != nil {
		return nil, err
	}
	location, recognized, err := ClassifyPath(relative)
	if err != nil || !recognized {
		return nil, errors.Join(err, fmt.Errorf("staged path %s is not canonical", relative))
	}
	document.Path = relative
	if err := ValidateDocumentPath(location, document); err != nil {
		return nil, err
	}
	encoded, err := encode(document)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded, data) {
		return nil, fmt.Errorf("staged bytes for %s are not the deterministic canonical encoding", relative)
	}
	return document, nil
}

func checkPrivateMode(info fs.FileInfo, want fs.FileMode, description string) error {
	if info == nil {
		return fmt.Errorf("%s has no file identity", description)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", description)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", description)
	}
	return checkModePortable(info.Mode(), want, description)
}
