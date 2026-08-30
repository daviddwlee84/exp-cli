package mlflow

import (
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

func TestSafeID(t *testing.T) {
	if !safeID("a-b_c:1.2") || safeID("bad id") || safeID("$(touch-x)") {
		t.Fatal("run id validation is not fail-closed")
	}
}
