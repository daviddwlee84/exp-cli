package record

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestStoreUpdateEnforcesLockedExperimentAmendmentHistory(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--quiet")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	root := filepath.Join(repository, "experiments")
	copyTree(t, filepath.Join("..", "..", "testdata", "v1", "valid-project"), root, false)
	store := NewStore(root, filepath.Join(repository, ".git"))
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current := inventory.OfKind(research.KindExperiment)[0]

	changedLock := current.Clone()
	changedLock.Record.(*research.Experiment).Design.DesignLockedAt = timePointer(changedLock.Record.(*research.Experiment).Design.DesignLockedAt.Add(time.Second))
	if _, err := store.Update(context.Background(), changedLock, current.Revision); err == nil {
		t.Fatal("Store.Update accepted a changed design lock time")
	}

	rewrittenHistory := current.Clone()
	experiment := rewrittenHistory.Record.(*research.Experiment)
	experiment.Amendments = []research.Amendment{{
		AmendedAt:      experiment.UpdatedAt,
		Reason:         "invented history",
		PreviousDigest: experiment.Design.DesignDigest,
		NewDigest:      experiment.Design.DesignDigest,
		Changes:        []string{"none"},
	}}
	// Establish an existing amendment as the immutable prefix, then attempt to
	// rewrite it through another update.
	withHistory, err := store.Update(context.Background(), rewrittenHistory, current.Revision)
	if err != nil {
		t.Fatalf("append baseline amendment: %v", err)
	}
	rewrite := withHistory.Clone()
	rewrite.Record.(*research.Experiment).Amendments[0].Reason = "rewritten reason"
	if _, err := store.Update(context.Background(), rewrite, withHistory.Revision); err == nil {
		t.Fatal("Store.Update accepted rewritten amendment history")
	}

	withoutAmendment := withHistory.Clone()
	withoutAmendment.Record.(*research.Experiment).Design.Hypothesis = "A silently rewritten hypothesis"
	newDigest, err := research.DesignDigest(withoutAmendment.Record.(*research.Experiment).Design)
	if err != nil {
		t.Fatal(err)
	}
	withoutAmendment.Record.(*research.Experiment).Design.DesignDigest = newDigest
	withoutAmendment.Record.(*research.Experiment).UpdatedAt = withoutAmendment.Record.(*research.Experiment).UpdatedAt.Add(time.Minute)
	if _, err := store.Update(context.Background(), withoutAmendment, withHistory.Revision); err == nil {
		t.Fatal("Store.Update accepted a locked design rewrite without an amendment")
	}

	amended := withHistory.Clone()
	amendedExperiment := amended.Record.(*research.Experiment)
	amendedExperiment.Design.Hypothesis = "A transparently amended hypothesis"
	newDigest, err = research.DesignDigest(amendedExperiment.Design)
	if err != nil {
		t.Fatal(err)
	}
	previousDigest := amendedExperiment.Design.DesignDigest
	amendedExperiment.Design.DesignDigest = newDigest
	amendedExperiment.UpdatedAt = amendedExperiment.UpdatedAt.Add(2 * time.Minute)
	amendedExperiment.Amendments = append(amendedExperiment.Amendments, research.Amendment{
		AmendedAt:      amendedExperiment.UpdatedAt.Add(-time.Minute),
		Reason:         "new evidence required a protocol correction",
		PreviousDigest: previousDigest,
		NewDigest:      newDigest,
		Changes:        []string{"changed hypothesis"},
	})
	updated, err := store.Update(context.Background(), amended, withHistory.Revision)
	if err != nil {
		t.Fatalf("Store.Update rejected valid amendment chain: %v", err)
	}
	if updated.Record.(*research.Experiment).Design.DesignDigest != newDigest {
		t.Fatalf("updated digest = %s, want %s", updated.Record.(*research.Experiment).Design.DesignDigest, newDigest)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
