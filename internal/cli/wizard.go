package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
	"github.com/spf13/cobra"
)

const maxWizardStatusPaths = 32

type wizardAuthorityStamp struct {
	ProjectID       string
	ProjectRevision string
	ProjectRootID   string
	ConfigDigest    string
	TrustDigest     string
	SourceID        string
	SourceRevision  string
	SourceTree      string
}

type wizardSourceObservation struct {
	SourceID       string
	SourceRevision string
	Root           string
	RootIdentity   string
	CommonIdentity string
	Head           string
	State          string
	Digest         string
	DirtyDigest    string
	DirtySummary   string
	Paths          []string
}

func observeWizardAuthority(command *cobra.Command, app *App, root *rootOptions, requireSource, includeTrust bool) (*workspace.Context, wizardAuthorityStamp, wizardSourceObservation, error) {
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return nil, wizardAuthorityStamp{}, wizardSourceObservation{}, err
	}
	if resolved == nil || resolved.Project == nil || resolved.Project.Document == nil || resolved.Project.Project() == nil {
		return nil, wizardAuthorityStamp{}, wizardSourceObservation{}, errors.New("wizard workspace has no canonical Project authority")
	}
	stamp, err := observeProjectStamp(resolved.Project)
	if err != nil {
		return nil, wizardAuthorityStamp{}, wizardSourceObservation{}, err
	}
	if resolved.Config != nil {
		stamp.ConfigDigest = resolved.Config.Digest
	}
	if includeTrust {
		stamp.TrustDigest, err = observeTrustDigest(command.Context(), app)
		if err != nil {
			return nil, wizardAuthorityStamp{}, wizardSourceObservation{}, err
		}
	}
	var source wizardSourceObservation
	if requireSource {
		source, err = observeWizardSource(command.Context(), app, resolved)
		if err != nil {
			return nil, wizardAuthorityStamp{}, wizardSourceObservation{}, err
		}
		stamp.SourceID = source.SourceID
		stamp.SourceRevision = source.SourceRevision
		stamp.SourceTree = source.Digest
	}
	return resolved, stamp, source, nil
}

func observeProjectStamp(info *project.Info) (wizardAuthorityStamp, error) {
	if info == nil || info.Project() == nil || info.Document == nil {
		return wizardAuthorityStamp{}, errors.New("canonical Project metadata is unavailable")
	}
	rootIdentity, err := pathx.DirectoryFilesystemIdentity(info.Repository.Root)
	if err != nil {
		return wizardAuthorityStamp{}, fmt.Errorf("identify canonical Project repository: %w", err)
	}
	commonIdentity, err := pathx.DirectoryFilesystemIdentity(info.Repository.GitCommonDir)
	if err != nil {
		return wizardAuthorityStamp{}, fmt.Errorf("identify canonical Project Git common directory: %w", err)
	}
	return wizardAuthorityStamp{
		ProjectID: info.Project().ProjectID.String(), ProjectRevision: info.Document.Revision,
		ProjectRootID: guidedFingerprint(rootIdentity, commonIdentity),
	}, nil
}

func observeTrustDigest(ctx context.Context, app *App) (string, error) {
	if app == nil || app.TrustStore == nil {
		return "", errors.New("trust store is unavailable")
	}
	receipts, err := app.TrustStore.List(ctx)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(receipts)
	if err != nil {
		return "", fmt.Errorf("encode trust observation: %w", err)
	}
	return digestWizardBytes(encoded), nil
}

func observeWizardSource(ctx context.Context, app *App, resolved *workspace.Context) (wizardSourceObservation, error) {
	if resolved == nil || resolved.Project == nil || resolved.Source == nil || resolved.Source.ID.IsZero() {
		return wizardSourceObservation{}, errors.New("wizard requires one resolved Source")
	}
	if resolved.SourceRoot == "" || resolved.Association.Source == nil {
		return wizardSourceObservation{}, errors.New("wizard Source has no validated local association")
	}
	store, err := app.NewTransactionalStore(resolved.Project)
	if err != nil {
		return wizardSourceObservation{}, err
	}
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return wizardSourceObservation{}, err
	}
	document, err := inventory.ByID(resolved.Source.ID)
	if err != nil {
		return wizardSourceObservation{}, err
	}
	if document.Kind() != research.KindSource || !record.ValidRevision(document.Revision) {
		return wizardSourceObservation{}, errors.New("resolved Source has no valid canonical revision")
	}
	rootIdentity, err := pathx.DirectoryFilesystemIdentity(resolved.SourceRoot)
	if err != nil {
		return wizardSourceObservation{}, fmt.Errorf("identify Source root: %w", err)
	}
	commonIdentity, err := pathx.DirectoryFilesystemIdentity(resolved.Association.Source.GitCommonDir)
	if err != nil {
		return wizardSourceObservation{}, fmt.Errorf("identify Source Git common directory: %w", err)
	}
	head, err := wizardGitOutput(ctx, app.GitRunner, resolved.SourceRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return wizardSourceObservation{}, fmt.Errorf("inspect Source HEAD: %w", err)
	}
	head = strings.TrimSpace(head)
	if head == "" || strings.ContainsAny(head, "\x00\r\n") {
		return wizardSourceObservation{}, errors.New("Source HEAD observation is malformed")
	}
	status, err := wizardGitOutput(ctx, app.GitRunner, resolved.SourceRoot,
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=no", "--ignore-submodules=none", "--no-renames")
	if err != nil {
		return wizardSourceObservation{}, fmt.Errorf("inspect Source status: %w", err)
	}
	paths, err := wizardStatusPaths(status)
	if err != nil {
		return wizardSourceObservation{}, err
	}
	state := "clean"
	dirtyDigest := ""
	dirtySummary := ""
	if status != "" {
		state = "dirty"
		previewTry, parseErr := research.ParseIDForKind("try_01a00000-0000-7000-8000-000000000000", research.KindTry)
		if parseErr != nil {
			return wizardSourceObservation{}, parseErr
		}
		preview, previewErr := (sourcesnapshot.Capturer{Git: app.GitRunner}).PreviewDirty(ctx, sourcesnapshot.Request{
			Source: resolved.Source, RepositoryRoot: resolved.SourceRoot,
			RegisteredGitCommonDir:      resolved.Association.Source.GitCommonDir,
			RegisteredGitCommonIdentity: resolved.Association.Source.GitCommonIdentity,
			BaseCommit:                  head, ExpectedHead: head, Try: previewTry,
			CapturedAt: resolved.Source.UpdatedAt.UTC(),
		})
		if previewErr != nil {
			return wizardSourceObservation{}, fmt.Errorf("preview bounded dirty Source snapshot: %w", previewErr)
		}
		dirtyDigest = preview.DirtyDigest
		dirtySummary = preview.DirtySummary
	}
	framed := []byte(document.Revision + "\x00" + rootIdentity + "\x00" + commonIdentity + "\x00" + head + "\x00" + status + "\x00" + dirtyDigest)
	return wizardSourceObservation{
		SourceID: resolved.Source.ID.String(), SourceRevision: document.Revision,
		Root: resolved.SourceRoot, RootIdentity: rootIdentity, CommonIdentity: commonIdentity,
		Head: head, State: state, Digest: digestWizardBytes(framed), DirtyDigest: dirtyDigest, DirtySummary: dirtySummary, Paths: paths,
	}, nil
}

func wizardGitOutput(ctx context.Context, runner gitx.Runner, directory string, arguments ...string) (string, error) {
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	prefix := []string{"--no-pager", "--no-replace-objects", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}
	stdout, stderr, err := runner.Run(ctx, directory, append(prefix, arguments...))
	if err != nil {
		return "", &gitx.Error{Dir: directory, Args: append(prefix, arguments...), Stderr: stderr, Err: err}
	}
	return stdout, nil
}

func wizardStatusPaths(status string) ([]string, error) {
	if status == "" {
		return []string{}, nil
	}
	if !strings.HasSuffix(status, "\x00") {
		return nil, errors.New("Source status observation is malformed")
	}
	records := strings.Split(status[:len(status)-1], "\x00")
	paths := make([]string, 0, len(records))
	for _, entry := range records {
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, errors.New("Source status observation is malformed")
		}
		value := entry[3:]
		if value == "" || !utf8.ValidString(value) {
			return nil, errors.New("Source status path is empty or invalid UTF-8")
		}
		paths = append(paths, safePromptText(value))
	}
	sort.Strings(paths)
	if len(paths) > maxWizardStatusPaths {
		remaining := len(paths) - maxWizardStatusPaths
		paths = append(paths[:maxWizardStatusPaths], fmt.Sprintf("… and %d more path(s)", remaining))
	}
	return paths, nil
}

func digestWizardBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func revalidateWizardAuthority(command *cobra.Command, app *App, root *rootOptions, expected wizardAuthorityStamp, requireSource, includeTrust bool) error {
	_, actual, _, err := observeWizardAuthority(command, app, root, requireSource, includeTrust)
	if err != nil {
		return err
	}
	if actual != expected {
		return &guidedPlanStaleError{subject: "Project, config, trust, or Source observation"}
	}
	return nil
}

func projectWizardFields(resolved *workspace.Context, stamp wizardAuthorityStamp) []guidedField {
	fields := []guidedField{
		{Label: "Project", Value: stamp.ProjectID},
		{Label: "Project revision", Value: stamp.ProjectRevision},
	}
	if stamp.ConfigDigest != "" {
		fields = append(fields, guidedField{Label: "Config digest", Value: stamp.ConfigDigest})
	}
	if resolved != nil && resolved.Project != nil && resolved.Project.Project() != nil {
		fields = append([]guidedField{{Label: "Name", Value: resolved.Project.Project().Name}}, fields...)
	}
	return fields
}

func sourceWizardFields(resolved *workspace.Context, source wizardSourceObservation) []guidedField {
	fields := []guidedField{
		{Label: "Source", Value: source.SourceID},
		{Label: "Source revision", Value: source.SourceRevision},
	}
	if resolved != nil && resolved.Source != nil {
		fields = append([]guidedField{{Label: "Key", Value: resolved.Source.Key}, {Label: "Subdir", Value: resolved.Source.Subdir}}, fields...)
	}
	return fields
}

func sourceSnapshotWizardFields(source wizardSourceObservation) []guidedField {
	fields := []guidedField{
		{Label: "State", Value: source.State},
		{Label: "HEAD", Value: source.Head},
		{Label: "Observed paths", Value: fmt.Sprintf("%d", len(source.Paths))},
		{Label: "Snapshot digest", Value: source.Digest},
	}
	if source.DirtyDigest != "" {
		fields = append(fields,
			guidedField{Label: "Dirty content digest", Value: source.DirtyDigest},
			guidedField{Label: "Dirty summary", Value: source.DirtySummary},
		)
	}
	return fields
}

func workspaceToolReadiness(ctx context.Context, app *App, cwd string) []guidedTool {
	tools := []guidedTool{{
		Name: "native_git workspace", Status: "ready", Reason: "built-in correctness baseline", Available: true,
	}}
	if app == nil || app.WorkspaceRegistry == nil {
		return append(tools, guidedTool{
			Name: "dev_cli workspace", Status: "disabled", Reason: "provider registry unavailable",
			Remediation: "use native_git or install a build with dev_cli support",
		})
	}
	readiness, err := app.WorkspaceRegistry.Readiness(ctx, workspacebackend.DevCLIName, cwd, false)
	if err != nil && readiness.State == "" {
		return append(tools, guidedTool{
			Name: "dev_cli workspace", Status: "disabled", Reason: safePromptText(err.Error()),
			Remediation: "install a compatible dev CLI or use native_git",
		})
	}
	status := string(readiness.State)
	available := readiness.State == workspacebackend.ReadinessBuiltIn || readiness.State == workspacebackend.ReadinessInstalledNotProbed || readiness.State == workspacebackend.ReadinessReady
	if !available {
		status = "disabled"
	}
	detail := readiness.Reason
	if detail == "" {
		detail = string(readiness.State)
	}
	remediation := ""
	if !available {
		remediation = "install and configure a compatible dev CLI, or keep native_git"
	}
	return append(tools, guidedTool{Name: "dev_cli workspace", Status: status, Reason: detail, Remediation: remediation, Available: available})
}

func commandToolReadiness(app *App, executable string) guidedTool {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return guidedTool{Name: "command", Status: "disabled", Reason: "no executable selected", Remediation: "enter a command before applying"}
	}
	if strings.ContainsAny(executable, `/\\`) {
		return guidedTool{Name: "command " + filepath.Base(executable), Status: "ready", Reason: "workspace-relative executable; verified again before execution", Available: true}
	}
	if app == nil || app.BinaryLookup == nil {
		return guidedTool{Name: "command " + executable, Status: "unknown", Reason: "binary lookup unavailable", Remediation: "ensure the executable is on PATH"}
	}
	if _, err := app.BinaryLookup(executable); err != nil {
		return guidedTool{Name: "command " + executable, Status: "disabled", Reason: "executable not found", Remediation: "install it or enter another command"}
	}
	return guidedTool{Name: "command " + executable, Status: "ready", Reason: "found on PATH", Available: true}
}
