package record

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

// PlanInput is the complete user-controlled payload for a new queued Plan.
type PlanInput struct {
	Title          string
	Body           string
	Priority       research.Priority
	Effort         research.Effort
	ExpectedPayoff research.ExpectedPayoff
	Tags           []string
	Assumptions    []research.ID
	Extensions     research.Extensions
}

// CreatePlan allocates a collision-checked UUIDv7 and publishes one queued Plan.
func (store *Store) CreatePlan(ctx context.Context, input PlanInput) (*Document, error) {
	if err := validatePlanBody(input.Body); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var result *Document
	err := store.withMutationLock(ctx, func() error {
		inventory, err := store.validInventoryLocked(ctx)
		if err != nil {
			return err
		}
		now := store.clock().UTC()
		for attempt := 0; attempt < store.collisionLimit; attempt++ {
			generated, err := store.generate(now)
			if err != nil {
				return fmt.Errorf("generate Plan UUIDv7: %w", err)
			}
			id, err := research.NewID(research.KindPlan, generated)
			if err != nil {
				return fmt.Errorf("generate Plan UUIDv7: %w", err)
			}
			if _, err := inventory.ByID(id); err == nil || !errors.Is(err, research.ErrReferenceNotFound) {
				if err == nil || errors.Is(err, research.ErrAmbiguousReference) {
					continue
				}
				return err
			}
			plan := &research.Plan{
				Common: research.Common{
					Schema:    research.SchemaPlan,
					ID:        id,
					Title:     input.Title,
					CreatedAt: now,
					UpdatedAt: now,
					Tags:      append([]string(nil), input.Tags...),
				},
				Priority:       input.Priority,
				Effort:         input.Effort,
				State:          research.PlanQueued,
				Assumptions:    append([]research.ID(nil), input.Assumptions...),
				ExpectedPayoff: input.ExpectedPayoff,
				Extensions:     cloneInputExtensions(input.Extensions),
			}
			document := &Document{Record: plan, Body: input.Body}
			document.Path, err = PathForNew(plan, inventory)
			if err != nil {
				return err
			}
			result, err = store.createLocked(inventory, document)
			if errors.Is(err, ErrAlreadyExists) {
				continue
			}
			return err
		}
		return ErrCollision
	})
	return result, err
}

func cloneInputExtensions(extensions research.Extensions) research.Extensions {
	if extensions == nil {
		return nil
	}
	// Clone through a temporary valid shape without exposing research's internal helper.
	plan := &research.Plan{Extensions: extensions}
	cloned := research.Clone(plan).(*research.Plan)
	return cloned.Extensions
}

func validatePlanBody(body string) error {
	if !utf8.ValidString(body) || strings.ContainsRune(body, '\x00') || strings.ContainsRune(body, '\r') {
		return fmt.Errorf("Plan body must be UTF-8 with LF line endings and no NUL: %w", ErrInvalidBody)
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("Plan body is empty: %w", ErrInvalidBody)
	}
	if err := research.ValidateCommitSafeText(body); err != nil {
		return fmt.Errorf("Plan body is not commit-safe: %w", errors.Join(ErrInvalidBody, err))
	}
	return nil
}

// ListPlans returns parse-valid Plans in deterministic state/priority/ID order,
// alongside all inventory diagnostics.
func (store *Store) ListPlans(ctx context.Context) ([]*Document, []Diagnostic, error) {
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return nil, nil, err
	}
	documents := inventory.OfKind(research.KindPlan)
	stateOrder := map[research.PlanState]int{research.PlanQueued: 0, research.PlanStarted: 1, research.PlanCompleted: 2, research.PlanDropped: 3}
	priorityOrder := map[research.Priority]int{research.PriorityP1: 0, research.PriorityP2: 1, research.PriorityP3: 2, research.PriorityUnknown: 3}
	sort.SliceStable(documents, func(i, j int) bool {
		left := documents[i].Record.(*research.Plan)
		right := documents[j].Record.(*research.Plan)
		if stateOrder[left.State] != stateOrder[right.State] {
			return stateOrder[left.State] < stateOrder[right.State]
		}
		if priorityOrder[left.Priority] != priorityOrder[right.Priority] {
			return priorityOrder[left.Priority] < priorityOrder[right.Priority]
		}
		return left.ID.String() < right.ID.String()
	})
	return documents, append([]Diagnostic(nil), inventory.Diagnostics...), nil
}

// ReadPlan resolves a full ID, unique typed prefix/display code, or migration alias.
func (store *Store) ReadPlan(ctx context.Context, reference string) (*Document, []Diagnostic, error) {
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return nil, nil, err
	}
	document, err := inventory.Resolve(reference, research.KindPlan)
	return document, append([]Diagnostic(nil), inventory.Diagnostics...), err
}
