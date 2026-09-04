package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/trust"
)

func TestMLflowProfileRejectsExpOwnedChildEnvironment(t *testing.T) {
	profile := MLflowProfile{
		Context: "local", Binary: "mlflow", Timeout: time.Second,
		Env: map[string]EnvBinding{"EXP_JOB_ID": {From: "PARENT_JOB_ID"}},
	}
	if err := validateMLflowProfile(profile); err == nil || !strings.Contains(err.Error(), "exp-owned") {
		t.Fatalf("EXP_ child binding validated: %v", err)
	}
}

func TestLoadPrecedenceReplacementMergeAndProvenance(t *testing.T) {
	fixture := newConfigFixture(t)
	writeExactConfig(t, fixture.userPath, `
schema = "exp.config/v1"

[defaults]
agent_profile = "user-agent"
mlflow_profile = "tracking"

[ui]
color = "always"

[workspace]
preferred_backends = ["native_git", "dev_cli"]

[workspace.backends.dev_cli]
enabled = true
priority = 10

[mlflow.profiles.tracking]
context = "user-context"
binary = "user-mlflow"
timeout = "2m"
default_metrics = ["loss", "accuracy"]

[mlflow.profiles.tracking.env.MLFLOW_TRACKING_URI]
from = "MLFLOW_TRACKING_URI"
required = true
`)
	canonicalPath := writeConfigAt(t, fixture.canonicalRoot, `
schema = "exp.config/v1"

[mlflow.profiles.tracking]
context = "canonical-context"

[workspace.backends.dev_cli]
priority = 50
`)
	sourcePath := writeConfigAt(t, fixture.sourceRoot, `
schema = "exp.config/v1"

[defaults]
source = "production"
workspace_backend = "dev_cli"

[workspace]
preferred_backends = []
`)
	writeConfigAt(t, fixture.subdirRoot, `
schema = "exp.config/v1"

[ui]
color = "never"
`)
	leafPath := writeConfigAt(t, fixture.invocation, `
schema = "exp.config/v1"

[defaults]
agent_profile = "leaf-agent"
`)

	result, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Layers) != 6 {
		t.Fatalf("layers = %#v", result.Layers)
	}
	wantKinds := []LayerKind{LayerBuiltin, LayerUser, LayerCanonical, LayerSource, LayerSubdir, LayerSubdir}
	for index, want := range wantKinds {
		if result.Layers[index].Kind != want {
			t.Errorf("layer %d kind = %s, want %s", index, result.Layers[index].Kind, want)
		}
	}
	if result.Layers[1].TrustSubject != nil || result.Layers[2].TrustSubject == nil || result.Layers[2].TrustSubject.GitCommonDir != fixture.canonicalCommon || result.Layers[2].TrustSubject.ConfigScope != RelativePath {
		t.Fatalf("layer trust subjects = user %#v canonical %#v", result.Layers[1].TrustSubject, result.Layers[2].TrustSubject)
	}
	if result.Effective.Defaults.Source != "production" || result.Effective.Defaults.WorkspaceBackend != "dev_cli" || result.Effective.Defaults.AgentProfile != "leaf-agent" || result.Effective.Defaults.MLflowProfile != "tracking" {
		t.Fatalf("effective defaults = %#v", result.Effective.Defaults)
	}
	if result.Effective.UI.Color != ColorNever {
		t.Fatalf("color = %q", result.Effective.UI.Color)
	}
	if result.Effective.Workspace.PreferredBackends == nil || len(result.Effective.Workspace.PreferredBackends) != 0 {
		t.Fatalf("explicit empty preferred_backends was not preserved: %#v", result.Effective.Workspace.PreferredBackends)
	}
	backend := result.Effective.Workspace.Backends["dev_cli"]
	if !backend.Enabled || backend.Priority != 50 {
		t.Fatalf("keyed backend merge = %#v", backend)
	}
	profile := result.Effective.MLflow.Profiles["tracking"]
	if profile.Context != "canonical-context" || profile.Binary != "mlflow" || profile.Timeout != 30*time.Second || len(profile.Env) != 0 || len(profile.DefaultMetrics) != 0 {
		t.Fatalf("atomic profile replacement = %#v", profile)
	}
	checks := map[string]struct {
		source  string
		layer   LayerKind
		trusted bool
	}{
		"defaults.mlflow_profile":                  {fixture.userPath, LayerUser, true},
		"defaults.source":                          {sourcePath, LayerSource, false},
		"defaults.agent_profile":                   {leafPath, LayerSubdir, false},
		"ui.color":                                 {filepath.Join(fixture.subdirRoot, filepath.FromSlash(RelativePath)), LayerSubdir, true},
		"workspace.backends.dev_cli.enabled":       {fixture.userPath, LayerUser, true},
		"workspace.backends.dev_cli.priority":      {canonicalPath, LayerCanonical, false},
		"mlflow.profiles.tracking.binary":          {canonicalPath, LayerCanonical, false},
		"mlflow.profiles.tracking.default_metrics": {canonicalPath, LayerCanonical, false},
	}
	for field, want := range checks {
		got, found := result.SourceFor(field)
		if !found || got.Source != want.source || got.Layer != want.layer || got.Trusted != want.trusted {
			t.Errorf("provenance %s = %#v, found=%v; want source=%s layer=%s trusted=%v", field, got, found, want.source, want.layer, want.trusted)
		}
	}
	if err := result.RequireTrusted("ui.color", "defaults.mlflow_profile"); err != nil {
		t.Fatalf("pure/global fields should be usable: %v", err)
	}
	if err := result.RequireTrusted("defaults.agent_profile"); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("untrusted leaf agent selector = %v", err)
	}
	if !strings.HasPrefix(result.Digest, "sha256:") || len(result.Digest) != 71 {
		t.Fatalf("combined digest = %q", result.Digest)
	}
	if result.Legacy.Agents.Path != filepath.Join(filepath.Dir(filepath.Dir(fixture.userPath)), "exp", "agents.toml") {
		t.Fatalf("legacy agents path = %q", result.Legacy.Agents.Path)
	}
	if result.Legacy.Runtime.Path != filepath.Join(fixture.canonicalRoot, ".exp", "runtime.json") || result.Legacy.Runtime.Trusted {
		t.Fatalf("legacy runtime provenance = %#v", result.Legacy.Runtime)
	}
}

func TestRepositoryCapabilityTrustUsesExactFileDigest(t *testing.T) {
	fixture := newConfigFixture(t)
	canonicalPath := writeConfigAt(t, fixture.canonicalRoot, `
schema = "exp.config/v1"
[mlflow.profiles.tracking]
context = "canonical-context"
`)
	writeExactConfig(t, fixture.userPath, `
schema = "exp.config/v1"
[defaults]
mlflow_profile = "tracking"
`)
	initial, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath))
	if err != nil {
		t.Fatal(err)
	}
	provenance, found := initial.SourceFor("mlflow.profiles.tracking")
	if !found || provenance.Trusted {
		t.Fatalf("initial profile provenance = %#v, %v", provenance, found)
	}
	trustPath := filepath.Join(fixture.root, "state", "trust", "v1.json")
	store := trust.NewStore(trust.WithPath(trustPath))
	subject := trust.Subject{
		ProjectID: fixture.projectID, SourceID: fixture.sourceID,
		GitCommonDir: fixture.canonicalCommon, ConfigScope: RelativePath,
	}
	if _, err := store.Approve(context.Background(), trust.Query{
		Subject: subject, ConfigDigest: provenance.Digest,
		Capabilities: []trust.Capability{trust.CapabilityMLflowProfile},
	}); err != nil {
		t.Fatal(err)
	}
	trusted, err := Load(context.Background(), fixture.request,
		WithUserConfigPath(fixture.userPath), WithTrustChecker(store))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := trusted.SourceFor("mlflow.profiles.tracking.binary"); !got.Trusted {
		t.Fatalf("exact approved profile is untrusted: %#v", got)
	}
	content, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonicalPath, append(content, []byte("# exact-byte edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Load(context.Background(), fixture.request,
		WithUserConfigPath(fixture.userPath), WithTrustChecker(store))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := changed.SourceFor("mlflow.profiles.tracking.binary"); got.Trusted || got.Digest == provenance.Digest {
		t.Fatalf("edited config retained trust: %#v", got)
	}
}

func TestIdentitySelectorsMustEqualResolvedContext(t *testing.T) {
	fixture := newConfigFixture(t)
	otherProject := "01a05100-0000-7001-8000-000000000001"
	writeConfigAt(t, fixture.canonicalRoot, `
schema = "exp.config/v1"
[identity]
project = "`+otherProject+`"
`)
	if _, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath)); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("Project identity conflict = %v", err)
	}
	writeConfigAt(t, fixture.canonicalRoot, `
schema = "exp.config/v1"
[identity]
project = "`+fixture.projectID.String()+`"
source = "src_01a05100-0000-7002-8000-000000000002"
`)
	if _, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath)); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("Source identity conflict = %v", err)
	}
	writeConfigAt(t, fixture.canonicalRoot, `
schema = "exp.config/v1"
[identity]
project = "`+fixture.projectID.String()+`"
source = "`+fixture.sourceID.String()+`"
`)
	result, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath))
	if err != nil {
		t.Fatalf("matching identities: %v", err)
	}
	if provenance, found := result.SourceFor("identity.source"); !found || !provenance.Trusted {
		t.Fatalf("identity provenance = %#v, %v", provenance, found)
	}
}

func TestStrictSchemaRejectsUnknownAndSecretValueFields(t *testing.T) {
	for name, content := range map[string]string{
		"wrong schema":      `schema = "exp.config/v2"`,
		"unknown top level": `schema = "exp.config/v1"\nhook = "run-me"`,
		"secret value":      `schema = "exp.config/v1"\n[mlflow.profiles.bad]\ncontext = "bad"\ntoken = "SECRET"`,
		"run id":            `schema = "exp.config/v1"\n[mlflow.profiles.bad]\ncontext = "bad"\nrun_id = "run-123"`,
		"raw artifact uri":  `schema = "exp.config/v1"\n[mlflow.profiles.bad]\ncontext = "bad"\nartifact_uri = "https://tracking.invalid/run/123"`,
		"endpoint value":    `schema = "exp.config/v1"\n[mlflow.profiles.bad]\ncontext = "bad"\ntracking_uri = "https://tracking.invalid"`,
		"unknown env value": `schema = "exp.config/v1"\n[mlflow.profiles.bad]\ncontext = "bad"\n[mlflow.profiles.bad.env.TOKEN]\nfrom = "TOKEN"\nvalue = "SECRET"`,
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newConfigFixture(t)
			if strings.Contains(content, `\n`) {
				content = strings.ReplaceAll(content, `\n`, "\n")
			}
			writeConfigAt(t, fixture.canonicalRoot, content+"\n")
			if _, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath)); err == nil {
				t.Fatal("invalid config was accepted")
			} else if strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("error leaked unknown secret value: %v", err)
			}
		})
	}
}

func TestConfigRejectsSymlinksOversizeAndExcessDepth(t *testing.T) {
	t.Run("user config parent symlink", func(t *testing.T) {
		fixture := newConfigFixture(t)
		target := filepath.Join(fixture.root, "real-user")
		writeExactConfig(t, filepath.Join(target, Filename), "schema = \"exp.config/v1\"\n")
		alias := filepath.Join(fixture.root, "user-alias")
		if err := os.Symlink(target, alias); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		request := fixture.request
		request.UserConfigPath = filepath.Join(alias, Filename)
		if _, err := Load(context.Background(), request); err == nil || !errors.Is(err, pathx.ErrSymlink) {
			t.Fatalf("user config parent symlink = %v", err)
		}
	})

	t.Run("config directory symlink", func(t *testing.T) {
		fixture := newConfigFixture(t)
		outside := filepath.Join(fixture.root, "outside")
		writeConfigAt(t, outside, "schema = \"exp.config/v1\"\n")
		link := filepath.Join(fixture.sourceRoot, DirectoryName)
		if err := os.Symlink(filepath.Join(outside, DirectoryName), link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath)); err == nil || !errors.Is(err, pathx.ErrSymlink) {
			t.Fatalf("symlink config directory = %v", err)
		}
	})
	t.Run("config file symlink", func(t *testing.T) {
		fixture := newConfigFixture(t)
		target := filepath.Join(fixture.root, "outside-config.toml")
		writeExactConfig(t, target, "schema = \"exp.config/v1\"\n")
		configDir := filepath.Join(fixture.sourceRoot, DirectoryName)
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(configDir, Filename)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath)); err == nil || !errors.Is(err, pathx.ErrSymlink) {
			t.Fatalf("config file symlink = %v", err)
		}
	})

	t.Run("Source subdir symlink escape", func(t *testing.T) {
		fixture := newConfigFixture(t)
		outside := canonicalTempDir(t)
		link := filepath.Join(fixture.sourceRoot, "linked")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		request := fixture.request
		request.Source.Subdir = "linked"
		request.SourceSubdir = "linked"
		request.InvocationDir = outside
		if _, err := Load(context.Background(), request, WithUserConfigPath(fixture.userPath)); err == nil || !errors.Is(err, pathx.ErrSymlink) {
			t.Fatalf("escaping Source subdir = %v", err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		fixture := newConfigFixture(t)
		writeConfigAt(t, fixture.canonicalRoot, "schema = \"exp.config/v1\"\n# "+strings.Repeat("x", 256)+"\n")
		if _, err := Load(context.Background(), fixture.request,
			WithUserConfigPath(fixture.userPath), WithMaxFileBytes(64)); err == nil || !errors.Is(err, pathx.ErrFileTooLarge) {
			t.Fatalf("oversized config = %v", err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		fixture := newConfigFixture(t)
		deep := filepath.Join(fixture.subdirRoot, "one", "two", "three")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		request := fixture.request
		request.InvocationDir = deep
		if _, err := Load(context.Background(), request,
			WithUserConfigPath(fixture.userPath), WithMaxDepth(2)); !errors.Is(err, ErrTooDeep) {
			t.Fatalf("excess config depth = %v", err)
		}
	})
}

func TestIdenticalCanonicalSourceAndSubdirFileIsAppliedOnce(t *testing.T) {
	root := canonicalTempDir(t)
	common := filepath.Join(root, ".git")
	if err := os.Mkdir(common, 0o755); err != nil {
		t.Fatal(err)
	}
	projectID := mustConfigProjectID(t, "01a05200-0000-7001-8000-000000000001")
	sourceID := mustConfigSourceID(t, "src_01a05200-0000-7002-8000-000000000002")
	writeConfigAt(t, root, "schema = \"exp.config/v1\"\n[ui]\ncolor = \"never\"\n")
	request := Request{
		ProjectID: projectID, CanonicalRoot: root, CanonicalGitCommonDir: common,
		Source:     &research.Source{Common: research.Common{ID: sourceID}, Subdir: "."},
		SourceRoot: root, SourceGitCommonDir: common, InvocationDir: root,
		UserConfigPath: filepath.Join(root, "missing", "config.toml"),
	}
	result, err := Load(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Layers) != 2 || result.Layers[1].Kind != LayerCanonical {
		t.Fatalf("duplicate file layers = %#v", result.Layers)
	}
}

type countingTrustSnapshotter struct {
	snapshots int
	checks    int
}

func (checker *countingTrustSnapshotter) Check(context.Context, trust.Subject, string, trust.Capability) (bool, error) {
	return false, errors.New("config load bypassed immutable trust snapshot")
}

func (checker *countingTrustSnapshotter) Snapshot(context.Context) (trust.Checker, error) {
	checker.snapshots++
	return trustCheckerFunc(func(context.Context, trust.Subject, string, trust.Capability) (bool, error) {
		checker.checks++
		return true, nil
	}), nil
}

type trustCheckerFunc func(context.Context, trust.Subject, string, trust.Capability) (bool, error)

func (check trustCheckerFunc) Check(ctx context.Context, subject trust.Subject, digest string, capability trust.Capability) (bool, error) {
	return check(ctx, subject, digest, capability)
}

func TestConfigLoadReadsOneTrustSnapshotForAllCapabilities(t *testing.T) {
	fixture := newConfigFixture(t)
	writeConfigAt(t, fixture.canonicalRoot, `
schema = "exp.config/v1"
[defaults]
source = "`+fixture.sourceID.String()+`"
agent_profile = "local-agent"
mlflow_profile = "tracking"
workspace_backend = "native_git"
[mlflow.profiles.tracking]
context = "local"
binary = "mlflow"
timeout = "30s"
default_metrics = []
`)
	checker := &countingTrustSnapshotter{}
	if _, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath), WithTrustChecker(checker)); err != nil {
		t.Fatal(err)
	}
	if checker.snapshots != 1 || checker.checks < 4 {
		t.Fatalf("trust snapshot reads=%d checks=%d", checker.snapshots, checker.checks)
	}
}

func TestRuntimeV2LegacyViewUsesExactTrust(t *testing.T) {
	fixture := newConfigFixture(t)
	runtimePath := filepath.Join(fixture.canonicalRoot, ".exp", "runtime.json")
	content := []byte(`{"schema_version":"exp.runtime/v2","pools":{},"plans":{}}`)
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	store := trust.NewStore(trust.WithPath(filepath.Join(fixture.root, "state", "runtime-trust.json")))
	if _, err := store.Approve(context.Background(), trust.Query{
		Subject:      trust.Subject{ProjectID: fixture.projectID, GitCommonDir: fixture.canonicalCommon, ConfigScope: ".exp/runtime.json"},
		ConfigDigest: digest, Capabilities: []trust.Capability{trust.CapabilityRuntimeDispatch},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := Load(context.Background(), fixture.request, WithUserConfigPath(fixture.userPath), WithTrustChecker(store))
	if err != nil {
		t.Fatal(err)
	}
	if result.Legacy.Runtime.Legacy || !result.Legacy.Runtime.Trusted || result.Legacy.Runtime.Source != "exp.runtime/v2 separate loader" {
		t.Fatalf("runtime v2 inspection = %#v", result.Legacy.Runtime)
	}
}

type configFixture struct {
	root            string
	canonicalRoot   string
	canonicalCommon string
	sourceRoot      string
	sourceCommon    string
	subdirRoot      string
	invocation      string
	userPath        string
	projectID       research.UUID
	sourceID        research.ID
	request         Request
}

func newConfigFixture(t *testing.T) configFixture {
	t.Helper()
	root := canonicalTempDir(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "user-config"))
	canonicalRoot := filepath.Join(root, "canonical")
	canonicalCommon := filepath.Join(canonicalRoot, ".git")
	sourceRoot := filepath.Join(root, "source")
	sourceCommon := filepath.Join(sourceRoot, ".git")
	subdirRoot := filepath.Join(sourceRoot, "services", "api")
	invocation := filepath.Join(subdirRoot, "pkg")
	for _, directory := range []string{canonicalCommon, sourceCommon, invocation} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projectID := mustConfigProjectID(t, "01a05000-0000-7001-8000-000000000001")
	sourceID := mustConfigSourceID(t, "src_01a05000-0000-7002-8000-000000000002")
	source := &research.Source{Common: research.Common{ID: sourceID}, Subdir: "services/api"}
	userPath := filepath.Join(root, "user-config", "exp", Filename)
	request := Request{
		ProjectID: projectID, CanonicalRoot: canonicalRoot, CanonicalGitCommonDir: canonicalCommon,
		Source: source, SourceRoot: sourceRoot, SourceGitCommonDir: sourceCommon,
		InvocationDir: invocation, UserConfigPath: userPath,
	}
	return configFixture{
		root: root, canonicalRoot: canonicalRoot, canonicalCommon: canonicalCommon,
		sourceRoot: sourceRoot, sourceCommon: sourceCommon, subdirRoot: subdirRoot,
		invocation: invocation, userPath: userPath, projectID: projectID, sourceID: sourceID,
		request: request,
	}
}

func writeConfigAt(t *testing.T, directory, content string) string {
	t.Helper()
	return writeExactConfig(t, filepath.Join(directory, filepath.FromSlash(RelativePath)), content)
}

func writeExactConfig(t *testing.T, filename, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(strings.TrimLeft(content, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return filename
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	value, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(value)
}

func mustConfigProjectID(t *testing.T, value string) research.UUID {
	t.Helper()
	id, err := research.ParseUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustConfigSourceID(t *testing.T, value string) research.ID {
	t.Helper()
	id, err := research.ParseIDForKind(value, research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
