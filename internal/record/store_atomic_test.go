package record_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestStoreUpdateDetectsInPlaceEditAfterRevisionValidation(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	inject := false
	var externalBytes []byte
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return now }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e90-0000-7101-8000-000000000101"), nil
		}),
		record.WithAtomicHook(func(stage record.AtomicStage, relative string) error {
			if inject && stage == record.StageRename {
				return os.WriteFile(filepath.Join(info.Root, filepath.FromSlash(relative)), externalBytes, 0o644)
			}
			return nil
		}),
	)
	created, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Original", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve", Metric: "score", Unit: "score"},
	})
	if err != nil {
		t.Fatal(err)
	}
	external := created.Clone()
	external.Record.(*research.Plan).Title = "External edit"
	external.Record.(*research.Plan).UpdatedAt = now.Add(time.Minute)
	externalBytes, err = record.Encode(external)
	if err != nil {
		t.Fatal(err)
	}
	replacement := created.Clone()
	replacement.Record.(*research.Plan).Title = "Store replacement"
	replacement.Record.(*research.Plan).UpdatedAt = now.Add(2 * time.Minute)
	inject = true
	updated, err := store.Update(context.Background(), replacement, created.Revision)
	if !errors.Is(err, record.ErrConflict) || updated != nil {
		t.Fatalf("racing Update = %#v, %v", updated, err)
	}
	actual, readErr := os.ReadFile(filepath.Join(info.Root, filepath.FromSlash(created.Path)))
	if readErr != nil || string(actual) != string(externalBytes) {
		t.Fatalf("external edit was not preserved: %q, %v", actual, readErr)
	}
}

func TestStoreCreateDoesNotOverwriteRacingDestination(t *testing.T) {
	info := initializeStoreProject(t)
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	sentinel := []byte("concurrent creator bytes\n")
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return now }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e91-0000-7202-8000-000000000202"), nil
		}),
		record.WithCollisionLimit(1),
		record.WithAtomicHook(func(stage record.AtomicStage, relative string) error {
			if stage == record.StageRename {
				return os.WriteFile(filepath.Join(info.Root, filepath.FromSlash(relative)), sentinel, 0o644)
			}
			return nil
		}),
	)
	created, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "No clobber", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Preserve", Metric: "score", Unit: "score"},
	})
	if !errors.Is(err, record.ErrCollision) || created != nil {
		t.Fatalf("racing CreatePlan = %#v, %v", created, err)
	}
	path := filepath.Join(info.Root, record.PlansDir, "plan_01a01e91-0000-7202-8000-000000000202-no-clobber.md")
	actual, readErr := os.ReadFile(path)
	if readErr != nil || string(actual) != string(sentinel) {
		t.Fatalf("racing destination was overwritten: %q, %v", actual, readErr)
	}
}

func TestStoreCreateRejectsExperimentsRootRetargetAfterValidation(t *testing.T) {
	info := initializeStoreProject(t)
	moved := filepath.Join(filepath.Dir(info.Root), "moved-experiments")
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e92-0000-7303-8000-000000000303"), nil
		}),
		record.WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage != record.StageRename {
				return nil
			}
			if err := os.Rename(info.Root, moved); err != nil {
				return err
			}
			return os.Mkdir(info.Root, 0o755)
		}),
	)
	created, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Retarget guard", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Preserve root", Metric: "score", Unit: "score"},
	})
	if err == nil || created != nil {
		t.Fatalf("retargeted CreatePlan = %#v, %v", created, err)
	}
	matches, globErr := filepath.Glob(filepath.Join(moved, record.PlansDir, "*.md"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("retargeted root received canonical Plan: %v, %v", matches, globErr)
	}
}

func TestStoreCreateRejectsLockRootRetargetBeforePublication(t *testing.T) {
	info := initializeStoreProject(t)
	coordination := info.Repository.CoordinationDir()
	moved := filepath.Join(filepath.Dir(coordination), "v1-moved")
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e93-0000-7404-8000-000000000404"), nil
		}),
		record.WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage != record.StageRename {
				return nil
			}
			if err := os.Rename(coordination, moved); err != nil {
				return err
			}
			return os.Symlink(moved, coordination)
		}),
	)
	created, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Lock retarget guard", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Preserve lock identity", Metric: "score", Unit: "score"},
	})
	if err == nil || created != nil {
		t.Fatalf("retargeted lock CreatePlan = %#v, %v", created, err)
	}
	matches, globErr := filepath.Glob(filepath.Join(info.Root, record.PlansDir, "*.md"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("Plan published under retargeted lock: %v, %v", matches, globErr)
	}
}

func TestStoreSeedsReservationsFromOtherLinkedWorktrees(t *testing.T) {
	mainInfo := initializeStoreProject(t)
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.name", "Exp Test")
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.email", "exp-test@example.invalid")
	runGitCommand(t, mainInfo.Repository.Root, "add", "experiments")
	runGitCommand(t, mainInfo.Repository.Root, "commit", "--quiet", "-m", "initialize")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runGitCommand(t, mainInfo.Repository.Root, "worktree", "add", "--quiet", "-b", "reservation-test", linkedRoot)
	linkedInfo, err := project.Discover(context.Background(), linkedRoot)
	if err != nil {
		t.Fatal(err)
	}

	firstID := uuid.MustParse("01a01e93-0000-7404-8000-000000000404")
	secondID := uuid.MustParse("01a01e94-0000-7505-8000-000000000505")
	mainStore := record.NewStore(mainInfo.Root, mainInfo.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return firstID, nil }),
	)
	created, err := mainStore.CreatePlan(context.Background(), record.PlanInput{
		Title: "Pre-reservation record", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Reserve", Metric: "score", Unit: "score"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reservation := filepath.Join(mainStore.CoordinationDir(), "reservations", created.Record.(*research.Plan).ID.String())
	if err := os.Remove(reservation); err != nil {
		t.Fatal(err)
	}

	calls := 0
	linkedStore := record.NewStore(linkedInfo.Root, linkedInfo.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 15, 1, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			calls++
			if calls == 1 {
				return firstID, nil
			}
			return secondID, nil
		}),
	)
	created, err = linkedStore.CreatePlan(context.Background(), record.PlanInput{
		Title: "Reserved across worktrees", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "No duplicate", Metric: "score", Unit: "score"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || created.Record.(*research.Plan).ID.UUID() != secondID {
		t.Fatalf("reservation retry calls=%d id=%s", calls, created.Record.(*research.Plan).ID)
	}
}

func TestStoreMutationSkipsPhysicallyMissingPrunableWorktree(t *testing.T) {
	mainInfo := initializeStoreProject(t)
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.name", "Exp Test")
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.email", "exp-test@example.invalid")
	runGitCommand(t, mainInfo.Repository.Root, "add", "experiments")
	runGitCommand(t, mainInfo.Repository.Root, "commit", "--quiet", "-m", "initialize")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runGitCommand(t, mainInfo.Repository.Root, "worktree", "add", "--quiet", "-b", "stale-worktree", linkedRoot)
	if err := os.RemoveAll(linkedRoot); err != nil {
		t.Fatal(err)
	}

	store := record.NewStore(mainInfo.Root, mainInfo.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e94-0000-7505-8000-000000000505"), nil
		}),
	)
	created, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Ignore stale registration", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Proceed safely", Metric: "score", Unit: "score"},
	})
	if err != nil || created == nil {
		t.Fatalf("mutation with stale worktree = %#v, %v", created, err)
	}
}

func TestStoreMutationRejectsConflictingLinkedProjectIdentity(t *testing.T) {
	mainInfo := initializeStoreProject(t)
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.name", "Exp Test")
	runGitCommand(t, mainInfo.Repository.Root, "config", "user.email", "exp-test@example.invalid")
	runGitCommand(t, mainInfo.Repository.Root, "add", "experiments")
	runGitCommand(t, mainInfo.Repository.Root, "commit", "--quiet", "-m", "initialize")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runGitCommand(t, mainInfo.Repository.Root, "worktree", "add", "--quiet", "-b", "identity-conflict", linkedRoot)
	markerPath := filepath.Join(linkedRoot, "experiments", record.ProjectFile)
	content, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	document, err := record.Decode(content)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := research.NewProjectUUID(uuid.MustParse("01a01e99-0000-7909-8000-000000000909"))
	if err != nil {
		t.Fatal(err)
	}
	document.Record.(*research.Project).ProjectID = otherID
	content, err = record.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	store := record.NewStore(mainInfo.Root, mainInfo.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e95-0000-7606-8000-000000000606"), nil
		}),
	)
	created, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Must reject identity conflict", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Reject", Metric: "score", Unit: "score"},
	})
	if !errors.Is(err, record.ErrProjectIdentityConflict) || created != nil {
		t.Fatalf("conflicting mutation = %#v, %v", created, err)
	}
}

func TestStoreCreateReturnsPublishedDocumentOnDirectorySyncFailure(t *testing.T) {
	info := initializeStoreProject(t)
	sentinel := errors.New("injected directory sync failure")
	store := record.NewStore(info.Root, info.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e92-0000-7303-8000-000000000303"), nil
		}),
		record.WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage == record.StageDirSync {
				return sentinel
			}
			return nil
		}),
	)
	created, err := store.CreatePlan(context.Background(), record.PlanInput{
		Title: "Published despite sync error", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Report state", Metric: "score", Unit: "score"},
	})
	if !errors.Is(err, sentinel) || created == nil {
		t.Fatalf("CreatePlan post-rename result = %#v, %v", created, err)
	}
	var publication *record.PublicationError
	if !errors.As(err, &publication) || !publication.Published {
		t.Fatalf("publication error = %#v", publication)
	}
	if _, statErr := os.Stat(filepath.Join(info.Root, filepath.FromSlash(created.Path))); statErr != nil {
		t.Fatalf("published record missing: %v", statErr)
	}
}
