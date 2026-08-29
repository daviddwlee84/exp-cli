package skill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/record"
)

const (
	installedFileMode fs.FileMode = 0o644
	installedDirMode  fs.FileMode = 0o755
)

// FileInstallAction describes what Install did to one binary-owned path.
type FileInstallAction string

const (
	FileCreated   FileInstallAction = "created"
	FileUpdated   FileInstallAction = "updated"
	FileUnchanged FileInstallAction = "unchanged"
)

// PublicationError reports that a skill entry was renamed into place before a
// later durability operation failed. Callers must treat Published as a completed
// filesystem mutation even when the overall install returns an error.
type PublicationError struct {
	Path      string
	Published bool
	Err       error
}

func (e *PublicationError) Error() string {
	if e.Published {
		return fmt.Sprintf("skill publication for %s reached rename: %v", e.Path, e.Err)
	}
	return fmt.Sprintf("skill publication for %s failed: %v", e.Path, e.Err)
}

func (e *PublicationError) Unwrap() error { return e.Err }

// InstalledFile reports the exact before/after state of a known file.
type InstalledFile struct {
	Path         string            `json:"path"`
	Action       FileInstallAction `json:"action"`
	PreviousHash string            `json:"previous_hash,omitempty"`
	ExpectedHash string            `json:"expected_hash"`
}

// LinkState is the read-only state of a consumer-specific link location.
type LinkState string

const (
	LinkBaseMissing      LinkState = "base_missing"
	LinkBaseNotDirectory LinkState = "base_not_directory"
	LinkMissing          LinkState = "missing"
	LinkCorrect          LinkState = "correct"
	LinkWrongSymlink     LinkState = "wrong_symlink"
	LinkRealDirectory    LinkState = "real_directory"
	LinkRealFile         LinkState = "real_file"
	LinkOther            LinkState = "other"
)

// LinkAction describes an installation mutation at a consumer link path.
type LinkAction string

const (
	LinkNotChanged LinkAction = "none"
	LinkCreated    LinkAction = "created"
	LinkReplaced   LinkAction = "replaced"
)

// ConsumerLinkResult reports one supported consumer location. A missing base
// directory is reported but never created. Any non-symlink target is preserved.
type ConsumerLinkResult struct {
	Consumer       string     `json:"consumer"`
	BaseDir        string     `json:"base_dir"`
	Path           string     `json:"path"`
	ExpectedTarget string     `json:"expected_target"`
	ActualTarget   string     `json:"actual_target,omitempty"`
	State          LinkState  `json:"state"`
	Action         LinkAction `json:"action"`
}

// InstallResult reports every binary-owned file, repaired owned directory, and
// supported consumer link. Its path lists are deterministic.
type InstallResult struct {
	Dir                   string               `json:"dir"`
	ManifestHash          string               `json:"manifest_hash"`
	SchemaVersion         string               `json:"schema_version"`
	SkillVersion          string               `json:"skill_version"`
	Changed               bool                 `json:"changed"`
	Written               []string             `json:"written"`
	Created               []string             `json:"created"`
	Updated               []string             `json:"updated"`
	Skipped               []string             `json:"skipped"`
	CreatedDirectories    []string             `json:"created_directories"`
	RepairedDirectories   []string             `json:"repaired_directories"`
	RemovedTemporaryFiles []string             `json:"removed_temporary_files"`
	Files                 []InstalledFile      `json:"files"`
	Links                 []string             `json:"links"`
	LinkResults           []ConsumerLinkResult `json:"link_results"`
}

// FileCheckState classifies one embedded path at an installation destination.
type FileCheckState string

const (
	FileCurrent FileCheckState = "current"
	FileMissing FileCheckState = "missing"
	FileDrifted FileCheckState = "drifted"
)

// CheckedFile records exact expected and observed data for one known path.
type CheckedFile struct {
	Path         string         `json:"path"`
	State        FileCheckState `json:"state"`
	ExpectedHash string         `json:"expected_hash"`
	ActualHash   string         `json:"actual_hash,omitempty"`
	ExpectedMode fs.FileMode    `json:"expected_mode"`
	ActualMode   fs.FileMode    `json:"actual_mode,omitempty"`
	Detail       string         `json:"detail,omitempty"`
}

// VersionCompatibility reports an installed metadata value against this build.
type VersionCompatibility struct {
	Expected   string `json:"expected"`
	Installed  string `json:"installed,omitempty"`
	Compatible bool   `json:"compatible"`
}

// CheckResult is a mutation-free comparison against this build. Ordinary
// unknown files are reported separately without making known content drifted;
// an exact writer-owned temporary marks ContentCurrent false because it records
// an interrupted publication.
type CheckResult struct {
	Dir                   string               `json:"dir"`
	ManifestHash          string               `json:"manifest_hash"`
	ObservedManifestHash  string               `json:"observed_manifest_hash,omitempty"`
	Schema                VersionCompatibility `json:"schema"`
	Version               VersionCompatibility `json:"version"`
	Current               bool                 `json:"current"`
	ContentCurrent        bool                 `json:"content_current"`
	DirectoryModesCurrent bool                 `json:"directory_modes_current"`
	LinksCurrent          bool                 `json:"links_current"`
	DriftedDirectories    []string             `json:"drifted_directories"`
	CurrentFiles          []string             `json:"current_files"`
	MissingFiles          []string             `json:"missing_files"`
	DriftedFiles          []string             `json:"drifted_files"`
	UnknownFiles          []string             `json:"unknown_files"`
	Files                 []CheckedFile        `json:"files"`
	Links                 []ConsumerLinkResult `json:"links"`

	interruptedInstall bool
}

// CheckOptions controls optional comparisons. Check includes consumer links by
// default; callers that deliberately installed with linking disabled can opt
// out without changing the filesystem.
type CheckOptions struct {
	Links bool
}

// ConsumerLocation names a supported per-tool skills directory and its exp-cli
// link path.
type ConsumerLocation struct {
	Consumer string `json:"consumer"`
	BaseDir  string `json:"base_dir"`
	Path     string `json:"path"`
}

type consumerCandidate struct {
	name string
	base string
}

type installOperations struct {
	acquireLock       func(context.Context, string) (io.Closer, error)
	syncRootDirectory func(*os.Root, string) error
	syncDirectory     func(string) error
	atomicHook        record.AtomicHook
}

func defaultInstallOperations() installOperations {
	return installOperations{
		acquireLock:       acquireInstallLock,
		syncRootDirectory: syncRootDirectory,
		syncDirectory:     syncDirectory,
	}
}

// ResolveDefaultDir returns the shared Agent Skills destination under the
// current user's home directory. It never falls back to a cwd-relative path.
func ResolveDefaultDir() (string, error) {
	home, err := resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills", Name), nil
}

// DefaultDir is retained for compatibility with callers that cannot return an
// error. A failed home lookup returns an empty path so mutation APIs fail closed.
func DefaultDir() string {
	dir, err := ResolveDefaultDir()
	if err != nil {
		return ""
	}
	return dir
}

func resolveHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for %s skill: %w", Name, err)
	}
	home = filepath.Clean(home)
	if strings.TrimSpace(home) == "" || !filepath.IsAbs(home) {
		return "", fmt.Errorf("resolve home directory for %s skill: home path is not absolute", Name)
	}
	return home, nil
}

// ConsumerLinkLocations returns only supported locations whose base directory
// already exists. It never creates, repairs, or follows a missing base.
func ConsumerLinkLocations() []ConsumerLocation {
	candidates, err := consumerCandidates()
	if err != nil {
		return nil
	}
	locations := make([]ConsumerLocation, 0, len(candidates))
	for _, candidate := range candidates {
		info, statErr := os.Stat(candidate.base)
		if statErr != nil || !info.IsDir() {
			continue
		}
		locations = append(locations, ConsumerLocation{
			Consumer: candidate.name,
			BaseDir:  candidate.base,
			Path:     filepath.Join(candidate.base, Name),
		})
	}
	return locations
}

// LinkDirs is a compact compatibility helper for callers that only need the
// existing consumer base directories.
func LinkDirs() []string {
	locations := ConsumerLinkLocations()
	dirs := make([]string, 0, len(locations))
	for _, location := range locations {
		dirs = append(dirs, location.BaseDir)
	}
	return dirs
}

// Install writes this build's embedded files while holding a context-aware,
// cross-process advisory lock. Only known paths are created or atomically
// replaced; unknown files and real consumer entries are never removed.
func Install(ctx context.Context, dir string, linkConsumers bool) (InstallResult, error) {
	return install(ctx, dir, linkConsumers, defaultInstallOperations())
}

func install(ctx context.Context, dir string, linkConsumers bool, operations installOperations) (InstallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(dir) == "" {
		return InstallResult{}, fmt.Errorf("install %s skill: destination is empty", Name)
	}
	if err := ctx.Err(); err != nil {
		return InstallResult{}, fmt.Errorf("install %s skill: %w", Name, err)
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return InstallResult{}, fmt.Errorf("make skill destination absolute: %w", err)
	}
	absoluteDir = filepath.Clean(absoluteDir)

	all, err := Files()
	if err != nil {
		return InstallResult{}, err
	}
	manifest := manifestFor(all)
	result := InstallResult{
		Dir:                   absoluteDir,
		ManifestHash:          manifest.Hash,
		SchemaVersion:         SchemaVersion,
		SkillVersion:          SkillVersion,
		Written:               []string{},
		Created:               []string{},
		Updated:               []string{},
		Skipped:               []string{},
		CreatedDirectories:    []string{},
		RepairedDirectories:   []string{},
		RemovedTemporaryFiles: []string{},
		Files:                 []InstalledFile{},
		Links:                 []string{},
		LinkResults:           []ConsumerLinkResult{},
	}
	destination, err := resolveInstallDestination(absoluteDir, Name+" skill install")
	if destination.created {
		result.CreatedDirectories = append(result.CreatedDirectories, ".")
		result.Changed = true
	}
	if err != nil {
		return result, fmt.Errorf("install %s skill: %w", Name, err)
	}
	result.Dir = destination.canonical

	err = withInstallLock(ctx, destination, Name+" skill install", operations.acquireLock, func(root *os.Root) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		removedTemporaryFiles, cleanupErr := cleanupInstallTemporaries(ctx, root, manifest, operations.syncRootDirectory)
		result.RemovedTemporaryFiles = append(result.RemovedTemporaryFiles, removedTemporaryFiles...)
		if len(removedTemporaryFiles) > 0 {
			result.Changed = true
		}
		if cleanupErr != nil {
			return fmt.Errorf("clean stale skill publication temporaries: %w", cleanupErr)
		}
		repairedRoot, err := repairInstalledDirectory(root, ".", destination.identity)
		if repairedRoot {
			result.RepairedDirectories = append(result.RepairedDirectories, ".")
		}
		if err != nil {
			return fmt.Errorf("repair skill destination mode: %w", err)
		}

		for _, entry := range manifest.Files {
			if err := ctx.Err(); err != nil {
				return err
			}
			installed, createdDirectories, repairedDirectories, installErr := installKnownFile(ctx, root, entry.Path, all[entry.Path], operations.syncRootDirectory, operations.atomicHook)
			result.CreatedDirectories = append(result.CreatedDirectories, createdDirectories...)
			result.RepairedDirectories = append(result.RepairedDirectories, repairedDirectories...)
			recordInstalledFile(&result, installed)
			if installErr != nil {
				return installErr
			}
			if err := revalidateInstallDestination(destination, root, Name+" skill install"); err != nil {
				return err
			}
		}

		if !linkConsumers {
			return nil
		}
		if err := revalidateInstallDestination(destination, root, Name+" skill install"); err != nil {
			return err
		}
		links, linkErr := installConsumerLinks(ctx, destination.canonical, operations.syncDirectory)
		result.LinkResults = append(result.LinkResults, links...)
		for _, installedLink := range links {
			if installedLink.Action == LinkCreated || installedLink.Action == LinkReplaced {
				result.Links = append(result.Links, installedLink.Path)
			}
		}
		return linkErr
	})
	result.Changed = result.Changed || len(result.Written) > 0 || len(result.CreatedDirectories) > 0 || len(result.RepairedDirectories) > 0 || len(result.RemovedTemporaryFiles) > 0 || len(result.Links) > 0
	if err != nil {
		return result, fmt.Errorf("install %s skill: %w", Name, err)
	}
	return result, nil
}

func recordInstalledFile(result *InstallResult, installed InstalledFile) {
	if installed.Action == "" {
		return
	}
	result.Files = append(result.Files, installed)
	switch installed.Action {
	case FileCreated:
		result.Created = append(result.Created, installed.Path)
		result.Written = append(result.Written, installed.Path)
	case FileUpdated:
		result.Updated = append(result.Updated, installed.Path)
		result.Written = append(result.Written, installed.Path)
	case FileUnchanged:
		result.Skipped = append(result.Skipped, installed.Path)
	}
}

// Check compares dir and all existing consumer locations without acquiring a
// lock, creating a lock file, repairing permissions, or otherwise mutating the
// filesystem.
func Check(ctx context.Context, dir string) (CheckResult, error) {
	return CheckWithOptions(ctx, dir, CheckOptions{Links: true})
}

// CheckWithOptions is Check with explicit consumer-link selection.
func CheckWithOptions(ctx context.Context, dir string, options CheckOptions) (CheckResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(dir) == "" {
		return CheckResult{}, fmt.Errorf("check %s skill: destination is empty", Name)
	}
	if err := ctx.Err(); err != nil {
		return CheckResult{}, fmt.Errorf("check %s skill: %w", Name, err)
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return CheckResult{}, fmt.Errorf("make skill destination absolute: %w", err)
	}
	absoluteDir = filepath.Clean(absoluteDir)
	all, err := Files()
	if err != nil {
		return CheckResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CheckResult{}, fmt.Errorf("check %s skill: %w", Name, err)
	}
	manifest := manifestFor(all)
	result := CheckResult{
		Dir:                   absoluteDir,
		ManifestHash:          manifest.Hash,
		Schema:                VersionCompatibility{Expected: SchemaVersion},
		Version:               VersionCompatibility{Expected: SkillVersion},
		DirectoryModesCurrent: true,
		LinksCurrent:          true,
		DriftedDirectories:    []string{},
	}

	info, statErr := os.Stat(absoluteDir)
	switch {
	case errors.Is(statErr, fs.ErrNotExist):
		result.DirectoryModesCurrent = false
		result.DriftedDirectories = append(result.DriftedDirectories, ".")
		for _, entry := range manifest.Files {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			result.Files = append(result.Files, CheckedFile{
				Path: entry.Path, State: FileMissing, ExpectedHash: entry.SHA256,
				ExpectedMode: expectedInstalledFileMode(),
			})
			result.MissingFiles = append(result.MissingFiles, entry.Path)
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
	case statErr != nil:
		return result, fmt.Errorf("inspect skill destination %s: %w", absoluteDir, statErr)
	case !info.IsDir():
		return result, fmt.Errorf("skill destination %s is not a directory", absoluteDir)
	default:
		if err := checkInstalledFiles(ctx, absoluteDir, all, manifest, &result); err != nil {
			return result, err
		}
	}

	result.ContentCurrent = result.DirectoryModesCurrent && len(result.MissingFiles) == 0 && len(result.DriftedFiles) == 0 && !result.interruptedInstall
	if options.Links {
		links, err := checkConsumerLinks(ctx, absoluteDir)
		result.Links = links
		if err != nil {
			result.LinksCurrent = false
			return result, err
		}
		for _, link := range links {
			switch link.State {
			case LinkBaseMissing, LinkCorrect:
				// A consumer that is not installed does not require a link.
			default:
				result.LinksCurrent = false
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Current = result.ContentCurrent && result.LinksCurrent && result.Schema.Compatible && result.Version.Compatible
	return result, nil
}

func installKnownFile(
	ctx context.Context,
	root *os.Root,
	relativePath string,
	expected []byte,
	syncParent func(*os.Root, string) error,
	atomicHook record.AtomicHook,
) (InstalledFile, []string, []string, error) {
	result := InstalledFile{Path: relativePath, ExpectedHash: digest(expected)}
	localPath := filepath.FromSlash(relativePath)
	createdDirectories, repairedDirectories, err := ensureKnownParent(root, filepath.Dir(localPath), syncParent)
	if err != nil {
		return result, createdDirectories, repairedDirectories, fmt.Errorf("prepare %s: %w", relativePath, err)
	}

	snapshot, err := inspectDestination(root, localPath)
	if err != nil {
		return result, createdDirectories, repairedDirectories, fmt.Errorf("inspect %s: %w", relativePath, err)
	}
	if snapshot.exists {
		if snapshot.info.Mode()&os.ModeSymlink != 0 || !snapshot.info.Mode().IsRegular() {
			return result, createdDirectories, repairedDirectories, fmt.Errorf("refuse to replace non-regular entry at binary-owned path %s (%s)", relativePath, snapshot.info.Mode())
		}
		content, readErr := readInstalledFile(root, localPath, snapshot.info)
		if readErr != nil {
			return result, createdDirectories, repairedDirectories, fmt.Errorf("read %s: %w", relativePath, readErr)
		}
		snapshot.content = content
		result.PreviousHash = digest(content)
		if bytes.Equal(content, expected) && installedFileModeCurrent(snapshot.info.Mode()) {
			result.Action = FileUnchanged
			return result, createdDirectories, repairedDirectories, nil
		}
	}

	action := FileCreated
	if snapshot.exists {
		action = FileUpdated
	}
	if err := atomicWriteKnownFile(ctx, root, localPath, expected, snapshot, syncParent, atomicHook); err != nil {
		var publication *PublicationError
		if errors.As(err, &publication) && publication.Published {
			result.Action = action
		}
		return result, createdDirectories, repairedDirectories, fmt.Errorf("publish %s: %w", relativePath, err)
	}
	result.Action = action
	return result, createdDirectories, repairedDirectories, nil
}

type destinationSnapshot struct {
	exists  bool
	info    fs.FileInfo
	content []byte
}

func readInstalledFile(root *os.Root, name string, expected fs.FileInfo) (content []byte, resultErr error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return nil, fmt.Errorf("file identity changed before read")
	}
	content, err = io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	pathInfo, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(opened, after) || !os.SameFile(opened, pathInfo) ||
		opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("file changed while reading")
	}
	return content, nil
}

func inspectDestination(root *os.Root, name string) (destinationSnapshot, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return destinationSnapshot{}, nil
	}
	if err != nil {
		return destinationSnapshot{}, err
	}
	return destinationSnapshot{exists: true, info: info}, nil
}

func ensureKnownParent(root *os.Root, parent string, syncParent func(*os.Root, string) error) ([]string, []string, error) {
	if parent == "." || parent == "" {
		return nil, nil, nil
	}
	var createdDirectories []string
	var repairedDirectories []string
	current := ""
	for _, element := range strings.Split(filepath.Clean(parent), string(filepath.Separator)) {
		if element == "" || element == "." {
			continue
		}
		current = filepath.Join(current, element)
		info, err := root.Lstat(current)
		created := false
		if errors.Is(err, fs.ErrNotExist) {
			if err := root.Mkdir(current, installedDirMode); err != nil {
				return createdDirectories, repairedDirectories, err
			}
			createdDirectories = append(createdDirectories, filepath.ToSlash(current))
			info, err = root.Lstat(current)
			if err != nil {
				return createdDirectories, repairedDirectories, err
			}
			created = true
		} else if err != nil {
			return createdDirectories, repairedDirectories, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return createdDirectories, repairedDirectories, fmt.Errorf("refuse to follow symlinked destination directory %s", current)
		}
		if !info.IsDir() {
			return createdDirectories, repairedDirectories, fmt.Errorf("destination parent %s is not a directory", current)
		}
		modeRepaired, err := repairInstalledDirectory(root, current, info)
		if modeRepaired {
			repairedDirectories = append(repairedDirectories, filepath.ToSlash(current))
		}
		if err != nil {
			return createdDirectories, repairedDirectories, err
		}
		if created {
			if err := syncParent(root, filepath.Dir(current)); err != nil {
				return createdDirectories, repairedDirectories, err
			}
		}
	}
	return createdDirectories, repairedDirectories, nil
}

func atomicWriteKnownFile(
	ctx context.Context,
	root *os.Root,
	name string,
	content []byte,
	snapshot destinationSnapshot,
	syncParent func(*os.Root, string) error,
	atomicHook record.AtomicHook,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	options := record.AtomicWriteOptions{Mode: installedFileMode, Hook: atomicHook}
	if snapshot.exists {
		options.Expected = snapshot.info
		options.ExpectedContent = snapshot.content
	}
	relative := filepath.ToSlash(name)
	if err := record.AtomicWriteDerivedRoot(root, relative, content, options); err != nil {
		var publication *record.PublicationError
		if errors.As(err, &publication) {
			return &PublicationError{Path: relative, Published: publication.Published, Err: err}
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return &PublicationError{Path: relative, Published: true, Err: err}
	}
	if err := syncParent(root, filepath.Dir(name)); err != nil {
		return &PublicationError{Path: relative, Published: true, Err: fmt.Errorf("sync destination directory: %w", err)}
	}
	return nil
}

func cleanupInstallTemporaries(
	ctx context.Context,
	root *os.Root,
	manifest ManifestInfo,
	syncParent func(*os.Root, string) error,
) (removed []string, resultErr error) {
	parents := installTemporaryParents(manifest)
	orderedParents := make([]string, 0, len(parents))
	for parent := range parents {
		orderedParents = append(orderedParents, parent)
	}
	sort.Strings(orderedParents)

	for _, parent := range orderedParents {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		parentRoot, err := openInstallTemporaryParent(root, parent)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, err
		}
		cleanupErr := func() (err error) {
			defer func() {
				err = errors.Join(err, parentRoot.Close())
			}()
			entries, err := fs.ReadDir(parentRoot.FS(), ".")
			if err != nil {
				return err
			}
			removedFromParent := false
			for _, entry := range entries {
				if !record.IsAtomicTempName(entry.Name()) {
					continue
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				relative := path.Join(parent, entry.Name())
				info, err := parentRoot.Lstat(entry.Name())
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return fmt.Errorf("reserved publication temporary %s is not a regular non-symlink file", relative)
				}
				temporary, err := parentRoot.Open(entry.Name())
				if err != nil {
					return err
				}
				opened, statErr := temporary.Stat()
				closeErr := temporary.Close()
				if statErr != nil || closeErr != nil {
					return errors.Join(statErr, closeErr)
				}
				current, err := parentRoot.Lstat(entry.Name())
				if err != nil {
					return err
				}
				if !opened.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(opened, current) {
					return fmt.Errorf("reserved publication temporary %s changed before cleanup", relative)
				}
				if err := parentRoot.Remove(entry.Name()); err != nil {
					return err
				}
				removed = append(removed, relative)
				removedFromParent = true
			}
			if removedFromParent {
				if err := syncParent(parentRoot, "."); err != nil {
					return err
				}
			}
			return nil
		}()
		if cleanupErr != nil {
			return removed, cleanupErr
		}
	}
	sort.Strings(removed)
	return removed, nil
}

func installTemporaryParents(manifest ManifestInfo) map[string]struct{} {
	parents := map[string]struct{}{".": {}}
	for _, entry := range manifest.Files {
		parents[path.Dir(entry.Path)] = struct{}{}
	}
	return parents
}

func isInstallTemporaryPath(relative string, parents map[string]struct{}) bool {
	relative = path.Clean(filepath.ToSlash(relative))
	if _, ownedParent := parents[path.Dir(relative)]; !ownedParent {
		return false
	}
	return record.IsAtomicTempName(path.Base(relative))
}

func openInstallTemporaryParent(root *os.Root, parent string) (*os.Root, error) {
	localParent := filepath.FromSlash(parent)
	before, err := root.Lstat(localParent)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("publication temporary parent %s is not a real directory", parent)
	}
	openedRoot, err := root.OpenRoot(localParent)
	if err != nil {
		return nil, err
	}
	opened, openedErr := openedRoot.Stat(".")
	current, currentErr := root.Lstat(localParent)
	if openedErr != nil || currentErr != nil || !opened.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, current) {
		_ = openedRoot.Close()
		return nil, fmt.Errorf("publication temporary parent %s changed while opening: %w", parent, errors.Join(openedErr, currentErr))
	}
	return openedRoot, nil
}

func checkInstalledDirectories(root *os.Root, manifest ManifestInfo, result *CheckResult) error {
	owned := map[string]struct{}{".": {}}
	for _, entry := range manifest.Files {
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(entry.Path)))
		for parent != "." && parent != "" {
			owned[parent] = struct{}{}
			next := filepath.ToSlash(filepath.Dir(filepath.FromSlash(parent)))
			if next == parent {
				break
			}
			parent = next
		}
	}
	directories := make([]string, 0, len(owned))
	for name := range owned {
		directories = append(directories, name)
	}
	sort.Strings(directories)
	for _, name := range directories {
		info, err := root.Lstat(filepath.FromSlash(name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			result.DirectoryModesCurrent = false
			result.DriftedDirectories = append(result.DriftedDirectories, name)
		case err != nil:
			return fmt.Errorf("inspect installed skill directory %s: %w", name, err)
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !installedDirectoryModeCurrent(info.Mode()):
			result.DirectoryModesCurrent = false
			result.DriftedDirectories = append(result.DriftedDirectories, name)
		}
	}
	return nil
}

func checkInstalledFiles(ctx context.Context, absoluteDir string, expected map[string][]byte, manifest ManifestInfo, result *CheckResult) (resultErr error) {
	root, err := os.OpenRoot(absoluteDir)
	if err != nil {
		return fmt.Errorf("open skill destination %s: %w", absoluteDir, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	if err := checkInstalledDirectories(root, manifest, result); err != nil {
		return err
	}

	observed := make(map[string][]byte, len(expected))
	allRegularReadable := true
	var skillContent []byte
	for _, entry := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		checked := CheckedFile{
			Path: entry.Path, ExpectedHash: entry.SHA256, ExpectedMode: expectedInstalledFileMode(),
		}
		localPath := filepath.FromSlash(entry.Path)
		info, statErr := root.Lstat(localPath)
		if errors.Is(statErr, fs.ErrNotExist) {
			checked.State = FileMissing
			result.MissingFiles = append(result.MissingFiles, entry.Path)
			result.Files = append(result.Files, checked)
			allRegularReadable = false
			continue
		}
		if statErr != nil {
			return fmt.Errorf("inspect installed skill file %s: %w", entry.Path, statErr)
		}
		checked.ActualMode = info.Mode().Perm()
		if !info.Mode().IsRegular() {
			checked.State = FileDrifted
			checked.Detail = "expected a regular file; found " + info.Mode().String()
			result.DriftedFiles = append(result.DriftedFiles, entry.Path)
			result.Files = append(result.Files, checked)
			allRegularReadable = false
			continue
		}
		content, readErr := root.ReadFile(localPath)
		if readErr != nil {
			return fmt.Errorf("read installed skill file %s: %w", entry.Path, readErr)
		}
		observed[entry.Path] = content
		checked.ActualHash = digest(content)
		if entry.Path == "SKILL.md" {
			skillContent = content
		}
		if !bytes.Equal(content, expected[entry.Path]) {
			checked.State = FileDrifted
			checked.Detail = "content differs"
			result.DriftedFiles = append(result.DriftedFiles, entry.Path)
		} else if !installedFileModeCurrent(info.Mode()) {
			checked.State = FileDrifted
			checked.Detail = fmt.Sprintf("mode is %04o; expected %04o", info.Mode().Perm(), expectedInstalledFileMode())
			result.DriftedFiles = append(result.DriftedFiles, entry.Path)
		} else {
			checked.State = FileCurrent
			result.CurrentFiles = append(result.CurrentFiles, entry.Path)
		}
		result.Files = append(result.Files, checked)
	}
	if allRegularReadable {
		result.ObservedManifestHash = framedContentHash(observed)
	}
	result.Schema.Installed, result.Version.Installed = installedVersions(skillContent)
	result.Schema.Compatible = result.Schema.Installed == result.Schema.Expected
	result.Version.Compatible = result.Version.Installed == result.Version.Expected

	expectedSet := make(map[string]struct{}, len(expected))
	for name := range expected {
		expectedSet[name] = struct{}{}
	}
	temporaryParents := installTemporaryParents(manifest)
	walkErr := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative := filepath.ToSlash(name)
		if isInstallTemporaryPath(relative, temporaryParents) {
			result.interruptedInstall = true
		}
		if name == "." || entry.IsDir() {
			return nil
		}
		if _, known := expectedSet[relative]; !known {
			result.UnknownFiles = append(result.UnknownFiles, relative)
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("inventory installed skill: %w", walkErr)
	}
	sort.Strings(result.UnknownFiles)
	return nil
}

func installedVersions(content []byte) (schema, version string) {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	inMetadata := false
	for _, raw := range lines[1:] {
		if strings.TrimSpace(raw) == "---" {
			break
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "metadata:" {
			inMetadata = true
			continue
		}
		if !inMetadata {
			continue
		}
		if raw != "" && raw[0] != ' ' && raw[0] != '\t' {
			inMetadata = false
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		switch strings.TrimSpace(key) {
		case "schema-version":
			schema = value
		case "skill-version":
			version = value
		}
	}
	return schema, version
}

func consumerCandidates() ([]consumerCandidate, error) {
	home, err := resolveHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory for skill consumers: %w", err)
	}
	return []consumerCandidate{
		{name: "claude-code", base: filepath.Join(home, ".claude", "skills")},
	}, nil
}

func installConsumerLinks(
	ctx context.Context,
	expectedTarget string,
	syncBase func(string) error,
) ([]ConsumerLinkResult, error) {
	links, err := checkConsumerLinks(ctx, expectedTarget)
	if err != nil {
		return links, err
	}
	for index := range links {
		if err := ctx.Err(); err != nil {
			return links, err
		}
		link := &links[index]
		switch link.State {
		case LinkMissing:
			if err := os.Symlink(expectedTarget, link.Path); err != nil {
				return links, fmt.Errorf("create consumer link %s: %w", link.Path, err)
			}
			link.Action = LinkCreated
			link.State = LinkCorrect
			link.ActualTarget = expectedTarget
			if err := syncBase(link.BaseDir); err != nil {
				return links, &PublicationError{Path: link.Path, Published: true, Err: fmt.Errorf("sync consumer directory: %w", err)}
			}
		case LinkWrongSymlink:
			return links, fmt.Errorf("refuse to replace existing consumer symlink %s without compare-and-swap support; remove it explicitly and retry", link.Path)
		}
	}
	return links, nil
}

func checkConsumerLinks(ctx context.Context, expectedTarget string) ([]ConsumerLinkResult, error) {
	candidates, err := consumerCandidates()
	if err != nil {
		return nil, err
	}
	results := make([]ConsumerLinkResult, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result, inspectErr := inspectConsumerLink(candidate, expectedTarget)
		results = append(results, result)
		if inspectErr != nil {
			return results, inspectErr
		}
	}
	return results, nil
}

func inspectConsumerLink(candidate consumerCandidate, expectedTarget string) (ConsumerLinkResult, error) {
	result := ConsumerLinkResult{
		Consumer: candidate.name, BaseDir: candidate.base,
		Path: filepath.Join(candidate.base, Name), ExpectedTarget: expectedTarget,
		Action: LinkNotChanged,
	}
	baseInfo, err := os.Stat(candidate.base)
	if errors.Is(err, fs.ErrNotExist) {
		result.State = LinkBaseMissing
		return result, nil
	}
	if err != nil {
		result.State = LinkOther
		return result, fmt.Errorf("inspect consumer base %s: %w", candidate.base, err)
	}
	if !baseInfo.IsDir() {
		result.State = LinkBaseNotDirectory
		return result, nil
	}
	info, err := os.Lstat(result.Path)
	if errors.Is(err, fs.ErrNotExist) {
		result.State = LinkMissing
		return result, nil
	}
	if err != nil {
		result.State = LinkOther
		return result, fmt.Errorf("inspect consumer link %s: %w", result.Path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(result.Path)
		if readErr != nil {
			result.State = LinkOther
			return result, fmt.Errorf("read consumer link %s: %w", result.Path, readErr)
		}
		result.ActualTarget = target
		if sameLinkTarget(candidate.base, target, expectedTarget) {
			result.State = LinkCorrect
		} else {
			result.State = LinkWrongSymlink
		}
		return result, nil
	}
	if info.IsDir() {
		expectedInfo, expectedErr := os.Stat(expectedTarget)
		if expectedErr == nil && expectedInfo.IsDir() && os.SameFile(info, expectedInfo) {
			result.State = LinkCorrect
			result.ActualTarget = expectedTarget
			return result, nil
		}
		result.State = LinkRealDirectory
	} else if info.Mode().IsRegular() {
		result.State = LinkRealFile
	} else {
		result.State = LinkOther
	}
	return result, nil
}

func sameLinkTarget(base, actual, expected string) bool {
	if !filepath.IsAbs(actual) {
		actual = filepath.Join(base, actual)
	}
	actual = filepath.Clean(actual)
	expected = filepath.Clean(expected)
	resolvedActual, actualErr := filepath.EvalSymlinks(actual)
	resolvedExpected, expectedErr := filepath.EvalSymlinks(expected)
	if actualErr == nil && expectedErr == nil {
		return resolvedActual == resolvedExpected
	}
	absoluteActual, actualAbsErr := filepath.Abs(actual)
	absoluteExpected, expectedAbsErr := filepath.Abs(expected)
	return actualAbsErr == nil && expectedAbsErr == nil && absoluteActual == absoluteExpected
}
