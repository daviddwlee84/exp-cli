package harnessv0

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/lockx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
)

const migrationTransactionSchema = "exp.migration-transaction.harness-v0/v1"

type ApplyRequest struct {
	RepositoryRoot string
	GitCommonDir   string
	Plan           *Plan
	Hook           func(ApplyStage) error
}

type ApplyStage string

const (
	StagePrepared        ApplyStage = "prepared"
	StageSourcePreserved ApplyStage = "source_preserved"
	StageRootPublished   ApplyStage = "root_published"
	StageCommitted       ApplyStage = "committed"
)

type migrationJournal struct {
	Schema          string    `json:"schema"`
	TransactionID   string    `json:"transaction_id"`
	PlanHash        string    `json:"plan_hash"`
	TreeFingerprint string    `json:"tree_fingerprint"`
	SourceRoot      string    `json:"source_root"`
	StageName       string    `json:"stage_name"`
	BackupName      string    `json:"backup_name"`
	PreparedAt      time.Time `json:"prepared_at"`
	State           string    `json:"state"`
}

// Apply executes a reviewed plan through a crash-recoverable whole-root
// prepared transaction. The legacy tree is copied into a sibling staging root,
// its exact source archive and canonical candidates are added, and only then is
// the root swapped under the Git-common project lock.
func Apply(ctx context.Context, request ApplyRequest) (ApplyResult, error) {
	if err := ValidatePlan(request.Plan); err != nil {
		return ApplyResult{}, err
	}
	if !request.Plan.Applicable {
		return ApplyResult{}, fmt.Errorf("migration plan has unresolved needs_review items")
	}
	if request.RepositoryRoot == "" || request.GitCommonDir == "" {
		return ApplyResult{}, fmt.Errorf("migration apply requires repository and Git common roots")
	}
	if err := pathx.ValidateRelativePOSIX(request.Plan.SourceRoot, false); err != nil {
		return ApplyResult{}, err
	}
	transactionID := strings.TrimPrefix(request.Plan.ContentHash, "sha256:")
	if len(transactionID) < 16 {
		return ApplyResult{}, fmt.Errorf("migration plan hash is invalid")
	}
	result := ApplyResult{
		PlanHash: request.Plan.ContentHash, TreeFingerprint: request.Plan.TreeFingerprint,
		ArchivePath: request.Plan.Archive.Path, TransactionID: transactionID,
		Published: candidatePaths(request.Plan),
	}
	err := lockx.WithTrustedRoot(ctx, request.GitCommonDir, "exp/v1", func(lockRoot *os.Root) error {
		migrations, _, err := pathx.EnsureRootAtNoSymlinks(lockRoot, "migrations", 0o700)
		if err != nil {
			return fmt.Errorf("open migration transaction directory: %w", err)
		}
		defer migrations.Close()
		transactionRoot, _, err := pathx.EnsureRootAtNoSymlinks(migrations, transactionID, 0o700)
		if err != nil {
			return err
		}
		defer transactionRoot.Close()
		journal, journalBytes, found, err := readMigrationJournal(ctx, transactionRoot)
		if err != nil {
			return err
		}
		sourcePath, err := pathx.ResolveUnderNoSymlinks(request.RepositoryRoot, request.Plan.SourceRoot, false)
		if err != nil {
			return err
		}
		parent := filepath.Dir(sourcePath)
		base := filepath.Base(sourcePath)
		stageName := "." + base + ".exp-migrate-" + transactionID[:12] + ".staged"
		backupName := "." + base + ".exp-migrate-" + transactionID[:12] + ".source"
		stagePath := filepath.Join(parent, stageName)
		backupPath := filepath.Join(parent, backupName)

		if !found {
			if ok, err := verifyAppliedRoot(ctx, sourcePath, request.Plan); err == nil && ok {
				result.AlreadyApplied = true
				return nil
			}
			if err := verifySourceTree(ctx, sourcePath, request.Plan); err != nil {
				return err
			}
			if err := requireAbsent(backupPath, "migration backup"); err != nil {
				return err
			}
			if pathExists(stagePath) {
				if ok, verifyErr := verifyAppliedRoot(ctx, stagePath, request.Plan); verifyErr != nil || !ok {
					return fmt.Errorf("existing migration stage does not match the reviewed plan")
				}
			} else {
				if err := buildStage(ctx, sourcePath, stagePath, request.Plan); err != nil {
					return err
				}
			}
			journal = migrationJournal{
				Schema: migrationTransactionSchema, TransactionID: transactionID,
				PlanHash: request.Plan.ContentHash, TreeFingerprint: request.Plan.TreeFingerprint,
				SourceRoot: request.Plan.SourceRoot, StageName: stageName, BackupName: backupName,
				PreparedAt: request.Plan.GeneratedAt.UTC(), State: "prepared",
			}
			journalBytes, err = writeMigrationJournal(transactionRoot, nil, nil, journal)
			if err != nil {
				return fmt.Errorf("publish migration prepared journal: %w", err)
			}
			if err := runApplyHook(request.Hook, StagePrepared); err != nil {
				return err
			}
		} else {
			if journal.Schema != migrationTransactionSchema || journal.TransactionID != transactionID || journal.PlanHash != request.Plan.ContentHash || journal.TreeFingerprint != request.Plan.TreeFingerprint || journal.SourceRoot != request.Plan.SourceRoot || journal.StageName != stageName || journal.BackupName != backupName {
				return fmt.Errorf("migration transaction journal does not match the reviewed plan")
			}
		}

		if journal.State == "committed" {
			ok, err := verifyAppliedRoot(ctx, sourcePath, request.Plan)
			if err != nil || !ok {
				return fmt.Errorf("committed migration destination is not exact: %w", err)
			}
			if err := removeVerifiedBackup(ctx, backupPath, request.Plan); err != nil {
				return err
			}
			result.AlreadyApplied = true
			return nil
		}
		if journal.State != "prepared" {
			return fmt.Errorf("unknown migration transaction state %q", journal.State)
		}

		sourceApplied, _ := verifyAppliedRoot(ctx, sourcePath, request.Plan)
		stageApplied, _ := verifyAppliedRoot(ctx, stagePath, request.Plan)
		backupLegacy := verifySourceTree(ctx, backupPath, request.Plan) == nil
		sourceLegacy := verifySourceTree(ctx, sourcePath, request.Plan) == nil
		switch {
		case sourceLegacy && stageApplied && !pathExists(backupPath):
			if err := os.Rename(sourcePath, backupPath); err != nil {
				return fmt.Errorf("preserve legacy source root: %w", err)
			}
			if err := syncDirectory(parent); err != nil {
				return fmt.Errorf("sync migration parent after source preservation: %w", err)
			}
			if err := runApplyHook(request.Hook, StageSourcePreserved); err != nil {
				return err
			}
			backupLegacy = true
			sourceLegacy = false
		case !pathExists(sourcePath) && stageApplied && backupLegacy:
			// Crash recovery after the first rename.
		case sourceApplied && !pathExists(stagePath) && backupLegacy:
			// Crash recovery after the second rename.
		default:
			return fmt.Errorf("migration transaction paths are inconsistent; refusing to overwrite source, stage, or backup")
		}
		if !sourceApplied {
			if pathExists(sourcePath) {
				return fmt.Errorf("migration destination unexpectedly exists before publication")
			}
			if err := os.Rename(stagePath, sourcePath); err != nil {
				return fmt.Errorf("publish migrated experiments root: %w", err)
			}
			if err := syncDirectory(parent); err != nil {
				return fmt.Errorf("sync migration parent after publication: %w", err)
			}
			if err := runApplyHook(request.Hook, StageRootPublished); err != nil {
				return err
			}
			ok, err := verifyAppliedRoot(ctx, sourcePath, request.Plan)
			if err != nil || !ok {
				return fmt.Errorf("verify published migration root: %w", err)
			}
		}
		journal.State = "committed"
		info, err := transactionRoot.Lstat("journal.json")
		if err != nil {
			return err
		}
		journalBytes, err = writeMigrationJournal(transactionRoot, info, journalBytes, journal)
		if err != nil {
			return fmt.Errorf("mark migration committed: %w", err)
		}
		if err := runApplyHook(request.Hook, StageCommitted); err != nil {
			return err
		}
		if err := removeVerifiedBackup(ctx, backupPath, request.Plan); err != nil {
			return err
		}
		result.Applied = true
		return nil
	})
	return result, err
}

func runApplyHook(hook func(ApplyStage) error, stage ApplyStage) error {
	if hook == nil {
		return nil
	}
	return hook(stage)
}

func buildStage(ctx context.Context, sourcePath, stagePath string, plan *Plan) (err error) {
	if err := copyTree(ctx, sourcePath, stagePath); err != nil {
		return fmt.Errorf("stage legacy tree: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stagePath)
		}
	}()
	for _, file := range plan.SourceFiles {
		data, err := os.ReadFile(filepath.Join(sourcePath, filepath.FromSlash(file.Path)))
		if err != nil || int64(len(data)) != file.Bytes || hashBytes(data) != file.SHA256 {
			return fmt.Errorf("source %s changed while staging", file.Path)
		}
		destination := filepath.Join(stagePath, filepath.FromSlash(plan.Archive.Path), "source", filepath.FromSlash(file.Path))
		if err := writeStageFile(stagePath, destination, data, false); err != nil {
			return err
		}
	}
	manifest, err := base64.StdEncoding.DecodeString(plan.Archive.ManifestContentBase64)
	if err != nil || hashBytes(manifest) != plan.Archive.ManifestSHA256 {
		return fmt.Errorf("reviewed archive manifest is invalid")
	}
	if err := writeStageFile(stagePath, filepath.Join(stagePath, filepath.FromSlash(plan.Archive.Path), "manifest.toml"), manifest, false); err != nil {
		return err
	}
	for _, file := range plan.CandidateFiles {
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil || hashBytes(content) != file.SHA256 {
			return fmt.Errorf("candidate file %s does not match its reviewed hash", file.Path)
		}
		destination := filepath.Join(stagePath, filepath.FromSlash(file.Path))
		if !file.Generated && file.Path != record.ProjectFile {
			if existing, err := os.ReadFile(destination); err == nil && hashBytes(existing) != file.SHA256 {
				return fmt.Errorf("canonical destination %s collides with a legacy file", file.Path)
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		if err := writeStageFile(stagePath, destination, content, true); err != nil {
			return err
		}
	}
	if err := syncTree(stagePath); err != nil {
		return err
	}
	inventory, err := record.LoadInventoryContext(ctx, stagePath)
	if err != nil {
		return err
	}
	if !inventory.Valid() {
		return inventory.Error()
	}
	ok, err := verifyAppliedRoot(ctx, stagePath, plan)
	if err != nil || !ok {
		return fmt.Errorf("staged migration root is not exact: %w", err)
	}
	keep = true
	return nil
}

func verifySourceTree(ctx context.Context, sourcePath string, plan *Plan) error {
	tree, err := readTree(ctx, sourcePath)
	if err != nil {
		return err
	}
	if tree.Fingerprint != plan.TreeFingerprint || len(tree.Files) != len(plan.SourceFiles) {
		return fmt.Errorf("harness-v0 source tree fingerprint changed")
	}
	for index := range tree.Files {
		expected := plan.SourceFiles[index]
		observed := tree.Files[index].SourceFile
		if observed.Path != expected.Path || observed.Bytes != expected.Bytes || observed.SHA256 != expected.SHA256 {
			return fmt.Errorf("harness-v0 source %s changed", expected.Path)
		}
		var cursor int64
		for _, span := range expected.Spans {
			if span.StartByte != cursor || span.EndByte < span.StartByte || span.EndByte > int64(len(tree.Files[index].Data)) || hashBytes(tree.Files[index].Data[span.StartByte:span.EndByte]) != span.SHA256 {
				return fmt.Errorf("harness-v0 source span changed in %s", expected.Path)
			}
			cursor = span.EndByte
		}
		if cursor != expected.Bytes {
			return fmt.Errorf("harness-v0 source span coverage changed in %s", expected.Path)
		}
	}
	return nil
}

func verifyAppliedRoot(ctx context.Context, rootPath string, plan *Plan) (bool, error) {
	info, err := os.Lstat(rootPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, err
	}
	for _, file := range plan.CandidateFiles {
		data, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(file.Path)))
		if err != nil || hashBytes(data) != file.SHA256 {
			return false, nil
		}
	}
	manifest, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(plan.Archive.Path), "manifest.toml"))
	if err != nil || hashBytes(manifest) != plan.Archive.ManifestSHA256 {
		return false, nil
	}
	for _, file := range plan.SourceFiles {
		data, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(plan.Archive.Path), "source", filepath.FromSlash(file.Path)))
		if err != nil || int64(len(data)) != file.Bytes || hashBytes(data) != file.SHA256 {
			return false, nil
		}
	}
	inventory, err := record.LoadInventoryContext(ctx, rootPath)
	if err != nil || !inventory.Valid() {
		return false, err
	}
	return true, nil
}

func copyTree(ctx context.Context, sourcePath, destinationPath string) error {
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil || sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
		return fmt.Errorf("legacy source root is not a real directory")
	}
	if err := os.Mkdir(destinationPath, sourceInfo.Mode().Perm()); err != nil {
		return err
	}
	return filepath.WalkDir(sourcePath, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if source == sourcePath {
			return nil
		}
		relative, err := filepath.Rel(sourcePath, source)
		if err != nil {
			return err
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s blocks migration", filepath.ToSlash(relative))
		}
		destination := filepath.Join(destinationPath, relative)
		if info.IsDir() {
			return os.Mkdir(destination, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular path %s blocks migration", filepath.ToSlash(relative))
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		syncErr := output.Sync()
		return errors.Join(copyErr, syncErr, output.Close(), input.Close())
	})
}

func writeStageFile(stageRoot, destination string, data []byte, replace bool) error {
	inside, err := pathx.Contains(stageRoot, destination)
	if err != nil || !inside || destination == stageRoot {
		return fmt.Errorf("unsafe migration stage destination %s", destination)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if replace {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	file, err := os.OpenFile(destination, flags, 0o644)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	return errors.Join(writeErr, syncErr, file.Close())
}

func syncTree(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, candidate)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, directory := range directories {
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func readMigrationJournal(ctx context.Context, root *os.Root) (migrationJournal, []byte, bool, error) {
	data, info, err := pathx.ReadBoundedRegularFile(ctx, root, "journal.json", 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return migrationJournal{}, nil, false, nil
	}
	if err != nil {
		return migrationJournal{}, nil, false, err
	}
	if info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSymlink != 0 {
		return migrationJournal{}, nil, false, fmt.Errorf("migration journal permissions or type are unsafe")
	}
	var journal migrationJournal
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return journal, data, false, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return journal, data, false, err
	}
	return journal, data, true, nil
}

func writeMigrationJournal(root *os.Root, expected fs.FileInfo, expectedBytes []byte, journal migrationJournal) ([]byte, error) {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	err = record.AtomicWriteDerivedRoot(root, "journal.json", data, record.AtomicWriteOptions{Expected: expected, ExpectedContent: expectedBytes, Mode: 0o600})
	return data, err
}

func removeVerifiedBackup(ctx context.Context, backupPath string, plan *Plan) error {
	if !pathExists(backupPath) {
		return nil
	}
	if err := verifySourceTree(ctx, backupPath, plan); err != nil {
		return fmt.Errorf("refuse to remove non-matching migration backup: %w", err)
	}
	if err := os.RemoveAll(backupPath); err != nil {
		return fmt.Errorf("remove verified legacy backup after archived publication: %w", err)
	}
	return syncDirectory(filepath.Dir(backupPath))
}

func requireAbsent(path, description string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%s already exists at %s", description, path)
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func candidatePaths(plan *Plan) []string {
	paths := make([]string, 0, len(plan.CandidateFiles)+1)
	for _, file := range plan.CandidateFiles {
		paths = append(paths, file.Path)
	}
	paths = append(paths, plan.Archive.Path+"/manifest.toml")
	sort.Strings(paths)
	return paths
}
