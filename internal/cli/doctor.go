package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/daviddwlee84/exp-cli/internal/provider"
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
	command.Flags().BoolVar(&options.live, "live", false, "request live probes (informational only in this milestone)")
	return command
}

func runDoctor(command *cobra.Command, app *App, options *doctorOptions) error {
	probes, err := app.Registry.DiscoverLocal(command.Context(), provider.LocalDiscoveryOptions{
		Context:   provider.ContextName("local"),
		Lookup:    app.BinaryLookup,
		Now:       app.clock,
		Redaction: provider.DefaultRedactionPolicy(),
	})
	if err != nil {
		return commandFailure(app, options.json, "doctor", doctorData{Providers: []doctorProviderView{}}, false, nil, err)
	}
	data := doctorData{
		LiveRequested:       options.live,
		LiveProbesPerformed: false,
		Providers:           makeDoctorViews(app.Registry.List(), probes),
	}
	diagnostics := []Diagnostic{}
	if options.live {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: SeverityInfo,
			Code:     "doctor.live_not_implemented",
			Message:  "live daemon, network, authentication, and service probes are not implemented in this milestone; only local discovery was performed",
		})
	}
	return commandSuccess(app, options.json, "doctor", data, false, diagnostics, renderDoctorHuman(data, diagnostics))
}

func renderDoctorHuman(data doctorData, diagnostics []Diagnostic) string {
	var output strings.Builder
	writer := tabwriter.NewWriter(&output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "PROVIDER\tSTATUS\tBINARY\tVERSION\tCAPABILITIES")
	for _, entry := range data.Providers {
		status := "found"
		binary := entry.Binary
		if entry.BuiltIn {
			status = "built-in"
			binary = "—"
		} else if entry.Missing {
			status = "missing"
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
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", entry.Name, status, binary, singleLineHuman(version), strings.Join(support, ", "))
	}
	_ = writer.Flush()
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(&output, "INFO [%s] %s\n", diagnostic.Code, diagnostic.Message)
	}
	return output.String()
}
