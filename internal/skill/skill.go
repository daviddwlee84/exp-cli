// Package skill embeds, installs, and verifies exp-cli's version-matched agent
// skill. The binary is authoritative for both its command contract and the
// research guidance shipped with that contract.
package skill

import (
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const (
	// Name is the installed directory and agent-skill name.
	Name = "exp-cli"
	// SchemaVersion identifies the metadata contract understood by Check.
	SchemaVersion = "exp.skill/v1"
	// SkillVersion identifies this revision of the methodology and command guide.
	SkillVersion = "1"

	embeddedRoot = "exp-cli"
	hashDomain   = "exp-cli-skill-content-v1"
)

//go:embed all:exp-cli
var embeddedFiles embed.FS

// Render returns the embedded SKILL.md exactly as shipped in this build.
func Render() (string, error) {
	content, err := embeddedFiles.ReadFile(path.Join(embeddedRoot, "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("read bundled %s skill: %w", Name, err)
	}
	return string(content), nil
}

// Files returns a fresh map of slash-separated relative file paths to exact
// embedded content. Mutating the returned map or byte slices cannot alter later
// calls.
func Files() (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := fs.WalkDir(embeddedFiles, embeddedRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := embeddedFiles.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read embedded file %s: %w", name, err)
		}
		rel := strings.TrimPrefix(name, embeddedRoot+"/")
		if rel == name || rel == "" || !fs.ValidPath(rel) {
			return fmt.Errorf("invalid embedded skill path %q", name)
		}
		files[rel] = append([]byte(nil), content...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk bundled %s skill: %w", Name, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no %s skill files are embedded in this build", Name)
	}
	return files, nil
}

// FilePaths returns the embedded paths in bytewise lexical order.
func FilePaths() ([]string, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	return sortedPaths(files), nil
}

// ManifestFile describes one exact file in the embedded skill.
type ManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ManifestInfo is a deterministic description of the embedded skill. Hash is
// framed over schema version, skill version, every sorted path, and every exact
// file byte sequence, so path/content boundary ambiguities cannot collide.
type ManifestInfo struct {
	Name          string         `json:"name"`
	SchemaVersion string         `json:"schema_version"`
	SkillVersion  string         `json:"skill_version"`
	Hash          string         `json:"hash"`
	Files         []ManifestFile `json:"files"`
}

// Manifest returns the deterministic manifest for this build.
func Manifest() (ManifestInfo, error) {
	files, err := Files()
	if err != nil {
		return ManifestInfo{}, err
	}
	return manifestFor(files), nil
}

// ManifestHash returns the deterministic SHA-256 content-manifest hash.
func ManifestHash() (string, error) {
	manifest, err := Manifest()
	if err != nil {
		return "", err
	}
	return manifest.Hash, nil
}

// ContentHash is an explicit alias for ManifestHash. The manifest hash covers
// file names as well as content, rather than hashing an ambiguous concatenation.
func ContentHash() (string, error) {
	return ManifestHash()
}

func manifestFor(files map[string][]byte) ManifestInfo {
	manifest := ManifestInfo{
		Name:          Name,
		SchemaVersion: SchemaVersion,
		SkillVersion:  SkillVersion,
		Files:         make([]ManifestFile, 0, len(files)),
	}
	for _, name := range sortedPaths(files) {
		content := files[name]
		manifest.Files = append(manifest.Files, ManifestFile{
			Path:   name,
			Size:   int64(len(content)),
			SHA256: digest(content),
		})
	}
	manifest.Hash = framedContentHash(files)
	return manifest
}

func framedContentHash(files map[string][]byte) string {
	hash := sha256.New()
	writeFrame(hash, []byte(hashDomain))
	writeFrame(hash, []byte(SchemaVersion))
	writeFrame(hash, []byte(SkillVersion))
	for _, name := range sortedPaths(files) {
		writeFrame(hash, []byte(name))
		writeFrame(hash, files[name])
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func writeFrame(hash interface{ Write([]byte) (int, error) }, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = hash.Write(size[:])
	_, _ = hash.Write(value)
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("sha256:%x", sum)
}

func sortedPaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}
