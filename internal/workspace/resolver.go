package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

var (
	ErrAmbiguousResolution = errors.New("workspace resolution is ambiguous")
	ErrNotRegistered       = errors.New("no registered workspace contains the invocation directory")
)

// ResolutionKind records which precedence rule selected the context.
type ResolutionKind string

const (
	ResolutionExplicitWorkspace ResolutionKind = "explicit_workspace"
	ResolutionExplicitSource    ResolutionKind = "explicit_source"
	ResolutionCurrentProject    ResolutionKind = "current_canonical_project"
	ResolutionContainingSource  ResolutionKind = "containing_registered_source"
)

// ResolveRequest contains explicit selectors and the physical invocation path.
// Workspace accepts a Project UUID or a path. Source accepts canonical key,
// typed ID, typed prefix, or display code through record.Inventory.
type ResolveRequest struct {
	InvocationDir string
	Workspace     string
	Source        string
	SkipConfig    bool
}

// AssociationObservation is the validated host-local evidence used during
// resolution. It contains no repository-config authority.
type AssociationObservation struct {
	Kind                ResolutionKind      `json:"kind"`
	Project             *ProjectAssociation `json:"project,omitempty"`
	Source              *SourceAssociation  `json:"source,omitempty"`
	MatchedLocatorHints []string            `json:"matched_locator_hints"`
	ValidatedAt         time.Time           `json:"validated_at"`
}

// Context keeps canonical authority, invocation, selected execution Source,
// host association evidence, and effective preferences in separate fields.
type Context struct {
	Project       *project.Info          `json:"-"`
	InvocationDir string                 `json:"invocation_dir"`
	Source        *research.Source       `json:"source,omitempty"`
	SourceRoot    string                 `json:"source_root,omitempty"`
	ResolvedRoot  string                 `json:"resolved_root,omitempty"`
	SourceSubdir  string                 `json:"source_subdir,omitempty"`
	Association   AssociationObservation `json:"association"`
	Config        *config.Result         `json:"config,omitempty"`
}

// ProjectID returns the canonical Project identity without duplicating it in
// mutable context state.
func (context *Context) ProjectID() research.UUID {
	if context == nil || context.Project == nil || context.Project.Project() == nil {
		return research.UUID{}
	}
	return context.Project.Project().ProjectID
}

// ResolutionDiagnostic is safe, deterministic, host-local diagnostic context.
type ResolutionDiagnostic struct {
	ProjectID research.UUID
	SourceID  research.ID
	Root      string
	Reason    string
}

// ResolutionError classifies ambiguity, staleness, and absence while retaining
// sorted actionable local candidates.
type ResolutionError struct {
	Kind          error
	InvocationDir string
	Candidates    []ResolutionDiagnostic
}

func (failure *ResolutionError) Error() string {
	if failure == nil {
		return "workspace resolution failed"
	}
	var builder strings.Builder
	switch {
	case errors.Is(failure.Kind, ErrAmbiguousResolution):
		fmt.Fprintf(&builder, "workspace resolution for %s is ambiguous", failure.InvocationDir)
	case errors.Is(failure.Kind, ErrStaleAssociation):
		fmt.Fprintf(&builder, "workspace association for %s is stale", failure.InvocationDir)
	default:
		fmt.Fprintf(&builder, "no registered workspace contains %s", failure.InvocationDir)
	}
	for _, candidate := range failure.Candidates {
		builder.WriteString("; ")
		if !candidate.ProjectID.IsZero() {
			builder.WriteString("project=")
			builder.WriteString(candidate.ProjectID.String())
		}
		if !candidate.SourceID.IsZero() {
			builder.WriteString(" source=")
			builder.WriteString(candidate.SourceID.String())
		}
		if candidate.Root != "" {
			builder.WriteString(" root=")
			builder.WriteString(candidate.Root)
		}
		if candidate.Reason != "" {
			builder.WriteString(" (")
			builder.WriteString(candidate.Reason)
			builder.WriteByte(')')
		}
	}
	return builder.String()
}

func (failure *ResolutionError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Kind
}

// AssociationReader is the immutable local-mapping seam used during
// resolution. Mutation remains a separate CLI/service boundary.
type AssociationReader interface {
	List(context.Context) (Associations, error)
}

// ConfigLoader is the narrow preference-loading seam used after identity is
// fully resolved.
type ConfigLoader interface {
	Load(context.Context, config.Request) (*config.Result, error)
}

// ProjectDiscoverer is the injected direct canonical-project discovery seam.
type ProjectDiscoverer func(context.Context, string, gitx.Runner) (*project.Info, error)

// Resolver implements explicit -> current Project -> longest registered Source.
type Resolver struct {
	associations AssociationReader
	git          gitx.Runner
	discover     ProjectDiscoverer
	config       ConfigLoader
	clock        func() time.Time
	inventory    func(context.Context, *project.Info) (*record.Inventory, error)
}

// ResolverOption injects deterministic resolution seams.
type ResolverOption func(*Resolver)

func WithResolverGitRunner(runner gitx.Runner) ResolverOption {
	return func(resolver *Resolver) { resolver.git = runner }
}

func WithProjectDiscoverer(discover ProjectDiscoverer) ResolverOption {
	return func(resolver *Resolver) { resolver.discover = discover }
}

func WithConfigLoader(loader ConfigLoader) ResolverOption {
	return func(resolver *Resolver) { resolver.config = loader }
}

func WithoutConfig() ResolverOption {
	return func(resolver *Resolver) { resolver.config = nil }
}

func WithResolverClock(clock func() time.Time) ResolverOption {
	return func(resolver *Resolver) { resolver.clock = clock }
}

// WithInventoryLoader injects the canonical read used during Source resolution.
// The loader remains read-only; ResolveSnapshot adds request-local memoization.
func WithInventoryLoader(loader func(context.Context, *project.Info) (*record.Inventory, error)) ResolverOption {
	return func(resolver *Resolver) { resolver.inventory = loader }
}

// NewResolver constructs a resolver. A nil reader uses the default association
// path; a default config loader annotates effective config after resolution.
func NewResolver(associations AssociationReader, options ...ResolverOption) *Resolver {
	if associations == nil {
		associations = NewStore()
	}
	gitRunner := gitx.Runner(gitx.ExecRunner{})
	if store, ok := associations.(*Store); ok {
		gitRunner = store.gitRunner()
	}
	resolver := &Resolver{
		associations: associations,
		git:          gitRunner,
		discover:     project.DiscoverWithGit,
		config:       config.NewLoader(),
		clock:        time.Now,
		inventory:    loadCanonicalInventory,
	}
	for _, option := range options {
		if option != nil {
			option(resolver)
		}
	}
	if resolver.git == nil {
		resolver.git = gitx.ExecRunner{}
	}
	if resolver.discover == nil {
		resolver.discover = project.DiscoverWithGit
	}
	if resolver.clock == nil {
		resolver.clock = time.Now
	}
	if resolver.inventory == nil {
		resolver.inventory = loadCanonicalInventory
	}
	return resolver
}

type resolvedSource struct {
	project     *project.Info
	source      *research.Source
	association SourceAssociation
	repository  gitx.Repository
	root        string
	matched     []string
}

type containingCandidate struct {
	resolved resolvedSource
	project  ProjectAssociation
}

// Resolve returns one context or a deterministic classified error.
func (resolver *Resolver) Resolve(ctx context.Context, request ResolveRequest) (*Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.SkipConfig && resolver.config != nil {
		identityOnly := *resolver
		identityOnly.config = nil
		request.SkipConfig = false
		return identityOnly.Resolve(ctx, request)
	}
	invocation := request.InvocationDir
	if invocation == "" {
		var err error
		invocation, err = filepath.Abs(".")
		if err != nil {
			return nil, fmt.Errorf("resolve invocation directory: %w", err)
		}
	} else if !filepath.IsAbs(invocation) {
		absolute, err := filepath.Abs(invocation)
		if err != nil {
			return nil, fmt.Errorf("resolve invocation directory: %w", err)
		}
		invocation = absolute
	}
	invocation = filepath.Clean(invocation)
	canonicalizeInvocation := func() error {
		canonical, err := pathx.Canonical(invocation)
		if err != nil {
			return fmt.Errorf("canonicalize invocation directory: %w", err)
		}
		invocation, err = canonicalExistingDirectory(canonical, "invocation directory")
		return err
	}
	// Preserve the embedded-project compatibility path before consulting optional
	// host-local association state. Explicit workspace selection still has higher
	// precedence, and explicit Source selection uses the current Project when one
	// is physically present.
	if request.Workspace == "" {
		current, currentErr := resolver.discover(ctx, invocation, resolver.git)
		if currentErr == nil {
			if err := canonicalizeInvocation(); err != nil {
				return nil, err
			}
			associations, listErr := resolver.associations.List(ctx)
			if listErr != nil {
				if request.Source != "" {
					return nil, listErr
				}
				// Association state is optional for the legacy embedded-project
				// path when no association-backed selector was requested.
				return resolver.finish(ctx, &Context{
					Project: current, InvocationDir: invocation,
					Association: AssociationObservation{Kind: ResolutionCurrentProject, MatchedLocatorHints: []string{}, ValidatedAt: resolver.now()},
				}, request.SkipConfig)
			}
			projectAssociation := matchingProjectAssociation(associations, current)
			if request.Source != "" {
				return resolver.resolveExplicitSource(ctx, invocation, current, projectAssociation, request.Source, associations, request.SkipConfig)
			}
			if len(associations.Sources) > 0 {
				containing, containingErr := resolver.resolveContainingSource(ctx, invocation, associations)
				switch {
				case containingErr == nil && containing.ProjectID() != current.Project().ProjectID:
					diagnostics := []ResolutionDiagnostic{
						{ProjectID: current.Project().ProjectID, Root: current.Repository.Root, Reason: "physical canonical Project marker"},
						{ProjectID: containing.ProjectID(), SourceID: containing.Source.ID, Root: containing.ResolvedRoot, Reason: "validated registered Source contains invocation"},
					}
					sortResolutionDiagnostics(diagnostics)
					return nil, &ResolutionError{Kind: ErrAmbiguousResolution, InvocationDir: invocation, Candidates: diagnostics}
				case containingErr != nil && !errors.Is(containingErr, ErrNotRegistered):
					return nil, containingErr
				}
			}
			return resolver.finish(ctx, &Context{
				Project: current, InvocationDir: invocation,
				Association: AssociationObservation{Kind: ResolutionCurrentProject, Project: projectAssociation, MatchedLocatorHints: []string{}, ValidatedAt: resolver.now()},
			}, request.SkipConfig)
		}
		if !errors.Is(currentErr, project.ErrNotInitialized) && !errors.Is(currentErr, gitx.ErrNotRepository) {
			return nil, currentErr
		}
	}

	if err := canonicalizeInvocation(); err != nil {
		return nil, err
	}
	associations, err := resolver.associations.List(ctx)
	if err != nil {
		return nil, err
	}
	if request.Workspace != "" {
		info, projectAssociation, err := resolver.resolveExplicitWorkspace(ctx, invocation, request.Workspace, associations)
		if err != nil {
			return nil, err
		}
		if request.Source != "" {
			return resolver.resolveExplicitSource(ctx, invocation, info, projectAssociation, request.Source, associations, request.SkipConfig)
		}
		return resolver.finish(ctx, &Context{
			Project: info, InvocationDir: invocation,
			Association: AssociationObservation{Kind: ResolutionExplicitWorkspace, Project: projectAssociation, MatchedLocatorHints: []string{}, ValidatedAt: resolver.now()},
		}, request.SkipConfig)
	}
	if request.Source != "" {
		info, projectAssociation, err := resolver.projectForExplicitSource(ctx, invocation, request.Source, associations)
		if err != nil {
			return nil, err
		}
		return resolver.resolveExplicitSource(ctx, invocation, info, projectAssociation, request.Source, associations, request.SkipConfig)
	}
	containing, err := resolver.resolveContainingSource(ctx, invocation, associations)
	if err != nil {
		return nil, err
	}
	return resolver.finish(ctx, containing, request.SkipConfig)
}

// ResolveSnapshot returns the exact canonical inventory used for the selected
// workspace. A request-local cache prevents Source resolution, containing-clone
// resolution, config annotation, and the caller's view builders from reopening
// the selected canonical root. Resolution across several explicitly registered
// Projects may still require one inventory per candidate Project.
func (resolver *Resolver) ResolveSnapshot(ctx context.Context, request ResolveRequest) (*Context, *record.Inventory, error) {
	if resolver == nil {
		return nil, nil, errors.New("workspace resolver is nil")
	}
	type cachedInventory struct {
		inventory *record.Inventory
		err       error
	}
	cache := make(map[string]cachedInventory)
	copy := *resolver
	base := copy.inventory
	if base == nil {
		base = loadCanonicalInventory
	}
	copy.inventory = func(ctx context.Context, info *project.Info) (*record.Inventory, error) {
		if info == nil {
			return nil, errors.New("canonical Project is required for inventory loading")
		}
		key := info.Root
		if cached, found := cache[key]; found {
			return cached.inventory, cached.err
		}
		inventory, err := base(ctx, info)
		cache[key] = cachedInventory{inventory: inventory, err: err}
		return inventory, err
	}
	resolved, err := copy.Resolve(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	inventory, err := copy.loadInventory(ctx, resolved.Project)
	if err != nil {
		return resolved, nil, err
	}
	return resolved, inventory, nil
}

// ResolveHistoricalSource validates the immutable local association for cleanup
// of already-created resources while permitting a canonically retired Source.
// It never selects a retired Source for new work and never loads execution config.
func (resolver *Resolver) ResolveHistoricalSource(ctx context.Context, request ResolveRequest) (*Context, error) {
	if strings.TrimSpace(request.Source) == "" {
		return nil, errors.New("historical Source resolution requires an explicit Source")
	}
	baseRequest := request
	baseRequest.Source = ""
	baseRequest.SkipConfig = true
	base, err := resolver.Resolve(ctx, baseRequest)
	if err != nil {
		return nil, err
	}
	associations, err := resolver.associations.List(ctx)
	if err != nil {
		return nil, err
	}
	projectAssociation := matchingProjectAssociation(associations, base.Project)
	return resolver.resolveExplicitSourceMode(ctx, base.InvocationDir, base.Project, projectAssociation, request.Source, associations, true, true)
}

func (resolver *Resolver) resolveExplicitWorkspace(ctx context.Context, invocation, selector string, associations Associations) (*project.Info, *ProjectAssociation, error) {
	if id, err := research.ParseUUID(strings.TrimSpace(selector)); err == nil {
		association, found := findProjectAssociation(associations, id)
		if !found {
			return nil, nil, &ResolutionError{
				Kind: ErrNotRegistered, InvocationDir: invocation,
				Candidates: []ResolutionDiagnostic{{ProjectID: id, Reason: "register this canonical workspace"}},
			}
		}
		info, err := resolver.resolveProjectAssociation(ctx, association)
		if err != nil {
			return nil, nil, err
		}
		copy := association
		return info, &copy, nil
	}
	workspacePath := selector
	if !filepath.IsAbs(workspacePath) {
		workspacePath = filepath.Join(invocation, workspacePath)
	}
	workspacePath = filepath.Clean(workspacePath)
	info, err := resolver.discover(ctx, workspacePath, resolver.git)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve explicit workspace %q: %w", selector, err)
	}
	return info, matchingProjectAssociation(associations, info), nil
}

func (resolver *Resolver) resolveExplicitSource(ctx context.Context, invocation string, info *project.Info, projectAssociation *ProjectAssociation, reference string, associations Associations, skipConfig bool) (*Context, error) {
	return resolver.resolveExplicitSourceMode(ctx, invocation, info, projectAssociation, reference, associations, skipConfig, false)
}

func (resolver *Resolver) resolveExplicitSourceMode(ctx context.Context, invocation string, info *project.Info, projectAssociation *ProjectAssociation, reference string, associations Associations, skipConfig, allowRetired bool) (*Context, error) {
	inventory, err := resolver.loadInventory(ctx, info)
	if err != nil {
		return nil, fmt.Errorf("load canonical Source inventory: %w", err)
	}
	if !inventory.Valid() {
		return nil, &record.InventoryError{Diagnostics: append([]record.Diagnostic(nil), inventory.Diagnostics...)}
	}
	document, err := inventory.ResolveSource(strings.TrimSpace(reference))
	if err != nil {
		return nil, fmt.Errorf("resolve Source %q: %w", reference, err)
	}
	source := research.Clone(document.Record.(*research.Source)).(*research.Source)
	if source.State == research.SourceRetired && !allowRetired {
		return nil, fmt.Errorf("Source %s is retired: %w", source.ID, ErrRetiredSource)
	}
	association, found := findSourceAssociation(associations, info.Project().ProjectID, source.ID)
	if !found {
		return nil, fmt.Errorf("Project %s Source %s: %w", info.Project().ProjectID, source.ID, ErrSourceNotRegistered)
	}
	resolved, err := resolver.resolveSourceAssociation(ctx, info, source, association)
	if err != nil {
		return nil, err
	}
	projectCopy := projectAssociation
	if projectCopy == nil {
		projectCopy = matchingProjectAssociation(associations, info)
	}
	sourceCopy := association
	return resolver.finish(ctx, &Context{
		Project: info, InvocationDir: invocation, Source: source,
		SourceRoot: resolved.repository.Root, ResolvedRoot: resolved.root, SourceSubdir: source.Subdir,
		Association: AssociationObservation{
			Kind: ResolutionExplicitSource, Project: projectCopy, Source: &sourceCopy,
			MatchedLocatorHints: append([]string{}, resolved.matched...), ValidatedAt: resolver.now(),
		},
	}, skipConfig)
}

func (resolver *Resolver) projectForExplicitSource(ctx context.Context, invocation, reference string, associations Associations) (*project.Info, *ProjectAssociation, error) {
	type match struct {
		info        *project.Info
		association ProjectAssociation
		sourceID    research.ID
	}
	matches := make([]match, 0)
	retired := make([]match, 0)
	ambiguousMatches := make([]ResolutionDiagnostic, 0)
	stale := make([]ResolutionDiagnostic, 0)
	for _, association := range associations.Projects {
		info, err := resolver.resolveProjectAssociation(ctx, association)
		if err != nil {
			stale = append(stale, ResolutionDiagnostic{ProjectID: association.ProjectID, Root: association.Root, Reason: "canonical workspace association is stale"})
			continue
		}
		inventory, err := resolver.loadInventory(ctx, info)
		if err != nil || !inventory.Valid() {
			stale = append(stale, ResolutionDiagnostic{ProjectID: association.ProjectID, Root: association.Root, Reason: "canonical Source inventory is invalid"})
			continue
		}
		document, err := inventory.ResolveSource(strings.TrimSpace(reference))
		if err != nil {
			if errors.Is(err, research.ErrAmbiguousReference) {
				ambiguousMatches = append(ambiguousMatches, ResolutionDiagnostic{ProjectID: association.ProjectID, Root: association.Root, Reason: "Source selector is ambiguous within this Project"})
			}
			continue
		}
		sourceID, _ := document.ID()
		candidate := match{info: info, association: association, sourceID: sourceID}
		if document.Record.(*research.Source).State == research.SourceRetired {
			retired = append(retired, candidate)
			continue
		}
		matches = append(matches, candidate)
	}
	sort.Slice(matches, func(left, right int) bool {
		if matches[left].association.ProjectID != matches[right].association.ProjectID {
			return matches[left].association.ProjectID.String() < matches[right].association.ProjectID.String()
		}
		return matches[left].sourceID.String() < matches[right].sourceID.String()
	})
	if len(ambiguousMatches) > 0 {
		for _, match := range matches {
			ambiguousMatches = append(ambiguousMatches, ResolutionDiagnostic{ProjectID: match.association.ProjectID, SourceID: match.sourceID, Root: match.association.Root, Reason: "Source selector also matches this Project"})
		}
		sortResolutionDiagnostics(ambiguousMatches)
		return nil, nil, &ResolutionError{Kind: ErrAmbiguousResolution, InvocationDir: invocation, Candidates: ambiguousMatches}
	}
	if len(matches) == 1 {
		copy := matches[0].association
		return matches[0].info, &copy, nil
	}
	if len(matches) > 1 {
		diagnostics := make([]ResolutionDiagnostic, 0, len(matches))
		for _, match := range matches {
			diagnostics = append(diagnostics, ResolutionDiagnostic{ProjectID: match.association.ProjectID, SourceID: match.sourceID, Root: match.association.Root, Reason: "Source selector matches this Project"})
		}
		return nil, nil, &ResolutionError{Kind: ErrAmbiguousResolution, InvocationDir: invocation, Candidates: diagnostics}
	}
	if len(retired) > 0 {
		diagnostics := make([]ResolutionDiagnostic, 0, len(retired))
		for _, match := range retired {
			diagnostics = append(diagnostics, ResolutionDiagnostic{ProjectID: match.association.ProjectID, SourceID: match.sourceID, Root: match.association.Root, Reason: "Source selector matches only a retired Source"})
		}
		sortResolutionDiagnostics(diagnostics)
		return nil, nil, &ResolutionError{Kind: ErrRetiredSource, InvocationDir: invocation, Candidates: diagnostics}
	}
	if len(stale) > 0 {
		sortResolutionDiagnostics(stale)
		return nil, nil, &ResolutionError{Kind: ErrStaleAssociation, InvocationDir: invocation, Candidates: stale}
	}
	return nil, nil, &ResolutionError{
		Kind: ErrNotRegistered, InvocationDir: invocation,
		Candidates: []ResolutionDiagnostic{{Reason: "select or register a canonical workspace before using this Source"}},
	}
}

func (resolver *Resolver) resolveContainingSource(ctx context.Context, invocation string, associations Associations) (*Context, error) {
	projects := make(map[research.UUID]*project.Info)
	projectFailures := make(map[research.UUID]error)
	projectAssociations := make(map[research.UUID]ProjectAssociation)
	for _, association := range associations.Projects {
		projectAssociations[association.ProjectID] = association
	}
	candidates := make([]containingCandidate, 0)
	stale := make([]ResolutionDiagnostic, 0)
	for _, association := range associations.Sources {
		if !lexicallyContains(association.Root, invocation) {
			continue
		}
		info, loaded := projects[association.ProjectID]
		if !loaded {
			if failure, failed := projectFailures[association.ProjectID]; failed {
				stale = append(stale, ResolutionDiagnostic{ProjectID: association.ProjectID, SourceID: association.SourceID, Root: association.Root, Reason: safeStaleReason(failure)})
				continue
			}
			projectAssociation := projectAssociations[association.ProjectID]
			var err error
			info, err = resolver.resolveProjectAssociation(ctx, projectAssociation)
			if err != nil {
				projectFailures[association.ProjectID] = err
				stale = append(stale, ResolutionDiagnostic{ProjectID: association.ProjectID, SourceID: association.SourceID, Root: association.Root, Reason: "canonical workspace association is stale"})
				continue
			}
			projects[association.ProjectID] = info
		}
		source, err := resolver.loadSource(ctx, info, association.SourceID)
		if err != nil {
			stale = append(stale, ResolutionDiagnostic{ProjectID: association.ProjectID, SourceID: association.SourceID, Root: association.Root, Reason: "canonical Source record is missing or invalid"})
			continue
		}
		if source.State == research.SourceRetired {
			continue
		}
		expectedRoot := filepath.Join(association.Root, filepath.FromSlash(source.Subdir))
		if !lexicallyContains(expectedRoot, invocation) {
			continue
		}
		resolved, err := resolver.resolveSourceAssociation(ctx, info, source, association)
		if err != nil {
			stale = append(stale, ResolutionDiagnostic{ProjectID: association.ProjectID, SourceID: association.SourceID, Root: expectedRoot, Reason: safeStaleReason(err)})
			continue
		}
		inside, err := pathx.Contains(resolved.root, invocation)
		if err != nil || !inside {
			continue
		}
		candidates = append(candidates, containingCandidate{resolved: resolved, project: projectAssociations[association.ProjectID]})
	}
	if len(candidates) == 0 {
		if len(stale) > 0 {
			sortResolutionDiagnostics(stale)
			return nil, &ResolutionError{Kind: staleResolutionKind(stale), InvocationDir: invocation, Candidates: stale}
		}
		return nil, &ResolutionError{
			Kind: ErrNotRegistered, InvocationDir: invocation,
			Candidates: []ResolutionDiagnostic{{Reason: "register a canonical workspace and Source clone"}},
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		leftDepth := pathDepth(candidates[left].resolved.root)
		rightDepth := pathDepth(candidates[right].resolved.root)
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		if candidates[left].resolved.root != candidates[right].resolved.root {
			return candidates[left].resolved.root < candidates[right].resolved.root
		}
		leftKey := sourceAssociationKey(candidates[left].project.ProjectID, candidates[left].resolved.source.ID)
		rightKey := sourceAssociationKey(candidates[right].project.ProjectID, candidates[right].resolved.source.ID)
		return leftKey < rightKey
	})
	longest := pathDepth(candidates[0].resolved.root)
	moreSpecificStale := make([]ResolutionDiagnostic, 0)
	for _, diagnostic := range stale {
		if pathDepth(diagnostic.Root) >= longest {
			moreSpecificStale = append(moreSpecificStale, diagnostic)
		}
	}
	if len(moreSpecificStale) > 0 {
		sortResolutionDiagnostics(moreSpecificStale)
		return nil, &ResolutionError{Kind: staleResolutionKind(moreSpecificStale), InvocationDir: invocation, Candidates: moreSpecificStale}
	}
	ambiguous := candidates[:1]
	for index := 1; index < len(candidates); index++ {
		if pathDepth(candidates[index].resolved.root) != longest {
			break
		}
		ambiguous = append(ambiguous, candidates[index])
	}
	if len(ambiguous) > 1 {
		diagnostics := make([]ResolutionDiagnostic, 0, len(ambiguous))
		for _, candidate := range ambiguous {
			diagnostics = append(diagnostics, ResolutionDiagnostic{
				ProjectID: candidate.project.ProjectID, SourceID: candidate.resolved.source.ID,
				Root: candidate.resolved.root, Reason: "equally specific registered Source",
			})
		}
		sortResolutionDiagnostics(diagnostics)
		return nil, &ResolutionError{Kind: ErrAmbiguousResolution, InvocationDir: invocation, Candidates: diagnostics}
	}
	selected := candidates[0]
	projectCopy := selected.project
	sourceCopy := selected.resolved.association
	return &Context{
		Project: selected.resolved.project, InvocationDir: invocation,
		Source: selected.resolved.source, SourceRoot: selected.resolved.repository.Root,
		ResolvedRoot: selected.resolved.root, SourceSubdir: selected.resolved.source.Subdir,
		Association: AssociationObservation{
			Kind: ResolutionContainingSource, Project: &projectCopy, Source: &sourceCopy,
			MatchedLocatorHints: append([]string{}, selected.resolved.matched...), ValidatedAt: resolver.now(),
		},
	}, nil
}

func (resolver *Resolver) resolveSourceAssociation(ctx context.Context, info *project.Info, source *research.Source, association SourceAssociation) (resolvedSource, error) {
	if info == nil || info.Project() == nil || association.ProjectID != info.Project().ProjectID || association.SourceID != source.ID {
		return resolvedSource{}, fmt.Errorf("Source association identity differs from canonical context: %w", ErrStaleAssociation)
	}
	root, err := canonicalExistingDirectory(association.Root, "registered Source clone root")
	if err != nil {
		return resolvedSource{}, fmt.Errorf("Source %s association is stale: %w", source.ID, errors.Join(ErrStaleAssociation, err))
	}
	common, err := canonicalExistingDirectory(association.GitCommonDir, "registered Source Git common dir")
	if err != nil {
		return resolvedSource{}, fmt.Errorf("Source %s association is stale: %w", source.ID, errors.Join(ErrStaleAssociation, err))
	}
	if association.GitCommonIdentity == "" {
		return resolvedSource{}, fmt.Errorf("Source %s association predates persistent Git identity and must be re-registered: %w", source.ID, ErrStaleAssociation)
	}
	commonIdentity, err := pathx.DirectoryFilesystemIdentity(common)
	if err != nil || commonIdentity != association.GitCommonIdentity {
		return resolvedSource{}, fmt.Errorf("Source %s Git common filesystem identity changed: %w", source.ID, errors.Join(ErrStaleAssociation, err))
	}
	repository, matched, verifiedIdentity, err := resolver.validateSourceClone(ctx, source, root, common, association.LocatorHints)
	if err != nil {
		return resolvedSource{}, fmt.Errorf("Source %s association is stale: %w", source.ID, errors.Join(ErrStaleAssociation, err))
	}
	if repository.Root != root || repository.GitCommonDir != common || verifiedIdentity != commonIdentity {
		return resolvedSource{}, fmt.Errorf("Source %s clone root or Git-common identity changed: %w", source.ID, ErrStaleAssociation)
	}
	resolvedRoot, err := pathx.ResolveUnderNoSymlinks(root, source.Subdir, true)
	if err != nil {
		return resolvedSource{}, fmt.Errorf("Source %s subdir is stale: %w", source.ID, errors.Join(ErrStaleAssociation, err))
	}
	return resolvedSource{
		project: info, source: research.Clone(source).(*research.Source), association: association,
		repository: repository, root: resolvedRoot, matched: append([]string{}, matched...),
	}, nil
}

func (resolver *Resolver) finish(ctx context.Context, value *Context, skipConfig ...bool) (*Context, error) {
	if value == nil || value.Project == nil {
		return nil, errors.New("resolved workspace context is incomplete")
	}
	if value.Association.MatchedLocatorHints == nil {
		value.Association.MatchedLocatorHints = []string{}
	}
	if resolver.config == nil || len(skipConfig) > 0 && skipConfig[0] {
		return value, nil
	}
	configInvocation := value.InvocationDir
	if value.Source != nil {
		inside, containErr := pathx.Contains(value.ResolvedRoot, configInvocation)
		if containErr != nil || !inside {
			// An explicitly selected external Source uses its semantic root as the
			// execution-config scope when the caller is in the canonical repository
			// or another unrelated directory. Context.InvocationDir remains physical.
			configInvocation = value.ResolvedRoot
		}
	}
	request := config.Request{Project: value.Project, InvocationDir: configInvocation}
	if value.Source != nil {
		request.Source = value.Source
		request.SourceRoot = value.SourceRoot
		request.SourceGitCommonDir = value.Association.Source.GitCommonDir
		request.SourceSubdir = value.SourceSubdir
	}
	effective, err := resolver.config.Load(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("load resolved workspace config: %w", err)
	}
	value.Config = effective
	return value, nil
}

func matchingProjectAssociation(associations Associations, info *project.Info) *ProjectAssociation {
	if info == nil || info.Project() == nil {
		return nil
	}
	association, found := findProjectAssociation(associations, info.Project().ProjectID)
	if !found || association.Root != info.Repository.Root || association.GitCommonDir != info.Repository.GitCommonDir {
		return nil
	}
	copy := association
	return &copy
}

func findSourceAssociation(associations Associations, projectID research.UUID, sourceID research.ID) (SourceAssociation, bool) {
	for _, association := range associations.Sources {
		if association.ProjectID == projectID && association.SourceID == sourceID {
			return association, true
		}
	}
	return SourceAssociation{}, false
}

func lexicallyContains(root, candidate string) bool {
	if root == "" || candidate == "" || !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && (relative == "." || relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func pathDepth(value string) int {
	clean := filepath.Clean(value)
	volume := filepath.VolumeName(clean)
	clean = strings.TrimPrefix(clean, volume)
	return len(strings.FieldsFunc(clean, func(character rune) bool { return character == '/' || character == '\\' }))
}

func staleResolutionKind(values []ResolutionDiagnostic) error {
	kind := error(ErrStaleAssociation)
	for _, value := range values {
		if value.Reason == "sanitized Source locator no longer matches" {
			kind = errors.Join(kind, ErrLocatorMismatch)
			break
		}
	}
	return kind
}

func safeStaleReason(err error) string {
	switch {
	case errors.Is(err, ErrLocatorMismatch):
		return "sanitized Source locator no longer matches"
	case errors.Is(err, pathx.ErrSymlink), errors.Is(err, pathx.ErrOutsideRoot):
		return "Source subdir is unsafe or outside its clone"
	default:
		return "registered path or Git identity is stale"
	}
}

func sortResolutionDiagnostics(values []ResolutionDiagnostic) {
	sort.Slice(values, func(left, right int) bool {
		leftKey := values[left].ProjectID.String() + "\x00" + values[left].SourceID.String() + "\x00" + values[left].Root + "\x00" + values[left].Reason
		rightKey := values[right].ProjectID.String() + "\x00" + values[right].SourceID.String() + "\x00" + values[right].Root + "\x00" + values[right].Reason
		return leftKey < rightKey
	})
}

func (resolver *Resolver) loadInventory(ctx context.Context, info *project.Info) (*record.Inventory, error) {
	if resolver == nil || resolver.inventory == nil {
		return loadCanonicalInventory(ctx, info)
	}
	return resolver.inventory(ctx, info)
}

func (resolver *Resolver) loadSource(ctx context.Context, info *project.Info, sourceID research.ID) (*research.Source, error) {
	inventory, err := resolver.loadInventory(ctx, info)
	if err != nil {
		return nil, err
	}
	if !inventory.Valid() {
		return nil, &record.InventoryError{Diagnostics: append([]record.Diagnostic(nil), inventory.Diagnostics...)}
	}
	document, err := inventory.ByID(sourceID)
	if err != nil {
		return nil, err
	}
	source, ok := document.Record.(*research.Source)
	if !ok {
		return nil, fmt.Errorf("record %s is not a Source", sourceID)
	}
	return research.Clone(source).(*research.Source), nil
}

func (resolver *Resolver) validationStore() *Store {
	if resolver == nil {
		return &Store{git: gitx.ExecRunner{}, clock: time.Now}
	}
	return &Store{git: resolver.git, clock: resolver.clock}
}

func (resolver *Resolver) resolveProjectAssociation(ctx context.Context, association ProjectAssociation) (*project.Info, error) {
	return resolver.validationStore().resolveProjectAssociation(ctx, association)
}

func (resolver *Resolver) validateSourceClone(ctx context.Context, source *research.Source, root, common string, observed []string) (gitx.Repository, []string, string, error) {
	return resolver.validationStore().validateSourceClone(ctx, source, root, common, observed)
}

func (resolver *Resolver) now() time.Time {
	if resolver != nil && resolver.clock != nil {
		return resolver.clock().UTC()
	}
	return time.Now().UTC()
}
