// Package record implements the strict Markdown/TOML envelope, deterministic
// revisions, canonical layout, inventory validation, and durable record store.
package record

import (
	"errors"
	"fmt"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

var (
	ErrInvalidEnvelope = errors.New("invalid record envelope")
	ErrUnknownField    = errors.New("unknown record field")
	ErrInvalidPath     = errors.New("invalid canonical record path")
	ErrRecordTooLarge  = errors.New("canonical record exceeds byte limit")
)

// Error carries one stable codec/layout diagnostic code.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// Diagnostic is one file-scoped inventory problem.
type Diagnostic struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (d Diagnostic) Error() string {
	if d.Path == "" {
		return fmt.Sprintf("%s: %s", d.Code, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", d.Path, d.Code, d.Message)
}

// ValidateRecordSize enforces MaxRecordBytes for canonical publication bytes.
func ValidateRecordSize(content []byte) error {
	if int64(len(content)) > MaxRecordBytes {
		return fmt.Errorf("canonical record is %d bytes; limit is %d: %w", len(content), MaxRecordBytes, ErrRecordTooLarge)
	}
	return nil
}

// Document joins a typed front matter value to its ordinary Markdown body.
// Path is experiments-root-relative POSIX syntax when loaded from an inventory.
type Document struct {
	Record   research.Record
	Body     string
	Path     string
	Revision string
}

func (d *Document) Kind() research.Kind {
	if d == nil || d.Record == nil {
		return research.KindUnknown
	}
	return d.Record.GetKind()
}

func (d *Document) ID() (research.ID, bool) {
	if d == nil || d.Record == nil {
		return research.ID{}, false
	}
	return d.Record.GetID()
}

// Clone returns a deep copy of a document and its typed record.
func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	return &Document{Record: research.Clone(d.Record), Body: d.Body, Path: d.Path, Revision: d.Revision}
}

// DiagnosticsForError expands schema validation failures into independent
// machine diagnostics while retaining one file path.
func DiagnosticsForError(path string, err error) []Diagnostic {
	if err == nil {
		return nil
	}
	if issues := research.IssuesFromError(err); len(issues) > 0 {
		out := make([]Diagnostic, 0, len(issues))
		for _, issue := range issues {
			message := issue.Message
			if issue.Field != "" {
				message = issue.Field + ": " + message
			}
			out = append(out, Diagnostic{Path: path, Code: issue.Code, Message: message})
		}
		return out
	}
	var coded *Error
	if errors.As(err, &coded) {
		return []Diagnostic{{Path: path, Code: coded.Code, Message: coded.Message}}
	}
	return []Diagnostic{{Path: path, Code: "record.io", Message: err.Error()}}
}
