package record

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	// MaxRecordBytes is the documented upper bound for one canonical Markdown
	// record, including TOML front matter and body. Inventory and direct record
	// reads reject larger files before decoding them.
	MaxRecordBytes int64 = 8 << 20
)

var ErrInventoryChanged = errors.New("canonical inventory changed during operation")

// Inventory contains every locally valid canonical record plus all file-scoped
// schema, layout, duplicate, ownership, and graph diagnostics.
type Inventory struct {
	Root        string
	Project     *Document
	Documents   []*Document
	Diagnostics []Diagnostic

	byID        map[research.ID][]*Document
	locations   map[string]Location
	boundRoot   *os.Root
	boundVerify func() error
	identities  map[string]fs.FileInfo
	snapshot    [sha256.Size]byte
	hasSnapshot bool
}

// LoadInventory parses canonical paths below root with a background context.
func LoadInventory(root string) (*Inventory, error) {
	return LoadInventoryContext(context.Background(), root)
}

// LoadInventoryContext parses canonical paths below one opened canonical root.
// Generated projections and unrelated subtrees are ignored, and traversal stops
// promptly between directory entries and bounded file reads when ctx is canceled.
func LoadInventoryContext(ctx context.Context, rootPath string) (*Inventory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	canonical, err := pathx.Canonical(rootPath)
	if err != nil {
		return nil, fmt.Errorf("canonicalize experiments root: %w", err)
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonical)
	if err != nil {
		return nil, fmt.Errorf("open experiments root: %w", err)
	}
	defer root.Close()
	inventory, err := LoadInventoryRoot(ctx, root, canonical)
	if inventory != nil {
		inventory.boundRoot = nil
	}
	return inventory, err
}

// LoadInventoryRoot scans through root without resolving the experiments-root
// pathname again. rootPath supplies the canonical display/containment path and
// must still identify root when the scan completes.
func LoadInventoryRoot(ctx context.Context, root *os.Root, rootPath string) (*Inventory, error) {
	return loadInventoryRoot(ctx, root, rootPath, nil)
}

func loadInventoryRoot(ctx context.Context, root *os.Root, rootPath string, beforeRead func(string)) (*Inventory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if root == nil {
		return nil, errors.New("inventory requires an opened canonical root")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("make experiments root absolute: %w", err)
	}
	absolute = filepath.Clean(absolute)
	rootInfo, err := root.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("inspect experiments root: %w", err)
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("experiments root %s is not a directory", absolute)
	}

	inventory := newInventory(absolute)
	inventory.boundRoot = root
	scanner := inventoryScanner{ctx: ctx, inventory: inventory, snapshot: sha256.New(), beforeRead: beforeRead}
	if err := scanner.scanRoot(root); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := pathx.VerifyRootPath(absolute, root); err != nil {
		return nil, fmt.Errorf("experiments root changed during inventory scan: %w", err)
	}
	copy(inventory.snapshot[:], scanner.snapshot.Sum(nil))
	inventory.hasSnapshot = true
	inventory.finalize()
	return inventory, nil
}

// BoundRoot returns the opened root used to construct this inventory while a
// Store.WithInventorySnapshot callback is active. Ordinary LoadInventory callers
// receive nil because that root is closed before return.
func (inventory *Inventory) BoundRoot() *os.Root {
	if inventory == nil {
		return nil
	}
	return inventory.boundRoot
}

// VerifyBoundRoots confirms any additional root identities attached by Store.
func (inventory *Inventory) VerifyBoundRoots() error {
	if inventory == nil || inventory.boundVerify == nil {
		return nil
	}
	return inventory.boundVerify()
}

// VerifySnapshot rescans the canonical paths and rejects any path, type, mode,
// or byte change observed since inventory load.
func (inventory *Inventory) VerifySnapshot(ctx context.Context) error {
	if inventory == nil || !inventory.hasSnapshot {
		return errors.New("inventory has no canonical snapshot")
	}
	if inventory.boundRoot != nil {
		return inventory.VerifySnapshotRoot(ctx, inventory.boundRoot)
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(inventory.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	return inventory.VerifySnapshotRoot(ctx, root)
}

// VerifySnapshotRoot verifies inventory through a caller-owned opened root.
func (inventory *Inventory) VerifySnapshotRoot(ctx context.Context, root *os.Root) error {
	if inventory == nil || !inventory.hasSnapshot {
		return errors.New("inventory has no canonical snapshot")
	}
	if root == nil {
		return errors.New("inventory snapshot root is nil")
	}
	current, err := LoadInventoryRoot(ctx, root, inventory.Root)
	if err != nil {
		return err
	}
	if current.snapshot != inventory.snapshot || len(current.identities) != len(inventory.identities) {
		return ErrInventoryChanged
	}
	for relative, expected := range inventory.identities {
		observed, found := current.identities[relative]
		if !found || !os.SameFile(expected, observed) {
			return ErrInventoryChanged
		}
	}
	return nil
}

type inventoryScanner struct {
	ctx        context.Context
	inventory  *Inventory
	snapshot   hash.Hash
	beforeRead func(string)
}

func (scanner *inventoryScanner) scanRoot(root *os.Root) error {
	entries, err := readRootDirectory(root)
	if err != nil {
		return fmt.Errorf("read experiments root: %w", err)
	}
	for _, entry := range entries {
		if err := scanner.ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		info, err := root.Lstat(name)
		if err != nil {
			scanner.inventory.addDiagnostic(name, "record.io", err.Error())
			continue
		}
		if IsAtomicTempName(name) {
			scanner.inspectAtomicTemporary(name, info)
			continue
		}
		if _, generated := generatedProjectionNames[name]; generated {
			continue
		}
		switch name {
		case PlansDir, FindingsDir, DecisionsDir:
			scanner.note(name, info, nil)
			if !scanner.openAndScanFlat(root, name, name, info) {
				scanner.inspectCandidate(root, name, name, info)
			}
			continue
		case ProjectFile:
			scanner.note(name, info, nil)
			scanner.inspectCandidate(root, name, name, info)
			continue
		}
		if experimentDirPattern.MatchString(name) && info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			scanner.note(name, info, nil)
			scanner.scanExperiment(root, name, info)
			continue
		}
		if strings.HasPrefix(name, "e-") || reservedPathPrefix(name) {
			scanner.note(name, info, nil)
			scanner.inspectCandidate(root, name, name, info)
		}
	}
	return nil
}

func (scanner *inventoryScanner) scanExperiment(root *os.Root, directory string, expected fs.FileInfo) {
	experiment, err := pathx.OpenRootAtNoSymlinks(root, directory)
	if err != nil {
		scanner.inventory.addDiagnostic(directory, "record.io", err.Error())
		return
	}
	defer experiment.Close()
	opened, err := experiment.Stat(".")
	if err != nil || !os.SameFile(expected, opened) {
		scanner.inventory.addDiagnostic(directory, "record.io", fmt.Sprintf("experiment directory changed while opening: %v", err))
		return
	}
	entries, err := readRootDirectory(experiment)
	if err != nil {
		scanner.inventory.addDiagnostic(directory, "record.io", err.Error())
		return
	}
	for _, entry := range entries {
		if scanner.ctx.Err() != nil {
			return
		}
		name := entry.Name()
		relative := path.Join(directory, name)
		info, statErr := experiment.Lstat(name)
		if statErr != nil {
			scanner.inventory.addDiagnostic(relative, "record.io", statErr.Error())
			continue
		}
		if IsAtomicTempName(name) {
			scanner.inspectAtomicTemporary(relative, info)
			continue
		}
		scanner.note(relative, info, nil)
		if name == "runs" || name == "attempts" {
			if !scanner.openAndScanFlat(experiment, name, relative, info) {
				scanner.inspectCandidate(experiment, name, relative, info)
			}
			continue
		}
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			scanner.scanInvalidTree(experiment, name, relative, info)
			continue
		}
		scanner.inspectCandidate(experiment, name, relative, info)
	}
	if err := pathx.VerifyRootAt(root, directory, experiment); err != nil {
		scanner.inventory.addDiagnostic(directory, "record.io", "experiment directory changed during scan: "+err.Error())
	}
}

func (scanner *inventoryScanner) openAndScanFlat(parent *os.Root, name, relative string, expected fs.FileInfo) bool {
	if expected.Mode()&os.ModeSymlink != 0 || !expected.IsDir() {
		return false
	}
	directory, err := pathx.OpenRootAtNoSymlinks(parent, name)
	if err != nil {
		scanner.inventory.addDiagnostic(relative, "record.io", err.Error())
		return true
	}
	defer directory.Close()
	opened, err := directory.Stat(".")
	if err != nil || !os.SameFile(expected, opened) {
		scanner.inventory.addDiagnostic(relative, "record.io", fmt.Sprintf("canonical directory changed while opening: %v", err))
		return true
	}
	entries, err := readRootDirectory(directory)
	if err != nil {
		scanner.inventory.addDiagnostic(relative, "record.io", err.Error())
		return true
	}
	for _, entry := range entries {
		if scanner.ctx.Err() != nil {
			return true
		}
		entryRelative := path.Join(relative, entry.Name())
		info, statErr := directory.Lstat(entry.Name())
		if statErr != nil {
			scanner.inventory.addDiagnostic(entryRelative, "record.io", statErr.Error())
			continue
		}
		if IsAtomicTempName(entry.Name()) {
			scanner.inspectAtomicTemporary(entryRelative, info)
			continue
		}
		scanner.note(entryRelative, info, nil)
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			scanner.scanInvalidTree(directory, entry.Name(), entryRelative, info)
			continue
		}
		scanner.inspectCandidate(directory, entry.Name(), entryRelative, info)
	}
	if err := pathx.VerifyRootAt(parent, name, directory); err != nil {
		scanner.inventory.addDiagnostic(relative, "record.io", "canonical directory changed during scan: "+err.Error())
	}
	return true
}

// scanInvalidTree retains diagnostics for files placed below reserved trees,
// while scanRoot itself prunes every unrelated top-level tree. No entry here can
// become a valid record, so regular files are classified only for diagnostics.
func (scanner *inventoryScanner) scanInvalidTree(parent *os.Root, name, relative string, expected fs.FileInfo) {
	directory, err := pathx.OpenRootAtNoSymlinks(parent, name)
	if err != nil {
		scanner.inventory.addDiagnostic(relative, "record.io", err.Error())
		return
	}
	defer directory.Close()
	opened, err := directory.Stat(".")
	if err != nil || !os.SameFile(expected, opened) {
		scanner.inventory.addDiagnostic(relative, "record.io", fmt.Sprintf("reserved directory changed while opening: %v", err))
		return
	}
	entries, err := readRootDirectory(directory)
	if err != nil {
		scanner.inventory.addDiagnostic(relative, "record.io", err.Error())
		return
	}
	for _, entry := range entries {
		if scanner.ctx.Err() != nil {
			return
		}
		childRelative := path.Join(relative, entry.Name())
		info, statErr := directory.Lstat(entry.Name())
		if statErr != nil {
			scanner.inventory.addDiagnostic(childRelative, "record.io", statErr.Error())
			continue
		}
		scanner.note(childRelative, info, nil)
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			scanner.scanInvalidTree(directory, entry.Name(), childRelative, info)
			continue
		}
		scanner.inspectCandidate(directory, entry.Name(), childRelative, info)
	}
}

func (scanner *inventoryScanner) inspectAtomicTemporary(relative string, info fs.FileInfo) {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		scanner.inventory.addDiagnostic(relative, "path.symlink", "atomic temporary path is a symlink")
	case !info.Mode().IsRegular():
		scanner.inventory.addDiagnostic(relative, "record.file_type", "atomic temporary path is not a regular file")
	}
}

func (scanner *inventoryScanner) inspectCandidate(parent *os.Root, name, relative string, info fs.FileInfo) {
	location, recognized, classifyErr := ClassifyPath(relative)
	if classifyErr != nil {
		scanner.inventory.Diagnostics = append(scanner.inventory.Diagnostics, DiagnosticsForError(relative, classifyErr)...)
		return
	}
	if !recognized {
		if info.Mode()&os.ModeSymlink != 0 && reservedPathPrefix(relative) {
			scanner.inventory.addDiagnostic(relative, "path.symlink", "a symlink occupies a reserved canonical path")
		}
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		scanner.inventory.addDiagnostic(relative, "path.symlink", "canonical records may not be symlinks")
		return
	}
	if !info.Mode().IsRegular() {
		scanner.inventory.addDiagnostic(relative, "record.file_type", "canonical record is not a regular file")
		return
	}
	mode := info.Mode()
	if runtime.GOOS != "windows" && (mode.Perm() != 0o644 || mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0) {
		scanner.inventory.addDiagnostic(relative, "record.permissions", fmt.Sprintf("canonical record mode is %s, want 0644", mode))
	}
	if scanner.beforeRead != nil {
		scanner.beforeRead(relative)
	}
	data, openedInfo, readErr := pathx.ReadBoundedRegularFile(scanner.ctx, parent, name, MaxRecordBytes)
	if readErr != nil {
		if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
			return
		}
		if errors.Is(readErr, pathx.ErrFileTooLarge) {
			scanner.inventory.addDiagnostic(relative, "record.size", fmt.Sprintf("canonical record exceeds %d-byte limit", MaxRecordBytes))
		} else if errors.Is(readErr, pathx.ErrSymlink) {
			scanner.inventory.addDiagnostic(relative, "path.symlink", "canonical records may not be symlinks")
		} else {
			scanner.inventory.addDiagnostic(relative, "record.io", readErr.Error())
		}
		return
	}
	if !os.SameFile(info, openedInfo) {
		scanner.inventory.addDiagnostic(relative, "record.io", "canonical record changed before read")
		return
	}
	scanner.note(relative, openedInfo, data)
	document, decodeErr := Decode(data)
	if decodeErr != nil {
		scanner.inventory.Diagnostics = append(scanner.inventory.Diagnostics, DiagnosticsForError(relative, decodeErr)...)
		return
	}
	document.Path = relative
	if pathErr := ValidateDocumentPath(location, document); pathErr != nil {
		scanner.inventory.Diagnostics = append(scanner.inventory.Diagnostics, DiagnosticsForError(relative, pathErr)...)
		return
	}
	scanner.inventory.validateCommittedPathContainment(document)
	scanner.inventory.Documents = append(scanner.inventory.Documents, document)
	scanner.inventory.locations[relative] = location
}

func (scanner *inventoryScanner) note(relative string, info fs.FileInfo, content []byte) {
	scanner.inventory.identities[relative] = info
	_, _ = fmt.Fprintf(scanner.snapshot, "%d:%s:%d:%d:", len(relative), relative, uint32(info.Mode()), len(content))
	_, _ = scanner.snapshot.Write(content)
	_, _ = scanner.snapshot.Write([]byte{0})
}

func readRootDirectory(root *os.Root) ([]fs.DirEntry, error) {
	before, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	opened, statErr := directory.Stat()
	if statErr != nil || !os.SameFile(before, opened) {
		_ = directory.Close()
		return nil, fmt.Errorf("directory changed while opening: %w", statErr)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return nil, fmt.Errorf("directory changed while reading: %w", err)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	return entries, nil
}

// InventoryFromDocuments validates an in-memory candidate snapshot. It is used
// while holding the project lock before a canonical publication.
func InventoryFromDocuments(root string, documents []*Document) *Inventory {
	inventory := newInventory(root)
	seenPath := map[string]struct{}{}
	for _, input := range documents {
		if input == nil {
			inventory.addDiagnostic("", "record.nil", "candidate document is nil")
			continue
		}
		document := input.Clone()
		if _, found := seenPath[document.Path]; found {
			inventory.addDiagnostic(document.Path, "record.duplicate_path", "more than one candidate record uses this path")
			continue
		}
		seenPath[document.Path] = struct{}{}
		if err := research.Validate(document.Record); err != nil {
			inventory.Diagnostics = append(inventory.Diagnostics, DiagnosticsForError(document.Path, err)...)
			continue
		}
		location, recognized, err := ClassifyPath(document.Path)
		if err != nil || !recognized {
			if err == nil {
				err = layoutError(document.Path, "path is not canonical")
			}
			inventory.Diagnostics = append(inventory.Diagnostics, DiagnosticsForError(document.Path, err)...)
			continue
		}
		if err := ValidateDocumentPath(location, document); err != nil {
			inventory.Diagnostics = append(inventory.Diagnostics, DiagnosticsForError(document.Path, err)...)
			continue
		}
		inventory.validateCommittedPathContainment(document)
		revision, err := Revision(document)
		if err != nil {
			inventory.Diagnostics = append(inventory.Diagnostics, DiagnosticsForError(document.Path, err)...)
			continue
		}
		document.Revision = revision
		inventory.Documents = append(inventory.Documents, document)
		inventory.locations[document.Path] = location
	}
	inventory.finalize()
	return inventory
}

func newInventory(root string) *Inventory {
	return &Inventory{
		Root:       root,
		byID:       make(map[research.ID][]*Document),
		locations:  make(map[string]Location),
		identities: make(map[string]fs.FileInfo),
	}
}

func (inventory *Inventory) finalize() {
	sort.SliceStable(inventory.Documents, func(i, j int) bool { return inventory.Documents[i].Path < inventory.Documents[j].Path })
	projectCount := 0
	for _, document := range inventory.Documents {
		if document.Kind() == research.KindProject {
			projectCount++
			if inventory.Project == nil {
				inventory.Project = document
			}
			continue
		}
		id, ok := document.ID()
		if ok {
			inventory.byID[id] = append(inventory.byID[id], document)
		}
	}
	switch {
	case projectCount == 0:
		inventory.addDiagnostic(ProjectFile, "project.missing", "canonical inventory has no PROJECT.md")
	case projectCount > 1:
		inventory.addDiagnostic(ProjectFile, "project.multiple", "canonical inventory has more than one Project record")
	}
	inventory.validateDuplicates()
	inventory.validateRelationships()
	inventory.validateCycles()
	inventory.sortDiagnostics()
}

func (inventory *Inventory) validateDuplicates() {
	ids := make([]research.ID, 0, len(inventory.byID))
	for id := range inventory.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, id := range ids {
		documents := inventory.byID[id]
		if len(documents) < 2 {
			continue
		}
		for _, document := range documents {
			inventory.addDiagnostic(document.Path, "record.duplicate_id", fmt.Sprintf("ID %s occurs in %d records", id, len(documents)))
		}
	}

	aliases := map[string][]*Document{}
	for _, document := range inventory.Documents {
		common := document.Record.GetCommon()
		if common == nil {
			continue
		}
		for _, alias := range common.LegacyAliases {
			key := document.Kind().String() + "\x00" + alias
			aliases[key] = append(aliases[key], document)
		}
	}
	keys := make([]string, 0, len(aliases))
	for key := range aliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		documents := aliases[key]
		if len(documents) < 2 {
			continue
		}
		alias := strings.SplitN(key, "\x00", 2)[1]
		for _, document := range documents {
			inventory.addDiagnostic(document.Path, "record.duplicate_alias", fmt.Sprintf("legacy alias %s occurs in %d %s records", alias, len(documents), document.Kind()))
		}
	}
}

func (inventory *Inventory) validateRelationships() {
	for _, document := range inventory.Documents {
		switch value := document.Record.(type) {
		case *research.Plan:
			for index, id := range value.Assumptions {
				inventory.require(document, fmt.Sprintf("assumptions[%d]", index), id, research.KindFinding)
			}
			if !value.ResultingExperiment.IsZero() {
				inventory.require(document, "resulting_experiment", value.ResultingExperiment, research.KindExperiment)
			}
		case *research.Experiment:
			if value.ClosureDetail != nil && !value.ClosureDetail.SupersededBy.IsZero() {
				inventory.require(document, "closure_detail.superseded_by", value.ClosureDetail.SupersededBy, research.KindExperiment)
			}
			if value.Conclusion != nil {
				for index, evidence := range value.Conclusion.Evidence {
					target := inventory.require(document, fmt.Sprintf("conclusion.evidence[%d].run", index), evidence.Run, research.KindRun)
					if target != nil {
						run := target.Record.(*research.Run)
						if run.Experiment != value.ID {
							inventory.addDiagnostic(document.Path, "reference.wrong_owner", fmt.Sprintf("conclusion evidence Run %s belongs to Experiment %s", run.ID, run.Experiment))
						}
					}
				}
			}
		case *research.Run:
			target := inventory.require(document, "experiment", value.Experiment, research.KindExperiment)
			if target != nil {
				inventory.validateRunLocation(document, target)
			}
		case *research.Attempt:
			target := inventory.require(document, "run", value.Run, research.KindRun)
			if target != nil {
				inventory.validateAttemptLocation(document, target)
				run := target.Record.(*research.Run)
				experimentDocument := inventory.unique(run.Experiment)
				if experimentDocument != nil {
					experiment := experimentDocument.Record.(*research.Experiment)
					if experiment.Design.DesignLockedAt == nil || experiment.Design.DesignDigest == "" {
						inventory.addDiagnostic(document.Path, "experiment.design_unlocked", fmt.Sprintf("Attempt %s cannot be registered before Experiment %s locks its design", value.ID, experiment.ID))
					} else if experiment.Design.DesignLockedAt.After(value.CreatedAt) {
						inventory.addDiagnostic(document.Path, "experiment.design_unlocked", fmt.Sprintf("Attempt %s predates Experiment %s's design lock", value.ID, experiment.ID))
					}
				}
			}
		case *research.Finding:
			for index, evidence := range value.Evidence {
				expected := research.KindRun
				if evidence.Kind == research.FindingEvidenceExperiment {
					expected = research.KindExperiment
				}
				inventory.require(document, fmt.Sprintf("evidence[%d].ref", index), evidence.Ref, expected)
			}
			for index, id := range value.Weakens {
				inventory.require(document, fmt.Sprintf("weakens[%d]", index), id, research.KindFinding)
			}
			for index, id := range value.Overturns {
				inventory.require(document, fmt.Sprintf("overturns[%d]", index), id, research.KindFinding)
			}
		case *research.Decision:
			for index, id := range value.BasedOn {
				inventory.require(document, fmt.Sprintf("based_on[%d]", index), id, research.KindFinding)
			}
			for index, id := range value.Supersedes {
				inventory.require(document, fmt.Sprintf("supersedes[%d]", index), id, research.KindDecision)
			}
		}
	}
}

func (inventory *Inventory) require(source *Document, field string, id research.ID, expected research.Kind) *Document {
	if id.IsZero() {
		return nil
	}
	if id.Kind() != expected {
		inventory.addDiagnostic(source.Path, "reference.wrong_kind", fmt.Sprintf("%s points to %s, expected %s", field, id.Kind(), expected))
		return nil
	}
	documents := inventory.byID[id]
	switch len(documents) {
	case 0:
		inventory.addDiagnostic(source.Path, "reference.not_found", fmt.Sprintf("%s target %s does not exist", field, id))
		return nil
	case 1:
		return documents[0]
	default:
		inventory.addDiagnostic(source.Path, "reference.ambiguous", fmt.Sprintf("%s target %s has duplicate records", field, id))
		return nil
	}
}

func (inventory *Inventory) unique(id research.ID) *Document {
	if documents := inventory.byID[id]; len(documents) == 1 {
		return documents[0]
	}
	return nil
}

func (inventory *Inventory) validateRunLocation(runDocument, experimentDocument *Document) {
	runLocation, runFound := inventory.locations[runDocument.Path]
	experimentLocation, experimentFound := inventory.locations[experimentDocument.Path]
	if !runFound || !experimentFound || runLocation.ExperimentDir == "" || experimentLocation.ExperimentDir == "" {
		return
	}
	if runLocation.ExperimentDir != experimentLocation.ExperimentDir {
		inventory.addDiagnostic(runDocument.Path, "relationship.wrong_owner", fmt.Sprintf("Run is stored under %s but owns Experiment in %s", runLocation.ExperimentDir, experimentLocation.ExperimentDir))
	}
}

func (inventory *Inventory) validateAttemptLocation(attemptDocument, runDocument *Document) {
	attemptLocation, attemptFound := inventory.locations[attemptDocument.Path]
	runLocation, runFound := inventory.locations[runDocument.Path]
	if !attemptFound || !runFound || attemptLocation.ExperimentDir == "" || runLocation.ExperimentDir == "" {
		return
	}
	if attemptLocation.ExperimentDir != runLocation.ExperimentDir {
		inventory.addDiagnostic(attemptDocument.Path, "relationship.wrong_owner", fmt.Sprintf("Attempt is stored under %s but its Run is under %s", attemptLocation.ExperimentDir, runLocation.ExperimentDir))
	}
}

func (inventory *Inventory) validateCycles() {
	inventory.findCycles(research.KindFinding, func(record research.Record) []research.ID {
		finding := record.(*research.Finding)
		return append(append([]research.ID(nil), finding.Weakens...), finding.Overturns...)
	})
	inventory.findCycles(research.KindDecision, func(record research.Record) []research.ID {
		return append([]research.ID(nil), record.(*research.Decision).Supersedes...)
	})
	inventory.findCycles(research.KindExperiment, func(record research.Record) []research.ID {
		experiment := record.(*research.Experiment)
		if experiment.ClosureDetail == nil || experiment.ClosureDetail.SupersededBy.IsZero() {
			return nil
		}
		return []research.ID{experiment.ClosureDetail.SupersededBy}
	})
}

func (inventory *Inventory) findCycles(kind research.Kind, edges func(research.Record) []research.ID) {
	nodes := make([]research.ID, 0)
	graph := map[research.ID][]research.ID{}
	for id, documents := range inventory.byID {
		if id.Kind() != kind || len(documents) != 1 {
			continue
		}
		nodes = append(nodes, id)
		for _, target := range edges(documents[0].Record) {
			if target.Kind() == kind && inventory.unique(target) != nil {
				graph[id] = append(graph[id], target)
			}
		}
		sort.Slice(graph[id], func(i, j int) bool { return graph[id][i].String() < graph[id][j].String() })
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].String() < nodes[j].String() })
	state := map[research.ID]uint8{}
	positions := map[research.ID]int{}
	var stack []research.ID
	cyclic := map[research.ID]struct{}{}
	var visit func(research.ID)
	visit = func(node research.ID) {
		state[node] = 1
		positions[node] = len(stack)
		stack = append(stack, node)
		for _, target := range graph[node] {
			switch state[target] {
			case 0:
				visit(target)
			case 1:
				for _, member := range stack[positions[target]:] {
					cyclic[member] = struct{}{}
				}
			}
		}
		stack = stack[:len(stack)-1]
		delete(positions, node)
		state[node] = 2
	}
	for _, node := range nodes {
		if state[node] == 0 {
			visit(node)
		}
	}
	cycleIDs := make([]research.ID, 0, len(cyclic))
	for id := range cyclic {
		cycleIDs = append(cycleIDs, id)
	}
	sort.Slice(cycleIDs, func(i, j int) bool { return cycleIDs[i].String() < cycleIDs[j].String() })
	for _, id := range cycleIDs {
		if document := inventory.unique(id); document != nil {
			inventory.addDiagnostic(document.Path, "reference.cycle", fmt.Sprintf("%s relationship graph contains a cycle through %s", kind, id))
		}
	}
}

func (inventory *Inventory) addDiagnostic(path, code, message string) {
	inventory.Diagnostics = append(inventory.Diagnostics, Diagnostic{Path: path, Code: code, Message: message})
}

func (inventory *Inventory) sortDiagnostics() {
	sort.SliceStable(inventory.Diagnostics, func(i, j int) bool {
		left, right := inventory.Diagnostics[i], inventory.Diagnostics[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
	out := inventory.Diagnostics[:0]
	for _, diagnostic := range inventory.Diagnostics {
		if len(out) > 0 && out[len(out)-1] == diagnostic {
			continue
		}
		out = append(out, diagnostic)
	}
	inventory.Diagnostics = out
}

func (inventory *Inventory) validateCommittedPathContainment(document *Document) {
	if inventory == nil || document == nil || document.Record == nil {
		return
	}
	root, err := filepath.Abs(inventory.Root)
	if err != nil {
		inventory.addDiagnostic(document.Path, "path.resolve", "cannot establish the Git worktree root")
		return
	}
	// In v1 Inventory.Root is the experiments/ directory, so committed Run and
	// Attempt paths are resolved relative to its parent Git worktree root.
	worktreeRoot := filepath.Dir(root)
	validate := func(field, relative string, allowRoot bool) {
		if _, resolveErr := pathx.ResolveUnder(worktreeRoot, relative, allowRoot); resolveErr != nil {
			code := "path.resolve"
			message := fmt.Sprintf("%s cannot be resolved safely within the Git worktree", field)
			if errors.Is(resolveErr, pathx.ErrOutsideRoot) {
				code = "path.outside_worktree"
				message = fmt.Sprintf("%s resolves outside the Git worktree", field)
			}
			inventory.addDiagnostic(document.Path, code, message)
		}
	}
	switch value := document.Record.(type) {
	case *research.Run:
		for index, output := range value.ExpectedOutputs {
			validate(fmt.Sprintf("expected_outputs[%d]", index), output, false)
		}
	case *research.Attempt:
		validate("cwd", value.CWD, true)
	}
}

func reservedPathPrefix(relative string) bool {
	return relative == ProjectFile || relative == PlansDir || relative == FindingsDir || relative == DecisionsDir || strings.HasPrefix(relative, PlansDir+"/") || strings.HasPrefix(relative, FindingsDir+"/") || strings.HasPrefix(relative, DecisionsDir+"/") || strings.HasPrefix(relative, "e-")
}

// Valid reports whether the complete canonical inventory has no diagnostics.
func (inventory *Inventory) Valid() bool { return inventory != nil && len(inventory.Diagnostics) == 0 }

// Error joins all deterministic diagnostics for mutation guards.
func (inventory *Inventory) Error() error {
	if inventory == nil {
		return errors.New("nil inventory")
	}
	if len(inventory.Diagnostics) == 0 {
		return nil
	}
	errorsList := make([]error, len(inventory.Diagnostics))
	for index := range inventory.Diagnostics {
		errorsList[index] = inventory.Diagnostics[index]
	}
	return errors.Join(errorsList...)
}

// ByID returns the unique document for id.
func (inventory *Inventory) ByID(id research.ID) (*Document, error) {
	if inventory == nil {
		return nil, research.ErrReferenceNotFound
	}
	switch documents := inventory.byID[id]; len(documents) {
	case 0:
		return nil, fmt.Errorf("%s: %w", id, research.ErrReferenceNotFound)
	case 1:
		return documents[0], nil
	default:
		return nil, fmt.Errorf("%s has duplicate records: %w", id, research.ErrAmbiguousReference)
	}
}

// Resolve accepts a full ID, unique prefix/display code, or migration alias.
func (inventory *Inventory) Resolve(query string, expected research.Kind) (*Document, error) {
	candidates := make([]research.Candidate, 0, len(inventory.Documents))
	for _, document := range inventory.Documents {
		id, ok := document.ID()
		if !ok {
			continue
		}
		common := document.Record.GetCommon()
		candidates = append(candidates, research.Candidate{ID: id, Aliases: append([]string(nil), common.LegacyAliases...)})
	}
	id, err := research.Resolve(query, expected, candidates)
	if err != nil {
		return nil, err
	}
	return inventory.ByID(id)
}

// OfKind returns documents in complete canonical-ID order.
func (inventory *Inventory) OfKind(kind research.Kind) []*Document {
	var documents []*Document
	for _, document := range inventory.Documents {
		if document.Kind() == kind {
			documents = append(documents, document)
		}
	}
	sort.SliceStable(documents, func(i, j int) bool {
		left, _ := documents[i].ID()
		right, _ := documents[j].ID()
		return left.String() < right.String()
	})
	return documents
}

// Location returns the classified canonical path for document.
func (inventory *Inventory) Location(document *Document) (Location, bool) {
	if inventory == nil || document == nil {
		return Location{}, false
	}
	location, found := inventory.locations[document.Path]
	return location, found
}
