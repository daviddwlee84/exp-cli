package record

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicTempNameMatchesOnlyCurrentAndLegacyWriterNamespaces(t *testing.T) {
	for _, name := range []string{".exp-0123456789abcdef0123456789abcdef.tmp", ".exp-1532303050.tmp", ".exp-0.tmp"} {
		if !IsAtomicTempName(name) {
			t.Errorf("writer temporary %q was not recognized", name)
		}
	}
	for _, name := range []string{".exp-.tmp", ".exp-abandoned.tmp", ".exp-0123456789ABCDEf0123456789abcdef.tmp", ".exp-0123456789abcdef.tmp", "nested/.exp-0123456789abcdef0123456789abcdef.tmp"} {
		if IsAtomicTempName(name) {
			t.Errorf("non-writer temporary %q was recognized", name)
		}
	}
}

func TestAtomicWriteFailureBoundariesAndModes(t *testing.T) {
	stages := []AtomicStage{StageTempCreate, StageTempWrite, StageFileSync, StageRename, StageDirSync}
	for _, stage := range stages {
		stage := stage
		t.Run(string(stage), func(t *testing.T) {
			root := t.TempDir()
			sentinel := errors.New("injected " + string(stage))
			err := AtomicWrite(root, "plans/record.md", []byte("new bytes\n"), AtomicWriteOptions{Hook: func(got AtomicStage, _ string) error {
				if got == stage {
					return sentinel
				}
				return nil
			}})
			if !errors.Is(err, sentinel) {
				t.Fatalf("AtomicWrite error = %v", err)
			}
			var publication *PublicationError
			if !errors.As(err, &publication) || publication.Stage != stage {
				t.Fatalf("publication error = %#v", publication)
			}
			destination := filepath.Join(root, "plans", "record.md")
			data, readErr := os.ReadFile(destination)
			if stage == StageDirSync {
				if !publication.Published || readErr != nil || string(data) != "new bytes\n" {
					t.Fatalf("post-rename state = published %v, data %q, err %v", publication.Published, data, readErr)
				}
				assertRecordMode(t, destination, 0o644)
			} else if !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("pre-rename failure published destination: %q, %v", data, readErr)
			}
			if matches, _ := filepath.Glob(filepath.Join(root, "plans", ".exp-*.tmp")); len(matches) != 0 {
				t.Fatalf("temporary files remain: %v", matches)
			}
		})
	}
}

func TestAtomicWriteReplacementChecksIdentity(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "plans", "record.md")
	if err := os.Mkdir(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(root, "plans/record.md", []byte("new\n"), AtomicWriteOptions{Expected: identity, ExpectedContent: []byte("old\n")}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	data, _ := os.ReadFile(destination)
	if string(data) != "new\n" {
		t.Fatalf("replacement data = %q", data)
	}
	assertRecordMode(t, destination, 0o644)

	staleIdentity, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(destination, destination+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(root, "plans/record.md", []byte("must-not-win\n"), AtomicWriteOptions{Expected: staleIdentity, ExpectedContent: []byte("new\n")}); err == nil {
		t.Fatal("replacement accepted changed file identity")
	}
	data, _ = os.ReadFile(destination)
	if string(data) != "unrelated\n" {
		t.Fatalf("unrelated replacement was overwritten: %q", data)
	}
}

func TestAtomicWriteCreateNeverClobbersRacingDestination(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "plans", "record.md")
	err := AtomicWrite(root, "plans/record.md", []byte("publisher\n"), AtomicWriteOptions{Hook: func(stage AtomicStage, _ string) error {
		if stage == StageRename {
			return os.WriteFile(destination, []byte("racing writer\n"), 0o644)
		}
		return nil
	}})
	if err == nil {
		t.Fatal("create replaced a racing destination")
	}
	content, readErr := os.ReadFile(destination)
	if readErr != nil || string(content) != "racing writer\n" {
		t.Fatalf("racing destination = %q, %v", content, readErr)
	}
}

func TestAtomicWriteUpdateDetectsAndPreservesInPlaceEdit(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "plans", "record.md")
	if err := os.Mkdir(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	identity, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	err = AtomicWrite(root, "plans/record.md", []byte("replacement\n"), AtomicWriteOptions{
		Expected:        identity,
		ExpectedContent: []byte("old\n"),
		Hook: func(stage AtomicStage, _ string) error {
			if stage == StageRename {
				return os.WriteFile(destination, []byte("external in-place edit\n"), 0o644)
			}
			return nil
		},
	})
	if !errors.Is(err, ErrAtomicConflict) {
		t.Fatalf("in-place edit error = %v", err)
	}
	content, readErr := os.ReadFile(destination)
	if readErr != nil || string(content) != "external in-place edit\n" {
		t.Fatalf("in-place edit was not preserved: %q, %v", content, readErr)
	}
}

func TestAtomicWriteParentSwapCannotRedirectPublication(t *testing.T) {
	root := t.TempDir()
	plans := filepath.Join(root, "plans")
	if err := os.Mkdir(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved-plans")
	err := AtomicWrite(root, "plans/record.md", []byte("must stay contained\n"), AtomicWriteOptions{Hook: func(stage AtomicStage, _ string) error {
		if stage != StageRename {
			return nil
		}
		if err := os.Rename(plans, moved); err != nil {
			return err
		}
		return os.Symlink(moved, plans)
	}})
	if err == nil {
		t.Fatal("parent swap was accepted")
	}
	if _, statErr := os.Lstat(filepath.Join(moved, "record.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("publication escaped through moved parent: %v", statErr)
	}
	if matches, _ := filepath.Glob(filepath.Join(moved, ".exp-*.tmp")); len(matches) != 0 {
		t.Fatalf("temporary files remained in moved parent: %v", matches)
	}
}

func TestAtomicWriteRejectsTemporaryPathSubstitutionAndPreservesBytes(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "create"
		if replacement {
			name = "replace"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "plans")
			if err := os.Mkdir(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(parent, "record.md")
			options := AtomicWriteOptions{}
			if replacement {
				if err := os.WriteFile(destination, []byte("old canonical bytes\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				identity, err := os.Lstat(destination)
				if err != nil {
					t.Fatal(err)
				}
				options.Expected = identity
				options.ExpectedContent = []byte("old canonical bytes\n")
			}
			outside := filepath.Join(t.TempDir(), "outside")
			outsideBytes := []byte("outside bytes\n")
			if err := os.WriteFile(outside, outsideBytes, 0o644); err != nil {
				t.Fatal(err)
			}
			intendedBytes := []byte("intended publication bytes\n")
			intendedCopy := filepath.Join(parent, "intended.saved")
			var substitutedTemp string
			options.Hook = func(stage AtomicStage, _ string) error {
				if stage != StageRename {
					return nil
				}
				matches, err := filepath.Glob(filepath.Join(parent, ".exp-*.tmp"))
				if err != nil || len(matches) != 1 {
					return fmt.Errorf("find publication temporary: %v (%v)", err, matches)
				}
				substitutedTemp = matches[0]
				if err := os.Rename(matches[0], intendedCopy); err != nil {
					return err
				}
				return os.Link(outside, matches[0])
			}
			err := AtomicWrite(root, "plans/record.md", intendedBytes, options)
			if !errors.Is(err, ErrAtomicConflict) {
				t.Fatalf("temporary substitution error = %v", err)
			}
			if got, readErr := os.ReadFile(intendedCopy); readErr != nil || string(got) != string(intendedBytes) {
				t.Fatalf("intended bytes = %q, %v", got, readErr)
			}
			if got, readErr := os.ReadFile(outside); readErr != nil || string(got) != string(outsideBytes) {
				t.Fatalf("outside bytes = %q, %v", got, readErr)
			}
			if got, readErr := os.ReadFile(substitutedTemp); readErr != nil || string(got) != string(outsideBytes) {
				t.Fatalf("substituted temporary was removed or changed: %q, %v", got, readErr)
			}
			if replacement {
				if got, readErr := os.ReadFile(destination); readErr != nil || string(got) != "old canonical bytes\n" {
					t.Fatalf("replacement destination = %q, %v", got, readErr)
				}
			} else if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("create destination exists after substitution: %v", statErr)
			}
		})
	}
}

func TestAtomicWriteRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for _, relative := range []string{"../outside.md", "/absolute.md", `C:\\outside.md`} {
		if err := AtomicWrite(root, relative, []byte("x"), AtomicWriteOptions{}); err == nil {
			t.Errorf("unsafe destination %q accepted", relative)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "plans")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := AtomicWrite(root, "plans/escape.md", []byte("x"), AtomicWriteOptions{}); err == nil {
		t.Fatal("symlink escape accepted")
	}
	if _, err := os.Lstat(filepath.Join(outside, "escape.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file created: %v", err)
	}
}

func assertRecordMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
