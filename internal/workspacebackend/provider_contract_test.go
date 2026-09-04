package workspacebackend

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
)

type contractBackend struct {
	prepare func(context.Context, PrepareRequest) (Workspace, error)
	verify  func(context.Context, PrepareRequest) (Workspace, error)
	inspect func(context.Context, PrepareRequest) (Inspection, error)
	finish  func(context.Context, PrepareRequest, FinalizeAction) (Inspection, error)
}

func (backend contractBackend) Prepare(ctx context.Context, request PrepareRequest) (Workspace, error) {
	if backend.prepare == nil {
		return Workspace{}, errors.New("unexpected prepare")
	}
	return backend.prepare(ctx, request)
}

func (backend contractBackend) VerifyPrepared(ctx context.Context, request PrepareRequest) (Workspace, error) {
	if backend.verify != nil {
		return backend.verify(ctx, request)
	}
	if backend.inspect != nil {
		inspection, err := backend.inspect(ctx, request)
		return inspection.Workspace, err
	}
	return Workspace{}, errors.New("unexpected verify")
}

func (backend contractBackend) Inspect(ctx context.Context, request PrepareRequest) (Inspection, error) {
	if backend.inspect == nil {
		return Inspection{}, errors.New("unexpected inspect")
	}
	return backend.inspect(ctx, request)
}

func (backend contractBackend) CleanupOrHandoff(ctx context.Context, request PrepareRequest, action FinalizeAction) (Inspection, error) {
	if backend.finish == nil {
		return Inspection{}, errors.New("unexpected finalize")
	}
	return backend.finish(ctx, request, action)
}

type recordingInvoker struct {
	calls []execx.CommandSpec
}

func (invoker *recordingInvoker) Invoke(context.Context, execx.CommandSpec) (execx.Result, error) {
	invoker.calls = append(invoker.calls, execx.CommandSpec{})
	return execx.Result{}, errors.New("unexpected provider invocation")
}

func TestDevCLICapabilityContractFailsClosedWithoutInvocations(t *testing.T) {
	invoker := &recordingInvoker{}
	adapter := DevCLI{Invoker: invoker, LookupBinary: func(string) (string, error) { return "/synthetic/bin/dev", nil }}
	for capability, invoke := range map[Capability]func() error{
		CapabilityPrepare: func() error { _, err := adapter.Prepare(t.Context(), PrepareRequest{}); return err },
		CapabilityOpen:    func() error { _, err := adapter.Open(t.Context(), contractBackend{}, PrepareRequest{}); return err },
		CapabilityHandoff: func() error { _, err := adapter.Handoff(t.Context(), contractBackend{}, PrepareRequest{}); return err },
		CapabilityRetire:  func() error { _, err := adapter.Retire(t.Context(), contractBackend{}, PrepareRequest{}); return err },
	} {
		err := invoke()
		var unsupported *UnsupportedCapabilityError
		if !errors.As(err, &unsupported) || !errors.Is(err, ErrUnsupportedCapability) || unsupported.Provider != DevCLIName || unsupported.Capability != capability {
			t.Fatalf("%s result = %#v, %v", capability, unsupported, err)
		}
	}
	for _, status := range devCLIDescriptor().Capabilities {
		if status.Support != SupportUnsupported {
			t.Fatalf("dev-cli capability %s = %s", status.Capability, status.Support)
		}
	}
	if len(invoker.calls) != 0 {
		t.Fatalf("unsupported capabilities invoked dev-cli: %#v", invoker.calls)
	}
}

func TestDevCLIReadinessNeverInfersSafetyFromHumanOrInventoryOutput(t *testing.T) {
	invoker := &recordingInvoker{}
	adapter := DevCLI{
		Invoker: invoker,
		LookupBinary: func(name string) (string, error) {
			if name != "dev" {
				t.Fatalf("binary lookup = %q", name)
			}
			return "/synthetic/bin/dev", nil
		},
	}
	installed, err := adapter.Readiness(t.Context(), t.TempDir(), false)
	if err != nil || installed.State != ReadinessInstalledNotProbed || installed.Probed || !installed.BinaryFound {
		t.Fatalf("LookPath-only readiness = %#v, %v", installed, err)
	}
	ready, err := adapter.Readiness(t.Context(), t.TempDir(), true)
	if ready.State != ReadinessUnsupported || !ready.Probed || ready.Reason != "machine-contract-unavailable" || !errors.Is(err, ErrProviderIncompatible) {
		t.Fatalf("live readiness = %#v, %v", ready, err)
	}
	for _, capability := range ready.Capabilities {
		if capability.Support != SupportUnsupported {
			t.Fatalf("live capability %s = %s", capability.Capability, capability.Support)
		}
	}
	if len(invoker.calls) != 0 {
		t.Fatalf("readiness parsed an unsafe provider surface: %#v", invoker.calls)
	}
}

func TestDevCLIReadinessCancellationAndInvalidBinaryRemainTyped(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	adapter := DevCLI{LookupBinary: func(string) (string, error) { return "/synthetic/bin/dev", nil }}
	status, err := adapter.Readiness(cancelled, t.TempDir(), true)
	if status.State != ReadinessUnknown || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled readiness = %#v, %v", status, err)
	}

	invoked := &recordingInvoker{}
	adapter = DevCLI{
		Invoker:      invoked,
		LookupBinary: func(string) (string, error) { return "/synthetic/bin/dev\n--evil", nil },
	}
	status, err = adapter.Readiness(t.Context(), t.TempDir(), true)
	if status.State != ReadinessUnknown || !errors.Is(err, ErrProviderProbeFailed) || len(invoked.calls) != 0 {
		t.Fatalf("invalid binary readiness = %#v, %v calls=%d", status, err, len(invoked.calls))
	}
}

func TestProviderSelectionTrustPrecedenceAndFallbackPolicy(t *testing.T) {
	user := providerConfigResult(config.LayerUser, true)
	selection, err := ResolveSelection("", user)
	if err != nil || selection.Requested != DevCLIName || selection.Origin != SelectionUser || !selection.AllowUnavailableFallback || !selection.AllowUnsupportedFallback {
		t.Fatalf("user selection = %#v, %v", selection, err)
	}

	repository := providerConfigResult(config.LayerSubdir, true)
	selection, err = ResolveSelection("", repository)
	if err != nil || selection.Origin != SelectionRepository || selection.Requested != DevCLIName {
		t.Fatalf("trusted repository selection = %#v, %v", selection, err)
	}

	untrusted := providerConfigResult(config.LayerSource, false)
	if _, err := ResolveSelection("", untrusted); !errors.Is(err, ErrUntrustedSelection) || !errors.Is(err, config.ErrUntrusted) {
		t.Fatalf("untrusted repository selection = %v", err)
	}
	selection, err = ResolveSelection(NativeGitName, untrusted)
	if err != nil || selection.Origin != SelectionExplicit || selection.Requested != NativeGitName {
		t.Fatalf("explicit override did not win: %#v, %v", selection, err)
	}
	if _, err := ResolveSelection("not-a-provider", user); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown explicit provider = %v", err)
	}
}

func TestRegistryDevFallbackUsesNativeOnlyForNativeSupportedCapabilities(t *testing.T) {
	cwd := canonicalBackendTemp(t)
	request, inspection := stubProviderRequest(t, cwd)
	prepared := 0
	native := contractBackend{
		prepare: func(context.Context, PrepareRequest) (Workspace, error) {
			prepared++
			return inspection.Workspace, nil
		},
		inspect: func(context.Context, PrepareRequest) (Inspection, error) { return inspection, nil },
	}
	registry := MustRegistry(native, DevCLI{LookupBinary: func(string) (string, error) { return "/synthetic/bin/dev", nil }})
	request.Provider = ProviderSelection{
		Requested: DevCLIName, Origin: SelectionUser, Trusted: true,
		AllowUnavailableFallback: true, AllowUnsupportedFallback: true,
	}
	workspace, err := registry.Prepare(t.Context(), request)
	if err != nil || workspace != inspection.Workspace || prepared != 1 {
		t.Fatalf("policy fallback prepare = %#v, %v prepared=%d", workspace, err, prepared)
	}
	if _, err := registry.Resolve(t.Context(), request.Provider, CapabilityHandoff, cwd); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("unsupported native handoff fallback = %v", err)
	}

	request.Provider = ProviderSelection{Requested: DevCLIName, Origin: SelectionExplicit, Trusted: true, AllowUnsupportedFallback: true}
	if _, err := registry.Prepare(t.Context(), request); err != nil || prepared != 2 {
		t.Fatalf("explicit unsupported prepare fallback = %v prepared=%d", err, prepared)
	}
}

func TestNativeHandoffNeverReportsAnUnperformedOpen(t *testing.T) {
	request, _ := stubProviderRequest(t, canonicalBackendTemp(t))
	request.Provider = ProviderSelection{Requested: NativeGitName, Origin: SelectionBuiltin, Trusted: true}
	registry := MustRegistry(contractBackend{}, DevCLI{})
	if _, err := registry.CleanupOrHandoff(t.Context(), request, FinalizeHandoff); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("native handoff = %v", err)
	}
	if supportFor(nativeDescriptor(), CapabilityHandoff) != SupportUnsupported || supportFor(nativeDescriptor(), CapabilityOpen) != SupportUnsupported {
		t.Fatalf("native open capabilities = %#v", nativeDescriptor().Capabilities)
	}
}

func TestRegistryPrepareRejectsMismatchedNativePostcondition(t *testing.T) {
	cwd := canonicalBackendTemp(t)
	request, inspection := stubProviderRequest(t, cwd)
	request.Provider = ProviderSelection{Requested: NativeGitName, Origin: SelectionBuiltin, Trusted: true}
	malicious := inspection.Workspace
	malicious.Root = filepath.Join(cwd, "provider-chosen-path")
	inspected := 0
	registry := MustRegistry(contractBackend{
		prepare: func(context.Context, PrepareRequest) (Workspace, error) { return malicious, nil },
		inspect: func(context.Context, PrepareRequest) (Inspection, error) {
			inspected++
			return inspection, nil
		},
	}, DevCLI{})
	if _, err := registry.Prepare(t.Context(), request); !errors.Is(err, ErrPostcondition) || inspected != 1 {
		t.Fatalf("mismatched Prepare postcondition = %v inspections=%d", err, inspected)
	}
}

func TestNativePrepareRecapturesSnapshotChangeSet(t *testing.T) {
	repository, base := newBackendRepository(t)
	writeBackendFile(t, filepath.Join(repository, "README.md"), "second\n", 0o644)
	runBackendGit(t, repository, "add", "--", "README.md")
	runBackendGit(t, repository, "commit", "-qm", "second")
	head := backendGitLine(t, repository, "rev-parse", "HEAD")
	gitInfo, err := gitx.Discover(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	source := backendSource(t, "src_01a09300-0000-7101-8000-000000000101", ".")
	snapshot, err := (sourcesnapshot.Capturer{}).CaptureClean(t.Context(), sourcesnapshot.Request{
		Source: source, RepositoryRoot: repository, RegisteredGitCommonDir: gitInfo.GitCommonDir,
		BaseCommit: base, ExpectedHead: head, CapturedAt: time.Date(2026, 9, 4, 7, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.ChangeSet, []string{"README.md"}) {
		t.Fatalf("fixture change set = %v", snapshot.ChangeSet)
	}
	snapshot.ChangeSet = []string{"services/api/model.txt"}
	snapshot.Digest, err = research.SourceSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	request := PrepareRequest{
		ProjectID: backendProjectID(t, "01a09300-0000-7100-8000-000000000100"), Source: source,
		RepositoryRoot: repository, RegisteredGitCommonDir: gitInfo.GitCommonDir,
		OwnerID:    backendID(t, "try_01a09300-0000-7102-8000-000000000102", research.KindTry),
		OwnerTitle: "Malicious Change Set", Snapshot: snapshot, AllowedPathGlobs: []string{"**"},
	}
	if _, err := (NativeGit{DataHome: filepath.Join(canonicalBackendTemp(t), "recapture-data")}).Prepare(t.Context(), request); !errors.Is(err, ErrSeedMismatch) {
		t.Fatalf("malicious snapshot change set = %v", err)
	}
}

func providerConfigResult(layer config.LayerKind, trusted bool) *config.Result {
	effective := config.Builtins()
	effective.Defaults.WorkspaceBackend = DevCLIName
	effective.Workspace.Backends[DevCLIName] = config.BackendPreference{Enabled: true, Priority: 50}
	return &config.Result{
		Effective: effective,
		Provenance: map[string]config.Provenance{
			"defaults.workspace_backend":            {Source: "config", Layer: layer, Trusted: trusted},
			"workspace.backends.dev_cli.enabled":    {Source: "config", Layer: layer, Trusted: trusted},
			"workspace.preferred_backends":          {Source: "<built-in>", Layer: config.LayerBuiltin, Trusted: true},
			"workspace.backends.native_git.enabled": {Source: "<built-in>", Layer: config.LayerBuiltin, Trusted: true},
		},
	}
}

func stubProviderRequest(t *testing.T, root string) (PrepareRequest, Inspection) {
	t.Helper()
	source := backendSource(t, "src_01a09500-0000-7101-8000-000000000101", ".")
	owner := backendID(t, "try_01a09500-0000-7102-8000-000000000102", research.KindTry)
	head := strings.Repeat("a", 40)
	request := PrepareRequest{
		ProjectID: backendProjectID(t, "01a09500-0000-7100-8000-000000000100"), Source: source,
		RepositoryRoot: root, RegisteredGitCommonDir: filepath.Join(root, ".git"),
		OwnerID: owner, Snapshot: research.SourceSnapshot{Source: source.ID, Subdir: ".", HeadCommit: head, Digest: "sha256:stub"},
		Provider: ProviderSelection{Requested: DevCLIName, Origin: SelectionExplicit, Trusted: true, AllowUnsupportedFallback: true},
	}
	workspaceRoot := filepath.Join(root, "managed")
	workspace := Workspace{
		Backend: NativeGitName, ProjectID: request.ProjectID, SourceID: source.ID, OwnerID: owner,
		Root: workspaceRoot, CWD: workspaceRoot, Branch: "exp/stub", HeadCommit: head, SnapshotDigest: request.Snapshot.Digest,
	}
	return request, Inspection{Workspace: workspace, HeadCommit: head, Clean: true, Paths: []string{}}
}
