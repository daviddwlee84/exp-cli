package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
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

const (
	sourceAddDataSchema           = "exp.command.source-add/v1"
	sourceListDataSchema          = "exp.command.source-list/v1"
	sourceShowDataSchema          = "exp.command.source-show/v1"
	sourceStatusDataSchema        = "exp.command.source-status/v1"
	sourceRegisterDataSchema      = "exp.command.source-register/v1"
	sourceAppendLocatorDataSchema = "exp.command.source-append-locator/v1"
	sourceRetireDataSchema        = "exp.command.source-retire/v1"
)

type sourceAddOptions struct {
	key              string
	title            string
	repo             string
	subdir           string
	locators         []string
	tags             []string
	confirm          bool
	confirmLocalOnly bool
	json             bool
}

type sourceLookupOptions struct {
	json bool
}

type sourceRegisterOptions struct {
	repo string
	json bool
}

type sourceAppendLocatorOptions struct {
	locator          string
	expectedRevision string
	confirm          bool
	json             bool
}

type sourceRetireOptions struct {
	expectedRevision string
	confirm          bool
	json             bool
}

type sourceAddPlanView struct {
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Subdir       string   `json:"subdir"`
	LocatorHints []string `json:"locator_hints"`
	CloneRoot    string   `json:"clone_root"`
	ResolvedRoot string   `json:"resolved_root"`
	LocalOnly    bool     `json:"local_only"`
	Effects      []string `json:"effects"`
}

type sourceAssociationView struct {
	ProjectID  string   `json:"project_id"`
	SourceID   string   `json:"source_id"`
	Root       string   `json:"root"`
	Locators   []string `json:"matched_locator_hints"`
	ObservedAt string   `json:"observed_at"`
}

type sourceAddData struct {
	SchemaVersion       string                    `json:"schema_version"`
	Project             projectView               `json:"project"`
	Plan                sourceAddPlanView         `json:"plan"`
	Source              sourceView                `json:"source"`
	CanonicalPublished  bool                      `json:"canonical_published"`
	WorkspaceRegistered bool                      `json:"workspace_registered"`
	Associated          bool                      `json:"associated"`
	Association         *sourceAssociationView    `json:"association,omitempty"`
	Transaction         *record.TransactionResult `json:"transaction,omitempty"`
	Projections         projection.Result         `json:"projections"`
}

type sourceListData struct {
	SchemaVersion string       `json:"schema_version"`
	Project       projectView  `json:"project"`
	Sources       []sourceView `json:"sources"`
}

type sourceShowData struct {
	SchemaVersion string      `json:"schema_version"`
	Project       projectView `json:"project"`
	Source        sourceView  `json:"source"`
}

type sourceStatusEntry struct {
	Source     sourceView `json:"source"`
	Registered bool       `json:"registered"`
	Resolvable bool       `json:"resolvable"`
	Status     string     `json:"status"`
	Root       string     `json:"root,omitempty"`
	Diagnostic string     `json:"diagnostic,omitempty"`
}

type sourceStatusData struct {
	SchemaVersion string              `json:"schema_version"`
	Project       projectView         `json:"project"`
	Sources       []sourceStatusEntry `json:"sources"`
}

type sourceRegisterData struct {
	SchemaVersion string                `json:"schema_version"`
	Project       projectView           `json:"project"`
	Source        sourceView            `json:"source"`
	Association   sourceAssociationView `json:"association"`
}

type sourceAppendLocatorData struct {
	SchemaVersion string                    `json:"schema_version"`
	Project       projectView               `json:"project"`
	Source        sourceView                `json:"source"`
	Transaction   *record.TransactionResult `json:"transaction,omitempty"`
	Projections   projection.Result         `json:"projections"`
}

type sourceRetireData struct {
	SchemaVersion string                    `json:"schema_version"`
	Project       projectView               `json:"project"`
	Source        sourceView                `json:"source"`
	Transaction   *record.TransactionResult `json:"transaction,omitempty"`
	Projections   projection.Result         `json:"projections"`
}

type inspectedSourcePlan struct {
	plan         sourcepkg.AddPlan
	resolvedRoot string
}

func newSourceCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "source", Short: "Manage canonical Git Source bindings and local clone associations", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(
		newSourceAddCommand(app, root),
		newSourceListCommand(app, root),
		newSourceShowCommand(app, root),
		newSourceStatusCommand(app, root),
		newSourceRegisterCommand(app, root),
		newSourceAppendLocatorCommand(app, root),
		newSourceRetireCommand(app, root),
	)
	return command
}

func newSourceAddCommand(app *App, root *rootOptions) *cobra.Command {
	options := &sourceAddOptions{subdir: "."}
	command := &cobra.Command{
		Use: "add", Short: "Review, publish, and associate a Git Source", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runSourceAdd(command, app, root, options) },
	}
	flags := command.Flags()
	flags.StringVar(&options.key, "key", "", "set the project-unique Source key")
	flags.StringVar(&options.title, "title", "", "set the Source title (defaults to the normalized key)")
	flags.StringVar(&options.repo, "repo", "", "inspect and associate this existing Git clone")
	flags.StringVar(&options.subdir, "subdir", ".", "bind a Git-root-relative POSIX subdirectory")
	flags.StringSliceVar(&options.locators, "locator", nil, "add a sanitized remote locator hint (repeatable)")
	flags.StringSliceVar(&options.tags, "tag", nil, "add a canonical Source tag (repeatable)")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm the displayed canonical and local effects")
	flags.BoolVar(&options.confirmLocalOnly, "confirm-local-only", false, "confirm a Source clone with no sanitized remote locator")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newSourceListCommand(app *App, root *rootOptions) *cobra.Command {
	options := &sourceLookupOptions{}
	command := &cobra.Command{
		Use: "list", Short: "List canonical Sources without inspecting providers", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runSourceList(command, app, root, options) },
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newSourceShowCommand(app *App, root *rootOptions) *cobra.Command {
	options := &sourceLookupOptions{}
	command := &cobra.Command{
		Use: "show [source]", Short: "Show one canonical Source", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runSourceShow(command, app, root, options, args)
		},
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newSourceStatusCommand(app *App, root *rootOptions) *cobra.Command {
	options := &sourceLookupOptions{}
	command := &cobra.Command{
		Use: "status [source]", Short: "Validate local associations for canonical Sources", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runSourceStatus(command, app, root, options, args)
		},
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newSourceRegisterCommand(app *App, root *rootOptions) *cobra.Command {
	options := &sourceRegisterOptions{}
	command := &cobra.Command{
		Use: "register [source]", Short: "Review, register, or repair a Source clone association", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runSourceRegister(command, app, root, options, args)
		},
	}
	command.Flags().StringVar(&options.repo, "repo", "", "inspect and register this existing Git clone")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newSourceAppendLocatorCommand(app *App, root *rootOptions) *cobra.Command {
	options := &sourceAppendLocatorOptions{}
	command := &cobra.Command{
		Use: "append-locator [source]", Short: "Append one Source locator through exact-revision CAS", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runSourceAppendLocator(command, app, root, options, args)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.locator, "locator", "", "append this identity-preserving remote Git locator")
	flags.StringVar(&options.expectedRevision, "expected-revision", "", "require the exact canonical Source revision")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm the exact Source locator append without prompting")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newSourceRetireCommand(app *App, root *rootOptions) *cobra.Command {
	options := &sourceRetireOptions{}
	command := &cobra.Command{
		Use: "retire [source]", Short: "Retire an active Source through exact-revision CAS", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runSourceRetire(command, app, root, options, args)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.expectedRevision, "expected-revision", "", "require the exact canonical Source revision")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm retirement of the exact Source revision")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runSourceAdd(command *cobra.Command, app *App, root *rootOptions, options *sourceAddOptions) error {
	empty := sourceAddData{SchemaVersion: sourceAddDataSchema, Projections: emptyProjectionResult()}
	if strings.TrimSpace(options.key) == "" || strings.TrimSpace(options.repo) == "" || !options.confirm {
		if !app.interactive(options.json) {
			return commandFailure(app, options.json, "source add", empty, false, nil, invalidUsagef("Source add requires --key, --repo, and --confirm in non-interactive or JSON mode"))
		}
		if err := prepareGuidedSourceAdd(command, app, root, options); err != nil {
			return commandFailure(app, options.json, "source add", empty, false, nil, err)
		}
	}
	resolved, err := resolveSourceManagementContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "source add", sourceAddData{SchemaVersion: sourceAddDataSchema, Projections: emptyProjectionResult()}, false, nil, err)
	}
	inspected, err := inspectSourceAddPlan(command, app, resolved, sourcepkg.AddRequest{
		Key: options.key, Title: options.title, Subdir: options.subdir,
		LocatorHints: options.locators, Tags: options.tags,
	}, options.repo, options.confirmLocalOnly)
	if err != nil {
		return commandFailure(app, options.json, "source add", sourceAddData{SchemaVersion: sourceAddDataSchema, Projections: emptyProjectionResult()}, false, nil, err)
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "source add", sourceAddData{SchemaVersion: sourceAddDataSchema, Projections: emptyProjectionResult()}, false, nil, err)
	}
	planData := makeSourceAddPlanView(inspected)
	data := sourceAddData{
		SchemaVersion: sourceAddDataSchema, Project: projectData, Plan: planData, Projections: emptyProjectionResult(),
	}
	store, err := app.NewTransactionalStore(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "source add", data, false, nil, err)
	}
	service := app.NewSourceService(store)
	created, err := service.ApplyAddPlan(command.Context(), inspected.plan)
	if err != nil {
		if view, ok := transactionSourceView(resolved.Project, created); ok {
			data.Source = view
		}
		data.Transaction, _ = sourcepkg.TransactionResultFromError(err)
		data.CanonicalPublished = transactionHasPublishedPath(data.Transaction)
		return commandFailure(app, options.json, "source add", data, data.Transaction != nil || publicationWasPublished(err), transactionFailureDiagnostics(data.Transaction), err)
	}
	data.CanonicalPublished = true
	data.Source, err = sourceViewForDocument(command.Context(), resolved.Project, store, created)
	if err != nil {
		return commandFailure(app, options.json, "source add", data, true, nil, fmt.Errorf("Source was published but its result view failed: %w", err))
	}
	if _, err = app.Associations.RegisterProject(command.Context(), resolved.Project); err != nil {
		return commandFailure(app, options.json, "source add", data, true, sourceAssociationFailureDiagnostics(err), fmt.Errorf("Source %s was published but workspace association failed: %w", data.Source.ID, err))
	}
	data.WorkspaceRegistered = true
	association, err := app.Associations.RegisterSource(command.Context(), resolved.ProjectID(), created.Record.(*research.Source).ID, inspected.plan.CloneRoot)
	if err != nil {
		return commandFailure(app, options.json, "source add", data, true, sourceAssociationFailureDiagnostics(err), fmt.Errorf("Source %s was published but local clone association failed: %w", data.Source.ID, err))
	}
	associationData := makeSourceAssociationView(association)
	data.Associated = true
	data.Association = &associationData
	_, rendered, renderErr := renderFreshProjections(command.Context(), app, resolved.Project, store)
	data.Projections = rendered
	if renderErr != nil {
		return commandFailure(app, options.json, "source add", data, true, nil, fmt.Errorf("Source %s was published and associated but projection refresh failed: %w", data.Source.ID, renderErr))
	}
	human := fmt.Sprintf("Published %s %q at %s and associated clone %s.\n", data.Source.Display, data.Source.Title, data.Source.Path, data.Association.Root)
	return commandSuccess(app, options.json, "source add", data, false, nil, human)
}

func runSourceList(command *cobra.Command, app *App, root *rootOptions, options *sourceLookupOptions) error {
	resolved, _, service, err := openSourceService(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "source list", sourceListData{SchemaVersion: sourceListDataSchema, Sources: []sourceView{}}, false, nil, err)
	}
	documents, err := service.List(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "source list", sourceListData{SchemaVersion: sourceListDataSchema, Sources: []sourceView{}}, false, nil, err)
	}
	views, err := makeSourceViews(resolved.Project, documents)
	if err != nil {
		return commandFailure(app, options.json, "source list", sourceListData{SchemaVersion: sourceListDataSchema, Sources: []sourceView{}}, false, nil, err)
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "source list", sourceListData{SchemaVersion: sourceListDataSchema, Sources: []sourceView{}}, false, nil, err)
	}
	data := sourceListData{SchemaVersion: sourceListDataSchema, Project: projectData, Sources: views}
	return commandSuccess(app, options.json, "source list", data, false, nil, renderSourceListHuman(views))
}

func runSourceShow(command *cobra.Command, app *App, root *rootOptions, options *sourceLookupOptions, args []string) error {
	resolved, store, service, err := openSourceService(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "source show", sourceShowData{SchemaVersion: sourceShowDataSchema}, false, nil, err)
	}
	reference, err := sourceReference(args, root, resolved)
	if err != nil {
		return commandFailure(app, options.json, "source show", sourceShowData{SchemaVersion: sourceShowDataSchema}, false, nil, err)
	}
	document, err := service.Lookup(command.Context(), reference)
	if err != nil {
		return commandFailure(app, options.json, "source show", sourceShowData{SchemaVersion: sourceShowDataSchema}, false, nil, err)
	}
	view, err := sourceViewForDocument(command.Context(), resolved.Project, store, document)
	if err != nil {
		return commandFailure(app, options.json, "source show", sourceShowData{SchemaVersion: sourceShowDataSchema}, false, nil, err)
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "source show", sourceShowData{SchemaVersion: sourceShowDataSchema}, false, nil, err)
	}
	data := sourceShowData{SchemaVersion: sourceShowDataSchema, Project: projectData, Source: view}
	human := fmt.Sprintf("%s %s\nTitle: %s\nState: %s\nSubdir: %s\nPath: %s\nRevision: %s\n", view.Display, view.ID, view.Title, view.State, view.Subdir, view.Path, view.Revision)
	return commandSuccess(app, options.json, "source show", data, false, nil, human)
}

func runSourceStatus(command *cobra.Command, app *App, root *rootOptions, options *sourceLookupOptions, args []string) error {
	resolved, _, service, err := openSourceService(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: []sourceStatusEntry{}}, false, nil, err)
	}
	documents, err := service.List(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: []sourceStatusEntry{}}, false, nil, err)
	}
	views, err := makeSourceViews(resolved.Project, documents)
	if err != nil {
		return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: []sourceStatusEntry{}}, false, nil, err)
	}
	if len(args) > 0 || strings.TrimSpace(root.source) != "" {
		reference, referenceErr := sourceReference(args, root, resolved)
		if referenceErr != nil {
			return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: []sourceStatusEntry{}}, false, nil, referenceErr)
		}
		document, lookupErr := service.Lookup(command.Context(), reference)
		if lookupErr != nil {
			return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: []sourceStatusEntry{}}, false, nil, lookupErr)
		}
		selectedID, _ := document.ID()
		selectedViews := make([]sourceView, 0, 1)
		for _, view := range views {
			if view.ID == selectedID.String() {
				selectedViews = append(selectedViews, view)
				break
			}
		}
		if len(selectedViews) != 1 {
			return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: []sourceStatusEntry{}}, false, nil, errors.New("resolved Source is absent from the canonical Source view"))
		}
		views = selectedViews
	}
	entries := make([]sourceStatusEntry, 0, len(views))
	for _, view := range views {
		entry := sourceStatusEntry{Source: view, Status: "unregistered"}
		association, lookupErr := app.Associations.LookupSource(command.Context(), resolved.ProjectID(), mustParseSourceID(view.ID))
		if lookupErr == nil {
			entry.Registered = true
			entry.Root = safex.NewRedactor().Path(association.Root)
			if view.State == string(research.SourceRetired) {
				entry.Status = "retired"
			} else {
				invocation := filepath.Join(association.Root, filepath.FromSlash(view.Subdir))
				_, resolveErr := app.ResolveWorkspace.Resolve(command.Context(), workspace.ResolveRequest{
					InvocationDir: invocation, Workspace: resolved.ProjectID().String(), Source: view.ID,
				})
				if resolveErr == nil {
					entry.Resolvable = true
					entry.Status = "ready"
				} else if errors.Is(resolveErr, workspace.ErrStaleAssociation) || errors.Is(resolveErr, workspace.ErrLocatorMismatch) || errors.Is(resolveErr, fs.ErrNotExist) {
					entry.Status = "stale"
					entry.Diagnostic = safeDiagnosticText(resolveErr.Error())
				} else {
					return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: entries}, false, nil, resolveErr)
				}
			}
		} else if !errors.Is(lookupErr, workspace.ErrSourceNotRegistered) {
			return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: entries}, false, nil, lookupErr)
		} else if view.State == string(research.SourceRetired) {
			entry.Status = "retired"
		}
		entries = append(entries, entry)
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "source status", sourceStatusData{SchemaVersion: sourceStatusDataSchema, Sources: entries}, false, nil, err)
	}
	data := sourceStatusData{SchemaVersion: sourceStatusDataSchema, Project: projectData, Sources: entries}
	partial := false
	for _, entry := range entries {
		if entry.Status == "stale" {
			partial = true
			_ = app.Warnf("Source %s association is stale: %s", entry.Source.ID, entry.Diagnostic)
		}
	}
	return commandSuccess(app, options.json, "source status", data, partial, nil, renderSourceStatusHuman(entries))
}

func runSourceRegister(command *cobra.Command, app *App, root *rootOptions, options *sourceRegisterOptions, args []string) error {
	hasReference := len(args) == 1 || strings.TrimSpace(root.source) != ""
	if !hasReference {
		if resolved, resolveErr := resolveSourceManagementContext(command, app, root); resolveErr == nil && resolved != nil && resolved.Source != nil {
			hasReference = true
		}
	}
	if strings.TrimSpace(options.repo) == "" || !hasReference {
		if !app.interactive(options.json) {
			return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema}, false, nil, invalidUsagef("Source register requires a Source reference and --repo in non-interactive or JSON mode"))
		}
		prepared, err := prepareGuidedSourceRegister(command, app, root, options, args)
		if err != nil {
			return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema}, false, nil, err)
		}
		args = prepared
	}
	resolved, store, service, err := openSourceService(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema}, false, nil, err)
	}
	reference, err := sourceReference(args, root, resolved)
	if err != nil {
		return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema}, false, nil, err)
	}
	document, err := service.Lookup(command.Context(), reference)
	if err != nil {
		return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema}, false, nil, err)
	}
	value := document.Record.(*research.Source)
	if value.State == research.SourceRetired {
		return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema}, false, nil, workspace.ErrRetiredSource)
	}
	clonePath := inputPath(resolved.InvocationDir, options.repo)
	observation, err := workspace.InspectSourceClone(command.Context(), clonePath, value.Subdir, app.GitRunner)
	if err != nil {
		return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema}, false, nil, err)
	}
	view, err := sourceViewForDocument(command.Context(), resolved.Project, store, document)
	if err != nil {
		return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema}, false, nil, err)
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema, Source: view}, false, nil, err)
	}
	_, err = app.Associations.RegisterProject(command.Context(), resolved.Project)
	if err != nil {
		partial := publicationWasPublished(err)
		return commandFailure(app, options.json, "source register", sourceRegisterData{SchemaVersion: sourceRegisterDataSchema, Project: projectData, Source: view}, partial, mutationPublicationDiagnostics(partial, "workspace association"), err)
	}
	association, err := app.Associations.RegisterSource(command.Context(), resolved.ProjectID(), value.ID, observation.Repository.Root)
	associationData := makeSourceAssociationView(association)
	data := sourceRegisterData{SchemaVersion: sourceRegisterDataSchema, Project: projectData, Source: view, Association: associationData}
	if err != nil {
		partial := publicationWasPublished(err)
		return commandFailure(app, options.json, "source register", data, partial, []Diagnostic{{Severity: SeverityError, Code: "source.association_failed", Message: safeDiagnosticText("local Source association publication failed: " + err.Error())}}, err)
	}
	human := fmt.Sprintf("Registered Source %s (%s) clone at %s.\n", view.Key, view.ID, associationData.Root)
	return commandSuccess(app, options.json, "source register", data, false, nil, human)
}

func runSourceAppendLocator(command *cobra.Command, app *App, root *rootOptions, options *sourceAppendLocatorOptions, args []string) error {
	empty := sourceAppendLocatorData{SchemaVersion: sourceAppendLocatorDataSchema, Projections: emptyProjectionResult()}
	if !options.confirm || !record.ValidRevision(options.expectedRevision) || strings.TrimSpace(options.locator) == "" {
		return commandFailure(app, options.json, "source append-locator", empty, false, nil, errors.New("Source locator append requires --locator, --expected-revision, and --confirm; no prompt is performed"))
	}
	resolved, store, service, err := openSourceService(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "source append-locator", empty, false, nil, err)
	}
	empty.Project, err = makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "source append-locator", empty, false, nil, err)
	}
	reference, err := sourceReference(args, root, resolved)
	if err != nil {
		return commandFailure(app, options.json, "source append-locator", empty, false, nil, err)
	}
	updated, err := service.AppendLocator(command.Context(), sourcepkg.AppendLocatorRequest{
		Reference: reference, ExpectedRevision: options.expectedRevision, Locator: options.locator,
	})
	if err != nil {
		if view, ok := transactionSourceView(resolved.Project, updated); ok {
			empty.Source = view
		}
		empty.Transaction, _ = sourcepkg.TransactionResultFromError(err)
		return commandFailure(app, options.json, "source append-locator", empty, empty.Transaction != nil, transactionFailureDiagnostics(empty.Transaction), err)
	}
	view, err := sourceViewForDocument(command.Context(), resolved.Project, store, updated)
	if err != nil {
		return commandFailure(app, options.json, "source append-locator", empty, true, nil, err)
	}
	_, rendered, renderErr := renderFreshProjections(command.Context(), app, resolved.Project, store)
	data := sourceAppendLocatorData{SchemaVersion: sourceAppendLocatorDataSchema, Project: empty.Project, Source: view, Projections: rendered}
	if renderErr != nil {
		return commandFailure(app, options.json, "source append-locator", data, true, nil, fmt.Errorf("Source %s was updated but projection refresh failed: %w", view.ID, renderErr))
	}
	return commandSuccess(app, options.json, "source append-locator", data, false, nil, fmt.Sprintf("Appended locator to Source %s at revision %s.\n", view.ID, view.Revision))
}

func runSourceRetire(command *cobra.Command, app *App, root *rootOptions, options *sourceRetireOptions, args []string) error {
	if !options.confirm || !record.ValidRevision(options.expectedRevision) {
		return commandFailure(app, options.json, "source retire", sourceRetireData{SchemaVersion: sourceRetireDataSchema, Projections: emptyProjectionResult()}, false, nil, errors.New("Source retirement requires --expected-revision and --confirm; no prompt is performed"))
	}
	resolved, store, service, err := openSourceService(command, app, root)
	data := sourceRetireData{SchemaVersion: sourceRetireDataSchema, Projections: emptyProjectionResult()}
	if err != nil {
		return commandFailure(app, options.json, "source retire", data, false, nil, err)
	}
	data.Project, err = makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "source retire", data, false, nil, err)
	}
	reference, err := sourceReference(args, root, resolved)
	if err != nil {
		return commandFailure(app, options.json, "source retire", data, false, nil, err)
	}
	retired, err := service.Retire(command.Context(), sourcepkg.RetireRequest{Reference: reference, ExpectedRevision: options.expectedRevision})
	if err != nil {
		if view, ok := transactionSourceView(resolved.Project, retired); ok {
			data.Source = view
		}
		data.Transaction, _ = sourcepkg.TransactionResultFromError(err)
		return commandFailure(app, options.json, "source retire", data, data.Transaction != nil, transactionFailureDiagnostics(data.Transaction), err)
	}
	view, err := sourceViewForDocument(command.Context(), resolved.Project, store, retired)
	if err != nil {
		return commandFailure(app, options.json, "source retire", sourceRetireData{SchemaVersion: sourceRetireDataSchema, Projections: emptyProjectionResult()}, true, nil, err)
	}
	_, rendered, renderErr := renderFreshProjections(command.Context(), app, resolved.Project, store)
	data.Source = view
	data.Projections = rendered
	if renderErr != nil {
		return commandFailure(app, options.json, "source retire", data, true, nil, fmt.Errorf("Source %s was retired but projection refresh failed: %w", view.ID, renderErr))
	}
	return commandSuccess(app, options.json, "source retire", data, false, nil, fmt.Sprintf("Retired Source %s at revision %s.\n", view.ID, view.Revision))
}

func resolveSourceManagementContext(command *cobra.Command, app *App, root *rootOptions) (*workspace.Context, error) {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return nil, err
	}
	request := workspace.ResolveRequest{
		InvocationDir: start, Workspace: strings.TrimSpace(root.workspace), SkipConfig: true,
	}
	resolved, err := app.ResolveWorkspace.Resolve(command.Context(), request)
	if err == nil || strings.TrimSpace(root.source) == "" || !errors.Is(err, workspace.ErrNotRegistered) {
		return resolved, err
	}
	request.Source = strings.TrimSpace(root.source)
	return app.ResolveWorkspace.Resolve(command.Context(), request)
}

func openSourceService(command *cobra.Command, app *App, root *rootOptions) (*workspace.Context, TransactionalRecordStore, SourceService, error) {
	resolved, err := resolveSourceManagementContext(command, app, root)
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := app.NewTransactionalStore(resolved.Project)
	if err != nil {
		return nil, nil, nil, err
	}
	service := app.NewSourceService(store)
	if service == nil {
		return nil, nil, nil, errors.New("Source service factory returned nil")
	}
	return resolved, store, service, nil
}

func inspectSourceAddPlan(command *cobra.Command, app *App, resolved *workspace.Context, request sourcepkg.AddRequest, repo string, confirmLocalOnly bool) (inspectedSourcePlan, error) {
	clonePath := inputPath(resolved.InvocationDir, repo)
	observation, err := workspace.InspectSourceClone(command.Context(), clonePath, request.Subdir, app.GitRunner)
	if err != nil {
		return inspectedSourcePlan{}, err
	}
	plan, err := sourcepkg.BuildAddPlan(sourcepkg.AddPlanRequest{
		Request: request, CloneRoot: observation.Repository.Root,
		CloneGitCommonDir:    observation.Repository.GitCommonDir,
		ObservedLocatorHints: observation.LocatorHints, ConfirmLocalOnly: confirmLocalOnly,
	})
	if err != nil {
		return inspectedSourcePlan{}, err
	}
	return inspectedSourcePlan{plan: plan, resolvedRoot: observation.ResolvedRoot}, nil
}

func makeSourceAddPlanView(inspected inspectedSourcePlan) sourceAddPlanView {
	plan := inspected.plan
	return sourceAddPlanView{
		Key: plan.Request.Key, Title: plan.Request.Title, Subdir: plan.Request.Subdir,
		LocatorHints: append([]string{}, plan.Request.LocatorHints...),
		CloneRoot:    safex.NewRedactor().Path(plan.CloneRoot), ResolvedRoot: safex.NewRedactor().Path(inspected.resolvedRoot),
		LocalOnly: plan.LocalOnly,
		Effects:   []string{"publish_canonical_source", "register_workspace_association", "register_source_association", "refresh_projections"},
	}
}

func transactionSourceView(info *project.Info, document *record.Document) (sourceView, bool) {
	if info == nil || document == nil {
		return sourceView{}, false
	}
	value, ok := document.Record.(*research.Source)
	if !ok || value.ID.IsZero() {
		return sourceView{}, false
	}
	relative, err := repositoryRelativePath(info, document.Path)
	if err != nil {
		return sourceView{}, false
	}
	retiredAt := ""
	if value.RetiredAt != nil {
		retiredAt = value.RetiredAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	tags := append([]string{}, value.Tags...)
	sort.Strings(tags)
	return sourceView{
		ID: value.ID.String(), Path: relative, Revision: document.Revision,
		Key: value.Key, Title: value.Title, Kind: string(value.Kind), Subdir: value.Subdir,
		LocatorHints: append([]string{}, value.LocatorHints...), State: string(value.State), RetiredAt: retiredAt, Tags: tags,
	}, true
}

func sourceViewForDocument(ctx context.Context, info *project.Info, store canonicalInventoryStore, document *record.Document) (sourceView, error) {
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return sourceView{}, err
	}
	views, err := makeSourceViews(info, inventory.OfKind(research.KindSource))
	if err != nil {
		return sourceView{}, err
	}
	id, ok := document.ID()
	if !ok {
		return sourceView{}, errors.New("Source document has no identity")
	}
	for _, view := range views {
		if view.ID == id.String() {
			return view, nil
		}
	}
	return sourceView{}, fmt.Errorf("Source %s is absent from canonical inventory", id)
}

func sourceReference(args []string, root *rootOptions, resolved *workspace.Context) (string, error) {
	argument := ""
	if len(args) > 0 {
		argument = strings.TrimSpace(args[0])
	}
	selector := strings.TrimSpace(root.source)
	if argument != "" && selector != "" {
		return "", errors.New("Source target is ambiguous: provide either the positional Source argument or --source, not both")
	}
	if argument != "" {
		return argument, nil
	}
	if selector != "" {
		return selector, nil
	}
	if resolved != nil && resolved.Source != nil {
		return resolved.Source.ID.String(), nil
	}
	return "", errors.New("Source reference is required as an argument or --source selector")
}

func inputPath(invocation, value string) string {
	value = strings.TrimSpace(value)
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(invocation, value))
}

func makeSourceAssociationView(association workspace.SourceAssociation) sourceAssociationView {
	return sourceAssociationView{
		ProjectID: association.ProjectID.String(), SourceID: association.SourceID.String(),
		Root: safex.NewRedactor().Path(association.Root), Locators: append([]string{}, association.LocatorHints...),
		ObservedAt: association.ObservedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func transactionHasPublishedPath(result *record.TransactionResult) bool {
	if result == nil {
		return false
	}
	for _, path := range result.Paths {
		if path.Published {
			return true
		}
	}
	return false
}

func transactionFailureDiagnostics(result *record.TransactionResult) []Diagnostic {
	if result == nil {
		return nil
	}
	message := fmt.Sprintf("canonical transaction %s is %s", result.TransactionID, result.State)
	if result.RecoveryRequired {
		message += "; recovery is required before further canonical access"
	}
	return []Diagnostic{{Severity: SeverityError, Code: "transaction.recovery_required", Message: message}}
}

func sourceAssociationFailureDiagnostics(err error) []Diagnostic {
	return []Diagnostic{{
		Severity: SeverityError, Code: "source.association_failed",
		Message: safeDiagnosticText("canonical Source was published, but local association failed: " + err.Error()),
	}}
}

func renderSourceListHuman(sources []sourceView) string {
	if len(sources) == 0 {
		return "No canonical Sources.\n"
	}
	table := newHumanRows(5)
	for _, source := range sources {
		table.Add(source.Display, source.Key, source.State, source.Subdir, singleLineHuman(source.Title))
	}
	return mustRenderTable(table)
}

func renderSourceStatusHuman(entries []sourceStatusEntry) string {
	if len(entries) == 0 {
		return "No canonical Sources.\n"
	}
	table := newHumanRows(5)
	for _, entry := range entries {
		table.Add(entry.Source.Display, entry.Status, fmt.Sprintf("registered=%t", entry.Registered), fmt.Sprintf("resolvable=%t", entry.Resolvable), entry.Root)
	}
	return mustRenderTable(table)
}

func mustParseSourceID(value string) research.ID {
	parsed, _ := research.ParseIDForKind(value, research.KindSource)
	return parsed
}
