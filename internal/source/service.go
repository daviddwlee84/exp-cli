// Package source manages canonical Source bindings through exact-revision
// prepared transactions.
package source

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/google/uuid"
)

var (
	ErrPrecondition          = errors.New("Source lifecycle precondition failed")
	ErrRevisionRequired      = errors.New("canonical Source revision is required")
	ErrCollision             = errors.New("unable to allocate a unique Source ID")
	ErrKeyExists             = errors.New("Source key already exists")
	ErrLocatorExists         = errors.New("Source locator already exists")
	ErrAlreadyRetired        = errors.New("Source is already retired")
	ErrLocalOnlyConfirmation = errors.New("local-only Source requires explicit confirmation")
	ErrLocatorObservation    = errors.New("Source locator does not match the inspected clone")
)

// Store is the canonical prepared-transaction boundary used by Service.
type Store interface {
	Inventory(context.Context) (*record.Inventory, error)
	Transact(context.Context, record.TransactionRequest) (*record.TransactionResult, error)
}

// TransactionError preserves durable transaction identity and per-path progress
// when canonical Source publication returns an error after preparation.
type TransactionError struct {
	Result *record.TransactionResult
	Err    error
}

func (failure *TransactionError) Error() string {
	if failure == nil || failure.Err == nil {
		return "canonical Source transaction failed"
	}
	return failure.Err.Error()
}

func (failure *TransactionError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

// TransactionResultFromError returns durable Source transaction metadata carried
// by err. The result must be treated as immutable by callers.
func TransactionResultFromError(err error) (*record.TransactionResult, bool) {
	var failure *TransactionError
	if !errors.As(err, &failure) || failure.Result == nil {
		return nil, false
	}
	return failure.Result, true
}

// Service owns Source creation, lookup, locator enrichment, and retirement.
type Service struct {
	store          Store
	clock          func() time.Time
	generate       research.UUIDGenerator
	collisionLimit int
}

// Option configures deterministic Source creation seams.
type Option func(*Service)

// WithClock supplies Source creation and update timestamps.
func WithClock(clock func() time.Time) Option {
	return func(service *Service) { service.clock = clock }
}

// WithUUIDGenerator supplies UUIDv7 values for new Source records.
func WithUUIDGenerator(generator research.UUIDGenerator) Option {
	return func(service *Service) { service.generate = generator }
}

// WithCollisionLimit bounds retries after generated IDs collide with canonical
// inventories or Git-common linked-worktree reservations.
func WithCollisionLimit(limit int) Option {
	return func(service *Service) { service.collisionLimit = limit }
}

// New constructs a Source service. Operations reject a nil store.
func New(store Store, options ...Option) *Service {
	service := &Service{
		store:          store,
		clock:          time.Now,
		generate:       research.DefaultUUIDGenerator,
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

// AddRequest is the complete caller-controlled payload for an active Git Source.
// Empty Subdir defaults to "." and empty Title defaults to the normalized Key.
type AddRequest struct {
	Key          string
	Title        string
	Subdir       string
	LocatorHints []string
	Tags         []string
	Body         string
	Extensions   research.Extensions
}

// AddPlanRequest combines canonical Source input with a previously inspected
// local clone observation. Building the plan is deterministic and side-effect
// free; repository inspection remains a separate boundary.
type AddPlanRequest struct {
	Request              AddRequest
	CloneRoot            string
	CloneGitCommonDir    string
	ObservedLocatorHints []string
	ConfirmLocalOnly     bool
}

// AddPlan is the complete normalized effect description accepted by
// ApplyAddPlan. Local paths stay host-local and are never copied into Source.
type AddPlan struct {
	Request              AddRequest
	CloneRoot            string
	CloneGitCommonDir    string
	ObservedLocatorHints []string
	LocalOnly            bool
	LocalOnlyConfirmed   bool
}

// AppendLocatorRequest appends one sanitized locator against an exact revision.
type AppendLocatorRequest struct {
	Reference        string
	ExpectedRevision string
	Locator          string
}

// RetireRequest retires one active Source against an exact revision.
type RetireRequest struct {
	Reference        string
	ExpectedRevision string
}

// BuildAddPlan normalizes and validates the complete Source/clone effect without
// allocating an ID, publishing canonical bytes, or updating local associations.
func BuildAddPlan(input AddPlanRequest) (AddPlan, error) {
	if err := validatePlanAbsolutePath(input.CloneRoot, "Source clone root"); err != nil {
		return AddPlan{}, err
	}
	if err := validatePlanAbsolutePath(input.CloneGitCommonDir, "Source clone Git common dir"); err != nil {
		return AddPlan{}, err
	}
	key, err := research.NormalizeSourceKey(input.Request.Key)
	if err != nil {
		return AddPlan{}, fmt.Errorf("normalize Source key: %w", err)
	}
	subdir, err := research.NormalizeSourceSubdir(input.Request.Subdir)
	if err != nil {
		return AddPlan{}, fmt.Errorf("normalize Source subdir: %w", err)
	}
	requested, err := normalizeLocatorHints(input.Request.LocatorHints)
	if err != nil {
		return AddPlan{}, err
	}
	observed, err := normalizeLocatorHints(input.ObservedLocatorHints)
	if err != nil {
		return AddPlan{}, fmt.Errorf("normalize inspected clone locator: %w", err)
	}
	locatorSet := make(map[string]struct{}, len(requested)+len(observed))
	locators := make([]string, 0, len(requested)+len(observed))
	for _, values := range [][]string{requested, observed} {
		for _, locator := range values {
			if _, found := locatorSet[locator]; found {
				continue
			}
			locatorSet[locator] = struct{}{}
			locators = append(locators, locator)
		}
	}
	sort.Strings(locators)
	sort.Strings(observed)
	localOnly := len(locators) == 0
	if localOnly && !input.ConfirmLocalOnly {
		return AddPlan{}, ErrLocalOnlyConfirmation
	}
	if !localOnly && len(observed) == 0 {
		return AddPlan{}, ErrLocatorObservation
	}

	title := input.Request.Title
	if title == "" {
		title = key
	}
	body := input.Request.Body
	if body == "" {
		body = "\n# " + title + "\n"
	}
	normalized := AddRequest{
		Key: key, Title: title, Subdir: subdir, LocatorHints: append([]string{}, locators...),
		Tags: append([]string{}, input.Request.Tags...), Body: body,
		Extensions: cloneExtensions(input.Request.Extensions),
	}
	placeholderID, idErr := research.NewID(research.KindSource, uuid.MustParse("01a00000-0000-7000-8000-000000000000"))
	if idErr != nil {
		return AddPlan{}, idErr
	}
	placeholderTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidate := &research.Source{
		Common: research.Common{
			Schema: research.SchemaSource, ID: placeholderID, Title: normalized.Title,
			CreatedAt: placeholderTime, UpdatedAt: placeholderTime, Tags: append([]string{}, normalized.Tags...),
		},
		Key: normalized.Key, Kind: research.SourceGit, Subdir: normalized.Subdir,
		LocatorHints: append([]string{}, normalized.LocatorHints...), State: research.SourceActive,
		Extensions: cloneExtensions(normalized.Extensions),
	}
	if err := research.Validate(candidate); err != nil {
		return AddPlan{}, fmt.Errorf("validate Source plan: %w", err)
	}
	research.Normalize(candidate)
	normalized.Key = candidate.Key
	normalized.Title = candidate.Title
	normalized.Subdir = candidate.Subdir
	normalized.LocatorHints = append([]string{}, candidate.LocatorHints...)
	normalized.Tags = append([]string{}, candidate.Tags...)
	normalized.Extensions = cloneExtensions(candidate.Extensions)
	encoded, err := record.Encode(&record.Document{Record: candidate, Body: normalized.Body})
	if err != nil {
		return AddPlan{}, fmt.Errorf("encode Source plan: %w", err)
	}
	if err := record.ValidateRecordSize(encoded); err != nil {
		return AddPlan{}, fmt.Errorf("validate Source plan size: %w", err)
	}
	return AddPlan{
		Request: normalized, CloneRoot: input.CloneRoot, CloneGitCommonDir: input.CloneGitCommonDir,
		ObservedLocatorHints: append([]string{}, observed...), LocalOnly: localOnly,
		LocalOnlyConfirmed: input.ConfirmLocalOnly,
	}, nil
}

// ApplyAddPlan revalidates a pure plan immediately before canonical
// publication. Local association publication is intentionally owned by the
// caller so a later failure cannot roll back canonical authority.
func (service *Service) ApplyAddPlan(ctx context.Context, plan AddPlan) (*record.Document, error) {
	normalized, err := BuildAddPlan(AddPlanRequest{
		Request: plan.Request, CloneRoot: plan.CloneRoot, CloneGitCommonDir: plan.CloneGitCommonDir,
		ObservedLocatorHints: plan.ObservedLocatorHints, ConfirmLocalOnly: plan.LocalOnlyConfirmed,
	})
	if err != nil {
		return nil, err
	}
	return service.Add(ctx, normalized.Request)
}

func validatePlanAbsolutePath(value, label string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s must be a clean absolute path", label)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains control characters", label)
		}
	}
	return nil
}

// Add creates one active Source. ID reservations held by another linked worktree
// are treated as collisions and retried with a fresh UUID.
func (service *Service) Add(ctx context.Context, request AddRequest) (*record.Document, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	key, err := research.NormalizeSourceKey(request.Key)
	if err != nil {
		return nil, fmt.Errorf("normalize Source key: %w", err)
	}
	if _, err := inventory.BySourceKey(key); err == nil || errors.Is(err, research.ErrAmbiguousReference) {
		return nil, fmt.Errorf("Source key %q: %w", key, ErrKeyExists)
	} else if !errors.Is(err, research.ErrReferenceNotFound) {
		return nil, err
	}
	subdir, err := research.NormalizeSourceSubdir(request.Subdir)
	if err != nil {
		return nil, fmt.Errorf("normalize Source subdir: %w", err)
	}
	locators, err := normalizeLocatorHints(request.LocatorHints)
	if err != nil {
		return nil, err
	}
	title := request.Title
	if title == "" {
		title = key
	}
	now := service.now()
	for attempt := 0; attempt < service.collisionLimit; attempt++ {
		generated, generateErr := service.generate(now)
		if generateErr != nil {
			return nil, fmt.Errorf("generate Source UUIDv7: %w", generateErr)
		}
		id, idErr := research.NewID(research.KindSource, generated)
		if idErr != nil {
			return nil, fmt.Errorf("construct Source ID: %w", idErr)
		}
		if _, lookupErr := inventory.ByID(id); lookupErr == nil || errors.Is(lookupErr, research.ErrAmbiguousReference) {
			continue
		} else if !errors.Is(lookupErr, research.ErrReferenceNotFound) {
			return nil, lookupErr
		}
		value := &research.Source{
			Common: research.Common{
				Schema:    research.SchemaSource,
				ID:        id,
				Title:     title,
				CreatedAt: now,
				UpdatedAt: now,
				Tags:      append([]string(nil), request.Tags...),
			},
			Key:          key,
			Kind:         research.SourceGit,
			Subdir:       subdir,
			LocatorHints: append([]string{}, locators...),
			State:        research.SourceActive,
			Extensions:   cloneExtensions(request.Extensions),
		}
		result, transactErr := service.store.Transact(ctx, record.TransactionRequest{
			Operation: "source.add",
			Changes: []record.TransactionChange{{
				Operation: record.TransactionCreate,
				Document:  &record.Document{Record: value, Body: request.Body},
			}},
		})
		if result == nil && errors.Is(transactErr, record.ErrAlreadyExists) {
			continue
		}
		if result == nil && transactErr != nil && sourceKeyConflict(transactErr, key) {
			return nil, fmt.Errorf("Source key %q: %w", key, ErrKeyExists)
		}
		return transactionOutcome(result, id, transactErr)
	}
	return nil, ErrCollision
}

// Lookup resolves an exact project-local key before falling back to typed ID,
// typed-prefix, and Source display-code syntax. Retired Sources remain resolvable.
func (service *Service) Lookup(ctx context.Context, reference string) (*record.Document, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return inventory.ResolveSource(strings.TrimSpace(reference))
}

// List returns all Sources ordered by key and then complete typed ID.
func (service *Service) List(ctx context.Context) ([]*record.Document, error) {
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	documents := append([]*record.Document(nil), inventory.OfKind(research.KindSource)...)
	sort.SliceStable(documents, func(left, right int) bool {
		leftSource := documents[left].Record.(*research.Source)
		rightSource := documents[right].Record.(*research.Source)
		if leftSource.Key != rightSource.Key {
			return leftSource.Key < rightSource.Key
		}
		return leftSource.ID.String() < rightSource.ID.String()
	})
	return documents, nil
}

// AppendLocator adds one normalized remote hint while preserving the exact
// existing locator prefix.
func (service *Service) AppendLocator(ctx context.Context, request AppendLocatorRequest) (*record.Document, error) {
	current, err := service.resolveExact(ctx, request.Reference, request.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	locator, err := research.NormalizeSourceLocator(request.Locator)
	if err != nil {
		return nil, fmt.Errorf("normalize Source locator: %w", err)
	}
	source := current.Record.(*research.Source)
	for _, existing := range source.LocatorHints {
		if existing == locator {
			return nil, fmt.Errorf("Source %s: %w", source.ID, ErrLocatorExists)
		}
	}
	if len(source.LocatorHints) >= research.MaxSourceLocatorHints {
		return nil, fmt.Errorf("Source %s already has the maximum number of locator hints: %w", source.ID, ErrPrecondition)
	}
	replacement := current.Clone()
	updated := replacement.Record.(*research.Source)
	updated.LocatorHints = append(updated.LocatorHints, locator)
	updated.UpdatedAt = service.now()
	return service.replace(ctx, current, replacement, request.ExpectedRevision, "source.append-locator")
}

// Retire transitions an active Source to retired and records the same exact UTC
// timestamp in updated_at and retired_at.
func (service *Service) Retire(ctx context.Context, request RetireRequest) (*record.Document, error) {
	current, err := service.resolveExact(ctx, request.Reference, request.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	source := current.Record.(*research.Source)
	if source.State == research.SourceRetired {
		return nil, fmt.Errorf("Source %s: %w", source.ID, ErrAlreadyRetired)
	}
	if source.State != research.SourceActive {
		return nil, fmt.Errorf("Source %s is not active: %w", source.ID, ErrPrecondition)
	}
	now := service.now()
	replacement := current.Clone()
	updated := replacement.Record.(*research.Source)
	updated.State = research.SourceRetired
	updated.RetiredAt = &now
	updated.UpdatedAt = now
	return service.replace(ctx, current, replacement, request.ExpectedRevision, "source.retire")
}

func (service *Service) snapshot(ctx context.Context) (*record.Inventory, error) {
	if service == nil || service.store == nil {
		return nil, errors.New("Source service requires a canonical store")
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

func (service *Service) resolveExact(ctx context.Context, reference, expectedRevision string) (*record.Document, error) {
	if !record.ValidRevision(expectedRevision) {
		return nil, ErrRevisionRequired
	}
	inventory, err := service.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	document, err := inventory.ResolveSource(strings.TrimSpace(reference))
	if err != nil {
		return nil, err
	}
	id, ok := document.ID()
	if !ok || document.Kind() != research.KindSource {
		return nil, fmt.Errorf("reference does not identify a Source: %w", ErrPrecondition)
	}
	if document.Revision != expectedRevision {
		return nil, &record.ConflictError{ID: id, Expected: expectedRevision, Actual: document.Revision}
	}
	return document, nil
}

func (service *Service) replace(ctx context.Context, current, replacement *record.Document, expectedRevision, operation string) (*record.Document, error) {
	id, _ := current.ID()
	result, err := service.store.Transact(ctx, record.TransactionRequest{
		Operation: operation,
		Changes: []record.TransactionChange{{
			Operation:        record.TransactionReplace,
			Document:         replacement,
			ExpectedRevision: expectedRevision,
		}},
	})
	return transactionOutcome(result, id, err)
}

func (service *Service) now() time.Time { return service.clock().UTC() }

func normalizeLocatorHints(values []string) ([]string, error) {
	if len(values) > research.MaxSourceLocatorHints {
		return nil, fmt.Errorf("Source has more than %d locator hints: %w", research.MaxSourceLocatorHints, ErrPrecondition)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		normalized, err := research.NormalizeSourceLocator(value)
		if err != nil {
			return nil, fmt.Errorf("normalize Source locator %d: %w", index, err)
		}
		if _, duplicate := seen[normalized]; duplicate {
			return nil, fmt.Errorf("Source locator %d duplicates an earlier normalized locator: %w", index, ErrLocatorExists)
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func transactionDocument(result *record.TransactionResult, id research.ID) (*record.Document, error) {
	if result == nil {
		return nil, errors.New("canonical Source transaction returned no result")
	}
	for _, document := range result.Documents {
		if documentID, ok := document.ID(); ok && documentID == id {
			return document, nil
		}
	}
	return nil, fmt.Errorf("canonical Source transaction omitted %s", id)
}

func transactionOutcome(result *record.TransactionResult, id research.ID, transactionErr error) (*record.Document, error) {
	document, documentErr := transactionDocument(result, id)
	if transactionErr == nil {
		return document, documentErr
	}
	if result == nil {
		return nil, transactionErr
	}
	return document, &TransactionError{Result: result, Err: errors.Join(transactionErr, documentErr)}
}

func sourceKeyConflict(err error, key string) bool {
	var inventory *record.InventoryError
	if !errors.As(err, &inventory) {
		return false
	}
	for _, diagnostic := range inventory.Diagnostics {
		if diagnostic.Code == "source.duplicate_key" && strings.Contains(diagnostic.Message, fmt.Sprintf("%q", key)) {
			return true
		}
	}
	return false
}

func cloneExtensions(extensions research.Extensions) research.Extensions {
	if extensions == nil {
		return nil
	}
	value := &research.Source{LocatorHints: []string{}, Extensions: extensions}
	return research.Clone(value).(*research.Source).Extensions
}
