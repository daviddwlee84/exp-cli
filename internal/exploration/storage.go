package exploration

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

//go:embed mlflow_bridge.py
var mlflowBridge string

func mlflowCall(ctx context.Context, e Execution, request map[string]any) (map[string]string, error) {
	python, err := exec.LookPath(e.Storage.Python)
	if err != nil {
		return nil, errors.New("MLflow storage Python is unavailable; configure an installed interpreter with MLflow")
	}
	python, err = filepath.Abs(python)
	if err != nil {
		return nil, err
	}
	request["project"], request["try"], request["attempt"] = e.Project, e.Try, e.Attempt
	if e.Storage.Kind == "mlflow-local" {
		request["tracking_uri"] = "sqlite:///" + filepath.ToSlash(filepath.Join(e.Storage.Root, "mlflow.db"))
		request["artifact_location"] = (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(e.Storage.Root, "mlflow-artifacts"))}).String()
	} else {
		request["tracking_uri"] = e.Storage.TrackingURI
	}
	bindings := []execx.Binding{}
	if e.Storage.TokenEnv != "" {
		bindings = append(bindings, execx.BindSecretFromEnv("MLFLOW_TRACKING_TOKEN", e.Storage.TokenEnv))
	}
	environment, err := execx.NewEnvironment(execx.MinimalAllowlist(), bindings...)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	result, invokeErr := execx.NewInvoker().Invoke(ctx, execx.CommandSpec{Executable: python, Argv: []string{"-c", mlflowBridge}, CWD: e.Storage.Root,
		Environment: environment, Stdin: bytes.NewReader(data), Timeout: 10 * time.Minute,
		Output: execx.OutputPolicy{Mode: execx.OutputCapture, MaxStdoutBytes: 16 << 10, MaxStderrBytes: 16 << 10}})
	var response map[string]string
	if json.Unmarshal([]byte(result.Stdout), &response) != nil {
		return nil, errors.New("MLflow storage returned an invalid response; local artifacts retained")
	}
	if response["error"] == "mlflow-sqlite-dependencies-missing" {
		return nil, errors.New("local SQLite tracking requires the full mlflow package in the configured Python environment; mlflow-skinny is insufficient")
	}
	if invokeErr != nil {
		if response["error"] == "mlflow-not-installed" {
			return nil, errors.New("install MLflow in the configured Python environment before retrying exp results save")
		}
		return nil, errors.New("MLflow storage operation failed; local artifacts retained; retry exp results save after restoring the service")
	}
	return response, nil
}

// Save is independent from workload execution. Failed remote publication keeps
// verified local objects and a pending manifest. Retrying it cannot run code.
func Save(ctx context.Context, e Execution, metadata Metadata, allowLarge bool) (Metadata, error) {
	filename, err := ExecutionPath(e.Project, e.Attempt)
	if err != nil {
		return metadata, err
	}
	lockfile := filepath.Join(filepath.Dir(filename), "archive.json")
	err = localstate.WithLockedFile(ctx, lockfile, func(root *os.Root, name string) error {
		previous, err := localstate.ReadRoot(ctx, root, name, maxStateBytes)
		if err != nil {
			return err
		}
		if previous != nil {
			var saved Metadata
			if Decode(previous.Data, &saved) != nil || saved.ContextDigest != metadata.ContextDigest {
				return errors.New("artifact receipt identity mismatch")
			}
			// A published receipt is immutable; changed/new plots belong to a new Attempt.
			// Descriptions are editable canonical annotations, not provider bytes.
			for i := range saved.Artifacts {
				for _, current := range metadata.Artifacts {
					if current.Name == saved.Artifacts[i].Name {
						saved.Artifacts[i].Description = current.Description
					}
				}
			}
			metadata = saved
			if metadata.ArchiveState == "saved" {
				return nil
			}
		}
		files := metadata.Artifacts
		if len(files) == 0 {
			files, err = Scan(ctx, e.OutputDir)
			if err != nil {
				return err
			}
		}
		bounded := metadata
		bounded.Artifacts = append([]Artifact{}, files...)
		for i := range bounded.Artifacts {
			bounded.Artifacts[i].Storage = e.StorageName
			bounded.Artifacts[i].State = "remote"
			if e.Storage.Kind != "local" {
				bounded.Artifacts[i].RunID = strings.Repeat("f", 128)
			}
		}
		if data, err := json.Marshal(bounded); err != nil || len(data) > 512<<10 {
			return errors.New("artifact manifest is too large; package the outputs into an archive before saving")
		}
		var total int64
		for index := range files {
			file := &files[index]
			file.Storage = e.StorageName
			file.State = "pending"
			total += file.Bytes
			object, err := ObjectPath(e.Storage, *file)
			if err != nil {
				return err
			}
			if err := CopyVerified(ctx, filepath.Join(e.OutputDir, filepath.FromSlash(file.Name)), object, *file); err != nil {
				return err
			}
			file.State = "local"
		}
		metadata.Artifacts = files
		metadata.ArchiveState = "pending"
		publish := func() error {
			data, err := json.Marshal(metadata)
			if err != nil {
				return err
			}
			return localstate.WriteRoot(root, name, data, previous, nil)
		}
		if e.Storage.Kind != "local" && e.Storage.LargeBytes > 0 && total > e.Storage.LargeBytes && !e.AllowLarge && !allowLarge {
			if err := publish(); err != nil {
				return err
			}
			return fmt.Errorf("artifacts exceed the storage profile's %d-byte transfer threshold; review and use exp results save --allow-large", e.Storage.LargeBytes)
		}
		if e.Storage.Kind != "local" && len(files) > 0 {
			// Stage logical filenames from immutable objects for MLflow's API.
			staging, err := os.MkdirTemp(e.Storage.Root, ".upload-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(staging)
			items := []map[string]string{}
			for _, file := range files {
				object, _ := ObjectPath(e.Storage, file)
				target := filepath.Join(staging, filepath.FromSlash(file.Name))
				if err := CopyVerified(ctx, object, target, file); err != nil {
					return err
				}
				items = append(items, map[string]string{"name": file.Name, "path": target})
			}
			response, callErr := mlflowCall(ctx, e, map[string]any{"action": "save", "artifacts": items})
			if callErr != nil {
				if err := publish(); err != nil {
					return err
				}
				return callErr
			}
			runID := response["run_id"]
			if !validRunID(runID) {
				return errors.New("MLflow returned an invalid run identity")
			}
			for index := range metadata.Artifacts {
				metadata.Artifacts[index].RunID = runID
				metadata.Artifacts[index].State = "remote"
			}
		}
		metadata.ArchiveState = "saved"
		return publish()
	})
	return metadata, err
}

func validRunID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", r) {
			return false
		}
	}
	return true
}

// Availability is a local, provider-free observation. Hashes are verified when
// fetching/opening, not on every history listing.
func Availability(e *Execution, a Artifact) string {
	if e == nil {
		if a.RunID != "" {
			return "needs-profile"
		}
		return "unavailable-on-host"
	}
	object, err := ObjectPath(e.Storage, a)
	if err != nil {
		return "invalid"
	}
	info, err := os.Lstat(object)
	if err == nil && info.Mode().IsRegular() && info.Size() == a.Bytes {
		return "local"
	}
	if a.RunID != "" {
		return "download-required"
	}
	return "missing"
}

func Fetch(ctx context.Context, e Execution, a Artifact) (string, error) {
	object, err := ObjectPath(e.Storage, a)
	if err != nil {
		return "", err
	}
	digest, size, hashErr := HashFile(ctx, object)
	if hashErr == nil {
		if digest != a.Digest || size != a.Bytes {
			return "", errors.New("local artifact content differs from recorded digest")
		}
		return object, nil
	}
	if !errors.Is(hashErr, os.ErrNotExist) {
		return "", hashErr
	}
	if e.Storage.Kind == "local" || a.RunID == "" {
		return "", errors.New("artifact bytes are missing on this host")
	}
	if err := privateDirectory(e.Storage.Root); err != nil {
		return "", err
	}
	destination, err := os.MkdirTemp(e.Storage.Root, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(destination)
	response, err := mlflowCall(ctx, e, map[string]any{"action": "fetch", "run_id": a.RunID, "name": a.Name, "destination": destination})
	if err != nil {
		return "", err
	}
	local := response["path"]
	inside, err := pathx.Contains(destination, local)
	if err != nil || !inside {
		return "", errors.New("MLflow download escaped its assigned directory")
	}
	if err := CopyVerified(ctx, local, object, a); err != nil {
		return "", err
	}
	return object, nil
}
