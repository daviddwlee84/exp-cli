package execx_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/execx"
)

func TestCommandSafeViewRedactsKnownAndExplicitSecrets(t *testing.T) {
	const canary = "execx-plan-canary-e94f"
	environment, err := execx.NewEnvironment(nil,
		execx.Bind("MODE", "test"),
		execx.BindSecret("TRACKER_TOKEN", canary),
	)
	if err != nil {
		t.Fatal(err)
	}
	spec := helperSpecWithEnvironment(t, environment, "argv",
		"--token", canary,
		"--label", "two words",
		"--opaque", canary,
	)
	spec.Redaction = execx.NewRedactor(canary)

	view, err := spec.SafeView()
	if err != nil {
		t.Fatalf("SafeView() error = %v", err)
	}
	if len(view.Argv) != len(spec.Argv) {
		t.Fatalf("SafeView() changed argv boundaries: %d != %d", len(view.Argv), len(spec.Argv))
	}
	if view.Environment == nil {
		t.Fatal("SafeView() encoded a nil environment")
	}
	for _, rendered := range []string{
		mustCommandJSON(t, view),
		mustCommandJSON(t, spec),
		spec.String(),
		fmt.Sprintf("%#v", spec),
	} {
		if strings.Contains(rendered, canary) {
			t.Fatalf("safe command rendering leaked canary: %q", rendered)
		}
	}
}

func TestSensitiveArgumentIndexesAreValidatedAndRedacted(t *testing.T) {
	const opaque = "not-named-like-a-secret"
	spec := helperSpec(t, "argv", "--opaque", opaque)
	spec.SensitiveArgs = []int{len(spec.Argv) - 1}
	view, err := spec.SafeView()
	if err != nil {
		t.Fatalf("SafeView() error = %v", err)
	}
	if got := view.Argv[len(view.Argv)-1]; got != execx.Redacted {
		t.Fatalf("sensitive argument = %q, want %q", got, execx.Redacted)
	}

	spec.SensitiveArgs = []int{len(spec.Argv)}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate() accepted an out-of-range sensitive index")
	}
}

func TestSensitiveFlagIndexDoesNotExposeFollowingValue(t *testing.T) {
	const canary = "execx-overlap-canary-14c2"
	spec := helperSpec(t, "argv", "--token", canary)
	flagIndex := len(spec.Argv) - 2
	spec.SensitiveArgs = []int{flagIndex}
	view, err := spec.SafeView()
	if err != nil {
		t.Fatal(err)
	}
	if view.Argv[flagIndex] != execx.Redacted || view.Argv[flagIndex+1] != execx.Redacted {
		t.Fatalf("overlapping sensitive argv = %#v", view.Argv)
	}
	if rendered := mustCommandJSON(t, spec); strings.Contains(rendered, canary) {
		t.Fatalf("CommandSpec JSON leaked overlapping canary: %s", rendered)
	}
}

func TestCommandSafeViewAndErrorRedactAttachedShortOptions(t *testing.T) {
	const canary = "execx-attached-short-canary-a821"
	for _, argv := range [][]string{
		{"-HAuthorization: Bearer " + canary},
		{"-H=Cookie: session=" + canary},
		{"-ualice:" + canary},
		{"-u=alice:" + canary},
		{"--header=Authorization: Bearer " + canary},
		{"--user=alice:" + canary},
	} {
		spec := helperSpec(t, "argv")
		spec.Argv = append(spec.Argv, argv...)
		view, err := spec.SafeView()
		if err != nil {
			t.Fatalf("SafeView(%q) error = %v", argv, err)
		}
		spec.Output.Mode = execx.OutputMode("invalid")
		validationErr := spec.Validate()
		if validationErr == nil {
			t.Fatalf("Validate(%q) succeeded", argv)
		}
		for _, rendered := range []string{mustCommandJSON(t, view), validationErr.Error(), fmt.Sprintf("%#v", validationErr)} {
			if strings.Contains(rendered, canary) {
				t.Errorf("attached option %q leaked in %q", argv, rendered)
			}
		}
	}
}

func TestErrorRenderingReappliesAttachedOptionRedaction(t *testing.T) {
	const canary = "execx-mutated-error-canary-37be"
	err := &execx.Error{
		Kind:       execx.ErrorExit,
		Executable: "/synthetic/bin/curl",
		Argv:       []string{"-ualice:" + canary, "-HAuthorization: Bearer " + canary},
		CWD:        "/synthetic/repo",
		Result:     execx.Result{ExitCode: 1, Stdout: canary, Stderr: "password=" + canary},
		Reason:     "Basic " + canary,
	}
	for _, rendered := range []string{err.Error(), fmt.Sprintf("%#v", err), mustCommandJSON(t, err)} {
		if strings.Contains(rendered, canary) || !strings.Contains(rendered, execx.Redacted) {
			t.Fatalf("Error rendering leaked attached-option value: %q", rendered)
		}
	}
}

func mustCommandJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(encoded)
}
