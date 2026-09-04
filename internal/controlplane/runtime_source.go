package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/mlflow"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/sourcesnapshot"
	"github.com/daviddwlee84/exp-cli/internal/trust"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
)

var ErrRuntimeSourceUnavailable = errors.New("runtime Source is unavailable or changed")

type resolvedSourceRuntime struct {
	route                       validatedSourceRuntime
	source                      *research.Source
	repositoryRoot              string
	semanticRoot                string
	snapshot                    research.SourceSnapshot
	registeredGitCommonDir      string
	registeredGitCommonIdentity string
}

type resolvedPlanRuntimeV2 struct {
	contract          validatedPlanRuntimeV2
	sources           []resolvedSourceRuntime
	execution         int
	absoluteCWD       string
	outputRoot        string
	configDigest      string
	mlflowProfile     *mlflow.ResolvedProfile
	workspaceProvider workspacebackend.ProviderSelection
	preparations      []workspacebackend.PrepareRequest
}

func (resolved *resolvedPlanRuntimeV2) executionSource() *resolvedSourceRuntime {
	if resolved == nil || resolved.execution < 0 || resolved.execution >= len(resolved.sources) {
		return nil
	}
	return &resolved.sources[resolved.execution]
}

func (adapter Adapter) verifyRuntimeV2(ctx context.Context, inventory *record.Inventory, runtime *loadedRuntime) error {
	if runtime == nil || runtime.schema != RuntimeSchemaV2 {
		return nil
	}
	info, err := adapter.requireCanonicalWorkspace(ctx)
	if err != nil {
		return err
	}
	if err := adapter.verifyRuntimeConfigTrust(ctx, info, *runtime); err != nil {
		return err
	}
	needed := runtimePlansRequiringGitVerification(inventory)
	planIDs := make([]research.ID, 0, len(needed))
	for planID := range needed {
		configured := runtime.plans[planID]
		document, lookupErr := inventory.ByID(planID)
		if lookupErr == nil && document.Record.(*research.Plan).State == research.PlanQueued && configured.V2 != nil {
			planIDs = append(planIDs, planID)
		}
	}
	sort.Slice(planIDs, func(left, right int) bool { return planIDs[left].String() < planIDs[right].String() })
	for _, planID := range planIDs {
		plan := runtime.plans[planID]
		resolved, resolveErr := adapter.resolvePlanRuntimeV2(ctx, info, inventory, *plan.V2, plan.digest, runtime.configDigest, adapter.now(), research.ID{}, "", nil, false)
		if resolveErr != nil {
			return fmt.Errorf("runtime Plan %s: %w", planID, errors.Join(ErrRuntimeSourceUnavailable, resolveErr))
		}
		plan.resolvedV2 = &resolved
		runtime.plans[planID] = plan
	}
	return nil
}

func (adapter Adapter) verifyRuntimeConfigTrust(ctx context.Context, info *project.Info, runtime loadedRuntime) error {
	if runtime.schema != RuntimeSchemaV2 {
		return nil
	}
	if adapter.RuntimeTrust == nil {
		return errors.New("exp.runtime/v2 requires an exact-digest runtime trust checker")
	}
	if info == nil || info.Project() == nil {
		return errors.New("canonical Project information is unavailable")
	}
	trusted, err := adapter.RuntimeTrust.Check(ctx, trust.Subject{
		ProjectID: info.Project().ProjectID, GitCommonDir: info.Repository.GitCommonDir,
		ConfigScope: runtime.configPath,
	}, runtime.configDigest, trust.CapabilityRuntimeDispatch)
	if err != nil {
		return fmt.Errorf("inspect runtime config trust: %w", err)
	}
	if !trusted {
		return fmt.Errorf("runtime config %s at digest %s requires %s: %w", runtime.configPath, runtime.configDigest, trust.CapabilityRuntimeDispatch, trust.ErrUntrusted)
	}
	return nil
}

func (adapter Adapter) requireCanonicalWorkspace(ctx context.Context) (*project.Info, error) {
	if adapter.CanonicalWorkspace == nil || adapter.CanonicalWorkspace.Project() == nil {
		return nil, errors.New("exp.runtime/v2 requires explicit canonical project.Info")
	}
	expected := adapter.CanonicalWorkspace
	runner := adapter.Git
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	actual, err := project.DiscoverWithGit(ctx, expected.Repository.Root, runner)
	if err != nil {
		return nil, fmt.Errorf("re-discover canonical workspace: %w", err)
	}
	if actual.Project() == nil || actual.Project().ProjectID != expected.Project().ProjectID ||
		actual.Repository.Root != expected.Repository.Root || actual.Repository.GitCommonDir != expected.Repository.GitCommonDir || actual.Root != expected.Root {
		return nil, errors.New("canonical workspace identity changed")
	}
	if adapter.RepositoryRoot != "" {
		root, err := pathx.Canonical(adapter.RepositoryRoot)
		if err != nil {
			return nil, fmt.Errorf("canonicalize repository root: %w", err)
		}
		if root != actual.Repository.Root {
			return nil, errors.New("repository root disagrees with canonical workspace")
		}
	}
	return actual, nil
}

func (adapter Adapter) resolvePlanRuntimeV2(
	ctx context.Context,
	info *project.Info,
	inventory *record.Inventory,
	contract validatedPlanRuntimeV2,
	planDigest string,
	runtimeDigest string,
	capturedAt time.Time,
	owner research.ID,
	ownerTitle string,
	expected map[research.ID]research.SourceSnapshot,
	createManaged bool,
) (result resolvedPlanRuntimeV2, resultErr error) {
	preparations := make([]workspacebackend.PrepareRequest, 0)
	defer func() {
		if resultErr != nil && len(preparations) > 0 {
			resultErr = errors.Join(resultErr, adapter.rollbackManagedPreparations(context.WithoutCancel(ctx), preparations))
		}
	}()
	if info == nil || info.Project() == nil || inventory == nil {
		return resolvedPlanRuntimeV2{}, errors.New("canonical runtime context is incomplete")
	}
	if adapter.SourceResolver == nil || adapter.ConfigLoader == nil {
		return resolvedPlanRuntimeV2{}, errors.New("runtime Source resolver and config loader are required")
	}
	usesManagedWorkspace := contract.Execution.Checkout == CheckoutManagedWorktree
	for _, route := range contract.ReadOnly {
		usesManagedWorkspace = usesManagedWorkspace || route.Checkout == CheckoutManagedWorktree
	}
	if createManaged && usesManagedWorkspace {
		if _, ok := adapter.Workspace.(workspacebackend.PreparationRollbacker); !ok {
			return resolvedPlanRuntimeV2{}, errors.New("managed runtime workspace backend does not support exact preparation rollback")
		}
	}
	effective, err := adapter.loadExecutionWorkspaceConfig(ctx, info, inventory, contract)
	if err != nil {
		return resolvedPlanRuntimeV2{}, err
	}
	mlflowProfile, err := mlflow.ResolveProfile(effective, adapter.MLflowProfile)
	if err != nil {
		return resolvedPlanRuntimeV2{}, err
	}
	if mlflowProfile != nil && len(mlflowProfile.Environment) != 0 {
		return resolvedPlanRuntimeV2{}, errors.New("environment-bound MLflow profiles are unsupported for Pueue; use a workload-side credential broker")
	}
	providerSelection := workspacebackend.ProviderSelection{}
	if usesManagedWorkspace {
		providerSelection, err = workspacebackend.ResolveSelection(adapter.WorkspaceBackend, effective)
		if err != nil {
			return resolvedPlanRuntimeV2{}, err
		}
	}
	if capturedAt.IsZero() {
		capturedAt = adapter.now()
	}
	capturedAt = capturedAt.UTC()
	routes := make([]validatedSourceRuntime, 0, 1+len(contract.ReadOnly))
	routes = append(routes, contract.Execution)
	routes = append(routes, contract.ReadOnly...)
	resolvedSources := make([]resolvedSourceRuntime, 0, len(routes))
	for _, route := range routes {
		document, err := inventory.ByID(route.Source)
		if err != nil {
			return resolvedPlanRuntimeV2{}, fmt.Errorf("resolve canonical Source %s: %w", route.Source, err)
		}
		source, ok := document.Record.(*research.Source)
		if !ok || source.State != research.SourceActive {
			return resolvedPlanRuntimeV2{}, fmt.Errorf("Source %s is not an active canonical Git Source", route.Source)
		}
		var expectedSnapshot *research.SourceSnapshot
		if expected != nil {
			value, found := expected[route.Source]
			if !found {
				return resolvedPlanRuntimeV2{}, fmt.Errorf("canonical Attempt omits Source %s", route.Source)
			}
			expectedSnapshot = &value
			if err := snapshotMatchesRoute(value, route); err != nil {
				return resolvedPlanRuntimeV2{}, err
			}
		}
		resolved, err := adapter.resolveSourceRuntime(ctx, info, source, route, capturedAt, owner, ownerTitle, expectedSnapshot, createManaged, providerSelection, &preparations)
		if err != nil {
			return resolvedPlanRuntimeV2{}, fmt.Errorf("Source %s: %w", route.Source, err)
		}
		resolvedSources = append(resolvedSources, resolved)
	}
	if expected != nil && len(expected) != len(resolvedSources) {
		return resolvedPlanRuntimeV2{}, errors.New("canonical Attempt SourceSnapshots differ from the runtime Source set")
	}
	sort.Slice(resolvedSources, func(left, right int) bool {
		return resolvedSources[left].source.ID.String() < resolvedSources[right].source.ID.String()
	})
	executionIndex := -1
	for index := range resolvedSources {
		if resolvedSources[index].source.ID == contract.Execution.Source {
			executionIndex = index
			break
		}
	}
	if executionIndex < 0 {
		return resolvedPlanRuntimeV2{}, errors.New("execution Source was not resolved")
	}
	execution := &resolvedSources[executionIndex]
	absoluteCWD, err := pathx.ResolveUnderNoSymlinks(execution.semanticRoot, contract.CWD, true)
	if err != nil {
		return resolvedPlanRuntimeV2{}, fmt.Errorf("resolve execution Source cwd: %w", err)
	}
	infoAtCWD, err := os.Lstat(absoluteCWD)
	if err != nil {
		return resolvedPlanRuntimeV2{}, fmt.Errorf("inspect execution Source cwd: %w", err)
	}
	if infoAtCWD.Mode()&os.ModeSymlink != 0 || !infoAtCWD.IsDir() {
		return resolvedPlanRuntimeV2{}, errors.New("execution Source cwd is not a real directory")
	}
	combined := combinedRuntimeConfigDigest(runtimeDigest, planDigest, effective.Digest)
	return resolvedPlanRuntimeV2{
		contract: contract, sources: resolvedSources, execution: executionIndex,
		absoluteCWD: absoluteCWD, outputRoot: execution.semanticRoot, configDigest: combined,
		mlflowProfile: mlflowProfile, workspaceProvider: providerSelection,
		preparations: append([]workspacebackend.PrepareRequest{}, preparations...),
	}, nil
}

func (adapter Adapter) loadExecutionWorkspaceConfig(ctx context.Context, info *project.Info, inventory *record.Inventory, contract validatedPlanRuntimeV2) (*config.Result, error) {
	document, err := inventory.ByID(contract.Execution.Source)
	if err != nil {
		return nil, fmt.Errorf("resolve execution Source config: %w", err)
	}
	source, ok := document.Record.(*research.Source)
	if !ok || source.State != research.SourceActive {
		return nil, errors.New("execution Source is not an active canonical Git Source")
	}
	resolved, err := adapter.SourceResolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: info.Repository.Root, Workspace: info.Repository.Root,
		Source: source.ID.String(), SkipConfig: true,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve execution Source config context: %w", err)
	}
	if resolved.Project == nil || resolved.Project.Project() == nil || resolved.Project.Project().ProjectID != info.Project().ProjectID ||
		resolved.Project.Repository.Root != info.Repository.Root || resolved.Source == nil || !sameCanonicalSource(source, resolved.Source) ||
		resolved.Association.Source == nil || resolved.SourceRoot == "" {
		return nil, errors.New("execution Source config context differs from canonical identity")
	}
	semanticRoot, err := pathx.ResolveUnderNoSymlinks(resolved.SourceRoot, source.Subdir, true)
	if err != nil {
		return nil, fmt.Errorf("resolve execution Source config root: %w", err)
	}
	absoluteCWD, err := pathx.ResolveUnderNoSymlinks(semanticRoot, contract.CWD, true)
	if err != nil {
		return nil, fmt.Errorf("resolve execution Source config cwd: %w", err)
	}
	effective, err := adapter.ConfigLoader.Load(ctx, config.Request{
		Project: info, Source: source, SourceRoot: resolved.SourceRoot,
		SourceGitCommonDir: resolved.Association.Source.GitCommonDir, SourceSubdir: source.Subdir,
		InvocationDir: absoluteCWD, RuntimeConfigPath: adapter.ConfigPath,
	})
	if err != nil {
		return nil, fmt.Errorf("load execution Source config: %w", err)
	}
	if effective == nil || effective.Digest == "" {
		return nil, errors.New("execution Source config result is incomplete")
	}
	return effective, nil
}

func (adapter Adapter) resolveSourceRuntime(
	ctx context.Context,
	info *project.Info,
	source *research.Source,
	route validatedSourceRuntime,
	capturedAt time.Time,
	owner research.ID,
	ownerTitle string,
	expected *research.SourceSnapshot,
	createManaged bool,
	providerSelection workspacebackend.ProviderSelection,
	preparations *[]workspacebackend.PrepareRequest,
) (resolvedSourceRuntime, error) {
	resolved, err := adapter.SourceResolver.Resolve(ctx, workspace.ResolveRequest{
		InvocationDir: info.Repository.Root, Workspace: info.Repository.Root,
		Source: source.ID.String(), SkipConfig: true,
	})
	if err != nil {
		return resolvedSourceRuntime{}, err
	}
	if resolved.Project == nil || resolved.Project.Project() == nil || resolved.Project.Project().ProjectID != info.Project().ProjectID ||
		resolved.Project.Repository.Root != info.Repository.Root || resolved.Source == nil || resolved.Source.ID != source.ID ||
		resolved.Association.Source == nil || resolved.SourceRoot == "" {
		return resolvedSourceRuntime{}, errors.New("Source resolver returned a different Project or Source identity")
	}
	if !sameCanonicalSource(source, resolved.Source) {
		return resolvedSourceRuntime{}, errors.New("resolved Source record differs from canonical inventory")
	}
	association := *resolved.Association.Source
	checkoutRoot := resolved.SourceRoot
	if route.Checkout == CheckoutRegisteredWorktree {
		runner := adapter.Git
		if runner == nil {
			runner = gitx.ExecRunner{}
		}
		main, err := gitx.DiscoverWithRunner(ctx, resolved.SourceRoot, runner)
		if err != nil {
			return resolvedSourceRuntime{}, err
		}
		worktrees, err := gitx.Worktrees(ctx, main.Root, runner)
		if err != nil {
			return resolvedSourceRuntime{}, err
		}
		checkoutRoot, err = registeredWorktreeForHead(ctx, runner, main, worktrees, route.HeadCommit)
		if err != nil {
			return resolvedSourceRuntime{}, err
		}
	}

	capturer := adapter.SourceCapturer
	if capturer == nil {
		capturer = sourcesnapshot.Capturer{Git: adapter.Git}
	}
	capture := func(root string, snapshotAt time.Time) (research.SourceSnapshot, error) {
		observed, err := capturer.CaptureClean(ctx, sourcesnapshot.Request{
			Source: research.Clone(source).(*research.Source), RepositoryRoot: root,
			RegisteredGitCommonDir:      association.GitCommonDir,
			RegisteredGitCommonIdentity: association.GitCommonIdentity,
			BaseCommit:                  route.BaseCommit, ExpectedHead: route.HeadCommit,
			AllowNoChanges: route.ObservationalNoChange, CapturedAt: snapshotAt,
		})
		if err != nil {
			return research.SourceSnapshot{}, err
		}
		if !sameStrings(observed.ChangeSet, route.ChangeSet) {
			return research.SourceSnapshot{}, errors.New("captured base..head paths differ from runtime change_set")
		}
		return observed, nil
	}

	var snapshot research.SourceSnapshot
	if expected != nil && route.Checkout == CheckoutManagedWorktree {
		if adapter.Workspace == nil || owner.IsZero() {
			return resolvedSourceRuntime{}, errors.New("managed runtime workspace backend and owner are required")
		}
		request := managedRuntimeRequest(info, source, association, resolved.SourceRoot, route, owner, ownerTitle, providerSelection, *expected)
		inspection, err := adapter.Workspace.Inspect(ctx, request)
		if err != nil {
			return resolvedSourceRuntime{}, fmt.Errorf("inspect managed Source worktree: %w", err)
		}
		workspaceValue := inspection.Workspace
		checkoutRoot = workspaceValue.Root
		snapshot, err = capture(checkoutRoot, expected.CapturedAt)
		if err != nil || snapshot.Digest != expected.Digest {
			return resolvedSourceRuntime{}, fmt.Errorf("verify managed Source snapshot digest: %w", errors.Join(sourcesnapshot.ErrSourceChanged, err))
		}
	} else {
		snapshotAt := capturedAt
		if expected != nil {
			snapshotAt = expected.CapturedAt
		}
		snapshot, err = capture(checkoutRoot, snapshotAt)
		if err != nil {
			return resolvedSourceRuntime{}, err
		}
		if expected != nil && snapshot.Digest != expected.Digest {
			return resolvedSourceRuntime{}, sourcesnapshot.ErrSourceChanged
		}
		if expected == nil && route.Checkout == CheckoutManagedWorktree && !owner.IsZero() && createManaged {
			if adapter.Workspace == nil {
				return resolvedSourceRuntime{}, errors.New("managed runtime workspace backend is required")
			}
			request := managedRuntimeRequest(info, source, association, resolved.SourceRoot, route, owner, ownerTitle, providerSelection, snapshot)
			if preparations == nil {
				return resolvedSourceRuntime{}, errors.New("managed runtime preparation tracker is unavailable")
			}
			*preparations = append(*preparations, request)
			workspaceValue, err := adapter.Workspace.Prepare(ctx, request)
			if err != nil {
				return resolvedSourceRuntime{}, fmt.Errorf("prepare managed Source worktree: %w", err)
			}
			checkoutRoot = workspaceValue.Root
			verified, err := capture(checkoutRoot, snapshot.CapturedAt)
			if err != nil || verified.Digest != snapshot.Digest {
				return resolvedSourceRuntime{}, fmt.Errorf("verify prepared managed Source snapshot: %w", errors.Join(sourcesnapshot.ErrSourceChanged, err))
			}
		}
	}
	semanticRoot, err := pathx.ResolveUnderNoSymlinks(checkoutRoot, source.Subdir, true)
	if err != nil {
		return resolvedSourceRuntime{}, fmt.Errorf("resolve Source subdir in selected checkout: %w", err)
	}
	return resolvedSourceRuntime{
		route: route, source: research.Clone(source).(*research.Source),
		repositoryRoot: checkoutRoot, semanticRoot: semanticRoot, snapshot: snapshot,
		registeredGitCommonDir: association.GitCommonDir, registeredGitCommonIdentity: association.GitCommonIdentity,
	}, nil
}

func managedRuntimeRequest(info *project.Info, source *research.Source, association workspace.SourceAssociation, repositoryRoot string, route validatedSourceRuntime, owner research.ID, ownerTitle string, providerSelection workspacebackend.ProviderSelection, snapshot research.SourceSnapshot) workspacebackend.PrepareRequest {
	return workspacebackend.PrepareRequest{
		ProjectID: info.Project().ProjectID, Source: research.Clone(source).(*research.Source),
		RepositoryRoot: repositoryRoot, RegisteredGitCommonDir: association.GitCommonDir,
		RegisteredGitCommonIdentity: association.GitCommonIdentity,
		OwnerID:                     owner, OwnerTitle: ownerTitle, Snapshot: snapshot,
		AllowedPaths: []string{}, AllowedPathGlobs: []string{},
		CanonicalMetadataRoot: canonicalMetadataRootForRuntime(info, repositoryRoot),
		Provider:              providerSelection,
	}
}

func (adapter Adapter) rollbackManagedPreparations(ctx context.Context, requests []workspacebackend.PrepareRequest) error {
	if len(requests) == 0 {
		return nil
	}
	rollback, ok := adapter.Workspace.(workspacebackend.PreparationRollbacker)
	if !ok {
		return errors.New("managed runtime workspace backend cannot roll back preparation")
	}
	var failures []error
	for index := len(requests) - 1; index >= 0; index-- {
		if err := rollback.RollbackPreparation(ctx, requests[index]); err != nil {
			failures = append(failures, fmt.Errorf("roll back unpublished managed Source %s: %w", requests[index].Source.ID, err))
		}
	}
	return errors.Join(failures...)
}

func canonicalMetadataRootForRuntime(info *project.Info, sourceRoot string) string {
	if info == nil || info.Root == "" || sourceRoot == "" {
		return ""
	}
	inside, err := pathx.Contains(sourceRoot, info.Root)
	if err != nil || !inside {
		return ""
	}
	relative, err := filepath.Rel(sourceRoot, info.Root)
	if err != nil {
		return ""
	}
	relative = filepath.ToSlash(relative)
	if relative == "." || research.ValidateCommittedPath(relative, false) != nil {
		return ""
	}
	return relative
}

func snapshotMatchesRoute(snapshot research.SourceSnapshot, route validatedSourceRuntime) error {
	if snapshot.Source != route.Source || snapshot.State != research.SourceSnapshotClean ||
		snapshot.BaseCommit != route.BaseCommit || snapshot.HeadCommit != route.HeadCommit ||
		!sameStrings(snapshot.ChangeSet, route.ChangeSet) {
		return fmt.Errorf("SourceSnapshot %s does not match the runtime route", snapshot.Source)
	}
	if err := sourcesnapshot.Validate(snapshot); err != nil {
		return err
	}
	return nil
}

func sameCanonicalSource(left, right *research.Source) bool {
	if left == nil || right == nil {
		return false
	}
	return left.ID == right.ID && left.Schema == right.Schema && left.Key == right.Key && left.Kind == right.Kind &&
		left.Subdir == right.Subdir && left.State == right.State && sameStrings(left.LocatorHints, right.LocatorHints)
}

func combinedRuntimeConfigDigest(runtimeDigest, planDigest, effectiveDigest string) string {
	hash := sha256.New()
	for _, value := range []string{"exp.runtime.execution-config/v2", runtimeDigest, planDigest, effectiveDigest} {
		_, _ = hash.Write([]byte(fmt.Sprintf("%d:", len(value))))
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func snapshotMap(values []research.SourceSnapshot) (map[research.ID]research.SourceSnapshot, error) {
	result := make(map[research.ID]research.SourceSnapshot, len(values))
	for _, value := range values {
		if _, duplicate := result[value.Source]; duplicate {
			return nil, fmt.Errorf("SourceSnapshot %s occurs more than once", value.Source)
		}
		result[value.Source] = value
	}
	return result, nil
}

func resolvedSnapshots(value resolvedPlanRuntimeV2) []research.SourceSnapshot {
	result := make([]research.SourceSnapshot, len(value.sources))
	for index := range value.sources {
		result[index] = value.sources[index].snapshot
		result[index].ChangeSet = append([]string{}, value.sources[index].snapshot.ChangeSet...)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Source.String() < result[right].Source.String() })
	return result
}
