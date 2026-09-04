package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
	"github.com/spf13/cobra"
)

const (
	workspaceRegisterDataSchema = "exp.command.workspace-register/v1"
	workspaceStatusDataSchema   = "exp.command.workspace-status/v1"
)

type workspaceCommandOptions struct {
	json bool
}

type projectAssociationView struct {
	ProjectID  string `json:"project_id"`
	Root       string `json:"root"`
	ObservedAt string `json:"observed_at"`
}

type workspaceRegisterData struct {
	SchemaVersion string                 `json:"schema_version"`
	Project       projectView            `json:"project"`
	Association   projectAssociationView `json:"association"`
}

type workspaceStatusData struct {
	SchemaVersion string                 `json:"schema_version"`
	Project       projectView            `json:"project"`
	Workspace     workspaceContextView   `json:"workspace"`
	Source        *selectedSourceView    `json:"source,omitempty"`
	Association   associationSummaryView `json:"association"`
	Config        configSummaryView      `json:"config"`
}

func newWorkspaceCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "workspace", Short: "Register or inspect canonical workspace associations", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newWorkspaceRegisterCommand(app, root), newWorkspaceStatusCommand(app, root), newWorkspaceBackendCommand(app, root))
	return command
}

func newWorkspaceRegisterCommand(app *App, root *rootOptions) *cobra.Command {
	options := &workspaceCommandOptions{}
	command := &cobra.Command{
		Use: "register", Short: "Register the resolved canonical workspace on this host", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runWorkspaceRegister(command, app, root, options)
		},
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newWorkspaceStatusCommand(app *App, root *rootOptions) *cobra.Command {
	options := &workspaceCommandOptions{}
	command := &cobra.Command{
		Use: "status", Short: "Show resolved workspace, Source, association, and config status", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runWorkspaceStatus(command, app, root, options) },
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runWorkspaceRegister(command *cobra.Command, app *App, root *rootOptions, options *workspaceCommandOptions) error {
	management := *root
	management.source = ""
	resolved, err := resolveWorkspaceContext(command, app, &management)
	if err != nil {
		return commandFailure(app, options.json, "workspace register", workspaceRegisterData{SchemaVersion: workspaceRegisterDataSchema}, false, nil, err)
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "workspace register", workspaceRegisterData{SchemaVersion: workspaceRegisterDataSchema}, false, nil, err)
	}
	association, err := app.Associations.RegisterProject(command.Context(), resolved.Project)
	associationData := makeProjectAssociationView(association)
	data := workspaceRegisterData{SchemaVersion: workspaceRegisterDataSchema, Project: projectData, Association: associationData}
	if err != nil {
		partial := publicationWasPublished(err)
		return commandFailure(app, options.json, "workspace register", data, partial, mutationPublicationDiagnostics(partial, "workspace association"), err)
	}
	human := fmt.Sprintf("Registered workspace %s at %s (observed %s).\n", associationData.ProjectID, associationData.Root, associationData.ObservedAt)
	return commandSuccess(app, options.json, "workspace register", data, false, nil, human)
}

func runWorkspaceStatus(command *cobra.Command, app *App, root *rootOptions, options *workspaceCommandOptions) error {
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "workspace status", workspaceStatusData{SchemaVersion: workspaceStatusDataSchema}, false, nil, err)
	}
	store, err := app.NewStore(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "workspace status", workspaceStatusData{SchemaVersion: workspaceStatusDataSchema}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "workspace status", workspaceStatusData{SchemaVersion: workspaceStatusDataSchema}, false, nil, err)
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "workspace status", workspaceStatusData{SchemaVersion: workspaceStatusDataSchema}, false, nil, err)
	}
	workspaceData, sourceData, associationData, configData, err := makeWorkspaceContextViews(resolved, inventory)
	if err != nil {
		return commandFailure(app, options.json, "workspace status", workspaceStatusData{SchemaVersion: workspaceStatusDataSchema}, false, nil, err)
	}
	data := workspaceStatusData{
		SchemaVersion: workspaceStatusDataSchema, Project: projectData, Workspace: workspaceData,
		Source: sourceData, Association: associationData, Config: configData,
	}
	human := fmt.Sprintf("Workspace %s at %s; resolution=%s registered=%t config_layers=%d trusted=%t.\n",
		workspaceData.ProjectID, workspaceData.RepositoryRoot, workspaceData.Resolution,
		associationData.ProjectRegistered, len(configData.Layers), configData.Trusted)
	if sourceData != nil {
		human += fmt.Sprintf("Source %s (%s) at %s; registered=%t.\n", sourceData.Source.Key, sourceData.Source.ID, sourceData.ResolvedRoot, associationData.SourceRegistered)
	}
	warnUntrustedConfig(app, configData)
	return commandSuccess(app, options.json, "workspace status", data, !inventory.Valid() || !configData.Trusted, convertRecordDiagnostics(inventory.Diagnostics), human)
}

func makeProjectAssociationView(association workspace.ProjectAssociation) projectAssociationView {
	observedAt := ""
	if !association.ObservedAt.IsZero() {
		observedAt = association.ObservedAt.UTC().Format(time.RFC3339Nano)
	}
	return projectAssociationView{
		ProjectID: association.ProjectID.String(), Root: safex.NewRedactor().Path(association.Root), ObservedAt: observedAt,
	}
}

const (
	workspaceBackendListDataSchema    = "exp.command.workspace-backend-list/v1"
	workspaceBackendStatusDataSchema  = "exp.command.workspace-backend-status/v1"
	workspaceBackendHandoffDataSchema = "exp.command.workspace-backend-handoff/v1"
)

type workspaceBackendOptions struct {
	json    bool
	probe   bool
	attempt string
}

type workspaceBackendListData struct {
	SchemaVersion string                        `json:"schema_version"`
	Descriptors   []workspacebackend.Descriptor `json:"descriptors"`
	Readiness     []workspacebackend.Readiness  `json:"readiness"`
}

type workspaceBackendStatusData struct {
	SchemaVersion string                             `json:"schema_version"`
	Selection     workspacebackend.ProviderSelection `json:"selection"`
	Resolution    workspacebackend.Resolution        `json:"resolution"`
}

type workspaceBackendHandoffData struct {
	SchemaVersion   string `json:"schema_version"`
	Try             string `json:"try"`
	Attempt         string `json:"attempt"`
	PreparedBy      string `json:"prepared_by"`
	HandoffProvider string `json:"handoff_provider"`
	Branch          string `json:"branch"`
	HeadCommit      string `json:"head_commit"`
	Clean           bool   `json:"clean"`
}

type workspaceHandoffCoordinator interface {
	Handoff(context.Context, tryflow.HandoffRequest) (*tryflow.HandoffResult, error)
}

func newWorkspaceBackendCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "backend", Short: "Inspect workspace provider capabilities and selection", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newWorkspaceBackendListCommand(app), newWorkspaceBackendStatusCommand(app, root), newWorkspaceBackendHandoffCommand(app, root))
	return command
}

func newWorkspaceBackendListCommand(app *App) *cobra.Command {
	options := &workspaceBackendOptions{}
	command := &cobra.Command{
		Use: "list", Short: "List built-in and optional workspace providers", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runWorkspaceBackendList(command, app, options) },
	}
	command.Flags().BoolVar(&options.probe, "probe", false, "run bounded local probes (no network, auth, install, or service start)")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newWorkspaceBackendStatusCommand(app *App, root *rootOptions) *cobra.Command {
	options := &workspaceBackendOptions{}
	command := &cobra.Command{
		Use: "status", Short: "Show requested and actual workspace preparation provider", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runWorkspaceBackendStatus(command, app, root, options)
		},
	}
	command.Flags().BoolVar(&options.probe, "probe", false, "run the bounded local compatibility probe")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newWorkspaceBackendHandoffCommand(app *App, root *rootOptions) *cobra.Command {
	options := &workspaceBackendOptions{}
	command := &cobra.Command{
		Use: "handoff <try>", Short: "Open one exact managed Try Attempt through the selected provider", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runWorkspaceBackendHandoff(command, app, root, options, args[0])
		},
	}
	command.Flags().StringVar(&options.attempt, "attempt", "", "select the exact canonical Attempt to hand off")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runWorkspaceBackendList(command *cobra.Command, app *App, options *workspaceBackendOptions) error {
	empty := workspaceBackendListData{
		SchemaVersion: workspaceBackendListDataSchema,
		Descriptors:   []workspacebackend.Descriptor{}, Readiness: []workspacebackend.Readiness{},
	}
	cwd, err := app.Getwd()
	if err != nil {
		return commandFailure(app, options.json, "workspace backend list", empty, false, nil, err)
	}
	readiness, err := app.WorkspaceRegistry.Statuses(command.Context(), cwd, options.probe)
	data := workspaceBackendListData{SchemaVersion: workspaceBackendListDataSchema, Descriptors: app.WorkspaceRegistry.Descriptors(), Readiness: readiness}
	if err != nil {
		return commandFailure(app, options.json, "workspace backend list", data, false, nil, err)
	}
	return commandSuccess(app, options.json, "workspace backend list", data, false, nil, renderWorkspaceBackendList(data))
}

func runWorkspaceBackendStatus(command *cobra.Command, app *App, root *rootOptions, options *workspaceBackendOptions) error {
	empty := workspaceBackendStatusData{SchemaVersion: workspaceBackendStatusDataSchema}
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "workspace backend status", empty, false, nil, err)
	}
	selection, err := workspacebackend.ResolveSelection(root.workspaceBackend, resolved.Config)
	if err != nil {
		return commandFailure(app, options.json, "workspace backend status", empty, false, nil, err)
	}
	resolution, resolveErr := app.WorkspaceRegistry.Preview(command.Context(), selection, workspacebackend.CapabilityPrepare, resolved.InvocationDir, options.probe)
	data := workspaceBackendStatusData{SchemaVersion: workspaceBackendStatusDataSchema, Selection: selection, Resolution: resolution}
	if resolveErr != nil {
		return commandFailure(app, options.json, "workspace backend status", data, false, nil, resolveErr)
	}
	human := fmt.Sprintf("Workspace provider requested=%s actual=%s origin=%s readiness=%s fallback=%t", resolution.Requested, resolution.Actual, resolution.Origin, resolution.Readiness.State, resolution.Fallback)
	if resolution.FallbackReason != "" {
		human += " reason=" + resolution.FallbackReason
	}
	return commandSuccess(app, options.json, "workspace backend status", data, false, nil, human+".\n")
}

func runWorkspaceBackendHandoff(command *cobra.Command, app *App, root *rootOptions, options *workspaceBackendOptions, tryReference string) error {
	empty := workspaceBackendHandoffData{SchemaVersion: workspaceBackendHandoffDataSchema}
	if strings.TrimSpace(options.attempt) == "" {
		return commandFailure(app, options.json, "workspace backend handoff", empty, false, nil, fmt.Errorf("workspace backend handoff requires --attempt"))
	}
	selection, err := trySelection(app, root)
	if err != nil {
		return commandFailure(app, options.json, "workspace backend handoff", empty, false, nil, err)
	}
	coordinator, ok := app.NewTryCoordinator().(workspaceHandoffCoordinator)
	if !ok {
		return commandFailure(app, options.json, "workspace backend handoff", empty, false, nil, fmt.Errorf("Try coordinator does not support workspace handoff"))
	}
	result, handoffErr := coordinator.Handoff(command.Context(), tryflow.HandoffRequest{
		Selection: selection, Try: tryReference, Attempt: options.attempt,
	})
	data := empty
	if result != nil && result.Try != nil && result.Attempt != nil {
		data.Try = result.Try.Record.(*research.Try).ID.String()
		data.Attempt = result.Attempt.Record.(*research.Attempt).ID.String()
		data.PreparedBy = result.Inspection.Workspace.Backend
		data.HandoffProvider = result.Inspection.HandoffProvider
		data.Branch = result.Inspection.Workspace.Branch
		data.HeadCommit = result.Inspection.HeadCommit
		data.Clean = result.Inspection.Clean
	}
	if handoffErr != nil {
		return commandFailure(app, options.json, "workspace backend handoff", data, false, nil, handoffErr)
	}
	human := fmt.Sprintf("Handed off Attempt %s prepared by %s through %s on %s.\n", data.Attempt, data.PreparedBy, data.HandoffProvider, data.Branch)
	return commandSuccess(app, options.json, "workspace backend handoff", data, false, nil, human)
}

func renderWorkspaceBackendList(data workspaceBackendListData) string {
	byName := make(map[string]workspacebackend.Readiness, len(data.Readiness))
	for _, status := range data.Readiness {
		byName[status.Provider] = status
	}
	table := newHumanTable("BACKEND", "READINESS", "PREPARE", "HANDOFF", "RETIRE")
	for _, descriptor := range data.Descriptors {
		status := byName[descriptor.Name]
		table.Add(descriptor.Name, string(status.State),
			string(workspaceCapabilitySupport(descriptor, workspacebackend.CapabilityPrepare)),
			string(workspaceCapabilitySupport(descriptor, workspacebackend.CapabilityHandoff)),
			string(workspaceCapabilitySupport(descriptor, workspacebackend.CapabilityRetire)))
	}
	return mustRenderTable(table)
}

func workspaceCapabilitySupport(descriptor workspacebackend.Descriptor, capability workspacebackend.Capability) workspacebackend.Support {
	for _, status := range descriptor.Capabilities {
		if status.Capability == capability {
			return status.Support
		}
	}
	return workspacebackend.SupportUnsupported
}
