package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/daviddwlee84/exp-cli/internal/lockx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/record"
)

type inventorySnapshotStore interface {
	WithInventorySnapshot(context.Context, func(*record.Inventory) error) error
}

// renderFreshProjections holds the same Git-common lock used by canonical
// writers and, for the production Store, keeps one opened canonical root across
// inventory loading, comparison, publication, and snapshot verification.
func renderFreshProjections(ctx context.Context, app *App, info *project.Info, store RecordStore) (*record.Inventory, projection.Result, error) {
	return withFreshProjectionSnapshot(ctx, app, info, store, app.RenderProjections)
}

func checkFreshProjections(ctx context.Context, app *App, info *project.Info, store RecordStore) (*record.Inventory, projection.Result, error) {
	return withFreshProjectionSnapshot(ctx, app, info, store, app.CheckProjections)
}

func withFreshProjectionSnapshot(ctx context.Context, app *App, info *project.Info, store RecordStore, operation func(context.Context, *record.Inventory) (projection.Result, error)) (*record.Inventory, projection.Result, error) {
	if info == nil {
		return nil, emptyProjectionResult(), fmt.Errorf("project information is required for projection refresh")
	}
	if operation == nil {
		return nil, emptyProjectionResult(), fmt.Errorf("projection operation is not configured")
	}
	var inventory *record.Inventory
	result := emptyProjectionResult()
	invoke := func(snapshot *record.Inventory) error {
		inventory = snapshot
		var err error
		result, err = operation(ctx, snapshot)
		result = stableProjectionResult(result)
		return err
	}
	if rooted, ok := store.(inventorySnapshotStore); ok {
		err := rooted.WithInventorySnapshot(ctx, invoke)
		return inventory, result, err
	}
	// Preserve injection compatibility for focused CLI tests. Production stores
	// implement inventorySnapshotStore and therefore do not reopen by pathname.
	err := lockx.WithTrustedRoot(ctx, info.Repository.GitCommonDir, "exp/v1", func(_ *os.Root) error {
		var err error
		inventory, err = store.Inventory(ctx)
		if err != nil {
			return err
		}
		return invoke(inventory)
	})
	return inventory, result, err
}
