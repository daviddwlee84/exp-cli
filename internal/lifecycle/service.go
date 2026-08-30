// Package lifecycle implements the scientific closure, evaluation, candidate,
// release, and human promotion workflows on top of canonical prepared
// transactions.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

var (
	// ErrPrecondition means canonical state does not permit the requested
	// scientific transition.
	ErrPrecondition = errors.New("scientific lifecycle precondition failed")
	// ErrRevisionRequired means a prerequisite was not pinned to an exact
	// canonical revision.
	ErrRevisionRequired = errors.New("canonical expected revision is required")
	// ErrCollision means the service could not allocate a free canonical ID.
	ErrCollision = errors.New("unable to allocate a unique lifecycle record ID")
)

// RevisionRef pins one canonical prerequisite. Every lifecycle operation uses
// exact revisions so that concurrent edits fail closed.
type RevisionRef struct {
	ID       research.ID
	Revision string
}

// Store is the canonical prepared-transaction boundary used by lifecycle.
type Store interface {
	Inventory(context.Context) (*record.Inventory, error)
	Transact(context.Context, record.TransactionRequest) (*record.TransactionResult, error)
}

// Service owns lifecycle transitions for one canonical record store.
type Service struct {
	store          Store
	clock          func() time.Time
	generate       research.UUIDGenerator
	collisionLimit int
}

// Option configures deterministic lifecycle creation seams.
type Option func(*Service)

// WithClock supplies the timestamp used for one immutable audit event.
func WithClock(clock func() time.Time) Option {
	return func(service *Service) { service.clock = clock }
}

// WithUUIDGenerator supplies UUIDv7 values for newly created records.
func WithUUIDGenerator(generator research.UUIDGenerator) Option {
	return func(service *Service) { service.generate = generator }
}

// WithCollisionLimit bounds retries after generated-ID collisions.
func WithCollisionLimit(limit int) Option {
	return func(service *Service) { service.collisionLimit = limit }
}

// New constructs a lifecycle service. A nil store is rejected by every
// operation rather than panicking.
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

func (service *Service) snapshot(ctx context.Context) (*record.Inventory, error) {
	if service == nil || service.store == nil {
		return nil, errors.New("lifecycle service requires a canonical store")
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

func (service *Service) allocate(inventory *record.Inventory, kind research.Kind, now time.Time, reserved map[research.ID]struct{}) (research.ID, error) {
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

func resolveAny(inventory *record.Inventory, reference RevisionRef, expected ...research.Kind) (*record.Document, error) {
	if reference.ID.IsZero() {
		return nil, fmt.Errorf("subject reference is empty: %w", ErrPrecondition)
	}
	for _, kind := range expected {
		if reference.ID.Kind() == kind {
			return resolve(inventory, reference, kind)
		}
	}
	return nil, fmt.Errorf("%s has unsupported kind %s: %w", reference.ID, reference.ID.Kind(), ErrPrecondition)
}

type guardedChanges struct {
	changes []record.TransactionChange
	seen    map[research.ID]string
}

func newGuardedChanges() *guardedChanges {
	return &guardedChanges{seen: make(map[research.ID]string)}
}

func (changes *guardedChanges) guard(document *record.Document, revision string) error {
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
	changes.changes = append(changes.changes, record.TransactionChange{
		Operation: record.TransactionReplace, Document: document.Clone(), ExpectedRevision: revision,
	})
	return nil
}

func (changes *guardedChanges) replace(document *record.Document, revision string) error {
	id, ok := document.ID()
	if !ok {
		return fmt.Errorf("cannot replace ID-less %s: %w", document.Kind(), ErrPrecondition)
	}
	if _, found := changes.seen[id]; found {
		return fmt.Errorf("%s is already guarded by this transaction: %w", id, ErrPrecondition)
	}
	changes.seen[id] = revision
	changes.changes = append(changes.changes, record.TransactionChange{
		Operation: record.TransactionReplace, Document: document, ExpectedRevision: revision,
	})
	return nil
}

func (changes *guardedChanges) create(document *record.Document) {
	changes.changes = append(changes.changes, record.TransactionChange{Operation: record.TransactionCreate, Document: document})
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
	return research.Clone(&research.Candidate{Extensions: extensions}).(*research.Candidate).Extensions
}

func cloneExternalRefs(references []research.ExternalRef) []research.ExternalRef {
	if references == nil {
		return nil
	}
	candidate := research.Clone(&research.Candidate{ExternalRefs: references}).(*research.Candidate)
	return candidate.ExternalRefs
}

func uniqueIDs(ids []research.ID) []research.ID {
	seen := make(map[research.ID]struct{}, len(ids))
	result := make([]research.ID, 0, len(ids))
	for _, id := range ids {
		if _, found := seen[id]; found {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func sameIDSet(left, right []research.ID) bool {
	left, right = uniqueIDs(left), uniqueIDs(right)
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
