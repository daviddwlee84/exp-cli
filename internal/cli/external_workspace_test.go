package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/experimentgit"
	"github.com/daviddwlee84/exp-cli/internal/operation"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/daviddwlee84/exp-cli/internal/trust"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
)

type externalCLIFixture struct {
	base       string
	home       string
	canonical  string
	source     string
	sourcePath string
	app        *App
	projectID  string
	sourceID   string
	revision   string
}

func TestExternalWorkspaceCommandsResolveCanonicalAuthorityFromTwoRepositories(t *testing.T) {
	fixture := newExternalCLIFixture(t, "services/api", "https://example.com/org/source.git",
		"01a08000-0000-7001-8000-000000000001",
		"01a08000-0000-7002-8000-000000000002",
		"01a08000-0000-7003-8000-000000000003",
		"01a08000-0000-7004-8000-000000000004",
	)
	registeredWorkspace := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "workspace", "register", "--json")
	requireCommandSuccess(t, registeredWorkspace)
	var workspaceRegistration workspaceRegisterData
	decodeData(t, decodeEnvelope(t, registeredWorkspace.stdout), &workspaceRegistration)
	if workspaceRegistration.Association.ProjectID != fixture.projectID {
		t.Fatalf("workspace registration = %#v", workspaceRegistration)
	}
	added := addFixtureSource(t, fixture, "production")
	if strings.Contains(added.stdout, "redaction-canary") || strings.Contains(added.stdout, "unsupported=") {
		t.Fatalf("Source add leaked a rejected remote component: %s", added.stdout)
	}

	canonicalContext := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "context", "--json")
	requireCommandSuccess(t, canonicalContext)
	var canonicalData contextData
	decodeData(t, decodeEnvelope(t, canonicalContext.stdout), &canonicalData)
	if canonicalData.Project.ID != fixture.projectID || canonicalData.Source != nil || canonicalData.Workspace.Resolution != string(workspace.ResolutionCurrentProject) {
		t.Fatalf("canonical context = %#v", canonicalData)
	}

	sourceContext := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "context", "--json")
	requireCommandSuccess(t, sourceContext)
	var sourceData contextData
	decodeData(t, decodeEnvelope(t, sourceContext.stdout), &sourceData)
	if sourceData.Project.ID != fixture.projectID || sourceData.Project.RepositoryRoot != fixture.canonical || sourceData.Source == nil || sourceData.Source.Source.ID != fixture.sourceID || sourceData.Workspace.Resolution != string(workspace.ResolutionContainingSource) {
		t.Fatalf("Source context = %#v", sourceData)
	}
	if !sourceData.Association.ProjectRegistered || !sourceData.Association.SourceRegistered || len(sourceData.Config.Layers) == 0 || len(sourceData.Config.Provenance) == 0 {
		t.Fatalf("Source context omitted association/config summaries: %#v", sourceData)
	}

	explicit := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"--workspace", fixture.projectID, "--source", "production", "context", "--json")
	requireCommandSuccess(t, explicit)
	var explicitData contextData
	decodeData(t, decodeEnvelope(t, explicit.stdout), &explicitData)
	if explicitData.Workspace.Resolution != string(workspace.ResolutionExplicitSource) || explicitData.Source == nil || explicitData.Source.Source.ID != fixture.sourceID {
		t.Fatalf("explicit selector context = %#v", explicitData)
	}

	plan := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "plan", "add",
		"--title", "Cross repository plan", "--priority", "P1", "--effort", "S",
		"--payoff-summary", "Keep authority canonical", "--payoff-metric", "score", "--payoff-unit", "score", "--json")
	requireCommandSuccess(t, plan)
	var planData planAddData
	decodeData(t, decodeEnvelope(t, plan.stdout), &planData)
	if _, err := os.Stat(filepath.Join(fixture.canonical, filepath.FromSlash(planData.Plan.Path))); err != nil {
		t.Fatalf("canonical Plan missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.source, "experiments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Source repository received canonical records: %v", err)
	}

	paused := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath, "daemon", "pause", "--reason", "integration", "--json")
	requireCommandSuccess(t, paused)
	info, err := project.Discover(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	canonicalOperational, err := operation.PathFor(info.Repository.GitCommonDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(canonicalOperational); err != nil {
		t.Fatalf("canonical operation store missing: %v", err)
	}
	sourceRepository, err := projectRepository(t.Context(), fixture.source)
	if err != nil {
		t.Fatal(err)
	}
	sourceOperational, err := operation.PathFor(sourceRepository.GitCommonDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(sourceOperational); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("operation store opened in Source repository: %v", err)
	}

	projectUUID, err := research.ParseUUID(fixture.projectID)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := research.ParseIDForKind(fixture.sourceID, research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := fixture.app.Associations.RemoveSource(t.Context(), projectUUID, sourceID); err != nil || !removed {
		t.Fatalf("remove Source association = %t, %v", removed, err)
	}
	unregistered := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "status", "production", "--json")
	requireCommandSuccess(t, unregistered)
	var unregisteredData sourceStatusData
	decodeData(t, decodeEnvelope(t, unregistered.stdout), &unregisteredData)
	if len(unregisteredData.Sources) != 1 || unregisteredData.Sources[0].Registered || unregisteredData.Sources[0].Status != "unregistered" {
		t.Fatalf("unregistered Source status = %#v", unregisteredData)
	}
	repaired := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "register", "production", "--repo", fixture.source, "--json")
	requireCommandSuccess(t, repaired)

	for _, invocation := range []commandInvocation{
		invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "workspace", "status", "--json"),
		invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "list", "--json"),
		invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "show", fixture.sourceID, "--json"),
		invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "status", "production", "--json"),
	} {
		requireCommandSuccess(t, invocation)
	}
}

func TestWorkspaceSelectorsReportAmbiguityAndStaleSourceMappings(t *testing.T) {
	base, home := externalTestRoots(t)
	shared := initExternalGitRepository(t, filepath.Join(base, "shared"))
	if err := os.MkdirAll(filepath.Join(shared, "services", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	externalGit(t, shared, "remote", "add", "origin", "https://example.com/org/shared.git")

	canonicalA := initExternalGitRepository(t, filepath.Join(base, "canonical-a"))
	appA := deterministicApp(t, "01a08100-0000-7001-8000-000000000001", "01a08100-0000-7002-8000-000000000002", "01a08100-0000-7005-8000-000000000005")
	initA := invokeCommand(t, appA, "", "--start-dir", canonicalA, "init", "--name", "A", "--json")
	requireCommandSuccess(t, initA)
	var initAData initData
	decodeData(t, decodeEnvelope(t, initA.stdout), &initAData)
	requireCommandSuccess(t, invokeCommand(t, appA, "", "--start-dir", canonicalA, "source", "add", "--key", "production", "--repo", shared, "--subdir", "services/api", "--confirm", "--json"))

	canonicalB := initExternalGitRepository(t, filepath.Join(base, "canonical-b"))
	appB := deterministicApp(t, "01a08100-0000-7003-8000-000000000003", "01a08100-0000-7004-8000-000000000004", "01a08100-0000-7006-8000-000000000006")
	initB := invokeCommand(t, appB, "", "--start-dir", canonicalB, "init", "--name", "B", "--json")
	requireCommandSuccess(t, initB)
	requireCommandSuccess(t, invokeCommand(t, appB, "", "--start-dir", canonicalB, "source", "add", "--key", "production", "--repo", shared, "--subdir", "services/api", "--confirm", "--json"))

	ambiguous := invokeCommand(t, appA, "", "--start-dir", filepath.Join(shared, "services", "api"), "context", "--json")
	if !errors.Is(ambiguous.err, workspace.ErrAmbiguousResolution) {
		t.Fatalf("ambiguous context error = %v", ambiguous.err)
	}
	ambiguousEnvelope := decodeEnvelope(t, ambiguous.stdout)
	if ambiguousEnvelope.OK || ambiguousEnvelope.Diagnostics[0].Code != "workspace.ambiguous" {
		t.Fatalf("ambiguous envelope = %#v", ambiguousEnvelope)
	}

	explicit := invokeCommand(t, appA, "", "--start-dir", filepath.Join(shared, "services", "api"),
		"--workspace", initAData.Project.ID, "--source", "production", "context", "--json")
	requireCommandSuccess(t, explicit)
	var explicitData contextData
	decodeData(t, decodeEnvelope(t, explicit.stdout), &explicitData)
	if explicitData.Project.ID != initAData.Project.ID {
		t.Fatalf("explicit workspace selected %s", explicitData.Project.ID)
	}

	externalGit(t, shared, "remote", "set-url", "origin", "https://example.com/org/replaced.git")
	stale := invokeCommand(t, appA, "", "--start-dir", canonicalA, "--workspace", initAData.Project.ID, "source", "status", "production", "--json")
	requireCommandSuccess(t, stale)
	var staleData sourceStatusData
	staleEnvelope := decodeEnvelope(t, stale.stdout)
	decodeData(t, staleEnvelope, &staleData)
	if !staleEnvelope.Partial || len(staleData.Sources) != 1 || staleData.Sources[0].Status != "stale" || !strings.Contains(stale.stderr, "warning") {
		t.Fatalf("stale Source status = envelope %#v data %#v stderr %q", staleEnvelope, staleData, stale.stderr)
	}
	if strings.Contains(stale.stdout+stale.stderr, "replaced.git") {
		t.Fatalf("stale mapping leaked observed remote: stdout=%s stderr=%s", stale.stdout, stale.stderr)
	}
	_ = home
}

func TestSourceManagementCanUseExplicitSourceOutsideProjectTrees(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a08120-0000-7001-8000-000000000001",
		"01a08120-0000-7002-8000-000000000002",
		"01a08120-0000-7003-8000-000000000003",
		"01a08120-0000-7004-8000-000000000004",
	)
	added := addFixtureSource(t, fixture, "production")
	var addedData sourceAddData
	decodeData(t, decodeEnvelope(t, added.stdout), &addedData)
	outside := filepath.Join(fixture.base, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	shown := invokeCommand(t, fixture.app, "", "--start-dir", outside, "--source", "production", "source", "show", "--json")
	requireCommandSuccess(t, shown)
	var data sourceShowData
	decodeData(t, decodeEnvelope(t, shown.stdout), &data)
	if data.Project.ID != fixture.projectID || data.Source.ID != addedData.Source.ID {
		t.Fatalf("explicit outside Source management = %#v", data)
	}
}

func TestSourceStatusClassifiesMissingRegisteredCloneAsStale(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a08150-0000-7001-8000-000000000001",
		"01a08150-0000-7002-8000-000000000002",
		"01a08150-0000-7003-8000-000000000003",
		"01a08150-0000-7004-8000-000000000004",
	)
	addFixtureSource(t, fixture, "production")
	if err := os.Rename(fixture.source, fixture.source+"-moved"); err != nil {
		t.Fatal(err)
	}
	status := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "status", "production", "--json")
	requireCommandSuccess(t, status)
	var data sourceStatusData
	envelope := decodeEnvelope(t, status.stdout)
	decodeData(t, envelope, &data)
	if !envelope.Partial || len(data.Sources) != 1 || data.Sources[0].Status != "stale" || !data.Sources[0].Registered {
		t.Fatalf("missing clone status = envelope %#v data %#v", envelope, data)
	}
}

func TestSourceLocalOnlyConfirmationNoPromptAndLifecycle(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a08200-0000-7001-8000-000000000001",
		"01a08200-0000-7002-8000-000000000002",
		"01a08200-0000-7003-8000-000000000003",
		"01a08200-0000-7004-8000-000000000004",
	)
	reader := &countingEOFReader{}
	missingConfirm := invokeCommandWithReader(t, fixture.app, reader,
		"--start-dir", fixture.canonical, "source", "add", "--key", "local", "--repo", fixture.source, "--json")
	if missingConfirm.err == nil || reader.calls != 0 {
		t.Fatalf("noninteractive Source add error=%v reader calls=%d", missingConfirm.err, reader.calls)
	}
	if envelope := decodeEnvelope(t, missingConfirm.stdout); envelope.OK || strings.Count(missingConfirm.stdout, "\n") != 1 {
		t.Fatalf("missing-confirm envelope = %#v output=%q", envelope, missingConfirm.stdout)
	}

	withoutLocalConfirmation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical,
		"source", "add", "--key", "local", "--repo", fixture.source, "--confirm", "--json")
	if !errors.Is(withoutLocalConfirmation.err, sourcepkg.ErrLocalOnlyConfirmation) {
		t.Fatalf("local-only Source error = %v", withoutLocalConfirmation.err)
	}
	if envelope := decodeEnvelope(t, withoutLocalConfirmation.stdout); envelope.Diagnostics[0].Code != "source.local_only_confirmation_required" {
		t.Fatalf("local-only failure envelope = %#v", envelope)
	}
	assertCanonicalSourceCount(t, fixture.canonical, 0)

	added := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical,
		"source", "add", "--key", "local", "--repo", fixture.source, "--confirm", "--confirm-local-only", "--json")
	requireCommandSuccess(t, added)
	var addData sourceAddData
	decodeData(t, decodeEnvelope(t, added.stdout), &addData)
	if !addData.Plan.LocalOnly || addData.Plan.LocatorHints == nil || len(addData.Plan.LocatorHints) != 0 {
		t.Fatalf("local-only add plan = %#v", addData.Plan)
	}
	fromSourceRoot := invokeCommand(t, fixture.app, "", "--start-dir", fixture.source, "context", "--json")
	requireCommandSuccess(t, fromSourceRoot)
	var rootContext contextData
	decodeData(t, decodeEnvelope(t, fromSourceRoot.stdout), &rootContext)
	if rootContext.Source == nil || rootContext.Source.Source.ID != addData.Source.ID {
		t.Fatalf("Source-root context = %#v", rootContext)
	}

	staleRevision := "sha256:" + strings.Repeat("0", 64)
	stale := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "retire", "local", "--expected-revision", staleRevision, "--confirm", "--json")
	if !errors.Is(stale.err, record.ErrConflict) {
		t.Fatalf("stale Source retirement = %v", stale.err)
	}
	retired := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "retire", "local", "--expected-revision", addData.Source.Revision, "--confirm", "--json")
	requireCommandSuccess(t, retired)
	var retiredData sourceRetireData
	decodeData(t, decodeEnvelope(t, retired.stdout), &retiredData)
	if retiredData.Source.State != string(research.SourceRetired) || retiredData.Source.RetiredAt == "" {
		t.Fatalf("retired Source = %#v", retiredData.Source)
	}
	registerRetired := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "register", "local", "--repo", fixture.source, "--json")
	if !errors.Is(registerRetired.err, workspace.ErrRetiredSource) {
		t.Fatalf("register retired Source = %v", registerRetired.err)
	}
}

func TestSourceAppendLocatorRepairsStaleAssociationAndRejectsConflictingTargets(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source-a.git",
		"01a08250-0000-7001-8000-000000000001",
		"01a08250-0000-7002-8000-000000000002",
		"01a08250-0000-7003-8000-000000000003",
		"01a08250-0000-7004-8000-000000000004",
		"01a08250-0000-7005-8000-000000000005",
		"01a08250-0000-7006-8000-000000000006",
		"01a08250-0000-7007-8000-000000000007",
		"01a08250-0000-7008-8000-000000000008",
	)
	added := addFixtureSource(t, fixture, "production")
	var addedData sourceAddData
	decodeData(t, decodeEnvelope(t, added.stdout), &addedData)
	newLocator := "https://example.com/org/source-b.git"
	externalGit(t, fixture.source, "remote", "set-url", "origin", newLocator)
	stale := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "status", "production", "--json")
	requireCommandSuccess(t, stale)
	var staleData sourceStatusData
	decodeData(t, decodeEnvelope(t, stale.stdout), &staleData)
	if len(staleData.Sources) != 1 || staleData.Sources[0].Status != "stale" {
		t.Fatalf("relocated Source was not stale: %#v", staleData)
	}

	appended := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "append-locator", "production",
		"--locator", newLocator, "--expected-revision", addedData.Source.Revision, "--confirm", "--json")
	requireCommandSuccess(t, appended)
	var appendedData sourceAppendLocatorData
	decodeData(t, decodeEnvelope(t, appended.stdout), &appendedData)
	if appendedData.Source.Revision == addedData.Source.Revision || len(appendedData.Source.LocatorHints) != 2 || appendedData.Source.LocatorHints[1] != newLocator {
		t.Fatalf("appended Source locator = %#v", appendedData.Source)
	}
	requireCommandSuccess(t, invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "register", "production", "--repo", fixture.source, "--json"))
	ready := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "status", "production", "--json")
	requireCommandSuccess(t, ready)
	var readyData sourceStatusData
	decodeData(t, decodeEnvelope(t, ready.stdout), &readyData)
	if len(readyData.Sources) != 1 || readyData.Sources[0].Status != "ready" {
		t.Fatalf("repaired Source status = %#v", readyData)
	}

	staging := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "add", "--key", "staging", "--repo", fixture.source, "--confirm", "--json")
	requireCommandSuccess(t, staging)
	var stagingData sourceAddData
	decodeData(t, decodeEnvelope(t, staging.stdout), &stagingData)
	conflict := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "--source", "production", "source", "retire", "staging",
		"--expected-revision", stagingData.Source.Revision, "--confirm", "--json")
	if conflict.err == nil || !strings.Contains(conflict.err.Error(), "provide either") {
		t.Fatalf("conflicting Source targets = %v", conflict.err)
	}
	shown := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "show", "staging", "--json")
	requireCommandSuccess(t, shown)
	var shownData sourceShowData
	decodeData(t, decodeEnvelope(t, shown.stdout), &shownData)
	if shownData.Source.State != string(research.SourceActive) {
		t.Fatalf("conflicting target retired staging: %#v", shownData.Source)
	}
}

func TestSourceRetireReportsPreparedPublishedTransaction(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a08280-0000-7001-8000-000000000001",
		"01a08280-0000-7002-8000-000000000002",
		"01a08280-0000-7003-8000-000000000003",
		"01a08280-0000-7004-8000-000000000004",
		"01a08280-0000-7005-8000-000000000005",
	)
	added := addFixtureSource(t, fixture, "production")
	var addedData sourceAddData
	decodeData(t, decodeEnvelope(t, added.stdout), &addedData)
	injected := errors.New("interrupt Source retirement commit mark")
	fired := false
	fixture.app.NewTransactionalStore = func(info *project.Info) (TransactionalRecordStore, error) {
		return record.NewStore(info.Root, info.Repository.GitCommonDir,
			record.WithClock(fixture.app.clock), record.WithUUIDGenerator(fixture.app.GenerateUUID),
			record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
				if !fired && stage == record.StageTransactionCommitMark {
					fired = true
					return injected
				}
				return nil
			}),
		), nil
	}
	retired := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "source", "retire", "production",
		"--expected-revision", addedData.Source.Revision, "--confirm", "--json")
	if !errors.Is(retired.err, injected) || !fired {
		t.Fatalf("interrupted Source retirement = %v, fired=%t", retired.err, fired)
	}
	envelope := decodeEnvelope(t, retired.stdout)
	var data sourceRetireData
	decodeData(t, envelope, &data)
	if !envelope.Partial || data.Transaction == nil || !data.Transaction.RecoveryRequired || len(data.Transaction.Paths) != 1 || !data.Transaction.Paths[0].Published {
		t.Fatalf("Source retirement transaction envelope = %#v data=%#v", envelope, data)
	}
	store := record.NewStore(filepath.Join(fixture.canonical, "experiments"), filepath.Join(fixture.canonical, ".git"))
	if err := store.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	inventory, err := store.Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	document, err := inventory.ResolveSource("production")
	if err != nil || document.Record.(*research.Source).State != research.SourceRetired {
		t.Fatalf("recovered retirement = %#v, %v", document, err)
	}
}

func TestSourceAddReportsCanonicalPublicationWhenAssociationFails(t *testing.T) {
	const canary = "association-secret-canary"
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a08300-0000-7001-8000-000000000001",
		"01a08300-0000-7002-8000-000000000002",
		"01a08300-0000-7003-8000-000000000003",
	)
	realAssociations := fixture.app.Associations
	sentinel := errors.New("write association password=" + canary)
	fixture.app.Associations = &failingSourceAssociationStore{AssociationStore: realAssociations, failure: sentinel}

	invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical,
		"source", "add", "--key", "production", "--repo", fixture.source, "--confirm", "--json")
	if !errors.Is(invocation.err, sentinel) {
		t.Fatalf("association failure lost cause: %v", invocation.err)
	}
	envelope := decodeEnvelope(t, invocation.stdout)
	var data sourceAddData
	decodeData(t, envelope, &data)
	if envelope.OK || !envelope.Partial || !data.CanonicalPublished || data.Associated || data.Source.ID == "" {
		t.Fatalf("partial Source add = envelope %#v data %#v", envelope, data)
	}
	if strings.Contains(invocation.stdout+invocation.stderr+invocation.err.Error(), canary) {
		t.Fatalf("partial association failure leaked secret: stdout=%s stderr=%s error=%v", invocation.stdout, invocation.stderr, invocation.err)
	}
	assertCanonicalSourceCount(t, fixture.canonical, 1)
	projectID, err := research.ParseUUID(data.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := research.ParseIDForKind(data.Source.ID, research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := realAssociations.LookupSource(t.Context(), projectID, sourceID); !errors.Is(err, workspace.ErrSourceNotRegistered) {
		t.Fatalf("failed association was published: %v", err)
	}
}

func TestConfigExplainTrustRevokeAndListAreRedacted(t *testing.T) {
	const secret = "runtime-secret-value-canary"
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a08400-0000-7001-8000-000000000001",
	)
	t.Setenv("EXP_TEST_SECRET", secret)
	configPath := filepath.Join(fixture.canonical, ".exp-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `schema = "exp.config/v1"
[defaults]
agent_profile = "reviewer"
mlflow_profile = "local"
[mlflow.profiles.local]
context = "local"
binary = "mlflow"
timeout = "30s"
[mlflow.profiles.local.env.TOKEN]
from = "EXP_TEST_SECRET"
secret = true
required = true
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	explained := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "explain", "defaults.agent_profile", "--json")
	requireCommandSuccess(t, explained)
	explainEnvelope := decodeEnvelope(t, explained.stdout)
	var explainData configExplainData
	decodeData(t, explainEnvelope, &explainData)
	if !explainEnvelope.Partial || len(explainData.Provenance) != 1 || explainData.Provenance[0].Trusted || !strings.Contains(explained.stderr, "warning") {
		t.Fatalf("untrusted config explain = envelope %#v data %#v stderr %q", explainEnvelope, explainData, explained.stderr)
	}
	if strings.Contains(explained.stdout+explained.stderr, secret) {
		t.Fatalf("config explain leaked environment value: %s%s", explained.stdout, explained.stderr)
	}
	digest := explainData.Provenance[0].Digest

	wrongDigest := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "trust",
		"--path", configPath, "--digest", "sha256:"+strings.Repeat("0", 64), "--capability", "agent.profile", "--confirm", "--json")
	if wrongDigest.err == nil {
		t.Fatal("config trust accepted a stale digest")
	}
	trusted := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "trust",
		"--path", configPath, "--digest", digest, "--capability", "agent.profile", "--capability", "mlflow.profile", "--confirm", "--json")
	requireCommandSuccess(t, trusted)
	var trustData configTrustData
	decodeData(t, decodeEnvelope(t, trusted.stdout), &trustData)
	if trustData.Receipt.ConfigDigest != digest || len(trustData.Receipt.Capabilities) != 2 {
		t.Fatalf("trust receipt view = %#v", trustData)
	}

	listed := invokeCommand(t, fixture.app, "", "config", "list", "--json")
	requireCommandSuccess(t, listed)
	if strings.Contains(listed.stdout, "git_common_dir") || strings.Contains(listed.stdout, filepath.Join(fixture.canonical, ".git")) || strings.Contains(listed.stdout, secret) {
		t.Fatalf("trust listing leaked internal or secret details: %s", listed.stdout)
	}
	var listData configListData
	decodeData(t, decodeEnvelope(t, listed.stdout), &listData)
	if len(listData.Receipts) != 1 {
		t.Fatalf("trust receipts = %#v", listData.Receipts)
	}

	shown := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "show", "--json")
	requireCommandSuccess(t, shown)
	var showData configShowData
	decodeData(t, decodeEnvelope(t, shown.stdout), &showData)
	if !showData.Summary.Trusted || strings.Contains(shown.stdout, secret) || shown.stderr != "" {
		t.Fatalf("trusted config show = data %#v stdout %s stderr %q", showData, shown.stdout, shown.stderr)
	}

	paths := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "path", "--json")
	requireCommandSuccess(t, paths)
	var pathData configPathData
	decodeData(t, decodeEnvelope(t, paths.stdout), &pathData)
	found := false
	for _, entry := range pathData.Paths {
		if entry.Path == configPath && entry.Present {
			found = true
		}
	}
	if !found {
		t.Fatalf("config path omitted canonical layer: %#v", pathData.Paths)
	}

	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	revoked := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "revoke", "--path", configPath, "--confirm", "--json")
	requireCommandSuccess(t, revoked)
	var revokeData configRevokeData
	decodeData(t, decodeEnvelope(t, revoked.stdout), &revokeData)
	if revokeData.Changed != 1 {
		t.Fatalf("revocation result = %#v", revokeData)
	}
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	after := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "show", "--json")
	requireCommandSuccess(t, after)
	if envelope := decodeEnvelope(t, after.stdout); !envelope.Partial || !strings.Contains(after.stderr, "warning") {
		t.Fatalf("revoked config did not become untrusted: %#v stderr=%q", envelope, after.stderr)
	}
	for _, args := range [][]string{{"context", "--json"}, {"workspace", "status", "--json"}} {
		invocation := invokeCommand(t, fixture.app, "", append([]string{"--start-dir", fixture.canonical}, args...)...)
		requireCommandSuccess(t, invocation)
		if envelope := decodeEnvelope(t, invocation.stdout); !envelope.Partial || !strings.Contains(invocation.stderr, "warning") {
			t.Fatalf("%v did not report untrusted effective config as partial: %#v stderr=%q", args, envelope, invocation.stderr)
		}
	}
}

func TestEffectiveConfigTrustUsesWinningProvenance(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a08420-0000-7001-8000-000000000001",
		"01a08420-0000-7002-8000-000000000002",
		"01a08420-0000-7003-8000-000000000003",
		"01a08420-0000-7004-8000-000000000004",
	)
	addFixtureSource(t, fixture, "production")
	canonicalConfig := filepath.Join(fixture.canonical, ".exp-cli", "config.toml")
	sourceConfig := filepath.Join(fixture.source, ".exp-cli", "config.toml")
	for path, profile := range map[string]string{canonicalConfig: "canonical-profile", sourceConfig: "source-profile"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("schema = \"exp.config/v1\"\n[defaults]\nagent_profile = %q\n", profile)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	explained := invokeCommand(t, fixture.app, "", "--start-dir", fixture.source, "config", "explain", "defaults.agent_profile", "--json")
	requireCommandSuccess(t, explained)
	var explanation configExplainData
	decodeData(t, decodeEnvelope(t, explained.stdout), &explanation)
	if len(explanation.Provenance) != 1 || explanation.Provenance[0].Source != sourceConfig {
		t.Fatalf("winning Source provenance = %#v", explanation.Provenance)
	}
	trusted := invokeCommand(t, fixture.app, "", "--start-dir", fixture.source, "config", "trust",
		"--path", sourceConfig, "--digest", explanation.Provenance[0].Digest, "--capability", "agent.profile", "--confirm", "--json")
	requireCommandSuccess(t, trusted)

	shown := invokeCommand(t, fixture.app, "", "--start-dir", fixture.source, "config", "show", "--json")
	requireCommandSuccess(t, shown)
	var shownData configShowData
	shownEnvelope := decodeEnvelope(t, shown.stdout)
	decodeData(t, shownEnvelope, &shownData)
	if shownEnvelope.Partial || !shownData.Summary.Trusted || shownData.Effective.Defaults.AgentProfile != "source-profile" || shown.stderr != "" {
		t.Fatalf("trusted winning override = envelope %#v data %#v stderr=%q", shownEnvelope, shownData, shown.stderr)
	}
	foundHistoricalUntrusted := false
	for _, layer := range shownData.Summary.Layers {
		foundHistoricalUntrusted = foundHistoricalUntrusted || layer.Source == canonicalConfig && !layer.Trusted
	}
	if !foundHistoricalUntrusted {
		t.Fatalf("layer audit lost overridden untrusted canonical layer: %#v", shownData.Summary.Layers)
	}
	for _, args := range [][]string{{"context", "--json"}, {"workspace", "status", "--json"}} {
		invocation := invokeCommand(t, fixture.app, "", append([]string{"--start-dir", fixture.source}, args...)...)
		requireCommandSuccess(t, invocation)
		if envelope := decodeEnvelope(t, invocation.stdout); envelope.Partial || invocation.stderr != "" {
			t.Fatalf("%v disagreed with winning trusted provenance: %#v stderr=%q", args, envelope, invocation.stderr)
		}
	}
}

func TestLocalMutationFailuresReportPublishedPartialState(t *testing.T) {
	t.Run("workspace register", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "", "01a08430-0000-7001-8000-000000000001")
		associationPath, err := fixture.app.Associations.Path()
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("interrupt workspace association directory sync")
		fixture.app.Associations = workspace.NewStore(
			workspace.WithPath(associationPath), workspace.WithGitRunner(fixture.app.GitRunner),
			workspace.WithAtomicHook(func(stage record.AtomicStage, _ string) error {
				if stage == record.StageDirSync {
					return injected
				}
				return nil
			}),
		)
		invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "workspace", "register", "--json")
		if !errors.Is(invocation.err, injected) {
			t.Fatalf("workspace publication failure = %v", invocation.err)
		}
		envelope := decodeEnvelope(t, invocation.stdout)
		var data workspaceRegisterData
		decodeData(t, envelope, &data)
		if !envelope.Partial || data.Association.ProjectID != fixture.projectID || envelope.Diagnostics[0].Code != "publication.durability_uncertain" {
			t.Fatalf("workspace partial publication = envelope %#v data %#v", envelope, data)
		}
	})

	t.Run("trust and revoke", func(t *testing.T) {
		fixture := newExternalCLIFixture(t, ".", "", "01a08430-0000-7002-8000-000000000002")
		configPath := filepath.Join(fixture.canonical, ".exp-cli", "config.toml")
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, []byte("schema = \"exp.config/v1\"\n[defaults]\nagent_profile = \"reviewer\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		explained := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "explain", "defaults.agent_profile", "--json")
		requireCommandSuccess(t, explained)
		var explanation configExplainData
		decodeData(t, decodeEnvelope(t, explained.stdout), &explanation)
		trustPath, err := fixture.app.TrustStore.Path()
		if err != nil {
			t.Fatal(err)
		}
		approveFailure := errors.New("interrupt trust approval directory sync")
		fixture.app.TrustStore = trust.NewStore(trust.WithPath(trustPath), trust.WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage == record.StageDirSync {
				return approveFailure
			}
			return nil
		}))
		approved := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "trust", "--path", configPath,
			"--digest", explanation.Provenance[0].Digest, "--capability", "agent.profile", "--confirm", "--json")
		if !errors.Is(approved.err, approveFailure) {
			t.Fatalf("trust approval failure = %v", approved.err)
		}
		approvedEnvelope := decodeEnvelope(t, approved.stdout)
		var approvedData configTrustData
		decodeData(t, approvedEnvelope, &approvedData)
		if !approvedEnvelope.Partial || approvedData.Receipt.ProjectID != fixture.projectID || approvedEnvelope.Diagnostics[0].Code != "publication.durability_uncertain" {
			t.Fatalf("trust approval partial = envelope %#v data %#v", approvedEnvelope, approvedData)
		}

		revokeFailure := errors.New("interrupt trust revocation directory sync")
		fixture.app.TrustStore = trust.NewStore(trust.WithPath(trustPath), trust.WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage == record.StageDirSync {
				return revokeFailure
			}
			return nil
		}))
		revoked := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "revoke", "--path", configPath, "--confirm", "--json")
		if !errors.Is(revoked.err, revokeFailure) {
			t.Fatalf("trust revocation failure = %v", revoked.err)
		}
		revokedEnvelope := decodeEnvelope(t, revoked.stdout)
		var revokedData configRevokeData
		decodeData(t, revokedEnvelope, &revokedData)
		if !revokedEnvelope.Partial || revokedData.Changed != 1 || revokedEnvelope.Diagnostics[0].Code != "publication.durability_uncertain" {
			t.Fatalf("trust revocation partial = envelope %#v data %#v", revokedEnvelope, revokedData)
		}
	})
}

func TestConfigIdentityCannotRedirectResolvedWorkspace(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a08450-0000-7001-8000-000000000001",
	)
	configPath := filepath.Join(fixture.canonical, ".exp-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "schema = \"exp.config/v1\"\n[identity]\nproject = \"01a08450-0000-7002-8000-000000000002\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "context", "--json")
	if !errors.Is(invocation.err, config.ErrIdentityConflict) {
		t.Fatalf("config identity conflict = %v", invocation.err)
	}
	envelope := decodeEnvelope(t, invocation.stdout)
	if envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "config.identity_conflict" {
		t.Fatalf("config identity conflict envelope = %#v", envelope)
	}
}

func TestConfigAndSourceMutationsNeverPromptInJSONOrNonTTYMode(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a08500-0000-7001-8000-000000000001",
	)
	reader := &countingEOFReader{}
	trustInvocation := invokeCommandWithReader(t, fixture.app, reader, "--start-dir", fixture.canonical, "config", "trust", "--json")
	if trustInvocation.err == nil || reader.calls != 0 {
		t.Fatalf("config trust error=%v reader calls=%d", trustInvocation.err, reader.calls)
	}
	if envelope := decodeEnvelope(t, trustInvocation.stdout); envelope.OK || strings.Count(trustInvocation.stdout, "\n") != 1 {
		t.Fatalf("config trust failure output = %#v %q", envelope, trustInvocation.stdout)
	}
	humanReader := &countingEOFReader{}
	human := invokeCommandWithReader(t, fixture.app, humanReader, "--start-dir", fixture.canonical, "source", "add", "--key", "local", "--repo", fixture.source)
	if human.err == nil || humanReader.calls != 0 || human.stdout != "" {
		t.Fatalf("non-TTY human Source add error=%v reads=%d stdout=%q", human.err, humanReader.calls, human.stdout)
	}
}

func TestDedicatedInitAdoptsExistingGitRepoWithoutRemoteCommitOrSubmodule(t *testing.T) {
	base, _ := externalTestRoots(t)
	canonical := initExternalGitRepository(t, filepath.Join(base, "dedicated"))
	sourceRoot := initExternalGitRepository(t, filepath.Join(base, "source"))
	if err := os.MkdirAll(filepath.Join(sourceRoot, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	externalGit(t, sourceRoot, "remote", "add", "origin", "https://example.com/org/source.git")
	app := deterministicApp(t,
		"01a08600-0000-7001-8000-000000000001",
		"01a08600-0000-7002-8000-000000000002",
		"01a08600-0000-7003-8000-000000000003",
		"01a08600-0000-7004-8000-000000000004",
	)
	args := []string{
		"--start-dir", sourceRoot, "init", "--dedicated-repo", canonical, "--source-repo", sourceRoot,
		"--source-key", "production", "--source-subdir", "app", "--source-tag", "zeta", "--source-tag", "alpha", "--name", "Dedicated", "--confirm", "--json",
	}
	first := invokeCommand(t, app, "", args...)
	requireCommandSuccess(t, first)
	var firstData initData
	decodeData(t, decodeEnvelope(t, first.stdout), &firstData)
	if firstData.Mode != "dedicated" || !firstData.Created || !firstData.SourceCreated || !firstData.WorkspaceRegistered || !firstData.SourceAssociated || firstData.Plan == nil || firstData.Source == nil {
		t.Fatalf("dedicated init = %#v", firstData)
	}
	if got := strings.TrimSpace(externalGitOutput(t, canonical, "remote")); got != "" {
		t.Fatalf("dedicated init created remote %q", got)
	}
	if got := strings.TrimSpace(externalGitOutput(t, canonical, "rev-list", "--all")); got != "" {
		t.Fatalf("dedicated init created commit %q", got)
	}
	if _, err := os.Lstat(filepath.Join(canonical, ".gitmodules")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dedicated init created .gitmodules: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(sourceRoot, "experiments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dedicated init wrote canonical files to Source: %v", err)
	}
	if got, want := firstData.Source.Tags, []string{"alpha", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("dedicated Source tags = %#v, want %#v", got, want)
	}
	appended := invokeCommand(t, app, "", "--start-dir", canonical, "source", "append-locator", "production",
		"--locator", "https://mirror.example.com/org/source.git", "--expected-revision", firstData.Source.Revision, "--confirm", "--json")
	requireCommandSuccess(t, appended)

	second := invokeCommand(t, app, "", args...)
	requireCommandSuccess(t, second)
	var secondData initData
	decodeData(t, decodeEnvelope(t, second.stdout), &secondData)
	if secondData.Created || secondData.SourceCreated || !secondData.SourceAssociated || secondData.Source == nil || secondData.Source.ID != firstData.Source.ID {
		t.Fatalf("idempotent dedicated init = %#v", secondData)
	}
	resolved := invokeCommand(t, app, "", "--start-dir", filepath.Join(sourceRoot, "app"), "context", "--json")
	requireCommandSuccess(t, resolved)
	var contextValue contextData
	decodeData(t, decodeEnvelope(t, resolved.stdout), &contextValue)
	if contextValue.Project.ID != firstData.Project.ID || contextValue.Source == nil || contextValue.Source.Source.ID != firstData.Source.ID {
		t.Fatalf("dedicated Source context = %#v", contextValue)
	}

	absent := filepath.Join(base, "must-not-be-created")
	failed := invokeCommand(t, app, "", "--start-dir", sourceRoot, "init", "--dedicated-repo", absent, "--source-repo", sourceRoot, "--source-key", "other", "--confirm", "--json")
	if failed.err == nil {
		t.Fatal("dedicated init unexpectedly created a missing Git repo")
	}
	if _, err := os.Lstat(absent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing dedicated target was created: %v", err)
	}
	strayDedicatedFlag := invokeCommand(t, app, "", "--start-dir", sourceRoot, "init", "--source-subdir", "app", "--json")
	if strayDedicatedFlag.err == nil {
		t.Fatal("dedicated-only flag was silently ignored by embedded init")
	}
	if _, err := os.Lstat(filepath.Join(sourceRoot, "experiments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete dedicated flags initialized the Source repo: %v", err)
	}
}

func TestAppResolverUsesInjectedDiscoveryConfigAndAssociationBoundaries(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a08650-0000-7001-8000-000000000001",
	)
	discoverCalls := 0
	originalDiscover := fixture.app.DiscoverProject
	fixture.app.DiscoverProject = func(ctx context.Context, start string) (*project.Info, error) {
		discoverCalls++
		return originalDiscover(ctx, start)
	}
	configCalls := 0
	delegate := config.NewLoader(config.WithTrustChecker(fixture.app.TrustStore))
	fixture.app.ConfigLoader = configLoaderFunc(func(ctx context.Context, request config.Request) (*config.Result, error) {
		configCalls++
		return delegate.Load(ctx, request)
	})
	associations := &countingAssociationStore{AssociationStore: fixture.app.Associations}
	fixture.app.Associations = associations

	invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "context", "--json")
	requireCommandSuccess(t, invocation)
	if discoverCalls == 0 || configCalls == 0 || associations.lists == 0 {
		t.Fatalf("injected boundaries were bypassed: discover=%d config=%d associations=%d", discoverCalls, configCalls, associations.lists)
	}
}

func TestConfigPathIncludesInjectedLoaderAppliedPath(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a08680-0000-7001-8000-000000000001",
	)
	customPath := filepath.Join(fixture.canonical, "custom", "user-config.toml")
	fixture.app.ConfigLoader = configLoaderFunc(func(context.Context, config.Request) (*config.Result, error) {
		return &config.Result{
			Effective: config.Builtins(),
			Layers: []config.Layer{{Kind: config.LayerBuiltin, Source: "<built-in>", Trusted: true}, {
				Kind: config.LayerUser, Source: customPath, Digest: "sha256:" + strings.Repeat("1", 64), Trusted: true,
			}},
			Provenance: map[string]config.Provenance{},
		}, nil
	})
	invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "config", "path", "--json")
	requireCommandSuccess(t, invocation)
	var data configPathData
	decodeData(t, decodeEnvelope(t, invocation.stdout), &data)
	for _, entry := range data.Paths {
		if entry.Layer == config.LayerUser && entry.Path == customPath && entry.Present {
			return
		}
	}
	t.Fatalf("config path omitted injected applied layer: %#v", data.Paths)
}

func TestRecordTransactionReportsPreparedPublishedResult(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a08688-0000-7001-8000-000000000001",
		"01a08688-0000-7002-8000-000000000002",
	)
	poolID, err := research.ParseIDForKind("pool_01a08688-0000-7003-8000-000000000003", research.KindResourcePool)
	if err != nil {
		t.Fatal(err)
	}
	now := fixture.app.clock()
	document, err := record.Encode(&record.Document{Record: &research.ResourcePool{
		Common:  research.Common{Schema: research.SchemaResourcePool, ID: poolID, Title: "GPU", CreatedAt: now, UpdatedAt: now},
		Enabled: true, Capacity: 1, Unit: "gpu", Bottleneck: "gpu",
	}, Body: "# GPU\n"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(recordTransactionRequest{
		SchemaVersion: recordTransactionRequestSchema,
		Operation:     "pool.add",
		Changes:       []recordTransactionChangeRequest{{Operation: string(record.TransactionCreate), Document: string(document)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("interrupt record transaction commit mark")
	fired := false
	fixture.app.NewTransactionalStore = func(info *project.Info) (TransactionalRecordStore, error) {
		return record.NewStore(info.Root, info.Repository.GitCommonDir,
			record.WithClock(fixture.app.clock), record.WithUUIDGenerator(fixture.app.GenerateUUID),
			record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
				if !fired && stage == record.StageTransactionCommitMark {
					fired = true
					return injected
				}
				return nil
			}),
		), nil
	}
	invocation := invokeCommandWithReader(t, fixture.app, strings.NewReader(string(request)), "--start-dir", fixture.canonical, "record", "transaction", "--input", "-", "--json")
	if !errors.Is(invocation.err, injected) || !fired {
		t.Fatalf("record transaction failure = %v, fired=%t", invocation.err, fired)
	}
	envelope := decodeEnvelope(t, invocation.stdout)
	var data recordTransactionData
	decodeData(t, envelope, &data)
	if !envelope.Partial || !data.RecoveryRequired || data.State != record.TransactionPrepared || len(data.Paths) != 1 || !data.Paths[0].Published || envelope.Diagnostics[0].Code != "transaction.recovery_required" {
		t.Fatalf("record transaction partial = envelope %#v data %#v", envelope, data)
	}
}

func TestRecordRecoverBypassesMalformedOptionalConfig(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "", "01a08690-0000-7001-8000-000000000001")
	info, err := project.Discover(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	now := fixture.app.clock()
	sourceID, err := research.ParseIDForKind("src_01a08690-0000-7002-8000-000000000002", research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("leave recovery fixture prepared")
	store := record.NewStore(info.Root, info.Repository.GitCommonDir, record.WithTransactionHook(func(stage record.TransactionStage, _, _ string) error {
		if stage == record.StageTransactionCanonicalCreate {
			return injected
		}
		return nil
	}))
	result, err := store.Transact(t.Context(), record.TransactionRequest{
		Operation: "source.add",
		Changes: []record.TransactionChange{{Operation: record.TransactionCreate, Document: &record.Document{Record: &research.Source{
			Common: research.Common{Schema: research.SchemaSource, ID: sourceID, Title: "Recovery", CreatedAt: now, UpdatedAt: now},
			Key:    "recovery", Kind: research.SourceGit, Subdir: ".", LocatorHints: []string{}, State: research.SourceActive,
		}, Body: "# Recovery\n"}}},
	})
	if !errors.Is(err, injected) || result == nil {
		t.Fatalf("prepare recovery fixture = %#v, %v", result, err)
	}
	configPath := filepath.Join(fixture.canonical, ".exp-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("not valid config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recovered := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical, "record", "recover", "--json")
	requireCommandSuccess(t, recovered)
	inventory, err := record.NewStore(info.Root, info.Repository.GitCommonDir).Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inventory.ByID(sourceID); err != nil {
		t.Fatalf("record recover did not publish prepared record: %v", err)
	}
}

func TestLegacyEmbeddedInvocationAndStartDirRemainCompatible(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a08700-0000-7001-8000-000000000001",
	)
	associations, err := fixture.app.Associations.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(associations.Projects) != 0 || len(associations.Sources) != 0 {
		t.Fatalf("embedded init unexpectedly registered associations: %#v", associations)
	}
	start := filepath.Join(fixture.canonical, "experiments", record.PlansDir)
	for _, args := range [][]string{
		{"--start-dir", start, "validate", "--json"},
		{"--start-dir", start, "plan", "list", "--json"},
		{"--start-dir", start, "--workspace", fixture.canonical, "context", "--json"},
	} {
		requireCommandSuccess(t, invokeCommand(t, fixture.app, "", args...))
	}
	contextInvocation := invokeCommand(t, fixture.app, "", "--start-dir", start, "context", "--json")
	requireCommandSuccess(t, contextInvocation)
	var data contextData
	decodeData(t, decodeEnvelope(t, contextInvocation.stdout), &data)
	if data.Project.ID != fixture.projectID || data.Association.ProjectRegistered || data.Source != nil {
		t.Fatalf("legacy embedded context = %#v", data)
	}
	associationPath, err := fixture.app.Associations.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(associationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(associationPath, []byte("{malformed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"plan", "list", "--json"}, {"validate", "--json"}} {
		requireCommandSuccess(t, invokeCommand(t, fixture.app, "", append([]string{"--start-dir", start}, args...)...))
	}
}

func TestInitRejectsPersistentAuthoritySelectorsBeforeMutation(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "",
		"01a08710-0000-7001-8000-000000000001",
	)
	for name, selector := range map[string][]string{
		"workspace": {"--workspace", fixture.canonical},
		"source":    {"--source", "production"},
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"--start-dir", fixture.source}, selector...)
			args = append(args, "init", "--name", "Must not initialize", "--json")
			invocation := invokeCommand(t, fixture.app, "", args...)
			if invocation.err == nil {
				t.Fatalf("init accepted %s selector", name)
			}
			if _, err := os.Lstat(filepath.Join(fixture.source, "experiments")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected init mutated invocation repository: %v", err)
			}
		})
	}
}

func TestRegisteredSourceWithForeignProjectMarkerCannotMutateEitherProject(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a08718-0000-7001-8000-000000000001",
		"01a08718-0000-7002-8000-000000000002",
		"01a08718-0000-7003-8000-000000000003",
		"01a08718-0000-7004-8000-000000000004",
		"01a08718-0000-7005-8000-000000000005",
	)
	addFixtureSource(t, fixture, "production")
	requireCommandSuccess(t, invokeCommand(t, fixture.app, "", "--start-dir", fixture.source, "init", "--name", "Foreign", "--json"))
	mutation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.source, "plan", "add",
		"--title", "Must not publish", "--priority", "P1", "--effort", "S",
		"--payoff-summary", "Wrong authority", "--payoff-metric", "score", "--payoff-unit", "score", "--json")
	if !errors.Is(mutation.err, workspace.ErrAmbiguousResolution) {
		t.Fatalf("dual-role mutation authority = %v", mutation.err)
	}
	for _, root := range []string{fixture.canonical, fixture.source} {
		matches, err := filepath.Glob(filepath.Join(root, "experiments", record.PlansDir, "*.md"))
		if err != nil || len(matches) != 0 {
			t.Fatalf("ambiguous mutation wrote Plans under %s: %v, %v", root, matches, err)
		}
	}
}

func TestExperimentGitCommandsResolveImplicitExternalSource(t *testing.T) {
	fixture := newExternalCLIFixture(t, ".", "https://example.com/org/source.git",
		"01a08720-0000-7001-8000-000000000001",
		"01a08720-0000-7002-8000-000000000002",
		"01a08720-0000-7003-8000-000000000003",
		"01a08720-0000-7004-8000-000000000004",
	)
	addFixtureSource(t, fixture, "production")
	base := strings.Repeat("0", 40)
	implicit := invokeCommand(t, fixture.app, "", "--start-dir", fixture.source,
		"experiment", "workspace", "prepare", "E-00000000", "--base", base, "--json")
	if implicit.err == nil || errors.Is(implicit.err, ErrExternalSourceExecutionUnsupported) {
		t.Fatalf("implicit external experiment workspace did not reach canonical Experiment resolution: %v", implicit.err)
	}

	explicit := invokeCommand(t, fixture.app, "", "--start-dir", fixture.source, "--workspace", fixture.canonical,
		"experiment", "workspace", "prepare", "E-00000000", "--base", base, "--json")
	if explicit.err == nil || errors.Is(explicit.err, ErrExternalSourceExecutionUnsupported) {
		t.Fatalf("explicit canonical workspace should pass the Source execution guard: %v", explicit.err)
	}
}

func TestExperimentWorkspaceUsesSelectedSourceSubdirAndExactAllowlist(t *testing.T) {
	fixture := newExternalCLIFixture(t, "services/api", "https://example.com/org/source.git",
		"01a08730-0000-7001-8000-000000000001",
		"01a08730-0000-7002-8000-000000000002",
		"01a08730-0000-7003-8000-000000000003",
		"01a08730-0000-7004-8000-000000000004",
	)
	t.Setenv("XDG_DATA_HOME", filepath.Join(fixture.home, "data"))
	externalGit(t, fixture.source, "config", "user.name", "External Experiment")
	externalGit(t, fixture.source, "config", "user.email", "external@example.invalid")
	tracked := filepath.Join(fixture.source, "services", "api", "pkg", "main.txt")
	if err := os.WriteFile(tracked, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalGit(t, fixture.source, "add", "services/api/pkg/main.txt")
	externalGit(t, fixture.source, "commit", "--quiet", "-m", "source base")
	base := strings.TrimSpace(externalGitOutput(t, fixture.source, "rev-parse", "HEAD"))
	addFixtureSource(t, fixture, "production")

	info, err := fixture.app.DiscoverProject(t.Context(), fixture.canonical)
	if err != nil {
		t.Fatal(err)
	}
	store, err := fixture.app.NewTransactionalStore(info)
	if err != nil {
		t.Fatal(err)
	}
	experimentID, err := research.ParseIDForKind("exp_01a08730-0000-7105-8000-000000000005", research.KindExperiment)
	if err != nil {
		t.Fatal(err)
	}
	now := fixture.app.clock()
	design := research.Design{
		Question: "Does the selected Source change work?", Hypothesis: "It does", Kind: research.ExperimentSingleFactor,
		PrimaryFactor: "implementation", SecondaryFactors: []string{}, Baseline: "source base",
		ComparabilitySpec: "Use the same Source snapshot", SuccessCriteria: []string{"The exact file changes"},
		DecisionRule: "Review the commit", DesignLockedAt: &now,
	}
	design.DesignDigest, err = research.DesignDigest(design)
	if err != nil {
		t.Fatal(err)
	}
	experiment := &research.Experiment{
		Common:    research.Common{Schema: research.SchemaExperimentV2, ID: experimentID, Title: "External Source experiment", CreatedAt: now, UpdatedAt: now},
		Lifecycle: research.LifecycleActive, Design: design, Parents: []research.ID{}, CandidateInputs: []research.ID{},
	}
	if _, err := store.Transact(t.Context(), record.TransactionRequest{Operation: "fixture.external-experiment", Changes: []record.TransactionChange{{
		Operation: record.TransactionCreate, Document: &record.Document{Record: experiment, Body: "# External Source experiment\n"},
	}}}); err != nil {
		t.Fatal(err)
	}

	prepared := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"experiment", "workspace", "prepare", experimentID.String(), "--base", base, "--allow", "pkg/**", "--json")
	requireCommandSuccess(t, prepared)
	var workspaceData experimentgit.Workspace
	decodeData(t, decodeEnvelope(t, prepared.stdout), &workspaceData)
	if workspaceData.ProjectID.String() != fixture.projectID || workspaceData.SourceID.String() != fixture.sourceID ||
		workspaceData.SourceSubdir != "services/api" || workspaceData.CWD != filepath.Join(workspaceData.Worktree, "services", "api") ||
		!reflect.DeepEqual(workspaceData.AllowedGlobs, []string{"pkg/**"}) {
		t.Fatalf("Source-aware experiment workspace = %#v", workspaceData)
	}
	if err := os.WriteFile(filepath.Join(workspaceData.CWD, "pkg", "main.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed := invokeCommand(t, fixture.app, "", "--start-dir", fixture.sourcePath,
		"experiment", "workspace", "commit", experimentID.String(), "--base", base, "--allow", "pkg/**", "--json")
	requireCommandSuccess(t, committed)
	var changeSet experimentgit.ChangeSet
	decodeData(t, decodeEnvelope(t, committed.stdout), &changeSet)
	if !reflect.DeepEqual(changeSet.Paths, []string{"services/api/pkg/main.txt"}) || changeSet.BaseCommit != base || changeSet.HeadCommit == base {
		t.Fatalf("Source-aware change set = %#v", changeSet)
	}
	if sourceHead := strings.TrimSpace(externalGitOutput(t, fixture.source, "rev-parse", "HEAD")); sourceHead != base {
		t.Fatalf("managed workspace implicitly changed Source checkout HEAD: %s", sourceHead)
	}
	if _, err := os.Stat(workspaceData.Worktree); err != nil {
		t.Fatalf("managed worktree was implicitly removed: %v", err)
	}
}

type configLoaderFunc func(context.Context, config.Request) (*config.Result, error)

func (function configLoaderFunc) Load(ctx context.Context, request config.Request) (*config.Result, error) {
	return function(ctx, request)
}

type countingAssociationStore struct {
	AssociationStore
	lists int
}

func (store *countingAssociationStore) List(ctx context.Context) (workspace.Associations, error) {
	store.lists++
	return store.AssociationStore.List(ctx)
}

type failingSourceAssociationStore struct {
	AssociationStore
	failure error
}

func (store *failingSourceAssociationStore) RegisterSource(context.Context, research.UUID, research.ID, string) (workspace.SourceAssociation, error) {
	return workspace.SourceAssociation{}, store.failure
}

type countingEOFReader struct{ calls int }

func (reader *countingEOFReader) Read([]byte) (int, error) {
	reader.calls++
	return 0, io.EOF
}

func invokeCommandWithReader(t *testing.T, app *App, reader io.Reader, args ...string) commandInvocation {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app.In = reader
	app.Out = &stdout
	app.Err = &stderr
	root := NewRootCommand(app)
	root.SetArgs(args)
	err := root.ExecuteContext(app.Context)
	return commandInvocation{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func newExternalCLIFixture(t *testing.T, subdir, remote string, ids ...string) *externalCLIFixture {
	t.Helper()
	base, home := externalTestRoots(t)
	canonical := initExternalGitRepository(t, filepath.Join(base, "canonical"))
	sourceRoot := initExternalGitRepository(t, filepath.Join(base, "source"))
	sourcePath := sourceRoot
	if subdir != "." {
		sourcePath = filepath.Join(sourceRoot, filepath.FromSlash(subdir), "pkg")
		if err := os.MkdirAll(sourcePath, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if remote != "" {
		externalGit(t, sourceRoot, "remote", "add", "origin", remote)
	}
	app := deterministicApp(t, ids...)
	initialized := invokeCommand(t, app, "", "--start-dir", canonical, "init", "--name", "External CLI", "--json")
	requireCommandSuccess(t, initialized)
	var data initData
	decodeData(t, decodeEnvelope(t, initialized.stdout), &data)
	return &externalCLIFixture{
		base: base, home: home, canonical: canonical, source: sourceRoot, sourcePath: sourcePath,
		app: app, projectID: data.Project.ID,
	}
}

func addFixtureSource(t *testing.T, fixture *externalCLIFixture, key string) commandInvocation {
	t.Helper()
	subdir := "."
	if fixture.sourcePath != fixture.source {
		relative, err := filepath.Rel(fixture.source, filepath.Dir(fixture.sourcePath))
		if err != nil {
			t.Fatal(err)
		}
		subdir = filepath.ToSlash(relative)
	}
	invocation := invokeCommand(t, fixture.app, "", "--start-dir", fixture.canonical,
		"source", "add", "--key", key, "--repo", fixture.source, "--subdir", subdir, "--confirm", "--json")
	requireCommandSuccess(t, invocation)
	var data sourceAddData
	decodeData(t, decodeEnvelope(t, invocation.stdout), &data)
	fixture.sourceID = data.Source.ID
	fixture.revision = data.Source.Revision
	return invocation
}

func externalTestRoots(t *testing.T) (string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	return base, home
}

func initExternalGitRepository(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	externalGit(t, root, "init", "--quiet")
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(canonical)
}

func externalGit(t *testing.T, root string, args ...string) {
	t.Helper()
	_ = externalGitOutput(t, root, args...)
}

func externalGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, root, err, output)
	}
	return string(output)
}

func projectRepository(ctx context.Context, path string) (repositoryInfo, error) {
	info, err := project.Discover(ctx, path)
	if err == nil {
		return repositoryInfo{GitCommonDir: info.Repository.GitCommonDir}, nil
	}
	command := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	command.Dir = path
	output, commandErr := command.Output()
	if commandErr != nil {
		return repositoryInfo{}, commandErr
	}
	common := strings.TrimSpace(string(output))
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return repositoryInfo{}, err
	}
	return repositoryInfo{GitCommonDir: common}, nil
}

type repositoryInfo struct{ GitCommonDir string }

func assertCanonicalSourceCount(t *testing.T, canonical string, expected int) {
	t.Helper()
	inventory, err := record.LoadInventory(filepath.Join(canonical, "experiments"))
	if err != nil {
		t.Fatal(err)
	}
	if count := len(inventory.OfKind(research.KindSource)); count != expected {
		t.Fatalf("canonical Source count = %d, want %d", count, expected)
	}
}
