package lockx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWithDirSerializesAndReportsOwnerOnCancellation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "common", "exp", "v1")
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- WithDir(context.Background(), dir, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first lock did not enter")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := WithDir(ctx, dir, func() error {
		t.Fatal("contending operation ran")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contending error = %v", err)
	}
	var acquire *AcquireError
	if !errors.As(err, &acquire) || acquire.Owner == nil || acquire.Owner.PID != os.Getpid() {
		t.Fatalf("owner metadata = %#v, error %v", acquire, err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := WithDir(context.Background(), dir, func() error { return nil }); err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, filepath.Join(dir, "lock"), 0o600)
}

func TestWithTrustedRootRejectsSymlinkAncestor(t *testing.T) {
	trusted, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(trusted, "exp")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	called := false
	err = WithTrustedRoot(context.Background(), trusted, "exp/v1", func(*os.Root) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("symlink ancestor accepted: called=%v err=%v", called, err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "v1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock path escaped trusted root: %v", err)
	}
}

func TestWithTrustedRootRejectsHardLinkedLockBeforeMutation(t *testing.T) {
	trusted, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	coordination := filepath.Join(trusted, "exp", "v1")
	if err := os.MkdirAll(coordination, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	original := []byte("do not truncate\n")
	if err := os.WriteFile(outside, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(coordination, "lock")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	called := false
	err = WithTrustedRoot(context.Background(), trusted, "exp/v1", func(*os.Root) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("hard-linked lock accepted: called=%v err=%v", called, err)
	}
	content, readErr := os.ReadFile(outside)
	if readErr != nil || string(content) != string(original) {
		t.Fatalf("hard-link target changed: %q, %v", content, readErr)
	}
	info, statErr := os.Stat(outside)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("hard-link target mode changed: %#o", info.Mode().Perm())
	}
}

func TestWithTrustedRootDetectsRetargetWhileWaiting(t *testing.T) {
	trusted, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- WithTrustedRoot(context.Background(), trusted, "exp/v1", func(*os.Root) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first lock did not enter")
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	called := false
	go func() {
		close(secondStarted)
		secondDone <- WithTrustedRoot(context.Background(), trusted, "exp/v1", func(*os.Root) error {
			called = true
			return nil
		})
	}()
	<-secondStarted
	time.Sleep(50 * time.Millisecond)
	original := filepath.Join(trusted, "exp", "v1")
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(original, moved); err != nil {
		close(release)
		t.Fatal(err)
	}
	if err := os.Symlink(moved, original); err != nil {
		close(release)
		t.Skipf("symlinks unavailable: %v", err)
	}
	close(release)
	if err := <-firstDone; err == nil {
		t.Fatal("active lock did not report its retargeted root")
	}
	if err := <-secondDone; err == nil || called {
		t.Fatalf("retargeted waiter ran operation: called=%v err=%v", called, err)
	}
}

func TestWithDirRejectsSymlinkLockFile(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "lock")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := WithDir(context.Background(), dir, func() error { return nil }); err == nil {
		t.Fatal("symlink lock file was accepted")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
