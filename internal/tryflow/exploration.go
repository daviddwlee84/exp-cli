package tryflow

import (
	"context"
	"errors"

	"github.com/daviddwlee84/exp-cli/internal/exploration"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func saveExplorationMetadata(ctx context.Context, store Store, current *record.Document, metadata exploration.Metadata) (*record.Document, error) {
	replacement := current.Clone()
	if err := exploration.SetMetadata(replacement.Record.(*research.Attempt), metadata); err != nil {
		return nil, err
	}
	transaction, err := store.Transact(ctx, record.TransactionRequest{Operation: "try.artifacts", Changes: []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: current.Revision}}})
	if err != nil {
		return nil, err
	}
	id, _ := current.ID()
	return resultDocument(transaction, id)
}

func DescribeArtifact(ctx context.Context, store Store, attemptID research.ID, name, description string) (*record.Document, error) {
	if description == "" || len(description) > 4096 {
		return nil, errors.New("artifact description must contain 1..4096 bytes")
	}
	if err := research.ValidateCommitSafeText(description); err != nil {
		return nil, err
	}
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	document, err := inventory.ByID(attemptID)
	if err != nil {
		return nil, err
	}
	attempt := document.Record.(*research.Attempt)
	metadata, err := exploration.MetadataFor(attempt)
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, errors.New("Attempt has no artifact manifest")
	}
	found := false
	for i := range metadata.Artifacts {
		if metadata.Artifacts[i].Name == name {
			metadata.Artifacts[i].Description = description
			found = true
		}
	}
	if !found {
		return nil, errors.New("artifact name does not belong to this Attempt")
	}
	return saveExplorationMetadata(ctx, store, document, *metadata)
}

// SaveArtifacts is callable after a terminal Attempt even if its worktree or
// Source clone has been retired. Only the frozen private artifact receipt and
// canonical ownership are required, never workload execution.
func SaveArtifacts(ctx context.Context, store Store, project string, attemptID research.ID, allowLarge bool) (*record.Document, error) {
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	document, err := inventory.ByID(attemptID)
	if err != nil {
		return nil, err
	}
	attempt, ok := document.Record.(*research.Attempt)
	if !ok || !terminalAttempt(attempt.State) {
		return nil, errors.New("saving artifacts requires a terminal Attempt")
	}
	metadata, err := exploration.MetadataFor(attempt)
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, errors.New("Attempt has no managed artifact outputs")
	}
	execution, err := exploration.LoadExecution(ctx, project, attemptID.String())
	if err != nil {
		return nil, err
	}
	if execution.Digest() != metadata.ContextDigest {
		return nil, errors.New("artifact receipt differs from canonical Attempt")
	}
	updated, saveErr := exploration.Save(ctx, execution, *metadata, allowLarge)
	result, publishErr := saveExplorationMetadata(ctx, store, document, updated)
	return result, errors.Join(saveErr, publishErr)
}
