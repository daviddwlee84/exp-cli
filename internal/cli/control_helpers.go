package cli

import (
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

func transactionDocument(result *record.TransactionResult, kind research.Kind) *record.Document {
	if result == nil {
		return nil
	}
	for _, document := range result.Documents {
		if document.Kind() == kind {
			return document
		}
	}
	return nil
}

// refreshAfterTransaction keeps projections convenient without weakening the
// canonical result: a projection failure is surfaced as a warning diagnostic,
// while the already durable Git-backed transaction remains successful.
func refreshAfterTransaction(command *cobra.Command, app *App, info *project.Info, store TransactionalRecordStore) []Diagnostic {
	legacy, ok := store.(RecordStore)
	if !ok {
		return []Diagnostic{{Severity: SeverityWarning, Code: "projection.refresh_skipped", Message: "canonical transaction succeeded; run exp render to refresh generated views"}}
	}
	_, _, err := renderFreshProjections(command.Context(), app, info, legacy)
	if err == nil {
		return nil
	}
	return []Diagnostic{{Severity: SeverityWarning, Code: "projection.refresh_failed", Message: safeDiagnosticText(fmt.Sprintf("canonical transaction succeeded; projection refresh failed: %v", err))}}
}
