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
	"reflect"
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
	Policy      *Document
	Documents   []*Document
	Diagnostics []Diagnostic

	byID                   map[research.ID][]*Document
	locations              map[string]Location
	boundRoot              *os.Root
	boundVerify            func() error
	identities             map[string]fs.FileInfo
	snapshot               [sha256.Size]byte
	hasSnapshot            bool
	skipImportedProvenance bool
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
		if _, flat := flatLayouts[name]; flat {
			scanner.note(name, info, nil)
			if !scanner.openAndScanFlat(root, name, name, info) {
				scanner.inspectCandidate(root, name, name, info)
			}
			continue
		}
		switch name {
		case ProjectFile, PolicyFile:
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
		document, decodeErr = DecodeImported(data)
	}
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
	return inventoryFromDocuments(root, documents, false)
}

// InventoryFromMigratedDocuments validates the schema, layout, and graph of a
// complete migration candidate before an archive exists. UUIDv5 provenance is
// still provisional; LoadInventoryContext authenticates the published result
// against its exact archived source bytes.
func InventoryFromMigratedDocuments(root string, documents []*Document) *Inventory {
	inventory := inventoryFromDocuments(root, documents, true)
	return inventory
}

func inventoryFromDocuments(root string, documents []*Document, imported bool) *Inventory {
	inventory := newInventory(root)
	if imported {
		inventory.skipImportedProvenance = true
	}
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
		validate := research.Validate
		if imported {
			validate = research.ValidateImported
		}
		if err := validate(document.Record); err != nil {
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
		if imported {
			revision, err = RevisionImported(document)
		}
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
	policyCount := 0
	for _, document := range inventory.Documents {
		if document.Kind() == research.KindProject {
			projectCount++
			if inventory.Project == nil {
				inventory.Project = document
			}
			continue
		}
		if document.Kind() == research.KindPolicy {
			policyCount++
			if inventory.Policy == nil {
				inventory.Policy = document
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
	if policyCount > 1 {
		inventory.addDiagnostic(PolicyFile, "policy.multiple", "canonical inventory has more than one Policy record")
	}
	inventory.validateDuplicates()
	if !inventory.skipImportedProvenance {
		inventory.validateImportedProvenance()
	}
	inventory.validateRelationships()
	inventory.validatePlanExperimentSemantics()
	inventory.validatePromotionSemantics()
	inventory.validateCycles()
	inventory.sortDiagnostics()
}

// validatePromotionSemantics mirrors the append-only lifecycle service at the
// whole-inventory boundary. This prevents hand-edited records from bypassing
// the same sealed-holdout and champion-chain invariants enforced by commands.
func (inventory *Inventory) validatePromotionSemantics() {
	promotions := inventory.OfKind(research.KindPromotion)
	consumers := make(map[research.ID][]*Document)
	for _, document := range promotions {
		promotion := document.Record.(*research.Promotion)
		consumers[promotion.Evaluation] = append(consumers[promotion.Evaluation], document)

		if challenger := inventory.unique(promotion.Challenger); challenger != nil {
			if challenger.Record.(*research.Release).State != research.ReleaseValidated {
				inventory.addDiagnostic(document.Path, "promotion.challenger_state", "Promotion challenger must be a validated Release")
			}
		}
		specDocument := inventory.unique(promotion.Spec)
		evaluationDocument := inventory.unique(promotion.Evaluation)
		if specDocument != nil && evaluationDocument != nil {
			spec := specDocument.Record.(*research.PromotionSpec)
			evaluation := evaluationDocument.Record.(*research.Evaluation)
			if !evaluation.EvaluatedAt.After(spec.SealedAt) {
				inventory.addDiagnostic(document.Path, "promotion.holdout_stale", "Promotion Evaluation must be evaluated strictly after the PromotionSpec was sealed")
			}
		}
	}
	for evaluation, documents := range consumers {
		if evaluation.IsZero() || len(documents) < 2 {
			continue
		}
		for _, document := range documents {
			inventory.addDiagnostic(document.Path, "promotion.holdout_reused", fmt.Sprintf("Evaluation %s is consumed by %d Promotions", evaluation, len(documents)))
		}
	}

	roots := make(map[string][]*Document)
	followers := make(map[research.ID][]*Document)
	for _, document := range promotions {
		promotion := document.Record.(*research.Promotion)
		if promotion.Previous.IsZero() {
			roots[promotion.Target] = append(roots[promotion.Target], document)
		} else {
			followers[promotion.Previous] = append(followers[promotion.Previous], document)
		}
	}
	for target, candidates := range roots {
		if len(candidates) != 1 {
			continue
		}
		current := candidates[0]
		visited := make(map[research.ID]struct{})
		var champion research.ID
		var championSetting *research.Promotion
		for current != nil {
			promotion := current.Record.(*research.Promotion)
			if _, seen := visited[promotion.ID]; seen {
				break
			}
			visited[promotion.ID] = struct{}{}
			if promotion.Incumbent != champion {
				inventory.addDiagnostic(current.Path, "promotion.incumbent_mismatch", fmt.Sprintf("Promotion incumbent %s does not match current champion %s for target %s", promotion.Incumbent, champion, target))
			}
			switch promotion.Outcome {
			case research.PromotionAccepted:
				if promotion.Challenger == champion {
					inventory.addDiagnostic(current.Path, "promotion.challenger_is_champion", "accepted Promotion challenger is already the current champion")
				}
				champion = promotion.Challenger
				championSetting = promotion
			case research.PromotionRejected:
				if promotion.Challenger == champion {
					inventory.addDiagnostic(current.Path, "promotion.challenger_is_champion", "rejected Promotion challenger is already the current champion")
				}
			case research.PromotionRolledBack:
				expected := research.ID{}
				if championSetting != nil {
					expected = championSetting.Incumbent
				}
				if expected.IsZero() || promotion.Challenger != expected {
					inventory.addDiagnostic(current.Path, "promotion.rollback_incumbent", fmt.Sprintf("rollback must restore the incumbent displaced by the current champion-setting Promotion (%s)", expected))
				}
				champion = promotion.Challenger
				championSetting = promotion
			}
			next := followers[promotion.ID]
			if len(next) != 1 {
				break
			}
			current = next[0]
		}
	}
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
	queuePartitions := map[string]*Document{}
	for _, document := range inventory.OfKind(research.KindQueue) {
		queue := document.Record.(*research.Queue)
		for _, partition := range queue.Partitions {
			key := partition.Pool.String() + "\x00" + string(partition.Lane)
			if previous := queuePartitions[key]; previous != nil {
				inventory.addDiagnostic(previous.Path, "queue.partition_global_duplicate", fmt.Sprintf("pool/lane partition %s/%s is also owned by %s", partition.Pool, partition.Lane, document.Path))
				inventory.addDiagnostic(document.Path, "queue.partition_global_duplicate", fmt.Sprintf("pool/lane partition %s/%s is already owned by %s", partition.Pool, partition.Lane, previous.Path))
				continue
			}
			queuePartitions[key] = document
		}
	}
	for _, document := range inventory.Documents {
		switch value := document.Record.(type) {
		case *research.Idea:
			for index, id := range value.Parents {
				inventory.require(document, fmt.Sprintf("parents[%d]", index), id, research.KindIdea)
			}
			if !value.ResultingPlan.IsZero() {
				planDocument := inventory.require(document, "resulting_plan", value.ResultingPlan, research.KindPlan)
				if planDocument != nil && planDocument.Record.(*research.Plan).Idea != value.ID {
					inventory.addDiagnostic(document.Path, "idea.plan_mismatch", "Idea resulting_plan does not point back through Plan.idea")
				}
			}
			if !value.MergedInto.IsZero() {
				inventory.require(document, "merged_into", value.MergedInto, research.KindIdea)
			}
			inventory.validateClassificationValues(document, &value.Classification)
			inventory.validateClusterValue(document, value.PrimaryCluster)
		case *research.ResourcePool:
		case *research.Queue:
			for partitionIndex, partition := range value.Partitions {
				inventory.require(document, fmt.Sprintf("partitions[%d].pool", partitionIndex), partition.Pool, research.KindResourcePool)
				for entryIndex, entry := range partition.Entries {
					field := fmt.Sprintf("partitions[%d].entries[%d].plan", partitionIndex, entryIndex)
					planDocument := inventory.require(document, field, entry.Plan, research.KindPlan)
					if planDocument != nil {
						plan := planDocument.Record.(*research.Plan)
						if plan.Schema != research.SchemaPlanV2 || plan.State != research.PlanQueued {
							inventory.addDiagnostic(document.Path, "queue.plan_state", fmt.Sprintf("%s must reference a queued exp.plan/v2", field))
						}
						if len(plan.Resources) != 1 || plan.Resources[0].Pool != partition.Pool {
							inventory.addDiagnostic(document.Path, "queue.plan_pool", fmt.Sprintf("%s ResourcePool does not match its Queue partition", field))
						}
					}
					if planDocument != nil && entry.PlanRevision != planDocument.Revision {
						inventory.addDiagnostic(document.Path, "queue.plan_stale", fmt.Sprintf("%s pins revision %s but current Plan revision is %s", field, entry.PlanRevision, planDocument.Revision))
					}
					if planDocument != nil && !entry.Pinned {
						if plan, ok := planDocument.Record.(*research.Plan); ok && inventory.clusterSaturated(plan.PrimaryCluster) {
							inventory.addDiagnostic(document.Path, "queue.cluster_saturated", fmt.Sprintf("%s belongs to saturated cluster %s", field, plan.PrimaryCluster))
						}
					}
				}
			}
		case *research.QueueAdvice:
			inventory.require(document, "queue", value.Queue, research.KindQueue)
			inventory.require(document, "candidate_plan", value.CandidatePlan, research.KindPlan)
			inventory.require(document, "pool", value.Pool, research.KindResourcePool)
			for index, id := range value.ListwiseOrder {
				inventory.require(document, fmt.Sprintf("listwise_order[%d]", index), id, research.KindPlan)
			}
		case *research.Battle:
			inventory.require(document, "queue", value.Queue, research.KindQueue)
			if !value.Advice.IsZero() {
				adviceDocument := inventory.require(document, "advice", value.Advice, research.KindQueueAdvice)
				if adviceDocument != nil {
					advice := adviceDocument.Record.(*research.QueueAdvice)
					if advice.Queue != value.Queue || advice.QueueRevision != value.QueueRevision || advice.CandidatePlan != value.CandidatePlan || advice.Pool != value.Pool || advice.Lane != value.Lane {
						inventory.addDiagnostic(document.Path, "battle.advice_mismatch", "Battle context does not match its QueueAdvice")
					}
				}
			}
			inventory.require(document, "candidate_plan", value.CandidatePlan, research.KindPlan)
			inventory.require(document, "incumbent_plan", value.IncumbentPlan, research.KindPlan)
			inventory.require(document, "pool", value.Pool, research.KindResourcePool)
		case *research.Plan:
			for index, id := range value.Assumptions {
				inventory.require(document, fmt.Sprintf("assumptions[%d]", index), id, research.KindFinding)
			}
			if !value.ResultingExperiment.IsZero() {
				inventory.require(document, "resulting_experiment", value.ResultingExperiment, research.KindExperiment)
			}
			if value.Schema == research.SchemaPlanV2 {
				if !value.Idea.IsZero() {
					ideaDocument := inventory.require(document, "idea", value.Idea, research.KindIdea)
					if ideaDocument != nil && ideaDocument.Record.(*research.Idea).ResultingPlan != value.ID {
						inventory.addDiagnostic(document.Path, "plan.idea_mismatch", "Plan.idea is not the Idea's sole resulting_plan")
					}
				}
				if value.Classification != nil {
					inventory.validateClassificationValues(document, value.Classification)
				}
				inventory.validateClusterValue(document, value.PrimaryCluster)
				for index, dependency := range value.Dependencies {
					field := fmt.Sprintf("dependencies[%d]", index)
					finding := inventory.require(document, field+".finding", dependency.Finding, research.KindFinding)
					if finding == nil {
						continue
					}
					if value.State == research.PlanQueued && dependency.Revision != finding.Revision {
						inventory.addDiagnostic(document.Path, "plan.dependency_stale", fmt.Sprintf("%s pins revision %s but current Finding revision is %s", field, dependency.Revision, finding.Revision))
					}
					digest, err := inventory.BeliefDigest(dependency.Finding)
					if value.State == research.PlanQueued && err == nil && digest != dependency.BeliefDigest {
						inventory.addDiagnostic(document.Path, "plan.belief_stale", fmt.Sprintf("%s pins belief digest %s but current digest is %s", field, dependency.BeliefDigest, digest))
					}
				}
				for index, need := range value.Resources {
					inventory.require(document, fmt.Sprintf("resources[%d].pool", index), need.Pool, research.KindResourcePool)
				}
			}
		case *research.Experiment:
			for index, id := range value.Parents {
				inventory.require(document, fmt.Sprintf("parents[%d]", index), id, research.KindExperiment)
			}
			for index, id := range value.CandidateInputs {
				inventory.require(document, fmt.Sprintf("candidate_inputs[%d]", index), id, research.KindCandidate)
			}
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
						if evidence.Disposition == research.EvidenceIncluded && !inventory.runHasSuccessfulDirectAttempt(evidence.Run) {
							inventory.addDiagnostic(document.Path, "experiment.evidence_unexecuted", fmt.Sprintf("included Run %s has no successful direct Attempt", evidence.Run))
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
			if value.Schema == research.SchemaAttemptV2 {
				inventory.require(document, "pool", value.Pool, research.KindResourcePool)
				inventory.require(document, "queue", value.Queue, research.KindQueue)
			}
		case *research.EvaluationSpec:
			inventory.require(document, "budget_pool", value.BudgetPool, research.KindResourcePool)
		case *research.Evaluation:
			specDocument := inventory.require(document, "spec", value.Spec, research.KindEvaluationSpec)
			inventory.requireAny(document, "subject", value.Subject, research.KindExperiment, research.KindCandidate, research.KindRelease)
			if specDocument != nil {
				spec := specDocument.Record.(*research.EvaluationSpec)
				allowedMetrics := make(map[string]research.MetricSpec, len(spec.Metrics))
				for _, metric := range spec.Metrics {
					allowedMetrics[metric.Name] = metric
				}
				observed := make(map[string]research.MetricValue, len(value.Metrics))
				for _, metric := range value.Metrics {
					specification, found := allowedMetrics[metric.Name]
					if !found || specification.Unit != metric.Unit {
						inventory.addDiagnostic(document.Path, "evaluation.metric_mismatch", fmt.Sprintf("metric %s (%s) is not declared by EvaluationSpec %s", metric.Name, metric.Unit, spec.ID))
					}
					observed[metric.Name] = metric
				}
				if len(observed) != len(allowedMetrics) {
					inventory.addDiagnostic(document.Path, "evaluation.metric_incomplete", fmt.Sprintf("Evaluation supplies %d of %d declared metrics", len(observed), len(allowedMetrics)))
				}
				thresholds, passed := 0, true
				for name, specification := range allowedMetrics {
					if specification.Threshold == nil {
						continue
					}
					metric, found := observed[name]
					if !found {
						continue
					}
					thresholds++
					if specification.Direction == research.MetricMaximize {
						passed = passed && metric.Value >= *specification.Threshold
					} else if specification.Direction == research.MetricMinimize {
						passed = passed && metric.Value <= *specification.Threshold
					}
				}
				if thresholds > 0 && value.Outcome != research.EvaluationInvalid && (passed && value.Outcome != research.EvaluationPassed || !passed && value.Outcome != research.EvaluationFailed) {
					inventory.addDiagnostic(document.Path, "evaluation.outcome_threshold", "Evaluation outcome does not match its declared metric thresholds")
				}
			}
			mlflowOwner := ""
			for _, reference := range value.ExternalRefs {
				if reference.Provider != "mlflow" || reference.Role != research.ExternalTracker {
					continue
				}
				if owner, ok := reference.Metadata["mlflow.owner_attempt"].(string); ok && owner != "" {
					if mlflowOwner != "" && mlflowOwner != owner {
						inventory.addDiagnostic(document.Path, "evaluation.mlflow_owner_conflict", "MLflow references claim different owner Attempts")
					}
					mlflowOwner = owner
				}
			}
		case *research.Finding:
			for index, evidence := range value.Evidence {
				expected := research.KindRun
				if evidence.Kind == research.FindingEvidenceExperiment {
					expected = research.KindExperiment
				}
				evidenceDocument := inventory.require(document, fmt.Sprintf("evidence[%d].ref", index), evidence.Ref, expected)
				if evidence.Kind == research.FindingEvidenceRun {
					if !inventory.runHasSuccessfulDirectAttempt(evidence.Ref) {
						inventory.addDiagnostic(document.Path, "finding.evidence_unexecuted", fmt.Sprintf("Finding evidence Run %s has no successful direct Attempt", evidence.Ref))
					}
					if evidenceDocument != nil {
						run := evidenceDocument.Record.(*research.Run)
						experimentDocument := inventory.unique(run.Experiment)
						if experimentDocument == nil || !experimentIncludesRun(experimentDocument.Record.(*research.Experiment), run.ID) {
							inventory.addDiagnostic(document.Path, "finding.evidence_excluded", fmt.Sprintf("Finding evidence Run %s is not included in its Experiment conclusion", run.ID))
						}
					}
				}
			}
			for index, id := range value.Weakens {
				inventory.require(document, fmt.Sprintf("weakens[%d]", index), id, research.KindFinding)
			}
			for index, id := range value.Overturns {
				inventory.require(document, fmt.Sprintf("overturns[%d]", index), id, research.KindFinding)
			}
		case *research.Candidate:
			experimentDocument := inventory.require(document, "experiment", value.Experiment, research.KindExperiment)
			if experimentDocument != nil {
				experiment := experimentDocument.Record.(*research.Experiment)
				if experiment.Lifecycle != research.LifecycleClosed || experiment.Closure != research.ClosureConcluded || experiment.Verdict != research.VerdictSupported {
					inventory.addDiagnostic(document.Path, "candidate.experiment_outcome", "Candidate requires a closed, concluded, supported Experiment")
				}
			}
			evaluationDocument := inventory.require(document, "evaluation", value.Evaluation, research.KindEvaluation)
			if evaluationDocument != nil {
				evaluation := evaluationDocument.Record.(*research.Evaluation)
				if evaluation.Subject != value.Experiment {
					inventory.addDiagnostic(document.Path, "candidate.evaluation_subject", "Candidate Evaluation must evaluate its Experiment")
				}
				if evaluation.Outcome != research.EvaluationPassed {
					inventory.addDiagnostic(document.Path, "candidate.evaluation_outcome", "Candidate requires a passed Evaluation")
				}
				if specDocument := inventory.unique(evaluation.Spec); specDocument == nil || specDocument.Kind() != research.KindEvaluationSpec || specDocument.Record.(*research.EvaluationSpec).Purpose != research.EvaluationScientific {
					inventory.addDiagnostic(document.Path, "candidate.evaluation_spec", "Candidate Evaluation must use a scientific EvaluationSpec")
				}
			}
			for index, id := range value.Parents {
				inventory.require(document, fmt.Sprintf("parents[%d]", index), id, research.KindCandidate)
			}
			if !inventory.candidateHasSuccessfulAttempt(value, experimentDocument) {
				inventory.addDiagnostic(document.Path, "candidate.attempt_provenance", "Candidate Git commit and change_set require a matching successful Attempt in its Experiment")
			}
		case *research.Release:
			for index, slot := range value.Slots {
				inventory.require(document, fmt.Sprintf("slots[%d].candidate", index), slot.Candidate, research.KindCandidate)
			}
			var combinationExperiment *research.Experiment
			if !value.CombinationExperiment.IsZero() {
				combination := inventory.require(document, "combination_experiment", value.CombinationExperiment, research.KindExperiment)
				if combination != nil {
					experiment := combination.Record.(*research.Experiment)
					combinationExperiment = experiment
					if experiment.Lifecycle != research.LifecycleClosed || experiment.Closure != research.ClosureConcluded || experiment.Verdict != research.VerdictSupported {
						inventory.addDiagnostic(document.Path, "release.combination_outcome", "combination Experiment must be closed, concluded, and supported")
					}
					if experiment.Design.Kind != research.ExperimentCombination {
						inventory.addDiagnostic(document.Path, "release.combination_kind", "combination_experiment must use design.kind = combination")
					}
					wanted := map[research.ID]struct{}{}
					for _, slot := range value.Slots {
						wanted[slot.Candidate] = struct{}{}
					}
					actual := map[research.ID]struct{}{}
					for _, candidate := range experiment.CandidateInputs {
						actual[candidate] = struct{}{}
					}
					if len(wanted) != len(actual) {
						inventory.addDiagnostic(document.Path, "release.combination_inputs", "combination Experiment inputs do not match Release slot Candidates")
					} else {
						for candidate := range wanted {
							if _, found := actual[candidate]; !found {
								inventory.addDiagnostic(document.Path, "release.combination_inputs", "combination Experiment inputs do not match Release slot Candidates")
								break
							}
						}
					}
				}
			}
			if !value.CombinationEvaluation.IsZero() {
				combinationEvaluation := inventory.require(document, "combination_evaluation", value.CombinationEvaluation, research.KindEvaluation)
				if combinationEvaluation != nil {
					evaluation := combinationEvaluation.Record.(*research.Evaluation)
					if combinationExperiment == nil || evaluation.Subject != combinationExperiment.ID {
						inventory.addDiagnostic(document.Path, "release.combination_evaluation_subject", "combination Evaluation must evaluate the combination Experiment")
					}
					if evaluation.Outcome != research.EvaluationPassed {
						inventory.addDiagnostic(document.Path, "release.combination_evaluation_outcome", "combination Evaluation must pass")
					}
					if specDocument := inventory.unique(evaluation.Spec); specDocument == nil || specDocument.Kind() != research.KindEvaluationSpec || specDocument.Record.(*research.EvaluationSpec).Purpose != research.EvaluationScientific {
						inventory.addDiagnostic(document.Path, "release.combination_evaluation_spec", "combination Evaluation must use a scientific EvaluationSpec")
					}
				}
			}
			if !value.Evaluation.IsZero() {
				evaluation := inventory.require(document, "evaluation", value.Evaluation, research.KindEvaluation)
				if evaluation != nil {
					result := evaluation.Record.(*research.Evaluation)
					if result.Subject != value.ID {
						inventory.addDiagnostic(document.Path, "release.evaluation_subject", "Release Evaluation must evaluate this Release")
					}
					if value.State == research.ReleaseValidated && result.Outcome != research.EvaluationPassed {
						inventory.addDiagnostic(document.Path, "release.evaluation_outcome", "validated Release requires a passed Evaluation")
					}
					if value.State == research.ReleaseValidated {
						specDocument := inventory.unique(result.Spec)
						if specDocument == nil || specDocument.Kind() != research.KindEvaluationSpec {
							inventory.addDiagnostic(document.Path, "release.evaluation_spec", "validated Release EvaluationSpec is missing")
						} else {
							spec := specDocument.Record.(*research.EvaluationSpec)
							if spec.Purpose != research.EvaluationPromotion || spec.SealedAt == nil {
								inventory.addDiagnostic(document.Path, "release.evaluation_spec", "validated Release requires a sealed promotion EvaluationSpec")
							}
						}
					}
				}
			}
		case *research.PromotionSpec:
			evaluationSpec := inventory.require(document, "evaluation_spec", value.EvaluationSpec, research.KindEvaluationSpec)
			if evaluationSpec != nil {
				spec := evaluationSpec.Record.(*research.EvaluationSpec)
				if spec.Purpose != research.EvaluationPromotion || spec.SealedAt == nil {
					inventory.addDiagnostic(document.Path, "promotion.evaluation_spec", "PromotionSpec requires a sealed promotion EvaluationSpec")
				}
				if value.HoldoutBudgetHours > spec.BudgetHours {
					inventory.addDiagnostic(document.Path, "promotion.holdout_budget", "PromotionSpec holdout budget exceeds its EvaluationSpec budget")
				}
			}
		case *research.Promotion:
			specDocument := inventory.require(document, "spec", value.Spec, research.KindPromotionSpec)
			challenger := inventory.require(document, "challenger", value.Challenger, research.KindRelease)
			var incumbent *Document
			if !value.Incumbent.IsZero() {
				incumbent = inventory.require(document, "incumbent", value.Incumbent, research.KindRelease)
			}
			evaluationDocument := inventory.require(document, "evaluation", value.Evaluation, research.KindEvaluation)
			var previous *Document
			if !value.Previous.IsZero() {
				previous = inventory.require(document, "previous", value.Previous, research.KindPromotion)
			}
			if specDocument != nil && specDocument.Record.(*research.PromotionSpec).Target != value.Target {
				inventory.addDiagnostic(document.Path, "promotion.target_mismatch", "Promotion target does not match PromotionSpec")
			}
			for _, releaseDocument := range []*Document{challenger, incumbent} {
				if releaseDocument != nil && releaseDocument.Record.(*research.Release).Target != value.Target {
					inventory.addDiagnostic(document.Path, "promotion.target_mismatch", "Promotion release target does not match Promotion target")
				}
			}
			if evaluationDocument != nil && evaluationDocument.Record.(*research.Evaluation).Subject != value.Challenger {
				inventory.addDiagnostic(document.Path, "promotion.evaluation_subject", "Promotion Evaluation must evaluate the challenger Release")
			}
			if specDocument != nil && evaluationDocument != nil {
				spec := specDocument.Record.(*research.PromotionSpec)
				evaluation := evaluationDocument.Record.(*research.Evaluation)
				if evaluation.Spec != spec.EvaluationSpec {
					inventory.addDiagnostic(document.Path, "promotion.evaluation_spec", "Promotion Evaluation does not use the sealed EvaluationSpec")
				}
				if (value.Outcome == research.PromotionAccepted || value.Outcome == research.PromotionRolledBack) && evaluation.Outcome != research.EvaluationPassed {
					inventory.addDiagnostic(document.Path, "promotion.evaluation_outcome", "champion-setting Promotion requires a passed Evaluation")
				}
			}
			if previous != nil && previous.Record.(*research.Promotion).Target != value.Target {
				inventory.addDiagnostic(document.Path, "promotion.target_mismatch", "previous Promotion has a different target")
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

func (inventory *Inventory) runHasSuccessfulDirectAttempt(run research.ID) bool {
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		if attempt.Run == run && attempt.State == research.AttemptSucceeded && attempt.Terminal != nil && attempt.Terminal.Source == "direct" {
			return true
		}
	}
	return false
}

func experimentIncludesRun(experiment *research.Experiment, run research.ID) bool {
	if experiment == nil || experiment.Conclusion == nil {
		return false
	}
	for _, evidence := range experiment.Conclusion.Evidence {
		if evidence.Run == run && evidence.Disposition == research.EvidenceIncluded {
			return true
		}
	}
	return false
}

func (inventory *Inventory) validatePlanExperimentSemantics() {
	origins := map[research.ID][]*Document{}
	for _, document := range inventory.OfKind(research.KindPlan) {
		plan := document.Record.(*research.Plan)
		if plan.ResultingExperiment.IsZero() {
			continue
		}
		origins[plan.ResultingExperiment] = append(origins[plan.ResultingExperiment], document)
		experimentDocument := inventory.unique(plan.ResultingExperiment)
		if experimentDocument == nil || experimentDocument.Kind() != research.KindExperiment {
			continue
		}
		experiment := experimentDocument.Record.(*research.Experiment)
		if plan.State == research.PlanStarted && experiment.Lifecycle != research.LifecycleActive {
			inventory.addDiagnostic(document.Path, "plan.experiment_state", "started Plan requires an active resulting Experiment")
		}
		if plan.State == research.PlanCompleted && experiment.Lifecycle != research.LifecycleClosed {
			inventory.addDiagnostic(document.Path, "plan.experiment_state", "completed Plan requires a closed resulting Experiment")
		}
	}
	for experiment, plans := range origins {
		if len(plans) < 2 {
			continue
		}
		for _, document := range plans {
			inventory.addDiagnostic(document.Path, "experiment.multiple_origin_plans", fmt.Sprintf("Experiment %s is claimed by %d Plans", experiment, len(plans)))
		}
	}
}

func (inventory *Inventory) candidateHasSuccessfulAttempt(candidate *research.Candidate, experimentDocument *Document) bool {
	if inventory == nil || candidate == nil || experimentDocument == nil {
		return false
	}
	experiment := experimentDocument.Record.(*research.Experiment)
	mlflowOwner := ""
	mlflowOwnerConflict := false
	if evaluationDocument := inventory.unique(candidate.Evaluation); evaluationDocument != nil {
		for _, reference := range evaluationDocument.Record.(*research.Evaluation).ExternalRefs {
			if reference.Provider == "mlflow" && reference.Role == research.ExternalTracker {
				if owner, ok := reference.Metadata["mlflow.owner_attempt"].(string); ok {
					if mlflowOwner != "" && owner != "" && mlflowOwner != owner {
						mlflowOwnerConflict = true
					}
					if owner != "" {
						mlflowOwner = owner
					}
				}
			}
		}
	}
	if mlflowOwnerConflict {
		return false
	}
	for _, document := range inventory.OfKind(research.KindAttempt) {
		attempt := document.Record.(*research.Attempt)
		if attempt.Schema != research.SchemaAttemptV2 || attempt.State != research.AttemptSucceeded || attempt.Terminal == nil || attempt.Terminal.Source != "direct" ||
			attempt.HeadCommit != candidate.GitCommit || !reflect.DeepEqual(attempt.ChangeSet, candidate.ChangeSet) {
			continue
		}
		if mlflowOwner != "" && attempt.ID.String() != mlflowOwner {
			continue
		}
		runDocument := inventory.unique(attempt.Run)
		if runDocument != nil && runDocument.Record.(*research.Run).Experiment == candidate.Experiment && experiment.Conclusion != nil {
			for _, evidence := range experiment.Conclusion.Evidence {
				if evidence.Run == attempt.Run && evidence.Disposition == research.EvidenceIncluded {
					return true
				}
			}
		}
	}
	return false
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

func (inventory *Inventory) requireAny(source *Document, field string, id research.ID, expected ...research.Kind) *Document {
	if id.IsZero() {
		return nil
	}
	allowed := false
	for _, kind := range expected {
		allowed = allowed || id.Kind() == kind
	}
	if !allowed {
		inventory.addDiagnostic(source.Path, "reference.wrong_kind", fmt.Sprintf("%s points to %s, expected one of %v", field, id.Kind(), expected))
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

func (inventory *Inventory) validateClassificationValues(source *Document, classification *research.Classification) {
	if inventory.Policy == nil || classification == nil {
		return
	}
	policy, ok := inventory.Policy.Record.(*research.Policy)
	if !ok {
		return
	}
	checks := []struct {
		field   string
		value   string
		allowed []string
	}{
		{"classification.domain", classification.Domain, policy.Taxonomy.Domains},
		{"classification.work", classification.Work, policy.Taxonomy.Work},
		{"classification.method", classification.Method, policy.Taxonomy.Methods},
		{"classification.component", classification.Component, policy.Taxonomy.Components},
	}
	for _, check := range checks {
		found := false
		for _, allowed := range check.allowed {
			found = found || check.value == allowed
		}
		if !found {
			inventory.addDiagnostic(source.Path, "classification.not_allowed", fmt.Sprintf("%s value %q is not allowed by POLICY.md", check.field, check.value))
		}
	}
}

func (inventory *Inventory) validateClusterValue(source *Document, cluster string) {
	if inventory.Policy == nil {
		return
	}
	policy, ok := inventory.Policy.Record.(*research.Policy)
	if !ok || len(policy.Clusters) == 0 {
		return
	}
	for _, configured := range policy.Clusters {
		if configured.Name == cluster {
			return
		}
	}
	inventory.addDiagnostic(source.Path, "classification.cluster_not_allowed", fmt.Sprintf("primary_cluster value %q is not configured by POLICY.md", cluster))
}

func (inventory *Inventory) clusterSaturated(cluster string) bool {
	if inventory.Policy == nil {
		return false
	}
	policy, ok := inventory.Policy.Record.(*research.Policy)
	if !ok {
		return false
	}
	for _, configured := range policy.Clusters {
		if configured.Name == cluster {
			return configured.State == research.ClusterSaturated
		}
	}
	return false
}

// BeliefDigest returns the current digest for one Finding, including every
// incoming weakens/overturns edge and the owning source revision.
func (inventory *Inventory) BeliefDigest(id research.ID) (string, error) {
	if inventory == nil || id.Kind() != research.KindFinding {
		return "", research.ErrReferenceNotFound
	}
	target := inventory.unique(id)
	if target == nil {
		return "", fmt.Errorf("%s: %w", id, research.ErrReferenceNotFound)
	}
	var incoming []research.BeliefInfluence
	for _, document := range inventory.OfKind(research.KindFinding) {
		finding := document.Record.(*research.Finding)
		for _, weakened := range finding.Weakens {
			if weakened == id {
				incoming = append(incoming, research.BeliefInfluence{Source: finding.ID, Relation: research.BeliefWeakens, Revision: document.Revision})
			}
		}
		for _, overturned := range finding.Overturns {
			if overturned == id {
				incoming = append(incoming, research.BeliefInfluence{Source: finding.ID, Relation: research.BeliefOverturns, Revision: document.Revision})
			}
		}
	}
	return research.ComputeBeliefDigest(id, target.Revision, incoming)
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
	inventory.findCycles(research.KindIdea, func(record research.Record) []research.ID {
		idea := record.(*research.Idea)
		edges := append([]research.ID(nil), idea.Parents...)
		if !idea.MergedInto.IsZero() {
			edges = append(edges, idea.MergedInto)
		}
		return edges
	})
	inventory.findCycles(research.KindFinding, func(record research.Record) []research.ID {
		finding := record.(*research.Finding)
		return append(append([]research.ID(nil), finding.Weakens...), finding.Overturns...)
	})
	inventory.findCycles(research.KindDecision, func(record research.Record) []research.ID {
		return append([]research.ID(nil), record.(*research.Decision).Supersedes...)
	})
	inventory.findCycles(research.KindExperiment, func(record research.Record) []research.ID {
		experiment := record.(*research.Experiment)
		edges := append([]research.ID(nil), experiment.Parents...)
		if experiment.ClosureDetail != nil && !experiment.ClosureDetail.SupersededBy.IsZero() {
			edges = append(edges, experiment.ClosureDetail.SupersededBy)
		}
		return edges
	})
	inventory.findCycles(research.KindCandidate, func(record research.Record) []research.ID {
		return append([]research.ID(nil), record.(*research.Candidate).Parents...)
	})
	inventory.findCycles(research.KindPromotion, func(record research.Record) []research.ID {
		promotion := record.(*research.Promotion)
		if promotion.Previous.IsZero() {
			return nil
		}
		return []research.ID{promotion.Previous}
	})
	inventory.validatePromotionForks()
}

func (inventory *Inventory) validatePromotionForks() {
	followers := map[research.ID][]*Document{}
	roots := map[string][]*Document{}
	for _, document := range inventory.OfKind(research.KindPromotion) {
		promotion := document.Record.(*research.Promotion)
		if promotion.Previous.IsZero() {
			roots[promotion.Target] = append(roots[promotion.Target], document)
		} else {
			followers[promotion.Previous] = append(followers[promotion.Previous], document)
		}
	}
	for previous, documents := range followers {
		if len(documents) < 2 {
			continue
		}
		for _, document := range documents {
			inventory.addDiagnostic(document.Path, "promotion.chain_fork", fmt.Sprintf("Promotion %s has %d followers", previous, len(documents)))
		}
	}
	for target, documents := range roots {
		if len(documents) < 2 {
			continue
		}
		for _, document := range documents {
			inventory.addDiagnostic(document.Path, "promotion.multiple_roots", fmt.Sprintf("target %s has %d Promotion chain roots", target, len(documents)))
		}
	}
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
		for index, changed := range value.ChangeSet {
			validate(fmt.Sprintf("change_set[%d]", index), changed, false)
		}
	case *research.Candidate:
		for index, changed := range value.ChangeSet {
			validate(fmt.Sprintf("change_set[%d]", index), changed, false)
		}
	}
}

func reservedPathPrefix(relative string) bool {
	if relative == ProjectFile || relative == PolicyFile || strings.HasPrefix(relative, "e-") {
		return true
	}
	for directory := range flatLayouts {
		if relative == directory || strings.HasPrefix(relative, directory+"/") {
			return true
		}
	}
	return false
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
	candidates := make([]research.ReferenceCandidate, 0, len(inventory.Documents))
	for _, document := range inventory.Documents {
		id, ok := document.ID()
		if !ok {
			continue
		}
		common := document.Record.GetCommon()
		candidates = append(candidates, research.ReferenceCandidate{ID: id, Aliases: append([]string(nil), common.LegacyAliases...)})
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
