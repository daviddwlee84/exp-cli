package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/exploration"
	"github.com/spf13/cobra"
)

func explorationOutput(command *cobra.Command, app *App, data any, err error) error {
	machine, _ := command.Flags().GetBool("json")
	name := strings.TrimPrefix(command.CommandPath(), "exp ")
	partial := false
	if value, ok := data.(map[string]any); ok {
		partial, _ = value["partial"].(bool)
	}
	if err != nil {
		var mutation *explorationMutationError
		partial = partial || errors.As(err, &mutation)
		return commandFailure(app, machine, name, data, partial, nil, err)
	}
	if !machine {
		if human, ok := explorationHuman(command.CommandPath(), data); ok {
			return commandSuccess(app, false, name, data, partial, nil, human)
		}
	}
	encoded, encodeErr := json.MarshalIndent(data, "", "  ")
	if encodeErr != nil {
		return encodeErr
	}
	return commandSuccess(app, machine, name, data, partial, nil, string(encoded)+"\n")
}

type explorationMutationError struct{ cause error }

func (e *explorationMutationError) Error() string { return e.cause.Error() }
func (e *explorationMutationError) Unwrap() error { return e.cause }

func explorationHuman(command string, data any) (string, bool) {
	var out strings.Builder
	switch command {
	case "exp history search":
		value, ok := data.(map[string]any)
		if !ok {
			return "", false
		}
		cards, ok := value["results"].([]exploration.Card)
		if !ok {
			return "", false
		}
		for _, card := range cards {
			fmt.Fprintf(&out, "%s  %s  [%s]\n", card.ID, card.Title, card.State)
			fmt.Fprintf(&out, "  %s\n", briefExplorationText(card.Description))
			if card.Summary != "" {
				fmt.Fprintf(&out, "  %s\n", briefExplorationText(card.Summary))
			}
			fmt.Fprintf(&out, "  Project %s · %d artifact(s) · %s\n\n", card.Project, len(card.Artifacts), card.UpdatedAt.Format("2006-01-02"))
		}
		if len(cards) == 0 {
			out.WriteString("No matching history.\n")
		}
		if skipped, ok := value["skipped_projects"].([]string); ok && len(skipped) > 0 {
			fmt.Fprintf(&out, "Unavailable registered Projects: %s\n", strings.Join(skipped, ", "))
		}
		return out.String(), true
	case "exp results list", "exp results compare":
		artifacts, ok := data.([]artifactView)
		if !ok {
			return "", false
		}
		for _, item := range artifacts {
			fmt.Fprintf(&out, "%s  %s\n  %d bytes · %s · %s\n", item.Attempt, item.Artifact.Name, item.Artifact.Bytes, item.Artifact.Storage, item.Availability)
			if item.Artifact.Description != "" {
				fmt.Fprintf(&out, "  %s\n", briefExplorationText(item.Artifact.Description))
			}
			fmt.Fprintf(&out, "  %s\n", item.Artifact.Digest)
			if item.Runner != nil {
				fmt.Fprintf(&out, "  Runner %s · source agreement %s\n", item.Runner.Version, item.Runner.SourceMatch)
			}
		}
		if len(artifacts) == 0 {
			out.WriteString("No registered artifacts. Inspect try status for pending execution or saving.\n")
		}
		return out.String(), true
	case "exp results fetch", "exp results open":
		value, ok := data.(map[string]any)
		if !ok {
			return "", false
		}
		path, ok := value["path"].(string)
		return path + "\n", ok
	}
	return "", false
}

func briefExplorationText(value string) string {
	text := []rune(strings.Join(strings.Fields(value), " "))
	if len(text) > 180 {
		return string(text[:180]) + "…"
	}
	return string(text)
}

func explorationLeaf(use, short string, app *App, run func(*cobra.Command, []string) (any, error)) *cobra.Command {
	command := &cobra.Command{Use: use, Short: short}
	command.Flags().Bool("json", false, jsonFlagUsage)
	command.RunE = func(command *cobra.Command, args []string) error {
		data, err := run(command, args)
		return explorationOutput(command, app, data, err)
	}
	return command
}

func preferenceProject(command *cobra.Command, app *App, root *rootOptions) (string, error) {
	global, _ := command.Flags().GetBool("global")
	if global {
		return "", nil
	}
	info, _, err := openTransactionalStore(command, app, root)
	if err != nil {
		return "", err
	}
	return info.Project().ProjectID.String(), nil
}

func changePreference(ctx context.Context, project string, change func(*exploration.Preferences)) error {
	return exploration.Update(ctx, "", func(settings *exploration.Settings) error {
		if project == "" {
			change(&settings.Defaults)
		} else {
			p := settings.Projects[project]
			change(&p)
			settings.Projects[project] = p
		}
		return nil
	})
}

func newStorageCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "storage", Short: "Configure local or MLflow artifact storage and remembered preferences"}
	show := explorationLeaf("show", "Show private storage profiles and preference provenance", app, func(cmd *cobra.Command, _ []string) (any, error) {
		settings, err := exploration.Load(cmd.Context(), "")
		if err != nil {
			return nil, err
		}
		project, err := preferenceProject(cmd, app, root)
		if err != nil {
			return nil, err
		}
		prefs := settings.Preferences(project)
		origin := "global"
		if settings.Projects[project].Storage != "" {
			origin = "project"
		}
		filename, err := exploration.SettingsPath()
		return map[string]any{"settings_path": filename, "project": project, "selected": prefs.Storage, "origin": origin, "profiles": settings.Storage}, err
	})
	show.Args = cobra.NoArgs
	show.Flags().Bool("global", false, "inspect global preferences without resolving a Project")
	add := explorationLeaf("add NAME", "Save a named storage profile in private XDG configuration", app, func(cmd *cobra.Command, args []string) (any, error) {
		kind, _ := cmd.Flags().GetString("kind")
		rootPath, _ := cmd.Flags().GetString("root")
		uri, _ := cmd.Flags().GetString("tracking-uri")
		python, _ := cmd.Flags().GetString("python")
		token, _ := cmd.Flags().GetString("token-env")
		large, _ := cmd.Flags().GetInt64("large-bytes")
		if rootPath == "" {
			return nil, errors.New("storage add requires --root for artifacts or local staging")
		}
		rootPath, err := filepath.Abs(rootPath)
		if err != nil {
			return nil, err
		}
		profile := exploration.StorageProfile{Kind: kind, Root: rootPath, TrackingURI: uri, Python: python, TokenEnv: token, LargeBytes: large}
		err = exploration.Update(cmd.Context(), "", func(settings *exploration.Settings) error { settings.Storage[args[0]] = profile; return nil })
		return map[string]any{"name": args[0], "profile": profile}, err
	})
	add.Args = cobra.ExactArgs(1)
	add.Flags().String("kind", "local", "local, mlflow-local, or mlflow-remote")
	add.Flags().String("root", "", "absolute artifact or staging directory outside Git repositories")
	add.Flags().String("tracking-uri", "", "remote MLflow HTTP(S) tracking endpoint without credentials")
	add.Flags().String("python", "python3", "installed Python executable with the MLflow SDK")
	add.Flags().String("token-env", "", "parent environment variable containing the remote tracking token")
	add.Flags().Int64("large-bytes", 1<<30, "review transfers above this size; zero disables the transfer threshold")
	use := explorationLeaf("use NAME", "Remember the storage preference for this Project or globally", app, func(cmd *cobra.Command, args []string) (any, error) {
		project, err := preferenceProject(cmd, app, root)
		if err != nil {
			return nil, err
		}
		err = changePreference(cmd.Context(), project, func(p *exploration.Preferences) { p.Storage = args[0] })
		return map[string]string{"project": project, "storage": args[0]}, err
	})
	use.Args = cobra.ExactArgs(1)
	use.Flags().Bool("global", false, "save the default for all Projects without an override")
	command.AddCommand(show, add, use)
	return command
}

func newInputCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "input", Short: "Bind external data paths privately and pass named inputs to Tries"}
	bind := explorationLeaf("bind NAME PATH", "Remember an external file or directory without adding its bytes to Git", app, func(cmd *cobra.Command, args []string) (any, error) {
		project, err := preferenceProject(cmd, app, root)
		if err != nil {
			return nil, err
		}
		path, err := filepath.Abs(args[1])
		if err != nil {
			return nil, err
		}
		identity, err := exploration.InspectInput(cmd.Context(), args[0], path)
		if err != nil {
			return nil, err
		}
		err = changePreference(cmd.Context(), project, func(p *exploration.Preferences) {
			if p.Inputs == nil {
				p.Inputs = map[string]string{}
			}
			p.Inputs[args[0]] = path
		})
		return map[string]any{"project": project, "input": identity, "environment": "EXP_INPUT_" + strings.ToUpper(strings.ReplaceAll(args[0], "-", "_"))}, err
	})
	bind.Args = cobra.ExactArgs(2)
	bind.Flags().Bool("global", false, "bind the input globally")
	list := explorationLeaf("list", "List effective host-local input bindings", app, func(cmd *cobra.Command, _ []string) (any, error) {
		project, err := preferenceProject(cmd, app, root)
		if err != nil {
			return nil, err
		}
		settings, err := exploration.Load(cmd.Context(), "")
		return settings.Preferences(project).Inputs, err
	})
	list.Args = cobra.NoArgs
	list.Flags().Bool("global", false, "list global bindings")
	command.AddCommand(bind, list)
	return command
}

func newRunnerProfileCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "runner", Short: "Configure project CLI argv and machine-readable build identity"}
	add := explorationLeaf("add NAME -- COMMAND [ARG...]", "Register a portable runner and optional version/environment probes", app, func(cmd *cobra.Command, args []string) (any, error) {
		if cmd.ArgsLenAtDash() != 1 || len(args) < 2 {
			return nil, errors.New("runner add requires NAME -- COMMAND [ARG...]")
		}
		version, _ := cmd.Flags().GetStringArray("version-arg")
		files, _ := cmd.Flags().GetStringArray("environment-file")
		profile := exploration.RunnerProfile{Argv: args[1:], VersionArgv: version, EnvironmentFiles: files}
		err := exploration.Update(cmd.Context(), "", func(settings *exploration.Settings) error { settings.Runners[args[0]] = profile; return nil })
		return map[string]any{"name": args[0], "profile": profile}, err
	})
	add.Args = cobra.MinimumNArgs(2)
	add.Flags().StringArray("version-arg", nil, "one argv element of the version JSON command, including executable; repeat in order")
	add.Flags().StringArray("environment-file", nil, "Source-relative lockfile or environment manifest to hash")
	use := explorationLeaf("use NAME", "Remember a runner preference for the Project or globally", app, func(cmd *cobra.Command, args []string) (any, error) {
		project, err := preferenceProject(cmd, app, root)
		if err != nil {
			return nil, err
		}
		err = changePreference(cmd.Context(), project, func(p *exploration.Preferences) { p.Runner = args[0] })
		return map[string]string{"project": project, "runner": args[0]}, err
	})
	use.Args = cobra.ExactArgs(1)
	use.Flags().Bool("global", false, "save the global runner preference")
	list := explorationLeaf("list", "List configured portable runner profiles", app, func(cmd *cobra.Command, _ []string) (any, error) {
		s, err := exploration.Load(cmd.Context(), "")
		return s.Runners, err
	})
	list.Args = cobra.NoArgs
	command.AddCommand(add, use, list)
	return command
}

func newTryRootCommand(app *App) *cobra.Command {
	command := explorationLeaf("root [PATH]", "Inspect or set the private adhoc root; existing associations remain valid", app, func(cmd *cobra.Command, args []string) (any, error) {
		if len(args) == 1 {
			path, err := filepath.Abs(args[0])
			if err != nil {
				return nil, err
			}
			if err := exploration.Update(cmd.Context(), "", func(s *exploration.Settings) error { s.TriesRoot = path; return nil }); err != nil {
				return nil, err
			}
		}
		s, err := exploration.Load(cmd.Context(), "")
		return map[string]string{"tries_root": s.TriesRoot, "layout": "records/ and sources/; artifacts use the selected storage profile"}, err
	})
	command.Args = cobra.MaximumNArgs(1)
	return command
}

func addExplorationFlags(command *cobra.Command, options *tryRunOptions) {
	command.Flags().BoolVar(&options.outputs, "outputs", false, "save managed outputs outside Git through the configured storage profile")
	command.Flags().StringVar(&options.storage, "storage", "", "select a storage profile and enable managed outputs")
	command.Flags().StringVar(&options.runner, "runner", "", "select a runner profile and enable managed outputs")
	command.Flags().StringArrayVar(&options.inputs, "input", nil, "pass a named external data binding as EXP_INPUT_NAME (repeatable)")
	command.Flags().BoolVar(&options.allowLarge, "allow-large", false, "allow artifact transfer above the selected profile's size threshold")
}

func runnerArgv(ctx context.Context, project, selected string, argv []string) ([]string, string, error) {
	s, err := exploration.Load(ctx, "")
	if err != nil {
		return nil, "", err
	}
	if selected == "" {
		// An explicit executable is an invocation override. Bare options (or no
		// argv) belong to the remembered runner; --runner is unambiguous for
		// positional runner arguments.
		if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
			return argv, "", nil
		}
		selected = s.Preferences(project).Runner
	}
	if selected == "" {
		return argv, "", nil
	}
	profile, ok := s.Runners[selected]
	if !ok {
		return nil, "", fmt.Errorf("runner profile %s is not configured", selected)
	}
	return append(append([]string{}, profile.Argv...), argv...), selected, nil
}
