package research

import (
	"errors"
	"fmt"
	"math"
	"net"
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

const (
	// MaxSourceKeyBytes bounds one canonical project-local Source key.
	MaxSourceKeyBytes = 64
	// MaxSourceSubdirBytes bounds one canonical Git-root-relative Source path.
	MaxSourceSubdirBytes = 4096
	// MaxSourceLocatorBytes bounds one normalized remote locator hint.
	MaxSourceLocatorBytes = 4096
	// MaxSourceLocatorHints bounds append-only locator history per Source.
	MaxSourceLocatorHints = 64
)

var (
	tagPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	slugPattern      = tagPattern
	metricPattern    = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	namespacePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	gitCommitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

	uriCandidatePattern     = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"']+`)
	sourceSCPLocatorPattern = regexp.MustCompile(`^(?:([^@/:\s]+)@)?([^/:\s]+):(.+)$`)
	sourceSSHUserPattern    = regexp.MustCompile(`^[A-Za-z0-9._~+-]+$`)
)

func validSlug(value string) bool      { return slugPattern.MatchString(value) }
func validMetric(value string) bool    { return metricPattern.MatchString(value) }
func validNamespace(value string) bool { return namespacePattern.MatchString(value) }
func validDigest(value string) bool    { return digestPattern.MatchString(value) }

// NormalizeSourceKey converts a user-facing Source key to its canonical ASCII
// slug. Canonical records must already contain the returned form.
func NormalizeSourceKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	separator := false
	for _, character := range value {
		switch {
		case character >= 'A' && character <= 'Z':
			if separator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			builder.WriteByte(byte(character - 'A' + 'a'))
			separator = false
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			if separator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			builder.WriteRune(character)
			separator = false
		default:
			separator = builder.Len() > 0
		}
		if builder.Len() > MaxSourceKeyBytes {
			return "", errors.New("Source key exceeds the canonical byte limit")
		}
	}
	key := strings.Trim(builder.String(), "-")
	if key == "" || !validSlug(key) {
		return "", errors.New("Source key must normalize to a non-empty lower-case ASCII slug")
	}
	return key, nil
}

// NormalizeSourceSubdir applies the Git-root-relative POSIX contract. Empty
// input selects the Git root. Parent traversal is rejected rather than cleaned.
func NormalizeSourceSubdir(value string) (string, error) {
	failure := func(code, message string) error {
		return &PolicyError{Code: code, Message: message, Err: ErrUnsafePath}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ".", nil
	}
	if !utf8.ValidString(value) || hasAnyControl(value) {
		return "", failure("path.invalid_text", "Source subdir contains invalid text")
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || driveQualified(value) || strings.HasPrefix(value, "~") || strings.HasPrefix(strings.ToLower(value), "file:") {
		return "", failure("path.not_relative", "Source subdir must be Git-root-relative POSIX syntax")
	}
	for _, component := range strings.Split(value, "/") {
		if component == ".." {
			return "", failure("path.traversal", "Source subdir contains parent traversal")
		}
	}
	value = path.Clean(value)
	if len(value) > MaxSourceSubdirBytes {
		return "", failure("path.size", "Source subdir exceeds the canonical byte limit")
	}
	if err := ValidateCommittedPath(value, true); err != nil {
		return "", err
	}
	if containsCredentialMaterial(value) {
		return "", failure("privacy.secret", "credential-bearing Source subdir is forbidden")
	}
	if err := ValidateCommitSafeText(value); err != nil {
		return "", err
	}
	return value, nil
}

// NormalizeSourceLocator returns an identity-preserving remote Git locator.
// SSH usernames and the distinction between SCP home-relative and URI absolute
// paths are significant repository identity. Passwords, query strings, and
// fragments are rejected rather than silently discarded. Local path forms and
// credential-bearing repository paths are also rejected.
func NormalizeSourceLocator(value string) (string, error) {
	failure := func(code, message string) error {
		return &PolicyError{Code: code, Message: message, Err: ErrUnsafeURI}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", failure("source.locator", "Source locator is empty")
	}
	if !utf8.ValidString(value) || hasAnyControl(value) {
		return "", failure("source.locator", "Source locator contains invalid text")
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~") || driveQualified(value) || strings.Contains(value, "\\") || strings.HasPrefix(strings.ToLower(value), "file:") {
		return "", failure("source.locator_local", "local paths are forbidden as Source locator hints")
	}

	if match := sourceSCPLocatorPattern.FindStringSubmatch(value); match != nil && !strings.Contains(value, "://") && portableSourceSCPHost(value, match[2]) {
		if strings.ContainsAny(match[3], "?#") {
			return "", failure("source.locator_components", "Source locator query strings and fragments are forbidden")
		}
		user, err := normalizeSourceSSHUser(match[1])
		if err != nil {
			return "", err
		}
		relative := !strings.HasPrefix(match[3], "/")
		remotePath, err := normalizeSourceRemotePath(match[3])
		if err != nil {
			return "", err
		}
		scheme := "ssh"
		if relative {
			scheme = "ssh+scp"
		}
		parsed := &url.URL{Scheme: scheme, Host: strings.ToLower(match[2])}
		canonicalHost, err := normalizeSourceRemoteHost(parsed)
		if err != nil {
			return "", err
		}
		normalizedURL := &url.URL{Scheme: scheme, Host: canonicalHost, Path: remotePath}
		if user != "" {
			normalizedURL.User = url.User(user)
		}
		normalized := normalizedURL.String()
		if len(normalized) > MaxSourceLocatorBytes {
			return "", failure("source.locator_size", "Source locator exceeds the canonical byte limit")
		}
		return normalized, nil
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return "", failure("source.locator", "Source locator must be a remote hierarchical URI or Git SCP-style locator")
	}
	if strings.EqualFold(parsed.Scheme, "file") {
		return "", failure("source.locator_local", "file URIs are forbidden as Source locator hints")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", failure("source.locator_components", "Source locator query strings and fragments are forbidden")
	}
	var user string
	if parsed.User != nil {
		sshScheme := strings.EqualFold(parsed.Scheme, "ssh") || strings.EqualFold(parsed.Scheme, "ssh+scp")
		if _, password := parsed.User.Password(); password || !sshScheme || parsed.User.Username() == "" {
			return "", failure("uri.credentials", "credential-bearing or empty Source locator userinfo is forbidden")
		}
		user, err = normalizeSourceSSHUser(parsed.User.Username())
		if err != nil {
			return "", err
		}
	}
	canonicalHost, err := normalizeSourceRemoteHost(parsed)
	if err != nil {
		return "", err
	}
	remotePath, err := normalizeSourceRemotePath(parsed.EscapedPath())
	if err != nil {
		return "", err
	}
	normalizedURL := &url.URL{
		Scheme: strings.ToLower(parsed.Scheme),
		Host:   canonicalHost,
		Path:   remotePath,
	}
	if user != "" {
		normalizedURL.User = url.User(user)
	}
	normalized := normalizedURL.String()
	if len(normalized) > MaxSourceLocatorBytes {
		return "", failure("source.locator_size", "Source locator exceeds the canonical byte limit")
	}
	return normalized, nil
}

func portableSourceSCPHost(value, host string) bool {
	prefix, _, _ := strings.Cut(value, ":")
	return strings.Contains(prefix, "@") || strings.Contains(host, ".") || strings.EqualFold(host, "localhost")
}

func normalizeSourceSSHUser(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > 255 || !sourceSSHUserPattern.MatchString(value) {
		return "", &PolicyError{Code: "source.locator", Message: "Source SSH locator has an invalid username", Err: ErrUnsafeURI}
	}
	return value, nil
}

func normalizeSourceRemoteHost(parsed *url.URL) (string, error) {
	failure := func(message string) error {
		return &PolicyError{Code: "source.locator", Message: message, Err: ErrUnsafeURI}
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" || hasAnyControl(hostname) || strings.ContainsAny(hostname, " /\\@") {
		return "", failure("Source locator has an invalid remote host")
	}
	port := parsed.Port()
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		if port == "80" {
			port = ""
		}
	case "https":
		if port == "443" {
			port = ""
		}
	case "ssh", "ssh+scp":
		if port == "22" {
			port = ""
		}
	}
	if port != "" {
		return net.JoinHostPort(hostname, port), nil
	}
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]", nil
	}
	return hostname, nil
}

func normalizeSourceRemotePath(value string) (string, error) {
	failure := func(code, message string) error {
		return &PolicyError{Code: code, Message: message, Err: ErrUnsafeURI}
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", failure("source.locator", "Source locator path has invalid escaping")
	}
	if decoded == "" || !utf8.ValidString(decoded) || hasAnyControl(decoded) || strings.Contains(decoded, "\\") {
		return "", failure("source.locator", "Source locator requires a valid remote repository path")
	}
	for _, component := range strings.Split(decoded, "/") {
		if component == ".." {
			return "", failure("source.locator", "Source locator path contains parent traversal")
		}
	}
	cleaned := path.Clean("/" + strings.TrimLeft(decoded, "/"))
	if cleaned == "/" {
		return "", failure("source.locator", "Source locator requires a remote repository path")
	}
	if containsCredentialMaterial(cleaned) {
		return "", failure("uri.credentials", "credential-bearing Source locator paths are forbidden")
	}
	if err := ValidateCommitSafeText(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

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
	key, cached := safeTextCached(value)
	if cached {
		return nil
	}
	if err := validateCommitSafeTextUncached(value); err != nil {
		return err
	}
	rememberSafeText(value, key)
	return nil
}

func validateCommitSafeTextUncached(value string) error {
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
	key, cached := safeTextCachedDomain(value, 1)
	if cached {
		return false
	}
	if safex.ContainsSecretText(safex.DecodePercentEncoding(value)) {
		return true
	}
	rememberSafeText(value, key)
	return false
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
