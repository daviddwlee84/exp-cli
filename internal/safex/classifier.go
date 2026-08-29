// Package safex provides cycle-free classification and redaction for secret
// names, text, diagnostics, and argument vectors.
package safex

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Redacted is the stable replacement for credential-bearing material.
const Redacted = "[REDACTED]"

const truncatedMarker = "[TRUNCATED]"

var (
	privateBlockPattern  = regexp.MustCompile(`(?is)-----BEGIN[^\r\n]*(?:PRIVATE KEY|SECRET|TOKEN)[^\r\n]*-----.*?(?:-----END[^\r\n]*-----|$)`)
	headerLinePattern    = regexp.MustCompile(`(?im)^([ \t]*)([a-z][a-z0-9_.-]*)([ \t]*:[ \t]*)([^\r\n]*)$`)
	headerOptionPattern  = regexp.MustCompile(`(?m)(^|[ \t"'(])(--header|-H)(=?[ \t]*)([^\r\n]+)`)
	userOptionPattern    = regexp.MustCompile(`(?i)(^|[ \t"'(])(--proxy-user|--username|--user|-u)(=?[ \t]*)([^\s,;&]+)`)
	longOptionPattern    = regexp.MustCompile(`(?i)(^|[^a-z0-9_])(--[a-z][a-z0-9_.-]*)([ \t]*=[ \t]*|[ \t]+)("[^"\r\n]*"|'[^'\r\n]*'|[^\s,;&]+)`)
	authorizationPattern = regexp.MustCompile(`(?i)\b((?:proxy[-_ ]?authorization|authorization)[ \t]*[:=][ \t]*)(?:(?:basic|bearer)[ \t]+)?([^\s,;"']+)`)
	keyValuePattern      = regexp.MustCompile(`(?i)(^|[^a-z0-9_])("?'?)([a-z_][a-z0-9_.%+-]*)("?'?)([ \t]*[:=][ \t]*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s,;&]+)`)
	bareSchemePattern    = regexp.MustCompile(`(?i)\b(bearer|basic)([ \t]+)([a-z0-9._~+/%:=-]{4,})`)
	uriUserinfoPattern   = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)([^/@\s]+)@`)
	quotedNamePattern    = regexp.MustCompile(`(["'])([^"'\r\n]+)(["'])`)
	identifierPattern    = regexp.MustCompile(`--?[[:alnum:]_./%+\\-]+|[[:alnum:]_][[:alnum:]_./%+\\-]*`)
)

// SensitiveName reports whether name conventionally identifies a credential.
// It recognizes separator-delimited and camel-case spellings without treating
// prose-oriented names such as token_count, max_tokens, or tokenizer as secret.
func SensitiveName(name string) bool {
	tokens := nameTokens(name)
	if len(tokens) == 0 {
		return false
	}
	for index, token := range tokens {
		switch token {
		case "authorization", "cookie", "password", "passwd", "pwd", "secret", "credential", "credentials", "jwt", "bearer":
			return true
		case "auth", "oauth":
			return true
		case "session":
			if len(tokens) == 1 || index == len(tokens)-1 {
				return true
			}
			switch tokens[index+1] {
			case "id", "key", "token", "value":
				return true
			}
		case "token":
			if len(tokens) == 1 || index == len(tokens)-1 {
				return true
			}
			if index > 0 {
				switch tokens[index-1] {
				case "access", "api", "auth", "id", "identity", "refresh", "session", "x":
					return true
				}
			}
			switch tokens[index+1] {
			case "file", "key", "path", "secret", "value":
				return true
			}
		case "key":
			if index > 0 {
				switch tokens[index-1] {
				case "access", "api", "auth", "aws", "private", "secret", "signing", "ssh", "tls", "x":
					return true
				}
			}
			if index+1 < len(tokens) {
				switch tokens[index+1] {
				case "file", "id", "path", "value":
					return true
				}
			}
		}
	}
	return false
}

func nameTokens(name string) []string {
	name = DecodePercentEncoding(strings.TrimSpace(name))
	name = strings.TrimLeft(name, "-")
	runes := []rune(name)
	tokens := make([]string, 0, 4)
	var token []rune
	flush := func() {
		if len(token) == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(string(token)))
		token = token[:0]
	}
	for index, character := range runes {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			flush()
			continue
		}
		if unicode.IsUpper(character) && len(token) > 0 {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || unicode.IsUpper(previous) && nextIsLower {
				flush()
			}
		}
		token = append(token, character)
	}
	flush()
	return tokens
}

// DecodePercentEncoding recursively decodes URL query escaping to the shared
// bounded depth used by credential classifiers. Malformed escaping stops at the
// last successfully decoded value rather than returning partially transformed
// data.
func DecodePercentEncoding(value string) string {
	for iteration := 0; iteration < 4; iteration++ {
		decoded, err := url.QueryUnescape(value)
		if err != nil || decoded == value {
			break
		}
		value = decoded
	}
	return value
}

// SensitiveHeader reports whether value is a non-empty credential-bearing
// HTTP-style header.
func SensitiveHeader(value string) bool {
	value = strings.Trim(value, " \t\"'")
	name, contents, found := strings.Cut(value, ":")
	return found && strings.TrimSpace(contents) != "" && SensitiveName(name) && !benignNamedValue(name, contents)
}

// SensitiveAssignment reports whether value is a non-empty NAME=value secret
// assignment. NLP placeholders such as token=[CLS] remain ordinary text.
func SensitiveAssignment(value string) bool {
	name, contents, found := strings.Cut(value, "=")
	return found && strings.TrimSpace(contents) != "" && SensitiveName(name) && !benignNamedValue(name, contents)
}

func benignNamedValue(name, value string) bool {
	tokens := nameTokens(name)
	if len(tokens) != 1 || tokens[0] != "token" {
		return false
	}
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	value = strings.TrimRight(value, ".,;:!?")
	if strings.EqualFold(value, "count") || strings.HasPrefix(strings.ToLower(value), "count ") {
		return true
	}
	switch strings.ToUpper(value) {
	case "CLS", "MASK", "PAD", "SEP", "UNK", "[CLS]", "[MASK]", "[PAD]", "[SEP]", "[UNK]":
		return true
	default:
		return false
	}
}

// Redactor carries explicit secret canaries and applies the shared structural
// classifier. Secret values are private and are never formatted or encoded.
type Redactor struct {
	secrets []string
}

// NewRedactor constructs a redactor. Empty canaries are ignored and longer
// values are replaced first so overlaps cannot expose suffixes.
func NewRedactor(secrets ...string) Redactor {
	unique := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		unique[secret] = struct{}{}
		for _, line := range strings.Split(secret, "\n") {
			if line != "" {
				unique[line] = struct{}{}
			}
		}
	}
	ordered := make([]string, 0, len(unique))
	for secret := range unique {
		ordered = append(ordered, secret)
	}
	sort.Slice(ordered, func(left, right int) bool {
		if len(ordered[left]) != len(ordered[right]) {
			return len(ordered[left]) > len(ordered[right])
		}
		return ordered[left] < ordered[right]
	})
	return Redactor{secrets: ordered}
}

// WithSecrets returns a copy extended with additional explicit canaries.
func (r Redactor) WithSecrets(secrets ...string) Redactor {
	combined := append(append([]string(nil), r.secrets...), secrets...)
	return NewRedactor(combined...)
}

func (r Redactor) String() string   { return "<redactor>" }
func (r Redactor) GoString() string { return r.String() }

// MarshalJSON reports configuration without exposing configured values.
func (r Redactor) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Configured bool `json:"configured"`
	}{Configured: len(r.secrets) > 0})
}

// Text redacts explicit canaries and structurally credential-bearing text. It
// always returns valid UTF-8.
func (r Redactor) Text(value string) string {
	value = strings.ToValidUTF8(value, "�")
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, Redacted)
	}
	value, _ = rewriteStructuredText(value)
	return value
}

// ContainsSecretText reports whether value contains concrete credential
// material. It does not classify ordinary discussion of credentials as secret.
func ContainsSecretText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	_, sensitive := rewriteStructuredText(value)
	return sensitive
}

func rewriteStructuredText(value string) (string, bool) {
	sensitive := false
	replace := func(pattern *regexp.Regexp, rewrite func([]string) (string, bool)) {
		var matched bool
		value, matched = rewritePattern(value, pattern, rewrite)
		sensitive = sensitive || matched
	}

	if privateBlockPattern.MatchString(value) {
		sensitive = true
		value = privateBlockPattern.ReplaceAllString(value, Redacted)
	}
	replace(headerLinePattern, func(groups []string) (string, bool) {
		if len(groups) != 5 || !SensitiveName(groups[2]) || benignNamedValue(groups[2], groups[4]) || strings.TrimSpace(groups[4]) == "" {
			return "", false
		}
		return groups[1] + groups[2] + groups[3] + Redacted, true
	})
	replace(headerOptionPattern, func(groups []string) (string, bool) {
		if len(groups) != 5 || !SensitiveHeader(groups[4]) {
			return "", false
		}
		return groups[1] + groups[2] + groups[3] + redactHeader(groups[4]), true
	})
	replace(userOptionPattern, func(groups []string) (string, bool) {
		if len(groups) != 5 || strings.TrimSpace(groups[4]) == "" {
			return "", false
		}
		return groups[1] + groups[2] + groups[3] + Redacted, true
	})
	replace(longOptionPattern, func(groups []string) (string, bool) {
		if len(groups) != 5 || !sensitiveArgumentName(groups[2]) || benignNamedValue(groups[2], groups[4]) {
			return "", false
		}
		return groups[1] + groups[2] + groups[3] + quoteReplacement(groups[4]), true
	})
	replace(authorizationPattern, func(groups []string) (string, bool) {
		if len(groups) != 3 || groups[2] == "" {
			return "", false
		}
		return groups[1] + Redacted, true
	})
	replace(keyValuePattern, func(groups []string) (string, bool) {
		if len(groups) != 7 || !SensitiveName(groups[3]) || benignNamedValue(groups[3], groups[6]) {
			return "", false
		}
		return groups[1] + groups[2] + groups[3] + groups[4] + groups[5] + quoteReplacement(groups[6]), true
	})
	replace(bareSchemePattern, func(groups []string) (string, bool) {
		if len(groups) != 4 || groups[3] == "" {
			return "", false
		}
		return groups[1] + groups[2] + Redacted, true
	})
	replace(uriUserinfoPattern, func(groups []string) (string, bool) {
		if len(groups) != 3 || groups[2] == "" {
			return "", false
		}
		return groups[1] + Redacted + "@", true
	})
	return value, sensitive
}

func rewritePattern(value string, pattern *regexp.Regexp, rewrite func([]string) (string, bool)) (string, bool) {
	var output strings.Builder
	cursor := 0
	search := 0
	changed := false
	for search < len(value) {
		indexes := pattern.FindStringSubmatchIndex(value[search:])
		if indexes == nil {
			break
		}
		for index := range indexes {
			if indexes[index] >= 0 {
				indexes[index] += search
			}
		}
		groups := make([]string, len(indexes)/2)
		for index := range groups {
			start, end := indexes[index*2], indexes[index*2+1]
			if start >= 0 {
				groups[index] = value[start:end]
			}
		}
		replacement, matched := rewrite(groups)
		if matched {
			output.WriteString(value[cursor:indexes[0]])
			output.WriteString(replacement)
			cursor = indexes[1]
			search = indexes[1]
			changed = true
			continue
		}
		resume := indexes[0]
		for index := len(groups) - 1; index > 0; index-- {
			if indexes[index*2] > resume {
				resume = indexes[index*2]
				break
			}
		}
		if resume == indexes[0] {
			_, width := utf8.DecodeRuneInString(value[indexes[0]:])
			if width == 0 {
				width = 1
			}
			resume += width
		}
		search = resume
	}
	if !changed {
		return value, false
	}
	output.WriteString(value[cursor:])
	return output.String(), true
}

func quoteReplacement(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[:1] + Redacted + value[len(value)-1:]
	}
	return Redacted
}

func redactHeader(value string) string {
	trimmed := strings.TrimSpace(value)
	name, _, found := strings.Cut(trimmed, ":")
	if !found {
		return Redacted
	}
	return name + ": " + Redacted
}

// Diagnostic applies Text plus conservative redaction of structured identifiers
// such as unknown JSON keys and filesystem paths whose components are sensitive.
func (r Redactor) Diagnostic(value string) string {
	return strings.Join(strings.Fields(r.diagnosticText(value)), " ")
}

// Path applies the shared structural classifier independently to path
// components. Safe paths are returned byte-for-byte, including spacing and
// separators, while a credential-shaped component is replaced as a unit so it
// cannot expose either a credential name or value in machine output.
func (r Redactor) Path(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	componentStart := 0
	for index := 0; index < len(value); index++ {
		if value[index] != '/' && value[index] != '\\' {
			continue
		}
		output.WriteString(r.pathComponent(value[componentStart:index]))
		output.WriteByte(value[index])
		componentStart = index + 1
	}
	output.WriteString(r.pathComponent(value[componentStart:]))
	return output.String()
}

func (r Redactor) pathComponent(value string) string {
	if value == "" {
		return value
	}
	if strings.Contains(r.Diagnostic(value), Redacted) {
		return Redacted
	}
	return value
}

func (r Redactor) diagnosticText(value string) string {
	value = r.Text(value)
	value = quotedNamePattern.ReplaceAllStringFunc(value, func(match string) string {
		groups := quotedNamePattern.FindStringSubmatch(match)
		if len(groups) != 4 || !SensitiveName(groups[2]) {
			return match
		}
		return groups[1] + Redacted + groups[3]
	})
	value = identifierPattern.ReplaceAllStringFunc(value, func(identifier string) string {
		if strings.Contains(identifier, Redacted) || !structuredIdentifier(identifier) || !SensitiveName(identifier) {
			return identifier
		}
		return Redacted
	})
	return strings.Map(func(character rune) rune {
		if character != '\n' && character != '\t' && unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
}

func structuredIdentifier(value string) bool {
	if strings.ContainsAny(value, "_./%+\\-") {
		return true
	}
	for _, character := range value {
		if unicode.IsUpper(character) {
			return true
		}
	}
	return false
}

// SafeText redacts value and then enforces maxBytes.
func (r Redactor) SafeText(value string, maxBytes int) (string, bool) {
	return BoundText(r.Text(value), maxBytes)
}

// SafeDiagnostic redacts diagnostic text, normalizes it to one line, and then
// enforces maxBytes.
func (r Redactor) SafeDiagnostic(value string, maxBytes int) (string, bool) {
	return BoundText(r.Diagnostic(value), maxBytes)
}

// DiagnosticOutput applies diagnostic redaction while preserving newlines,
// tabs, and printable spacing used by complete human-oriented result streams.
// Unlike SafeDiagnosticOutput, it deliberately does not impose a size limit.
func (r Redactor) DiagnosticOutput(value string) string {
	return r.diagnosticText(value)
}

// SafeDiagnosticOutput applies the same diagnostic redaction while preserving
// newlines, tabs, and printable spacing, then enforces maxBytes.
func (r Redactor) SafeDiagnosticOutput(value string, maxBytes int) (string, bool) {
	return BoundText(r.DiagnosticOutput(value), maxBytes)
}

// BoundText returns valid UTF-8 no larger than maxBytes.
func BoundText(value string, maxBytes int) (string, bool) {
	value = strings.ToValidUTF8(value, "�")
	if maxBytes <= 0 {
		return "", value != ""
	}
	if len(value) <= maxBytes {
		return value, false
	}
	if maxBytes <= len(truncatedMarker) {
		return validUTF8Prefix(truncatedMarker, maxBytes), true
	}
	prefixBytes := maxBytes - len(truncatedMarker)
	return validUTF8Prefix(value, prefixBytes) + truncatedMarker, true
}

func validUTF8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	prefix := value[:maxBytes]
	for !utf8.ValidString(prefix) && len(prefix) > 0 {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

type argvKind uint8

const (
	argvText argvKind = iota
	argvSecretValue
	argvHeaderValue
	argvEnvironmentValue
	argvInlineSecret
	argvInlineHeader
	argvInlineEnvironment
	argvAttachedUser
	argvAttachedHeader
	argvInlineScheme
)

type classifiedArg struct {
	kind   argvKind
	name   string
	joiner string
	value  string
}

func classifyArgv(argv []string) []classifiedArg {
	classified := make([]classifiedArg, len(argv))
	expected := argvText
	for index, argument := range argv {
		if expected != argvText {
			classified[index] = classifiedArg{kind: expected, value: argument}
			expected = argvText
			continue
		}

		lower := strings.ToLower(argument)
		switch {
		case argument == "-H" || lower == "--header":
			expected = argvHeaderValue
			continue
		case strings.HasPrefix(argument, "-H") && len(argument) > 2:
			value := argument[2:]
			joiner := ""
			if strings.HasPrefix(value, "=") {
				joiner, value = "=", value[1:]
			}
			classified[index] = classifiedArg{kind: argvAttachedHeader, name: argument[:2], joiner: joiner, value: value}
			continue
		case argument == "-u" || lower == "--user" || lower == "--proxy-user" || lower == "--username":
			expected = argvSecretValue
			continue
		case strings.HasPrefix(argument, "-u") && len(argument) > 2:
			value := argument[2:]
			joiner := ""
			if strings.HasPrefix(value, "=") {
				joiner, value = "=", value[1:]
			}
			classified[index] = classifiedArg{kind: argvAttachedUser, name: argument[:2], joiner: joiner, value: value}
			continue
		case argument == "-e" || lower == "--env" || lower == "--environment":
			expected = argvEnvironmentValue
			continue
		case strings.HasPrefix(argument, "-e") && len(argument) > 2 && strings.Contains(argument[2:], "="):
			classified[index] = classifiedArg{kind: argvInlineEnvironment, name: argument[:2], value: argument[2:]}
			continue
		}

		name, value, hasValue := strings.Cut(argument, "=")
		lowerName := strings.ToLower(name)
		switch lowerName {
		case "--header":
			classified[index] = classifiedArg{kind: argvInlineHeader, name: name, joiner: "=", value: value}
			continue
		case "--env", "--environment":
			classified[index] = classifiedArg{kind: argvInlineEnvironment, name: name, joiner: "=", value: value}
			continue
		case "--user", "--proxy-user", "--username":
			classified[index] = classifiedArg{kind: argvInlineSecret, name: name, joiner: "=", value: value}
			continue
		}
		if hasValue && sensitiveArgumentName(name) && !benignNamedValue(name, value) {
			classified[index] = classifiedArg{kind: argvInlineSecret, name: name, joiner: "=", value: value}
			continue
		}
		if !hasValue && structurallyNamedArgument(name) && sensitiveArgumentName(name) {
			expected = argvSecretValue
			continue
		}
		if SensitiveHeader(argument) {
			classified[index] = classifiedArg{kind: argvHeaderValue, value: argument}
			continue
		}
		if SensitiveAssignment(argument) {
			classified[index] = classifiedArg{kind: argvEnvironmentValue, value: argument}
			continue
		}
		if _, credential, ok := bareCredential(argument); ok {
			classified[index] = classifiedArg{kind: argvInlineScheme, value: credential}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(argument), "bearer") || strings.EqualFold(strings.TrimSpace(argument), "basic") {
			expected = argvSecretValue
		}
	}
	return classified
}

func structurallyNamedArgument(name string) bool {
	return strings.HasPrefix(name, "-")
}

func sensitiveArgumentName(name string) bool {
	return SensitiveName(name) || strings.EqualFold(strings.TrimLeft(strings.TrimSpace(name), "-"), "key")
}

// Argv returns a redacted copy while preserving argument count and boundaries.
func (r Redactor) Argv(argv []string, sensitiveIndexes ...int) []string {
	explicit := indexSet(len(argv), sensitiveIndexes)
	classified := classifyArgv(argv)
	out := make([]string, len(argv))
	for index, argument := range argv {
		if _, sensitive := explicit[index]; sensitive {
			out[index] = Redacted
			continue
		}
		item := classified[index]
		switch item.kind {
		case argvSecretValue:
			out[index] = Redacted
		case argvHeaderValue:
			out[index] = redactHeaderArgument(r, argument)
		case argvEnvironmentValue:
			out[index] = redactEnvironmentArgument(r, argument)
		case argvInlineSecret:
			out[index] = r.Text(item.name) + item.joiner + Redacted
		case argvInlineHeader:
			out[index] = r.Text(item.name) + item.joiner + redactHeaderArgument(r, item.value)
		case argvInlineEnvironment:
			out[index] = r.Text(item.name) + item.joiner + redactEnvironmentArgument(r, item.value)
		case argvAttachedUser:
			out[index] = item.name + item.joiner + Redacted
		case argvAttachedHeader:
			out[index] = item.name + item.joiner + redactHeaderArgument(r, item.value)
		case argvInlineScheme:
			out[index] = r.Text(argument)
		default:
			out[index] = r.Text(argument)
		}
	}
	return out
}

// SensitiveArgvIndexes returns indexes that contain inferred or explicitly
// declared secret values.
func SensitiveArgvIndexes(argv []string, sensitiveIndexes ...int) []int {
	indexes := indexSet(len(argv), sensitiveIndexes)
	classified := classifyArgv(argv)
	for index, item := range classified {
		switch item.kind {
		case argvSecretValue, argvInlineSecret, argvAttachedUser, argvInlineScheme:
			indexes[index] = struct{}{}
		case argvHeaderValue, argvInlineHeader, argvAttachedHeader:
			if SensitiveHeader(item.value) {
				indexes[index] = struct{}{}
			}
		case argvEnvironmentValue, argvInlineEnvironment:
			if SensitiveAssignment(item.value) {
				indexes[index] = struct{}{}
			}
		}
		if ContainsSecretText(argv[index]) {
			indexes[index] = struct{}{}
		}
	}
	out := make([]int, 0, len(indexes))
	for index := range indexes {
		out = append(out, index)
	}
	sort.Ints(out)
	return out
}

// SensitiveArgvValues extracts values whose repetition in subprocess output
// must also be redacted.
func SensitiveArgvValues(argv []string, sensitiveIndexes ...int) []string {
	explicit := indexSet(len(argv), sensitiveIndexes)
	classified := classifyArgv(argv)
	values := make([]string, 0, len(sensitiveIndexes)+4)
	for index, argument := range argv {
		if _, sensitive := explicit[index]; sensitive {
			values = append(values, secretDerivatives(argument)...)
		}
		item := classified[index]
		switch item.kind {
		case argvSecretValue, argvInlineSecret, argvAttachedUser:
			values = append(values, secretDerivatives(item.value)...)
		case argvHeaderValue, argvInlineHeader, argvAttachedHeader:
			values = append(values, sensitiveHeaderValues(item.value)...)
		case argvEnvironmentValue, argvInlineEnvironment:
			if value, sensitive := sensitiveEnvironmentValue(item.value); sensitive {
				values = append(values, secretDerivatives(value)...)
			}
		case argvInlineScheme:
			values = append(values, secretDerivatives(item.value)...)
		}
	}
	unique := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, found := unique[value]; found {
			continue
		}
		unique[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func indexSet(length int, indexes []int) map[int]struct{} {
	set := make(map[int]struct{}, len(indexes))
	for _, index := range indexes {
		if index >= 0 && index < length {
			set[index] = struct{}{}
		}
	}
	return set
}

func redactHeaderArgument(redactor Redactor, argument string) string {
	if !SensitiveHeader(argument) {
		return redactor.Text(argument)
	}
	return redactHeader(argument)
}

func redactEnvironmentArgument(redactor Redactor, argument string) string {
	name, _, found := strings.Cut(argument, "=")
	if found && SensitiveAssignment(argument) {
		return name + "=" + Redacted
	}
	return redactor.Text(argument)
}

func sensitiveHeaderValues(header string) []string {
	header = strings.Trim(header, " \t\"'")
	name, value, found := strings.Cut(header, ":")
	if !found || !SensitiveName(name) || benignNamedValue(name, value) {
		return nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	values := secretDerivatives(value)
	if _, credential, ok := bareCredential(value); ok {
		values = append(values, secretDerivatives(credential)...)
	}
	if strings.EqualFold(strings.TrimSpace(name), "cookie") || strings.EqualFold(strings.TrimSpace(name), "set-cookie") {
		for _, component := range strings.Split(value, ";") {
			if _, secret, found := strings.Cut(strings.TrimSpace(component), "="); found {
				values = append(values, secretDerivatives(secret)...)
			}
		}
	}
	return values
}

func sensitiveEnvironmentValue(argument string) (string, bool) {
	name, value, found := strings.Cut(argument, "=")
	return value, found && value != "" && SensitiveName(name) && !benignNamedValue(name, value)
}

func bareCredential(value string) (scheme, credential string, ok bool) {
	value = strings.TrimSpace(value)
	index := strings.IndexAny(value, " \t")
	if index <= 0 {
		return "", "", false
	}
	scheme = value[:index]
	if !strings.EqualFold(scheme, "bearer") && !strings.EqualFold(scheme, "basic") {
		return "", "", false
	}
	credential = strings.TrimSpace(value[index:])
	if credential == "" || strings.ContainsAny(credential, " \t\r\n") {
		return "", "", false
	}
	return scheme, credential, true
}

func secretDerivatives(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	if value == "" {
		return nil
	}
	values := []string{value}
	if _, suffix, found := strings.Cut(value, "="); found && suffix != "" {
		values = append(values, suffix)
	}
	if prefix, suffix, found := strings.Cut(value, ":"); found {
		if prefix != "" {
			values = append(values, prefix)
		}
		if suffix != "" {
			values = append(values, suffix)
		}
	}
	return values
}
