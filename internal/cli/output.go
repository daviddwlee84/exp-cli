package cli

import (
	"encoding/json"
	"io"
	"time"
)

// MachineSchema identifies the stable generic envelope used by exp commands.
const MachineSchema = "exp.cli/v1"

// DiagnosticSeverity is a machine-readable diagnostic classification.
type DiagnosticSeverity string

const (
	SeverityInfo    DiagnosticSeverity = "info"
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"
)

// Diagnostic reports a non-payload observation without writing human warnings
// into machine-readable stdout.
type Diagnostic struct {
	Severity DiagnosticSeverity `json:"severity"`
	Code     string             `json:"code"`
	Message  string             `json:"message"`
	Path     string             `json:"path,omitempty"`
}

// Envelope is the generic machine response shared by every JSON command. Data
// remains command-specific while the surrounding observation contract is
// stable.
type Envelope struct {
	SchemaVersion string       `json:"schema_version"`
	Command       string       `json:"command"`
	OK            bool         `json:"ok"`
	Partial       bool         `json:"partial"`
	ObservedAt    string       `json:"observed_at"`
	Data          any          `json:"data"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

// NewEnvelope constructs a deterministic machine response. observedAt is
// normalized to UTC, and an absent diagnostic list is encoded as [] rather
// than null.
func NewEnvelope(command string, ok, partial bool, observedAt time.Time, data any, diagnostics []Diagnostic) Envelope {
	if diagnostics == nil {
		diagnostics = []Diagnostic{}
	} else {
		copied := make([]Diagnostic, len(diagnostics))
		copy(copied, diagnostics)
		diagnostics = copied
	}

	if data == nil {
		data = struct{}{}
	}

	return Envelope{
		SchemaVersion: MachineSchema,
		Command:       command,
		OK:            ok,
		Partial:       partial,
		ObservedAt:    observedAt.UTC().Format(time.RFC3339Nano),
		Data:          data,
		Diagnostics:   diagnostics,
	}
}

// NewEnvelope timestamps a machine response using App's injected clock.
func (a *App) NewEnvelope(command string, ok, partial bool, data any, diagnostics []Diagnostic) Envelope {
	return NewEnvelope(command, ok, partial, a.observedAt(), data, diagnostics)
}

// WriteJSON renders only the machine contract to stdout.
func (a *App) WriteJSON(envelope Envelope) error {
	// Mark the attempt before writing so a short or failed writer cannot trigger a
	// second envelope from Execute and corrupt machine stdout further.
	a.jsonEnvelopeAttempted = true
	encoder := json.NewEncoder(a.Out)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(envelope)
}

// WriteHuman renders only human-oriented text to stdout.
func (a *App) WriteHuman(text string) error {
	_, err := io.WriteString(a.Out, text)
	return err
}
