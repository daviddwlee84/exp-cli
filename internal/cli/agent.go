package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/spf13/cobra"
)

type agentOptions struct {
	config  string
	json    bool
	role    string
	profile string
	prompt  string
	schema  string
	cwd     string
}

type agentProfilesData struct {
	Config   string            `json:"config"`
	Roles    map[string]string `json:"roles"`
	Profiles []string          `json:"profiles"`
}

type agentRunData struct {
	Profile string            `json:"profile"`
	Model   string            `json:"model,omitempty"`
	Output  json.RawMessage   `json:"output"`
	Command execx.CommandView `json:"command"`
	Process execx.Result      `json:"process"`
}

func newAgentCommand(app *App) *cobra.Command {
	command := &cobra.Command{Use: "agent", Short: "Inspect or run configured fresh agent CLI profiles", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newAgentProfilesCommand(app), newAgentRunCommand(app))
	return command
}

func newAgentProfilesCommand(app *App) *cobra.Command {
	options := &agentOptions{}
	command := &cobra.Command{
		Use: "profiles", Short: "Validate and list local agent CLI profiles", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runAgentProfiles(command, app, options) },
	}
	command.Flags().StringVar(&options.config, "config", "", "agent profile TOML path (default: $XDG_CONFIG_HOME/exp/agents.toml)")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newAgentRunCommand(app *App) *cobra.Command {
	options := &agentOptions{}
	command := &cobra.Command{
		Use: "run", Short: "Run one fresh agent CLI with a strict JSON output contract", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runAgent(command, app, options) },
	}
	flags := command.Flags()
	flags.StringVar(&options.config, "config", "", "agent profile TOML path (default: $XDG_CONFIG_HOME/exp/agents.toml)")
	flags.StringVar(&options.role, "role", "", "configured agent role")
	flags.StringVar(&options.profile, "profile", "", "override the role's configured profile")
	flags.StringVar(&options.prompt, "prompt", "-", "prompt file path or - for stdin")
	flags.StringVar(&options.schema, "schema", "", "JSON Schema file for the final response")
	flags.StringVar(&options.cwd, "cwd", "", "agent working directory (default: current directory)")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	_ = command.MarkFlagRequired("role")
	_ = command.MarkFlagRequired("schema")
	return command
}

func agentConfig(app *App, explicit string) (string, agentcli.Config, error) {
	path := explicit
	var err error
	if path == "" {
		path, err = app.ResolveAgentConfigPath()
		if err != nil {
			return "", agentcli.Config{}, err
		}
	}
	if !filepath.IsAbs(path) {
		path, err = filepath.Abs(path)
		if err != nil {
			return "", agentcli.Config{}, err
		}
	}
	path = filepath.Clean(path)
	config, err := app.LoadAgentConfig(path)
	return path, config, err
}

func runAgentProfiles(command *cobra.Command, app *App, options *agentOptions) error {
	path, config, err := agentConfig(app, options.config)
	data := agentProfilesData{Config: path, Roles: map[string]string{}, Profiles: []string{}}
	if err != nil {
		return commandFailure(app, options.json, "agent profiles", data, false, nil, err)
	}
	for role, profile := range config.Roles {
		data.Roles[role] = profile
	}
	for profile := range config.Profiles {
		data.Profiles = append(data.Profiles, profile)
	}
	sort.Strings(data.Profiles)
	human := fmt.Sprintf("Agent profiles: %d; roles: %d; config: %s\n", len(data.Profiles), len(data.Roles), path)
	return commandSuccess(app, options.json, "agent profiles", data, false, nil, human)
}

func runAgent(command *cobra.Command, app *App, options *agentOptions) error {
	_, config, err := agentConfig(app, options.config)
	if err != nil {
		return commandFailure(app, options.json, "agent run", agentRunData{}, false, nil, err)
	}
	prompt, err := readBoundedInput(app.In, options.prompt, 4<<20)
	if err != nil {
		return commandFailure(app, options.json, "agent run", agentRunData{}, false, nil, err)
	}
	schema, err := readBoundedInput(app.In, options.schema, 1<<20)
	if err != nil {
		return commandFailure(app, options.json, "agent run", agentRunData{}, false, nil, err)
	}
	cwd := options.cwd
	if cwd == "" {
		cwd, err = app.Getwd()
	}
	if err != nil {
		return commandFailure(app, options.json, "agent run", agentRunData{}, false, nil, err)
	}
	if !filepath.IsAbs(cwd) {
		cwd, err = filepath.Abs(cwd)
		if err != nil {
			return commandFailure(app, options.json, "agent run", agentRunData{}, false, nil, err)
		}
	}
	runner := agentcli.Runner{Config: config, Invoker: app.Invoker, LookupBinary: app.BinaryLookup}
	result, err := runner.Run(command.Context(), agentcli.Request{
		Role: options.role, Profile: options.profile, Prompt: prompt, Schema: schema, CWD: filepath.Clean(cwd),
	})
	data := agentRunData{Profile: result.Profile, Model: result.ReportedModel, Output: result.Output, Command: result.Command, Process: result.Process}
	if err != nil {
		return commandFailure(app, options.json, "agent run", data, false, nil, err)
	}
	if options.json {
		return commandSuccess(app, true, "agent run", data, false, nil, "")
	}
	return app.WriteHuman(string(result.Output) + "\n")
}

func readBoundedInput(stdin io.Reader, path string, limit int64) ([]byte, error) {
	if path == "" {
		return nil, errors.New("input path is required")
	}
	var reader io.Reader
	var close func() error
	if path == "-" {
		reader = stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		reader, close = file, file.Close
	}
	if close != nil {
		defer close()
	}
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return content, nil
}
