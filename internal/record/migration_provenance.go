package record

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const maxArchivedMigrationSourceBytes int64 = 64 << 20

func (inventory *Inventory) validateImportedProvenance() {
	if inventory == nil || inventory.Project == nil {
		return
	}
	project, ok := inventory.Project.Record.(*research.Project)
	if !ok || !project.ProjectID.IsImported() {
		for _, document := range inventory.Documents {
			if id, found := document.ID(); found && id.IsImported() {
				inventory.addDiagnostic(document.Path, "migration.project_identity", "UUIDv5 records require a UUIDv5 Project identity")
			}
		}
		return
	}
	projectExtension, ok := project.Extensions[research.MigrationExtension]
	if !ok {
		inventory.addDiagnostic(ProjectFile, "migration.provenance", "imported Project is missing harness-v0 provenance")
		return
	}
	fingerprint, fingerprintOK := extensionString(projectExtension, "fingerprint")
	archivePath, archiveOK := extensionString(projectExtension, "archive_path")
	namespace, namespaceOK := extensionString(projectExtension, "namespace")
	reader, readerOK := extensionString(projectExtension, "reader_version")
	manifestHash, manifestOK := extensionString(projectExtension, "manifest_sha256")
	if !fingerprintOK || !archiveOK || !namespaceOK || !readerOK || !manifestOK {
		inventory.addDiagnostic(ProjectFile, "migration.provenance", "imported Project provenance is incomplete")
		return
	}
	expectedProject, err := research.ImportedProjectID(fingerprint)
	if err != nil || expectedProject != project.ProjectID {
		inventory.addDiagnostic(ProjectFile, "migration.project_identity", "Project UUIDv5 does not match the archived source fingerprint")
		return
	}
	hexFingerprint := strings.TrimPrefix(fingerprint, "sha256:")
	if namespace != research.HarnessV0Namespace || reader != research.HarnessV0Reader || archivePath != path.Join("legacy/harness-v0", hexFingerprint) {
		inventory.addDiagnostic(ProjectFile, "migration.provenance", "Project migration namespace, reader, or archive path is not canonical")
		return
	}
	root, err := pathx.OpenRootNoSymlinks(inventory.Root)
	if err != nil {
		inventory.addDiagnostic(ProjectFile, "migration.archive", err.Error())
		return
	}
	defer root.Close()
	manifestPath := path.Join(archivePath, "manifest.toml")
	manifest, _, err := pathx.ReadBoundedRegularFile(context.Background(), root, manifestPath, MaxRecordBytes)
	if err != nil || exactHash(manifest) != manifestHash {
		inventory.addDiagnostic(manifestPath, "migration.archive_manifest", "archived manifest is missing or does not match Project provenance")
		return
	}

	for _, document := range inventory.Documents {
		if document.Kind() == research.KindProject {
			continue
		}
		id, found := document.ID()
		if !found || !id.IsImported() {
			continue
		}
		inventory.verifyImportedDocument(root, project.ProjectID, fingerprint, archivePath, document)
	}
}

func (inventory *Inventory) verifyImportedDocument(root *os.Root, projectID research.UUID, fingerprint, archivePath string, document *Document) {
	id, _ := document.ID()
	extension, ok := document.Record.GetExtensions()[research.MigrationExtension]
	if !ok {
		inventory.addDiagnostic(document.Path, "migration.provenance", "imported record is missing harness-v0 provenance")
		return
	}
	sourceFingerprint, fingerprintOK := extensionString(extension, "fingerprint")
	sourcePath, pathOK := extensionString(extension, "source_path")
	sourceHash, hashOK := extensionString(extension, "source_sha256")
	stableKey, keyOK := extensionString(extension, "stable_source_key")
	spanHash, spanHashOK := extensionString(extension, "span_sha256")
	start, startOK := extensionInt64(extension, "start_byte")
	end, endOK := extensionInt64(extension, "end_byte")
	if !fingerprintOK || !pathOK || !hashOK || !keyOK || !spanHashOK || !startOK || !endOK {
		inventory.addDiagnostic(document.Path, "migration.provenance", "imported record provenance is incomplete")
		return
	}
	if sourceFingerprint != fingerprint || pathx.ValidateRelativePOSIX(sourcePath, false) != nil || start < 0 || end < start {
		inventory.addDiagnostic(document.Path, "migration.provenance", "imported record provenance values are invalid")
		return
	}
	expectedID, err := research.ImportedRecordID(projectID, document.Kind(), stableKey)
	if err != nil || expectedID != id {
		inventory.addDiagnostic(document.Path, "migration.record_identity", "record UUIDv5 does not match its stable source key")
		return
	}
	archivedPath := path.Join(archivePath, "source", sourcePath)
	data, info, err := pathx.ReadBoundedRegularFile(context.Background(), root, archivedPath, maxArchivedMigrationSourceBytes)
	if err != nil {
		code := "migration.archive"
		if errors.Is(err, fs.ErrNotExist) {
			code = "migration.archive_missing"
		}
		inventory.addDiagnostic(document.Path, code, "archived source is unavailable")
		return
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || exactHash(data) != sourceHash || end > int64(len(data)) {
		inventory.addDiagnostic(document.Path, "migration.archive_hash", "archived source bytes do not match record provenance")
		return
	}
	span := data[start:end]
	if exactHash(span) != spanHash {
		inventory.addDiagnostic(document.Path, "migration.span_hash", "archived source span does not match record provenance")
	}
}

func extensionString(values map[string]any, key string) (string, bool) {
	value, ok := values[key].(string)
	return value, ok && value != ""
}

func extensionInt64(values map[string]any, key string) (int64, bool) {
	switch value := values[key].(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	default:
		return 0, false
	}
}
