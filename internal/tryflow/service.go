// Package tryflow implements the canonical Try lifecycle and the recoverable
// direct runtime that executes Try Attempts only in managed Source worktrees.
package tryflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

var (
	ErrPrecondition     = errors.New("Try lifecycle precondition failed")
	ErrRevisionRequired = errors.New("canonical expected revision is required")
	ErrCollision        = errors.New("unable to allocate a unique Try lifecycle record ID")
)

// RevisionRef pins one canonical prerequisite to exact normalized bytes.
type RevisionRef struct {
	ID       research.ID
	Revision string
}

// Store is the prepared-transaction boundary used by Try lifecycle operations.
type Store interface {
	Inventory(context.Context) (*record.Inventory, error)
	Transact(context.Context, record.TransactionRequest) (*record.TransactionResult, error)
}

// TransactionError preserves a durably prepared Try lifecycle transaction when
// publication or commit marking returns an error after preparation.
type TransactionError struct {
	Result *record.TransactionResult
	Err    error
}

func (failure *TransactionError) Error() string {
	if failure == nil || failure.Err == nil {
		return "canonical Try transaction failed"
	}
	return failure.Err.Error()
}

func (failure *TransactionError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

// TransactionResultFromError returns durable lifecycle recovery identity carried
// by err. Callers must treat the returned result as immutable.
func TransactionResultFromError(err error) (*record.TransactionResult, bool) {
	var failure *TransactionError
	if !errors.As(err, &failure) || failure.Result == nil {
		return nil, false
	}
	return failure.Result, true
}

// Service owns Try record transitions for one canonical store.
type Service struct {
	store          Store
	clock          func() time.Time
	generate       research.UUIDGenerator
	collisionLimit int
}

type Option func(*Service)

func WithClock(clock func() time.Time) Option {
	return func(service *Service) { service.clock = clock }
}

func WithUUIDGenerator(generator research.UUIDGenerator) Option {
	return func(service *Service) { service.generate = generator }
}

func WithCollisionLimit(limit int) Option {
	return func(service *Service) { service.collisionLimit = limit }
}

func New(store Store, options ...Option) *Service {
	service := &Service{
		store: store, clock: time.Now, generate: research.DefaultUUIDGenerator,
		collisionLimit: 128,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	if service.clock == nil {
		service.clock = time.Now
	}
	if service.generate == nil {
		service.generate = research.DefaultUUIDGenerator
	}
	if service.collisionLimit <= 0 {
		service.collisionLimit = 128
	}
	return service
}

type CreateRequest struct {
	Title      string
	Body       string
	Goal       string
	Sources    []RevisionRef
	Tags       []string
	Extensions research.Extensions
}

type CreateResult struct {
	TransactionID string
	Try           *record.Document
}

// Create publishes an open Try with an immutable declared Source set.
func (service *Service) Create(ctx context.Context, request CreateRequest) (*CreateResult, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Title) == "" || strings.TrimSpace(request.Goal) == "" || len(request.Sources) == 0 {
		return nil, fmt.Errorf("title, goal, and at least one Source are required: %w", ErrPrecondition)
	}
	changes := newChanges()
	sourceIDs := make([]research.ID, 0, len(request.Sources))
	for _, reference := range request.Sources {
		document, resolveErr := resolve(inventory, reference, research.KindSource)
		if resolveErr != nil {
			return nil, resolveErr
		}
		source := document.Record.(*research.Source)
		if source.State != research.SourceActive {
			return nil, fmt.Errorf("Source %s is retired: %w", source.ID, ErrPrecondition)
		}
		if guardErr := changes.guard(document, reference.Revision); guardErr != nil {
			return nil, guardErr
		}
		sourceIDs = append(sourceIDs, source.ID)
	}
	sort.Slice(sourceIDs, func(i, j int) bool { return sourceIDs[i].String() < sourceIDs[j].String() })
	for index := 1; index < len(sourceIDs); index++ {
		if sourceIDs[index-1] == sourceIDs[index] {
			return nil, fmt.Errorf("Source %s is declared more than once: %w", sourceIDs[index], ErrPrecondition)
		}
	}

	now := service.now()
	id, err := service.allocate(inventory, research.KindTry, now, nil)
	if err != nil {
		return nil, err
	}
	value := &research.Try{
		Common: research.Common{
			Schema: research.SchemaTry, ID: id, Title: request.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), request.Tags...),
		},
		State: research.TryOpen, Goal: request.Goal, Sources: sourceIDs,
		Extensions: cloneExtensions(request.Extensions),
	}
	changes.create(&record.Document{Record: value, Body: request.Body})
	transaction, err := service.store.Transact(ctx, record.TransactionRequest{Operation: "try.create", Changes: changes.values})
	if err != nil {
		result := &CreateResult{TransactionID: transactionID(transaction)}
		if transaction != nil {
			result.Try, _ = resultDocument(transaction, id)
			return result, transactionError(transaction, err)
		}
		return nil, err
	}
	document, err := resultDocument(transaction, id)
	if err != nil {
		return nil, err
	}
	return &CreateResult{TransactionID: transaction.TransactionID, Try: document}, nil
}

type ConcludeRequest struct {
	Try           RevisionRef
	Summary       string
	ResultDigests []string
	ExternalRefs  []research.ExternalRef
}

type TransitionResult struct {
	TransactionID string
	Try           *record.Document
}

// Conclude records an explicit human summary and selected bounded results.
func (service *Service) Conclude(ctx context.Context, request ConcludeRequest) (*TransitionResult, error) {
	if strings.TrimSpace(request.Summary) == "" {
		return nil, fmt.Errorf("human conclusion summary is required: %w", ErrPrecondition)
	}
	inventory, currentDocument, current, err := service.openTry(ctx, request.Try)
	if err != nil {
		return nil, err
	}
	digests := append([]string(nil), request.ResultDigests...)
	sort.Strings(digests)
	if (current.State == research.TryConcluded || current.State == research.TryAdopted) && current.Conclusion != nil {
		if current.Conclusion.Summary == request.Summary && sameStrings(current.Conclusion.ResultDigests, digests) && sameExternalRefs(current.Conclusion.ExternalRefs, request.ExternalRefs) {
			return &TransitionResult{Try: currentDocument.Clone()}, nil
		}
		return nil, fmt.Errorf("Try %s already has a different conclusion: %w", current.ID, ErrPrecondition)
	}
	if current.State != research.TryOpen {
		return nil, fmt.Errorf("Try %s is %s, not open: %w", current.ID, current.State, ErrPrecondition)
	}
	attempts, err := closureAttempts(inventory, current.ID, digests)
	if err != nil {
		return nil, err
	}
	now := service.lifecycleTime(inventory, current)
	replacement := currentDocument.Clone()
	value := replacement.Record.(*research.Try)
	value.State = research.TryConcluded
	value.UpdatedAt = now
	value.Conclusion = &research.TryConclusion{
		ConcludedAt: now, Summary: request.Summary,
		ResultDigests: digests,
		ExternalRefs:  cloneExternalRefs(request.ExternalRefs),
	}
	return service.transition(ctx, currentDocument, replacement, request.Try.Revision, attempts, "try.conclude")
}

type AbandonRequest struct {
	Try    RevisionRef
	Reason string
}

// Abandon records the explicit human reason for ending an open Try.
func (service *Service) Abandon(ctx context.Context, request AbandonRequest) (*TransitionResult, error) {
	if strings.TrimSpace(request.Reason) == "" {
		return nil, fmt.Errorf("human abandonment reason is required: %w", ErrPrecondition)
	}
	inventory, currentDocument, current, err := service.openTry(ctx, request.Try)
	if err != nil {
		return nil, err
	}
	if current.State == research.TryAbandoned && current.Abandonment != nil {
		if current.Abandonment.Reason == request.Reason {
			return &TransitionResult{Try: currentDocument.Clone()}, nil
		}
		return nil, fmt.Errorf("Try %s already has a different abandonment reason: %w", current.ID, ErrPrecondition)
	}
	if current.State != research.TryOpen {
		return nil, fmt.Errorf("Try %s is %s, not open: %w", current.ID, current.State, ErrPrecondition)
	}
	attempts, err := closureAttempts(inventory, current.ID, nil)
	if err != nil {
		return nil, err
	}
	now := service.lifecycleTime(inventory, current)
	replacement := currentDocument.Clone()
	value := replacement.Record.(*research.Try)
	value.State = research.TryAbandoned
	value.UpdatedAt = now
	value.Abandonment = &research.TryAbandonment{AbandonedAt: now, Reason: request.Reason}
	return service.transition(ctx, currentDocument, replacement, request.Try.Revision, attempts, "try.abandon")
}

type AdoptRequest struct {
	Try            RevisionRef
	Title          string
	Body           string
	Summary        string
	ProposedBy     string
	PrimaryCluster string
	Classification research.Classification
	Parents        []RevisionRef
	Tags           []string
	Extensions     research.Extensions
}

type AdoptResult struct {
	TransactionID string
	Try           *record.Document
	Idea          *record.Document
}

// Adopt atomically creates an Idea v2 linked through origin_try and marks the
// already-concluded Try adopted. ProposedBy must explicitly identify a human.
func (service *Service) Adopt(ctx context.Context, request AdoptRequest) (*AdoptResult, error) {
	if strings.TrimSpace(request.Title) == "" || strings.TrimSpace(request.Summary) == "" || !humanIdentity(request.ProposedBy) {
		return nil, fmt.Errorf("Idea title, summary, and explicit human proposed_by are required: %w", ErrPrecondition)
	}
	inventory, currentDocument, current, err := service.openTry(ctx, request.Try)
	if err != nil {
		return nil, err
	}
	if current.State == research.TryAdopted && !current.AdoptedIdea.IsZero() {
		ideaDocument, lookupErr := inventory.ByID(current.AdoptedIdea)
		if lookupErr != nil {
			return nil, lookupErr
		}
		idea, ok := ideaDocument.Record.(*research.Idea)
		parentIDs := make([]research.ID, 0, len(request.Parents))
		for _, parent := range request.Parents {
			parentIDs = append(parentIDs, parent.ID)
		}
		sort.Slice(parentIDs, func(i, j int) bool { return parentIDs[i].String() < parentIDs[j].String() })
		tags := append([]string(nil), request.Tags...)
		sort.Strings(tags)
		if ok && idea.OriginTry == current.ID && idea.Title == request.Title && idea.Summary == request.Summary && idea.ProposedBy == request.ProposedBy && idea.PrimaryCluster == request.PrimaryCluster &&
			reflect.DeepEqual(idea.Classification, request.Classification) && sameIDs(idea.Parents, parentIDs) && sameStrings(idea.Tags, tags) &&
			(request.Body == "" || ideaDocument.Body == request.Body) {
			return &AdoptResult{Try: currentDocument.Clone(), Idea: ideaDocument.Clone()}, nil
		}
		return nil, fmt.Errorf("Try %s was already adopted with different Idea content: %w", current.ID, ErrPrecondition)
	}
	if current.State != research.TryConcluded || current.Conclusion == nil {
		return nil, fmt.Errorf("Try %s must have a human conclusion before adoption: %w", current.ID, ErrPrecondition)
	}

	changes := newChanges()
	parentIDs := make([]research.ID, 0, len(request.Parents))
	for _, parent := range request.Parents {
		document, resolveErr := resolve(inventory, parent, research.KindIdea)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if guardErr := changes.guard(document, parent.Revision); guardErr != nil {
			return nil, guardErr
		}
		parentIDs = append(parentIDs, parent.ID)
	}
	sort.Slice(parentIDs, func(i, j int) bool { return parentIDs[i].String() < parentIDs[j].String() })
	for index := 1; index < len(parentIDs); index++ {
		if parentIDs[index-1] == parentIDs[index] {
			return nil, fmt.Errorf("Idea parent %s occurs more than once: %w", parentIDs[index], ErrPrecondition)
		}
	}

	now := service.lifecycleTime(inventory, current)
	ideaID, err := service.allocate(inventory, research.KindIdea, now, nil)
	if err != nil {
		return nil, err
	}
	idea := &research.Idea{
		Common: research.Common{
			Schema: research.SchemaIdeaV2, ID: ideaID, Title: request.Title,
			CreatedAt: now, UpdatedAt: now, Tags: append([]string(nil), request.Tags...),
		},
		State: research.IdeaProposed, Summary: request.Summary, ProposedBy: request.ProposedBy,
		PrimaryCluster: request.PrimaryCluster, Classification: request.Classification,
		Parents: parentIDs, OriginTry: current.ID, Extensions: cloneExtensions(request.Extensions),
	}
	replacement := currentDocument.Clone()
	adopted := replacement.Record.(*research.Try)
	adopted.State = research.TryAdopted
	adopted.UpdatedAt = now
	adopted.AdoptedIdea = ideaID
	changes.replace(replacement, request.Try.Revision)
	changes.create(&record.Document{Record: idea, Body: request.Body})

	transaction, err := service.store.Transact(ctx, record.TransactionRequest{Operation: "try.adopt", Changes: changes.values})
	if err != nil {
		result := &AdoptResult{TransactionID: transactionID(transaction)}
		if transaction != nil {
			result.Try, _ = resultDocument(transaction, current.ID)
			result.Idea, _ = resultDocument(transaction, ideaID)
			return result, transactionError(transaction, err)
		}
		return nil, err
	}
	tryDocument, err := resultDocument(transaction, current.ID)
	if err != nil {
		return nil, err
	}
	ideaDocument, err := resultDocument(transaction, ideaID)
	if err != nil {
		return nil, err
	}
	return &AdoptResult{TransactionID: transaction.TransactionID, Try: tryDocument, Idea: ideaDocument}, nil
}

func (service *Service) transition(ctx context.Context, current, replacement *record.Document, revision string, guards []*record.Document, operation string) (*TransitionResult, error) {
	id, _ := current.ID()
	changes := newChanges()
	changes.replace(replacement, revision)
	for _, guarded := range guards {
		if err := changes.guard(guarded, guarded.Revision); err != nil {
			return nil, err
		}
	}
	transaction, err := service.store.Transact(ctx, record.TransactionRequest{Operation: operation, Changes: changes.values})
	if err != nil {
		result := &TransitionResult{TransactionID: transactionID(transaction)}
		if transaction != nil {
			result.Try, _ = resultDocument(transaction, id)
			return result, transactionError(transaction, err)
		}
		return nil, err
	}
	document, err := resultDocument(transaction, id)
	if err != nil {
		return nil, err
	}
	return &TransitionResult{TransactionID: transaction.TransactionID, Try: document}, nil
}

func closureAttempts(inventory *record.Inventory, tryID research.ID, selectedDigests []string) ([]*record.Document, error) {
	attempts := attemptsForTry(inventory, tryID)
	available := make(map[string]struct{})
	for _, document := range attempts {
		attempt := document.Record.(*research.Attempt)
		if !terminalAttempt(attempt.State) {
			return nil, fmt.Errorf("Try %s still owns nonterminal Attempt %s (%s): %w", tryID, attempt.ID, attempt.State, ErrPrecondition)
		}
		if table := attempt.Extensions[DirectExtensionNamespace]; table != nil {
			for _, digest := range extensionStringSlice(table["result_digests"]) {
				available[digest] = struct{}{}
			}
		}
	}
	for _, digest := range selectedDigests {
		if _, found := available[digest]; !found {
			return nil, fmt.Errorf("result digest %s was not produced by a terminal Attempt owned by Try %s: %w", digest, tryID, ErrPrecondition)
		}
	}
	return attempts, nil
}

func extensionStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func transactionError(result *record.TransactionResult, err error) error {
	if result == nil || err == nil {
		return err
	}
	copy := *result
	copy.Paths = append([]record.TransactionPathResult(nil), result.Paths...)
	copy.Documents = make([]*record.Document, len(result.Documents))
	for index, document := range result.Documents {
		copy.Documents[index] = document.Clone()
	}
	return &TransactionError{Result: &copy, Err: err}
}

func (service *Service) openTry(ctx context.Context, reference RevisionRef) (*record.Inventory, *record.Document, *research.Try, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	document, err := resolve(inventory, reference, research.KindTry)
	if err != nil {
		return nil, nil, nil, err
	}
	value, ok := document.Record.(*research.Try)
	if !ok {
		return nil, nil, nil, fmt.Errorf("Try record has unexpected type %T: %w", document.Record, ErrPrecondition)
	}
	return inventory, document, value, nil
}

func (service *Service) snapshot(ctx context.Context) (*record.Inventory, error) {
	if service == nil || service.store == nil {
		return nil, errors.New("Try service requires a canonical store")
	}
	inventory, err := service.store.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	if !inventory.Valid() {
		return nil, &record.InventoryError{Diagnostics: append([]record.Diagnostic(nil), inventory.Diagnostics...)}
	}
	return inventory, nil
}

func (service *Service) now() time.Time { return service.clock().UTC() }

func (service *Service) after(previous time.Time) time.Time {
	now := service.now()
	if !now.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return now
}

func (service *Service) lifecycleTime(inventory *record.Inventory, current *research.Try) time.Time {
	latest := current.UpdatedAt
	if inventory != nil {
		for _, document := range inventory.OfKind(research.KindAttempt) {
			attempt := document.Record.(*research.Attempt)
			if attempt.Try == current.ID && attempt.UpdatedAt.After(latest) {
				latest = attempt.UpdatedAt
			}
		}
	}
	return service.after(latest)
}

func (service *Service) allocate(inventory *record.Inventory, kind research.Kind, now time.Time, reserved map[research.ID]struct{}) (research.ID, error) {
	if reserved == nil {
		reserved = make(map[research.ID]struct{})
	}
	for attempt := 0; attempt < service.collisionLimit; attempt++ {
		value, err := service.generate(now)
		if err != nil {
			return research.ID{}, fmt.Errorf("generate %s UUIDv7: %w", kind, err)
		}
		id, err := research.NewID(kind, value)
		if err != nil {
			return research.ID{}, fmt.Errorf("construct %s ID: %w", kind, err)
		}
		if _, found := reserved[id]; found {
			continue
		}
		if _, err := inventory.ByID(id); err == nil || errors.Is(err, research.ErrAmbiguousReference) {
			continue
		} else if !errors.Is(err, research.ErrReferenceNotFound) {
			return research.ID{}, err
		}
		reserved[id] = struct{}{}
		return id, nil
	}
	return research.ID{}, ErrCollision
}

func resolve(inventory *record.Inventory, reference RevisionRef, expected research.Kind) (*record.Document, error) {
	if reference.ID.IsZero() || reference.ID.Kind() != expected {
		return nil, fmt.Errorf("expected %s reference, got %s: %w", expected, reference.ID, ErrPrecondition)
	}
	if !record.ValidRevision(reference.Revision) {
		return nil, fmt.Errorf("%s: %w", reference.ID, ErrRevisionRequired)
	}
	document, err := inventory.ByID(reference.ID)
	if err != nil {
		return nil, err
	}
	if document.Kind() != expected {
		return nil, fmt.Errorf("%s is %s, expected %s: %w", reference.ID, document.Kind(), expected, ErrPrecondition)
	}
	if document.Revision != reference.Revision {
		return nil, &record.ConflictError{ID: reference.ID, Expected: reference.Revision, Actual: document.Revision}
	}
	return document, nil
}

type changes struct {
	values []record.TransactionChange
	seen   map[research.ID]string
}

func newChanges() *changes { return &changes{seen: make(map[research.ID]string)} }

func (changes *changes) guard(document *record.Document, revision string) error {
	id, ok := document.ID()
	if !ok {
		return fmt.Errorf("cannot revision-guard ID-less %s: %w", document.Kind(), ErrPrecondition)
	}
	if previous, found := changes.seen[id]; found {
		if previous != revision {
			return fmt.Errorf("%s has conflicting expected revisions: %w", id, ErrPrecondition)
		}
		return nil
	}
	changes.seen[id] = revision
	changes.values = append(changes.values, record.TransactionChange{
		Operation: record.TransactionReplace, Document: document.Clone(), ExpectedRevision: revision,
	})
	return nil
}

func (changes *changes) replace(document *record.Document, revision string) {
	id, _ := document.ID()
	changes.seen[id] = revision
	changes.values = append(changes.values, record.TransactionChange{
		Operation: record.TransactionReplace, Document: document, ExpectedRevision: revision,
	})
}

func (changes *changes) create(document *record.Document) {
	changes.values = append(changes.values, record.TransactionChange{Operation: record.TransactionCreate, Document: document})
}

func resultDocument(result *record.TransactionResult, id research.ID) (*record.Document, error) {
	if result == nil {
		return nil, errors.New("canonical transaction returned no result")
	}
	for _, document := range result.Documents {
		if documentID, ok := document.ID(); ok && documentID == id {
			return document, nil
		}
	}
	return nil, fmt.Errorf("canonical transaction omitted result %s", id)
}

func cloneExtensions(extensions research.Extensions) research.Extensions {
	if extensions == nil {
		return nil
	}
	return research.Clone(&research.Try{Extensions: extensions}).(*research.Try).Extensions
}

func cloneExternalRefs(references []research.ExternalRef) []research.ExternalRef {
	if references == nil {
		return nil
	}
	value := research.Clone(&research.Try{Conclusion: &research.TryConclusion{ExternalRefs: references}}).(*research.Try)
	return value.Conclusion.ExternalRefs
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameIDs(left, right []research.ID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameExternalRefs(left, right []research.ExternalRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !reflect.DeepEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func humanIdentity(value string) bool {
	return research.IsHumanIdentity(value)
}
