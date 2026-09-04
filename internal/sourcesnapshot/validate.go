package sourcesnapshot

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

// Validate checks the standalone capture contract without requiring an owning
// Attempt record. It is used at bundle and backend trust boundaries.
func Validate(snapshot research.SourceSnapshot) error {
	if snapshot.Source.IsZero() || snapshot.Source.Kind() != research.KindSource {
		return fmt.Errorf("snapshot Source ID is invalid: %w", ErrInvalidRequest)
	}
	if normalized, err := research.NormalizeSourceSubdir(snapshot.Subdir); err != nil || normalized != snapshot.Subdir {
		return fmt.Errorf("snapshot subdir is invalid: %w", errors.Join(ErrInvalidRequest, err))
	}
	_, timezoneOffset := snapshot.CapturedAt.Zone()
	if snapshot.PolicyVersion != PolicyVersion || snapshot.CapturedAt.IsZero() || timezoneOffset != 0 {
		return fmt.Errorf("snapshot policy or capture time is invalid: %w", ErrInvalidRequest)
	}
	objectLength := objectIDLength(snapshot.GitObjectFormat)
	if objectLength == 0 || !validObjectID(snapshot.BaseCommit, objectLength) || !validObjectID(snapshot.HeadCommit, objectLength) {
		return fmt.Errorf("snapshot Git identity is invalid: %w", ErrInvalidRequest)
	}
	if snapshot.ChangeSet == nil || !sort.StringsAreSorted(snapshot.ChangeSet) {
		return fmt.Errorf("snapshot change set is missing or unsorted: %w", ErrInvalidRequest)
	}
	previous := ""
	for _, name := range snapshot.ChangeSet {
		if err := validateGitPath(name); err != nil || previous != "" && name <= previous {
			return fmt.Errorf("snapshot change set is invalid: %w", errors.Join(ErrInvalidRequest, err))
		}
		previous = name
	}
	switch snapshot.State {
	case research.SourceSnapshotClean:
		if snapshot.DirtyDigest != "" || snapshot.DirtySummary != "" || snapshot.Reproducibility != research.ReproducibilityExact {
			return fmt.Errorf("clean snapshot has dirty or inexact fields: %w", ErrInvalidRequest)
		}
	case research.SourceSnapshotDirty:
		if !validDigest(snapshot.DirtyDigest) || strings.TrimSpace(snapshot.DirtySummary) == "" || len(snapshot.DirtySummary) > research.MaxSourceSnapshotSummaryBytes || snapshot.Reproducibility != research.ReproducibilityBounded {
			return fmt.Errorf("dirty snapshot fields are invalid: %w", ErrInvalidRequest)
		}
		if err := research.ValidateCommitSafeText(snapshot.DirtySummary); err != nil {
			return fmt.Errorf("dirty snapshot summary is unsafe: %w", errors.Join(ErrInvalidRequest, err))
		}
	default:
		return fmt.Errorf("snapshot state is invalid: %w", ErrInvalidRequest)
	}
	if !validDigest(snapshot.Digest) {
		return fmt.Errorf("snapshot digest is invalid: %w", ErrInvalidRequest)
	}
	computed, err := research.SourceSnapshotDigest(snapshot)
	if err != nil || computed != snapshot.Digest {
		return fmt.Errorf("snapshot digest does not match fields: %w", errors.Join(ErrInvalidRequest, err))
	}
	return nil
}
