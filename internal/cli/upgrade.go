package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/daviddwlee84/exp-cli/internal/brewupgrade"
	"github.com/daviddwlee84/exp-cli/internal/scoopupgrade"
	"github.com/spf13/cobra"
)

var upgradeProduct = scoopupgrade.Product{Binary: "exp", Module: "github.com/daviddwlee84/exp-cli", Main: "github.com/daviddwlee84/exp-cli/cmd/exp"}

func newUpgradeCommand(app *App) *cobra.Command {
	var check, yes, jsonMode bool
	cmd := &cobra.Command{Use: "upgrade", Short: "Check or upgrade this executable through its verified package owner", Args: cobra.NoArgs}
	cmd.Flags().BoolVar(&check, "check", false, "Inspect ownership without modifying the installation")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Approve updating the inspected package")
	cmd.Flags().BoolVar(&jsonMode, "json", false, jsonFlagUsage)
	write := func(cmd *cobra.Command, r scoopupgrade.Report) error {
		var human bytes.Buffer
		if r.Manager == "homebrew" && r.Status == "checked" {
			fmt.Fprintf(&human, "Owner: Homebrew (%s)\nVersion: %s\nCommand: %v\n", r.Package, r.CurrentVersion, r.Command)
		} else {
			_ = scoopupgrade.WriteHuman(&human, r)
		}
		if r.Status == "failed" || r.Status == "blocked" || r.Status == "interrupted" || r.Status == "canceled" {
			return commandFailure(app, jsonMode, "upgrade", r, false, nil, errors.New(r.Reason))
		}
		return commandSuccess(app, jsonMode, "upgrade", r, false, nil, human.String())
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		plan, err := brewupgrade.Prepare(cmd.Context(), exe, "exp", brewupgrade.Options{Inspect: func(ctx context.Context, path string) (string, error) {
			return scoopupgrade.Inspect(ctx, path, upgradeProduct)
		}})
		if errors.Is(err, brewupgrade.ErrNotManaged) {
			r := scoopupgrade.Report{Status: "unsupported", Reason: "No supported package owner was verified. Update this installation using its original installer; Homebrew and Scoop packages support exp upgrade."}
			if check {
				return write(cmd, r)
			}
			return errors.New(r.Reason)
		}
		if err != nil {
			return err
		}
		r := scoopupgrade.Report{Status: "checked", Manager: "homebrew", Package: plan.Formula, CurrentVersion: plan.CurrentVersion, Path: plan.StablePath, Command: plan.Command(), CanUpgrade: true}
		if check {
			return write(cmd, r)
		}
		if !yes {
			if jsonMode || !scoopupgrade.IsTerminal(cmd) {
				return fmt.Errorf("upgrade requires confirmation; inspect with --check, then pass --yes")
			}
			if err := scoopupgrade.Confirm(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), "Let Homebrew update "+plan.Formula+"?"); err != nil {
				return err
			}
		}
		progress := cmd.ErrOrStderr()
		if jsonMode {
			progress = io.Discard
		}
		outcome, err := plan.Apply(cmd.Context(), progress)
		r.Status = "up-to-date"
		r.ChangeKnown = err == nil
		r.Changed = outcome.Changed
		r.Version = outcome.Version
		r.Path = outcome.Path
		if outcome.Changed {
			r.Status = "updated"
		}
		if err != nil {
			r.Status = "failed"
			r.Reason = err.Error()
		}
		if writeErr := write(cmd, r); writeErr != nil {
			return writeErr
		}
		return err
	}
	return scoopupgrade.Wrap(cmd, upgradeProduct, scoopupgrade.CommandOptions{WriteJSON: func(cmd *cobra.Command, r scoopupgrade.Report) error { return write(cmd, r) }})
}
