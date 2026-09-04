// Package trust stores explicit, host-local approvals for execution-bearing
// repository configuration. Receipts never establish Project or Source identity.
package trust

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	// Schema is the closed trust receipt schema stored in trust/v1.json.
	Schema = "exp.trust/v1"
	// MaxStoreBytes bounds the complete private receipt database.
	MaxStoreBytes int64 = 4 << 20
)

// Capability identifies one execution-bearing configuration class.
type Capability string

const (
	CapabilitySourceSelection  Capability = "source.selection"
	CapabilityWorkspaceBackend Capability = "workspace.backend"
	CapabilityAgentProfile     Capability = "agent.profile"
	CapabilityMLflowProfile    Capability = "mlflow.profile"
	CapabilityRuntimeDispatch  Capability = "runtime.dispatch"
)

var (
	ErrUntrusted              = errors.New("configuration capability is not trusted")
	ErrInvalidDigest          = errors.New("invalid configuration digest")
	errNoChange               = errors.New("trust receipt mutation made no change")
	digestPattern             = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	filesystemIdentityPattern = regexp.MustCompile(`^(?:unix|windows):[0-9a-f]+:[0-9a-f]+$`)
)

// Subject binds approval to resolved canonical context and one config file's
// stable Git-root-relative scope. ConfigScope may be empty for callers that
// approve a context-wide synthetic digest.
type Subject struct {
	ProjectID         research.UUID `json:"project_id"`
	SourceID          research.ID   `json:"source_id,omitempty"`
	GitCommonDir      string        `json:"git_common_dir"`
	GitCommonIdentity string        `json:"git_common_identity,omitempty"`
	ConfigScope       string        `json:"config_scope,omitempty"`
}

// Receipt stores no commands, environment values, config bytes, or locator
// hints. Exact digest and capability membership are the complete approval.
type Receipt struct {
	Subject
	ConfigDigest string       `json:"config_digest"`
	Capabilities []Capability `json:"capabilities"`
	ApprovedAt   time.Time    `json:"approved_at"`
}

type receiptFile struct {
	Schema   string    `json:"schema"`
	Receipts []Receipt `json:"receipts"`
}

type receiptJSON struct {
	ProjectID         string       `json:"project_id"`
	SourceID          string       `json:"source_id,omitempty"`
	GitCommonDir      string       `json:"git_common_dir"`
	GitCommonIdentity string       `json:"git_common_identity,omitempty"`
	ConfigScope       string       `json:"config_scope,omitempty"`
	ConfigDigest      string       `json:"config_digest"`
	Capabilities      []Capability `json:"capabilities"`
	ApprovedAt        time.Time    `json:"approved_at"`
}

// MarshalJSON encodes an absent Source as an omitted string rather than asking
// encoding/json to infer zero-ness from research.ID's custom marshaler.
func (receipt Receipt) MarshalJSON() ([]byte, error) {
	return json.Marshal(receiptJSON{
		ProjectID: receipt.ProjectID.String(), SourceID: receipt.SourceID.String(),
		GitCommonDir: receipt.GitCommonDir, GitCommonIdentity: receipt.GitCommonIdentity, ConfigScope: receipt.ConfigScope,
		ConfigDigest: receipt.ConfigDigest, Capabilities: receipt.Capabilities, ApprovedAt: receipt.ApprovedAt,
	})
}

// UnmarshalJSON retains closed-field decoding for each receipt.
func (receipt *Receipt) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire receiptJSON
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trust receipt contains trailing JSON")
		}
		return err
	}
	projectID, err := research.ParseUUID(wire.ProjectID)
	if err != nil {
		return fmt.Errorf("parse trust receipt Project UUID: %w", err)
	}
	var sourceID research.ID
	if wire.SourceID != "" {
		sourceID, err = research.ParseIDForKind(wire.SourceID, research.KindSource)
		if err != nil {
			return fmt.Errorf("parse trust receipt Source ID: %w", err)
		}
	}
	*receipt = Receipt{
		Subject: Subject{
			ProjectID: projectID, SourceID: sourceID, GitCommonDir: wire.GitCommonDir,
			GitCommonIdentity: wire.GitCommonIdentity, ConfigScope: wire.ConfigScope,
		},
		ConfigDigest: wire.ConfigDigest, Capabilities: append([]Capability(nil), wire.Capabilities...), ApprovedAt: wire.ApprovedAt,
	}
	return nil
}

// Query is shared by inspection and approval.
type Query struct {
	Subject      Subject
	ConfigDigest string
	Capabilities []Capability
}

// Inspection is a non-mutating exact-match result.
type Inspection struct {
	Trusted bool
	Missing []Capability
	Matched []Receipt
}

// Checker is an immutable exact-receipt lookup surface.
type Checker interface {
	Check(context.Context, Subject, string, Capability) (bool, error)
}

// ReceiptSnapshot answers any number of checks against one validated immutable
// receipt read. It avoids reopening and reparsing the private database per field.
type ReceiptSnapshot struct {
	receipts []Receipt
}

// Snapshot reads and validates the receipt database once for one higher-level
// config operation.
func (store *Store) Snapshot(ctx context.Context) (Checker, error) {
	receipts, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	return &ReceiptSnapshot{receipts: receipts}, nil
}

func (snapshot *ReceiptSnapshot) Check(ctx context.Context, subject Subject, digest string, capability Capability) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	inspection, err := inspectReceipts(snapshot.receipts, Query{Subject: subject, ConfigDigest: digest, Capabilities: []Capability{capability}})
	return inspection.Trusted, err
}

// RevokeRequest removes all or selected capabilities from matching receipts.
// An empty ConfigDigest matches every digest for Subject; an empty capability
// list removes each matching receipt in full.
type RevokeRequest struct {
	Subject      Subject
	ConfigDigest string
	Capabilities []Capability
}

// PathResolver supplies the complete trust/v1.json path.
type PathResolver func() (string, error)

// Store owns the private receipt database.
type Store struct {
	path       PathResolver
	clock      func() time.Time
	atomicHook record.AtomicHook
	mu         sync.Mutex
}

// Option injects trust-store seams.
type Option func(*Store)

func WithPath(path string) Option {
	return func(store *Store) { store.path = func() (string, error) { return path, nil } }
}

func WithPathResolver(resolve PathResolver) Option {
	return func(store *Store) { store.path = resolve }
}

func WithClock(clock func() time.Time) Option {
	return func(store *Store) { store.clock = clock }
}

func WithAtomicHook(hook record.AtomicHook) Option {
	return func(store *Store) { store.atomicHook = hook }
}

// NewStore constructs a trust store at the XDG state path by default.
func NewStore(options ...Option) *Store {
	store := &Store{path: DefaultPath, clock: time.Now}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	if store.path == nil {
		store.path = DefaultPath
	}
	if store.clock == nil {
		store.clock = time.Now
	}
	return store
}

// DefaultPath returns $XDG_STATE_HOME/exp/trust/v1.json.
func DefaultPath() (string, error) {
	home, err := localstate.StateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "exp", "trust", "v1.json"), nil
}

func (store *Store) Path() (string, error) {
	if store == nil || store.path == nil {
		return "", errors.New("trust store path is not configured")
	}
	value, err := store.path()
	if err != nil {
		return "", fmt.Errorf("resolve trust store path: %w", err)
	}
	canonical, err := localstate.CanonicalPath(value)
	if err != nil {
		return "", fmt.Errorf("resolve trust store path: %w", err)
	}
	return canonical, nil
}

// Inspect reports whether every requested capability has an exact receipt. It
// does not create directories, lock files, or receipt state.
func (store *Store) Inspect(ctx context.Context, query Query) (Inspection, error) {
	receipts, err := store.List(ctx)
	if err != nil {
		return Inspection{}, err
	}
	return inspectReceipts(receipts, query)
}

func inspectReceipts(receipts []Receipt, query Query) (Inspection, error) {
	normalized, err := normalizeQuery(query, true)
	if err != nil {
		return Inspection{}, err
	}
	found := make(map[Capability]struct{})
	matched := make([]Receipt, 0)
	for _, receipt := range receipts {
		if receipt.ConfigDigest != normalized.ConfigDigest || !sameSubject(receipt.Subject, normalized.Subject) {
			continue
		}
		matched = append(matched, cloneReceipt(receipt))
		for _, capability := range receipt.Capabilities {
			found[capability] = struct{}{}
		}
	}
	missing := make([]Capability, 0)
	for _, capability := range normalized.Capabilities {
		if _, ok := found[capability]; !ok {
			missing = append(missing, capability)
		}
	}
	return Inspection{Trusted: len(missing) == 0, Missing: missing, Matched: matched}, nil
}

// Check is a concise exact-match convenience for config loaders.
func (store *Store) Check(ctx context.Context, subject Subject, digest string, capability Capability) (bool, error) {
	inspection, err := store.Inspect(ctx, Query{Subject: subject, ConfigDigest: digest, Capabilities: []Capability{capability}})
	return inspection.Trusted, err
}

// Approve replaces prior approvals for the same config scope and overlapping
// capabilities, preventing content reversion from reviving an older receipt.
func (store *Store) Approve(ctx context.Context, query Query) (Receipt, error) {
	normalized, err := normalizeQuery(query, true)
	if err != nil {
		return Receipt{}, err
	}
	approved := Receipt{
		Subject:      normalized.Subject,
		ConfigDigest: normalized.ConfigDigest,
		Capabilities: append([]Capability{}, normalized.Capabilities...),
		ApprovedAt:   store.now(),
	}
	var result Receipt
	err = store.update(ctx, func(receipts *[]Receipt) error {
		requested := capabilitySet(approved.Capabilities)
		kept := make([]Receipt, 0, len(*receipts)+1)
		mergedCapabilities := append([]Capability{}, approved.Capabilities...)
		for _, existing := range *receipts {
			if !sameLogicalSubject(existing.Subject, approved.Subject) {
				kept = append(kept, existing)
				continue
			}
			if !sameSubject(existing.Subject, approved.Subject) {
				// A repository replacement at the same logical path invalidates every
				// approval from the prior filesystem identity; never merge capabilities
				// across identities even when the config digest happens to match.
				continue
			}
			if existing.ConfigDigest == approved.ConfigDigest {
				mergedCapabilities = append(mergedCapabilities, existing.Capabilities...)
				continue
			}
			remaining := make([]Capability, 0, len(existing.Capabilities))
			for _, capability := range existing.Capabilities {
				if _, replace := requested[capability]; !replace {
					remaining = append(remaining, capability)
				}
			}
			if len(remaining) > 0 {
				existing.Capabilities = remaining
				kept = append(kept, existing)
			}
		}
		approved.Capabilities, _ = normalizeCapabilities(mergedCapabilities, true)
		kept = append(kept, approved)
		*receipts = kept
		result = cloneReceipt(approved)
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

// Revoke removes selected capability approvals and reports the number of
// receipts changed. It accepts stale-but-lexically-valid Git-common paths so a
// moved or deleted clone never makes its old approval impossible to remove.
func (store *Store) Revoke(ctx context.Context, request RevokeRequest) (int, error) {
	subject, err := normalizeSubject(request.Subject, false)
	if err != nil {
		return 0, err
	}
	if request.ConfigDigest != "" && !digestPattern.MatchString(request.ConfigDigest) {
		return 0, fmt.Errorf("%q: %w", request.ConfigDigest, ErrInvalidDigest)
	}
	capabilities, err := normalizeCapabilities(request.Capabilities, false)
	if err != nil {
		return 0, err
	}
	remove := capabilitySet(capabilities)
	changed := 0
	err = store.update(ctx, func(receipts *[]Receipt) error {
		kept := make([]Receipt, 0, len(*receipts))
		for _, receipt := range *receipts {
			if !sameLogicalSubject(receipt.Subject, subject) || request.ConfigDigest != "" && receipt.ConfigDigest != request.ConfigDigest {
				kept = append(kept, receipt)
				continue
			}
			if len(capabilities) == 0 {
				changed++
				continue
			}
			remaining := make([]Capability, 0, len(receipt.Capabilities))
			for _, capability := range receipt.Capabilities {
				if _, found := remove[capability]; !found {
					remaining = append(remaining, capability)
				}
			}
			if len(remaining) != len(receipt.Capabilities) {
				changed++
			}
			if len(remaining) > 0 {
				receipt.Capabilities = remaining
				kept = append(kept, receipt)
			}
		}
		*receipts = kept
		if changed == 0 {
			return errNoChange
		}
		return nil
	})
	return changed, err
}

// RevokeAll is a context-wide convenience.
func (store *Store) RevokeAll(ctx context.Context, subject Subject) (bool, error) {
	count, err := store.Revoke(ctx, RevokeRequest{Subject: subject})
	return count > 0, err
}

// List returns a sorted, validated receipt snapshot without mutating state.
func (store *Store) List(ctx context.Context) ([]Receipt, error) {
	path, err := store.Path()
	if err != nil {
		return nil, err
	}
	snapshot, err := localstate.Read(ctx, path, MaxStoreBytes)
	if err != nil {
		return nil, fmt.Errorf("read trust receipts: %w", err)
	}
	receipts, err := decodeReceipts(snapshot)
	if err != nil {
		return nil, err
	}
	out := make([]Receipt, len(receipts))
	for index := range receipts {
		out[index] = cloneReceipt(receipts[index])
	}
	return out, nil
}

func (store *Store) update(ctx context.Context, mutate func(*[]Receipt) error) error {
	if mutate == nil {
		return errors.New("trust receipt mutation is nil")
	}
	path, err := store.Path()
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return localstate.WithLockedFile(ctx, path, func(root *os.Root, name string) error {
		previous, err := localstate.ReadRoot(ctx, root, name, MaxStoreBytes)
		if err != nil {
			return fmt.Errorf("read trust receipts: %w", err)
		}
		receipts, err := decodeReceipts(previous)
		if err != nil {
			return err
		}
		if err := mutate(&receipts); err != nil {
			if errors.Is(err, errNoChange) {
				return nil
			}
			return err
		}
		normalizeReceipts(receipts)
		if err := validateReceipts(receipts); err != nil {
			return fmt.Errorf("validate trust receipts: %w", err)
		}
		data, err := json.MarshalIndent(receiptFile{Schema: Schema, Receipts: receipts}, "", "  ")
		if err != nil {
			return fmt.Errorf("encode trust receipts: %w", err)
		}
		data = append(data, '\n')
		if int64(len(data)) > MaxStoreBytes {
			return errors.New("trust receipt store exceeds byte limit")
		}
		if previous != nil && bytes.Equal(previous.Data, data) {
			return nil
		}
		return localstate.WriteRoot(root, name, data, previous, store.atomicHook)
	})
}

func decodeReceipts(snapshot *localstate.Snapshot) ([]Receipt, error) {
	if snapshot == nil {
		return []Receipt{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(snapshot.Data))
	decoder.DisallowUnknownFields()
	var file receiptFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode trust receipts: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return nil, fmt.Errorf("decode trust receipts: %w", err)
	}
	if file.Schema != Schema {
		return nil, fmt.Errorf("trust receipt schema must be %q", Schema)
	}
	if file.Receipts == nil {
		return nil, errors.New("trust receipts array must be present")
	}
	if err := validateReceipts(file.Receipts); err != nil {
		return nil, fmt.Errorf("validate trust receipts: %w", err)
	}
	normalizeReceipts(file.Receipts)
	return file.Receipts, nil
}

func validateReceipts(receipts []Receipt) error {
	seen := make(map[string]struct{}, len(receipts))
	for index, receipt := range receipts {
		subject, err := normalizeSubject(receipt.Subject, false)
		if err != nil {
			return fmt.Errorf("receipt %d has invalid subject: %w", index, err)
		}
		if !sameSubject(subject, receipt.Subject) {
			return fmt.Errorf("receipt %d subject is not normalized", index)
		}
		if !digestPattern.MatchString(receipt.ConfigDigest) {
			return fmt.Errorf("receipt %d config digest: %w", index, ErrInvalidDigest)
		}
		capabilities, err := normalizeCapabilities(receipt.Capabilities, true)
		if err != nil {
			return fmt.Errorf("receipt %d: %w", index, err)
		}
		if !equalCapabilities(capabilities, receipt.Capabilities) {
			return fmt.Errorf("receipt %d capabilities are not sorted and unique", index)
		}
		if !validUTC(receipt.ApprovedAt) {
			return fmt.Errorf("receipt %d has invalid approved_at", index)
		}
		key := subjectKey(receipt.Subject) + "\x00" + receipt.ConfigDigest
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate receipt %d", index)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func normalizeQuery(query Query, requireExisting bool) (Query, error) {
	subject, err := normalizeSubject(query.Subject, requireExisting)
	if err != nil {
		return Query{}, err
	}
	if !digestPattern.MatchString(query.ConfigDigest) {
		return Query{}, fmt.Errorf("%q: %w", query.ConfigDigest, ErrInvalidDigest)
	}
	capabilities, err := normalizeCapabilities(query.Capabilities, true)
	if err != nil {
		return Query{}, err
	}
	return Query{Subject: subject, ConfigDigest: query.ConfigDigest, Capabilities: capabilities}, nil
}

func normalizeSubject(subject Subject, requireExisting bool) (Subject, error) {
	if subject.ProjectID.IsZero() {
		return Subject{}, errors.New("trust subject requires a Project UUID")
	}
	if !subject.SourceID.IsZero() && subject.SourceID.Kind() != research.KindSource {
		return Subject{}, errors.New("trust subject Source ID has the wrong kind")
	}
	if err := validateAbsolutePath(subject.GitCommonDir); err != nil {
		return Subject{}, fmt.Errorf("trust subject Git common dir: %w", err)
	}
	canonical, err := pathx.Canonical(subject.GitCommonDir)
	if err != nil {
		return Subject{}, fmt.Errorf("canonicalize trust subject Git common dir: %w", err)
	}
	if subject.GitCommonIdentity != "" && !filesystemIdentityPattern.MatchString(subject.GitCommonIdentity) {
		return Subject{}, errors.New("trust subject Git common identity is invalid")
	}
	if requireExisting {
		info, statErr := os.Lstat(canonical)
		if statErr != nil {
			return Subject{}, fmt.Errorf("inspect trust subject Git common dir: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return Subject{}, errors.New("trust subject Git common dir is not a real directory")
		}
		identity, identityErr := pathx.DirectoryFilesystemIdentity(canonical)
		if identityErr != nil {
			return Subject{}, fmt.Errorf("identify trust subject Git common dir: %w", identityErr)
		}
		if subject.GitCommonIdentity != "" && subject.GitCommonIdentity != identity {
			return Subject{}, errors.New("trust subject Git common filesystem identity changed")
		}
		subject.GitCommonIdentity = identity
	}
	subject.GitCommonDir = canonical
	if subject.ConfigScope != "" {
		if err := pathx.ValidateRelativePOSIX(subject.ConfigScope, true); err != nil {
			return Subject{}, fmt.Errorf("trust config scope: %w", err)
		}
	}
	return subject, nil
}

func normalizeCapabilities(input []Capability, require bool) ([]Capability, error) {
	if require && len(input) == 0 {
		return nil, errors.New("at least one trust capability is required")
	}
	seen := map[Capability]struct{}{}
	out := make([]Capability, 0, len(input))
	for _, capability := range input {
		if !capability.Valid() {
			return nil, fmt.Errorf("unsupported trust capability %q", capability)
		}
		if _, duplicate := seen[capability]; duplicate {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	sort.Slice(out, func(left, right int) bool { return out[left] < out[right] })
	if out == nil {
		out = []Capability{}
	}
	return out, nil
}

// Valid reports whether capability is part of the v1 allowlist.
func (capability Capability) Valid() bool {
	switch capability {
	case CapabilitySourceSelection, CapabilityWorkspaceBackend, CapabilityAgentProfile, CapabilityMLflowProfile, CapabilityRuntimeDispatch:
		return true
	default:
		return false
	}
}

func normalizeReceipts(receipts []Receipt) {
	for index := range receipts {
		receipts[index].ApprovedAt = receipts[index].ApprovedAt.UTC()
		receipts[index].Capabilities, _ = normalizeCapabilities(receipts[index].Capabilities, false)
	}
	sort.Slice(receipts, func(left, right int) bool {
		leftKey := subjectKey(receipts[left].Subject) + "\x00" + receipts[left].ConfigDigest
		rightKey := subjectKey(receipts[right].Subject) + "\x00" + receipts[right].ConfigDigest
		return leftKey < rightKey
	})
}

func sameSubject(left, right Subject) bool {
	return sameLogicalSubject(left, right) && left.GitCommonIdentity == right.GitCommonIdentity
}

func sameLogicalSubject(left, right Subject) bool {
	return left.ProjectID == right.ProjectID && left.SourceID == right.SourceID && left.GitCommonDir == right.GitCommonDir && left.ConfigScope == right.ConfigScope
}

func subjectKey(subject Subject) string {
	return subject.ProjectID.String() + "\x00" + subject.SourceID.String() + "\x00" + subject.GitCommonDir + "\x00" + subject.GitCommonIdentity + "\x00" + subject.ConfigScope
}

func capabilitySet(capabilities []Capability) map[Capability]struct{} {
	set := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		set[capability] = struct{}{}
	}
	return set
}

func equalCapabilities(left, right []Capability) bool {
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

func cloneReceipt(receipt Receipt) Receipt {
	receipt.Capabilities = append([]Capability{}, receipt.Capabilities...)
	return receipt
}

func validUTC(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0 && value.Equal(value.UTC())
}

func validateAbsolutePath(value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return errors.New("path must be clean, absolute, and valid UTF-8")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return errors.New("path contains control characters")
		}
	}
	return nil
}

func (store *Store) now() time.Time {
	if store != nil && store.clock != nil {
		return store.clock().UTC()
	}
	return time.Now().UTC()
}
