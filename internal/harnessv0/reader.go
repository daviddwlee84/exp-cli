package harnessv0

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

const maxSourceFileBytes int64 = 64 << 20

var legacyExperimentDirectory = regexp.MustCompile(`^[0-9]{3,}-[^/]+$`)

type sourceTree struct {
	Root        string
	Fingerprint string
	Files       []sourceData
}

type sourceData struct {
	SourceFile
	Data []byte
}

func readTree(ctx context.Context, rootPath string) (*sourceTree, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	canonical, err := pathx.Canonical(rootPath)
	if err != nil {
		return nil, fmt.Errorf("canonicalize harness-v0 root: %w", err)
	}
	rootInfo, err := os.Lstat(canonical)
	if err != nil {
		return nil, fmt.Errorf("inspect harness-v0 root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("harness-v0 root must be a real directory")
	}
	var paths []string
	seen := map[string]struct{}{}
	err = filepath.WalkDir(canonical, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if candidate == canonical {
			return nil
		}
		relative, err := filepath.Rel(canonical, candidate)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if err := pathx.ValidateRelativePOSIX(relative, false); err != nil {
			return fmt.Errorf("unsafe harness-v0 path %q: %w", relative, err)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s blocks harness-v0 migration", relative)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular path %s blocks harness-v0 migration", relative)
		}
		if !sourceSurface(relative) {
			return nil
		}
		if _, duplicate := seen[relative]; duplicate {
			return fmt.Errorf("duplicate normalized harness-v0 path %s", relative)
		}
		seen[relative] = struct{}{}
		paths = append(paths, relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no harness-v0 source surfaces found in %s", canonical)
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonical)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := make([]sourceData, 0, len(paths))
	for _, relative := range paths {
		data, info, err := pathx.ReadBoundedRegularFile(ctx, root, relative, maxSourceFileBytes)
		if err != nil {
			return nil, fmt.Errorf("read harness-v0 source %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("source %s changed type while reading", relative)
		}
		files = append(files, sourceData{SourceFile: SourceFile{
			Path: relative, Bytes: int64(len(data)), SHA256: hashBytes(data), Spans: []Span{},
		}, Data: data})
	}
	if err := pathx.VerifyRootPath(canonical, root); err != nil {
		return nil, fmt.Errorf("harness-v0 root changed while reading: %w", err)
	}
	return &sourceTree{Root: canonical, Fingerprint: treeFingerprint(files), Files: files}, nil
}

func sourceSurface(relative string) bool {
	parts := strings.Split(relative, "/")
	if len(parts) == 1 {
		lower := strings.ToLower(parts[0])
		return strings.HasSuffix(lower, ".md")
	}
	return len(parts) == 2 && parts[1] == "REPORT.md" && legacyExperimentDirectory.MatchString(parts[0])
}

func treeFingerprint(files []sourceData) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("exp-harness-v0-tree\x00"))
	for _, file := range files {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(file.Path)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(file.Path))
		binary.BigEndian.PutUint64(length[:], uint64(len(file.Data)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write(file.Data)
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil))
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", digest[:])
}
