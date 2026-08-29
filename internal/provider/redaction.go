package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/daviddwlee84/exp-cli/internal/safex"
)

const (
	DefaultMaxTextBytes = 16 << 10
	DefaultMaxRawBytes  = 1 << 20
	MaxTextBytes        = 64 << 10
	MaxRawBytes         = 8 << 20
	MaxRawInputBytes    = 64 << 20
)

var (
	ErrUnsafeURI       = errors.New("unsafe canonical URI")
	ErrRedactionLimit  = errors.New("sanitized value exceeds byte bound")
	ErrUnsupportedData = errors.New("provider raw state is not JSON-compatible")
)

// RedactionPolicy carries explicit canaries in a non-serializable redactor and
// enforces independent text and structured-state output bounds. Its zero value
// uses secure defaults.
type RedactionPolicy struct {
	MaxTextBytes int `json:"max_text_bytes"`
	MaxRawBytes  int `json:"max_raw_bytes"`
	redactor     safex.Redactor
}

// NewRedactionPolicy constructs a bounded policy with explicit secret
// canaries. Secret values are retained only in safex.Redactor's private state.
func NewRedactionPolicy(maxTextBytes, maxRawBytes int, secrets ...string) (RedactionPolicy, error) {
	policy := RedactionPolicy{
		MaxTextBytes: maxTextBytes,
		MaxRawBytes:  maxRawBytes,
		redactor:     safex.NewRedactor(secrets...),
	}
	if _, err := policy.effective(); err != nil {
		return RedactionPolicy{}, err
	}
	return policy, nil
}

// DefaultRedactionPolicy returns bounded provider defaults.
func DefaultRedactionPolicy() RedactionPolicy {
	policy, _ := NewRedactionPolicy(DefaultMaxTextBytes, DefaultMaxRawBytes)
	return policy
}

// WithSecrets returns a copy extended with explicit canaries.
func (p RedactionPolicy) WithSecrets(secrets ...string) RedactionPolicy {
	effective, err := p.effective()
	if err != nil {
		p.redactor = p.redactor.WithSecrets(secrets...)
		return p
	}
	effective.redactor = effective.redactor.WithSecrets(secrets...)
	return effective
}

// String intentionally omits secret values.
func (p RedactionPolicy) String() string {
	effective, err := p.effective()
	if err != nil {
		return "<invalid-redaction-policy>"
	}
	return fmt.Sprintf("text<=%d,raw<=%d", effective.MaxTextBytes, effective.MaxRawBytes)
}

// GoString intentionally omits secret values.
func (p RedactionPolicy) GoString() string { return p.String() }

// MarshalJSON emits only bounds, never explicit canaries.
func (p RedactionPolicy) MarshalJSON() ([]byte, error) {
	effective, err := p.effective()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		MaxTextBytes int `json:"max_text_bytes"`
		MaxRawBytes  int `json:"max_raw_bytes"`
	}{effective.MaxTextBytes, effective.MaxRawBytes})
}

func (p RedactionPolicy) effective() (RedactionPolicy, error) {
	if p.MaxTextBytes == 0 && p.MaxRawBytes == 0 {
		p.MaxTextBytes = DefaultMaxTextBytes
		p.MaxRawBytes = DefaultMaxRawBytes
	}
	if p.MaxTextBytes <= 0 || p.MaxTextBytes > MaxTextBytes {
		return RedactionPolicy{}, fmt.Errorf("invalid text redaction bound")
	}
	if p.MaxRawBytes <= 0 || p.MaxRawBytes > MaxRawBytes {
		return RedactionPolicy{}, fmt.Errorf("invalid raw-state redaction bound")
	}
	return p, nil
}

// SanitizeText structurally redacts text and enforces the policy text bound.
func SanitizeText(value string, policy RedactionPolicy) (string, bool, error) {
	effective, err := policy.effective()
	if err != nil {
		return "", false, err
	}
	safe, truncated := effective.redactor.SafeText(value, effective.MaxTextBytes)
	return safe, truncated, nil
}

func sanitizeTextStrict(value string, policy RedactionPolicy) (string, error) {
	safe, truncated, err := SanitizeText(value, policy)
	if err != nil {
		return "", err
	}
	if truncated {
		return "", ErrRedactionLimit
	}
	return safe, nil
}

func sanitizeSingleLine(value string, policy RedactionPolicy) (string, error) {
	safe, err := sanitizeTextStrict(value, policy)
	if err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(safe), " "), nil
}

func sanitizeDiagnosticText(value string, policy RedactionPolicy) (string, error) {
	effective, err := policy.effective()
	if err != nil {
		return "", err
	}
	safe, truncated := effective.redactor.SafeDiagnostic(value, effective.MaxTextBytes)
	if truncated {
		return "", ErrRedactionLimit
	}
	return safe, nil
}

// SanitizeURI creates a display-safe URI using the default policy. Userinfo is
// removed, credential-like query fields are removed, other query keys and values
// are redacted, and credential-bearing fragments are sanitized structurally.
func SanitizeURI(raw string) (string, error) {
	return SanitizeURIWithPolicy(raw, DefaultRedactionPolicy())
}

// SanitizeURIWithPolicy creates a bounded display-safe URI. It never returns
// the raw input on parse or bound failures.
func SanitizeURIWithPolicy(raw string, policy RedactionPolicy) (string, error) {
	effective, err := policy.effective()
	if err != nil {
		return "", err
	}
	parsed, err := parseProviderURI(raw, effective)
	if err != nil {
		return "", err
	}
	parsed.User = nil
	parsed.ForceQuery = false
	safeHost, err := sanitizeTextStrict(parsed.Host, effective)
	if err != nil || safeHost != parsed.Host {
		return "", ErrUnsafeURI
	}
	parsed.Path, err = sanitizeURIComponent(parsed.EscapedPath(), effective)
	if err != nil {
		return "", err
	}
	parsed.RawPath = ""
	parsed.Opaque, err = sanitizeURIComponent(parsed.Opaque, effective)
	if err != nil {
		return "", err
	}
	if parsed.RawQuery != "" {
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil {
			return "", ErrUnsafeURI
		}
		query, err = sanitizeQuery(query, effective)
		if err != nil {
			return "", err
		}
		parsed.RawQuery = query.Encode()
		parsed.ForceQuery = false
	}
	decodedFragment, err := decodeURIComponent(parsed.EscapedFragment())
	if err != nil {
		return "", err
	}
	fragment, err := sanitizeFragment(decodedFragment, effective)
	if err != nil {
		return "", err
	}
	parsed.Fragment = fragment
	safe := parsed.String()
	if len(safe) > effective.MaxTextBytes {
		return "", ErrRedactionLimit
	}
	return safe, nil
}

// ValidateCanonicalURI rejects canonical references containing userinfo, any
// query component, credential-like fragment data, file paths, controls, or a
// redaction placeholder. Safe routing fragments such as MLflow's #/runs/... are
// retained.
func ValidateCanonicalURI(raw string) error {
	if raw == "" {
		return nil
	}
	policy := DefaultRedactionPolicy()
	parsed, err := parseProviderURI(raw, policy)
	if err != nil {
		return ErrUnsafeURI
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery {
		return ErrUnsafeURI
	}
	if strings.Contains(raw, safex.Redacted) {
		return ErrUnsafeURI
	}
	safeHost, err := sanitizeTextStrict(parsed.Host, policy)
	if err != nil || safeHost != parsed.Host {
		return ErrUnsafeURI
	}
	decodedPath, err := decodeURIComponent(parsed.EscapedPath())
	if err != nil {
		return ErrUnsafeURI
	}
	safePath, err := sanitizeTextStrict(decodedPath, policy)
	if err != nil || safePath != decodedPath {
		return ErrUnsafeURI
	}
	decodedOpaque, err := decodeURIComponent(parsed.Opaque)
	if err != nil {
		return ErrUnsafeURI
	}
	safeOpaque, err := sanitizeTextStrict(decodedOpaque, policy)
	if err != nil || safeOpaque != decodedOpaque {
		return ErrUnsafeURI
	}
	decodedFragment, err := decodeURIComponent(parsed.EscapedFragment())
	if err != nil {
		return ErrUnsafeURI
	}
	safeFragment, err := sanitizeFragment(decodedFragment, policy)
	if err != nil || safeFragment != decodedFragment {
		return ErrUnsafeURI
	}
	return nil
}

func sanitizeURIComponent(value string, policy RedactionPolicy) (string, error) {
	decoded, err := decodeURIComponent(value)
	if err != nil {
		return "", err
	}
	return sanitizeTextStrict(decoded, policy)
}

func decodeURIComponent(value string) (string, error) {
	decoded := safex.DecodePercentEncoding(value)
	if hasControl(decoded) {
		return "", ErrUnsafeURI
	}
	return decoded, nil
}

func parseProviderURI(raw string, policy RedactionPolicy) (*url.URL, error) {
	if raw == "" || raw != strings.TrimSpace(raw) || hasControl(raw) || len(raw) > policy.MaxTextBytes {
		return nil, ErrUnsafeURI
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || strings.EqualFold(parsed.Scheme, "file") {
		return nil, ErrUnsafeURI
	}
	if (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Host == "" {
		return nil, ErrUnsafeURI
	}
	if hasControl(parsed.Path) || hasControl(parsed.Opaque) || hasControl(parsed.Fragment) || hasControl(parsed.RawQuery) || hasControl(parsed.Host) {
		return nil, ErrUnsafeURI
	}
	if parsed.User != nil && hasControl(parsed.User.String()) {
		return nil, ErrUnsafeURI
	}
	return parsed, nil
}

func sanitizeQuery(query url.Values, policy RedactionPolicy) (url.Values, error) {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	safeQuery := make(url.Values, len(query))
	for _, key := range keys {
		if credentialParameter(key) {
			continue
		}
		safeKey, err := sanitizeTextStrict(key, policy)
		if err != nil {
			return nil, err
		}
		if _, collision := safeQuery[safeKey]; collision {
			return nil, ErrUnsafeURI
		}
		values := query[key]
		safeValues := make([]string, len(values))
		for index, value := range values {
			safeValues[index], err = sanitizeTextStrict(value, policy)
			if err != nil {
				return nil, err
			}
		}
		safeQuery[safeKey] = safeValues
	}
	return safeQuery, nil
}

func sanitizeFragment(fragment string, policy RedactionPolicy) (string, error) {
	if fragment == "" {
		return "", nil
	}
	if route, rawQuery, found := strings.Cut(fragment, "?"); found {
		safeRoute, err := sanitizeTextStrict(route, policy)
		if err != nil {
			return "", err
		}
		query, err := url.ParseQuery(rawQuery)
		if err != nil {
			return "", ErrUnsafeURI
		}
		query, err = sanitizeQuery(query, policy)
		if err != nil {
			return "", err
		}
		if encoded := query.Encode(); encoded != "" {
			return safeRoute + "?" + encoded, nil
		}
		return safeRoute, nil
	}
	if strings.Contains(fragment, "=") {
		query, err := url.ParseQuery(fragment)
		if err == nil {
			query, err = sanitizeQuery(query, policy)
			if err != nil {
				return "", err
			}
			return query.Encode(), nil
		}
	}
	return sanitizeTextStrict(fragment, policy)
}

func credentialParameter(name string) bool {
	if safex.SensitiveName(name) {
		return true
	}
	compact := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	switch compact {
	case "auth", "oauth", "code", "key", "sig", "signature", "session", "sessionid",
		"xamzsignature", "xamzcredential", "xgoogsignature", "xgoogcredential", "sas":
		return true
	}
	return false
}

// SanitizeHeaders returns a bounded deep copy. Values of credential-related
// headers are replaced wholesale; other values receive text redaction.
func SanitizeHeaders(headers map[string][]string, policy RedactionPolicy) (map[string][]string, error) {
	effective, err := policy.effective()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(headers))
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "" || hasControl(key) {
			return nil, fmt.Errorf("invalid header name")
		}
		safeKey, err := sanitizeTextStrict(key, effective)
		if err != nil {
			return nil, err
		}
		if _, collision := out[safeKey]; collision {
			return nil, fmt.Errorf("redacted header name collision")
		}
		values := headers[key]
		safeValues := make([]string, len(values))
		for index, value := range values {
			if safex.SensitiveName(key) {
				safeValues[index] = safex.Redacted
				continue
			}
			safeValues[index], err = sanitizeTextStrict(value, effective)
			if err != nil {
				return nil, err
			}
		}
		out[safeKey] = safeValues
	}
	if err := withinRawBound(out, effective); err != nil {
		return nil, err
	}
	return out, nil
}

// SanitizeArgv returns a bounded redacted copy with argument boundaries intact.
func SanitizeArgv(argv []string, policy RedactionPolicy, sensitiveIndexes ...int) ([]string, error) {
	effective, err := policy.effective()
	if err != nil {
		return nil, err
	}
	out := effective.redactor.Argv(argv, sensitiveIndexes...)
	for index := range out {
		out[index], err = sanitizeTextStrict(out[index], effective)
		if err != nil {
			return nil, err
		}
	}
	if err := withinRawBound(out, effective); err != nil {
		return nil, err
	}
	return out, nil
}

// SanitizeEnvironment returns a bounded copy; values whose names are
// credential-related are replaced wholesale.
func SanitizeEnvironment(environment map[string]string, policy RedactionPolicy) (map[string]string, error) {
	effective, err := policy.effective()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(environment))
	for name, value := range environment {
		if name == "" || !validEnvironmentDisplayName(name) {
			return nil, fmt.Errorf("invalid environment variable name")
		}
		if safex.SensitiveName(name) {
			out[name] = safex.Redacted
			continue
		}
		out[name], err = sanitizeTextStrict(value, effective)
		if err != nil {
			return nil, err
		}
	}
	if err := withinRawBound(out, effective); err != nil {
		return nil, err
	}
	return out, nil
}

// SanitizeRawState copies JSON-compatible provider state, removes every
// recursively nested envs entry, redacts every string and sensitive map value,
// and enforces the structured output bound. Removing envs is universal because
// provider identity is itself untrusted at this boundary.
func SanitizeRawState(value any, policy RedactionPolicy) (any, error) {
	return sanitizeRawState(value, policy)
}

// SanitizePueueRawState is retained for callers that know they are handling
// Pueue. The envs prohibition is universal and identical to SanitizeRawState.
func SanitizePueueRawState(value any, policy RedactionPolicy) (any, error) {
	return sanitizeRawState(value, policy)
}

// SanitizeProviderRawState applies the universal structural policy regardless
// of the untrusted provider label.
func SanitizeProviderRawState(_ ProviderName, value any, policy RedactionPolicy) (any, error) {
	return sanitizeRawState(value, policy)
}

func sanitizeRawState(value any, policy RedactionPolicy) (any, error) {
	effective, err := policy.effective()
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		return nil, ErrUnsupportedData
	}
	safe, err := sanitizeJSONValue(normalized, effective, true)
	if err != nil {
		return nil, err
	}
	if err := withinRawBound(safe, effective); err != nil {
		return nil, err
	}
	return safe, nil
}

func sanitizeExternalRefMetadata(value any, policy RedactionPolicy) (any, error) {
	effective, err := policy.effective()
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		return nil, ErrUnsupportedData
	}
	safe, err := sanitizeJSONValue(normalized, effective, false)
	if err != nil {
		return nil, err
	}
	if err := withinRawBound(safe, effective); err != nil {
		return nil, err
	}
	return safe, nil
}

// SanitizePueueJSON is the raw protocol boundary for Pueue JSON. It rejects
// trailing data, removes envs recursively, compacts stable JSON, and returns no
// partial raw bytes on failure.
func SanitizePueueJSON(raw []byte, policy RedactionPolicy) (json.RawMessage, error) {
	effective, err := policy.effective()
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxRawInputBytes {
		return nil, ErrRedactionLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrUnsupportedData
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrUnsupportedData
	}
	normalized, err := normalizeJSONNumbers(value)
	if err != nil {
		return nil, ErrUnsupportedData
	}
	safe, err := sanitizeJSONValue(normalized, effective, true)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return nil, ErrUnsupportedData
	}
	if len(encoded) > effective.MaxRawBytes {
		return nil, ErrRedactionLimit
	}
	return json.RawMessage(encoded), nil
}

func normalizeJSONValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxRawInputBytes {
		return nil, ErrUnsupportedData
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, ErrUnsupportedData
	}
	return normalizeJSONNumbers(normalized)
}

func normalizeJSONNumbers(value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool, string, int64, float64:
		if number, ok := typed.(float64); ok && (math.IsInf(number, 0) || math.IsNaN(number)) {
			return nil, ErrUnsupportedData
		}
		return typed, nil
	case json.Number:
		raw := typed.String()
		if !strings.ContainsAny(raw, ".eE") {
			integer, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, ErrUnsupportedData
			}
			return integer, nil
		}
		number, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, ErrUnsupportedData
		}
		return number, nil
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			normalized, err := normalizeJSONNumbers(item)
			if err != nil {
				return nil, err
			}
			out[index] = normalized
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized, err := normalizeJSONNumbers(item)
			if err != nil {
				return nil, err
			}
			out[key] = normalized
		}
		return out, nil
	default:
		return nil, ErrUnsupportedData
	}
}

func sanitizeJSONValue(value any, policy RedactionPolicy, removeEnvs bool) (any, error) {
	switch typed := value.(type) {
	case nil, bool, int64, float64:
		return typed, nil
	case string:
		return sanitizeTextStrict(typed, policy)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			safe, err := sanitizeJSONValue(item, policy, removeEnvs)
			if err != nil {
				return nil, err
			}
			out[index] = safe
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if removeEnvs && strings.EqualFold(key, "envs") {
				continue
			}
			safeKey, err := sanitizeTextStrict(key, policy)
			if err != nil {
				return nil, err
			}
			if _, collision := out[safeKey]; collision {
				return nil, fmt.Errorf("redacted raw-state key collision")
			}
			if safex.SensitiveName(key) {
				out[safeKey] = safex.Redacted
				continue
			}
			safe, err := sanitizeJSONValue(typed[key], policy, removeEnvs)
			if err != nil {
				return nil, err
			}
			out[safeKey] = safe
		}
		return out, nil
	default:
		return nil, ErrUnsupportedData
	}
}

func withinRawBound(value any, policy RedactionPolicy) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ErrUnsupportedData
	}
	if len(encoded) > policy.MaxRawBytes {
		return ErrRedactionLimit
	}
	return nil
}

func validEnvironmentDisplayName(name string) bool {
	for _, r := range name {
		if r == '=' || r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return name != ""
}

func hasControl(value string) bool {
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return true
		}
	}
	return false
}
