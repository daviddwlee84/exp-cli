package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

type poolOptions struct {
	json        bool
	title       string
	body        string
	capacity    uint64
	unit        string
	bottleneck  string
	costPerHour float64
	disabled    bool
}

func newPoolCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "pool", Short: "Define constrained compute or human resource pools", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newPoolAddCommand(app, root), newPoolListCommand(app, root))
	return command
}

func newPoolAddCommand(app *App, root *rootOptions) *cobra.Command {
	options := &poolOptions{capacity: 1}
	command := &cobra.Command{Use: "add", Short: "Create a named canonical ResourcePool", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runPoolAdd(command, app, root, options) }
	flags := command.Flags()
	flags.StringVar(&options.title, "title", "", "set the pool title")
	flags.StringVar(&options.body, "body", "", "set optional Markdown detail")
	flags.Uint64Var(&options.capacity, "capacity", 1, "set maximum concurrent units")
	flags.StringVar(&options.unit, "unit", "slot", "name one capacity unit")
	flags.StringVar(&options.bottleneck, "bottleneck", "compute", "set the controlled bottleneck slug")
	flags.Float64Var(&options.costPerHour, "cost-per-hour", 0, "set optional non-negative cost per unit-hour")
	flags.BoolVar(&options.disabled, "disabled", false, "create the pool disabled")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newPoolListCommand(app *App, root *rootOptions) *cobra.Command {
	options := &poolOptions{}
	command := &cobra.Command{Use: "list", Short: "List canonical ResourcePools", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runPoolList(command, app, root, options) }
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runPoolAdd(command *cobra.Command, app *App, root *rootOptions, options *poolOptions) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "pool add", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "pool add", struct{}{}, false, nil, err)
	}
	if inventory.Policy == nil {
		return commandFailure(app, options.json, "pool add", struct{}{}, false, nil, errors.New("POLICY.md is required; run exp policy init"))
	}
	now := app.clock()
	id, err := generatedID(app, research.KindResourcePool, now)
	if err != nil {
		return commandFailure(app, options.json, "pool add", struct{}{}, false, nil, err)
	}
	var cost *float64
	if command.Flags().Changed("cost-per-hour") {
		value := options.costPerHour
		cost = &value
	}
	body := options.body
	if body == "" {
		body = "\n# " + options.title + "\n"
	}
	pool := &research.ResourcePool{
		Common:  research.Common{Schema: research.SchemaResourcePool, ID: id, Title: options.title, CreatedAt: now, UpdatedAt: now},
		Enabled: !options.disabled, Capacity: options.capacity, Unit: options.unit, Bottleneck: options.bottleneck, CostPerHour: cost,
	}
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "pool.add", Changes: []record.TransactionChange{{Operation: record.TransactionCreate, Document: &record.Document{Record: pool, Body: body}}}})
	if err != nil {
		return commandFailure(app, options.json, "pool add", struct{}{}, false, nil, err)
	}
	published := transactionDocument(result, research.KindResourcePool)
	data := struct {
		Pool canonicalRecordView `json:"pool"`
	}{Pool: canonicalView(published)}
	return commandSuccess(app, options.json, "pool add", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Created ResourcePool %s with capacity %d %s.\n", id, options.capacity, options.unit))
}

func runPoolList(command *cobra.Command, app *App, root *rootOptions, options *poolOptions) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "pool list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "pool list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	views := []canonicalRecordView{}
	var human strings.Builder
	for _, document := range inventory.OfKind(research.KindResourcePool) {
		views = append(views, canonicalView(document))
		pool := document.Record.(*research.ResourcePool)
		fmt.Fprintf(&human, "%s\tcapacity=%d %s\tenabled=%t\t%s\n", pool.ID, pool.Capacity, pool.Unit, pool.Enabled, pool.Title)
	}
	if len(views) == 0 {
		human.WriteString("No ResourcePools.\n")
	}
	return commandSuccess(app, options.json, "pool list", recordListData{Records: views}, false, convertRecordDiagnostics(inventory.Diagnostics), human.String())
}
