package controlplane

import (
	"context"
	"errors"
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

// LocalPueueContext is the canonical provider context used by the local Pueue
// adapter. A task reference from another context must never authorize a local
// scheduler mutation.
const LocalPueueContext = "local"

// PueueTaskIdentity is the exact live scheduler route expected for a canonical
// Attempt under the current runtime contract.
type PueueTaskIdentity struct {
	Context string
	Group   string
	Label   string
}

// ResolvePueueTaskIdentity verifies that an Attempt is a Pueue dispatch and
// derives its exact group/label from the configured pool route. The immutable
// route captured when the Attempt was prepared must still agree with runtime.
func ResolvePueueTaskIdentity(ctx context.Context, repositoryRoot, configPath string, attempt *research.Attempt) (PueueTaskIdentity, error) {
	if attempt == nil {
		return PueueTaskIdentity{}, errors.New("canonical Attempt is required")
	}
	if attempt.Schema != research.SchemaAttemptV2 || attempt.Scheduler != "pueue" {
		return PueueTaskIdentity{}, errors.New("canonical Attempt is not a Pueue v2 dispatch")
	}
	if attempt.DispatchID == "" || attempt.Pool.IsZero() {
		return PueueTaskIdentity{}, errors.New("canonical Attempt has no dispatch route")
	}
	runtime, err := loadRuntime(ctx, repositoryRoot, configPath)
	if err != nil {
		return PueueTaskIdentity{}, err
	}
	pool, configured := runtime.pools[attempt.Pool]
	if !configured {
		return PueueTaskIdentity{}, fmt.Errorf("ResourcePool %s has no runtime Pueue binding", attempt.Pool)
	}
	expected := PueueTaskIdentity{
		Context: LocalPueueContext,
		Group:   pool.PueueGroup,
		Label:   pool.LabelPrefix + attempt.DispatchID,
	}
	group, label, routed := attemptRoute(attempt)
	if !routed || group != expected.Group || label != expected.Label {
		return PueueTaskIdentity{}, errors.New("canonical Attempt Pueue route does not match the current runtime contract")
	}
	return expected, nil
}
