package tryflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestTryLifecycleTransactionsAndImmutability(t *testing.T) {
	fixture := newTryFixture(t, nil)
	created, err := fixture.service.Create(context.Background(), CreateRequest{
		Title: "Quick encoder check", Body: "# Quick encoder check\n", Goal: "Determine whether the direction merits formal study.",
		Sources: []RevisionRef{fixture.sourceRef()}, Tags: []string{"encoder"},
	})
	if err != nil || created == nil {
		t.Fatalf("Create = %#v, %v", created, err)
	}
	if created.Try.Path != "t-"+created.Try.Record.(*research.Try).ID.UUIDHex()+"-quick-encoder-check/TRY.md" {
		t.Fatalf("Try path = %s", created.Try.Path)
	}

	fixture.advance()
	if result, err := fixture.service.Conclude(context.Background(), ConcludeRequest{Try: ref(created.Try)}); !errors.Is(err, ErrPrecondition) || result != nil {
		t.Fatalf("empty conclusion = %#v, %v", result, err)
	}
	assertTryState(t, fixture.inventory(t), created.Try, research.TryOpen)

	concluded, err := fixture.service.Conclude(context.Background(), ConcludeRequest{
		Try: ref(created.Try), Summary: "The result merits a formal experiment.",
	})
	if err != nil || concluded == nil {
		t.Fatalf("Conclude = %#v, %v", concluded, err)
	}
	if got := concluded.Try.Record.(*research.Try); got.State != research.TryConcluded || got.Conclusion == nil {
		t.Fatalf("concluded Try = %#v", got)
	}

	fixture.advance()
	if result, err := fixture.service.Adopt(context.Background(), AdoptRequest{
		Try: ref(concluded.Try), Title: "Formalize encoder", Summary: "Create a controlled study.", ProposedBy: "agent:auto",
		PrimaryCluster: "encoder", Classification: testClassification(),
	}); !errors.Is(err, ErrPrecondition) || result != nil {
		t.Fatalf("agent adoption = %#v, %v", result, err)
	}
	if ideas := fixture.inventory(t).OfKind(research.KindIdea); len(ideas) != 0 {
		t.Fatalf("invalid adoption created Ideas: %#v", ideas)
	}

	adopted, err := fixture.service.Adopt(context.Background(), AdoptRequest{
		Try: ref(concluded.Try), Title: "Formalize encoder", Body: "# Formalize encoder\n",
		Summary: "Create a controlled study.", ProposedBy: "human:david",
		PrimaryCluster: "encoder", Classification: testClassification(),
	})
	if err != nil || adopted == nil {
		t.Fatalf("Adopt = %#v, %v", adopted, err)
	}
	tryRecord := adopted.Try.Record.(*research.Try)
	ideaRecord := adopted.Idea.Record.(*research.Idea)
	if tryRecord.State != research.TryAdopted || tryRecord.AdoptedIdea != ideaRecord.ID || ideaRecord.Schema != research.SchemaIdeaV2 || ideaRecord.OriginTry != tryRecord.ID {
		t.Fatalf("adoption links = Try %#v Idea %#v", tryRecord, ideaRecord)
	}
	inventory := fixture.inventory(t)
	if !inventory.Valid() {
		t.Fatalf("adopted inventory = %v", inventory.Diagnostics)
	}

	rewritten := adopted.Try.Clone()
	rewritten.Record.(*research.Try).Goal = "Rewrite history."
	rewritten.Record.(*research.Try).UpdatedAt = fixture.now.Add(time.Minute)
	if _, err := fixture.store.Update(context.Background(), rewritten, adopted.Try.Revision); err == nil {
		t.Fatal("published Try goal was mutable")
	}
	if _, err := fixture.store.Transact(context.Background(), record.TransactionRequest{
		Operation: "try.delete",
		Changes:   []record.TransactionChange{{Operation: record.TransactionDelete, ID: tryRecord.ID, ExpectedRevision: adopted.Try.Revision}},
	}); !errors.Is(err, record.ErrInvalidTransaction) {
		t.Fatalf("published Try deletion error = %v", err)
	}
}

func TestTryAbandonIsTerminal(t *testing.T) {
	fixture := newTryFixture(t, nil)
	created, err := fixture.service.Create(context.Background(), CreateRequest{
		Title: "Disposable check", Goal: "Decide quickly.", Sources: []RevisionRef{fixture.sourceRef()},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.advance()
	abandoned, err := fixture.service.Abandon(context.Background(), AbandonRequest{Try: ref(created.Try), Reason: "The premise was invalid."})
	if err != nil || abandoned.Try.Record.(*research.Try).State != research.TryAbandoned {
		t.Fatalf("Abandon = %#v, %v", abandoned, err)
	}
	fixture.advance()
	if result, err := fixture.service.Conclude(context.Background(), ConcludeRequest{Try: ref(abandoned.Try), Summary: "Too late."}); !errors.Is(err, ErrPrecondition) || result != nil {
		t.Fatalf("conclude abandoned Try = %#v, %v", result, err)
	}
}

func TestTryClosureRequiresTerminalAttemptsAndOwnedResultDigests(t *testing.T) {
	fixture := newTryFixture(t, nil)
	created, err := fixture.service.Create(context.Background(), CreateRequest{
		Title: "Recoverable execution", Goal: "Exercise closure fencing.", Sources: []RevisionRef{fixture.sourceRef()},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.advance()
	attemptID := fixture.nextID(t, research.KindAttempt)
	snapshot := research.SourceSnapshot{
		Source: fixture.sourceID, Subdir: ".", PolicyVersion: "capture-v1", CapturedAt: fixture.now,
		GitObjectFormat: research.GitObjectSHA1, BaseCommit: strings.Repeat("a", 40), HeadCommit: strings.Repeat("a", 40),
		ChangeSet: []string{}, State: research.SourceSnapshotClean, Reproducibility: research.ReproducibilityExact,
	}
	snapshot.Digest, err = research.SourceSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := fixture.store.Create(context.Background(), &record.Document{Record: &research.Attempt{
		Common: research.Common{Schema: research.SchemaAttemptV3, ID: attemptID, Title: "Pending attempt", CreatedAt: fixture.now, UpdatedAt: fixture.now},
		Try:    created.Try.Record.(*research.Try).ID, State: research.AttemptPlanned, Runner: "direct", Scheduler: "direct",
		CWD: ".", Argv: []string{"true"}, ExecutionSource: fixture.sourceID, SourceSnapshots: []research.SourceSnapshot{snapshot},
	}, Body: "# Pending attempt\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result, closeErr := fixture.service.Conclude(context.Background(), ConcludeRequest{Try: ref(created.Try), Summary: "Too early."}); !errors.Is(closeErr, ErrPrecondition) || result != nil {
		t.Fatalf("conclude with planned Attempt = %#v, %v", result, closeErr)
	}
	if result, abandonErr := fixture.service.Abandon(context.Background(), AbandonRequest{Try: ref(created.Try), Reason: "Too early."}); !errors.Is(abandonErr, ErrPrecondition) || result != nil {
		t.Fatalf("abandon with planned Attempt = %#v, %v", result, abandonErr)
	}

	fixture.advance()
	digest := "sha256:" + strings.Repeat("b", 64)
	replacement := attempt.Clone()
	terminal := replacement.Record.(*research.Attempt)
	terminal.State = research.AttemptSucceeded
	terminal.UpdatedAt = fixture.now
	exitCode := 0
	started := fixture.now.Add(-time.Second)
	terminal.Terminal = &research.Terminal{Source: "direct", ObservedAt: fixture.now, StartedAt: &started, EndedAt: fixture.now, ExitCode: &exitCode}
	terminal.Extensions = research.Extensions{DirectExtensionNamespace: map[string]any{"result_digests": []string{digest}}}
	attempt, err = fixture.store.Update(context.Background(), replacement, attempt.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_ = attempt
	if result, closeErr := fixture.service.Conclude(context.Background(), ConcludeRequest{
		Try: ref(created.Try), Summary: "Fabricated result.", ResultDigests: []string{"sha256:" + strings.Repeat("c", 64)},
	}); !errors.Is(closeErr, ErrPrecondition) || result != nil {
		t.Fatalf("conclude with foreign digest = %#v, %v", result, closeErr)
	}
	concluded, err := fixture.service.Conclude(context.Background(), ConcludeRequest{Try: ref(created.Try), Summary: "Verified result.", ResultDigests: []string{digest}})
	if err != nil || concluded == nil || concluded.Try.Record.(*research.Try).State != research.TryConcluded {
		t.Fatalf("conclude terminal Try = %#v, %v", concluded, err)
	}
}

func TestTryAdoptionRecoversAtomicallyAfterPartialPublication(t *testing.T) {
	injected := errors.New("stop before Try CAS")
	fail := false
	hook := func(stage record.TransactionStage, _, _ string) error {
		if fail && stage == record.StageTransactionCanonicalCAS {
			return injected
		}
		return nil
	}
	fixture := newTryFixture(t, hook)
	created, err := fixture.service.Create(context.Background(), CreateRequest{
		Title: "Recoverable check", Goal: "Test recovery.", Sources: []RevisionRef{fixture.sourceRef()},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.advance()
	concluded, err := fixture.service.Conclude(context.Background(), ConcludeRequest{Try: ref(created.Try), Summary: "Ready to adopt."})
	if err != nil {
		t.Fatal(err)
	}
	fixture.advance()
	fail = true
	result, err := fixture.service.Adopt(context.Background(), AdoptRequest{
		Try: ref(concluded.Try), Title: "Recovered idea", Summary: "Continue formally.", ProposedBy: "human:david",
		PrimaryCluster: "encoder", Classification: testClassification(),
	})
	transaction, carried := TransactionResultFromError(err)
	if !errors.Is(err, injected) || result == nil || result.TransactionID == "" || !carried || transaction.TransactionID != result.TransactionID || !transaction.RecoveryRequired {
		t.Fatalf("partially published adoption = %#v, transaction=%#v carried=%v err=%v", result, transaction, carried, err)
	}
	if _, err := fixture.store.Inventory(context.Background()); !errors.Is(err, record.ErrTransactionRecoveryRequired) {
		t.Fatalf("prepared adoption did not require recovery: %v", err)
	}
	fail = false
	if err := fixture.store.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	inventory := fixture.inventory(t)
	if !inventory.Valid() || len(inventory.OfKind(research.KindIdea)) != 1 {
		t.Fatalf("recovered adoption inventory = %v", inventory.Diagnostics)
	}
	tryDocument := inventory.OfKind(research.KindTry)[0]
	ideaDocument := inventory.OfKind(research.KindIdea)[0]
	tryRecord := tryDocument.Record.(*research.Try)
	ideaRecord := ideaDocument.Record.(*research.Idea)
	if tryRecord.State != research.TryAdopted || tryRecord.AdoptedIdea != ideaRecord.ID || ideaRecord.OriginTry != tryRecord.ID {
		t.Fatalf("recovered adoption links = %#v %#v", tryRecord, ideaRecord)
	}
}

func TestDirectRuntimeDefaultsToShortRecoverableLease(t *testing.T) {
	direct := NewDirect(Dependencies{})
	if direct.dependencies.ClaimTTL != 2*time.Minute {
		t.Fatalf("default direct claim TTL = %s", direct.dependencies.ClaimTTL)
	}
}

func TestAttemptStatusDistinguishesMissingLiveAndExpiredJobs(t *testing.T) {
	now := time.Date(2026, 9, 4, 3, 30, 0, 0, time.UTC)
	missing := AttemptStatus{}
	classifyAttemptStatus(now, research.AttemptQueued, &missing)
	if !missing.Uncertain || missing.Active || missing.Status != "operational job missing" {
		t.Fatalf("missing queued job status = %#v", missing)
	}
	future := now.Add(time.Minute)
	live := AttemptStatus{Job: &operation.Job{State: operation.JobRunning, LeaseExpiresAt: &future}}
	classifyAttemptStatus(now, research.AttemptRunning, &live)
	if !live.Active || live.Uncertain || live.LeaseExpired {
		t.Fatalf("live running job status = %#v", live)
	}
	past := now.Add(-time.Minute)
	expired := AttemptStatus{Job: &operation.Job{State: operation.JobRunning, LeaseExpiresAt: &past}}
	classifyAttemptStatus(now, research.AttemptRunning, &expired)
	if expired.Active || !expired.Uncertain || !expired.LeaseExpired {
		t.Fatalf("expired running job status = %#v", expired)
	}
}

func TestTryTransactionsSerializeAcrossLinkedWorktrees(t *testing.T) {
	fixture := newTryFixture(t, nil)
	runGit(t, fixture.repository, "config", "user.name", "Exp Test")
	runGit(t, fixture.repository, "config", "user.email", "exp-test@example.invalid")
	runGit(t, fixture.repository, "add", "experiments")
	runGit(t, fixture.repository, "commit", "--quiet", "-m", "initialize Try source")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runGit(t, fixture.repository, "worktree", "add", "--quiet", "-b", "try-linked", linkedRoot)
	linkedInfo, err := project.Discover(context.Background(), linkedRoot)
	if err != nil {
		t.Fatal(err)
	}
	linkedInventory, err := record.NewStore(linkedInfo.Root, linkedInfo.Repository.GitCommonDir).Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	linkedSource := linkedInventory.OfKind(research.KindSource)[0]

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	mainStore := record.NewStore(fixture.info.Root, fixture.info.Repository.GitCommonDir,
		record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
			if stage == record.StageTransactionCanonicalCreate {
				once.Do(func() { close(entered) })
				<-release
			}
			return nil
		}),
	)
	mainService := New(mainStore, WithClock(func() time.Time { return fixture.now }), WithUUIDGenerator(fixedGenerator("01a09000-0000-7601-8000-000000000601")))
	linkedService := New(record.NewStore(linkedInfo.Root, linkedInfo.Repository.GitCommonDir), WithClock(func() time.Time { return fixture.now }), WithUUIDGenerator(fixedGenerator("01a09000-0000-7602-8000-000000000602")))

	type outcome struct {
		result *CreateResult
		err    error
	}
	mainResult := make(chan outcome, 1)
	go func() {
		result, err := mainService.Create(context.Background(), CreateRequest{
			Title: "Main Try", Goal: "Serialize main.", Sources: []RevisionRef{{ID: fixture.sourceID, Revision: fixture.source.Revision}},
		})
		mainResult <- outcome{result: result, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("main Try transaction did not reach publication hook")
	}
	linkedResult := make(chan outcome, 1)
	go func() {
		id, _ := linkedSource.ID()
		result, err := linkedService.Create(context.Background(), CreateRequest{
			Title: "Linked Try", Goal: "Serialize linked.", Sources: []RevisionRef{{ID: id, Revision: linkedSource.Revision}},
		})
		linkedResult <- outcome{result: result, err: err}
	}()
	select {
	case result := <-linkedResult:
		t.Fatalf("linked Try transaction bypassed common lock: %#v", result)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	first, second := <-mainResult, <-linkedResult
	if first.err != nil || second.err != nil || first.result == nil || second.result == nil {
		t.Fatalf("linked Try transactions failed: main=%#v linked=%#v", first, second)
	}
}

type tryFixture struct {
	repository string
	info       *project.Info
	store      *record.Store
	service    *Service
	now        time.Time
	entropy    byte
	source     *record.Document
	sourceID   research.ID
}

func newTryFixture(t *testing.T, hook record.TransactionHook) *tryFixture {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "--quiet")
	initial := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	info, _, err := project.Initialize(context.Background(), project.InitRequest{StartDir: repository, Name: "Try Test"},
		project.WithClock(func() time.Time { return initial }),
		project.WithUUIDGenerator(fixedGenerator("01a09000-0000-7000-8000-000000000000")),
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &tryFixture{repository: repository, info: info, now: initial.Add(time.Minute)}
	options := []record.StoreOption{record.WithClock(func() time.Time { return fixture.now })}
	if hook != nil {
		options = append(options, record.WithTransactionHook(hook))
	}
	fixture.store = record.NewStore(info.Root, info.Repository.GitCommonDir, options...)
	fixture.service = New(fixture.store, WithClock(func() time.Time { return fixture.now }), WithUUIDGenerator(fixture.generate))
	fixture.sourceID = fixture.nextID(t, research.KindSource)
	fixture.source, err = fixture.store.Create(context.Background(), &record.Document{Record: &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: fixture.sourceID, Title: "Production source", CreatedAt: fixture.now, UpdatedAt: fixture.now},
		Key:    "production", Kind: research.SourceGit, Subdir: ".", LocatorHints: []string{"https://github.com/example/production.git"}, State: research.SourceActive,
	}, Body: "# Production source\n"})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *tryFixture) generate(at time.Time) (uuid.UUID, error) {
	fixture.entropy++
	return research.NewUUIDv7(at, bytes.NewReader(bytes.Repeat([]byte{fixture.entropy}, 10)))
}

func (fixture *tryFixture) nextID(t *testing.T, kind research.Kind) research.ID {
	t.Helper()
	value, err := fixture.generate(fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := research.NewID(kind, value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (fixture *tryFixture) sourceRef() RevisionRef {
	return RevisionRef{ID: fixture.sourceID, Revision: fixture.source.Revision}
}
func (fixture *tryFixture) advance() { fixture.now = fixture.now.Add(time.Minute) }
func (fixture *tryFixture) inventory(t *testing.T) *record.Inventory {
	t.Helper()
	inventory, err := fixture.store.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return inventory
}

func ref(document *record.Document) RevisionRef {
	id, _ := document.ID()
	return RevisionRef{ID: id, Revision: document.Revision}
}

func assertTryState(t *testing.T, inventory *record.Inventory, original *record.Document, want research.TryState) {
	t.Helper()
	id, _ := original.ID()
	document, err := inventory.ByID(id)
	if err != nil || document.Record.(*research.Try).State != want {
		t.Fatalf("Try state = %#v, %v; want %s", document, err, want)
	}
}

func testClassification() research.Classification {
	return research.Classification{
		Domain: "ml", Work: "training", Method: "ablation", Component: "encoder",
		Lane: research.LaneExplore, Risk: research.RiskLow, Horizon: research.HorizonShort, Origin: research.OriginHuman,
	}
}

func fixedGenerator(value string) research.UUIDGenerator {
	parsed := uuid.MustParse(value)
	return func(time.Time) (uuid.UUID, error) { return parsed, nil }
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
