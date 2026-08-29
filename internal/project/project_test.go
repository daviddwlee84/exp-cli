package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestInitializeIsIdempotentAndCreatesOnlyCanonicalRoot(t *testing.T) {
	repositoryRoot := initRepository(t)
	now := time.Date(2026, 8, 29, 10, 11, 12, 0, time.UTC)
	projectUUID := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	calls := 0
	generator := research.UUIDGenerator(func(got time.Time) (uuid.UUID, error) {
		calls++
		if !got.Equal(now) {
			t.Fatalf("generator time = %s, want %s", got, now)
		}
		return projectUUID, nil
	})
	info, created, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot, Name: "研究 Project"}, WithClock(func() time.Time { return now }), WithUUIDGenerator(generator))
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !created || calls != 1 || info.Project().ProjectID.String() != projectUUID.String() {
		t.Fatalf("created=%v calls=%d info=%#v", created, calls, info)
	}
	if info.Root != filepath.Join(repositoryRoot, "experiments") {
		t.Fatalf("root = %q", info.Root)
	}
	for _, relative := range []string{record.PlansDir, record.FindingsDir, record.DecisionsDir} {
		assertProjectMode(t, filepath.Join(info.Root, relative), 0o755)
	}
	assertProjectMode(t, filepath.Join(info.Root, record.ProjectFile), 0o644)
	for _, projection := range []string{"README.md", "ROADMAP.md", "LEDGER.md", "DECISIONS.md"} {
		if _, err := os.Lstat(filepath.Join(info.Root, projection)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("initializer wrote projection %s: %v", projection, err)
		}
	}
	coordination := info.Repository.CoordinationDir()
	assertProjectMode(t, coordination, 0o700)
	assertProjectMode(t, filepath.Join(coordination, "lock"), 0o600)
	assertProjectMode(t, filepath.Join(coordination, "transactions"), 0o700)
	assertProjectMode(t, filepath.Join(coordination, "attempts"), 0o700)

	second, created, err := Initialize(context.Background(), InitRequest{StartDir: filepath.Join(repositoryRoot, "experiments"), Name: "ignored"}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
		t.Fatal("idempotent initialize called UUID generator")
		return uuid.Nil, nil
	}))
	if err != nil || created {
		t.Fatalf("second Initialize = created %v, err %v", created, err)
	}
	if second.Project().ProjectID != info.Project().ProjectID {
		t.Fatalf("project identity changed: %s != %s", second.Project().ProjectID, info.Project().ProjectID)
	}
	inventory, err := record.LoadInventory(info.Root)
	if err != nil || !inventory.Valid() || len(inventory.Documents) != 1 {
		t.Fatalf("initialized inventory = %#v, %v", inventory, err)
	}
}

func TestDiscoverRequiresGitAndIgnoresOutOfScopeMarkers(t *testing.T) {
	if _, err := Discover(context.Background(), t.TempDir()); !errors.Is(err, gitx.ErrNotRepository) {
		t.Fatalf("non-Git discovery = %v", err)
	}
	repositoryRoot := initRepository(t)
	fixed := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	info, _, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return fixed, nil }))
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repositoryRoot, "src", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	discovered, err := Discover(context.Background(), nested)
	if err != nil || discovered.Root != info.Root {
		t.Fatalf("Discover nested = %#v, %v", discovered, err)
	}
	secondRoot := filepath.Join(repositoryRoot, "other-experiments")
	if err := os.Mkdir(secondRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	projectBytes, err := os.ReadFile(filepath.Join(info.Root, record.ProjectFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondRoot, record.ProjectFile), projectBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	discovered, err = Discover(context.Background(), repositoryRoot)
	if err != nil || discovered.Root != info.Root {
		t.Fatalf("out-of-scope marker changed discovery: %#v, %v", discovered, err)
	}
}

func TestDiscoverDoesNotAdoptNestedFixtureAndRejectsMalformedDefaultMarker(t *testing.T) {
	repositoryRoot := initRepository(t)
	nested := filepath.Join(repositoryRoot, "testdata", "v1", "valid-project")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, record.ProjectFile), []byte("not a project marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), repositoryRoot); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("nested marker was adopted: %v", err)
	}

	defaultRoot := filepath.Join(repositoryRoot, "experiments")
	if err := os.Mkdir(defaultRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, record.ProjectFile), []byte("+++\nschema = \"exp.project/v1\"\n+++\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), repositoryRoot); err == nil || errors.Is(err, ErrNotInitialized) {
		t.Fatalf("malformed default marker was not rejected: %v", err)
	}
}

func TestInitializeRefusesLegacyAndUnrelatedRootsWithoutOverwrite(t *testing.T) {
	for name, file := range map[string]string{"legacy": "REPORT.md", "unrelated": "notes.txt"} {
		t.Run(name, func(t *testing.T) {
			repositoryRoot := initRepository(t)
			root := filepath.Join(repositoryRoot, "experiments")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			original := []byte("keep me exactly\n")
			path := filepath.Join(root, file)
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}
			_, _, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot})
			want := ErrUnrelatedRoot
			if name == "legacy" {
				want = ErrLegacyRoot
			}
			if !errors.Is(err, want) {
				t.Fatalf("Initialize error = %v, want %v", err, want)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != string(original) {
				t.Fatalf("existing content changed: %q, %v", got, readErr)
			}
			if _, statErr := os.Lstat(filepath.Join(root, record.ProjectFile)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("PROJECT.md was created: %v", statErr)
			}
			receipt := filepath.Join(repositoryRoot, ".git", "exp", "v1", projectReceiptFile)
			if _, statErr := os.Lstat(receipt); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("refused initialization left a sticky Project receipt: %v", statErr)
			}
		})
	}
}

func TestConcurrentLinkedWorktreeInitializationSharesProjectIdentity(t *testing.T) {
	mainRoot := initRepository(t)
	runProjectGit(t, mainRoot, "config", "user.name", "Exp Test")
	runProjectGit(t, mainRoot, "config", "user.email", "exp-test@example.invalid")
	if err := os.WriteFile(filepath.Join(mainRoot, "seed"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runProjectGit(t, mainRoot, "add", "seed")
	runProjectGit(t, mainRoot, "commit", "--quiet", "-m", "seed")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runProjectGit(t, mainRoot, "worktree", "add", "--quiet", "-b", "linked-init", linkedRoot)

	firstID := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	secondID := uuid.MustParse("01a01e67-0e80-7202-8000-000000000202")
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	type result struct {
		info    *Info
		created bool
		err     error
	}
	firstResult := make(chan result, 1)
	go func() {
		info, created, err := Initialize(context.Background(), InitRequest{StartDir: mainRoot, Name: "Shared Project"},
			WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return firstID, nil }),
			WithReceiptAtomicHook(func(stage record.AtomicStage, _ string) error {
				if stage == record.StageTempWrite {
					once.Do(func() { close(entered) })
					<-release
				}
				return nil
			}),
		)
		firstResult <- result{info: info, created: created, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first initializer did not reach receipt publication")
	}
	secondGeneratorCalls := 0
	secondResult := make(chan result, 1)
	go func() {
		info, created, err := Initialize(context.Background(), InitRequest{StartDir: linkedRoot, Name: "Different Request Name"},
			WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
				secondGeneratorCalls++
				return secondID, nil
			}),
		)
		secondResult <- result{info: info, created: created, err: err}
	}()
	select {
	case result := <-secondResult:
		t.Fatalf("linked initializer bypassed common lock: %#v", result)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	first := <-firstResult
	second := <-secondResult
	if first.err != nil || second.err != nil || !first.created || !second.created {
		t.Fatalf("initializers = first %#v, second %#v", first, second)
	}
	if first.info.Project().ProjectID != second.info.Project().ProjectID || first.info.Project().ProjectID.String() != firstID.String() {
		t.Fatalf("Project identities differ: %s and %s", first.info.Project().ProjectID, second.info.Project().ProjectID)
	}
	if secondGeneratorCalls != 0 {
		t.Fatalf("linked initializer generated %d unnecessary UUIDs", secondGeneratorCalls)
	}
	assertProjectMode(t, filepath.Join(first.info.Repository.CoordinationDir(), projectReceiptFile), 0o600)
}

func TestInitializationReceiptSurvivesPostRenameFailure(t *testing.T) {
	repositoryRoot := initRepository(t)
	projectID := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	sentinel := errors.New("injected receipt sync failure")
	info, created, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot, Name: "Receipt Project"},
		WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return projectID, nil }),
		WithReceiptAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage == record.StageDirSync {
				return sentinel
			}
			return nil
		}),
	)
	if !errors.Is(err, sentinel) || info != nil || created {
		t.Fatalf("failed initialization = %#v, %v, %v", info, created, err)
	}
	if _, err := os.Lstat(filepath.Join(repositoryRoot, "experiments", record.ProjectFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canonical Project unexpectedly published: %v", err)
	}

	second, created, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot, Name: "Ignored"}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
		t.Fatal("receipt recovery generated a new UUID")
		return uuid.Nil, nil
	}))
	if err != nil || !created || second.Project().ProjectID.String() != projectID.String() {
		t.Fatalf("receipt recovery = %#v, %v, %v", second, created, err)
	}
}

func TestInitializePropagatesCanonicalDirectorySyncFailureAndReusesReceipt(t *testing.T) {
	repositoryRoot := initRepository(t)
	projectID := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	sentinel := errors.New("injected experiments parent sync failure")
	info, created, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot, Name: "Durable Directories"},
		WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return projectID, nil }),
		WithDirectorySyncHook(func(relative string) error {
			if relative == "experiments" {
				return sentinel
			}
			return nil
		}),
	)
	if !errors.Is(err, sentinel) || info != nil || created {
		t.Fatalf("directory-sync failure = %#v, %v, %v", info, created, err)
	}
	if _, err := os.Lstat(filepath.Join(repositoryRoot, "experiments", record.ProjectFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Project published after directory-sync failure: %v", err)
	}
	second, created, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
		t.Fatal("directory-sync retry generated another UUID")
		return uuid.Nil, nil
	}))
	if err != nil || !created || second.Project().ProjectID.String() != projectID.String() {
		t.Fatalf("directory-sync retry = %#v, %v, %v", second, created, err)
	}
}

func TestInitializeReconcilesStaleReceiptFromCanonicalProject(t *testing.T) {
	repositoryRoot := initRepository(t)
	canonicalID := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	info, _, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot, Name: "Canonical"}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return canonicalID, nil }))
	if err != nil {
		t.Fatal(err)
	}
	coordination, err := pathx.OpenCanonicalRootNoSymlinks(info.Repository.CoordinationDir())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := readProjectReceipt(coordination)
	if err != nil {
		_ = coordination.Close()
		t.Fatal(err)
	}
	stale := info.Document.Clone()
	staleID, err := research.NewProjectUUID(uuid.MustParse("01a01e67-0e80-7202-8000-000000000202"))
	if err != nil {
		_ = coordination.Close()
		t.Fatal(err)
	}
	stale.Record.(*research.Project).ProjectID = staleID
	staleContent, err := record.Encode(stale)
	if err != nil {
		_ = coordination.Close()
		t.Fatal(err)
	}
	if err := writeProjectReceipt(coordination, staleContent, receipt, nil); err != nil {
		_ = coordination.Close()
		t.Fatal(err)
	}
	if err := coordination.Close(); err != nil {
		t.Fatal(err)
	}

	reconciled, created, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
		t.Fatal("receipt reconciliation generated another UUID")
		return uuid.Nil, nil
	}))
	if err != nil || created || reconciled.Project().ProjectID.String() != canonicalID.String() {
		t.Fatalf("reconciled initialization = %#v, %v, %v", reconciled, created, err)
	}
	coordination, err = pathx.OpenCanonicalRootNoSymlinks(info.Repository.CoordinationDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coordination.Close()
	receipt, err = readProjectReceipt(coordination)
	if err != nil || receipt.document.Record.(*research.Project).ProjectID.String() != canonicalID.String() {
		t.Fatalf("reconciled receipt = %#v, %v", receipt, err)
	}
}

func TestInitializeRejectsConflictingLinkedProjectIdentities(t *testing.T) {
	mainRoot := initRepository(t)
	runProjectGit(t, mainRoot, "config", "user.name", "Exp Test")
	runProjectGit(t, mainRoot, "config", "user.email", "exp-test@example.invalid")
	firstID := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	mainInfo, _, err := Initialize(context.Background(), InitRequest{StartDir: mainRoot, Name: "Conflict"}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return firstID, nil }))
	if err != nil {
		t.Fatal(err)
	}
	runProjectGit(t, mainRoot, "add", "experiments")
	runProjectGit(t, mainRoot, "commit", "--quiet", "-m", "initialize")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runProjectGit(t, mainRoot, "worktree", "add", "--quiet", "-b", "linked-conflict", linkedRoot)
	linkedPath := filepath.Join(linkedRoot, "experiments", record.ProjectFile)
	content, err := os.ReadFile(linkedPath)
	if err != nil {
		t.Fatal(err)
	}
	document, err := record.Decode(content)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := research.NewProjectUUID(uuid.MustParse("01a01e67-0e80-7202-8000-000000000202"))
	if err != nil {
		t.Fatal(err)
	}
	document.Record.(*research.Project).ProjectID = otherID
	content, err = record.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkedPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Initialize(context.Background(), InitRequest{StartDir: mainInfo.Repository.Root}); !errors.Is(err, ErrProjectIdentityConflict) {
		t.Fatalf("conflicting linked identity error = %v", err)
	}
}

func TestDiscoverRejectsOversizedAndSwappedProjectMarkers(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		repositoryRoot := initRepository(t)
		info, _, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot, Name: "Bounded marker"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(filepath.Join(info.Root, record.ProjectFile), record.MaxRecordBytes+1); err != nil {
			t.Fatal(err)
		}
		if _, err := Discover(context.Background(), repositoryRoot); !errors.Is(err, pathx.ErrFileTooLarge) {
			t.Fatalf("oversized marker error = %v", err)
		}
	})

	t.Run("file swap", func(t *testing.T) {
		repositoryRoot := initRepository(t)
		info, _, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot, Name: "Marker swap"})
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside")
		outsideBytes := []byte("outside bytes\n")
		if err := os.WriteFile(outside, outsideBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		markerPath := filepath.Join(info.Root, record.ProjectFile)
		saved := filepath.Join(info.Root, "PROJECT.saved")
		_, found, err := readDefaultMarkerWithHook(context.Background(), repositoryRoot, func() {
			if renameErr := os.Rename(markerPath, saved); renameErr != nil {
				t.Fatal(renameErr)
			}
			if symlinkErr := os.Symlink(outside, markerPath); symlinkErr != nil {
				t.Fatal(symlinkErr)
			}
		})
		if err == nil || found {
			t.Fatalf("swapped marker = found %v, err %v", found, err)
		}
		if got, readErr := os.ReadFile(outside); readErr != nil || string(got) != string(outsideBytes) {
			t.Fatalf("outside marker target changed: %q, %v", got, readErr)
		}
	})
}

func TestInitializeRebuildsMissingOrCorruptReceiptFromCanonicalProject(t *testing.T) {
	repositoryRoot := initRepository(t)
	projectID := uuid.MustParse("01a01e66-0e80-7101-8000-000000000101")
	info, _, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot, Name: "Receipt authority"}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return projectID, nil }))
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(info.Repository.CoordinationDir(), projectReceiptFile)
	for _, mutate := range []struct {
		name string
		do   func() error
	}{
		{name: "missing", do: func() error { return os.Remove(receiptPath) }},
		{name: "corrupt", do: func() error { return os.WriteFile(receiptPath, []byte("not json\n"), 0o600) }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			if err := mutate.do(); err != nil {
				t.Fatal(err)
			}
			rebuilt, created, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot}, WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
				t.Fatal("authoritative Project recovery generated another identity")
				return uuid.Nil, nil
			}))
			if err != nil || created || rebuilt.Project().ProjectID != info.Project().ProjectID {
				t.Fatalf("receipt recovery = %#v, %v, %v", rebuilt, created, err)
			}
			coordination, err := pathx.OpenCanonicalRootNoSymlinks(info.Repository.CoordinationDir())
			if err != nil {
				t.Fatal(err)
			}
			receipt, readErr := readProjectReceipt(coordination)
			closeErr := coordination.Close()
			if readErr != nil || closeErr != nil || receipt == nil || !sameProjectIdentity(receipt.document, info.Document) {
				t.Fatalf("rebuilt receipt = %#v, %v, %v", receipt, readErr, closeErr)
			}
		})
	}
}

func TestInitializeRejectsUnknownTransactionArtifacts(t *testing.T) {
	repositoryRoot := initRepository(t)
	artifact := filepath.Join(repositoryRoot, ".git", "exp", "v1", "transactions", "future-v2", "journal.toml")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("schema = \"future/v2\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot}); !errors.Is(err, record.ErrUnsupportedTransaction) {
		t.Fatalf("unknown transaction error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repositoryRoot, "experiments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initialization mutated canonical root despite transaction artifact: %v", err)
	}
}

func TestInitializeRejectsSymlinkedGitCommonCoordinationPath(t *testing.T) {
	repositoryRoot := initRepository(t)
	outside := t.TempDir()
	gitDir := filepath.Join(repositoryRoot, ".git")
	if err := os.Symlink(outside, filepath.Join(gitDir, "exp")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, _, err := Initialize(context.Background(), InitRequest{StartDir: repositoryRoot})
	if !errors.Is(err, pathx.ErrSymlink) {
		t.Fatalf("symlinked coordination error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "v1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initializer wrote through coordination symlink: %v", err)
	}
}

func initRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--quiet")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func runProjectGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}

func assertProjectMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
