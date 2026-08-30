package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

const (
	// PlanAddRequestSchema is the exact stdin request understood by plan add.
	PlanAddRequestSchema = "exp.request.plan-add/v1"
	maxPlanRequestBytes  = 1 << 20
)

type planAddPayoffRequest struct {
	Summary  string   `json:"summary"`
	Metric   string   `json:"metric"`
	Unit     string   `json:"unit"`
	Estimate *float64 `json:"estimate,omitempty"`
}

type optionalPlanBody struct {
	value   string
	present bool
}

func (body *optionalPlanBody) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("body must be a JSON string when provided")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	body.value = value
	body.present = true
	return nil
}

type planAddRequest struct {
	SchemaVersion  string               `json:"schema_version"`
	Title          string               `json:"title"`
	Body           optionalPlanBody     `json:"body,omitempty"`
	Priority       string               `json:"priority"`
	Effort         string               `json:"effort"`
	ExpectedPayoff planAddPayoffRequest `json:"expected_payoff"`
	Tags           []string             `json:"tags,omitempty"`
	Assumptions    []string             `json:"assumptions,omitempty"`
}

type planAddOptions struct {
	input          string
	json           bool
	title          string
	body           string
	priority       string
	effort         string
	payoffSummary  string
	payoffMetric   string
	payoffUnit     string
	payoffEstimate float64
	tags           []string
	assumptions    []string
}

type planListOptions struct {
	json bool
}

func newPlanCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:          "plan",
		Short:        "Work with priced research Plans",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.AddCommand(newPlanAddCommand(app, rootOptions), newPlanListCommand(app, rootOptions), newPlanRefreshCommand(app, rootOptions))
	return command
}

func newPlanAddCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &planAddOptions{}
	command := &cobra.Command{
		Use:   "add [human flags | --input -]",
		Short: "Create one validated queued Plan",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runPlanAdd(command, app, rootOptions, options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.input, "input", "", "read an exp.request.plan-add/v1 request from standard input (must be -)")
	flags.BoolVar(&options.json, "json", false, jsonFlagUsage)
	flags.StringVar(&options.title, "title", "", "set the Plan title")
	flags.StringVar(&options.body, "body", "", "set the Markdown body (defaults to a title heading)")
	flags.StringVar(&options.priority, "priority", "", "set priority: P1, P2, P3, or P?")
	flags.StringVar(&options.effort, "effort", "", "set effort: S, M, L, or XL")
	flags.StringVar(&options.payoffSummary, "payoff-summary", "", "describe the expected payoff")
	flags.StringVar(&options.payoffMetric, "payoff-metric", "", "set the expected-payoff metric slug")
	flags.StringVar(&options.payoffUnit, "payoff-unit", "", "set the expected-payoff unit")
	flags.Float64Var(&options.payoffEstimate, "payoff-estimate", 0, "set an optional finite expected-payoff estimate")
	flags.StringSliceVar(&options.tags, "tags", nil, "set lower-case Plan tags (comma-separated or repeated)")
	flags.StringSliceVar(&options.assumptions, "assumptions", nil, "set Finding assumptions by ID or display reference")
	return command
}

func newPlanListCommand(app *App, rootOptions *rootOptions) *cobra.Command {
	options := &planListOptions{}
	command := &cobra.Command{
		Use:   "list",
		Short: "List canonical Plans without reading ROADMAP.md",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runPlanList(command, app, rootOptions, options)
		},
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runPlanAdd(command *cobra.Command, app *App, rootOptions *rootOptions, options *planAddOptions) error {
	request, err := planAddRequestFromCommand(command, command.InOrStdin(), options)
	if err != nil {
		return commandFailure(app, options.json, "plan add", struct{}{}, false, nil, err)
	}
	start, err := app.startDir(rootOptions.startDir)
	if err != nil {
		return commandFailure(app, options.json, "plan add", struct{}{}, false, nil, err)
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, "plan add", struct{}{}, false, nil, err)
	}
	store, err := app.NewStore(info)
	if err != nil {
		return commandFailure(app, options.json, "plan add", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "plan add", struct{}{}, false, nil, err)
	}
	assumptions, err := resolveAssumptions(inventory, request.Assumptions)
	if err != nil {
		return commandFailure(app, options.json, "plan add", struct{}{}, false, nil, err)
	}
	body := request.Body.value
	if !request.Body.present {
		body = "\n# " + request.Title + "\n"
	}
	created, err := store.CreatePlan(command.Context(), record.PlanInput{
		Title:    request.Title,
		Body:     body,
		Priority: research.Priority(request.Priority),
		Effort:   research.Effort(request.Effort),
		ExpectedPayoff: research.ExpectedPayoff{
			Summary:  request.ExpectedPayoff.Summary,
			Metric:   request.ExpectedPayoff.Metric,
			Unit:     request.ExpectedPayoff.Unit,
			Estimate: request.ExpectedPayoff.Estimate,
		},
		Tags:        append([]string(nil), request.Tags...),
		Assumptions: assumptions,
	})
	if err != nil {
		if created != nil && publicationWasPublished(err) {
			createdID, _ := created.ID()
			data := planAddData{Projections: emptyProjectionResult()}
			views, viewErr := makePlanViews(info, append(inventory.OfKind(research.KindPlan), created))
			if viewErr == nil {
				if view, found := findPlanView(views, createdID.String()); found {
					data.Plan = view
				} else {
					data.Plan.ID = createdID.String()
					err = errors.Join(err, fmt.Errorf("published Plan is absent from its result view"))
				}
			} else {
				data.Plan.ID = createdID.String()
				err = errors.Join(err, fmt.Errorf("build published Plan result: %w", viewErr))
			}
			return commandFailure(
				app,
				options.json,
				"plan add",
				data,
				true,
				durabilityUncertainDiagnostics("canonical", created.Path),
				fmt.Errorf("Plan %s was published but durability is uncertain: %w", createdID, err),
			)
		}
		return commandFailure(app, options.json, "plan add", struct{}{}, false, nil, err)
	}

	createdID, _ := created.ID()
	freshInventory, rendered, renderErr := renderFreshProjections(command.Context(), app, info, store)
	planDocuments := append(inventory.OfKind(research.KindPlan), created)
	if freshInventory != nil {
		planDocuments = freshInventory.OfKind(research.KindPlan)
	}
	views, err := makePlanViews(info, planDocuments)
	if err != nil {
		return commandFailure(app, options.json, "plan add", struct{}{}, true, nil, fmt.Errorf("Plan %s was created but its result view failed: %w", createdID, err))
	}
	createdView, found := findPlanView(views, createdID.String())
	if !found {
		return commandFailure(app, options.json, "plan add", struct{}{}, true, nil, fmt.Errorf("created Plan %s is absent from the canonical inventory", createdID))
	}
	data := planAddData{Plan: createdView, Projections: rendered}
	if renderErr != nil {
		var diagnostics []Diagnostic
		if publicationWasPublished(renderErr) && len(rendered.Written) > 0 {
			diagnostics = durabilityUncertainDiagnostics("generated projection", rendered.Written[len(rendered.Written)-1])
		}
		return commandFailure(app, options.json, "plan add", data, true, diagnostics, fmt.Errorf("Plan %s was created but projection refresh failed: %w", createdID, renderErr))
	}
	human := fmt.Sprintf("Created %s %q at %s (revision %s)\n", createdView.Display, createdView.Title, createdView.Path, createdView.Revision)
	return commandSuccess(app, options.json, "plan add", data, false, nil, human)
}

func runPlanList(command *cobra.Command, app *App, rootOptions *rootOptions, options *planListOptions) error {
	start, err := app.startDir(rootOptions.startDir)
	if err != nil {
		return commandFailure(app, options.json, "plan list", planListData{Plans: []planView{}}, false, nil, err)
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return commandFailure(app, options.json, "plan list", planListData{Plans: []planView{}}, false, nil, err)
	}
	projectData, err := makeProjectView(info)
	if err != nil {
		return commandFailure(app, options.json, "plan list", planListData{Plans: []planView{}}, false, nil, err)
	}
	store, err := app.NewStore(info)
	if err != nil {
		return commandFailure(app, options.json, "plan list", planListData{Project: projectData, Plans: []planView{}}, false, nil, err)
	}
	documents, recordDiagnostics, err := store.ListPlans(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "plan list", planListData{Project: projectData, Plans: []planView{}}, false, nil, err)
	}
	plans, err := makePlanViews(info, documents)
	if err != nil {
		return commandFailure(app, options.json, "plan list", planListData{Project: projectData, Plans: []planView{}}, false, nil, err)
	}
	data := planListData{Project: projectData, Plans: plans}
	diagnostics := convertRecordDiagnostics(recordDiagnostics)
	human := renderPlanListHuman(plans)
	if len(recordDiagnostics) != 0 {
		inventoryErr := &record.InventoryError{Diagnostics: append([]record.Diagnostic(nil), recordDiagnostics...)}
		if options.json {
			return commandFailure(app, true, "plan list", data, true, diagnostics, inventoryErr)
		}
		if writeErr := app.WriteHuman(safeHumanOutput(human)); writeErr != nil {
			return writeErr
		}
		return inventoryErr
	}
	return commandSuccess(app, options.json, "plan list", data, false, diagnostics, human)
}

func planAddRequestFromCommand(command *cobra.Command, input io.Reader, options *planAddOptions) (planAddRequest, error) {
	if options.input != "" {
		if options.input != "-" {
			return planAddRequest{}, fmt.Errorf("--input must be - for standard input")
		}
		for _, name := range []string{
			"title", "body", "priority", "effort", "payoff-summary", "payoff-metric", "payoff-unit", "payoff-estimate", "tags", "assumptions",
		} {
			if command.Flags().Changed(name) {
				return planAddRequest{}, fmt.Errorf("--input cannot be mixed with human payload flag --%s", name)
			}
		}
		return decodePlanAddRequest(command.Context(), input)
	}
	estimate := (*float64)(nil)
	if command.Flags().Changed("payoff-estimate") {
		value := options.payoffEstimate
		estimate = &value
	}
	tags := append([]string(nil), options.tags...)
	if tags == nil {
		tags = []string{}
	}
	assumptions := append([]string(nil), options.assumptions...)
	if assumptions == nil {
		assumptions = []string{}
	}
	return planAddRequest{
		SchemaVersion: PlanAddRequestSchema,
		Title:         options.title,
		Body: optionalPlanBody{
			value: options.body, present: command.Flags().Changed("body"),
		},
		Priority: options.priority,
		Effort:   options.effort,
		ExpectedPayoff: planAddPayoffRequest{
			Summary:  options.payoffSummary,
			Metric:   options.payoffMetric,
			Unit:     options.payoffUnit,
			Estimate: estimate,
		},
		Tags:        tags,
		Assumptions: assumptions,
	}, nil
}

func decodePlanAddRequest(ctx context.Context, input io.Reader) (_ planAddRequest, err error) {
	defer func() { err = safeCLIError(err) }()
	if ctx == nil {
		return planAddRequest{}, fmt.Errorf("read plan add request: context is required")
	}
	if input == nil {
		input = strings.NewReader("")
	}
	content, err := readBoundedContext(ctx, input, maxPlanRequestBytes)
	if err != nil {
		return planAddRequest{}, fmt.Errorf("read plan add request: %w", err)
	}
	if !utf8.Valid(content) {
		return planAddRequest{}, fmt.Errorf("decode plan add request: input is not valid UTF-8")
	}
	if err := validatePlanAddJSON(content); err != nil {
		return planAddRequest{}, fmt.Errorf("decode plan add request: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var request planAddRequest
	if err := decoder.Decode(&request); err != nil {
		return planAddRequest{}, fmt.Errorf("decode plan add request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return planAddRequest{}, fmt.Errorf("decode plan add request: trailing JSON value")
		}
		return planAddRequest{}, fmt.Errorf("decode plan add request trailing data: %w", err)
	}
	if request.SchemaVersion != PlanAddRequestSchema {
		return planAddRequest{}, fmt.Errorf("plan add request schema_version must be %q", PlanAddRequestSchema)
	}
	if request.Tags == nil {
		request.Tags = []string{}
	}
	if request.Assumptions == nil {
		request.Assumptions = []string{}
	}
	return request, nil
}

type boundedReadResult struct {
	content []byte
	err     error
}

func readBoundedContext(ctx context.Context, input io.Reader, maxBytes int64) ([]byte, error) {
	completed := make(chan boundedReadResult, 1)
	go func() {
		content, err := io.ReadAll(io.LimitReader(input, maxBytes+1))
		completed <- boundedReadResult{content: content, err: err}
	}()

	select {
	case result := <-completed:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return validateBoundedRead(result, maxBytes)
	case <-ctx.Done():
		// A deadline wakes pollable files and sockets; Close wakes readers such as
		// io.Pipe. Use both when available. The result channel is buffered, so a
		// reader released after this return cannot become stuck trying to report.
		if deadline, ok := input.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = deadline.SetReadDeadline(time.Now())
		}
		if closer, ok := input.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil, ctx.Err()
	}
}

func validateBoundedRead(result boundedReadResult, maxBytes int64) ([]byte, error) {
	if result.err != nil {
		return nil, result.err
	}
	if int64(len(result.content)) > maxBytes {
		return nil, fmt.Errorf("plan add request exceeds %d bytes", maxBytes)
	}
	return result.content, nil
}

var planAddJSONFields = map[string]map[string]struct{}{
	"request": {
		"schema_version":  {},
		"title":           {},
		"body":            {},
		"priority":        {},
		"effort":          {},
		"expected_payoff": {},
		"tags":            {},
		"assumptions":     {},
	},
	"request.expected_payoff": {
		"summary":  {},
		"metric":   {},
		"unit":     {},
		"estimate": {},
	},
}

func validatePlanAddJSON(content []byte) (err error) {
	defer func() { err = safeCLIError(err) }()
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := inspectPlanAddJSONValue(decoder, "request", true); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func inspectPlanAddJSONValue(decoder *json.Decoder, path string, requireObject bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		if requireObject {
			return fmt.Errorf("%s must be a JSON object", path)
		}
		return nil
	}
	if requireObject && delimiter != json.Delim('{') {
		return fmt.Errorf("%s must be a JSON object", path)
	}
	switch delimiter {
	case '{':
		allowed, objectAllowed := planAddJSONFields[path]
		if !objectAllowed {
			return fmt.Errorf("object value is not allowed at %s", path)
		}
		seen := make(map[string]struct{})
		semantic := make(map[string]string)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate field %s.%s", path, key)
			}
			folded := strings.ToLower(key)
			if previous, duplicate := semantic[folded]; duplicate {
				return fmt.Errorf("semantic duplicate fields %s.%s and %s.%s", path, previous, path, key)
			}
			seen[key] = struct{}{}
			semantic[folded] = key
			if _, known := allowed[key]; !known {
				return fmt.Errorf("unknown field %s.%s", path, key)
			}
			childPath := path + "." + key
			if err := inspectPlanAddJSONValue(decoder, childPath, childPath == "request.expected_payoff"); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object at %s is not closed", path)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := inspectPlanAddJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index), false); err != nil {
				return err
			}
			index++
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array at %s is not closed", path)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delimiter, path)
	}
	return nil
}

func resolveAssumptions(inventory *record.Inventory, references []string) ([]research.ID, error) {
	resolved := make([]research.ID, 0, len(references))
	for _, reference := range references {
		document, err := inventory.Resolve(reference, research.KindFinding)
		if err != nil {
			return nil, fmt.Errorf("resolve assumption %q: %w", reference, err)
		}
		id, ok := document.ID()
		if !ok {
			return nil, fmt.Errorf("assumption %q has no canonical ID", reference)
		}
		resolved = append(resolved, id)
	}
	return resolved, nil
}

func findPlanView(plans []planView, id string) (planView, bool) {
	for _, plan := range plans {
		if plan.ID == id {
			return plan, true
		}
	}
	return planView{}, false
}

func renderPlanListHuman(plans []planView) string {
	if len(plans) == 0 {
		return "No Plans.\n"
	}
	var output strings.Builder
	writer := tabwriter.NewWriter(&output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "DISPLAY\tSTATE\tPRIORITY\tEFFORT\tTITLE\tID\tREVISION")
	for _, plan := range plans {
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			plan.Display, plan.State, plan.Priority, plan.Effort, singleLineHuman(plan.Title), plan.ID, plan.Revision)
	}
	_ = writer.Flush()
	return output.String()
}

func singleLineHuman(value string) string {
	return safeDiagnosticText(value)
}

func sortQueuedPlanDocuments(documents []*record.Document) []*record.Document {
	queued := make([]*record.Document, 0)
	for _, document := range documents {
		plan, ok := document.Record.(*research.Plan)
		if ok && plan.State == research.PlanQueued {
			queued = append(queued, document)
		}
	}
	priority := map[research.Priority]int{
		research.PriorityP1: 0, research.PriorityP2: 1, research.PriorityP3: 2, research.PriorityUnknown: 3,
	}
	sort.SliceStable(queued, func(left, right int) bool {
		leftPlan := queued[left].Record.(*research.Plan)
		rightPlan := queued[right].Record.(*research.Plan)
		if priority[leftPlan.Priority] != priority[rightPlan.Priority] {
			return priority[leftPlan.Priority] < priority[rightPlan.Priority]
		}
		return leftPlan.ID.String() < rightPlan.ID.String()
	})
	return queued
}
