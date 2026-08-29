package execx

import "github.com/daviddwlee84/exp-cli/internal/safex"

// Redacted is the stable replacement for a structurally sensitive value.
const Redacted = safex.Redacted

const truncatedMarker = "[TRUNCATED]"

// Redactor is the shared cycle-free secret text and argv redactor.
type Redactor = safex.Redactor

// NewRedactor constructs a redactor for explicit secret values.
func NewRedactor(secrets ...string) Redactor { return safex.NewRedactor(secrets...) }

// BoundText returns valid UTF-8 no larger than maxBytes. Callers must redact
// before bounding; truncating raw secret text can expose a credential prefix.
func BoundText(value string, maxBytes int) (string, bool) {
	return safex.BoundText(value, maxBytes)
}

// SensitiveName reports whether a header, argument, or environment name is
// conventionally credential-bearing.
func SensitiveName(name string) bool { return safex.SensitiveName(name) }

func sensitiveArgvValues(argv []string, sensitiveIndexes ...int) []string {
	return safex.SensitiveArgvValues(argv, sensitiveIndexes...)
}
