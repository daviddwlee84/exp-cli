package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/research"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/spf13/cobra"
)

type guidedGitSnapshot struct {
	Head        string
	State       string
	Paths       []string
	fingerprint string
}

type guidedCloneObservation struct {
	Clone       workspace.SourceCloneObservation
	Snapshot    guidedGitSnapshot
	fingerprint string
}

func prepareGuidedSourceAdd(command *cobra.Command, app *App, root *rootOptions, options *sourceAddOptions) error {
	resolved, err := resolveSourceManagementContext(command, app, root)
	if err != nil {
		return err
	}
	draft := *options
	draft.locators = append([]string{}, options.locators...)
	draft.tags = append([]string{}, options.tags...)

	if strings.TrimSpace(draft.key) == "" {
		draft.key, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Source key", Description: "Project-unique lower-case name for this code Source",
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
	if strings.TrimSpace(draft.repo) == "" {
		draft.repo, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Source repository", Description: "Existing Git clone to inspect and associate",
			Default: resolved.InvocationDir, DefaultDisplay: "current directory",
		}, nonEmptySingleLine("Source repository"))
		if err != nil {
			return err
		}
	}

	customize := guidedSourceAddCustomized(command, &draft)
	if !customize {
		customize, err = app.askYesNo(command.Context(), "Customize Source metadata", "Title, subdirectory, locator hints, and tags are optional", false)
		if err != nil {
			return err
		}
	}
	if customize {
		normalizedKey, _ := research.NormalizeSourceKey(draft.key)
		draft.title, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Source title", Description: "Blank stores the canonical key fallback",
			Default: strings.TrimSpace(draft.title), DefaultDisplay: firstNonEmpty(strings.TrimSpace(draft.title), normalizedKey),
		}, optionalSingleLine("Source title"))
		if err != nil {
			return err
		}
		draft.subdir, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Source subdirectory", Description: "Git-root-relative POSIX directory",
			Default: firstNonEmpty(strings.TrimSpace(draft.subdir), "."), DefaultDisplay: firstNonEmpty(strings.TrimSpace(draft.subdir), "."),
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
		locatorText, askErr := app.askValidated(command.Context(), PromptRequest{
			Label: "Locator hints", Description: "Optional comma-separated sanitized Git remotes",
			Default: strings.Join(draft.locators, ","), DefaultDisplay: displayListFallback(draft.locators),
		}, validateGuidedLocatorList)
		if askErr != nil {
			return askErr
		}
		draft.locators = parseGuidedList(locatorText)
		tagText, askErr := app.askValidated(command.Context(), PromptRequest{
			Label: "Source tags", Description: "Optional comma-separated canonical tags",
			Default: strings.Join(draft.tags, ","), DefaultDisplay: displayListFallback(draft.tags),
		}, optionalSingleLine("Source tags"))
		if askErr != nil {
			return askErr
		}
		draft.tags = parseGuidedList(tagText)
	}

	clonePath := inputPath(resolved.InvocationDir, draft.repo)
	observed, err := observeGuidedClone(command.Context(), app, clonePath, draft.subdir, true)
	if err != nil {
		return err
	}
	localOnly := len(observed.Clone.LocatorHints) == 0 && len(draft.locators) == 0
	plan, err := sourcepkg.BuildAddPlan(sourcepkg.AddPlanRequest{
		Request: sourcepkg.AddRequest{
			Key: draft.key, Title: draft.title, Subdir: draft.subdir,
			LocatorHints: draft.locators, Tags: draft.tags,
		},
		CloneRoot: observed.Clone.Repository.Root, CloneGitCommonDir: observed.Clone.Repository.GitCommonDir,
		ObservedLocatorHints: observed.Clone.LocatorHints, ConfirmLocalOnly: localOnly || draft.confirmLocalOnly,
	})
	if err != nil {
		return err
	}
	projectObservation := guidedProjectObservation(resolved.Project)
	planFingerprint := guidedSourceAddPlanFingerprint(plan)
	if err := writeGuidedPlan(app, guidedPlan{
		Title: "Review Source add plan",
		Summary: []guidedField{
			{Label: "Key", Value: plan.Request.Key}, {Label: "Title", Value: plan.Request.Title},
			{Label: "Project", Value: resolved.ProjectID().String()}, {Label: "Local only", Value: fmt.Sprintf("%t", plan.LocalOnly)},
		},
		Effects: []string{"Publish one canonical Source", "Register the Project and local clone association", "Refresh generated projections"},
		Paths:   []string{plan.CloneRoot, resolved.Project.Root},
		Source: []guidedField{
			{Label: "Subdirectory", Value: plan.Request.Subdir}, {Label: "Locator hints", Value: displayGuidedValues(plan.Request.LocatorHints)},
			{Label: "Tags", Value: displayGuidedValues(plan.Request.Tags)},
		},
		Snapshot: guidedSnapshotFields(observed.Snapshot, observed.Clone.GitCommonIdentity),
		Tools:    []guidedTool{{Name: "git", Status: "ready", Reason: "built-in Source inspection and identity verification", Available: true}},
	}); err != nil {
		return err
	}
	confirmationToken := ""
	confirmationDescription := "No canonical or local state changes until confirmation"
	if plan.LocalOnly {
		confirmationToken = "LOCAL"
		confirmationDescription = "This Source has no sanitized remote locator; type LOCAL to accept a host-local-only identity"
	}
	if err := app.requireConfirmation(command.Context(), "Apply Source add plan", confirmationDescription, confirmationToken); err != nil {
		return err
	}

	currentResolved, err := resolveSourceManagementContext(command, app, root)
	if err != nil {
		return err
	}
	if err := requireSameGuidedObservation("Project revision", projectObservation, guidedProjectObservation(currentResolved.Project)); err != nil {
		return err
	}
	currentObserved, err := observeGuidedClone(command.Context(), app, clonePath, draft.subdir, true)
	if err != nil {
		return err
	}
	if err := requireSameGuidedObservation("Source clone snapshot", observed.fingerprint, currentObserved.fingerprint); err != nil {
		return err
	}
	currentPlan, err := sourcepkg.BuildAddPlan(sourcepkg.AddPlanRequest{
		Request: plan.Request, CloneRoot: currentObserved.Clone.Repository.Root,
		CloneGitCommonDir:    currentObserved.Clone.Repository.GitCommonDir,
		ObservedLocatorHints: currentObserved.Clone.LocatorHints, ConfirmLocalOnly: plan.LocalOnly,
	})
	if err != nil {
		return err
	}
	if err := requireSameGuidedObservation("Source add plan", planFingerprint, guidedSourceAddPlanFingerprint(currentPlan)); err != nil {
		return err
	}

	draft.confirm = true
	draft.confirmLocalOnly = plan.LocalOnly || draft.confirmLocalOnly
	*options = draft
	return nil
}

func prepareGuidedSourceRegister(command *cobra.Command, app *App, root *rootOptions, options *sourceRegisterOptions, args []string) ([]string, error) {
	resolved, store, service, err := openSourceService(command, app, root)
	if err != nil {
		return nil, err
	}
	reference, referenceErr := sourceReference(args, root, resolved)
	if referenceErr != nil {
		reference, err = askGuidedSourceReference(command.Context(), app, service)
		if err != nil {
			return nil, err
		}
	}
	document, err := service.Lookup(command.Context(), reference)
	if err != nil {
		return nil, err
	}
	value := document.Record.(*research.Source)
	if value.State != research.SourceActive {
		return nil, workspace.ErrRetiredSource
	}
	repo := strings.TrimSpace(options.repo)
	if repo == "" {
		repo, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Source repository", Description: "Existing Git clone to register for the selected Source",
			Default: resolved.InvocationDir, DefaultDisplay: "current directory",
		}, nonEmptySingleLine("Source repository"))
		if err != nil {
			return nil, err
		}
	}
	clonePath := inputPath(resolved.InvocationDir, repo)
	observed, err := observeGuidedClone(command.Context(), app, clonePath, value.Subdir, true)
	if err != nil {
		return nil, err
	}
	if err := validateGuidedSourceLocatorMatch(value, observed.Clone.LocatorHints); err != nil {
		return nil, err
	}
	view, err := sourceViewForDocument(command.Context(), resolved.Project, store, document)
	if err != nil {
		return nil, err
	}
	projectObservation := guidedProjectObservation(resolved.Project)
	sourceRevision := document.Revision
	if err := writeGuidedPlan(app, guidedPlan{
		Title: "Review Source registration plan",
		Summary: []guidedField{
			{Label: "Source", Value: view.Key + " (" + view.ID + ")"}, {Label: "State", Value: view.State},
			{Label: "Project", Value: resolved.ProjectID().String()},
		},
		Effects: []string{"Register or repair a private host-local Source association"},
		Paths:   []string{observed.Clone.Repository.Root},
		Source: []guidedField{
			{Label: "Canonical revision", Value: sourceRevision}, {Label: "Subdirectory", Value: value.Subdir},
			{Label: "Matched locators", Value: displayGuidedValues(locatorIntersectionForGuide(value.LocatorHints, observed.Clone.LocatorHints))},
		},
		Snapshot: guidedSnapshotFields(observed.Snapshot, observed.Clone.GitCommonIdentity),
		Tools:    []guidedTool{{Name: "git", Status: "ready", Reason: "built-in Source inspection and identity verification", Available: true}},
	}); err != nil {
		return nil, err
	}
	if err := app.requireConfirmation(command.Context(), "Register this Source clone", "The association is private and does not modify either Git repository", ""); err != nil {
		return nil, err
	}

	currentResolved, currentStore, currentService, err := openSourceService(command, app, root)
	if err != nil {
		return nil, err
	}
	_ = currentStore
	if err := requireSameGuidedObservation("Project revision", projectObservation, guidedProjectObservation(currentResolved.Project)); err != nil {
		return nil, err
	}
	currentDocument, err := currentService.Lookup(command.Context(), value.ID.String())
	if err != nil {
		return nil, err
	}
	if err := requireSameGuidedObservation("Source revision", sourceRevision, currentDocument.Revision); err != nil {
		return nil, err
	}
	currentObserved, err := observeGuidedClone(command.Context(), app, clonePath, value.Subdir, true)
	if err != nil {
		return nil, err
	}
	if err := requireSameGuidedObservation("Source clone snapshot", observed.fingerprint, currentObserved.fingerprint); err != nil {
		return nil, err
	}
	options.repo = repo
	return []string{value.ID.String()}, nil
}

func askGuidedSourceReference(ctx context.Context, app *App, service SourceService) (string, error) {
	documents, err := service.List(ctx)
	if err != nil {
		return "", err
	}
	active := make([]string, 0, len(documents))
	for _, document := range documents {
		value, ok := document.Record.(*research.Source)
		if ok && value.State == research.SourceActive {
			active = append(active, value.Key)
		}
	}
	sort.Strings(active)
	if len(active) == 0 {
		return "", errors.New("no active canonical Source is available to register")
	}
	fallback := ""
	if len(active) == 1 {
		fallback = active[0]
	}
	return app.askValidated(ctx, PromptRequest{
		Label: "Source", Description: "Active Source key or typed ID", Default: fallback, DefaultDisplay: fallback,
	}, func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("Source reference is required")
		}
		_, lookupErr := service.Lookup(ctx, value)
		return lookupErr
	})
}

func observeGuidedClone(ctx context.Context, app *App, clonePath, subdir string, allowUnborn bool) (guidedCloneObservation, error) {
	clone, err := workspace.InspectSourceClone(ctx, clonePath, subdir, app.GitRunner)
	if err != nil {
		return guidedCloneObservation{}, err
	}
	snapshot, err := observeGuidedGitSnapshot(ctx, app, clone.Repository.Root, allowUnborn)
	if err != nil {
		return guidedCloneObservation{}, err
	}
	locators := append([]string{}, clone.LocatorHints...)
	sort.Strings(locators)
	fingerprintParts := []string{
		clone.Repository.Root, clone.Repository.GitCommonDir, clone.GitCommonIdentity,
		clone.ResolvedRoot, snapshot.fingerprint,
	}
	fingerprintParts = append(fingerprintParts, locators...)
	return guidedCloneObservation{
		Clone: clone, Snapshot: snapshot, fingerprint: guidedFingerprint(fingerprintParts...),
	}, nil
}

func observeGuidedGitSnapshot(ctx context.Context, app *App, root string, allowUnborn bool) (guidedGitSnapshot, error) {
	headOutput, headStderr, headErr := app.GitRunner.Run(ctx, root, []string{
		"--no-pager", "--no-replace-objects", "--no-optional-locks", "rev-parse", "--verify", "HEAD^{commit}",
	})
	head := strings.TrimSpace(headOutput)
	if headErr != nil {
		headFailure := &gitx.Error{
			Dir: root, Args: []string{"--no-pager", "--no-replace-objects", "--no-optional-locks", "rev-parse", "--verify", "HEAD^{commit}"},
			Stderr: headStderr, Err: headErr,
		}
		if !allowUnborn {
			return guidedGitSnapshot{}, fmt.Errorf("inspect Source HEAD: %w", headFailure)
		}
		refs, refsStderr, refsErr := app.GitRunner.Run(ctx, root, []string{
			"--no-pager", "--no-replace-objects", "--no-optional-locks", "rev-list", "--all", "--max-count=1",
		})
		if refsErr != nil || strings.TrimSpace(refs) != "" {
			if refsErr != nil {
				headFailure = &gitx.Error{Dir: root, Args: []string{"rev-list", "--all", "--max-count=1"}, Stderr: refsStderr, Err: errors.Join(headFailure, refsErr)}
			}
			return guidedGitSnapshot{}, fmt.Errorf("inspect Source HEAD: %w", headFailure)
		}
		head = "unborn"
	}
	if head == "" || strings.ContainsAny(head, "\x00\r\n") {
		return guidedGitSnapshot{}, errors.New("Source HEAD observation is malformed")
	}
	status, _, err := app.GitRunner.Run(ctx, root, []string{
		"--no-pager", "--no-replace-objects", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false",
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=no", "--ignore-submodules=none", "--no-renames",
	})
	if err != nil {
		return guidedGitSnapshot{}, fmt.Errorf("inspect Source worktree status: %w", err)
	}
	paths, err := parseGuidedStatusPaths(status)
	if err != nil {
		return guidedGitSnapshot{}, err
	}
	state := "clean"
	if len(paths) > 0 {
		state = "dirty"
	}
	return guidedGitSnapshot{
		Head: head, State: state, Paths: paths,
		fingerprint: guidedFingerprint(root, head, status),
	}, nil
}

func parseGuidedStatusPaths(status string) ([]string, error) {
	if status == "" {
		return []string{}, nil
	}
	if !strings.HasSuffix(status, "\x00") {
		return nil, errors.New("Source status observation is malformed")
	}
	records := strings.Split(status[:len(status)-1], "\x00")
	paths := make([]string, 0, len(records))
	for _, entry := range records {
		if len(entry) < 4 || entry[2] != ' ' || !utf8.ValidString(entry[3:]) {
			return nil, errors.New("Source status observation contains an invalid path")
		}
		paths = append(paths, entry[3:])
	}
	sort.Strings(paths)
	return paths, nil
}

func guidedSnapshotFields(snapshot guidedGitSnapshot, identity string) []guidedField {
	fields := []guidedField{
		{Label: "Git state", Value: snapshot.State}, {Label: "HEAD", Value: snapshot.Head},
		{Label: "Changed paths", Value: fmt.Sprintf("%d", len(snapshot.Paths))},
		{Label: "Filesystem identity", Value: identity},
	}
	for index, path := range snapshot.Paths {
		if index == 20 {
			fields = append(fields, guidedField{Label: "More paths", Value: fmt.Sprintf("%d", len(snapshot.Paths)-index)})
			break
		}
		fields = append(fields, guidedField{Label: "Path", Value: path})
	}
	return fields
}

func guidedProjectObservation(info *project.Info) string {
	if info == nil || info.Project() == nil || info.Document == nil {
		return ""
	}
	rootIdentity, rootErr := pathx.DirectoryFilesystemIdentity(info.Repository.Root)
	commonIdentity, commonErr := pathx.DirectoryFilesystemIdentity(info.Repository.GitCommonDir)
	if rootErr != nil {
		rootIdentity = "unavailable:" + rootErr.Error()
	}
	if commonErr != nil {
		commonIdentity = "unavailable:" + commonErr.Error()
	}
	return guidedFingerprint(
		info.Project().ProjectID.String(), info.Document.Revision,
		info.Repository.Root, info.Repository.GitCommonDir, info.Root,
		rootIdentity, commonIdentity,
	)
}

func guidedSourceAddPlanFingerprint(plan sourcepkg.AddPlan) string {
	parts := []string{
		plan.Request.Key, plan.Request.Title, plan.Request.Subdir, plan.Request.Body,
		plan.CloneRoot, plan.CloneGitCommonDir, fmt.Sprintf("%t", plan.LocalOnly),
	}
	parts = append(parts, plan.Request.LocatorHints...)
	parts = append(parts, plan.Request.Tags...)
	parts = append(parts, plan.ObservedLocatorHints...)
	return guidedFingerprint(parts...)
}

func guidedSourceAddCustomized(command *cobra.Command, options *sourceAddOptions) bool {
	if command == nil || options == nil {
		return false
	}
	for _, name := range []string{"title", "subdir", "locator", "tag", "confirm-local-only"} {
		if command.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func validateGuidedLocatorList(value string) error {
	for _, locator := range parseGuidedList(value) {
		if _, err := research.NormalizeSourceLocator(locator); err != nil {
			return err
		}
	}
	return nil
}

func validateGuidedSourceLocatorMatch(source *research.Source, observed []string) error {
	if source == nil {
		return errors.New("canonical Source is unavailable")
	}
	if len(source.LocatorHints) == 0 {
		return nil
	}
	if len(locatorIntersectionForGuide(source.LocatorHints, observed)) == 0 {
		return sourcepkg.ErrLocatorObservation
	}
	return nil
}

func locatorIntersectionForGuide(left, right []string) []string {
	available := make(map[string]struct{}, len(right))
	for _, value := range right {
		available[value] = struct{}{}
	}
	result := make([]string, 0)
	for _, value := range left {
		if _, found := available[value]; found {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return slices.Compact(result)
}

func nonEmptySingleLine(label string) func(string) error {
	return func(value string) error {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%s must be a non-empty trimmed single line", label)
		}
		return nil
	}
}

func optionalSingleLine(label string) func(string) error {
	return func(value string) error {
		if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%s must be a trimmed single line", label)
		}
		return nil
	}
}

func parseGuidedList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func displayGuidedValues(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func displayListFallback(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
