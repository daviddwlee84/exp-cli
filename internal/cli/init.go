package cli

import (
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/spf13/cobra"
)

type initOptions struct {
	name string
	json bool
}

func newInitCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &initOptions{}
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize an idempotent v1 experiments root",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runInit(command, app, rootOptions, options)
		},
	}
	command.Flags().StringVar(&options.name, "name", "", "set the project name (defaults to the Git repository name)")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runInit(command *cobra.Command, app *App, rootOptions *rootOptions, options *initOptions) error {
	start, err := app.startDir(rootOptions.startDir)
	if err != nil {
		return commandFailure(app, options.json, "init", struct{}{}, false, nil, err)
	}
	info, created, err := app.InitializeProject(command.Context(), project.InitRequest{StartDir: start, Name: options.name})
	if err != nil {
		if info != nil && publicationWasPublished(err) {
			projectData, viewErr := makeProjectView(info)
			if viewErr != nil {
				return commandFailure(app, options.json, "init", struct{}{}, true, durabilityUncertainDiagnostics("canonical", record.ProjectFile), fmt.Errorf("project was published but durability is uncertain: %w", err))
			}
			data := initData{Project: projectData, Created: created, Projections: emptyProjectionResult()}
			return commandFailure(app, options.json, "init", data, true, durabilityUncertainDiagnostics("canonical", record.ProjectFile), fmt.Errorf("project %s was published but durability is uncertain: %w", projectData.ID, err))
		}
		return commandFailure(app, options.json, "init", struct{}{}, false, nil, err)
	}
	projectData, err := makeProjectView(info)
	if err != nil {
		return commandFailure(app, options.json, "init", initData{Created: created, Projections: emptyProjectionResult()}, created, nil, err)
	}
	store, err := app.NewStore(info)
	if err != nil {
		return commandFailure(app, options.json, "init", initData{Project: projectData, Created: created, Projections: emptyProjectionResult()}, created, nil, err)
	}
	_, rendered, err := renderFreshProjections(command.Context(), app, info, store)
	data := initData{Project: projectData, Created: created, Projections: rendered}
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
