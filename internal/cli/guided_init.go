package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/research"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/spf13/cobra"
)

type dedicatedTargetState string

const (
	dedicatedTargetMissing dedicatedTargetState = "missing"
	dedicatedTargetEmpty   dedicatedTargetState = "empty"
	dedicatedTargetGit     dedicatedTargetState = "git"
)

type dedicatedTargetObservation struct {
	Path           string
	Parent         string
	ParentIdentity string
	TargetIdentity string
	State          dedicatedTargetState
	Repository     gitx.Repository
	fingerprint    string
}

type dedicatedCreationError struct {
	Stage      string
	CleanupErr error
	Err        error
}

func (failure *dedicatedCreationError) Error() string {
	if failure == nil {
		return "dedicated repository creation failed"
	}
	message := "dedicated repository creation failed"
	if failure.Stage != "" {
		message += " during " + failure.Stage
	}
	if failure.Err != nil {
		message += ": " + failure.Err.Error()
	}
	if failure.CleanupErr != nil {
		message += "; safe cleanup failed: " + failure.CleanupErr.Error()
	}
	return message
}

func (failure *dedicatedCreationError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return errors.Join(failure.Err, failure.CleanupErr)
}

func prepareGuidedInit(command *cobra.Command, app *App, root *rootOptions, options *initOptions) error {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return err
	}
	draft := *options
	draft.sourceLocators = append([]string{}, options.sourceLocators...)
	draft.sourceTags = append([]string{}, options.sourceTags...)

	dedicated := dedicatedInitRequested(command, options)
	if !dedicated {
		mode, askErr := app.askChoice(command.Context(), "Project layout", "Dedicated keeps experiment records in a separate private Git repository", "dedicated", "dedicated", "embedded")
		if askErr != nil {
			return askErr
		}
		if mode == "embedded" {
			return prepareGuidedEmbeddedInit(command, app, start, &draft, options)
		}
	}

	if strings.TrimSpace(draft.sourceRepo) == "" {
		sourceRepository, discoverErr := discoverExactRepository(command.Context(), app, start)
		if discoverErr != nil {
			return fmt.Errorf("discover default Source repository: %w", discoverErr)
		}
		draft.sourceRepo, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Source repository", Description: "Existing code Git repository to bind",
			Default: sourceRepository.Root, DefaultDisplay: "current Git repository",
		}, nonEmptySingleLine("Source repository"))
		if err != nil {
			return err
		}
	}
	sourceInput := inputPath(start, draft.sourceRepo)
	sourceObserved, err := observeGuidedClone(command.Context(), app, sourceInput, firstNonEmpty(draft.sourceSubdir, "."), true)
	if err != nil {
		return err
	}

	defaultTarget := filepath.Join(filepath.Dir(sourceObserved.Clone.Repository.Root), filepath.Base(sourceObserved.Clone.Repository.Root)+"-experiments")
	if strings.TrimSpace(draft.dedicatedRepo) == "" {
		draft.dedicatedRepo, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Experiment repository", Description: "Separate private Git repository for canonical experiment records",
			Default: defaultTarget, DefaultDisplay: filepath.Base(defaultTarget),
		}, nonEmptySingleLine("Experiment repository"))
		if err != nil {
			return err
		}
	}
	target, err := inspectDedicatedTarget(command.Context(), app, start, draft.dedicatedRepo)
	if err != nil {
		return err
	}
	if target.State != dedicatedTargetGit {
		if command.Flags().Changed("dedicated-repo") && !draft.create {
			return invalidUsagef("an explicit missing or empty --dedicated-repo target also requires explicit --create")
		}
		draft.create = true
	}
	if target.State == dedicatedTargetGit && target.Repository.GitCommonDir == sourceObserved.Clone.Repository.GitCommonDir {
		return errors.New("dedicated and Source repositories must have different Git-common identities")
	}

	defaultKey := defaultSourceKey(sourceObserved.Clone.Repository.Name)
	if strings.TrimSpace(draft.sourceKey) == "" {
		draft.sourceKey, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Source key", Description: "Project-unique lower-case name for the code Source",
			Default: defaultKey, DefaultDisplay: defaultKey,
		}, func(value string) error {
			normalized, normalizeErr := research.NormalizeSourceKey(value)
			if normalizeErr != nil {
				return normalizeErr
			}
			if normalized != value {
				return fmt.Errorf("use normalized Source key %q", normalized)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(draft.name) == "" {
		defaultName := sourceObserved.Clone.Repository.Name + " experiments"
		draft.name, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Project name", Description: "Human-readable name stored in the dedicated Project",
			Default: defaultName, DefaultDisplay: defaultName,
		}, nonEmptySingleLine("Project name"))
		if err != nil {
			return err
		}
	}

	customize := guidedDedicatedInitCustomized(command, &draft)
	if !customize {
		customize, err = app.askYesNo(command.Context(), "Customize Source metadata", "Title, subdirectory, locator hints, and tags are optional", false)
		if err != nil {
			return err
		}
	}
	if customize {
		if err := gatherGuidedInitialSource(command.Context(), app, &draft); err != nil {
			return err
		}
		// Subdir selection changes the resolved Source root and therefore the
		// complete observation used by the reviewed plan.
		sourceObserved, err = observeGuidedClone(command.Context(), app, sourceInput, draft.sourceSubdir, true)
		if err != nil {
			return err
		}
	}
	localOnly := len(sourceObserved.Clone.LocatorHints) == 0 && len(draft.sourceLocators) == 0
	sourcePlan, err := sourcepkg.BuildAddPlan(sourcepkg.AddPlanRequest{
		Request: sourcepkg.AddRequest{
			Key: draft.sourceKey, Title: draft.sourceTitle, Subdir: draft.sourceSubdir,
			LocatorHints: draft.sourceLocators, Tags: draft.sourceTags,
		},
		CloneRoot: sourceObserved.Clone.Repository.Root, CloneGitCommonDir: sourceObserved.Clone.Repository.GitCommonDir,
		ObservedLocatorHints: sourceObserved.Clone.LocatorHints, ConfirmLocalOnly: localOnly || draft.confirmLocalOnly,
	})
	if err != nil {
		return err
	}

	effects := []string{"Initialize the canonical Project", "Register the private workspace association", "Publish and associate the initial Source", "Refresh generated projections"}
	if target.State != dedicatedTargetGit {
		effects = append([]string{"Create or adopt one empty target directory and run argv-only git init"}, effects...)
	}
	tools := []guidedTool{{Name: "git", Status: "ready", Reason: "required for exact repository discovery and initialization", Available: true}}
	if err := writeGuidedPlan(app, guidedPlan{
		Title: "Review dedicated init plan",
		Summary: []guidedField{
			{Label: "Layout", Value: "dedicated private repository"}, {Label: "Project name", Value: draft.name},
			{Label: "Target state", Value: string(target.State)}, {Label: "Local-only Source", Value: fmt.Sprintf("%t", sourcePlan.LocalOnly)},
		},
		Effects: effects,
		Paths:   []string{target.Path, sourcePlan.CloneRoot},
		Source: []guidedField{
			{Label: "Key", Value: sourcePlan.Request.Key}, {Label: "Title", Value: sourcePlan.Request.Title},
			{Label: "Subdirectory", Value: sourcePlan.Request.Subdir}, {Label: "Locator hints", Value: displayGuidedValues(sourcePlan.Request.LocatorHints)},
			{Label: "Tags", Value: displayGuidedValues(sourcePlan.Request.Tags)},
		},
		Snapshot: guidedSnapshotFields(sourceObserved.Snapshot, sourceObserved.Clone.GitCommonIdentity), Tools: tools,
	}); err != nil {
		return err
	}
	if sourcePlan.LocalOnly {
		if err := app.requireConfirmation(command.Context(), "Accept local-only Source", "Type LOCAL because no sanitized remote locator can corroborate this Source identity", "LOCAL"); err != nil {
			return err
		}
	}
	if target.State != dedicatedTargetGit {
		if err := app.requireConfirmation(command.Context(), "Create dedicated repository", "Type CREATE to create or initialize only the reviewed empty target", "CREATE"); err != nil {
			return err
		}
	} else if err := app.requireConfirmation(command.Context(), "Initialize dedicated Project", "No remotes, submodules, commits, or pushes will be created", ""); err != nil {
		return err
	}

	currentTarget, err := inspectDedicatedTarget(command.Context(), app, start, draft.dedicatedRepo)
	if err != nil {
		return err
	}
	if err := requireSameGuidedObservation("Dedicated target", target.fingerprint, currentTarget.fingerprint); err != nil {
		return err
	}
	currentSource, err := observeGuidedClone(command.Context(), app, sourceInput, sourcePlan.Request.Subdir, true)
	if err != nil {
		return err
	}
	if err := requireSameGuidedObservation("Source snapshot", sourceObserved.fingerprint, currentSource.fingerprint); err != nil {
		return err
	}
	currentPlan, err := sourcepkg.BuildAddPlan(sourcepkg.AddPlanRequest{
		Request: sourcePlan.Request, CloneRoot: currentSource.Clone.Repository.Root,
		CloneGitCommonDir:    currentSource.Clone.Repository.GitCommonDir,
		ObservedLocatorHints: currentSource.Clone.LocatorHints, ConfirmLocalOnly: sourcePlan.LocalOnly,
	})
	if err != nil {
		return err
	}
	if err := requireSameGuidedObservation("Initial Source plan", guidedSourceAddPlanFingerprint(sourcePlan), guidedSourceAddPlanFingerprint(currentPlan)); err != nil {
		return err
	}

	draft.confirm = true
	draft.confirmLocalOnly = sourcePlan.LocalOnly || draft.confirmLocalOnly
	draft.expectedTarget = target.fingerprint
	*options = draft
	return nil
}

func prepareGuidedEmbeddedInit(command *cobra.Command, app *App, start string, draft, options *initOptions) error {
	repository, err := discoverExactRepository(command.Context(), app, start)
	if err != nil {
		return err
	}
	if strings.TrimSpace(draft.name) == "" {
		draft.name, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Project name", Description: "Human-readable name stored beside this repository",
			Default: repository.Name, DefaultDisplay: repository.Name,
		}, nonEmptySingleLine("Project name"))
		if err != nil {
			return err
		}
	}
	observation, snapshot, err := observeEmbeddedInit(command.Context(), app, repository)
	if err != nil {
		return err
	}
	if err := writeGuidedPlan(app, guidedPlan{
		Title:    "Review embedded init plan",
		Summary:  []guidedField{{Label: "Layout", Value: "embedded"}, {Label: "Project name", Value: draft.name}},
		Effects:  []string{"Initialize or adopt experiments/ in the current Git repository", "Refresh generated projections"},
		Paths:    []string{repository.Root, filepath.Join(repository.Root, "experiments")},
		Snapshot: guidedSnapshotFields(snapshot, observation),
		Tools:    []guidedTool{{Name: "git", Status: "ready", Reason: "required for exact repository discovery", Available: true}},
	}); err != nil {
		return err
	}
	if err := app.requireConfirmation(command.Context(), "Initialize embedded Project", "This writes canonical files in the current repository", ""); err != nil {
		return err
	}
	currentObservation, _, err := observeEmbeddedInit(command.Context(), app, repository)
	if err != nil {
		return err
	}
	if err := requireSameGuidedObservation("Embedded Project target", observation, currentObservation); err != nil {
		return err
	}
	*options = *draft
	return nil
}

func observeEmbeddedInit(ctx context.Context, app *App, repository gitx.Repository) (string, guidedGitSnapshot, error) {
	rootIdentity, err := pathx.DirectoryFilesystemIdentity(repository.Root)
	if err != nil {
		return "", guidedGitSnapshot{}, err
	}
	commonIdentity, err := pathx.DirectoryFilesystemIdentity(repository.GitCommonDir)
	if err != nil {
		return "", guidedGitSnapshot{}, err
	}
	snapshot, err := observeGuidedGitSnapshot(ctx, app, repository.Root, true)
	if err != nil {
		return "", guidedGitSnapshot{}, err
	}
	projectState := "uninitialized"
	if info, discoverErr := app.DiscoverProject(ctx, repository.Root); discoverErr == nil {
		projectState = guidedProjectObservation(info)
	} else if !errors.Is(discoverErr, project.ErrNotInitialized) {
		return "", guidedGitSnapshot{}, discoverErr
	}
	return guidedFingerprint(repository.Root, repository.GitCommonDir, rootIdentity, commonIdentity, projectState, snapshot.fingerprint), snapshot, nil
}

func gatherGuidedInitialSource(ctx context.Context, app *App, draft *initOptions) error {
	var err error
	draft.sourceTitle, err = app.askValidated(ctx, PromptRequest{
		Label: "Source title", Description: "Blank stores the canonical key fallback",
		Default: strings.TrimSpace(draft.sourceTitle), DefaultDisplay: firstNonEmpty(strings.TrimSpace(draft.sourceTitle), draft.sourceKey),
	}, optionalSingleLine("Source title"))
	if err != nil {
		return err
	}
	draft.sourceSubdir, err = app.askValidated(ctx, PromptRequest{
		Label: "Source subdirectory", Description: "Git-root-relative POSIX directory",
		Default: firstNonEmpty(strings.TrimSpace(draft.sourceSubdir), "."), DefaultDisplay: firstNonEmpty(strings.TrimSpace(draft.sourceSubdir), "."),
	}, func(value string) error {
		normalized, normalizeErr := research.NormalizeSourceSubdir(value)
		if normalizeErr != nil {
			return normalizeErr
		}
		if normalized != value {
			return fmt.Errorf("use normalized Source subdirectory %q", normalized)
		}
		return nil
	})
	if err != nil {
		return err
	}
	locatorText, err := app.askValidated(ctx, PromptRequest{
		Label: "Locator hints", Description: "Optional comma-separated sanitized Git remotes",
		Default: strings.Join(draft.sourceLocators, ","), DefaultDisplay: displayListFallback(draft.sourceLocators),
	}, validateGuidedLocatorList)
	if err != nil {
		return err
	}
	draft.sourceLocators = parseGuidedList(locatorText)
	tagText, err := app.askValidated(ctx, PromptRequest{
		Label: "Source tags", Description: "Optional comma-separated canonical tags",
		Default: strings.Join(draft.sourceTags, ","), DefaultDisplay: displayListFallback(draft.sourceTags),
	}, optionalSingleLine("Source tags"))
	if err != nil {
		return err
	}
	draft.sourceTags = parseGuidedList(tagText)
	return nil
}

func guidedDedicatedInitCustomized(command *cobra.Command, options *initOptions) bool {
	if command == nil || options == nil {
		return false
	}
	for _, name := range []string{"source-title", "source-subdir", "source-locator", "source-tag", "confirm-local-only"} {
		if command.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func defaultSourceKey(name string) string {
	candidate := strings.ToLower(strings.TrimSpace(name))
	var output strings.Builder
	previousSeparator := false
	for _, character := range candidate {
		valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-'
		if valid {
			output.WriteRune(character)
			previousSeparator = character == '.' || character == '_' || character == '-'
			continue
		}
		if !previousSeparator && output.Len() > 0 {
			output.WriteByte('-')
			previousSeparator = true
		}
	}
	value := strings.Trim(output.String(), "._-")
	if normalized, err := research.NormalizeSourceKey(value); err == nil {
		return normalized
	}
	return "source"
}

func discoverExactRepository(ctx context.Context, app *App, start string) (gitx.Repository, error) {
	repository, err := gitx.DiscoverWithRunner(ctx, start, app.GitRunner)
	if err != nil {
		return gitx.Repository{}, err
	}
	return repository, nil
}

func inspectDedicatedTarget(ctx context.Context, app *App, start, value string) (dedicatedTargetObservation, error) {
	input := inputPath(start, value)
	if !filepath.IsAbs(input) || filepath.Clean(input) != input || filepath.Base(input) == "." || filepath.Base(input) == string(filepath.Separator) {
		return dedicatedTargetObservation{}, errors.New("dedicated repository target must be a clean absolute directory path")
	}
	parent := filepath.Dir(input)
	canonicalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return dedicatedTargetObservation{}, fmt.Errorf("resolve dedicated target parent: %w", err)
	}
	if filepath.Clean(canonicalParent) != parent {
		return dedicatedTargetObservation{}, errors.New("dedicated target parent must be an existing real path without symlink substitution")
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return dedicatedTargetObservation{}, fmt.Errorf("inspect dedicated target parent: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return dedicatedTargetObservation{}, errors.New("dedicated target parent must be a real directory")
	}
	parentIdentity, err := pathx.DirectoryFilesystemIdentity(parent)
	if err != nil {
		return dedicatedTargetObservation{}, err
	}
	observation := dedicatedTargetObservation{Path: input, Parent: parent, ParentIdentity: parentIdentity}
	info, statErr := os.Lstat(input)
	if errors.Is(statErr, fs.ErrNotExist) {
		if enclosing, discoverErr := gitx.DiscoverWithRunner(ctx, parent, app.GitRunner); discoverErr == nil && enclosing.Root != input {
			return dedicatedTargetObservation{}, fmt.Errorf("missing dedicated target would be nested inside Git repository %s", enclosing.Root)
		}
		observation.State = dedicatedTargetMissing
		observation.fingerprint = dedicatedTargetFingerprint(observation)
		return observation, nil
	}
	if statErr != nil {
		return dedicatedTargetObservation{}, fmt.Errorf("inspect dedicated target: %w", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return dedicatedTargetObservation{}, errors.New("dedicated target must be a missing or real directory")
	}
	canonicalTarget, err := filepath.EvalSymlinks(input)
	if err != nil {
		return dedicatedTargetObservation{}, fmt.Errorf("resolve dedicated target: %w", err)
	}
	if filepath.Clean(canonicalTarget) != input {
		return dedicatedTargetObservation{}, errors.New("dedicated target path was substituted through a symlink")
	}
	observation.TargetIdentity, err = pathx.DirectoryFilesystemIdentity(input)
	if err != nil {
		return dedicatedTargetObservation{}, err
	}
	repository, discoverErr := gitx.DiscoverWithRunner(ctx, input, app.GitRunner)
	if discoverErr == nil {
		if repository.Root != input {
			return dedicatedTargetObservation{}, fmt.Errorf("dedicated target is nested inside Git repository %s; name the exact repository root", repository.Root)
		}
		if enclosing, enclosingErr := gitx.DiscoverWithRunner(ctx, parent, app.GitRunner); enclosingErr == nil && enclosing.Root != input {
			return dedicatedTargetObservation{}, fmt.Errorf("dedicated target is an inner Git repository nested inside %s", enclosing.Root)
		}
		observation.State = dedicatedTargetGit
		observation.Repository = repository
		observation.fingerprint = dedicatedTargetFingerprint(observation)
		return observation, nil
	}
	empty, err := directoryEmpty(input)
	if err != nil {
		return dedicatedTargetObservation{}, err
	}
	if !empty {
		return dedicatedTargetObservation{}, errors.New("dedicated target is nonempty and is not an exact Git repository root")
	}
	// If discovery failed because an enclosing repository was reached, this target
	// is ambiguous even though its own directory is empty.
	if enclosing, enclosingErr := gitx.DiscoverWithRunner(ctx, parent, app.GitRunner); enclosingErr == nil && enclosing.Root != input {
		return dedicatedTargetObservation{}, fmt.Errorf("empty dedicated target is nested inside Git repository %s", enclosing.Root)
	}
	observation.State = dedicatedTargetEmpty
	observation.fingerprint = dedicatedTargetFingerprint(observation)
	return observation, nil
}

func dedicatedTargetFingerprint(observation dedicatedTargetObservation) string {
	return guidedFingerprint(
		observation.Path, observation.Parent, observation.ParentIdentity, observation.TargetIdentity,
		string(observation.State), observation.Repository.Root, observation.Repository.GitCommonDir,
	)
}

func directoryEmpty(path string) (bool, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return false, err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return false, err
	}
	entries, readErr := directory.ReadDir(1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, errors.Join(readErr, closeErr)
	}
	return len(entries) == 0, closeErr
}

func ensureDedicatedRepository(ctx context.Context, app *App, start, value string, create bool, expected string) (gitx.Repository, error) {
	planned, err := inspectDedicatedTarget(ctx, app, start, value)
	if err != nil {
		return gitx.Repository{}, err
	}
	if expected != "" && planned.fingerprint != expected {
		return gitx.Repository{}, &guidedPlanStaleError{subject: "Dedicated target"}
	}
	if planned.State == dedicatedTargetGit {
		return planned.Repository, nil
	}
	if !create {
		return gitx.Repository{}, errors.New("dedicated repository must already be an exact Git root unless --create and --confirm are both provided")
	}

	parentRoot, err := pathx.OpenCanonicalRootNoSymlinks(planned.Parent)
	if err != nil {
		return gitx.Repository{}, err
	}
	defer parentRoot.Close()
	if verifyErr := pathx.VerifyRootPath(planned.Parent, parentRoot); verifyErr != nil {
		return gitx.Repository{}, &guidedPlanStaleError{subject: "Dedicated target parent"}
	}
	if identity, identityErr := pathx.DirectoryFilesystemIdentity(planned.Parent); identityErr != nil || identity != planned.ParentIdentity {
		return gitx.Repository{}, &guidedPlanStaleError{subject: "Dedicated target parent"}
	}
	created := false
	if planned.State == dedicatedTargetMissing {
		if err := parentRoot.Mkdir(filepath.Base(planned.Path), 0o700); err != nil {
			return gitx.Repository{}, fmt.Errorf("create dedicated target directory: %w", err)
		}
		created = true
	}
	targetInfo, err := parentRoot.Lstat(filepath.Base(planned.Path))
	if err != nil || targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() {
		cleanupErr := cleanupDedicatedGitInit(parentRoot, planned.Path, targetInfo, created, false)
		return gitx.Repository{}, &dedicatedCreationError{Stage: "target verification", CleanupErr: cleanupErr, Err: err}
	}
	targetIdentity, err := pathx.DirectoryFilesystemIdentity(planned.Path)
	if err != nil {
		cleanupErr := cleanupDedicatedGitInit(parentRoot, planned.Path, targetInfo, created, false)
		return gitx.Repository{}, &dedicatedCreationError{Stage: "target identity", CleanupErr: cleanupErr, Err: err}
	}
	if planned.State == dedicatedTargetEmpty && targetIdentity != planned.TargetIdentity {
		return gitx.Repository{}, &guidedPlanStaleError{subject: "Dedicated target filesystem identity"}
	}
	if empty, emptyErr := directoryEmpty(planned.Path); emptyErr != nil || !empty {
		cleanupErr := cleanupDedicatedGitInit(parentRoot, planned.Path, targetInfo, created, false)
		return gitx.Repository{}, &dedicatedCreationError{Stage: "empty-target revalidation", CleanupErr: cleanupErr, Err: errors.Join(emptyErr, ErrGuidedPlanStale)}
	}

	if verifyErr := verifyDedicatedTargetPath(parentRoot, planned, targetInfo, targetIdentity); verifyErr != nil {
		cleanupErr := cleanupDedicatedGitInit(parentRoot, planned.Path, targetInfo, created, false)
		return gitx.Repository{}, &dedicatedCreationError{Stage: "pre-init path verification", CleanupErr: cleanupErr, Err: verifyErr}
	}
	_, stderr, initErr := app.GitRunner.Run(ctx, planned.Path, []string{"init", "--quiet"})
	if initErr != nil {
		cleanupErr := cleanupDedicatedGitInit(parentRoot, planned.Path, targetInfo, created, true)
		return gitx.Repository{}, &dedicatedCreationError{Stage: "git init", CleanupErr: cleanupErr, Err: &gitx.Error{Dir: planned.Path, Args: []string{"init", "--quiet"}, Stderr: stderr, Err: initErr}}
	}
	repository, discoverErr := gitx.DiscoverWithRunner(ctx, planned.Path, app.GitRunner)
	if discoverErr != nil || repository.Root != planned.Path {
		cleanupErr := cleanupDedicatedGitInit(parentRoot, planned.Path, targetInfo, created, true)
		return gitx.Repository{}, &dedicatedCreationError{Stage: "git init postcondition", CleanupErr: cleanupErr, Err: discoverErr}
	}
	currentInfo, statErr := parentRoot.Lstat(filepath.Base(planned.Path))
	currentIdentity, identityErr := pathx.DirectoryFilesystemIdentity(planned.Path)
	if statErr != nil || identityErr != nil || !os.SameFile(targetInfo, currentInfo) || currentIdentity != targetIdentity {
		cleanupErr := cleanupDedicatedGitInit(parentRoot, planned.Path, targetInfo, created, true)
		return gitx.Repository{}, &dedicatedCreationError{Stage: "path-substitution postcondition", CleanupErr: cleanupErr, Err: errors.Join(statErr, identityErr, ErrGuidedPlanStale)}
	}
	entries, entriesErr := directoryNames(planned.Path)
	if entriesErr != nil || len(entries) != 1 || entries[0] != ".git" {
		cleanupErr := cleanupDedicatedGitInit(parentRoot, planned.Path, targetInfo, created, true)
		return gitx.Repository{}, &dedicatedCreationError{Stage: "empty-target race postcondition", CleanupErr: cleanupErr, Err: errors.Join(entriesErr, ErrGuidedPlanStale)}
	}
	return repository, nil
}

func verifyDedicatedTargetPath(parent *os.Root, planned dedicatedTargetObservation, expected fs.FileInfo, expectedIdentity string) error {
	if parent == nil || expected == nil {
		return errors.New("dedicated target verification lacks a pinned filesystem identity")
	}
	if err := pathx.VerifyRootPath(planned.Parent, parent); err != nil {
		return err
	}
	rootInfo, rootErr := parent.Lstat(filepath.Base(planned.Path))
	pathInfo, pathErr := os.Lstat(planned.Path)
	identity, identityErr := pathx.DirectoryFilesystemIdentity(planned.Path)
	canonical, canonicalErr := filepath.EvalSymlinks(planned.Path)
	if rootErr != nil || pathErr != nil || identityErr != nil || canonicalErr != nil ||
		rootInfo.Mode()&os.ModeSymlink != 0 || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!rootInfo.IsDir() || !pathInfo.IsDir() || !os.SameFile(expected, rootInfo) || !os.SameFile(expected, pathInfo) ||
		identity != expectedIdentity || filepath.Clean(canonical) != planned.Path {
		return errors.Join(rootErr, pathErr, identityErr, canonicalErr, ErrGuidedPlanStale)
	}
	return nil
}

func directoryNames(path string) ([]string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
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

func cleanupDedicatedGitInit(parent *os.Root, target string, expected fs.FileInfo, created, gitInvoked bool) error {
	if parent == nil || expected == nil {
		return errors.New("dedicated cleanup lacks a pinned target identity")
	}
	name := filepath.Base(target)
	current, err := parent.Lstat(name)
	if err != nil || !os.SameFile(expected, current) {
		return errors.Join(err, errors.New("dedicated target changed identity; cleanup refused"))
	}
	if created {
		return parent.RemoveAll(name)
	}
	if !gitInvoked {
		return nil
	}
	entries, err := directoryNames(target)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if len(entries) != 1 || entries[0] != ".git" {
		return errors.New("dedicated target contains paths not owned by git init; cleanup refused")
	}
	targetRoot, err := pathx.OpenCanonicalRootNoSymlinks(target)
	if err != nil {
		return err
	}
	defer targetRoot.Close()
	opened, err := targetRoot.Stat(".")
	if err != nil || !os.SameFile(expected, opened) {
		return errors.Join(err, errors.New("dedicated target changed identity; cleanup refused"))
	}
	if err := targetRoot.RemoveAll(".git"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
