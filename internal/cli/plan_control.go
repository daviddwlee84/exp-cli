package cli

import (
	"errors"
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

type planRefreshOptions struct {
	json            bool
	probability     float64
	impact          float64
	informationGain float64
	unblockValue    float64
	riskPenalty     float64
}

func newPlanRefreshCommand(app *App, root *rootOptions) *cobra.Command {
	options := &planRefreshOptions{probability: -1, impact: -1, informationGain: -1, unblockValue: -1, riskPenalty: -1}
	command := &cobra.Command{Use: "refresh <plan>", Short: "Repin current beliefs and dequeue the Plan for re-ranking", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runPlanRefresh(command, app, root, options, args[0])
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	command.Flags().Float64Var(&options.probability, "probability", -1, "reassess probability of improving the key metric (0..1)")
	command.Flags().Float64Var(&options.impact, "impact", -1, "reassess impact if successful")
	command.Flags().Float64Var(&options.informationGain, "information-gain", -1, "reassess information value")
	command.Flags().Float64Var(&options.unblockValue, "unblock-value", -1, "reassess downstream unblock value")
	command.Flags().Float64Var(&options.riskPenalty, "risk-penalty", -1, "reassess downside/risk penalty")
	return command
}

func runPlanRefresh(command *cobra.Command, app *App, root *rootOptions, options *planRefreshOptions, reference string) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, err)
	}
	for _, diagnostic := range inventory.Diagnostics {
		switch diagnostic.Code {
		case "plan.dependency_stale", "plan.belief_stale", "queue.plan_stale", "queue.cluster_saturated":
		default:
			return commandFailure(app, options.json, "plan refresh", struct{}{}, false, convertRecordDiagnostics(inventory.Diagnostics), inventory.Error())
		}
	}
	document, err := inventory.Resolve(reference, research.KindPlan)
	if err != nil {
		return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, err)
	}
	plan := document.Record.(*research.Plan)
	if plan.Schema != research.SchemaPlanV2 || plan.State != research.PlanQueued {
		return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, errors.New("only queued exp.plan/v2 Plans can refresh belief pins"))
	}
	if options.probability < 0 || options.probability > 1 || options.impact < 0 || options.informationGain < 0 || options.unblockValue < 0 || options.riskPenalty < 0 {
		return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, errors.New("refresh requires a complete non-negative utility reassessment and --probability must be within 0..1"))
	}
	replacement := document.Clone()
	updated := replacement.Record.(*research.Plan)
	for index, dependency := range updated.Dependencies {
		finding, resolveErr := inventory.ByID(dependency.Finding)
		if resolveErr != nil {
			return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, resolveErr)
		}
		digest, digestErr := inventory.BeliefDigest(dependency.Finding)
		if digestErr != nil {
			return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, digestErr)
		}
		updated.Dependencies[index].Revision = finding.Revision
		updated.Dependencies[index].BeliefDigest = digest
	}
	updated.Utility = &research.UtilityEstimate{
		Probability: options.probability, Impact: options.impact, InformationGain: options.informationGain,
		UnblockValue: options.unblockValue, RiskPenalty: options.riskPenalty,
	}
	updated.UpdatedAt = app.clock()
	changes := []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: document.Revision}}
	if !plan.Idea.IsZero() {
		ideaDocument, resolveErr := inventory.ByID(plan.Idea)
		if resolveErr != nil {
			return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, resolveErr)
		}
		if idea := ideaDocument.Record.(*research.Idea); idea.State == research.IdeaQueued {
			ideaReplacement := ideaDocument.Clone()
			ideaReplacement.Record.(*research.Idea).State = research.IdeaQualified
			ideaReplacement.Record.(*research.Idea).UpdatedAt = app.clock()
			changes = append(changes, record.TransactionChange{Operation: record.TransactionReplace, Document: ideaReplacement, ExpectedRevision: ideaDocument.Revision})
		}
	}
	var queueResultID research.ID
	for _, queueDocument := range inventory.OfKind(research.KindQueue) {
		queue := queueDocument.Record.(*research.Queue)
		queueReplacement := queueDocument.Clone()
		queueUpdated := queueReplacement.Record.(*research.Queue)
		found := false
		for partitionIndex := range queueUpdated.Partitions {
			entries := queueUpdated.Partitions[partitionIndex].Entries
			kept := make([]research.QueueEntry, 0, len(entries))
			for _, entry := range entries {
				if entry.Plan == plan.ID {
					found = true
					continue
				}
				kept = append(kept, entry)
			}
			queueUpdated.Partitions[partitionIndex].Entries = kept
		}
		if found {
			queueUpdated.Revision++
			queueUpdated.UpdatedAt = app.clock()
			changes = append(changes, record.TransactionChange{Operation: record.TransactionReplace, Document: queueReplacement, ExpectedRevision: queueDocument.Revision})
			queueResultID = queue.ID
		}
	}
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "plan.refresh", Changes: changes, AllowStale: true})
	if err != nil {
		return commandFailure(app, options.json, "plan refresh", struct{}{}, false, nil, err)
	}
	planResult := transactionDocument(result, research.KindPlan)
	data := struct {
		Plan  canonicalRecordView  `json:"plan"`
		Queue *canonicalRecordView `json:"queue,omitempty"`
	}{Plan: canonicalView(planResult)}
	if !queueResultID.IsZero() {
		for _, resultDocument := range result.Documents {
			if id, ok := resultDocument.ID(); ok && id == queueResultID {
				view := canonicalView(resultDocument)
				data.Queue = &view
			}
		}
	}
	return commandSuccess(app, options.json, "plan refresh", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Refreshed belief pins and dequeued Plan %s for re-ranking.\n", plan.ID))
}
