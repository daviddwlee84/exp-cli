// Package workspace resolves canonical experiment projects and their registered
// Source clones without allowing repository configuration to become authority.
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	// AssociationSchema is the closed schema stored in associations/v1.json.
	AssociationSchema = "exp.associations/v1"
	// MaxAssociationBytes bounds the complete host-local association database.
	MaxAssociationBytes int64 = 4 << 20
)

var (
	ErrProjectNotRegistered   = errors.New("canonical workspace is not registered")
	ErrSourceNotRegistered    = errors.New("Source clone is not registered")
	ErrStaleAssociation       = errors.New("workspace association is stale")
	ErrLocatorMismatch        = errors.New("Source clone does not match any sanitized locator hint")
	ErrRetiredSource          = errors.New("retired Source cannot be selected for new work")
	errNoAssociationChange    = errors.New("association mutation made no change")
	filesystemIdentityPattern = regexp.MustCompile(`^(?:unix|windows):[0-9a-f]+:[0-9a-f]+$`)
)

// ProjectAssociation maps one canonical Project UUID to the selected local
// canonical clone. Root is the Git worktree root, not experiments/.
type ProjectAssociation struct {
	ProjectID    research.UUID `json:"project_id"`
	Root         string        `json:"root"`
	GitCommonDir string        `json:"git_common_dir"`
	ObservedAt   time.Time     `json:"observed_at"`
}

// SourceAssociation maps one project-scoped Source ID to a local Git clone.
// LocatorHints contains only normalized hints actually observed on that clone.
type SourceAssociation struct {
	ProjectID         research.UUID `json:"project_id"`
	SourceID          research.ID   `json:"source_id"`
	Root              string        `json:"root"`
	GitCommonDir      string        `json:"git_common_dir"`
	GitCommonIdentity string        `json:"git_common_identity,omitempty"`
	LocatorHints      []string      `json:"locator_hints"`
	ObservedAt        time.Time     `json:"observed_at"`
}

// Associations is an immutable-by-convention snapshot sorted by identity.
type Associations struct {
	Schema   string               `json:"schema"`
	Projects []ProjectAssociation `json:"projects"`
	Sources  []SourceAssociation  `json:"sources"`
}

// SourceCloneObservation is sanitized, host-local evidence gathered before a
// Source record is published. Repository paths are never canonical authority.
type SourceCloneObservation struct {
	Repository        gitx.Repository
	ResolvedRoot      string
	GitCommonIdentity string
	LocatorHints      []string
}

// PathResolver supplies the complete associations/v1.json path.
type PathResolver func() (string, error)

// Store owns the private host-local association database.
type Store struct {
	path       PathResolver
	clock      func() time.Time
	git        gitx.Runner
	atomicHook record.AtomicHook
	mu         sync.Mutex
}

// StoreOption injects deterministic association-store seams.
type StoreOption func(*Store)

// WithPath stores associations at an explicit clean absolute file path.
func WithPath(path string) StoreOption {
	return func(store *Store) { store.path = func() (string, error) { return path, nil } }
}

// WithPathResolver injects association path discovery.
func WithPathResolver(resolve PathResolver) StoreOption {
	return func(store *Store) { store.path = resolve }
}

// WithClock injects registration observation time.
func WithClock(clock func() time.Time) StoreOption {
	return func(store *Store) { store.clock = clock }
}

// WithGitRunner injects all Git discovery and remote inspection.
func WithGitRunner(runner gitx.Runner) StoreOption {
	return func(store *Store) { store.git = runner }
}

// WithAtomicHook injects failures at local-state publication boundaries.
func WithAtomicHook(hook record.AtomicHook) StoreOption {
	return func(store *Store) { store.atomicHook = hook }
}

// NewStore constructs an association store at the XDG state path by default.
func NewStore(options ...StoreOption) *Store {
	store := &Store{path: DefaultAssociationPath, clock: time.Now, git: gitx.ExecRunner{}}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	if store.path == nil {
		store.path = DefaultAssociationPath
	}
	if store.clock == nil {
		store.clock = time.Now
	}
	if store.git == nil {
		store.git = gitx.ExecRunner{}
	}
	return store
}

// NewAssociationStore is an explicit alias useful at composition boundaries.
func NewAssociationStore(options ...StoreOption) *Store { return NewStore(options...) }

// DefaultAssociationPath returns $XDG_STATE_HOME/exp/associations/v1.json,
// using the XDG ~/.local/state fallback when necessary.
func DefaultAssociationPath() (string, error) {
	home, err := localstate.StateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "exp", "associations", "v1.json"), nil
}

// Path returns the canonical-parent path used by the store without creating it.
func (store *Store) Path() (string, error) {
	if store == nil || store.path == nil {
		return "", errors.New("association store path is not configured")
	}
	path, err := store.path()
	if err != nil {
		return "", fmt.Errorf("resolve association store path: %w", err)
	}
	canonical, err := localstate.CanonicalPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve association store path: %w", err)
	}
	return canonical, nil
}

// List returns a validated stable snapshot. It does not create state or repair
// permissions when the store is absent.
func (store *Store) List(ctx context.Context) (Associations, error) {
	path, err := store.Path()
	if err != nil {
		return Associations{}, err
	}
	snapshot, err := localstate.Read(ctx, path, MaxAssociationBytes)
	if err != nil {
		return Associations{}, fmt.Errorf("read workspace associations: %w", err)
	}
	return decodeAssociations(snapshot)
}

// Snapshot is an alias for List that emphasizes the returned value's lifetime.
func (store *Store) Snapshot(ctx context.Context) (Associations, error) {
	return store.List(ctx)
}

// LookupProject returns one project mapping without touching the filesystem
// paths named by the association.
func (store *Store) LookupProject(ctx context.Context, id research.UUID) (ProjectAssociation, error) {
	associations, err := store.List(ctx)
	if err != nil {
		return ProjectAssociation{}, err
	}
	for _, association := range associations.Projects {
		if association.ProjectID == id {
			return association, nil
		}
	}
	return ProjectAssociation{}, fmt.Errorf("Project %s: %w", id, ErrProjectNotRegistered)
}

// LookupSource returns one project-scoped Source mapping without resolving it.
func (store *Store) LookupSource(ctx context.Context, projectID research.UUID, sourceID research.ID) (SourceAssociation, error) {
	associations, err := store.List(ctx)
	if err != nil {
		return SourceAssociation{}, err
	}
	for _, association := range associations.Sources {
		if association.ProjectID == projectID && association.SourceID == sourceID {
			return association, nil
		}
	}
	return SourceAssociation{}, fmt.Errorf("Project %s Source %s: %w", projectID, sourceID, ErrSourceNotRegistered)
}

// RegisterProjectPath discovers and registers the canonical Project at root.
func (store *Store) RegisterProjectPath(ctx context.Context, root string) (ProjectAssociation, error) {
	info, err := project.DiscoverWithGit(ctx, root, store.gitRunner())
	if err != nil {
		return ProjectAssociation{}, fmt.Errorf("discover canonical workspace: %w", err)
	}
	return store.RegisterProject(ctx, info)
}

// RegisterProject re-discovers info through the injected Git seam before
// publishing its Project UUID, canonical clone root, and Git-common identity.
func (store *Store) RegisterProject(ctx context.Context, info *project.Info) (ProjectAssociation, error) {
	validated, err := store.validateProjectInfo(ctx, info)
	if err != nil {
		return ProjectAssociation{}, err
	}
	association := ProjectAssociation{
		ProjectID:    validated.Project().ProjectID,
		Root:         validated.Repository.Root,
		GitCommonDir: validated.Repository.GitCommonDir,
		ObservedAt:   store.now(),
	}
	err = store.update(ctx, func(current *Associations) error {
		replaced := false
		for index := range current.Projects {
			if current.Projects[index].ProjectID == association.ProjectID {
				current.Projects[index] = association
				replaced = true
				break
			}
		}
		if !replaced {
			current.Projects = append(current.Projects, association)
		}
		return nil
	})
	if err != nil {
		return association, err
	}
	return association, nil
}

// RegisterSource validates the canonical Source record, the clone Git-common
// identity, subdir containment, and locator match before publishing the mapping.
func (store *Store) RegisterSource(ctx context.Context, projectID research.UUID, sourceID research.ID, cloneRoot string) (SourceAssociation, error) {
	if projectID.IsZero() {
		return SourceAssociation{}, errors.New("Source registration requires a Project UUID")
	}
	if sourceID.IsZero() || sourceID.Kind() != research.KindSource {
		return SourceAssociation{}, errors.New("Source registration requires a typed Source ID")
	}
	projectAssociation, err := store.LookupProject(ctx, projectID)
	if err != nil {
		return SourceAssociation{}, err
	}
	validatedProject, err := store.resolveProjectAssociation(ctx, projectAssociation)
	if err != nil {
		return SourceAssociation{}, err
	}
	source, err := loadCanonicalSource(ctx, validatedProject, sourceID)
	if err != nil {
		return SourceAssociation{}, err
	}
	validated, matched, commonIdentity, err := store.validateSourceClone(ctx, source, cloneRoot, "", nil)
	if err != nil {
		return SourceAssociation{}, err
	}
	association := SourceAssociation{
		ProjectID: projectID, SourceID: sourceID, Root: validated.Root,
		GitCommonDir: validated.GitCommonDir, GitCommonIdentity: commonIdentity,
		LocatorHints: append([]string{}, matched...), ObservedAt: store.now(),
	}
	err = store.update(ctx, func(current *Associations) error {
		observedIdentity, identityErr := pathx.DirectoryFilesystemIdentity(validated.GitCommonDir)
		if identityErr != nil || observedIdentity != commonIdentity {
			return fmt.Errorf("Source clone Git common identity changed before association publication: %w", errors.Join(ErrStaleAssociation, identityErr))
		}
		registered, found := findProjectAssociation(*current, projectID)
		if !found {
			return fmt.Errorf("Project %s: %w", projectID, ErrProjectNotRegistered)
		}
		if registered.Root != projectAssociation.Root || registered.GitCommonDir != projectAssociation.GitCommonDir {
			return fmt.Errorf("Project %s changed during Source registration: %w", projectID, ErrStaleAssociation)
		}
		replaced := false
		for index := range current.Sources {
			if current.Sources[index].ProjectID == projectID && current.Sources[index].SourceID == sourceID {
				current.Sources[index] = association
				replaced = true
				break
			}
		}
		if !replaced {
			current.Sources = append(current.Sources, association)
		}
		return nil
	})
	if err != nil {
		return association, err
	}
	return association, nil
}

// RemoveProject removes one workspace mapping and all of its Source mappings.
func (store *Store) RemoveProject(ctx context.Context, projectID research.UUID) (bool, error) {
	removed := false
	err := store.update(ctx, func(current *Associations) error {
		projects := current.Projects[:0]
		for _, association := range current.Projects {
			if association.ProjectID == projectID {
				removed = true
				continue
			}
			projects = append(projects, association)
		}
		current.Projects = projects
		if removed {
			sources := current.Sources[:0]
			for _, association := range current.Sources {
				if association.ProjectID != projectID {
					sources = append(sources, association)
				}
			}
			current.Sources = sources
			return nil
		}
		return errNoAssociationChange
	})
	return removed, err
}

// RemoveSource removes one project-scoped clone mapping.
func (store *Store) RemoveSource(ctx context.Context, projectID research.UUID, sourceID research.ID) (bool, error) {
	removed := false
	err := store.update(ctx, func(current *Associations) error {
		sources := current.Sources[:0]
		for _, association := range current.Sources {
			if association.ProjectID == projectID && association.SourceID == sourceID {
				removed = true
				continue
			}
			sources = append(sources, association)
		}
		current.Sources = sources
		if !removed {
			return errNoAssociationChange
		}
		return nil
	})
	return removed, err
}

func (store *Store) update(ctx context.Context, mutate func(*Associations) error) error {
	if mutate == nil {
		return errors.New("association mutation is nil")
	}
	path, err := store.Path()
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return localstate.WithLockedFile(ctx, path, func(root *os.Root, name string) error {
		previous, err := localstate.ReadRoot(ctx, root, name, MaxAssociationBytes)
		if err != nil {
			return fmt.Errorf("read workspace associations: %w", err)
		}
		current, err := decodeAssociations(previous)
		if err != nil {
			return err
		}
		if err := mutate(&current); err != nil {
			if errors.Is(err, errNoAssociationChange) {
				return nil
			}
			return err
		}
		normalizeAssociations(&current)
		if err := validateAssociations(current); err != nil {
			return fmt.Errorf("validate workspace associations: %w", err)
		}
		data, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			return fmt.Errorf("encode workspace associations: %w", err)
		}
		data = append(data, '\n')
		if int64(len(data)) > MaxAssociationBytes {
			return errors.New("workspace association store exceeds byte limit")
		}
		if previous != nil && bytes.Equal(previous.Data, data) {
			return nil
		}
		return localstate.WriteRoot(root, name, data, previous, store.atomicHook)
	})
}

func decodeAssociations(snapshot *localstate.Snapshot) (Associations, error) {
	if snapshot == nil {
		return Associations{Schema: AssociationSchema, Projects: []ProjectAssociation{}, Sources: []SourceAssociation{}}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(snapshot.Data))
	decoder.DisallowUnknownFields()
	var associations Associations
	if err := decoder.Decode(&associations); err != nil {
		return Associations{}, fmt.Errorf("decode workspace associations: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return Associations{}, fmt.Errorf("decode workspace associations: %w", err)
	}
	if err := validateAssociations(associations); err != nil {
		return Associations{}, fmt.Errorf("validate workspace associations: %w", err)
	}
	normalizeAssociations(&associations)
	return cloneAssociations(associations), nil
}

func validateAssociations(associations Associations) error {
	if associations.Schema != AssociationSchema {
		return fmt.Errorf("association schema must be %q", AssociationSchema)
	}
	if associations.Projects == nil || associations.Sources == nil {
		return errors.New("association projects and sources arrays must be present")
	}
	projects := make(map[research.UUID]struct{}, len(associations.Projects))
	for index, association := range associations.Projects {
		if association.ProjectID.IsZero() {
			return fmt.Errorf("project association %d has no Project UUID", index)
		}
		if _, duplicate := projects[association.ProjectID]; duplicate {
			return fmt.Errorf("duplicate Project association %s", association.ProjectID)
		}
		projects[association.ProjectID] = struct{}{}
		if err := validateStoredPath(association.Root); err != nil {
			return fmt.Errorf("project association %s root: %w", association.ProjectID, err)
		}
		if err := validateStoredPath(association.GitCommonDir); err != nil {
			return fmt.Errorf("project association %s Git common dir: %w", association.ProjectID, err)
		}
		if !validObservationTime(association.ObservedAt) {
			return fmt.Errorf("project association %s has invalid observed_at", association.ProjectID)
		}
	}
	sources := make(map[string]struct{}, len(associations.Sources))
	for index, association := range associations.Sources {
		if association.ProjectID.IsZero() || association.SourceID.IsZero() || association.SourceID.Kind() != research.KindSource {
			return fmt.Errorf("Source association %d has invalid Project/Source identity", index)
		}
		if _, found := projects[association.ProjectID]; !found {
			return fmt.Errorf("Source association %s/%s has no registered Project", association.ProjectID, association.SourceID)
		}
		key := sourceAssociationKey(association.ProjectID, association.SourceID)
		if _, duplicate := sources[key]; duplicate {
			return fmt.Errorf("duplicate Source association %s/%s", association.ProjectID, association.SourceID)
		}
		sources[key] = struct{}{}
		if err := validateStoredPath(association.Root); err != nil {
			return fmt.Errorf("Source association %s/%s root: %w", association.ProjectID, association.SourceID, err)
		}
		if err := validateStoredPath(association.GitCommonDir); err != nil {
			return fmt.Errorf("Source association %s/%s Git common dir: %w", association.ProjectID, association.SourceID, err)
		}
		if association.GitCommonIdentity != "" && !filesystemIdentityPattern.MatchString(association.GitCommonIdentity) {
			return fmt.Errorf("Source association %s/%s has an invalid Git common identity", association.ProjectID, association.SourceID)
		}
		if association.LocatorHints == nil {
			return fmt.Errorf("Source association %s/%s locator_hints array is missing", association.ProjectID, association.SourceID)
		}
		seen := map[string]struct{}{}
		for _, hint := range association.LocatorHints {
			normalized, err := research.NormalizeSourceLocator(hint)
			if err != nil || normalized != hint {
				return fmt.Errorf("Source association %s/%s has an unsanitized locator hint", association.ProjectID, association.SourceID)
			}
			if _, duplicate := seen[hint]; duplicate {
				return fmt.Errorf("Source association %s/%s has duplicate locator hints", association.ProjectID, association.SourceID)
			}
			seen[hint] = struct{}{}
		}
		if !validObservationTime(association.ObservedAt) {
			return fmt.Errorf("Source association %s/%s has invalid observed_at", association.ProjectID, association.SourceID)
		}
	}
	return nil
}

func normalizeAssociations(associations *Associations) {
	if associations.Schema == "" {
		associations.Schema = AssociationSchema
	}
	if associations.Projects == nil {
		associations.Projects = []ProjectAssociation{}
	}
	if associations.Sources == nil {
		associations.Sources = []SourceAssociation{}
	}
	for index := range associations.Projects {
		associations.Projects[index].ObservedAt = associations.Projects[index].ObservedAt.UTC()
	}
	for index := range associations.Sources {
		associations.Sources[index].ObservedAt = associations.Sources[index].ObservedAt.UTC()
		if associations.Sources[index].LocatorHints == nil {
			associations.Sources[index].LocatorHints = []string{}
		}
	}
	sort.Slice(associations.Projects, func(left, right int) bool {
		return associations.Projects[left].ProjectID.String() < associations.Projects[right].ProjectID.String()
	})
	sort.Slice(associations.Sources, func(left, right int) bool {
		leftKey := sourceAssociationKey(associations.Sources[left].ProjectID, associations.Sources[left].SourceID)
		rightKey := sourceAssociationKey(associations.Sources[right].ProjectID, associations.Sources[right].SourceID)
		return leftKey < rightKey
	})
}

func cloneAssociations(associations Associations) Associations {
	out := Associations{Schema: associations.Schema}
	out.Projects = append([]ProjectAssociation{}, associations.Projects...)
	out.Sources = append([]SourceAssociation{}, associations.Sources...)
	for index := range out.Sources {
		out.Sources[index].LocatorHints = append([]string{}, out.Sources[index].LocatorHints...)
	}
	return out
}

func validateStoredPath(value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return errors.New("path must be clean, absolute, and valid UTF-8")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return errors.New("path contains control characters")
		}
	}
	return nil
}

func validObservationTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0 && value.Equal(value.UTC())
}

func sourceAssociationKey(projectID research.UUID, sourceID research.ID) string {
	return projectID.String() + "\x00" + sourceID.String()
}

func findProjectAssociation(associations Associations, id research.UUID) (ProjectAssociation, bool) {
	for _, association := range associations.Projects {
		if association.ProjectID == id {
			return association, true
		}
	}
	return ProjectAssociation{}, false
}

func (store *Store) gitRunner() gitx.Runner {
	if store != nil && store.git != nil {
		return store.git
	}
	return gitx.ExecRunner{}
}

func (store *Store) now() time.Time {
	if store != nil && store.clock != nil {
		return store.clock().UTC()
	}
	return time.Now().UTC()
}

func (store *Store) validateProjectInfo(ctx context.Context, expected *project.Info) (*project.Info, error) {
	if expected == nil || expected.Project() == nil || expected.Project().ProjectID.IsZero() {
		return nil, errors.New("canonical Project information is required")
	}
	start := expected.Repository.Root
	if start == "" && expected.Root != "" {
		start = filepath.Dir(expected.Root)
	}
	actual, err := project.DiscoverWithGit(ctx, start, store.gitRunner())
	if err != nil {
		return nil, fmt.Errorf("re-discover canonical workspace: %w", err)
	}
	if actual.Project().ProjectID != expected.Project().ProjectID {
		return nil, fmt.Errorf("canonical workspace Project identity changed: %w", ErrStaleAssociation)
	}
	for label, value := range map[string]string{
		"workspace root":   actual.Repository.Root,
		"experiments root": actual.Root,
		"Git common dir":   actual.Repository.GitCommonDir,
	} {
		if _, err := canonicalExistingDirectory(value, label); err != nil {
			return nil, err
		}
	}
	if expected.Repository.Root != "" {
		canonical, err := pathx.Canonical(expected.Repository.Root)
		if err != nil || canonical != actual.Repository.Root {
			return nil, fmt.Errorf("supplied workspace root differs from Git discovery: %w", errors.Join(ErrStaleAssociation, err))
		}
	}
	if expected.Repository.GitCommonDir != "" {
		canonical, err := pathx.Canonical(expected.Repository.GitCommonDir)
		if err != nil || canonical != actual.Repository.GitCommonDir {
			return nil, fmt.Errorf("supplied Git-common identity differs from Git discovery: %w", errors.Join(ErrStaleAssociation, err))
		}
	}
	return actual, nil
}

func (store *Store) resolveProjectAssociation(ctx context.Context, association ProjectAssociation) (*project.Info, error) {
	root, err := canonicalExistingDirectory(association.Root, "registered workspace root")
	if err != nil {
		return nil, staleProjectError(association, err)
	}
	common, err := canonicalExistingDirectory(association.GitCommonDir, "registered workspace Git common dir")
	if err != nil {
		return nil, staleProjectError(association, err)
	}
	info, err := project.DiscoverWithGit(ctx, root, store.gitRunner())
	if err != nil {
		return nil, staleProjectError(association, err)
	}
	if info.Project() == nil || info.Project().ProjectID != association.ProjectID || info.Repository.Root != root || info.Repository.GitCommonDir != common {
		return nil, staleProjectError(association, errors.New("Project or Git-common identity no longer matches"))
	}
	return info, nil
}

func staleProjectError(association ProjectAssociation, cause error) error {
	return fmt.Errorf("registered Project %s at %s is stale: %w", association.ProjectID, association.Root, errors.Join(ErrStaleAssociation, cause))
}

func canonicalExistingDirectory(value, label string) (string, error) {
	if err := validateStoredPath(value); err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	canonical, err := pathx.Canonical(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", label, err)
	}
	if canonical != value {
		return "", fmt.Errorf("%s is no longer canonical", label)
	}
	info, err := os.Lstat(value)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%s is not a real directory", label)
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(value)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", label, err)
	}
	defer root.Close()
	if err := pathx.VerifyRootPath(value, root); err != nil {
		return "", fmt.Errorf("verify %s: %w", label, err)
	}
	return value, nil
}

func loadCanonicalInventory(ctx context.Context, info *project.Info) (*record.Inventory, error) {
	if info == nil {
		return nil, errors.New("canonical Project information is required")
	}
	return record.NewStore(info.Root, info.Repository.GitCommonDir).Inventory(ctx)
}

func loadCanonicalSource(ctx context.Context, info *project.Info, sourceID research.ID) (*research.Source, error) {
	inventory, err := loadCanonicalInventory(ctx, info)
	if err != nil {
		return nil, fmt.Errorf("load canonical Source inventory: %w", err)
	}
	if !inventory.Valid() {
		return nil, &record.InventoryError{Diagnostics: append([]record.Diagnostic(nil), inventory.Diagnostics...)}
	}
	document, err := inventory.ByID(sourceID)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical Source %s: %w", sourceID, err)
	}
	source, ok := document.Record.(*research.Source)
	if !ok {
		return nil, fmt.Errorf("canonical record %s is not a Source", sourceID)
	}
	return research.Clone(source).(*research.Source), nil
}

// InspectSourceClone safely discovers one existing Git clone, validates the
// immutable Source subdir, and returns only sanitized remote locators. It makes
// no filesystem or association-state changes.
func InspectSourceClone(ctx context.Context, clonePath, subdir string, runner gitx.Runner) (SourceCloneObservation, error) {
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	normalizedSubdir, err := research.NormalizeSourceSubdir(subdir)
	if err != nil {
		return SourceCloneObservation{}, fmt.Errorf("normalize Source subdir: %w", err)
	}
	repository, err := gitx.DiscoverWithRunner(ctx, clonePath, runner)
	if err != nil {
		return SourceCloneObservation{}, fmt.Errorf("discover Source clone: %w", err)
	}
	root, err := canonicalExistingDirectory(repository.Root, "Source clone root")
	if err != nil {
		return SourceCloneObservation{}, fmt.Errorf("validate Source clone root: %w", err)
	}
	common, err := canonicalExistingDirectory(repository.GitCommonDir, "Source clone Git common dir")
	if err != nil {
		return SourceCloneObservation{}, err
	}
	commonIdentity, err := pathx.DirectoryFilesystemIdentity(common)
	if err != nil {
		return SourceCloneObservation{}, fmt.Errorf("identify Source clone Git common dir: %w", err)
	}
	resolvedSubdir, err := pathx.ResolveUnderNoSymlinks(root, normalizedSubdir, true)
	if err != nil {
		return SourceCloneObservation{}, fmt.Errorf("resolve Source subdir: %w", err)
	}
	info, err := os.Lstat(resolvedSubdir)
	if err != nil {
		return SourceCloneObservation{}, fmt.Errorf("inspect Source subdir: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return SourceCloneObservation{}, errors.New("Source subdir is not a real directory")
	}
	locators, err := sanitizedRemoteLocators(ctx, runner, root)
	if err != nil {
		return SourceCloneObservation{}, err
	}
	verifiedIdentity, err := pathx.DirectoryFilesystemIdentity(common)
	if err != nil || verifiedIdentity != commonIdentity {
		return SourceCloneObservation{}, fmt.Errorf("Source clone Git common identity changed during inspection: %w", errors.Join(ErrStaleAssociation, err))
	}
	repository.Root = root
	repository.GitCommonDir = common
	return SourceCloneObservation{
		Repository: repository, ResolvedRoot: resolvedSubdir, GitCommonIdentity: commonIdentity,
		LocatorHints: append([]string{}, locators...),
	}, nil
}

func (store *Store) validateSourceClone(ctx context.Context, source *research.Source, root, expectedCommon string, expectedObserved []string) (gitx.Repository, []string, string, error) {
	if source == nil || source.ID.IsZero() || source.ID.Kind() != research.KindSource {
		return gitx.Repository{}, nil, "", errors.New("canonical Source is invalid")
	}
	subdir, err := research.NormalizeSourceSubdir(source.Subdir)
	if err != nil || subdir != source.Subdir {
		return gitx.Repository{}, nil, "", fmt.Errorf("canonical Source %s has invalid subdir", source.ID)
	}
	for _, locator := range source.LocatorHints {
		normalized, normalizeErr := research.NormalizeSourceLocator(locator)
		if normalizeErr != nil || normalized != locator {
			return gitx.Repository{}, nil, "", fmt.Errorf("canonical Source %s has an unsanitized locator hint", source.ID)
		}
	}
	observation, err := InspectSourceClone(ctx, root, source.Subdir, store.gitRunner())
	if err != nil {
		return gitx.Repository{}, nil, "", fmt.Errorf("inspect Source %s clone: %w", source.ID, err)
	}
	repository := observation.Repository
	if expectedCommon != "" && repository.GitCommonDir != expectedCommon {
		return gitx.Repository{}, nil, "", fmt.Errorf("Source clone Git-common identity changed: %w", ErrStaleAssociation)
	}
	available := observation.LocatorHints
	matched := locatorIntersection(source.LocatorHints, available)
	if len(source.LocatorHints) > 0 && len(matched) == 0 {
		return gitx.Repository{}, nil, "", fmt.Errorf("Source %s clone has no matching sanitized remote: %w", source.ID, ErrLocatorMismatch)
	}
	if expectedObserved != nil {
		canonicalSet := make(map[string]struct{}, len(source.LocatorHints))
		for _, locator := range source.LocatorHints {
			canonicalSet[locator] = struct{}{}
		}
		for _, locator := range expectedObserved {
			normalized, normalizeErr := research.NormalizeSourceLocator(locator)
			if normalizeErr != nil || normalized != locator {
				return gitx.Repository{}, nil, "", errors.New("stored Source association has an unsanitized locator")
			}
			if _, stillCanonical := canonicalSet[locator]; !stillCanonical {
				return gitx.Repository{}, nil, "", fmt.Errorf("Source %s locator history no longer contains the registered observation: %w", source.ID, ErrStaleAssociation)
			}
		}
	}
	repository.Root = filepath.Clean(repository.Root)
	return repository, matched, observation.GitCommonIdentity, nil
}

func sanitizedRemoteLocators(ctx context.Context, runner gitx.Runner, root string) ([]string, error) {
	stdout, _, err := runner.Run(ctx, root, []string{"remote"})
	if err != nil {
		return nil, fmt.Errorf("inspect Source clone remote names: %w", err)
	}
	remoteNames := splitNonemptyLines(stdout)
	sort.Strings(remoteNames)
	seenNames := map[string]struct{}{}
	seenLocators := map[string]struct{}{}
	locators := make([]string, 0)
	for _, name := range remoteNames {
		if !safeGitRemoteName(name) {
			return nil, errors.New("Git returned an invalid remote name")
		}
		if _, duplicate := seenNames[name]; duplicate {
			continue
		}
		seenNames[name] = struct{}{}
		output, _, remoteErr := runner.Run(ctx, root, []string{"remote", "get-url", "--all", name})
		if remoteErr != nil {
			return nil, fmt.Errorf("inspect Source clone remote URL: %w", remoteErr)
		}
		for _, raw := range splitNonemptyLines(output) {
			normalized, normalizeErr := research.NormalizeSourceLocator(raw)
			if normalizeErr != nil {
				continue // Local-only and malformed remotes are not identity hints.
			}
			if _, duplicate := seenLocators[normalized]; duplicate {
				continue
			}
			seenLocators[normalized] = struct{}{}
			locators = append(locators, normalized)
		}
	}
	sort.Strings(locators)
	return locators, nil
}

func splitNonemptyLines(value string) []string {
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "\n")
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSuffix(part, "\r")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func safeGitRemoteName(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func locatorIntersection(canonical, available []string) []string {
	set := make(map[string]struct{}, len(available))
	for _, locator := range available {
		set[locator] = struct{}{}
	}
	matched := make([]string, 0)
	for _, locator := range canonical {
		if _, found := set[locator]; found {
			matched = append(matched, locator)
		}
	}
	return matched
}
