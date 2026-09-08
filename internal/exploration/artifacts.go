package exploration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const MetadataSchema = "exp.exploration/v1"
const ExecutionSchema = "exp.exploration-execution/v1"
const maxFiles = 4096

type Input struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

type Artifact struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MediaType   string `json:"media_type"`
	Bytes       int64  `json:"bytes"`
	Digest      string `json:"digest"`
	Storage     string `json:"storage"`
	State       string `json:"state"`
	RunID       string `json:"run_id,omitempty"`
}

type RunnerIdentity struct {
	Profile           string `json:"profile,omitempty"`
	Version           string `json:"version,omitempty"`
	BuildCommit       string `json:"build_commit,omitempty"`
	ExecutableDigest  string `json:"executable_digest"`
	EnvironmentDigest string `json:"environment_digest,omitempty"`
	SourceMatch       string `json:"source_match"`
}

// Metadata is the portable, bounded part of an execution. No local path,
// tracking endpoint, credential or resolved environment belongs here.
type Metadata struct {
	Schema        string          `json:"schema_version"`
	Storage       string          `json:"storage"`
	ContextDigest string          `json:"context_digest"`
	Inputs        []Input         `json:"inputs"`
	Runner        *RunnerIdentity `json:"runner,omitempty"`
	Artifacts     []Artifact      `json:"artifacts"`
	ArchiveState  string          `json:"archive_state"`
}

// Execution is host-private and frozen before canonical publication. Recovery
// reads this receipt, never the user's possibly changed storage preferences.
type Execution struct {
	Schema          string            `json:"schema_version"`
	Project         string            `json:"project"`
	Try             string            `json:"try"`
	Attempt         string            `json:"attempt"`
	StorageName     string            `json:"storage_name"`
	Storage         StorageProfile    `json:"storage"`
	OutputDir       string            `json:"output_dir"`
	Inputs          map[string]string `json:"inputs"`
	InputIdentities []Input           `json:"input_identities"`
	RunnerProfile   RunnerProfile     `json:"runner_profile"`
	RunnerName      string            `json:"runner_name,omitempty"`
	AllowLarge      bool              `json:"allow_large"`
}

func ExecutionPath(project, attempt string) (string, error) {
	if _, err := research.ParseUUID(project); err != nil {
		return "", err
	}
	if _, err := research.ParseIDForKind(attempt, research.KindAttempt); err != nil {
		return "", err
	}
	home, err := localstate.StateHome()
	return filepath.Join(home, "exp", "exploration", project, attempt, "execution.json"), err
}

func LoadExecution(ctx context.Context, project, attempt string) (Execution, error) {
	filename, err := ExecutionPath(project, attempt)
	if err != nil {
		return Execution{}, err
	}
	snapshot, err := localstate.Read(ctx, filename, maxStateBytes)
	if err != nil {
		return Execution{}, err
	}
	if snapshot == nil {
		return Execution{}, errors.New("exploration execution receipt is unavailable on this host")
	}
	var execution Execution
	if err := Decode(snapshot.Data, &execution); err != nil {
		return execution, err
	}
	if execution.Schema != ExecutionSchema || execution.Project != project || execution.Attempt != attempt || !AbsolutePath(execution.OutputDir) {
		return execution, errors.New("exploration execution receipt identity mismatch")
	}
	return execution, nil
}

func (e Execution) Digest() string { data, _ := json.Marshal(e); return hashBytes(data) }

func Prepare(ctx context.Context, settings Settings, project, try, attempt, selectedStorage, selectedRunner string, inputs []string, allowLarge bool) (Execution, Metadata, error) {
	prefs := settings.Preferences(project)
	if selectedStorage == "" {
		selectedStorage = prefs.Storage
	}
	profile, ok := settings.Storage[selectedStorage]
	if !ok {
		return Execution{}, Metadata{}, errors.New("storage profile is not configured; use exp storage add/use")
	}
	execution := Execution{Schema: ExecutionSchema, Project: project, Try: try, Attempt: attempt, StorageName: selectedStorage, Storage: profile,
		OutputDir: filepath.Join(profile.Root, "attempts", project, attempt, "output"), Inputs: map[string]string{}, InputIdentities: []Input{}, RunnerName: selectedRunner, AllowLarge: allowLarge}
	if selectedRunner != "" {
		var found bool
		execution.RunnerProfile, found = settings.Runners[selectedRunner]
		if !found {
			return execution, Metadata{}, errors.New("runner profile is not configured")
		}
	}
	seen := map[string]bool{}
	environmentNames := map[string]bool{}
	for _, name := range inputs {
		if !NameValid(name) || seen[name] {
			return execution, Metadata{}, errors.New("input names must be valid and unique")
		}
		seen[name] = true
		envName := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		if environmentNames[envName] {
			return execution, Metadata{}, errors.New("input names collide after conversion to environment names")
		}
		environmentNames[envName] = true
		filename, found := prefs.Inputs[name]
		if !found {
			return execution, Metadata{}, fmt.Errorf("input %s is not bound; use exp input bind", name)
		}
		identity, err := InspectInput(ctx, name, filename)
		if err != nil {
			return execution, Metadata{}, fmt.Errorf("inspect input %s: %w", name, err)
		}
		execution.Inputs[name] = filename
		execution.InputIdentities = append(execution.InputIdentities, identity)
	}
	sort.Slice(execution.InputIdentities, func(i, j int) bool { return execution.InputIdentities[i].Name < execution.InputIdentities[j].Name })
	metadata := Metadata{Schema: MetadataSchema, Storage: selectedStorage, ContextDigest: execution.Digest(), Inputs: execution.InputIdentities, Artifacts: []Artifact{}, ArchiveState: "pending"}
	return execution, metadata, nil
}

func PersistExecution(ctx context.Context, e Execution) error {
	filename, err := ExecutionPath(e.Project, e.Attempt)
	if err != nil {
		return err
	}
	// Private state locking makes a repeated preparation idempotent and prevents
	// replacement of an Attempt's output routing after a crash.
	return localstate.WithLockedFile(ctx, filename, func(root *os.Root, name string) error {
		previous, err := localstate.ReadRoot(ctx, root, name, maxStateBytes)
		if err != nil {
			return err
		}
		if previous != nil {
			var old Execution
			if err := Decode(previous.Data, &old); err != nil || old.Digest() != e.Digest() {
				return errors.New("Attempt already has a different exploration execution receipt")
			}
			return nil
		}
		if err := privateDirectory(e.OutputDir); err != nil {
			return err
		}
		if err := CheckCapacity(e.OutputDir); err != nil {
			return err
		}
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return localstate.WriteRoot(root, name, data, nil, nil)
	})
}

func privateDirectory(dir string) error {
	if !AbsolutePath(dir) {
		return errors.New("exploration directory must be an absolute path")
	}
	canonical, err := pathx.Canonical(dir)
	if err != nil {
		return err
	}
	if canonical != dir {
		return errors.New("exploration directory must not traverse symlinks")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	_, err = pathx.CheckPrivateRoot(root, 0o700, "exploration directory")
	return err
}

func MetadataFor(attempt *research.Attempt) (*Metadata, error) {
	value, found := attempt.Extensions[Namespace]
	if !found {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var metadata Metadata
	if err := Decode(data, &metadata); err != nil {
		return nil, err
	}
	if metadata.Schema != MetadataSchema {
		return nil, errors.New("unsupported exploration metadata schema")
	}
	if err := metadata.Validate(); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func (m Metadata) Validate() error {
	validDigest := func(value string) bool {
		if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
			return false
		}
		_, err := hex.DecodeString(value[7:])
		return err == nil
	}
	if m.Schema != MetadataSchema || !NameValid(m.Storage) || !validDigest(m.ContextDigest) || (m.ArchiveState != "pending" && m.ArchiveState != "saved") || len(m.Artifacts) > maxFiles {
		return errors.New("invalid exploration manifest identity or state")
	}
	seen := map[string]bool{}
	for _, a := range m.Artifacts {
		if research.ValidateCommittedPath(a.Name, false) != nil || seen[a.Name] || a.Bytes < 0 || !validDigest(a.Digest) || a.Storage != m.Storage || (a.State != "pending" && a.State != "local" && a.State != "remote") || (a.RunID != "" && !validRunID(a.RunID)) {
			return errors.New("invalid artifact identity in manifest")
		}
		seen[a.Name] = true
		if research.ValidateCommitSafeText(a.Description) != nil || len(a.Description) > 4096 {
			return errors.New("invalid artifact description")
		}
	}
	for _, input := range m.Inputs {
		if !NameValid(input.Name) || input.Bytes < 0 || !validDigest(input.Digest) || (input.Kind != "file" && input.Kind != "directory") {
			return errors.New("invalid input identity in manifest")
		}
	}
	if m.Runner != nil && !validDigest(m.Runner.ExecutableDigest) {
		return errors.New("invalid runner executable digest")
	}
	return nil
}

func SetMetadata(attempt *research.Attempt, metadata Metadata) error {
	if err := metadata.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(data) > 512<<10 {
		return errors.New("artifact manifest exceeds canonical metadata limit")
	}
	var table map[string]any
	if err := json.Unmarshal(data, &table); err != nil {
		return err
	}
	if attempt.Extensions == nil {
		attempt.Extensions = research.Extensions{}
	}
	attempt.Extensions[Namespace] = table
	return research.Validate(attempt)
}

func InspectInput(ctx context.Context, name, filename string) (Input, error) {
	if !AbsolutePath(filename) {
		return Input{}, errors.New("input must have an absolute host binding")
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return Input{}, errors.New("input is unavailable")
	}
	if info.Mode().IsRegular() {
		digest, size, err := HashFile(ctx, filename)
		return Input{Name: name, Kind: "file", Digest: digest, Bytes: size}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Input{}, errors.New("input must be a regular file or real directory")
	}
	files, err := Scan(ctx, filename)
	if err != nil {
		return Input{}, err
	}
	var size int64
	for _, file := range files {
		size += file.Bytes
	}
	data, _ := json.Marshal(files)
	return Input{Name: name, Kind: "directory", Digest: hashBytes(data), Bytes: size}, nil
}

func VerifyInputs(ctx context.Context, e Execution) error {
	for _, expected := range e.InputIdentities {
		actual, err := InspectInput(ctx, expected.Name, e.Inputs[expected.Name])
		if err != nil || actual != expected {
			return fmt.Errorf("input %s differs from the captured data identity", expected.Name)
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

func HashFile(ctx context.Context, filename string) (string, int64, error) {
	parent, err := pathx.Canonical(filepath.Dir(filename))
	if err != nil {
		return "", 0, err
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(parent)
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	file, info, err := pathx.OpenRegularFileNoFollow(root, filepath.Base(filename))
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, contextReader{ctx, io.LimitReader(file, info.Size()+1)})
	if err != nil {
		return "", 0, err
	}
	after, err := file.Stat()
	current, currentErr := root.Lstat(filepath.Base(filename))
	if err != nil || currentErr != nil || !os.SameFile(info, current) || !os.SameFile(info, after) || info.Size() != size || after.Size() != size || !info.ModTime().Equal(after.ModTime()) {
		return "", 0, errors.New("file changed while hashing")
	}
	if err := pathx.VerifyRootPath(parent, root); err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}

func Scan(ctx context.Context, dir string) ([]Artifact, error) {
	root, err := pathx.OpenCanonicalRootNoSymlinks(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := []Artifact{}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("artifact and input trees cannot contain symlinks")
		}
		if entry.IsDir() {
			return nil
		}
		if len(files) >= maxFiles {
			return errors.New("too many files; package the dataset or artifacts into an archive")
		}
		if err := research.ValidateCommittedPath(name, false); err != nil {
			return err
		}
		digest, size, err := HashFile(ctx, filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		media := mime.TypeByExtension(filepath.Ext(name))
		if media == "" {
			media = "application/octet-stream"
		}
		files = append(files, Artifact{Name: name, MediaType: media, Bytes: size, Digest: digest})
		return nil
	})
	if err == nil {
		err = pathx.VerifyRootPath(dir, root)
	}
	return files, err
}

func ObjectPath(profile StorageProfile, artifact Artifact) (string, error) {
	if !AbsolutePath(profile.Root) || !strings.HasPrefix(artifact.Digest, "sha256:") || len(artifact.Digest) != 71 {
		return "", errors.New("invalid artifact storage identity")
	}
	if _, err := hex.DecodeString(artifact.Digest[7:]); err != nil {
		return "", err
	}
	return filepath.Join(profile.Root, "objects", artifact.Digest[7:]), nil
}

// CopyVerified freezes bytes before publishing a manifest. It streams to a
// private temporary file, verifies its digest, and atomically renames it.
func CopyVerified(ctx context.Context, source, destination string, artifact Artifact) error {
	if err := privateDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		digest, size, err := HashFile(ctx, destination)
		if err == nil && digest == artifact.Digest && size == artifact.Bytes {
			return nil
		}
		return errors.New("existing artifact differs from its content identity")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	parent, err := pathx.OpenCanonicalRootNoSymlinks(filepath.Dir(source))
	if err != nil {
		return err
	}
	defer parent.Close()
	input, _, err := pathx.OpenRegularFileNoFollow(parent, filepath.Base(source))
	if err != nil {
		return err
	}
	defer input.Close()
	temp, err := os.CreateTemp(filepath.Dir(destination), ".artifact-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	h := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temp, h), contextReader{ctx, io.LimitReader(input, artifact.Bytes+1)})
	err = errors.Join(copyErr, temp.Sync(), temp.Close())
	if err != nil {
		return err
	}
	if size != artifact.Bytes || "sha256:"+hex.EncodeToString(h.Sum(nil)) != artifact.Digest {
		return errors.New("artifact changed before it could be saved")
	}
	// A competing publisher can only have the same digest. Hard linking is
	// no-clobber and preserves an existing verified object's inode.
	if err := os.Link(temp.Name(), destination); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		digest, size, hashErr := HashFile(ctx, destination)
		if hashErr != nil || digest != artifact.Digest || size != artifact.Bytes {
			return errors.New("artifact publication conflict")
		}
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
