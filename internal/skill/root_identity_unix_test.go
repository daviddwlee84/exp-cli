//go:build unix

package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallDetectsCanonicalRootReplacementDuringPublication(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "skill")
	moved := filepath.Join(parent, "moved-skill")
	operations := defaultInstallOperations()
	swapped := false
	operations.syncRootDirectory = func(_ *os.Root, _ string) error {
		if swapped {
			return nil
		}
		swapped = true
		if err := os.Rename(dir, moved); err != nil {
			return err
		}
		return os.Mkdir(dir, 0o755)
	}

	result, err := install(context.Background(), dir, false, operations)
	if err == nil {
		t.Fatal("Install reported success after the canonical root was replaced")
	}
	if !result.Changed || len(result.Written) != 1 || result.Written[0] != "SKILL.md" {
		t.Fatalf("published write was not reported as partial: %#v", result)
	}
	if _, statErr := os.Stat(filepath.Join(moved, "SKILL.md")); statErr != nil {
		t.Fatalf("selected directory did not receive the published file: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "SKILL.md")); !os.IsNotExist(statErr) {
		t.Fatalf("replacement directory received a file: %v", statErr)
	}
}
