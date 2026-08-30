package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/lifecycle"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

const (
	experimentCloseRequestSchema = "exp.request.experiment-close/v1"
	releaseCreateRequestSchema   = "exp.request.release-create/v1"
)

type scientificOptions struct {
	json           bool
	input          string
	title          string
	body           string
	experiment     string
	evaluation     string
	parents        []string
	gitCommit      string
	changeSet      []string
	tags           []string
	target         string
	evaluationSpec string
	holdoutHours   float64
	spec           string
	challenger     string
	outcome        string
	approvedBy     string
	confirm        bool
}

type closeEvidenceRequest struct {
	Run         string `json:"run"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
}

type findingEvidenceRequest struct {
	Run    string `json:"run"`
	Detail string `json:"detail,omitempty"`
}

type findingCreateRequest struct {
	Title     string                   `json:"title"`
	Body      string                   `json:"body,omitempty"`
	Statement string                   `json:"statement"`
	Scope     string                   `json:"scope"`
	Weakens   []string                 `json:"weakens,omitempty"`
	Overturns []string                 `json:"overturns,omitempty"`
	Evidence  []findingEvidenceRequest `json:"evidence"`
	Tags      []string                 `json:"tags,omitempty"`
}

type experimentCloseRequest struct {
	SchemaVersion string                 `json:"schema_version"`
	Experiment    string                 `json:"experiment"`
	Plan          string                 `json:"plan"`
	Verdict       string                 `json:"verdict"`
	Summary       string                 `json:"summary"`
	Evidence      []closeEvidenceRequest `json:"evidence"`
	Findings      []findingCreateRequest `json:"findings,omitempty"`
}

type releaseSlotRequest struct {
	Name      string `json:"name"`
	Candidate string `json:"candidate"`
}

type releaseCombinationRequest struct {
	Experiment string `json:"experiment"`
	Evaluation string `json:"evaluation"`
}

type releaseEvaluationRequest struct {
	Spec    string   `json:"spec"`
	Title   string   `json:"title"`
	Body    string   `json:"body,omitempty"`
	Outcome string   `json:"outcome"`
	Metrics []string `json:"metrics"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags,omitempty"`
}

type releaseCreateRequest struct {
	SchemaVersion string                     `json:"schema_version"`
	Title         string                     `json:"title"`
	Body          string                     `json:"body,omitempty"`
	Target        string                     `json:"target"`
	Version       string                     `json:"version"`
	State         string                     `json:"state"`
	Slots         []releaseSlotRequest       `json:"slots"`
	Combination   *releaseCombinationRequest `json:"combination,omitempty"`
	Evaluation    *releaseEvaluationRequest  `json:"evaluation,omitempty"`
	Tags          []string                   `json:"tags,omitempty"`
}

func newExperimentCloseCommand(app *App, root *rootOptions) *cobra.Command {
	options := &scientificOptions{input: "-"}
	command := &cobra.Command{Use: "close --input PATH|-", Short: "Atomically conclude an Experiment, complete its Plan, and publish Findings", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runExperimentClose(command, app, root, options) }
	command.Flags().StringVar(&options.input, "input", "-", "read an exp.request.experiment-close/v1 JSON request")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newCandidateCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "candidate", Short: "Promote supported experimental evidence into Git-addressed Candidates", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	options := &scientificOptions{}
	create := &cobra.Command{Use: "create", Short: "Create a Candidate from a supported Experiment and passing Evaluation", Args: cobra.NoArgs}
	create.RunE = func(command *cobra.Command, _ []string) error { return runCandidateCreate(command, app, root, options) }
	flags := create.Flags()
	flags.StringVar(&options.title, "title", "", "set the Candidate title")
	flags.StringVar(&options.body, "body", "", "set optional Markdown detail")
	flags.StringVar(&options.experiment, "experiment", "", "reference the closed supported Experiment")
	flags.StringVar(&options.evaluation, "evaluation", "", "reference its passing scientific Evaluation")
	flags.StringSliceVar(&options.parents, "parent", nil, "link parent Candidates")
	flags.StringVar(&options.gitCommit, "git-commit", "", "set the full exact Git object ID")
	flags.StringSliceVar(&options.changeSet, "change", nil, "add an exact changed path")
	flags.StringSliceVar(&options.tags, "tags", nil, "set free tags")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	command.AddCommand(create)
	return command
}

func newReleaseCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "release", Short: "Assemble typed Candidate slots for a downstream target", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	options := &scientificOptions{input: "-"}
	create := &cobra.Command{Use: "create --input PATH|-", Short: "Create a draft or atomically validated typed Release", Args: cobra.NoArgs}
	create.RunE = func(command *cobra.Command, _ []string) error { return runReleaseCreate(command, app, root, options) }
	create.Flags().StringVar(&options.input, "input", "-", "read an exp.request.release-create/v1 JSON request")
	create.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	command.AddCommand(create)
	return command
}

func newPromotionCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "promotion", Short: "Seal and append human-only production promotion decisions", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(newPromotionSpecCommand(app, root), newPromotionAppendCommand(app, root))
	return command
}

func newPromotionSpecCommand(app *App, root *rootOptions) *cobra.Command {
	options := &scientificOptions{}
	command := &cobra.Command{Use: "spec-create", Short: "Create a sealed human-gated PromotionSpec", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error {
		return runPromotionSpecCreate(command, app, root, options)
	}
	flags := command.Flags()
	flags.StringVar(&options.title, "title", "", "set the PromotionSpec title")
	flags.StringVar(&options.body, "body", "", "set optional Markdown detail")
	flags.StringVar(&options.target, "target", "", "set the production target slug")
	flags.StringVar(&options.evaluationSpec, "evaluation-spec", "", "reference a sealed promotion EvaluationSpec")
	flags.Float64Var(&options.holdoutHours, "holdout-budget-hours", 0, "set the finite holdout budget")
	flags.StringSliceVar(&options.tags, "tags", nil, "set free tags")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newPromotionAppendCommand(app *App, root *rootOptions) *cobra.Command {
	options := &scientificOptions{}
	command := &cobra.Command{Use: "append", Short: "Append an accepted, rejected, or rollback human decision", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runPromotionAppend(command, app, root, options) }
	flags := command.Flags()
	flags.StringVar(&options.title, "title", "", "set the Promotion event title")
	flags.StringVar(&options.body, "body", "", "set optional Markdown detail")
	flags.StringVar(&options.target, "target", "", "set the exact production target")
	flags.StringVar(&options.spec, "spec", "", "reference the PromotionSpec")
	flags.StringVar(&options.challenger, "challenger", "", "reference the validated challenger Release")
	flags.StringVar(&options.evaluation, "evaluation", "", "reference the sealed holdout Evaluation")
	flags.StringVar(&options.outcome, "outcome", "", "set accepted, rejected, or rolled_back")
	flags.StringVar(&options.approvedBy, "approved-by", "", "identify the human approver")
	flags.StringSliceVar(&options.tags, "tags", nil, "set free tags")
	flags.BoolVar(&options.confirm, "confirm", false, "confirm this exact human production decision")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newChampionCommand(app *App, root *rootOptions) *cobra.Command {
	options := &scientificOptions{}
	command := &cobra.Command{Use: "champion", Short: "Show downstream champions derived from Promotion chains", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runChampion(command, app, root, options) }
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	manifestOptions := &scientificOptions{}
	manifest := &cobra.Command{Use: "manifest", Short: "Render a deterministic downstream manifest from current champions", Args: cobra.NoArgs}
	manifest.RunE = func(command *cobra.Command, _ []string) error {
		return runChampionManifest(command, app, root, manifestOptions)
	}
	manifest.Flags().StringVar(&manifestOptions.target, "target", "", "select one production target")
	manifest.Flags().BoolVar(&manifestOptions.json, "json", false, jsonFlagUsage)
	command.AddCommand(manifest)
	return command
}

func runExperimentClose(command *cobra.Command, app *App, root *rootOptions, options *scientificOptions) error {
	content, err := readBoundedInput(command.InOrStdin(), options.input, 4<<20)
	if err != nil {
		return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, err)
	}
	var request experimentCloseRequest
	if err := decodeStrictJSON(content, &request); err != nil || request.SchemaVersion != experimentCloseRequestSchema {
		if err == nil {
			err = fmt.Errorf("schema_version must be %q", experimentCloseRequestSchema)
		}
		return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, err)
	}
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, err)
	}
	experiment, err := currentRevisionRef(inventory, request.Experiment, research.KindExperiment)
	if err != nil {
		return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, err)
	}
	plan, err := currentRevisionRef(inventory, request.Plan, research.KindPlan)
	if err != nil {
		return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, err)
	}
	evidence := make([]lifecycle.ConclusionEvidenceInput, 0, len(request.Evidence))
	for _, item := range request.Evidence {
		run, resolveErr := currentRevisionRef(inventory, item.Run, research.KindRun)
		if resolveErr != nil {
			return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, resolveErr)
		}
		evidence = append(evidence, lifecycle.ConclusionEvidenceInput{Run: run, Disposition: research.EvidenceDisposition(item.Disposition), Reason: item.Reason})
	}
	findings := make([]lifecycle.FindingInput, 0, len(request.Findings))
	for _, input := range request.Findings {
		finding := lifecycle.FindingInput{Title: input.Title, Body: input.Body, Statement: input.Statement, Scope: input.Scope, Tags: input.Tags}
		for _, ref := range input.Weakens {
			value, resolveErr := currentRevisionRef(inventory, ref, research.KindFinding)
			if resolveErr != nil {
				return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, resolveErr)
			}
			finding.Weakens = append(finding.Weakens, value)
		}
		for _, ref := range input.Overturns {
			value, resolveErr := currentRevisionRef(inventory, ref, research.KindFinding)
			if resolveErr != nil {
				return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, resolveErr)
			}
			finding.Overturns = append(finding.Overturns, value)
		}
		for _, item := range input.Evidence {
			run, resolveErr := currentRevisionRef(inventory, item.Run, research.KindRun)
			if resolveErr != nil {
				return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, resolveErr)
			}
			finding.Evidence = append(finding.Evidence, lifecycle.FindingEvidenceInput{Run: run, Detail: item.Detail})
		}
		findings = append(findings, finding)
	}
	result, err := lifecycle.New(store, lifecycle.WithClock(app.clock), lifecycle.WithUUIDGenerator(app.GenerateUUID)).CloseExperiment(command.Context(), lifecycle.CloseExperimentRequest{
		Experiment: experiment, Plan: plan, Verdict: research.Verdict(request.Verdict), Summary: request.Summary, Evidence: evidence, Findings: findings,
	})
	if err != nil {
		return commandFailure(app, options.json, "experiment close", struct{}{}, false, nil, err)
	}
	data := struct {
		Transaction string                `json:"transaction_id"`
		Experiment  canonicalRecordView   `json:"experiment"`
		Plan        canonicalRecordView   `json:"plan"`
		Findings    []canonicalRecordView `json:"findings"`
	}{Transaction: result.TransactionID, Experiment: canonicalView(result.Experiment), Plan: canonicalView(result.Plan), Findings: []canonicalRecordView{}}
	for _, document := range result.Findings {
		data.Findings = append(data.Findings, canonicalView(document))
	}
	return commandSuccess(app, options.json, "experiment close", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Closed Experiment %s and published %d Finding(s).\n", data.Experiment.ID, len(data.Findings)))
}

func runCandidateCreate(command *cobra.Command, app *App, root *rootOptions, options *scientificOptions) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "candidate create", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "candidate create", struct{}{}, false, nil, err)
	}
	experiment, err := currentRevisionRef(inventory, options.experiment, research.KindExperiment)
	if err != nil {
		return commandFailure(app, options.json, "candidate create", struct{}{}, false, nil, err)
	}
	evaluation, err := currentRevisionRef(inventory, options.evaluation, research.KindEvaluation)
	if err != nil {
		return commandFailure(app, options.json, "candidate create", struct{}{}, false, nil, err)
	}
	evaluationDocument, _ := inventory.ByID(evaluation.ID)
	evaluationValue := evaluationDocument.Record.(*research.Evaluation)
	evaluationSpecDocument, err := inventory.ByID(evaluationValue.Spec)
	if err != nil {
		return commandFailure(app, options.json, "candidate create", struct{}{}, false, nil, err)
	}
	parents := make([]lifecycle.RevisionRef, 0, len(options.parents))
	for _, reference := range options.parents {
		value, resolveErr := currentRevisionRef(inventory, reference, research.KindCandidate)
		if resolveErr != nil {
			return commandFailure(app, options.json, "candidate create", struct{}{}, false, nil, resolveErr)
		}
		parents = append(parents, value)
	}
	result, err := lifecycle.New(store, lifecycle.WithClock(app.clock), lifecycle.WithUUIDGenerator(app.GenerateUUID)).CreateCandidate(command.Context(), lifecycle.CreateCandidateRequest{
		Title: options.title, Body: options.body, Experiment: experiment, Evaluation: evaluation,
		EvaluationSpecExpectedRevision: evaluationSpecDocument.Revision, Parents: parents,
		GitCommit: options.gitCommit, ChangeSet: options.changeSet, Tags: options.tags,
	})
	if err != nil {
		return commandFailure(app, options.json, "candidate create", struct{}{}, false, nil, err)
	}
	data := struct {
		Transaction string              `json:"transaction_id"`
		Candidate   canonicalRecordView `json:"candidate"`
	}{Transaction: result.TransactionID, Candidate: canonicalView(result.Candidate)}
	return commandSuccess(app, options.json, "candidate create", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Created Candidate %s.\n", data.Candidate.ID))
}

func runReleaseCreate(command *cobra.Command, app *App, root *rootOptions, options *scientificOptions) error {
	content, err := readBoundedInput(command.InOrStdin(), options.input, 4<<20)
	if err != nil {
		return commandFailure(app, options.json, "release create", struct{}{}, false, nil, err)
	}
	var request releaseCreateRequest
	if err := decodeStrictJSON(content, &request); err != nil || request.SchemaVersion != releaseCreateRequestSchema {
		if err == nil {
			err = fmt.Errorf("schema_version must be %q", releaseCreateRequestSchema)
		}
		return commandFailure(app, options.json, "release create", struct{}{}, false, nil, err)
	}
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "release create", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "release create", struct{}{}, false, nil, err)
	}
	slots := make([]lifecycle.ReleaseSlotInput, 0, len(request.Slots))
	for _, slot := range request.Slots {
		candidate, resolveErr := currentRevisionRef(inventory, slot.Candidate, research.KindCandidate)
		if resolveErr != nil {
			return commandFailure(app, options.json, "release create", struct{}{}, false, nil, resolveErr)
		}
		slots = append(slots, lifecycle.ReleaseSlotInput{Name: slot.Name, Candidate: candidate})
	}
	var combination *lifecycle.CombinationEvidence
	if request.Combination != nil {
		experiment, resolveErr := currentRevisionRef(inventory, request.Combination.Experiment, research.KindExperiment)
		if resolveErr != nil {
			return commandFailure(app, options.json, "release create", struct{}{}, false, nil, resolveErr)
		}
		evaluation, resolveErr := currentRevisionRef(inventory, request.Combination.Evaluation, research.KindEvaluation)
		if resolveErr != nil {
			return commandFailure(app, options.json, "release create", struct{}{}, false, nil, resolveErr)
		}
		evaluationDocument, _ := inventory.ByID(evaluation.ID)
		specDocument, resolveErr := inventory.ByID(evaluationDocument.Record.(*research.Evaluation).Spec)
		if resolveErr != nil {
			return commandFailure(app, options.json, "release create", struct{}{}, false, nil, resolveErr)
		}
		combination = &lifecycle.CombinationEvidence{Experiment: experiment, Evaluation: evaluation, EvaluationSpecExpectedRevision: specDocument.Revision}
	}
	var evaluation *lifecycle.ReleaseEvaluationInput
	if request.Evaluation != nil {
		spec, resolveErr := currentRevisionRef(inventory, request.Evaluation.Spec, research.KindEvaluationSpec)
		if resolveErr != nil {
			return commandFailure(app, options.json, "release create", struct{}{}, false, nil, resolveErr)
		}
		metrics, parseErr := parseMetricValues(request.Evaluation.Metrics)
		if parseErr != nil {
			return commandFailure(app, options.json, "release create", struct{}{}, false, nil, parseErr)
		}
		evaluation = &lifecycle.ReleaseEvaluationInput{Spec: spec, Data: lifecycle.EvaluationData{Title: request.Evaluation.Title, Body: request.Evaluation.Body, Outcome: research.EvaluationOutcome(request.Evaluation.Outcome), Metrics: metrics, Summary: request.Evaluation.Summary, Tags: request.Evaluation.Tags}}
	}
	result, err := lifecycle.New(store, lifecycle.WithClock(app.clock), lifecycle.WithUUIDGenerator(app.GenerateUUID)).CreateRelease(command.Context(), lifecycle.CreateReleaseRequest{
		Title: request.Title, Body: request.Body, Target: request.Target, Version: request.Version,
		State: research.ReleaseState(request.State), Slots: slots, Combination: combination, Evaluation: evaluation, Tags: request.Tags,
	})
	if err != nil {
		return commandFailure(app, options.json, "release create", struct{}{}, false, nil, err)
	}
	data := struct {
		Transaction string               `json:"transaction_id"`
		Release     canonicalRecordView  `json:"release"`
		Evaluation  *canonicalRecordView `json:"evaluation,omitempty"`
	}{Transaction: result.TransactionID, Release: canonicalView(result.Release)}
	if result.Evaluation != nil {
		view := canonicalView(result.Evaluation)
		data.Evaluation = &view
	}
	return commandSuccess(app, options.json, "release create", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Created %s Release %s.\n", request.State, data.Release.ID))
}

func runPromotionSpecCreate(command *cobra.Command, app *App, root *rootOptions, options *scientificOptions) error {
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "promotion spec-create", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "promotion spec-create", struct{}{}, false, nil, err)
	}
	spec, err := currentRevisionRef(inventory, options.evaluationSpec, research.KindEvaluationSpec)
	if err != nil {
		return commandFailure(app, options.json, "promotion spec-create", struct{}{}, false, nil, err)
	}
	result, err := lifecycle.New(store, lifecycle.WithClock(app.clock), lifecycle.WithUUIDGenerator(app.GenerateUUID)).CreatePromotionSpec(command.Context(), lifecycle.CreatePromotionSpecRequest{
		Title: options.title, Body: options.body, Target: options.target, EvaluationSpec: spec, HoldoutBudgetHours: options.holdoutHours, Tags: options.tags,
	})
	if err != nil {
		return commandFailure(app, options.json, "promotion spec-create", struct{}{}, false, nil, err)
	}
	data := struct {
		Transaction string              `json:"transaction_id"`
		Spec        canonicalRecordView `json:"spec"`
	}{Transaction: result.TransactionID, Spec: canonicalView(result.Spec)}
	return commandSuccess(app, options.json, "promotion spec-create", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Created sealed PromotionSpec %s.\n", data.Spec.ID))
}

func runPromotionAppend(command *cobra.Command, app *App, root *rootOptions, options *scientificOptions) error {
	if !options.confirm {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, errors.New("production Promotion requires --confirm and a human --approved-by"))
	}
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, err)
	}
	spec, err := currentRevisionRef(inventory, options.spec, research.KindPromotionSpec)
	if err != nil {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, err)
	}
	challenger, err := currentRevisionRef(inventory, options.challenger, research.KindRelease)
	if err != nil {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, err)
	}
	evaluation, err := currentRevisionRef(inventory, options.evaluation, research.KindEvaluation)
	if err != nil {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, err)
	}
	specDocument, _ := inventory.ByID(spec.ID)
	evaluationSpecDocument, err := inventory.ByID(specDocument.Record.(*research.PromotionSpec).EvaluationSpec)
	if err != nil {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, err)
	}
	tip, champion, tipRevision, incumbentRevision, err := promotionExpectations(inventory, options.target)
	if err != nil {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, err)
	}
	result, err := lifecycle.New(store, lifecycle.WithClock(app.clock), lifecycle.WithUUIDGenerator(app.GenerateUUID)).AppendPromotion(command.Context(), lifecycle.AppendPromotionRequest{
		Title: options.title, Body: options.body, Target: options.target, Spec: spec, Challenger: challenger, Evaluation: evaluation,
		EvaluationSpecExpectedRevision: evaluationSpecDocument.Revision, Outcome: research.PromotionOutcome(options.outcome), ApprovedBy: options.approvedBy,
		ExpectedPrevious: tip, PreviousExpectedRevision: tipRevision, ExpectedChampion: champion, IncumbentExpectedRevision: incumbentRevision, Tags: options.tags,
	})
	if err != nil {
		return commandFailure(app, options.json, "promotion append", struct{}{}, false, nil, err)
	}
	data := struct {
		Transaction string              `json:"transaction_id"`
		Promotion   canonicalRecordView `json:"promotion"`
		Champion    *record.Champion    `json:"champion,omitempty"`
	}{Transaction: result.TransactionID, Promotion: canonicalView(result.Promotion), Champion: result.Champion}
	return commandSuccess(app, options.json, "promotion append", data, false, refreshAfterTransaction(command, app, info, store), fmt.Sprintf("Recorded human Promotion %s; outcome=%s.\n", data.Promotion.ID, options.outcome))
}

func runChampion(command *cobra.Command, app *App, root *rootOptions, options *scientificOptions) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "champion", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "champion", struct{}{}, false, nil, err)
	}
	champions, err := inventory.CurrentChampions()
	if err != nil {
		return commandFailure(app, options.json, "champion", struct{}{}, false, nil, err)
	}
	if champions == nil {
		champions = []record.Champion{}
	}
	var human strings.Builder
	for _, champion := range champions {
		fmt.Fprintf(&human, "%s\t%s\t%s\n", champion.Target, champion.Release, champion.Promotion)
	}
	if len(champions) == 0 {
		human.WriteString("No promoted champions.\n")
	}
	data := struct {
		Champions []record.Champion `json:"champions"`
	}{Champions: champions}
	return commandSuccess(app, options.json, "champion", data, false, convertRecordDiagnostics(inventory.Diagnostics), human.String())
}

type championManifestSlot struct {
	Name       string   `json:"name"`
	Candidate  string   `json:"candidate"`
	Experiment string   `json:"experiment"`
	Evaluation string   `json:"evaluation"`
	GitCommit  string   `json:"git_commit"`
	ChangeSet  []string `json:"change_set"`
}

type championManifestEntry struct {
	Target    string                 `json:"target"`
	Release   string                 `json:"release"`
	Version   string                 `json:"version"`
	Promotion string                 `json:"promotion"`
	Slots     []championManifestSlot `json:"slots"`
}

type championManifest struct {
	SchemaVersion string                  `json:"schema_version"`
	Champions     []championManifestEntry `json:"champions"`
}

func runChampionManifest(command *cobra.Command, app *App, root *rootOptions, options *scientificOptions) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "champion manifest", championManifest{SchemaVersion: "exp.champion-manifest/v1", Champions: []championManifestEntry{}}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "champion manifest", championManifest{SchemaVersion: "exp.champion-manifest/v1", Champions: []championManifestEntry{}}, false, nil, err)
	}
	champions, err := inventory.CurrentChampions()
	if err != nil {
		return commandFailure(app, options.json, "champion manifest", championManifest{SchemaVersion: "exp.champion-manifest/v1", Champions: []championManifestEntry{}}, false, nil, err)
	}
	manifest := championManifest{SchemaVersion: "exp.champion-manifest/v1", Champions: []championManifestEntry{}}
	for _, champion := range champions {
		if options.target != "" && champion.Target != options.target {
			continue
		}
		releaseDocument, resolveErr := inventory.ByID(champion.Release)
		if resolveErr != nil {
			return commandFailure(app, options.json, "champion manifest", manifest, false, nil, resolveErr)
		}
		release := releaseDocument.Record.(*research.Release)
		entry := championManifestEntry{Target: champion.Target, Release: release.ID.String(), Version: release.Version, Promotion: champion.Promotion.String(), Slots: []championManifestSlot{}}
		for _, slot := range release.Slots {
			candidateDocument, candidateErr := inventory.ByID(slot.Candidate)
			if candidateErr != nil {
				return commandFailure(app, options.json, "champion manifest", manifest, false, nil, candidateErr)
			}
			candidate := candidateDocument.Record.(*research.Candidate)
			entry.Slots = append(entry.Slots, championManifestSlot{Name: slot.Name, Candidate: candidate.ID.String(), Experiment: candidate.Experiment.String(), Evaluation: candidate.Evaluation.String(), GitCommit: candidate.GitCommit, ChangeSet: append([]string(nil), candidate.ChangeSet...)})
		}
		manifest.Champions = append(manifest.Champions, entry)
	}
	if options.target != "" && len(manifest.Champions) == 0 {
		return commandFailure(app, options.json, "champion manifest", manifest, false, nil, fmt.Errorf("target %q has no current champion", options.target))
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return commandFailure(app, options.json, "champion manifest", manifest, false, nil, err)
	}
	return commandSuccess(app, options.json, "champion manifest", manifest, false, convertRecordDiagnostics(inventory.Diagnostics), string(encoded)+"\n")
}

func currentRevisionRef(inventory *record.Inventory, reference string, kind research.Kind) (lifecycle.RevisionRef, error) {
	document, err := inventory.Resolve(reference, kind)
	if err != nil {
		return lifecycle.RevisionRef{}, err
	}
	id, ok := document.ID()
	if !ok {
		return lifecycle.RevisionRef{}, errors.New("reference has no typed ID")
	}
	return lifecycle.RevisionRef{ID: id, Revision: document.Revision}, nil
}

func promotionExpectations(inventory *record.Inventory, target string) (tip, champion research.ID, tipRevision, incumbentRevision string, err error) {
	documents := map[research.ID]*record.Document{}
	followers := map[research.ID]*record.Document{}
	var root *record.Document
	for _, document := range inventory.OfKind(research.KindPromotion) {
		promotion := document.Record.(*research.Promotion)
		if promotion.Target != target {
			continue
		}
		documents[promotion.ID] = document
		if promotion.Previous.IsZero() {
			if root != nil {
				return tip, champion, "", "", errors.New("promotion chain has multiple roots")
			}
			root = document
		} else {
			if followers[promotion.Previous] != nil {
				return tip, champion, "", "", errors.New("promotion chain branches")
			}
			followers[promotion.Previous] = document
		}
	}
	current := root
	visited := map[research.ID]struct{}{}
	for current != nil {
		promotion := current.Record.(*research.Promotion)
		if _, found := visited[promotion.ID]; found {
			return tip, champion, "", "", errors.New("promotion chain is cyclic")
		}
		visited[promotion.ID] = struct{}{}
		tip, tipRevision = promotion.ID, current.Revision
		if promotion.Outcome == research.PromotionAccepted || promotion.Outcome == research.PromotionRolledBack {
			champion = promotion.Challenger
		}
		current = followers[promotion.ID]
	}
	if !champion.IsZero() {
		document, found := documents[champion]
		if !found {
			document, err = inventory.ByID(champion)
			if err != nil {
				return tip, champion, "", "", err
			}
		}
		incumbentRevision = document.Revision
	}
	return tip, champion, tipRevision, incumbentRevision, nil
}

func decodeStrictJSON(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains a trailing value")
		}
		return err
	}
	return nil
}
