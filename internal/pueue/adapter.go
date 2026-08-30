// Package pueue implements exp's narrow local Pueue 4.x scheduler adapter.
// It never exposes task environments, command strings, or raw status objects.
package pueue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

type State string

const (
	StateQueued           State = "queued"
	StateBlocked          State = "blocked"
	StateRunning          State = "running"
	StateSucceeded        State = "succeeded"
	StateFailed           State = "failed"
	StateCancelled        State = "cancelled"
	StateDependencyFailed State = "dependency_failed"
	StateUnknown          State = "unknown"
)

type Task struct {
	ID           int64      `json:"id"`
	Label        string     `json:"label"`
	Group        string     `json:"group"`
	Priority     int        `json:"priority"`
	Dependencies []int64    `json:"dependencies"`
	State        State      `json:"state"`
	NativeState  string     `json:"native_state"`
	NativeReason string     `json:"native_reason,omitempty"`
	ExitCode     *int       `json:"exit_code,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
}

type Group struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Parallelism int    `json:"parallelism"`
}

type Snapshot struct {
	Tasks  []Task  `json:"tasks"`
	Groups []Group `json:"groups"`
}

type SubmitRequest struct {
	Group       string
	Label       string
	Priority    int
	WorkingDir  string
	Worker      string
	WorkerArgs  []string
	Environment execx.Environment
}

type Adapter struct {
	Invoker      execx.Invoker
	LookupBinary func(string) (string, error)
	Timeout      time.Duration
}

func (adapter Adapter) Status(ctx context.Context) (Snapshot, error) {
	result, err := adapter.invoke(ctx, []string{"status", "--json"}, "", execx.Environment{})
	if err != nil {
		return Snapshot{}, err
	}
	return ParseStatus([]byte(result.Stdout))
}

func (adapter Adapter) Submit(ctx context.Context, request SubmitRequest) (int64, error) {
	if err := validateSubmit(request); err != nil {
		return 0, err
	}
	command, err := WorkerCommand(request.Worker, request.WorkerArgs)
	if err != nil {
		return 0, err
	}
	arguments := []string{"add", "--print-task-id", "--group", request.Group, "--label", request.Label,
		"--priority", strconv.Itoa(request.Priority), "--working-directory", request.WorkingDir, command}
	result, err := adapter.invoke(ctx, arguments, request.WorkingDir, request.Environment)
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(strings.TrimSpace(result.Stdout), 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("parse Pueue task id from bounded output")
	}
	return id, nil
}

func (adapter Adapter) Cancel(ctx context.Context, taskID int64, environment execx.Environment) error {
	if taskID < 0 {
		return errors.New("Pueue task id cannot be negative")
	}
	_, err := adapter.invoke(ctx, []string{"kill", strconv.FormatInt(taskID, 10)}, "", environment)
	return err
}

func (adapter Adapter) invoke(ctx context.Context, arguments []string, cwd string, environment execx.Environment) (execx.Result, error) {
	lookup := adapter.LookupBinary
	if lookup == nil {
		lookup = exec.LookPath
	}
	binary, err := lookup("pueue")
	if err != nil {
		return execx.Result{ExitCode: -1}, fmt.Errorf("resolve pueue: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return execx.Result{ExitCode: -1}, err
	}
	if cwd == "" {
		cwd = filepath.Dir(binary)
	}
	if len(environment.Variables()) == 0 {
		environment, err = execx.MinimalEnvironment()
		if err != nil {
			return execx.Result{ExitCode: -1}, err
		}
	}
	timeout := adapter.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	spec := execx.CommandSpec{
		Executable: filepath.Clean(binary), Argv: append([]string(nil), arguments...), CWD: filepath.Clean(cwd),
		Environment: environment, Timeout: timeout, Output: execx.OutputPolicy{Mode: execx.OutputCapture, MaxStdoutBytes: 32 << 20, MaxStderrBytes: 1 << 20},
		Redaction: execx.NewRedactor(),
	}
	invoker := adapter.Invoker
	if invoker == nil {
		invoker = execx.NewInvoker()
	}
	return invoker.Invoke(ctx, spec)
}

func validateSubmit(request SubmitRequest) error {
	for name, value := range map[string]string{"group": request.Group, "label": request.Label} {
		if !safeToken(value) {
			return fmt.Errorf("Pueue %s contains unsupported characters", name)
		}
	}
	if !filepath.IsAbs(request.WorkingDir) || filepath.Clean(request.WorkingDir) != request.WorkingDir {
		return errors.New("Pueue working directory must be a clean absolute path")
	}
	if request.Priority < -100000 || request.Priority > 100000 {
		return errors.New("Pueue priority is outside the supported range")
	}
	return nil
}

func WorkerCommand(worker string, arguments []string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("Pueue worker submission is unsupported on Windows because its shell envelope is POSIX-specific")
	}
	if !filepath.IsAbs(worker) || filepath.Clean(worker) != worker || !utf8.ValidString(worker) || strings.ContainsRune(worker, 0) {
		return "", errors.New("worker executable must be a clean absolute path")
	}
	parts := []string{quotePOSIX(worker)}
	for _, argument := range arguments {
		if !safeToken(argument) {
			return "", fmt.Errorf("worker argument %q is not a safe envelope token", argument)
		}
		parts = append(parts, quotePOSIX(argument))
	}
	return strings.Join(parts, " "), nil
}

func quotePOSIX(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }

func safeToken(value string) bool {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
		if !(unicode.IsLetter(character) || unicode.IsDigit(character) || strings.ContainsRune("._:@/+-=", character)) {
			return false
		}
	}
	return true
}

func ParseStatus(data []byte) (Snapshot, error) {
	if len(data) == 0 || len(data) > 32<<20 {
		return Snapshot{}, errors.New("Pueue status output is empty or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		return Snapshot{}, fmt.Errorf("decode Pueue status: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Snapshot{}, errors.New("Pueue status output contains trailing JSON")
	}
	stripSensitive(raw)
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Snapshot{}, err
	}
	var status struct {
		Tasks  map[string]rawTask  `json:"tasks"`
		Groups map[string]rawGroup `json:"groups"`
	}
	if err := json.Unmarshal(encoded, &status); err != nil {
		return Snapshot{}, fmt.Errorf("parse sanitized Pueue status: %w", err)
	}
	snapshot := Snapshot{Tasks: []Task{}, Groups: []Group{}}
	for key, raw := range status.Tasks {
		task, err := normalizeTask(key, raw)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Tasks = append(snapshot.Tasks, task)
	}
	for name, raw := range status.Groups {
		snapshot.Groups = append(snapshot.Groups, Group{Name: name, State: raw.Status, Parallelism: raw.ParallelTasks})
	}
	sort.Slice(snapshot.Tasks, func(i, j int) bool { return snapshot.Tasks[i].ID < snapshot.Tasks[j].ID })
	sort.Slice(snapshot.Groups, func(i, j int) bool { return snapshot.Groups[i].Name < snapshot.Groups[j].Name })
	return snapshot, nil
}

type rawTask struct {
	ID           int64           `json:"id"`
	Group        string          `json:"group"`
	Dependencies []int64         `json:"dependencies"`
	Priority     int             `json:"priority"`
	Label        *string         `json:"label"`
	Status       json.RawMessage `json:"status"`
}

type rawGroup struct {
	Status        string `json:"status"`
	ParallelTasks int    `json:"parallel_tasks"`
}

func normalizeTask(key string, raw rawTask) (Task, error) {
	if raw.ID == 0 && key != "0" {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return Task{}, fmt.Errorf("invalid Pueue task key %q", key)
		}
		raw.ID = id
	}
	task := Task{ID: raw.ID, Group: raw.Group, Priority: raw.Priority, Dependencies: append([]int64(nil), raw.Dependencies...), State: StateUnknown}
	if raw.Label != nil {
		task.Label = *raw.Label
	}
	var status map[string]json.RawMessage
	if err := json.Unmarshal(raw.Status, &status); err != nil || len(status) != 1 {
		task.NativeReason = "unrecognized status shape"
		return task, nil
	}
	for native, detail := range status {
		task.NativeState = native
		switch native {
		case "Queued":
			task.State = StateQueued
		case "Stashed", "Paused", "Locked":
			task.State = StateBlocked
		case "Running", "Starting":
			task.State = StateRunning
		case "Done":
			parseDone(&task, detail)
		default:
			task.State = StateUnknown
			task.NativeReason = "unknown native state"
		}
	}
	return task, nil
}

func parseDone(task *Task, detail json.RawMessage) {
	var done struct {
		Start  *time.Time      `json:"start"`
		End    *time.Time      `json:"end"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(detail, &done); err != nil {
		task.State, task.NativeReason = StateUnknown, "invalid Done payload"
		return
	}
	task.StartedAt, task.EndedAt = done.Start, done.End
	var text string
	if err := json.Unmarshal(done.Result, &text); err == nil {
		task.NativeReason = text
		switch text {
		case "Success":
			task.State = StateSucceeded
		case "Killed":
			task.State = StateCancelled
		case "DependencyFailed":
			task.State = StateDependencyFailed
		default:
			task.State = StateUnknown
		}
		return
	}
	var failed map[string]int
	if err := json.Unmarshal(done.Result, &failed); err == nil {
		if code, ok := failed["Failed"]; ok {
			task.State, task.NativeReason, task.ExitCode = StateFailed, "Failed", &code
			return
		}
	}
	task.State, task.NativeReason = StateUnknown, "unknown Done result"
}

func stripSensitive(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if strings.EqualFold(key, "envs") || strings.EqualFold(key, "environment") {
				delete(current, key)
				continue
			}
			stripSensitive(child)
		}
	case []any:
		for _, child := range current {
			stripSensitive(child)
		}
	}
}
