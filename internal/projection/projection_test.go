package projection_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

func TestBuildMatchesValidProjectFixtureExactly(t *testing.T) {
	root := fixtureRoot(t)
	inventory, err := record.LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := projection.Build(inventory)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got, want := len(files), 4; got != want {
		t.Fatalf("Build() files = %d, want %d", got, want)
	}
	for _, generated := range files {
		want, err := os.ReadFile(filepath.Join(root, generated.Path))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(generated.Content, want) {
			t.Fatalf("%s differs from fixture\n--- got ---\n%s\n--- want ---\n%s", generated.Path, generated.Content, want)
		}
		if len(generated.Content) == 0 || generated.Content[len(generated.Content)-1] != '\n' || bytes.Contains(generated.Content, []byte{'\r'}) {
			t.Fatalf("%s does not use LF with one final newline", generated.Path)
		}
	}
}

func TestCheckDetectsExactDriftWithoutWritingAndRenderRepairs(t *testing.T) {
	root := copyFixture(t)
	inventory, err := record.LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Unix(1234, 0)
	for _, name := range projection.Paths() {
		path := filepath.Join(root, name)
		if err := os.Chtimes(path, fixed, fixed); err != nil {
			t.Fatal(err)
		}
	}
	roadmap := filepath.Join(root, projection.RoadmapFile)
	if err := os.WriteFile(roadmap, []byte("stale projection\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotProjections(t, root)

	checked, err := projection.Check(t.Context(), inventory)
	if !errors.Is(err, projection.ErrStale) || checked.Current {
		t.Fatalf("Check() = %#v, %v", checked, err)
	}
	if !reflect.DeepEqual(checked.Drifted, []string{projection.RoadmapFile}) || checked.Changed || len(checked.Written) != 0 {
		t.Fatalf("Check() drift metadata = %#v", checked)
	}
	afterCheck := snapshotProjections(t, root)
	if !reflect.DeepEqual(before, afterCheck) {
		t.Fatal("Check() changed projection bytes or metadata")
	}

	rendered, err := projection.Render(t.Context(), inventory)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !rendered.Current || !rendered.Changed || !reflect.DeepEqual(rendered.Written, []string{projection.RoadmapFile}) || len(rendered.Drifted) != 0 {
		t.Fatalf("Render() = %#v", rendered)
	}
	fixture := fixtureRoot(t)
	for _, name := range projection.Paths() {
		got, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		want, readErr := os.ReadFile(filepath.Join(fixture, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("rendered %s differs from fixture", name)
		}
	}
}

func TestCheckClassifiesSparseOversizedProjectionWithoutReadingIt(t *testing.T) {
	root := copyFixture(t)
	inventory, err := record.LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, projection.READMEFile)
	const sparseSize int64 = 8 << 30
	if err := os.Truncate(path, sparseSize); err != nil {
		t.Fatalf("create sparse projection: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result, err := projection.Check(ctx, inventory)
	if !errors.Is(err, projection.ErrStale) {
		t.Fatalf("Check() error = %v, want stale", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Check() read the sparse projection until timeout: %v", err)
	}
	var observed *projection.FileResult
	for index := range result.Files {
		if result.Files[index].Path == projection.READMEFile {
			observed = &result.Files[index]
			break
		}
	}
	if observed == nil || observed.State != projection.FileStale || observed.Detail != "size differs" || observed.ActualHash != "" {
		t.Fatalf("sparse projection result = %#v", observed)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != sparseSize {
		t.Fatalf("Check() changed sparse projection size to %d", info.Size())
	}

	rendered, err := projection.Render(t.Context(), inventory)
	if err != nil {
		t.Fatalf("Render() sparse replacement error = %v", err)
	}
	if !rendered.Current || !reflect.DeepEqual(rendered.Written, []string{projection.READMEFile}) {
		t.Fatalf("Render() sparse replacement = %#v", rendered)
	}
}

func TestInvalidInventoryBlocksRenderWithoutProjectionWrites(t *testing.T) {
	root := copyFixture(t)
	planPath := filepath.Join(root, "plans", "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	plan = bytes.Replace(plan, []byte("priority = \"P1\"\n"), []byte("priority = \"P1\"\nunknown_cli_test_field = true\n"), 1)
	if err := os.WriteFile(planPath, plan, 0o644); err != nil {
		t.Fatal(err)
	}
	inventory, err := record.LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Valid() {
		t.Fatal("test inventory unexpectedly valid")
	}
	before := snapshotProjections(t, root)
	result, err := projection.Render(t.Context(), inventory)
	if !errors.Is(err, projection.ErrInvalidInventory) {
		t.Fatalf("Render() error = %v", err)
	}
	if result.Changed || len(result.Written) != 0 {
		t.Fatalf("Render() reported writes for invalid inventory: %#v", result)
	}
	if after := snapshotProjections(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("invalid inventory changed generated projections")
	}
}

func TestRenderRetainsEarlierWritesWhenLaterPublicationFails(t *testing.T) {
	root := copyFixture(t)
	inventory, err := record.LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{projection.READMEFile, projection.RoadmapFile} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("stale "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := errors.New("injected second projection failure")
	result, err := projection.RenderWithOptions(t.Context(), inventory, projection.RenderOptions{AtomicHook: func(stage record.AtomicStage, destination string) error {
		if stage == record.StageRename && destination == projection.RoadmapFile {
			return sentinel
		}
		return nil
	}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RenderWithOptions error = %v", err)
	}
	if !reflect.DeepEqual(result.Written, []string{projection.READMEFile}) || !result.Changed || result.Current {
		t.Fatalf("partial render result = %#v", result)
	}
	if !reflect.DeepEqual(result.Drifted, []string{projection.RoadmapFile}) {
		t.Fatalf("partial render drift = %#v", result.Drifted)
	}
}

func TestRenderMarksPostRenameDirectorySyncFailurePublished(t *testing.T) {
	root := copyFixture(t)
	inventory, err := record.LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, projection.RoadmapFile), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("injected directory sync failure")
	result, err := projection.RenderWithOptions(t.Context(), inventory, projection.RenderOptions{AtomicHook: func(stage record.AtomicStage, destination string) error {
		if stage == record.StageDirSync && destination == projection.RoadmapFile {
			return sentinel
		}
		return nil
	}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RenderWithOptions error = %v", err)
	}
	if !reflect.DeepEqual(result.Written, []string{projection.RoadmapFile}) || !result.Changed || !result.Current || len(result.Drifted) != 0 {
		t.Fatalf("post-rename result = %#v", result)
	}
	files, err := projection.Build(inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Path != projection.RoadmapFile {
			continue
		}
		actual, readErr := os.ReadFile(filepath.Join(root, file.Path))
		if readErr != nil || !bytes.Equal(actual, file.Content) {
			t.Fatalf("published bytes = %q, %v", actual, readErr)
		}
	}
}

func TestCheckRejectsCanonicalCommitAfterProjectionInspection(t *testing.T) {
	root := copyFixture(t)
	inventory, err := record.LoadInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(root, "plans", "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md")
	original, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := projection.CheckWithOptions(context.Background(), inventory, projection.CheckOptions{AfterInspect: func() error {
		replacement := planPath + ".replacement"
		if err := os.WriteFile(replacement, original, 0o644); err != nil {
			return err
		}
		return os.Rename(replacement, planPath)
	}})
	if !errors.Is(err, record.ErrInventoryChanged) {
		t.Fatalf("check race error = %v", err)
	}
	if result.Current {
		t.Fatalf("check reported current after canonical change: %#v", result)
	}
}

func TestLockedProjectionCheckSerializesConcurrentCanonicalCommit(t *testing.T) {
	repository := t.TempDir()
	command := exec.Command("git", "init", "--quiet")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	root := filepath.Join(repository, "experiments")
	if err := os.CopyFS(root, os.DirFS(fixtureRoot(t))); err != nil {
		t.Fatal(err)
	}
	reader := record.NewStore(root, filepath.Join(repository, ".git"))
	writer := record.NewStore(root, filepath.Join(repository, ".git"),
		record.WithClock(func() time.Time { return time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC) }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a01e95-0000-7606-8000-000000000606"), nil
		}),
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	checkDone := make(chan struct {
		result projection.Result
		err    error
	}, 1)
	go func() {
		var result projection.Result
		err := reader.WithInventorySnapshot(context.Background(), func(inventory *record.Inventory) error {
			close(entered)
			<-release
			var checkErr error
			result, checkErr = projection.Check(context.Background(), inventory)
			return checkErr
		})
		checkDone <- struct {
			result projection.Result
			err    error
		}{result: result, err: err}
	}()
	<-entered

	writeDone := make(chan error, 1)
	go func() {
		_, err := writer.CreatePlan(context.Background(), record.PlanInput{
			Title: "Concurrent commit", Body: "body\n", Priority: research.PriorityP1, Effort: research.EffortS,
			ExpectedPayoff: research.ExpectedPayoff{Summary: "Serialize", Metric: "score", Unit: "score"},
		})
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		t.Fatalf("canonical writer bypassed render check lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	checked := <-checkDone
	if checked.err != nil || !checked.result.Current {
		t.Fatalf("locked check = %#v, %v", checked.result, checked.err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("concurrent canonical write after check: %v", err)
	}
}

func TestCheckHonorsCancellation(t *testing.T) {
	inventory, err := record.LoadInventory(fixtureRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := projection.Check(ctx, inventory)
	if !errors.Is(err, context.Canceled) || result.Changed {
		t.Fatalf("canceled Check() = %#v, %v", result, err)
	}
}

type projectionSnapshot struct {
	Content []byte
	Mode    fs.FileMode
	ModTime time.Time
}

func snapshotProjections(t *testing.T, root string) map[string]projectionSnapshot {
	t.Helper()
	result := make(map[string]projectionSnapshot)
	for _, name := range projection.Paths() {
		path := filepath.Join(root, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = projectionSnapshot{Content: content, Mode: info.Mode(), ModTime: info.ModTime()}
	}
	return result
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "v1", "valid-project"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func copyFixture(t *testing.T) string {
	t.Helper()
	destination := t.TempDir()
	if err := os.CopyFS(destination, os.DirFS(fixtureRoot(t))); err != nil {
		t.Fatal(err)
	}
	return destination
}
