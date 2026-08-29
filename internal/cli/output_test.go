package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestEnvelopeUsesStableSchemaAndInjectedUTCClock(t *testing.T) {
	var stdout bytes.Buffer
	app := NewApp(t.Context(), nil, &stdout, nil)
	app.Now = func() time.Time {
		return time.Date(2026, time.August, 29, 15, 4, 5, 600, time.FixedZone("test", -7*60*60))
	}

	envelope := app.NewEnvelope("doctor", true, false, struct {
		Status string `json:"status"`
	}{Status: "ready"}, nil)
	if err := app.WriteJSON(envelope); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	want := "{\"schema_version\":\"exp.cli/v1\",\"command\":\"doctor\",\"ok\":true,\"partial\":false,\"observed_at\":\"2026-08-29T22:04:05.0000006Z\",\"data\":{\"status\":\"ready\"},\"diagnostics\":[]}\n"
	if got := stdout.String(); got != want {
		t.Fatalf("machine output mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestWarningNeverContaminatesMachineStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := NewApp(t.Context(), nil, &stdout, &stderr)
	app.Now = func() time.Time { return time.Unix(0, 0) }

	if err := app.Warnf("optional tool %s is unavailable", "tracker"); err != nil {
		t.Fatalf("Warnf() error = %v", err)
	}
	if err := app.WriteJSON(app.NewEnvelope("doctor", true, true, nil, []Diagnostic{{
		Severity: SeverityWarning,
		Code:     "tool_unavailable",
		Message:  "optional tracker is unavailable",
	}})); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	if bytes.Contains(stdout.Bytes(), []byte("exp: warning:")) {
		t.Fatalf("stdout contains a human warning: %q", stdout.String())
	}
	if got, want := stderr.String(), "exp: warning: optional tool tracker is unavailable\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestWriteHumanIsSeparateFromJSONRendering(t *testing.T) {
	var stdout bytes.Buffer
	app := NewApp(t.Context(), nil, &stdout, nil)
	if err := app.WriteHuman("human summary\n"); err != nil {
		t.Fatalf("WriteHuman() error = %v", err)
	}
	if got, want := stdout.String(), "human summary\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestCommandSuccessTreatsClosedResultPipeAsConsumerAbandonment(t *testing.T) {
	for _, machine := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[machine], func(t *testing.T) {
			app := NewApp(t.Context(), nil, writerFunc(func([]byte) (int, error) {
				return 0, io.ErrClosedPipe
			}), nil)
			if err := commandSuccess(app, machine, "test", struct{}{}, false, nil, "result\n"); err != nil {
				t.Fatalf("closed result pipe became command failure: %v", err)
			}
		})
	}
}

func TestCommandSuccessPreservesRealWriterErrors(t *testing.T) {
	sentinel := errors.New("result writer failed")
	for name, writerErr := range map[string]error{
		"ordinary error":          sentinel,
		"joined with closed pipe": errors.Join(io.ErrClosedPipe, sentinel),
	} {
		t.Run(name, func(t *testing.T) {
			app := NewApp(t.Context(), nil, writerFunc(func([]byte) (int, error) {
				return 0, writerErr
			}), nil)
			if err := commandSuccess(app, false, "test", struct{}{}, false, nil, "result\n"); !errors.Is(err, sentinel) {
				t.Fatalf("commandSuccess() error = %v, want writer error", err)
			}
		})
	}
}

func TestPlanListHumanResultIsCompleteAndRedactedBeyondDiagnosticLimit(t *testing.T) {
	const (
		planCount = 130
		canary    = "plan-list-human-canary-604e"
	)
	plans := make([]planView, 0, planCount)
	for index := range planCount {
		title := fmt.Sprintf("Plan %03d %s", index, strings.Repeat("detail-", 24))
		if index == planCount/2 {
			title += " auth_value=" + canary
		}
		plans = append(plans, planView{
			ID:       fmt.Sprintf("plan-row-%03d", index),
			Display:  fmt.Sprintf("P-%08d", index),
			State:    "queued",
			Priority: "P1",
			Effort:   "S",
			Title:    title,
			Revision: fmt.Sprintf("sha256:%064d", index),
		})
	}
	human := renderPlanListHuman(plans)
	if len(human) <= maxCLIDiagnosticBytes {
		t.Fatalf("test Plan list is only %d bytes", len(human))
	}

	var stdout bytes.Buffer
	app := NewApp(t.Context(), nil, &stdout, nil)
	if err := commandSuccess(app, false, "plan list", planListData{}, false, nil, human); err != nil {
		t.Fatal(err)
	}
	result := stdout.String()
	if strings.Contains(result, canary) || !strings.Contains(result, "[REDACTED]") {
		t.Fatalf("human Plan list was not redacted")
	}
	if strings.Contains(result, "[TRUNCATED]") {
		t.Fatalf("human Plan list was silently truncated at %d bytes", len(result))
	}
	for index := range planCount {
		id := fmt.Sprintf("plan-row-%03d", index)
		if strings.Count(result, id) != 1 {
			t.Fatalf("human Plan list contains %d copies of %s, want one", strings.Count(result, id), id)
		}
	}

	diagnostic := safeDiagnosticText(strings.Repeat("x", maxCLIDiagnosticBytes+1))
	if len(diagnostic) > maxCLIDiagnosticBytes || !strings.Contains(diagnostic, "[TRUNCATED]") {
		t.Fatalf("diagnostic limit changed: %d bytes, suffix=%q", len(diagnostic), diagnostic[len(diagnostic)-16:])
	}
}

type writerFunc func([]byte) (int, error)

func (write writerFunc) Write(content []byte) (int, error) {
	return write(content)
}
