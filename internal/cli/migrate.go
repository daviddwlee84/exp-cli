package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/harnessv0"
	"github.com/spf13/cobra"
)

type migratePlanOptions struct {
	source      string
	resolutions string
	output      string
	json        bool
}

type migrateApplyOptions struct {
	plan string
	json bool
}

type migratePlanData struct {
	Plan       *harnessv0.Plan `json:"plan"`
	OutputPath string          `json:"output_path,omitempty"`
}

func newMigrateCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Plan or apply an explicit harness-v0 migration",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.AddCommand(newMigratePlanCommand(app, rootOptions), newMigrateApplyCommand(app, rootOptions))
	return command
}

func newMigratePlanCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &migratePlanOptions{}
	command := &cobra.Command{
		Use:   "plan",
		Short: "Build a read-only, fingerprinted harness-v0 migration plan",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runMigratePlan(command, app, rootOptions, options)
		},
	}
	command.Flags().StringVar(&options.source, "source", "experiments", "set the Git-root-relative harness-v0 source directory")
	command.Flags().StringVar(&options.resolutions, "resolutions", "", "read explicit needs_review resolutions from JSON PATH or -")
	command.Flags().StringVar(&options.output, "output", "", "write the complete no-clobber plan to PATH or - for raw stdout")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newMigrateApplyCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &migrateApplyOptions{}
	command := &cobra.Command{
		Use:   "apply --plan PATH",
		Short: "Apply one fully reviewed harness-v0 migration plan",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runMigrateApply(command, app, rootOptions, options)
		},
	}
	command.Flags().StringVar(&options.plan, "plan", "", "read the exact reviewed migration plan from PATH or -")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	_ = command.MarkFlagRequired("plan")
	return command
}

func runMigratePlan(command *cobra.Command, app *App, rootOptions *rootOptions, options *migratePlanOptions) error {
	if options.output == "-" && options.json {
		return commandFailure(app, true, "migrate plan", struct{}{}, false, nil, fmt.Errorf("--output - and --json cannot be combined"))
	}
	start, err := app.startDir(rootOptions.startDir)
	if err != nil {
		return commandFailure(app, options.json, "migrate plan", struct{}{}, false, nil, err)
	}
	repository, err := gitx.Discover(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, "migrate plan", struct{}{}, false, nil, err)
	}
	resolutions := harnessv0.ResolutionSet{}
	if options.resolutions != "" {
		reader, closeReader, err := migrationInput(command, options.resolutions)
		if err != nil {
			return commandFailure(app, options.json, "migrate plan", struct{}{}, false, nil, err)
		}
		if closeReader != nil {
			defer closeReader()
		}
		resolutions, err = harnessv0.DecodeResolutions(reader)
		if err != nil {
			return commandFailure(app, options.json, "migrate plan", struct{}{}, false, nil, err)
		}
	}
	plan, err := harnessv0.BuildPlan(command.Context(), harnessv0.BuildRequest{
		RepositoryRoot: repository.Root, SourceRoot: options.source,
		GeneratedAt: app.clock(), Resolutions: resolutions,
	})
	if err != nil {
		return commandFailure(app, options.json, "migrate plan", struct{}{}, false, nil, err)
	}
	encoded, err := harnessv0.EncodePlan(plan)
	if err != nil {
		return commandFailure(app, options.json, "migrate plan", struct{}{}, false, nil, err)
	}
	outputPath := ""
	if options.output == "-" {
		return successfulOutputError(writeAll(command.OutOrStdout(), encoded))
	}
	if options.output != "" {
		outputPath, err = writeMigrationPlan(options.output, encoded)
		if err != nil {
			return commandFailure(app, options.json, "migrate plan", migratePlanData{Plan: plan}, false, nil, err)
		}
	}
	diagnostics := migrationDiagnostics(plan.Diagnostics)
	data := migratePlanData{Plan: plan, OutputPath: outputPath}
	human := fmt.Sprintf("Migration plan %s: files=%d mappings=%d unknown_spans=%d applicable=%t\n", plan.ContentHash, len(plan.SourceFiles), len(plan.Mappings), len(plan.UnknownSpans), plan.Applicable)
	if outputPath != "" {
		human += "Wrote reviewed plan candidate to " + outputPath + "\n"
	} else {
		human += "Use --output PATH to save the complete plan; resolve every needs_review key before apply.\n"
	}
	return commandSuccess(app, options.json, "migrate plan", data, !plan.Applicable, diagnostics, human)
}

func runMigrateApply(command *cobra.Command, app *App, rootOptions *rootOptions, options *migrateApplyOptions) error {
	reader, closeReader, err := migrationInput(command, options.plan)
	if err != nil {
		return commandFailure(app, options.json, "migrate apply", struct{}{}, false, nil, err)
	}
	if closeReader != nil {
		defer closeReader()
	}
	plan, err := harnessv0.DecodePlan(reader)
	if err != nil {
		return commandFailure(app, options.json, "migrate apply", struct{}{}, false, nil, err)
	}
	start, err := app.startDir(rootOptions.startDir)
	if err != nil {
		return commandFailure(app, options.json, "migrate apply", struct{}{}, false, nil, err)
	}
	repository, err := gitx.Discover(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, "migrate apply", struct{}{}, false, nil, err)
	}
	result, err := harnessv0.Apply(command.Context(), harnessv0.ApplyRequest{RepositoryRoot: repository.Root, GitCommonDir: repository.GitCommonDir, Plan: plan})
	if err != nil {
		return commandFailure(app, options.json, "migrate apply", result, false, nil, err)
	}
	human := fmt.Sprintf("Migration %s; archive=%s transaction=%s\n", map[bool]string{true: "already applied", false: "applied"}[result.AlreadyApplied], result.ArchivePath, result.TransactionID)
	return commandSuccess(app, options.json, "migrate apply", result, false, nil, human)
}

func migrationInput(command *cobra.Command, path string) (io.Reader, func() error, error) {
	if path == "-" {
		return command.InOrStdin(), nil, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, nil, fmt.Errorf("migration input path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("migration input must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return file, file.Close, nil
}

func writeMigrationPlan(path string, data []byte) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(absolute)
	info, err := os.Lstat(parent)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("migration plan parent must be an existing real directory")
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", err
	}
	directory, err := os.Open(parent)
	if err != nil {
		return "", err
	}
	err = errors.Join(directory.Sync(), directory.Close())
	return absolute, err
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func migrationDiagnostics(input []harnessv0.Diagnostic) []Diagnostic {
	output := make([]Diagnostic, 0, len(input))
	for _, diagnostic := range input {
		severity := SeverityInfo
		switch diagnostic.State {
		case "needs_review", "warning":
			severity = SeverityWarning
		case "error":
			severity = SeverityError
		}
		message := diagnostic.Message
		if diagnostic.Key != "" {
			message = diagnostic.Key + ": " + message
		}
		output = append(output, Diagnostic{Severity: severity, Code: "migration." + diagnostic.Code, Message: message, Path: diagnostic.Path})
	}
	if output == nil {
		return []Diagnostic{}
	}
	return output
}
