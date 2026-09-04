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
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	// RuntimeSchemaV2 is the Source-aware formal execution contract. Runtime v1
	// remains a separate closed decoder and retains its embedded-repository rules.
	RuntimeSchemaV2 = "exp.runtime/v2"

	CheckoutMain               = "main"
	CheckoutRegisteredWorktree = "registered_worktree"
	CheckoutManagedWorktree    = "managed_worktree"
)

// RuntimeConfigV2 is deliberately separate from RuntimeConfig so adding Source
// authority cannot make any field legal in the closed v1 schema.
type RuntimeConfigV2 struct {
	Schema string                   `json:"schema_version"`
	Pools  map[string]PoolRuntime   `json:"pools"`
	Plans  map[string]PlanRuntimeV2 `json:"plans"`
}

// PlanRuntimeV2 binds one formal Plan to one writable execution Source and zero
// or more read-only Source snapshots. CWD and expected outputs are relative to
// the execution Source's canonical subdir; Git change sets remain repository-
// root-relative because that is the canonical SourceSnapshot representation.
type PlanRuntimeV2 struct {
	ExecutionSource       string                  `json:"execution_source"`
	ReadOnlySources       []ReadOnlySourceRuntime `json:"read_only_sources,omitempty"`
	Executable            string                  `json:"executable"`
	Argv                  []string                `json:"argv"`
	Checkout              string                  `json:"checkout"`
	CWD                   string                  `json:"cwd"`
	Timeout               string                  `json:"timeout,omitempty"`
	AllowedEnv            []string                `json:"allowed_env"`
	SecretEnv             []string                `json:"secret_env"`
	BaseCommit            string                  `json:"base_commit"`
	HeadCommit            string                  `json:"head_commit"`
	ChangeSet             []string                `json:"change_set"`
	ObservationalNoChange bool                    `json:"observational_no_change,omitempty"`
	ExpectedOutputs       []string                `json:"expected_outputs"`
}

// ReadOnlySourceRuntime pins an additional Source. Read-only describes runtime
// access, not whether the pinned commit differs from its declared base.
type ReadOnlySourceRuntime struct {
	Source                string   `json:"source"`
	Checkout              string   `json:"checkout"`
	BaseCommit            string   `json:"base_commit"`
	HeadCommit            string   `json:"head_commit"`
	ChangeSet             []string `json:"change_set"`
	ObservationalNoChange bool     `json:"observational_no_change,omitempty"`
}

type validatedSourceRuntime struct {
	Source                research.ID
	Checkout              string
	BaseCommit            string
	HeadCommit            string
	ChangeSet             []string
	ObservationalNoChange bool
	ReadOnly              bool
}

type validatedPlanRuntimeV2 struct {
	PlanRuntimeV2
	Execution validatedSourceRuntime
	ReadOnly  []validatedSourceRuntime
}

func loadRuntimeContract(ctx context.Context, repositoryRoot, relative string) (loadedRuntime, error) {
	schema, err := runtimeSchema(ctx, repositoryRoot, relative)
	if err != nil {
		return loadedRuntime{}, err
	}
	switch schema {
	case RuntimeSchema:
		return loadRuntime(ctx, repositoryRoot, relative)
	case RuntimeSchemaV2:
		return loadRuntimeV2(ctx, repositoryRoot, relative)
	default:
		return loadedRuntime{}, fmt.Errorf("runtime config schema must be %q or %q", RuntimeSchema, RuntimeSchemaV2)
	}
}

func runtimeSchema(ctx context.Context, repositoryRoot, relative string) (string, error) {
	content, _, err := readRuntimeConfig(ctx, repositoryRoot, relative)
	if err != nil {
		return "", err
	}
	var selector struct {
		Schema string `json:"schema_version"`
	}
	if err := json.Unmarshal(content, &selector); err != nil {
		return "", fmt.Errorf("decode project runtime config: %w", err)
	}
	return selector.Schema, nil
}

// RuntimeConfigIdentity returns the closed schema and exact raw-file digest used
// by host-local trust receipts. It performs the same no-symlink bounded read as
// runtime loading but does not validate or execute the document.
func RuntimeConfigIdentity(ctx context.Context, repositoryRoot, relative string) (string, string, string, error) {
	content, configPath, err := readRuntimeConfig(ctx, repositoryRoot, relative)
	if err != nil {
		return "", "", "", err
	}
	var selector struct {
		Schema string `json:"schema_version"`
	}
	if err := json.Unmarshal(content, &selector); err != nil {
		return "", "", "", fmt.Errorf("decode project runtime config: %w", err)
	}
	digest := sha256.Sum256(content)
	return selector.Schema, "sha256:" + hex.EncodeToString(digest[:]), configPath, nil
}

func readRuntimeConfig(ctx context.Context, repositoryRoot, relative string) ([]byte, string, error) {
	configPath, err := normalizeRuntimeConfigPath(relative)
	if err != nil {
		return nil, "", err
	}
	canonicalRoot, err := pathx.Canonical(repositoryRoot)
	if err != nil {
		return nil, "", err
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(canonicalRoot)
	if err != nil {
		return nil, "", fmt.Errorf("open project root for runtime config: %w", err)
	}
	defer root.Close()
	content, _, err := pathx.ReadBoundedRegularFile(ctx, root, configPath, maxConfigBytes)
	if err != nil {
		return nil, "", fmt.Errorf("read project runtime config: %w", err)
	}
	return content, configPath, nil
}

func loadRuntimeV2(ctx context.Context, repositoryRoot, relative string) (loadedRuntime, error) {
	content, configPath, err := readRuntimeConfig(ctx, repositoryRoot, relative)
	if err != nil {
		return loadedRuntime{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var config RuntimeConfigV2
	if err := decoder.Decode(&config); err != nil {
		return loadedRuntime{}, fmt.Errorf("decode project runtime config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return loadedRuntime{}, errors.New("project runtime config contains trailing JSON")
	}
	if config.Schema != RuntimeSchemaV2 {
		return loadedRuntime{}, fmt.Errorf("runtime config schema must be %q", RuntimeSchemaV2)
	}
	if config.Pools == nil || config.Plans == nil {
		return loadedRuntime{}, errors.New("runtime config pools and plans must be present")
	}

	configHash := sha256.Sum256(content)
	loaded := loadedRuntime{
		plans:        make(map[research.ID]validatedPlanRuntime, len(config.Plans)),
		pools:        make(map[research.ID]PoolRuntime, len(config.Pools)),
		configPath:   configPath,
		schema:       RuntimeSchemaV2,
		configDigest: "sha256:" + hex.EncodeToString(configHash[:]),
	}
	if err := loadRuntimePools(config.Pools, &loaded); err != nil {
		return loadedRuntime{}, err
	}
	for key, value := range config.Plans {
		id, err := research.ParseIDForKind(key, research.KindPlan)
		if err != nil {
			return loadedRuntime{}, fmt.Errorf("runtime plan key %q: %w", key, err)
		}
		validated, err := validatePlanRuntimeV2(value)
		if err != nil {
			return loadedRuntime{}, fmt.Errorf("runtime plan %s: %w", key, err)
		}
		encoded, err := json.Marshal(validated.PlanRuntimeV2)
		if err != nil {
			return loadedRuntime{}, err
		}
		digest := sha256.Sum256(encoded)
		loaded.plans[id] = validatedPlanRuntime{
			V2:                &validated,
			runtimeConfigPath: configPath,
			digest:            "sha256:" + hex.EncodeToString(digest[:]),
		}
	}
	return loaded, nil
}

func loadRuntimePools(values map[string]PoolRuntime, loaded *loadedRuntime) error {
	routes := map[string]map[string]research.ID{}
	for key, value := range values {
		id, err := research.ParseIDForKind(key, research.KindResourcePool)
		if err != nil {
			return fmt.Errorf("runtime pool key %q: %w", key, err)
		}
		if !validToken(value.PueueGroup) {
			return fmt.Errorf("runtime pool %s has an invalid Pueue group", key)
		}
		if value.LabelPrefix == "" {
			value.LabelPrefix = "exp-"
		}
		if !validToken(value.LabelPrefix) {
			return fmt.Errorf("runtime pool %s has an invalid label prefix", key)
		}
		if len(value.LabelPrefix) > maxPueueLabelPrefixBytes {
			return fmt.Errorf("runtime pool %s label prefix is too long to append a dispatch ID", key)
		}
		prefixes := routes[value.PueueGroup]
		if prefixes == nil {
			prefixes = map[string]research.ID{}
			routes[value.PueueGroup] = prefixes
		}
		for prefix, previous := range prefixes {
			if strings.HasPrefix(prefix, value.LabelPrefix) || strings.HasPrefix(value.LabelPrefix, prefix) {
				return fmt.Errorf("runtime pools %s and %s have overlapping label prefixes in Pueue group %s", previous, id, value.PueueGroup)
			}
		}
		prefixes[value.LabelPrefix] = id
		loaded.pools[id] = value
	}
	return nil
}

func validatePlanRuntimeV2(value PlanRuntimeV2) (validatedPlanRuntimeV2, error) {
	if err := validateRuntimeExecutable(value.Executable); err != nil {
		return validatedPlanRuntimeV2{}, err
	}
	if value.Argv == nil {
		return validatedPlanRuntimeV2{}, errors.New("argv array must be present, even when empty")
	}
	for _, argument := range value.Argv {
		if argument == "" || !utf8.ValidString(argument) || strings.ContainsAny(argument, "\x00\r\n") {
			return validatedPlanRuntimeV2{}, errors.New("argv contains an empty or invalid argument")
		}
		if err := research.ValidateCommitSafeText(argument); err != nil {
			return validatedPlanRuntimeV2{}, errors.New("argv contains credential-bearing material")
		}
	}
	if value.Checkout == "" {
		return validatedPlanRuntimeV2{}, errors.New("checkout must be explicit")
	}
	if !validRuntimeCheckout(value.Checkout) {
		return validatedPlanRuntimeV2{}, errors.New("checkout must be main, registered_worktree, or managed_worktree")
	}
	if value.CWD == "" {
		return validatedPlanRuntimeV2{}, errors.New("cwd must be present; use . for the Source subdir root")
	}
	if err := research.ValidateCommittedPath(value.CWD, true); err != nil {
		return validatedPlanRuntimeV2{}, fmt.Errorf("cwd: %w", err)
	}
	if value.Timeout != "" {
		timeout, err := time.ParseDuration(value.Timeout)
		if err != nil || timeout <= 0 {
			return validatedPlanRuntimeV2{}, errors.New("timeout must be a positive duration")
		}
	}
	changeSet, err := normalizedRuntimeV2Paths(value.ChangeSet, false)
	if err != nil {
		return validatedPlanRuntimeV2{}, fmt.Errorf("change_set: %w", err)
	}
	value.ChangeSet = changeSet
	executionID, err := research.ParseIDForKind(value.ExecutionSource, research.KindSource)
	if err != nil {
		return validatedPlanRuntimeV2{}, fmt.Errorf("execution_source: %w", err)
	}
	execution, err := validateSourceRuntime(validatedSourceRuntime{
		Source: executionID, Checkout: value.Checkout, BaseCommit: value.BaseCommit,
		HeadCommit: value.HeadCommit, ChangeSet: append([]string{}, value.ChangeSet...),
		ObservationalNoChange: value.ObservationalNoChange,
	})
	if err != nil {
		return validatedPlanRuntimeV2{}, err
	}

	readOnly := make([]validatedSourceRuntime, 0, len(value.ReadOnlySources))
	seen := map[research.ID]struct{}{executionID: {}}
	for index := range value.ReadOnlySources {
		binding := &value.ReadOnlySources[index]
		id, err := research.ParseIDForKind(binding.Source, research.KindSource)
		if err != nil {
			return validatedPlanRuntimeV2{}, fmt.Errorf("read_only_sources[%d].source: %w", index, err)
		}
		if _, duplicate := seen[id]; duplicate {
			return validatedPlanRuntimeV2{}, fmt.Errorf("Source %s occurs more than once", id)
		}
		seen[id] = struct{}{}
		paths, err := normalizedRuntimeV2Paths(binding.ChangeSet, false)
		if err != nil {
			return validatedPlanRuntimeV2{}, fmt.Errorf("read_only_sources[%d].change_set: %w", index, err)
		}
		binding.ChangeSet = paths
		validated, err := validateSourceRuntime(validatedSourceRuntime{
			Source: id, Checkout: binding.Checkout, BaseCommit: binding.BaseCommit,
			HeadCommit: binding.HeadCommit, ChangeSet: append([]string{}, paths...),
			ObservationalNoChange: binding.ObservationalNoChange, ReadOnly: true,
		})
		if err != nil {
			return validatedPlanRuntimeV2{}, fmt.Errorf("read_only_sources[%d]: %w", index, err)
		}
		readOnly = append(readOnly, validated)
	}
	sort.Slice(readOnly, func(left, right int) bool { return readOnly[left].Source.String() < readOnly[right].Source.String() })
	sort.Slice(value.ReadOnlySources, func(left, right int) bool {
		return value.ReadOnlySources[left].Source < value.ReadOnlySources[right].Source
	})
	if value.ReadOnlySources == nil {
		value.ReadOnlySources = []ReadOnlySourceRuntime{}
	}

	value.ExpectedOutputs, err = normalizedPaths(value.ExpectedOutputs, true)
	if err != nil {
		return validatedPlanRuntimeV2{}, fmt.Errorf("expected_outputs: %w", err)
	}
	if len(value.ExpectedOutputs) > 256 {
		return validatedPlanRuntimeV2{}, errors.New("expected_outputs exceeds the worker terminal capacity of 256 paths")
	}
	pathBytes := 0
	for _, output := range value.ExpectedOutputs {
		pathBytes += len(output)
	}
	if pathBytes > 32<<10 {
		return validatedPlanRuntimeV2{}, errors.New("expected_outputs paths exceed the worker terminal byte budget")
	}
	value.AllowedEnv, value.SecretEnv, err = normalizedEnvironment(value.AllowedEnv, value.SecretEnv)
	if err != nil {
		return validatedPlanRuntimeV2{}, err
	}
	if len(value.SecretEnv) != 0 {
		return validatedPlanRuntimeV2{}, errors.New("secret_env is unsupported for Pueue because the scheduler persists task environments; use a workload-side credential broker")
	}
	for _, name := range value.AllowedEnv {
		if execx.SensitiveName(name) {
			return validatedPlanRuntimeV2{}, fmt.Errorf("allowed_env %s is credential-sensitive and cannot be persisted by Pueue", name)
		}
	}
	return validatedPlanRuntimeV2{PlanRuntimeV2: value, Execution: execution, ReadOnly: readOnly}, nil
}

func validateRuntimeExecutable(value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("executable must be a clean absolute UTF-8 path")
	}
	if err := research.ValidateCommitSafeText(value); err != nil {
		return errors.New("executable contains credential-bearing material")
	}
	return nil
}

func validateSourceRuntime(value validatedSourceRuntime) (validatedSourceRuntime, error) {
	if value.Source.IsZero() || value.Source.Kind() != research.KindSource {
		return validatedSourceRuntime{}, errors.New("Source identity is invalid")
	}
	if !validRuntimeCheckout(value.Checkout) {
		return validatedSourceRuntime{}, errors.New("checkout must be main, registered_worktree, or managed_worktree")
	}
	if !fullObjectID.MatchString(value.BaseCommit) || !fullObjectID.MatchString(value.HeadCommit) || len(value.BaseCommit) != len(value.HeadCommit) {
		return validatedSourceRuntime{}, errors.New("base_commit and head_commit must be full lower-case Git object IDs of one object format")
	}
	if len(value.ChangeSet) == 0 {
		if !value.ObservationalNoChange || value.BaseCommit != value.HeadCommit {
			return validatedSourceRuntime{}, errors.New("empty change_set requires explicit observational_no_change with base_commit equal to head_commit")
		}
	} else if value.ObservationalNoChange {
		return validatedSourceRuntime{}, errors.New("observational_no_change forbids a non-empty change_set")
	}
	return value, nil
}

func normalizedRuntimeV2Paths(input []string, allowNil bool) ([]string, error) {
	if input == nil && !allowNil {
		return nil, errors.New("change_set array must be present, even when empty")
	}
	seen := make(map[string]struct{}, len(input))
	output := make([]string, 0, len(input))
	for _, value := range input {
		if err := research.ValidateCommittedPath(value, false); err != nil {
			return nil, err
		}
		if value == ".git" || strings.HasPrefix(value, ".git/") {
			return nil, fmt.Errorf("change_set path %q is Git metadata", value)
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

func validRuntimeCheckout(value string) bool {
	return value == CheckoutMain || value == CheckoutRegisteredWorktree || value == CheckoutManagedWorktree
}
