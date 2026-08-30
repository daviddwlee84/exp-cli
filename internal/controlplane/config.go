package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	RuntimeSchema            = "exp.runtime/v1"
	DefaultConfigPath        = ".exp/runtime.json"
	maxConfigBytes           = 1 << 20
	maxPueueTokenBytes       = 128
	generatedDispatchIDBytes = len("dispatch-") + 32
	maxPueueLabelPrefixBytes = maxPueueTokenBytes - generatedDispatchIDBytes
)

var (
	fullObjectID  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	environmentID = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	safeToken     = regexp.MustCompile(`^[A-Za-z0-9._:@/+=-]+$`)
)

// RuntimeConfig is a strict, project-local, non-secret execution contract.
// SecretEnv contains environment variable names only; credential values are
// resolved by the worker at process start and never enter canonical records.
type RuntimeConfig struct {
	Schema string                 `json:"schema_version"`
	Pools  map[string]PoolRuntime `json:"pools"`
	Plans  map[string]PlanRuntime `json:"plans"`
}

// PoolRuntime binds one canonical ResourcePool to a Pueue group and label
// namespace.
type PoolRuntime struct {
	PueueGroup  string `json:"pueue_group"`
	LabelPrefix string `json:"label_prefix"`
}

// PlanRuntime is the explicit executable/argv and committed-code contract for
// one canonical Plan. It contains environment names, never environment values.
type PlanRuntime struct {
	Executable      string   `json:"executable"`
	Argv            []string `json:"argv"`
	Checkout        string   `json:"checkout,omitempty"`
	CWD             string   `json:"cwd"`
	Timeout         string   `json:"timeout,omitempty"`
	AllowedEnv      []string `json:"allowed_env"`
	SecretEnv       []string `json:"secret_env"`
	BaseCommit      string   `json:"base_commit"`
	HeadCommit      string   `json:"head_commit"`
	ChangeSet       []string `json:"change_set"`
	ExpectedOutputs []string `json:"expected_outputs"`
}

type loadedRuntime struct {
	plans      map[research.ID]validatedPlanRuntime
	pools      map[research.ID]PoolRuntime
	configPath string
}

type validatedPlanRuntime struct {
	PlanRuntime
	absoluteCWD       string
	repositoryRoot    string
	runtimeConfigPath string
	digest            string
}

func loadRuntime(ctx context.Context, repositoryRoot, relative string) (loadedRuntime, error) {
	configPath, err := normalizeRuntimeConfigPath(relative)
	if err != nil {
		return loadedRuntime{}, err
	}
	canonicalRoot, err := pathx.Canonical(repositoryRoot)
	if err != nil {
		return loadedRuntime{}, err
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonicalRoot)
	if err != nil {
		return loadedRuntime{}, fmt.Errorf("open project root for runtime config: %w", err)
	}
	defer root.Close()
	content, _, err := pathx.ReadBoundedRegularFile(ctx, root, configPath, maxConfigBytes)
	if err != nil {
		return loadedRuntime{}, fmt.Errorf("read project runtime config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var config RuntimeConfig
	if err := decoder.Decode(&config); err != nil {
		return loadedRuntime{}, fmt.Errorf("decode project runtime config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return loadedRuntime{}, errors.New("project runtime config contains trailing JSON")
	}
	if config.Schema != RuntimeSchema {
		return loadedRuntime{}, fmt.Errorf("runtime config schema must be %q", RuntimeSchema)
	}
	if config.Pools == nil || config.Plans == nil {
		return loadedRuntime{}, errors.New("runtime config pools and plans must be present")
	}

	loaded := loadedRuntime{
		plans:      make(map[research.ID]validatedPlanRuntime, len(config.Plans)),
		pools:      make(map[research.ID]PoolRuntime, len(config.Pools)),
		configPath: configPath,
	}
	routes := map[string]map[string]research.ID{}
	for key, value := range config.Pools {
		id, err := research.ParseIDForKind(key, research.KindResourcePool)
		if err != nil {
			return loadedRuntime{}, fmt.Errorf("runtime pool key %q: %w", key, err)
		}
		if !validToken(value.PueueGroup) {
			return loadedRuntime{}, fmt.Errorf("runtime pool %s has an invalid Pueue group", key)
		}
		if value.LabelPrefix == "" {
			value.LabelPrefix = "exp-"
		}
		if !validToken(value.LabelPrefix) {
			return loadedRuntime{}, fmt.Errorf("runtime pool %s has an invalid label prefix", key)
		}
		if len(value.LabelPrefix) > maxPueueLabelPrefixBytes {
			return loadedRuntime{}, fmt.Errorf("runtime pool %s label prefix is too long to append a dispatch ID", key)
		}
		prefixes := routes[value.PueueGroup]
		if prefixes == nil {
			prefixes = map[string]research.ID{}
			routes[value.PueueGroup] = prefixes
		}
		for prefix, previous := range prefixes {
			if strings.HasPrefix(prefix, value.LabelPrefix) || strings.HasPrefix(value.LabelPrefix, prefix) {
				return loadedRuntime{}, fmt.Errorf("runtime pools %s and %s have overlapping label prefixes in Pueue group %s", previous, id, value.PueueGroup)
			}
		}
		prefixes[value.LabelPrefix] = id
		loaded.pools[id] = value
	}
	for key, value := range config.Plans {
		id, err := research.ParseIDForKind(key, research.KindPlan)
		if err != nil {
			return loadedRuntime{}, fmt.Errorf("runtime plan key %q: %w", key, err)
		}
		validated, err := validatePlanRuntime(canonicalRoot, configPath, value)
		if err != nil {
			return loadedRuntime{}, fmt.Errorf("runtime plan %s: %w", key, err)
		}
		loaded.plans[id] = validated
	}
	return loaded, nil
}

func normalizeRuntimeConfigPath(value string) (string, error) {
	if value == "" {
		value = DefaultConfigPath
	}
	if err := pathx.ValidateRelativePOSIX(value, false); err != nil {
		return "", fmt.Errorf("runtime config path: %w", err)
	}
	return path.Clean(value), nil
}

func validatePlanRuntime(repositoryRoot, runtimeConfigPath string, value PlanRuntime) (validatedPlanRuntime, error) {
	if value.Executable == "" || !filepath.IsAbs(value.Executable) || filepath.Clean(value.Executable) != value.Executable || !utf8.ValidString(value.Executable) || strings.ContainsAny(value.Executable, "\x00\r\n") {
		return validatedPlanRuntime{}, errors.New("executable must be a clean absolute UTF-8 path")
	}
	if err := research.ValidateCommitSafeText(value.Executable); err != nil {
		return validatedPlanRuntime{}, errors.New("executable contains credential-bearing material")
	}
	if len(value.Argv) == 0 {
		return validatedPlanRuntime{}, errors.New("argv must contain at least one argument")
	}
	for _, argument := range value.Argv {
		if argument == "" || !utf8.ValidString(argument) || strings.ContainsAny(argument, "\x00\r\n") {
			return validatedPlanRuntime{}, errors.New("argv contains an empty or invalid argument")
		}
		if err := research.ValidateCommitSafeText(argument); err != nil {
			return validatedPlanRuntime{}, errors.New("argv contains credential-bearing material")
		}
	}
	if value.CWD == "" {
		value.CWD = "."
	}
	if value.Checkout == "" {
		value.Checkout = "main"
	}
	if value.Checkout != "main" && value.Checkout != "registered_worktree" {
		return validatedPlanRuntime{}, errors.New("checkout must be main or registered_worktree")
	}
	if err := research.ValidateCommittedPath(value.CWD, true); err != nil {
		return validatedPlanRuntime{}, fmt.Errorf("cwd: %w", err)
	}
	absoluteCWD := ""
	var err error
	if value.Checkout == "main" {
		absoluteCWD, err = pathx.ResolveUnderNoSymlinks(repositoryRoot, value.CWD, true)
		if err != nil {
			return validatedPlanRuntime{}, fmt.Errorf("cwd: %w", err)
		}
		info, err := os.Stat(absoluteCWD)
		if err != nil {
			return validatedPlanRuntime{}, fmt.Errorf("inspect cwd: %w", err)
		}
		if !info.IsDir() {
			return validatedPlanRuntime{}, errors.New("cwd must name an existing directory")
		}
	}
	if value.Timeout != "" {
		timeout, err := time.ParseDuration(value.Timeout)
		if err != nil || timeout <= 0 {
			return validatedPlanRuntime{}, errors.New("timeout must be a positive duration")
		}
	}
	if !fullObjectID.MatchString(value.BaseCommit) || !fullObjectID.MatchString(value.HeadCommit) {
		return validatedPlanRuntime{}, errors.New("base_commit and head_commit must be full lower-case Git object IDs")
	}
	if len(value.ChangeSet) == 0 {
		return validatedPlanRuntime{}, errors.New("change_set must contain at least one committed path")
	}
	value.ChangeSet, err = normalizedPaths(value.ChangeSet, false)
	if err != nil {
		return validatedPlanRuntime{}, fmt.Errorf("change_set: %w", err)
	}
	for _, changed := range value.ChangeSet {
		if forbiddenRuntimeChangePath(changed, runtimeConfigPath) {
			return validatedPlanRuntime{}, fmt.Errorf("change_set path %q is control metadata, not experiment code", changed)
		}
	}
	value.ExpectedOutputs, err = normalizedPaths(value.ExpectedOutputs, true)
	if err != nil {
		return validatedPlanRuntime{}, fmt.Errorf("expected_outputs: %w", err)
	}
	if len(value.ExpectedOutputs) > 256 {
		return validatedPlanRuntime{}, errors.New("expected_outputs exceeds the worker terminal capacity of 256 paths")
	}
	pathBytes := 0
	for _, output := range value.ExpectedOutputs {
		pathBytes += len(output)
	}
	if pathBytes > 32<<10 {
		return validatedPlanRuntime{}, errors.New("expected_outputs paths exceed the worker terminal byte budget")
	}
	value.AllowedEnv, value.SecretEnv, err = normalizedEnvironment(value.AllowedEnv, value.SecretEnv)
	if err != nil {
		return validatedPlanRuntime{}, err
	}
	if len(value.SecretEnv) != 0 {
		return validatedPlanRuntime{}, errors.New("secret_env is unsupported for Pueue because the scheduler persists task environments; use a workload-side credential broker")
	}
	for _, name := range value.AllowedEnv {
		if execx.SensitiveName(name) {
			return validatedPlanRuntime{}, fmt.Errorf("allowed_env %s is credential-sensitive and cannot be persisted by Pueue", name)
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return validatedPlanRuntime{}, err
	}
	digest := sha256.Sum256(encoded)
	return validatedPlanRuntime{
		PlanRuntime: value, absoluteCWD: absoluteCWD, repositoryRoot: repositoryRoot,
		runtimeConfigPath: runtimeConfigPath, digest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func forbiddenRuntimeChangePath(value, runtimeConfigPath string) bool {
	return value == "experiments" || strings.HasPrefix(value, "experiments/") ||
		value == ".git" || strings.HasPrefix(value, ".git/") ||
		value == DefaultConfigPath || value == runtimeConfigPath
}

func normalizedPaths(input []string, allowEmpty bool) ([]string, error) {
	if !allowEmpty && len(input) == 0 {
		return nil, errors.New("at least one path is required")
	}
	seen := make(map[string]struct{}, len(input))
	output := make([]string, 0, len(input))
	for _, value := range input {
		if err := research.ValidateCommittedPath(value, false); err != nil {
			return nil, err
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("path %q is duplicated", value)
		}
		seen[value] = struct{}{}
		output = append(output, value)
	}
	sort.Strings(output)
	if output == nil {
		output = []string{}
	}
	return output, nil
}

func normalizedEnvironment(allowed, secret []string) ([]string, []string, error) {
	seen := make(map[string]struct{}, len(allowed)+len(secret))
	normalize := func(values []string) ([]string, error) {
		output := make([]string, 0, len(values))
		for _, value := range values {
			if !environmentID.MatchString(value) {
				return nil, fmt.Errorf("invalid environment variable name %q", value)
			}
			if _, duplicate := seen[value]; duplicate {
				return nil, fmt.Errorf("environment variable %s is duplicated", value)
			}
			seen[value] = struct{}{}
			output = append(output, value)
		}
		sort.Strings(output)
		if output == nil {
			output = []string{}
		}
		return output, nil
	}
	normalAllowed, err := normalize(allowed)
	if err != nil {
		return nil, nil, err
	}
	normalSecret, err := normalize(secret)
	if err != nil {
		return nil, nil, err
	}
	return normalAllowed, normalSecret, nil
}

func validToken(value string) bool {
	return value != "" && len(value) <= maxPueueTokenBytes && safeToken.MatchString(value)
}
