package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/lifecycle"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/skill"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

var ErrExternalSourceExecutionUnsupported = errors.New("external Source execution is not supported in this foundation")

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
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "projection.refresh_failed" || diagnostic.Code == "projection.refresh_skipped" {
			partial = true
		}
	}
	if machine {
		return successfulOutputError(app.WriteJSON(app.NewEnvelope(command, true, partial, data, safeDiagnostics(diagnostics))))
	}
	safe := safeHumanOutput(human)
	return successfulOutputError(app.WriteStyledHuman(safe))
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
	case errors.Is(err, context.Canceled), errors.Is(err, ErrPromptCanceled):
		code = "command.canceled"
	case errors.Is(err, ErrInvalidUsage):
		code = "command.invalid_usage"
	case errors.Is(err, ErrGuidedPlanStale):
		code = "command.stale_plan"
	case errors.Is(err, context.DeadlineExceeded):
		code = "command.deadline_exceeded"
	case errors.Is(err, workspace.ErrAmbiguousResolution):
		code = "workspace.ambiguous"
	case errors.Is(err, workspace.ErrStaleAssociation), errors.Is(err, workspace.ErrLocatorMismatch):
		code = "workspace.stale_association"
	case errors.Is(err, workspace.ErrRetiredSource):
		code = "source.retired"
	case errors.Is(err, tryflow.ErrExecutionInProgress):
		code = "try.execution_in_progress"
	case errors.Is(err, tryflow.ErrExecutionUncertain):
		code = "try.execution_uncertain"
	case errors.Is(err, tryflow.ErrConfigChanged):
		code = "try.config_changed"
	case errors.Is(err, tryflow.ErrCommandFailed):
		code = "try.command_failed"
	case errors.Is(err, tryflow.ErrCleanupIncomplete), errors.Is(err, workspacebackend.ErrUnsafeCleanup):
		code = "try.cleanup_refused"
	case errors.Is(err, sourcesnapshot.ErrDirtySource):
		code = "try.dirty_capture_required"
	case errors.Is(err, sourcesnapshot.ErrSourceChanged), errors.Is(err, sourcesnapshot.ErrSourceMismatch):
		code = "try.source_changed"
	case errors.Is(err, config.ErrUntrusted), errors.Is(err, workspacebackend.ErrUntrustedSelection):
		code = "config.untrusted"
	case errors.Is(err, pueue.ErrBinaryMissing):
		code = "provider.pueue_missing"
	case errors.Is(err, pueue.ErrServiceUnavailable):
		code = "provider.pueue_misconfigured"
	case errors.Is(err, workspacebackend.ErrProviderMissing):
		code = "workspace.backend_missing"
	case errors.Is(err, workspacebackend.ErrProviderMisconfigured):
		code = "workspace.backend_misconfigured"
	case errors.Is(err, workspacebackend.ErrProviderIncompatible):
		code = "workspace.backend_incompatible"
	case errors.Is(err, workspacebackend.ErrProviderProbeFailed):
		code = "workspace.backend_probe_failed"
	case errors.Is(err, workspacebackend.ErrUnsupportedCapability), errors.Is(err, tryflow.ErrUnsupportedBackend):
		code = "workspace.backend_unsupported"
	case errors.Is(err, workspacebackend.ErrOperationFailed):
		code = "workspace.backend_operation_failed"
	case errors.Is(err, workspacebackend.ErrPostcondition):
		code = "workspace.backend_postcondition"
	case errors.Is(err, operation.ErrUnsupported):
		code = "try.runtime_unsupported"
	case errors.Is(err, ErrExternalSourceExecutionUnsupported):
		code = "source.execution_unsupported"
	case errors.Is(err, sourcepkg.ErrLocalOnlyConfirmation):
		code = "source.local_only_confirmation_required"
	case errors.Is(err, sourcepkg.ErrLocatorObservation):
		code = "source.locator_mismatch"
	case errors.Is(err, sourcepkg.ErrRevisionRequired), errors.Is(err, record.ErrConflict):
		code = "source.revision_conflict"
	case errors.Is(err, config.ErrIdentityConflict):
		code = "config.identity_conflict"
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

func mutationPublicationDiagnostics(partial bool, subject string) []Diagnostic {
	if !partial {
		return nil
	}
	return durabilityUncertainDiagnostics(subject, "")
}

type failedTransactionData struct {
	Transaction *record.TransactionResult `json:"transaction,omitempty"`
	Records     []canonicalRecordView     `json:"records"`
}

func transactionCommandFailure(app *App, machine bool, command string, result *record.TransactionResult, err error) error {
	data := failedTransactionData{Transaction: result, Records: []canonicalRecordView{}}
	if result != nil {
		for _, document := range result.Documents {
			if document != nil {
				data.Records = append(data.Records, canonicalView(document))
			}
		}
	}
	return commandFailure(app, machine, command, data, result != nil, transactionFailureDiagnostics(result), err)
}

func lifecycleCommandFailure(app *App, machine bool, command string, err error) error {
	if transaction, found := lifecycle.TransactionResultFromError(err); found {
		return transactionCommandFailure(app, machine, command, transaction, err)
	}
	return commandFailure(app, machine, command, struct{}{}, false, nil, err)
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
