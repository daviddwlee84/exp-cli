//go:build unix

package skill_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/skill"
)

func TestRequestedConsumerLinkCheckFailsOnInaccessibleBase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	base := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	if _, err := skill.Install(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(home, ".claude")
	if err := os.Chmod(claudeDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(claudeDir, 0o755) })
	if _, err := os.Stat(base); err == nil {
		t.Skip("current user can stat mode-000 directory; permission denial unavailable")
	}

	result, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: true})
	if err == nil {
		t.Fatal("requested consumer check accepted an inaccessible base")
	}
	if result.Current || result.LinksCurrent {
		t.Fatalf("inaccessible consumer base reported current: %#v", result)
	}
	if len(result.Links) != 1 || result.Links[0].State != skill.LinkOther {
		t.Fatalf("inaccessible consumer result = %#v", result.Links)
	}
}

func TestInstallReplacesModeDriftWithoutChmoddingExternalHardlink(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "skill")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := skill.Files()
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "user-owned.md")
	if err := os.WriteFile(outside, files["SKILL.md"], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0o600); err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(dir, "SKILL.md")
	if err := os.Link(outside, installedPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	outsideBefore, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}

	result, err := skill.Install(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !slices.Contains(result.Updated, "SKILL.md") {
		t.Fatalf("mode drift was not replaced: %#v", result)
	}
	outsideAfter, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if outsideAfter.Mode().Perm() != 0o600 {
		t.Fatalf("external hardlink mode = %04o, want 0600", outsideAfter.Mode().Perm())
	}
	installedAfter, err := os.Stat(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(outsideBefore, installedAfter) {
		t.Fatal("installed file still aliases the user-owned inode")
	}
	if installedAfter.Mode().Perm() != 0o644 {
		t.Fatalf("installed mode = %04o, want 0644", installedAfter.Mode().Perm())
	}
}

func TestInstallRepairsFreshDirectoryModesUnderRestrictiveUmask(t *testing.T) {
	parent := t.TempDir()
	oldMask := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(oldMask) })

	dir := filepath.Join(parent, "skill")
	result, err := skill.Install(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || len(result.RepairedDirectories) != 2 || result.RepairedDirectories[0] != "." || result.RepairedDirectories[1] != "references" {
		t.Fatalf("restrictive-umask repairs were not reported: %#v", result)
	}
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{
		{dir, 0o755},
		{filepath.Join(dir, "references"), 0o755},
		{filepath.Join(dir, "SKILL.md"), 0o644},
		{filepath.Join(dir, "references", "commands.md"), 0o644},
		{filepath.Join(parent, ".skill.lock"), 0o600},
	} {
		info, err := os.Stat(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != entry.mode {
			t.Fatalf("%s mode = %04o, want %04o", entry.path, got, entry.mode)
		}
	}
}
