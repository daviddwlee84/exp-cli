package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/queueflow"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

type queueOptions struct {
	json              bool
	title             string
	body              string
	pools             []string
	pool              string
	lane              string
	position          int
	score             float64
	agent             bool
	config            string
	advisorProfile    string
	battleProfile     string
	minimumConfidence float64
	pinned            bool
}

type queueEntryView struct {
	Position     int     `json:"position"`
	Plan         string  `json:"plan"`
	Title        string  `json:"title"`
	PlanRevision string  `json:"plan_revision"`
	Score        float64 `json:"score"`
	Pinned       bool    `json:"pinned"`
}

type queuePartitionView struct {
	Pool    string           `json:"pool"`
	Lane    string           `json:"lane"`
	Entries []queueEntryView `json:"entries"`
}

type queueView struct {
	Queue      canonicalRecordView  `json:"queue"`
	Revision   uint64               `json:"queue_revision"`
	Paused     bool                 `json:"paused"`
	Partitions []queuePartitionView `json:"partitions"`
}

type queueInsertData struct {
	Queue         canonicalRecordView   `json:"queue,omitempty"`
	Position      int                   `json:"position"`
	Score         float64               `json:"score"`
	Applied       bool                  `json:"applied"`
	NeedsHuman    bool                  `json:"needs_human"`
	Reason        string                `json:"reason,omitempty"`
	Advice        *canonicalRecordView  `json:"advice,omitempty"`
	Battles       []canonicalRecordView `json:"battles"`
	TransactionID string                `json:"transaction_id,omitempty"`
	AgentProfile  string                `json:"agent_profile,omitempty"`
	ReportedModel string                `json:"reported_model,omitempty"`
}

func newQueueCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "queue", Short: "Rank Plans across constrained pool/lane queues", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(
		newQueueCreateCommand(app, root),
		newQueueListCommand(app, root),
		newQueueShowCommand(app, root),
		newQueueInsertCommand(app, root),
		newQueueRemoveCommand(app, root),
	)
	return command
}

func newQueueCreateCommand(app *App, root *rootOptions) *cobra.Command {
	options := &queueOptions{}
	command := &cobra.Command{Use: "create", Short: "Create exploit/explore partitions for named ResourcePools", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runQueueCreate(command, app, root, options) }
	command.Flags().StringVar(&options.title, "title", "Main research queue", "set the Queue title")
	command.Flags().StringVar(&options.body, "body", "", "set optional Markdown detail")
	command.Flags().StringSliceVar(&options.pools, "pool", nil, "add a ResourcePool by ID or display code")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newQueueListCommand(app *App, root *rootOptions) *cobra.Command {
	options := &queueOptions{}
	command := &cobra.Command{Use: "list", Short: "List canonical Queues", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runQueueList(command, app, root, options) }
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newQueueShowCommand(app *App, root *rootOptions) *cobra.Command {
	options := &queueOptions{}
	command := &cobra.Command{Use: "show <queue>", Short: "Show ordered pool/lane frontiers and pinned Plan revisions", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runQueueShow(command, app, root, options, args[0])
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newQueueInsertCommand(app *App, root *rootOptions) *cobra.Command {
	options := &queueOptions{position: -1, minimumConfidence: .6}
	command := &cobra.Command{Use: "insert <queue> <plan>", Short: "Score and insert a Plan, optionally with listwise advice and battles", Args: cobra.ExactArgs(2)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runQueueInsert(command, app, root, options, args[0], args[1])
	}
	flags := command.Flags()
	flags.StringVar(&options.pool, "pool", "", "select the constrained ResourcePool")
	flags.StringVar(&options.lane, "lane", "", "select exploit or explore (defaults to Plan classification)")
	flags.IntVar(&options.position, "position", -1, "override the provisional zero-based insertion position")
	flags.Float64Var(&options.score, "score", 0, "override the transparent numeric score")
	flags.BoolVar(&options.agent, "agent", false, "request global listwise advice plus order-swapped adjacent battles")
	flags.BoolVar(&options.pinned, "pin", false, "human-pin the entry, including an explicit saturated-cluster override")
	flags.StringVar(&options.config, "config", "", "agent profile TOML path")
	flags.StringVar(&options.advisorProfile, "advisor-profile", "", "override the queue_advisor role profile")
	flags.StringVar(&options.battleProfile, "battle-profile", "", "override the queue_battle role profile")
	flags.Float64Var(&options.minimumConfidence, "minimum-confidence", .6, "minimum confidence required in both battle orders")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newQueueRemoveCommand(app *App, root *rootOptions) *cobra.Command {
	options := &queueOptions{}
	command := &cobra.Command{Use: "remove <queue> <plan>", Short: "Remove a queued Plan with an exact Queue CAS", Args: cobra.ExactArgs(2)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runQueueRemove(command, app, root, options, args[0], args[1])
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runQueueCreate(command *cobra.Command, app *App, root *rootOptions, options *queueOptions) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "queue create", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "queue create", struct{}{}, false, nil, err)
	}
	if inventory.Policy == nil {
		return commandFailure(app, options.json, "queue create", struct{}{}, false, nil, errors.New("POLICY.md is required; run exp policy init"))
	}
	poolIDs, err := resolveMany(inventory, options.pools, research.KindResourcePool)
	if err != nil || len(poolIDs) == 0 {
		if err == nil {
			err = errors.New("at least one --pool is required")
		}
		return commandFailure(app, options.json, "queue create", struct{}{}, false, nil, err)
	}
	partitions := make([]research.QueuePartition, 0, 2*len(poolIDs))
	for _, pool := range poolIDs {
		partitions = append(partitions,
			research.QueuePartition{Pool: pool, Lane: research.LaneExploit, Entries: []research.QueueEntry{}},
			research.QueuePartition{Pool: pool, Lane: research.LaneExplore, Entries: []research.QueueEntry{}},
		)
	}
	now := app.clock()
	id, err := generatedID(app, research.KindQueue, now)
	if err != nil {
		return commandFailure(app, options.json, "queue create", struct{}{}, false, nil, err)
	}
	body := options.body
	if body == "" {
		body = "\n# " + options.title + "\n"
	}
	queue := &research.Queue{Common: research.Common{Schema: research.SchemaQueue, ID: id, Title: options.title, CreatedAt: now, UpdatedAt: now}, Revision: 1, Partitions: partitions}
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: "queue.create", Changes: []record.TransactionChange{{Operation: record.TransactionCreate, Document: &record.Document{Record: queue, Body: body}}}})
	if err != nil {
		return commandFailure(app, options.json, "queue create", struct{}{}, false, nil, err)
	}
	published := transactionDocument(result, research.KindQueue)
	data := struct {
		Queue canonicalRecordView `json:"queue"`
	}{Queue: canonicalView(published)}
	return commandSuccess(app, options.json, "queue create", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Created Queue %s with %d pool/lane partitions.\n", id, len(partitions)))
}

func runQueueList(command *cobra.Command, app *App, root *rootOptions, options *queueOptions) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "queue list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "queue list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	views := []canonicalRecordView{}
	var human strings.Builder
	for _, document := range inventory.OfKind(research.KindQueue) {
		views = append(views, canonicalView(document))
		queue := document.Record.(*research.Queue)
		entries := 0
		for _, partition := range queue.Partitions {
			entries += len(partition.Entries)
		}
		fmt.Fprintf(&human, "%s\trevision=%d\tentries=%d\tpaused=%t\t%s\n", queue.ID, queue.Revision, entries, queue.Paused, queue.Title)
	}
	if len(views) == 0 {
		human.WriteString("No Queues.\n")
	}
	return commandSuccess(app, options.json, "queue list", recordListData{Records: views}, false, convertRecordDiagnostics(inventory.Diagnostics), human.String())
}

func runQueueShow(command *cobra.Command, app *App, root *rootOptions, options *queueOptions, reference string) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "queue show", queueView{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "queue show", queueView{}, false, nil, err)
	}
	document, err := inventory.Resolve(reference, research.KindQueue)
	if err != nil {
		return commandFailure(app, options.json, "queue show", queueView{}, false, nil, err)
	}
	view := makeQueueView(inventory, document)
	return commandSuccess(app, options.json, "queue show", view, false, convertRecordDiagnostics(inventory.Diagnostics), renderQueueView(view))
}

func runQueueInsert(command *cobra.Command, app *App, root *rootOptions, options *queueOptions, queueReference, planReference string) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "queue insert", queueInsertData{Battles: []canonicalRecordView{}}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "queue insert", queueInsertData{Battles: []canonicalRecordView{}}, false, nil, err)
	}
	queueDocument, err := inventory.Resolve(queueReference, research.KindQueue)
	if err != nil {
		return commandFailure(app, options.json, "queue insert", queueInsertData{Battles: []canonicalRecordView{}}, false, nil, err)
	}
	planDocument, err := inventory.Resolve(planReference, research.KindPlan)
	if err != nil {
		return commandFailure(app, options.json, "queue insert", queueInsertData{Battles: []canonicalRecordView{}}, false, nil, err)
	}
	plan := planDocument.Record.(*research.Plan)
	poolReference := options.pool
	if poolReference == "" && len(plan.Resources) == 1 {
		poolReference = plan.Resources[0].Pool.String()
	}
	if poolReference == "" {
		return commandFailure(app, options.json, "queue insert", queueInsertData{Battles: []canonicalRecordView{}}, false, nil, errors.New("--pool is required when the Plan uses zero or multiple pools"))
	}
	poolDocument, err := inventory.Resolve(poolReference, research.KindResourcePool)
	if err != nil {
		return commandFailure(app, options.json, "queue insert", queueInsertData{Battles: []canonicalRecordView{}}, false, nil, err)
	}
	lane := research.ResearchLane(options.lane)
	if lane == "" && plan.Classification != nil {
		lane = plan.Classification.Lane
	}
	queueID, _ := queueDocument.ID()
	planID, _ := planDocument.ID()
	poolID, _ := poolDocument.ID()
	request := queueflow.InsertRequest{Queue: queueID, Plan: planID, Pool: poolID, Lane: lane, MinimumConfidence: options.minimumConfidence, Pinned: options.pinned}
	if command.Flags().Changed("position") {
		value := options.position
		request.Position = &value
	}
	if command.Flags().Changed("score") {
		value := options.score
		request.Score = &value
	}
	if inventory.Policy != nil {
		policy := inventory.Policy.Record.(*research.Policy)
		request.TieIncumbentFirst = policy.TiePolicy == research.QueueTieKeepIncumbent
		request.TieRequiresHuman = policy.TiePolicy == research.QueueTieHumanReview
	} else {
		request.TieIncumbentFirst = true
	}
	service := queueflow.Service{Store: store, Now: app.clock, GenerateUUID: app.GenerateUUID}
	if options.agent {
		_, config, configErr := agentConfig(app, options.config)
		if configErr != nil {
			return commandFailure(app, options.json, "queue insert", queueInsertData{Battles: []canonicalRecordView{}}, false, nil, configErr)
		}
		service.Agent = agentcli.Runner{Config: config, Invoker: app.Invoker, LookupBinary: app.BinaryLookup}
		request.UseAgent = true
		request.AdvisorProfile = options.advisorProfile
		request.BattleProfile = options.battleProfile
		request.AgentCWD = info.Repository.Root
	}
	inserted, insertErr := service.Insert(command.Context(), request)
	data := queueInsertResultData(inserted)
	if insertErr != nil && !errors.Is(insertErr, queueflow.ErrHumanReview) {
		return commandFailure(app, options.json, "queue insert", data, false, nil, insertErr)
	}
	diagnostics := refreshAfterTransaction(command, app, info, store)
	if errors.Is(insertErr, queueflow.ErrHumanReview) {
		diagnostics = append(diagnostics, Diagnostic{Severity: SeverityWarning, Code: "queue.human_review", Message: inserted.Reason})
		human := fmt.Sprintf("Queue unchanged; human review required. Advice and %d battle audit(s) were recorded.\n", len(inserted.Battles))
		return commandSuccess(app, options.json, "queue insert", data, false, diagnostics, human)
	}
	return commandSuccess(app, options.json, "queue insert", data, false, diagnostics, fmt.Sprintf("Inserted Plan %s at position %d with score %.6g.\n", planID, inserted.Position, inserted.Score))
}

func runQueueRemove(command *cobra.Command, app *App, root *rootOptions, options *queueOptions, queueReference, planReference string) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "queue remove", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "queue remove", struct{}{}, false, nil, err)
	}
	queueDocument, err := inventory.Resolve(queueReference, research.KindQueue)
	if err != nil {
		return commandFailure(app, options.json, "queue remove", struct{}{}, false, nil, err)
	}
	planDocument, err := inventory.Resolve(planReference, research.KindPlan)
	if err != nil {
		return commandFailure(app, options.json, "queue remove", struct{}{}, false, nil, err)
	}
	queueID, _ := queueDocument.ID()
	planID, _ := planDocument.ID()
	published, err := (queueflow.Service{Store: store, Now: app.clock}).Remove(command.Context(), queueflow.RemoveRequest{Queue: queueID, Plan: planID})
	if err != nil {
		return commandFailure(app, options.json, "queue remove", struct{}{}, false, nil, err)
	}
	data := struct {
		Queue canonicalRecordView `json:"queue"`
	}{Queue: canonicalView(published)}
	return commandSuccess(app, options.json, "queue remove", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Removed Plan %s from Queue %s.\n", planID, queueID))
}

func makeQueueView(inventory *record.Inventory, document *record.Document) queueView {
	queue := document.Record.(*research.Queue)
	view := queueView{Queue: canonicalView(document), Revision: queue.Revision, Paused: queue.Paused, Partitions: []queuePartitionView{}}
	partitions := append([]research.QueuePartition(nil), queue.Partitions...)
	sort.SliceStable(partitions, func(i, j int) bool {
		if partitions[i].Pool != partitions[j].Pool {
			return partitions[i].Pool.String() < partitions[j].Pool.String()
		}
		return partitions[i].Lane < partitions[j].Lane
	})
	for _, partition := range partitions {
		part := queuePartitionView{Pool: partition.Pool.String(), Lane: string(partition.Lane), Entries: []queueEntryView{}}
		for index, entry := range partition.Entries {
			title := ""
			if plan, err := inventory.ByID(entry.Plan); err == nil {
				title = plan.Record.(*research.Plan).Title
			}
			part.Entries = append(part.Entries, queueEntryView{Position: index, Plan: entry.Plan.String(), Title: title, PlanRevision: entry.PlanRevision, Score: entry.Score, Pinned: entry.Pinned})
		}
		view.Partitions = append(view.Partitions, part)
	}
	return view
}

func renderQueueView(view queueView) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%s revision %d paused=%t\n", view.Queue.Title, view.Revision, view.Paused)
	for _, partition := range view.Partitions {
		fmt.Fprintf(&output, "%s / %s:\n", partition.Pool, partition.Lane)
		if len(partition.Entries) == 0 {
			output.WriteString("  (empty)\n")
		}
		for _, entry := range partition.Entries {
			fmt.Fprintf(&output, "  %d  %.6g  %s  %s\n", entry.Position, entry.Score, entry.Plan, entry.Title)
		}
	}
	return output.String()
}

func queueInsertResultData(result queueflow.InsertResult) queueInsertData {
	data := queueInsertData{
		Position: result.Position, Score: result.Score, Applied: result.Applied, NeedsHuman: result.NeedsHuman,
		Reason: result.Reason, TransactionID: result.TransactionID, AgentProfile: result.AgentProfile,
		ReportedModel: result.ReportedModel, Battles: []canonicalRecordView{},
	}
	if result.Queue != nil {
		data.Queue = canonicalView(result.Queue)
	}
	if result.Advice != nil {
		value := canonicalView(result.Advice)
		data.Advice = &value
	}
	for _, battle := range result.Battles {
		data.Battles = append(data.Battles, canonicalView(battle))
	}
	return data
}
