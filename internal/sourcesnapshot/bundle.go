package sourcesnapshot

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	BundleSchema    = "exp.source-seed/v1"
	manifestName    = "manifest.json"
	trackedPatch    = "tracked.patch"
	untrackedDir    = "untracked"
	maxManifestSize = 1 << 20
	maxBundleFiles  = 4096
)

// PayloadDescriptor authenticates one fixed-name payload without carrying it.
type PayloadDescriptor struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BundleFile authenticates one untracked regular file. Payload is the private
// numeric filename inside the bundle; Path is Git-root-relative.
type BundleFile struct {
	Path    string `json:"path"`
	Payload string `json:"payload"`
	Mode    uint32 `json:"mode"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

// SeedPath authenticates the final state of one dirty baseline path. A deleted
// tracked path has Exists=false and no mode, size, or digest.
type SeedPath struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Mode   uint32 `json:"mode,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// BundleSubmodule records the clean direct gitlink state included in DirtyDigest.
type BundleSubmodule struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
}

// BundleManifest is a closed, bounded description of exact local seed bytes.
// BundleDigest covers every field except itself using length-prefixed framing.
type BundleManifest struct {
	Schema          string                   `json:"schema"`
	PolicyVersion   string                   `json:"policy_version"`
	Source          string                   `json:"source"`
	Subdir          string                   `json:"subdir"`
	GitObjectFormat research.GitObjectFormat `json:"git_object_format"`
	BaseCommit      string                   `json:"base_commit"`
	HeadCommit      string                   `json:"head_commit"`
	SnapshotDigest  string                   `json:"snapshot_digest"`
	DirtyDigest     string                   `json:"dirty_digest"`
	TrackedPatch    PayloadDescriptor        `json:"tracked_patch"`
	Untracked       []BundleFile             `json:"untracked"`
	SeedPaths       []SeedPath               `json:"seed_paths,omitempty"`
	Submodules      []BundleSubmodule        `json:"submodules"`
	BundleDigest    string                   `json:"bundle_digest"`
}

// Bundle is a validated local capability. Directory is intentionally omitted
// from JSON; callers may log the digest and manifest but not infer raw payloads.
type Bundle struct {
	Directory string         `json:"-"`
	Manifest  BundleManifest `json:"manifest"`
}

func (bundle Bundle) Digest() string { return bundle.Manifest.BundleDigest }

// BundleStore publishes, opens, restores, and explicitly cleans private seed
// bundles. Root, when empty, defaults to $XDG_CACHE_HOME/exp/source-seeds.
type BundleStore struct {
	Root       string
	FileSystem FileSystem
}

type bundlePayload struct {
	Snapshot   research.SourceSnapshot
	Patch      []byte
	Untracked  []capturedFile
	SeedPaths  []SeedPath
	Submodules []submoduleState
}

func (store *BundleStore) publish(ctx context.Context, payload bundlePayload) (Bundle, error) {
	if ctx == nil {
		return Bundle{}, fmt.Errorf("context is required: %w", ErrBundleInvalid)
	}
	manifest, err := manifestForPayload(payload)
	if err != nil {
		return Bundle{}, err
	}
	root, rootPath, err := store.ensureRoot()
	if err != nil {
		return Bundle{}, err
	}
	defer root.Close()
	key, err := bundleKey(manifest.BundleDigest)
	if err != nil {
		return Bundle{}, err
	}
	if _, err := root.Lstat(key); err == nil {
		existing, openErr := store.openFromRoot(ctx, root, rootPath, manifest.BundleDigest)
		if openErr != nil || !equalManifest(existing.Manifest, manifest) {
			return Bundle{}, fmt.Errorf("bundle %s exists with unverifiable content: %w", manifest.BundleDigest, errors.Join(ErrBundleExists, openErr))
		}
		return existing, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Bundle{}, fmt.Errorf("inspect bundle destination: %w", err)
	}
	temporary, err := createTemporaryDirectory(root)
	if err != nil {
		return Bundle{}, err
	}
	published := false
	validated := false
	defer func() {
		if !published {
			_ = root.RemoveAll(temporary)
		} else if !validated {
			_ = root.RemoveAll(key)
			_ = pathx.SyncRoot(root)
		}
	}()
	tempRoot, err := pathx.OpenRootAtNoSymlinks(root, temporary)
	if err != nil {
		return Bundle{}, fmt.Errorf("open bundle staging directory: %w", err)
	}
	defer tempRoot.Close()
	if err := chmodRootDirectory(tempRoot, 0o700); err != nil {
		return Bundle{}, fmt.Errorf("protect bundle staging directory: %w", err)
	}
	if err := writePrivateFile(tempRoot, trackedPatch, payload.Patch); err != nil {
		return Bundle{}, fmt.Errorf("write tracked patch: %w", err)
	}
	if err := tempRoot.Mkdir(untrackedDir, 0o700); err != nil {
		return Bundle{}, fmt.Errorf("create untracked payload directory: %w", err)
	}
	filesRoot, err := pathx.OpenRootAtNoSymlinks(tempRoot, untrackedDir)
	if err != nil {
		return Bundle{}, fmt.Errorf("open untracked payload directory: %w", err)
	}
	if err := chmodRootDirectory(filesRoot, 0o700); err != nil {
		_ = filesRoot.Close()
		return Bundle{}, fmt.Errorf("protect untracked payload directory: %w", err)
	}
	for index, file := range payload.Untracked {
		if err := writePrivateFile(filesRoot, payloadName(index), file.Data); err != nil {
			_ = filesRoot.Close()
			return Bundle{}, fmt.Errorf("write untracked payload %d: %w", index, err)
		}
	}
	if err := errors.Join(pathx.SyncRoot(filesRoot), filesRoot.Close()); err != nil {
		return Bundle{}, fmt.Errorf("sync untracked payload directory: %w", err)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Bundle{}, fmt.Errorf("encode source seed manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxManifestSize {
		return Bundle{}, fmt.Errorf("source seed manifest exceeds size limit: %w", ErrBundleInvalid)
	}
	if err := writePrivateFile(tempRoot, manifestName, encoded); err != nil {
		return Bundle{}, fmt.Errorf("write source seed manifest: %w", err)
	}
	if err := pathx.SyncRoot(tempRoot); err != nil {
		return Bundle{}, fmt.Errorf("sync bundle staging directory: %w", err)
	}
	if err := pathx.VerifyRootPath(rootPath, root); err != nil {
		return Bundle{}, fmt.Errorf("bundle root changed before publication: %w", err)
	}
	if err := root.Rename(temporary, key); err != nil {
		if _, statErr := root.Lstat(key); statErr == nil {
			return Bundle{}, fmt.Errorf("bundle %s: %w", manifest.BundleDigest, ErrBundleExists)
		}
		return Bundle{}, fmt.Errorf("atomically publish source seed bundle: %w", err)
	}
	published = true
	if err := pathx.SyncRoot(root); err != nil {
		return Bundle{}, fmt.Errorf("sync source seed bundle parent: %w", err)
	}
	opened, err := store.openFromRoot(ctx, root, rootPath, manifest.BundleDigest)
	if err != nil {
		return Bundle{}, fmt.Errorf("reopen published source seed bundle: %w", err)
	}
	if !equalManifest(opened.Manifest, manifest) {
		return Bundle{}, fmt.Errorf("published manifest changed during reopen: %w", ErrBundleInvalid)
	}
	validated = true
	return opened, nil
}

// Open strictly revalidates an already-published bundle. Missing, partial,
// stale, permission-weakened, symlinked, or augmented bundles fail closed.
func (store *BundleStore) Open(ctx context.Context, digest string) (Bundle, error) {
	root, rootPath, err := store.openRoot()
	if err != nil {
		return Bundle{}, err
	}
	defer root.Close()
	return store.openFromRoot(ctx, root, rootPath, digest)
}

func (store *BundleStore) openFromRoot(ctx context.Context, root *os.Root, rootPath, digest string) (Bundle, error) {
	key, err := bundleKey(digest)
	if err != nil {
		return Bundle{}, err
	}
	info, err := root.Lstat(key)
	if errors.Is(err, fs.ErrNotExist) {
		return Bundle{}, ErrBundleNotFound
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Bundle{}, fmt.Errorf("bundle directory is missing, unsafe, or not private: %w", errors.Join(ErrBundleInvalid, err))
	}
	bundleRoot, err := pathx.OpenRootAtNoSymlinks(root, key)
	if err != nil {
		return Bundle{}, fmt.Errorf("open source seed bundle: %w", errors.Join(ErrBundleInvalid, err))
	}
	defer bundleRoot.Close()
	openedInfo, err := pathx.CheckPrivateRoot(bundleRoot, 0o700, "source seed bundle directory")
	if err != nil || !os.SameFile(info, openedInfo) {
		return Bundle{}, fmt.Errorf("bundle directory changed while opening: %w", errors.Join(ErrBundleInvalid, err))
	}
	manifestChecked, err := pathx.CheckPrivateFile(bundleRoot, manifestName, 0o600, "source seed manifest")
	if err != nil {
		return Bundle{}, fmt.Errorf("read private source seed manifest: %w", errors.Join(ErrBundleInvalid, err))
	}
	manifestBytes, manifestInfo, err := pathx.ReadBoundedRegularFile(ctx, bundleRoot, manifestName, maxManifestSize)
	if err != nil || !os.SameFile(manifestChecked, manifestInfo) {
		return Bundle{}, fmt.Errorf("read private source seed manifest: %w", errors.Join(ErrBundleInvalid, err))
	}
	manifest, err := decodeManifest(manifestBytes)
	if err != nil || manifest.BundleDigest != digest {
		return Bundle{}, fmt.Errorf("validate source seed manifest: %w", errors.Join(ErrBundleInvalid, err))
	}
	if err := validateDirectoryEntries(bundleRoot, manifest); err != nil {
		return Bundle{}, err
	}
	if err := validatePayloadFile(ctx, store.fileSystem(), bundleRoot, trackedPatch, manifest.TrackedPatch); err != nil {
		return Bundle{}, fmt.Errorf("validate tracked patch: %w", err)
	}
	filesRoot, err := pathx.OpenRootAtNoSymlinks(bundleRoot, untrackedDir)
	if err != nil {
		return Bundle{}, fmt.Errorf("open untracked payloads: %w", errors.Join(ErrBundleInvalid, err))
	}
	defer filesRoot.Close()
	for _, file := range manifest.Untracked {
		if err := validatePayloadFile(ctx, store.fileSystem(), filesRoot, file.Payload, PayloadDescriptor{Size: file.Size, SHA256: file.SHA256}); err != nil {
			return Bundle{}, fmt.Errorf("validate untracked payload %s: %w", file.Payload, err)
		}
	}
	if err := errors.Join(pathx.VerifyRootAt(root, key, bundleRoot), pathx.VerifyRootPath(rootPath, root)); err != nil {
		return Bundle{}, fmt.Errorf("bundle path changed during validation: %w", errors.Join(ErrBundleInvalid, err))
	}
	return Bundle{Directory: filepath.Join(rootPath, key), Manifest: manifest}, nil
}

// TrackedPatchPath reopens bundle and returns the authenticated local patch
// pathname suitable for passing as a Git argv element (never shell input).
func (store *BundleStore) TrackedPatchPath(ctx context.Context, bundle Bundle) (string, error) {
	opened, err := store.requireBundle(ctx, bundle)
	if err != nil {
		return "", err
	}
	return filepath.Join(opened.Directory, trackedPatch), nil
}

// RestoreUntracked writes the authenticated untracked bytes beneath one new
// worktree. It refuses existing paths and rolls back only files it created.
func (store *BundleStore) RestoreUntracked(ctx context.Context, bundle Bundle, destination string) error {
	opened, err := store.requireBundle(ctx, bundle)
	if err != nil {
		return err
	}
	if err := validateAbsoluteCanonicalInput(destination); err != nil {
		return err
	}
	canonical, err := store.fileSystem().Canonical(destination)
	if err != nil || canonical != destination {
		return fmt.Errorf("destination worktree root is not canonical: %w", errors.Join(ErrBundleInvalid, err))
	}
	destinationRoot, err := store.fileSystem().OpenCanonicalRootNoSymlinks(destination)
	if err != nil {
		return fmt.Errorf("open destination worktree: %w", err)
	}
	defer destinationRoot.Close()
	bundleRoot, err := store.fileSystem().OpenCanonicalRootNoSymlinks(opened.Directory)
	if err != nil {
		return fmt.Errorf("reopen source seed bundle root: %w", err)
	}
	defer bundleRoot.Close()
	payloadRoot, err := pathx.OpenRootAtNoSymlinks(bundleRoot, untrackedDir)
	if err != nil {
		return fmt.Errorf("open source seed untracked payloads: %w", err)
	}
	defer payloadRoot.Close()

	created := make([]string, 0, len(opened.Manifest.Untracked))
	rollback := func() {
		for index := len(created) - 1; index >= 0; index-- {
			_ = destinationRoot.Remove(created[index])
		}
	}
	for _, manifestFile := range opened.Manifest.Untracked {
		if err := ctx.Err(); err != nil {
			rollback()
			return err
		}
		parent := path.Dir(manifestFile.Path)
		if parent != "." {
			parentRoot, _, ensureErr := pathx.EnsureRootAtNoSymlinks(destinationRoot, parent, 0o755)
			if ensureErr != nil {
				rollback()
				return fmt.Errorf("create untracked parent for %q: %w", manifestFile.Path, ensureErr)
			}
			_ = parentRoot.Close()
		}
		if _, err := destinationRoot.Lstat(manifestFile.Path); err == nil {
			rollback()
			return fmt.Errorf("destination path %q already exists: %w", manifestFile.Path, ErrBundleInvalid)
		} else if !errors.Is(err, fs.ErrNotExist) {
			rollback()
			return fmt.Errorf("inspect destination path %q: %w", manifestFile.Path, err)
		}
		source, sourceInfo, err := store.fileSystem().OpenRegularFileNoFollow(payloadRoot, manifestFile.Payload)
		if err != nil {
			rollback()
			return fmt.Errorf("open bundle payload %s: %w", manifestFile.Payload, err)
		}
		destinationFile, err := destinationRoot.OpenFile(manifestFile.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = source.Close()
			rollback()
			return fmt.Errorf("create untracked destination %q: %w", manifestFile.Path, err)
		}
		created = append(created, manifestFile.Path)
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(destinationFile, hash), io.LimitReader(&contextReader{ctx: ctx, reader: source}, manifestFile.Size+1))
		copyErr = errors.Join(copyErr, destinationFile.Sync())
		if chmodErr := destinationFile.Chmod(fs.FileMode(manifestFile.Mode)); chmodErr != nil {
			copyErr = errors.Join(copyErr, chmodErr)
		}
		copyErr = errors.Join(copyErr, destinationFile.Close(), source.Close())
		if copyErr != nil || written != manifestFile.Size || digestBytes(hash.Sum(nil)) != manifestFile.SHA256 {
			rollback()
			return fmt.Errorf("restore untracked destination %q: %w", manifestFile.Path, errors.Join(ErrBundleInvalid, copyErr))
		}
		after, err := destinationRoot.Lstat(manifestFile.Path)
		if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || after.Size() != manifestFile.Size || after.Mode().Perm() != fs.FileMode(manifestFile.Mode).Perm() {
			rollback()
			return fmt.Errorf("verify untracked destination %q: %w", manifestFile.Path, errors.Join(ErrBundleInvalid, err))
		}
		if sourceInfo.Size() != manifestFile.Size {
			rollback()
			return fmt.Errorf("bundle payload changed while restoring: %w", ErrBundleInvalid)
		}
	}
	if err := pathx.VerifyRootPath(destination, destinationRoot); err != nil {
		rollback()
		return fmt.Errorf("destination worktree changed during restore: %w", err)
	}
	return nil
}

// Cleanup explicitly deletes exactly one still-valid bundle. Validation occurs
// immediately before removal, so stale or partial paths are never cleaned up as
// though they were the requested capability.
func (store *BundleStore) Cleanup(ctx context.Context, bundle Bundle) error {
	opened, err := store.requireBundle(ctx, bundle)
	if err != nil {
		return err
	}
	root, rootPath, err := store.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	key, err := bundleKey(opened.Manifest.BundleDigest)
	if err != nil {
		return err
	}
	if filepath.Join(rootPath, key) != opened.Directory {
		return ErrBundleInvalid
	}
	if err := root.RemoveAll(key); err != nil {
		return fmt.Errorf("remove source seed bundle: %w", err)
	}
	if err := pathx.SyncRoot(root); err != nil {
		return fmt.Errorf("sync source seed bundle cleanup: %w", err)
	}
	return nil
}

func (store *BundleStore) requireBundle(ctx context.Context, expected Bundle) (Bundle, error) {
	if expected.Manifest.BundleDigest == "" {
		return Bundle{}, ErrBundleInvalid
	}
	opened, err := store.Open(ctx, expected.Manifest.BundleDigest)
	if err != nil {
		return Bundle{}, err
	}
	if expected.Directory != "" && expected.Directory != opened.Directory || !equalManifest(expected.Manifest, opened.Manifest) {
		return Bundle{}, ErrBundleInvalid
	}
	return opened, nil
}

func manifestForPayload(payload bundlePayload) (BundleManifest, error) {
	snapshot := payload.Snapshot
	if err := Validate(snapshot); err != nil {
		return BundleManifest{}, errors.Join(ErrBundleInvalid, err)
	}
	if snapshot.State != research.SourceSnapshotDirty || snapshot.PolicyVersion != PolicyVersion || snapshot.DirtyDigest == "" || snapshot.Digest == "" {
		return BundleManifest{}, ErrBundleInvalid
	}
	manifest := BundleManifest{
		Schema: BundleSchema, PolicyVersion: snapshot.PolicyVersion, Source: snapshot.Source.String(),
		Subdir: snapshot.Subdir, GitObjectFormat: snapshot.GitObjectFormat,
		BaseCommit: snapshot.BaseCommit, HeadCommit: snapshot.HeadCommit,
		SnapshotDigest: snapshot.Digest, DirtyDigest: snapshot.DirtyDigest,
		TrackedPatch: PayloadDescriptor{Size: int64(len(payload.Patch)), SHA256: digestData(payload.Patch)},
		Untracked:    make([]BundleFile, len(payload.Untracked)), SeedPaths: append([]SeedPath{}, payload.SeedPaths...),
		Submodules: make([]BundleSubmodule, len(payload.Submodules)),
	}
	for index, file := range payload.Untracked {
		if int64(len(file.Data)) != file.Size || digestData(file.Data) != file.Digest {
			return BundleManifest{}, ErrBundleInvalid
		}
		manifest.Untracked[index] = BundleFile{
			Path: file.Path, Payload: payloadName(index), Mode: uint32(file.Mode.Perm()),
			Size: file.Size, SHA256: file.Digest,
		}
	}
	for index, submodule := range payload.Submodules {
		manifest.Submodules[index] = BundleSubmodule{Path: submodule.Path, Commit: submodule.Commit}
	}
	manifest.BundleDigest = computeBundleDigest(manifest)
	if err := validateManifest(manifest); err != nil {
		return BundleManifest{}, err
	}
	return manifest, nil
}

func decodeManifest(data []byte) (BundleManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest BundleManifest
	if err := decoder.Decode(&manifest); err != nil {
		return BundleManifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return BundleManifest{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return BundleManifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest BundleManifest) error {
	if manifest.Schema != BundleSchema || manifest.PolicyVersion != PolicyVersion || !validDigest(manifest.SnapshotDigest) || !validDigest(manifest.DirtyDigest) || !validDigest(manifest.BundleDigest) || !validDigest(manifest.TrackedPatch.SHA256) {
		return ErrBundleInvalid
	}
	sourceID, err := research.ParseIDForKind(manifest.Source, research.KindSource)
	if err != nil || sourceID.IsZero() {
		return ErrBundleInvalid
	}
	if normalized, err := research.NormalizeSourceSubdir(manifest.Subdir); err != nil || normalized != manifest.Subdir {
		return ErrBundleInvalid
	}
	objectLength := objectIDLength(manifest.GitObjectFormat)
	if objectLength == 0 || !validObjectID(manifest.BaseCommit, objectLength) || !validObjectID(manifest.HeadCommit, objectLength) {
		return ErrBundleInvalid
	}
	if manifest.TrackedPatch.Size < 0 || manifest.TrackedPatch.Size > absoluteMaxBundlePayloadBytes || len(manifest.Untracked) > maxBundleFiles || len(manifest.Submodules) > maxBundleFiles {
		return ErrBundleInvalid
	}
	total := manifest.TrackedPatch.Size
	previous := ""
	payloads := make(map[string]struct{}, len(manifest.Untracked))
	for index, file := range manifest.Untracked {
		if err := validateGitPath(file.Path); err != nil || file.Path <= previous || file.Payload != payloadName(index) || file.Mode > 0o777 || file.Size < 0 || file.Size > absoluteMaxBundlePayloadBytes || !validDigest(file.SHA256) {
			return ErrBundleInvalid
		}
		if total > absoluteMaxBundlePayloadBytes-file.Size {
			return ErrBundleInvalid
		}
		total += file.Size
		previous = file.Path
		if _, duplicate := payloads[file.Payload]; duplicate {
			return ErrBundleInvalid
		}
		payloads[file.Payload] = struct{}{}
	}
	previous = ""
	seedTotal := int64(0)
	for _, file := range manifest.SeedPaths {
		if err := validateGitPath(file.Path); err != nil || file.Path <= previous {
			return ErrBundleInvalid
		}
		if file.Exists {
			if file.Mode > 0o777 || file.Size < 0 || file.Size > absoluteMaxBundlePayloadBytes || !validDigest(file.SHA256) || seedTotal > absoluteMaxBundlePayloadBytes-file.Size {
				return ErrBundleInvalid
			}
			seedTotal += file.Size
		} else if file.Mode != 0 || file.Size != 0 || file.SHA256 != "" {
			return ErrBundleInvalid
		}
		previous = file.Path
	}
	previous = ""
	for _, submodule := range manifest.Submodules {
		if err := validateGitPath(submodule.Path); err != nil || submodule.Path <= previous || !validObjectID(submodule.Commit, objectLength) {
			return ErrBundleInvalid
		}
		previous = submodule.Path
	}
	if computeBundleDigest(manifest) != manifest.BundleDigest {
		return ErrBundleInvalid
	}
	return nil
}

func computeBundleDigest(manifest BundleManifest) string {
	framed := newFrameHash("exp.source-seed/v1")
	framed.addString("schema", manifest.Schema)
	framed.addString("policy-version", manifest.PolicyVersion)
	framed.addString("source", manifest.Source)
	framed.addString("subdir", manifest.Subdir)
	framed.addString("git-object-format", string(manifest.GitObjectFormat))
	framed.addString("base-commit", manifest.BaseCommit)
	framed.addString("head-commit", manifest.HeadCommit)
	framed.addString("snapshot-digest", manifest.SnapshotDigest)
	framed.addString("dirty-digest", manifest.DirtyDigest)
	framed.addUint("tracked-patch-size", uint64(manifest.TrackedPatch.Size))
	framed.addString("tracked-patch-sha256", manifest.TrackedPatch.SHA256)
	framed.addUint("untracked-count", uint64(len(manifest.Untracked)))
	for _, file := range manifest.Untracked {
		framed.addString("untracked-path", file.Path)
		framed.addString("untracked-payload", file.Payload)
		framed.addUint("untracked-mode", uint64(file.Mode))
		framed.addUint("untracked-size", uint64(file.Size))
		framed.addString("untracked-sha256", file.SHA256)
	}
	if manifest.SeedPaths != nil {
		framed.addUint("seed-path-count", uint64(len(manifest.SeedPaths)))
		for _, file := range manifest.SeedPaths {
			framed.addString("seed-path", file.Path)
			framed.addUint("seed-exists", boolUint(file.Exists))
			framed.addUint("seed-mode", uint64(file.Mode))
			framed.addUint("seed-size", uint64(file.Size))
			framed.addString("seed-sha256", file.SHA256)
		}
	}
	framed.addUint("submodule-count", uint64(len(manifest.Submodules)))
	for _, submodule := range manifest.Submodules {
		framed.addString("submodule-path", submodule.Path)
		framed.addString("submodule-commit", submodule.Commit)
	}
	return framed.sum()
}

func validateDirectoryEntries(root *os.Root, manifest BundleManifest) error {
	entries, err := readDirectory(root)
	if err != nil {
		return fmt.Errorf("list source seed bundle: %w", errors.Join(ErrBundleInvalid, err))
	}
	if !equalStrings(entries, []string{manifestName, trackedPatch, untrackedDir}) {
		return fmt.Errorf("source seed bundle has missing or extra entries: %w", ErrBundleInvalid)
	}
	directoryInfo, err := root.Lstat(untrackedDir)
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return fmt.Errorf("untracked payload directory is unsafe: %w", errors.Join(ErrBundleInvalid, err))
	}
	filesRoot, err := pathx.OpenRootAtNoSymlinks(root, untrackedDir)
	if err != nil {
		return fmt.Errorf("open untracked payload directory: %w", errors.Join(ErrBundleInvalid, err))
	}
	defer filesRoot.Close()
	openedInfo, err := pathx.CheckPrivateRoot(filesRoot, 0o700, "untracked source seed payload directory")
	if err != nil || !os.SameFile(directoryInfo, openedInfo) {
		return fmt.Errorf("untracked payload directory changed while opening: %w", errors.Join(ErrBundleInvalid, err))
	}
	files, err := readDirectory(filesRoot)
	if err != nil {
		return fmt.Errorf("list untracked payload directory: %w", errors.Join(ErrBundleInvalid, err))
	}
	expected := make([]string, len(manifest.Untracked))
	for index := range manifest.Untracked {
		expected[index] = payloadName(index)
	}
	if !equalStrings(files, expected) {
		return fmt.Errorf("untracked payload directory has missing or extra entries: %w", ErrBundleInvalid)
	}
	return nil
}

func validatePayloadFile(ctx context.Context, filesystem FileSystem, root *os.Root, name string, expected PayloadDescriptor) error {
	checked, err := pathx.CheckPrivateFile(root, name, 0o600, "source seed payload")
	if err != nil {
		return errors.Join(ErrBundleInvalid, err)
	}
	file, info, err := filesystem.OpenRegularFileNoFollow(root, name)
	if err != nil {
		return errors.Join(ErrBundleInvalid, err)
	}
	defer file.Close()
	if !os.SameFile(checked, info) || info.Size() != expected.Size {
		return ErrBundleInvalid
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: file}, expected.Size+1))
	if err != nil || written != expected.Size || digestBytes(hash.Sum(nil)) != expected.SHA256 {
		return errors.Join(ErrBundleInvalid, err)
	}
	after, err := file.Stat()
	pathInfo, pathErr := root.Lstat(name)
	privateAfter, privateErr := pathx.CheckPrivateFile(root, name, 0o600, "source seed payload")
	if err != nil || pathErr != nil || privateErr != nil || !os.SameFile(info, after) || !os.SameFile(info, pathInfo) ||
		!os.SameFile(info, privateAfter) || after.Size() != expected.Size {
		return errors.Join(ErrBundleInvalid, err, pathErr, privateErr)
	}
	return nil
}

func (store *BundleStore) ensureRoot() (*os.Root, string, error) {
	rootPath, err := store.rootPath()
	if err != nil {
		return nil, "", err
	}
	ancestor, relative, err := existingDirectoryAncestor(rootPath)
	if err != nil {
		return nil, "", err
	}
	base, err := store.fileSystem().OpenCanonicalRootNoSymlinks(ancestor)
	if err != nil {
		return nil, "", fmt.Errorf("open bundle root ancestor: %w", err)
	}
	defer base.Close()
	root, _, err := pathx.EnsureRootAtNoSymlinks(base, relative, 0o700)
	if err != nil {
		return nil, "", fmt.Errorf("create private bundle root: %w", err)
	}
	if _, err := pathx.CheckPrivateRoot(root, 0o700, "source seed bundle root"); err != nil {
		_ = root.Close()
		return nil, "", fmt.Errorf("bundle root is not private: %w", errors.Join(ErrBundleInvalid, err))
	}
	canonical := filepath.Clean(root.Name())
	if canonical != rootPath {
		_ = root.Close()
		return nil, "", ErrBundleInvalid
	}
	return root, canonical, nil
}

func (store *BundleStore) openRoot() (*os.Root, string, error) {
	rootPath, err := store.rootPath()
	if err != nil {
		return nil, "", err
	}
	canonical, err := store.fileSystem().Canonical(rootPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", ErrBundleNotFound
	}
	if err != nil || canonical != rootPath {
		return nil, "", fmt.Errorf("bundle root is missing or noncanonical: %w", errors.Join(ErrBundleInvalid, err))
	}
	root, err := store.fileSystem().OpenCanonicalRootNoSymlinks(rootPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", ErrBundleNotFound
		}
		return nil, "", fmt.Errorf("open bundle root: %w", errors.Join(ErrBundleInvalid, err))
	}
	if _, err := pathx.CheckPrivateRoot(root, 0o700, "source seed bundle root"); err != nil {
		_ = root.Close()
		return nil, "", fmt.Errorf("bundle root permissions are unsafe: %w", errors.Join(ErrBundleInvalid, err))
	}
	return root, rootPath, nil
}

func (store *BundleStore) rootPath() (string, error) {
	value := store.Root
	if value == "" {
		home, err := localstate.CacheHome()
		if err != nil {
			return "", fmt.Errorf("resolve XDG cache home: %w", err)
		}
		value = filepath.Join(home, "exp", "source-seeds")
	}
	if err := validateAbsoluteCanonicalInput(value); err != nil {
		return "", fmt.Errorf("bundle root must be a clean absolute path: %w", err)
	}
	canonical, err := store.fileSystem().Canonical(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize bundle root: %w", err)
	}
	if canonical != value {
		return "", fmt.Errorf("bundle root contains a symlink or noncanonical component: %w", ErrBundleInvalid)
	}
	return canonical, nil
}

func (store *BundleStore) fileSystem() FileSystem {
	if store != nil && store.FileSystem != nil {
		return store.FileSystem
	}
	return OSFileSystem{}
}

func existingDirectoryAncestor(target string) (string, string, error) {
	ancestor := target
	for {
		info, err := os.Lstat(ancestor)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", "", fmt.Errorf("bundle ancestor is not a real directory: %w", ErrBundleInvalid)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", "", ErrBundleInvalid
		}
		ancestor = parent
	}
	canonical, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", "", err
	}
	ancestor = filepath.Clean(canonical)
	relative, err := filepath.Rel(ancestor, target)
	if err != nil {
		return "", "", err
	}
	relative = filepath.ToSlash(relative)
	if relative == "" {
		relative = "."
	}
	if err := pathx.ValidateRelativePOSIX(relative, true); err != nil {
		return "", "", err
	}
	return ancestor, relative, nil
}

func createTemporaryDirectory(root *os.Root) (string, error) {
	for attempt := 0; attempt < 128; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("generate bundle staging name: %w", err)
		}
		name := ".partial-" + hex.EncodeToString(random[:])
		if err := root.Mkdir(name, 0o700); err == nil {
			return name, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create bundle staging directory: %w", err)
		}
	}
	return "", errors.New("unable to allocate bundle staging directory")
}

func chmodRootDirectory(root *os.Root, mode fs.FileMode) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(pathx.ProtectPrivateOpenFile(directory, mode), directory.Sync(), directory.Close())
}

func writePrivateFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if _, err := file.Write(data); err != nil {
		writeErr = err
	}
	writeErr = errors.Join(writeErr, pathx.ProtectPrivateOpenFile(file, 0o600), file.Sync(), file.Close())
	return writeErr
}

func readDirectory(root *os.Root) ([]string, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	sort.Strings(names)
	return names, nil
}

func payloadName(index int) string { return fmt.Sprintf("%06d", index) }

func bundleKey(digest string) (string, error) {
	if !validDigest(digest) {
		return "", ErrBundleInvalid
	}
	return "bundle-" + strings.TrimPrefix(digest, "sha256:"), nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func digestData(data []byte) string {
	digest := sha256.Sum256(data)
	return digestBytes(digest[:])
}

func digestBytes(data []byte) string { return "sha256:" + hex.EncodeToString(data) }

func boolUint(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func equalManifest(left, right BundleManifest) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}
