// Package operation stores exp's private, local orchestration state.
//
// Canonical research meaning remains in Git-backed record files. This package
// only owns leases, idempotency, agent/scheduler jobs, provider observations,
// outbox intents, and rebuildable scheduling counters.
package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SchemaVersion = 5
	MaxJSONBytes  = 1 << 20
)

var (
	ErrUnsupported = errors.New("operational store is unsupported on this platform")
	ErrNotFound    = errors.New("operational record not found")
	ErrConflict    = errors.New("operational state conflict")
	ErrLeaseHeld   = errors.New("operational lease is held by another worker")
	ErrFenced      = errors.New("operational writer has a stale fencing token")
	ErrPaused      = errors.New("operational dispatch is paused")
)

type Option func(*options)

type options struct {
	clock func() time.Time
}

func WithClock(clock func() time.Time) Option {
	return func(value *options) { value.clock = clock }
}

type OperationState string

const (
	OperationPending   OperationState = "pending"
	OperationRunning   OperationState = "running"
	OperationSucceeded OperationState = "succeeded"
	OperationFailed    OperationState = "failed"
	OperationStale     OperationState = "stale"
)

func (state OperationState) valid() bool {
	switch state {
	case OperationPending, OperationRunning, OperationSucceeded, OperationFailed, OperationStale:
		return true
	default:
		return false
	}
}

type OperationInput struct {
	ID             string
	Kind           string
	SubjectID      string
	IdempotencyKey string
	SnapshotDigest string
	Payload        json.RawMessage
}

type Operation struct {
	OperationInput
	State     OperationState
	Result    json.RawMessage
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Lease struct {
	Subject      string
	Holder       string
	FencingToken int64
	ExpiresAt    time.Time
	UpdatedAt    time.Time
}

// DispatchAction is invoked while the operational store holds the same
// serialization gate used by pause changes and after it has fenced the daemon
// lease. It must perform only the one external scheduler submission.
type DispatchAction func() (int64, error)

type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobCancelled JobState = "cancelled"
	JobUnknown   JobState = "unknown"
)

func (state JobState) valid() bool {
	switch state {
	case JobQueued, JobRunning, JobSucceeded, JobFailed, JobCancelled, JobUnknown:
		return true
	default:
		return false
	}
}

func (state JobState) terminal() bool {
	return state == JobSucceeded || state == JobFailed || state == JobCancelled
}

type JobInput struct {
	ID             string
	IdempotencyKey string
	Kind           string
	Role           string
	SubjectID      string
	CanonicalScope string
	Pool           string
	Lane           string
	Units          int
	Profile        string
	Payload        json.RawMessage
	MaxAttempts    int
}

type Job struct {
	JobInput
	State          JobState
	ClaimedBy      string
	FencingToken   int64
	AttemptCount   int
	PueueTaskID    *int64
	MLflowRunID    string
	Result         json.RawMessage
	Error          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LeaseExpiresAt *time.Time
}

// ActiveAllocation is the minimal scheduler-accounting projection. It omits
// potentially large job payloads and results so admission cost is bounded by
// live work rather than research history.
type ActiveAllocation struct {
	PueueTaskID int64
	Pool        string
	Units       int
}

type OutboxState string

const (
	OutboxPending   OutboxState = "pending"
	OutboxRunning   OutboxState = "running"
	OutboxSucceeded OutboxState = "succeeded"
	OutboxFailed    OutboxState = "failed"
)

type OutboxInput struct {
	ID             string
	OperationID    string
	Kind           string
	IdempotencyKey string
	Payload        json.RawMessage
}

// OutboxFactory constructs an external side-effect intent after the job has a
// durable fencing token, while the same SQLite transaction is still open.
// Implementations must be deterministic and must not perform I/O.
type OutboxFactory func(Job) (OutboxInput, error)

type OutboxItem struct {
	OutboxInput
	State         OutboxState
	AttemptCount  int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ObservationInput struct {
	Provider   string
	Context    string
	SubjectID  string
	ObservedAt time.Time
	ExpiresAt  time.Time
	Partial    bool
	Payload    json.RawMessage
}

type Observation struct {
	ID int64
	ObservationInput
	CreatedAt time.Time
}

type Fairness struct {
	Pool         string
	ExploitUnits float64
	ExploreUnits float64
	UpdatedAt    time.Time
}

type RuntimeState struct {
	Paused    bool      `json:"paused"`
	Reason    string    `json:"reason,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ChooseLane applies weighted fair service to ready exploit/explore work. It
// is platform-independent even when the SQLite operational store is not.
func ChooseLane(fairness Fairness, exploitReady, exploreReady bool, exploitWeight, exploreWeight float64) (string, bool) {
	if !exploitReady && !exploreReady {
		return "", false
	}
	if !exploitReady {
		return "explore", true
	}
	if !exploreReady {
		return "exploit", true
	}
	if exploitWeight <= 0 || exploreWeight <= 0 {
		exploitWeight, exploreWeight = 80, 20
	}
	if fairness.ExploitUnits/exploitWeight <= fairness.ExploreUnits/exploreWeight {
		return "exploit", true
	}
	return "explore", true
}

func validateIdentifier(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 512 || !utf8.ValidString(value) {
		return fmt.Errorf("%s is empty, oversized, or invalid UTF-8", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func normalizeJSON(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage("{}"), nil
	}
	if len(value) > MaxJSONBytes {
		return nil, fmt.Errorf("JSON payload exceeds %d bytes", MaxJSONBytes)
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode JSON payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("JSON payload contains trailing data")
		}
		return nil, fmt.Errorf("decode trailing JSON payload: %w", err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("encode JSON payload: %w", err)
	}
	return canonical, nil
}

func utc(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}
