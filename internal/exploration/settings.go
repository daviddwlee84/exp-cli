// Package exploration owns host-local exploration preferences and portable
// artifact metadata. It never makes a scientific or promotion decision.
package exploration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const Namespace = "io.github.daviddwlee84.exp-cli.exploration"
const SettingsSchema = "exp.exploration-config/v1"
const maxStateBytes = 4 << 20

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var envPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
var binaryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type StorageProfile struct {
	Kind        string `json:"kind"`
	Root        string `json:"root"`
	TrackingURI string `json:"tracking_uri,omitempty"`
	Python      string `json:"python,omitempty"`
	TokenEnv    string `json:"token_env,omitempty"`
	LargeBytes  int64  `json:"large_bytes"`
}

type RunnerProfile struct {
	Argv             []string `json:"argv"`
	VersionArgv      []string `json:"version_argv,omitempty"`
	EnvironmentFiles []string `json:"environment_files,omitempty"`
}

type Preferences struct {
	Storage string            `json:"storage,omitempty"`
	Runner  string            `json:"runner,omitempty"`
	Inputs  map[string]string `json:"inputs,omitempty"`
}

// This file is private, including Project overrides. No physical path from it
// is serialized into a canonical record or copied to repository config.
type Settings struct {
	Schema    string                    `json:"schema_version"`
	TriesRoot string                    `json:"tries_root"`
	Defaults  Preferences               `json:"defaults"`
	Storage   map[string]StorageProfile `json:"storage"`
	Runners   map[string]RunnerProfile  `json:"runners"`
	Projects  map[string]Preferences    `json:"projects"`
}

func NameValid(name string) bool { return namePattern.MatchString(name) }

func DataHome() (string, error) {
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return "", errors.New("XDG_DATA_HOME must be a clean absolute path")
		}
		return value, nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".local", "share"), err
}

func SettingsPath() (string, error) {
	home, err := localstate.ConfigHome()
	return filepath.Join(home, "exp", "exploration.json"), err
}

func DefaultSettings() (Settings, error) {
	home, err := DataHome()
	if err != nil {
		return Settings{}, err
	}
	return Settings{Schema: SettingsSchema, TriesRoot: filepath.Join(home, "exp", "tries"),
		Defaults: Preferences{Storage: "local"},
		Storage:  map[string]StorageProfile{"local": {Kind: "local", Root: filepath.Join(home, "exp", "artifacts"), LargeBytes: 1 << 30}},
		Runners:  map[string]RunnerProfile{}, Projects: map[string]Preferences{}}, nil
}

func Decode(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON is not allowed")
	}
	return nil
}

func Load(ctx context.Context, filename string) (Settings, error) {
	if filename == "" {
		var err error
		filename, err = SettingsPath()
		if err != nil {
			return Settings{}, err
		}
	}
	snapshot, err := localstate.Read(ctx, filename, maxStateBytes)
	if err != nil {
		return Settings{}, err
	}
	if snapshot == nil {
		return DefaultSettings()
	}
	var settings Settings
	if err := Decode(snapshot.Data, &settings); err != nil {
		return settings, fmt.Errorf("invalid exploration settings: %w", err)
	}
	return settings, settings.Validate()
}

func Update(ctx context.Context, filename string, change func(*Settings) error) error {
	if filename == "" {
		var err error
		filename, err = SettingsPath()
		if err != nil {
			return err
		}
	}
	return localstate.WithLockedFile(ctx, filename, func(root *os.Root, name string) error {
		previous, err := localstate.ReadRoot(ctx, root, name, maxStateBytes)
		if err != nil {
			return err
		}
		settings, err := DefaultSettings()
		if err != nil {
			return err
		}
		if previous != nil {
			if err := Decode(previous.Data, &settings); err != nil {
				return err
			}
		}
		if err := settings.Validate(); err != nil {
			return err
		}
		if err := change(&settings); err != nil {
			return err
		}
		if err := settings.Validate(); err != nil {
			return err
		}
		data, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return err
		}
		return localstate.WriteRoot(root, name, append(data, '\n'), previous, nil)
	})
}

func AbsolutePath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func (s Settings) Validate() error {
	if s.Schema != SettingsSchema || !AbsolutePath(s.TriesRoot) {
		return errors.New("exploration settings require schema and absolute tries_root")
	}
	if s.Storage == nil || s.Projects == nil || s.Runners == nil {
		return errors.New("exploration storage, runners, and projects maps are required")
	}
	for name, profile := range s.Storage {
		if !NameValid(name) || !AbsolutePath(profile.Root) || profile.LargeBytes < 0 {
			return errors.New("invalid storage profile name, root, or size threshold")
		}
		switch profile.Kind {
		case "local", "mlflow-local":
			if profile.TrackingURI != "" {
				return errors.New("local storage cannot specify tracking_uri")
			}
		case "mlflow-remote":
			u, err := url.Parse(profile.TrackingURI)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return errors.New("remote tracking_uri must be an HTTP(S) endpoint without credentials or query")
			}
		default:
			return errors.New("storage kind must be local, mlflow-local, or mlflow-remote")
		}
		if profile.Kind != "local" && !binaryPattern.MatchString(profile.Python) {
			return errors.New("MLflow storage requires a Python executable basename")
		}
		if profile.TokenEnv != "" && !envPattern.MatchString(profile.TokenEnv) {
			return errors.New("token_env must be an environment variable name")
		}
	}
	for name, runner := range s.Runners {
		if !NameValid(name) || len(runner.Argv) == 0 {
			return errors.New("runner requires a name and argv")
		}
		for _, args := range [][]string{runner.Argv, runner.VersionArgv} {
			for _, arg := range args {
				if arg == "" || strings.ContainsAny(arg, "\x00\r\n") || AbsolutePath(arg) {
					return errors.New("runner argv must use portable, nonempty arguments")
				}
			}
		}
		for _, name := range runner.EnvironmentFiles {
			if err := research.ValidateCommittedPath(name, false); err != nil {
				return err
			}
		}
	}
	validatePrefs := func(p Preferences) error {
		if p.Storage != "" {
			if _, ok := s.Storage[p.Storage]; !ok {
				return errors.New("selected storage profile does not exist")
			}
		}
		if p.Runner != "" {
			if _, ok := s.Runners[p.Runner]; !ok {
				return errors.New("selected runner profile does not exist")
			}
		}
		for name, value := range p.Inputs {
			if !NameValid(name) || !AbsolutePath(value) {
				return errors.New("inputs require names and absolute local paths")
			}
		}
		return nil
	}
	if err := validatePrefs(s.Defaults); err != nil {
		return err
	}
	for id, p := range s.Projects {
		if _, err := research.ParseUUID(id); err != nil {
			return err
		}
		if err := validatePrefs(p); err != nil {
			return err
		}
	}
	return nil
}

func (s Settings) Preferences(project string) Preferences {
	p := s.Defaults
	p.Inputs = cloneMap(p.Inputs)
	if override, ok := s.Projects[project]; ok {
		if override.Storage != "" {
			p.Storage = override.Storage
		}
		if override.Runner != "" {
			p.Runner = override.Runner
		}
		for k, v := range override.Inputs {
			p.Inputs[k] = v
		}
	}
	return p
}

func cloneMap(input map[string]string) map[string]string {
	result := map[string]string{}
	for k, v := range input {
		result[k] = v
	}
	return result
}

func WritePrivate(ctx context.Context, filename string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxStateBytes {
		return errors.New("private exploration metadata exceeds size limit")
	}
	return localstate.WithLockedFile(ctx, filename, func(root *os.Root, name string) error {
		previous, err := localstate.ReadRoot(ctx, root, name, maxStateBytes)
		if err != nil {
			return err
		}
		return localstate.WriteRoot(root, name, append(data, '\n'), previous, nil)
	})
}
