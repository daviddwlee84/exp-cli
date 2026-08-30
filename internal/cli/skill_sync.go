package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/spf13/cobra"
)

const (
	commandReferenceSourcePath = "internal/skill/exp-cli/references/commands.md"
	commandReferenceMaxBytes   = 1 << 20
)

var errSkillSourceDrift = errors.New("source command reference differs from generated command metadata")

type skillSyncOptions struct {
	check bool
}

func newSkillSyncCommand(app *App) *cobra.Command {
	options := &skillSyncOptions{}
	command := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize the source-tree command reference for development",
		Long:  "Synchronize the source-tree command reference from curated metadata for the actual Cobra tree. This is a developer-only source maintenance command.",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runSkillSync(command, app, options)
		},
	}
	command.Flags().BoolVar(&options.check, "check", false, "report source-tree command-reference drift without writing")
	return command
}

func runSkillSync(command *cobra.Command, app *App, options *skillSyncOptions) error {
	if err := command.Context().Err(); err != nil {
		return err
	}
	if app.Getwd == nil {
		return fmt.Errorf("current-directory lookup is not configured")
	}
	startDir, err := app.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	repositoryRoot, err := findCommandReferenceSourceRoot(startDir)
	if err != nil {
		return err
	}

	expected, err := GenerateCommandReference(command.Root())
	if err != nil {
		return fmt.Errorf("generate command reference: %w", err)
	}
	root, err := pathx.OpenCanonicalRootNoSymlinks(repositoryRoot)
	if err != nil {
		return fmt.Errorf("open source root: %w", err)
	}
	defer root.Close()
	actual, identity, err := pathx.ReadBoundedRegularFile(command.Context(), root, commandReferenceSourcePath, commandReferenceMaxBytes)
	if err != nil {
		return fmt.Errorf("read source command reference: %w", err)
	}
	if bytes.Equal(actual, []byte(expected)) {
		return app.WriteHuman(safeHumanOutput(fmt.Sprintf("Skill command reference is current at %s\n", filepath.Join(repositoryRoot, filepath.FromSlash(commandReferenceSourcePath)))))
	}
	if options.check {
		if writeErr := app.WriteHuman(safeHumanOutput(fmt.Sprintf("Skill command reference drift at %s\n", filepath.Join(repositoryRoot, filepath.FromSlash(commandReferenceSourcePath))))); writeErr != nil {
			return writeErr
		}
		return errSkillSourceDrift
	}

	if err := record.AtomicWriteDerivedRoot(root, commandReferenceSourcePath, []byte(expected), record.AtomicWriteOptions{
		Expected:        identity,
		ExpectedContent: actual,
		Mode:            identity.Mode().Perm(),
	}); err != nil {
		return fmt.Errorf("replace source command reference: %w", err)
	}
	return app.WriteHuman(safeHumanOutput(fmt.Sprintf("Synchronized skill command reference at %s\n", filepath.Join(repositoryRoot, filepath.FromSlash(commandReferenceSourcePath)))))
}

func findCommandReferenceSourceRoot(startDir string) (string, error) {
	if startDir == "" {
		return "", fmt.Errorf("current-directory lookup returned an empty path")
	}
	absolute, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("make source search path absolute: %w", err)
	}
	current := filepath.Clean(absolute)
	for {
		candidate := filepath.Join(current, filepath.FromSlash(commandReferenceSourcePath))
		info, inspectErr := os.Lstat(candidate)
		switch {
		case inspectErr == nil:
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return "", fmt.Errorf("source command reference %s is not a regular non-symlink file", candidate)
			}
			canonical, canonicalErr := pathx.Canonical(current)
			if canonicalErr != nil {
				return "", fmt.Errorf("canonicalize source root: %w", canonicalErr)
			}
			return canonical, nil
		case !errors.Is(inspectErr, fs.ErrNotExist):
			return "", fmt.Errorf("inspect source command reference %s: %w", candidate, inspectErr)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("could not find %s from %s or any parent directory", filepath.FromSlash(commandReferenceSourcePath), absolute)
		}
		current = parent
	}
}
