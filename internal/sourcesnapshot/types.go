// Package sourcesnapshot captures deterministic, host-path-free Git source
// identities and manages private local seed bundles for dirty Try workspaces.
package sourcesnapshot

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	PolicyVersion = "capture-v1"

	defaultMaxStatusBytes         = 4 << 20
	defaultMaxPatchBytes          = 4 << 20
	defaultMaxChangePaths         = 4096
	defaultMaxFilesystemEntries   = 100000
	defaultMaxUntrackedFiles      = 512
	defaultMaxUntrackedFileBytes  = 8 << 20
	defaultMaxUntrackedTotalBytes = 32 << 20
	absoluteMaxBundlePayloadBytes = 64 << 20
)

var (
	ErrInvalidRequest       = errors.New("invalid Source snapshot request")
	ErrSourceMismatch       = errors.New("selected Source does not match the registered clone")
	ErrDirtySource          = errors.New("clean Source snapshot requires a clean checkout")
	ErrNoChanges            = errors.New("Source snapshot has no changes")
	ErrDirtyModeRequiresTry = errors.New("dirty Source capture is legal only for an explicit Try")
	ErrStatusMalformed      = errors.New("malformed Git porcelain-v2 status")
	ErrRenameOrCopy         = errors.New("dirty capture rejects rename or copy ambiguity")
	ErrOutsideSourceSubdir  = errors.New("dirty path is outside the canonical Source subdir")
	ErrSubmodule            = errors.New("dirty or nested submodules are unsupported")
	ErrUnsupportedIndex     = errors.New("Git index uses hidden or unsupported path flags")
	ErrLimit                = errors.New("Source snapshot capture limit exceeded")
	ErrSourceChanged        = errors.New("Source changed during snapshot capture")
	ErrBundleExists         = errors.New("source seed bundle already exists")
	ErrBundleInvalid        = errors.New("source seed bundle is invalid")
	ErrBundleNotFound       = errors.New("source seed bundle does not exist")
)

// Limits bounds every dirty capture dimension. Zero fields use conservative
// defaults; negative values are invalid. MaxPatchBytes cannot exceed gitx's
// independent command-output bound.
type Limits struct {
	MaxStatusBytes         int64
	MaxPatchBytes          int64
	MaxChangePaths         int
	MaxFilesystemEntries   int
	MaxUntrackedFiles      int
	MaxUntrackedFileBytes  int64
	MaxUntrackedTotalBytes int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxStatusBytes: defaultMaxStatusBytes, MaxPatchBytes: defaultMaxPatchBytes,
		MaxChangePaths: defaultMaxChangePaths, MaxFilesystemEntries: defaultMaxFilesystemEntries,
		MaxUntrackedFiles:      defaultMaxUntrackedFiles,
		MaxUntrackedFileBytes:  defaultMaxUntrackedFileBytes,
		MaxUntrackedTotalBytes: defaultMaxUntrackedTotalBytes,
	}
}

// FileSystem is the injected physical-I/O seam. Its default implementation
// delegates containment and no-follow operations to pathx.
type FileSystem interface {
	Canonical(string) (string, error)
	ResolveUnderNoSymlinks(root, relative string, allowRoot bool) (string, error)
	OpenRootNoSymlinks(string) (*os.Root, error)
	OpenCanonicalRootNoSymlinks(string) (*os.Root, error)
	OpenRegularFileNoFollow(*os.Root, string) (*os.File, fs.FileInfo, error)
}

// OSFileSystem is the production pathx-backed filesystem implementation.
type OSFileSystem struct{}

func (OSFileSystem) Canonical(value string) (string, error) {
	return pathx.Canonical(value)
}
func (OSFileSystem) ResolveUnderNoSymlinks(root, relative string, allowRoot bool) (string, error) {
	return pathx.ResolveUnderNoSymlinks(root, relative, allowRoot)
}
func (OSFileSystem) OpenRootNoSymlinks(value string) (*os.Root, error) {
	return pathx.OpenRootNoSymlinks(value)
}
func (OSFileSystem) OpenCanonicalRootNoSymlinks(value string) (*os.Root, error) {
	return pathx.OpenCanonicalRootNoSymlinks(value)
}
func (OSFileSystem) OpenRegularFileNoFollow(root *os.Root, relative string) (*os.File, fs.FileInfo, error) {
	return pathx.OpenRegularFileNoFollow(root, relative)
}

// Request binds a canonical Source to its registered local clone. BaseCommit is
// always an exact full object ID. ExpectedHead, when present, must be the exact
// checked-out HEAD. AllowNoChanges is an explicit observational opt-in.
// CapturedAt is reserved for deterministic recapture; zero uses Capturer.Clock.
type Request struct {
	Source                      *research.Source
	RepositoryRoot              string
	RegisteredGitCommonDir      string
	RegisteredGitCommonIdentity string
	BaseCommit                  string
	ExpectedHead                string
	AllowNoChanges              bool
	AllowRetiredSource          bool
	Try                         research.ID
	CapturedAt                  time.Time
	Limits                      Limits
}

// Result contains the canonical bounded snapshot and, for a dirty capture, the
// validated private bundle needed to reproduce it. Bundle exposes no raw bytes.
type Result struct {
	Snapshot research.SourceSnapshot `json:"snapshot"`
	Bundle   *Bundle                 `json:"bundle,omitempty"`
}

// Capturer owns all nondeterministic seams used during source capture.
type Capturer struct {
	Git        gitx.Runner
	FileSystem FileSystem
	Clock      func() time.Time
	Bundles    *BundleStore
}

// CaptureClean records committed state only and rejects every dirty status
// entry, including untracked files and dirty submodules.
func (capturer Capturer) CaptureClean(ctx context.Context, request Request) (research.SourceSnapshot, error) {
	return capturer.captureClean(ctx, request)
}

// CaptureDirty performs the explicit Try-only capture and atomically publishes
// the private seed bundle before returning.
func (capturer Capturer) CaptureDirty(ctx context.Context, request Request) (Result, error) {
	return capturer.captureDirty(ctx, request, true)
}

// PreviewDirty validates and fingerprints bounded dirty Source bytes without
// publishing a seed bundle. Interactive plans use it before confirmation;
// CaptureDirty remains the only apply path that creates private bundle state.
func (capturer Capturer) PreviewDirty(ctx context.Context, request Request) (research.SourceSnapshot, error) {
	result, err := capturer.captureDirty(ctx, request, false)
	return result.Snapshot, err
}

// VerifyDirty recaptures an explicit dirty Try state without publishing another
// bundle and requires it to match expected's complete canonical digest.
func (capturer Capturer) VerifyDirty(ctx context.Context, request Request, expected research.SourceSnapshot) (research.SourceSnapshot, error) {
	result, err := capturer.captureDirty(ctx, request, false)
	if err != nil {
		return research.SourceSnapshot{}, err
	}
	if result.Snapshot.Digest != expected.Digest {
		return result.Snapshot, ErrSourceChanged
	}
	return result.Snapshot, nil
}
