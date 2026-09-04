package sourcesnapshot

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestRealGitCleanCaptureAndExplicitObservation(t *testing.T) {
	repository, first, source := newCaptureRepository(t, "services/api")
	writeTestFile(t, filepath.Join(repository, "services", "api", "z.txt"), "z2\n", 0o644)
	writeTestFile(t, filepath.Join(repository, "services", "api", "a.txt"), "a\n", 0o644)
	runCaptureGit(t, repository, "add", "--", "services/api/a.txt", "services/api/z.txt")
	runCaptureGit(t, repository, "commit", "-qm", "ordered changes")
	head := captureGitLine(t, repository, "rev-parse", "HEAD")
	repositoryInfo, err := gitx.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Date(2026, 9, 3, 12, 0, 0, 123, time.UTC)
	capturer := Capturer{Clock: func() time.Time { return capturedAt }}
	if _, err := capturer.CaptureClean(t.Context(), Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: repositoryInfo.GitCommonDir,
		BaseCommit: first, ExpectedHead: first,
	}); !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("mismatched exact HEAD error = %v", err)
	}
	tree := captureGitLine(t, repository, "rev-parse", "HEAD^{tree}")
	divergent := captureGitLine(t, repository, "commit-tree", tree, "-m", "unrelated root")
	if _, err := capturer.CaptureClean(t.Context(), Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: repositoryInfo.GitCommonDir,
		BaseCommit: divergent, ExpectedHead: head,
	}); !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("non-ancestor base error = %v", err)
	}
	snapshot, err := capturer.CaptureClean(t.Context(), Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: repositoryInfo.GitCommonDir,
		BaseCommit: first, ExpectedHead: head,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"services/api/a.txt", "services/api/z.txt"}
	if !reflect.DeepEqual(snapshot.ChangeSet, wantPaths) || snapshot.CapturedAt != capturedAt || snapshot.State != research.SourceSnapshotClean || snapshot.Reproducibility != research.ReproducibilityExact {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if digest, digestErr := research.SourceSnapshotDigest(snapshot); digestErr != nil || digest != snapshot.Digest {
		t.Fatalf("snapshot digest = %q, %v", digest, digestErr)
	}
	if _, err := capturer.CaptureClean(t.Context(), Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: repositoryInfo.GitCommonDir,
		BaseCommit: head, ExpectedHead: head,
	}); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("implicit no-change capture error = %v", err)
	}
	observed, err := capturer.CaptureClean(t.Context(), Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: repositoryInfo.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, AllowNoChanges: true,
	})
	if err != nil || observed.ChangeSet == nil || len(observed.ChangeSet) != 0 {
		t.Fatalf("explicit observation = %#v, %v", observed, err)
	}
	if status := runCaptureGit(t, repository, "status", "--porcelain=v2", "-z", "--untracked-files=all"); status != "" {
		t.Fatalf("clean capture changed production checkout: %q", status)
	}
}

func TestRealGitSHA256ObjectFormatCapture(t *testing.T) {
	repository := filepath.Join(canonicalCaptureTemp(t), "sha256 source")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "git", "init", "-q", "--object-format=sha256")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("Git SHA-256 repositories unavailable: %v: %s", err, output)
	}
	runCaptureGit(t, repository, "config", "user.name", "SHA256 Test")
	runCaptureGit(t, repository, "config", "user.email", "sha256@example.invalid")
	writeTestFile(t, filepath.Join(repository, "file.txt"), "sha256\n", 0o644)
	runCaptureGit(t, repository, "add", "--", "file.txt")
	runCaptureGit(t, repository, "commit", "-qm", "sha256 baseline")
	head := captureGitLine(t, repository, "rev-parse", "HEAD")
	info, err := gitx.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	source := validCaptureSource(t, "src_01a09000-0000-7601-8000-000000000601", ".")
	snapshot, err := (Capturer{}).CaptureClean(t.Context(), Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: info.GitCommonDir,
		BaseCommit: head, ExpectedHead: head, AllowNoChanges: true,
		CapturedAt: time.Date(2026, 9, 3, 12, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GitObjectFormat != research.GitObjectSHA256 || len(snapshot.HeadCommit) != 64 {
		t.Fatalf("SHA-256 snapshot = %#v", snapshot)
	}
}

func TestRealGitDirtyCaptureBundleDeterminismAndPrivacy(t *testing.T) {
	repository, base, source := newCaptureRepository(t, "services/api")
	repositoryInfo, err := gitx.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repository, "services", "api", "z.txt"), "tracked dirty\n", 0o644)
	writeTestFile(t, filepath.Join(repository, "services", "api", "new-b.txt"), "BINARY-SECRET-B\x00\x01", 0o600)
	writeTestFile(t, filepath.Join(repository, "services", "api", "new-a.sh"), "#!/bin/sh\nCANARY-UNTRACKED\n", 0o755)
	writeTestFile(t, filepath.Join(repository, ".gitignore"), "*.ignored\n", 0o644)
	runCaptureGit(t, repository, "add", "--", ".gitignore")
	runCaptureGit(t, repository, "commit", "-qm", "ignore policy")
	head := captureGitLine(t, repository, "rev-parse", "HEAD")
	writeTestFile(t, filepath.Join(repository, "services", "api", "skip.ignored"), "IGNORED-CANARY", 0o644)
	tryID := mustCaptureID(t, "try_01a09000-0000-7004-8000-000000000004", research.KindTry)
	capturedAt := time.Date(2026, 9, 3, 13, 0, 0, 0, time.UTC)
	firstStore := &BundleStore{Root: filepath.Join(canonicalCaptureTemp(t), "bundles-one")}
	request := Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: repositoryInfo.GitCommonDir,
		BaseCommit: base, ExpectedHead: head, Try: tryID, CapturedAt: capturedAt,
	}
	first, err := (Capturer{Bundles: firstStore}).CaptureDirty(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{".gitignore", "services/api/new-a.sh", "services/api/new-b.txt", "services/api/z.txt"}
	if !reflect.DeepEqual(first.Snapshot.ChangeSet, wantPaths) {
		t.Fatalf("dirty paths = %#v", first.Snapshot.ChangeSet)
	}
	if strings.Contains(first.Snapshot.DirtySummary, "CANARY") || first.Bundle == nil || len(first.Bundle.Manifest.Untracked) != 2 {
		t.Fatalf("dirty result = %#v", first)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"tracked dirty", "CANARY-UNTRACKED", "BINARY-SECRET-B", "IGNORED-CANARY"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("command-safe JSON leaked raw payload %q: %s", secret, encoded)
		}
	}
	if got := first.Bundle.Manifest.Untracked[0].Path; got != "services/api/new-a.sh" {
		t.Fatalf("untracked order starts with %q", got)
	}
	if first.Bundle.Manifest.Untracked[0].Mode != 0o755 || first.Bundle.Manifest.Untracked[1].Mode != 0o600 {
		t.Fatalf("untracked modes = %#v", first.Bundle.Manifest.Untracked)
	}
	assertPrivateTree(t, first.Bundle.Directory)

	secondStore := &BundleStore{Root: filepath.Join(canonicalCaptureTemp(t), "bundles-two")}
	second, err := (Capturer{Bundles: secondStore}).CaptureDirty(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot.Digest != second.Snapshot.Digest || first.Snapshot.DirtyDigest != second.Snapshot.DirtyDigest || first.Bundle.Digest() != second.Bundle.Digest() {
		t.Fatalf("nondeterministic capture:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if err := firstStore.Cleanup(t.Context(), *first.Bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(first.Bundle.Directory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("bundle remains after explicit cleanup: %v", err)
	}
}

func TestDirtyCaptureLimitsSymlinkAndTOCTOU(t *testing.T) {
	t.Run("entry and byte limits", func(t *testing.T) {
		repository, base, source := newCaptureRepository(t, ".")
		info, _ := gitx.Discover(t.Context(), repository)
		writeTestFile(t, filepath.Join(repository, "one.txt"), "1", 0o644)
		writeTestFile(t, filepath.Join(repository, "two.txt"), "22", 0o644)
		request := dirtyRequest(t, repository, info.GitCommonDir, base, source)
		request.Limits.MaxUntrackedFiles = 1
		if _, err := (Capturer{}).CaptureDirty(t.Context(), request); !errors.Is(err, ErrLimit) {
			t.Fatalf("entry limit error = %v", err)
		}
		request.Limits = Limits{MaxUntrackedFileBytes: 1}
		if _, err := (Capturer{}).CaptureDirty(t.Context(), request); !errors.Is(err, ErrLimit) {
			t.Fatalf("file limit error = %v", err)
		}
		request.Limits = Limits{MaxUntrackedTotalBytes: 2}
		if _, err := (Capturer{}).CaptureDirty(t.Context(), request); !errors.Is(err, ErrLimit) {
			t.Fatalf("total limit error = %v", err)
		}
	})

	t.Run("hidden index flags", func(t *testing.T) {
		repository, base, source := newCaptureRepository(t, ".")
		info, _ := gitx.Discover(t.Context(), repository)
		runCaptureGit(t, repository, "update-index", "--assume-unchanged", "z.txt")
		writeTestFile(t, filepath.Join(repository, "z.txt"), "hidden change", 0o644)
		request := dirtyRequest(t, repository, info.GitCommonDir, base, source)
		if _, err := (Capturer{}).CaptureDirty(t.Context(), request); !errors.Is(err, ErrUnsupportedIndex) {
			t.Fatalf("hidden-index dirty error = %v", err)
		}
		if _, err := (Capturer{}).CaptureClean(t.Context(), request); !errors.Is(err, ErrUnsupportedIndex) {
			t.Fatalf("hidden-index clean error = %v", err)
		}
	})

	t.Run("tracked patch limit", func(t *testing.T) {
		repository, base, source := newCaptureRepository(t, ".")
		info, _ := gitx.Discover(t.Context(), repository)
		writeTestFile(t, filepath.Join(repository, "z.txt"), strings.Repeat("large", 32), 0o644)
		request := dirtyRequest(t, repository, info.GitCommonDir, base, source)
		request.Limits = Limits{MaxPatchBytes: 16}
		if _, err := (Capturer{}).CaptureDirty(t.Context(), request); !errors.Is(err, ErrLimit) {
			t.Fatalf("patch limit error = %v", err)
		}
	})

	t.Run("outside Source subdir", func(t *testing.T) {
		repository, base, source := newCaptureRepository(t, "services/api")
		info, _ := gitx.Discover(t.Context(), repository)
		writeTestFile(t, filepath.Join(repository, "outside.txt"), "outside", 0o644)
		if _, err := (Capturer{}).CaptureDirty(t.Context(), dirtyRequest(t, repository, info.GitCommonDir, base, source)); !errors.Is(err, ErrOutsideSourceSubdir) {
			t.Fatalf("outside-subdir capture error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		repository, base, source := newCaptureRepository(t, ".")
		info, _ := gitx.Discover(t.Context(), repository)
		if err := os.Symlink("z.txt", filepath.Join(repository, "link.txt")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := (Capturer{}).CaptureDirty(t.Context(), dirtyRequest(t, repository, info.GitCommonDir, base, source)); !errors.Is(err, pathx.ErrSymlink) {
			t.Fatalf("symlink capture error = %v", err)
		}
	})

	t.Run("identity replacement", func(t *testing.T) {
		repository, base, source := newCaptureRepository(t, ".")
		info, _ := gitx.Discover(t.Context(), repository)
		writeTestFile(t, filepath.Join(repository, "untracked.txt"), "before", 0o644)
		filesystem := &replacingFileSystem{root: repository, target: "untracked.txt"}
		_, err := (Capturer{FileSystem: filesystem}).CaptureDirty(t.Context(), dirtyRequest(t, repository, info.GitCommonDir, base, source))
		if !errors.Is(err, ErrSourceChanged) {
			t.Fatalf("TOCTOU error = %v", err)
		}
	})
}

func TestDirtySubmoduleAndMalformedPorcelainRejected(t *testing.T) {
	child, _, _ := newCaptureRepository(t, ".")
	parent, _, source := newCaptureRepository(t, ".")
	runCaptureGit(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", child, "module")
	runCaptureGit(t, parent, "commit", "-qam", "add submodule")
	base := captureGitLine(t, parent, "rev-parse", "HEAD")
	info, _ := gitx.Discover(t.Context(), parent)
	writeTestFile(t, filepath.Join(parent, "module", "z.txt"), "dirty child\n", 0o644)
	if _, err := (Capturer{}).CaptureDirty(t.Context(), dirtyRequest(t, parent, info.GitCommonDir, base, source)); !errors.Is(err, ErrSubmodule) {
		t.Fatalf("dirty submodule error = %v", err)
	}

	malformed := []string{
		"? ../escape\x00",
		"? \xff\x00",
		"2 R. N... 100644 100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + " R100 renamed\x00original\x00",
		"? unterminated",
	}
	for _, value := range malformed {
		if _, err := parsePorcelainV2(value); err == nil {
			t.Errorf("malformed porcelain parsed: %q", value)
		}
	}
}

func TestNestedSubmoduleRejected(t *testing.T) {
	grandchild, _, _ := newCaptureRepository(t, ".")
	child, _, _ := newCaptureRepository(t, ".")
	runCaptureGit(t, child, "-c", "protocol.file.allow=always", "submodule", "add", "-q", grandchild, "nested")
	runCaptureGit(t, child, "commit", "-qam", "nested submodule")
	parent, _, source := newCaptureRepository(t, ".")
	runCaptureGit(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", child, "module")
	runCaptureGit(t, parent, "commit", "-qam", "direct submodule")
	base := captureGitLine(t, parent, "rev-parse", "HEAD")
	info, _ := gitx.Discover(t.Context(), parent)
	writeTestFile(t, filepath.Join(parent, "dirty.txt"), "trigger capture", 0o644)
	if _, err := (Capturer{}).CaptureDirty(t.Context(), dirtyRequest(t, parent, info.GitCommonDir, base, source)); !errors.Is(err, ErrSubmodule) {
		t.Fatalf("nested submodule error = %v", err)
	}
}

func TestBundleTamperingFailsClosed(t *testing.T) {
	repository, base, source := newCaptureRepository(t, ".")
	info, _ := gitx.Discover(t.Context(), repository)
	writeTestFile(t, filepath.Join(repository, "untracked.txt"), "bundle-canary", 0o644)
	store := &BundleStore{Root: filepath.Join(canonicalCaptureTemp(t), "bundles")}
	result, err := (Capturer{Bundles: store}).CaptureDirty(t.Context(), dirtyRequest(t, repository, info.GitCommonDir, base, source))
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(result.Bundle.Directory, untrackedDir, payloadName(0))
	writeTestFile(t, payload, "tampered", 0o600)
	if _, err := store.Open(t.Context(), result.Bundle.Digest()); !errors.Is(err, ErrBundleInvalid) {
		t.Fatalf("tampered bundle open error = %v", err)
	}
	if err := store.Cleanup(t.Context(), *result.Bundle); !errors.Is(err, ErrBundleInvalid) {
		t.Fatalf("tampered bundle cleanup error = %v", err)
	}
}

type replacingFileSystem struct {
	OSFileSystem
	root   string
	target string
	once   sync.Once
}

func (filesystem *replacingFileSystem) OpenRegularFileNoFollow(root *os.Root, relative string) (*os.File, fs.FileInfo, error) {
	file, info, err := filesystem.OSFileSystem.OpenRegularFileNoFollow(root, relative)
	if err != nil {
		return nil, nil, err
	}
	if relative == filesystem.target {
		filesystem.once.Do(func() {
			replacement := filepath.Join(filesystem.root, ".replacement")
			_ = os.WriteFile(replacement, []byte("after!"), 0o644)
			_ = os.Rename(replacement, filepath.Join(filesystem.root, filesystem.target))
		})
	}
	return file, info, nil
}

func validCaptureSource(t *testing.T, id, subdir string) *research.Source {
	t.Helper()
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	source := &research.Source{
		Common: research.Common{Schema: research.SchemaSource, ID: mustCaptureID(t, id, research.KindSource), Title: "Source", CreatedAt: now, UpdatedAt: now},
		Key:    "source", Kind: research.SourceGit, Subdir: subdir, LocatorHints: []string{}, State: research.SourceActive,
	}
	if err := research.Validate(source); err != nil {
		t.Fatal(err)
	}
	return source
}

func newCaptureRepository(t *testing.T, subdir string) (string, string, *research.Source) {
	t.Helper()
	repository := filepath.Join(canonicalCaptureTemp(t), "source repo")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runCaptureGit(t, repository, "init", "-q")
	runCaptureGit(t, repository, "config", "user.name", "Snapshot Test")
	runCaptureGit(t, repository, "config", "user.email", "snapshot@example.invalid")
	if subdir == "" {
		subdir = "."
	}
	working := repository
	if subdir != "." {
		working = filepath.Join(repository, filepath.FromSlash(subdir))
		if err := os.MkdirAll(working, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(working, "z.txt"), "baseline\n", 0o644)
	runCaptureGit(t, repository, "add", "--", ".")
	runCaptureGit(t, repository, "commit", "-qm", "baseline")
	base := captureGitLine(t, repository, "rev-parse", "HEAD")
	return repository, base, validCaptureSource(t, "src_01a09000-0000-7002-8000-000000000002", subdir)
}

func TestDirtyCaptureRejectsCredentialBearingTrackedAndUntrackedPaths(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		name := "untracked"
		if tracked {
			name = "tracked"
		}
		t.Run(name, func(t *testing.T) {
			repository, base, source := newCaptureRepository(t, "services/api")
			info, err := gitx.Discover(t.Context(), repository)
			if err != nil {
				t.Fatal(err)
			}
			credentialPath := filepath.Join(repository, "services", "api", "access_token=CANARY")
			if tracked {
				writeTestFile(t, credentialPath, "baseline\n", 0o600)
				runCaptureGit(t, repository, "add", "--", "services/api/access_token=CANARY")
				runCaptureGit(t, repository, "commit", "-qm", "credential-shaped filename fixture")
				base = captureGitLine(t, repository, "rev-parse", "HEAD")
			}
			writeTestFile(t, credentialPath, "changed\n", 0o600)
			_, captureErr := (Capturer{}).CaptureDirty(t.Context(), dirtyRequest(t, repository, info.GitCommonDir, base, source))
			if captureErr == nil || !errors.Is(captureErr, research.ErrUnsafeText) {
				t.Fatalf("credential-shaped %s path was captured: %v", name, captureErr)
			}
		})
	}
}

func dirtyRequest(t *testing.T, repository, common, base string, source *research.Source) Request {
	t.Helper()
	return Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: common,
		BaseCommit: base, ExpectedHead: captureGitLine(t, repository, "rev-parse", "HEAD"),
		Try:        mustCaptureID(t, "try_01a09000-0000-7004-8000-000000000004", research.KindTry),
		CapturedAt: time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC),
		Limits:     DefaultLimits(),
	}
}

func mustCaptureID(t *testing.T, value string, kind research.Kind) research.ID {
	t.Helper()
	id, err := research.ParseIDForKind(value, kind)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func canonicalCaptureTemp(t *testing.T) string {
	t.Helper()
	value, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeTestFile(t *testing.T, filename, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filename, mode); err != nil {
		t.Fatal(err)
	}
}

func runCaptureGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", arguments, directory, err, output)
	}
	return string(output)
}

func captureGitLine(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	return strings.TrimSuffix(runCaptureGit(t, directory, arguments...), "\n")
}

func assertPrivateTree(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if info.Mode().Perm()&0o077 != 0 {
				t.Errorf("directory %s mode = %04o", name, info.Mode().Perm())
			}
		} else if info.Mode().Perm() != 0o600 {
			t.Errorf("file %s mode = %04o", name, info.Mode().Perm())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
