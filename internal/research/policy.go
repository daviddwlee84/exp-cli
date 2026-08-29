package research

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/safex"
)

var (
	ErrUnsafePath = errors.New("unsafe committed path")
	ErrUnsafeURI  = errors.New("unsafe committed URI")
	ErrUnsafeText = errors.New("unsafe commit text")
)

// PolicyError carries the stable diagnostic code for path/URI/privacy failures.
type PolicyError struct {
	Code    string
	Message string
	Err     error
}

func (e *PolicyError) Error() string { return e.Message }
func (e *PolicyError) Unwrap() error { return e.Err }

var (
	tagPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	slugPattern      = tagPattern
	metricPattern    = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	namespacePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	gitCommitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

	uriCandidatePattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"']+`)
)

func validSlug(value string) bool      { return slugPattern.MatchString(value) }
func validMetric(value string) bool    { return metricPattern.MatchString(value) }
func validNamespace(value string) bool { return namespacePattern.MatchString(value) }
func validDigest(value string) bool    { return digestPattern.MatchString(value) }

// ValidateCommittedPath validates the platform-independent lexical contract.
// Physical containment and symlink checks are performed by pathx at I/O time.
func ValidateCommittedPath(value string, allowDot bool) error {
	failure := func(code, message string) error {
		return &PolicyError{Code: code, Message: message, Err: ErrUnsafePath}
	}
	switch {
	case value == "":
		return failure("path.empty", "path is empty")
	case !utf8.ValidString(value):
		return failure("path.invalid_utf8", "path is not valid UTF-8")
	case strings.ContainsRune(value, '\x00'):
		return failure("path.nul", "path contains NUL")
	case hasAnyControl(value):
		return failure("path.invalid_text", "path contains control characters")
	case strings.Contains(value, "\\"):
		return failure("path.not_relative", "path uses a backslash or UNC separator")
	case strings.HasPrefix(value, "/") || driveQualified(value):
		return failure("path.not_relative", "path is absolute or drive-qualified")
	case strings.HasPrefix(strings.ToLower(value), "file:"):
		return failure("path.not_relative", "file: paths are not repository-relative")
	case strings.HasPrefix(value, "~"):
		return failure("path.home", "home-directory shorthand is forbidden")
	case value == ".":
		if allowDot {
			return nil
		}
		return failure("path.root", "the repository root is not allowed here")
	case strings.Contains(value, "//"):
		return failure("path.unclean", "path contains an empty segment")
	}
	components := strings.Split(value, "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			if component == ".." {
				return failure("path.traversal", "path contains parent traversal")
			}
			return failure("path.unclean", "path contains an empty or current-directory segment")
		}
	}
	if cleaned := path.Clean(value); cleaned != value || cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return failure("path.unclean", "path is not clean repository-relative POSIX syntax")
	}
	return nil
}

func driveQualified(value string) bool {
	return len(value) >= 2 && value[1] == ':' && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z'))
}

// ValidateCommittedURI rejects credential-bearing or host-path URI forms.
func ValidateCommittedURI(value string) error {
	failure := func(code, message string) error {
		return &PolicyError{Code: code, Message: message, Err: ErrUnsafeURI}
	}
	if value == "" {
		return nil
	}
	if !utf8.ValidString(value) || hasAnyControl(value) || value != strings.TrimSpace(value) {
		return failure("uri.invalid", "URI contains invalid text")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return failure("uri.invalid", "URI must be structurally parseable and include a scheme")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery {
		return failure("uri.credentials", "URI userinfo and query components are forbidden")
	}
	if strings.EqualFold(parsed.Scheme, "file") {
		return failure("uri.file", "file: URIs are forbidden in canonical records")
	}
	if (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Host == "" {
		return failure("uri.invalid", "HTTP URI has no host")
	}
	if containsCredentialMaterial(parsed.Path) || containsCredentialMaterial(parsed.Opaque) || containsCredentialMaterial(parsed.Fragment) {
		return failure("uri.credentials", "URI path, opaque data, or fragment contains credential material")
	}
	return nil
}

func policyCode(err error, fallback string) string {
	var policy *PolicyError
	if errors.As(err, &policy) && policy.Code != "" {
		return policy.Code
	}
	return fallback
}

func validUTC(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}

func nonempty(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && strings.TrimSpace(value) != ""
}

func singleLine(value string) bool {
	return nonempty(value) && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n")
}

func finite(value float64) bool { return !math.IsInf(value, 0) && !math.IsNaN(value) }

func hasMigrationExtension(record Record) bool {
	if record == nil {
		return false
	}
	_, found := record.GetExtensions()[MigrationExtension]
	return found
}

func validateExtensions(extensions Extensions, collector *issueCollector) {
	for namespace, table := range extensions {
		field := `extensions."` + namespace + `"`
		if !validNamespace(namespace) {
			collector.add("extension.namespace", field, "extension namespace %q is not lower-case reverse-DNS syntax", namespace)
		}
		if table == nil {
			collector.add("extension.table", field, "extension namespace must contain a table")
			continue
		}
		validateOpenValue(table, field, 0, collector)
	}
}

func validateOpenValue(value any, field string, depth int, collector *issueCollector) {
	if depth > 32 {
		collector.add("extension.depth", field, "extension data exceeds maximum nesting depth")
		return
	}
	if value == nil {
		collector.add("extension.value", field, "TOML extension values cannot be null")
		return
	}
	switch typed := value.(type) {
	case string:
		validateCredentialSensitiveString(typed, field, collector)
	case float32:
		if !finite(float64(typed)) {
			collector.add("extension.number", field, "extension number must be finite")
		}
	case float64:
		if !finite(typed) {
			collector.add("extension.number", field, "extension number must be finite")
		}
	case time.Time:
		// Extension timestamps are uninterpreted TOML values; core UTC policy does not apply.
	case map[string]any:
		for key, nested := range typed {
			child := field + "." + key
			if credentialKey(key) {
				collector.add("privacy.secret_field", child, "credential-bearing extension keys are forbidden")
			}
			validateCommitSafeString(key, child, collector)
			validateOpenValue(nested, child, depth+1, collector)
		}
	case []any:
		for index, nested := range typed {
			validateOpenValue(nested, fmt.Sprintf("%s[%d]", field, index), depth+1, collector)
		}
	default:
		reflected := reflect.ValueOf(value)
		switch reflected.Kind() {
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		case reflect.Slice, reflect.Array:
			for index := 0; index < reflected.Len(); index++ {
				validateOpenValue(reflected.Index(index).Interface(), fmt.Sprintf("%s[%d]", field, index), depth+1, collector)
			}
		case reflect.Map:
			if reflected.Type().Key().Kind() != reflect.String {
				collector.add("extension.value", field, "extension map keys must be strings")
				return
			}
			iterator := reflected.MapRange()
			for iterator.Next() {
				key := iterator.Key().String()
				child := field + "." + key
				if credentialKey(key) {
					collector.add("privacy.secret_field", child, "credential-bearing keys are forbidden")
				}
				validateCommitSafeString(key, child, collector)
				validateOpenValue(iterator.Value().Interface(), child, depth+1, collector)
			}
		case reflect.Struct:
			// BurntSushi TOML local date/time values are structs and round-trip through its encoder.
		default:
			collector.add("extension.value", field, "value of type %T is not representable by TOML", value)
		}
	}
}

func credentialKey(key string) bool { return safex.SensitiveName(key) }

// ValidateCommitSafeText rejects concrete credential syntax while allowing
// ordinary prose that merely discusses authentication, cookies, or tokens.
func ValidateCommitSafeText(value string) error {
	failure := func(code, message string) error {
		return &PolicyError{Code: code, Message: message, Err: ErrUnsafeText}
	}
	if !utf8.ValidString(value) || hasUnsafeTextControl(value) {
		return failure("privacy.invalid_text", "value is not safe UTF-8 text")
	}
	if safex.ContainsSecretText(value) || containsSensitiveCommandText(value) {
		return failure("privacy.secret", "credential-bearing material is forbidden")
	}
	for _, candidate := range uriCandidatePattern.FindAllString(value, -1) {
		candidate = strings.TrimRight(candidate, ".,;:!?)]}")
		if credentialBearingURI(candidate) {
			return failure("privacy.secret", "credential-bearing URI material is forbidden")
		}
	}
	return nil
}

func hasUnsafeTextControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return true
		}
	}
	return false
}

func hasAnyControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func containsSensitiveCommandText(value string) bool {
	return len(safex.SensitiveArgvIndexes(strings.Fields(value))) > 0
}

func credentialBearingURI(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	if parsed.User != nil || containsCredentialMaterial(parsed.Path) || containsCredentialMaterial(parsed.Opaque) || containsCredentialMaterial(parsed.Fragment) {
		return true
	}
	if parsed.RawQuery == "" && !parsed.ForceQuery {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return containsCredentialMaterial(parsed.RawQuery)
	}
	for key, values := range query {
		if credentialKey(key) || strings.EqualFold(strings.TrimSpace(key), "key") {
			return true
		}
		for _, value := range values {
			if containsCredentialMaterial(value) {
				return true
			}
		}
	}
	return false
}

func containsCredentialMaterial(value string) bool {
	return safex.ContainsSecretText(safex.DecodePercentEncoding(value))
}

func validateCredentialSensitiveString(value, field string, collector *issueCollector) {
	if containsCredentialMaterial(value) {
		collector.add("privacy.secret", field, "credential-bearing material is forbidden")
		return
	}
	validateCommitSafeString(value, field, collector)
}

func validateCommitSafeString(value, field string, collector *issueCollector) {
	if err := ValidateCommitSafeText(value); err != nil {
		collector.add(policyCode(err, "privacy.secret"), field, "%v", err)
	}
}
