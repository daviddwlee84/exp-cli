//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestRealCLIPlanInputExitsWithCanceledEnvelopeWhileStdinRemainsOpen(t *testing.T) {
	binary := buildRealCLI(t)

	for _, testCase := range []struct {
		name   string
		signal syscall.Signal
	}{
		{name: "SIGINT", signal: syscall.SIGINT},
		{name: "SIGTERM", signal: syscall.SIGTERM},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(binary, "plan", "add", "--input", "-", "--json")
			stdin, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			partial := `{"schema_version":"exp.request.plan-add/v1"` + strings.Repeat(" ", maxPlanRequestBytes/2)
			if _, err := io.WriteString(stdin, partial); err != nil {
				t.Fatalf("write partial request: %v", err)
			}
			// A payload larger than an OS pipe buffer cannot finish writing until exp
			// has installed its signal context and entered the bounded stdin read. EOF
			// deliberately remains withheld for the cancellation regression.
			if err := command.Process.Signal(testCase.signal); err != nil {
				t.Fatalf("send %s: %v", testCase.name, err)
			}

			finished := make(chan error, 1)
			go func() { finished <- command.Wait() }()
			select {
			case waitErr := <-finished:
				var exitErr *exec.ExitError
				if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 1 {
					t.Fatalf("process wait error = %v, stderr=%q", waitErr, stderr.String())
				}
			case <-time.After(3 * time.Second):
				_ = command.Process.Kill()
				t.Fatal("process remained blocked on open stdin after cancellation")
			}
			envelope := decodeEnvelope(t, stdout.String())
			if envelope.OK || envelope.Partial || envelope.Command != "plan add" || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "command.canceled" {
				t.Fatalf("canceled envelope = %#v; stderr=%q", envelope, stderr.String())
			}
		})
	}
}

func TestRealCLIPlanAddSucceedsWhenResultReaderCloses(t *testing.T) {
	binary := buildRealCLI(t)
	repository := newGitRepository(t)

	initialize := exec.Command(binary, "--start-dir", repository, "init", "--name", "Broken Pipe", "--json")
	if output, err := initialize.CombinedOutput(); err != nil {
		t.Fatalf("initialize repository: %v\n%s", err, output)
	}

	command := exec.Command(binary,
		"--start-dir", repository,
		"plan", "add",
		"--title", "Commit before closed output",
		"--priority", "P1",
		"--effort", "S",
		"--payoff-summary", "Do not invite a retry",
		"--payoff-metric", "plans",
		"--payoff-unit", "count",
		"--json",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Close the only read end before the process starts. The Plan commit and
	// projection refresh still happen before the first result write, which must
	// report consumer abandonment as success instead of inviting a retry.
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("Plan add with abandoned result stream exited unsuccessfully: %v; stderr=%q", err, stderr.String())
	}

	inventory, err := record.LoadInventory(filepath.Join(repository, "experiments"))
	if err != nil {
		t.Fatal(err)
	}
	if plans := inventory.OfKind(research.KindPlan); len(plans) != 1 {
		t.Fatalf("committed Plans = %d, want exactly one", len(plans))
	}
}

func TestRealCLIPureBrokenPipeIsSuccessfulButJoinedFailureIsNot(t *testing.T) {
	binary := buildRealCLI(t)
	for _, args := range [][]string{
		{"--version"},
		{"completion", "bash"},
		{"--skill"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			command := exec.Command(binary, args...)
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := stdout.Close(); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("successful output with abandoned reader failed: %v; stderr=%q", err, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("successful output reported stderr: %q", stderr.String())
			}
		})
	}

	command := exec.Command(binary, "--color", "rainbow", "--json")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err = command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(stderr.String(), "invalid --color value") {
		t.Fatalf("joined command/write failure was swallowed: error=%v stderr=%q", err, stderr.String())
	}
}

func TestRealCLIAgentRunExecutesOnceAndSucceedsWhenReaderCloses(t *testing.T) {
	binary := buildRealCLI(t)
	directory := t.TempDir()
	marker := filepath.Join(directory, "agent-ran")
	agent := filepath.Join(directory, "fake-agent")
	script := "#!/bin/sh\n" +
		"printf '%s' '{\"winner\":\"challenger\"}' > \"$1\"\n" +
		"printf 'ran\\n' > \"$2\"\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "agents.toml")
	configuration := "schema = \"exp.agents/v1\"\n" +
		"[roles]\nranker = \"fake\"\n" +
		"[profiles.fake]\nexecutable = \"fake-agent\"\n" +
		"args = [\"{output_file}\", \"" + marker + "\"]\n" +
		"output = \"output_file_json\"\ntimeout = \"20s\"\n"
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(directory, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["winner"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(directory, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte("rank"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(binary, "agent", "run",
		"--config", configPath,
		"--role", "ranker",
		"--schema", schemaPath,
		"--prompt", promptPath,
		"--cwd", directory,
	)
	command.Env = append(os.Environ(), "PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("agent run with abandoned reader failed after provider execution: %v; stderr=%q", err, stderr.String())
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "ran\n" {
		t.Fatalf("provider marker = %q, error=%v", content, err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("successful agent run reported stderr: %q", stderr.String())
	}
}

func buildRealCLI(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "exp")
	build := exec.Command("go", "build", "-o", binary, "./cmd/exp")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build exp: %v\n%s", err, output)
	}
	return binary
}
