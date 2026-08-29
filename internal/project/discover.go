// Package project discovers and initializes the one canonical experiments root
// belonging to a Git working tree.
package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

var (
	ErrNotInitialized = errors.New("exp project is not initialized")
	ErrMultipleRoots  = errors.New("multiple exp v1 roots in one Git repository")
	ErrLegacyRoot     = errors.New("legacy experiments root requires explicit migration")
	ErrUnrelatedRoot  = errors.New("existing experiments root is not an exp v1 root")
)

// Info identifies the current checkout, its unique experiments root, and Project record.
type Info struct {
	Repository gitx.Repository
	Root       string
	Document   *record.Document
}

func (info *Info) Project() *research.Project {
	if info == nil || info.Document == nil {
		return nil
	}
	project, _ := info.Document.Record.(*research.Project)
	return project
}

// Discover requires a Git worktree and a valid marker at exactly
// <worktree>/experiments/PROJECT.md.
func Discover(ctx context.Context, start string) (*Info, error) {
	return DiscoverWithGit(ctx, start, gitx.ExecRunner{})
}

// DiscoverWithGit exposes only Git process execution for deterministic tests.
func DiscoverWithGit(ctx context.Context, start string, runner gitx.Runner) (*Info, error) {
	repository, err := gitx.DiscoverWithRunner(ctx, start, runner)
	if err != nil {
		return nil, err
	}
	return discoverInRepository(ctx, repository)
}

func discoverInRepository(ctx context.Context, repository gitx.Repository) (*Info, error) {
	root, err := pathx.Canonical(repository.Root)
	if err != nil {
		return nil, fmt.Errorf("canonicalize Git worktree root: %w", err)
	}
	repository.Root = root
	marker, found, err := readDefaultMarker(ctx, root)
	if err != nil {
		return nil, err
	}
	if found {
		return &Info{Repository: repository, Root: marker.Root, Document: marker.Document}, nil
	}
	defaultRoot := filepath.Join(root, "experiments")
	classification, err := classifyUnmarkedRoot(defaultRoot)
	if err != nil {
		return nil, err
	}
	switch classification {
	case rootLegacy:
		return nil, fmt.Errorf("%s: %w", defaultRoot, ErrLegacyRoot)
	case rootUnrelated:
		return nil, fmt.Errorf("%s: %w", defaultRoot, ErrUnrelatedRoot)
	default:
		return nil, ErrNotInitialized
	}
}

type marker struct {
	Root     string
	Document *record.Document
	Content  []byte
	Identity fs.FileInfo
}

func readDefaultMarker(ctx context.Context, repositoryRoot string) (marker, bool, error) {
	return readDefaultMarkerWithHook(ctx, repositoryRoot, nil)
}

func readDefaultMarkerWithHook(ctx context.Context, repositoryRoot string, beforeRead func()) (marker, bool, error) {
	root, err := pathx.OpenCanonicalRootNoSymlinks(repositoryRoot)
	if err != nil {
		return marker{}, false, fmt.Errorf("open Git worktree root: %w", err)
	}
	defer root.Close()
	experimentsInfo, err := root.Lstat("experiments")
	if errors.Is(err, fs.ErrNotExist) {
		if verifyErr := pathx.VerifyRootPath(repositoryRoot, root); verifyErr != nil {
			return marker{}, false, fmt.Errorf("Git worktree root changed during Project discovery: %w", verifyErr)
		}
		return marker{}, false, nil
	}
	if err != nil {
		return marker{}, false, fmt.Errorf("inspect experiments root: %w", err)
	}
	if experimentsInfo.Mode()&os.ModeSymlink != 0 || !experimentsInfo.IsDir() {
		return marker{}, false, fmt.Errorf("experiments root is not a real directory: %w", ErrUnrelatedRoot)
	}
	experiments, err := pathx.OpenRootAtNoSymlinks(root, "experiments")
	if err != nil {
		return marker{}, false, fmt.Errorf("open experiments root: %w", err)
	}
	defer experiments.Close()
	openedExperimentsInfo, err := experiments.Stat(".")
	if err != nil || !os.SameFile(experimentsInfo, openedExperimentsInfo) {
		return marker{}, false, fmt.Errorf("experiments root changed while opening: %w", errors.Join(pathx.ErrOutsideRoot, err))
	}
	markerInfo, err := experiments.Lstat(record.ProjectFile)
	if errors.Is(err, fs.ErrNotExist) {
		if verifyErr := errors.Join(pathx.VerifyRootAt(root, "experiments", experiments), pathx.VerifyRootPath(repositoryRoot, root)); verifyErr != nil {
			return marker{}, false, fmt.Errorf("Project root changed during discovery: %w", verifyErr)
		}
		return marker{}, false, nil
	}
	if err != nil {
		return marker{}, false, fmt.Errorf("inspect experiments/%s: %w", record.ProjectFile, err)
	}
	if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
		return marker{}, false, fmt.Errorf("invalid Project marker experiments/%s: expected a regular non-symlink file", record.ProjectFile)
	}
	if beforeRead != nil {
		beforeRead()
	}
	data, openedMarkerInfo, readErr := pathx.ReadBoundedRegularFile(ctx, experiments, record.ProjectFile, record.MaxRecordBytes)
	if readErr != nil {
		if errors.Is(readErr, pathx.ErrFileTooLarge) {
			readErr = errors.Join(record.ErrRecordTooLarge, readErr)
		}
		return marker{}, false, fmt.Errorf("read experiments/%s: %w", record.ProjectFile, readErr)
	}
	if !os.SameFile(markerInfo, openedMarkerInfo) {
		return marker{}, false, fmt.Errorf("Project marker changed while opening: %w", pathx.ErrOutsideRoot)
	}
	schema, err := record.PeekSchema(data)
	if err != nil {
		return marker{}, false, fmt.Errorf("invalid Project marker experiments/%s: %w", record.ProjectFile, err)
	}
	if schema != research.SchemaProject {
		return marker{}, false, fmt.Errorf("invalid Project marker experiments/%s: unsupported or unrelated Project schema %s", record.ProjectFile, schema)
	}
	if err := errors.Join(pathx.VerifyRootAt(root, "experiments", experiments), pathx.VerifyRootPath(repositoryRoot, root)); err != nil {
		return marker{}, false, fmt.Errorf("experiments root changed while reading Project marker: %w", err)
	}
	document, err := record.Decode(data)
	if err != nil {
		return marker{}, false, fmt.Errorf("invalid Project marker experiments/%s: %w", record.ProjectFile, err)
	}
	document.Path = record.ProjectFile
	return marker{Root: filepath.Join(repositoryRoot, "experiments"), Document: document, Content: append([]byte(nil), data...), Identity: experimentsInfo}, true, nil
}

type rootClassification uint8

const (
	rootAbsentOrEmpty rootClassification = iota
	rootLegacy
	rootUnrelated
)

func classifyUnmarkedRoot(rootPath string) (rootClassification, error) {
	info, err := os.Lstat(rootPath)
	if errors.Is(err, fs.ErrNotExist) {
		return rootAbsentOrEmpty, nil
	}
	if err != nil {
		return rootUnrelated, fmt.Errorf("inspect existing experiments root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return rootUnrelated, nil
	}
	root, err := pathx.OpenRootNoSymlinks(rootPath)
	if err != nil {
		return rootUnrelated, fmt.Errorf("open existing experiments root: %w", err)
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return rootUnrelated, err
	}
	entries, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return rootUnrelated, fmt.Errorf("inspect existing experiments root: %w", err)
	}
	if len(entries) == 0 {
		return rootAbsentOrEmpty, nil
	}
	allowedEmptyDirectories := map[string]struct{}{record.PlansDir: {}, record.FindingsDir: {}, record.DecisionsDir: {}}
	legacyNames := map[string]struct{}{"REPORT.md": {}, "ROADMAP.md": {}, "LEDGER.md": {}, "INBOX.md": {}}
	allAllowedEmpty := true
	for _, entry := range entries {
		if _, legacy := legacyNames[entry.Name()]; legacy {
			return rootLegacy, nil
		}
		if _, allowed := allowedEmptyDirectories[entry.Name()]; !allowed || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			allAllowedEmpty = false
			continue
		}
		child, openErr := pathx.OpenRootAtNoSymlinks(root, entry.Name())
		if openErr != nil {
			return rootUnrelated, openErr
		}
		childDirectory, openErr := child.Open(".")
		if openErr != nil {
			_ = child.Close()
			return rootUnrelated, openErr
		}
		children, readErr := childDirectory.ReadDir(-1)
		_ = childDirectory.Close()
		_ = child.Close()
		if readErr != nil {
			return rootUnrelated, readErr
		}
		if len(children) != 0 {
			allAllowedEmpty = false
		}
	}
	if allAllowedEmpty {
		return rootAbsentOrEmpty, nil
	}
	return rootUnrelated, nil
}
