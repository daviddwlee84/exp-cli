package record

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const Delimiter = "+++"

var (
	openingDelimiter = []byte(Delimiter + "\n")
	closingDelimiter = []byte("\n" + Delimiter + "\n")
)

// Decode parses and validates one complete canonical Markdown record.
func Decode(data []byte) (*Document, error) {
	return decodeWithValidator(data, research.Validate)
}

// DecodeImported parses a candidate migration document while permitting the
// UUIDv5 form. Callers must authenticate its migration extension against the
// archived source tree before treating the result as canonical.
func DecodeImported(data []byte) (*Document, error) {
	return decodeWithValidator(data, research.ValidateImported)
}

func decodeWithValidator(data []byte, validate func(research.Record) error) (*Document, error) {
	front, body, err := Split(data)
	if err != nil {
		return nil, err
	}
	raw, err := decodeRawFrontMatter(front)
	if err != nil {
		return nil, err
	}
	schema, err := exactSchemaSelector(raw)
	if err != nil {
		return nil, err
	}
	if err := preflightSchemaFields(raw, schema); err != nil {
		return nil, err
	}
	kind, err := research.KindForSchema(schema)
	if err != nil {
		return nil, &Error{Code: "record.schema", Message: err.Error(), Err: err}
	}
	record := newRecord(kind)
	if err := preflightFrontMatter(raw, reflect.TypeOf(record)); err != nil {
		return nil, err
	}
	metadata, err := toml.Decode(string(front), record)
	if err != nil {
		return nil, &Error{Code: "record.frontmatter", Message: fmt.Sprintf("decode %s front matter: %v", kind, err), Err: ErrInvalidEnvelope}
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		fields := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			name := key.String()
			if openContainerPath(name) {
				continue
			}
			fields = append(fields, name)
		}
		if len(fields) > 0 {
			return nil, &Error{Code: "record.unknown_field", Message: fmt.Sprintf("unknown field(s): %v", fields), Err: ErrUnknownField}
		}
	}
	if err := validate(record); err != nil {
		return nil, err
	}
	if err := research.ValidateCommitSafeText(string(body)); err != nil {
		code := "privacy.secret"
		var policy *research.PolicyError
		if errors.As(err, &policy) && policy.Code != "" {
			code = policy.Code
		}
		return nil, &Error{Code: code, Message: fmt.Sprintf("Markdown body is not commit-safe: %v", err), Err: err}
	}
	document := &Document{Record: record, Body: string(body)}
	revision, err := revisionWithEncoder(document, func(document *Document) ([]byte, error) {
		return encodeWithValidator(document, validate)
	})
	if err != nil {
		return nil, fmt.Errorf("compute normalized revision: %w", err)
	}
	document.Revision = revision
	return document, nil
}

// preflightSchemaFields keeps legacy decoders closed even though the in-memory
// model also carries fields introduced by a later schema version.
func preflightSchemaFields(raw map[string]any, schema research.Schema) error {
	var forbidden []string
	switch schema {
	case research.SchemaPlan:
		forbidden = []string{"idea", "primary_cluster", "classification", "dependencies", "resources", "utility"}
	case research.SchemaExperiment:
		forbidden = []string{"parents", "candidate_inputs"}
	case research.SchemaAttempt:
		forbidden = []string{"pool", "queue", "queue_revision", "lane", "dispatch_id", "base_commit", "head_commit", "change_set"}
	}
	for _, field := range forbidden {
		if _, found := raw[field]; found {
			return schemaPreflightError("record.unknown_field", field, fmt.Sprintf("field is not part of %s", schema), ErrUnknownField)
		}
	}
	return nil
}

func openContainerPath(name string) bool {
	return name == "extensions" || strings.HasPrefix(name, "extensions.") ||
		name == "external_refs.metadata" || strings.HasPrefix(name, "external_refs.metadata.")
}

var (
	timeValueType       = reflect.TypeOf(time.Time{})
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

type frontMatterField struct {
	typ      reflect.Type
	required bool
}

func decodeRawFrontMatter(front []byte) (map[string]any, error) {
	var raw map[string]any
	if _, err := toml.Decode(string(front), &raw); err != nil {
		return nil, &Error{Code: "record.frontmatter", Message: fmt.Sprintf("decode front matter: %v", err), Err: ErrInvalidEnvelope}
	}
	return raw, nil
}

func exactSchemaSelector(raw map[string]any) (research.Schema, error) {
	value, found := raw["schema"]
	if !found {
		var variants []string
		for key := range raw {
			if strings.EqualFold(key, "schema") {
				variants = append(variants, key)
			}
		}
		sort.Strings(variants)
		if len(variants) > 0 {
			return "", schemaPreflightError("record.unknown_field", variants[0], "field name must use exact canonical case", ErrUnknownField)
		}
		return "", schemaPreflightError("record.missing_field", "schema", "required field is missing", ErrInvalidEnvelope)
	}
	text, ok := value.(string)
	if !ok {
		return "", schemaPreflightError("record.field_type", "schema", "field must be a TOML string", ErrInvalidEnvelope)
	}
	return research.Schema(text), nil
}

func preflightFrontMatter(raw map[string]any, target reflect.Type) error {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target.Kind() != reflect.Struct {
		return schemaPreflightError("record.frontmatter", "", "schema target is not a struct", ErrInvalidEnvelope)
	}
	return preflightStruct(raw, target, "")
}

func preflightStruct(raw map[string]any, target reflect.Type, prefix string) error {
	fields := frontMatterFields(target)
	actual := make([]string, 0, len(raw))
	for key := range raw {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	for _, key := range actual {
		field, known := fields[key]
		fieldPath := joinFrontMatterPath(prefix, key)
		if !known {
			return schemaPreflightError("record.unknown_field", fieldPath, "field name is not part of the exact schema", ErrUnknownField)
		}
		if err := preflightValue(raw[key], field.typ, fieldPath); err != nil {
			return err
		}
	}

	required := make([]string, 0, len(fields))
	for name, field := range fields {
		if field.required {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	for _, name := range required {
		if _, found := raw[name]; !found {
			return schemaPreflightError("record.missing_field", joinFrontMatterPath(prefix, name), "required field is missing", ErrInvalidEnvelope)
		}
	}
	return nil
}

func frontMatterFields(target reflect.Type) map[string]frontMatterField {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	fields := make(map[string]frontMatterField)
	for index := 0; index < target.NumField(); index++ {
		field := target.Field(index)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("toml")
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			for embeddedName, embedded := range frontMatterFields(field.Type) {
				fields[embeddedName] = embedded
			}
			continue
		}
		if name == "" {
			name = field.Name
		}
		required := true
		for _, option := range parts[1:] {
			if option == "omitempty" {
				required = false
			}
		}
		fields[name] = frontMatterField{typ: field.Type, required: required}
	}
	return fields
}

func preflightValue(value any, target reflect.Type, field string) error {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target == timeValueType {
		timestamp, ok := value.(time.Time)
		if !ok {
			return schemaTypeError(field, "an offset datetime", value)
		}
		switch timestamp.Location().String() {
		case "datetime-local", "date-local", "time-local":
			return schemaPreflightError("timestamp.type", field, "core timestamp must be a TOML offset datetime", ErrInvalidEnvelope)
		}
		return nil
	}
	if reflect.PointerTo(target).Implements(textUnmarshalerType) {
		if _, ok := value.(string); !ok {
			return schemaTypeError(field, "a string", value)
		}
		return nil
	}

	switch target.Kind() {
	case reflect.Struct:
		table, ok := value.(map[string]any)
		if !ok {
			return schemaTypeError(field, "a table", value)
		}
		return preflightStruct(table, target, field)
	case reflect.Map:
		if _, ok := value.(map[string]any); !ok {
			return schemaTypeError(field, "a table", value)
		}
		// Extensions and ExternalRef metadata are deliberately open recursively.
		return nil
	case reflect.Slice, reflect.Array:
		items := reflect.ValueOf(value)
		if !items.IsValid() || items.Kind() != reflect.Slice && items.Kind() != reflect.Array {
			return schemaTypeError(field, "an array", value)
		}
		for index := 0; index < items.Len(); index++ {
			if err := preflightValue(items.Index(index).Interface(), target.Elem(), fmt.Sprintf("%s[%d]", field, index)); err != nil {
				return err
			}
		}
		return nil
	case reflect.String:
		if _, ok := value.(string); !ok {
			return schemaTypeError(field, "a string", value)
		}
	case reflect.Bool:
		if _, ok := value.(bool); !ok {
			return schemaTypeError(field, "a boolean", value)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if _, ok := value.(int64); !ok {
			return schemaTypeError(field, "an integer", value)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		integer, ok := value.(int64)
		if !ok || integer < 0 {
			return schemaTypeError(field, "a non-negative integer", value)
		}
	case reflect.Float32, reflect.Float64:
		switch value.(type) {
		case int64, float64:
		default:
			return schemaTypeError(field, "a number", value)
		}
	case reflect.Interface:
		return nil
	default:
		return schemaPreflightError("record.field_type", field, fmt.Sprintf("unsupported schema field type %s", target), ErrInvalidEnvelope)
	}
	return nil
}

func schemaTypeError(field, expected string, value any) error {
	return schemaPreflightError("record.field_type", field, fmt.Sprintf("field must be %s, got %T", expected, value), ErrInvalidEnvelope)
}

func schemaPreflightError(code, field, message string, cause error) error {
	if field != "" {
		message = field + ": " + message
	}
	return &Error{Code: code, Message: message, Err: cause}
}

func joinFrontMatterPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func newRecord(kind research.Kind) research.Record {
	switch kind {
	case research.KindProject:
		return &research.Project{}
	case research.KindPolicy:
		return &research.Policy{}
	case research.KindIdea:
		return &research.Idea{}
	case research.KindResourcePool:
		return &research.ResourcePool{}
	case research.KindQueue:
		return &research.Queue{}
	case research.KindQueueAdvice:
		return &research.QueueAdvice{}
	case research.KindBattle:
		return &research.Battle{}
	case research.KindPlan:
		return &research.Plan{}
	case research.KindExperiment:
		return &research.Experiment{}
	case research.KindRun:
		return &research.Run{}
	case research.KindAttempt:
		return &research.Attempt{}
	case research.KindEvaluationSpec:
		return &research.EvaluationSpec{}
	case research.KindEvaluation:
		return &research.Evaluation{}
	case research.KindFinding:
		return &research.Finding{}
	case research.KindCandidate:
		return &research.Candidate{}
	case research.KindRelease:
		return &research.Release{}
	case research.KindPromotionSpec:
		return &research.PromotionSpec{}
	case research.KindPromotion:
		return &research.Promotion{}
	case research.KindDecision:
		return &research.Decision{}
	default:
		return nil
	}
}

// Split returns the TOML bytes and exact Markdown remainder. Envelope delimiters
// are not part of either result; every body byte after the closing delimiter LF
// is preserved, including leading blank lines.
func Split(data []byte) ([]byte, []byte, error) {
	if !utf8.Valid(data) {
		return nil, nil, &Error{Code: "record.utf8", Message: "record is not valid UTF-8", Err: ErrInvalidEnvelope}
	}
	if bytes.ContainsRune(data, '\r') {
		return nil, nil, &Error{Code: "record.line_endings", Message: "record must use LF line endings", Err: ErrInvalidEnvelope}
	}
	if !bytes.HasPrefix(data, openingDelimiter) {
		return nil, nil, &Error{Code: "record.delimiter", Message: "record must begin at byte zero with +++ followed by LF", Err: ErrInvalidEnvelope}
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		return nil, nil, &Error{Code: "record.final_lf", Message: "record must end with LF", Err: ErrInvalidEnvelope}
	}
	remainder := data[len(openingDelimiter):]
	index := bytes.Index(remainder, closingDelimiter)
	if index < 0 {
		return nil, nil, &Error{Code: "record.delimiter", Message: "record is missing a closing +++ delimiter line", Err: ErrInvalidEnvelope}
	}
	front := append([]byte(nil), remainder[:index]...)
	body := append([]byte(nil), remainder[index+len(closingDelimiter):]...)
	if len(bytes.TrimSpace(front)) == 0 {
		return nil, nil, &Error{Code: "record.frontmatter", Message: "front matter is empty", Err: ErrInvalidEnvelope}
	}
	return front, body, nil
}

// PeekSchema reads only enough structure to classify a potential PROJECT marker.
func PeekSchema(data []byte) (research.Schema, error) {
	front, _, err := Split(data)
	if err != nil {
		return "", err
	}
	raw, err := decodeRawFrontMatter(front)
	if err != nil {
		return "", err
	}
	return exactSchemaSelector(raw)
}
