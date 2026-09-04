package cli

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/safex"
)

// ErrGuidedPlanStale means an authority input changed after review and before
// apply. The caller must gather and confirm a new plan; it must never silently
// rebuild and apply a different plan.
var ErrGuidedPlanStale = errors.New("guided plan became stale before apply")

type guidedPlanStaleError struct{ subject string }

func (failure *guidedPlanStaleError) Error() string {
	subject := safePromptText(failure.subject)
	if subject == "" {
		return ErrGuidedPlanStale.Error()
	}
	return subject + " changed after review: " + ErrGuidedPlanStale.Error()
}

func (failure *guidedPlanStaleError) Unwrap() error { return ErrGuidedPlanStale }

type guidedField struct {
	Label string
	Value string
}

type guidedTool struct {
	Name        string
	Status      string
	Reason      string
	Remediation string
	Available   bool
}

type guidedPlan struct {
	Title    string
	Summary  []guidedField
	Effects  []string
	Paths    []string
	Source   []guidedField
	Snapshot []guidedField
	Tools    []guidedTool
}

func writeGuidedPlan(app *App, plan guidedPlan) error {
	if app == nil {
		return errors.New("guided plan application is nil")
	}
	style := app.outStyle()
	var output strings.Builder
	title := safePromptText(plan.Title)
	if title == "" {
		title = "Review plan"
	}
	fmt.Fprintf(&output, "\n%s\n", style.title(title))
	writeGuidedFields(&output, style, "Summary", plan.Summary)
	writeGuidedList(&output, style, "Effects", plan.Effects)
	writeGuidedList(&output, style, "Paths", plan.Paths)
	writeGuidedFields(&output, style, "Source", plan.Source)
	writeGuidedFields(&output, style, "Snapshot", plan.Snapshot)
	writeGuidedTools(&output, style, plan.Tools)
	_, err := io.WriteString(app.Out, output.String())
	return err
}

func writeGuidedFields(output *strings.Builder, style cliStyle, heading string, fields []guidedField) {
	fmt.Fprintf(output, "\n%s\n", style.header(heading))
	if len(fields) == 0 {
		fmt.Fprintf(output, "  %s\n", style.label("None"))
		return
	}
	for _, field := range fields {
		label := safePromptText(field.Label)
		value := safePromptText(field.Value)
		if value == "" {
			value = "—"
		}
		fmt.Fprintf(output, "  %s %s\n", style.label(label+":"), value)
	}
}

func writeGuidedList(output *strings.Builder, style cliStyle, heading string, values []string) {
	fmt.Fprintf(output, "\n%s\n", style.header(heading))
	if len(values) == 0 {
		fmt.Fprintf(output, "  %s\n", style.label("None"))
		return
	}
	redactor := safex.NewRedactor()
	for _, value := range values {
		if heading == "Paths" {
			value = redactor.Path(value)
		}
		fmt.Fprintf(output, "  - %s\n", safePromptText(value))
	}
}

func writeGuidedTools(output *strings.Builder, style cliStyle, tools []guidedTool) {
	fmt.Fprintf(output, "\n%s\n", style.header("Optional-tool readiness (Optional tools)"))
	if len(tools) == 0 {
		fmt.Fprintf(output, "  %s\n", style.label("None"))
		return
	}
	for _, tool := range tools {
		name := safePromptText(tool.Name)
		status := safePromptText(tool.Status)
		if status == "" {
			status = "unknown"
		}
		painted := style.warning(status)
		if tool.Available {
			painted = style.success(status)
		} else if status == "missing" || status == "unsupported" || status == "misconfigured" {
			painted = style.danger(status)
		}
		fmt.Fprintf(output, "  - %s: %s", name, painted)
		if reason := safePromptText(tool.Reason); reason != "" {
			fmt.Fprintf(output, " — %s", reason)
		}
		output.WriteByte('\n')
		if remediation := safePromptText(tool.Remediation); remediation != "" {
			fmt.Fprintf(output, "    %s %s\n", style.label("Remediation:"), remediation)
		}
	}
}

// guidedFingerprint frames each field before hashing so concatenation cannot
// produce aliases. It is suitable for private freshness comparisons, not for
// canonical identities.
func guidedFingerprint(parts ...string) string {
	hash := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func requireSameGuidedObservation(subject, planned, current string) error {
	if planned != current {
		return &guidedPlanStaleError{subject: subject}
	}
	return nil
}
