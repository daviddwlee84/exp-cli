package workspacebackend

import (
	"errors"
	"fmt"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/config"
)

// ResolveSelection applies explicit flag -> trusted repository/subdirectory
// config -> user config -> built-in native precedence. A repository layer that
// wins an execution-bearing field without an exact trust receipt fails closed;
// it is never silently skipped in favor of a lower layer.
func ResolveSelection(explicit string, result *config.Result) (ProviderSelection, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		if !knownProvider(explicit) {
			return ProviderSelection{}, fmt.Errorf("workspace provider %q: %w", explicit, ErrUnknownProvider)
		}
		return ProviderSelection{
			Requested:                explicit,
			Origin:                   SelectionExplicit,
			Trusted:                  true,
			AllowUnsupportedFallback: explicit == DevCLIName,
		}, nil
	}
	if result == nil {
		return nativeSelection(), nil
	}

	requested := strings.TrimSpace(result.Effective.Defaults.WorkspaceBackend)
	if !knownProvider(requested) {
		return ProviderSelection{}, fmt.Errorf("workspace provider %q: %w", requested, ErrUnknownProvider)
	}
	provenance, found := result.SourceFor("defaults.workspace_backend")
	if !found {
		return ProviderSelection{}, fmt.Errorf("workspace provider selection has no provenance: %w", ErrUntrustedSelection)
	}
	origin, err := selectionOrigin(provenance.Layer)
	if err != nil {
		return ProviderSelection{}, err
	}
	if !provenance.Trusted {
		return ProviderSelection{}, fmt.Errorf("workspace provider %q from %s is not trusted: %w", requested, provenance.Layer, errors.Join(ErrUntrustedSelection, config.ErrUntrusted))
	}
	preference, found := result.Effective.Workspace.Backends[requested]
	if !found || !preference.Enabled {
		return ProviderSelection{}, fmt.Errorf("workspace provider %q is not enabled", requested)
	}
	if field := "workspace.backends." + requested + ".enabled"; hasField(result, field) {
		if err := result.RequireTrusted(field); err != nil {
			return ProviderSelection{}, fmt.Errorf("workspace provider enablement is not trusted: %w", errors.Join(ErrUntrustedSelection, err))
		}
	}

	selection := ProviderSelection{Requested: requested, Origin: origin, Trusted: true}
	if requested == NativeGitName {
		return selection, nil
	}
	if hasField(result, "workspace.preferred_backends") {
		if err := result.RequireTrusted("workspace.preferred_backends"); err != nil {
			return ProviderSelection{}, fmt.Errorf("workspace fallback policy is not trusted: %w", errors.Join(ErrUntrustedSelection, err))
		}
	}
	if hasField(result, "workspace.backends.native_git.enabled") {
		if err := result.RequireTrusted("workspace.backends.native_git.enabled"); err != nil {
			return ProviderSelection{}, fmt.Errorf("native fallback enablement is not trusted: %w", errors.Join(ErrUntrustedSelection, err))
		}
	}
	if !nativeFallbackConfigured(result) {
		return selection, nil
	}
	selection.AllowUnavailableFallback = true
	selection.AllowUnsupportedFallback = true
	return selection, nil
}

func nativeSelection() ProviderSelection {
	return ProviderSelection{Requested: NativeGitName, Origin: SelectionBuiltin, Trusted: true}
}

func knownProvider(name string) bool { return name == NativeGitName || name == DevCLIName }

func selectionOrigin(layer config.LayerKind) (SelectionOrigin, error) {
	switch layer {
	case config.LayerBuiltin:
		return SelectionBuiltin, nil
	case config.LayerUser:
		return SelectionUser, nil
	case config.LayerCanonical, config.LayerSource, config.LayerSubdir:
		return SelectionRepository, nil
	default:
		return "", fmt.Errorf("workspace provider provenance layer %q is unsupported: %w", layer, ErrUntrustedSelection)
	}
}

func nativeFallbackConfigured(result *config.Result) bool {
	if result == nil {
		return true
	}
	native, found := result.Effective.Workspace.Backends[NativeGitName]
	if !found || !native.Enabled {
		return false
	}
	for _, name := range result.Effective.Workspace.PreferredBackends {
		if name == NativeGitName {
			return true
		}
	}
	return false
}

func hasField(result *config.Result, field string) bool {
	if result == nil {
		return false
	}
	_, found := result.SourceFor(field)
	return found
}
