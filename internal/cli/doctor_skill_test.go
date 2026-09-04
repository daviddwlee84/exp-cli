package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/skill"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

func TestDoctorUsesOnlyInjectedExecutableLookup(t *testing.T) {
	app := NewApp(t.Context(), nil, nil, nil)
	app.Now = func() time.Time { return time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC) }
	var lookups []string
	app.BinaryLookup = func(name string) (string, error) {
		lookups = append(lookups, name)
		if name == "pueue" {
			return "/synthetic/bin/pueue", nil
		}
		return "", errors.New("missing")
	}
	app.Invoker = execx.InvokerFunc(func(context.Context, execx.CommandSpec) (execx.Result, error) {
		t.Fatal("doctor executed a discovered third-party binary")
		return execx.Result{}, nil
	})

	invocation := invokeCommand(t, app, "", "doctor", "--json")
	requireCommandSuccess(t, invocation)
	envelope := decodeEnvelope(t, invocation.stdout)
	var data doctorData
	decodeData(t, envelope, &data)
	if data.LiveRequested || data.LiveProbesPerformed || len(data.Providers) != 7 || len(data.WorkspaceBackends) != 2 {
		t.Fatalf("doctor data=%#v", data)
	}
	if data.WorkspaceBackends[0].Provider != "dev_cli" || data.WorkspaceBackends[0].State != "missing" ||
		data.WorkspaceBackends[1].Provider != "native_git" || data.WorkspaceBackends[1].State != "built-in" {
		t.Fatalf("workspace backend readiness=%#v", data.WorkspaceBackends)
	}
	direct := findDoctorProvider(t, data.Providers, provider.ProviderDirect)
	pueue := findDoctorProvider(t, data.Providers, provider.ProviderPueue)
	dvc := findDoctorProvider(t, data.Providers, provider.ProviderDVC)
	if !direct.BuiltIn || !direct.Found || direct.Missing {
		t.Fatalf("direct status = %#v", direct)
	}
	if pueue.BuiltIn || !pueue.Found || pueue.Missing || pueue.Version != "" || !hasProviderDiagnostic(pueue.Diagnostics, "version_probe_not_configured") {
		t.Fatalf("Pueue status = %#v", pueue)
	}
	for _, capability := range pueue.Capabilities {
		if capability.Support != provider.SupportUnknown {
			t.Fatalf("binary presence promoted %s to %s", capability.Name, capability.Support)
		}
	}
	if !dvc.Missing || dvc.Found || dvc.Version != "" {
		t.Fatalf("missing DVC status = %#v", dvc)
	}
	wantLookups := []string{"dvc", "jupyter", "marimo", "mlflow", "pueue", "sacct", "sbatch", "scancel", "squeue", "dev"}
	if !reflect.DeepEqual(lookups, wantLookups) {
		t.Fatalf("doctor lookups = %v, want %v", lookups, wantLookups)
	}

	lookups = nil
	var liveCalls [][]string
	app.Invoker = execx.InvokerFunc(func(_ context.Context, spec execx.CommandSpec) (execx.Result, error) {
		liveCalls = append(liveCalls, append([]string{}, spec.Argv...))
		switch strings.Join(spec.Argv, " ") {
		case "--version":
			return execx.Result{Stdout: "pueue 4.0.4\n", ExitCode: 0}, nil
		case "status --json":
			return execx.Result{Stdout: `{"tasks":{},"groups":{}}`, ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected live doctor argv: %v", spec.Argv)
			return execx.Result{}, nil
		}
	})
	live := invokeCommand(t, app, "", "doctor", "--live", "--json")
	requireCommandSuccess(t, live)
	liveEnvelope := decodeEnvelope(t, live.stdout)
	decodeData(t, liveEnvelope, &data)
	if !data.LiveRequested || !data.LiveProbesPerformed || len(liveEnvelope.Diagnostics) != 1 || liveEnvelope.Diagnostics[0].Code != "doctor.local_workspace_probe" {
		t.Fatalf("live doctor result = envelope %#v data %#v", liveEnvelope, data)
	}
	livePueue := findDoctorProvider(t, data.Providers, provider.ProviderPueue)
	if livePueue.State != provider.ReadinessReady || !livePueue.Probed || len(liveCalls) != 2 {
		t.Fatalf("live Pueue readiness = %#v calls=%v", livePueue, liveCalls)
	}
	if !reflect.DeepEqual(lookups, wantLookups) {
		t.Fatalf("--live performed unexpected discovery: lookups=%v", lookups)
	}

	human := invokeCommand(t, app, "", "doctor")
	if human.err != nil || !strings.Contains(human.stdout, "built-in") || !strings.Contains(human.stdout, "missing") || !strings.Contains(human.stdout, "unknown") {
		t.Fatalf("human doctor = %q, %v", human.stdout, human.err)
	}
}

func TestDoctorLiveProbeKeepsInconclusiveAndUnimplementedProvidersUnknown(t *testing.T) {
	registry := provider.CompiledRegistry()
	mlflowDescriptor, _ := registry.Get(provider.ProviderMLflow)
	timedOut := NewApp(t.Context(), nil, nil, nil)
	timedOut.Invoker = execx.InvokerFunc(func(context.Context, execx.CommandSpec) (execx.Result, error) {
		return execx.Result{TimedOut: true}, &execx.Error{Kind: execx.ErrorTimeout, Reason: "test timeout"}
	})
	result, err := doctorLiveProbe(timedOut)(t.Context(), mlflowDescriptor, "/synthetic/mlflow")
	if err != nil || result.Readiness != provider.ReadinessUnknown || result.Reason != "version-probe-failed" {
		t.Fatalf("timed-out provider probe = %#v, %v", result, err)
	}
	for capability, support := range result.Capabilities {
		if support != provider.SupportUnknown {
			t.Fatalf("timeout promoted %s to %s", capability, support)
		}
	}

	slurmDescriptor, _ := registry.Get(provider.ProviderSlurm)
	versionOnly := NewApp(t.Context(), nil, nil, nil)
	versionOnly.Invoker = execx.InvokerFunc(func(context.Context, execx.CommandSpec) (execx.Result, error) {
		return execx.Result{Stdout: "slurm 25.05.1\n", ExitCode: 0}, nil
	})
	result, err = doctorLiveProbe(versionOnly)(t.Context(), slurmDescriptor, "/synthetic/squeue")
	if err != nil || result.Readiness != provider.ReadinessUnknown || result.Reason != "capabilities-not-probed" {
		t.Fatalf("single-binary Slurm probe = %#v, %v", result, err)
	}
	for capability, support := range result.Capabilities {
		if support != provider.SupportUnknown {
			t.Fatalf("single Slurm binary promoted %s to %s", capability, support)
		}
	}

	if !doctorPartial([]doctorProviderView{{
		Name: provider.ProviderMLflow, State: provider.ReadinessReady,
		Capabilities: []doctorCapabilityView{{Name: provider.CapabilityTrackerResolve, Support: provider.SupportUnknown}},
	}}, nil) {
		t.Fatal("unknown capability support was omitted from aggregate partiality")
	}
	if !doctorPartial(nil, []workspacebackend.Readiness{{
		Provider: workspacebackend.DevCLIName, State: workspacebackend.ReadinessInstalledNotProbed,
		Capabilities: []workspacebackend.CapabilityStatus{{Capability: workspacebackend.CapabilityHandoff, Support: workspacebackend.SupportUnsupported}},
	}}) {
		t.Fatal("installed-not-probed workspace backend was omitted from aggregate partiality")
	}
}

func TestDefaultDoctorNeverExecutesFoundBinaryOrMutatesHome(t *testing.T) {
	temporary := t.TempDir()
	home := filepath.Join(temporary, "home")
	binaryDir := filepath.Join(temporary, "bin")
	marker := filepath.Join(temporary, "executed")
	for _, directory := range []string{home, binaryDir} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake := filepath.Join(binaryDir, "pueue")
	payload := "#!/bin/sh\nprintf executed > " + marker + "\nmkdir -p \"$HOME/provider-state\" \"$XDG_CONFIG_HOME/provider-state\" \"$XDG_CACHE_HOME/provider-state\" \"$XDG_DATA_HOME/provider-state\" \"$XDG_STATE_HOME/provider-state\"\n"
	if err := os.WriteFile(fake, []byte(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	xdgConfig := filepath.Join(temporary, "xdg-config")
	xdgCache := filepath.Join(temporary, "xdg-cache")
	xdgData := filepath.Join(temporary, "xdg-data")
	xdgState := filepath.Join(temporary, "xdg-state")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_CACHE_HOME", xdgCache)
	t.Setenv("XDG_DATA_HOME", xdgData)
	t.Setenv("XDG_STATE_HOME", xdgState)
	t.Setenv("PATH", binaryDir)

	invocation := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", "doctor", "--json")
	requireCommandSuccess(t, invocation)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor executed fake binary: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("doctor changed HOME: entries=%v err=%v", entries, err)
	}
	for _, directory := range []string{xdgConfig, xdgCache, xdgData, xdgState} {
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("doctor changed %s: %v", directory, err)
		}
	}
}

func TestDoctorLiveKeepsPartialProviderFailuresIndependentAndPathFree(t *testing.T) {
	const canary = "DOCTOR_CREDENTIAL_CANARY_193a"
	app := NewApp(t.Context(), nil, nil, nil)
	app.BinaryLookup = func(name string) (string, error) {
		switch name {
		case "pueue", "mlflow", "dev":
			return "/synthetic/tools/" + name, nil
		default:
			return "", errors.New("missing")
		}
	}
	app.Invoker = execx.InvokerFunc(func(_ context.Context, spec execx.CommandSpec) (execx.Result, error) {
		switch filepath.Base(spec.Executable) {
		case "pueue":
			if reflect.DeepEqual(spec.Argv, []string{"--version"}) {
				return execx.Result{Stdout: "pueue 4.1.0\n", ExitCode: 0}, nil
			}
			return execx.Result{}, errors.New("token=" + canary)
		case "mlflow":
			return execx.Result{Stdout: "mlflow, version 3.2.1\n", ExitCode: 0}, nil
		case "dev":
			return execx.Result{}, errors.New("password=" + canary)
		default:
			t.Fatalf("unexpected live executable: %s", spec.Executable)
			return execx.Result{}, nil
		}
	})
	invocation := invokeCommand(t, app, "", "doctor", "--live", "--json")
	requireCommandSuccess(t, invocation)
	envelope := decodeEnvelope(t, invocation.stdout)
	var data doctorData
	decodeData(t, envelope, &data)
	pueueView := findDoctorProvider(t, data.Providers, provider.ProviderPueue)
	mlflowView := findDoctorProvider(t, data.Providers, provider.ProviderMLflow)
	if !envelope.Partial || !data.Partial || pueueView.State != provider.ReadinessMisconfigured ||
		mlflowView.State != provider.ReadinessUnknown || mlflowView.Capabilities == nil {
		t.Fatalf("partial live readiness = envelope %#v data %#v", envelope, data)
	}
	if strings.Contains(invocation.stdout, canary) || strings.Contains(invocation.stdout, "/synthetic/") ||
		len(pueueView.Requirements) != 2 || len(mlflowView.Requirements) != 2 {
		t.Fatalf("doctor exposed a value/path or omitted requirements: %s", invocation.stdout)
	}
	if len(data.WorkspaceBackends) != 2 || data.WorkspaceBackends[0].State != "unsupported" || data.WorkspaceBackends[1].State != "built-in" {
		t.Fatalf("workspace readiness did not remain independent: %#v", data.WorkspaceBackends)
	}
}

func TestSkillPrintInstallCheckAndDriftUseIsolatedHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app := NewApp(t.Context(), nil, nil, nil)
	app.Now = func() time.Time { return time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC) }
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(canonicalHome, ".agents", "skills", skill.Name)

	printed := invokeCommand(t, app, "", "skill", "print")
	if printed.err != nil || !strings.HasPrefix(printed.stdout, "---\nname: exp-cli\n") {
		t.Fatalf("skill print = %q, %v", printed.stdout, printed.err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skill print mutated HOME: %v", err)
	}

	missing := invokeCommand(t, app, "", "skill", "check", "--json")
	if !errors.Is(missing.err, errSkillDrift) {
		t.Fatalf("missing skill check error = %v", missing.err)
	}
	missingEnvelope := decodeEnvelope(t, missing.stdout)
	var missingData skill.CheckResult
	decodeData(t, missingEnvelope, &missingData)
	if missingEnvelope.OK || missingData.Current || len(missingData.MissingFiles) == 0 || missingData.DriftedDirectories == nil || missingData.Files == nil || missingData.Links == nil {
		t.Fatalf("missing skill data = %#v", missingData)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skill check mutated HOME: %v", err)
	}

	installed := invokeCommand(t, app, "", "skill", "install", "--json")
	requireCommandSuccess(t, installed)
	var installData skill.InstallResult
	decodeData(t, decodeEnvelope(t, installed.stdout), &installData)
	if !installData.Changed || installData.Dir != destination || len(installData.Written) == 0 || installData.CreatedDirectories == nil || installData.RepairedDirectories == nil || installData.RemovedTemporaryFiles == nil || installData.Links == nil || installData.LinkResults == nil {
		t.Fatalf("install result = %#v", installData)
	}

	current := invokeCommand(t, app, "", "skill", "check", "--json")
	requireCommandSuccess(t, current)
	var currentData skill.CheckResult
	decodeData(t, decodeEnvelope(t, current.stdout), &currentData)
	if !currentData.Current || !currentData.ContentCurrent || !currentData.DirectoryModesCurrent || currentData.DriftedDirectories == nil || currentData.CurrentFiles == nil || currentData.DriftedFiles == nil {
		t.Fatalf("current skill check = %#v", currentData)
	}

	driftPath := filepath.Join(destination, "references", "methodology.md")
	if err := os.WriteFile(driftPath, []byte("local drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(driftPath)
	if err != nil {
		t.Fatal(err)
	}
	drifted := invokeCommand(t, app, "", "skill", "check", "--json")
	if !errors.Is(drifted.err, errSkillDrift) {
		t.Fatalf("drift check error = %v", drifted.err)
	}
	var driftData skill.CheckResult
	decodeData(t, decodeEnvelope(t, drifted.stdout), &driftData)
	if driftData.Current || !reflect.DeepEqual(driftData.DriftedFiles, []string{"references/methodology.md"}) {
		t.Fatalf("drift check = %#v", driftData)
	}
	after, err := os.ReadFile(driftPath)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("skill check repaired drift: %q, %v", after, err)
	}

	consumerBase := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(consumerBase, 0o755); err != nil {
		t.Fatal(err)
	}
	repairedAndLinked := invokeCommand(t, app, "", "skill", "install", "--link", "--json")
	requireCommandSuccess(t, repairedAndLinked)
	var linkedData skill.InstallResult
	decodeData(t, decodeEnvelope(t, repairedAndLinked.stdout), &linkedData)
	if len(linkedData.Links) != 1 {
		t.Fatalf("linked install = %#v", linkedData)
	}
	target, err := os.Readlink(filepath.Join(consumerBase, skill.Name))
	if err != nil || target != destination {
		t.Fatalf("consumer link target = %q, %v", target, err)
	}
	linksCurrent := invokeCommand(t, app, "", "skill", "check", "--links", "--json")
	requireCommandSuccess(t, linksCurrent)
	decodeData(t, decodeEnvelope(t, linksCurrent.stdout), &currentData)
	if !currentData.LinksCurrent || len(currentData.Links) != 1 || currentData.Links[0].State != skill.LinkCorrect {
		t.Fatalf("consumer link check = %#v", currentData)
	}
}

func hasProviderDiagnostic(diagnostics []provider.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func TestSkillInstallPreservesPublishedResultOnFailure(t *testing.T) {
	app := NewApp(t.Context(), nil, nil, nil)
	destination := filepath.Join(t.TempDir(), "exp-cli")
	app.ResolveDefaultSkillDir = func() (string, error) { return destination, nil }
	sentinel := errors.New("injected post-publication durability failure")
	app.InstallSkill = func(context.Context, string, bool) (skill.InstallResult, error) {
		return skill.InstallResult{
			Dir:                 destination,
			Changed:             true,
			Written:             []string{"SKILL.md"},
			Created:             []string{"SKILL.md"},
			RepairedDirectories: []string{},
		}, sentinel
	}
	invocation := invokeCommand(t, app, "", "skill", "install", "--json")
	if !errors.Is(invocation.err, sentinel) {
		t.Fatalf("skill install error = %v", invocation.err)
	}
	envelope := decodeEnvelope(t, invocation.stdout)
	var result skill.InstallResult
	decodeData(t, envelope, &result)
	if envelope.OK || !envelope.Partial || !result.Changed || !reflect.DeepEqual(result.Written, []string{"SKILL.md"}) || !reflect.DeepEqual(result.Created, []string{"SKILL.md"}) {
		t.Fatalf("partial skill install = envelope %#v result %#v", envelope, result)
	}
}

func TestSkillInstallTreatsDirectoryRepairAsPartialOnFailure(t *testing.T) {
	app := NewApp(t.Context(), nil, nil, nil)
	destination := filepath.Join(t.TempDir(), "exp-cli")
	app.ResolveDefaultSkillDir = func() (string, error) { return destination, nil }
	sentinel := errors.New("injected failure after directory repair")
	app.InstallSkill = func(context.Context, string, bool) (skill.InstallResult, error) {
		return skill.InstallResult{
			Dir:                 destination,
			Changed:             false,
			RepairedDirectories: []string{"."},
		}, sentinel
	}
	invocation := invokeCommand(t, app, "", "skill", "install", "--json")
	if !errors.Is(invocation.err, sentinel) {
		t.Fatalf("skill install error = %v", invocation.err)
	}
	envelope := decodeEnvelope(t, invocation.stdout)
	var result skill.InstallResult
	decodeData(t, envelope, &result)
	if envelope.OK || !envelope.Partial || result.Changed || !reflect.DeepEqual(result.RepairedDirectories, []string{"."}) || result.CreatedDirectories == nil || result.RemovedTemporaryFiles == nil || len(result.Written) != 0 || len(result.Links) != 0 {
		t.Fatalf("directory-repair partial result = envelope %#v result %#v", envelope, result)
	}
}

func TestSkillCheckHumanReportsDirectoryModeDrift(t *testing.T) {
	app := NewApp(t.Context(), nil, nil, nil)
	destination := filepath.Join(t.TempDir(), "exp-cli")
	app.ResolveDefaultSkillDir = func() (string, error) { return destination, nil }
	app.CheckSkill = func(context.Context, string, skill.CheckOptions) (skill.CheckResult, error) {
		return skill.CheckResult{
			Dir:                   destination,
			ContentCurrent:        true,
			DirectoryModesCurrent: false,
			LinksCurrent:          true,
			DriftedDirectories:    []string{"."},
		}, nil
	}
	invocation := invokeCommand(t, app, "", "skill", "check")
	if !errors.Is(invocation.err, errSkillDrift) || !strings.Contains(invocation.stdout, "directory_modes_current=false") || !strings.Contains(invocation.stdout, "drifted_directories=1") {
		t.Fatalf("directory-mode drift output = %q, %v", invocation.stdout, invocation.err)
	}
}

func TestSkillDefaultResolutionFailsBeforeMutationOrCheck(t *testing.T) {
	sentinel := errors.New("home directory unavailable")
	for _, testCase := range []struct {
		name    string
		resolve func() (string, error)
	}{
		{name: "resolver error", resolve: func() (string, error) { return "", sentinel }},
		{name: "relative fallback", resolve: func() (string, error) { return ".agents/skills/exp-cli", nil }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			app := NewApp(t.Context(), nil, nil, nil)
			app.ResolveDefaultSkillDir = testCase.resolve
			installCalled := false
			checkCalled := false
			app.InstallSkill = func(context.Context, string, bool) (skill.InstallResult, error) {
				installCalled = true
				return skill.InstallResult{}, nil
			}
			app.CheckSkill = func(context.Context, string, skill.CheckOptions) (skill.CheckResult, error) {
				checkCalled = true
				return skill.CheckResult{}, nil
			}
			for _, command := range []string{"install", "check"} {
				invocation := invokeCommand(t, app, "", "skill", command, "--json")
				if invocation.err == nil {
					t.Fatalf("skill %s accepted an unavailable default", command)
				}
				envelope := decodeEnvelope(t, invocation.stdout)
				if envelope.OK || envelope.Partial || len(envelope.Diagnostics) != 1 {
					t.Fatalf("skill %s failure envelope = %#v", command, envelope)
				}
			}
			if installCalled || checkCalled {
				t.Fatalf("invalid default reached skill boundary: install=%t check=%t", installCalled, checkCalled)
			}
		})
	}
}

func findDoctorProvider(t *testing.T, providers []doctorProviderView, name provider.ProviderName) doctorProviderView {
	t.Helper()
	for _, entry := range providers {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("doctor provider %s not found", name)
	return doctorProviderView{}
}
