package pathx

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestResolveUnderAcceptsMissingChildAndRejectsLexicalEscapes(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveUnder(root, "plans/new.md", false)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(canonicalRoot, "plans", "new.md"); got != want {
		t.Fatalf("ResolveUnder = %q, want %q", got, want)
	}
	for _, value := range []string{"../outside", "/absolute", `C:\\outside`, `a\\b`, "a//b", "~/.secret", "."} {
		if _, err := ResolveUnder(root, value, false); err == nil {
			t.Errorf("unsafe path %q validated", value)
		}
	}
}

func TestResolveUnderRejectsSymlinkEscapeAndWriteRejectsAnySymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	outside := filepath.Join(parent, "outside")
	inside := filepath.Join(root, "inside")
	for _, directory := range []string{root, outside, inside} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolveUnder(root, "escape/new.md", false); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("symlink escape error = %v", err)
	}
	insideLink := filepath.Join(root, "inside-link")
	if err := os.Symlink(inside, insideLink); err != nil {
		t.Fatal(err)
	}
	canonicalInside, err := Canonical(inside)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := ResolveUnder(root, "inside-link/new.md", false); err != nil || resolved != filepath.Join(canonicalInside, "new.md") {
		t.Fatalf("read containment through internal symlink = %q, %v", resolved, err)
	}
	if _, err := ResolveUnderNoSymlinks(root, "inside-link/new.md", false); !errors.Is(err, ErrSymlink) {
		t.Fatalf("write traversal through symlink = %v", err)
	}
}

func TestOpenedRootDetectsPathRetargetAndRemainsBoundToOriginalDirectory(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	movedPath := filepath.Join(parent, "moved")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := OpenRootNoSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(rootPath, movedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRootPath(rootPath, root); err == nil {
		t.Fatal("retargeted root path retained the same identity")
	}
	if err := root.WriteFile("anchored", []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(movedPath, "anchored")); err != nil {
		t.Fatalf("opened root did not remain anchored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "anchored")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opened root followed replacement path: %v", err)
	}
}

func TestEnsureRootAtNoSymlinksSyncsAndRejectsSymlinkComponents(t *testing.T) {
	rootPath, err := Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := OpenRootNoSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	nested, created, err := EnsureRootAtNoSymlinks(root, "one/two", 0o700)
	if err != nil || !created {
		t.Fatalf("EnsureRootAtNoSymlinks = %v, %v", created, err)
	}
	_ = nested.Close()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(rootPath, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := EnsureRootAtNoSymlinks(root, "link/child", 0o700); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked component error = %v", err)
	}
}

func TestEnsureRootAtNoSymlinksAcceptsConcurrentSafeCreator(t *testing.T) {
	rootPath, err := Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	start := make(chan struct{})
	results := make([]error, workers)
	created := make([]bool, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			root, err := OpenCanonicalRootNoSymlinks(rootPath)
			if err != nil {
				results[index] = err
				return
			}
			defer root.Close()
			<-start
			nested, wasCreated, err := EnsureRootAtNoSymlinks(root, "shared/nested", 0o700)
			if nested != nil {
				err = errors.Join(err, nested.Close())
			}
			created[index] = wasCreated
			results[index] = err
		}(index)
	}
	close(start)
	wait.Wait()
	createdAny := false
	for index, err := range results {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
		createdAny = createdAny || created[index]
	}
	if !createdAny {
		t.Fatal("no concurrent writer reported creating the directory")
	}
	info, err := os.Stat(filepath.Join(rootPath, "shared", "nested"))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("concurrent directory = %#v, %v", info, err)
	}
}

func TestCanonicalRejectsDanglingSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolveUnderNoSymlinks(root, "link/file", false); !errors.Is(err, ErrSymlink) {
		t.Fatalf("dangling symlink error = %v", err)
	}
}
