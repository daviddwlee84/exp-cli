package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
	"github.com/spf13/cobra"
)

func prepareGuidedTryRun(command *cobra.Command, app *App, root *rootOptions, options *tryRunOptions, argv []string) ([]string, error) {
	draft := *options
	draft.allow = append([]string{}, options.allow...)
	draft.tags = append([]string{}, options.tags...)
	rootDraft := *root
	var err error

	if strings.TrimSpace(draft.title) == "" {
		draft.title, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Try title", Description: "Short human-readable description of the bounded exploration",
		}, nonEmptySingleLine("Try title"))
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(draft.goal) == "" {
		draft.goal, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Try goal", Description: "Evidence this direct command should gather",
		}, nonEmptySingleLine("Try goal"))
		if err != nil {
			return nil, err
		}
	}
	if len(argv) == 0 {
		commandLine, askErr := app.askValidated(command.Context(), PromptRequest{
			Label: "Command", Description: "Quoted argv only; no shell expansion, pipes, redirects, or interpolation",
		}, func(value string) error {
			parsed, parseErr := parseGuidedArgv(value)
			if parseErr != nil {
				return parseErr
			}
			return validateGuidedExecutable(app, parsed[0])
		})
		if askErr != nil {
			return nil, askErr
		}
		argv, err = parseGuidedArgv(commandLine)
		if err != nil {
			return nil, err
		}
	}

	resolved, authority, source, err := observeWizardAuthority(command, app, &rootDraft, true, true)
	if err != nil {
		return nil, err
	}
	customize := guidedTryRunCustomized(command, root, &draft)
	if !customize {
		customize, err = app.askYesNo(command.Context(), "Customize execution", "Dirty capture, write allowlist, workspace backend, MLflow profile, timeout, and tags are advanced", false)
		if err != nil {
			return nil, err
		}
	}
	if source.State == "dirty" && !customize {
		customize, err = app.askYesNo(command.Context(), "Review dirty capture", "The Source is dirty; continuing requires advanced review and explicit bounded capture", false)
		if err != nil {
			return nil, err
		}
		if !customize {
			return nil, ErrPromptCanceled
		}
	}
	if customize {
		if err := gatherGuidedTryRunAdvanced(command.Context(), app, resolved, source, &rootDraft, &draft); err != nil {
			return nil, err
		}
	}

	dirtyCapture := strings.TrimSpace(draft.dirty) == "capture"
	request := tryflow.RunRequest{
		Selection: tryflow.Selection{
			InvocationDir: resolved.InvocationDir, Workspace: strings.TrimSpace(rootDraft.workspace), Source: strings.TrimSpace(rootDraft.source),
			WorkspaceBackend: strings.TrimSpace(rootDraft.workspaceBackend), MLflowProfile: strings.TrimSpace(rootDraft.mlflowProfile),
		},
		Title: draft.title, Body: draft.body, Goal: draft.goal, Argv: append([]string{}, argv...),
		DirtyCapture: dirtyCapture, AllowedGlobs: append([]string{}, draft.allow...), Timeout: draft.timeout,
		Tags: append([]string{}, draft.tags...),
	}
	if source.State == "dirty" && !dirtyCapture {
		return nil, errors.New("dirty Source requires explicit capture in the advanced review")
	}
	if source.State == "clean" && dirtyCapture {
		return nil, errors.New("dirty capture was selected but the reviewed Source is clean")
	}
	if err := tryflow.ValidateRunPlan(request, resolved); err != nil {
		return nil, err
	}
	if err := validateGuidedWorkspaceBackend(command.Context(), app, resolved, rootDraft.workspaceBackend); err != nil {
		return nil, err
	}
	if err := validateGuidedExecutableInContext(app, resolved, argv[0]); err != nil {
		return nil, err
	}

	redactedArgv := safex.NewRedactor().Argv(argv, safex.SensitiveArgvIndexes(argv)...)
	tools := workspaceToolReadiness(command.Context(), app, resolved.SourceRoot)
	tools = append(tools, mlflowToolReadiness(app, resolved, rootDraft.mlflowProfile), commandToolReadiness(app, argv[0]))
	effects := []string{"Publish one canonical Try and planned Attempt atomically", "Prepare an isolated managed Source worktree", "Execute the reviewed argv directly without a shell", "Import the durable terminal result and refresh projections"}
	if dirtyCapture {
		effects = append([]string{"Capture the reviewed bounded dirty Source state in private seed storage"}, effects...)
	}
	snapshot := sourceSnapshotWizardFields(source)
	for _, path := range source.Paths {
		snapshot = append(snapshot, guidedField{Label: "Changed path", Value: path})
	}
	if err := writeGuidedPlan(app, guidedPlan{
		Title: "Review direct Try plan",
		Summary: append(projectWizardFields(resolved, authority),
			guidedField{Label: "Title", Value: draft.title}, guidedField{Label: "Goal", Value: draft.goal},
			guidedField{Label: "Argv", Value: strings.Join(redactedArgv, " ")}, guidedField{Label: "Dirty capture", Value: strconv.FormatBool(dirtyCapture)},
			guidedField{Label: "Allowed writes", Value: displayGuidedValues(draft.allow)},
		),
		Effects: effects,
		Paths:   []string{resolved.Project.Root, source.Root},
		Source:  sourceWizardFields(resolved, source), Snapshot: snapshot, Tools: tools,
	}); err != nil {
		return nil, err
	}
	if err := app.requireConfirmation(command.Context(), "Run this Try", "Type RUN because the command can consume compute and modify its isolated worktree", "RUN"); err != nil {
		return nil, err
	}
	if err := revalidateWizardAuthority(command, app, &rootDraft, authority, true, true); err != nil {
		return nil, err
	}
	if err := validateGuidedWorkspaceBackend(command.Context(), app, resolved, rootDraft.workspaceBackend); err != nil {
		return nil, errors.Join(&guidedPlanStaleError{subject: "Workspace backend readiness"}, err)
	}
	if err := validateGuidedExecutableInContext(app, resolved, argv[0]); err != nil {
		return nil, errors.Join(&guidedPlanStaleError{subject: "Command executable readiness"}, err)
	}

	*options = draft
	*root = rootDraft
	return append([]string{}, argv...), nil
}

func gatherGuidedTryRunAdvanced(ctx context.Context, app *App, resolved *workspace.Context, source wizardSourceObservation, root *rootOptions, options *tryRunOptions) error {
	dirtyDefault := "clean"
	if strings.TrimSpace(options.dirty) == "capture" {
		dirtyDefault = "capture"
	}
	dirty, err := app.askValidated(ctx, PromptRequest{
		Label: "Source state", Description: "Choose clean or capture; capture stores a bounded private seed", Default: dirtyDefault, DefaultDisplay: dirtyDefault,
	}, func(value string) error {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "clean" && value != "capture" {
			return errors.New("choose clean or capture")
		}
		if source.State == "dirty" && value != "capture" {
			return errors.New("the reviewed Source is dirty; choose capture or cancel")
		}
		if source.State == "clean" && value == "capture" {
			return errors.New("the reviewed Source is clean; choose clean")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if strings.EqualFold(dirty, "capture") {
		options.dirty = "capture"
	} else {
		options.dirty = ""
	}
	allowText, err := app.askValidated(ctx, PromptRequest{
		Label: "Allowed writes", Description: "Optional comma-separated Source-relative glob allowlist; blank is read-only",
		Default: strings.Join(options.allow, ","), DefaultDisplay: displayListFallback(options.allow),
	}, validateGuidedAllowlist)
	if err != nil {
		return err
	}
	options.allow = parseGuidedList(allowText)

	backendDefault := strings.TrimSpace(root.workspaceBackend)
	if backendDefault == "" && resolved != nil && resolved.Config != nil {
		backendDefault = resolved.Config.Effective.Defaults.WorkspaceBackend
	}
	backendDefault = firstNonEmpty(backendDefault, "native_git")
	backend, err := app.askChoice(ctx, "Workspace backend", "native_git is the built-in correctness baseline", backendDefault, "native_git", "dev_cli")
	if err != nil {
		return err
	}
	root.workspaceBackend = backend

	profileDefault := strings.TrimSpace(root.mlflowProfile)
	if profileDefault == "" && resolved != nil && resolved.Config != nil {
		profileDefault = resolved.Config.Effective.Defaults.MLflowProfile
	}
	profile, err := app.askValidated(ctx, PromptRequest{
		Label: "MLflow profile", Description: "Optional trusted profile name; no environment values are displayed", Default: profileDefault,
		DefaultDisplay: firstNonEmpty(profileDefault, "none"),
	}, func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		if resolved == nil || resolved.Config == nil {
			return errors.New("configuration is unavailable")
		}
		if _, found := resolved.Config.Effective.MLflow.Profiles[value]; !found {
			return fmt.Errorf("MLflow profile %q is not configured", value)
		}
		return nil
	})
	if err != nil {
		return err
	}
	root.mlflowProfile = strings.TrimSpace(profile)

	timeoutDefault, timeoutDisplay := "0", "none"
	if options.timeout > 0 {
		timeoutDefault, timeoutDisplay = options.timeout.String(), options.timeout.String()
	}
	timeoutText, err := app.askValidated(ctx, PromptRequest{
		Label: "Timeout", Description: "Go duration, or 0 for no additional direct-command deadline", Default: timeoutDefault, DefaultDisplay: timeoutDisplay,
	}, func(value string) error {
		if strings.TrimSpace(value) == "0" {
			return nil
		}
		duration, parseErr := time.ParseDuration(strings.TrimSpace(value))
		if parseErr != nil || duration <= 0 || duration > 7*24*time.Hour {
			return errors.New("use 0 or a positive duration no longer than 168h")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(timeoutText) == "0" {
		options.timeout = 0
	} else {
		options.timeout, _ = time.ParseDuration(strings.TrimSpace(timeoutText))
	}
	tagText, err := app.askValidated(ctx, PromptRequest{
		Label: "Try tags", Description: "Optional comma-separated canonical tags", Default: strings.Join(options.tags, ","), DefaultDisplay: displayListFallback(options.tags),
	}, optionalSingleLine("Try tags"))
	if err != nil {
		return err
	}
	options.tags = parseGuidedList(tagText)
	return nil
}

func guidedTryRunCustomized(command *cobra.Command, root *rootOptions, options *tryRunOptions) bool {
	if command != nil {
		for _, name := range []string{"dirty", "allow", "timeout", "tags", "body"} {
			if command.Flags().Changed(name) {
				return true
			}
		}
	}
	return root != nil && (strings.TrimSpace(root.workspaceBackend) != "" || strings.TrimSpace(root.mlflowProfile) != "") ||
		options != nil && (strings.TrimSpace(options.dirty) != "" || len(options.allow) > 0 || options.timeout > 0 || len(options.tags) > 0)
}

func validateGuidedAllowlist(value string) error {
	for _, pattern := range parseGuidedList(value) {
		if len(pattern) > 512 || strings.ContainsAny(pattern, "\x00\r\n\\") || filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
			return fmt.Errorf("unsafe Source-relative allowlist glob %q", pattern)
		}
		if _, err := filepath.Match(pattern, "probe"); err != nil {
			return fmt.Errorf("invalid allowlist glob %q: %w", pattern, err)
		}
	}
	return nil
}

func validateGuidedWorkspaceBackend(ctx context.Context, app *App, resolved *workspace.Context, explicit string) error {
	if app == nil || app.WorkspaceRegistry == nil {
		return errors.New("workspace provider registry is unavailable")
	}
	if resolved == nil || resolved.Config == nil {
		return errors.New("effective workspace configuration is unavailable")
	}
	selection, err := workspacebackend.ResolveSelection(strings.TrimSpace(explicit), resolved.Config)
	if err != nil {
		return err
	}
	resolution, err := app.WorkspaceRegistry.Preview(ctx, selection, workspacebackend.CapabilityPrepare, resolved.SourceRoot, false)
	if err != nil {
		return fmt.Errorf("workspace backend %s is disabled for Try preparation: %w", selection.Requested, err)
	}
	if resolution.Fallback || resolution.Actual != selection.Requested {
		reason := resolution.FallbackReason
		if reason == "" {
			reason = "requested provider is unavailable"
		}
		return fmt.Errorf("workspace backend %s is disabled for Try preparation (%s); select %s explicitly", selection.Requested, reason, resolution.Actual)
	}
	return nil
}

func validateGuidedExecutableInContext(app *App, resolved *workspace.Context, executable string) error {
	if err := validateGuidedExecutable(app, executable); err != nil {
		return err
	}
	if !strings.ContainsAny(executable, `/\\`) {
		return nil
	}
	if resolved == nil || resolved.ResolvedRoot == "" {
		return errors.New("resolved Source working directory is unavailable")
	}
	base := resolved.ResolvedRoot
	if inside, err := pathx.Contains(resolved.ResolvedRoot, resolved.InvocationDir); err == nil && inside {
		base = resolved.InvocationDir
	}
	relative := filepath.ToSlash(strings.TrimPrefix(executable, "./"))
	if relative == "" {
		return errors.New("relative command executable is malformed")
	}
	target, err := pathx.ResolveUnderNoSymlinks(base, relative, false)
	if err != nil {
		return fmt.Errorf("resolve relative command executable: %w", err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("inspect relative command executable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("relative command executable must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("relative command executable is not executable")
	}
	return nil
}

func validateGuidedExecutable(app *App, executable string) error {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return errors.New("command executable is required")
	}
	if strings.ContainsAny(executable, `/\\`) {
		if filepath.IsAbs(executable) {
			return errors.New("command executable must be PATH-resolved or managed-worktree-relative")
		}
		return nil
	}
	if app == nil || app.BinaryLookup == nil {
		return errors.New("command executable lookup is unavailable")
	}
	if _, err := app.BinaryLookup(executable); err != nil {
		return fmt.Errorf("command executable %q is not on PATH: %w", executable, err)
	}
	return nil
}

func mlflowToolReadiness(app *App, resolved *workspace.Context, explicit string) guidedTool {
	name := strings.TrimSpace(explicit)
	if name == "" && resolved != nil && resolved.Config != nil {
		name = resolved.Config.Effective.Defaults.MLflowProfile
	}
	if name == "" {
		return guidedTool{Name: "MLflow observation", Status: "disabled", Reason: "no profile selected", Remediation: "customize the Try and select a trusted MLflow profile"}
	}
	if resolved == nil || resolved.Config == nil {
		return guidedTool{Name: "MLflow profile " + name, Status: "disabled", Reason: "configuration unavailable", Remediation: "inspect exp config explain before selecting this profile"}
	}
	profile, found := resolved.Config.Effective.MLflow.Profiles[name]
	if !found {
		return guidedTool{Name: "MLflow profile " + name, Status: "disabled", Reason: "profile not configured", Remediation: "configure and trust the profile, or select none"}
	}
	if app == nil || app.BinaryLookup == nil {
		return guidedTool{Name: "MLflow profile " + name, Status: "disabled", Reason: "binary lookup unavailable", Remediation: "install the profile binary or select none"}
	}
	if _, err := app.BinaryLookup(profile.Binary); err != nil {
		return guidedTool{Name: "MLflow profile " + name, Status: "disabled", Reason: "optional binary " + profile.Binary + " is missing", Remediation: "install the named binary; Try execution can continue without observation"}
	}
	return guidedTool{Name: "MLflow profile " + name, Status: "ready", Reason: "optional binary " + profile.Binary + " found; environment values remain unresolved", Available: true}
}

func prepareGuidedTryFinish(command *cobra.Command, app *App, root *rootOptions, options *tryHumanOptions, reference string) (string, error) {
	resolved, authority, _, err := observeWizardAuthority(command, app, root, false, true)
	if err != nil {
		return "", err
	}
	store, err := app.NewTransactionalStore(resolved.Project)
	if err != nil {
		return "", err
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(reference) == "" {
		reference, err = askGuidedTryReference(command.Context(), app, inventory, research.TryOpen)
		if err != nil {
			return "", err
		}
	}
	document, err := inventory.Resolve(reference, research.KindTry)
	if err != nil {
		return "", err
	}
	value := document.Record.(*research.Try)
	if value.State != research.TryOpen {
		return "", fmt.Errorf("Try %s is %s, not open", value.ID, value.State)
	}
	draft := *options
	draft.resultDigests = append([]string{}, options.resultDigests...)
	draft.externalRefs = append([]string{}, options.externalRefs...)
	if strings.TrimSpace(draft.summary) == "" {
		draft.summary, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Conclusion summary", Description: "Human interpretation of the bounded evidence",
		}, nonEmptySingleLine("Conclusion summary"))
		if err != nil {
			return "", err
		}
	}
	available, attempts, closureFingerprint, err := guidedTryClosure(inventory, value.ID, document.Revision)
	if err != nil {
		return "", err
	}
	if !draft.noResults && len(draft.resultDigests) == 0 && len(draft.externalRefs) == 0 {
		mode := "none"
		if len(available) > 0 {
			mode, err = app.askChoice(command.Context(), "Result selection", "Select produced digests or explicitly conclude with none", "none", "none", "digests")
			if err != nil {
				return "", err
			}
		}
		if mode == "none" {
			draft.noResults = true
		} else {
			selected, askErr := app.askValidated(command.Context(), PromptRequest{
				Label: "Result digests", Description: "Comma-separated sha256 digests produced by terminal Attempts",
			}, func(value string) error {
				return validateSelectedTryDigests(parseGuidedList(value), available)
			})
			if askErr != nil {
				return "", askErr
			}
			draft.resultDigests = parseGuidedList(selected)
		}
	}
	if draft.noResults == (len(draft.resultDigests) > 0 || len(draft.externalRefs) > 0) {
		return "", errors.New("select result digests/external refs, or explicitly choose no results")
	}
	if err := validateSelectedTryDigests(draft.resultDigests, available); err != nil {
		return "", err
	}
	if _, err := parseTryExternalRefs(draft.externalRefs); err != nil {
		return "", err
	}

	snapshot := []guidedField{{Label: "Attempts", Value: strconv.Itoa(attempts)}, {Label: "Available result digests", Value: displayGuidedValues(available)}}
	if err := writeGuidedPlan(app, guidedPlan{
		Title: "Review Try conclusion plan",
		Summary: append(projectWizardFields(resolved, authority),
			guidedField{Label: "Try", Value: value.ID.String()}, guidedField{Label: "Try revision", Value: document.Revision},
			guidedField{Label: "Summary", Value: draft.summary}, guidedField{Label: "Selected results", Value: displayGuidedValues(draft.resultDigests)},
			guidedField{Label: "External references", Value: strconv.Itoa(len(draft.externalRefs))}, guidedField{Label: "Explicit no results", Value: strconv.FormatBool(draft.noResults)},
		),
		Effects: []string{"Atomically conclude the exact open Try revision", "Guard every owned Attempt revision", "Refresh generated projections"},
		Paths:   []string{resolved.Project.Root},
		Source:  []guidedField{{Label: "Declared Sources", Value: displayResearchIDs(value.Sources)}}, Snapshot: snapshot,
		Tools: []guidedTool{{Name: "canonical transaction", Status: "ready", Reason: "prepared CAS with recovery envelope", Available: true}},
	}); err != nil {
		return "", err
	}
	if err := app.requireConfirmation(command.Context(), "Conclude this Try", "This is a canonical human conclusion and defaults to no", ""); err != nil {
		return "", err
	}
	if err := revalidateWizardAuthority(command, app, root, authority, false, true); err != nil {
		return "", err
	}
	currentInventory, err := store.Inventory(command.Context())
	if err != nil {
		return "", err
	}
	currentDocument, err := currentInventory.ByID(value.ID)
	if err != nil {
		return "", err
	}
	_, _, currentClosure, err := guidedTryClosure(currentInventory, value.ID, currentDocument.Revision)
	if err != nil {
		return "", err
	}
	if err := requireSameGuidedObservation("Try and Attempt revisions", closureFingerprint, currentClosure); err != nil {
		return "", err
	}
	draft.confirm = true
	draft.expectedRevision = document.Revision
	*options = draft
	return value.ID.String(), nil
}

func prepareGuidedTryAdopt(command *cobra.Command, app *App, root *rootOptions, options *tryHumanOptions, reference string) (string, error) {
	resolved, authority, _, err := observeWizardAuthority(command, app, root, false, true)
	if err != nil {
		return "", err
	}
	store, err := app.NewTransactionalStore(resolved.Project)
	if err != nil {
		return "", err
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(reference) == "" {
		reference, err = askGuidedTryReference(command.Context(), app, inventory, research.TryConcluded)
		if err != nil {
			return "", err
		}
	}
	document, err := inventory.Resolve(reference, research.KindTry)
	if err != nil {
		return "", err
	}
	value := document.Record.(*research.Try)
	if value.State != research.TryConcluded || value.Conclusion == nil {
		return "", fmt.Errorf("Try %s must be concluded before adoption", value.ID)
	}
	draft := *options
	draft.parents = append([]string{}, options.parents...)
	draft.tags = append([]string{}, options.tags...)
	if strings.TrimSpace(draft.title) == "" {
		draft.title, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Idea title", Description: "Formal direction adopted from this Try", Default: value.Title, DefaultDisplay: value.Title,
		}, nonEmptySingleLine("Idea title"))
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(draft.summary) == "" {
		draft.summary, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Idea summary", Description: "Formal mechanism or direction", Default: value.Conclusion.Summary, DefaultDisplay: value.Conclusion.Summary,
		}, nonEmptySingleLine("Idea summary"))
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(draft.proposedBy) == "" {
		draft.proposedBy, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Proposed by", Description: "Explicit human identity; agent, bot, and model identities are refused",
		}, validateGuidedHuman)
		if err != nil {
			return "", err
		}
	}
	fillGuidedAdoptDefaults(&draft)
	customize := guidedTryAdoptCustomized(command, options)
	if !customize {
		customize, err = app.askYesNo(command.Context(), "Customize Idea classification", "Cluster, taxonomy, parents, tags, and Markdown detail have safe defaults", false)
		if err != nil {
			return "", err
		}
	}
	if customize {
		if err := gatherGuidedTryAdoptAdvanced(command.Context(), app, &draft); err != nil {
			return "", err
		}
	}
	if err := validateGuidedAdoptOptions(&draft); err != nil {
		return "", err
	}
	parentRevisions := make(map[string]string, len(draft.parents))
	for _, parent := range draft.parents {
		resolvedParent, resolveErr := currentRevisionRef(inventory, parent, research.KindIdea)
		if resolveErr != nil {
			return "", resolveErr
		}
		parentRevisions[resolvedParent.ID.String()] = resolvedParent.Revision
	}
	planFingerprint := guidedAdoptFingerprint(document, parentRevisions)
	if err := writeGuidedPlan(app, guidedPlan{
		Title: "Review Try adoption plan",
		Summary: append(projectWizardFields(resolved, authority),
			guidedField{Label: "Try", Value: value.ID.String()}, guidedField{Label: "Try revision", Value: document.Revision},
			guidedField{Label: "Idea title", Value: draft.title}, guidedField{Label: "Idea summary", Value: draft.summary},
			guidedField{Label: "Proposed by", Value: draft.proposedBy}, guidedField{Label: "Primary cluster", Value: draft.cluster},
		),
		Effects: []string{"Create one proposed Idea v2", "Atomically mark the exact concluded Try adopted", "Guard exact parent Idea revisions", "Refresh generated projections"},
		Paths:   []string{resolved.Project.Root},
		Source:  []guidedField{{Label: "Origin Try", Value: value.ID.String()}, {Label: "Parents", Value: displayGuidedValues(draft.parents)}},
		Snapshot: []guidedField{
			{Label: "Domain", Value: draft.domain}, {Label: "Work", Value: draft.work}, {Label: "Method", Value: draft.method},
			{Label: "Component", Value: draft.component}, {Label: "Lane", Value: draft.lane}, {Label: "Risk", Value: draft.risk},
			{Label: "Horizon", Value: draft.horizon}, {Label: "Origin", Value: draft.origin},
		},
		Tools: []guidedTool{{Name: "canonical transaction", Status: "ready", Reason: "Try and Idea publish atomically through existing recovery semantics", Available: true}},
	}); err != nil {
		return "", err
	}
	if err := app.requireConfirmation(command.Context(), "Adopt this Try", "Type ADOPT because this creates a formal Idea and irreversibly transitions the Try", "ADOPT"); err != nil {
		return "", err
	}
	if err := revalidateWizardAuthority(command, app, root, authority, false, true); err != nil {
		return "", err
	}
	currentInventory, err := store.Inventory(command.Context())
	if err != nil {
		return "", err
	}
	currentDocument, err := currentInventory.ByID(value.ID)
	if err != nil {
		return "", err
	}
	currentParents := make(map[string]string, len(parentRevisions))
	for id := range parentRevisions {
		parentID, parseErr := research.ParseIDForKind(id, research.KindIdea)
		if parseErr != nil {
			return "", parseErr
		}
		parentDocument, lookupErr := currentInventory.ByID(parentID)
		if lookupErr != nil {
			return "", lookupErr
		}
		currentParents[id] = parentDocument.Revision
	}
	if err := requireSameGuidedObservation("Try or parent Idea revisions", planFingerprint, guidedAdoptFingerprint(currentDocument, currentParents)); err != nil {
		return "", err
	}
	draft.confirm = true
	draft.expectedRevision = document.Revision
	draft.expectedParents = parentRevisions
	*options = draft
	return value.ID.String(), nil
}

func askGuidedTryReference(ctx context.Context, app *App, inventory *record.Inventory, state research.TryState) (string, error) {
	candidates := make([]string, 0)
	if inventory != nil {
		for _, document := range inventory.OfKind(research.KindTry) {
			value := document.Record.(*research.Try)
			if value.State == state {
				candidates = append(candidates, value.ID.String())
			}
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return "", fmt.Errorf("no %s Try is available", state)
	}
	fallback := ""
	if len(candidates) == 1 {
		fallback = candidates[0]
	}
	return app.askValidated(ctx, PromptRequest{
		Label: "Try", Description: "Typed Try ID, prefix, or display code", Default: fallback, DefaultDisplay: fallback,
	}, func(value string) error {
		document, err := inventory.Resolve(strings.TrimSpace(value), research.KindTry)
		if err != nil {
			return err
		}
		if document.Record.(*research.Try).State != state {
			return fmt.Errorf("selected Try is not %s", state)
		}
		return nil
	})
}

func guidedTryClosure(inventory *record.Inventory, tryID research.ID, tryRevision string) ([]string, int, string, error) {
	if inventory == nil {
		return nil, 0, "", errors.New("canonical inventory is unavailable")
	}
	parts := []string{tryID.String(), tryRevision}
	available := make([]string, 0)
	attempts := 0
	for _, document := range inventory.OfKind(research.KindAttempt) {
		value := document.Record.(*research.Attempt)
		if value.Try != tryID {
			continue
		}
		attempts++
		if !guidedAttemptTerminal(value.State) {
			return nil, attempts, "", fmt.Errorf("Try still owns nonterminal Attempt %s (%s)", value.ID, value.State)
		}
		parts = append(parts, value.ID.String(), document.Revision, string(value.State))
		if table := value.Extensions[tryflow.DirectExtensionNamespace]; table != nil {
			digests := extensionStrings(table["result_digests"])
			sort.Strings(digests)
			available = append(available, digests...)
			parts = append(parts, digests...)
		}
	}
	sort.Strings(available)
	available = compactStrings(available)
	return available, attempts, guidedFingerprint(parts...), nil
}

func guidedAttemptTerminal(state research.AttemptState) bool {
	switch state {
	case research.AttemptSucceeded, research.AttemptFailed, research.AttemptTimedOut,
		research.AttemptOutOfMemory, research.AttemptPreempted, research.AttemptCancelled:
		return true
	default:
		return false
	}
}

func validateSelectedTryDigests(selected, available []string) error {
	set := make(map[string]struct{}, len(available))
	for _, value := range available {
		set[value] = struct{}{}
	}
	for _, digest := range selected {
		if !validTryDigest(digest) {
			return fmt.Errorf("invalid result digest %q", digest)
		}
		if _, found := set[digest]; !found {
			return fmt.Errorf("result digest %s was not produced by this Try", digest)
		}
	}
	return nil
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	output := values[:1]
	for _, value := range values[1:] {
		if value != output[len(output)-1] {
			output = append(output, value)
		}
	}
	return output
}

func displayResearchIDs(values []research.ID) string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = value.String()
	}
	return displayGuidedValues(out)
}

func fillGuidedAdoptDefaults(options *tryHumanOptions) {
	if options.cluster == "" {
		options.cluster = "exploratory"
	}
	if options.domain == "" {
		options.domain = "research"
	}
	if options.work == "" {
		options.work = "validation"
	}
	if options.method == "" {
		options.method = "experiment"
	}
	if options.component == "" {
		options.component = "source"
	}
	if options.lane == "" {
		options.lane = string(research.LaneExplore)
	}
	if options.risk == "" {
		options.risk = string(research.RiskLow)
	}
	if options.horizon == "" {
		options.horizon = string(research.HorizonShort)
	}
	if options.origin == "" {
		options.origin = string(research.OriginHuman)
	}
}

func guidedTryAdoptCustomized(command *cobra.Command, options *tryHumanOptions) bool {
	if command != nil {
		for _, name := range []string{"body", "cluster", "domain", "work", "method", "component", "lane", "risk", "horizon", "origin", "parent", "tags"} {
			if command.Flags().Changed(name) {
				return true
			}
		}
	}
	return options != nil && (len(options.parents) > 0 || len(options.tags) > 0 || strings.TrimSpace(options.body) != "")
}

func gatherGuidedTryAdoptAdvanced(ctx context.Context, app *App, options *tryHumanOptions) error {
	fields := []struct {
		label       string
		description string
		value       *string
		validate    func(string) error
	}{
		{"Primary cluster", "Lower-case cluster slug", &options.cluster, validateGuidedSlug("primary cluster")},
		{"Domain", "Lower-case domain slug", &options.domain, validateGuidedSlug("domain")},
		{"Work", "Lower-case work-class slug", &options.work, validateGuidedSlug("work")},
		{"Method", "Lower-case method slug", &options.method, validateGuidedSlug("method")},
		{"Component", "Lower-case component slug", &options.component, validateGuidedSlug("component")},
	}
	for _, field := range fields {
		value, err := app.askValidated(ctx, PromptRequest{Label: field.label, Description: field.description, Default: *field.value, DefaultDisplay: *field.value}, field.validate)
		if err != nil {
			return err
		}
		*field.value = value
	}
	var err error
	options.lane, err = app.askChoice(ctx, "Lane", "Research allocation lane", options.lane, string(research.LaneExplore), string(research.LaneExploit))
	if err != nil {
		return err
	}
	options.risk, err = app.askChoice(ctx, "Risk", "Estimated risk class", options.risk, string(research.RiskLow), string(research.RiskMedium), string(research.RiskHigh))
	if err != nil {
		return err
	}
	options.horizon, err = app.askChoice(ctx, "Horizon", "Expected research horizon", options.horizon, string(research.HorizonShort), string(research.HorizonMedium), string(research.HorizonLong))
	if err != nil {
		return err
	}
	options.origin, err = app.askChoice(ctx, "Origin", "Idea authorship class", options.origin, string(research.OriginHuman), string(research.OriginHybrid))
	if err != nil {
		return err
	}
	parentText, err := app.askValidated(ctx, PromptRequest{
		Label: "Parent Ideas", Description: "Optional comma-separated Idea references", Default: strings.Join(options.parents, ","), DefaultDisplay: displayListFallback(options.parents),
	}, optionalSingleLine("Parent Ideas"))
	if err != nil {
		return err
	}
	options.parents = parseGuidedList(parentText)
	tagText, err := app.askValidated(ctx, PromptRequest{
		Label: "Idea tags", Description: "Optional comma-separated canonical tags", Default: strings.Join(options.tags, ","), DefaultDisplay: displayListFallback(options.tags),
	}, optionalSingleLine("Idea tags"))
	if err != nil {
		return err
	}
	options.tags = parseGuidedList(tagText)
	options.body, err = app.askValidated(ctx, PromptRequest{
		Label: "Idea detail", Description: "Optional single-line Markdown detail", Default: options.body, DefaultDisplay: firstNonEmpty(options.body, "none"),
	}, optionalSingleLine("Idea detail"))
	return err
}

func validateGuidedAdoptOptions(options *tryHumanOptions) error {
	if options == nil || strings.TrimSpace(options.title) == "" || strings.TrimSpace(options.summary) == "" {
		return errors.New("Idea title and summary are required")
	}
	if err := validateGuidedHuman(options.proposedBy); err != nil {
		return err
	}
	for label, value := range map[string]string{
		"primary cluster": options.cluster, "domain": options.domain, "work": options.work, "method": options.method, "component": options.component,
	} {
		if err := validateGuidedSlug(label)(value); err != nil {
			return err
		}
	}
	if options.lane != string(research.LaneExplore) && options.lane != string(research.LaneExploit) {
		return errors.New("lane must be explore or exploit")
	}
	if options.risk != string(research.RiskLow) && options.risk != string(research.RiskMedium) && options.risk != string(research.RiskHigh) {
		return errors.New("risk must be low, medium, or high")
	}
	if options.horizon != string(research.HorizonShort) && options.horizon != string(research.HorizonMedium) && options.horizon != string(research.HorizonLong) {
		return errors.New("horizon must be short, medium, or long")
	}
	if options.origin != string(research.OriginHuman) && options.origin != string(research.OriginHybrid) {
		return errors.New("guided adoption origin must be human or hybrid")
	}
	return nil
}

func validateGuidedHuman(value string) error {
	if err := nonEmptySingleLine("human identity")(value); err != nil {
		return err
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "agent:") || strings.HasPrefix(lower, "bot:") || strings.HasPrefix(lower, "model:") {
		return errors.New("proposed-by must identify a human, not an agent, bot, or model")
	}
	return nil
}

func validateGuidedSlug(label string) func(string) error {
	return func(value string) error {
		if value == "" || len(value) > 64 || value != strings.ToLower(value) || value[0] < 'a' || value[0] > 'z' {
			return fmt.Errorf("%s must be a lower-case slug", label)
		}
		for _, character := range value {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
				return fmt.Errorf("%s must be a lower-case slug", label)
			}
		}
		return nil
	}
}

func guidedAdoptFingerprint(document *record.Document, parents map[string]string) string {
	parts := []string{}
	if document != nil {
		id, _ := document.ID()
		parts = append(parts, id.String(), document.Revision)
	}
	keys := make([]string, 0, len(parents))
	for id := range parents {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		parts = append(parts, id, parents[id])
	}
	return guidedFingerprint(parts...)
}

func parseGuidedArgv(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("command is required")
	}
	arguments := make([]string, 0)
	var current strings.Builder
	quote := rune(0)
	escaped := false
	started := false
	flush := func() error {
		if !started || current.Len() == 0 {
			return errors.New("command argv cannot contain an empty argument")
		}
		arguments = append(arguments, current.String())
		current.Reset()
		started = false
		return nil
	}
	for _, character := range input {
		if character == utf8.RuneError || character == 0 || character == '\r' || character == '\n' || unicode.IsControl(character) {
			return nil, errors.New("command contains invalid control text")
		}
		if escaped {
			current.WriteRune(character)
			started = true
			escaped = false
			continue
		}
		if character == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			} else {
				current.WriteRune(character)
			}
			started = true
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			started = true
			continue
		}
		if unicode.IsSpace(character) {
			if started {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			continue
		}
		current.WriteRune(character)
		started = true
	}
	if escaped || quote != 0 {
		return nil, errors.New("command has an unterminated escape or quote")
	}
	if started {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	if len(arguments) == 0 {
		return nil, errors.New("command is required")
	}
	return arguments, nil
}
