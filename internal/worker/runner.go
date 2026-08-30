// Package worker executes one private operational job without interpreting a
// shell string and publishes a durable terminal marker before updating the
// operational database.
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

const (
	JobSchema              = "exp.worker-job/v1"
	TerminalSchema         = "exp.worker-terminal/v1"
	ResultSchema           = "exp.worker-result/v1"
	maxTerminalBytes       = 64 << 10
	maxWorkloadResultBytes = 768 << 10
	maxExpectedOutputs     = 256
	maxExpectedOutputBytes = 512 << 20
	maxExpectedTotalBytes  = 1 << 30
	maxExpectedHashTime    = 2 * time.Minute
)

type Workload struct {
	Schema            string   `json:"schema_version"`
	AttemptID         string   `json:"attempt_id"`
	Executable        string   `json:"executable"`
	Args              []string `json:"args"`
	CWD               string   `json:"cwd"`
	Timeout           string   `json:"timeout"`
	AllowedEnv        []string `json:"allowed_env"`
	SecretEnv         []string `json:"secret_env"`
	ResultFile        string   `json:"result_file,omitempty"`
	RepositoryRoot    string   `json:"repository_root,omitempty"`
	BaseCommit        string   `json:"base_commit"`
	HeadCommit        string   `json:"head_commit"`
	ChangeSet         []string `json:"change_set"`
	RuntimeConfigPath string   `json:"runtime_config_path,omitempty"`
	ExpectedOutputs   []string `json:"expected_outputs"`
}

type Terminal struct {
	Schema       string             `json:"schema_version"`
	JobID        string             `json:"job_id"`
	AttemptID    string             `json:"attempt_id"`
	FencingToken int64              `json:"fencing_token"`
	State        operation.JobState `json:"state"`
	ExitCode     int                `json:"exit_code"`
	StartedAt    time.Time          `json:"started_at"`
	EndedAt      time.Time          `json:"ended_at"`
	TimedOut     bool               `json:"timed_out"`
	Cancelled    bool               `json:"cancelled"`
	ResultSHA256 string             `json:"result_sha256,omitempty"`
	ResultSize   int64              `json:"result_size,omitempty"`
	Outputs      map[string]string  `json:"outputs,omitempty"`
}

// JobResult is the exact durable operational result. Keeping terminal facts in
// the database lets the controller import timeout/timing/exit observations
// even when the worker process has already exited.
type JobResult struct {
	Schema   string          `json:"schema_version"`
	Result   json.RawMessage `json:"result"`
	Terminal Terminal        `json:"terminal"`
}

type Store interface {
	FinishJob(context.Context, string, int64, operation.JobState, json.RawMessage, string) (operation.Job, error)
}

type Runner struct {
	Store      Store
	Invoker    execx.Invoker
	Git        gitx.Runner
	MarkerRoot string
	Clock      func() time.Time
}

func (runner Runner) Run(ctx context.Context, job operation.Job) (Terminal, error) {
	if runner.Store == nil {
		return Terminal{}, errors.New("worker store is required")
	}
	if job.FencingToken <= 0 {
		return Terminal{}, errors.New("worker requires a positive fencing token")
	}
	workload, err := decodeWorkload(job.Payload)
	if err != nil {
		return Terminal{}, err
	}
	markerRootPath, markerRoot, err := secureRoot(runner.MarkerRoot)
	if err != nil {
		return Terminal{}, err
	}
	defer markerRoot.Close()
	markerName := jobArtifactBase(job.ID) + ".json"
	resultName := jobArtifactBase(job.ID) + ".result.json"
	if existing, found, err := readTerminal(ctx, markerRoot, markerName); err != nil {
		return Terminal{}, err
	} else if found {
		if existing.JobID == job.ID && existing.AttemptID == workload.AttemptID && existing.FencingToken == job.FencingToken && terminalJobState(existing.State) {
			resultPayload, recoveryErr := recoveredResultPayload(ctx, markerRoot, resultName, existing)
			if recoveryErr != nil {
				return Terminal{}, recoveryErr
			}
			if job.State == operation.JobRunning {
				if _, finishErr := runner.Store.FinishJob(ctx, job.ID, job.FencingToken, existing.State, resultPayload, "recovered from durable terminal marker"); finishErr != nil && !errors.Is(finishErr, operation.ErrFenced) {
					return existing, fmt.Errorf("repair operational state from terminal marker: %w", finishErr)
				}
			} else if job.State != existing.State {
				return Terminal{}, errors.New("terminal marker disagrees with operational job state")
			}
			return existing, nil
		}
		return Terminal{}, errors.New("terminal marker belongs to a different job claim")
	}
	if job.State != operation.JobRunning {
		return Terminal{}, errors.New("worker requires a claimed running job when no terminal marker exists")
	}
	if err := verifyWorkloadGit(ctx, runner.Git, workload); err != nil {
		return Terminal{}, fmt.Errorf("verify worker Git identity: %w", err)
	}

	environment, _, err := workloadEnvironment(workload, job, markerRootPath)
	if err != nil {
		return Terminal{}, err
	}
	resultIdentity, err := prepareResultFile(markerRoot, resultName)
	if err != nil {
		return Terminal{}, err
	}
	timeout := time.Duration(0)
	if workload.Timeout != "" {
		timeout, err = time.ParseDuration(workload.Timeout)
		if err != nil || timeout <= 0 {
			return Terminal{}, errors.New("worker timeout must be a positive duration")
		}
	}
	clock := runner.Clock
	if clock == nil {
		clock = time.Now
	}
	invoker := runner.Invoker
	if invoker == nil {
		invoker = execx.NewInvoker()
	}
	spec := execx.CommandSpec{
		Executable: workload.Executable, Argv: append([]string(nil), workload.Args...), CWD: workload.CWD,
		Environment: environment, Timeout: timeout, Output: execx.DefaultOutputPolicy(execx.OutputCapture),
		Redaction: execx.NewRedactor(),
	}
	started := clock().UTC()
	invokeContext, cancelInvoke := context.WithCancel(ctx)
	resultMonitor := monitorResultFile(invokeContext, markerRoot, resultName, resultIdentity, maxWorkloadResultBytes, cancelInvoke)
	process, invokeErr := invoker.Invoke(invokeContext, spec)
	finalResultErr := validateLiveResultFile(markerRoot, resultName, resultIdentity, maxWorkloadResultBytes)
	cancelInvoke()
	monitorErr := <-resultMonitor
	if finalResultErr != nil {
		monitorErr = finalResultErr
	}
	if monitorErr != nil {
		invokeErr = monitorErr
		process.Canceled = false
		process.TimedOut = false
		resultIdentity, err = resetResultFile(markerRoot, resultName)
		if err != nil {
			return Terminal{}, fmt.Errorf("reset unsafe worker result: %w", err)
		}
	}
	ended := clock().UTC()
	state := operation.JobSucceeded
	message := ""
	if invokeErr != nil {
		state = operation.JobFailed
		message = invokeErr.Error()
		if process.TimedOut {
			state = operation.JobFailed
		} else if process.Canceled {
			state = operation.JobCancelled
		}
	}
	terminal := Terminal{
		Schema: TerminalSchema, JobID: job.ID, AttemptID: workload.AttemptID, FencingToken: job.FencingToken,
		State: state, ExitCode: process.ExitCode, StartedAt: started, EndedAt: ended,
		TimedOut: process.TimedOut, Cancelled: process.Canceled,
	}
	workloadResult := json.RawMessage(`{}`)
	content, _, readErr := pathx.ReadBoundedRegularFile(ctx, markerRoot, resultName, maxWorkloadResultBytes)
	if readErr != nil || resultIdentity == nil {
		state, terminal.State, message = operation.JobFailed, operation.JobFailed, "worker result file could not be read safely"
	} else if current, statErr := markerRoot.Lstat(resultName); statErr != nil || !os.SameFile(resultIdentity, current) {
		state, terminal.State, message = operation.JobFailed, operation.JobFailed, "worker result file identity changed"
	} else if len(content) > 0 {
		if !json.Valid(content) {
			state, terminal.State, message = operation.JobFailed, operation.JobFailed, "worker result file is invalid"
		} else {
			digest := sha256.Sum256(content)
			terminal.ResultSHA256 = "sha256:" + hex.EncodeToString(digest[:])
			terminal.ResultSize = int64(len(content))
			workloadResult = append(json.RawMessage(nil), content...)
		}
	}
	freezeContent := content
	if state == operation.JobFailed && (readErr != nil || len(content) > 0 && !json.Valid(content)) {
		freezeContent = nil
	}
	if _, err := freezeResultFile(markerRoot, resultName, freezeContent); err != nil {
		return Terminal{}, fmt.Errorf("freeze durable worker result: %w", err)
	}
	if state == operation.JobSucceeded {
		hashContext, cancelHash := context.WithTimeout(ctx, maxExpectedHashTime)
		outputs, outputErr := verifyExpectedOutputs(hashContext, workload)
		cancelHash()
		if outputErr != nil {
			state, terminal.State, message = operation.JobFailed, operation.JobFailed, outputErr.Error()
		} else {
			terminal.Outputs = outputs
		}
	}
	terminal.State = state
	if err := pathx.VerifyRootPath(markerRootPath, markerRoot); err != nil {
		return Terminal{}, fmt.Errorf("worker marker root changed during execution: %w", err)
	}
	if err := writeTerminal(markerRoot, markerName, terminal); err != nil {
		return Terminal{}, err
	}
	resultPayload, err := encodeJobResult(workloadResult, terminal)
	if err != nil {
		return Terminal{}, err
	}
	if _, err := runner.Store.FinishJob(ctx, job.ID, job.FencingToken, state, resultPayload, message); err != nil {
		return terminal, fmt.Errorf("terminal marker published but operational state update failed: %w", err)
	}
	return terminal, nil
}

func validateLiveResultFile(root *os.Root, name string, expected os.FileInfo, limit int64) error {
	info, err := root.Lstat(name)
	if err != nil || expected == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return errors.New("worker result file identity changed during execution")
	}
	if info.Size() > limit {
		return fmt.Errorf("worker result exceeds %d bytes", limit)
	}
	return nil
}

func recoveredResultPayload(ctx context.Context, markerRoot *os.Root, resultName string, terminal Terminal) (json.RawMessage, error) {
	workloadResult, err := readDurableResult(ctx, markerRoot, resultName, terminal)
	if err != nil {
		return nil, err
	}
	return encodeJobResult(workloadResult, terminal)
}

func terminalJobState(state operation.JobState) bool {
	return state == operation.JobSucceeded || state == operation.JobFailed || state == operation.JobCancelled
}

func readDurableResult(ctx context.Context, root *os.Root, name string, terminal Terminal) (json.RawMessage, error) {
	content, _, err := pathx.ReadBoundedRegularFile(ctx, root, name, maxWorkloadResultBytes)
	if err != nil {
		return nil, fmt.Errorf("read durable worker result: %w", err)
	}
	if terminal.ResultSHA256 == "" {
		if terminal.ResultSize != 0 || len(content) != 0 {
			return nil, errors.New("worker terminal omits a digest for a non-empty result")
		}
		return json.RawMessage(`{}`), nil
	}
	if terminal.ResultSize <= 0 || int64(len(content)) != terminal.ResultSize || !json.Valid(content) {
		return nil, errors.New("durable worker result is missing, changed, or invalid")
	}
	digest := sha256.Sum256(content)
	if "sha256:"+hex.EncodeToString(digest[:]) != terminal.ResultSHA256 {
		return nil, errors.New("durable worker result digest does not match terminal marker")
	}
	return append(json.RawMessage(nil), content...), nil
}

func encodeJobResult(result json.RawMessage, terminal Terminal) (json.RawMessage, error) {
	if len(result) == 0 || !json.Valid(result) {
		return nil, errors.New("worker result payload is invalid")
	}
	encoded, err := json.Marshal(JobResult{Schema: ResultSchema, Result: result, Terminal: terminal})
	return json.RawMessage(encoded), err
}

// DecodeJobResult validates the exact result envelope persisted by Runner.
func DecodeJobResult(payload []byte) (JobResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var result JobResult
	if err := decoder.Decode(&result); err != nil {
		return JobResult{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return JobResult{}, errors.New("worker result contains trailing JSON")
	}
	if result.Schema != ResultSchema || result.Terminal.Schema != TerminalSchema || len(result.Result) == 0 || !json.Valid(result.Result) {
		return JobResult{}, errors.New("worker result schema or payload is invalid")
	}
	return result, nil
}

func jobArtifactBase(jobID string) string {
	digest := sha256.Sum256([]byte(jobID))
	return "job-" + hex.EncodeToString(digest[:16])
}

func decodeWorkload(payload []byte) (Workload, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var workload Workload
	if err := decoder.Decode(&workload); err != nil {
		return Workload{}, fmt.Errorf("decode worker job: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Workload{}, errors.New("worker job contains trailing JSON")
	}
	if workload.Schema != JobSchema || workload.AttemptID == "" {
		return Workload{}, errors.New("worker job schema or attempt id is invalid")
	}
	if !filepath.IsAbs(workload.Executable) || filepath.Clean(workload.Executable) != workload.Executable {
		return Workload{}, errors.New("worker executable must be a clean absolute path")
	}
	if !filepath.IsAbs(workload.CWD) || filepath.Clean(workload.CWD) != workload.CWD {
		return Workload{}, errors.New("worker cwd must be a clean absolute path")
	}
	if workload.RepositoryRoot == "" {
		workload.RepositoryRoot = workload.CWD
	}
	if workload.ResultFile != "" {
		return Workload{}, errors.New("worker result_file is reserved; exp assigns a hashed private path")
	}
	if !filepath.IsAbs(workload.RepositoryRoot) || filepath.Clean(workload.RepositoryRoot) != workload.RepositoryRoot {
		return Workload{}, errors.New("worker repository_root must be a clean absolute path")
	}
	canonicalRepository, err := pathx.Canonical(workload.RepositoryRoot)
	if err != nil {
		return Workload{}, fmt.Errorf("canonicalize worker repository_root: %w", err)
	}
	workload.RepositoryRoot = canonicalRepository
	inside, err := pathx.Contains(workload.RepositoryRoot, workload.CWD)
	if err != nil || !inside {
		return Workload{}, errors.New("worker cwd must remain inside repository_root")
	}
	if workload.BaseCommit == "" || workload.HeadCommit == "" {
		return Workload{}, errors.New("worker base_commit and head_commit are required")
	}
	if workload.RuntimeConfigPath != "" {
		if err := pathx.ValidateRelativePOSIX(workload.RuntimeConfigPath, false); err != nil {
			return Workload{}, fmt.Errorf("worker runtime_config_path: %w", err)
		}
	}
	previous := ""
	for _, changed := range workload.ChangeSet {
		if err := pathx.ValidateRelativePOSIX(changed, false); err != nil {
			return Workload{}, fmt.Errorf("worker change_set path %q: %w", changed, err)
		}
		if previous != "" && changed <= previous {
			return Workload{}, errors.New("worker change_set must be sorted and unique")
		}
		previous = changed
	}
	if len(workload.ExpectedOutputs) > maxExpectedOutputs {
		return Workload{}, fmt.Errorf("worker expected_outputs exceeds %d entries", maxExpectedOutputs)
	}
	pathBytes := 0
	for _, output := range workload.ExpectedOutputs {
		if err := pathx.ValidateRelativePOSIX(output, false); err != nil {
			return Workload{}, fmt.Errorf("worker expected output %q: %w", output, err)
		}
		pathBytes += len(output)
	}
	if pathBytes > maxTerminalBytes/2 {
		return Workload{}, errors.New("worker expected output paths are too large for a durable terminal marker")
	}
	for _, argument := range workload.Args {
		if !utf8.ValidString(argument) || strings.ContainsRune(argument, 0) {
			return Workload{}, errors.New("worker args contain invalid text")
		}
	}
	return workload, nil
}

func verifyExpectedOutputs(ctx context.Context, workload Workload) (map[string]string, error) {
	repositoryRoot, err := os.OpenRoot(workload.RepositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("open expected-output repository root: %w", err)
	}
	defer repositoryRoot.Close()
	outputs := make(map[string]string, len(workload.ExpectedOutputs))
	var totalBytes int64
	for _, relative := range workload.ExpectedOutputs {
		file, info, err := pathx.OpenRegularFileNoFollow(repositoryRoot, relative)
		if err != nil {
			return nil, fmt.Errorf("read expected output %s: %w", relative, err)
		}
		if info.Size() < 0 || info.Size() > maxExpectedOutputBytes || totalBytes > maxExpectedTotalBytes-info.Size() {
			_ = file.Close()
			return nil, fmt.Errorf("expected output %s exceeds the worker hash budget", relative)
		}
		totalBytes += info.Size()
		hash := sha256.New()
		copied, copyErr := io.CopyN(hash, contextReader{ctx: ctx, reader: file}, info.Size())
		if copyErr == nil && copied != info.Size() {
			copyErr = io.ErrUnexpectedEOF
		}
		afterOpen, statErr := file.Stat()
		afterPath, pathErr := repositoryRoot.Lstat(relative)
		closeErr := file.Close()
		if copyErr != nil || statErr != nil || pathErr != nil || closeErr != nil ||
			!os.SameFile(info, afterOpen) || !os.SameFile(info, afterPath) || afterPath.Mode()&os.ModeSymlink != 0 ||
			afterOpen.Size() != info.Size() || !afterOpen.ModTime().Equal(info.ModTime()) {
			return nil, fmt.Errorf("hash expected output %s: %w", relative, errors.Join(copyErr, statErr, pathErr, closeErr, pathx.ErrNotRegular))
		}
		outputs[relative] = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	}
	if err := pathx.VerifyRootPath(workload.RepositoryRoot, repositoryRoot); err != nil {
		return nil, fmt.Errorf("expected-output repository root changed: %w", err)
	}
	return outputs, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(buffer)
	}
}

func workloadEnvironment(workload Workload, job operation.Job, markerRoot string) (execx.Environment, string, error) {
	resultPath := filepath.Join(markerRoot, jobArtifactBase(job.ID)+".result.json")
	if !filepath.IsAbs(resultPath) || filepath.Clean(resultPath) != resultPath {
		return execx.Environment{}, "", errors.New("worker result path must be a clean absolute path")
	}
	bindings := []execx.Binding{
		execx.Bind("EXP_JOB_ID", job.ID),
		execx.Bind("EXP_ATTEMPT_ID", workload.AttemptID),
		execx.Bind("EXP_RESULT_PATH", resultPath),
	}
	for _, name := range workload.SecretEnv {
		bindings = append(bindings, execx.BindSecretFromEnv(name, name))
	}
	allowed := append(execx.MinimalAllowlist(), workload.AllowedEnv...)
	environment, err := execx.NewEnvironment(unique(allowed), bindings...)
	return environment, resultPath, err
}

func secureRoot(path string) (string, *os.Root, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", nil, errors.New("worker marker root must be absolute")
	}
	path = filepath.Clean(path)
	requested := path
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	if filepath.Clean(canonical) != requested {
		return "", nil, errors.New("worker marker root path contains a symlink")
	}
	path = filepath.Clean(canonical)
	root, err := pathx.OpenCanonicalRootNoSymlinks(path)
	if err != nil {
		return "", nil, errors.New("worker marker root must already be a real private directory")
	}
	if err := root.Chmod(".", 0o700); err != nil {
		_ = root.Close()
		return "", nil, err
	}
	if err := pathx.VerifyRootPath(path, root); err != nil {
		_ = root.Close()
		return "", nil, err
	}
	return path, root, nil
}

func prepareResultFile(root *os.Root, name string) (os.FileInfo, error) {
	if current, err := root.Lstat(name); err == nil {
		if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
			return nil, errors.New("existing worker result is not a regular file")
		}
		if err := root.Remove(name); err != nil {
			return nil, err
		}
		if err := pathx.SyncRoot(root); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	syncErr := file.Sync()
	closeErr := file.Close()
	if statErr != nil || syncErr != nil || closeErr != nil || !info.Mode().IsRegular() {
		return nil, errors.Join(statErr, syncErr, closeErr)
	}
	if err := root.Chmod(name, 0o600); err != nil {
		return nil, err
	}
	return info, pathx.SyncRoot(root)
}

func monitorResultFile(ctx context.Context, root *os.Root, name string, expected os.FileInfo, limit int64, cancel context.CancelFunc) <-chan error {
	result := make(chan error, 1)
	go func() {
		defer close(result)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				result <- nil
				return
			case <-ticker.C:
				info, err := root.Lstat(name)
				if err != nil || expected == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(expected, info) {
					cancel()
					result <- errors.New("worker result file identity changed during execution")
					return
				}
				if info.Size() > limit {
					cancel()
					result <- fmt.Errorf("worker result exceeds %d bytes", limit)
					return
				}
			}
		}
	}()
	return result
}

func freezeResultFile(root *os.Root, name string, content []byte) (os.FileInfo, error) {
	temporary := name + ".freeze"
	if err := root.Remove(temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = root.Remove(temporary)
		}
	}()
	if len(content) > 0 {
		if _, err := file.Write(content); err != nil {
			return nil, err
		}
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := root.Rename(temporary, name); err != nil {
		return nil, err
	}
	cleanup = false
	if err := pathx.SyncRoot(root); err != nil {
		return nil, err
	}
	return root.Lstat(name)
}

func resetResultFile(root *os.Root, name string) (os.FileInfo, error) {
	if _, err := root.Lstat(name); err == nil {
		if err := root.Remove(name); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return prepareResultFile(root, name)
}

func writeTerminal(root *os.Root, name string, terminal Terminal) error {
	content, err := json.MarshalIndent(terminal, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if len(content) > maxTerminalBytes {
		return fmt.Errorf("worker terminal marker exceeds %d bytes", maxTerminalBytes)
	}
	temporary := name + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = root.Remove(temporary)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if _, err := root.Lstat(name); err == nil {
		return errors.New("worker terminal marker appeared during execution")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	cleanup = false
	return pathx.SyncRoot(root)
}

func readTerminal(ctx context.Context, root *os.Root, name string) (Terminal, bool, error) {
	content, _, err := pathx.ReadBoundedRegularFile(ctx, root, name, maxTerminalBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return Terminal{}, false, nil
	}
	if err != nil {
		return Terminal{}, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var terminal Terminal
	if err := decoder.Decode(&terminal); err != nil || terminal.Schema != TerminalSchema {
		return Terminal{}, false, errors.New("existing worker terminal marker is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Terminal{}, false, errors.New("existing worker terminal marker has trailing data")
	}
	return terminal, true, nil
}

// LoadTerminal reads one identity-safe durable marker without requiring the
// operational database. Canonical reconciliation uses this after DB loss or a
// failed FinishJob call; absence is not an error.
func LoadTerminal(ctx context.Context, markerRoot, jobID string) (Terminal, bool, error) {
	if markerRoot == "" || !filepath.IsAbs(markerRoot) || jobID == "" {
		return Terminal{}, false, errors.New("worker marker root and job id are required")
	}
	requested := filepath.Clean(markerRoot)
	canonical, err := filepath.EvalSymlinks(requested)
	if errors.Is(err, fs.ErrNotExist) {
		return Terminal{}, false, nil
	}
	if err != nil {
		return Terminal{}, false, err
	}
	if filepath.Clean(canonical) != requested {
		return Terminal{}, false, errors.New("worker marker root path contains a symlink")
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonical)
	if err != nil {
		return Terminal{}, false, err
	}
	defer root.Close()
	terminal, found, err := readTerminal(ctx, root, jobArtifactBase(jobID)+".json")
	if err != nil || !found {
		return terminal, found, err
	}
	if terminal.JobID != jobID {
		return Terminal{}, false, errors.New("worker terminal marker job identity mismatch")
	}
	if !terminalJobState(terminal.State) {
		return Terminal{}, false, errors.New("worker terminal marker has a nonterminal state")
	}
	if _, err := readDurableResult(ctx, root, jobArtifactBase(jobID)+".result.json", terminal); err != nil {
		return Terminal{}, false, err
	}
	return terminal, true, nil
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
