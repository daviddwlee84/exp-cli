package mlflow

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	AttemptOwnershipTag    = "exp.attempt_id"
	MaxAttachmentJSONBytes = 128 << 10
)

type ObservationState string

const (
	ObservationVerified    ObservationState = "verified"
	ObservationUnverified  ObservationState = "unverified"
	ObservationUnavailable ObservationState = "unavailable"
)

// Attachment is the bounded, replay-safe result of observing one workload-owned
// run. It contains only selected fields and a URI that already crossed the
// provider sanitization boundary.
type Attachment struct {
	Profile      string             `json:"profile"`
	Context      string             `json:"context"`
	RunID        string             `json:"run_id"`
	ExperimentID string             `json:"experiment_id,omitempty"`
	Status       string             `json:"status,omitempty"`
	ArtifactURI  string             `json:"artifact_uri,omitempty"`
	Metrics      map[string]float64 `json:"metrics"`
	Tags         map[string]string  `json:"tags"`
	ObservedAt   time.Time          `json:"observed_at"`
	State        ObservationState   `json:"state"`
	Reasons      []string           `json:"reasons"`
}

// ObserveAttachment performs only the adapter's explicit read-only describe
// operation. Provider absence, environment absence, and assertion mismatches are
// evidence states, not workload execution failures.
func ObserveAttachment(ctx context.Context, profile WorkloadProfile, runID, attemptID, cwd string, invoker execx.Invoker, lookup func(string) (string, error), clock func() time.Time) Attachment {
	observedAt := time.Now().UTC()
	if clock != nil {
		observedAt = clock().UTC()
	}
	attachment := Attachment{
		Profile: profile.Name, Context: profile.Context, RunID: runID,
		Metrics: map[string]float64{}, Tags: map[string]string{}, ObservedAt: observedAt,
		State: ObservationUnavailable, Reasons: []string{},
	}
	if err := profile.Validate(); err != nil {
		attachment.Reasons = []string{"profile-invalid"}
		return attachment
	}
	if !ValidRunID(runID) {
		attachment.RunID = ""
		attachment.State = ObservationUnverified
		attachment.Reasons = []string{"run-id-invalid"}
		return attachment
	}
	if _, err := research.ParseIDForKind(attemptID, research.KindAttempt); err != nil {
		attachment.State = ObservationUnverified
		attachment.Reasons = []string{"attempt-id-invalid"}
		return attachment
	}
	environment, err := profile.EnvironmentPolicy()
	if err != nil {
		attachment.Reasons = []string{"profile-environment-invalid"}
		return attachment
	}
	timeout, err := profile.TimeoutDuration()
	if err != nil {
		attachment.Reasons = []string{"profile-timeout-invalid"}
		return attachment
	}
	run, err := (Adapter{Invoker: invoker, LookupBinary: lookup, Binary: profile.Binary, Timeout: timeout}).Describe(ctx, DescribeRequest{
		RunID: runID, MetricNames: append([]string{}, profile.DefaultMetrics...),
		ExpectedTags: map[string]string{AttemptOwnershipTag: attemptID}, Environment: environment, CWD: cwd,
	})
	if err != nil {
		attachment.Reasons = []string{"provider-unavailable"}
		return attachment
	}
	attachment.ExperimentID = run.ExperimentID
	attachment.Status = run.Status
	attachment.ArtifactURI = run.ArtifactURI
	attachment.Metrics = cloneMetrics(run.Metrics)
	attachment.Tags = cloneTags(run.Tags)
	attachment.Reasons = append([]string{}, run.Diagnostics...)
	sort.Strings(attachment.Reasons)
	if run.Verified && run.RunID == runID && run.Tags[AttemptOwnershipTag] == attemptID {
		attachment.State = ObservationVerified
	} else {
		attachment.State = ObservationUnverified
		if len(attachment.Reasons) == 0 {
			attachment.Reasons = []string{"ownership-unverified"}
		}
	}
	return attachment
}

func (attachment Attachment) Validate() error {
	if !profileNamePattern.MatchString(attachment.Profile) || !profileNamePattern.MatchString(attachment.Context) || !ValidRunID(attachment.RunID) {
		return errors.New("MLflow attachment identity is invalid")
	}
	if attachment.ObservedAt.IsZero() || attachment.ObservedAt.Location() != time.UTC {
		return errors.New("MLflow attachment observation time is invalid")
	}
	if attachment.ExperimentID != "" && !ValidRunID(attachment.ExperimentID) || attachment.Status != "" && !ValidRunID(attachment.Status) {
		return errors.New("MLflow attachment selected identity is invalid")
	}
	if err := provider.ValidateCanonicalURI(attachment.ArtifactURI); err != nil {
		return errors.New("MLflow attachment artifact URI is unsafe")
	}
	if attachment.State != ObservationVerified && attachment.State != ObservationUnverified && attachment.State != ObservationUnavailable {
		return errors.New("MLflow attachment state is invalid")
	}
	if len(attachment.Metrics) > 256 || len(attachment.Tags) > 64 || len(attachment.Reasons) > 256 {
		return errors.New("MLflow attachment exceeds its field limit")
	}
	for name := range attachment.Metrics {
		if !profileMetric.MatchString(name) || strings.Contains(name, "..") {
			return errors.New("MLflow attachment metric is invalid")
		}
	}
	for name, value := range attachment.Tags {
		if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") || len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("MLflow attachment tag is invalid")
		}
	}
	for _, reason := range attachment.Reasons {
		if reason == "" || len(reason) > 512 || strings.ContainsAny(reason, "\x00\r\n") || execx.NewRedactor().Text(reason) != reason {
			return errors.New("MLflow attachment reason is invalid")
		}
	}
	if attachment.State == ObservationVerified && attachment.Tags[AttemptOwnershipTag] == "" {
		return errors.New("verified MLflow attachment omits Attempt ownership")
	}
	encoded, err := json.Marshal(attachment)
	if err != nil || len(encoded) > MaxAttachmentJSONBytes {
		return errors.New("MLflow attachment exceeds its encoded-byte limit")
	}
	return nil
}

// ExternalRef converts only exact verified ownership into canonical evidence.
func (attachment Attachment) ExternalRef(attemptID string) (research.ExternalRef, error) {
	if err := attachment.Validate(); err != nil {
		return research.ExternalRef{}, err
	}
	if attachment.State != ObservationVerified || attachment.Tags[AttemptOwnershipTag] != attemptID {
		return research.ExternalRef{}, errors.New("MLflow attachment is not owned by the exact Attempt")
	}
	metrics := make(map[string]any, len(attachment.Metrics))
	for name, value := range attachment.Metrics {
		metrics[name] = value
	}
	observed := attachment.ObservedAt.UTC()
	return research.ExternalRef{
		Role: research.ExternalTracker, Provider: "mlflow", Context: attachment.Context,
		NativeKind: "run", NativeID: attachment.RunID, URI: attachment.ArtifactURI, ObservedAt: &observed,
		Metadata: map[string]any{
			"mlflow.profile": attachment.Profile, "mlflow.status": attachment.Status,
			"mlflow.experiment_id": attachment.ExperimentID, "mlflow.verified": true,
			"mlflow.owner_attempt": attemptID, "mlflow.metrics": metrics,
		},
	}, nil
}

func cloneMetrics(input map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneTags(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
