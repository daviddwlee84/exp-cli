package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillSyncFindsSourceFromNestedDirectoryChecksAndAtomicallyReplaces(t *testing.T) {
	repository := t.TempDir()
	target := filepath.Join(repository, filepath.FromSlash(commandReferenceSourcePath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := []byte("stale command reference without generated trailing bytes")
	if err := os.WriteFile(target, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repository, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	app := NewApp(t.Context(), nil, nil, nil)
	app.Getwd = func() (string, error) { return nested, nil }

	checked := invokeCommand(t, app, "", "skill", "sync", "--check")
	if !errors.Is(checked.err, errSkillSourceDrift) || !strings.Contains(checked.stdout, "Skill command reference drift at ") {
		t.Fatalf("skill sync --check = stdout %q stderr %q error %v", checked.stdout, checked.stderr, checked.err)
	}
	if after, err := os.ReadFile(target); err != nil || !bytes.Equal(after, stale) {
		t.Fatalf("skill sync --check mutated target: %q, %v", after, err)
	}

	synced := invokeCommand(t, app, "", "skill", "sync")
	if synced.err != nil || !strings.Contains(synced.stdout, "Synchronized skill command reference at ") {
		t.Fatalf("skill sync = stdout %q stderr %q error %v", synced.stdout, synced.stderr, synced.err)
	}
	expected, err := GenerateCommandReference(NewRootCommand(NewApp(t.Context(), nil, nil, nil)))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != expected {
		t.Fatalf("synchronized command reference differs: %v\n%s", err, actual)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("synchronized mode = %04o, want 0600", got)
	}

	currentIdentity := info
	current := invokeCommand(t, app, "", "skill", "sync", "--check")
	if current.err != nil || !strings.Contains(current.stdout, "Skill command reference is current at ") {
		t.Fatalf("current skill sync --check = stdout %q stderr %q error %v", current.stdout, current.stderr, current.err)
	}
	afterIdentity, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(currentIdentity, afterIdentity) {
		t.Fatal("current skill sync --check replaced the source file")
	}
}

func TestSkillSyncRequiresARealSourceTree(t *testing.T) {
	app := NewApp(t.Context(), nil, nil, nil)
	start := t.TempDir()
	app.Getwd = func() (string, error) { return start, nil }

	invocation := invokeCommand(t, app, "", "skill", "sync", "--check")
	if invocation.err == nil || !strings.Contains(invocation.err.Error(), "could not find "+filepath.FromSlash(commandReferenceSourcePath)) {
		t.Fatalf("skill sync outside source tree error = %v", invocation.err)
	}
}
