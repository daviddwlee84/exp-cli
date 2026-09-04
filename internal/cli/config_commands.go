package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/controlplane"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/trust"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/spf13/cobra"
)

const (
	configPathDataSchema    = "exp.command.config-path/v1"
	configShowDataSchema    = "exp.command.config-show/v1"
	configExplainDataSchema = "exp.command.config-explain/v1"
	configTrustDataSchema   = "exp.command.config-trust/v1"
	configRevokeDataSchema  = "exp.command.config-revoke/v1"
	configListDataSchema    = "exp.command.config-list/v1"
)

type configReadOptions struct {
	json bool
}

type configTrustOptions struct {
	path         string
	digest       string
	capabilities []string
	confirm      bool
	json         bool
}

type configRevokeOptions struct {
	path         string
	digest       string
	capabilities []string
	confirm      bool
	json         bool
}

type configPathEntryView struct {
	Layer   config.LayerKind `json:"layer"`
	Path    string           `json:"path"`
	Present bool             `json:"present"`
	Trusted bool             `json:"trusted"`
}

type configPathData struct {
	SchemaVersion string                `json:"schema_version"`
	Project       projectView           `json:"project"`
	Paths         []configPathEntryView `json:"paths"`
	Legacy        []configLegacyView    `json:"legacy"`
}

type configShowData struct {
	SchemaVersion string            `json:"schema_version"`
	Project       projectView       `json:"project"`
	Effective     config.Effective  `json:"effective"`
	Summary       configSummaryView `json:"summary"`
}

type configExplainData struct {
	SchemaVersion string                 `json:"schema_version"`
	Project       projectView            `json:"project"`
	Field         string                 `json:"field,omitempty"`
	Digest        string                 `json:"digest"`
	Layers        []configLayerView      `json:"layers"`
	Provenance    []configProvenanceView `json:"provenance"`
	Legacy        []configLegacyView     `json:"legacy"`
}

type trustReceiptView struct {
	ProjectID    string             `json:"project_id"`
	SourceID     string             `json:"source_id,omitempty"`
	ConfigScope  string             `json:"config_scope,omitempty"`
	ConfigDigest string             `json:"config_digest"`
	Capabilities []trust.Capability `json:"capabilities"`
	ApprovedAt   string             `json:"approved_at"`
}

type configTrustData struct {
	SchemaVersion string           `json:"schema_version"`
	Receipt       trustReceiptView `json:"receipt"`
}

type configRevokeData struct {
	SchemaVersion string             `json:"schema_version"`
	ProjectID     string             `json:"project_id"`
	SourceID      string             `json:"source_id,omitempty"`
	ConfigScope   string             `json:"config_scope,omitempty"`
	Digest        string             `json:"digest,omitempty"`
	Capabilities  []trust.Capability `json:"capabilities"`
	Changed       int                `json:"changed"`
}

type configListData struct {
	SchemaVersion string             `json:"schema_version"`
	Receipts      []trustReceiptView `json:"receipts"`
}

func newConfigCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Inspect layered configuration and manage exact-digest trust", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(
		newConfigPathCommand(app, root),
		newConfigShowCommand(app, root),
		newConfigExplainCommand(app, root),
		newConfigTrustCommand(app, root),
		newConfigRevokeCommand(app, root),
		newConfigListCommand(app),
	)
	return command
}

func newConfigPathCommand(app *App, root *rootOptions) *cobra.Command {
	options := &configReadOptions{}
	command := &cobra.Command{
		Use: "path", Short: "Show candidate and applied configuration paths", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runConfigPath(command, app, root, options) },
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newConfigShowCommand(app *App, root *rootOptions) *cobra.Command {
	options := &configReadOptions{}
	command := &cobra.Command{
		Use: "show", Short: "Show effective non-secret configuration and a safe provenance summary", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runConfigShow(command, app, root, options) },
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newConfigExplainCommand(app *App, root *rootOptions) *cobra.Command {
	options := &configReadOptions{}
	command := &cobra.Command{
		Use: "explain [field]", Short: "Explain layer and trust provenance for effective fields", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runConfigExplain(command, app, root, options, args)
		},
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newConfigTrustCommand(app *App, root *rootOptions) *cobra.Command {
	options := &configTrustOptions{}
	command := &cobra.Command{
		Use: "trust", Short: "Review and approve execution-bearing config by exact digest", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runConfigTrust(command, app, root, options) },
	}
	flags := command.Flags()
	flags.StringVar(&options.path, "path", "", "select one currently applied repository config file")
	flags.StringVar(&options.digest, "digest", "", "require the exact current sha256 config digest")
	flags.StringSliceVar(&options.capabilities, "capability", nil, "approve an execution-bearing capability (repeatable)")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm exact-digest approval without prompting")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newConfigRevokeCommand(app *App, root *rootOptions) *cobra.Command {
	options := &configRevokeOptions{}
	command := &cobra.Command{
		Use: "revoke", Short: "Revoke trust for a current or deleted config scope", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runConfigRevoke(command, app, root, options) },
	}
	flags := command.Flags()
	flags.StringVar(&options.path, "path", "", "select a current or deleted repository config file")
	flags.StringVar(&options.digest, "digest", "", "limit revocation to one exact sha256 config digest")
	flags.StringSliceVar(&options.capabilities, "capability", nil, "revoke an execution-bearing capability (repeatable; empty revokes all)")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm local trust revocation without prompting")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newConfigListCommand(app *App) *cobra.Command {
	options := &configReadOptions{}
	command := &cobra.Command{
		Use: "list", Short: "List sanitized local trust receipts", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return runConfigList(command, app, options) },
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runConfigPath(command *cobra.Command, app *App, root *rootOptions, options *configReadOptions) error {
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "config path", configPathData{SchemaVersion: configPathDataSchema, Paths: []configPathEntryView{}, Legacy: []configLegacyView{}}, false, nil, err)
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "config path", configPathData{SchemaVersion: configPathDataSchema, Paths: []configPathEntryView{}, Legacy: []configLegacyView{}}, false, nil, err)
	}
	summary := makeConfigSummaryView(resolved.Config)
	paths, err := configPathEntries(resolved, summary)
	if err != nil {
		return commandFailure(app, options.json, "config path", configPathData{SchemaVersion: configPathDataSchema, Project: projectData, Paths: []configPathEntryView{}, Legacy: summary.Legacy}, false, nil, err)
	}
	data := configPathData{SchemaVersion: configPathDataSchema, Project: projectData, Paths: paths, Legacy: summary.Legacy}
	var human strings.Builder
	for _, entry := range paths {
		fmt.Fprintf(&human, "%s\t%s\tpresent=%t\ttrusted=%t\n", entry.Layer, entry.Path, entry.Present, entry.Trusted)
	}
	for _, legacy := range summary.Legacy {
		fmt.Fprintf(&human, "legacy:%s\t%s\t%s\n", legacy.Name, legacy.Path, legacy.Source)
	}
	return commandSuccess(app, options.json, "config path", data, false, nil, human.String())
}

func runConfigShow(command *cobra.Command, app *App, root *rootOptions, options *configReadOptions) error {
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "config show", configShowData{SchemaVersion: configShowDataSchema}, false, nil, err)
	}
	if resolved.Config == nil {
		return commandFailure(app, options.json, "config show", configShowData{SchemaVersion: configShowDataSchema}, false, nil, errors.New("workspace resolver returned no configuration result"))
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "config show", configShowData{SchemaVersion: configShowDataSchema}, false, nil, err)
	}
	summary := makeConfigSummaryView(resolved.Config)
	warnUntrustedConfig(app, summary)
	data := configShowData{SchemaVersion: configShowDataSchema, Project: projectData, Effective: resolved.Config.Effective, Summary: summary}
	human := renderConfigShowHuman(data)
	return commandSuccess(app, options.json, "config show", data, !summary.Trusted, nil, human)
}

func runConfigExplain(command *cobra.Command, app *App, root *rootOptions, options *configReadOptions, args []string) error {
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "config explain", configExplainData{SchemaVersion: configExplainDataSchema, Layers: []configLayerView{}, Provenance: []configProvenanceView{}, Legacy: []configLegacyView{}}, false, nil, err)
	}
	if resolved.Config == nil {
		return commandFailure(app, options.json, "config explain", configExplainData{SchemaVersion: configExplainDataSchema, Layers: []configLayerView{}, Provenance: []configProvenanceView{}, Legacy: []configLegacyView{}}, false, nil, errors.New("workspace resolver returned no configuration result"))
	}
	projectData, err := makeProjectView(resolved.Project)
	if err != nil {
		return commandFailure(app, options.json, "config explain", configExplainData{SchemaVersion: configExplainDataSchema, Layers: []configLayerView{}, Provenance: []configProvenanceView{}, Legacy: []configLegacyView{}}, false, nil, err)
	}
	summary := makeConfigSummaryView(resolved.Config)
	field := ""
	provenance := summary.Provenance
	if len(args) == 1 {
		field = strings.TrimSpace(args[0])
		provenance = nil
		for _, item := range summary.Provenance {
			if item.Field == field {
				provenance = append(provenance, item)
			}
		}
		if len(provenance) == 0 {
			return commandFailure(app, options.json, "config explain", configExplainData{SchemaVersion: configExplainDataSchema, Layers: []configLayerView{}, Provenance: []configProvenanceView{}, Legacy: []configLegacyView{}}, false, nil, fmt.Errorf("effective config field %q has no provenance", field))
		}
	}
	warnUntrustedConfig(app, summary)
	data := configExplainData{
		SchemaVersion: configExplainDataSchema, Project: projectData, Field: field, Digest: summary.Digest,
		Layers: summary.Layers, Provenance: provenance, Legacy: summary.Legacy,
	}
	return commandSuccess(app, options.json, "config explain", data, !summary.Trusted, nil, renderConfigExplainHuman(data))
}

func runConfigTrust(command *cobra.Command, app *App, root *rootOptions, options *configTrustOptions) error {
	if strings.TrimSpace(options.path) == "" || strings.TrimSpace(options.digest) == "" || len(options.capabilities) == 0 || !options.confirm {
		if !app.interactive(options.json) {
			return commandFailure(app, options.json, "config trust", configTrustData{SchemaVersion: configTrustDataSchema}, false, nil, invalidUsagef("config trust requires --path, --digest, --capability, and --confirm in non-interactive or JSON mode"))
		}
		if err := prepareGuidedConfigTrust(command, app, root, options); err != nil {
			return commandFailure(app, options.json, "config trust", configTrustData{SchemaVersion: configTrustDataSchema}, false, nil, err)
		}
	}
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "config trust", configTrustData{SchemaVersion: configTrustDataSchema}, false, nil, err)
	}
	layer, err := findTrustableLayer(command.Context(), resolved, options.path)
	if err != nil {
		return commandFailure(app, options.json, "config trust", configTrustData{SchemaVersion: configTrustDataSchema}, false, nil, err)
	}
	if layer.Digest != strings.TrimSpace(options.digest) {
		return commandFailure(app, options.json, "config trust", configTrustData{SchemaVersion: configTrustDataSchema}, false, nil, errors.New("provided digest does not equal the current config layer digest"))
	}
	capabilities, err := parseCapabilities(options.capabilities, true)
	if err != nil {
		return commandFailure(app, options.json, "config trust", configTrustData{SchemaVersion: configTrustDataSchema}, false, nil, err)
	}
	if err := requireLayerCapabilities(layer, capabilities); err != nil {
		return commandFailure(app, options.json, "config trust", configTrustData{SchemaVersion: configTrustDataSchema}, false, nil, err)
	}
	receipt, err := app.TrustStore.Approve(command.Context(), trust.Query{
		Subject: *layer.TrustSubject, ConfigDigest: layer.Digest, Capabilities: capabilities,
	})
	data := configTrustData{SchemaVersion: configTrustDataSchema, Receipt: makeTrustReceiptView(receipt)}
	if err != nil {
		partial := publicationWasPublished(err)
		return commandFailure(app, options.json, "config trust", data, partial, mutationPublicationDiagnostics(partial, "trust receipt"), err)
	}
	human := fmt.Sprintf("Trusted %s for %s at digest %s.\n", strings.Join(capabilityStrings(receipt.Capabilities), ","), receipt.ConfigScope, receipt.ConfigDigest)
	return commandSuccess(app, options.json, "config trust", data, false, nil, human)
}

func runConfigRevoke(command *cobra.Command, app *App, root *rootOptions, options *configRevokeOptions) error {
	if strings.TrimSpace(options.path) == "" || !options.confirm {
		return commandFailure(app, options.json, "config revoke", configRevokeData{SchemaVersion: configRevokeDataSchema, Capabilities: []trust.Capability{}}, false, nil, errors.New("config revoke requires --path and --confirm; no prompt is performed"))
	}
	resolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "config revoke", configRevokeData{SchemaVersion: configRevokeDataSchema, Capabilities: []trust.Capability{}}, false, nil, err)
	}
	subject, layer, err := findRevocableSubject(command, app, resolved, options.path)
	if err != nil {
		return commandFailure(app, options.json, "config revoke", configRevokeData{SchemaVersion: configRevokeDataSchema, Capabilities: []trust.Capability{}}, false, nil, err)
	}
	capabilities, err := parseCapabilities(options.capabilities, false)
	if err != nil {
		return commandFailure(app, options.json, "config revoke", configRevokeData{SchemaVersion: configRevokeDataSchema, Capabilities: []trust.Capability{}}, false, nil, err)
	}
	if len(capabilities) > 0 && layer != nil {
		if err := requireLayerCapabilities(*layer, capabilities); err != nil {
			return commandFailure(app, options.json, "config revoke", configRevokeData{SchemaVersion: configRevokeDataSchema, Capabilities: []trust.Capability{}}, false, nil, err)
		}
	}
	digest := strings.TrimSpace(options.digest)
	changed, err := app.TrustStore.Revoke(command.Context(), trust.RevokeRequest{
		Subject: subject, ConfigDigest: digest, Capabilities: capabilities,
	})
	data := configRevokeData{
		SchemaVersion: configRevokeDataSchema, ProjectID: subject.ProjectID.String(),
		SourceID: subject.SourceID.String(), ConfigScope: subject.ConfigScope,
		Digest: digest, Capabilities: capabilities, Changed: changed,
	}
	if err != nil {
		partial := publicationWasPublished(err)
		if !partial {
			data.Changed = 0
		}
		return commandFailure(app, options.json, "config revoke", data, partial, mutationPublicationDiagnostics(partial, "trust revocation"), err)
	}
	return commandSuccess(app, options.json, "config revoke", data, false, nil, fmt.Sprintf("Revoked %d trust receipt(s) for %s.\n", changed, data.ConfigScope))
}

func runConfigList(command *cobra.Command, app *App, options *configReadOptions) error {
	receipts, err := app.TrustStore.List(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "config list", configListData{SchemaVersion: configListDataSchema, Receipts: []trustReceiptView{}}, false, nil, err)
	}
	views := make([]trustReceiptView, 0, len(receipts))
	for _, receipt := range receipts {
		views = append(views, makeTrustReceiptView(receipt))
	}
	data := configListData{SchemaVersion: configListDataSchema, Receipts: views}
	var human strings.Builder
	if len(views) == 0 {
		human.WriteString("No local config trust receipts.\n")
	} else {
		table := newHumanRows(5)
		for _, receipt := range views {
			table.Add(receipt.ProjectID, receipt.SourceID, receipt.ConfigScope, receipt.ConfigDigest, strings.Join(capabilityStrings(receipt.Capabilities), ","))
		}
		human.WriteString(mustRenderTable(table))
	}
	return commandSuccess(app, options.json, "config list", data, false, nil, human.String())
}

func configPathEntries(resolved *workspace.Context, summary configSummaryView) ([]configPathEntryView, error) {
	present := make(map[string]configLayerView)
	for _, layer := range summary.Layers {
		if layer.Source != "<built-in>" {
			present[layer.Source] = layer
		}
	}
	paths := make([]struct {
		kind config.LayerKind
		path string
	}, 0)
	userPath, err := config.DefaultUserConfigPath()
	if err != nil {
		return nil, err
	}
	paths = append(paths,
		struct {
			kind config.LayerKind
			path string
		}{config.LayerUser, userPath},
		struct {
			kind config.LayerKind
			path string
		}{config.LayerCanonical, filepath.Join(resolved.Project.Repository.Root, filepath.FromSlash(config.RelativePath))},
	)
	if resolved.Source != nil {
		paths = append(paths, struct {
			kind config.LayerKind
			path string
		}{config.LayerSource, filepath.Join(resolved.SourceRoot, filepath.FromSlash(config.RelativePath))})
		relative, relErr := filepath.Rel(resolved.ResolvedRoot, resolved.InvocationDir)
		if relErr != nil {
			return nil, relErr
		}
		current := resolved.ResolvedRoot
		directories := []string{current}
		if relative != "." {
			for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
				current = filepath.Join(current, component)
				directories = append(directories, current)
			}
		}
		for _, directory := range directories {
			paths = append(paths, struct {
				kind config.LayerKind
				path string
			}{config.LayerSubdir, filepath.Join(directory, filepath.FromSlash(config.RelativePath))})
		}
	}
	redactor := safex.NewRedactor()
	seen := map[string]struct{}{}
	entries := make([]configPathEntryView, 0, len(paths)+len(summary.Layers))
	// Applied layers come from the injected ConfigLoader and are authoritative for
	// their actual paths. Candidate discovery only supplements absent defaults.
	for _, layer := range summary.Layers {
		if layer.Source == "<built-in>" {
			continue
		}
		key := string(layer.Kind) + "\x00" + layer.Source
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, configPathEntryView{Layer: layer.Kind, Path: layer.Source, Present: true, Trusted: layer.Trusted})
	}
	for _, candidate := range paths {
		rendered := redactor.Path(filepath.Clean(candidate.path))
		key := string(candidate.kind) + "\x00" + rendered
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		layer, found := present[rendered]
		found = found && layer.Kind == candidate.kind
		entries = append(entries, configPathEntryView{Layer: candidate.kind, Path: rendered, Present: found, Trusted: found && layer.Trusted})
	}
	return entries, nil
}

func findTrustableLayer(ctx context.Context, resolved *workspace.Context, input string) (config.Layer, error) {
	if resolved == nil || resolved.Config == nil {
		return config.Layer{}, errors.New("resolved config layers are unavailable")
	}
	path, err := pathx.Canonical(inputPath(resolved.InvocationDir, input))
	if err != nil {
		return config.Layer{}, fmt.Errorf("canonicalize config trust path: %w", err)
	}
	for _, layer := range resolved.Config.Layers {
		if layer.Source != path {
			continue
		}
		if layer.TrustSubject == nil {
			return config.Layer{}, errors.New("selected config layer is inherently trusted and has no receipt subject")
		}
		return layer, nil
	}
	if resolved.Project != nil && filepath.Clean(resolved.Config.Legacy.Runtime.Path) == path {
		relative, relErr := filepath.Rel(resolved.Project.Repository.Root, path)
		if relErr != nil {
			return config.Layer{}, relErr
		}
		schema, digest, scope, identityErr := controlplane.RuntimeConfigIdentity(ctx, resolved.Project.Repository.Root, filepath.ToSlash(relative))
		if identityErr != nil {
			return config.Layer{}, identityErr
		}
		if schema != controlplane.RuntimeSchemaV2 {
			return config.Layer{}, errors.New("runtime.dispatch trust applies only to exp.runtime/v2")
		}
		subject := trust.Subject{
			ProjectID: resolved.ProjectID(), GitCommonDir: resolved.Project.Repository.GitCommonDir,
			ConfigScope: scope,
		}
		return config.Layer{
			Kind: config.LayerCanonical, Source: path, Digest: digest, Trusted: false,
			Capabilities: []trust.Capability{trust.CapabilityRuntimeDispatch}, TrustSubject: &subject,
			Fields: []string{"runtime.dispatch"},
		}, nil
	}
	return config.Layer{}, fmt.Errorf("config path %q is not a currently applied layer", input)
}

func findRevocableSubject(command *cobra.Command, app *App, resolved *workspace.Context, input string) (trust.Subject, *config.Layer, error) {
	layer, liveErr := findTrustableLayer(command.Context(), resolved, input)
	if liveErr == nil {
		return *layer.TrustSubject, &layer, nil
	}
	if resolved == nil || resolved.Project == nil {
		return trust.Subject{}, nil, liveErr
	}
	target, err := pathx.Canonical(inputPath(resolved.InvocationDir, input))
	if err != nil {
		return trust.Subject{}, nil, fmt.Errorf("canonicalize config revoke path: %w", err)
	}
	receipts, err := app.TrustStore.List(command.Context())
	if err != nil {
		return trust.Subject{}, nil, err
	}
	matches := make([]trust.Subject, 0)
	seen := map[string]struct{}{}
	for _, receipt := range receipts {
		if receipt.ProjectID != resolved.ProjectID() || receipt.ConfigScope == "" {
			continue
		}
		root := ""
		switch {
		case receipt.GitCommonDir == resolved.Project.Repository.GitCommonDir:
			root = resolved.Project.Repository.Root
		case !receipt.SourceID.IsZero() && resolved.Source != nil && receipt.SourceID == resolved.Source.ID && resolved.SourceRoot != "" && resolved.Association.Source != nil && receipt.GitCommonDir == resolved.Association.Source.GitCommonDir:
			root = resolved.SourceRoot
		case !receipt.SourceID.IsZero():
			association, lookupErr := app.Associations.LookupSource(command.Context(), receipt.ProjectID, receipt.SourceID)
			if lookupErr == nil && association.GitCommonDir == receipt.GitCommonDir {
				root = association.Root
			}
		}
		if root == "" {
			continue
		}
		candidate, candidateErr := pathx.Canonical(filepath.Join(root, filepath.FromSlash(receipt.ConfigScope)))
		if candidateErr != nil || candidate != target {
			continue
		}
		key := receipt.ProjectID.String() + "\x00" + receipt.SourceID.String() + "\x00" + receipt.GitCommonDir + "\x00" + receipt.ConfigScope
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		matches = append(matches, receipt.Subject)
	}
	if len(matches) == 1 {
		return matches[0], nil, nil
	}
	if len(matches) > 1 {
		return trust.Subject{}, nil, fmt.Errorf("config path %q matches multiple historical trust subjects", input)
	}
	return trust.Subject{}, nil, liveErr
}

func parseCapabilities(values []string, require bool) ([]trust.Capability, error) {
	if require && len(values) == 0 {
		return nil, errors.New("at least one config trust capability is required")
	}
	seen := map[trust.Capability]struct{}{}
	capabilities := make([]trust.Capability, 0, len(values))
	for _, value := range values {
		capability := trust.Capability(strings.TrimSpace(value))
		if !capability.Valid() {
			return nil, fmt.Errorf("unsupported trust capability %q", value)
		}
		if _, duplicate := seen[capability]; duplicate {
			continue
		}
		seen[capability] = struct{}{}
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(left, right int) bool { return capabilities[left] < capabilities[right] })
	if capabilities == nil {
		capabilities = []trust.Capability{}
	}
	return capabilities, nil
}

func requireLayerCapabilities(layer config.Layer, requested []trust.Capability) error {
	available := make(map[trust.Capability]struct{}, len(layer.Capabilities))
	for _, capability := range layer.Capabilities {
		available[capability] = struct{}{}
	}
	for _, capability := range requested {
		if _, found := available[capability]; !found {
			return fmt.Errorf("config layer does not define capability %s", capability)
		}
	}
	return nil
}

func makeTrustReceiptView(receipt trust.Receipt) trustReceiptView {
	approvedAt := ""
	if !receipt.ApprovedAt.IsZero() {
		approvedAt = receipt.ApprovedAt.UTC().Format(time.RFC3339Nano)
	}
	return trustReceiptView{
		ProjectID: receipt.ProjectID.String(), SourceID: receipt.SourceID.String(), ConfigScope: receipt.ConfigScope,
		ConfigDigest: receipt.ConfigDigest, Capabilities: append([]trust.Capability{}, receipt.Capabilities...), ApprovedAt: approvedAt,
	}
}

func capabilityStrings(capabilities []trust.Capability) []string {
	values := make([]string, len(capabilities))
	for index, capability := range capabilities {
		values[index] = string(capability)
	}
	return values
}

func warnUntrustedConfig(app *App, summary configSummaryView) {
	if summary.Trusted {
		return
	}
	_ = app.Warnf("effective configuration includes untrusted execution-bearing fields; use `exp config explain` before approval")
}

func renderConfigShowHuman(data configShowData) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Config digest: %s\n", data.Summary.Digest)
	fmt.Fprintf(&output, "Trusted: %t\n", data.Summary.Trusted)
	fmt.Fprintf(&output, "Default Source: %s\n", data.Effective.Defaults.Source)
	fmt.Fprintf(&output, "Workspace backend: %s\n", data.Effective.Defaults.WorkspaceBackend)
	fmt.Fprintf(&output, "Agent profile: %s\n", data.Effective.Defaults.AgentProfile)
	fmt.Fprintf(&output, "MLflow profile: %s\n", data.Effective.Defaults.MLflowProfile)
	fmt.Fprintf(&output, "Color: %s\n", data.Effective.UI.Color)
	fmt.Fprintf(&output, "Layers: %d\n", len(data.Summary.Layers))
	return output.String()
}

func renderConfigExplainHuman(data configExplainData) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Config digest: %s\n", data.Digest)
	for _, provenance := range data.Provenance {
		fmt.Fprintf(&output, "%s <- %s (%s, digest=%s, trusted=%t", provenance.Field, provenance.Source, provenance.Layer, provenance.Digest, provenance.Trusted)
		if provenance.Capability != "" {
			fmt.Fprintf(&output, ", capability=%s", provenance.Capability)
		}
		output.WriteString(")\n")
	}
	return output.String()
}
