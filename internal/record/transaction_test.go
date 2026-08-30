package record_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestPreparedTransactionPublishesValidatedCompoundState(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02200-0000-7001-8000-000000000001", nil)
	alpha := createTransactionPlan(t, store, transactionPlan(t, "01a02100-0000-7001-8000-000000000001", "Alpha", now))
	beta := createTransactionIdea(t, store, transactionIdea(t, "01a02100-0000-7002-8000-000000000002", "Beta", now))

	replacement := alpha.Clone()
	replacement.Record.(*research.Plan).Title = "Alpha improved"
	replacement.Record.(*research.Plan).UpdatedAt = now.Add(time.Minute)
	replacement.Body = "replacement bytes\n"
	gamma := transactionPlan(t, "01a02100-0000-7003-8000-000000000003", "Gamma", now.Add(time.Minute))
	projectionPath := filepath.Join(info.Root, "ROADMAP.md")
	projectionBytes := []byte("projection remains outside canonical transaction\n")
	if err := os.WriteFile(projectionPath, projectionBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.compound",
		Changes: []record.TransactionChange{
			{Operation: record.TransactionCreate, Document: gamma},
			{Operation: record.TransactionDelete, ID: beta.Record.(*research.Idea).ID, ExpectedRevision: beta.Revision},
			{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: alpha.Revision},
		},
	})
	if err != nil || result == nil || len(result.Documents) != 2 {
		t.Fatalf("Transact = %#v, %v", result, err)
	}
	if parsed, parseErr := research.ParseUUID(result.TransactionID); parseErr != nil || !parsed.IsNative() {
		t.Fatalf("transaction ID = %q, %v", result.TransactionID, parseErr)
	}
	inventory, err := store.Inventory(context.Background())
	if err != nil || !inventory.Valid() {
		t.Fatalf("Inventory after transaction = %#v, %v", inventory, err)
	}
	if _, err := inventory.ByID(beta.Record.(*research.Idea).ID); err == nil {
		t.Fatal("deleted Idea remains in inventory")
	}
	updated, err := inventory.ByID(alpha.Record.(*research.Plan).ID)
	if err != nil || updated.Record.(*research.Plan).Title != "Alpha improved" {
		t.Fatalf("replacement = %#v, %v", updated, err)
	}
	created, err := inventory.ByID(gamma.Record.(*research.Plan).ID)
	if err != nil || created.Record.(*research.Plan).Title != "Gamma" {
		t.Fatalf("create = %#v, %v", created, err)
	}
	if actual, readErr := os.ReadFile(projectionPath); readErr != nil || string(actual) != string(projectionBytes) {
		t.Fatalf("projection participated in transaction: %q, %v", actual, readErr)
	}
	journalPath := filepath.Join(store.CoordinationDir(), "transactions", result.TransactionID, "journal.toml")
	journal, err := os.ReadFile(journalPath)
	if err != nil || !strings.Contains(string(journal), `phase = "committed"`) {
		t.Fatalf("committed journal = %q, %v", journal, err)
	}
	assertStoreMode(t, journalPath, 0o600)
	assertStoreMode(t, filepath.Dir(journalPath), 0o700)
	assertStoreMode(t, filepath.Join(filepath.Dir(journalPath), "staged"), 0o700)
	if strings.Contains(string(journal), info.Root) || strings.Contains(string(journal), store.GitCommonDir) {
		t.Fatalf("journal contains an absolute path: %s", journal)
	}
}

func TestPreparedTransactionRecoveryRollsForwardAndIsIdempotent(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	crash := errors.New("injected crash before second publication")
	var secondPath string
	hook := func(stage record.TransactionStage, _, relative string) error {
		if stage == record.StageTransactionCanonicalCAS && relative == secondPath {
			return crash
		}
		return nil
	}
	store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02201-0000-7001-8000-000000000001", hook)
	alpha := createTransactionPlan(t, store, transactionPlan(t, "01a02101-0000-7001-8000-000000000001", "Alpha", now))
	zulu := createTransactionPlan(t, store, transactionPlan(t, "01a02101-0000-7002-8000-000000000002", "Zulu", now))
	secondPath = zulu.Path
	alphaReplacement := replacementPlan(alpha, "Alpha recovered", now.Add(time.Minute))
	zuluReplacement := replacementPlan(zulu, "Zulu recovered", now.Add(time.Minute))

	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.recovery",
		Changes: []record.TransactionChange{
			{Operation: record.TransactionReplace, Document: zuluReplacement, ExpectedRevision: zulu.Revision},
			{Operation: record.TransactionReplace, Document: alphaReplacement, ExpectedRevision: alpha.Revision},
		},
	})
	if !errors.Is(err, crash) || result == nil {
		t.Fatalf("interrupted Transact = %#v, %v", result, err)
	}
	if _, err := store.Inventory(context.Background()); !errors.Is(err, record.ErrTransactionRecoveryRequired) {
		t.Fatalf("split inventory was presented as committed: %v", err)
	}
	transactionRoot := filepath.Join(store.CoordinationDir(), "transactions", result.TransactionID)
	for _, temporary := range []string{
		filepath.Join(transactionRoot, ".exp-0123456789abcdef0123456789abcdef.tmp"),
		filepath.Join(transactionRoot, "staged", ".exp-fedcba9876543210fedcba9876543210.tmp"),
	} {
		if err := os.WriteFile(temporary, []byte("abandoned writer temporary\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	restarted := record.NewStore(info.Root, info.Repository.GitCommonDir)
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	inventory, err := restarted.Inventory(context.Background())
	if err != nil || !inventory.Valid() {
		t.Fatalf("recovered Inventory = %#v, %v", inventory, err)
	}
	for id, title := range map[research.ID]string{
		alpha.Record.(*research.Plan).ID: "Alpha recovered",
		zulu.Record.(*research.Plan).ID:  "Zulu recovered",
	} {
		document, readErr := inventory.ByID(id)
		if readErr != nil || document.Record.(*research.Plan).Title != title {
			t.Fatalf("recovered %s = %#v, %v", id, document, readErr)
		}
	}
	journal, err := os.ReadFile(filepath.Join(restarted.CoordinationDir(), "transactions", result.TransactionID, "journal.toml"))
	if err != nil || !strings.Contains(string(journal), `phase = "committed"`) {
		t.Fatalf("recovery did not commit journal: %q, %v", journal, err)
	}
}

func TestOrdinaryMutationRecoversPreparedTransactionBeforeReadingCandidateState(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 9, 30, 0, 0, time.UTC)
	injected := errors.New("leave prepared journal")
	preparing := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02201-0000-7003-8000-000000000003", func(stage record.TransactionStage, _, _ string) error {
		if stage == record.StageTransactionCanonicalCreate {
			return injected
		}
		return nil
	})
	preparedPlan := transactionPlan(t, "01a02101-0000-7003-8000-000000000003", "Prepared first", now)
	result, err := preparing.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.auto-recover",
		Changes:   []record.TransactionChange{{Operation: record.TransactionCreate, Document: preparedPlan}},
	})
	if !errors.Is(err, injected) || result == nil {
		t.Fatalf("prepare transaction = %#v, %v", result, err)
	}

	ordinary := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return now.Add(time.Minute) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a02101-0000-7004-8000-000000000004"), nil
		}),
	)
	created, err := ordinary.CreatePlan(context.Background(), linkedPlanInput("Ordinary after recovery"))
	if err != nil || created == nil {
		t.Fatalf("ordinary mutation after prepared journal = %#v, %v", created, err)
	}
	inventory, err := ordinary.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inventory.ByID(preparedPlan.Record.(*research.Plan).ID); err != nil {
		t.Fatalf("ordinary mutation did not recover prepared transaction: %v", err)
	}
}

func TestPreparedTransactionRecoveryPreservesUnrelatedEdit(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	crash := errors.New("stop after prepare")
	hook := func(stage record.TransactionStage, _, _ string) error {
		if stage == record.StageTransactionCanonicalCAS {
			return crash
		}
		return nil
	}
	store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02202-0000-7001-8000-000000000001", hook)
	current := createTransactionPlan(t, store, transactionPlan(t, "01a02102-0000-7001-8000-000000000001", "Current", now))
	replacement := replacementPlan(current, "Transaction replacement", now.Add(time.Minute))
	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.conflict",
		Changes:   []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: current.Revision}},
	})
	if !errors.Is(err, crash) || result == nil {
		t.Fatalf("prepared conflict transaction = %#v, %v", result, err)
	}
	external := replacementPlan(current, "Unrelated external edit", now.Add(2*time.Minute))
	external.Path = current.Path
	externalBytes, err := record.Encode(external)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(info.Root, filepath.FromSlash(current.Path))
	if err := os.WriteFile(target, externalBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	restarted := record.NewStore(info.Root, info.Repository.GitCommonDir)
	err = restarted.Recover(context.Background())
	var conflict *record.TransactionConflictError
	if !errors.Is(err, record.ErrTransactionConflict) || !errors.As(err, &conflict) || conflict.Path != current.Path {
		t.Fatalf("Recover conflict = %#v, %v", conflict, err)
	}
	actual, readErr := os.ReadFile(target)
	if readErr != nil || string(actual) != string(externalBytes) {
		t.Fatalf("unrelated edit was overwritten: %q, %v", actual, readErr)
	}
}

func TestPreparedTransactionCreateNeverClobbersRacingDestination(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 10, 30, 0, 0, time.UTC)
	candidate := transactionPlan(t, "01a02102-0000-7002-8000-000000000002", "Racing create", now)
	sentinel := []byte("uncoordinated creator bytes\n")
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return now }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a02202-0000-7002-8000-000000000002"), nil
		}),
		record.WithAtomicHook(func(stage record.AtomicStage, relative string) error {
			if stage != record.StageRename || !strings.HasSuffix(relative, "-racing-create.md") {
				return nil
			}
			return os.WriteFile(filepath.Join(info.Root, filepath.FromSlash(relative)), sentinel, 0o644)
		}),
	)
	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.create-race",
		Changes:   []record.TransactionChange{{Operation: record.TransactionCreate, Document: candidate}},
	})
	if err == nil || result == nil {
		t.Fatalf("racing create = %#v, %v", result, err)
	}
	target := filepath.Join(info.Root, record.PlansDir, candidate.Record.(*research.Plan).ID.String()+"-racing-create.md")
	actual, readErr := os.ReadFile(target)
	if readErr != nil || string(actual) != string(sentinel) {
		t.Fatalf("racing destination was clobbered: %q, %v", actual, readErr)
	}
	err = record.NewStore(info.Root, info.Repository.GitCommonDir).Recover(context.Background())
	if !errors.Is(err, record.ErrTransactionConflict) {
		t.Fatalf("racing create recovery = %v", err)
	}
}

func TestPreparedTransactionRejectsCorruptStagedBytesBeforeRecovery(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	crash := errors.New("stop after journal prepare")
	hook := func(stage record.TransactionStage, _, _ string) error {
		if stage == record.StageTransactionCanonicalCreate {
			return crash
		}
		return nil
	}
	store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02203-0000-7001-8000-000000000001", hook)
	candidate := transactionPlan(t, "01a02103-0000-7001-8000-000000000001", "Corrupt staged", now)
	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.corrupt",
		Changes:   []record.TransactionChange{{Operation: record.TransactionCreate, Document: candidate}},
	})
	if !errors.Is(err, crash) || result == nil {
		t.Fatalf("prepared corrupt transaction = %#v, %v", result, err)
	}
	staged := filepath.Join(store.CoordinationDir(), "transactions", result.TransactionID, "staged", "0000")
	if err := os.WriteFile(staged, []byte("tampered staged bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = record.NewStore(info.Root, info.Repository.GitCommonDir).Recover(context.Background())
	if !errors.Is(err, record.ErrUnsupportedTransaction) {
		t.Fatalf("corrupt staged recovery = %v", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(info.Root, record.PlansDir, "*corrupt-staged.md"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("corrupt staged bytes reached canonical state: %v, %v", matches, globErr)
	}
}

func TestPreparedTransactionRejectsUnknownJournalSchema(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 11, 30, 0, 0, time.UTC)
	crash := errors.New("stop after journal prepare")
	store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02203-0000-7002-8000-000000000002", func(stage record.TransactionStage, _, _ string) error {
		if stage == record.StageTransactionCanonicalCreate {
			return crash
		}
		return nil
	})
	candidate := transactionPlan(t, "01a02103-0000-7002-8000-000000000002", "Unknown schema", now)
	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.unknown-schema",
		Changes:   []record.TransactionChange{{Operation: record.TransactionCreate, Document: candidate}},
	})
	if !errors.Is(err, crash) || result == nil {
		t.Fatalf("prepared unknown-schema transaction = %#v, %v", result, err)
	}
	journalPath := filepath.Join(store.CoordinationDir(), "transactions", result.TransactionID, "journal.toml")
	journal, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	journal = []byte(strings.Replace(string(journal), `schema = "exp.transaction/v1"`, `schema = "exp.transaction/v2"`, 1))
	if err := os.WriteFile(journalPath, journal, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := record.NewStore(info.Root, info.Repository.GitCommonDir).Recover(context.Background()); !errors.Is(err, record.ErrUnsupportedTransaction) {
		t.Fatalf("unknown schema recovery = %v", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(info.Root, record.PlansDir, "*unknown-schema.md"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("unknown journal schema changed canonical state: %v, %v", matches, globErr)
	}
}

func TestPreparedTransactionChecksExpectedRevisionBeforeJournal(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02204-0000-7001-8000-000000000001", nil)
	current := createTransactionPlan(t, store, transactionPlan(t, "01a02104-0000-7001-8000-000000000001", "Current", now))
	replacement := replacementPlan(current, "Stale", now.Add(time.Minute))
	stale := "sha256:" + strings.Repeat("0", 64)
	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.stale",
		Changes:   []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: stale}},
	})
	if !errors.Is(err, record.ErrConflict) || result != nil {
		t.Fatalf("stale Transact = %#v, %v", result, err)
	}
	entries, err := os.ReadDir(filepath.Join(store.CoordinationDir(), "transactions"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("stale transaction wrote a journal: %v, %v", entries, err)
	}
}

func TestPreparedTransactionFailureInjectionBoundaries(t *testing.T) {
	preJournal := []record.TransactionStage{
		record.StageTransactionTempCreate,
		record.StageTransactionTempWrite,
		record.StageTransactionFileSync,
		record.StageTransactionJournalPublish,
	}
	for index, stage := range preJournal {
		t.Run(string(stage), func(t *testing.T) {
			info := initializeStoreProject(t)
			now := time.Date(2026, 8, 30, 13, index, 0, 0, time.UTC)
			injected := errors.New("injected pre-journal failure")
			store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02210-0000-7001-8000-000000000001", func(observed record.TransactionStage, _, _ string) error {
				if observed == stage {
					return injected
				}
				return nil
			})
			candidate := transactionPlan(t, "01a02110-0000-7001-8000-000000000001", "Boundary", now)
			result, err := store.Transact(context.Background(), record.TransactionRequest{
				Operation: "test.prepare-failure",
				Changes:   []record.TransactionChange{{Operation: record.TransactionCreate, Document: candidate}},
			})
			if !errors.Is(err, injected) || result != nil {
				t.Fatalf("Transact = %#v, %v", result, err)
			}
			matches, globErr := filepath.Glob(filepath.Join(info.Root, record.PlansDir, "*boundary.md"))
			if globErr != nil || len(matches) != 0 {
				t.Fatalf("pre-journal failure changed canonical state: %v, %v", matches, globErr)
			}
			// Before journal publication there is no durable transaction authority;
			// recovery may safely remove the UUID-scoped staged artifact.
			if err := record.NewStore(info.Root, info.Repository.GitCommonDir).Recover(context.Background()); err != nil {
				t.Fatalf("incomplete prepare cleanup failed: %v", err)
			}
		})
	}

	postJournal := []record.TransactionStage{
		record.StageTransactionJournalDirSync,
		record.StageTransactionCanonicalCreate,
		record.StageTransactionCanonicalSync,
		record.StageTransactionCommitMark,
	}
	for index, stage := range postJournal {
		t.Run(string(stage), func(t *testing.T) {
			info := initializeStoreProject(t)
			now := time.Date(2026, 8, 30, 14, index, 0, 0, time.UTC)
			injected := errors.New("injected recoverable failure")
			fired := false
			store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02211-0000-7001-8000-000000000001", func(observed record.TransactionStage, _, _ string) error {
				if !fired && observed == stage {
					fired = true
					return injected
				}
				return nil
			})
			candidate := transactionPlan(t, "01a02111-0000-7001-8000-000000000001", "Recoverable", now)
			result, err := store.Transact(context.Background(), record.TransactionRequest{
				Operation: "test.publish-failure",
				Changes:   []record.TransactionChange{{Operation: record.TransactionCreate, Document: candidate}},
			})
			if !errors.Is(err, injected) || result == nil || !fired {
				t.Fatalf("Transact = %#v, %v fired=%v", result, err, fired)
			}
			restarted := record.NewStore(info.Root, info.Repository.GitCommonDir)
			if err := restarted.Recover(context.Background()); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			inventory, err := restarted.Inventory(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inventory.ByID(candidate.Record.(*research.Plan).ID); err != nil {
				t.Fatalf("recoverable create missing: %v", err)
			}
		})
	}
}

func TestPreparedTransactionRecoversInterruptedDelete(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	injected := errors.New("injected unlink interruption")
	store := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02212-0000-7001-8000-000000000001", func(stage record.TransactionStage, _, _ string) error {
		if stage == record.StageTransactionCanonicalDelete {
			return injected
		}
		return nil
	})
	current := createTransactionIdea(t, store, transactionIdea(t, "01a02112-0000-7001-8000-000000000001", "Delete me", now))
	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.delete-recovery",
		Changes:   []record.TransactionChange{{Operation: record.TransactionDelete, ID: current.Record.(*research.Idea).ID, ExpectedRevision: current.Revision}},
	})
	if !errors.Is(err, injected) || result == nil {
		t.Fatalf("interrupted delete = %#v, %v", result, err)
	}
	restarted := record.NewStore(info.Root, info.Repository.GitCommonDir)
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	inventory, err := restarted.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inventory.ByID(current.Record.(*research.Idea).ID); err == nil {
		t.Fatal("recovered delete left its canonical record")
	}
}

func TestPreparedTransactionUsesLinkedWorktreeCommonLock(t *testing.T) {
	mainInfo := initializeStoreProject(t)
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.name", "Exp Test")
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.email", "exp-test@example.invalid")
	runGitCommand(t, mainInfo.Repository.Root, "add", "experiments")
	runGitCommand(t, mainInfo.Repository.Root, "commit", "--quiet", "-m", "initialize transactions")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runGitCommand(t, mainInfo.Repository.Root, "worktree", "add", "--quiet", "-b", "transaction-lock-test", linkedRoot)
	linkedInfo, err := project.Discover(context.Background(), linkedRoot)
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	now := time.Date(2026, 8, 30, 16, 0, 0, 0, time.UTC)
	mainStore := transactionStore(t, mainInfo.Root, mainInfo.Repository.GitCommonDir, now, "01a02213-0000-7001-8000-000000000001", func(stage record.TransactionStage, _, _ string) error {
		if stage == record.StageTransactionTempWrite {
			once.Do(func() { close(entered) })
			<-release
		}
		return nil
	})
	linkedStore := record.NewStore(linkedInfo.Root, linkedInfo.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return now.Add(time.Second) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a02113-0000-7002-8000-000000000002"), nil
		}),
	)

	type transactionOutcome struct {
		result *record.TransactionResult
		err    error
	}
	mainCandidate := transactionPlan(t, "01a02113-0000-7001-8000-000000000001", "Main transaction", now)
	mainDone := make(chan transactionOutcome, 1)
	go func() {
		result, txErr := mainStore.Transact(context.Background(), record.TransactionRequest{
			Operation: "test.common-lock",
			Changes: []record.TransactionChange{{
				Operation: record.TransactionCreate,
				Document:  mainCandidate,
			}},
		})
		mainDone <- transactionOutcome{result: result, err: txErr}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("transaction did not enter staged write")
	}

	type createOutcome struct {
		document *record.Document
		err      error
	}
	linkedDone := make(chan createOutcome, 1)
	go func() {
		document, createErr := linkedStore.CreatePlan(context.Background(), linkedPlanInput("Linked worktree contender"))
		linkedDone <- createOutcome{document: document, err: createErr}
	}()
	select {
	case outcome := <-linkedDone:
		t.Fatalf("linked mutation bypassed transaction lock: %#v", outcome)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	mainOutcome := <-mainDone
	linkedOutcome := <-linkedDone
	if mainOutcome.err != nil || mainOutcome.result == nil || linkedOutcome.err != nil || linkedOutcome.document == nil {
		t.Fatalf("serialized outcomes: main=%#v linked=%#v", mainOutcome, linkedOutcome)
	}
}

func TestPreparedTransactionsSupportPolicySingletonAndLaterReplacement(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 30, 17, 0, 0, 0, time.UTC)
	policy := transactionPolicy(now)
	firstStore := transactionStore(t, info.Root, info.Repository.GitCommonDir, now, "01a02214-0000-7001-8000-000000000001", nil)
	if _, err := firstStore.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.policy-create",
		Changes:   []record.TransactionChange{{Operation: record.TransactionCreate, Document: policy}},
	}); err != nil {
		t.Fatalf("create Policy transaction: %v", err)
	}
	inventory, err := firstStore.Inventory(context.Background())
	if err != nil || inventory.Policy == nil {
		t.Fatalf("created Policy inventory = %#v, %v", inventory, err)
	}

	replacement := inventory.Policy.Clone()
	replacement.Record.(*research.Policy).UpdatedAt = now.Add(time.Minute)
	replacement.Record.(*research.Policy).Autonomy = research.AutonomyShadow
	secondStore := transactionStore(t, info.Root, info.Repository.GitCommonDir, now.Add(time.Minute), "01a02214-0000-7002-8000-000000000002", nil)
	if _, err := secondStore.Transact(context.Background(), record.TransactionRequest{
		Operation: "test.policy-replace",
		Changes: []record.TransactionChange{{
			Operation:        record.TransactionReplace,
			Document:         replacement,
			ExpectedRevision: inventory.Policy.Revision,
		}},
	}); err != nil {
		t.Fatalf("replace Policy transaction: %v", err)
	}
	inventory, err = secondStore.Inventory(context.Background())
	if err != nil || inventory.Policy == nil || inventory.Policy.Record.(*research.Policy).Autonomy != research.AutonomyShadow {
		t.Fatalf("replaced Policy inventory = %#v, %v", inventory, err)
	}
}

func transactionStore(t *testing.T, root, common string, now time.Time, transactionID string, hook record.TransactionHook) *record.Store {
	t.Helper()
	value := uuid.MustParse(transactionID)
	return record.NewStore(root, common,
		record.WithClock(func() time.Time { return now }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return value, nil }),
		record.WithTransactionHook(hook),
	)
}

func transactionPlan(t *testing.T, rawID, title string, now time.Time) *record.Document {
	t.Helper()
	id, err := research.NewID(research.KindPlan, uuid.MustParse(rawID))
	if err != nil {
		t.Fatal(err)
	}
	return &record.Document{
		Record: &research.Plan{
			Common:   research.Common{Schema: research.SchemaPlan, ID: id, Title: title, CreatedAt: now, UpdatedAt: now},
			Priority: research.PriorityP1,
			Effort:   research.EffortS,
			State:    research.PlanQueued,
			ExpectedPayoff: research.ExpectedPayoff{
				Summary: "Exercise prepared transactions",
				Metric:  "score",
				Unit:    "score",
			},
		},
		Body: "transaction test body\n",
	}
}

func transactionIdea(t *testing.T, rawID, title string, now time.Time) *record.Document {
	t.Helper()
	id, err := research.NewID(research.KindIdea, uuid.MustParse(rawID))
	if err != nil {
		t.Fatal(err)
	}
	return &record.Document{
		Record: &research.Idea{
			Common:         research.Common{Schema: research.SchemaIdea, ID: id, Title: title, CreatedAt: now, UpdatedAt: now},
			State:          research.IdeaProposed,
			Summary:        "Exercise prepared transaction deletion",
			ProposedBy:     "human:test",
			PrimaryCluster: "general",
			Classification: research.Classification{
				Domain:    "general",
				Work:      "research",
				Method:    "experiment",
				Component: "system",
				Lane:      research.LaneExplore,
				Risk:      research.RiskLow,
				Horizon:   research.HorizonShort,
				Origin:    research.OriginHuman,
			},
		},
		Body: "transaction test idea\n",
	}
}

func createTransactionPlan(t *testing.T, store *record.Store, document *record.Document) *record.Document {
	t.Helper()
	created, err := store.Create(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func createTransactionIdea(t *testing.T, store *record.Store, document *record.Document) *record.Document {
	t.Helper()
	created, err := store.Create(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func replacementPlan(current *record.Document, title string, updatedAt time.Time) *record.Document {
	replacement := current.Clone()
	replacement.Record.(*research.Plan).Title = title
	replacement.Record.(*research.Plan).UpdatedAt = updatedAt
	replacement.Body = title + "\n"
	return replacement
}

func transactionPolicy(now time.Time) *record.Document {
	return &record.Document{
		Record: &research.Policy{
			Schema:                 research.SchemaPolicy,
			CreatedAt:              now,
			UpdatedAt:              now,
			Autonomy:               research.AutonomyManual,
			ExploitShare:           0.8,
			ExploreShare:           0.2,
			ScoreFormula:           "utility-v1",
			TiePolicy:              research.QueueTieKeepIncumbent,
			PromotionRequiresHuman: true,
			Taxonomy: research.ClassificationTaxonomy{
				Domains:    []string{"general"},
				Work:       []string{"research"},
				Methods:    []string{"experiment"},
				Components: []string{"system"},
			},
			ClusterSaturation: research.ClusterSaturationPolicy{
				BudgetHours:        24,
				PlateauWindow:      3,
				MinimumImprovement: 0.01,
				MinimumProbability: 0.1,
			},
		},
		Body: "research control policy\n",
	}
}
