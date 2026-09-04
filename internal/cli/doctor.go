package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/provider"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
	"github.com/daviddwlee84/exp-cli/internal/workspacebackend"
	"github.com/spf13/cobra"
)

type doctorOptions struct {
	json bool
	live bool
}

func newDoctorCommand(app *App) *cobra.Command {
	options := &doctorOptions{}
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Inspect built-in and local optional-provider capabilities",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runDoctor(command, app, options)
		},
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	command.Flags().BoolVar(&options.live, "live", false, "run bounded version and read-only local service probes (no login, install, config write, or service start)")
	return command
}

func runDoctor(command *cobra.Command, app *App, options *doctorOptions) error {
	empty := doctorData{Providers: []doctorProviderView{}, WorkspaceBackends: []workspacebackend.Readiness{}}
	discovery := provider.LocalDiscoveryOptions{
		Context:   provider.ContextName("local"),
		Lookup:    app.BinaryLookup,
		Now:       app.clock,
		Redaction: provider.DefaultRedactionPolicy(),
	}
	if options.live {
		discovery.VersionProbe = doctorLiveProbe(app)
	}
	probes, err := app.Registry.DiscoverLocal(command.Context(), discovery)
	if err != nil {
		return commandFailure(app, options.json, "doctor", empty, false, nil, err)
	}
	cwd, err := app.Getwd()
	if err != nil {
		return commandFailure(app, options.json, "doctor", empty, false, nil, err)
	}
	workspaceBackends, err := app.WorkspaceRegistry.Statuses(command.Context(), cwd, options.live)
	if err != nil {
		return commandFailure(app, options.json, "doctor", empty, false, nil, err)
	}
	providerViews := makeDoctorViews(app.Registry.List(), probes)
	data := doctorData{
		LiveRequested:       options.live,
		LiveProbesPerformed: options.live,
		Providers:           providerViews,
		WorkspaceBackends:   workspaceBackends,
	}
	data.Partial = doctorPartial(providerViews, workspaceBackends)
	diagnostics := []Diagnostic{}
	if options.live {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: SeverityInfo,
			Code:     "doctor.local_workspace_probe",
			Message:  "bounded local version and explicit read-only service probes ran; no installation, login, configuration write, daemon start, or workload execution was performed",
		})
	}
	return commandSuccess(app, options.json, "doctor", data, data.Partial, diagnostics, renderDoctorHuman(data, diagnostics))
}

var doctorVersionPattern = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+){1,3}(?:[-+][A-Za-z0-9.-]+)?\b`)

func doctorLiveProbe(app *App) provider.LocalVersionProbe {
	return func(ctx context.Context, descriptor provider.Descriptor, binary string) (provider.LocalVersionResult, error) {
		if app == nil || app.Invoker == nil {
			return provider.LocalVersionResult{Readiness: provider.ReadinessUnknown, Reason: "probe-unavailable"}, nil
		}
		environment, err := execx.MinimalEnvironment()
		if err != nil {
			return provider.LocalVersionResult{Readiness: provider.ReadinessUnknown, Reason: "environment-unavailable"}, nil
		}
		invoke := func(arguments ...string) (execx.Result, error) {
			return app.Invoker.Invoke(ctx, execx.CommandSpec{
				Executable: binary, Argv: append([]string{}, arguments...), CWD: filepath.Dir(binary),
				Environment: environment, Timeout: 2 * time.Second,
				Output:    execx.OutputPolicy{Mode: execx.OutputCapture, MaxStdoutBytes: 64 << 10, MaxStderrBytes: 64 << 10},
				Redaction: execx.NewRedactor(),
			})
		}
		versionResult, versionErr := invoke("--version")
		if ctx.Err() != nil {
			return provider.LocalVersionResult{}, ctx.Err()
		}
		capabilities := doctorCapabilitySupport(descriptor, provider.SupportUnknown)
		if versionErr != nil {
			return provider.LocalVersionResult{
				Readiness: provider.ReadinessUnknown, Reason: "version-probe-failed", Capabilities: capabilities,
			}, nil
		}
		version := doctorVersionPattern.FindString(versionResult.Stdout + " " + versionResult.Stderr)
		if version == "" {
			return provider.LocalVersionResult{
				Readiness: provider.ReadinessUnknown, Reason: "version-output-inconclusive", Capabilities: capabilities,
			}, nil
		}
		switch descriptor.Name {
		case provider.ProviderPueue:
			if !strings.HasPrefix(version, "4.") {
				return provider.LocalVersionResult{
					Version: version, Readiness: provider.ReadinessUnsupported,
					Reason: "version-incompatible", Capabilities: capabilities,
				}, nil
			}
			capabilities = doctorCapabilitySupport(descriptor, provider.SupportSupported)
			status, statusErr := invoke("status", "--json")
			if ctx.Err() != nil {
				return provider.LocalVersionResult{}, ctx.Err()
			}
			if statusErr != nil {
				return provider.LocalVersionResult{
					Version: version, Readiness: provider.ReadinessMisconfigured,
					Reason: "service-unavailable", Capabilities: capabilities,
				}, nil
			}
			if _, err := pueue.ParseStatus([]byte(status.Stdout)); err != nil {
				return provider.LocalVersionResult{
					Version: version, Readiness: provider.ReadinessUnsupported,
					Reason: "status-contract-unsupported", Capabilities: capabilities,
				}, nil
			}
			return provider.LocalVersionResult{
				Version: version, Readiness: provider.ReadinessReady,
				Reason: "service-ready", Capabilities: capabilities,
			}, nil
		case provider.ProviderMLflow:
			// `--version` proves only executable presence. MLflow exposes no local,
			// content-free feature contract for the exact runs-describe behavior.
			return provider.LocalVersionResult{
				Version: version, Readiness: provider.ReadinessUnknown,
				Reason: "capabilities-not-probed", Capabilities: capabilities,
			}, nil
		default:
			// DVC, Slurm, Marimo, and Jupyter are descriptor/planning entries in
			// this release. A parseable version (or one Slurm binary among several)
			// cannot authorize capabilities that have no provider-specific probe.
			return provider.LocalVersionResult{
				Version: version, Readiness: provider.ReadinessUnknown,
				Reason: "capabilities-not-probed", Capabilities: capabilities,
			}, nil
		}
	}
}

func doctorCapabilitySupport(descriptor provider.Descriptor, support provider.Support) map[provider.Capability]provider.Support {
	result := make(map[provider.Capability]provider.Support, len(descriptor.Capabilities))
	for _, capability := range descriptor.Capabilities {
		result[capability] = support
	}
	return result
}

func doctorPartial(providers []doctorProviderView, workspaces []workspacebackend.Readiness) bool {
	for _, entry := range providers {
		if entry.State != provider.ReadinessBuiltIn && entry.State != provider.ReadinessReady {
			return true
		}
		for _, capability := range entry.Capabilities {
			if capability.Support == provider.SupportUnknown {
				return true
			}
		}
	}
	for _, entry := range workspaces {
		if entry.State != workspacebackend.ReadinessBuiltIn && entry.State != workspacebackend.ReadinessReady {
			return true
		}
		for _, capability := range entry.Capabilities {
			if capability.Support == workspacebackend.SupportUnknown {
				return true
			}
		}
	}
	return false
}

func renderDoctorHuman(data doctorData, diagnostics []Diagnostic) string {
	var output strings.Builder
	providers := newHumanTable("PROVIDER", "STATUS", "BINARY", "VERSION", "CAPABILITIES")
	for _, entry := range data.Providers {
		status := string(entry.State)
		binary := entry.Binary
		if entry.BuiltIn || entry.Missing {
			binary = "—"
		}
		version := entry.Version
		if version == "" {
			version = "unknown"
		}
		support := make([]string, 0, len(entry.Capabilities))
		for _, capability := range entry.Capabilities {
			support = append(support, string(capability.Name)+"="+capability.Support.String())
		}
		providers.Add(string(entry.Name), status, binary, singleLineHuman(version), strings.Join(support, ", "))
	}
	output.WriteString(mustRenderTable(providers))
	output.WriteByte('\n')
	backends := newHumanTable("WORKSPACE BACKEND", "STATUS", "PROBED", "CAPABILITIES")
	for _, entry := range data.WorkspaceBackends {
		support := make([]string, 0, len(entry.Capabilities))
		for _, capability := range entry.Capabilities {
			support = append(support, string(capability.Capability)+"="+string(capability.Support))
		}
		backends.Add(entry.Provider, string(entry.State), fmt.Sprintf("%t", entry.Probed), strings.Join(support, ", "))
	}
	output.WriteString(mustRenderTable(backends))
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(&output, "INFO [%s] %s\n", diagnostic.Code, diagnostic.Message)
	}
	return output.String()
}
