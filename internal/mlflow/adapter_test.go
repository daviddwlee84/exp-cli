package mlflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseDescribeReturnsOnlyRequestedSafeFields(t *testing.T) {
	raw := []byte(`{
  "info": {
    "run_id": "abc123",
    "experiment_id": "7",
    "status": "FINISHED",
    "artifact_uri": "https://user:secret@example.test/bucket?token=bad"
  },
  "data": {
    "metrics": {"macro_f1": 0.91, "private_metric": 123},
    "params": {"api_key": "should-not-cross"},
    "tags": {"exp.attempt_id": "att_1", "secret": "hidden"}
  }
}`)
	run, err := ParseDescribe(raw, DescribeRequest{
		RunID: "abc123", MetricNames: []string{"macro_f1"}, ExpectedTags: map[string]string{"exp.attempt_id": "att_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !run.Verified || len(run.Metrics) != 1 || run.Metrics["macro_f1"] != 0.91 || len(run.Tags) != 1 {
		t.Fatalf("run = %#v", run)
	}
	if strings.Contains(run.ArtifactURI, "secret") || strings.Contains(run.ArtifactURI, "token") || strings.Contains(run.ArtifactURI, "?") {
		t.Fatalf("unsafe artifact uri = %q", run.ArtifactURI)
	}
}

func TestParseDescribeReportsMissingOrMismatchedFields(t *testing.T) {
	raw := []byte(`{"info":{"run_id":"different","experiment_id":"7","status":"RUNNING"},"data":{"metrics":{},"tags":{"exp.attempt_id":"other"}}}`)
	run, err := ParseDescribe(raw, DescribeRequest{
		RunID: "expected", MetricNames: []string{"loss"}, ExpectedTags: map[string]string{"exp.attempt_id": "att_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Verified || len(run.Diagnostics) != 4 {
		t.Fatalf("run = %#v", run)
	}
}

func TestParseDescribeRejectsUnsafeIdentitiesAndRedactsSelectedSensitiveTags(t *testing.T) {
	unsafe := []byte(`{"info":{"run_id":"token=LEAK","experiment_id":"7","status":"FINISHED"},"data":{"metrics":{},"tags":{}}}`)
	if _, err := ParseDescribe(unsafe, DescribeRequest{RunID: "expected", ExpectedTags: map[string]string{"exp.attempt_id": "att_1"}}); err == nil || strings.Contains(err.Error(), "LEAK") {
		t.Fatalf("unsafe returned run identity error = %v", err)
	}
	raw := []byte(`{"info":{"run_id":"safe-run","experiment_id":"password=LEAK","status":"FINISHED"},"data":{"metrics":{},"tags":{"api_token":"SECRET"}}}`)
	run, err := ParseDescribe(raw, DescribeRequest{RunID: "safe-run", ExpectedTags: map[string]string{"api_token": "SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(run)
	if run.Verified || run.ExperimentID != "" || run.Tags["api_token"] != "[REDACTED]" || strings.Contains(string(encoded), "SECRET") {
		t.Fatalf("unsafe selected metadata crossed parser: %#v", run)
	}
}

func TestParseDescribeCanonicalizesArtifactURIAndRejectsEmptyAssertions(t *testing.T) {
	raw := []byte(`{"info":{"run_id":"run-1","experiment_id":"7","status":"FINISHED","artifact_uri":"https://tracking.example/runs/1?version=2"},"data":{"metrics":{"score":1},"tags":{}}}`)
	run, err := ParseDescribe(raw, DescribeRequest{RunID: "run-1", MetricNames: []string{"score"}})
	if err != nil || !run.Verified || run.ArtifactURI != "https://tracking.example/runs/1" {
		t.Fatalf("canonical artifact URI = %#v, %v", run, err)
	}
	if _, err := ParseDescribe(raw, DescribeRequest{RunID: "run-1", MetricNames: []string{""}}); err == nil {
		t.Fatal("empty metric selector bypassed assertion validation")
	}
	if _, err := ParseDescribe(raw, DescribeRequest{RunID: "run-1", MetricNames: []string{}}); err == nil {
		t.Fatal("empty normalized assertion set was accepted")
	}
}

func TestSafeID(t *testing.T) {
	if !safeID("a-b_c:1.2") || safeID("bad id") || safeID("$(touch-x)") {
		t.Fatal("run id validation is not fail-closed")
	}
}
