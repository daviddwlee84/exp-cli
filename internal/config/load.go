package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/trust"
)

type identityDocument struct {
	Project *string `toml:"project"`
	Source  *string `toml:"source"`
}

type defaultsDocument struct {
	Source           *string `toml:"source"`
	WorkspaceBackend *string `toml:"workspace_backend"`
	AgentProfile     *string `toml:"agent_profile"`
	MLflowProfile    *string `toml:"mlflow_profile"`
}

type uiDocument struct {
	Color *string `toml:"color"`
}

type envBindingDocument struct {
	From     string `toml:"from"`
	Secret   bool   `toml:"secret"`
	Required bool   `toml:"required"`
}

type mlflowProfileDocument struct {
	Context        string                        `toml:"context"`
	Binary         string                        `toml:"binary"`
	Timeout        string                        `toml:"timeout"`
	Env            map[string]envBindingDocument `toml:"env"`
	DefaultMetrics *[]string                     `toml:"default_metrics"`
}

type mlflowDocument struct {
	Profiles map[string]mlflowProfileDocument `toml:"profiles"`
}

type backendDocument struct {
	Enabled  *bool `toml:"enabled"`
	Priority *int  `toml:"priority"`
}

type workspaceDocument struct {
	PreferredBackends *[]string                  `toml:"preferred_backends"`
	Backends          map[string]backendDocument `toml:"backends"`
}

type document struct {
	Schema    string            `toml:"schema"`
	Identity  identityDocument  `toml:"identity"`
	Defaults  defaultsDocument  `toml:"defaults"`
	UI        uiDocument        `toml:"ui"`
	MLflow    mlflowDocument    `toml:"mlflow"`
	Workspace workspaceDocument `toml:"workspace"`
}

// PathResolver supplies the global exp/config.toml path.
type PathResolver func() (string, error)

// Loader owns bounded discovery, strict decoding, merge, and trust annotation.
type Loader struct {
	userPath PathResolver
	getwd    func() (string, error)
	trust    TrustChecker
	builtins Effective
	maxBytes int64
	maxDepth int
}

// Option injects deterministic loader seams.
type Option func(*Loader)

func WithUserConfigPath(filename string) Option {
	return func(loader *Loader) { loader.userPath = func() (string, error) { return filename, nil } }
}

func WithUserPathResolver(resolve PathResolver) Option {
	return func(loader *Loader) { loader.userPath = resolve }
}

func WithGetwd(getwd func() (string, error)) Option {
	return func(loader *Loader) { loader.getwd = getwd }
}

func WithTrustChecker(checker TrustChecker) Option {
	return func(loader *Loader) { loader.trust = checker }
}

func WithBuiltins(value Effective) Option {
	return func(loader *Loader) { loader.builtins = cloneEffective(value) }
}

func WithMaxFileBytes(limit int64) Option {
	return func(loader *Loader) { loader.maxBytes = limit }
}

func WithMaxDepth(limit int) Option {
	return func(loader *Loader) { loader.maxDepth = limit }
}

func NewLoader(options ...Option) *Loader {
	loader := &Loader{
		userPath: DefaultUserConfigPath,
		getwd:    os.Getwd,
		builtins: Builtins(),
		maxBytes: MaxFileBytes,
		maxDepth: DefaultMaxDepth,
	}
	for _, option := range options {
		if option != nil {
			option(loader)
		}
	}
	if loader.userPath == nil {
		loader.userPath = DefaultUserConfigPath
	}
	if loader.getwd == nil {
		loader.getwd = os.Getwd
	}
	if loader.maxBytes <= 0 || loader.maxBytes > 64<<20 {
		loader.maxBytes = MaxFileBytes
	}
	if loader.maxDepth <= 0 || loader.maxDepth > 1024 {
		loader.maxDepth = DefaultMaxDepth
	}
	return loader
}

// Load uses a default Loader with optional seams.
func Load(ctx context.Context, request Request, options ...Option) (*Result, error) {
	return NewLoader(options...).Load(ctx, request)
}

// DefaultUserConfigPath returns $XDG_CONFIG_HOME/exp/config.toml.
func DefaultUserConfigPath() (string, error) {
	home, err := localstate.ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "exp", Filename), nil
}

type loadScope struct {
	projectID       research.UUID
	canonicalRoot   string
	canonicalCommon string
	sourceID        research.ID
	sourceRoot      string
	sourceCommon    string
	sourceSubdir    string
	resolvedSubdir  string
	invocationDir   string
}

type candidate struct {
	kind        LayerKind
	root        string
	relative    string
	absolute    string
	common      string
	configScope string
	user        bool
}

type loadedFile struct {
	candidate candidate
	content   []byte
	info      fs.FileInfo
	digest    string
}

type applyState struct {
	ctx        context.Context
	checker    TrustChecker
	scope      loadScope
	effective  Effective
	provenance map[string]Provenance
	layers     []Layer
	frames     []string
}

type lazyTrustSnapshot struct {
	source interface {
		Snapshot(context.Context) (trust.Checker, error)
	}
	once    sync.Once
	checker trust.Checker
	err     error
}

func (snapshot *lazyTrustSnapshot) Check(ctx context.Context, subject trust.Subject, digest string, capability trust.Capability) (bool, error) {
	snapshot.once.Do(func() {
		snapshot.checker, snapshot.err = snapshot.source.Snapshot(ctx)
	})
	if snapshot.err != nil {
		return false, fmt.Errorf("read configuration trust snapshot: %w", snapshot.err)
	}
	return snapshot.checker.Check(ctx, subject, digest, capability)
}

// Load discovers and applies built-in, user, canonical, Source-root, and
// root-to-leaf subdirectory layers in that exact order.
func (loader *Loader) Load(ctx context.Context, request Request) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scope, err := loader.normalizeScope(request)
	if err != nil {
		return nil, err
	}
	if err := validateEffective(loader.builtins); err != nil {
		return nil, fmt.Errorf("validate built-in config: %w", err)
	}
	checker := loader.trust
	if snapshotter, ok := checker.(interface {
		Snapshot(context.Context) (trust.Checker, error)
	}); ok {
		checker = &lazyTrustSnapshot{source: snapshotter}
	}
	state := &applyState{
		ctx:        ctx,
		checker:    checker,
		scope:      scope,
		effective:  cloneEffective(loader.builtins),
		provenance: map[string]Provenance{},
	}
	if err := state.seedBuiltins(); err != nil {
		return nil, err
	}
	candidates, err := loader.candidates(request, scope)
	if err != nil {
		return nil, err
	}
	seenPaths := map[string]struct{}{}
	seenFiles := make([]fs.FileInfo, 0)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if candidate.absolute != "" {
			if _, duplicate := seenPaths[candidate.absolute]; duplicate {
				continue
			}
		}
		loaded, present, err := loader.readCandidate(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		duplicate := false
		for _, previous := range seenFiles {
			if os.SameFile(previous, loaded.info) {
				duplicate = true
				break
			}
		}
		if duplicate {
			seenPaths[loaded.candidate.absolute] = struct{}{}
			continue
		}
		seenPaths[loaded.candidate.absolute] = struct{}{}
		seenFiles = append(seenFiles, loaded.info)
		decoded, err := decode(loaded.content, loaded.candidate.absolute)
		if err != nil {
			return nil, err
		}
		if err := state.apply(decoded, loaded); err != nil {
			return nil, err
		}
		if len(state.layers) > MaxLayers {
			return nil, fmt.Errorf("configuration applies more than %d layers", MaxLayers)
		}
	}
	if err := validateEffective(state.effective); err != nil {
		return nil, fmt.Errorf("validate effective config: %w", err)
	}
	legacy, err := legacyPaths(ctx, request, scope, checker)
	if err != nil {
		return nil, err
	}
	result := &Result{
		Effective:  cloneEffective(state.effective),
		Layers:     cloneLayers(state.layers),
		Provenance: cloneProvenance(state.provenance),
		Digest:     combinedDigest(state.frames),
		Legacy:     legacy,
	}
	return result, nil
}

func (loader *Loader) normalizeScope(request Request) (loadScope, error) {
	var scope loadScope
	if request.Project != nil {
		projectRecord := request.Project.Project()
		if projectRecord == nil || projectRecord.ProjectID.IsZero() {
			return loadScope{}, errors.New("config requires valid canonical Project information")
		}
		scope.projectID = projectRecord.ProjectID
		scope.canonicalRoot = request.Project.Repository.Root
		scope.canonicalCommon = request.Project.Repository.GitCommonDir
	}
	if !request.ProjectID.IsZero() {
		if !scope.projectID.IsZero() && scope.projectID != request.ProjectID {
			return loadScope{}, fmt.Errorf("request Project identities disagree: %w", ErrIdentityConflict)
		}
		scope.projectID = request.ProjectID
	}
	if request.CanonicalRoot != "" {
		if scope.canonicalRoot != "" && !sameCanonicalPath(scope.canonicalRoot, request.CanonicalRoot) {
			return loadScope{}, fmt.Errorf("request canonical roots disagree: %w", ErrIdentityConflict)
		}
		scope.canonicalRoot = request.CanonicalRoot
	}
	if request.CanonicalGitCommonDir != "" {
		if scope.canonicalCommon != "" && !sameCanonicalPath(scope.canonicalCommon, request.CanonicalGitCommonDir) {
			return loadScope{}, fmt.Errorf("request Git-common identities disagree: %w", ErrIdentityConflict)
		}
		scope.canonicalCommon = request.CanonicalGitCommonDir
	}
	if scope.projectID.IsZero() || scope.canonicalRoot == "" || scope.canonicalCommon == "" {
		return loadScope{}, errors.New("config requires resolved Project UUID, canonical root, and Git-common identity")
	}
	var err error
	scope.canonicalRoot, err = canonicalDirectory(scope.canonicalRoot, "canonical workspace root")
	if err != nil {
		return loadScope{}, err
	}
	scope.canonicalCommon, err = canonicalDirectory(scope.canonicalCommon, "canonical workspace Git common dir")
	if err != nil {
		return loadScope{}, err
	}

	if request.Source != nil {
		if request.Source.ID.IsZero() || request.Source.ID.Kind() != research.KindSource {
			return loadScope{}, errors.New("config Source record has invalid identity")
		}
		scope.sourceID = request.Source.ID
		scope.sourceSubdir = request.Source.Subdir
	}
	if !request.SourceID.IsZero() {
		if request.SourceID.Kind() != research.KindSource {
			return loadScope{}, errors.New("config Source ID has the wrong kind")
		}
		if !scope.sourceID.IsZero() && scope.sourceID != request.SourceID {
			return loadScope{}, fmt.Errorf("request Source identities disagree: %w", ErrIdentityConflict)
		}
		scope.sourceID = request.SourceID
	}
	if request.SourceSubdir != "" {
		if scope.sourceSubdir != "" && scope.sourceSubdir != request.SourceSubdir {
			return loadScope{}, fmt.Errorf("request Source subdirs disagree: %w", ErrIdentityConflict)
		}
		scope.sourceSubdir = request.SourceSubdir
	}
	if request.SourceRoot != "" {
		scope.sourceRoot = request.SourceRoot
	}
	if request.SourceGitCommonDir != "" {
		scope.sourceCommon = request.SourceGitCommonDir
	}
	if !scope.sourceID.IsZero() {
		if scope.sourceRoot == "" || scope.sourceCommon == "" {
			return loadScope{}, errors.New("selected Source config requires clone root and Git-common identity")
		}
		if scope.sourceSubdir == "" {
			scope.sourceSubdir = "."
		}
		normalized, normalizeErr := research.NormalizeSourceSubdir(scope.sourceSubdir)
		if normalizeErr != nil {
			return loadScope{}, fmt.Errorf("normalize selected Source subdir: %w", normalizeErr)
		}
		if normalized != scope.sourceSubdir {
			return loadScope{}, errors.New("selected Source subdir is not canonical")
		}
		scope.sourceRoot, err = canonicalDirectory(scope.sourceRoot, "Source clone root")
		if err != nil {
			return loadScope{}, err
		}
		scope.sourceCommon, err = canonicalDirectory(scope.sourceCommon, "Source clone Git common dir")
		if err != nil {
			return loadScope{}, err
		}
		scope.resolvedSubdir, err = pathx.ResolveUnderNoSymlinks(scope.sourceRoot, scope.sourceSubdir, true)
		if err != nil {
			return loadScope{}, fmt.Errorf("resolve Source subdir for config: %w", err)
		}
		if _, err := canonicalDirectory(scope.resolvedSubdir, "resolved Source subdir"); err != nil {
			return loadScope{}, err
		}
	} else if scope.sourceRoot != "" || scope.sourceCommon != "" || scope.sourceSubdir != "" {
		return loadScope{}, errors.New("Source paths were provided without a selected Source ID")
	}

	invocation := request.InvocationDir
	if invocation == "" {
		if loader.getwd == nil {
			return loadScope{}, errors.New("config current-directory lookup is not configured")
		}
		invocation, err = loader.getwd()
		if err != nil {
			return loadScope{}, fmt.Errorf("resolve config invocation directory: %w", err)
		}
	}
	scope.invocationDir, err = canonicalDirectory(invocation, "invocation directory")
	if err != nil {
		return loadScope{}, err
	}
	if !scope.sourceID.IsZero() {
		inside, containErr := pathx.Contains(scope.resolvedSubdir, scope.invocationDir)
		if containErr != nil || !inside {
			return loadScope{}, fmt.Errorf("invocation directory is outside selected Source subdir: %w", errors.Join(pathx.ErrOutsideRoot, containErr))
		}
		relative, relErr := filepath.Rel(scope.sourceRoot, scope.invocationDir)
		if relErr != nil {
			return loadScope{}, relErr
		}
		resolved, resolveErr := pathx.ResolveUnderNoSymlinks(scope.sourceRoot, filepath.ToSlash(relative), true)
		if resolveErr != nil || resolved != scope.invocationDir {
			return loadScope{}, fmt.Errorf("invocation directory contains a symlink or changed identity: %w", errors.Join(pathx.ErrSymlink, resolveErr))
		}
	}
	return scope, nil
}

func (loader *Loader) candidates(request Request, scope loadScope) ([]candidate, error) {
	userPath := request.UserConfigPath
	if userPath == "" {
		var err error
		userPath, err = loader.userPath()
		if err != nil {
			return nil, fmt.Errorf("resolve user config path: %w", err)
		}
	}
	if !filepath.IsAbs(userPath) || filepath.Clean(userPath) != userPath {
		return nil, errors.New("user config path must be a clean absolute path")
	}
	canonicalUser, err := canonicalOptionalFilePath(userPath)
	if err != nil {
		return nil, fmt.Errorf("resolve user config path: %w", err)
	}
	out := []candidate{
		{kind: LayerUser, absolute: canonicalUser, user: true},
		{
			kind: LayerCanonical, root: scope.canonicalRoot, relative: RelativePath,
			absolute: filepath.Join(scope.canonicalRoot, filepath.FromSlash(RelativePath)),
			common:   scope.canonicalCommon, configScope: RelativePath,
		},
	}
	if scope.sourceID.IsZero() {
		return out, nil
	}
	out = append(out, candidate{
		kind: LayerSource, root: scope.sourceRoot, relative: RelativePath,
		absolute: filepath.Join(scope.sourceRoot, filepath.FromSlash(RelativePath)),
		common:   scope.sourceCommon, configScope: RelativePath,
	})
	relative, err := filepath.Rel(scope.resolvedSubdir, scope.invocationDir)
	if err != nil {
		return nil, err
	}
	components := []string{}
	if relative != "." {
		components = strings.Split(filepath.Clean(relative), string(filepath.Separator))
	}
	if len(components) > loader.maxDepth {
		return nil, fmt.Errorf("Source subdir to invocation has depth %d; limit is %d: %w", len(components), loader.maxDepth, ErrTooDeep)
	}
	current := scope.resolvedSubdir
	directories := []string{current}
	for _, component := range components {
		current = filepath.Join(current, component)
		directories = append(directories, current)
	}
	for _, directory := range directories {
		directoryRelative, relErr := filepath.Rel(scope.sourceRoot, directory)
		if relErr != nil {
			return nil, relErr
		}
		configRelative := path.Join(filepath.ToSlash(directoryRelative), RelativePath)
		if directoryRelative == "." {
			configRelative = RelativePath
		}
		if err := pathx.ValidateRelativePOSIX(configRelative, false); err != nil {
			return nil, fmt.Errorf("construct subdir config path: %w", err)
		}
		out = append(out, candidate{
			kind: LayerSubdir, root: scope.sourceRoot, relative: configRelative,
			absolute: filepath.Join(scope.sourceRoot, filepath.FromSlash(configRelative)),
			common:   scope.sourceCommon, configScope: configRelative,
		})
	}
	return out, nil
}

func (loader *Loader) readCandidate(ctx context.Context, value candidate) (loadedFile, bool, error) {
	var content []byte
	var info fs.FileInfo
	var err error
	if value.user {
		content, info, err = readOptionalAbsolute(ctx, value.absolute, loader.maxBytes)
	} else {
		content, info, err = readOptionalUnder(ctx, value.root, value.relative, loader.maxBytes)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return loadedFile{}, false, nil
	}
	if err != nil {
		return loadedFile{}, false, fmt.Errorf("read %s config %s: %w", value.kind, value.absolute, err)
	}
	if info == nil {
		return loadedFile{}, false, nil
	}
	sum := sha256.Sum256(content)
	return loadedFile{
		candidate: value,
		content:   append([]byte(nil), content...),
		info:      info,
		digest:    "sha256:" + hex.EncodeToString(sum[:]),
	}, true, nil
}

func decode(content []byte, source string) (document, error) {
	var decoded document
	metadata, err := toml.Decode(string(content), &decoded)
	if err != nil {
		return document{}, fmt.Errorf("decode config %s: %w", source, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for index := range undecoded {
			keys[index] = undecoded[index].String()
		}
		sort.Strings(keys)
		return document{}, fmt.Errorf("config %s contains unknown field %q", source, keys[0])
	}
	if decoded.Schema != Schema {
		return document{}, fmt.Errorf("config %s schema must be %q", source, Schema)
	}
	return decoded, nil
}

func (state *applyState) seedBuiltins() error {
	encoded, err := json.Marshal(state.effective)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(encoded)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	fields := builtinFieldNames(state.effective)
	for _, field := range fields {
		state.provenance[field] = Provenance{Source: "<built-in>", Layer: LayerBuiltin, Digest: digest, Trusted: true}
	}
	state.layers = append(state.layers, Layer{
		Kind: LayerBuiltin, Source: "<built-in>", Digest: digest, Trusted: true,
		Capabilities: []trust.Capability{}, Fields: append([]string{}, fields...),
	})
	state.frames = append(state.frames, string(LayerBuiltin)+"\x00<built-in>\x00"+digest)
	return nil
}

func builtinFieldNames(value Effective) []string {
	fields := []string{
		"defaults.source", "defaults.workspace_backend", "defaults.agent_profile", "defaults.mlflow_profile",
		"ui.color", "workspace.preferred_backends",
	}
	for name, profile := range value.MLflow.Profiles {
		fields = append(fields, profileFieldNames(name, profile)...)
	}
	for name := range value.Workspace.Backends {
		fields = append(fields, "workspace.backends."+name+".enabled", "workspace.backends."+name+".priority")
	}
	sort.Strings(fields)
	return fields
}

func (state *applyState) apply(decoded document, loaded loadedFile) error {
	if err := state.validateIdentity(decoded.Identity, loaded.candidate.absolute); err != nil {
		return err
	}
	layer := Layer{Kind: loaded.candidate.kind, Source: loaded.candidate.absolute, Digest: loaded.digest}
	if loaded.candidate.kind != LayerBuiltin && loaded.candidate.kind != LayerUser {
		subject := trust.Subject{
			ProjectID: state.scope.projectID, SourceID: state.scope.sourceID,
			GitCommonDir: loaded.candidate.common, ConfigScope: loaded.candidate.configScope,
		}
		layer.TrustSubject = &subject
	}
	capabilityTrust := map[trust.Capability]bool{}
	fieldSet := map[string]struct{}{}
	capabilitySet := map[trust.Capability]bool{}
	set := func(field string, capability trust.Capability) (bool, error) {
		trusted := true
		if capability != "" {
			capabilitySet[capability] = true
			if value, found := capabilityTrust[capability]; found {
				trusted = value
			} else {
				value, err := state.layerTrusted(loaded, capability)
				if err != nil {
					return false, err
				}
				capabilityTrust[capability] = value
				trusted = value
			}
		}
		state.provenance[field] = Provenance{
			Source: loaded.candidate.absolute, Layer: loaded.candidate.kind, Digest: loaded.digest,
			Trusted: trusted, Capability: capability,
		}
		fieldSet[field] = struct{}{}
		return trusted, nil
	}
	if decoded.Identity.Project != nil {
		if _, err := set("identity.project", ""); err != nil {
			return err
		}
	}
	if decoded.Identity.Source != nil {
		if _, err := set("identity.source", ""); err != nil {
			return err
		}
	}
	if decoded.Defaults.Source != nil {
		state.effective.Defaults.Source = *decoded.Defaults.Source
		if _, err := set("defaults.source", trust.CapabilitySourceSelection); err != nil {
			return err
		}
	}
	if decoded.Defaults.WorkspaceBackend != nil {
		state.effective.Defaults.WorkspaceBackend = *decoded.Defaults.WorkspaceBackend
		if _, err := set("defaults.workspace_backend", trust.CapabilityWorkspaceBackend); err != nil {
			return err
		}
	}
	if decoded.Defaults.AgentProfile != nil {
		state.effective.Defaults.AgentProfile = *decoded.Defaults.AgentProfile
		if _, err := set("defaults.agent_profile", trust.CapabilityAgentProfile); err != nil {
			return err
		}
	}
	if decoded.Defaults.MLflowProfile != nil {
		state.effective.Defaults.MLflowProfile = *decoded.Defaults.MLflowProfile
		if _, err := set("defaults.mlflow_profile", trust.CapabilityMLflowProfile); err != nil {
			return err
		}
	}
	if decoded.UI.Color != nil {
		state.effective.UI.Color = ColorMode(*decoded.UI.Color)
		if _, err := set("ui.color", ""); err != nil {
			return err
		}
	}
	profileNames := make([]string, 0, len(decoded.MLflow.Profiles))
	for name := range decoded.MLflow.Profiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	for _, name := range profileNames {
		profile, err := materializeMLflowProfile(decoded.MLflow.Profiles[name])
		if err != nil {
			return fmt.Errorf("config %s MLflow profile %s: %w", loaded.candidate.absolute, name, err)
		}
		if !validName(name) {
			return fmt.Errorf("config %s has invalid MLflow profile name %q", loaded.candidate.absolute, name)
		}
		deleteProvenancePrefix(state.provenance, "mlflow.profiles."+name)
		state.effective.MLflow.Profiles[name] = profile
		for _, field := range profileFieldNames(name, profile) {
			if _, err := set(field, trust.CapabilityMLflowProfile); err != nil {
				return err
			}
		}
	}
	if decoded.Workspace.PreferredBackends != nil {
		state.effective.Workspace.PreferredBackends = append([]string{}, (*decoded.Workspace.PreferredBackends)...)
		if _, err := set("workspace.preferred_backends", trust.CapabilityWorkspaceBackend); err != nil {
			return err
		}
	}
	backendNames := make([]string, 0, len(decoded.Workspace.Backends))
	for name := range decoded.Workspace.Backends {
		backendNames = append(backendNames, name)
	}
	sort.Strings(backendNames)
	for _, name := range backendNames {
		if !validName(name) {
			return fmt.Errorf("config %s has invalid workspace backend name %q", loaded.candidate.absolute, name)
		}
		override := decoded.Workspace.Backends[name]
		backend := state.effective.Workspace.Backends[name]
		if override.Enabled != nil {
			backend.Enabled = *override.Enabled
			if _, err := set("workspace.backends."+name+".enabled", trust.CapabilityWorkspaceBackend); err != nil {
				return err
			}
		}
		if override.Priority != nil {
			backend.Priority = *override.Priority
			if _, err := set("workspace.backends."+name+".priority", trust.CapabilityWorkspaceBackend); err != nil {
				return err
			}
		}
		state.effective.Workspace.Backends[name] = backend
	}
	fields := make([]string, 0, len(fieldSet))
	for field := range fieldSet {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	layer.Fields = fields
	layer.Capabilities = sortedCapabilities(capabilitySet)
	layer.Trusted = true
	for capability := range capabilitySet {
		if !capabilityTrust[capability] {
			layer.Trusted = false
			break
		}
	}
	state.layers = append(state.layers, layer)
	state.frames = append(state.frames, string(layer.Kind)+"\x00"+loaded.candidate.configScope+"\x00"+loaded.digest)
	return nil
}

func (state *applyState) validateIdentity(identity identityDocument, source string) error {
	if identity.Project != nil {
		parsed, err := research.ParseUUID(*identity.Project)
		if err != nil || parsed != state.scope.projectID {
			return fmt.Errorf("config %s identity.project does not equal resolved Project %s: %w", source, state.scope.projectID, errors.Join(ErrIdentityConflict, err))
		}
	}
	if identity.Source != nil {
		parsed, err := research.ParseIDForKind(*identity.Source, research.KindSource)
		if err != nil || state.scope.sourceID.IsZero() || parsed != state.scope.sourceID {
			return fmt.Errorf("config %s identity.source does not equal resolved Source %s: %w", source, state.scope.sourceID, errors.Join(ErrIdentityConflict, err))
		}
	}
	return nil
}

func (state *applyState) layerTrusted(loaded loadedFile, capability trust.Capability) (bool, error) {
	if loaded.candidate.kind == LayerBuiltin || loaded.candidate.kind == LayerUser {
		return true, nil
	}
	if state.checker == nil {
		return false, nil
	}
	subject := trust.Subject{
		ProjectID: state.scope.projectID, SourceID: state.scope.sourceID,
		GitCommonDir: loaded.candidate.common, ConfigScope: loaded.candidate.configScope,
	}
	trusted, err := state.checker.Check(state.ctx, subject, loaded.digest, capability)
	if err != nil {
		return false, fmt.Errorf("inspect trust for config %s capability %s: %w", loaded.candidate.absolute, capability, err)
	}
	return trusted, nil
}

func materializeMLflowProfile(value mlflowProfileDocument) (MLflowProfile, error) {
	profile := MLflowProfile{
		Context: value.Context, Binary: value.Binary, Timeout: 30 * time.Second,
		Env: map[string]EnvBinding{}, DefaultMetrics: []string{},
	}
	if profile.Binary == "" {
		profile.Binary = "mlflow"
	}
	if value.Timeout != "" {
		parsed, err := time.ParseDuration(value.Timeout)
		if err != nil {
			return MLflowProfile{}, errors.New("timeout must be a valid duration")
		}
		profile.Timeout = parsed
	}
	for child, binding := range value.Env {
		profile.Env[child] = EnvBinding{From: binding.From, Secret: binding.Secret, Required: binding.Required}
	}
	if value.DefaultMetrics != nil {
		profile.DefaultMetrics = append([]string{}, (*value.DefaultMetrics)...)
	}
	if err := validateMLflowProfile(profile); err != nil {
		return MLflowProfile{}, err
	}
	return profile, nil
}

func profileFieldNames(name string, profile MLflowProfile) []string {
	prefix := "mlflow.profiles." + name
	fields := []string{prefix, prefix + ".context", prefix + ".binary", prefix + ".timeout", prefix + ".env", prefix + ".default_metrics"}
	for child := range profile.Env {
		fields = append(fields,
			prefix+".env."+child+".from",
			prefix+".env."+child+".secret",
			prefix+".env."+child+".required",
		)
	}
	sort.Strings(fields)
	return fields
}

func deleteProvenancePrefix(provenance map[string]Provenance, prefix string) {
	for field := range provenance {
		if field == prefix || strings.HasPrefix(field, prefix+".") {
			delete(provenance, field)
		}
	}
}

func readOptionalUnder(ctx context.Context, rootPath, relative string, maxBytes int64) ([]byte, fs.FileInfo, error) {
	if err := pathx.ValidateRelativePOSIX(relative, false); err != nil {
		return nil, nil, err
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(rootPath)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	parentRelative := path.Dir(relative)
	parent, err := pathx.OpenRootAtNoSymlinks(root, parentRelative)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer parent.Close()
	name := path.Base(relative)
	if _, err := parent.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	content, info, err := pathx.ReadBoundedRegularFile(ctx, parent, name, maxBytes)
	if err != nil {
		return nil, nil, err
	}
	if err := errors.Join(pathx.VerifyRootAt(root, parentRelative, parent), pathx.VerifyRootPath(rootPath, root)); err != nil {
		return nil, nil, err
	}
	return content, info, nil
}

func readOptionalAbsolute(ctx context.Context, filename string, maxBytes int64) ([]byte, fs.FileInfo, error) {
	parent := filepath.Dir(filename)
	canonicalParent, err := pathx.Canonical(parent)
	if err != nil {
		return nil, nil, err
	}
	if canonicalParent != parent {
		return nil, nil, pathx.ErrSymlink
	}
	info, err := os.Lstat(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect config parent: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, nil, errors.New("config parent is not a real directory")
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(parent)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	name := filepath.Base(filename)
	if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	return pathx.ReadBoundedRegularFile(ctx, root, name, maxBytes)
}

func canonicalOptionalFilePath(filename string) (string, error) {
	if filename == "" || !filepath.IsAbs(filename) || filepath.Clean(filename) != filename {
		return "", errors.New("config path must be a clean absolute path")
	}
	parent := filepath.Dir(filename)
	canonicalParent, err := pathx.Canonical(parent)
	if err != nil {
		return "", err
	}
	if canonicalParent != parent {
		return "", fmt.Errorf("user config parent contains a symlink: %w", pathx.ErrSymlink)
	}
	return filename, nil
}

func canonicalDirectory(value, label string) (string, error) {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", fmt.Errorf("%s must be a clean absolute path", label)
	}
	canonical, err := pathx.Canonical(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", label, err)
	}
	if canonical != value {
		return "", fmt.Errorf("%s is not canonical", label)
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

func sameCanonicalPath(left, right string) bool {
	leftCanonical, leftErr := pathx.Canonical(left)
	rightCanonical, rightErr := pathx.Canonical(right)
	return leftErr == nil && rightErr == nil && leftCanonical == rightCanonical
}

func legacyPaths(ctx context.Context, request Request, scope loadScope, checker TrustChecker) (LegacyPaths, error) {
	agentPath := request.AgentConfigPath
	if agentPath == "" {
		home, err := localstate.ConfigHome()
		if err != nil {
			return LegacyPaths{}, err
		}
		agentPath = filepath.Join(home, "exp", "agents.toml")
	} else if !filepath.IsAbs(agentPath) {
		absolute, err := filepath.Abs(agentPath)
		if err != nil {
			return LegacyPaths{}, err
		}
		agentPath = absolute
	}
	agentPath = filepath.Clean(agentPath)

	runtimePath := request.RuntimeConfigPath
	if runtimePath == "" {
		runtimePath = filepath.Join(scope.canonicalRoot, ".exp", "runtime.json")
	} else if filepath.IsAbs(runtimePath) {
		runtimePath = filepath.Clean(runtimePath)
	} else {
		relative := filepath.ToSlash(runtimePath)
		if err := pathx.ValidateRelativePOSIX(relative, false); err != nil {
			return LegacyPaths{}, fmt.Errorf("runtime config path: %w", err)
		}
		runtimePath = filepath.Join(scope.canonicalRoot, filepath.FromSlash(relative))
	}
	runtime := LegacyPath{Path: runtimePath, Source: "runtime config separate loader", Layer: LayerCanonical, Legacy: true}
	content, _, readErr := readOptionalAbsolute(ctx, runtimePath, MaxFileBytes)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return LegacyPaths{}, fmt.Errorf("inspect runtime config identity: %w", readErr)
	}
	if len(content) > 0 {
		var selector struct {
			Schema string `json:"schema_version"`
		}
		if err := json.Unmarshal(content, &selector); err == nil {
			switch selector.Schema {
			case "exp.runtime/v1":
				runtime.Source = "exp.runtime/v1 separate loader"
			case "exp.runtime/v2":
				runtime.Source = "exp.runtime/v2 separate loader"
				runtime.Legacy = false
				relative, relErr := filepath.Rel(scope.canonicalRoot, runtimePath)
				if relErr == nil {
					configScope := filepath.ToSlash(relative)
					if pathx.ValidateRelativePOSIX(configScope, false) == nil && checker != nil {
						sum := sha256.Sum256(content)
						digest := "sha256:" + hex.EncodeToString(sum[:])
						trusted, trustErr := checker.Check(ctx, trust.Subject{
							ProjectID: scope.projectID, GitCommonDir: scope.canonicalCommon, ConfigScope: configScope,
						}, digest, trust.CapabilityRuntimeDispatch)
						if trustErr != nil {
							return LegacyPaths{}, fmt.Errorf("inspect runtime config trust: %w", trustErr)
						}
						runtime.Trusted = trusted
					}
				}
			default:
				runtime.Source = "unrecognized runtime config separate loader"
			}
		}
	}
	return LegacyPaths{
		Agents:  LegacyPath{Path: agentPath, Source: "exp.agents/v1 separate loader", Layer: LayerUser, Trusted: true, Legacy: true},
		Runtime: runtime,
	}, nil
}

func combinedDigest(frames []string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("exp.config.layers/v1\x00"))
	for _, frame := range frames {
		_, _ = hash.Write([]byte(fmt.Sprintf("%d:", len(frame))))
		_, _ = hash.Write([]byte(frame))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func cloneLayers(input []Layer) []Layer {
	out := append([]Layer{}, input...)
	for index := range out {
		out[index].Capabilities = append([]trust.Capability{}, out[index].Capabilities...)
		if out[index].TrustSubject != nil {
			subject := *out[index].TrustSubject
			out[index].TrustSubject = &subject
		}
		out[index].Fields = append([]string{}, out[index].Fields...)
	}
	return out
}

func cloneProvenance(input map[string]Provenance) map[string]Provenance {
	out := make(map[string]Provenance, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
