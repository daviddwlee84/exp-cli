// Package mlflow provides exp's read-only attachment verification boundary.
// Workloads own MLflow run creation and logging; exp only resolves an explicit
// run ID and returns requested, sanitized fields.
package mlflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/provider"
)

type DescribeRequest struct {
	RunID        string
	MetricNames  []string
	ExpectedTags map[string]string
	Environment  execx.Environment
	CWD          string
}

type Run struct {
	RunID        string             `json:"run_id"`
	ExperimentID string             `json:"experiment_id"`
	Status       string             `json:"status"`
	ArtifactURI  string             `json:"artifact_uri,omitempty"`
	Metrics      map[string]float64 `json:"metrics"`
	Tags         map[string]string  `json:"tags"`
	Verified     bool               `json:"verified"`
	Diagnostics  []string           `json:"diagnostics"`
}

type Adapter struct {
	Invoker      execx.Invoker
	LookupBinary func(string) (string, error)
	Timeout      time.Duration
}

func (adapter Adapter) Describe(ctx context.Context, request DescribeRequest) (Run, error) {
	if !safeID(request.RunID) {
		return Run{}, errors.New("MLflow run id is invalid")
	}
	if len(request.MetricNames) == 0 && len(request.ExpectedTags) == 0 {
		return Run{}, errors.New("MLflow verification requires at least one metric or expected tag assertion")
	}
	lookup := adapter.LookupBinary
	if lookup == nil {
		lookup = exec.LookPath
	}
	binary, err := lookup("mlflow")
	if err != nil {
		return Run{}, fmt.Errorf("resolve mlflow: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return Run{}, err
	}
	cwd := request.CWD
	if cwd == "" {
		cwd = filepath.Dir(binary)
	}
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd {
		return Run{}, errors.New("MLflow cwd must be a clean absolute path")
	}
	environment := request.Environment
	if len(environment.Variables()) == 0 {
		environment, err = execx.MinimalEnvironment()
		if err != nil {
			return Run{}, err
		}
	}
	timeout := adapter.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	spec := execx.CommandSpec{
		Executable: filepath.Clean(binary), Argv: []string{"runs", "describe", "--run-id", request.RunID},
		CWD: filepath.Clean(cwd), Environment: environment, Timeout: timeout,
		Output: execx.OutputPolicy{Mode: execx.OutputCapture, MaxStdoutBytes: 16 << 20, MaxStderrBytes: 1 << 20}, Redaction: execx.NewRedactor(),
	}
	invoker := adapter.Invoker
	if invoker == nil {
		invoker = execx.NewInvoker()
	}
	result, err := invoker.Invoke(ctx, spec)
	if err != nil {
		return Run{}, err
	}
	return ParseDescribe([]byte(result.Stdout), request)
}

func ParseDescribe(data []byte, request DescribeRequest) (Run, error) {
	if len(data) == 0 || len(data) > 16<<20 {
		return Run{}, errors.New("MLflow describe output is empty or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw struct {
		Info struct {
			RunID        string `json:"run_id"`
			ExperimentID string `json:"experiment_id"`
			Status       string `json:"status"`
			ArtifactURI  string `json:"artifact_uri"`
		} `json:"info"`
		Data struct {
			Metrics map[string]float64 `json:"metrics"`
			Tags    map[string]string  `json:"tags"`
		} `json:"data"`
	}
	if err := decoder.Decode(&raw); err != nil {
		return Run{}, fmt.Errorf("decode MLflow run: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Run{}, errors.New("MLflow describe output contains trailing JSON")
	}
	run := Run{
		RunID: raw.Info.RunID, ExperimentID: raw.Info.ExperimentID, Status: raw.Info.Status,
		Metrics: map[string]float64{}, Tags: map[string]string{}, Diagnostics: []string{},
	}
	if raw.Info.ArtifactURI != "" {
		run.ArtifactURI, _ = provider.SanitizeURI(raw.Info.ArtifactURI)
		if run.ArtifactURI == "" {
			run.Diagnostics = append(run.Diagnostics, "artifact URI was omitted because it could not be sanitized")
		}
	}
	for _, name := range unique(request.MetricNames) {
		if value, found := raw.Data.Metrics[name]; found {
			run.Metrics[name] = value
		} else {
			run.Diagnostics = append(run.Diagnostics, "missing metric "+name)
		}
	}
	for name, expected := range request.ExpectedTags {
		if value, found := raw.Data.Tags[name]; found {
			run.Tags[name] = value
			if value != expected {
				run.Diagnostics = append(run.Diagnostics, "tag mismatch "+name)
			}
		} else {
			run.Diagnostics = append(run.Diagnostics, "missing tag "+name)
		}
	}
	if request.RunID != "" && run.RunID != request.RunID {
		run.Diagnostics = append(run.Diagnostics, "run id mismatch")
	}
	if run.Status != "FINISHED" {
		run.Diagnostics = append(run.Diagnostics, "run status is not FINISHED")
	}
	run.Verified = len(run.Diagnostics) == 0
	sort.Strings(run.Diagnostics)
	return run, nil
}

func safeID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._:-", character)) {
			return false
		}
	}
	return true
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, found := seen[value]; found || value == "" {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
