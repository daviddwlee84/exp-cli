package record

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const revisionPrefix = "sha256:"

// Encode validates, normalizes, and deterministically encodes one document.
// Markdown body bytes are not reflowed or trimmed; only a missing final LF is added.
func Encode(document *Document) ([]byte, error) {
	if document == nil || document.Record == nil {
		return nil, &Error{Code: "record.nil", Message: "document or typed record is nil", Err: ErrInvalidEnvelope}
	}
	if !utf8.ValidString(document.Body) || strings.ContainsRune(document.Body, '\x00') {
		return nil, &Error{Code: "record.body", Message: "Markdown body must be valid UTF-8 without NUL", Err: ErrInvalidEnvelope}
	}
	if strings.ContainsRune(document.Body, '\r') {
		return nil, &Error{Code: "record.line_endings", Message: "Markdown body must use LF line endings", Err: ErrInvalidEnvelope}
	}
	if err := research.ValidateCommitSafeText(document.Body); err != nil {
		return nil, &Error{Code: "record.body_privacy", Message: fmt.Sprintf("Markdown body is not safe to commit: %v", err), Err: err}
	}
	if err := research.Validate(document.Record); err != nil {
		return nil, err
	}
	normalized := research.Clone(document.Record)
	if normalized == nil {
		return nil, &Error{Code: "record.type", Message: fmt.Sprintf("unsupported record type %T", document.Record), Err: ErrInvalidEnvelope}
	}
	research.Normalize(normalized)
	if err := research.Validate(normalized); err != nil {
		return nil, err
	}
	var front bytes.Buffer
	encoder := toml.NewEncoder(&front)
	if err := encoder.Encode(normalized); err != nil {
		return nil, &Error{Code: "record.frontmatter", Message: fmt.Sprintf("encode front matter: %v", err), Err: ErrInvalidEnvelope}
	}
	if front.Len() == 0 || front.Bytes()[front.Len()-1] != '\n' {
		front.WriteByte('\n')
	}
	var content bytes.Buffer
	content.WriteString(Delimiter)
	content.WriteByte('\n')
	content.Write(front.Bytes())
	content.WriteString(Delimiter)
	content.WriteByte('\n')
	content.WriteString(document.Body)
	if content.Len() == 0 || content.Bytes()[content.Len()-1] != '\n' {
		content.WriteByte('\n')
	}
	return content.Bytes(), nil
}

// Revision computes the normalized optimistic revision; it is never persisted.
func Revision(document *Document) (string, error) {
	content, err := Encode(document)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return revisionPrefix + hex.EncodeToString(digest[:]), nil
}

// ValidRevision reports whether value is a canonical sha256 revision.
func ValidRevision(value string) bool {
	if !strings.HasPrefix(value, revisionPrefix) || len(value) != len(revisionPrefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, revisionPrefix))
	return err == nil && value == strings.ToLower(value)
}
