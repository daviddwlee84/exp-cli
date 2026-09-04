package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/google/uuid"
)

func TestAssociationRegistrationResolutionModesAndDirectProject(t *testing.T) {
	fixture := newWorkspaceFixture(t, "services/api", "https://github.com/example/source.git")
	registrationGit := &countingGitRunner{delegate: gitx.ExecRunner{}}
	store := NewStore(
		WithPath(fixture.associationPath),
		WithClock(func() time.Time { return fixture.observedAt }),
		WithGitRunner(registrationGit),
	)
	if _, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot); !errors.Is(err, ErrProjectNotRegistered) {
		t.Fatalf("Source registration before Project = %v", err)
	}
	projectAssociation, err := store.RegisterProject(context.Background(), fixture.project)
	if err != nil {
		t.Fatal(err)
	}
	sourceAssociation, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if projectAssociation.Root != fixture.project.Repository.Root || projectAssociation.GitCommonDir != fixture.project.Repository.GitCommonDir || !projectAssociation.ObservedAt.Equal(fixture.observedAt) {
		t.Fatalf("Project association = %#v", projectAssociation)
	}
	if sourceAssociation.Root != fixture.sourceRoot || sourceAssociation.GitCommonDir != filepath.Join(fixture.sourceRoot, ".git") || len(sourceAssociation.LocatorHints) != 1 || sourceAssociation.LocatorHints[0] != "https://github.com/example/source.git" {
		t.Fatalf("Source association = %#v", sourceAssociation)
	}
	if registrationGit.calls == 0 {
		t.Fatal("injected registration Git runner was not used")
	}
	assertWorkspaceMode(t, filepath.Dir(filepath.Dir(filepath.Dir(fixture.associationPath))), 0o700)
	assertWorkspaceMode(t, filepath.Dir(filepath.Dir(fixture.associationPath)), 0o700)
	assertWorkspaceMode(t, filepath.Dir(fixture.associationPath), 0o700)
	assertWorkspaceMode(t, filepath.Join(filepath.Dir(fixture.associationPath), "lock"), 0o600)
	assertWorkspaceMode(t, fixture.associationPath, 0o600)

	resolutionGit := &countingGitRunner{delegate: gitx.ExecRunner{}}
	resolver := NewResolver(store, WithoutConfig(), WithResolverGitRunner(resolutionGit), WithResolverClock(func() time.Time { return fixture.resolvedAt }))
	invocation := filepath.Join(fixture.sourceRoot, "services", "api", "pkg")
	contextValue, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: invocation})
	if err != nil {
		t.Fatal(err)
	}
	if contextValue.ProjectID() != fixture.projectID || contextValue.Source == nil || contextValue.Source.ID != fixture.source.ID || contextValue.SourceRoot != fixture.sourceRoot || contextValue.ResolvedRoot != filepath.Join(fixture.sourceRoot, "services", "api") || contextValue.SourceSubdir != "services/api" {
		t.Fatalf("containing context = %#v", contextValue)
	}
	if contextValue.Association.Kind != ResolutionContainingSource || !contextValue.Association.ValidatedAt.Equal(fixture.resolvedAt) || len(contextValue.Association.MatchedLocatorHints) != 1 {
		t.Fatalf("containing observation = %#v", contextValue.Association)
	}
	if resolutionGit.calls == 0 {
		t.Fatal("injected resolver Git runner was not used")
	}

	explicitSource, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: invocation, Source: fixture.source.Key})
	if err != nil || explicitSource.Association.Kind != ResolutionExplicitSource || explicitSource.Source.ID != fixture.source.ID {
		t.Fatalf("explicit Source context = %#v, %v", explicitSource, err)
	}
	explicitWorkspace, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.project.Repository.Root, Workspace: fixture.projectID.String()})
	if err != nil || explicitWorkspace.ProjectID() != fixture.projectID || explicitWorkspace.Source != nil || explicitWorkspace.Association.Kind != ResolutionExplicitWorkspace {
		t.Fatalf("explicit workspace context = %#v, %v", explicitWorkspace, err)
	}
	direct, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: filepath.Join(fixture.project.Repository.Root, "experiments")})
	if err != nil || direct.ProjectID() != fixture.projectID || direct.Source != nil || direct.Association.Kind != ResolutionCurrentProject {
		t.Fatalf("direct embedded context = %#v, %v", direct, err)
	}
}

func TestResolveSnapshotReusesExplicitSourceInventory(t *testing.T) {
	fixture := newWorkspaceFixture(t, "services/api", "https://github.com/example/source.git")
	store := NewStore(WithPath(fixture.associationPath))
	if _, err := store.RegisterProject(t.Context(), fixture.project); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterSource(t.Context(), fixture.projectID, fixture.source.ID, fixture.sourceRoot); err != nil {
		t.Fatal(err)
	}
	loads := 0
	var loaded *record.Inventory
	resolver := NewResolver(store, WithoutConfig(), WithInventoryLoader(func(ctx context.Context, info *project.Info) (*record.Inventory, error) {
		loads++
		var err error
		loaded, err = loadCanonicalInventory(ctx, info)
		return loaded, err
	}))
	resolved, inventory, err := resolver.ResolveSnapshot(t.Context(), ResolveRequest{
		InvocationDir: fixture.project.Repository.Root,
		Source:        fixture.source.Key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source == nil || resolved.Source.ID != fixture.source.ID {
		t.Fatalf("resolved Source = %#v", resolved.Source)
	}
	if loads != 1 {
		t.Fatalf("canonical inventory loaded %d times, want one shared load", loads)
	}
	if inventory != loaded {
		t.Fatal("ResolveSnapshot did not return the inventory used during Source resolution")
	}
}

func TestRepositoryConfigCannotRedirectCanonicalAuthority(t *testing.T) {
	fixture := newWorkspaceFixture(t, ".", "")
	store := NewStore(WithPath(fixture.associationPath))
	if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(fixture.project.Repository.Root, filepath.FromSlash(config.RelativePath))
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("schema = \"exp.config/v1\"\n[defaults]\nsource = \"production\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(store, WithConfigLoader(config.NewLoader(config.WithUserConfigPath(filepath.Join(fixture.root, "missing", "config.toml")))))
	resolved, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.project.Repository.Root})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProjectID() != fixture.projectID || resolved.Source != nil || resolved.Association.Kind != ResolutionCurrentProject {
		t.Fatalf("repo config redirected authority: %#v", resolved)
	}
	if resolved.Config == nil || resolved.Config.Effective.Defaults.Source != "production" {
		t.Fatalf("repo preference was not exposed separately: %#v", resolved.Config)
	}
}

func TestAssociationMoveRepairAndCanonicalStaleness(t *testing.T) {
	fixture := newWorkspaceFixture(t, "services/api", "")
	store := NewStore(WithPath(fixture.associationPath))
	if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(store, WithoutConfig())

	movedSource := filepath.Join(fixture.root, "source-moved")
	if err := os.Rename(fixture.sourceRoot, movedSource); err != nil {
		t.Fatal(err)
	}
	movedInvocation := filepath.Join(movedSource, "services", "api", "pkg")
	if _, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: movedInvocation}); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("unrepaired Source move = %v", err)
	}
	association, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, movedSource)
	if err != nil {
		t.Fatalf("repair Source registration: %v", err)
	}
	if association.Root != movedSource {
		t.Fatalf("repaired Source root = %q", association.Root)
	}
	if resolved, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: movedInvocation}); err != nil || resolved.SourceRoot != movedSource {
		t.Fatalf("resolve repaired Source = %#v, %v", resolved, err)
	}

	movedCanonical := filepath.Join(fixture.root, "canonical-moved")
	if err := os.Rename(fixture.project.Repository.Root, movedCanonical); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: movedInvocation}); !errors.Is(err, ErrStaleAssociation) {
		t.Fatalf("moved canonical workspace = %v", err)
	}
	projectAssociation, err := store.RegisterProjectPath(context.Background(), movedCanonical)
	if err != nil {
		t.Fatalf("repair canonical registration: %v", err)
	}
	if projectAssociation.Root != movedCanonical {
		t.Fatalf("repaired canonical root = %q", projectAssociation.Root)
	}
	if resolved, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: movedInvocation}); err != nil || resolved.ProjectID() != fixture.projectID {
		t.Fatalf("resolve after canonical repair = %#v, %v", resolved, err)
	}
}

func TestLongestContainingSourceAndDeterministicAmbiguity(t *testing.T) {
	root := canonicalWorkspaceTemp(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	sourceRoot := filepath.Join(root, "shared-source")
	initWorkspaceGit(t, sourceRoot)
	if err := os.MkdirAll(filepath.Join(sourceRoot, "services", "api", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewStore(WithPath(filepath.Join(root, "state", "exp", "associations", "v1.json")))
	firstProject, firstSource := createCanonicalBinding(t, filepath.Join(root, "project-a"), "01a06200-0000-7001-8000-000000000001", "01a06200-0000-7002-8000-000000000002", ".", "")
	secondProject, secondSource := createCanonicalBinding(t, filepath.Join(root, "project-b"), "01a06200-0000-7003-8000-000000000003", "01a06200-0000-7004-8000-000000000004", "services/api", "")
	for _, binding := range []struct {
		project *project.Info
		source  *research.Source
	}{
		{firstProject, firstSource}, {secondProject, secondSource},
	} {
		if _, err := store.RegisterProject(context.Background(), binding.project); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterSource(context.Background(), binding.project.Project().ProjectID, binding.source.ID, sourceRoot); err != nil {
			t.Fatal(err)
		}
	}
	resolver := NewResolver(store, WithoutConfig())
	invocation := filepath.Join(sourceRoot, "services", "api", "pkg")
	selected, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: invocation})
	if err != nil {
		t.Fatal(err)
	}
	if selected.ProjectID() != secondProject.Project().ProjectID || selected.Source.ID != secondSource.ID {
		t.Fatalf("longest Source selection = project %s Source %s", selected.ProjectID(), selected.Source.ID)
	}

	thirdProject, thirdSource := createCanonicalBinding(t, filepath.Join(root, "project-c"), "01a06200-0000-7005-8000-000000000005", "01a06200-0000-7006-8000-000000000006", "services/api", "")
	if _, err := store.RegisterProject(context.Background(), thirdProject); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterSource(context.Background(), thirdProject.Project().ProjectID, thirdSource.ID, sourceRoot); err != nil {
		t.Fatal(err)
	}
	_, firstErr := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: invocation})
	_, secondErr := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: invocation})
	if !errors.Is(firstErr, ErrAmbiguousResolution) || firstErr.Error() != secondErr.Error() {
		t.Fatalf("ambiguity errors = %v / %v", firstErr, secondErr)
	}
	var resolutionError *ResolutionError
	if !errors.As(firstErr, &resolutionError) || len(resolutionError.Candidates) != 2 || resolutionError.Candidates[0].ProjectID.String() > resolutionError.Candidates[1].ProjectID.String() {
		t.Fatalf("ambiguity diagnostics = %#v", resolutionError)
	}
}

func TestLocatorMismatchWrongGitIdentityAndLocalOnlySource(t *testing.T) {
	t.Run("newly appended locator may replace observed remote", func(t *testing.T) {
		fixture := newWorkspaceFixture(t, ".", "https://github.com/example/source.git")
		store := NewStore(WithPath(fixture.associationPath))
		if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
			t.Fatal(err)
		}
		association, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot)
		if err != nil {
			t.Fatal(err)
		}
		canonicalStore := record.NewStore(fixture.project.Root, fixture.project.Repository.GitCommonDir)
		inventory, err := canonicalStore.Inventory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		current, err := inventory.ByID(fixture.source.ID)
		if err != nil {
			t.Fatal(err)
		}
		service := sourcepkg.New(canonicalStore, sourcepkg.WithClock(func() time.Time {
			return time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
		}))
		appended, err := service.AppendLocator(context.Background(), sourcepkg.AppendLocatorRequest{
			Reference: fixture.source.ID.String(), ExpectedRevision: current.Revision,
			Locator: "https://mirror.example/example/source.git",
		})
		if err != nil {
			t.Fatal(err)
		}
		workspaceGit(t, fixture.sourceRoot, "remote", "set-url", "origin", "https://mirror.example/example/source.git")
		resolved, err := NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.sourceRoot})
		if err != nil {
			t.Fatalf("resolve through appended locator: %v", err)
		}
		if resolved.Source.ID != appended.Record.(*research.Source).ID || len(resolved.Association.MatchedLocatorHints) != 1 || resolved.Association.MatchedLocatorHints[0] != "https://mirror.example/example/source.git" {
			t.Fatalf("appended locator observation = %#v", resolved.Association)
		}
		if len(association.LocatorHints) != 1 || association.LocatorHints[0] != "https://github.com/example/source.git" {
			t.Fatalf("registration history changed unexpectedly: %#v", association)
		}
	})

	t.Run("locator mismatch", func(t *testing.T) {
		fixture := newWorkspaceFixture(t, ".", "https://github.com/example/source.git")
		store := NewStore(WithPath(fixture.associationPath))
		if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot); err != nil {
			t.Fatal(err)
		}
		workspaceGit(t, fixture.sourceRoot, "remote", "set-url", "origin", "https://github.com/example/other.git")
		_, err := NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.sourceRoot})
		if !errors.Is(err, ErrStaleAssociation) || !errors.Is(err, ErrLocatorMismatch) {
			t.Fatalf("locator mismatch = %v", err)
		}
		if strings.Contains(err.Error(), "other.git") {
			t.Fatalf("locator mismatch leaked an untrusted raw locator: %v", err)
		}
	})

	t.Run("wrong Git common identity", func(t *testing.T) {
		fixture := newWorkspaceFixture(t, ".", "")
		store := NewStore(WithPath(fixture.associationPath))
		if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
			t.Fatal(err)
		}
		association, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot)
		if err != nil {
			t.Fatal(err)
		}
		otherRepo := filepath.Join(fixture.root, "other")
		initWorkspaceGit(t, otherRepo)
		otherCommon := filepath.Join(otherRepo, ".git")
		data, err := os.ReadFile(fixture.associationPath)
		if err != nil {
			t.Fatal(err)
		}
		updated := strings.Replace(string(data), association.GitCommonDir, otherCommon, 1)
		if updated == string(data) {
			t.Fatal("failed to alter stored Git-common identity")
		}
		if err := os.WriteFile(fixture.associationPath, []byte(updated), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{
			InvocationDir: fixture.sourceRoot, Workspace: fixture.projectID.String(), Source: fixture.source.ID.String(),
		})
		if !errors.Is(err, ErrStaleAssociation) {
			t.Fatalf("wrong Git-common identity = %v", err)
		}
	})

	t.Run("local-only", func(t *testing.T) {
		fixture := newWorkspaceFixture(t, ".", "")
		store := NewStore(WithPath(fixture.associationPath))
		if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
			t.Fatal(err)
		}
		association, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot)
		if err != nil {
			t.Fatal(err)
		}
		if association.LocatorHints == nil || len(association.LocatorHints) != 0 {
			t.Fatalf("local-only observations = %#v", association.LocatorHints)
		}
		resolved, err := NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.sourceRoot})
		if err != nil || resolved.Source.ID != fixture.source.ID || len(resolved.Association.MatchedLocatorHints) != 0 {
			t.Fatalf("local-only resolution = %#v, %v", resolved, err)
		}
	})
}

func TestSourceSubdirSymlinkEscapeAndUnsanitizedStoredHintAreRejected(t *testing.T) {
	t.Run("subdir symlink", func(t *testing.T) {
		fixture := newWorkspaceFixture(t, "linked", "")
		linked := filepath.Join(fixture.sourceRoot, "linked")
		if err := os.RemoveAll(linked); err != nil {
			t.Fatal(err)
		}
		outside := canonicalWorkspaceTemp(t)
		if err := os.Symlink(outside, linked); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		store := NewStore(WithPath(fixture.associationPath))
		if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot); err == nil || !errors.Is(err, pathx.ErrSymlink) {
			t.Fatalf("symlink Source subdir registration = %v", err)
		}
	})

	t.Run("unsanitized stored locator", func(t *testing.T) {
		fixture := newWorkspaceFixture(t, ".", "https://github.com/example/source.git")
		store := NewStore(WithPath(fixture.associationPath))
		if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(fixture.associationPath)
		if err != nil {
			t.Fatal(err)
		}
		secret := "CANARY-SECRET"
		data = []byte(strings.Replace(string(data), "https://github.com/example/source.git", "https://alice:"+secret+"@github.com/example/source.git", 1))
		if err := os.WriteFile(fixture.associationPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = store.List(context.Background())
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("unsanitized association error = %v", err)
		}
	})
}

func TestAssociationAtomicPublicationIsRecoverableAndListIsPure(t *testing.T) {
	fixture := newWorkspaceFixture(t, ".", "")
	absentPath := filepath.Join(fixture.root, "absent", "exp", "associations", "v1.json")
	absent := NewStore(WithPath(absentPath))
	if snapshot, err := absent.List(context.Background()); err != nil || len(snapshot.Projects) != 0 || len(snapshot.Sources) != 0 {
		t.Fatalf("absent associations = %#v, %v", snapshot, err)
	}
	if _, err := os.Lstat(filepath.Dir(absentPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only List created state: %v", err)
	}

	sentinel := errors.New("injected association sync failure")
	fired := false
	store := NewStore(
		WithPath(fixture.associationPath),
		WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage == record.StageDirSync && !fired {
				fired = true
				return sentinel
			}
			return nil
		}),
	)
	if _, err := store.RegisterProject(context.Background(), fixture.project); !errors.Is(err, sentinel) || !fired {
		t.Fatalf("interrupted Project registration = %v, fired=%v", err, fired)
	}
	snapshot, err := NewStore(WithPath(fixture.associationPath)).List(context.Background())
	if err != nil || len(snapshot.Projects) != 1 || snapshot.Projects[0].ProjectID != fixture.projectID {
		t.Fatalf("published association recovery = %#v, %v", snapshot, err)
	}
}

func TestDirectProjectCompatibilityAndForeignSourceAuthority(t *testing.T) {
	t.Run("corrupt optional associations do not block direct Project", func(t *testing.T) {
		fixture := newWorkspaceFixture(t, ".", "")
		if err := os.MkdirAll(filepath.Dir(fixture.associationPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.associationPath, []byte("{not-json\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		resolved, err := NewResolver(NewStore(WithPath(fixture.associationPath)), WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.project.Repository.Root})
		if err != nil || resolved.ProjectID() != fixture.projectID || resolved.Association.Kind != ResolutionCurrentProject {
			t.Fatalf("direct Project with corrupt optional state = %#v, %v", resolved, err)
		}
	})

	t.Run("registered foreign Source and local Project are ambiguous", func(t *testing.T) {
		fixture := newWorkspaceFixture(t, ".", "https://example.com/org/source.git")
		store := NewStore(WithPath(fixture.associationPath))
		if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot); err != nil {
			t.Fatal(err)
		}
		_, _, err := project.Initialize(context.Background(), project.InitRequest{StartDir: fixture.sourceRoot, Name: "Foreign marker"},
			project.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
				return uuid.MustParse("01a06300-0000-7001-8000-000000000001"), nil
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		resolver := NewResolver(store, WithoutConfig())
		if _, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.sourceRoot}); !errors.Is(err, ErrAmbiguousResolution) {
			t.Fatalf("dual-role repository resolution = %v", err)
		}
		explicit, err := resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.sourceRoot, Workspace: fixture.projectID.String()})
		if err != nil || explicit.ProjectID() != fixture.projectID {
			t.Fatalf("explicit canonical authority = %#v, %v", explicit, err)
		}
	})
}

func TestContainingSourceFailsClosedForMoreSpecificStaleMapping(t *testing.T) {
	root := canonicalWorkspaceTemp(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	shared := filepath.Join(root, "shared")
	initWorkspaceGit(t, shared)
	if err := os.MkdirAll(filepath.Join(shared, "services", "api", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspaceGit(t, shared, "remote", "add", "origin", "https://example.com/org/source.git")
	store := NewStore(WithPath(filepath.Join(root, "state", "exp", "associations", "v1.json")))
	broadProject, broadSource := createCanonicalBinding(t, filepath.Join(root, "broad"), "01a06400-0000-7001-8000-000000000001", "01a06400-0000-7002-8000-000000000002", ".", "")
	specificProject, specificSource := createCanonicalBinding(t, filepath.Join(root, "specific"), "01a06400-0000-7003-8000-000000000003", "01a06400-0000-7004-8000-000000000004", "services/api", "https://example.com/org/source.git")
	for _, binding := range []struct {
		project *project.Info
		source  *research.Source
	}{{broadProject, broadSource}, {specificProject, specificSource}} {
		if _, err := store.RegisterProject(context.Background(), binding.project); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterSource(context.Background(), binding.project.Project().ProjectID, binding.source.ID, shared); err != nil {
			t.Fatal(err)
		}
	}
	workspaceGit(t, shared, "remote", "set-url", "origin", "https://example.com/org/replaced.git")
	_, err := NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: filepath.Join(shared, "services", "api", "pkg")})
	if !errors.Is(err, ErrStaleAssociation) || !errors.Is(err, ErrLocatorMismatch) {
		t.Fatalf("more-specific stale mapping fell back to broad mapping: %v", err)
	}
}

func TestContainingSourceIgnoresLessSpecificStaleProjectMapping(t *testing.T) {
	root := canonicalWorkspaceTemp(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	shared := filepath.Join(root, "shared")
	initWorkspaceGit(t, shared)
	if err := os.MkdirAll(filepath.Join(shared, "services", "api", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewStore(WithPath(filepath.Join(root, "state", "exp", "associations", "v1.json")))
	broadProject, broadSource := createCanonicalBinding(t, filepath.Join(root, "broad"), "01a06410-0000-7001-8000-000000000001", "01a06410-0000-7002-8000-000000000002", ".", "")
	specificProject, specificSource := createCanonicalBinding(t, filepath.Join(root, "specific"), "01a06410-0000-7003-8000-000000000003", "01a06410-0000-7004-8000-000000000004", "services/api", "")
	for _, binding := range []struct {
		project *project.Info
		source  *research.Source
	}{{broadProject, broadSource}, {specificProject, specificSource}} {
		if _, err := store.RegisterProject(context.Background(), binding.project); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterSource(context.Background(), binding.project.Project().ProjectID, binding.source.ID, shared); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(broadProject.Repository.Root, broadProject.Repository.Root+"-moved"); err != nil {
		t.Fatal(err)
	}
	resolved, err := NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: filepath.Join(shared, "services", "api", "pkg")})
	if err != nil || resolved.ProjectID() != specificProject.Project().ProjectID || resolved.Source == nil || resolved.Source.ID != specificSource.ID {
		t.Fatalf("nested healthy Source was shadowed by broad stale mapping: %#v, %v", resolved, err)
	}
}

func TestExplicitExternalSourceUsesSemanticRootForConfigScope(t *testing.T) {
	fixture := newWorkspaceFixture(t, "services/api", "")
	store := NewStore(WithPath(fixture.associationPath))
	if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(fixture.sourceRoot), "unrelated")
	if err := os.Mkdir(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(store)
	for name, invocation := range map[string]string{"canonical": fixture.project.Repository.Root, "unrelated": unrelated} {
		t.Run(name, func(t *testing.T) {
			resolved, err := resolver.Resolve(context.Background(), ResolveRequest{
				InvocationDir: invocation, Workspace: fixture.projectID.String(), Source: fixture.source.ID.String(),
			})
			if err != nil || resolved == nil || resolved.Config == nil || resolved.InvocationDir != invocation || resolved.Source == nil || resolved.Source.ID != fixture.source.ID {
				t.Fatalf("external Source config scope = %#v, %v", resolved, err)
			}
		})
	}
}

func TestExplicitCrossProjectSourcePrefersActiveOverRetired(t *testing.T) {
	root := canonicalWorkspaceTemp(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	clone := filepath.Join(root, "clone")
	initWorkspaceGit(t, clone)
	invocation := filepath.Join(root, "outside")
	if err := os.Mkdir(invocation, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewStore(WithPath(filepath.Join(root, "state", "exp", "associations", "v1.json")))
	retiredProject, retiredSource := createCanonicalBinding(t, filepath.Join(root, "retired"), "01a06500-0000-7001-8000-000000000001", "01a06500-0000-7002-8000-000000000002", ".", "")
	activeProject, activeSource := createCanonicalBinding(t, filepath.Join(root, "active"), "01a06500-0000-7003-8000-000000000003", "01a06500-0000-7004-8000-000000000004", ".", "")
	for _, binding := range []struct {
		project *project.Info
		source  *research.Source
	}{{retiredProject, retiredSource}, {activeProject, activeSource}} {
		if _, err := store.RegisterProject(context.Background(), binding.project); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterSource(context.Background(), binding.project.Project().ProjectID, binding.source.ID, clone); err != nil {
			t.Fatal(err)
		}
	}
	retiredStore := record.NewStore(retiredProject.Root, retiredProject.Repository.GitCommonDir)
	inventory, err := retiredStore.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current, err := inventory.ByID(retiredSource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourcepkg.New(retiredStore).Retire(context.Background(), sourcepkg.RetireRequest{Reference: retiredSource.ID.String(), ExpectedRevision: current.Revision}); err != nil {
		t.Fatal(err)
	}
	resolved, err := NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: invocation, Source: "production"})
	if err != nil || resolved.ProjectID() != activeProject.Project().ProjectID || resolved.Source.ID != activeSource.ID {
		t.Fatalf("active/retired cross-project selection = %#v, %v", resolved, err)
	}
}

func TestExplicitCrossProjectSourcePreservesPerProjectAmbiguity(t *testing.T) {
	root := canonicalWorkspaceTemp(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	clone := filepath.Join(root, "clone")
	initWorkspaceGit(t, clone)
	invocation := filepath.Join(root, "outside")
	if err := os.Mkdir(invocation, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewStore(WithPath(filepath.Join(root, "state", "exp", "associations", "v1.json")))
	ambiguousProject, first := createCanonicalBinding(t, filepath.Join(root, "ambiguous"), "01a06810-0000-7001-8000-000000000001", "01a06800-0000-7001-8000-000000000001", ".", "")
	secondDocument, err := sourcepkg.New(record.NewStore(ambiguousProject.Root, ambiguousProject.Repository.GitCommonDir),
		sourcepkg.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a06800-0000-7002-8000-000000000002"), nil
		}),
	).Add(context.Background(), sourcepkg.AddRequest{Key: "staging", Title: "Staging", Subdir: ".", LocatorHints: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	second := secondDocument.Record.(*research.Source)
	uniqueProject, unique := createCanonicalBinding(t, filepath.Join(root, "unique"), "01a06810-0000-7003-8000-000000000003", "01a06800-0000-7004-8000-000000000004", ".", "")
	for _, projectValue := range []*project.Info{ambiguousProject, uniqueProject} {
		if _, err := store.RegisterProject(context.Background(), projectValue); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []struct {
		projectID research.UUID
		sourceID  research.ID
	}{{ambiguousProject.Project().ProjectID, first.ID}, {ambiguousProject.Project().ProjectID, second.ID}, {uniqueProject.Project().ProjectID, unique.ID}} {
		if _, err := store.RegisterSource(context.Background(), binding.projectID, binding.sourceID, clone); err != nil {
			t.Fatal(err)
		}
	}
	_, err = NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: invocation, Source: "src_01a06800"})
	if !errors.Is(err, ErrAmbiguousResolution) {
		t.Fatalf("cross-project selector ignored within-project ambiguity: %v", err)
	}
}

func TestSourceResolutionRejectsPreparedCanonicalTransaction(t *testing.T) {
	fixture := newWorkspaceFixture(t, ".", "")
	injected := errors.New("leave Source transaction prepared")
	now := time.Date(2026, 9, 3, 17, 0, 0, 0, time.UTC)
	sourceID, err := research.NewID(research.KindSource, uuid.MustParse("01a06700-0000-7001-8000-000000000001"))
	if err != nil {
		t.Fatal(err)
	}
	store := record.NewStore(fixture.project.Root, fixture.project.Repository.GitCommonDir,
		record.WithClock(func() time.Time { return now }),
		record.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) {
			return uuid.MustParse("01a06700-0000-7002-8000-000000000002"), nil
		}),
		record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
			if stage == record.StageTransactionCanonicalCreate {
				return injected
			}
			return nil
		}),
	)
	result, err := store.Transact(context.Background(), record.TransactionRequest{
		Operation: "source.add",
		Changes: []record.TransactionChange{{Operation: record.TransactionCreate, Document: &record.Document{Record: &research.Source{
			Common: research.Common{Schema: research.SchemaSource, ID: sourceID, Title: "Prepared", CreatedAt: now, UpdatedAt: now},
			Key:    "prepared", Kind: research.SourceGit, Subdir: ".", LocatorHints: []string{}, State: research.SourceActive,
		}, Body: "# Prepared\n"}}},
	})
	if !errors.Is(err, injected) || result == nil {
		t.Fatalf("prepare Source transaction = %#v, %v", result, err)
	}
	resolver := NewResolver(NewStore(WithPath(fixture.associationPath)), WithoutConfig())
	_, err = resolver.Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.project.Repository.Root, Source: "prepared"})
	if !errors.Is(err, record.ErrTransactionRecoveryRequired) {
		t.Fatalf("prepared Source transaction was treated as committed inventory: %v", err)
	}
}

func TestSourceAssociationRejectsRepositoryReplacementAtSamePath(t *testing.T) {
	fixture := newWorkspaceFixture(t, ".", "https://example.com/org/source.git")
	store := NewStore(WithPath(fixture.associationPath))
	if _, err := store.RegisterProject(context.Background(), fixture.project); err != nil {
		t.Fatal(err)
	}
	association, err := store.RegisterSource(context.Background(), fixture.projectID, fixture.source.ID, fixture.sourceRoot)
	if err != nil || association.GitCommonIdentity == "" {
		t.Fatalf("registered identity = %#v, %v", association, err)
	}
	original := fixture.sourceRoot + "-original"
	if err := os.Rename(fixture.sourceRoot, original); err != nil {
		t.Fatal(err)
	}
	initWorkspaceGit(t, fixture.sourceRoot)
	workspaceGit(t, fixture.sourceRoot, "remote", "add", "origin", "https://example.com/org/source.git")
	_, err = NewResolver(store, WithoutConfig()).Resolve(context.Background(), ResolveRequest{InvocationDir: fixture.sourceRoot})
	if !errors.Is(err, ErrStaleAssociation) {
		t.Fatalf("same-path repository replacement resolution = %v", err)
	}
}

type countingGitRunner struct {
	delegate gitx.Runner
	calls    int
}

func (runner *countingGitRunner) Run(ctx context.Context, directory string, arguments []string) (string, string, error) {
	runner.calls++
	return runner.delegate.Run(ctx, directory, arguments)
}

type workspaceFixture struct {
	root            string
	project         *project.Info
	projectID       research.UUID
	source          *research.Source
	sourceRoot      string
	associationPath string
	observedAt      time.Time
	resolvedAt      time.Time
}

func newWorkspaceFixture(t *testing.T, subdir, locator string) workspaceFixture {
	t.Helper()
	root := canonicalWorkspaceTemp(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	sourceRoot := filepath.Join(root, "source")
	initWorkspaceGit(t, sourceRoot)
	if subdir != "." {
		if err := os.MkdirAll(filepath.Join(sourceRoot, filepath.FromSlash(subdir), "pkg"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if locator != "" {
		workspaceGit(t, sourceRoot, "remote", "add", "origin", locator)
	}
	info, source := createCanonicalBinding(t, filepath.Join(root, "canonical"), "01a06000-0000-7001-8000-000000000001", "01a06000-0000-7002-8000-000000000002", subdir, locator)
	return workspaceFixture{
		root: root, project: info, projectID: info.Project().ProjectID, source: source,
		sourceRoot: sourceRoot, associationPath: filepath.Join(root, "state", "exp", "associations", "v1.json"),
		observedAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		resolvedAt: time.Date(2026, 9, 3, 12, 1, 0, 0, time.UTC),
	}
}

func createCanonicalBinding(t *testing.T, repositoryRoot, projectUUID, sourceUUID, subdir, locator string) (*project.Info, *research.Source) {
	t.Helper()
	initWorkspaceGit(t, repositoryRoot)
	projectValue := uuid.MustParse(projectUUID)
	info, _, err := project.Initialize(context.Background(), project.InitRequest{StartDir: repositoryRoot, Name: "Workspace Test"},
		project.WithClock(func() time.Time { return time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC) }),
		project.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return projectValue, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	locators := []string{}
	if locator != "" {
		locators = []string{locator}
	}
	service := sourcepkg.New(record.NewStore(info.Root, info.Repository.GitCommonDir),
		sourcepkg.WithClock(func() time.Time { return time.Date(2026, 9, 3, 9, 1, 0, 0, time.UTC) }),
		sourcepkg.WithUUIDGenerator(func(time.Time) (uuid.UUID, error) { return uuid.MustParse(sourceUUID), nil }),
	)
	document, err := service.Add(context.Background(), sourcepkg.AddRequest{
		Key: "production", Title: "Production", Subdir: subdir, LocatorHints: locators,
	})
	if err != nil {
		t.Fatal(err)
	}
	return info, research.Clone(document.Record.(*research.Source)).(*research.Source)
}

func initWorkspaceGit(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	workspaceGit(t, root, "init", "--quiet")
}

func workspaceGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", arguments, root, err, output)
	}
}

func canonicalWorkspaceTemp(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(root)
}

func assertWorkspaceMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("stat %s: %v", path, err)
		return
	}
	if info.Mode().Perm() != mode {
		t.Errorf("%s mode = %04o, want %04o", path, info.Mode().Perm(), mode)
	}
}
