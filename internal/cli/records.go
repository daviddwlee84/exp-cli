package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/spf13/cobra"
)

const recordTransactionRequestSchema = "exp.request.record-transaction/v1"

type recordOptions struct {
	json      bool
	kind      string
	input     string
	operation string
	raw       bool
}

type canonicalRecordView struct {
	Kind     string `json:"kind"`
	ID       string `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

type recordListData struct {
	Records []canonicalRecordView `json:"records"`
}

type recordTransactionChangeRequest struct {
	Operation        string `json:"operation"`
	Document         string `json:"document,omitempty"`
	ID               string `json:"id,omitempty"`
	Path             string `json:"path,omitempty"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
}

type recordTransactionRequest struct {
	SchemaVersion string                           `json:"schema_version"`
	Operation     string                           `json:"operation"`
	Changes       []recordTransactionChangeRequest `json:"changes"`
}

type recordTransactionData struct {
	TransactionID string                `json:"transaction_id"`
	Records       []canonicalRecordView `json:"records"`
}

func newRecordCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "record", Short: "Inspect or atomically apply canonical records", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return command.Help() }
	command.AddCommand(
		newRecordListCommand(app, root),
		newRecordShowCommand(app, root),
		newRecordTransactionCommand(app, root),
		newRecordRecoverCommand(app, root),
	)
	return command
}

func newRecordListCommand(app *App, root *rootOptions) *cobra.Command {
	options := &recordOptions{}
	command := &cobra.Command{Use: "list", Short: "List canonical records from Git-backed authority", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runRecordList(command, app, root, options) }
	command.Flags().StringVar(&options.kind, "kind", "", "filter by canonical kind")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newRecordShowCommand(app *App, root *rootOptions) *cobra.Command {
	options := &recordOptions{}
	command := &cobra.Command{Use: "show <id|display-code|POLICY.md>", Short: "Show one canonical record", Args: cobra.ExactArgs(1)}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runRecordShow(command, app, root, options, args[0])
	}
	command.Flags().BoolVar(&options.raw, "raw", false, "write the normalized canonical Markdown envelope")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newRecordTransactionCommand(app *App, root *rootOptions) *cobra.Command {
	options := &recordOptions{}
	command := &cobra.Command{Use: "transaction --input PATH|-", Short: "Apply a versioned multi-record prepared transaction", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error {
		return runRecordTransaction(command, app, root, options)
	}
	command.Flags().StringVar(&options.input, "input", "-", "read an exp.request.record-transaction/v1 JSON request")
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func newRecordRecoverCommand(app *App, root *rootOptions) *cobra.Command {
	options := &recordOptions{}
	command := &cobra.Command{Use: "recover", Short: "Roll durable prepared canonical transactions forward", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error { return runRecordRecover(command, app, root, options) }
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	return command
}

func runRecordList(command *cobra.Command, app *App, root *rootOptions, options *recordOptions) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "record list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "record list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	filter := research.KindUnknown
	if options.kind != "" {
		filter, err = parseKind(options.kind)
		if err != nil {
			return commandFailure(app, options.json, "record list", recordListData{Records: []canonicalRecordView{}}, false, nil, err)
		}
	}
	views := make([]canonicalRecordView, 0, len(inventory.Documents))
	for _, document := range inventory.Documents {
		if filter != research.KindUnknown && document.Kind() != filter {
			continue
		}
		views = append(views, canonicalView(document))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Kind != views[j].Kind {
			return views[i].Kind < views[j].Kind
		}
		return views[i].Path < views[j].Path
	})
	data := recordListData{Records: views}
	var human strings.Builder
	for _, view := range views {
		fmt.Fprintf(&human, "%s\t%s\t%s\t%s\n", view.Kind, view.ID, view.Title, view.Path)
	}
	if len(views) == 0 {
		human.WriteString("No matching canonical records.\n")
	}
	return commandSuccess(app, options.json, "record list", data, false, convertRecordDiagnostics(inventory.Diagnostics), human.String())
}

func runRecordShow(command *cobra.Command, app *App, root *rootOptions, options *recordOptions, reference string) error {
	if options.raw && options.json {
		return commandFailure(app, true, "record show", struct{}{}, false, nil, errors.New("--raw and --json are mutually exclusive"))
	}
	_, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "record show", struct{}{}, false, nil, err)
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil {
		return commandFailure(app, options.json, "record show", struct{}{}, false, nil, err)
	}
	document, err := resolveCanonicalDocument(inventory, reference)
	if err != nil {
		return commandFailure(app, options.json, "record show", struct{}{}, false, nil, err)
	}
	encoded, err := record.Encode(document)
	if err != nil {
		return commandFailure(app, options.json, "record show", struct{}{}, false, nil, err)
	}
	if options.raw {
		return app.WriteHuman(string(encoded))
	}
	data := struct {
		Record   canonicalRecordView `json:"record"`
		Document string              `json:"document"`
	}{Record: canonicalView(document), Document: string(encoded)}
	return commandSuccess(app, options.json, "record show", data, false, nil, string(encoded))
}

func runRecordTransaction(command *cobra.Command, app *App, root *rootOptions, options *recordOptions) error {
	content, err := readBoundedInput(command.InOrStdin(), options.input, 8<<20)
	if err != nil {
		return commandFailure(app, options.json, "record transaction", recordTransactionData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	request, err := decodeRecordTransactionRequest(content)
	if err != nil {
		return commandFailure(app, options.json, "record transaction", recordTransactionData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	info, store, err := openTransactionalStore(command, app, root)
	if err != nil {
		return commandFailure(app, options.json, "record transaction", recordTransactionData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	changes := make([]record.TransactionChange, 0, len(request.Changes))
	for index, change := range request.Changes {
		converted, err := convertTransactionChange(change)
		if err != nil {
			return commandFailure(app, options.json, "record transaction", recordTransactionData{Records: []canonicalRecordView{}}, false, nil, fmt.Errorf("change %d: %w", index, err))
		}
		changes = append(changes, converted)
	}
	result, err := store.Transact(command.Context(), record.TransactionRequest{Operation: request.Operation, Changes: changes})
	if err != nil {
		return commandFailure(app, options.json, "record transaction", recordTransactionData{Records: []canonicalRecordView{}}, false, nil, err)
	}
	data := recordTransactionData{TransactionID: result.TransactionID, Records: []canonicalRecordView{}}
	for _, document := range result.Documents {
		data.Records = append(data.Records, canonicalView(document))
	}
	human := fmt.Sprintf("Applied canonical transaction %s (%d published record(s)).\n", data.TransactionID, len(data.Records))
	return commandSuccess(app, options.json, "record transaction", data, false, refreshAfterTransaction(command, app, info, store), human)
}

func runRecordRecover(command *cobra.Command, app *App, root *rootOptions, options *recordOptions) error {
	_, store, err := openTransactionalStore(command, app, root)
	if err == nil {
		err = store.Recover(command.Context())
	}
	if err != nil {
		return commandFailure(app, options.json, "record recover", struct{}{}, false, nil, err)
	}
	return commandSuccess(app, options.json, "record recover", struct{}{}, false, nil, "Prepared canonical transactions are fully recovered.\n")
}

func openTransactionalStore(command *cobra.Command, app *App, root *rootOptions) (*project.Info, TransactionalRecordStore, error) {
	start, err := app.startDir(root.startDir)
	if err != nil {
		return nil, nil, err
	}
	info, err := app.DiscoverProject(command.Context(), start)
	if err != nil {
		return nil, nil, err
	}
	store, err := app.NewTransactionalStore(info)
	return info, store, err
}

func decodeRecordTransactionRequest(content []byte) (recordTransactionRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var request recordTransactionRequest
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("decode transaction request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return request, errors.New("transaction request contains a trailing JSON value")
		}
		return request, err
	}
	if request.SchemaVersion != recordTransactionRequestSchema {
		return request, fmt.Errorf("schema_version must be %q", recordTransactionRequestSchema)
	}
	if request.Operation == "" || len(request.Changes) == 0 {
		return request, errors.New("transaction operation and changes are required")
	}
	return request, nil
}

func convertTransactionChange(input recordTransactionChangeRequest) (record.TransactionChange, error) {
	change := record.TransactionChange{ExpectedRevision: input.ExpectedRevision, Path: input.Path}
	switch input.Operation {
	case string(record.TransactionCreate), string(record.TransactionReplace):
		if input.Document == "" || input.ID != "" || input.Path != "" {
			return change, errors.New("create/replace requires document and forbids separate id/path")
		}
		document, err := record.Decode([]byte(input.Document))
		if err != nil {
			return change, err
		}
		if !genericMutableKind(document.Kind()) {
			return change, fmt.Errorf("public record transactions cannot %s %s; use its lifecycle command", input.Operation, document.Kind())
		}
		change.Document = document
		if input.Operation == string(record.TransactionCreate) {
			change.Operation = record.TransactionCreate
		} else {
			change.Operation = record.TransactionReplace
		}
	case string(record.TransactionDelete):
		change.Operation = record.TransactionDelete
		if input.Document != "" {
			return change, errors.New("delete forbids document")
		}
		if input.Path == record.PolicyFile && input.ID == "" {
			return change, errors.New("public record transactions cannot delete POLICY.md")
		}
		if input.Path != "" || input.ID == "" {
			return change, errors.New("delete requires one typed id or path POLICY.md")
		}
		id, err := research.ParseID(input.ID)
		if err != nil {
			return change, err
		}
		change.ID = id
		if !genericMutableKind(id.Kind()) {
			return change, fmt.Errorf("public record transactions cannot delete %s; use its lifecycle command", id.Kind())
		}
	default:
		return change, fmt.Errorf("unsupported operation %q", input.Operation)
	}
	return change, nil
}

func genericMutableKind(kind research.Kind) bool {
	return kind == research.KindIdea || kind == research.KindResourcePool
}

func resolveCanonicalDocument(inventory *record.Inventory, reference string) (*record.Document, error) {
	switch reference {
	case record.ProjectFile:
		if inventory.Project == nil {
			return nil, errors.New("PROJECT.md is absent")
		}
		return inventory.Project, nil
	case record.PolicyFile:
		if inventory.Policy == nil {
			return nil, errors.New("POLICY.md is absent")
		}
		return inventory.Policy, nil
	default:
		return inventory.Resolve(reference, research.KindUnknown)
	}
}

func canonicalView(document *record.Document) canonicalRecordView {
	view := canonicalRecordView{Kind: document.Kind().String(), Path: document.Path, Revision: document.Revision}
	if id, ok := document.ID(); ok {
		view.ID = id.String()
	}
	if common := document.Record.GetCommon(); common != nil {
		view.Title = common.Title
	} else if project, ok := document.Record.(*research.Project); ok {
		view.Title = project.Name
	}
	return view
}

func parseKind(value string) (research.Kind, error) {
	normalized := research.Kind(strings.TrimSpace(value))
	for _, kind := range research.RecordKinds {
		if kind == normalized {
			return kind, nil
		}
	}
	return research.KindUnknown, fmt.Errorf("unknown canonical kind %q", value)
}
