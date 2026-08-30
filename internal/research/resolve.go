package research

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	ErrReferenceNotFound  = errors.New("reference not found")
	ErrAmbiguousReference = errors.New("ambiguous reference")
)

// ReferenceCandidate is the identity data needed for display and user-input
// resolution. It is deliberately distinct from the canonical Candidate record.
type ReferenceCandidate struct {
	ID      ID
	Aliases []string
}

var displayPattern = regexp.MustCompile(`(?i)^([IOQVBPERASNFCLTMD])-([0-9a-f]{8,32})$`)

// DisplayCode allocates the shortest same-kind code with at least eight UUID hex digits.
func DisplayCode(target ID, candidates []ReferenceCandidate) (string, error) {
	if target.IsZero() {
		return "", fmt.Errorf("display zero ID: %w", ErrInvalidID)
	}
	letter, err := target.Kind().DisplayLetter()
	if err != nil {
		return "", err
	}
	hex := strings.ToUpper(target.UUIDHex())
	for length := 8; length <= len(hex); length++ {
		prefix := hex[:length]
		unique := true
		for _, candidate := range candidates {
			if candidate.ID == target || candidate.ID.Kind() != target.Kind() || candidate.ID.IsZero() {
				continue
			}
			if strings.HasPrefix(strings.ToUpper(candidate.ID.UUIDHex()), prefix) {
				unique = false
				break
			}
		}
		if unique {
			return fmt.Sprintf("%c-%s", letter, prefix), nil
		}
	}
	return "", fmt.Errorf("duplicate identity %s: %w", target, ErrAmbiguousReference)
}

// Resolve accepts a full typed ID, an unambiguous typed prefix, a display code,
// or the exact type-aware legacy alias. expected may be KindUnknown when the
// reference syntax itself supplies the kind.
func Resolve(query string, expected Kind, candidates []ReferenceCandidate) (ID, error) {
	if query == "" {
		return ID{}, fmt.Errorf("empty reference: %w", ErrReferenceNotFound)
	}
	if id, err := ParseID(query); err == nil {
		if expected != KindUnknown && id.Kind() != expected {
			return ID{}, fmt.Errorf("%s is %s, expected %s: %w", query, id.Kind(), expected, ErrWrongIDKind)
		}
		for _, candidate := range candidates {
			if candidate.ID == id {
				return id, nil
			}
		}
		return ID{}, fmt.Errorf("%s: %w", query, ErrReferenceNotFound)
	}

	if alias, err := ParseLegacyAlias(query); err == nil {
		if expected != KindUnknown && alias.Kind != expected {
			return ID{}, fmt.Errorf("%s is a %s alias, expected %s: %w", query, alias.Kind, expected, ErrWrongIDKind)
		}
		return uniqueMatch(query, alias.Kind, candidates, func(candidate ReferenceCandidate) bool {
			for _, value := range candidate.Aliases {
				if value == query {
					return true
				}
			}
			return false
		})
	}

	if match := displayPattern.FindStringSubmatch(query); match != nil {
		kind, _ := KindForDisplayLetter(match[1][0])
		if expected != KindUnknown && kind != expected {
			return ID{}, fmt.Errorf("%s is a %s display code, expected %s: %w", query, kind, expected, ErrWrongIDKind)
		}
		prefix := strings.ToUpper(match[2])
		return uniqueMatch(query, kind, candidates, func(candidate ReferenceCandidate) bool {
			return strings.HasPrefix(strings.ToUpper(candidate.ID.UUIDHex()), prefix)
		})
	}

	if kind, uuidPrefix, ok := parseTypedPrefix(query); ok {
		if expected != KindUnknown && kind != expected {
			return ID{}, fmt.Errorf("%s is a %s prefix, expected %s: %w", query, kind, expected, ErrWrongIDKind)
		}
		return uniqueMatch(query, kind, candidates, func(candidate ReferenceCandidate) bool {
			return strings.HasPrefix(candidate.ID.String(), uuidPrefix)
		})
	}

	return ID{}, fmt.Errorf("%s: %w", query, ErrReferenceNotFound)
}

func parseTypedPrefix(query string) (Kind, string, bool) {
	for _, kind := range RecordKinds {
		prefix, prefixErr := kind.IDPrefix()
		if prefixErr != nil {
			continue
		}
		if !strings.HasPrefix(query, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(query, prefix)
		hexCount := 0
		for _, character := range remainder {
			switch {
			case character >= '0' && character <= '9', character >= 'a' && character <= 'f':
				hexCount++
			case character == '-':
			default:
				return KindUnknown, "", false
			}
		}
		if hexCount < 8 || len(remainder) > 36 {
			return KindUnknown, "", false
		}
		return kind, query, true
	}
	return KindUnknown, "", false
}

func uniqueMatch(query string, kind Kind, candidates []ReferenceCandidate, matches func(ReferenceCandidate) bool) (ID, error) {
	found := make([]ID, 0, 1)
	for _, candidate := range candidates {
		if candidate.ID.Kind() == kind && matches(candidate) {
			found = append(found, candidate.ID)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].String() < found[j].String() })
	switch len(found) {
	case 0:
		return ID{}, fmt.Errorf("%s: %w", query, ErrReferenceNotFound)
	case 1:
		return found[0], nil
	default:
		return ID{}, fmt.Errorf("%s matches %d records: %w", query, len(found), ErrAmbiguousReference)
	}
}
