package research

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidID        = errors.New("invalid canonical ID")
	ErrWrongIDKind      = errors.New("canonical ID has the wrong kind")
	ErrWrongUUIDVersion = errors.New("UUID has an unsupported version")
	ErrInvalidAlias     = errors.New("invalid legacy alias")
)

// ID is a canonical typed UUID reference. It is comparable and safe as a map key.
type ID struct {
	kind Kind
	uuid uuid.UUID
}

// NewID constructs a native UUIDv7 typed ID.
func NewID(kind Kind, value uuid.UUID) (ID, error) {
	if err := validateIDKind(kind); err != nil {
		return ID{}, err
	}
	if err := validateUUID(value, uuid.Version(7)); err != nil {
		return ID{}, err
	}
	return ID{kind: kind, uuid: value}, nil
}

// NewImportedID constructss the UUIDv5 form reserved for deterministic migration.
func NewImportedID(kind Kind, value uuid.UUID) (ID, error) {
	if err := validateIDKind(kind); err != nil {
		return ID{}, err
	}
	if err := validateUUID(value, uuid.Version(5)); err != nil {
		return ID{}, err
	}
	return ID{kind: kind, uuid: value}, nil
}

func validateIDKind(kind Kind) error {
	if kind == KindProject || kind == KindUnknown || !kind.Valid() {
		return fmt.Errorf("kind %q cannot have a typed ID: %w", kind, ErrInvalidID)
	}
	return nil
}

func validateUUID(value uuid.UUID, versions ...uuid.Version) error {
	if value == uuid.Nil {
		return fmt.Errorf("nil UUID: %w", ErrInvalidID)
	}
	if value.Variant() != uuid.RFC4122 {
		return fmt.Errorf("UUID %s does not use the RFC 9562 variant: %w", value, ErrInvalidID)
	}
	for _, version := range versions {
		if value.Version() == version {
			return nil
		}
	}
	return fmt.Errorf("UUID %s is version %d: %w", value, value.Version(), ErrWrongUUIDVersion)
}

// ParseID parses the canonical lower-case typed form. UUIDv7 and the migration-only
// UUIDv5 form are recognized; record validation decides whether v5 is authorized.
func ParseID(value string) (ID, error) {
	for _, kind := range RecordKinds {
		if kind == KindProject {
			continue
		}
		prefix, _ := kind.IDPrefix()
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		raw := strings.TrimPrefix(value, prefix)
		parsed, err := uuid.Parse(raw)
		if err != nil || parsed.String() != raw {
			return ID{}, fmt.Errorf("%q: %w", value, ErrInvalidID)
		}
		if err := validateUUID(parsed, uuid.Version(7), uuid.Version(5)); err != nil {
			return ID{}, fmt.Errorf("%q: %w", value, err)
		}
		return ID{kind: kind, uuid: parsed}, nil
	}
	return ID{}, fmt.Errorf("%q has no known typed prefix: %w", value, ErrInvalidID)
}

// ParseIDForKind parses value and checks its persisted kind prefix.
func ParseIDForKind(value string, expected Kind) (ID, error) {
	id, err := ParseID(value)
	if err != nil {
		return ID{}, err
	}
	if id.kind != expected {
		return ID{}, fmt.Errorf("%s is %s, expected %s: %w", value, id.kind, expected, ErrWrongIDKind)
	}
	return id, nil
}

func (id ID) IsZero() bool     { return id.kind == KindUnknown || id.uuid == uuid.Nil }
func (id ID) Kind() Kind       { return id.kind }
func (id ID) UUID() uuid.UUID  { return id.uuid }
func (id ID) UUIDVersion() int { return int(id.uuid.Version()) }
func (id ID) IsNative() bool {
	return !id.IsZero() && id.uuid.Variant() == uuid.RFC4122 && id.uuid.Version() == uuid.Version(7)
}
func (id ID) IsImported() bool {
	return !id.IsZero() && id.uuid.Variant() == uuid.RFC4122 && id.uuid.Version() == uuid.Version(5)
}
func (id ID) UUIDHex() string { return strings.ReplaceAll(id.uuid.String(), "-", "") }

func (id ID) String() string {
	if id.IsZero() {
		return ""
	}
	prefix, err := id.kind.IDPrefix()
	if err != nil {
		return ""
	}
	return prefix + id.uuid.String()
}

func (id ID) MarshalText() ([]byte, error) {
	if id.IsZero() {
		return nil, fmt.Errorf("marshal zero ID: %w", ErrInvalidID)
	}
	return []byte(id.String()), nil
}

func (id *ID) UnmarshalText(text []byte) error {
	parsed, err := ParseID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func (id ID) MarshalJSON() ([]byte, error) {
	if id.IsZero() {
		return []byte(`""`), nil
	}
	return json.Marshal(id.String())
}

func (id *ID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return id.UnmarshalText([]byte(value))
}

// UUID is a bare canonical UUID used for the project namespace root.
type UUID struct{ uuid.UUID }

func NewProjectUUID(value uuid.UUID) (UUID, error) {
	if err := validateUUID(value, uuid.Version(7)); err != nil {
		return UUID{}, err
	}
	return UUID{UUID: value}, nil
}

func NewImportedProjectUUID(value uuid.UUID) (UUID, error) {
	if err := validateUUID(value, uuid.Version(5)); err != nil {
		return UUID{}, err
	}
	return UUID{UUID: value}, nil
}

func ParseUUID(value string) (UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return UUID{}, fmt.Errorf("%q: %w", value, ErrInvalidID)
	}
	if err := validateUUID(parsed, uuid.Version(7), uuid.Version(5)); err != nil {
		return UUID{}, err
	}
	return UUID{UUID: parsed}, nil
}

func (id UUID) IsZero() bool { return id.UUID == uuid.Nil }
func (id UUID) IsNative() bool {
	return !id.IsZero() && id.Variant() == uuid.RFC4122 && id.Version() == uuid.Version(7)
}
func (id UUID) IsImported() bool {
	return !id.IsZero() && id.Variant() == uuid.RFC4122 && id.Version() == uuid.Version(5)
}
func (id UUID) String() string {
	if id.IsZero() {
		return ""
	}
	return id.UUID.String()
}
func (id UUID) MarshalText() ([]byte, error) {
	if id.IsZero() {
		return nil, fmt.Errorf("marshal zero project UUID: %w", ErrInvalidID)
	}
	return []byte(id.String()), nil
}
func (id *UUID) UnmarshalText(text []byte) error {
	parsed, err := ParseUUID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
func (id UUID) MarshalJSON() ([]byte, error) { return json.Marshal(id.String()) }
func (id *UUID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return id.UnmarshalText([]byte(value))
}

// UUIDGenerator is injected into creation services. The supplied timestamp is
// the same injected clock value used for created_at.
type UUIDGenerator func(at time.Time) (uuid.UUID, error)

// DefaultUUIDGenerator generates an RFC 9562 UUIDv7 using crypto/rand.
func DefaultUUIDGenerator(at time.Time) (uuid.UUID, error) {
	return NewUUIDv7(at, rand.Reader)
}

// NewUUIDv7 constructs an RFC 9562 UUIDv7 from an explicit clock and entropy
// source, making creation deterministic in tests without weakening production IDs.
func NewUUIDv7(at time.Time, entropy io.Reader) (uuid.UUID, error) {
	if entropy == nil {
		return uuid.Nil, errors.New("UUIDv7 entropy reader is nil")
	}
	milliseconds := at.UTC().UnixMilli()
	if milliseconds < 0 || uint64(milliseconds) > uint64(1)<<48-1 {
		return uuid.Nil, fmt.Errorf("UUIDv7 timestamp %s is outside the 48-bit Unix-millisecond range", at)
	}
	var value uuid.UUID
	if _, err := io.ReadFull(entropy, value[6:]); err != nil {
		return uuid.Nil, fmt.Errorf("read UUIDv7 entropy: %w", err)
	}
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], uint64(milliseconds))
	copy(value[0:6], timestamp[2:])
	value[6] = value[6]&0x0f | 0x70
	value[8] = value[8]&0x3f | 0x80
	return value, nil
}

var (
	experimentAliasPattern = regexp.MustCompile(`^#[0-9]{3,}$`)
	findingAliasPattern    = regexp.MustCompile(`^F-[0-9]{3,}$`)
)

// LegacyAlias carries the exact type-aware harness-v0 alias.
type LegacyAlias struct {
	Kind  Kind
	Value string
}

func ParseLegacyAlias(value string) (LegacyAlias, error) {
	switch {
	case experimentAliasPattern.MatchString(value):
		return LegacyAlias{Kind: KindExperiment, Value: value}, nil
	case findingAliasPattern.MatchString(value):
		return LegacyAlias{Kind: KindFinding, Value: value}, nil
	default:
		return LegacyAlias{}, fmt.Errorf("%q: %w", value, ErrInvalidAlias)
	}
}
