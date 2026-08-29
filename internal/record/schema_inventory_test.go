package record

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSchemaInventoryDiagnosesNestedFilesInEveryReservedTree(t *testing.T) {
	parents := []string{
		PlansDir,
		FindingsDir,
		DecisionsDir,
		"e-01a01e67-calibrate-encoder-learning-rate/runs",
		"e-01a01e67-calibrate-encoder-learning-rate/attempts",
	}
	for _, parent := range parents {
		t.Run(parent, func(t *testing.T) {
			root := copyValidProjectForSchemaTest(t)
			relative := filepath.Join(parent, "archive", "hidden.md")
			absolute := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absolute, []byte("not canonical\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			inventory, err := LoadInventory(root)
			if err != nil {
				t.Fatal(err)
			}
			if !hasDiagnosticAt(inventory.Diagnostics, filepath.ToSlash(relative), "record.invalid_path") {
				t.Fatalf("nested file was silently ignored: %v", inventory.Diagnostics)
			}
		})
	}
}

func TestSchemaInventoryIgnoresOnlyExactWriterTemporaryFiles(t *testing.T) {
	root := copyValidProjectForSchemaTest(t)
	attempts := filepath.Join(root, "e-01a01e67-calibrate-encoder-learning-rate", "attempts")
	for _, name := range []string{".exp-0123456789abcdef0123456789abcdef.tmp", ".exp-1234567890.tmp"} {
		if err := os.WriteFile(filepath.Join(attempts, name), []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Valid() {
		t.Fatalf("writer-owned temporary poisoned inventory: %v", inventory.Diagnostics)
	}

	for _, name := range []string{".DS_Store", ".keep", ".exp-not-writer.tmp", ".exp-0123456789abcdef0123456789abcdeg.tmp"} {
		t.Run(name, func(t *testing.T) {
			caseRoot := copyValidProjectForSchemaTest(t)
			path := filepath.Join(caseRoot, PlansDir, name)
			if err := os.WriteFile(path, []byte("unrelated\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := LoadInventory(caseRoot)
			if err != nil {
				t.Fatal(err)
			}
			if !hasDiagnosticAt(got.Diagnostics, filepath.ToSlash(filepath.Join(PlansDir, name)), "record.invalid_path") {
				t.Fatalf("arbitrary dotfile was ignored: %v", got.Diagnostics)
			}
		})
	}

	t.Run("temp-shaped file outside writer parent", func(t *testing.T) {
		caseRoot := copyValidProjectForSchemaTest(t)
		const name = ".exp-0123456789abcdef0123456789abcdef.tmp"
		relative := filepath.Join(PlansDir, "archive", name)
		path := filepath.Join(caseRoot, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not writer-owned here\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadInventory(caseRoot)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiagnosticAt(got.Diagnostics, filepath.ToSlash(relative), "record.invalid_path") {
			t.Fatalf("temp-shaped nested file was ignored: %v", got.Diagnostics)
		}
	})

	t.Run("temp-shaped symlink", func(t *testing.T) {
		caseRoot := copyValidProjectForSchemaTest(t)
		const name = ".exp-0123456789abcdef0123456789abcdef.tmp"
		path := filepath.Join(caseRoot, PlansDir, name)
		if err := os.Symlink(t.TempDir(), path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		got, err := LoadInventory(caseRoot)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiagnosticAt(got.Diagnostics, filepath.ToSlash(filepath.Join(PlansDir, name)), "path.symlink") {
			t.Fatalf("temp-shaped symlink was ignored: %v", got.Diagnostics)
		}
	})
}

func TestSchemaInventoryRejectsExistingSymlinkPrefixesOutsideWorktree(t *testing.T) {
	t.Run("Run expected output", func(t *testing.T) {
		root := copyValidProjectForSchemaTest(t)
		repository := filepath.Dir(root)
		if err := os.Symlink(t.TempDir(), filepath.Join(repository, "artifacts")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		inventory, err := LoadInventory(root)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiagnosticCode(inventory.Diagnostics, "path.outside_worktree") {
			t.Fatalf("outside expected-output prefix validated: %v", inventory.Diagnostics)
		}
	})

	t.Run("in-memory candidate snapshot", func(t *testing.T) {
		root := copyValidProjectForSchemaTest(t)
		baseline, err := LoadInventory(root)
		if err != nil || !baseline.Valid() {
			t.Fatalf("load baseline: %v, %v", err, baseline.Diagnostics)
		}
		repository := filepath.Dir(root)
		if err := os.Symlink(t.TempDir(), filepath.Join(repository, "artifacts")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		candidate := InventoryFromDocuments(root, cloneDocuments(baseline.Documents))
		if !hasDiagnosticCode(candidate.Diagnostics, "path.outside_worktree") {
			t.Fatalf("candidate inventory accepted outside prefix: %v", candidate.Diagnostics)
		}
	})

	t.Run("Attempt cwd", func(t *testing.T) {
		root := copyValidProjectForSchemaTest(t)
		repository := filepath.Dir(root)
		if err := os.Symlink(t.TempDir(), filepath.Join(repository, "workspace-link")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		attemptPath := filepath.Join(root, "e-01a01e67-calibrate-encoder-learning-rate", "attempts", "att_01a01e69-b800-7505-8000-000000000505.md")
		data, err := os.ReadFile(attemptPath)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.Replace(data, []byte(`cwd = "."`), []byte(`cwd = "workspace-link"`), 1)
		if err := os.WriteFile(attemptPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		inventory, err := LoadInventory(root)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiagnosticCode(inventory.Diagnostics, "path.outside_worktree") {
			t.Fatalf("outside cwd prefix validated: %v", inventory.Diagnostics)
		}
	})
}

func TestSchemaInventoryAllowsMissingLeavesAndSymlinksContainedInWorktree(t *testing.T) {
	root := copyValidProjectForSchemaTest(t)
	repository := filepath.Dir(root)
	target := filepath.Join(repository, "real-artifacts")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repository, "artifacts")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	inventory, err := LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Valid() {
		t.Fatalf("contained symlink with missing output leaf rejected: %v", inventory.Diagnostics)
	}
}

func TestSchemaInventoryValidatesCanonicalFilePermissionsPortably(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose portable Unix permission bits")
	}
	root := copyValidProjectForSchemaTest(t)
	planPath := filepath.Join(root, PlansDir, "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md")
	if err := os.Chmod(planPath, 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnosticAt(inventory.Diagnostics, filepath.ToSlash(filepath.Join(PlansDir, filepath.Base(planPath))), "record.permissions") {
		t.Fatalf("noncanonical permissions accepted: %v", inventory.Diagnostics)
	}
}

func copyValidProjectForSchemaTest(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	root := filepath.Join(repository, "experiments")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join("..", "..", "testdata", "v1", "valid-project"), root, false)
	return root
}

func hasDiagnosticAt(diagnostics []Diagnostic, path, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Path == path && diagnostic.Code == code {
			return true
		}
	}
	return false
}
