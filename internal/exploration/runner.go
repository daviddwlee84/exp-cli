package exploration

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func InspectRunner(ctx context.Context, e Execution, cwd, executable, sourceCommit string) (*RunnerIdentity, error) {
	digest, err := HashExecutable(ctx, executable)
	if err != nil {
		return nil, err
	}
	identity := &RunnerIdentity{Profile: e.RunnerName, ExecutableDigest: digest, SourceMatch: "unknown"}
	if len(e.RunnerProfile.VersionArgv) > 0 {
		argv := e.RunnerProfile.VersionArgv
		binary := argv[0]
		if strings.ContainsAny(binary, `/\`) {
			binary, err = pathx.ResolveUnderNoSymlinks(cwd, strings.TrimPrefix(binary, "./"), true)
		} else {
			binary, err = exec.LookPath(binary)
		}
		if err != nil {
			return nil, errors.New("runner version executable is unavailable")
		}
		binary, err = filepath.Abs(binary)
		if err != nil {
			return nil, err
		}
		environment, err := execx.MinimalEnvironment()
		if err != nil {
			return nil, err
		}
		result, err := execx.NewInvoker().Invoke(ctx, execx.CommandSpec{Executable: binary, Argv: argv[1:], CWD: cwd, Environment: environment, Timeout: 10 * time.Second,
			Output: execx.OutputPolicy{Mode: execx.OutputCapture, MaxStdoutBytes: 16 << 10, MaxStderrBytes: 4096}})
		if err != nil {
			return nil, errors.New("runner version probe failed; check the configured version argv")
		}
		var version struct {
			Version     string `json:"version"`
			BuildCommit string `json:"build_commit"`
		}
		if err := json.Unmarshal([]byte(result.Stdout), &version); err != nil {
			return nil, errors.New("runner version command must return a JSON object with version and optional build_commit")
		}
		if len(version.Version) > 512 || research.ValidateCommitSafeText(version.Version) != nil {
			return nil, errors.New("runner version is not bounded portable text")
		}
		identity.Version = version.Version
		if version.BuildCommit != "" {
			if len(version.BuildCommit) != 40 && len(version.BuildCommit) != 64 {
				return nil, errors.New("runner build_commit must be a full Git object ID")
			}
			for _, c := range version.BuildCommit {
				if !strings.ContainsRune("0123456789abcdef", c) {
					return nil, errors.New("runner build_commit is invalid")
				}
			}
			identity.BuildCommit = version.BuildCommit
			identity.SourceMatch = "mismatch"
			if sourceCommit == version.BuildCommit {
				identity.SourceMatch = "match"
			}
		}
	}
	if len(e.RunnerProfile.EnvironmentFiles) > 0 {
		files := []Input{}
		for _, name := range e.RunnerProfile.EnvironmentFiles {
			filename, err := pathx.ResolveUnderNoSymlinks(cwd, name, true)
			if err != nil {
				return nil, err
			}
			digest, size, err := HashFile(ctx, filename)
			if err != nil {
				return nil, err
			}
			files = append(files, Input{Name: name, Kind: "file", Digest: digest, Bytes: size})
		}
		data, _ := json.Marshal(files)
		identity.EnvironmentDigest = hashBytes(data)
	}
	return identity, nil
}

// Hash the target bytes while preserving the invocation path for runtimes
// such as Python, which discover their virtual environment from argv[0].
func HashExecutable(ctx context.Context, executable string) (string, error) {
	canonical, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	digest, _, err := HashFile(ctx, canonical)
	return digest, err
}
