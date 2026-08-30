package record

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	transactionSchema         = "exp.transaction/v1"
	transactionJournalFile    = "journal.toml"
	transactionStagedDir      = "staged"
	transactionPhasePrepared  = "prepared"
	transactionPhaseCommitted = "committed"
	absentHash                = "absent"
	maxTransactionJournalSize = int64(1 << 20)
	maxTransactionEntries     = 4096
	maxTransactionDirectories = 8192
	committedJournalRetention = 256
)

type transactionJournal struct {
	Schema        string                    `toml:"schema"`
	TransactionID string                    `toml:"transaction_id"`
	ProjectID     research.UUID             `toml:"project_id"`
	Operation     string                    `toml:"operation"`
	CreatedAt     time.Time                 `toml:"created_at"`
	Phase         string                    `toml:"phase"`
	Entries       []transactionJournalEntry `toml:"entries"`
}

type transactionJournalEntry struct {
	Path       string               `toml:"path"`
	Operation  TransactionOperation `toml:"operation"`
	OldHash    string               `toml:"old_hash"`
	NewHash    string               `toml:"new_hash"`
	Staged     string               `toml:"staged,omitempty"`
	StagedHash string               `toml:"staged_hash"`
}

func exactHash(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validExactHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func encodeTransactionJournal(journal transactionJournal) ([]byte, error) {
	var content bytes.Buffer
	if err := toml.NewEncoder(&content).Encode(journal); err != nil {
		return nil, fmt.Errorf("encode transaction journal: %w", err)
	}
	if int64(content.Len()) > maxTransactionJournalSize {
		return nil, fmt.Errorf("transaction journal is %d bytes; limit is %d: %w", content.Len(), maxTransactionJournalSize, ErrInvalidTransaction)
	}
	return content.Bytes(), nil
}

func decodeTransactionJournal(data []byte) (transactionJournal, error) {
	var journal transactionJournal
	metadata, err := toml.Decode(string(data), &journal)
	if err != nil {
		return journal, fmt.Errorf("decode transaction journal: %w", errors.Join(ErrInvalidTransaction, err))
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		fields := make([]string, len(undecoded))
		for index := range undecoded {
			fields[index] = undecoded[index].String()
		}
		sort.Strings(fields)
		return journal, fmt.Errorf("unknown transaction journal field(s) %v: %w", fields, ErrInvalidTransaction)
	}
	return journal, nil
}

func (store *Store) createTransactionRoots(now time.Time) (string, *os.Root, *os.Root, error) {
	transactions, err := pathx.OpenRootAtNoSymlinks(store.coordinationRoot, "transactions")
	if err != nil {
		return "", nil, nil, fmt.Errorf("open transactions directory: %w", err)
	}
	defer transactions.Close()
	for attempt := 0; attempt < store.collisionLimit; attempt++ {
		value, generateErr := store.generate(now)
		if generateErr != nil {
			return "", nil, nil, fmt.Errorf("generate transaction UUID: %w", generateErr)
		}
		parsed, parseErr := research.NewProjectUUID(value)
		if parseErr != nil {
			return "", nil, nil, fmt.Errorf("generate transaction UUIDv7: %w", parseErr)
		}
		transactionID := parsed.String()
		if err := store.runTransactionHook(StageTransactionDirectoryCreate, transactionID, path.Join("transactions", transactionID)); err != nil {
			return "", nil, nil, err
		}
		if err := transactions.Mkdir(transactionID, 0o700); errors.Is(err, fs.ErrExist) {
			continue
		} else if err != nil {
			return "", nil, nil, fmt.Errorf("create transaction directory %s: %w", transactionID, err)
		}
		if err := pathx.SyncRoot(transactions); err != nil {
			return "", nil, nil, fmt.Errorf("sync transactions directory: %w", err)
		}
		transactionRoot, err := pathx.OpenRootAtNoSymlinks(transactions, transactionID)
		if err != nil {
			return "", nil, nil, fmt.Errorf("open new transaction directory: %w", err)
		}
		if err := chmodDirectoryRoot(transactionRoot, 0o700); err != nil {
			_ = transactionRoot.Close()
			return "", nil, nil, fmt.Errorf("protect transaction directory: %w", err)
		}
		stagedRoot, _, err := pathx.EnsureRootAtNoSymlinks(transactionRoot, transactionStagedDir, 0o700)
		if err != nil {
			_ = transactionRoot.Close()
			return "", nil, nil, fmt.Errorf("create staged directory: %w", err)
		}
		if err := chmodDirectoryRoot(stagedRoot, 0o700); err != nil {
			_ = stagedRoot.Close()
			_ = transactionRoot.Close()
			return "", nil, nil, fmt.Errorf("protect staged directory: %w", err)
		}
		return transactionID, transactionRoot, stagedRoot, nil
	}
	return "", nil, nil, fmt.Errorf("allocate transaction identity after %d attempts: %w", store.collisionLimit, ErrCollision)
}

func (store *Store) persistPreparedTransaction(transactionRoot, stagedRoot *os.Root, prepared *preparedTransaction) (bool, error) {
	if transactionRoot == nil || stagedRoot == nil || prepared == nil {
		return false, fmt.Errorf("transaction persistence requires opened roots and a journal: %w", ErrInvalidTransaction)
	}
	transactionID := prepared.journal.TransactionID
	for index := range prepared.entries {
		entry := &prepared.entries[index]
		if entry.journal.Operation == TransactionDelete {
			continue
		}
		name := entry.journal.Staged
		err := AtomicWriteRoot(stagedRoot, name, entry.data, AtomicWriteOptions{
			Mode:   0o600,
			Hook:   transactionAtomicHook(store, transactionID, path.Join(transactionStagedDir, name)),
			Verify: store.verifyMutationRoots,
		})
		if err != nil {
			return false, fmt.Errorf("stage transaction entry %s: %w", entry.journal.Path, err)
		}
	}
	if err := store.runTransactionHook(StageTransactionStagedDirSync, transactionID, transactionStagedDir); err != nil {
		return false, err
	}
	if err := pathx.SyncRoot(stagedRoot); err != nil {
		return false, fmt.Errorf("sync staged transaction bytes: %w", err)
	}
	journalBytes, err := encodeTransactionJournal(prepared.journal)
	if err != nil {
		return false, err
	}
	prepared.journalBytes = append([]byte(nil), journalBytes...)
	if err := store.runTransactionHook(StageTransactionJournalPublish, transactionID, transactionJournalFile); err != nil {
		return false, err
	}
	writeErr := AtomicWriteRoot(transactionRoot, transactionJournalFile, journalBytes, AtomicWriteOptions{
		Mode: 0o600,
		Hook: func(AtomicStage, string) error {
			return store.verifyMutationRoots()
		},
		Verify: store.verifyMutationRoots,
	})
	durable := writeErr == nil || publicationSucceeded(writeErr)
	if writeErr != nil {
		return durable, fmt.Errorf("publish prepared transaction journal: %w", writeErr)
	}
	if err := store.runTransactionHook(StageTransactionJournalDirSync, transactionID, transactionJournalFile); err != nil {
		return true, err
	}
	if err := pathx.SyncRoot(transactionRoot); err != nil {
		return true, fmt.Errorf("sync transaction directory: %w", err)
	}
	transactions, err := pathx.OpenRootAtNoSymlinks(store.coordinationRoot, "transactions")
	if err != nil {
		return true, err
	}
	syncErr := pathx.SyncRoot(transactions)
	closeErr := transactions.Close()
	if syncErr != nil || closeErr != nil {
		return true, fmt.Errorf("sync transactions parent: %w", errors.Join(syncErr, closeErr))
	}
	return true, nil
}

func (store *Store) recoverPreparedTransactionsLocked(ctx context.Context) error {
	if err := cleanupTransactionAtomicTemps(store.coordinationRoot); err != nil {
		return fmt.Errorf("clean abandoned transaction temporaries: %w", err)
	}
	if err := cleanupUnjournaledTransactions(store.coordinationRoot); err != nil {
		return fmt.Errorf("clean unjournaled transaction preparation: %w", err)
	}
	projectID, err := canonicalProjectID(ctx, store.canonicalRoot)
	if err != nil {
		return err
	}
	transactions, err := loadTransactionJournals(ctx, store.coordinationRoot, projectID)
	if err != nil {
		return err
	}
	// All journals and staged files are validated before any one transaction is
	// allowed to change canonical state. A committed journal is historical: a
	// later transaction may legitimately have changed the same destination.
	for _, transaction := range transactions {
		if transaction.journal.Phase != transactionPhasePrepared {
			continue
		}
		for index := range transaction.entries {
			entry := &transaction.entries[index]
			observed, _, _, err := canonicalDestinationState(ctx, store.canonicalRoot, entry.journal.Path)
			if err != nil {
				return err
			}
			if observed != entry.journal.OldHash && observed != entry.journal.NewHash {
				return &TransactionConflictError{
					TransactionID: transaction.journal.TransactionID,
					Path:          entry.journal.Path,
					OldHash:       entry.journal.OldHash,
					NewHash:       entry.journal.NewHash,
					ObservedHash:  observed,
				}
			}
		}
	}
	for _, transaction := range transactions {
		if transaction.journal.Phase != transactionPhasePrepared {
			continue
		}
		if err := store.publishPreparedTransactionLocked(ctx, transaction); err != nil {
			return err
		}
	}
	return pruneCommittedTransactions(store.coordinationRoot, transactions, committedJournalRetention)
}

func cleanupUnjournaledTransactions(coordination *os.Root) error {
	transactions, err := pathx.OpenRootAtNoSymlinks(coordination, "transactions")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer transactions.Close()
	entries, err := readRootDirectory(transactions)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		id, parseErr := research.ParseUUID(entry.Name())
		if parseErr != nil || !id.IsNative() {
			continue
		}
		root, openErr := pathx.OpenRootAtNoSymlinks(transactions, entry.Name())
		if openErr != nil {
			return openErr
		}
		_, journalErr := root.Lstat(transactionJournalFile)
		if journalErr == nil {
			if closeErr := root.Close(); closeErr != nil {
				return closeErr
			}
			continue
		}
		if !errors.Is(journalErr, fs.ErrNotExist) {
			_ = root.Close()
			return journalErr
		}
		rootEntries, readErr := readRootDirectory(root)
		if readErr != nil {
			_ = root.Close()
			return readErr
		}
		for _, artifact := range rootEntries {
			if artifact.Name() != transactionStagedDir {
				_ = root.Close()
				return fmt.Errorf("unjournaled transaction %s contains unknown artifact %s", entry.Name(), artifact.Name())
			}
		}
		staged, stagedErr := pathx.OpenRootAtNoSymlinks(root, transactionStagedDir)
		if stagedErr == nil {
			stagedEntries, stagedReadErr := readRootDirectory(staged)
			if stagedReadErr != nil {
				_ = staged.Close()
				_ = root.Close()
				return stagedReadErr
			}
			for _, artifact := range stagedEntries {
				info, statErr := staged.Lstat(artifact.Name())
				if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
					_ = staged.Close()
					_ = root.Close()
					return fmt.Errorf("unjournaled staged artifact %s is not a regular file", artifact.Name())
				}
				if removeErr := staged.Remove(artifact.Name()); removeErr != nil {
					_ = staged.Close()
					_ = root.Close()
					return removeErr
				}
			}
			if syncErr := pathx.SyncRoot(staged); syncErr != nil {
				_ = staged.Close()
				_ = root.Close()
				return syncErr
			}
			if closeErr := staged.Close(); closeErr != nil {
				_ = root.Close()
				return closeErr
			}
			if removeErr := root.Remove(transactionStagedDir); removeErr != nil {
				_ = root.Close()
				return removeErr
			}
		} else if !errors.Is(stagedErr, fs.ErrNotExist) {
			_ = root.Close()
			return stagedErr
		}
		if closeErr := root.Close(); closeErr != nil {
			return closeErr
		}
		if removeErr := transactions.Remove(entry.Name()); removeErr != nil {
			return removeErr
		}
		removed = true
	}
	if removed {
		return pathx.SyncRoot(transactions)
	}
	return nil
}

func cleanupTransactionAtomicTemps(coordination *os.Root) error {
	transactions, err := pathx.OpenRootAtNoSymlinks(coordination, "transactions")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer transactions.Close()
	transactionEntries, err := readRootDirectory(transactions)
	if err != nil {
		return err
	}
	for _, transactionEntry := range transactionEntries {
		parsedID, parseErr := research.ParseUUID(transactionEntry.Name())
		if parseErr != nil || !parsedID.IsNative() {
			continue
		}
		info, err := transactions.Lstat(transactionEntry.Name())
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		transactionRoot, err := pathx.OpenRootAtNoSymlinks(transactions, transactionEntry.Name())
		if err != nil {
			return err
		}
		cleanupErr := cleanupAtomicTempsInRoot(transactionRoot)
		stagedRoot, stagedErr := pathx.OpenRootAtNoSymlinks(transactionRoot, transactionStagedDir)
		if stagedErr == nil {
			stagedErr = cleanupAtomicTempsInRoot(stagedRoot)
			stagedErr = errors.Join(stagedErr, stagedRoot.Close())
		} else if errors.Is(stagedErr, fs.ErrNotExist) {
			stagedErr = nil
		}
		verifyErr := pathx.VerifyRootAt(transactions, transactionEntry.Name(), transactionRoot)
		closeErr := transactionRoot.Close()
		if cleanupErr != nil || stagedErr != nil || verifyErr != nil || closeErr != nil {
			return errors.Join(cleanupErr, stagedErr, verifyErr, closeErr)
		}
	}
	return nil
}

func cleanupAtomicTempsInRoot(root *os.Root) error {
	entries, err := readRootDirectory(root)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if !IsAtomicTempName(entry.Name()) {
			continue
		}
		info, err := root.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("transaction temporary %s is not a regular non-symlink file", entry.Name())
		}
		if err := root.Remove(entry.Name()); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return pathx.SyncRoot(root)
	}
	return nil
}

func (store *Store) publishPreparedTransactionLocked(ctx context.Context, prepared *preparedTransaction) error {
	if prepared == nil || prepared.journal.Phase != transactionPhasePrepared {
		return fmt.Errorf("publish requires a prepared journal: %w", ErrInvalidTransaction)
	}
	for index := range prepared.entries {
		entry := &prepared.entries[index]
		observed, currentBytes, identity, err := canonicalDestinationState(ctx, store.canonicalRoot, entry.journal.Path)
		if err != nil {
			return err
		}
		if observed == entry.journal.NewHash {
			continue
		}
		if observed != entry.journal.OldHash {
			return &TransactionConflictError{
				TransactionID: prepared.journal.TransactionID,
				Path:          entry.journal.Path,
				OldHash:       entry.journal.OldHash,
				NewHash:       entry.journal.NewHash,
				ObservedHash:  observed,
			}
		}
		switch entry.journal.Operation {
		case TransactionCreate:
			if err := store.runTransactionHook(StageTransactionCanonicalCreate, prepared.journal.TransactionID, entry.journal.Path); err != nil {
				return err
			}
			err = AtomicWriteRoot(store.canonicalRoot, entry.journal.Path, entry.data, AtomicWriteOptions{
				Hook:   store.canonicalTransactionAtomicHook(prepared.journal.TransactionID, entry.journal.Path),
				Verify: store.verifyMutationRoots,
			})
		case TransactionReplace:
			if err := store.runTransactionHook(StageTransactionCanonicalCAS, prepared.journal.TransactionID, entry.journal.Path); err != nil {
				return err
			}
			err = AtomicWriteRoot(store.canonicalRoot, entry.journal.Path, entry.data, AtomicWriteOptions{
				Expected:        identity,
				ExpectedContent: currentBytes,
				Hook:            store.canonicalTransactionAtomicHook(prepared.journal.TransactionID, entry.journal.Path),
				Verify:          store.verifyMutationRoots,
			})
		case TransactionDelete:
			if err := store.runTransactionHook(StageTransactionCanonicalDelete, prepared.journal.TransactionID, entry.journal.Path); err != nil {
				return err
			}
			err = store.removeCanonicalTransactionEntry(ctx, prepared.journal.TransactionID, entry.journal.Path, currentBytes, identity)
		default:
			err = fmt.Errorf("unknown transaction operation %q: %w", entry.journal.Operation, ErrUnsupportedTransaction)
		}
		if err != nil {
			return err
		}
		observed, _, _, err = canonicalDestinationState(ctx, store.canonicalRoot, entry.journal.Path)
		if err != nil {
			return err
		}
		if observed != entry.journal.NewHash {
			return &TransactionConflictError{
				TransactionID: prepared.journal.TransactionID,
				Path:          entry.journal.Path,
				OldHash:       entry.journal.OldHash,
				NewHash:       entry.journal.NewHash,
				ObservedHash:  observed,
			}
		}
	}
	return store.markTransactionCommitted(prepared)
}

func (store *Store) canonicalTransactionAtomicHook(transactionID, relative string) AtomicHook {
	return func(stage AtomicStage, destination string) error {
		if store.atomicHook != nil {
			if err := store.atomicHook(stage, destination); err != nil {
				return err
			}
		}
		if stage == StageDirSync {
			return store.runTransactionHook(StageTransactionCanonicalSync, transactionID, relative)
		}
		return store.verifyMutationRoots()
	}
}

func (store *Store) removeCanonicalTransactionEntry(ctx context.Context, transactionID, relative string, expected []byte, identity fs.FileInfo) error {
	parentRelative := path.Dir(relative)
	parent, err := pathx.OpenRootAtNoSymlinks(store.canonicalRoot, parentRelative)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := pathx.VerifyRootAt(store.canonicalRoot, parentRelative, parent); err != nil {
		return fmt.Errorf("canonical parent %s changed before unlink: %w", parentRelative, err)
	}
	name := path.Base(relative)
	current, currentIdentity, err := pathx.ReadBoundedRegularFile(ctx, parent, name, MaxRecordBytes)
	if err != nil {
		return err
	}
	if !os.SameFile(identity, currentIdentity) || !bytes.Equal(expected, current) {
		return ErrAtomicConflict
	}
	if err := store.verifyMutationRoots(); err != nil {
		return err
	}
	if err := pathx.VerifyRootAt(store.canonicalRoot, parentRelative, parent); err != nil {
		return fmt.Errorf("canonical parent %s changed before unlink: %w", parentRelative, err)
	}
	if err := parent.Remove(name); err != nil {
		return fmt.Errorf("unlink canonical transaction entry %s: %w", relative, err)
	}
	if err := store.runTransactionHook(StageTransactionCanonicalSync, transactionID, relative); err != nil {
		return err
	}
	if err := pathx.SyncRoot(parent); err != nil {
		return fmt.Errorf("sync canonical parent %s: %w", parentRelative, err)
	}
	return store.verifyMutationRoots()
}

func (store *Store) markTransactionCommitted(prepared *preparedTransaction) error {
	transactions, err := pathx.OpenRootAtNoSymlinks(store.coordinationRoot, "transactions")
	if err != nil {
		return err
	}
	defer transactions.Close()
	transactionRoot, err := pathx.OpenRootAtNoSymlinks(transactions, prepared.journal.TransactionID)
	if err != nil {
		return err
	}
	defer transactionRoot.Close()
	currentBytes, identity, err := pathx.ReadBoundedRegularFile(context.Background(), transactionRoot, transactionJournalFile, maxTransactionJournalSize)
	if err != nil {
		return fmt.Errorf("re-read prepared journal: %w", err)
	}
	expectedBytes := prepared.journalBytes
	if len(expectedBytes) == 0 {
		return fmt.Errorf("prepared journal has no exact publication bytes: %w", ErrInvalidTransaction)
	}
	if !bytes.Equal(currentBytes, expectedBytes) {
		return fmt.Errorf("prepared journal changed before commit marking: %w", ErrInvalidTransaction)
	}
	committed := prepared.journal
	committed.Phase = transactionPhaseCommitted
	committedBytes, err := encodeTransactionJournal(committed)
	if err != nil {
		return err
	}
	if err := store.runTransactionHook(StageTransactionCommitMark, prepared.journal.TransactionID, transactionJournalFile); err != nil {
		return err
	}
	writeErr := AtomicWriteDerivedRoot(transactionRoot, transactionJournalFile, committedBytes, AtomicWriteOptions{
		Expected:        identity,
		ExpectedContent: currentBytes,
		Mode:            0o600,
		Hook: func(AtomicStage, string) error {
			return store.verifyMutationRoots()
		},
		Verify: store.verifyMutationRoots,
	})
	if writeErr != nil {
		return fmt.Errorf("mark transaction committed: %w", writeErr)
	}
	if err := pathx.SyncRoot(transactionRoot); err != nil {
		return fmt.Errorf("sync committed transaction directory: %w", err)
	}
	if err := pathx.SyncRoot(transactions); err != nil {
		return fmt.Errorf("sync transactions directory after commit: %w", err)
	}
	prepared.journal = committed
	prepared.journalBytes = append(prepared.journalBytes[:0], committedBytes...)
	return nil
}

func canonicalDestinationState(ctx context.Context, root *os.Root, relative string) (string, []byte, fs.FileInfo, error) {
	if err := validateCanonicalTransactionPath(relative); err != nil {
		return "", nil, nil, err
	}
	parent, err := pathx.OpenRootAtNoSymlinks(root, path.Dir(relative))
	if errors.Is(err, fs.ErrNotExist) {
		return absentHash, nil, nil, nil
	}
	if err != nil {
		return "", nil, nil, err
	}
	defer parent.Close()
	data, identity, err := pathx.ReadBoundedRegularFile(ctx, parent, path.Base(relative), MaxRecordBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return absentHash, nil, nil, nil
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("read transaction destination %s: %w", relative, err)
	}
	return exactHash(data), data, identity, nil
}

func canonicalProjectID(ctx context.Context, root *os.Root) (research.UUID, error) {
	data, _, err := readCanonicalFileRoot(ctx, root, ProjectFile)
	if err != nil {
		return research.UUID{}, fmt.Errorf("read transaction Project identity: %w", err)
	}
	document, err := decodeCanonicalDocument(data)
	if err != nil {
		return research.UUID{}, fmt.Errorf("decode transaction Project identity: %w", err)
	}
	project, ok := document.Record.(*research.Project)
	if !ok || project.ProjectID.IsZero() {
		return research.UUID{}, fmt.Errorf("canonical Project identity is invalid: %w", ErrInvalidTransaction)
	}
	return project.ProjectID, nil
}

func loadTransactionJournals(ctx context.Context, coordination *os.Root, projectID research.UUID) ([]*preparedTransaction, error) {
	transactions, err := pathx.OpenRootAtNoSymlinks(coordination, "transactions")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open transactions directory: %w", err)
	}
	defer transactions.Close()
	entries, err := readRootDirectory(transactions)
	if err != nil {
		return nil, err
	}
	if len(entries) > maxTransactionDirectories {
		return nil, fmt.Errorf("transactions directory has %d entries; limit is %d: %w", len(entries), maxTransactionDirectories, ErrUnsupportedTransaction)
	}
	loaded := make([]*preparedTransaction, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := transactions.Lstat(entry.Name())
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("transaction artifact %s is not a real directory: %w", entry.Name(), ErrUnsupportedTransaction)
		}
		parsedID, err := research.ParseUUID(entry.Name())
		if err != nil || !parsedID.IsNative() {
			return nil, fmt.Errorf("unknown transaction directory %s: %w", entry.Name(), errors.Join(ErrUnsupportedTransaction, err))
		}
		transactionRoot, err := pathx.OpenRootAtNoSymlinks(transactions, entry.Name())
		if err != nil {
			return nil, err
		}
		transaction, loadErr := loadOneTransaction(ctx, transactionRoot, entry.Name(), projectID)
		verifyErr := pathx.VerifyRootAt(transactions, entry.Name(), transactionRoot)
		closeErr := transactionRoot.Close()
		if loadErr != nil || verifyErr != nil || closeErr != nil {
			return nil, fmt.Errorf("load transaction %s: %w", entry.Name(), errors.Join(loadErr, verifyErr, closeErr))
		}
		loaded = append(loaded, transaction)
	}
	sort.Slice(loaded, func(left, right int) bool {
		return loaded[left].journal.TransactionID < loaded[right].journal.TransactionID
	})
	return loaded, nil
}

func pruneCommittedTransactions(coordination *os.Root, loaded []*preparedTransaction, keep int) error {
	if keep < 0 {
		keep = 0
	}
	committed := make([]*preparedTransaction, 0, len(loaded))
	for _, transaction := range loaded {
		if transaction != nil && transaction.journal.Phase == transactionPhaseCommitted {
			committed = append(committed, transaction)
		}
	}
	if len(committed) <= keep {
		return nil
	}
	sort.Slice(committed, func(i, j int) bool { return committed[i].journal.TransactionID < committed[j].journal.TransactionID })
	transactions, err := pathx.OpenRootAtNoSymlinks(coordination, "transactions")
	if err != nil {
		return err
	}
	defer transactions.Close()
	for _, transaction := range committed[:len(committed)-keep] {
		if err := removeCommittedTransactionDirectory(transactions, transaction.journal.TransactionID); err != nil {
			return fmt.Errorf("prune committed transaction %s: %w", transaction.journal.TransactionID, err)
		}
	}
	return pathx.SyncRoot(transactions)
}

func removeCommittedTransactionDirectory(transactions *os.Root, id string) error {
	root, err := pathx.OpenRootAtNoSymlinks(transactions, id)
	if err != nil {
		return err
	}
	// The committed journal is the only thing that makes this directory part of
	// recovery. Unlink it first: after any crash, the remaining staged-only
	// directory is harmless garbage that cleanupUnjournaledTransactions can
	// remove. Removing staged bytes first could leave a committed journal whose
	// promised recovery artifacts are missing and block every later mutation.
	if err := root.Remove(transactionJournalFile); err != nil {
		_ = root.Close()
		return err
	}
	if err := pathx.SyncRoot(root); err != nil {
		_ = root.Close()
		return err
	}
	if err := pathx.SyncRoot(transactions); err != nil {
		_ = root.Close()
		return err
	}
	staged, err := pathx.OpenRootAtNoSymlinks(root, transactionStagedDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = root.Close()
		return err
	}
	if err == nil {
		entries, readErr := readRootDirectory(staged)
		if readErr != nil {
			_ = staged.Close()
			_ = root.Close()
			return readErr
		}
		for _, entry := range entries {
			info, statErr := staged.Lstat(entry.Name())
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				_ = staged.Close()
				_ = root.Close()
				return fmt.Errorf("staged artifact %s is not a regular file", entry.Name())
			}
			if removeErr := staged.Remove(entry.Name()); removeErr != nil {
				_ = staged.Close()
				_ = root.Close()
				return removeErr
			}
		}
		if syncErr := pathx.SyncRoot(staged); syncErr != nil {
			_ = staged.Close()
			_ = root.Close()
			return syncErr
		}
		if closeErr := staged.Close(); closeErr != nil {
			_ = root.Close()
			return closeErr
		}
		if removeErr := root.Remove(transactionStagedDir); removeErr != nil {
			_ = root.Close()
			return removeErr
		}
	}
	if err := pathx.SyncRoot(root); err != nil {
		_ = root.Close()
		return err
	}
	if err := root.Close(); err != nil {
		return err
	}
	return transactions.Remove(id)
}

func loadOneTransaction(ctx context.Context, transactionRoot *os.Root, directoryID string, projectID research.UUID) (*preparedTransaction, error) {
	if err := checkDirectoryRootMode(transactionRoot, 0o700, "transaction directory"); err != nil {
		return nil, err
	}
	rootEntries, err := readRootDirectory(transactionRoot)
	if err != nil {
		return nil, err
	}
	if len(rootEntries) != 2 || rootEntries[0].Name() != transactionJournalFile || rootEntries[1].Name() != transactionStagedDir {
		return nil, fmt.Errorf("transaction directory must contain only %s and %s: %w", transactionJournalFile, transactionStagedDir, ErrUnsupportedTransaction)
	}
	journalBytes, journalInfo, err := pathx.ReadBoundedRegularFile(ctx, transactionRoot, transactionJournalFile, maxTransactionJournalSize)
	if err != nil {
		return nil, fmt.Errorf("read transaction journal: %w", errors.Join(ErrUnsupportedTransaction, err))
	}
	if err := checkPrivateMode(journalInfo, 0o600, "transaction journal"); err != nil {
		return nil, errors.Join(ErrUnsupportedTransaction, err)
	}
	journal, err := decodeTransactionJournal(journalBytes)
	if err != nil {
		return nil, errors.Join(ErrUnsupportedTransaction, err)
	}
	if err := validateTransactionJournal(journal, directoryID, projectID); err != nil {
		return nil, err
	}
	stagedRoot, err := pathx.OpenRootAtNoSymlinks(transactionRoot, transactionStagedDir)
	if err != nil {
		return nil, fmt.Errorf("open staged transaction directory: %w", errors.Join(ErrUnsupportedTransaction, err))
	}
	defer stagedRoot.Close()
	if err := checkDirectoryRootMode(stagedRoot, 0o700, "staged transaction directory"); err != nil {
		return nil, errors.Join(ErrUnsupportedTransaction, err)
	}
	stagedEntries, err := readRootDirectory(stagedRoot)
	if err != nil {
		return nil, err
	}
	expectedStaged := make(map[string]struct{})
	prepared := &preparedTransaction{journal: journal, journalBytes: append([]byte(nil), journalBytes...), entries: make([]preparedTransactionEntry, len(journal.Entries))}
	for index, journalEntry := range journal.Entries {
		entry := preparedTransactionEntry{journal: journalEntry}
		if journalEntry.Operation != TransactionDelete {
			expectedName := fmt.Sprintf("%04d", index)
			if journalEntry.Staged != expectedName {
				return nil, fmt.Errorf("entry %d staged name is %q, want %q: %w", index, journalEntry.Staged, expectedName, ErrUnsupportedTransaction)
			}
			expectedStaged[expectedName] = struct{}{}
			data, info, err := pathx.ReadBoundedRegularFile(ctx, stagedRoot, expectedName, MaxRecordBytes)
			if err != nil {
				return nil, fmt.Errorf("read staged entry %d: %w", index, errors.Join(ErrUnsupportedTransaction, err))
			}
			if err := checkPrivateMode(info, 0o600, "staged transaction file"); err != nil {
				return nil, errors.Join(ErrUnsupportedTransaction, err)
			}
			if exactHash(data) != journalEntry.StagedHash {
				return nil, fmt.Errorf("staged hash mismatch for %s: journal %s observed %s: %w", journalEntry.Path, journalEntry.StagedHash, exactHash(data), ErrUnsupportedTransaction)
			}
			document, err := verifyStagedDocument(journalEntry.Path, data)
			if err != nil {
				return nil, fmt.Errorf("invalid staged document %s: %w", journalEntry.Path, errors.Join(ErrUnsupportedTransaction, err))
			}
			entry.data = data
			entry.document = document
			prepared.documents = append(prepared.documents, document)
		}
		prepared.entries[index] = entry
	}
	if len(stagedEntries) != len(expectedStaged) {
		return nil, fmt.Errorf("staged directory contains unexpected entries: %w", ErrUnsupportedTransaction)
	}
	for _, entry := range stagedEntries {
		if _, found := expectedStaged[entry.Name()]; !found {
			return nil, fmt.Errorf("unknown staged transaction artifact %s: %w", entry.Name(), ErrUnsupportedTransaction)
		}
	}
	if err := pathx.VerifyRootAt(transactionRoot, transactionStagedDir, stagedRoot); err != nil {
		return nil, fmt.Errorf("staged transaction directory changed while loading: %w", err)
	}
	journalAgain, _, err := pathx.ReadBoundedRegularFile(ctx, transactionRoot, transactionJournalFile, maxTransactionJournalSize)
	if err != nil || !bytes.Equal(journalAgain, journalBytes) {
		return nil, fmt.Errorf("transaction journal changed while loading: %w", errors.Join(ErrUnsupportedTransaction, err))
	}
	return prepared, nil
}

func validateTransactionJournal(journal transactionJournal, directoryID string, projectID research.UUID) error {
	if journal.Schema != transactionSchema {
		return fmt.Errorf("unknown transaction schema %q: %w", journal.Schema, ErrUnsupportedTransaction)
	}
	parsedID, err := research.ParseUUID(journal.TransactionID)
	if err != nil || !parsedID.IsNative() || journal.TransactionID != directoryID {
		return fmt.Errorf("transaction ID %q does not match directory %q: %w", journal.TransactionID, directoryID, errors.Join(ErrUnsupportedTransaction, err))
	}
	if journal.ProjectID != projectID {
		return fmt.Errorf("transaction %s belongs to project %s, current project is %s: %w", journal.TransactionID, journal.ProjectID, projectID, ErrUnsupportedTransaction)
	}
	if !transactionOperationPattern.MatchString(journal.Operation) || len(journal.Operation) > 64 {
		return fmt.Errorf("invalid transaction operation %q: %w", journal.Operation, ErrUnsupportedTransaction)
	}
	if journal.CreatedAt.IsZero() {
		return fmt.Errorf("transaction created_at is required: %w", ErrUnsupportedTransaction)
	}
	_, offset := journal.CreatedAt.Zone()
	if offset != 0 {
		return fmt.Errorf("transaction created_at must use UTC: %w", ErrUnsupportedTransaction)
	}
	if journal.Phase != transactionPhasePrepared && journal.Phase != transactionPhaseCommitted {
		return fmt.Errorf("unknown transaction phase %q: %w", journal.Phase, ErrUnsupportedTransaction)
	}
	if len(journal.Entries) == 0 || len(journal.Entries) > maxTransactionEntries {
		return fmt.Errorf("transaction entry count %d is invalid: %w", len(journal.Entries), ErrUnsupportedTransaction)
	}
	previous := ""
	for index, entry := range journal.Entries {
		if err := validateCanonicalTransactionPath(entry.Path); err != nil {
			return fmt.Errorf("entry %d path: %w", index, errors.Join(ErrUnsupportedTransaction, err))
		}
		if previous != "" && entry.Path <= previous {
			return fmt.Errorf("transaction entries are not in strict path order at %s: %w", entry.Path, ErrUnsupportedTransaction)
		}
		previous = entry.Path
		switch entry.Operation {
		case TransactionCreate:
			if entry.OldHash != absentHash || !validExactHash(entry.NewHash) || entry.Staged == "" || entry.StagedHash != entry.NewHash {
				return fmt.Errorf("invalid create entry %s: %w", entry.Path, ErrUnsupportedTransaction)
			}
		case TransactionReplace:
			if !validExactHash(entry.OldHash) || !validExactHash(entry.NewHash) || entry.Staged == "" || entry.StagedHash != entry.NewHash {
				return fmt.Errorf("invalid replace entry %s: %w", entry.Path, ErrUnsupportedTransaction)
			}
		case TransactionDelete:
			if !validExactHash(entry.OldHash) || entry.NewHash != absentHash || entry.Staged != "" || entry.StagedHash != absentHash {
				return fmt.Errorf("invalid delete entry %s: %w", entry.Path, ErrUnsupportedTransaction)
			}
		default:
			return fmt.Errorf("unknown entry operation %q: %w", entry.Operation, ErrUnsupportedTransaction)
		}
	}
	return nil
}

func inspectTransactionJournalsReadOnly(ctx context.Context, coordination, canonical *os.Root) error {
	projectID, err := canonicalProjectID(ctx, canonical)
	if err != nil {
		return err
	}
	transactions, err := loadTransactionJournals(ctx, coordination, projectID)
	if err != nil {
		return err
	}
	for _, transaction := range transactions {
		if transaction.journal.Phase == transactionPhasePrepared {
			return fmt.Errorf("transaction %s is prepared: %w", transaction.journal.TransactionID, ErrTransactionRecoveryRequired)
		}
	}
	return nil
}

func chmodDirectoryRoot(root *os.Root, mode fs.FileMode) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	chmodErr := directory.Chmod(mode)
	closeErr := directory.Close()
	return errors.Join(chmodErr, closeErr)
}

func checkDirectoryRootMode(root *os.Root, want fs.FileMode, description string) error {
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", description)
	}
	return checkModePortable(info.Mode(), want, description)
}

func checkModePortable(mode, want fs.FileMode, description string) error {
	if runtime.GOOS != "windows" && (mode.Perm() != want.Perm() || mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0) {
		return fmt.Errorf("%s mode is %s, want %04o", description, mode, want.Perm())
	}
	return nil
}
