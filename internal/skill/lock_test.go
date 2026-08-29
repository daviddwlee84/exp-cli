package skill

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
)

func TestInstallLockHonorsContextWhileContended(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skill")
	destination, err := resolveInstallDestination(dir, "test holder")
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- withInstallLock(context.Background(), destination, "test holder", nil, func(*os.Root) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err = Install(ctx, dir, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		<-firstDone
		t.Fatalf("contended Install error = %v, want context deadline", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("holder lock: %v", err)
	}
}

func TestLockFilePresenceAloneDoesNotClaimLock(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(parent, ".skill.lock")
	if err := os.WriteFile(lockPath, []byte("not an advisory lock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Install(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Install treated lock-file existence as ownership: %v", err)
	}
	if !result.Changed {
		t.Fatal("first install should write embedded files")
	}
}

func TestInstallRejectsSymlinkLockFileWithoutChangingItsTarget(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "dragonfly", "freebsd", "illumos", "linux", "netbsd", "openbsd":
	default:
		t.Skip("platform uses the portable directory-lock backend")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "skill")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "user-owned.txt")
	sentinel := []byte("do not touch\n")
	if err := os.WriteFile(outside, sentinel, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(parent, ".skill.lock")
	if err := os.Symlink(outside, lockPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Install(context.Background(), dir, false); err == nil {
		t.Fatal("Install accepted a symlink at its advisory lock path")
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != string(sentinel) {
		t.Fatalf("lock symlink target changed: content=%q err=%v", content, err)
	}
	info, err := os.Stat(outside)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("lock symlink target mode changed: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe lock path allowed skill publication: %v", err)
	}
}

func TestInstallRejectsHardLinkedLockFileWithoutChangingItsTarget(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "dragonfly", "freebsd", "illumos", "linux", "netbsd", "openbsd":
	default:
		t.Skip("platform uses the portable directory-lock backend")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "skill")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "user-owned.txt")
	sentinel := []byte("do not chmod me\n")
	if err := os.WriteFile(outside, sentinel, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(parent, ".skill.lock")
	if err := os.Link(outside, lockPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	if _, err := Install(context.Background(), dir, false); err == nil {
		t.Fatal("Install accepted a hard-linked advisory lock with an unsafe mode")
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != string(sentinel) {
		t.Fatalf("hard-linked lock target changed: content=%q err=%v", content, err)
	}
	info, err := os.Stat(outside)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("hard-linked lock target mode changed: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe hard-linked lock allowed skill publication: %v", err)
	}
}

func TestInstallRejectsNonregularLockPathWithoutRemovingIt(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "skill")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(parent, ".skill.lock")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(context.Background(), dir, false); err == nil {
		t.Fatal("Install accepted a directory at its advisory lock path")
	}
	info, err := os.Lstat(lockPath)
	if err != nil || !info.IsDir() {
		t.Fatalf("nonregular lock entry was changed: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe lock path allowed skill publication: %v", err)
	}
}

func TestInstallCanceledBeforeLockDoesNotWriteKnownFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Install(ctx, dir, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Install error = %v, want context canceled", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled install wrote SKILL.md or returned unexpected error: %v", err)
	}
}

func TestInstallRefusesRetargetedRootSymlinkAfterSelectingCanonicalLock(t *testing.T) {
	parent := t.TempDir()
	targetA := filepath.Join(parent, "target-a")
	targetB := filepath.Join(parent, "target-b")
	if err := os.Mkdir(targetA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(targetB, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "skill-link")
	if err := os.Symlink(targetA, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	sentinel := []byte("user-owned target B\n")
	if err := os.WriteFile(filepath.Join(targetB, "SKILL.md"), sentinel, 0o644); err != nil {
		t.Fatal(err)
	}

	selected, err := resolveInstallDestination(targetA, "test target")
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(filepath.Dir(selected.canonical), "."+filepath.Base(selected.canonical)+".lock")
	heldLock, err := acquireInstallLock(context.Background(), lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if heldLock != nil {
			_ = heldLock.Close()
		}
	}()

	attempting := make(chan string, 1)
	operations := defaultInstallOperations()
	operations.acquireLock = func(ctx context.Context, path string) (io.Closer, error) {
		attempting <- path
		return acquireInstallLock(ctx, path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type outcome struct {
		result InstallResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, installErr := install(ctx, alias, false, operations)
		done <- outcome{result: result, err: installErr}
	}()

	select {
	case got := <-attempting:
		if got != lockPath {
			t.Fatalf("installer attempted lock %q, want canonical lock %q", got, lockPath)
		}
	case early := <-done:
		t.Fatalf("installer finished before attempting the canonical lock: %v", early.err)
	case <-ctx.Done():
		t.Fatal("installer did not attempt the canonical lock")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetB, alias); err != nil {
		t.Fatal(err)
	}
	if err := heldLock.Close(); err != nil {
		t.Fatal(err)
	}
	heldLock = nil

	var got outcome
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("installer did not finish after canonical lock release")
	}
	if got.err == nil {
		t.Fatal("install accepted a retargeted requested destination")
	}
	if got.result.Dir != selected.canonical {
		t.Fatalf("result dir = %q, want selected canonical target %q", got.result.Dir, selected.canonical)
	}
	if got.result.Changed || len(got.result.Written) != 0 {
		t.Fatalf("retargeted install reported changes: %#v", got.result)
	}
	if _, err := os.Stat(filepath.Join(targetA, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected target was written before refusal: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(targetB, "SKILL.md"))
	if err != nil || string(content) != string(sentinel) {
		t.Fatalf("retargeted destination was clobbered: content=%q err=%v", content, err)
	}
}

func TestInstallPreservesPublishedFileActionAfterDirectorySyncFailure(t *testing.T) {
	for _, test := range []struct {
		name   string
		update bool
		action FileInstallAction
	}{
		{name: "create", action: FileCreated},
		{name: "update", update: true, action: FileUpdated},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "skill")
			if test.update {
				if _, err := Install(context.Background(), dir, false); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("stale\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			sentinel := errors.New("injected directory sync failure")
			operations := defaultInstallOperations()
			operations.syncRootDirectory = func(_ *os.Root, name string) error {
				if name == "." {
					return sentinel
				}
				return nil
			}
			result, err := install(context.Background(), dir, false, operations)
			if !errors.Is(err, sentinel) {
				t.Fatalf("Install error = %v, want injected sync failure", err)
			}
			var publication *PublicationError
			if !errors.As(err, &publication) || !publication.Published || publication.Path != "SKILL.md" {
				t.Fatalf("publication error = %#v", publication)
			}
			if !result.Changed || len(result.Written) != 1 || result.Written[0] != "SKILL.md" || len(result.Files) != 1 {
				t.Fatalf("published result lost completed write: %#v", result)
			}
			if result.Files[0].Action != test.action {
				t.Fatalf("file action = %q, want %q", result.Files[0].Action, test.action)
			}
			switch test.action {
			case FileCreated:
				if len(result.Created) != 1 || result.Created[0] != "SKILL.md" || len(result.Updated) != 0 {
					t.Fatalf("created result = %#v", result)
				}
			case FileUpdated:
				if len(result.Updated) != 1 || result.Updated[0] != "SKILL.md" || len(result.Created) != 0 {
					t.Fatalf("updated result = %#v", result)
				}
			}
			embedded, filesErr := Files()
			if filesErr != nil {
				t.Fatal(filesErr)
			}
			content, readErr := os.ReadFile(filepath.Join(dir, "SKILL.md"))
			if readErr != nil || string(content) != string(embedded["SKILL.md"]) {
				t.Fatalf("published bytes = %q, err=%v", content, readErr)
			}
		})
	}
}

func TestConsumerBaseStatErrorIsNotReportedCurrent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".agents", "skills", Name)
	if _, err := Install(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude"), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := CheckWithOptions(context.Background(), dir, CheckOptions{Links: true})
	if err == nil {
		t.Fatal("requested consumer check accepted an uninspectable base")
	}
	if result.Current || result.LinksCurrent {
		t.Fatalf("consumer stat error reported current: %#v", result)
	}
	if len(result.Links) != 1 || result.Links[0].State != LinkOther {
		t.Fatalf("consumer stat error result = %#v", result.Links)
	}
}

func TestInstallCreateConflictPreservesRacingBytes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skill")
	racing := []byte("user-created bytes\n")
	operations := defaultInstallOperations()
	operations.atomicHook = func(stage record.AtomicStage, relative string) error {
		if stage != record.StageRename || relative != "SKILL.md" {
			return nil
		}
		return os.WriteFile(filepath.Join(dir, "SKILL.md"), racing, 0o600)
	}

	result, err := install(context.Background(), dir, false, operations)
	if err == nil {
		t.Fatal("Install clobbered a racing create")
	}
	if len(result.Written) != 0 {
		t.Fatalf("conflicted create was reported written: %#v", result)
	}
	content, readErr := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if readErr != nil || string(content) != string(racing) {
		t.Fatalf("racing create was not preserved: content=%q err=%v", content, readErr)
	}
}

func TestInstallUpdateConflictPreservesInPlaceEdit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skill")
	if _, err := Install(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("stale installer snapshot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	racing := []byte("user edit during install\n")
	operations := defaultInstallOperations()
	operations.atomicHook = func(stage record.AtomicStage, relative string) error {
		if stage != record.StageRename || relative != "SKILL.md" {
			return nil
		}
		return os.WriteFile(path, racing, 0o644)
	}

	result, err := install(context.Background(), dir, false, operations)
	if !errors.Is(err, record.ErrAtomicConflict) {
		t.Fatalf("Install error = %v, want atomic conflict", err)
	}
	if len(result.Written) != 0 {
		t.Fatalf("conflicted update was reported written: %#v", result)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != string(racing) {
		t.Fatalf("in-place user edit was not preserved: content=%q err=%v", content, readErr)
	}
}

func TestInstallNeverPublishesSubstitutedTemporaryBytes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skill")
	substituted := []byte("substituted temporary bytes\n")
	operations := defaultInstallOperations()
	operations.atomicHook = func(stage record.AtomicStage, relative string) error {
		if stage != record.StageRename || relative != "SKILL.md" {
			return nil
		}
		matches, err := filepath.Glob(filepath.Join(dir, ".exp-*.tmp"))
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			return errors.New("atomic temporary not found at rename boundary")
		}
		if err := os.Remove(matches[0]); err != nil {
			return err
		}
		return os.WriteFile(matches[0], substituted, 0o644)
	}

	_, installErr := install(context.Background(), dir, false, operations)
	content, readErr := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if readErr == nil && string(content) == string(substituted) {
		t.Fatal("Install published bytes from a substituted temporary inode")
	}
	if installErr == nil && readErr != nil {
		t.Fatalf("Install succeeded without publishing the intended file: %v", readErr)
	}
}

func TestInstallUpdateNeverPublishesSubstitutedTemporaryBytes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skill")
	if _, err := Install(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	stale := []byte("stale managed bytes\n")
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}

	substituted := []byte("substituted replacement bytes\n")
	operations := defaultInstallOperations()
	operations.atomicHook = func(stage record.AtomicStage, relative string) error {
		if stage != record.StageRename || relative != "SKILL.md" {
			return nil
		}
		matches, err := filepath.Glob(filepath.Join(dir, ".exp-*.tmp"))
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			return errors.New("atomic temporary not found at replacement boundary")
		}
		if err := os.Remove(matches[0]); err != nil {
			return err
		}
		return os.WriteFile(matches[0], substituted, 0o644)
	}

	result, err := install(context.Background(), dir, false, operations)
	if err == nil {
		t.Fatal("Install accepted a substituted replacement temporary")
	}
	if len(result.Written) != 0 {
		t.Fatalf("failed replacement was reported written: %#v", result)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != string(stale) {
		t.Fatalf("replacement destination = %q, %v; want original stale bytes", content, readErr)
	}
}

func TestInstallCancellationAfterManagedReplacementReportsPublished(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skill")
	if _, err := Install(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("stale managed bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	operations := defaultInstallOperations()
	operations.atomicHook = func(stage record.AtomicStage, relative string) error {
		if stage == record.StageDirSync && relative == "SKILL.md" {
			cancel()
		}
		return nil
	}
	result, err := install(ctx, dir, false, operations)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Install error = %v, want context canceled", err)
	}
	var publication *PublicationError
	if !errors.As(err, &publication) || !publication.Published || publication.Path != "SKILL.md" {
		t.Fatalf("publication error = %#v", publication)
	}
	if !result.Changed || len(result.Updated) != 1 || result.Updated[0] != "SKILL.md" {
		t.Fatalf("published replacement result = %#v", result)
	}
	embedded, filesErr := Files()
	if filesErr != nil {
		t.Fatal(filesErr)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != string(embedded["SKILL.md"]) {
		t.Fatalf("published replacement bytes = %q, %v", content, readErr)
	}
}

func TestCheckMissingDestinationReturnsLateCancellation(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	ctx := &cancelAfterErrChecks{
		Context:  context.Background(),
		cancelAt: len(files) + 3,
	}
	result, err := CheckWithOptions(ctx, filepath.Join(t.TempDir(), "missing"), CheckOptions{Links: false})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Check error = %v, want context canceled; result=%#v", err, result)
	}
	if result.Current {
		t.Fatalf("canceled missing check reported current: %#v", result)
	}
}

type cancelAfterErrChecks struct {
	context.Context
	calls    int
	cancelAt int
}

func (ctx *cancelAfterErrChecks) Err() error {
	ctx.calls++
	if ctx.calls >= ctx.cancelAt {
		return context.Canceled
	}
	return nil
}
