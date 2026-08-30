//go:build aix

package operation

import (
	"context"
	"encoding/json"
	"time"
)

// Store is a compile-time stub on AIX. Canonical Git-only commands remain
// available, but daemon/orchestration commands must report ErrUnsupported.
type Store struct{}

func PathFor(string) (string, error)                          { return "", ErrUnsupported }
func Open(context.Context, string, ...Option) (*Store, error) { return nil, ErrUnsupported }
func (*Store) Close() error                                   { return nil }
func (*Store) Path() string                                   { return "" }
func (*Store) BeginOperation(context.Context, OperationInput) (Operation, bool, error) {
	return Operation{}, false, ErrUnsupported
}
func (*Store) SetOperationState(context.Context, string, OperationState, json.RawMessage, string) (Operation, error) {
	return Operation{}, ErrUnsupported
}
func (*Store) GetOperationByKey(context.Context, string) (Operation, error) {
	return Operation{}, ErrUnsupported
}
func (*Store) AcquireLease(context.Context, string, string, time.Duration) (Lease, error) {
	return Lease{}, ErrUnsupported
}
func (*Store) RenewLease(context.Context, Lease, time.Duration) (Lease, error) {
	return Lease{}, ErrUnsupported
}
func (*Store) WithDispatchLease(context.Context, Lease, time.Duration, DispatchAction) (int64, Lease, error) {
	return 0, Lease{}, ErrUnsupported
}
func (*Store) ReleaseLease(context.Context, Lease) error { return ErrUnsupported }
func (*Store) EnqueueJob(context.Context, JobInput) (Job, bool, error) {
	return Job{}, false, ErrUnsupported
}
func (*Store) ClaimJob(context.Context, string, string, string, time.Duration) (Job, error) {
	return Job{}, ErrUnsupported
}
func (*Store) ClaimJobByID(context.Context, string, string, time.Duration) (Job, error) {
	return Job{}, ErrUnsupported
}
func (*Store) PrepareSubmission(context.Context, JobInput, string, time.Duration, OutboxFactory) (Job, OutboxItem, bool, error) {
	return Job{}, OutboxItem{}, false, ErrUnsupported
}
func (*Store) FinishJob(context.Context, string, int64, JobState, json.RawMessage, string) (Job, error) {
	return Job{}, ErrUnsupported
}
func (*Store) SetJobExternalRefs(context.Context, string, int64, *int64, string) error {
	return ErrUnsupported
}
func (*Store) ListJobs(context.Context, ...JobState) ([]Job, error) { return nil, ErrUnsupported }
func (*Store) ListUnreconciledTerminalJobs(context.Context, string, int) ([]Job, error) {
	return nil, ErrUnsupported
}
func (*Store) MarkJobReconciled(context.Context, string, int64) error { return ErrUnsupported }
func (*Store) ListActiveAllocations(context.Context) ([]ActiveAllocation, error) {
	return nil, ErrUnsupported
}
func (*Store) GetJob(context.Context, string) (Job, error) { return Job{}, ErrUnsupported }
func (*Store) AddOutbox(context.Context, OutboxInput, time.Time) (OutboxItem, bool, error) {
	return OutboxItem{}, false, ErrUnsupported
}
func (*Store) DueOutbox(context.Context, int) ([]OutboxItem, error) { return nil, ErrUnsupported }
func (*Store) DueOutboxForScope(context.Context, string, int) ([]OutboxItem, error) {
	return nil, ErrUnsupported
}
func (*Store) SetOutboxState(context.Context, string, OutboxState, time.Time, string) error {
	return ErrUnsupported
}
func (*Store) RecordObservation(context.Context, ObservationInput) (Observation, error) {
	return Observation{}, ErrUnsupported
}
func (*Store) RecordDispatch(context.Context, string, string, float64) (Fairness, error) {
	return Fairness{}, ErrUnsupported
}
func (*Store) RecordDispatchOnce(context.Context, string, string, string, float64) (Fairness, error) {
	return Fairness{}, ErrUnsupported
}
func (*Store) Fairness(context.Context, string) (Fairness, error) { return Fairness{}, ErrUnsupported }
func (*Store) SetPaused(context.Context, bool, string) (RuntimeState, error) {
	return RuntimeState{}, ErrUnsupported
}
func (*Store) RuntimeState(context.Context) (RuntimeState, error) {
	return RuntimeState{}, ErrUnsupported
}
func (*Store) RemoveIfEmpty() error { return ErrUnsupported }
