package exploration

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

// Run with: uv run --no-project --with mlflow mise exec -- go test
// ./internal/exploration -run TestMLflowSQLiteRoundTrip -count=1
func TestMLflowSQLiteRoundTrip(t *testing.T) {
	if exec.Command("python3", "-c", "import mlflow").Run() != nil {
		t.Skip("MLflow SDK is not installed in the selected Python")
	}
	dir := isolatedHomes(t)
	s, _ := DefaultSettings()
	ctx := t.Context()
	s.Storage["tracking"] = StorageProfile{Kind: "mlflow-local", Root: filepath.Join(dir, "tracking"), Python: "python3", LargeBytes: 1 << 30}
	e, m, err := Prepare(ctx, s, testProject, testTry, testAttempt, "tracking", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := PersistExecution(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.OutputDir, "plots"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.OutputDir, "plots", "plot.svg"), []byte("<svg>artifact</svg>"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err = Save(ctx, e, m, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].RunID == "" || m.ArchiveState != "saved" {
		t.Fatalf("MLflow manifest %#v", m)
	}
	if _, err := os.Stat(filepath.Join(e.Storage.Root, "mlflow.db")); err != nil {
		t.Fatal("SQLite tracking database was not created", err)
	}
	object, _ := ObjectPath(e.Storage, m.Artifacts[0])
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(e.OutputDir); err != nil {
		t.Fatal(err)
	}
	if Availability(&e, m.Artifacts[0]) != "download-required" {
		t.Fatal("missing local object was not identified")
	}
	file, err := Fetch(ctx, e, m.Artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "<svg>artifact</svg>" {
		t.Fatalf("MLflow roundtrip: %q %v", data, err)
	}
	response, err := mlflowCall(ctx, e, map[string]any{"action": "save", "artifacts": []any{}})
	if err != nil || response["run_id"] != m.Artifacts[0].RunID {
		t.Fatalf("save retry created a different run: %#v %v", response, err)
	}
}

func TestMLflowHTTPArtifactRoundTrip(t *testing.T) {
	if os.Getenv("EXP_TEST_MLFLOW_SERVER") != "1" {
		t.Skip("set EXP_TEST_MLFLOW_SERVER=1 to exercise a real loopback tracking server")
	}
	if exec.Command("python3", "-c", "import mlflow, sqlalchemy").Run() != nil {
		t.Skip("full MLflow is not installed")
	}
	dir := isolatedHomes(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	python, err = filepath.Abs(python)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := execx.MinimalEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := execx.NewInvoker().Invoke(ctx, execx.CommandSpec{Executable: python, Argv: []string{"-m", "mlflow", "server", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--workers", "1", "--backend-store-uri", "sqlite:///" + filepath.ToSlash(filepath.Join(dir, "server.db")), "--artifacts-destination", filepath.Join(dir, "server-artifacts")}, CWD: dir, Environment: environment, Timeout: 2 * time.Minute, Output: execx.DefaultOutputPolicy(execx.OutputCapture)})
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("tracking server did not terminate")
		}
	})
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}}
	deadline := time.Now().Add(60 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint + "/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("tracking server was not ready")
	}
	s, _ := DefaultSettings()
	s.Storage["remote"] = StorageProfile{Kind: "mlflow-remote", Root: filepath.Join(dir, "client"), TrackingURI: endpoint, Python: "python3", LargeBytes: 1 << 30}
	e, m, err := Prepare(t.Context(), s, testProject, testTry, testAttempt, "remote", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := PersistExecution(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.OutputDir, "plot.txt"), []byte("remote-plot"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err = Save(t.Context(), e, m, false)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := ObjectPath(e.Storage, m.Artifacts[0])
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	path, err := Fetch(t.Context(), e, m.Artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "remote-plot" {
		t.Fatalf("remote artifact roundtrip: %q %v", data, err)
	}
}
