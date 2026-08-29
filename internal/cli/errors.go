package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/skill"
)

func commandFailure(app *App, machine bool, command string, data any, partial bool, diagnostics []Diagnostic, err error) error {
	if err == nil {
		err = errors.New("command failed")
	}
	if len(diagnostics) == 0 {
		diagnostics = diagnosticsForError(err)
	}
	diagnostics = safeDiagnostics(diagnostics)
	if machine {
		if writeErr := app.WriteJSON(app.NewEnvelope(command, false, partial, data, diagnostics)); writeErr != nil {
			return safeCLIError(errors.Join(err, fmt.Errorf("write JSON failure envelope: %w", writeErr)))
		}
	}
	return safeCLIError(err)
}

func commandSuccess(app *App, machine bool, command string, data any, partial bool, diagnostics []Diagnostic, human string) error {
	if machine {
		return successfulOutputError(app.WriteJSON(app.NewEnvelope(command, true, partial, data, safeDiagnostics(diagnostics))))
	}
	return successfulOutputError(app.WriteHuman(safeHumanOutput(human)))
}

const maxCLIDiagnosticBytes = 16 << 10

type redactedCLIError struct {
	message string
	cause   error
}

func (e *redactedCLIError) Error() string    { return e.message }
func (e *redactedCLIError) String() string   { return e.message }
func (e *redactedCLIError) GoString() string { return e.message }
func (e *redactedCLIError) Unwrap() error    { return e.cause }

func safeCLIError(err error) error {
	if err == nil {
		return nil
	}
	message, _ := safex.NewRedactor().SafeDiagnostic(err.Error(), maxCLIDiagnosticBytes)
	if message == "" {
		message = "command failed"
	}
	return &redactedCLIError{message: message, cause: err}
}

func safeDiagnosticText(value string) string {
	safe, _ := safex.NewRedactor().SafeDiagnostic(value, maxCLIDiagnosticBytes)
	return safe
}

func safeHumanOutput(value string) string {
	return safex.NewRedactor().DiagnosticOutput(value)
}

func safeDiagnostics(input []Diagnostic) []Diagnostic {
	if input == nil {
		return []Diagnostic{}
	}
	output := make([]Diagnostic, len(input))
	for index, diagnostic := range input {
		output[index] = diagnostic
		output[index].Message = safeDiagnosticText(diagnostic.Message)
		output[index].Path = safeDiagnosticText(diagnostic.Path)
	}
	return output
}

func diagnosticsForError(err error) []Diagnostic {
	if err == nil {
		return []Diagnostic{}
	}
	if published, subject, path := publishedFailure(err); published {
		return durabilityUncertainDiagnostics(subject, path)
	}
	var inventoryError *record.InventoryError
	if errors.As(err, &inventoryError) {
		return convertRecordDiagnostics(inventoryError.Diagnostics)
	}
	var projectionInventory *projection.InventoryError
	if errors.As(err, &projectionInventory) {
		return convertRecordDiagnostics(projectionInventory.Diagnostics)
	}
	if diagnostics := record.DiagnosticsForError("", err); len(diagnostics) > 0 {
		if len(diagnostics) != 1 || diagnostics[0].Code != "record.io" {
			return convertRecordDiagnostics(diagnostics)
		}
	}
	code := "command.failed"
	switch {
	case errors.Is(err, context.Canceled):
		code = "command.canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "command.deadline_exceeded"
	case errors.Is(err, projection.ErrStale):
		code = "projection.stale"
	case errors.Is(err, projection.ErrInvalidInventory), errors.Is(err, record.ErrInvalidInventory):
		code = "inventory.invalid"
	}
	return []Diagnostic{{Severity: SeverityError, Code: code, Message: safeDiagnosticText(err.Error())}}
}

func convertRecordDiagnostics(input []record.Diagnostic) []Diagnostic {
	output := make([]Diagnostic, 0, len(input))
	for _, diagnostic := range input {
		output = append(output, Diagnostic{
			Severity: SeverityError,
			Code:     diagnostic.Code,
			Message:  safeDiagnosticText(diagnostic.Message),
			Path:     safeDiagnosticText(diagnostic.Path),
		})
	}
	if output == nil {
		output = []Diagnostic{}
	}
	return output
}

func publicationWasPublished(err error) bool {
	published, _, _ := publishedFailure(err)
	return published
}

func publishedFailure(err error) (published bool, subject, path string) {
	var skillPublication *skill.PublicationError
	if errors.As(err, &skillPublication) && skillPublication.Published {
		return true, "skill", skillPublication.Path
	}
	var recordPublication *record.PublicationError
	if errors.As(err, &recordPublication) && recordPublication.Published {
		return true, "canonical", ""
	}
	return false, "", ""
}

func durabilityUncertainDiagnostics(subject, path string) []Diagnostic {
	return []Diagnostic{{
		Severity: SeverityError,
		Code:     "publication.durability_uncertain",
		Message:  safeDiagnosticText(subject + " bytes were published, but durable directory synchronization could not be confirmed"),
		Path:     safeDiagnosticText(path),
	}}
}

func projectionDriftDiagnostics(files []string) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, len(files))
	for _, path := range files {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: SeverityError,
			Code:     "projection.stale",
			Message:  "generated projection differs from canonical records",
			Path:     safeDiagnosticText(path),
		})
	}
	return diagnostics
}
