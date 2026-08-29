package skill_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/skill"
)

func TestInstallUnchangedReinstallIsNoOp(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	first := requireInstall(t, dir, false)
	embedded, err := skill.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Created) != len(embedded) || len(first.Updated) != 0 || !first.Changed {
		t.Fatalf("unexpected first result: %#v", first)
	}

	skillPath := filepath.Join(dir, "SKILL.md")
	oldTime := time.Unix(946684800, 0)
	if err := os.Chtimes(skillPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	second := requireInstall(t, dir, false)
	if second.Changed || len(second.Written) != 0 || len(second.Skipped) != len(embedded) {
		t.Fatalf("unchanged reinstall was not a no-op: %#v", second)
	}
	info, err := os.Stat(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Fatalf("no-op install changed mtime from %s to %s", oldTime, info.ModTime())
	}
}

func TestInstallUpdatesKnownFileByAtomicRename(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	requireInstall(t, dir, false)
	path := filepath.Join(dir, "SKILL.md")
	stale := []byte("stale known content\n")
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	oldHandle, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer oldHandle.Close()
	oldInfo, err := oldHandle.Stat()
	if err != nil {
		t.Fatal(err)
	}

	result := requireInstall(t, dir, false)
	if !slices.Contains(result.Updated, "SKILL.md") {
		t.Fatalf("SKILL.md not reported updated: %#v", result)
	}
	newInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(oldInfo, newInfo) {
		t.Fatal("known file retained its identity; update was not a rename replacement")
	}
	stillOld, err := io.ReadAll(oldHandle)
	if err != nil {
		t.Fatal(err)
	}
	if string(stillOld) != string(stale) {
		t.Fatalf("open old inode changed in place: %q", stillOld)
	}
	rendered, err := skill.Render()
	if err != nil {
		t.Fatal(err)
	}
	newContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(newContent) != rendered {
		t.Fatal("destination does not contain embedded SKILL.md")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".SKILL.md.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary files remain: %v", leftovers)
	}
}

func TestInstallThroughRootSymlinkUsesSelectedCanonicalTarget(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "skill-link")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	result := requireInstall(t, alias, false)
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if result.Dir != filepath.Clean(canonical) {
		t.Fatalf("result dir = %q, want canonical target %q", result.Dir, canonical)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("canonical target was not installed: %v", err)
	}
}

func TestInstallRefusesFilesystemRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if _, err := skill.Install(context.Background(), root, false); err == nil {
		t.Fatalf("Install accepted filesystem root %q", root)
	}
}

func TestCheckReportsExactCurrentMissingAndDriftedFiles(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	installed := requireInstall(t, dir, false)

	driftedContent := filepath.Join(dir, "references", "methodology.md")
	if err := os.WriteFile(driftedContent, []byte("locally edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "references", "external-tools.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	wrongMode := filepath.Join(dir, "references", "records-and-project-knowledge.md")
	mode := fs.FileMode(0o600)
	if runtime.GOOS == "windows" {
		mode = 0o444
	}
	if err := os.Chmod(wrongMode, mode); err != nil {
		t.Fatal(err)
	}

	check, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Current || check.ContentCurrent {
		t.Fatalf("drifted install reported current: %#v", check)
	}
	if !check.Schema.Compatible || !check.Version.Compatible {
		t.Fatalf("unchanged SKILL.md should remain version compatible: %#v %#v", check.Schema, check.Version)
	}
	if check.ManifestHash != installed.ManifestHash {
		t.Fatalf("manifest hash changed: install=%s check=%s", installed.ManifestHash, check.ManifestHash)
	}
	if check.ObservedManifestHash != "" {
		t.Fatalf("incomplete install should not have an observed manifest hash: %s", check.ObservedManifestHash)
	}
	if !slices.Equal(check.MissingFiles, []string{"references/external-tools.md"}) {
		t.Fatalf("missing files = %v", check.MissingFiles)
	}
	if !slices.Equal(check.DriftedFiles, []string{
		"references/methodology.md",
		"references/records-and-project-knowledge.md",
	}) {
		t.Fatalf("drifted files = %v", check.DriftedFiles)
	}
	expectedCurrent := []string{
		"SKILL.md",
		"references/commands.md",
		"references/usage-and-fallback.md",
	}
	if !slices.Equal(check.CurrentFiles, expectedCurrent) {
		t.Fatalf("current files = %v, want %v", check.CurrentFiles, expectedCurrent)
	}

	repaired := requireInstall(t, dir, false)
	if !slices.Equal(repaired.Created, []string{"references/external-tools.md"}) {
		t.Fatalf("repair did not recreate missing file: %#v", repaired)
	}
	if !slices.Equal(repaired.Updated, []string{
		"references/methodology.md",
		"references/records-and-project-knowledge.md",
	}) {
		t.Fatalf("repair updates = %v", repaired.Updated)
	}
	current, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
	if err != nil {
		t.Fatal(err)
	}
	if !current.Current || current.ObservedManifestHash != current.ManifestHash {
		t.Fatalf("repaired install not current: %#v", current)
	}
}

func TestCheckMissingDestinationDoesNotMutateFilesystem(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, "not-created", skill.Name)
	check, err := skill.Check(context.Background(), dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	embedded, err := skill.Files()
	if err != nil {
		t.Fatal(err)
	}
	if check.Current || len(check.MissingFiles) != len(embedded) || len(check.DriftedFiles) != 0 {
		t.Fatalf("unexpected missing check: %#v", check)
	}
	if _, err := os.Stat(filepath.Dir(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("check created destination parent or returned unexpected error: %v", err)
	}
	if len(check.Links) != 1 || check.Links[0].State != skill.LinkBaseMissing {
		t.Fatalf("missing consumer base status = %#v", check.Links)
	}
	if got := skill.ConsumerLinkLocations(); len(got) != 0 {
		t.Fatalf("missing bases should yield no consumer locations: %#v", got)
	}
	if got := skill.DefaultDir(); got != filepath.Join(home, ".agents", "skills", skill.Name) {
		t.Fatalf("DefaultDir = %q", got)
	}
}

func TestResolveDefaultDirFailsClosedWithoutHome(t *testing.T) {
	for _, name := range []string{"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "home"} {
		t.Setenv(name, "")
	}
	cwd := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	dir, err := skill.ResolveDefaultDir()
	if err == nil || dir != "" {
		t.Fatalf("ResolveDefaultDir = %q, %v; want empty path and error", dir, err)
	}
	if got := skill.DefaultDir(); got != "" {
		t.Fatalf("DefaultDir = %q, want empty path on home lookup failure", got)
	}
	if _, err := skill.Install(context.Background(), skill.DefaultDir(), false); err == nil {
		t.Fatal("Install accepted an unresolved empty default directory")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".agents")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing-home path created cwd-relative files or returned unexpected error: %v", err)
	}
}

func TestInstallDoesNotCreateMissingConsumerBase(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	result := requireInstall(t, dir, true)
	if len(result.LinkResults) != 1 || result.LinkResults[0].State != skill.LinkBaseMissing || result.LinkResults[0].Action != skill.LinkNotChanged {
		t.Fatalf("missing base link result = %#v", result.LinkResults)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("install created missing consumer hierarchy or returned unexpected error: %v", err)
	}
}

func TestInstallReportsAndPreservesNonDirectoryConsumerBase(t *testing.T) {
	home := isolatedHome(t)
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(claudeDir, "skills")
	sentinel := []byte("not a directory\n")
	if err := os.WriteFile(base, sentinel, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	result := requireInstall(t, dir, true)
	if len(result.LinkResults) != 1 || result.LinkResults[0].State != skill.LinkBaseNotDirectory || result.LinkResults[0].Action != skill.LinkNotChanged {
		t.Fatalf("non-directory base result = %#v", result.LinkResults)
	}
	content, err := os.ReadFile(base)
	if err != nil || string(content) != string(sentinel) {
		t.Fatalf("non-directory consumer base was replaced: content=%q err=%v", content, err)
	}
	check, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: true})
	if err != nil {
		t.Fatal(err)
	}
	if check.Current || check.LinksCurrent || len(check.Links) != 1 || check.Links[0].State != skill.LinkBaseNotDirectory {
		t.Fatalf("non-directory consumer base reported current: %#v", check)
	}
}

func TestInstallCreatesCorrectConsumerSymlinkAndReinstallIsNoOp(t *testing.T) {
	home := isolatedHome(t)
	base := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	first := requireInstall(t, dir, true)
	linkPath := filepath.Join(base, skill.Name)
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("read consumer link: %v", err)
	}
	if target != first.Dir {
		t.Fatalf("link target = %q, want canonical destination %q", target, first.Dir)
	}
	if !slices.Equal(first.Links, []string{linkPath}) || first.LinkResults[0].Action != skill.LinkCreated {
		t.Fatalf("unexpected link creation result: %#v", first)
	}
	if locations := skill.ConsumerLinkLocations(); len(locations) != 1 || locations[0].Path != linkPath {
		t.Fatalf("consumer locations = %#v", locations)
	}

	second := requireInstall(t, dir, true)
	if second.Changed || len(second.Links) != 0 || second.LinkResults[0].State != skill.LinkCorrect || second.LinkResults[0].Action != skill.LinkNotChanged {
		t.Fatalf("correct-link reinstall was not a no-op: %#v", second)
	}
	check, err := skill.Check(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Current || !check.LinksCurrent || check.Links[0].State != skill.LinkCorrect {
		t.Fatalf("correct link check = %#v", check)
	}
}

func TestInstallRefusesWrongSymlinkAndPreservesItsTarget(t *testing.T) {
	home := isolatedHome(t)
	base := filepath.Join(home, ".claude", "skills")
	oldTarget := filepath.Join(home, "old-exp-skill")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(oldTarget, "mine.txt")
	if err := os.WriteFile(marker, []byte("preserve me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(base, skill.Name)
	if err := os.Symlink(oldTarget, linkPath); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	result, err := skill.Install(context.Background(), dir, true)
	if err == nil {
		t.Fatal("Install replaced a wrong consumer symlink without compare-and-swap support")
	}
	if !result.Changed || len(result.LinkResults) != 1 || result.LinkResults[0].Action != skill.LinkNotChanged || result.LinkResults[0].State != skill.LinkWrongSymlink {
		t.Fatalf("wrong link refusal did not preserve partial result: %#v", result)
	}
	target, readErr := os.Readlink(linkPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if target != oldTarget {
		t.Fatalf("wrong symlink target changed to %q, want %q", target, oldTarget)
	}
	content, readErr := os.ReadFile(marker)
	if readErr != nil || string(content) != "preserve me\n" {
		t.Fatalf("old symlink target was changed: content=%q err=%v", content, readErr)
	}
}

func TestInstallPreservesRealConsumerDirectory(t *testing.T) {
	home := isolatedHome(t)
	existing := filepath.Join(home, ".claude", "skills", skill.Name)
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "user-owned.md")
	if err := os.WriteFile(marker, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	result := requireInstall(t, dir, true)
	if result.LinkResults[0].State != skill.LinkRealDirectory || result.LinkResults[0].Action != skill.LinkNotChanged {
		t.Fatalf("real directory status = %#v", result.LinkResults)
	}
	if _, err := os.ReadFile(marker); err != nil {
		t.Fatalf("real consumer directory was changed: %v", err)
	}
	if info, err := os.Lstat(existing); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("consumer target no longer a real directory: info=%v err=%v", info, err)
	}
	check, err := skill.Check(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if check.Current || !check.ContentCurrent || check.LinksCurrent {
		t.Fatalf("consumer conflict should affect only aggregate link status: %#v", check)
	}
}

func TestInstallPreservesRealConsumerFile(t *testing.T) {
	home := isolatedHome(t)
	base := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(base, skill.Name)
	sentinel := []byte("user-owned file\n")
	if err := os.WriteFile(existing, sentinel, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	result := requireInstall(t, dir, true)
	if result.LinkResults[0].State != skill.LinkRealFile || result.LinkResults[0].Action != skill.LinkNotChanged {
		t.Fatalf("real file status = %#v", result.LinkResults)
	}
	content, err := os.ReadFile(existing)
	if err != nil || !slices.Equal(content, sentinel) {
		t.Fatalf("real consumer file was changed: content=%q err=%v", content, err)
	}
	check, err := skill.Check(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if check.Current || !check.ContentCurrent || check.LinksCurrent || check.Links[0].State != skill.LinkRealFile {
		t.Fatalf("consumer file conflict was not reported: %#v", check)
	}
}

func TestInstallNeverDeletesUnknownUserFiles(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	unknownDir := filepath.Join(dir, "private")
	if err := os.MkdirAll(unknownDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(unknownDir, "user-notes.txt")
	if err := os.WriteFile(unknown, []byte("do not delete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeUnknown, err := os.Stat(unknown)
	if err != nil {
		t.Fatal(err)
	}
	requireInstall(t, dir, false)
	if err := os.WriteFile(filepath.Join(dir, "references", "commands.md"), []byte("drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requireInstall(t, dir, false)
	content, err := os.ReadFile(unknown)
	if err != nil || string(content) != "do not delete\n" {
		t.Fatalf("unknown file was changed: content=%q err=%v", content, err)
	}
	unknownInfo, err := os.Stat(unknown)
	if err != nil || unknownInfo.Mode().Perm() != beforeUnknown.Mode().Perm() {
		t.Fatalf("unknown file mode changed: before=%v after=%v err=%v", beforeUnknown.Mode().Perm(), unknownInfo, err)
	}
	check, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
	if err != nil {
		t.Fatal(err)
	}
	if !check.Current || !slices.Equal(check.UnknownFiles, []string{"private/user-notes.txt"}) {
		t.Fatalf("unknown-file check = %#v", check)
	}
}

func TestCheckReportsInterruptedInstallAndInstallCleansOnlyOwnedTemporaries(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	requireInstall(t, dir, false)

	rootTemporary := ".exp-0123456789abcdef0123456789abcdef.tmp"
	referenceTemporary := filepath.Join("references", ".exp-123.tmp")
	privateTemporary := filepath.Join("private", ".exp-fedcba9876543210fedcba9876543210.tmp")
	nearMiss := ".exp-not-a-writer-temp.tmp"
	for relative, content := range map[string]string{
		rootTemporary:      "orphaned root publication\n",
		referenceTemporary: "orphaned reference publication\n",
		privateTemporary:   "user file in an unowned parent\n",
		nearMiss:           "similarly named user file\n",
	} {
		absolute := filepath.Join(dir, relative)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	check, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
	if err != nil {
		t.Fatal(err)
	}
	if check.Current || check.ContentCurrent {
		t.Fatalf("writer-owned orphan temporaries reported current: %#v", check)
	}
	for _, relative := range []string{filepath.ToSlash(rootTemporary), filepath.ToSlash(referenceTemporary)} {
		if !slices.Contains(check.UnknownFiles, relative) {
			t.Fatalf("orphan temporary %q absent from unknown files: %v", relative, check.UnknownFiles)
		}
	}

	repaired := requireInstall(t, dir, false)
	wantRemoved := []string{filepath.ToSlash(rootTemporary), filepath.ToSlash(referenceTemporary)}
	if !repaired.Changed || !slices.Equal(repaired.RemovedTemporaryFiles, wantRemoved) {
		t.Fatalf("temporary cleanup = %v, want %v; result=%#v", repaired.RemovedTemporaryFiles, wantRemoved, repaired)
	}
	for _, relative := range []string{privateTemporary, nearMiss} {
		content, readErr := os.ReadFile(filepath.Join(dir, relative))
		if readErr != nil || len(content) == 0 {
			t.Fatalf("non-writer path %q was removed: content=%q err=%v", relative, content, readErr)
		}
	}
	after, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
	if err != nil {
		t.Fatal(err)
	}
	if !after.Current || !after.ContentCurrent {
		t.Fatalf("cleanup did not restore current state: %#v", after)
	}
}

func TestInstallRefusesNonregularWriterTemporary(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	requireInstall(t, dir, false)
	temporary := filepath.Join(dir, ".exp-0123456789abcdef0123456789abcdef.tmp")
	if err := os.Mkdir(temporary, 0o700); err != nil {
		t.Fatal(err)
	}

	check, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
	if err != nil {
		t.Fatal(err)
	}
	if check.Current || check.ContentCurrent {
		t.Fatalf("nonregular writer temporary reported current: %#v", check)
	}
	if _, err := skill.Install(context.Background(), dir, false); err == nil {
		t.Fatal("Install removed or accepted a nonregular writer temporary")
	}
	info, err := os.Lstat(temporary)
	if err != nil || !info.IsDir() {
		t.Fatalf("nonregular writer temporary was changed: info=%v err=%v", info, err)
	}
}

func TestInstallTemporaryCleanupDoesNotFollowOwnedParentSymlink(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	requireInstall(t, dir, false)
	references := filepath.Join(dir, "references")
	moved := filepath.Join(dir, "user-owned-references")
	if err := os.Rename(references, moved); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(moved, ".exp-0123456789abcdef0123456789abcdef.tmp")
	if err := os.WriteFile(temporary, []byte("preserve through symlink\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(moved), references); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := skill.Install(context.Background(), dir, false); err == nil {
		t.Fatal("Install followed a symlinked temporary parent")
	}
	content, err := os.ReadFile(temporary)
	if err != nil || string(content) != "preserve through symlink\n" {
		t.Fatalf("temporary behind symlink was changed: content=%q err=%v", content, err)
	}
}

func TestCheckReportsIncompatibleSkillVersionWithoutRepair(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	requireInstall(t, dir, false)
	path := filepath.Join(dir, "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(strings.Replace(string(content), `skill-version: "1"`, `skill-version: "999"`, 1))
	if slices.Equal(content, changed) {
		t.Fatal("test did not change skill-version metadata")
	}
	if err := os.WriteFile(path, changed, 0o644); err != nil {
		t.Fatal(err)
	}

	check, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
	if err != nil {
		t.Fatal(err)
	}
	if !check.Schema.Compatible || check.Version.Compatible || check.Version.Installed != "999" {
		t.Fatalf("compatibility = schema %#v, version %#v", check.Schema, check.Version)
	}
	if !slices.Contains(check.DriftedFiles, "SKILL.md") || check.Current {
		t.Fatalf("incompatible SKILL.md status = %#v", check)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(after, changed) {
		t.Fatal("Check repaired incompatible content")
	}
}

func TestCheckReportsWrongSymlinkWithoutReplacingIt(t *testing.T) {
	home := isolatedHome(t)
	base := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	requireInstall(t, dir, false)
	wrongTarget := filepath.Join(home, "independent-skill")
	if err := os.MkdirAll(wrongTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(base, skill.Name)
	if err := os.Symlink(wrongTarget, linkPath); err != nil {
		t.Fatal(err)
	}

	check, err := skill.Check(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if check.LinksCurrent || len(check.Links) != 1 || check.Links[0].State != skill.LinkWrongSymlink || check.Links[0].Action != skill.LinkNotChanged {
		t.Fatalf("wrong link check = %#v", check.Links)
	}
	after, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if after != wrongTarget {
		t.Fatalf("Check replaced link with %q", after)
	}
}

func TestInstallAppliesFileAndDirectoryPermissions(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	requireInstall(t, dir, false)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, "references"), 0o777); err != nil {
			t.Fatal(err)
		}
		drifted, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
		if err != nil {
			t.Fatal(err)
		}
		if drifted.Current || drifted.ContentCurrent || drifted.DirectoryModesCurrent || !slices.Equal(drifted.DriftedDirectories, []string{".", "references"}) {
			t.Fatalf("directory mode drift was not reported: %#v", drifted)
		}
		repaired := requireInstall(t, dir, false)
		if !repaired.Changed || !slices.Equal(repaired.RepairedDirectories, []string{".", "references"}) {
			t.Fatalf("directory repairs were not reported: %#v", repaired)
		}
		current, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
		if err != nil {
			t.Fatal(err)
		}
		if !current.Current || !current.DirectoryModesCurrent || len(current.DriftedDirectories) != 0 {
			t.Fatalf("directory mode repair did not converge: %#v", current)
		}
	}
	fileMode := fs.FileMode(0o644)
	if runtime.GOOS == "windows" {
		fileMode = 0o666
	}
	for _, entry := range []struct {
		path string
		mode fs.FileMode
	}{
		{dir, 0o755},
		{filepath.Join(dir, "references"), 0o755},
		{filepath.Join(dir, "SKILL.md"), fileMode},
		{filepath.Join(dir, "references", "commands.md"), fileMode},
	} {
		info, err := os.Stat(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" && info.IsDir() {
			continue
		}
		if info.Mode().Perm() != entry.mode {
			t.Errorf("%s mode = %04o, want %04o", entry.path, info.Mode().Perm(), entry.mode)
		}
	}
}

func TestConcurrentInstallsSerializeAndConverge(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", skill.Name)
	const workers = 12
	errorsByWorker := make([]error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := range workers {
		go func() {
			defer wait.Done()
			_, errorsByWorker[worker] = skill.Install(context.Background(), dir, false)
		}()
	}
	wait.Wait()
	for worker, err := range errorsByWorker {
		if err != nil {
			t.Errorf("worker %d: %v", worker, err)
		}
	}
	check, err := skill.CheckWithOptions(context.Background(), dir, skill.CheckOptions{Links: false})
	if err != nil {
		t.Fatal(err)
	}
	if !check.Current || check.ObservedManifestHash != check.ManifestHash {
		t.Fatalf("concurrent installs did not converge: %#v", check)
	}
}

func requireInstall(t *testing.T, dir string, links bool) skill.InstallResult {
	t.Helper()
	result, err := skill.Install(context.Background(), dir, links)
	if err != nil {
		t.Fatalf("Install(%s): %v", dir, err)
	}
	return result
}

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}
