package research

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewUUIDv7UsesInjectedClockAndRFCBits(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 34, 56, 789_000_000, time.UTC)
	value, err := NewUUIDv7(now, bytes.NewReader(bytes.Repeat([]byte{0xab}, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if value.Version() != uuid.Version(7) {
		t.Fatalf("version = %d", value.Version())
	}
	if value.Variant() != uuid.RFC4122 {
		t.Fatalf("variant = %v", value.Variant())
	}
	var encoded [8]byte
	copy(encoded[2:], value[:6])
	if milliseconds := binary.BigEndian.Uint64(encoded[:]); milliseconds != uint64(now.UnixMilli()) {
		t.Fatalf("timestamp milliseconds = %d, want %d", milliseconds, now.UnixMilli())
	}
	if value[6]&0xf0 != 0x70 || value[8]&0xc0 != 0x80 {
		t.Fatalf("version/variant bits are not canonical: %x", value)
	}
}

func TestParseIDRequiresCanonicalTypedV7OrMigrationV5(t *testing.T) {
	native := "plan_01a01e66-f8e0-7202-8000-000000000202"
	id, err := ParseID(native)
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind() != KindPlan || !id.IsNative() || id.String() != native {
		t.Fatalf("parsed ID = %#v", id)
	}
	imported := "fnd_74738ff5-5367-5958-9aee-98fffdcd1876"
	id, err = ParseID(imported)
	if err != nil || !id.IsImported() {
		t.Fatalf("parse imported ID = %v, %v", id, err)
	}
	for _, invalid := range []string{
		"PLAN_01a01e66-f8e0-7202-8000-000000000202",
		"plan_01A01E66-F8E0-7202-8000-000000000202",
		"plan_550e8400-e29b-41d4-a716-446655440000",
		"01a01e66-f8e0-7202-8000-000000000202",
	} {
		if _, err := ParseID(invalid); err == nil {
			t.Errorf("ParseID(%q) unexpectedly succeeded", invalid)
		}
	}
	if _, err := ParseIDForKind(native, KindFinding); !errors.Is(err, ErrWrongIDKind) {
		t.Fatalf("wrong-kind error = %v", err)
	}
}

func TestDisplayAndPrefixResolutionLengthenOnCollision(t *testing.T) {
	first := mustID(t, "plan_01a01e66-f8e0-7202-8000-000000000202")
	second := mustID(t, "plan_01a01e66-a8e0-7202-8000-000000000303")
	candidates := []ReferenceCandidate{{ID: first}, {ID: second}}
	code, err := DisplayCode(first, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if code != "P-01A01E66F" {
		t.Fatalf("display code = %q", code)
	}
	if _, err := Resolve("P-01A01E66", KindPlan, candidates); !errors.Is(err, ErrAmbiguousReference) {
		t.Fatalf("short collision = %v", err)
	}
	resolved, err := Resolve(code, KindPlan, candidates)
	if err != nil || resolved != first {
		t.Fatalf("Resolve(%q) = %s, %v", code, resolved, err)
	}
	if _, err := Resolve("plan_01a01e66", KindPlan, candidates); !errors.Is(err, ErrAmbiguousReference) {
		t.Fatalf("typed short collision = %v", err)
	}
	resolved, err = Resolve("plan_01a01e66-f", KindPlan, candidates)
	if err != nil || resolved != first {
		t.Fatalf("typed extended resolution = %s, %v", resolved, err)
	}
}

func TestLegacyAliasesAreExactAndTypeAware(t *testing.T) {
	experiment := mustID(t, "exp_74738ff5-5367-5958-9aee-98fffdcd1876")
	finding := mustID(t, "fnd_d3f8be2b-b3cc-566b-85a0-1f72cd5f4062")
	candidates := []ReferenceCandidate{{ID: experiment, Aliases: []string{"#016"}}, {ID: finding, Aliases: []string{"F-039"}}}
	resolved, err := Resolve("#016", KindExperiment, candidates)
	if err != nil || resolved != experiment {
		t.Fatalf("experiment alias = %s, %v", resolved, err)
	}
	if _, err := Resolve("#16", KindExperiment, candidates); !errors.Is(err, ErrReferenceNotFound) {
		t.Fatalf("short alias error = %v", err)
	}
	if _, err := Resolve("F-039", KindExperiment, candidates); !errors.Is(err, ErrWrongIDKind) {
		t.Fatalf("type-aware alias error = %v", err)
	}
}

func mustID(t *testing.T, value string) ID {
	t.Helper()
	id, err := ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
