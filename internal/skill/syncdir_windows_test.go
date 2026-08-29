//go:build windows

package skill

import (
	"io/fs"
	"os"
	"testing"
)

func TestWindowsFileModePolicyUsesRepresentableWritableMode(t *testing.T) {
	if got := expectedInstalledFileMode(); got != fs.FileMode(0o666) {
		t.Fatalf("expected file mode = %04o, want 0666", got)
	}
	if !installedFileModeCurrent(0o666) {
		t.Fatal("writable Windows regular-file mode should be current")
	}
	if installedFileModeCurrent(0o444) {
		t.Fatal("read-only Windows regular-file mode should be drifted")
	}
}

func TestDirectorySyncIsPortableNoOpOnWindows(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := syncRootDirectory(root, "."); err != nil {
		t.Fatalf("sync rooted directory: %v", err)
	}
	if err := syncDirectory(dir); err != nil {
		t.Fatalf("sync directory path: %v", err)
	}
}
