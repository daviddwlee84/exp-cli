package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

type validateOptions struct {
	json bool
}

type renderOptions struct {
	check bool
	json  bool
}

type contextOptions struct {
	json bool
}

func newValidateCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &validateOptions{}
	command := &cobra.Command{
		Use:   "validate",
		Short: "Validate the complete canonical local inventory",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runValidate(command, app, rootOptions, options)
		},
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newRenderCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &renderOptions{}
	command := &cobra.Command{
		Use:   "render",
		Short: "Render or byte-check deterministic generated views",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runRender(command, app, rootOptions, options)
		},
	}
	command.Flags().BoolVar(&options.check, "check", false, "compare exact projection bytes without writing")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newContextCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &contextOptions{}
	command := &cobra.Command{
		Use:   "context",
		Short: "Show local resume context without provider refresh",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runContext(command, app, rootOptions, options)
		},
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runValidate(command *cobra.Command, app *App, rootOptions *rootOptions, options *validateOptions) error {
	info, inventory, err := loadProjectInventory(command, app, rootOptions)
	if err != nil {
		return commandFailure(app, options.json, "validate", struct{}{}, false, nil, err)
	}
	projectData, err := makeProjectView(info)
	if err != nil {
		return commandFailure(app, options.json, "validate", struct{}{}, false, nil, err)
	}
	data := validateData{Project: projectData, Valid: inventory.Valid(), Counts: countsFor(inventory)}
	diagnostics := convertRecordDiagnostics(inventory.Diagnostics)
	if !inventory.Valid() {
		inventoryErr := &record.InventoryError{Diagnostics: append([]record.Diagnostic(nil), inventory.Diagnostics...)}
		if options.json {
			return commandFailure(app, true, "validate", data, false, diagnostics, inventoryErr)
		}
		if err := app.WriteHuman(safeHumanOutput(renderValidationHuman(inventory))); err != nil {
			return err
		}
		return inventoryErr
	}
	human := fmt.Sprintf("Valid canonical inventory: %d research records at %s\n", data.Counts.Total, projectData.Root)
	return commandSuccess(app, options.json, "validate", data, false, diagnostics, human)
}

func runRender(command *cobra.Command, app *App, rootOptions *rootOptions, options *renderOptions) error {
	info, store, err := openProjectStore(command, app, rootOptions)
	if err != nil {
		return commandFailure(app, options.json, "render", struct{}{}, false, nil, err)
	}
	projectData, err := makeProjectView(info)
	if err != nil {
		return commandFailure(app, options.json, "render", struct{}{}, false, nil, err)
	}
	var result projection.Result
	if options.check {
		_, result, err = checkFreshProjections(command.Context(), app, info, store)
	} else {
		_, result, err = renderFreshProjections(command.Context(), app, info, store)
	}
	result = stableProjectionResult(result)
	data := renderData{Project: projectData, Check: options.check, Result: result}
	if err != nil {
		diagnostics := diagnosticsForError(err)
		if publicationWasPublished(err) && len(result.Written) > 0 {
			diagnostics = durabilityUncertainDiagnostics("generated projection", result.Written[len(result.Written)-1])
		} else if errors.Is(err, projection.ErrStale) && len(result.Drifted) > 0 {
			diagnostics = projectionDriftDiagnostics(result.Drifted)
		}
		if options.json {
			return commandFailure(app, true, "render", data, len(result.Written) > 0, diagnostics, err)
		}
		if len(result.Drifted) > 0 {
			if writeErr := app.WriteHuman(safeHumanOutput(renderProjectionDriftHuman(result))); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	if options.check {
		human := fmt.Sprintf("All %d projections are current at %s\n", len(result.Files), projectData.Root)
		return commandSuccess(app, options.json, "render", data, false, nil, human)
	}
	human := fmt.Sprintf("Rendered %d projections at %s (%d updated)\n", len(result.Files), projectData.Root, len(result.Written))
	return commandSuccess(app, options.json, "render", data, false, nil, human)
}

func runContext(command *cobra.Command, app *App, rootOptions *rootOptions, options *contextOptions) error {
	info, inventory, err := loadProjectInventory(command, app, rootOptions)
	if err != nil {
		return commandFailure(app, options.json, "context", contextData{QueuedPlans: []planView{}}, false, nil, err)
	}
	projectData, err := makeProjectView(info)
	if err != nil {
		return commandFailure(app, options.json, "context", contextData{QueuedPlans: []planView{}}, false, nil, err)
	}
	allPlans, err := makePlanViews(info, inventory.OfKind(research.KindPlan))
	if err != nil {
		return commandFailure(app, options.json, "context", contextData{Project: projectData, QueuedPlans: []planView{}}, false, nil, err)
	}
	byID := make(map[string]planView, len(allPlans))
	for _, plan := range allPlans {
		byID[plan.ID] = plan
	}
	queued := make([]planView, 0)
	for _, document := range sortQueuedPlanDocuments(inventory.OfKind(research.KindPlan)) {
		id, _ := document.ID()
		queued = append(queued, byID[id.String()])
	}
	if queued == nil {
		queued = []planView{}
	}
	frontier := []contextFrontierView{}
	for _, entry := range inventory.QueueFrontier() {
		title := ""
		if document, resolveErr := inventory.ByID(entry.Entry.Plan); resolveErr == nil {
			title = document.Record.(*research.Plan).Title
		}
		frontier = append(frontier, contextFrontierView{Queue: entry.Queue.String(), Pool: entry.Pool.String(), Lane: string(entry.Lane), Plan: entry.Entry.Plan.String(), Title: title, Score: entry.Entry.Score})
	}
	champions, championErr := inventory.CurrentChampions()
	if championErr != nil {
		return commandFailure(app, options.json, "context", contextData{Project: projectData, QueuedPlans: queued, QueueFrontier: frontier}, false, nil, championErr)
	}
	if champions == nil {
		champions = []record.Champion{}
	}
	data := contextData{
		Project:          projectData,
		Counts:           countsFor(inventory),
		QueuedPlans:      queued,
		QueueFrontier:    frontier,
		Champions:        champions,
		ProviderRefresh:  false,
		LiveObservations: false,
		ObservationScope: "local_canonical_records_only",
	}
	diagnostics := convertRecordDiagnostics(inventory.Diagnostics)
	human := renderContextHuman(data)
	if !inventory.Valid() {
		inventoryErr := &record.InventoryError{Diagnostics: append([]record.Diagnostic(nil), inventory.Diagnostics...)}
		if options.json {
			return commandFailure(app, true, "context", data, true, diagnostics, inventoryErr)
		}
		if writeErr := app.WriteHuman(safeHumanOutput(human + renderValidationHuman(inventory))); writeErr != nil {
			return writeErr
		}
		return inventoryErr
	}
	return commandSuccess(app, options.json, "context", data, false, diagnostics, human)
}

func loadProjectInventory(command *cobra.Command, app *App, rootOptions *rootOptions) (*project.Info, *record.Inventory, error) {
	info, store, err := openProjectStore(command, app, rootOptions)
	if err != nil {
		return nil, nil, err
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return nil, nil, err
	}
	return info, inventory, nil
}

func openProjectStore(command *cobra.Command, app *App, rootOptions *rootOptions) (*project.Info, RecordStore, error) {
	start, err := app.startDir(rootOptions.startDir)
	if err != nil {
		return nil, nil, err
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return nil, nil, err
	}
	store, err := app.NewStore(info)
	if err != nil {
		return nil, nil, err
	}
	return info, store, nil
}

func renderValidationHuman(inventory *record.Inventory) string {
	var output strings.Builder
	for _, diagnostic := range inventory.Diagnostics {
		path := diagnostic.Path
		if path == "" {
			path = "inventory"
		}
		fmt.Fprintf(&output, "ERROR %s [%s] %s\n", safeDiagnosticText(path), safeDiagnosticText(diagnostic.Code), safeDiagnosticText(diagnostic.Message))
	}
	fmt.Fprintf(&output, "Invalid canonical inventory: %d diagnostic(s)\n", len(inventory.Diagnostics))
	return output.String()
}

func renderProjectionDriftHuman(result projection.Result) string {
	var output strings.Builder
	output.WriteString("Stale generated projections:\n")
	for _, path := range result.Drifted {
		fmt.Fprintf(&output, "  %s\n", safeDiagnosticText(path))
	}
	return output.String()
}

func renderContextHuman(data contextData) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%s (%s)\n", data.Project.Name, data.Project.ID)
	fmt.Fprintf(&output, "Root: %s\n", data.Project.Root)
	fmt.Fprintf(&output, "Records: ideas=%d queues=%d plans=%d experiments=%d attempts=%d findings=%d candidates=%d releases=%d promotions=%d\n",
		data.Counts.Ideas, data.Counts.Queues, data.Counts.Plans, data.Counts.Experiments, data.Counts.Attempts,
		data.Counts.Findings, data.Counts.Candidates, data.Counts.Releases, data.Counts.Promotions)
	output.WriteString("Provider refresh: false; live observations: false (local canonical records only)\n")
	if len(data.QueuedPlans) == 0 {
		output.WriteString("Queued Plans: none\n")
	} else {
		output.WriteString("Queued Plans:\n")
		writer := tabwriter.NewWriter(&output, 0, 4, 2, ' ', 0)
		for _, plan := range data.QueuedPlans {
			_, _ = fmt.Fprintf(writer, "  %s\t%s/%s\t%s\t%s\t%s\n", plan.Display, plan.Priority, plan.Effort, singleLineHuman(plan.Title), plan.ID, plan.Revision)
		}
		_ = writer.Flush()
	}
	if len(data.QueueFrontier) > 0 {
		output.WriteString("Queue frontiers:\n")
		for _, entry := range data.QueueFrontier {
			fmt.Fprintf(&output, "  %s/%s  %.6g  %s  %s\n", entry.Pool, entry.Lane, entry.Score, entry.Plan, singleLineHuman(entry.Title))
		}
	}
	if len(data.Champions) > 0 {
		output.WriteString("Champions:\n")
		for _, champion := range data.Champions {
			fmt.Fprintf(&output, "  %s  %s\n", champion.Target, champion.Release)
		}
	}
	return output.String()
}
