package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

type policyOptions struct {
	json                  bool
	autonomy              string
	exploitShare          float64
	exploreShare          float64
	scoreFormula          string
	tiePolicy             string
	domains               []string
	work                  []string
	methods               []string
	components            []string
	clusterBudgetHours    float64
	plateauWindow         uint64
	minimumImprovement    float64
	minimumProbability    float64
	expectedRevision      string
	confirmAutoExperiment bool
	clusterState          string
	reopenCondition       string
}

type policyData struct {
	Policy canonicalRecordView `json:"policy"`
	Value  *research.Policy    `json:"value"`
}

func newPolicyCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "policy", Short: "Configure canonical research autonomy and queue policy", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newPolicyInitCommand(app, root), newPolicyShowCommand(app, root), newPolicyAutonomyCommand(app, root), newPolicyClusterSetCommand(app, root))
	return command
}

func newPolicyClusterSetCommand(app *App, root *rootOptions) *cobra.Command {
	options := &policyOptions{clusterState: string(research.ClusterOpen)}
	command := &cobra.Command{Use: "cluster-set <name>", Short: "Set cluster saturation thresholds or explicitly reopen a direction", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runPolicyClusterSet(command, app, root, options, args[0])
	}
	flags := command.Flags()
	flags.StringVar(&options.clusterState, "state", string(research.ClusterOpen), "set open or saturated")
	flags.Float64Var(&options.clusterBudgetHours, "budget-hours", 0, "override the cluster budget (defaults to policy threshold)")
	flags.Uint64Var(&options.plateauWindow, "plateau-window", 0, "override the plateau window (defaults to policy threshold)")
	flags.Float64Var(&options.minimumImprovement, "minimum-improvement", 0, "override the material-improvement threshold")
	flags.Float64Var(&options.minimumProbability, "minimum-probability", 0, "override the probability threshold")
	flags.StringVar(&options.reopenCondition, "reopen-condition", "", "state the evidence required to reopen a saturated cluster")
	flags.StringVar(&options.expectedRevision, "expected-revision", "", "require an exact current POLICY.md revision")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newPolicyInitCommand(app *App, root *rootOptions) *cobra.Command {
	options := defaultPolicyOptions()
	command := &cobra.Command{Use: "init", Short: "Create the explicit default-manual POLICY.md singleton", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runPolicyInit(command, app, root, options) }
	flags := command.Flags()
	flags.StringVar(&options.autonomy, "autonomy", string(research.AutonomyManual), "set manual, shadow, assisted, or limited autonomy")
	flags.Float64Var(&options.exploitShare, "exploit-share", .8, "set the exploit scheduling share")
	flags.Float64Var(&options.exploreShare, "explore-share", .2, "set the explore scheduling share")
	flags.StringVar(&options.scoreFormula, "score-formula", "utility-v1", "set the versioned transparent queue formula slug")
	flags.StringVar(&options.tiePolicy, "tie-policy", string(research.QueueTieKeepIncumbent), "set keep_incumbent or human_review")
	flags.StringSliceVar(&options.domains, "domains", []string{"general"}, "set controlled domain slugs")
	flags.StringSliceVar(&options.work, "work", []string{"experiment"}, "set controlled work slugs")
	flags.StringSliceVar(&options.methods, "methods", []string{"empirical"}, "set controlled method slugs")
	flags.StringSliceVar(&options.components, "components", []string{"core"}, "set controlled component slugs")
	flags.Float64Var(&options.clusterBudgetHours, "cluster-budget-hours", 168, "default cluster saturation budget")
	flags.Uint64Var(&options.plateauWindow, "plateau-window", 5, "default number of results used for plateau detection")
	flags.Float64Var(&options.minimumImprovement, "minimum-improvement", 0, "minimum material improvement for an open cluster")
	flags.Float64Var(&options.minimumProbability, "minimum-probability", .05, "minimum probability for an open cluster")
	flags.BoolVar(&options.confirmAutoExperiment, "confirm-auto-experiment", false, "explicitly enable autonomous experiment dispatch for assisted/limited mode")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newPolicyShowCommand(app *App, root *rootOptions) *cobra.Command {
	options := &policyOptions{}
	command := &cobra.Command{Use: "show", Short: "Show canonical autonomy and queue policy", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runPolicyShow(command, app, root, options) }
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newPolicyAutonomyCommand(app *App, root *rootOptions) *cobra.Command {
	options := &policyOptions{}
	command := &cobra.Command{Use: "autonomy <manual|shadow|assisted|limited>", Short: "Change autonomy with an explicit auto-experiment gate", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		options.autonomy = args[0]
		return runPolicyAutonomy(command, app, root, options)
	}
	command.Flags().StringVar(&options.expectedRevision, "expected-revision", "", "require an exact current POLICY.md revision")
	command.Flags().BoolVar(&options.confirmAutoExperiment, "confirm-auto-experiment", false, "explicitly acknowledge assisted/limited autonomous experiment dispatch")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func defaultPolicyOptions() *policyOptions { return &policyOptions{} }

func runPolicyInit(command *cobra.Command, app *App, root *rootOptions, options *policyOptions) error {
	mode, err := validatedAutonomy(options.autonomy, options.confirmAutoExperiment)
	if err != nil {
		return commandFailure(app, options.json, "policy init", policyData{}, false, nil, err)
	}
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "policy init", policyData{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "policy init", policyData{}, false, nil, err)
	}
	if inventory.Policy != nil {
		return commandFailure(app, options.json, "policy init", policyData{}, false, nil, record.ErrAlreadyExists)
	}
	now := app.clock()
	policy := &research.Policy{
		Schema: research.SchemaPolicy, CreatedAt: now, UpdatedAt: now, Autonomy: mode,
		ExploitShare: options.exploitShare, ExploreShare: options.exploreShare,
		ScoreFormula: options.scoreFormula, TiePolicy: research.QueueTiePolicy(options.tiePolicy), PromotionRequiresHuman: true,
		Taxonomy: research.ClassificationTaxonomy{Domains: options.domains, Work: options.work, Methods: options.methods, Components: options.components},
		ClusterSaturation: research.ClusterSaturationPolicy{
			BudgetHours: options.clusterBudgetHours, PlateauWindow: options.plateauWindow,
			MinimumImprovement: options.minimumImprovement, MinimumProbability: options.minimumProbability,
		},
	}
	document := &record.Document{Record: policy, Body: "\n# Research policy\n\nAutonomy and constrained-resource allocation policy.\n"}
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "policy.init", Changes: []record.TransactionChange{{Operation: record.TransactionCreate, Document: document}}})
	if err != nil {
		return commandFailure(app, options.json, "policy init", policyData{}, false, nil, err)
	}
	published := transactionDocument(result, research.KindPolicy)
	data := policyData{Policy: canonicalView(published), Value: published.Record.(*research.Policy)}
	diagnostics := refreshAfterTransaction(command, app, info, store)
	return commandSuccess(app, options.json, "policy init", data, false, diagnostics, fmt.Sprintf("Created POLICY.md in %s mode.\n", mode))
}

func runPolicyShow(command *cobra.Command, app *App, root *rootOptions, options *policyOptions) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "policy show", policyData{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil || inventory.Policy == nil {
		if err == nil {
			err = errors.New("POLICY.md is absent; run exp policy init")
		}
		return commandFailure(app, options.json, "policy show", policyData{}, false, nil, err)
	}
	policy := inventory.Policy.Record.(*research.Policy)
	data := policyData{Policy: canonicalView(inventory.Policy), Value: policy}
	human := fmt.Sprintf("Autonomy: %s; lanes: %.0f%% exploit / %.0f%% explore; formula: %s; promotion human gate: %t\n", policy.Autonomy, 100*policy.ExploitShare, 100*policy.ExploreShare, policy.ScoreFormula, policy.PromotionRequiresHuman)
	return commandSuccess(app, options.json, "policy show", data, false, nil, human)
}

func runPolicyAutonomy(command *cobra.Command, app *App, root *rootOptions, options *policyOptions) error {
	mode, err := validatedAutonomy(options.autonomy, options.confirmAutoExperiment)
	if err != nil {
		return commandFailure(app, options.json, "policy autonomy", policyData{}, false, nil, err)
	}
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "policy autonomy", policyData{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil || inventory.Policy == nil {
		if err == nil {
			err = errors.New("POLICY.md is absent; run exp policy init")
		}
		return commandFailure(app, options.json, "policy autonomy", policyData{}, false, nil, err)
	}
	if options.expectedRevision != "" && options.expectedRevision != inventory.Policy.Revision {
		return commandFailure(app, options.json, "policy autonomy", policyData{}, false, nil, &record.ConflictError{Path: record.PolicyFile, Expected: options.expectedRevision, Actual: inventory.Policy.Revision})
	}
	replacement := inventory.Policy.Clone()
	policy := replacement.Record.(*research.Policy)
	policy.Autonomy = mode
	policy.UpdatedAt = app.clock()
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "policy.autonomy", Changes: []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: inventory.Policy.Revision}}})
	if err != nil {
		return commandFailure(app, options.json, "policy autonomy", policyData{}, false, nil, err)
	}
	published := transactionDocument(result, research.KindPolicy)
	data := policyData{Policy: canonicalView(published), Value: published.Record.(*research.Policy)}
	diagnostics := refreshAfterTransaction(command, app, info, store)
	return commandSuccess(app, options.json, "policy autonomy", data, false, diagnostics, fmt.Sprintf("Research autonomy is now %s.\n", mode))
}

func validatedAutonomy(value string, confirmed bool) (research.AutonomyMode, error) {
	mode := research.AutonomyMode(strings.TrimSpace(value))
	switch mode {
	case research.AutonomyManual, research.AutonomyShadow:
		return mode, nil
	case research.AutonomyAssisted, research.AutonomyLimited:
		if !confirmed {
			return "", errors.New("assisted/limited mode requires --confirm-auto-experiment; production promotion remains human-only")
		}
		return mode, nil
	default:
		return "", fmt.Errorf("unknown autonomy mode %q", value)
	}
}

func runPolicyClusterSet(command *cobra.Command, app *App, root *rootOptions, options *policyOptions, name string) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "policy cluster-set", policyData{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil || inventory.Policy == nil {
		if err == nil {
			err = errors.New("POLICY.md is absent; run exp policy init")
		}
		return commandFailure(app, options.json, "policy cluster-set", policyData{}, false, nil, err)
	}
	if options.expectedRevision != "" && options.expectedRevision != inventory.Policy.Revision {
		return commandFailure(app, options.json, "policy cluster-set", policyData{}, false, nil, &record.ConflictError{Path: record.PolicyFile, Expected: options.expectedRevision, Actual: inventory.Policy.Revision})
	}
	replacement := inventory.Policy.Clone()
	policy := replacement.Record.(*research.Policy)
	cluster := research.ClusterPolicy{
		Name: name, State: research.ClusterState(options.clusterState),
		BudgetHours: policy.ClusterSaturation.BudgetHours, PlateauWindow: policy.ClusterSaturation.PlateauWindow,
		MinimumImprovement: policy.ClusterSaturation.MinimumImprovement, MinimumProbability: policy.ClusterSaturation.MinimumProbability,
		ReopenCondition: options.reopenCondition,
	}
	if command.Flags().Changed("budget-hours") {
		cluster.BudgetHours = options.clusterBudgetHours
	}
	if command.Flags().Changed("plateau-window") {
		cluster.PlateauWindow = options.plateauWindow
	}
	if command.Flags().Changed("minimum-improvement") {
		cluster.MinimumImprovement = options.minimumImprovement
	}
	if command.Flags().Changed("minimum-probability") {
		cluster.MinimumProbability = options.minimumProbability
	}
	found := false
	for index := range policy.Clusters {
		if policy.Clusters[index].Name == name {
			policy.Clusters[index] = cluster
			found = true
			break
		}
	}
	if !found {
		policy.Clusters = append(policy.Clusters, cluster)
	}
	policy.UpdatedAt = app.clock()
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "policy.cluster-set", Changes: []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: inventory.Policy.Revision}}})
	if err != nil {
		return commandFailure(app, options.json, "policy cluster-set", policyData{}, false, nil, err)
	}
	published := transactionDocument(result, research.KindPolicy)
	data := policyData{Policy: canonicalView(published), Value: published.Record.(*research.Policy)}
	return commandSuccess(app, options.json, "policy cluster-set", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Cluster %s is now %s.\n", name, options.clusterState))
}
