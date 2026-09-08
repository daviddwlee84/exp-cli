package exploration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testProject = "01a0a000-0000-7001-8000-000000000001"
const testTry = "try_01a0a000-0000-7002-8000-000000000002"
const testAttempt = "att_01a0a000-0000-7003-8000-000000000003"

func isolatedHomes(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(variable, filepath.Join(dir, variable))
	}
	return dir
}

func TestConcurrentArtifactPublicationAndPreferenceUpdates(t *testing.T) {
	dir := isolatedHomes(t)
	settings, _ := DefaultSettings()
	ctx := t.Context()
	var wait sync.WaitGroup
	for i := 0; i < 4; i++ {
		i := i
		wait.Add(1)
		go func() {
			defer wait.Done()
			attempt := fmt.Sprintf("att_01a0a000-0000-7003-8000-%012d", i+10)
			e, m, err := Prepare(ctx, settings, testProject, testTry, attempt, "", "", nil, false)
			if err != nil {
				t.Error(err)
				return
			}
			if err := PersistExecution(ctx, e); err != nil {
				t.Error(err)
				return
			}
			if err := os.WriteFile(filepath.Join(e.OutputDir, "plot.txt"), []byte("same content"), 0o600); err != nil {
				t.Error(err)
				return
			}
			m, err = Save(ctx, e, m, false)
			if err != nil {
				t.Error(err)
				return
			}
			if len(m.Artifacts) != 1 || m.Artifacts[0].Digest != hashBytes([]byte("same content")) {
				t.Error("concurrent artifact publication changed identity")
			}
			if err := Update(ctx, "", func(s *Settings) error {
				s.Storage[fmt.Sprintf("disk%d", i)] = StorageProfile{Kind: "local", Root: filepath.Join(dir, fmt.Sprintf("disk%d", i))}
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	s, err := Load(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, ok := s.Storage[fmt.Sprintf("disk%d", i)]; !ok {
			t.Fatal("concurrent preference update was lost")
		}
	}
}

func TestRunnerIdentityDistinguishesBuildCommitAndCapturesEnvironment(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	dir := isolatedHomes(t)
	program := filepath.Join(dir, "app")
	if err := os.WriteFile(program, []byte("binary-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deps.lock"), []byte("deps-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	build := strings.Repeat("a", 40)
	e := Execution{RunnerName: "app", RunnerProfile: RunnerProfile{VersionArgv: []string{"python3", "-c", `import json; print(json.dumps({"version":"1.0","build_commit":"` + build + `"}))`}, EnvironmentFiles: []string{"deps.lock"}}}
	identity, err := InspectRunner(t.Context(), e, dir, program, strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	if identity.SourceMatch != "mismatch" || identity.Version != "1.0" || identity.EnvironmentDigest == "" {
		t.Fatalf("runner %#v", identity)
	}
	e.RunnerProfile.VersionArgv = nil
	unknown, err := InspectRunner(t.Context(), e, dir, program, build)
	if err != nil || unknown.SourceMatch != "unknown" {
		t.Fatalf("unknown provenance %#v %v", unknown, err)
	}
}

func TestPreferencesPersistAndProjectOverridesDoNotEraseGlobalInputs(t *testing.T) {
	dir := isolatedHomes(t)
	ctx := t.Context()
	err := Update(ctx, "", func(s *Settings) error {
		s.TriesRoot = filepath.Join(dir, "scratch")
		s.Defaults.Inputs = map[string]string{"shared": filepath.Join(dir, "shared.csv")}
		s.Storage["disk"] = StorageProfile{Kind: "local", Root: filepath.Join(dir, "disk")}
		s.Projects[testProject] = Preferences{Storage: "disk", Inputs: map[string]string{"sample": filepath.Join(dir, "sample.csv")}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	p := s.Preferences(testProject)
	if p.Storage != "disk" || len(p.Inputs) != 2 || s.TriesRoot != filepath.Join(dir, "scratch") {
		t.Fatalf("preferences: %#v", s)
	}
	p.Inputs["shared"] = "changed"
	if s.Defaults.Inputs["shared"] == "changed" {
		t.Fatal("effective preference mutated defaults")
	}
	if err := Update(ctx, "", func(s *Settings) error { s.Defaults.Storage = "missing"; return nil }); err == nil {
		t.Fatal("accepted dangling default")
	}
	s, err = Load(ctx, "")
	if err != nil || s.Defaults.Storage != "local" {
		t.Fatalf("failed update changed settings: %#v %v", s, err)
	}
}

func TestArtifactsSurviveOutputRemovalAndRejectCorruption(t *testing.T) {
	dir := isolatedHomes(t)
	ctx := t.Context()
	s, err := DefaultSettings()
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(input, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Defaults.Inputs = map[string]string{"sample": input}
	e, metadata, err := Prepare(ctx, s, testProject, testTry, testAttempt, "", "", []string{"sample"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := PersistExecution(ctx, e); err != nil {
		t.Fatal(err)
	}
	plot := filepath.Join(e.OutputDir, "plot.svg")
	if err := os.WriteFile(plot, []byte("<svg>first</svg>"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, err := Save(ctx, e, metadata, false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ArchiveState != "saved" || len(saved.Artifacts) != 1 {
		t.Fatalf("manifest %#v", saved)
	}
	if err := os.RemoveAll(e.OutputDir); err != nil {
		t.Fatal(err)
	}
	recovered, err := Save(ctx, e, metadata, false)
	if err != nil || len(recovered.Artifacts) != 1 {
		t.Fatalf("receipt replay %#v %v", recovered, err)
	}
	file, err := Fetch(ctx, e, saved.Artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(ctx, e, saved.Artifacts[0]); err == nil {
		t.Fatal("accepted corrupt artifact")
	}
	if err := os.WriteFile(input, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInputs(ctx, e); err == nil {
		t.Fatal("accepted changed input")
	}
}

func TestRemoteThresholdPreservesLocalBytesAndDoesNotNeedMLflow(t *testing.T) {
	dir := isolatedHomes(t)
	ctx := t.Context()
	s, _ := DefaultSettings()
	s.Storage["remote"] = StorageProfile{Kind: "mlflow-remote", Root: filepath.Join(dir, "remote"), TrackingURI: "https://tracker.example.invalid", Python: "python3", LargeBytes: 1}
	e, m, err := Prepare(ctx, s, testProject, testTry, testAttempt, "remote", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := PersistExecution(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.OutputDir, "plot.txt"), []byte("plot"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err = Save(ctx, e, m, false)
	if err == nil || !strings.Contains(err.Error(), "threshold") || m.ArchiveState != "pending" || len(m.Artifacts) != 1 {
		t.Fatalf("pending manifest %#v err=%v", m, err)
	}
	if Availability(&e, m.Artifacts[0]) != "local" {
		t.Fatal("failed upload lost local copy")
	}
}

func TestIndependentAttemptsWithSameArtifactNameAndSymlinkRejection(t *testing.T) {
	isolatedHomes(t)
	s, _ := DefaultSettings()
	ctx := t.Context()
	var digests []string
	for i, id := range []string{testAttempt, "att_01a0a000-0000-7004-8000-000000000004"} {
		e, m, err := Prepare(ctx, s, testProject, testTry, id, "", "", nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := PersistExecution(ctx, e); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(e.OutputDir, "plot.txt"), []byte(strings.Repeat("x", i+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		m, err = Save(ctx, e, m, false)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, m.Artifacts[0].Digest)
	}
	if digests[0] == digests[1] {
		t.Fatal("independent output contents mixed")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "plot")); err != nil {
		t.Skip(err)
	}
	if _, err := Scan(ctx, root); err == nil {
		t.Fatal("followed an artifact symlink")
	}
}

func TestHistoryRefreshIsProjectScopedAndSearchesUnicodeDescriptions(t *testing.T) {
	isolatedHomes(t)
	ctx := context.Background()
	now := time.Now().UTC()
	card := Card{Project: testProject, ID: testTry, Kind: "try", Title: "散點圖", Summary: "取樣分析", UpdatedAt: now, SearchText: "散點圖 取樣分析", Sources: []string{"app"}, Versions: []string{"abcdef"}}
	projects := map[string][]Card{testProject: {card}, "second": {{Project: "second", ID: "second", Kind: "try", Title: "Other", SearchText: "other", UpdatedAt: now}}}
	results, err := Search(ctx, projects, HistoryQuery{Text: "取樣", Source: "app", Version: "abc", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != testTry {
		t.Fatalf("search %#v", results)
	}
	results, err = Search(ctx, map[string][]Card{testProject: {}}, HistoryQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatal("stale or unselected Project leaked into results")
	}
}
