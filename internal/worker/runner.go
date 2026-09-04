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
	"github.com/daviddwlee84/exp-cli/internal/mlflow"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
)

const (
	// JobSchema, TerminalSchema, and ResultSchema retain the exact legacy v1
	// shapes. Source-aware direct Try execution uses the separately dispatched v2
	// schemas so rolling upgrades never reinterpret a closed v1 payload.
	JobSchema              = "exp.worker-job/v1"
	SourceJobSchema        = "exp.worker-job/v2"
	TerminalSchema         = "exp.worker-terminal/v1"
	SourceTerminalSchema   = "exp.worker-terminal/v2"
	ResultSchema           = "exp.worker-result/v1"
	SourceResultSchema     = "exp.worker-result/v2"
	maxTerminalBytes       = 512 << 10
	maxTerminalStreamBytes = 16 << 10
	maxTerminalOutputBytes = 128 << 10
	maxWorkloadResultBytes = 768 << 10
	maxExpectedOutputs     = 256
	maxExpectedOutputBytes = 512 << 20
	maxExpectedTotalBytes  = 1 << 30
	maxExpectedHashTime    = 2 * time.Minute
)

// SnapshotBinding is the private, explicit JSON representation of a canonical
// SourceSnapshot. It keeps the operational job schema independent from TOML
// field tags while retaining every byte covered by the canonical digest.
type SnapshotBinding struct {
	Source          string                       `json:"source"`
	Subdir          string                       `json:"subdir"`
	PolicyVersion   string                       `json:"policy_version"`
	CapturedAt      time.Time                    `json:"captured_at"`
	GitObjectFormat research.GitObjectFormat     `json:"git_object_format"`
	BaseCommit      string                       `json:"base_commit"`
	HeadCommit      string                       `json:"head_commit"`
	ChangeSet       []string                     `json:"change_set"`
	State           research.SourceSnapshotState `json:"state"`
	DirtyDigest     string                       `json:"dirty_digest,omitempty"`
	DirtySummary    string                       `json:"dirty_summary,omitempty"`
	Digest          string                       `json:"digest"`
	Reproducibility research.Reproducibility     `json:"reproducibility"`
}

// BindSourceSnapshot copies a canonical snapshot into private worker payload
// form without adding host paths.
func BindSourceSnapshot(snapshot research.SourceSnapshot) SnapshotBinding {
	return SnapshotBinding{
		Source: snapshot.Source.String(), Subdir: snapshot.Subdir,
		PolicyVersion: snapshot.PolicyVersion, CapturedAt: snapshot.CapturedAt,
		GitObjectFormat: snapshot.GitObjectFormat, BaseCommit: snapshot.BaseCommit,
		HeadCommit: snapshot.HeadCommit, ChangeSet: append([]string{}, snapshot.ChangeSet...),
		State: snapshot.State, DirtyDigest: snapshot.DirtyDigest,
		DirtySummary: snapshot.DirtySummary, Digest: snapshot.Digest,
		Reproducibility: snapshot.Reproducibility,
	}
}

// SourceBinding is one private local checkout bound to a canonical snapshot.
// Host paths never leave the operational v2 payload.
type SourceBinding struct {
	Checkout                    string          `json:"checkout"`
	RepositoryRoot              string          `json:"repository_root"`
	SemanticRoot                string          `json:"semantic_root"`
	RegisteredGitCommonDir      string          `json:"registered_git_common_dir"`
	RegisteredGitCommonIdentity string          `json:"registered_git_common_identity"`
	ReadOnly                    bool            `json:"read_only"`
	Snapshot                    SnapshotBinding `json:"snapshot"`
}

func (binding SnapshotBinding) snapshot() (research.SourceSnapshot, error) {
	source, err := research.ParseIDForKind(binding.Source, research.KindSource)
	if err != nil {
		return research.SourceSnapshot{}, err
	}
	value := research.SourceSnapshot{
		Source: source, Subdir: binding.Subdir, PolicyVersion: binding.PolicyVersion,
		CapturedAt: binding.CapturedAt, GitObjectFormat: binding.GitObjectFormat,
		BaseCommit: binding.BaseCommit, HeadCommit: binding.HeadCommit,
		ChangeSet: append([]string{}, binding.ChangeSet...), State: binding.State,
		DirtyDigest: binding.DirtyDigest, DirtySummary: binding.DirtySummary,
		Digest: binding.Digest, Reproducibility: binding.Reproducibility,
	}
	if err := sourcesnapshot.Validate(value); err != nil {
		return research.SourceSnapshot{}, err
	}
	return value, nil
}

type Workload struct {
	Schema                      string                  `json:"schema_version"`
	AttemptID                   string                  `json:"attempt_id"`
	TryID                       string                  `json:"try_id,omitempty"`
	ProjectID                   string                  `json:"project_id,omitempty"`
	CanonicalScope              string                  `json:"canonical_scope,omitempty"`
	ExecutionSource             string                  `json:"execution_source,omitempty"`
	CheckoutIdentity            string                  `json:"checkout_identity,omitempty"`
	Sources                     []SourceBinding         `json:"sources,omitempty"`
	Executable                  string                  `json:"executable"`
	Args                        []string                `json:"args"`
	CWD                         string                  `json:"cwd"`
	Timeout                     string                  `json:"timeout"`
	AllowedEnv                  []string                `json:"allowed_env"`
	SecretEnv                   []string                `json:"secret_env"`
	MLflow                      *mlflow.WorkloadProfile `json:"mlflow,omitempty"`
	ResultFile                  string                  `json:"result_file,omitempty"`
	RepositoryRoot              string                  `json:"repository_root,omitempty"`
	OutputRoot                  string                  `json:"output_root,omitempty"`
	RegisteredGitCommonDir      string                  `json:"registered_git_common_dir,omitempty"`
	RegisteredGitCommonIdentity string                  `json:"registered_git_common_identity,omitempty"`
	SourceSnapshot              *SnapshotBinding        `json:"source_snapshot,omitempty"`
	BaseCommit                  string                  `json:"base_commit"`
	HeadCommit                  string                  `json:"head_commit"`
	ChangeSet                   []string                `json:"change_set"`
	RuntimeConfigPath           string                  `json:"runtime_config_path,omitempty"`
	ExpectedOutputs             []string                `json:"expected_outputs"`
}

type workloadV1 struct {
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

func (legacy workloadV1) workload() Workload {
	return Workload{
		Schema: legacy.Schema, AttemptID: legacy.AttemptID, Executable: legacy.Executable,
		Args: append([]string(nil), legacy.Args...), CWD: legacy.CWD, Timeout: legacy.Timeout,
		AllowedEnv: append([]string(nil), legacy.AllowedEnv...), SecretEnv: append([]string(nil), legacy.SecretEnv...),
		ResultFile: legacy.ResultFile, RepositoryRoot: legacy.RepositoryRoot,
		BaseCommit: legacy.BaseCommit, HeadCommit: legacy.HeadCommit, ChangeSet: append([]string(nil), legacy.ChangeSet...),
		RuntimeConfigPath: legacy.RuntimeConfigPath, ExpectedOutputs: append([]string(nil), legacy.ExpectedOutputs...),
	}
}

type Terminal struct {
	Schema          string             `json:"schema_version"`
	JobID           string             `json:"job_id"`
	AttemptID       string             `json:"attempt_id"`
	FencingToken    int64              `json:"fencing_token"`
	State           operation.JobState `json:"state"`
	ExitCode        int                `json:"exit_code"`
	StartedAt       time.Time          `json:"started_at"`
	EndedAt         time.Time          `json:"ended_at"`
	TimedOut        bool               `json:"timed_out"`
	Cancelled       bool               `json:"cancelled"`
	ResultSHA256    string             `json:"result_sha256,omitempty"`
	ResultSize      int64              `json:"result_size,omitempty"`
	Outputs         map[string]string  `json:"outputs,omitempty"`
	Stdout          string             `json:"stdout,omitempty"`
	Stderr          string             `json:"stderr,omitempty"`
	StdoutTruncated bool               `json:"stdout_truncated,omitempty"`
	StderrTruncated bool               `json:"stderr_truncated,omitempty"`
	MLflow          *mlflow.Attachment `json:"mlflow,omitempty"`
}

type terminalV1 struct {
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

func (legacy terminalV1) terminal() Terminal {
	return Terminal{
		Schema: legacy.Schema, JobID: legacy.JobID, AttemptID: legacy.AttemptID,
		FencingToken: legacy.FencingToken, State: legacy.State, ExitCode: legacy.ExitCode,
		StartedAt: legacy.StartedAt, EndedAt: legacy.EndedAt, TimedOut: legacy.TimedOut,
		Cancelled: legacy.Cancelled, ResultSHA256: legacy.ResultSHA256, ResultSize: legacy.ResultSize,
		Outputs: cloneStringMap(legacy.Outputs),
	}
}

// JobResult is the exact durable operational result. Keeping terminal facts in
// the database lets the controller import timeout/timing/exit observations
// even when the worker process has already exited.
type JobResult struct {
	Schema   string          `json:"schema_version"`
	Result   json.RawMessage `json:"result"`
	Terminal Terminal        `json:"terminal"`
}

type jobResultV1 struct {
	Schema   string          `json:"schema_version"`
	Result   json.RawMessage `json:"result"`
	Terminal terminalV1      `json:"terminal"`
}

type Store interface {
	FinishJob(context.Context, string, int64, operation.JobState, json.RawMessage, string) (operation.Job, error)
}

type externalReferenceStore interface {
	SetJobExternalRefs(context.Context, string, int64, *int64, string) error
}

// SnapshotVerifier is the exact Source recapture boundary used immediately
// before invocation. A sourcesnapshot.Capturer is the production implementation.
type SnapshotVerifier interface {
	CaptureClean(context.Context, sourcesnapshot.Request) (research.SourceSnapshot, error)
	VerifyDirty(context.Context, sourcesnapshot.Request, research.SourceSnapshot) (research.SourceSnapshot, error)
}

type Runner struct {
	Store          Store
	Invoker        execx.Invoker
	MLflowInvoker  execx.Invoker
	LookupBinary   func(string) (string, error)
	Git            gitx.Runner
	Snapshots      SnapshotVerifier
	Preflight      func(context.Context, Workload) error
	MarkerRoot     string
	Clock          func() time.Time
	ProjectID      string
	CanonicalScope string
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
	if formalWorkloadFieldsPresent(workload) && (runner.ProjectID != "" || runner.CanonicalScope != "") {
		if workload.ProjectID != runner.ProjectID || workload.CanonicalScope != runner.CanonicalScope || job.CanonicalScope != runner.CanonicalScope {
			return Terminal{}, errors.New("worker workload canonical authority mismatch")
		}
	}
	markerRootPath, markerRoot, err := secureRoot(runner.MarkerRoot)
	if err != nil {
		return Terminal{}, err
	}
	defer markerRoot.Close()
	markerName := jobArtifactBase(job.ID) + ".json"
	resultName := jobArtifactBase(job.ID) + ".result.json"
	if existing, found, err := readOrPromoteTerminal(ctx, markerRoot, markerName, resultName); err != nil {
		return Terminal{}, err
	} else if found {
		if err := ValidateTerminalForJob(existing, job, workload.AttemptID); err != nil {
			return Terminal{}, err
		}
		resultPayload, recoveryErr := recoveredResultPayload(ctx, markerRoot, resultName, existing)
		if recoveryErr != nil {
			return Terminal{}, recoveryErr
		}
		if referenceErr := runner.persistMLflowRunID(context.WithoutCancel(ctx), job, existing); referenceErr != nil && !errors.Is(referenceErr, operation.ErrFenced) {
			return existing, fmt.Errorf("repair operational MLflow identity from terminal marker: %w", referenceErr)
		}
		if job.State == operation.JobRunning {
			if _, finishErr := runner.Store.FinishJob(ctx, job.ID, job.FencingToken, existing.State, resultPayload, "recovered from durable terminal marker"); finishErr != nil && !errors.Is(finishErr, operation.ErrFenced) {
				return existing, fmt.Errorf("repair operational state from terminal marker: %w", finishErr)
			}
		}
		return existing, nil
	}
	if job.State != operation.JobRunning {
		return Terminal{}, errors.New("worker requires a claimed running job when no terminal marker exists")
	}
	clock := runner.Clock
	if clock == nil {
		clock = time.Now
	}
	resultIdentity, err := prepareResultFile(markerRoot, resultName)
	if err != nil {
		return Terminal{}, err
	}
	failBeforeInvocation := func(label string) (Terminal, error) {
		return runner.finishBeforeInvocation(ctx, markerRootPath, markerRoot, markerName, resultName, workload, job, clock, label)
	}
	if err := validateTerminalCapacity(workload, job); err != nil {
		return failBeforeInvocation("worker terminal envelope policy is unrepresentable")
	}
	timeout := time.Duration(0)
	if workload.Timeout != "" {
		timeout, err = time.ParseDuration(workload.Timeout)
		if err != nil || timeout <= 0 {
			return failBeforeInvocation("worker timeout policy is invalid")
		}
	}
	if runner.Preflight != nil {
		if err := runner.Preflight(ctx, workload); err != nil {
			return failBeforeInvocation("worker preflight rejected invocation")
		}
	}
	if err := verifyWorkloadGit(ctx, runner.Git, runner.Snapshots, workload); err != nil {
		return failBeforeInvocation("worker Git identity verification failed")
	}

	environment, _, err := workloadEnvironment(workload, job, markerRootPath)
	if err != nil {
		return failBeforeInvocation("worker environment preparation failed")
	}
	frozenEnvironment, resultRedactor, err := environment.Freeze(os.LookupEnv)
	if err != nil {
		return failBeforeInvocation("worker environment resolution failed")
	}
	environment = frozenEnvironment
	invoker := runner.Invoker
	if invoker == nil {
		invoker = execx.NewInvoker()
	}
	outputPolicy := execx.DefaultOutputPolicy(execx.OutputCapture)
	if workload.Schema == SourceJobSchema {
		outputPolicy.MaxStdoutBytes = maxTerminalStreamBytes
		outputPolicy.MaxStderrBytes = maxTerminalStreamBytes
	}
	spec := execx.CommandSpec{
		Executable: workload.Executable, Argv: append([]string(nil), workload.Args...), CWD: workload.CWD,
		Environment: environment, Timeout: timeout, Output: outputPolicy,
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
		Schema: terminalSchemaForWorkload(workload.Schema), JobID: job.ID, AttemptID: workload.AttemptID, FencingToken: job.FencingToken,
		State: state, ExitCode: process.ExitCode, StartedAt: started, EndedAt: ended,
		TimedOut: process.TimedOut, Cancelled: process.Canceled,
	}
	if workload.Schema == SourceJobSchema {
		terminal.Stdout, terminal.Stderr = process.Stdout, process.Stderr
		terminal.StdoutTruncated, terminal.StderrTruncated = process.StdoutTruncated, process.StderrTruncated
	}
	if state == operation.JobSucceeded && len(workload.Sources) > 0 {
		if err := verifyReadOnlySourceWorkload(ctx, runner.Git, runner.Snapshots, workload); err != nil {
			state, terminal.State, message = operation.JobFailed, operation.JobFailed, "read-only Source changed during formal execution"
		}
	}
	workloadResult := json.RawMessage(`{}`)
	content, _, readErr := pathx.ReadBoundedRegularFile(context.WithoutCancel(ctx), markerRoot, resultName, maxWorkloadResultBytes)
	unsafeResult := false
	if readErr != nil || resultIdentity == nil {
		state, terminal.State, message = operation.JobFailed, operation.JobFailed, "worker result file could not be read safely"
	} else if current, statErr := markerRoot.Lstat(resultName); statErr != nil || !os.SameFile(resultIdentity, current) {
		unsafeResult = true
		state, terminal.State, message = operation.JobFailed, operation.JobFailed, "worker result file identity changed"
	} else if len(content) > 0 {
		if !json.Valid(content) {
			unsafeResult = true
			state, terminal.State, message = operation.JobFailed, operation.JobFailed, "worker result file is invalid"
		} else if sanitized, sanitizeErr := sanitizeWorkloadResult(content, resultRedactor); sanitizeErr != nil {
			unsafeResult = true
			state, terminal.State, message = operation.JobFailed, operation.JobFailed, "worker result file crossed the redaction boundary"
		} else if runID, runIDErr := extractMLflowRunID(sanitized); runIDErr != nil {
			unsafeResult = true
			state, terminal.State, message = operation.JobFailed, operation.JobFailed, "worker result contains an invalid MLflow run identity"
		} else {
			content = sanitized
			digest := sha256.Sum256(content)
			terminal.ResultSHA256 = "sha256:" + hex.EncodeToString(digest[:])
			terminal.ResultSize = int64(len(content))
			workloadResult = append(json.RawMessage(nil), content...)
			if runID != "" && workload.MLflow != nil {
				observerInvoker := runner.MLflowInvoker
				if observerInvoker == nil {
					observerInvoker = runner.Invoker
				}
				attachment := mlflow.ObserveAttachment(ctx, *workload.MLflow, runID, workload.AttemptID, workload.CWD, observerInvoker, runner.LookupBinary, clock)
				if attachment.Validate() == nil {
					terminal.MLflow = &attachment
				}
			}
		}
	}
	freezeContent := content
	if state == operation.JobFailed && (readErr != nil || unsafeResult) {
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
	if err := runner.persistMLflowRunID(context.WithoutCancel(ctx), job, terminal); err != nil {
		return terminal, fmt.Errorf("terminal marker published but operational MLflow identity update failed: %w", err)
	}
	if _, err := runner.Store.FinishJob(ctx, job.ID, job.FencingToken, state, resultPayload, message); err != nil {
		return terminal, fmt.Errorf("terminal marker published but operational state update failed: %w", err)
	}
	return terminal, nil
}

func (runner Runner) persistMLflowRunID(ctx context.Context, job operation.Job, terminal Terminal) error {
	if terminal.MLflow == nil || terminal.MLflow.RunID == "" {
		return nil
	}
	if job.MLflowRunID != "" && job.MLflowRunID != terminal.MLflow.RunID {
		return errors.New("operational job already has a different MLflow run identity")
	}
	store, ok := runner.Store.(externalReferenceStore)
	if !ok {
		return nil
	}
	return store.SetJobExternalRefs(ctx, job.ID, job.FencingToken, job.PueueTaskID, terminal.MLflow.RunID)
}

func (runner Runner) finishBeforeInvocation(ctx context.Context, markerRootPath string, markerRoot *os.Root, markerName, resultName string, workload Workload, job operation.Job, clock func() time.Time, label string) (Terminal, error) {
	if _, err := freezeResultFile(markerRoot, resultName, nil); err != nil {
		return Terminal{}, fmt.Errorf("freeze pre-invocation worker result: %w", err)
	}
	now := clock().UTC()
	terminal := Terminal{
		Schema: terminalSchemaForWorkload(workload.Schema), JobID: job.ID, AttemptID: workload.AttemptID,
		FencingToken: job.FencingToken, State: operation.JobFailed, ExitCode: -1,
		StartedAt: now, EndedAt: now,
	}
	if err := pathx.VerifyRootPath(markerRootPath, markerRoot); err != nil {
		return Terminal{}, errors.New("worker marker root changed before pre-invocation failure publication")
	}
	if err := writeTerminal(markerRoot, markerName, terminal); err != nil {
		return Terminal{}, err
	}
	payload, err := encodeJobResult(json.RawMessage(`{}`), terminal)
	if err != nil {
		return terminal, err
	}
	if _, err := runner.Store.FinishJob(context.WithoutCancel(ctx), job.ID, job.FencingToken, operation.JobFailed, payload, label); err != nil {
		return terminal, fmt.Errorf("pre-invocation terminal marker published but operational state update failed: %w", err)
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

func terminalSchemaForWorkload(schema string) string {
	if schema == SourceJobSchema {
		return SourceTerminalSchema
	}
	return TerminalSchema
}

func terminalJobState(state operation.JobState) bool {
	return state == operation.JobSucceeded || state == operation.JobFailed || state == operation.JobCancelled
}

// ValidateTerminalIdentity applies the shared durable-marker binding checks used
// by Runner, direct recovery, and database-independent reconciliation.
func ValidateTerminalIdentity(terminal Terminal, jobID, attemptID string) error {
	if terminal.Schema != TerminalSchema && terminal.Schema != SourceTerminalSchema {
		return errors.New("worker terminal marker schema is unsupported")
	}
	if jobID == "" || terminal.JobID != jobID || terminal.AttemptID == "" || attemptID != "" && terminal.AttemptID != attemptID {
		return errors.New("worker terminal marker identity mismatch")
	}
	if terminal.FencingToken <= 0 || !terminalJobState(terminal.State) {
		return errors.New("worker terminal marker has an invalid claim or state")
	}
	if terminal.StartedAt.IsZero() || terminal.EndedAt.IsZero() || terminal.EndedAt.Before(terminal.StartedAt) {
		return errors.New("worker terminal marker has invalid timing")
	}
	if terminal.Schema == TerminalSchema && (terminal.Stdout != "" || terminal.Stderr != "" || terminal.StdoutTruncated || terminal.StderrTruncated) {
		return errors.New("exp.worker-terminal/v1 forbids captured stream fields")
	}
	if len(terminal.Stdout) > maxTerminalStreamBytes || len(terminal.Stderr) > maxTerminalStreamBytes {
		return errors.New("worker terminal marker stream exceeds its bound")
	}
	if terminal.ResultSHA256 == "" && terminal.ResultSize != 0 || terminal.ResultSHA256 != "" && (!validSHA256(terminal.ResultSHA256) || terminal.ResultSize <= 0) {
		return errors.New("worker terminal marker result identity is invalid")
	}
	for path, digest := range terminal.Outputs {
		if pathx.ValidateRelativePOSIX(path, false) != nil || !validSHA256(digest) {
			return errors.New("worker terminal marker output identity is invalid")
		}
	}
	if terminal.MLflow != nil {
		if err := terminal.MLflow.Validate(); err != nil {
			return errors.New("worker terminal marker MLflow observation is invalid")
		}
		if terminal.MLflow.State == mlflow.ObservationVerified && terminal.MLflow.Tags[mlflow.AttemptOwnershipTag] != terminal.AttemptID {
			return errors.New("worker terminal marker MLflow ownership differs from its Attempt")
		}
	}
	return nil
}

// ValidateTerminalForJob additionally fences a marker to the current operational
// row. Running rows may be repaired; terminal rows must agree exactly.
func ValidateTerminalForJob(terminal Terminal, job operation.Job, attemptID string) error {
	if err := ValidateTerminalIdentity(terminal, job.ID, attemptID); err != nil {
		return err
	}
	if job.SubjectID != "" && job.SubjectID != terminal.AttemptID || job.FencingToken != terminal.FencingToken {
		return errors.New("worker terminal marker does not match the current job claim")
	}
	if job.State == operation.JobRunning {
		return nil
	}
	if terminalJobState(job.State) && job.State == terminal.State {
		return nil
	}
	return errors.New("worker terminal marker disagrees with operational job state")
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
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

func sanitizeWorkloadResult(content []byte, redactor execx.Redactor) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("workload result contains trailing JSON")
	}
	changed := false
	var sanitize func(any) (any, error)
	sanitize = func(value any) (any, error) {
		switch typed := value.(type) {
		case string:
			safe := redactor.Text(typed)
			changed = changed || safe != typed
			return safe, nil
		case []any:
			for index := range typed {
				safe, err := sanitize(typed[index])
				if err != nil {
					return nil, err
				}
				typed[index] = safe
			}
			return typed, nil
		case map[string]any:
			for key, item := range typed {
				if redactor.Text(key) != key {
					return nil, errors.New("workload result key contains a resolved secret")
				}
				if execx.SensitiveName(key) {
					if text, ok := item.(string); !ok || text != execx.Redacted {
						changed = true
					}
					typed[key] = execx.Redacted
					continue
				}
				safe, err := sanitize(item)
				if err != nil {
					return nil, err
				}
				typed[key] = safe
			}
			return typed, nil
		case nil, bool, json.Number:
			return typed, nil
		default:
			return nil, errors.New("workload result contains an unsupported JSON value")
		}
	}
	safe, err := sanitize(decoded)
	if err != nil {
		return nil, err
	}
	if !changed {
		return append([]byte(nil), content...), nil
	}
	encoded, err := json.Marshal(safe)
	if err != nil || len(encoded) > maxWorkloadResultBytes {
		return nil, errors.New("sanitized workload result exceeds its byte limit")
	}
	return encoded, nil
}

func extractMLflowRunID(content []byte) (string, error) {
	if len(content) == 0 {
		return "", nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(content, &object); err != nil || object == nil {
		return "", nil
	}
	raw, found := object["mlflow_run_id"]
	if !found {
		return "", nil
	}
	var runID string
	if err := json.Unmarshal(raw, &runID); err != nil || !mlflow.ValidRunID(runID) {
		return "", errors.New("invalid MLflow run identity")
	}
	return runID, nil
}

func encodeJobResult(result json.RawMessage, terminal Terminal) (json.RawMessage, error) {
	if len(result) == 0 || !json.Valid(result) {
		return nil, errors.New("worker result payload is invalid")
	}
	schema := ResultSchema
	if terminal.Schema == SourceTerminalSchema {
		schema = SourceResultSchema
	}
	encoded, err := json.Marshal(JobResult{Schema: schema, Result: result, Terminal: terminal})
	return json.RawMessage(encoded), err
}

// DecodeJobResult validates the exact result envelope persisted by Runner.
func DecodeJobResult(payload []byte) (JobResult, error) {
	var selector struct {
		Schema string `json:"schema_version"`
	}
	if err := json.Unmarshal(payload, &selector); err != nil {
		return JobResult{}, err
	}
	var result JobResult
	switch selector.Schema {
	case ResultSchema:
		var legacy jobResultV1
		if err := decodeStrictWorkerJSON(payload, &legacy, "worker result"); err != nil {
			return JobResult{}, err
		}
		result = JobResult{Schema: legacy.Schema, Result: append(json.RawMessage(nil), legacy.Result...), Terminal: legacy.Terminal.terminal()}
	case SourceResultSchema:
		if err := decodeStrictWorkerJSON(payload, &result, "worker result"); err != nil {
			return JobResult{}, err
		}
	default:
		return JobResult{}, errors.New("worker result schema is unsupported")
	}
	expectedTerminal := TerminalSchema
	if result.Schema == SourceResultSchema {
		expectedTerminal = SourceTerminalSchema
	}
	if result.Terminal.Schema != expectedTerminal || len(result.Result) == 0 || !json.Valid(result.Result) {
		return JobResult{}, errors.New("worker result schema or payload is invalid")
	}
	return result, nil
}

func jobArtifactBase(jobID string) string {
	digest := sha256.Sum256([]byte(jobID))
	return "job-" + hex.EncodeToString(digest[:16])
}

func decodeStrictWorkerJSON(payload []byte, target any, label string) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s contains trailing JSON", label)
	}
	return nil
}

func decodeWorkload(payload []byte) (Workload, error) {
	var selector struct {
		Schema string `json:"schema_version"`
	}
	if err := json.Unmarshal(payload, &selector); err != nil {
		return Workload{}, fmt.Errorf("decode worker job schema: %w", err)
	}
	var workload Workload
	switch selector.Schema {
	case JobSchema:
		var legacy workloadV1
		if err := decodeStrictWorkerJSON(payload, &legacy, "worker job"); err != nil {
			return Workload{}, err
		}
		workload = legacy.workload()
	case SourceJobSchema:
		if err := decodeStrictWorkerJSON(payload, &workload, "worker job"); err != nil {
			return Workload{}, err
		}
	default:
		return Workload{}, errors.New("worker job schema is unsupported")
	}
	if workload.AttemptID == "" {
		return Workload{}, errors.New("worker job attempt id is invalid")
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
	if workload.MLflow != nil {
		if err := workload.MLflow.Validate(); err != nil {
			return Workload{}, fmt.Errorf("worker MLflow profile: %w", err)
		}
	}
	if workload.Schema == SourceJobSchema && formalWorkloadFieldsPresent(workload) {
		return validateFormalWorkload(workload)
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
	if workload.Schema == SourceJobSchema && workload.SourceSnapshot == nil {
		return Workload{}, errors.New("exp.worker-job/v2 requires source_snapshot")
	}
	if workload.SourceSnapshot != nil {
		snapshot, snapshotErr := workload.SourceSnapshot.snapshot()
		if snapshotErr != nil {
			return Workload{}, fmt.Errorf("worker source_snapshot: %w", snapshotErr)
		}
		if snapshot.BaseCommit != workload.BaseCommit || snapshot.HeadCommit != workload.HeadCommit || !sameWorkerPaths(snapshot.ChangeSet, workload.ChangeSet) {
			return Workload{}, errors.New("worker source_snapshot disagrees with legacy Git identity fields")
		}
		if workload.RegisteredGitCommonDir == "" || !filepath.IsAbs(workload.RegisteredGitCommonDir) || filepath.Clean(workload.RegisteredGitCommonDir) != workload.RegisteredGitCommonDir {
			return Workload{}, errors.New("worker registered_git_common_dir must be a clean absolute path")
		}
		if snapshot.State == research.SourceSnapshotDirty {
			tryID, parseErr := research.ParseIDForKind(workload.TryID, research.KindTry)
			if parseErr != nil || tryID.IsZero() {
				return Workload{}, errors.New("dirty worker source_snapshot requires a complete Try id")
			}
		} else if workload.TryID != "" {
			if _, parseErr := research.ParseIDForKind(workload.TryID, research.KindTry); parseErr != nil {
				return Workload{}, errors.New("worker Try id is invalid")
			}
		}
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
	previous = ""
	for _, output := range workload.ExpectedOutputs {
		if err := pathx.ValidateRelativePOSIX(output, false); err != nil {
			return Workload{}, fmt.Errorf("worker expected output %q: %w", output, err)
		}
		if previous != "" && output <= previous {
			return Workload{}, errors.New("worker expected_outputs must be sorted and unique")
		}
		previous = output
	}
	if err := validateExpectedOutputEnvelope(workload.ExpectedOutputs); err != nil {
		return Workload{}, err
	}
	for _, argument := range workload.Args {
		if !utf8.ValidString(argument) || strings.ContainsRune(argument, 0) {
			return Workload{}, errors.New("worker args contain invalid text")
		}
	}
	return workload, nil
}

func formalWorkloadFieldsPresent(workload Workload) bool {
	return workload.ProjectID != "" || workload.CanonicalScope != "" || workload.ExecutionSource != "" ||
		workload.CheckoutIdentity != "" || workload.Sources != nil || workload.OutputRoot != ""
}

func validateFormalWorkload(workload Workload) (Workload, error) {
	if _, err := research.ParseUUID(workload.ProjectID); err != nil {
		return Workload{}, errors.New("formal worker project_id is invalid")
	}
	if workload.CanonicalScope == "" || len(workload.CanonicalScope) > 512 || strings.ContainsAny(workload.CanonicalScope, "\x00\r\n") {
		return Workload{}, errors.New("formal worker canonical_scope is invalid")
	}
	executionID, err := research.ParseIDForKind(workload.ExecutionSource, research.KindSource)
	if err != nil {
		return Workload{}, errors.New("formal worker execution_source is invalid")
	}
	if _, err := research.ParseIDForKind(workload.AttemptID, research.KindAttempt); err != nil {
		return Workload{}, errors.New("formal worker attempt_id is invalid")
	}
	if workload.TryID != "" || workload.SourceSnapshot != nil || workload.RuntimeConfigPath != "" || len(workload.SecretEnv) != 0 {
		return Workload{}, errors.New("formal worker payload contains direct, secret, or legacy Source fields")
	}
	if len(workload.Sources) == 0 {
		return Workload{}, errors.New("formal worker sources must contain at least one binding")
	}
	previous := ""
	executionFound := false
	var primary SourceBinding
	for index := range workload.Sources {
		binding := &workload.Sources[index]
		snapshot, snapshotErr := binding.Snapshot.snapshot()
		if snapshotErr != nil {
			return Workload{}, fmt.Errorf("formal worker sources[%d] snapshot: %w", index, snapshotErr)
		}
		if snapshot.State != research.SourceSnapshotClean {
			return Workload{}, errors.New("formal worker accepts only clean Source snapshots")
		}
		if previous != "" && snapshot.Source.String() <= previous {
			return Workload{}, errors.New("formal worker sources must be sorted and unique by Source ID")
		}
		previous = snapshot.Source.String()
		if binding.Checkout != "main" && binding.Checkout != "registered_worktree" && binding.Checkout != "managed_worktree" {
			return Workload{}, fmt.Errorf("formal worker sources[%d] checkout is invalid", index)
		}
		for label, value := range map[string]string{
			"repository_root": binding.RepositoryRoot, "semantic_root": binding.SemanticRoot,
			"registered_git_common_dir": binding.RegisteredGitCommonDir,
		} {
			if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
				return Workload{}, fmt.Errorf("formal worker sources[%d] %s must be a clean absolute path", index, label)
			}
		}
		if binding.RegisteredGitCommonIdentity == "" || !lexicallyContains(binding.RepositoryRoot, binding.SemanticRoot) {
			return Workload{}, fmt.Errorf("formal worker sources[%d] checkout identity is incomplete", index)
		}
		if snapshot.Source == executionID {
			if executionFound || binding.ReadOnly {
				return Workload{}, errors.New("formal worker execution Source must occur exactly once and be writable")
			}
			executionFound = true
			primary = *binding
		} else if !binding.ReadOnly {
			return Workload{}, errors.New("formal worker non-execution Sources must be read-only")
		}
	}
	if !executionFound {
		return Workload{}, errors.New("formal worker execution Source has no checkout binding")
	}
	primarySnapshot, _ := primary.Snapshot.snapshot()
	if workload.RepositoryRoot != primary.RepositoryRoot || workload.OutputRoot != primary.SemanticRoot ||
		workload.BaseCommit != primarySnapshot.BaseCommit || workload.HeadCommit != primarySnapshot.HeadCommit ||
		!sameWorkerPaths(workload.ChangeSet, primarySnapshot.ChangeSet) {
		return Workload{}, errors.New("formal worker primary checkout fields disagree with the execution Source binding")
	}
	if !lexicallyContains(primary.SemanticRoot, workload.CWD) {
		return Workload{}, errors.New("formal worker cwd must remain inside the execution Source subdir")
	}
	if workload.CheckoutIdentity == "" || workload.CheckoutIdentity != SourceCheckoutIdentity(workload.ProjectID, workload.CanonicalScope, workload.ExecutionSource, workload.Sources) {
		return Workload{}, errors.New("formal worker checkout_identity does not match its private Source bindings")
	}
	if workload.BaseCommit == "" || workload.HeadCommit == "" {
		return Workload{}, errors.New("formal worker base_commit and head_commit are required")
	}
	previous = ""
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
	for _, output := range workload.ExpectedOutputs {
		if err := pathx.ValidateRelativePOSIX(output, false); err != nil {
			return Workload{}, fmt.Errorf("worker expected output %q: %w", output, err)
		}
	}
	if err := validateExpectedOutputEnvelope(workload.ExpectedOutputs); err != nil {
		return Workload{}, err
	}
	for _, argument := range workload.Args {
		if !utf8.ValidString(argument) || strings.ContainsRune(argument, 0) {
			return Workload{}, errors.New("worker args contain invalid text")
		}
	}
	return workload, nil
}

func expectedOutputIdentities(paths []string) map[string]string {
	if len(paths) == 0 {
		return nil
	}
	digest := "sha256:" + strings.Repeat("a", sha256.Size*2)
	identities := make(map[string]string, len(paths))
	for _, name := range paths {
		identities[name] = digest
	}
	return identities
}

func validateExpectedOutputEnvelope(paths []string) error {
	encoded, err := json.Marshal(expectedOutputIdentities(paths))
	if err != nil || len(encoded) > maxTerminalOutputBytes {
		return errors.New("worker expected output identities are too large for the terminal envelope")
	}
	return nil
}

func validateTerminalCapacity(workload Workload, job operation.Job) error {
	worstStream := strings.Repeat("\x01", maxTerminalStreamBytes)
	now := time.Unix(1, 0).UTC()
	terminal := Terminal{
		Schema: terminalSchemaForWorkload(workload.Schema), JobID: job.ID, AttemptID: workload.AttemptID,
		FencingToken: max(job.FencingToken, 1), State: operation.JobSucceeded, ExitCode: -1,
		StartedAt: now, EndedAt: now, Outputs: expectedOutputIdentities(workload.ExpectedOutputs),
		Stdout: worstStream, Stderr: worstStream, StdoutTruncated: true, StderrTruncated: true,
	}
	encoded, err := json.Marshal(terminal)
	if err != nil || len(encoded)+mlflow.MaxAttachmentJSONBytes+32 > maxTerminalBytes {
		return errors.New("worker workload cannot fit its worst-case terminal envelope")
	}
	return nil
}

func lexicallyContains(root, candidate string) bool {
	if root == "" || candidate == "" || !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && (relative == "." || relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

// SourceCheckoutIdentity binds formal v2 execution to the exact private checkout
// paths and canonical snapshot digests without treating a path hash as Source ID.
func SourceCheckoutIdentity(projectID, scope, executionSource string, sources []SourceBinding) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("exp.worker.checkout/v2\x00"))
	for _, value := range []string{projectID, scope, executionSource} {
		_, _ = hash.Write([]byte(fmt.Sprintf("%d:", len(value))))
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	for _, source := range sources {
		fields := []string{
			source.Snapshot.Source, source.Checkout, source.Snapshot.Digest,
			source.RepositoryRoot, source.SemanticRoot, source.RegisteredGitCommonDir,
			source.RegisteredGitCommonIdentity, fmt.Sprintf("%t", source.ReadOnly),
		}
		for _, value := range fields {
			_, _ = hash.Write([]byte(fmt.Sprintf("%d:", len(value))))
			_, _ = hash.Write([]byte(value))
			_, _ = hash.Write([]byte{0})
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func verifyExpectedOutputs(ctx context.Context, workload Workload) (map[string]string, error) {
	outputRoot := workload.RepositoryRoot
	if workload.OutputRoot != "" {
		outputRoot = workload.OutputRoot
	}
	repositoryRoot, err := os.OpenRoot(outputRoot)
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
	if err := pathx.VerifyRootPath(outputRoot, repositoryRoot); err != nil {
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
	if workload.ProjectID != "" {
		bindings = append(bindings,
			execx.Bind("EXP_PROJECT_ID", workload.ProjectID),
			execx.Bind("EXP_CANONICAL_SCOPE", workload.CanonicalScope),
			execx.Bind("EXP_EXECUTION_SOURCE", workload.ExecutionSource),
		)
	}
	for _, name := range workload.SecretEnv {
		bindings = append(bindings, execx.BindSecretFromEnv(name, name))
	}
	if workload.MLflow != nil {
		profileBindings, err := workload.MLflow.EnvironmentBindings()
		if err != nil {
			return execx.Environment{}, "", err
		}
		bindings = append(bindings, profileBindings...)
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
	if err := pathx.ProtectPrivateRoot(root, 0o700); err != nil {
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
	protected, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	if err := errors.Join(pathx.ProtectPrivateOpenFile(protected, 0o600), protected.Close()); err != nil {
		return nil, err
	}
	return info, pathx.SyncRoot(root)
}

func monitorResultFile(ctx context.Context, root *os.Root, name string, expected os.FileInfo, limit int64, cancel context.CancelFunc) <-chan error {
	result := make(chan error, 1)
	go func() {
		defer close(result)
		interval := 100 * time.Millisecond
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				result <- nil
				return
			case <-timer.C:
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
				interval = min(interval*2, 2*time.Second)
				timer.Reset(interval)
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
	if err := pathx.ProtectPrivateOpenFile(file, 0o600); err != nil {
		_ = file.Close()
		_ = root.Remove(temporary)
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
	var selector struct {
		Schema string `json:"schema_version"`
	}
	if err := json.Unmarshal(content, &selector); err != nil {
		return Terminal{}, false, errors.New("existing worker terminal marker is invalid")
	}
	var terminal Terminal
	switch selector.Schema {
	case TerminalSchema:
		var legacy terminalV1
		if err := decodeStrictWorkerJSON(content, &legacy, "worker terminal marker"); err != nil {
			return Terminal{}, false, err
		}
		terminal = legacy.terminal()
	case SourceTerminalSchema:
		if err := decodeStrictWorkerJSON(content, &terminal, "worker terminal marker"); err != nil {
			return Terminal{}, false, err
		}
	default:
		return Terminal{}, false, errors.New("existing worker terminal marker schema is unsupported")
	}
	return terminal, true, nil
}

func readOrPromoteTerminal(ctx context.Context, root *os.Root, markerName, resultName string) (Terminal, bool, error) {
	terminal, found, err := readTerminal(ctx, root, markerName)
	if err != nil || found {
		return terminal, found, err
	}
	temporary := markerName + ".tmp"
	terminal, found, err = readTerminal(ctx, root, temporary)
	if err != nil || !found {
		return terminal, found, err
	}
	if err := ValidateTerminalIdentity(terminal, terminal.JobID, terminal.AttemptID); err != nil {
		return Terminal{}, false, fmt.Errorf("validate recoverable worker terminal temporary: %w", err)
	}
	if _, err := readDurableResult(ctx, root, resultName, terminal); err != nil {
		return Terminal{}, false, fmt.Errorf("validate recoverable worker terminal result: %w", err)
	}
	if err := root.Rename(temporary, markerName); err != nil {
		if existing, existingFound, existingErr := readTerminal(ctx, root, markerName); existingErr == nil && existingFound {
			return existing, true, nil
		}
		return Terminal{}, false, fmt.Errorf("promote recoverable worker terminal marker: %w", err)
	}
	if err := pathx.SyncRoot(root); err != nil {
		return Terminal{}, false, fmt.Errorf("sync promoted worker terminal marker: %w", err)
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
	base := jobArtifactBase(jobID)
	terminal, found, err := readOrPromoteTerminal(ctx, root, base+".json", base+".result.json")
	if err != nil || !found {
		return terminal, found, err
	}
	if err := ValidateTerminalIdentity(terminal, jobID, ""); err != nil {
		return Terminal{}, false, err
	}
	if _, err := readDurableResult(ctx, root, jobArtifactBase(jobID)+".result.json", terminal); err != nil {
		return Terminal{}, false, err
	}
	return terminal, true, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
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
