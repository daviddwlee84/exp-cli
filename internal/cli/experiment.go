package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/experimentgit"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
	"github.com/spf13/cobra"
)

type experimentWorkspaceOptions struct {
	json  bool
	base  string
	allow []string
}

type experimentAgentOptions struct {
	experimentWorkspaceOptions
	config  string
	profile string
	prompt  string
}

func newExperimentCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "experiment", Short: "Operate isolated experiment workspaces and scientific lifecycle", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newExperimentWorkspaceCommand(app, root), newExperimentAgentCommand(app, root), newExperimentCloseCommand(app, root))
	return command
}

func newExperimentAgentCommand(app *App, root *rootOptions) *cobra.Command {
	options := &experimentAgentOptions{prompt: "-"}
	command := &cobra.Command{Use: "agent <experiment>", Short: "Run one fresh code-edit agent in an isolated worktree and commit its exact allowlisted changes", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runExperimentAgent(command, app, root, options, args[0])
	}
	addExperimentWorkspaceFlags(command, &options.experimentWorkspaceOptions)
	command.Flags().StringVar(&options.config, "config", "", "agent profile TOML path")
	command.Flags().StringVar(&options.profile, "profile", "", "override the experiment_implementer role profile")
	command.Flags().StringVar(&options.prompt, "prompt", "-", "read additional implementation instructions from a path or stdin")
	return command
}

func newExperimentWorkspaceCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "workspace", Short: "Prepare or commit an allowlisted experiment Git worktree", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newExperimentWorkspacePrepareCommand(app, root), newExperimentWorkspaceCommitCommand(app, root))
	return command
}

func newExperimentWorkspacePrepareCommand(app *App, root *rootOptions) *cobra.Command {
	options := &experimentWorkspaceOptions{}
	command := &cobra.Command{Use: "prepare <experiment>", Short: "Create an isolated exp/<id>-<slug> worktree at an exact base commit", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runExperimentWorkspace(command, app, root, options, args[0], false)
	}
	addExperimentWorkspaceFlags(command, options)
	return command
}

func newExperimentWorkspaceCommitCommand(app *App, root *rootOptions) *cobra.Command {
	options := &experimentWorkspaceOptions{}
	command := &cobra.Command{Use: "commit <experiment>", Short: "Commit only the exact allowlisted experiment change set", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runExperimentWorkspace(command, app, root, options, args[0], true)
	}
	addExperimentWorkspaceFlags(command, options)
	return command
}

func addExperimentWorkspaceFlags(command *cobra.Command, options *experimentWorkspaceOptions) {
	command.Flags().StringVar(&options.base, "base", "", "require a full exact Git base object ID")
	command.Flags().StringSliceVar(&options.allow, "allow", nil, "allow a Source-subdir-relative POSIX path glob (repeatable)")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	_ = command.MarkFlagRequired("base")
}

func runExperimentWorkspace(command *cobra.Command, app *App, root *rootOptions, options *experimentWorkspaceOptions, reference string, commit bool) error {
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "experiment workspace", struct{}{}, false, nil, err)
	}
	info := resolved.Project
	store, err := app.NewTransactionalStore(info)
	if err != nil {
		return commandFailure(app, options.json, "experiment workspace", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "experiment workspace", struct{}{}, false, nil, err)
	}
	document, err := inventory.Resolve(reference, research.KindExperiment)
	if err != nil {
		return commandFailure(app, options.json, "experiment workspace", struct{}{}, false, nil, err)
	}
	experiment := document.Record.(*research.Experiment)
	if err := requireExecutableExperiment(experiment); err != nil {
		return commandFailure(app, options.json, "experiment workspace", struct{}{}, false, nil, err)
	}
	request, err := experimentGitRequest(resolved, experiment, options.base, options.allow)
	if err != nil {
		return commandFailure(app, options.json, "experiment workspace", struct{}{}, false, nil, err)
	}
	manager := experimentgit.Manager{Git: app.GitRunner}
	if !commit {
		resolution, resolveErr := resolveExperimentPrepareProvider(command.Context(), app, resolved, root.workspaceBackend, request.RepositoryRoot)
		if resolveErr != nil {
			return commandFailure(app, options.json, "experiment workspace prepare", experimentgit.Workspace{}, false, nil, resolveErr)
		}
		workspace, prepareErr := manager.Prepare(command.Context(), request)
		workspace.Backend = resolution.Actual
		if prepareErr != nil {
			return commandFailure(app, options.json, "experiment workspace prepare", workspace, false, nil, prepareErr)
		}
		return commandSuccess(app, options.json, "experiment workspace prepare", workspace, false, nil, fmt.Sprintf("Prepared %s with %s on %s at %s.\n", workspace.Branch, workspace.Backend, workspace.BaseCommit, workspace.Worktree))
	}
	changeSet, err := manager.Commit(command.Context(), request)
	changeSet.Backend = workspacebackend.NativeGitName
	if err != nil {
		return commandFailure(app, options.json, "experiment workspace commit", changeSet, false, nil, err)
	}
	return commandSuccess(app, options.json, "experiment workspace commit", changeSet, false, nil, fmt.Sprintf("Committed %d allowlisted path(s) with %s as %s on %s.\n", len(changeSet.Paths), changeSet.Backend, changeSet.HeadCommit, changeSet.Branch))
}

func runExperimentAgent(command *cobra.Command, app *App, root *rootOptions, options *experimentAgentOptions, reference string) error {
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	info := resolved.Project
	store, err := app.NewTransactionalStore(info)
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	document, err := inventory.Resolve(reference, research.KindExperiment)
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	experiment := document.Record.(*research.Experiment)
	if err := requireExecutableExperiment(experiment); err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	request, err := experimentGitRequest(resolved, experiment, options.base, options.allow)
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	resolution, err := resolveExperimentPrepareProvider(command.Context(), app, resolved, root.workspaceBackend, request.RepositoryRoot)
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	additional, err := readBoundedInput(command.InOrStdin(), options.prompt, 4<<20)
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	contextPayload, err := json.MarshalIndent(struct {
		Task         string               `json:"task"`
		Experiment   *research.Experiment `json:"experiment"`
		Body         string               `json:"experiment_body"`
		BaseCommit   string               `json:"base_commit"`
		AllowedPaths []string             `json:"allowed_paths"`
		Instructions string               `json:"additional_instructions,omitempty"`
	}{
		Task:       "Implement only this registered experiment in the isolated worktree. Stay within allowed paths, do not edit experiments/ or Git metadata, run proportionate checks, and return the exact JSON result schema.",
		Experiment: experiment, Body: strings.TrimSpace(document.Body), BaseCommit: options.base, AllowedPaths: options.allow, Instructions: strings.TrimSpace(string(additional)),
	}, "", "  ")
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	_, config, err := agentConfig(app, options.config)
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	workspace, err := (experimentgit.Manager{Git: app.GitRunner}).Prepare(command.Context(), request)
	workspace.Backend = resolution.Actual
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", struct{}{}, false, nil, err)
	}
	agentResult, err := (agentcli.Runner{Config: config, Invoker: app.Invoker, LookupBinary: app.BinaryLookup}).Run(command.Context(), agentcli.Request{
		Role: "experiment_implementer", Profile: options.profile, Prompt: contextPayload,
		Schema: json.RawMessage(experimentAgentResultJSONSchema), CWD: workspace.CWD,
	})
	if err != nil {
		data := struct {
			Workspace experimentgit.Workspace `json:"workspace"`
			Agent     agentcli.Result         `json:"agent"`
		}{Workspace: workspace, Agent: agentResult}
		return commandFailure(app, options.json, "experiment agent", data, true, nil, err)
	}
	var report experimentAgentReport
	if err := decodeStrictJSON(agentResult.Output, &report); err != nil || report.SchemaVersion != "exp.agent.experiment-result/v1" {
		if err == nil {
			err = errors.New("agent returned the wrong experiment result schema")
		}
		return commandFailure(app, options.json, "experiment agent", struct {
			Workspace experimentgit.Workspace `json:"workspace"`
		}{workspace}, true, nil, err)
	}
	changeSet, err := (experimentgit.Manager{Git: app.GitRunner}).Commit(command.Context(), request)
	changeSet.Backend = workspacebackend.NativeGitName
	data := struct {
		Workspace experimentgit.Workspace `json:"workspace"`
		ChangeSet experimentgit.ChangeSet `json:"change_set"`
		Report    experimentAgentReport   `json:"report"`
		Profile   string                  `json:"agent_profile"`
		Model     string                  `json:"reported_model,omitempty"`
	}{Workspace: workspace, ChangeSet: changeSet, Report: report, Profile: agentResult.Profile, Model: agentResult.ReportedModel}
	if err != nil {
		return commandFailure(app, options.json, "experiment agent", data, true, nil, err)
	}
	diagnostics := []Diagnostic{}
	if !sameStringSet(report.ChangedPaths, changeSet.Paths) {
		diagnostics = append(diagnostics, Diagnostic{Severity: SeverityWarning, Code: "agent.change_report_mismatch", Message: "the committed Git diff is authoritative; agent-reported changed_paths differed"})
	}
	return commandSuccess(app, options.json, "experiment agent", data, false, diagnostics, fmt.Sprintf("Agent committed %d exact path(s) as %s on %s; human integration is still required.\n", len(changeSet.Paths), changeSet.HeadCommit, changeSet.Branch))
}

func experimentGitRequest(resolved *workspace.Context, experiment *research.Experiment, base string, allow []string) (experimentgit.Request, error) {
	if resolved == nil || resolved.Project == nil || experiment == nil {
		return experimentgit.Request{}, errors.New("experiment workspace context is incomplete")
	}
	if resolved.Source == nil {
		// Preserve the exact embedded v1 request shape and semantics.
		return experimentgit.Request{
			RepositoryRoot: resolved.Project.Repository.Root, BaseCommit: base, ExperimentID: experiment.ID,
			ExperimentTitle: experiment.Title, AllowedPathGlobs: allow,
		}, nil
	}
	if resolved.Association.Source == nil || resolved.SourceRoot == "" {
		return experimentgit.Request{}, errors.New("selected Source has no validated local association")
	}
	if resolved.Config == nil {
		return experimentgit.Request{}, errors.New("selected Source has no effective workspace configuration")
	}
	return experimentgit.Request{
		RepositoryRoot: resolved.SourceRoot, BaseCommit: base,
		ProjectID: resolved.ProjectID(), SourceID: resolved.Source.ID,
		OwnerID: experiment.ID, OwnerTitle: experiment.Title, SourceSubdir: resolved.Source.Subdir,
		RegisteredGitCommonDir: resolved.Association.Source.GitCommonDir,
		AllowedPathGlobs:       append([]string{}, allow...),
		CanonicalMetadataRoot:  experimentCanonicalMetadataRoot(resolved),
	}, nil
}

func resolveExperimentPrepareProvider(ctx context.Context, app *App, resolved *workspace.Context, explicitBackend, repositoryRoot string) (workspacebackend.Resolution, error) {
	if app == nil || app.WorkspaceRegistry == nil {
		return workspacebackend.Resolution{}, errors.New("workspace provider registry is unavailable")
	}
	var effective *config.Result
	if resolved != nil {
		effective = resolved.Config
	}
	selection, err := workspacebackend.ResolveSelection(explicitBackend, effective)
	if err != nil {
		return workspacebackend.Resolution{}, err
	}
	resolution, err := app.WorkspaceRegistry.Resolve(ctx, selection, workspacebackend.CapabilityPrepare, repositoryRoot)
	if err != nil {
		return resolution, err
	}
	if resolution.Actual != workspacebackend.NativeGitName {
		return resolution, &workspacebackend.UnsupportedCapabilityError{Provider: resolution.Actual, Capability: workspacebackend.CapabilityPrepare}
	}
	return resolution, nil
}

func experimentCanonicalMetadataRoot(resolved *workspace.Context) string {
	if resolved == nil || resolved.Project == nil || resolved.SourceRoot == "" {
		return ""
	}
	inside, err := pathx.Contains(resolved.SourceRoot, resolved.Project.Root)
	if err != nil || !inside {
		return ""
	}
	relative, err := filepath.Rel(resolved.SourceRoot, resolved.Project.Root)
	if err != nil {
		return ""
	}
	relative = filepath.ToSlash(relative)
	if relative == "." || research.ValidateCommittedPath(relative, false) != nil {
		return ""
	}
	return relative
}

func requireExecutableExperiment(experiment *research.Experiment) error {
	if experiment == nil || experiment.Lifecycle != research.LifecycleActive {
		return errors.New("experiment workspaces require an active Experiment; create a follow-up instead of modifying closed evidence")
	}
	if experiment.Design.DesignLockedAt == nil || experiment.Design.DesignDigest == "" {
		return errors.New("experiment design must be locked before preparing executable work")
	}
	return nil
}

type experimentAgentReport struct {
	SchemaVersion string   `json:"schema_version"`
	Summary       string   `json:"summary"`
	ChangedPaths  []string `json:"changed_paths"`
	Checks        []string `json:"checks"`
	FollowUps     []string `json:"follow_up_ideas"`
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	want := make(map[string]int, len(left))
	for _, value := range left {
		want[value]++
	}
	for _, value := range right {
		want[value]--
	}
	for _, count := range want {
		if count != 0 {
			return false
		}
	}
	return true
}

const experimentAgentResultJSONSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object","additionalProperties":false,
  "required":["schema_version","summary","changed_paths","checks","follow_up_ideas"],
  "properties":{
    "schema_version":{"const":"exp.agent.experiment-result/v1"},
    "summary":{"type":"string"},
    "changed_paths":{"type":"array","items":{"type":"string"},"uniqueItems":true},
    "checks":{"type":"array","items":{"type":"string"}},
    "follow_up_ideas":{"type":"array","items":{"type":"string"}}
  }
}`
