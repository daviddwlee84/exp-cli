package projection

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
)

// FileState is the exact comparison state of one generated path.
type FileState string

const (
	FileCurrent FileState = "current"
	FileMissing FileState = "missing"
	FileStale   FileState = "stale"
	FileUnsafe  FileState = "unsafe"
)

// FileResult describes one exact expected/observed generated file.
type FileResult struct {
	Path         string    `json:"path"`
	State        FileState `json:"state"`
	ExpectedHash string    `json:"expected_hash"`
	ActualHash   string    `json:"actual_hash,omitempty"`
	Detail       string    `json:"detail,omitempty"`
}

// Result is returned by both Render and Check. Check never writes; Render lists
// only paths whose exact bytes it published in Written.
type Result struct {
	Current   bool         `json:"current"`
	Changed   bool         `json:"changed"`
	Written   []string     `json:"written"`
	Unchanged []string     `json:"unchanged"`
	Drifted   []string     `json:"drifted"`
	Files     []FileResult `json:"files"`
}

type inspectedFile struct {
	generated File
	result    FileResult
	identity  fs.FileInfo
	observed  []byte
}

// RenderOptions exposes deterministic atomic-publication failure injection.
type RenderOptions struct {
	AtomicHook record.AtomicHook
}

// CheckOptions exposes a deterministic post-inspection boundary for race tests.
type CheckOptions struct {
	AfterInspect func() error
}

// Check compares all generated paths byte-for-byte and performs no writes.
func Check(ctx context.Context, inventory *record.Inventory) (Result, error) {
	return CheckWithOptions(ctx, inventory, CheckOptions{})
}

// CheckWithOptions is Check with a deterministic snapshot-verification hook.
func CheckWithOptions(ctx context.Context, inventory *record.Inventory, options CheckOptions) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	generated, err := Build(inventory)
	if err != nil {
		return emptyResult(), err
	}
	root, closeRoot, err := openProjectionRoot(inventory)
	if err != nil {
		return emptyResult(), err
	}
	if closeRoot {
		defer root.Close()
	}
	if err := verifyProjectionBinding(inventory, root); err != nil {
		return emptyResult(), err
	}
	if err := inventory.VerifySnapshotRoot(ctx, root); err != nil {
		return emptyResult(), err
	}
	inspected, err := inspectAll(ctx, root, generated)
	result := checkResultFromInspected(inspected)
	if err != nil {
		return result, err
	}
	if options.AfterInspect != nil {
		if err := options.AfterInspect(); err != nil {
			result.Current = false
			return result, err
		}
	}
	if err := verifyProjectionBinding(inventory, root); err != nil {
		result.Current = false
		return result, fmt.Errorf("projection root changed during check: %w", err)
	}
	if err := inventory.VerifySnapshotRoot(ctx, root); err != nil {
		result.Current = false
		return result, fmt.Errorf("canonical snapshot changed during projection check: %w", err)
	}
	if !result.Current {
		return result, &DriftError{Files: append([]string(nil), result.Drifted...)}
	}
	return result, nil
}

// Render builds all projections first, preflights every destination, and then
// atomically publishes only missing or stale regular files. Unsafe destination
// types block publication rather than being replaced.
func Render(ctx context.Context, inventory *record.Inventory) (Result, error) {
	return RenderWithOptions(ctx, inventory, RenderOptions{})
}

// RenderWithOptions is Render with deterministic publication hooks for tests.
func RenderWithOptions(ctx context.Context, inventory *record.Inventory, options RenderOptions) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	generated, err := Build(inventory)
	if err != nil {
		return emptyResult(), err
	}
	root, closeRoot, err := openProjectionRoot(inventory)
	if err != nil {
		return emptyResult(), err
	}
	if closeRoot {
		defer root.Close()
	}
	if err := verifyProjectionBinding(inventory, root); err != nil {
		return emptyResult(), err
	}
	if err := inventory.VerifySnapshotRoot(ctx, root); err != nil {
		return emptyResult(), err
	}
	inspected, err := inspectAll(ctx, root, generated)
	result := checkResultFromInspected(inspected)
	if err != nil {
		return result, err
	}
	written := make([]string, 0, len(result.Drifted))
	unchanged := append([]string(nil), result.Unchanged...)
	for index := range inspected {
		if err := ctx.Err(); err != nil {
			return renderResultFromInspected(inspected, written, unchanged), err
		}
		if err := inventory.VerifySnapshotRoot(ctx, root); err != nil {
			result := renderResultFromInspected(inspected, written, unchanged)
			result.Current = false
			return result, fmt.Errorf("canonical snapshot changed during render: %w", err)
		}
		entry := &inspected[index]
		switch entry.result.State {
		case FileCurrent:
			continue
		case FileMissing, FileStale:
			writeErr := record.AtomicWriteDerivedRoot(root, entry.generated.Path, entry.generated.Content, record.AtomicWriteOptions{
				Expected:        entry.identity,
				ExpectedContent: entry.observed,
				Hook:            options.AtomicHook,
				Verify: func() error {
					return verifyProjectionBinding(inventory, root)
				},
			})
			if writeErr == nil || publicationSucceeded(writeErr) {
				written = append(written, entry.generated.Path)
				entry.result.State = FileCurrent
				entry.result.ActualHash = entry.result.ExpectedHash
				entry.result.Detail = ""
			}
			if writeErr != nil {
				return renderResultFromInspected(inspected, written, unchanged), fmt.Errorf("publish projection %s: %w", entry.generated.Path, writeErr)
			}
		case FileUnsafe:
			return renderResultFromInspected(inspected, written, unchanged), fmt.Errorf("projection %s has an unsafe destination type", entry.generated.Path)
		}
	}
	result = renderResultFromInspected(inspected, written, unchanged)
	if err := verifyProjectionBinding(inventory, root); err != nil {
		result.Current = false
		return result, fmt.Errorf("projection root changed during render: %w", err)
	}
	if err := inventory.VerifySnapshotRoot(ctx, root); err != nil {
		result.Current = false
		return result, fmt.Errorf("canonical snapshot changed during render: %w", err)
	}
	return result, nil
}

func openProjectionRoot(inventory *record.Inventory) (*os.Root, bool, error) {
	if inventory == nil {
		return nil, false, errors.New("projection inventory is nil")
	}
	if root := inventory.BoundRoot(); root != nil {
		if err := verifyProjectionBinding(inventory, root); err != nil {
			return nil, false, fmt.Errorf("verify bound projection root: %w", err)
		}
		return root, false, nil
	}
	canonical, err := pathx.Canonical(inventory.Root)
	if err != nil {
		return nil, false, fmt.Errorf("resolve projection root: %w", err)
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonical)
	if err != nil {
		return nil, false, fmt.Errorf("open projection root: %w", err)
	}
	return root, true, nil
}

func verifyProjectionBinding(inventory *record.Inventory, root *os.Root) error {
	if inventory == nil || root == nil {
		return errors.New("projection binding is incomplete")
	}
	return errors.Join(pathx.VerifyRootPath(inventory.Root, root), inventory.VerifyBoundRoots())
}

func inspectAll(ctx context.Context, root *os.Root, generated []File) ([]inspectedFile, error) {
	inspected := make([]inspectedFile, 0, len(generated))
	for _, file := range generated {
		if err := ctx.Err(); err != nil {
			return inspected, err
		}
		entry, err := inspect(ctx, root, file)
		inspected = append(inspected, entry)
		if err != nil {
			return inspected, err
		}
	}
	return inspected, nil
}

func inspect(ctx context.Context, root *os.Root, generated File) (inspectedFile, error) {
	entry := inspectedFile{
		generated: generated,
		result: FileResult{
			Path:         generated.Path,
			ExpectedHash: digest(generated.Content),
		},
	}
	info, err := root.Lstat(generated.Path)
	if errors.Is(err, fs.ErrNotExist) {
		entry.result.State = FileMissing
		entry.result.Detail = "projection is missing"
		return entry, nil
	}
	if err != nil {
		entry.result.State = FileUnsafe
		entry.result.Detail = "projection cannot be inspected"
		return entry, fmt.Errorf("inspect projection %s: %w", generated.Path, err)
	}
	entry.identity = info
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		entry.result.State = FileUnsafe
		entry.result.Detail = "expected a regular non-symlink file; found " + info.Mode().String()
		return entry, fmt.Errorf("projection %s is not a regular non-symlink file", generated.Path)
	}
	file, openedInfo, err := pathx.OpenRegularFileNoFollow(root, generated.Path)
	if err != nil {
		entry.result.State = FileUnsafe
		entry.result.Detail = "projection cannot be read"
		return entry, fmt.Errorf("read projection %s: %w", generated.Path, err)
	}
	defer file.Close()
	if !os.SameFile(info, openedInfo) || info.Size() != openedInfo.Size() {
		entry.result.State = FileUnsafe
		entry.result.Detail = "projection changed while opening"
		return entry, fmt.Errorf("projection %s changed while opening", generated.Path)
	}
	if err := ctx.Err(); err != nil {
		return entry, err
	}

	// Size is a trustworthy bounded precondition only after the pathname and open
	// handle identify the same rooted regular file. A mismatch is already exact
	// drift, so never read attacker-sized generated output merely to prove it.
	if openedInfo.Size() != int64(len(generated.Content)) {
		if err := verifyInspectedProjection(root, generated.Path, openedInfo); err != nil {
			entry.result.State = FileUnsafe
			entry.result.Detail = "projection changed while inspecting size"
			return entry, err
		}
		if err := ctx.Err(); err != nil {
			return entry, err
		}
		entry.result.State = FileStale
		entry.result.Detail = "size differs"
		return entry, nil
	}

	content, err := readProjectionContent(ctx, file, len(generated.Content))
	if err != nil {
		entry.result.State = FileUnsafe
		entry.result.Detail = "projection cannot be read"
		return entry, fmt.Errorf("read projection %s: %w", generated.Path, err)
	}
	if err := verifyInspectedProjection(root, generated.Path, openedInfo); err != nil {
		entry.result.State = FileUnsafe
		entry.result.Detail = "projection changed while reading"
		return entry, err
	}
	if len(content) != len(generated.Content) {
		entry.result.State = FileStale
		entry.result.Detail = "size changed while reading"
		return entry, nil
	}
	entry.observed = content
	entry.result.ActualHash = digest(content)
	if bytes.Equal(content, generated.Content) {
		entry.result.State = FileCurrent
		return entry, nil
	}
	entry.result.State = FileStale
	entry.result.Detail = "exact bytes differ"
	return entry, nil
}

const projectionReadChunkSize = 32 << 10

func readProjectionContent(ctx context.Context, input io.Reader, expectedBytes int) ([]byte, error) {
	content := make([]byte, expectedBytes)
	for offset := 0; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(offset+projectionReadChunkSize, len(content))
		count, err := input.Read(content[offset:end])
		offset += count
		if err != nil {
			if errors.Is(err, io.EOF) {
				if contextErr := ctx.Err(); contextErr != nil {
					return nil, contextErr
				}
				return content[:offset], nil
			}
			return nil, err
		}
		if count == 0 {
			return nil, io.ErrNoProgress
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return content, nil
}

func verifyInspectedProjection(root *os.Root, path string, opened fs.FileInfo) error {
	finalInfo, err := root.Lstat(path)
	if err != nil {
		return fmt.Errorf("projection %s changed while reading: %w", path, err)
	}
	if finalInfo.Mode()&os.ModeSymlink != 0 || !finalInfo.Mode().IsRegular() || !os.SameFile(opened, finalInfo) || finalInfo.Size() != opened.Size() {
		return fmt.Errorf("projection %s changed while reading", path)
	}
	return nil
}

func checkResultFromInspected(inspected []inspectedFile) Result {
	result := emptyResult()
	result.Current = len(inspected) == len(projectionFiles)
	for _, entry := range inspected {
		result.Files = append(result.Files, entry.result)
		if entry.result.State == FileCurrent {
			result.Unchanged = append(result.Unchanged, entry.result.Path)
			continue
		}
		result.Current = false
		result.Drifted = append(result.Drifted, entry.result.Path)
	}
	return result
}

func renderResultFromInspected(inspected []inspectedFile, written, unchanged []string) Result {
	result := emptyResult()
	result.Written = append(result.Written, written...)
	result.Unchanged = append(result.Unchanged, unchanged...)
	result.Changed = len(result.Written) > 0
	result.Current = len(inspected) == len(projectionFiles)
	for _, entry := range inspected {
		result.Files = append(result.Files, entry.result)
		if entry.result.State != FileCurrent {
			result.Current = false
			result.Drifted = append(result.Drifted, entry.result.Path)
		}
	}
	return result
}

func emptyResult() Result {
	return Result{Written: []string{}, Unchanged: []string{}, Drifted: []string{}, Files: []FileResult{}}
}

func publicationSucceeded(err error) bool {
	var publication *record.PublicationError
	return errors.As(err, &publication) && publication.Published
}
