package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"github.com/daviddwlee84/exp-cli/internal/trust"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/spf13/cobra"
)

func prepareGuidedConfigTrust(command *cobra.Command, app *App, root *rootOptions, options *configTrustOptions) error {
	resolved, authority, _, err := observeWizardAuthority(command, app, root, false, true)
	if err != nil {
		return err
	}
	candidates, err := guidedTrustableLayers(command.Context(), resolved)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return errors.New("no currently applied execution-bearing repository config layer is available to trust")
	}
	draft := *options
	draft.capabilities = append([]string{}, options.capabilities...)
	if strings.TrimSpace(draft.path) == "" {
		fallback := ""
		fallbackDisplay := ""
		if len(candidates) == 1 {
			fallback = candidates[0].Source
			fallbackDisplay = guidedLayerDisplay(candidates[0])
		}
		draft.path, err = app.askValidated(command.Context(), PromptRequest{
			Label: "Config layer", Description: guidedLayerChoices(candidates), Default: fallback, DefaultDisplay: fallbackDisplay,
		}, func(value string) error {
			_, layerErr := findTrustableLayer(command.Context(), resolved, value)
			return layerErr
		})
		if err != nil {
			return err
		}
	}
	layer, err := findTrustableLayer(command.Context(), resolved, draft.path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(draft.digest) != "" && strings.TrimSpace(draft.digest) != layer.Digest {
		return errors.New("provided digest does not equal the current config layer digest")
	}
	draft.digest = layer.Digest
	if len(draft.capabilities) == 0 {
		customize, askErr := app.askYesNo(command.Context(), "Customize trust capabilities", "The default approves every execution-bearing capability defined by this exact layer", false)
		if askErr != nil {
			return askErr
		}
		if !customize {
			draft.capabilities = capabilityStrings(layer.Capabilities)
		} else {
			selected, selectErr := app.askValidated(command.Context(), PromptRequest{
				Label: "Capabilities", Description: "Comma-separated subset of " + strings.Join(capabilityStrings(layer.Capabilities), ", "),
			}, func(value string) error {
				parsed, parseErr := parseCapabilities(parseGuidedList(value), true)
				if parseErr != nil {
					return parseErr
				}
				return requireLayerCapabilities(layer, parsed)
			})
			if selectErr != nil {
				return selectErr
			}
			draft.capabilities = parseGuidedList(selected)
		}
	}
	capabilities, err := parseCapabilities(draft.capabilities, true)
	if err != nil {
		return err
	}
	if err := requireLayerCapabilities(layer, capabilities); err != nil {
		return err
	}
	inspection, err := app.TrustStore.Inspect(command.Context(), trust.Query{
		Subject: *layer.TrustSubject, ConfigDigest: layer.Digest, Capabilities: capabilities,
	})
	if err != nil {
		return err
	}
	layerFingerprint := guidedLayerFingerprint(layer)
	trustPath, err := app.TrustStore.Path()
	if err != nil {
		return err
	}
	tools := guidedTrustTools(command.Context(), app, resolved, capabilities)
	if err := writeGuidedPlan(app, guidedPlan{
		Title: "Review config trust plan",
		Summary: append(projectWizardFields(resolved, authority),
			guidedField{Label: "Layer", Value: string(layer.Kind)}, guidedField{Label: "Config digest", Value: layer.Digest},
			guidedField{Label: "Capabilities", Value: strings.Join(capabilityStrings(capabilities), ", ")},
			guidedField{Label: "Already trusted", Value: fmt.Sprintf("%t", inspection.Trusted)},
		),
		Effects: []string{"Approve only the selected capabilities for the exact current config digest", "Replace stale overlapping capability receipts for this same logical config scope"},
		Paths:   []string{layer.Source, trustPath},
		Source: []guidedField{
			{Label: "Project", Value: layer.TrustSubject.ProjectID.String()}, {Label: "Source", Value: layer.TrustSubject.SourceID.String()},
			{Label: "Config scope", Value: layer.TrustSubject.ConfigScope},
		},
		Snapshot: []guidedField{
			{Label: "Effective config digest", Value: authority.ConfigDigest}, {Label: "Trust state digest", Value: authority.TrustDigest},
			{Label: "Fields", Value: displayGuidedValues(layer.Fields)}, {Label: "Git-common identity", Value: guidedSubjectFilesystemIdentity(*layer.TrustSubject)},
		},
		Tools: tools,
	}); err != nil {
		return err
	}
	if err := app.requireConfirmation(command.Context(), "Trust this config", "Type TRUST because these fields can select executors, profiles, or external-impact behavior", "TRUST"); err != nil {
		return err
	}
	if err := revalidateWizardAuthority(command, app, root, authority, false, true); err != nil {
		return err
	}
	currentResolved, err := resolveWorkspaceContext(command, app, root)
	if err != nil {
		return err
	}
	currentLayer, err := findTrustableLayer(command.Context(), currentResolved, draft.path)
	if err != nil {
		return err
	}
	if err := requireSameGuidedObservation("Config trust layer", layerFingerprint, guidedLayerFingerprint(currentLayer)); err != nil {
		return err
	}
	draft.confirm = true
	*options = draft
	return nil
}

func guidedTrustableLayers(ctx context.Context, resolved *workspace.Context) ([]config.Layer, error) {
	if resolved == nil || resolved.Config == nil {
		return nil, errors.New("resolved config layers are unavailable")
	}
	layers := make([]config.Layer, 0)
	seen := make(map[string]struct{})
	for _, layer := range resolved.Config.Layers {
		if layer.TrustSubject == nil || len(layer.Capabilities) == 0 {
			continue
		}
		key := layer.Source + "\x00" + layer.Digest
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		layers = append(layers, layer)
	}
	if path := strings.TrimSpace(resolved.Config.Legacy.Runtime.Path); path != "" {
		if layer, err := findTrustableLayer(ctx, resolved, path); err == nil {
			key := layer.Source + "\x00" + layer.Digest
			if _, duplicate := seen[key]; !duplicate {
				seen[key] = struct{}{}
				layers = append(layers, layer)
			}
		}
	}
	sort.Slice(layers, func(left, right int) bool {
		if layers[left].Kind != layers[right].Kind {
			return layers[left].Kind < layers[right].Kind
		}
		return layers[left].Source < layers[right].Source
	})
	return layers, nil
}

func guidedLayerChoices(layers []config.Layer) string {
	choices := make([]string, 0, len(layers))
	for _, layer := range layers {
		choices = append(choices, guidedLayerDisplay(layer))
	}
	if len(choices) == 0 {
		return "No trustable layers"
	}
	return "Currently applied: " + strings.Join(choices, "; ")
}

func guidedLayerDisplay(layer config.Layer) string {
	path := safex.NewRedactor().Path(layer.Source)
	return string(layer.Kind) + " " + path
}

func guidedLayerFingerprint(layer config.Layer) string {
	parts := []string{string(layer.Kind), layer.Source, layer.Digest, fmt.Sprintf("%t", layer.Trusted)}
	parts = append(parts, capabilityStrings(layer.Capabilities)...)
	parts = append(parts, layer.Fields...)
	if layer.TrustSubject != nil {
		parts = append(parts,
			layer.TrustSubject.ProjectID.String(), layer.TrustSubject.SourceID.String(), layer.TrustSubject.GitCommonDir,
			layer.TrustSubject.GitCommonIdentity, layer.TrustSubject.ConfigScope,
		)
	}
	return guidedFingerprint(parts...)
}

func guidedSubjectFilesystemIdentity(subject trust.Subject) string {
	identity, err := pathx.DirectoryFilesystemIdentity(subject.GitCommonDir)
	if err != nil {
		return "unavailable"
	}
	return identity
}

func guidedTrustTools(ctx context.Context, app *App, resolved *workspace.Context, capabilities []trust.Capability) []guidedTool {
	tools := make([]guidedTool, 0)
	for _, capability := range capabilities {
		switch capability {
		case trust.CapabilityWorkspaceBackend:
			cwd := ""
			if resolved != nil {
				cwd = resolved.InvocationDir
			}
			for _, tool := range workspaceToolReadiness(ctx, app, cwd) {
				tools = append(tools, tool)
			}
		case trust.CapabilityMLflowProfile:
			tools = append(tools, mlflowToolReadiness(app, resolved, ""))
		case trust.CapabilityRuntimeDispatch:
			tools = append(tools, binaryOptionalTool(app, "pueue", "Install and configure Pueue before runtime dispatch"))
		case trust.CapabilityAgentProfile:
			tools = append(tools, guidedTool{Name: "agent profile", Status: "policy-only", Reason: "binary and environment values resolve only at explicit execution", Available: true})
		case trust.CapabilitySourceSelection:
			tools = append(tools, guidedTool{Name: "Source resolver", Status: "ready", Reason: "built-in exact canonical Source resolution", Available: true})
		}
	}
	if len(tools) == 0 {
		tools = append(tools, guidedTool{Name: "optional executor", Status: "disabled", Reason: "selected capabilities do not require an optional local tool", Remediation: "none"})
	}
	return tools
}

func binaryOptionalTool(app *App, name, remediation string) guidedTool {
	if app == nil || app.BinaryLookup == nil {
		return guidedTool{Name: name, Status: "disabled", Reason: "binary lookup unavailable", Remediation: remediation}
	}
	if _, err := app.BinaryLookup(name); err != nil {
		return guidedTool{Name: name, Status: "disabled", Reason: "optional binary is missing", Remediation: remediation}
	}
	return guidedTool{Name: name, Status: "ready", Reason: "optional binary found; no service or credential probe was performed", Available: true}
}
