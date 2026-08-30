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
	closedRewrite := current.Clone()
	closedRewrite.Record.(*research.Experiment).Conclusion.Summary = "rewritten after closure"
	if _, err := store.Update(context.Background(), closedRewrite, current.Revision); err == nil {
		t.Fatal("Store.Update accepted a rewritten closed Experiment")
	}

	active := current.Clone()
	activeExperiment := active.Record.(*research.Experiment)
	activeID, err := research.ParseIDForKind("exp_01a01e75-0000-7005-8000-000000000005", research.KindExperiment)
	if err != nil {
		t.Fatal(err)
	}
	activeExperiment.ID = activeID
	activeExperiment.Title = "Active locked experiment"
	activeExperiment.Lifecycle = research.LifecycleActive
	activeExperiment.Closure = ""
	activeExperiment.Verdict = ""
	activeExperiment.ClosureDetail = nil
	activeExperiment.Conclusion = nil
	activeExperiment.Amendments = nil
	active.Path = ""
	current, err = store.Create(context.Background(), active)
	if err != nil {
		t.Fatalf("create active locked Experiment: %v", err)
	}

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

func TestStoreUpdateFreezesImmutableScientificEvidence(t *testing.T) {
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
	copyTree(t, filepath.Join("..", "..", "testdata", "v2", "control-project"), root, false)
	store := NewStore(root, filepath.Join(repository, ".git"))
	inventory, err := store.Inventory(context.Background())
	if err != nil || !inventory.Valid() {
		t.Fatalf("inventory=%v err=%v", inventory.Diagnostics, err)
	}
	queueDocument := inventory.OfKind(research.KindQueue)[0]
	queue := queueDocument.Record.(*research.Queue)
	planDocument := inventory.OfKind(research.KindPlan)[0]
	plan := planDocument.Record.(*research.Plan)
	pool := inventory.OfKind(research.KindResourcePool)[0].Record.(*research.ResourcePool)
	id, err := research.ParseIDForKind("advice_01a01e74-0000-7005-8000-000000000005", research.KindQueueAdvice)
	if err != nil {
		t.Fatal(err)
	}
	now := queue.UpdatedAt
	created, err := store.Create(context.Background(), &Document{Record: &research.QueueAdvice{
		Common: research.Common{Schema: research.SchemaQueueAdvice, ID: id, Title: "Immutable advice", CreatedAt: now, UpdatedAt: now},
		Queue:  queue.ID, QueueRevision: queue.Revision, CandidatePlan: plan.ID, Pool: pool.ID, Lane: research.LaneExploit,
		ProposedPosition: 0, ListwiseOrder: []research.ID{plan.ID},
		Score: research.QueueScore{PoolHours: 1}, Model: "test", PromptDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Rationale: "test rationale",
	}, Body: "# Immutable advice\n"})
	if err != nil {
		t.Fatal(err)
	}
	replacement := created.Clone()
	replacement.Body += "rewritten\n"
	if _, err := store.Update(context.Background(), replacement, created.Revision); err == nil {
		t.Fatal("Store.Update accepted rewritten immutable QueueAdvice")
	}
}

func timePointer(value time.Time) *time.Time { return &value }
