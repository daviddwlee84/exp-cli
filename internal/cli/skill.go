package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/daviddwlee84/exp-cli/internal/skill"
	"github.com/spf13/cobra"
)

var errSkillDrift = errors.New("installed skill differs from this build")

type skillInstallOptions struct {
	dir  string
	link bool
	json bool
}

type skillCheckOptions struct {
	dir   string
	links bool
	json  bool
}

func newSkillCommand(app *App) *cobra.Command {
	command := &cobra.Command{
		Use:   "skill",
		Short: "Inspect or manage the embedded exp-cli guidance skill",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.AddCommand(newSkillPrintCommand(app), newSkillInstallCommand(app), newSkillCheckCommand(app), newSkillSyncCommand(app))
	return command
}

func newSkillPrintCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "print",
		Short: "Print this build's embedded SKILL.md",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runSkillPrint(command, app)
		},
	}
}

func runSkillPrint(command *cobra.Command, app *App) error {
	if err := command.Context().Err(); err != nil {
		return err
	}
	content, err := app.RenderSkill()
	if err != nil {
		return err
	}
	return app.WriteHuman(content)
}

func newSkillInstallCommand(app *App) *cobra.Command {
	options := &skillInstallOptions{}
	command := &cobra.Command{
		Use:   "install",
		Short: "Atomically install this build's embedded guidance skill",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runSkillInstall(command, app, options)
		},
	}
	command.Flags().StringVar(&options.dir, "dir", "", "set the skill destination (defaults to ~/.agents/skills/exp-cli)")
	command.Flags().BoolVar(&options.link, "link", false, "link into existing supported consumer skill directories")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newSkillCheckCommand(app *App) *cobra.Command {
	options := &skillCheckOptions{}
	command := &cobra.Command{
		Use:   "check",
		Short: "Check installed skill bytes and optional consumer links without mutation",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runSkillCheck(command, app, options)
		},
	}
	command.Flags().StringVar(&options.dir, "dir", "", "set the skill destination (defaults to ~/.agents/skills/exp-cli)")
	command.Flags().BoolVar(&options.links, "links", false, "also check existing supported consumer link locations")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func resolveSkillDestination(explicit string, resolveDefault func() (string, error)) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if resolveDefault == nil {
		return "", fmt.Errorf("default skill destination resolver is not configured")
	}
	destination, err := resolveDefault()
	if err != nil {
		return "", err
	}
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return "", fmt.Errorf("default skill destination must be a clean absolute path")
	}
	return destination, nil
}

func runSkillInstall(command *cobra.Command, app *App, options *skillInstallOptions) error {
	destination, err := resolveSkillDestination(options.dir, app.ResolveDefaultSkillDir)
	if err != nil {
		return commandFailure(app, options.json, "skill install", stableInstallResult(skill.InstallResult{}), false, nil, err)
	}
	result, err := app.InstallSkill(command.Context(), destination, options.link)
	result = stableInstallResult(result)
	if err != nil {
		partial := result.Changed || len(result.Written) > 0 || len(result.Created) > 0 || len(result.Updated) > 0 ||
			len(result.CreatedDirectories) > 0 || len(result.RepairedDirectories) > 0 || len(result.RemovedTemporaryFiles) > 0 || len(result.Links) > 0
		return commandFailure(app, options.json, "skill install", result, partial, nil, err)
	}
	human := fmt.Sprintf("Skill %s at %s; changed=%t, files_written=%d, links_changed=%d\n",
		skill.Name, result.Dir, result.Changed, len(result.Written), len(result.Links))
	return commandSuccess(app, options.json, "skill install", result, false, nil, human)
}

func runSkillCheck(command *cobra.Command, app *App, options *skillCheckOptions) error {
	destination, err := resolveSkillDestination(options.dir, app.ResolveDefaultSkillDir)
	if err != nil {
		return commandFailure(app, options.json, "skill check", stableCheckResult(skill.CheckResult{}), false, nil, err)
	}
	result, err := app.CheckSkill(command.Context(), destination, skill.CheckOptions{Links: options.links})
	result = stableCheckResult(result)
	if err != nil {
		return commandFailure(app, options.json, "skill check", result, false, nil, err)
	}
	if !result.Current {
		diagnostic := Diagnostic{
			Severity: SeverityError,
			Code:     "skill.drift",
			Message:  "installed skill files, compatibility metadata, or requested consumer links differ from this build",
			Path:     result.Dir,
		}
		if options.json {
			return commandFailure(app, true, "skill check", result, false, []Diagnostic{diagnostic}, errSkillDrift)
		}
		if writeErr := app.WriteHuman(safeHumanOutput(fmt.Sprintf("Skill drift at %s: missing=%d drifted=%d directory_modes_current=%t drifted_directories=%d links_current=%t\n",
			result.Dir, len(result.MissingFiles), len(result.DriftedFiles), result.DirectoryModesCurrent, len(result.DriftedDirectories), result.LinksCurrent))); writeErr != nil {
			return writeErr
		}
		return errSkillDrift
	}
	human := fmt.Sprintf("Skill %s is current at %s\n", skill.Name, result.Dir)
	return commandSuccess(app, options.json, "skill check", result, false, nil, human)
}

func stableInstallResult(result skill.InstallResult) skill.InstallResult {
	if result.CreatedDirectories == nil {
		result.CreatedDirectories = []string{}
	}
	if result.RepairedDirectories == nil {
		result.RepairedDirectories = []string{}
	}
	if result.RemovedTemporaryFiles == nil {
		result.RemovedTemporaryFiles = []string{}
	}
	if result.Written == nil {
		result.Written = []string{}
	}
	if result.Created == nil {
		result.Created = []string{}
	}
	if result.Updated == nil {
		result.Updated = []string{}
	}
	if result.Skipped == nil {
		result.Skipped = []string{}
	}
	if result.Files == nil {
		result.Files = []skill.InstalledFile{}
	}
	if result.Links == nil {
		result.Links = []string{}
	}
	if result.LinkResults == nil {
		result.LinkResults = []skill.ConsumerLinkResult{}
	}
	return result
}

func stableCheckResult(result skill.CheckResult) skill.CheckResult {
	if result.DriftedDirectories == nil {
		result.DriftedDirectories = []string{}
	}
	if result.CurrentFiles == nil {
		result.CurrentFiles = []string{}
	}
	if result.MissingFiles == nil {
		result.MissingFiles = []string{}
	}
	if result.DriftedFiles == nil {
		result.DriftedFiles = []string{}
	}
	if result.UnknownFiles == nil {
		result.UnknownFiles = []string{}
	}
	if result.Files == nil {
		result.Files = []skill.CheckedFile{}
	}
	if result.Links == nil {
		result.Links = []skill.ConsumerLinkResult{}
	}
	return result
}
