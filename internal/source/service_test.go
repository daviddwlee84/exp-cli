package source

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestServiceAddLookupAppendAndRetire(t *testing.T) {
	info := initializeSourceProject(t)
	if err := os.Remove(filepath.Join(info.Root, record.SourcesDir)); err != nil {
		t.Fatal(err)
	}
	initial := time.Date(2026, 9, 3, 13, 0, 0, 0, time.UTC)
	now := initial
	generator, calls := sourceUUIDSequence(
		uuid.MustParse("01a03300-0000-7001-8000-000000000001"),
		uuid.MustParse("01a03300-0000-7002-8000-000000000002"),
	)
	store := record.NewStore(info.Root, info.Repository.GitCommonDir)
	service := New(store, WithClock(func() time.Time { return now }), WithUUIDGenerator(generator))

	created, err := service.Add(context.Background(), AddRequest{
		Key:          " Production_Workspace ",
		Title:        "Production workspace",
		LocatorHints: []string{"HTTPS://GitHub.COM:443/Example/Repo.git/"},
		Tags:         []string{"workspace", "production"},
		Body:         "# Production source\n",
	})
	if err != nil {
		t.Fatalf("Add Source: %v", err)
	}
	createdSource := created.Record.(*research.Source)
	if createdSource.Key != "production-workspace" || createdSource.Subdir != "." || createdSource.Kind != research.SourceGit || createdSource.State != research.SourceActive || createdSource.RetiredAt != nil {
		t.Fatalf("created Source = %#v", createdSource)
	}
	if len(createdSource.LocatorHints) != 1 || createdSource.LocatorHints[0] != "https://github.com/Example/Repo.git" {
		t.Fatalf("created Source locators = %#v", createdSource.LocatorHints)
	}
	if created.Path != "sources/src_01a03300-0000-7001-8000-000000000001-production-workspace.md" || !record.ValidRevision(created.Revision) {
		t.Fatalf("created Source path/revision = %q, %q", created.Path, created.Revision)
	}
	persisted, err := os.ReadFile(filepath.Join(info.Root, filepath.FromSlash(created.Path)))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CANARY", "access_token", "#main", "?"} {
		if strings.Contains(string(persisted), forbidden) {
			t.Errorf("persisted Source contains sanitized component %q:\n%s", forbidden, persisted)
		}
	}

	display, err := research.DisplayCode(createdSource.ID, []research.ReferenceCandidate{{ID: createdSource.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"production-workspace", " Production Workspace ", createdSource.ID.String(), display} {
		lookedUp, err := service.Lookup(context.Background(), reference)
		if err != nil || lookedUp.Revision != created.Revision {
			t.Fatalf("Lookup(%q) = %#v, %v", reference, lookedUp, err)
		}
	}
	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Path != created.Path {
		t.Fatalf("List = %#v, %v", listed, err)
	}

	if _, err := service.AppendLocator(context.Background(), AppendLocatorRequest{Reference: createdSource.ID.String(), Locator: "https://mirror.example/repo.git"}); !errors.Is(err, ErrRevisionRequired) {
		t.Fatalf("append without revision = %v", err)
	}
	stale := "sha256:" + strings.Repeat("0", 64)
	if _, err := service.AppendLocator(context.Background(), AppendLocatorRequest{Reference: createdSource.ID.String(), ExpectedRevision: stale, Locator: "https://mirror.example/repo.git"}); !errors.Is(err, record.ErrConflict) {
		t.Fatalf("append with stale revision = %v", err)
	}

	now = initial.Add(time.Minute)
	appended, err := service.AppendLocator(context.Background(), AppendLocatorRequest{
		Reference: createdSource.ID.String(), ExpectedRevision: created.Revision,
		Locator: "ssh://git@Mirror.Example:22/Example/Repo.git",
	})
	if err != nil {
		t.Fatalf("AppendLocator: %v", err)
	}
	appendedSource := appended.Record.(*research.Source)
	if appended.Revision == created.Revision || len(appendedSource.LocatorHints) != 2 || appendedSource.LocatorHints[1] != "ssh://git@mirror.example/Example/Repo.git" {
		t.Fatalf("appended Source = %#v", appended)
	}
	if _, err := service.AppendLocator(context.Background(), AppendLocatorRequest{
		Reference: appendedSource.Key, ExpectedRevision: appended.Revision,
		Locator: "git@MIRROR.EXAMPLE:/Example/Repo.git",
	}); !errors.Is(err, ErrLocatorExists) {
		t.Fatalf("duplicate normalized locator append = %v", err)
	}
	if _, err := service.Retire(context.Background(), RetireRequest{Reference: appendedSource.Key, ExpectedRevision: created.Revision}); !errors.Is(err, record.ErrConflict) {
		t.Fatalf("retire with stale revision = %v", err)
	}

	now = initial.Add(2 * time.Minute)
	retired, err := service.Retire(context.Background(), RetireRequest{Reference: appendedSource.Key, ExpectedRevision: appended.Revision})
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	retiredSource := retired.Record.(*research.Source)
	if retiredSource.State != research.SourceRetired || retiredSource.RetiredAt == nil || !retiredSource.RetiredAt.Equal(now) || !retiredSource.UpdatedAt.Equal(now) {
		t.Fatalf("retired Source = %#v", retiredSource)
	}
	if _, err := service.Retire(context.Background(), RetireRequest{Reference: retiredSource.Key, ExpectedRevision: retired.Revision}); !errors.Is(err, ErrAlreadyRetired) {
		t.Fatalf("second Retire = %v", err)
	}
	if lookedUp, err := service.Lookup(context.Background(), retiredSource.Key); err != nil || lookedUp.Record.(*research.Source).State != research.SourceRetired {
		t.Fatalf("lookup retired Source = %#v, %v", lookedUp, err)
	}

	if _, err := service.Add(context.Background(), AddRequest{Key: "production workspace", Body: "duplicate\n"}); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("duplicate normalized key Add = %v", err)
	}
	if *calls != 1 {
		t.Fatalf("duplicate key consumed UUIDs; generator calls = %d", *calls)
	}
}

func TestServiceRejectsLocalAndDuplicateNormalizedLocatorHints(t *testing.T) {
	info := initializeSourceProject(t)
	service := New(record.NewStore(info.Root, info.Repository.GitCommonDir),
		WithClock(func() time.Time { return time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC) }),
		WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a03400-0000-7001-8000-000000000001"), nil
		}),
	)
	for name, locators := range map[string][]string{
		"local absolute": {"/Users/alice/repo"},
		"relative local": {"../repo"},
		"normalized duplicate": {
			"ssh://git@github.com:22/example/repo.git",
			"git@GITHUB.COM:/example/repo.git",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Add(context.Background(), AddRequest{Key: strings.ReplaceAll(name, " ", "-"), LocatorHints: locators})
			if err == nil {
				t.Fatal("invalid locator Add succeeded")
			}
		})
	}
	inventory, err := service.store.Inventory(context.Background())
	if err != nil || len(inventory.OfKind(research.KindSource)) != 0 {
		t.Fatalf("invalid locator Add published Sources: %#v, %v", inventory, err)
	}
}

func TestStoreEnforcesSourceUpdateAndDeleteHistory(t *testing.T) {
	info := initializeSourceProject(t)
	initial := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	now := initial
	store := record.NewStore(info.Root, info.Repository.GitCommonDir)
	service := New(store,
		WithClock(func() time.Time { return now }),
		WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a03500-0000-7001-8000-000000000001"), nil
		}),
	)
	current, err := service.Add(context.Background(), AddRequest{
		Key: "workspace", Title: "Workspace", Subdir: ".",
		LocatorHints: []string{"https://github.com/example/repo.git"}, Body: "# Source\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*record.Document)
	}{
		{"key", func(document *record.Document) { document.Record.(*research.Source).Key = "other" }},
		{"kind", func(document *record.Document) { document.Record.(*research.Source).Kind = "hg" }},
		{"subdir", func(document *record.Document) { document.Record.(*research.Source).Subdir = "services/api" }},
		{"title", func(document *record.Document) { document.Record.(*research.Source).Title = "Renamed" }},
		{"tags", func(document *record.Document) { document.Record.(*research.Source).Tags = []string{"new-tag"} }},
		{"body", func(document *record.Document) { document.Body = "rewritten\n" }},
		{"extensions", func(document *record.Document) {
			document.Record.(*research.Source).Extensions = research.Extensions{"org.example.source": {"value": true}}
		}},
		{"locator removal", func(document *record.Document) { document.Record.(*research.Source).LocatorHints = []string{} }},
		{"locator rewrite", func(document *record.Document) {
			document.Record.(*research.Source).LocatorHints[0] = "https://github.com/example/other.git"
		}},
		{"updated-only", func(document *record.Document) {
			document.Record.(*research.Source).UpdatedAt = initial.Add(time.Minute)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replacement := current.Clone()
			test.mutate(replacement)
			if _, err := store.Update(context.Background(), replacement, current.Revision); err == nil {
				t.Fatal("forbidden Source rewrite succeeded")
			}
		})
	}

	guarded, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "source.guard",
		Changes: []record.TransactionChange{{
			Operation: record.TransactionReplace, Document: current.Clone(), ExpectedRevision: current.Revision,
		}},
	})
	if err != nil || len(guarded.Documents) != 1 || guarded.Documents[0].Revision != current.Revision {
		t.Fatalf("exact no-op Source revision guard = %#v, %v", guarded, err)
	}

	directAppend := current.Clone()
	directSource := directAppend.Record.(*research.Source)
	directSource.LocatorHints = append(directSource.LocatorHints, "https://mirror.example/example/repo.git")
	directSource.UpdatedAt = initial.Add(time.Minute)
	current, err = store.Update(context.Background(), directAppend, current.Revision)
	if err != nil {
		t.Fatalf("direct append-only Source update: %v", err)
	}
	combined := current.Clone()
	combinedSource := combined.Record.(*research.Source)
	combinedSource.LocatorHints = append(combinedSource.LocatorHints, "https://backup.example/example/repo.git")
	combinedSource.State = research.SourceRetired
	combinedSource.UpdatedAt = initial.Add(2 * time.Minute)
	combinedSource.RetiredAt = sourceTimePointer(combinedSource.UpdatedAt)
	if _, err := store.Update(context.Background(), combined, current.Revision); err == nil {
		t.Fatal("combined Source locator append and retirement succeeded")
	}

	now = initial.Add(2 * time.Minute)
	retired, err := service.Retire(context.Background(), RetireRequest{Reference: directSource.ID.String(), ExpectedRevision: current.Revision})
	if err != nil {
		t.Fatalf("service retirement after direct append: %v", err)
	}
	retiredSource := retired.Record.(*research.Source)
	restored := retired.Clone()
	restoredSource := restored.Record.(*research.Source)
	restoredSource.State = research.SourceActive
	restoredSource.RetiredAt = nil
	restoredSource.UpdatedAt = initial.Add(3 * time.Minute)
	if _, err := store.Update(context.Background(), restored, retired.Revision); err == nil {
		t.Fatal("retired Source returned to active")
	}
	_, err = store.Transact(context.Background(), record.TransactionRequest{
		Operation: "source.delete",
		Changes: []record.TransactionChange{{
			Operation: record.TransactionDelete, ID: retiredSource.ID, ExpectedRevision: retired.Revision,
		}},
	})
	if !errors.Is(err, record.ErrInvalidTransaction) {
		t.Fatalf("published Source delete = %v", err)
	}
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if document, err := inventory.ByID(retiredSource.ID); err != nil || document.Revision != retired.Revision {
		t.Fatalf("forbidden delete changed Source = %#v, %v", document, err)
	}
}

func TestSourcePreparedTransactionRecoversAfterInterruptedPublication(t *testing.T) {
	info := initializeSourceProject(t)
	injected := errors.New("interrupt Source publication")
	fired := false
	store := record.NewStore(info.Root, info.Repository.GitCommonDir, record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
		if !fired && stage == record.StageTransactionCanonicalCreate {
			fired = true
			return injected
		}
		return nil
	}))
	service := New(store,
		WithClock(func() time.Time { return time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC) }),
		WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a03600-0000-7001-8000-000000000001"), nil
		}),
	)
	created, err := service.Add(context.Background(), AddRequest{Key: "recoverable", LocatorHints: []string{}, Body: "# Recoverable Source\n"})
	result, hasResult := TransactionResultFromError(err)
	if !errors.Is(err, injected) || created == nil || !fired || !hasResult || result.State != record.TransactionPrepared || !result.RecoveryRequired || len(result.Paths) != 1 || result.Paths[0].Published {
		t.Fatalf("interrupted Source Add = document %#v result %#v, %v, fired=%v", created, result, err, fired)
	}
	if _, err := store.Inventory(context.Background()); !errors.Is(err, record.ErrTransactionRecoveryRequired) {
		t.Fatalf("prepared Source transaction was exposed as committed: %v", err)
	}
	restarted := record.NewStore(info.Root, info.Repository.GitCommonDir)
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatalf("recover Source transaction: %v", err)
	}
	inventory, err := restarted.Inventory(context.Background())
	if err != nil || !inventory.Valid() {
		t.Fatalf("recovered Source inventory = %#v, %v", inventory, err)
	}
	document, err := inventory.ResolveSource("recoverable")
	if err != nil || document.Record.(*research.Source).ID.String() != "src_01a03600-0000-7001-8000-000000000001" {
		t.Fatalf("recovered Source = %#v, %v", document, err)
	}
}

func TestSourceTransactionErrorReportsPublishedCanonicalPath(t *testing.T) {
	info := initializeSourceProject(t)
	injected := errors.New("interrupt Source commit mark")
	store := record.NewStore(info.Root, info.Repository.GitCommonDir, record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
		if stage == record.StageTransactionCommitMark {
			return injected
		}
		return nil
	}))
	service := New(store,
		WithClock(func() time.Time { return time.Date(2026, 9, 3, 16, 30, 0, 0, time.UTC) }),
		WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a03610-0000-7001-8000-000000000001"), nil
		}),
	)
	created, err := service.Add(context.Background(), AddRequest{Key: "published", LocatorHints: []string{}, Body: "# Published Source\n"})
	result, hasResult := TransactionResultFromError(err)
	if !errors.Is(err, injected) || created == nil || !hasResult || result.State != record.TransactionPrepared || !result.RecoveryRequired || len(result.Paths) != 1 || !result.Paths[0].Published {
		t.Fatalf("published Source error = document %#v result %#v, %v", created, result, err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Root, filepath.FromSlash(result.Paths[0].Path))); statErr != nil {
		t.Fatalf("reported published Source path is absent: %v", statErr)
	}
}

func TestSourceAddRetriesIDReservedByLinkedWorktree(t *testing.T) {
	mainInfo := initializeSourceProject(t)
	sourceGitCommand(t, mainInfo.Repository.Root, "config", "user.name", "Exp Test")
	sourceGitCommand(t, mainInfo.Repository.Root, "config", "user.email", "exp-test@example.invalid")
	sourceGitCommand(t, mainInfo.Repository.Root, "add", "experiments")
	sourceGitCommand(t, mainInfo.Repository.Root, "commit", "--quiet", "-m", "initialize Source project")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	sourceGitCommand(t, mainInfo.Repository.Root, "worktree", "add", "--quiet", "-b", "source-reservation-test", linkedRoot)
	linkedInfo, err := project.Discover(context.Background(), linkedRoot)
	if err != nil {
		t.Fatal(err)
	}
	reserved := uuid.MustParse("01a03700-0000-7001-8000-000000000001")
	available := uuid.MustParse("01a03700-0000-7002-8000-000000000002")
	linkedService := New(record.NewStore(linkedInfo.Root, linkedInfo.Repository.GitCommonDir),
		WithClock(func() time.Time { return time.Date(2026, 9, 3, 17, 0, 0, 0, time.UTC) }),
		WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return reserved, nil }),
	)
	linkedSource, err := linkedService.Add(context.Background(), AddRequest{Key: "linked", LocatorHints: []string{}, Body: "linked\n"})
	if err != nil {
		t.Fatalf("linked Source Add: %v", err)
	}

	generator, calls := sourceUUIDSequence(reserved, available)
	mainStore := record.NewStore(mainInfo.Root, mainInfo.Repository.GitCommonDir)
	mainService := New(mainStore,
		WithClock(func() time.Time { return time.Date(2026, 9, 3, 17, 1, 0, 0, time.UTC) }),
		WithUUIDGenerator(generator),
	)
	mainSource, err := mainService.Add(context.Background(), AddRequest{Key: "main", LocatorHints: []string{}, Body: "main\n"})
	if err != nil {
		t.Fatalf("main Source Add after linked reservation: %v", err)
	}
	mainID, _ := mainSource.ID()
	linkedID, _ := linkedSource.ID()
	if mainID.UUID() != available || linkedID.UUID() != reserved || *calls != 2 {
		t.Fatalf("reservation retry IDs: main=%s linked=%s calls=%d", mainID, linkedID, *calls)
	}
	for _, id := range []research.ID{linkedID, mainID} {
		reservation := filepath.Join(mainStore.CoordinationDir(), "reservations", id.String())
		if content, err := os.ReadFile(reservation); err != nil || string(content) != id.String()+"\n" {
			t.Fatalf("reservation %s = %q, %v", id, content, err)
		}
	}
}

func TestBuildAddPlanIsPureNormalizedAndRequiresLocalOnlyConfirmation(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "missing-clone")
	missingCommon := filepath.Join(missingRoot, ".git")
	base := AddPlanRequest{
		Request:   AddRequest{Key: " Production_Workspace ", Title: "Production", Subdir: "services/api", Tags: []string{"zeta", "alpha"}},
		CloneRoot: missingRoot, CloneGitCommonDir: missingCommon,
	}
	if _, err := BuildAddPlan(base); !errors.Is(err, ErrLocalOnlyConfirmation) {
		t.Fatalf("local-only plan without confirmation = %v", err)
	}
	if _, err := os.Lstat(missingRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pure plan created clone path: %v", err)
	}
	base.Request.LocatorHints = []string{"https://mirror.example/org/repo.git"}
	if _, err := BuildAddPlan(base); !errors.Is(err, ErrLocatorObservation) {
		t.Fatalf("unobserved locator plan = %v", err)
	}
	base.ObservedLocatorHints = []string{"HTTPS://Example.COM:443/org/repo.git"}
	plan, err := BuildAddPlan(base)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Request.Key != "production-workspace" || plan.Request.Subdir != "services/api" || !slices.Equal(plan.Request.Tags, []string{"alpha", "zeta"}) || plan.LocalOnly || plan.LocalOnlyConfirmed {
		t.Fatalf("normalized plan = %#v", plan)
	}
	if got, want := plan.Request.LocatorHints, []string{"https://example.com/org/repo.git", "https://mirror.example/org/repo.git"}; !slices.Equal(got, want) {
		t.Fatalf("plan locators = %#v, want %#v", got, want)
	}
	if strings.Contains(strings.Join(plan.Request.LocatorHints, " "), "alice") || strings.Contains(strings.Join(plan.Request.LocatorHints, " "), "SECRET") {
		t.Fatalf("plan retained credentials: %#v", plan.Request.LocatorHints)
	}
	if _, err := os.Lstat(missingRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pure plan changed filesystem: %v", err)
	}
}

func initializeSourceProject(t *testing.T) *project.Info {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceGitCommand(t, repository, "init", "--quiet")
	info, _, err := project.Initialize(context.Background(), project.InitRequest{StartDir: repository, Name: "Source Test"},
		project.WithClock(func() time.Time { return time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC) }),
		project.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a032ff-0000-7001-8000-000000000001"), nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func sourceGitCommand(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}

func sourceUUIDSequence(values ...uuid.UUID) (research.UUIDGenerator, *int) {
	var mutex sync.Mutex
	calls := 0
	return func(time.Time) (uuid.UUID, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if calls >= len(values) {
			return uuid.Nil, errors.New("Source UUID sequence exhausted")
		}
		value := values[calls]
		calls++
		return value, nil
	}, &calls
}

func sourceTimePointer(value time.Time) *time.Time { return &value }
