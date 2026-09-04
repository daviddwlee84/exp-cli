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
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/provider"
)

var ErrUnavailable = errors.New("MLflow provider is unavailable")

// InvocationError preserves error classification without rendering an
// executable path, environment value, argv, or provider output.
type InvocationError struct {
	Operation string
	Err       error
}

func (failure *InvocationError) Error() string {
	if failure == nil || failure.Operation == "" {
		return ErrUnavailable.Error()
	}
	return "MLflow " + failure.Operation + " is unavailable"
}

func (failure *InvocationError) Unwrap() error {
	if failure == nil {
		return ErrUnavailable
	}
	return errors.Join(ErrUnavailable, failure.Err)
}

type DescribeRequest struct {
	RunID        string
	MetricNames  []string
	ExpectedTags map[string]string
	Environment  execx.Environment
	CWD          string
}

type Run struct {
	Profile      string             `json:"profile,omitempty"`
	Context      string             `json:"context,omitempty"`
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
	Binary       string
	Timeout      time.Duration
}

func (adapter Adapter) Describe(ctx context.Context, request DescribeRequest) (Run, error) {
	var err error
	request, err = normalizeDescribeRequest(request)
	if err != nil {
		return Run{}, err
	}
	lookup := adapter.LookupBinary
	if lookup == nil {
		lookup = exec.LookPath
	}
	binaryName := adapter.Binary
	if binaryName == "" {
		binaryName = "mlflow"
	}
	if filepath.Base(binaryName) != binaryName || strings.ContainsAny(binaryName, "/\\\x00\r\n\t ") {
		return Run{}, errors.New("MLflow binary name is invalid")
	}
	binary, err := lookup(binaryName)
	if err != nil || binary == "" {
		return Run{}, &InvocationError{Operation: "binary", Err: err}
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return Run{}, &InvocationError{Operation: "binary", Err: err}
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
		return Run{}, &InvocationError{Operation: "read-only run describe", Err: err}
	}
	return ParseDescribe([]byte(result.Stdout), request)
}

func ParseDescribe(data []byte, request DescribeRequest) (Run, error) {
	var err error
	request, err = normalizeDescribeRequest(request)
	if err != nil {
		return Run{}, err
	}
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
	if !ValidRunID(raw.Info.RunID) {
		return Run{}, errors.New("MLflow returned an invalid run identity")
	}
	run := Run{
		RunID:   raw.Info.RunID,
		Metrics: map[string]float64{}, Tags: map[string]string{}, Diagnostics: []string{},
	}
	if raw.Info.ExperimentID == "" || ValidRunID(raw.Info.ExperimentID) {
		run.ExperimentID = raw.Info.ExperimentID
	} else {
		run.Diagnostics = append(run.Diagnostics, "experiment identity was omitted because it was unsafe")
	}
	if raw.Info.Status != "" && ValidRunID(raw.Info.Status) {
		run.Status = raw.Info.Status
	} else {
		run.Diagnostics = append(run.Diagnostics, "run status was omitted because it was unsafe")
	}
	if raw.Info.ArtifactURI != "" {
		run.ArtifactURI, _ = provider.SanitizeCanonicalURI(raw.Info.ArtifactURI)
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
			safeValue := value
			if execx.SensitiveName(name) || execx.NewRedactor().Text(name+"="+value) != name+"="+value {
				safeValue = execx.Redacted
			}
			run.Tags[name] = safeValue
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

func normalizeDescribeRequest(request DescribeRequest) (DescribeRequest, error) {
	if !ValidRunID(request.RunID) {
		return DescribeRequest{}, errors.New("MLflow run id is invalid")
	}
	metrics := make([]string, 0, len(request.MetricNames))
	seen := make(map[string]struct{}, len(request.MetricNames))
	for _, name := range request.MetricNames {
		if !profileMetric.MatchString(name) || strings.Contains(name, "..") {
			return DescribeRequest{}, errors.New("MLflow metric assertion name is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		metrics = append(metrics, name)
	}
	sort.Strings(metrics)
	tags := make(map[string]string, len(request.ExpectedTags))
	for name, value := range request.ExpectedTags {
		if name == "" || name != strings.TrimSpace(name) || len(name) > 256 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") ||
			len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return DescribeRequest{}, errors.New("MLflow tag assertion is invalid")
		}
		tags[name] = value
	}
	if len(metrics) == 0 && len(tags) == 0 {
		return DescribeRequest{}, errors.New("MLflow verification requires at least one metric or expected tag assertion")
	}
	request.MetricNames = metrics
	request.ExpectedTags = tags
	return request, nil
}

// ValidRunID reports whether value is safe to persist as a provider-native run
// identity. It validates syntax only and never establishes ownership.
func ValidRunID(value string) bool {
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

// safeID is retained for package-local compatibility tests.
func safeID(value string) bool { return ValidRunID(value) }

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
