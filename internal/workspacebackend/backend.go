// Package workspacebackend defines the execution-neutral managed-workspace
// boundary used by direct Try and formal Experiment runtime wiring.
package workspacebackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/daviddwlee84/exp-cli/internal/lockx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
)

var (
	ErrInvalidRequest = errors.New("invalid managed-workspace request")
	ErrSeedMismatch   = errors.New("dirty seed bundle does not match the captured Source snapshot")
	ErrUnsafeCleanup  = errors.New("managed workspace cleanup refused unsafe state")
)

const NativeGitName = "native_git"

// FinalizeAction chooses explicit destructive cleanup or non-destructive handoff.
type FinalizeAction string

const (
	FinalizeCleanup FinalizeAction = "cleanup"
	FinalizeHandoff FinalizeAction = "handoff"
)

// PrepareRequest contains only already-resolved canonical and registered Source
// identity. Allowed paths and globs are relative to Source.Subdir.
type PrepareRequest struct {
	ProjectID                   research.UUID
	Source                      *research.Source
	RepositoryRoot              string
	RegisteredGitCommonDir      string
	RegisteredGitCommonIdentity string
	OwnerID                     research.ID
	OwnerTitle                  string
	// TryID binds a Try-backed Attempt workspace to its canonical Try. Legacy
	// callers may leave it zero when OwnerID itself is a Try.
	TryID                 research.ID
	Snapshot              research.SourceSnapshot
	AllowedPaths          []string
	BaselinePaths         []string
	AllowedPathGlobs      []string
	CanonicalMetadataRoot string
	SeedBundle            *sourcesnapshot.Bundle
	// SeedBundleDigest retains the authenticated capability identity after the
	// disposable bundle itself has already been removed during resumable cleanup.
	SeedBundleDigest   string
	AllowRetiredSource bool
	// Provider is invocation-local selection policy. It never enters canonical
	// research records; Workspace.Backend reports the provider that actually
	// prepared the checkout.
	Provider ProviderSelection
}

// Workspace is the stable backend-neutral execution location.
type Workspace struct {
	Backend        string        `json:"backend"`
	ProjectID      research.UUID `json:"project_id"`
	SourceID       research.ID   `json:"source_id"`
	OwnerID        research.ID   `json:"owner_id"`
	Root           string        `json:"root"`
	CWD            string        `json:"cwd"`
	Branch         string        `json:"branch"`
	HeadCommit     string        `json:"head_commit"`
	SnapshotDigest string        `json:"snapshot_digest"`
	SeedDigest     string        `json:"seed_digest,omitempty"`
}

// Inspection reports state without modifying the worktree.
type Inspection struct {
	Workspace       Workspace `json:"workspace"`
	HeadCommit      string    `json:"head_commit"`
	Clean           bool      `json:"clean"`
	Paths           []string  `json:"paths"`
	HandedOff       bool      `json:"handed_off"`
	HandoffProvider string    `json:"handoff_provider,omitempty"`
	Removed         bool      `json:"removed"`
	RetireProvider  string    `json:"retire_provider,omitempty"`
}

func workspaceLockRelative(request PrepareRequest) (string, error) {
	if request.RegisteredGitCommonDir == "" || request.ProjectID.IsZero() || request.Source == nil || request.Source.ID.IsZero() || request.OwnerID.IsZero() {
		return "", ErrInvalidRequest
	}
	identity := request.ProjectID.String() + "\x00" + request.Source.ID.String() + "\x00" + request.OwnerID.String()
	digest := sha256.Sum256([]byte(identity))
	return path.Join("exp", "v1", "workspaces", hex.EncodeToString(digest[:16])), nil
}

func withWorkspaceLock(ctx context.Context, request PrepareRequest, operation func(*os.Root) error) error {
	if operation == nil {
		return errors.New("workspace lock operation is required")
	}
	relative, err := workspaceLockRelative(request)
	if err != nil {
		return err
	}
	if err := lockx.WithTrustedRoot(ctx, request.RegisteredGitCommonDir, relative, operation); err != nil {
		return fmt.Errorf("lock managed workspace: %w", err)
	}
	return nil
}

// WithWorkspaceLock fences execution against preparation and destructive cleanup
// for one exact Project/Source/owner workspace identity.
func WithWorkspaceLock(ctx context.Context, request PrepareRequest, operation func() error) error {
	return withWorkspaceLock(ctx, request, func(*os.Root) error { return operation() })
}

// Backend is deliberately small: execution remains outside this package.
type Backend interface {
	Prepare(context.Context, PrepareRequest) (Workspace, error)
	// VerifyPrepared performs a non-mutating, byte-exact snapshot verification.
	// Callers that need a spawn-time guarantee hold WithWorkspaceLock around it
	// and the process start so no cooperating lifecycle operation can intervene.
	VerifyPrepared(context.Context, PrepareRequest) (Workspace, error)
	Inspect(context.Context, PrepareRequest) (Inspection, error)
	CleanupOrHandoff(context.Context, PrepareRequest, FinalizeAction) (Inspection, error)
}

// PreparationRollbacker removes only an exactly authenticated workspace/branch
// whose owner has not yet been published canonically. It is intentionally
// separate from ordinary cleanup so callers must opt into the prepublication
// protocol explicitly.
type PreparationRollbacker interface {
	RollbackPreparation(context.Context, PrepareRequest) error
}
