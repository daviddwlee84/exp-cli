// Package experimentgit prepares isolated Git worktrees for experiment agents
// and commits only an explicitly allowed change set. It never merges or removes
// branches; explicit cleanup removes only a verified clean-at-base worktree.
package experimentgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	maxTitleBytes = 512
	maxGlobBytes  = 512
	maxGlobs      = 128
)

var (
	ErrInvalidRequest    = errors.New("invalid experiment Git request")
	ErrDirtyBase         = errors.New("base worktree is not clean")
	ErrWorkspaceExists   = errors.New("experiment worktree already exists")
	ErrWorkspaceState    = errors.New("experiment worktree state is invalid")
	ErrNoChanges         = errors.New("experiment produced no changes")
	ErrPathNotAllowed    = errors.New("experiment changed a path outside its allowlist")
	ErrForbiddenMetadata = errors.New("experiment worktree contains forbidden Git metadata")
	ErrCleanupRefused    = errors.New("cleanup refuses a changed or unverified worktree")
)

var fullObjectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// Request is the stable input shared by Prepare and Commit. BaseCommit must be
// a full object ID; symbolic names and abbreviated hashes are deliberately not
// accepted.
type Request struct {
	RepositoryRoot   string
	BaseCommit       string
	ExperimentID     research.ID
	ExperimentTitle  string
	AllowedPathGlobs []string

	// Identity-aware callers provide the complete Project/Source/owner tuple.
	// ExperimentID/ExperimentTitle remain the legacy compatibility surface.
	ProjectID              research.UUID
	SourceID               research.ID
	OwnerID                research.ID
	OwnerTitle             string
	SourceSubdir           string
	RegisteredGitCommonDir string
	AllowedPaths           []string
	BaselinePaths          []string
	CanonicalMetadataRoot  string
	AllowDirtySource       bool
}

// Workspace describes the isolated checkout created for an experiment or Try.
type Workspace struct {
	Backend        string        `json:"workspace_backend,omitempty"`
	RepositoryRoot string        `json:"repository_root"`
	Worktree       string        `json:"worktree"`
	CWD            string        `json:"cwd"`
	BaseCommit     string        `json:"base_commit"`
	Branch         string        `json:"branch"`
	ProjectID      research.UUID `json:"project_id,omitempty"`
	SourceID       research.ID   `json:"source_id,omitempty"`
	OwnerID        research.ID   `json:"owner_id,omitempty"`
	SourceSubdir   string        `json:"source_subdir,omitempty"`
	AllowedGlobs   []string      `json:"allowed_globs"`
	AllowedPaths   []string      `json:"allowed_paths,omitempty"`
	BaselinePaths  []string      `json:"baseline_paths,omitempty"`
}

// ChangeSet is the exact commit identity produced by Commit.
type ChangeSet struct {
	Backend    string   `json:"workspace_backend,omitempty"`
	Worktree   string   `json:"worktree"`
	BaseCommit string   `json:"base_commit"`
	HeadCommit string   `json:"head_commit"`
	Branch     string   `json:"branch"`
	Paths      []string `json:"paths"`
	DiffDigest string   `json:"diff_digest"`
}

// Manager owns the injected Git and XDG resolution boundaries. Empty fields
// use installed Git, os.LookupEnv, and os.UserHomeDir.
type Manager struct {
	Git         gitx.Runner
	DataHome    string
	LookupEnv   func(string) (string, bool)
	UserHomeDir func() (string, error)
}

type normalizedRequest struct {
	repositoryRoot   string
	registeredCommon string
	baseCommit       string
	short            string
	slug             string
	branch           string
	globs            []string
	paths            []string
	baselinePaths    []string
	sourceSubdir     string
	metadataRoot     string
	projectNamespace string
	sourceNamespace  string
	workspaceLeaf    string
	projectID        research.UUID
	sourceID         research.ID
	ownerID          research.ID
	allowDirtySource bool
	identityAware    bool
}

// Prepare creates a new branch and linked worktree at the explicit base
// commit. The new checkout must be clean; legacy and formal callers also require
// a clean source checkout, while explicit dirty Try seeding may opt out.
func (manager Manager) Prepare(ctx context.Context, request Request) (Workspace, error) {
	if ctx == nil {
		return Workspace{}, fmt.Errorf("context is required: %w", ErrInvalidRequest)
	}
	normalized, err := normalizeRequest(request)
	if err != nil {
		return Workspace{}, err
	}
	runner := manager.gitRunner()
	repository, err := discoverExact(ctx, runner, normalized.repositoryRoot)
	if err != nil {
		return Workspace{}, err
	}
	if normalized.registeredCommon != "" && repository.GitCommonDir != normalized.registeredCommon {
		return Workspace{}, fmt.Errorf("registered Git common directory differs from discovery: %w", ErrWorkspaceState)
	}
	base, err := resolveCommit(ctx, runner, repository.Root, normalized.baseCommit)
	if err != nil {
		return Workspace{}, err
	}
	if !normalized.allowDirtySource {
		if err := requireCleanSource(ctx, runner, repository.Root); err != nil {
			return Workspace{}, fmt.Errorf("source checkout: %w", err)
		}
	}

	worktree, err := manager.prepareWorktreePath(ctx, runner, repository, normalized)
	if err != nil {
		return Workspace{}, err
	}
	if _, err := os.Lstat(worktree); err == nil {
		return Workspace{}, fmt.Errorf("%s: %w", worktree, ErrWorkspaceExists)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Workspace{}, fmt.Errorf("inspect experiment worktree: %w", err)
	}
	if _, err := runGit(ctx, runner, repository.Root, "-c", "core.hooksPath="+os.DevNull, "worktree", "add", "-b", normalized.branch, worktree, base); err != nil {
		return Workspace{}, fmt.Errorf("create experiment worktree: %w", err)
	}
	canonicalWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil || filepath.Clean(canonicalWorktree) != worktree {
		return Workspace{}, fmt.Errorf("verify experiment worktree path: %w", errors.Join(ErrWorkspaceState, err))
	}
	if err := requireWorkspaceIdentity(ctx, runner, worktree, base, normalized.branch); err != nil {
		return Workspace{}, err
	}
	if err := requireClean(ctx, runner, worktree); err != nil {
		return Workspace{}, fmt.Errorf("new experiment checkout: %w", err)
	}
	cwd, err := pathx.ResolveUnderNoSymlinks(worktree, normalized.sourceSubdir, true)
	if err != nil {
		return Workspace{}, fmt.Errorf("resolve Source semantic root in new worktree: %w", errors.Join(ErrWorkspaceState, err))
	}
	cwdInfo, err := os.Lstat(cwd)
	if err != nil || cwdInfo.Mode()&os.ModeSymlink != 0 || !cwdInfo.IsDir() {
		return Workspace{}, fmt.Errorf("Source semantic root is not a real directory: %w", errors.Join(ErrWorkspaceState, err))
	}
	return Workspace{
		RepositoryRoot: repository.Root,
		Worktree:       worktree,
		CWD:            cwd,
		BaseCommit:     base,
		Branch:         normalized.branch,
		ProjectID:      normalized.projectID,
		SourceID:       normalized.sourceID,
		OwnerID:        normalized.ownerID,
		SourceSubdir:   normalized.sourceSubdir,
		AllowedGlobs:   append([]string(nil), normalized.globs...),
		AllowedPaths:   append([]string(nil), normalized.paths...),
		BaselinePaths:  append([]string(nil), normalized.baselinePaths...),
	}, nil
}

// Commit validates the experiment diff, stages only the exact discovered
// paths, and creates one commit. It does not merge or remove the worktree.
func (manager Manager) Commit(ctx context.Context, request Request) (ChangeSet, error) {
	if ctx == nil {
		return ChangeSet{}, fmt.Errorf("context is required: %w", ErrInvalidRequest)
	}
	normalized, err := normalizeRequest(request)
	if err != nil {
		return ChangeSet{}, err
	}
	if normalized.identityAware && normalized.ownerID.Kind() != research.KindExperiment {
		return ChangeSet{}, fmt.Errorf("only formal Experiment owners may be committed: %w", ErrInvalidRequest)
	}
	runner := manager.gitRunner()
	repository, err := discoverExact(ctx, runner, normalized.repositoryRoot)
	if err != nil {
		return ChangeSet{}, err
	}
	if normalized.registeredCommon != "" && repository.GitCommonDir != normalized.registeredCommon {
		return ChangeSet{}, fmt.Errorf("registered Git common directory differs from discovery: %w", ErrWorkspaceState)
	}
	base, err := resolveCommit(ctx, runner, repository.Root, normalized.baseCommit)
	if err != nil {
		return ChangeSet{}, err
	}
	worktree, err := manager.existingWorktreePath(repository, normalized)
	if err != nil {
		return ChangeSet{}, err
	}
	checkout, err := discoverExact(ctx, runner, worktree)
	if err != nil {
		return ChangeSet{}, fmt.Errorf("discover experiment worktree: %w", err)
	}
	if checkout.GitCommonDir != repository.GitCommonDir {
		return ChangeSet{}, fmt.Errorf("worktree belongs to a different Git repository: %w", ErrWorkspaceState)
	}
	head, err := readHead(ctx, runner, worktree)
	if err != nil {
		return ChangeSet{}, err
	}
	if err := requireWorkspaceBranch(ctx, runner, worktree, normalized.branch); err != nil {
		return ChangeSet{}, err
	}
	if err := rejectForbiddenMetadata(worktree, normalized.metadataRoot); err != nil {
		return ChangeSet{}, err
	}
	if head != base {
		return committedChangeSet(ctx, runner, worktree, base, head, normalized)
	}

	paths, err := changedPaths(ctx, runner, worktree)
	if err != nil {
		return ChangeSet{}, err
	}
	if len(paths) == 0 {
		return ChangeSet{}, ErrNoChanges
	}
	for _, changed := range paths {
		if forbiddenPath(changed, normalized.metadataRoot) {
			return ChangeSet{}, fmt.Errorf("%s: %w", changed, ErrForbiddenMetadata)
		}
		if !allowedPath(normalized, changed) {
			return ChangeSet{}, fmt.Errorf("%s: %w", changed, ErrPathNotAllowed)
		}
	}

	addArgs := append([]string{"add", "--"}, paths...)
	if _, err := runGit(ctx, runner, worktree, addArgs...); err != nil {
		return ChangeSet{}, fmt.Errorf("stage exact experiment change set: %w", err)
	}
	stagedOutput, err := runGit(ctx, runner, worktree, "diff", "--cached", "--name-only", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		return ChangeSet{}, fmt.Errorf("verify staged experiment paths: %w", err)
	}
	staged, err := parseGitPaths(stagedOutput)
	if err != nil || !equalStrings(staged, paths) {
		return ChangeSet{}, fmt.Errorf("staged paths do not equal the validated change set: %w", errors.Join(ErrWorkspaceState, err))
	}

	message := fmt.Sprintf("Record experiment %s changes for %s", normalized.short, normalized.slug)
	if _, err := runGit(ctx, runner, worktree, "-c", "core.hooksPath="+os.DevNull, "commit", "--no-gpg-sign", "--no-verify", "-m", message); err != nil {
		return ChangeSet{}, fmt.Errorf("commit experiment change set: %w", err)
	}
	head, err = readHead(ctx, runner, worktree)
	partial := ChangeSet{Worktree: worktree, BaseCommit: base, HeadCommit: head, Branch: normalized.branch, Paths: append([]string(nil), paths...)}
	if err != nil {
		return partial, err
	}
	verified, err := committedChangeSet(ctx, runner, worktree, base, head, normalized)
	if err != nil {
		return partial, err
	}
	if !equalStrings(verified.Paths, paths) {
		return verified, fmt.Errorf("committed paths do not equal the validated change set: %w", ErrWorkspaceState)
	}
	return verified, nil
}

func (manager Manager) gitRunner() gitx.Runner {
	if manager.Git != nil {
		return manager.Git
	}
	return gitx.ExecRunner{}
}

func normalizeRequest(request Request) (normalizedRequest, error) {
	if request.RepositoryRoot == "" || !filepath.IsAbs(request.RepositoryRoot) || filepath.Clean(request.RepositoryRoot) != request.RepositoryRoot || !utf8.ValidString(request.RepositoryRoot) || strings.ContainsRune(request.RepositoryRoot, 0) {
		return normalizedRequest{}, fmt.Errorf("repository root must be a clean absolute path: %w", ErrInvalidRequest)
	}
	if !fullObjectID.MatchString(request.BaseCommit) {
		return normalizedRequest{}, fmt.Errorf("base commit must be a full lower-case object ID: %w", ErrInvalidRequest)
	}
	identityAware := !request.ProjectID.IsZero() || !request.SourceID.IsZero() || !request.OwnerID.IsZero() || request.OwnerTitle != "" || request.SourceSubdir != "" || request.RegisteredGitCommonDir != "" || len(request.AllowedPaths) != 0 || len(request.BaselinePaths) != 0 || request.CanonicalMetadataRoot != "" || request.AllowDirtySource
	if !identityAware {
		if request.ExperimentID.IsZero() || request.ExperimentID.Kind() != research.KindExperiment {
			return normalizedRequest{}, fmt.Errorf("experiment ID must be a typed Experiment ID: %w", ErrInvalidRequest)
		}
		if err := validateTitle(request.ExperimentTitle); err != nil {
			return normalizedRequest{}, err
		}
		globs, err := normalizeGlobs(request.AllowedPathGlobs, true)
		if err != nil {
			return normalizedRequest{}, err
		}
		// UUIDv7 prefixes are timestamp bits and collide for burst-created studies.
		// Use the full UUID payload so branch and worktree identity is exact.
		short := request.ExperimentID.UUIDHex()
		slug := slugify(request.ExperimentTitle, "experiment", 48)
		return normalizedRequest{
			repositoryRoot: request.RepositoryRoot, baseCommit: request.BaseCommit,
			short: short, slug: slug, branch: "exp/" + short + "-" + slug,
			globs: globs, sourceSubdir: ".", metadataRoot: "experiments",
			workspaceLeaf: short + "-" + slug,
		}, nil
	}

	if request.ProjectID.IsZero() || request.SourceID.IsZero() || request.SourceID.Kind() != research.KindSource || request.OwnerID.IsZero() {
		return normalizedRequest{}, fmt.Errorf("identity-aware requests require Project, Source, and owner IDs: %w", ErrInvalidRequest)
	}
	if !request.ExperimentID.IsZero() && request.ExperimentID != request.OwnerID {
		return normalizedRequest{}, fmt.Errorf("legacy Experiment ID conflicts with owner ID: %w", ErrInvalidRequest)
	}
	title := request.OwnerTitle
	if title == "" {
		title = request.ExperimentTitle
	}
	if err := validateTitle(title); err != nil {
		return normalizedRequest{}, err
	}
	if request.RegisteredGitCommonDir == "" || !filepath.IsAbs(request.RegisteredGitCommonDir) || filepath.Clean(request.RegisteredGitCommonDir) != request.RegisteredGitCommonDir || !utf8.ValidString(request.RegisteredGitCommonDir) || strings.ContainsRune(request.RegisteredGitCommonDir, 0) {
		return normalizedRequest{}, fmt.Errorf("registered Git common directory must be a clean absolute path: %w", ErrInvalidRequest)
	}
	subdir, err := research.NormalizeSourceSubdir(request.SourceSubdir)
	if err != nil {
		return normalizedRequest{}, fmt.Errorf("normalize Source subdir: %w", errors.Join(ErrInvalidRequest, err))
	}
	// An empty allowlist is an explicit read-only workspace. Changed paths are
	// still detected by Inspect and rejected because no path can match.
	globs, err := normalizeGlobs(request.AllowedPathGlobs, false)
	if err != nil {
		return normalizedRequest{}, err
	}
	paths, err := normalizeExactPaths(request.AllowedPaths)
	if err != nil {
		return normalizedRequest{}, err
	}
	baselinePaths, err := normalizeExactPaths(request.BaselinePaths)
	if err != nil {
		return normalizedRequest{}, err
	}
	metadataRoot := ""
	if request.CanonicalMetadataRoot != "" {
		metadataRoot, err = research.NormalizeSourceSubdir(request.CanonicalMetadataRoot)
		if err != nil || metadataRoot == "." {
			return normalizedRequest{}, fmt.Errorf("invalid canonical metadata root: %w", errors.Join(ErrInvalidRequest, err))
		}
	}
	owner := request.OwnerID.String()
	slug := slugify(title, "workspace", 48)
	projectNamespace := request.ProjectID.String()
	sourceNamespace := request.SourceID.String()
	leaf := owner + "-" + slug
	return normalizedRequest{
		repositoryRoot: request.RepositoryRoot, registeredCommon: request.RegisteredGitCommonDir,
		baseCommit: request.BaseCommit, short: owner, slug: slug,
		branch: "exp/" + projectNamespace + "/" + sourceNamespace + "/" + leaf,
		globs:  globs, paths: paths, baselinePaths: baselinePaths, sourceSubdir: subdir, metadataRoot: metadataRoot,
		projectNamespace: projectNamespace, sourceNamespace: sourceNamespace, workspaceLeaf: leaf,
		projectID: request.ProjectID, sourceID: request.SourceID, ownerID: request.OwnerID,
		allowDirtySource: request.AllowDirtySource, identityAware: true,
	}, nil
}

func validateTitle(title string) error {
	if title == "" || title != strings.TrimSpace(title) || len(title) > maxTitleBytes || !utf8.ValidString(title) {
		return fmt.Errorf("experiment title is empty, oversized, or invalid UTF-8: %w", ErrInvalidRequest)
	}
	for _, character := range title {
		if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
			return fmt.Errorf("experiment title contains control characters: %w", ErrInvalidRequest)
		}
	}
	return nil
}

func normalizeGlobs(input []string, required bool) ([]string, error) {
	if len(input) > maxGlobs || required && len(input) == 0 {
		return nil, fmt.Errorf("allowed path globs must contain %d..%d entries: %w", boolInt(required), maxGlobs, ErrInvalidRequest)
	}
	seen := make(map[string]struct{}, len(input))
	output := make([]string, 0, len(input))
	for _, glob := range input {
		if glob == "" || glob != strings.TrimSpace(glob) || len(glob) > maxGlobBytes || !utf8.ValidString(glob) || strings.ContainsRune(glob, '\\') {
			return nil, fmt.Errorf("invalid allowed path glob %q: %w", glob, ErrInvalidRequest)
		}
		for _, character := range glob {
			if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
				return nil, fmt.Errorf("allowed path glob contains control characters: %w", ErrInvalidRequest)
			}
		}
		if err := pathx.ValidateRelativePOSIX(glob, false); err != nil {
			return nil, fmt.Errorf("invalid allowed path glob %q: %w", glob, errors.Join(ErrInvalidRequest, err))
		}
		for _, component := range strings.Split(glob, "/") {
			if component == "**" {
				continue
			}
			if _, err := path.Match(component, "probe"); err != nil {
				return nil, fmt.Errorf("invalid allowed path glob %q: %w", glob, errors.Join(ErrInvalidRequest, err))
			}
		}
		if _, duplicate := seen[glob]; duplicate {
			continue
		}
		seen[glob] = struct{}{}
		output = append(output, glob)
	}
	sort.Strings(output)
	return output, nil
}

func normalizeExactPaths(input []string) ([]string, error) {
	if len(input) > maxGlobs {
		return nil, fmt.Errorf("allowed paths exceed %d entries: %w", maxGlobs, ErrInvalidRequest)
	}
	seen := make(map[string]struct{}, len(input))
	output := make([]string, 0, len(input))
	for _, value := range input {
		if len(value) > maxGlobBytes || strings.ContainsAny(value, "*?[") {
			return nil, fmt.Errorf("allowed path %q is not exact: %w", value, ErrInvalidRequest)
		}
		if err := research.ValidateCommittedPath(value, false); err != nil {
			return nil, fmt.Errorf("invalid allowed path %q: %w", value, errors.Join(ErrInvalidRequest, err))
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		output = append(output, value)
	}
	sort.Strings(output)
	return output, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func discoverExact(ctx context.Context, runner gitx.Runner, root string) (gitx.Repository, error) {
	canonical, err := pathx.Canonical(root)
	if err != nil {
		return gitx.Repository{}, fmt.Errorf("canonicalize repository root: %w", err)
	}
	repository, err := gitx.DiscoverWithRunner(ctx, root, runner)
	if err != nil {
		return gitx.Repository{}, err
	}
	if repository.Root != canonical {
		return gitx.Repository{}, fmt.Errorf("%s resolves inside repository %s rather than naming its root: %w", root, repository.Root, ErrInvalidRequest)
	}
	return repository, nil
}

func requireOutsideRegisteredWorktrees(ctx context.Context, runner gitx.Runner, repository gitx.Repository, candidate string) error {
	worktrees, err := gitx.Worktrees(ctx, repository.Root, runner)
	if err != nil {
		return fmt.Errorf("inspect registered Source worktrees: %w", err)
	}
	for _, worktree := range worktrees {
		inside, containErr := pathx.Contains(worktree.Root, candidate)
		if containErr != nil {
			return fmt.Errorf("check managed path against registered worktree %s: %w", worktree.Root, containErr)
		}
		if inside {
			return fmt.Errorf("XDG worktree path must be outside every registered Source worktree: %w", ErrInvalidRequest)
		}
	}
	return nil
}

func resolveCommit(ctx context.Context, runner gitx.Runner, root, base string) (string, error) {
	output, err := runGit(ctx, runner, root, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve explicit base commit: %w", err)
	}
	resolved, err := singleLine(output)
	if err != nil || resolved != base || !fullObjectID.MatchString(resolved) {
		return "", fmt.Errorf("base commit did not resolve to the exact requested object: %w", errors.Join(ErrInvalidRequest, err))
	}
	return resolved, nil
}

func (manager Manager) prepareWorktreePath(ctx context.Context, runner gitx.Runner, repository gitx.Repository, request normalizedRequest) (string, error) {
	dataHome, err := manager.resolveDataHome()
	if err != nil {
		return "", err
	}
	relativeParent, leaf := workspaceLocation(repository, request)
	proposed := filepath.Join(dataHome, filepath.FromSlash(relativeParent), leaf)
	inside, err := pathx.Contains(repository.Root, proposed)
	if err != nil {
		return "", fmt.Errorf("check worktree containment: %w", err)
	}
	if inside {
		return "", fmt.Errorf("XDG worktree path must be outside the source repository: %w", ErrInvalidRequest)
	}
	if request.identityAware {
		if err := requireOutsideRegisteredWorktrees(ctx, runner, repository, proposed); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dataHome, 0o700); err != nil {
		return "", fmt.Errorf("create XDG data home: %w", err)
	}
	dataRoot, err := pathx.OpenRootNoSymlinks(dataHome)
	if err != nil {
		return "", fmt.Errorf("open XDG data home: %w", err)
	}
	defer dataRoot.Close()
	parent, _, err := pathx.EnsureRootAtNoSymlinks(dataRoot, relativeParent, 0o700)
	if err != nil {
		return "", fmt.Errorf("create experiment worktree parent: %w", err)
	}
	if err := parent.Close(); err != nil {
		return "", fmt.Errorf("close experiment worktree parent: %w", err)
	}
	return filepath.Join(filepath.Clean(dataRoot.Name()), filepath.FromSlash(relativeParent), leaf), nil
}

func (manager Manager) existingWorktreePath(repository gitx.Repository, request normalizedRequest) (string, error) {
	dataHome, err := manager.resolveDataHome()
	if err != nil {
		return "", err
	}
	canonicalData, err := pathx.Canonical(dataHome)
	if err != nil {
		return "", fmt.Errorf("canonicalize XDG data home: %w", err)
	}
	relativeParent, leaf := workspaceLocation(repository, request)
	worktree := filepath.Join(canonicalData, filepath.FromSlash(relativeParent), leaf)
	info, err := os.Lstat(worktree)
	if err != nil {
		return "", fmt.Errorf("inspect experiment worktree: %w", errors.Join(ErrWorkspaceState, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("experiment worktree is not a regular directory: %w", ErrWorkspaceState)
	}
	canonicalWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil || filepath.Clean(canonicalWorktree) != worktree {
		return "", fmt.Errorf("canonicalize experiment worktree: %w", errors.Join(ErrWorkspaceState, err))
	}
	return worktree, nil
}

func workspaceLocation(repository gitx.Repository, request normalizedRequest) (string, string) {
	if request.identityAware {
		return path.Join("exp", "worktrees", request.projectNamespace, request.sourceNamespace), request.workspaceLeaf
	}
	return path.Join("exp", "worktrees", projectNamespace(repository)), request.workspaceLeaf
}

func projectNamespace(repository gitx.Repository) string {
	digest := sha256.Sum256([]byte(filepath.Clean(repository.GitCommonDir)))
	return slugify(repository.Name, "project", 48) + "-" + hex.EncodeToString(digest[:6])
}

func (manager Manager) resolveDataHome() (string, error) {
	dataHome := manager.DataHome
	if dataHome == "" {
		lookup := manager.LookupEnv
		if lookup == nil {
			lookup = os.LookupEnv
		}
		if configured, found := lookup("XDG_DATA_HOME"); found && configured != "" {
			dataHome = configured
		} else {
			home := manager.UserHomeDir
			if home == nil {
				home = os.UserHomeDir
			}
			resolved, err := home()
			if err != nil {
				return "", fmt.Errorf("resolve home directory: %w", err)
			}
			dataHome = filepath.Join(resolved, ".local", "share")
		}
	}
	if dataHome == "" || !filepath.IsAbs(dataHome) || filepath.Clean(dataHome) != dataHome || !utf8.ValidString(dataHome) || strings.ContainsRune(dataHome, 0) {
		return "", fmt.Errorf("XDG data home must be a clean absolute path: %w", ErrInvalidRequest)
	}
	return dataHome, nil
}

func requireWorkspaceIdentity(ctx context.Context, runner gitx.Runner, worktree, base, branch string) error {
	head, err := readHead(ctx, runner, worktree)
	if err != nil {
		return err
	}
	if head != base {
		return fmt.Errorf("worktree HEAD %s differs from base %s: %w", head, base, ErrWorkspaceState)
	}
	return requireWorkspaceBranch(ctx, runner, worktree, branch)
}

func requireWorkspaceBranch(ctx context.Context, runner gitx.Runner, worktree, branch string) error {
	output, err := runGit(ctx, runner, worktree, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return fmt.Errorf("read experiment branch: %w", err)
	}
	actual, err := singleLine(output)
	if err != nil || actual != branch {
		return fmt.Errorf("worktree branch %q differs from %q: %w", actual, branch, errors.Join(ErrWorkspaceState, err))
	}
	return nil
}

func committedChangeSet(ctx context.Context, runner gitx.Runner, worktree, base, head string, request normalizedRequest) (ChangeSet, error) {
	partial := ChangeSet{Worktree: worktree, BaseCommit: base, HeadCommit: head, Branch: request.branch}
	if err := requireSingleParent(ctx, runner, worktree, head, base); err != nil {
		return partial, err
	}
	committedOutput, err := runGit(ctx, runner, worktree, "diff", "--name-only", "-z", "--no-renames", base, head, "--")
	if err != nil {
		return partial, fmt.Errorf("inspect committed experiment paths: %w", err)
	}
	paths, err := parseGitPaths(committedOutput)
	if err != nil || len(paths) == 0 {
		return partial, fmt.Errorf("committed experiment path set is empty or invalid: %w", errors.Join(ErrWorkspaceState, err))
	}
	for _, changed := range paths {
		if forbiddenPath(changed, request.metadataRoot) {
			return partial, fmt.Errorf("%s: %w", changed, ErrForbiddenMetadata)
		}
		if !allowedPath(request, changed) {
			return partial, fmt.Errorf("%s: %w", changed, ErrPathNotAllowed)
		}
	}
	partial.Paths = paths
	if err := requireClean(ctx, runner, worktree); err != nil {
		return partial, fmt.Errorf("committed experiment checkout: %w", err)
	}
	framed := strings.Join(append([]string{"exp-change-set-v1", base, head}, paths...), "\x00")
	digest := sha256.Sum256([]byte(framed))
	partial.DiffDigest = "sha256:" + hex.EncodeToString(digest[:])
	return partial, nil
}

func readHead(ctx context.Context, runner gitx.Runner, worktree string) (string, error) {
	output, err := runGit(ctx, runner, worktree, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read experiment HEAD: %w", err)
	}
	head, err := singleLine(output)
	if err != nil || !fullObjectID.MatchString(head) {
		return "", fmt.Errorf("experiment HEAD is not a full object ID: %w", errors.Join(ErrWorkspaceState, err))
	}
	return head, nil
}

func requireCleanSource(ctx context.Context, runner gitx.Runner, directory string) error {
	output, err := runGit(ctx, runner, directory, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if output != "" {
		return ErrDirtyBase
	}
	return nil
}

func requireClean(ctx context.Context, runner gitx.Runner, directory string) error {
	output, err := runGit(ctx, runner, directory, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if output != "" {
		return ErrDirtyBase
	}
	return nil
}

func changedPaths(ctx context.Context, runner gitx.Runner, worktree string) ([]string, error) {
	trackedOutput, err := runGit(ctx, runner, worktree, "diff", "--name-only", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		return nil, fmt.Errorf("list tracked experiment changes: %w", err)
	}
	untrackedOutput, err := runGit(ctx, runner, worktree, "ls-files", "--others", "--exclude-standard", "-z", "--")
	if err != nil {
		return nil, fmt.Errorf("list untracked experiment changes: %w", err)
	}
	tracked, err := parseGitPaths(trackedOutput)
	if err != nil {
		return nil, err
	}
	untracked, err := parseGitPaths(untrackedOutput)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(tracked)+len(untracked))
	for _, name := range append(tracked, untracked...) {
		set[name] = struct{}{}
	}
	paths := make([]string, 0, len(set))
	for name := range set {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths, nil
}

func parseGitPaths(output string) ([]string, error) {
	if output == "" {
		return []string{}, nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return nil, fmt.Errorf("Git path output is not NUL-terminated: %w", ErrWorkspaceState)
	}
	fields := strings.Split(output[:len(output)-1], "\x00")
	paths := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, value := range fields {
		if err := validateGitPath(value); err != nil {
			return nil, err
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		paths = append(paths, value)
	}
	sort.Strings(paths)
	return paths, nil
}

func validateGitPath(value string) error {
	if err := pathx.ValidateRelativePOSIX(value, false); err != nil {
		return fmt.Errorf("unsafe Git path %q: %w", value, errors.Join(ErrWorkspaceState, err))
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
			return fmt.Errorf("Git path contains control characters: %w", ErrWorkspaceState)
		}
	}
	return nil
}

func rejectForbiddenMetadata(worktree, metadataRoot string) error {
	if metadataRoot == "" {
		return nil
	}
	metadata := filepath.Join(worktree, filepath.FromSlash(metadataRoot), ".git")
	if _, err := os.Lstat(metadata); err == nil {
		return fmt.Errorf("%s: %w", metadata, ErrForbiddenMetadata)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect forbidden Git metadata: %w", err)
	}
	return nil
}

func forbiddenPath(value, metadataRoot string) bool {
	if value == ".git" || strings.HasPrefix(value, ".git/") {
		return true
	}
	return metadataRoot != "" && (value == metadataRoot || strings.HasPrefix(value, metadataRoot+"/"))
}

func allowedPath(request normalizedRequest, repositoryPath string) bool {
	semantic, ok := semanticPath(request.sourceSubdir, repositoryPath)
	return ok && AllowsSemanticPath(request.paths, request.globs, semantic)
}

func baselinePath(request normalizedRequest, repositoryPath string) bool {
	semantic, ok := semanticPath(request.sourceSubdir, repositoryPath)
	if !ok {
		return false
	}
	index := sort.SearchStrings(request.baselinePaths, semantic)
	return index < len(request.baselinePaths) && request.baselinePaths[index] == semantic
}

// AllowsSemanticPath applies the same exact-path and recursive-glob semantics as
// managed-worktree inspection to one Source-relative path.
func AllowsSemanticPath(paths, globs []string, semantic string) bool {
	index := sort.SearchStrings(paths, semantic)
	if index < len(paths) && paths[index] == semantic {
		return true
	}
	return matchesAny(globs, semantic)
}

func semanticPath(subdir, repositoryPath string) (string, bool) {
	if subdir == "." {
		return repositoryPath, true
	}
	prefix := subdir + "/"
	if !strings.HasPrefix(repositoryPath, prefix) {
		return "", false
	}
	value := strings.TrimPrefix(repositoryPath, prefix)
	return value, value != ""
}

func matchesAny(globs []string, value string) bool {
	for _, glob := range globs {
		if matchGlob(glob, value) {
			return true
		}
	}
	return false
}

func matchGlob(glob, value string) bool {
	patternParts := strings.Split(glob, "/")
	valueParts := strings.Split(value, "/")
	type state struct{ pattern, value int }
	memo := make(map[state]bool)
	visited := make(map[state]bool)
	var match func(int, int) bool
	match = func(patternIndex, valueIndex int) bool {
		key := state{patternIndex, valueIndex}
		if visited[key] {
			return memo[key]
		}
		visited[key] = true
		matched := false
		switch {
		case patternIndex == len(patternParts):
			matched = valueIndex == len(valueParts)
		case patternParts[patternIndex] == "**":
			matched = match(patternIndex+1, valueIndex) || valueIndex < len(valueParts) && match(patternIndex, valueIndex+1)
		case valueIndex < len(valueParts):
			segment, err := path.Match(patternParts[patternIndex], valueParts[valueIndex])
			matched = err == nil && segment && match(patternIndex+1, valueIndex+1)
		}
		memo[key] = matched
		return matched
	}
	return match(0, 0)
}

func requireSingleParent(ctx context.Context, runner gitx.Runner, worktree, head, base string) error {
	output, err := runGit(ctx, runner, worktree, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return fmt.Errorf("inspect experiment commit parent: %w", err)
	}
	fields := strings.Fields(output)
	if len(fields) != 2 || fields[0] != head || fields[1] != base {
		return fmt.Errorf("experiment commit is not a single child of its base: %w", ErrWorkspaceState)
	}
	return nil
}

func runGit(ctx context.Context, runner gitx.Runner, directory string, args ...string) (string, error) {
	argumentCopy := append([]string(nil), args...)
	stdout, stderr, err := runner.Run(ctx, directory, argumentCopy)
	if err != nil {
		return "", &gitx.Error{Dir: directory, Args: argumentCopy, Stderr: stderr, Err: err}
	}
	return stdout, nil
}

func singleLine(output string) (string, error) {
	value := strings.TrimSuffix(output, "\n")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", ErrWorkspaceState
	}
	return value, nil
}

func slugify(value, fallback string, limit int) string {
	var builder strings.Builder
	separator := false
	for _, character := range strings.ToLower(value) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			if separator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			builder.WriteRune(character)
			separator = false
		case builder.Len() > 0:
			separator = true
		}
		if builder.Len() >= limit {
			break
		}
	}
	result := strings.Trim(builder.String(), "-")
	if len(result) > limit {
		result = strings.TrimRight(result[:limit], "-")
	}
	if result == "" {
		return fallback
	}
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
