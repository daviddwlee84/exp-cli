package record

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

type fixtureCase struct {
	Base          string   `toml:"base"`
	Mode          string   `toml:"mode"`
	ExpectedCodes []string `toml:"expected_codes"`
}

func TestLoadInventoryValidFixture(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "v1", "valid-project")
	inventory, err := LoadInventory(root)
	if err != nil {
		t.Fatalf("LoadInventory: %v", err)
	}
	if err := inventory.Error(); err != nil {
		t.Fatalf("valid fixture diagnostics: %v", err)
	}
	if len(inventory.Documents) != 7 {
		t.Fatalf("documents = %d, want 7", len(inventory.Documents))
	}
	if inventory.Project == nil || inventory.Project.Path != ProjectFile {
		t.Fatalf("project = %#v", inventory.Project)
	}
}

func TestMalformedFixtureDiagnostics(t *testing.T) {
	malformedRoot := filepath.Join("..", "..", "testdata", "v1", "malformed")
	entries, err := os.ReadDir(malformedRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entry := entry
		t.Run(entry.Name(), func(t *testing.T) {
			caseDir := filepath.Join(malformedRoot, entry.Name())
			var fixture fixtureCase
			if _, err := toml.DecodeFile(filepath.Join(caseDir, "case.toml"), &fixture); err != nil {
				t.Fatal(err)
			}
			if fixture.Mode != "overlay" {
				t.Fatalf("unsupported fixture mode %q", fixture.Mode)
			}
			root := t.TempDir()
			copyTree(t, filepath.Clean(filepath.Join(caseDir, fixture.Base)), root, false)
			copyTree(t, caseDir, root, true)
			inventory, err := LoadInventory(root)
			if err != nil {
				t.Fatalf("LoadInventory: %v", err)
			}
			var codes []string
			for _, diagnostic := range inventory.Diagnostics {
				codes = append(codes, diagnostic.Code)
			}
			for _, expected := range fixture.ExpectedCodes {
				if !slices.Contains(codes, expected) {
					t.Errorf("missing diagnostic %q; got %v", expected, inventory.Diagnostics)
				}
			}
		})
	}
}

func TestLoadInventoryHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inventory, err := LoadInventoryContext(ctx, filepath.Join("..", "..", "testdata", "v1", "valid-project"))
	if !errors.Is(err, context.Canceled) || inventory != nil {
		t.Fatalf("canceled inventory = %#v, %v", inventory, err)
	}
}

func TestLoadInventoryRejectsOversizedCanonicalRecord(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "testdata", "v1", "valid-project"), root, false)
	if err := os.Truncate(filepath.Join(root, ProjectFile), MaxRecordBytes+1); err != nil {
		t.Fatal(err)
	}
	inventory, err := LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnosticAt(inventory.Diagnostics, ProjectFile, "record.size") {
		t.Fatalf("oversized Project diagnostics = %v", inventory.Diagnostics)
	}
}

func TestLoadInventoryRejectsCanonicalFileSwapWithoutReadingTarget(t *testing.T) {
	rootPath := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "testdata", "v1", "valid-project"), rootPath, false)
	outside := filepath.Join(t.TempDir(), "outside")
	outsideContent := []byte("outside bytes must remain unread and unchanged\n")
	if err := os.WriteFile(outside, outsideContent, 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := pathx.Canonical(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootPath = canonicalRoot
	root, err := pathx.OpenCanonicalRootNoSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	swapped := false
	inventory, err := loadInventoryRoot(context.Background(), root, rootPath, func(relative string) {
		if relative != ProjectFile || swapped {
			return
		}
		swapped = true
		if renameErr := os.Rename(filepath.Join(rootPath, ProjectFile), filepath.Join(rootPath, "PROJECT.saved")); renameErr != nil {
			t.Fatal(renameErr)
		}
		if symlinkErr := os.Symlink(outside, filepath.Join(rootPath, ProjectFile)); symlinkErr != nil {
			t.Fatal(symlinkErr)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnosticAt(inventory.Diagnostics, ProjectFile, "path.symlink") || inventory.Project != nil {
		t.Fatalf("swapped Project inventory = %#v", inventory.Diagnostics)
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != string(outsideContent) {
		t.Fatalf("outside file changed: %q, %v", content, err)
	}
}

func TestLoadInventoryRejectsRootSwapDuringScan(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "experiments")
	copyTree(t, filepath.Join("..", "..", "testdata", "v1", "valid-project"), rootPath, false)
	canonicalRoot, err := pathx.Canonical(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootPath = canonicalRoot
	root, err := pathx.OpenCanonicalRootNoSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	moved := filepath.Join(parent, "moved-experiments")
	swapped := false
	inventory, err := loadInventoryRoot(context.Background(), root, rootPath, func(relative string) {
		if relative != ProjectFile || swapped {
			return
		}
		swapped = true
		if renameErr := os.Rename(rootPath, moved); renameErr != nil {
			t.Fatal(renameErr)
		}
		if mkdirErr := os.Mkdir(rootPath, 0o755); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
	})
	if err == nil || inventory != nil {
		t.Fatalf("root-swapped inventory = %#v, %v", inventory, err)
	}
}

func TestLoadInventoryPrunesUnrelatedTopLevelTrees(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permission bits cannot make a directory unreadable")
	}
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "testdata", "v1", "valid-project"), root, false)
	unrelated := filepath.Join(root, "vendor", "unreadable")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "PROJECT.md"), []byte("not canonical here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "vendor"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "vendor"), 0o755) })
	inventory, err := LoadInventory(root)
	if err != nil || !inventory.Valid() {
		t.Fatalf("unrelated subtree affected inventory: %#v, %v", inventory, err)
	}
}

func copyTree(t *testing.T, source, destination string, skipCase bool) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if skipCase && relative == "case.toml" {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
