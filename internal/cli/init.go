package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/spf13/cobra"
)

type initOptions struct {
	name             string
	dedicatedRepo    string
	create           bool
	expectedTarget   string
	sourceRepo       string
	sourceKey        string
	sourceTitle      string
	sourceSubdir     string
	sourceLocators   []string
	sourceTags       []string
	confirm          bool
	confirmLocalOnly bool
	json             bool
}

func newInitCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &initOptions{sourceSubdir: "."}
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize an embedded or dedicated experiments Project",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runInit(command, app, rootOptions, options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.name, "name", "", "set the project name (defaults to the Git repository name)")
	flags.StringVar(&options.dedicatedRepo, "dedicated-repo", "", "initialize or adopt an exact dedicated Git repository root")
	flags.BoolVar(&options.create, "create", false, "create or git-init only a missing or empty dedicated target (requires confirmation)")
	flags.StringVar(&options.sourceRepo, "source-repo", "", "bind this existing Source Git clone in dedicated mode")
	flags.StringVar(&options.sourceKey, "source-key", "", "set the initial Source key in dedicated mode")
	flags.StringVar(&options.sourceTitle, "source-title", "", "set the initial Source title")
	flags.StringVar(&options.sourceSubdir, "source-subdir", ".", "bind a Git-root-relative Source subdirectory")
	flags.StringSliceVar(&options.sourceLocators, "source-locator", nil, "add a sanitized Source locator hint (repeatable)")
	flags.StringSliceVar(&options.sourceTags, "source-tag", nil, "add an initial Source tag (repeatable)")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm the dedicated initialization plan without prompting")
	flags.BoolVar(&options.confirmLocalOnly, "confirm-local-only", false, "confirm an initial Source with no sanitized remote locator")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runInit(command *cobra.Command, app *App, rootOptions *rootOptions, options *initOptions) error {
	if strings.TrimSpace(rootOptions.workspace) != "" || strings.TrimSpace(rootOptions.source) != "" {
		mode := "embedded"
		if dedicatedInitRequested(command, options) {
			mode = "dedicated"
		}
		return commandFailure(app, options.json, "init", initData{SchemaVersion: "exp.command.init/v1", Mode: mode, Projections: emptyProjectionResult()}, false, nil, errors.New("init does not accept --workspace or --source selectors; use --dedicated-repo and --source-repo for dedicated initialization"))
	}
	if app.interactive(options.json) && !fullyExplicitInit(command, options) {
		if err := prepareGuidedInit(command, app, rootOptions, options); err != nil {
			mode := "embedded"
			if dedicatedInitRequested(command, options) {
				mode = "dedicated"
			}
			return commandFailure(app, options.json, "init", initData{SchemaVersion: "exp.command.init/v1", Mode: mode, Projections: emptyProjectionResult()}, false, nil, err)
		}
	}
	if dedicatedInitRequested(command, options) {
		return runDedicatedInit(command, app, rootOptions, options)
	}
	start, err := app.startDir(rootOptions.startDir)
	if err != nil {
		return commandFailure(app, options.json, "init", initData{SchemaVersion: "exp.command.init/v1", Mode: "embedded", Projections: emptyProjectionResult()}, false, nil, err)
	}
	info, created, err := app.InitializeProject(command.Context(), project.InitRequest{StartDir: start, Name: options.name})
	if err != nil {
		if info != nil && publicationWasPublished(err) {
			projectData, viewErr := makeProjectView(info)
			if viewErr != nil {
				return commandFailure(app, options.json, "init", initData{SchemaVersion: "exp.command.init/v1", Mode: "embedded", Projections: emptyProjectionResult()}, true, durabilityUncertainDiagnostics("canonical", record.ProjectFile), fmt.Errorf("project was published but durability is uncertain: %w", err))
			}
			data := initData{SchemaVersion: "exp.command.init/v1", Mode: "embedded", Project: projectData, Created: created, Projections: emptyProjectionResult()}
			return commandFailure(app, options.json, "init", data, true, durabilityUncertainDiagnostics("canonical", record.ProjectFile), fmt.Errorf("project %s was published but durability is uncertain: %w", projectData.ID, err))
		}
		return commandFailure(app, options.json, "init", initData{SchemaVersion: "exp.command.init/v1", Mode: "embedded", Projections: emptyProjectionResult()}, false, nil, err)
	}
	projectData, err := makeProjectView(info)
	if err != nil {
		return commandFailure(app, options.json, "init", initData{SchemaVersion: "exp.command.init/v1", Mode: "embedded", Created: created, Projections: emptyProjectionResult()}, created, nil, err)
	}
	store, err := app.NewStore(info)
	if err != nil {
		return commandFailure(app, options.json, "init", initData{SchemaVersion: "exp.command.init/v1", Mode: "embedded", Project: projectData, Created: created, Projections: emptyProjectionResult()}, created, nil, err)
	}
	_, rendered, err := renderFreshProjections(command.Context(), app, info, store)
	data := initData{SchemaVersion: "exp.command.init/v1", Mode: "embedded", Project: projectData, Created: created, Projections: rendered}
	if err != nil {
		var diagnostics []Diagnostic
		if publicationWasPublished(err) && len(rendered.Written) > 0 {
			diagnostics = durabilityUncertainDiagnostics("generated projection", rendered.Written[len(rendered.Written)-1])
		}
		return commandFailure(app, options.json, "init", data, created || len(rendered.Written) > 0, diagnostics, fmt.Errorf("initialize canonical project but refresh projections: %w", err))
	}
	human := fmt.Sprintf("Project %q (%s) at %s; created=%t\n", projectData.Name, projectData.ID, projectData.Root, created)
	return commandSuccess(app, options.json, "init", data, false, nil, human)
}

func dedicatedInitRequested(command *cobra.Command, options *initOptions) bool {
	if options == nil {
		return false
	}
	subdirSelected := command != nil && command.Flags().Changed("source-subdir")
	return strings.TrimSpace(options.dedicatedRepo) != "" || options.create || strings.TrimSpace(options.sourceRepo) != "" ||
		strings.TrimSpace(options.sourceKey) != "" || strings.TrimSpace(options.sourceTitle) != "" || subdirSelected ||
		len(options.sourceLocators) > 0 || len(options.sourceTags) > 0 || options.confirm || options.confirmLocalOnly
}

func fullyExplicitInit(command *cobra.Command, options *initOptions) bool {
	if command == nil || options == nil {
		return false
	}
	if dedicatedInitRequested(command, options) {
		return strings.TrimSpace(options.dedicatedRepo) != "" && strings.TrimSpace(options.sourceRepo) != "" &&
			strings.TrimSpace(options.sourceKey) != "" && options.confirm
	}
	// --name is the complete legacy embedded fast path. A bare TTY invocation is
	// reserved for the dedicated-by-default guided flow; non-TTY stays compatible.
	return command.Flags().Changed("name")
}

func runDedicatedInit(command *cobra.Command, app *App, rootOptions *rootOptions, options *initOptions) error {
	data := initData{SchemaVersion: "exp.command.init/v1", Mode: "dedicated", Projections: emptyProjectionResult()}
	if strings.TrimSpace(options.dedicatedRepo) == "" || strings.TrimSpace(options.sourceRepo) == "" || strings.TrimSpace(options.sourceKey) == "" || !options.confirm {
		return commandFailure(app, options.json, "init", data, false, nil, invalidUsagef("dedicated init requires --dedicated-repo, --source-repo, --source-key, and --confirm in non-interactive or JSON mode"))
	}
	start, err := app.startDir(rootOptions.startDir)
	if err != nil {
		return commandFailure(app, options.json, "init", data, false, nil, err)
	}
	if options.name != "" {
		if nameErr := nonEmptySingleLine("project name")(options.name); nameErr != nil {
			return commandFailure(app, options.json, "init", data, false, nil, invalidUsagef("%s", nameErr))
		}
	}
	sourceInput := inputPath(start, options.sourceRepo)
	observation, err := workspace.InspectSourceClone(command.Context(), sourceInput, options.sourceSubdir, app.GitRunner)
	if err != nil {
		return commandFailure(app, options.json, "init", data, false, nil, err)
	}
	plan, err := sourcepkg.BuildAddPlan(sourcepkg.AddPlanRequest{
		Request: sourcepkg.AddRequest{
			Key: options.sourceKey, Title: options.sourceTitle, Subdir: options.sourceSubdir,
			LocatorHints: options.sourceLocators, Tags: options.sourceTags,
		},
		CloneRoot: observation.Repository.Root, CloneGitCommonDir: observation.Repository.GitCommonDir,
		ObservedLocatorHints: observation.LocatorHints, ConfirmLocalOnly: options.confirmLocalOnly,
	})
	if err != nil {
		return commandFailure(app, options.json, "init", data, false, nil, err)
	}
	targetBefore, err := inspectDedicatedTarget(command.Context(), app, start, options.dedicatedRepo)
	if err != nil {
		return commandFailure(app, options.json, "init", data, false, nil, err)
	}
	targetRepository, err := ensureDedicatedRepository(command.Context(), app, start, options.dedicatedRepo, options.create, options.expectedTarget)
	if err != nil {
		partial := false
		var diagnostics []Diagnostic
		var creation *dedicatedCreationError
		if errors.As(err, &creation) && creation.CleanupErr != nil {
			partial = true
			diagnostics = []Diagnostic{{
				Severity: SeverityError, Code: "init.repository_cleanup_required",
				Message: "dedicated Git initialization failed and safe cleanup could not be completed; inspect the target before retrying",
				Path:    options.dedicatedRepo,
			}}
		}
		return commandFailure(app, options.json, "init", data, partial, diagnostics, err)
	}
	repositoryInitialized := targetBefore.State != dedicatedTargetGit
	if targetRepository.GitCommonDir == observation.Repository.GitCommonDir {
		return commandFailure(app, options.json, "init", data, false, nil, errors.New("dedicated and Source repositories must have different Git-common identities"))
	}
	inspected := inspectedSourcePlan{plan: plan, resolvedRoot: observation.ResolvedRoot}
	planView := dedicatedInitPlanView{
		CanonicalRepository: safex.NewRedactor().Path(targetRepository.Root),
		Source:              makeSourceAddPlanView(inspected),
		Effects: []string{
			"initialize_canonical_project", "register_workspace_association", "publish_canonical_source",
			"register_source_association", "refresh_projections",
		},
	}
	if repositoryInitialized {
		planView.Effects = append([]string{"initialize_dedicated_git_repository"}, planView.Effects...)
	}
	data.Plan = &planView

	info, discoverErr := app.DiscoverProject(command.Context(), targetRepository.Root)
	projectCreated := false
	if errors.Is(discoverErr, project.ErrNotInitialized) {
		info, projectCreated, err = app.InitializeProject(command.Context(), project.InitRequest{StartDir: targetRepository.Root, Name: options.name})
	} else {
		err = discoverErr
	}
	data.Created = projectCreated
	if info != nil {
		if projectData, viewErr := makeProjectView(info); viewErr == nil {
			data.Project = projectData
		}
	}
	if err != nil {
		published := info != nil && publicationWasPublished(err)
		diagnostics := dedicatedRepositoryRetainedDiagnostics(repositoryInitialized)
		if published {
			diagnostics = append(diagnostics, durabilityUncertainDiagnostics("canonical", record.ProjectFile)...)
		} else {
			diagnostics = append(diagnostics, diagnosticsForError(err)...)
		}
		return commandFailure(app, options.json, "init", data, repositoryInitialized || published, diagnostics, err)
	}
	projectData, err := makeProjectView(info)
	if err != nil {
		diagnostics := append(dedicatedRepositoryRetainedDiagnostics(repositoryInitialized), diagnosticsForError(err)...)
		return commandFailure(app, options.json, "init", data, repositoryInitialized || projectCreated, diagnostics, err)
	}
	data.Project = projectData
	if _, err = app.Associations.RegisterProject(command.Context(), info); err != nil {
		return commandFailure(app, options.json, "init", data, true, []Diagnostic{{Severity: SeverityError, Code: "workspace.association_failed", Message: safeDiagnosticText(err.Error())}}, fmt.Errorf("dedicated Project was initialized but workspace association failed: %w", err))
	}
	data.WorkspaceRegistered = true
	store, err := app.NewTransactionalStore(info)
	if err != nil {
		return commandFailure(app, options.json, "init", data, true, nil, err)
	}
	service := app.NewSourceService(store)
	if service == nil {
		return commandFailure(app, options.json, "init", data, true, nil, errors.New("Source service factory returned nil"))
	}
	document, lookupErr := service.Lookup(command.Context(), plan.Request.Key)
	switch {
	case lookupErr == nil:
		if !sourceMatchesDedicatedPlan(document, plan) {
			return commandFailure(app, options.json, "init", data, true, nil, errors.New("existing Source key does not match the dedicated initialization plan"))
		}
	case errors.Is(lookupErr, research.ErrReferenceNotFound):
		document, err = service.ApplyAddPlan(command.Context(), plan)
		if err != nil {
			data.Transaction, _ = sourcepkg.TransactionResultFromError(err)
			data.SourceCreated = transactionHasPublishedPath(data.Transaction)
			if view, ok := transactionSourceView(info, document); ok {
				data.Source = &view
			}
			return commandFailure(app, options.json, "init", data, true, transactionFailureDiagnostics(data.Transaction), fmt.Errorf("dedicated Project was initialized but Source publication failed: %w", err))
		}
		data.SourceCreated = true
	default:
		return commandFailure(app, options.json, "init", data, true, nil, lookupErr)
	}
	sourceData, err := sourceViewForDocument(command.Context(), info, store, document)
	if err != nil {
		return commandFailure(app, options.json, "init", data, true, nil, err)
	}
	data.Source = &sourceData
	association, err := app.Associations.RegisterSource(command.Context(), info.Project().ProjectID, document.Record.(*research.Source).ID, plan.CloneRoot)
	if err != nil {
		return commandFailure(app, options.json, "init", data, true, sourceAssociationFailureDiagnostics(err), fmt.Errorf("dedicated Project and Source were published but local Source association failed: %w", err))
	}
	associationData := makeSourceAssociationView(association)
	data.SourceAssociated = true
	data.Association = &associationData
	_, rendered, renderErr := renderFreshProjections(command.Context(), app, info, store)
	data.Projections = rendered
	if renderErr != nil {
		return commandFailure(app, options.json, "init", data, true, nil, fmt.Errorf("dedicated Project and Source were published but projection refresh failed: %w", renderErr))
	}
	human := fmt.Sprintf("Dedicated project %q (%s) at %s; created=%t; Source %s associated at %s.\n", projectData.Name, projectData.ID, projectData.RepositoryRoot, projectCreated, sourceData.ID, associationData.Root)
	return commandSuccess(app, options.json, "init", data, false, nil, human)
}

func dedicatedRepositoryRetainedDiagnostics(initialized bool) []Diagnostic {
	if !initialized {
		return nil
	}
	return []Diagnostic{{
		Severity: SeverityWarning,
		Code:     "init.repository_initialized",
		Message:  "the confirmed dedicated Git repository was initialized and retained; rerun the same idempotent init command after correcting the failure",
	}}
}

func sourceMatchesDedicatedPlan(document *record.Document, plan sourcepkg.AddPlan) bool {
	if document == nil {
		return false
	}
	value, ok := document.Record.(*research.Source)
	if !ok || value.State != research.SourceActive {
		return false
	}
	return value.Key == plan.Request.Key && value.Title == plan.Request.Title && value.Subdir == plan.Request.Subdir &&
		len(value.LocatorHints) >= len(plan.Request.LocatorHints) && slices.Equal(value.LocatorHints[:len(plan.Request.LocatorHints)], plan.Request.LocatorHints) &&
		slices.Equal(value.Tags, plan.Request.Tags) && document.Body == plan.Request.Body
}

func emptyProjectionResult() projection.Result {
	return stableProjectionResult(projection.Result{})
}

func stableProjectionResult(result projection.Result) projection.Result {
	if result.Written == nil {
		result.Written = []string{}
	}
	if result.Unchanged == nil {
		result.Unchanged = []string{}
	}
	if result.Drifted == nil {
		result.Drifted = []string{}
	}
	if result.Files == nil {
		result.Files = []projection.FileResult{}
	}
	return result
}
