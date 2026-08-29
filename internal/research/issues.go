package research

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/safex"
)

var ErrInvalidRecord = errors.New("invalid research record")

// Issue is one stable validation diagnostic. Code is suitable for machine output.
type Issue struct {
	Code    string
	Field   string
	Message string
}

func (i Issue) Error() string {
	location := i.Field
	if location == "" {
		location = "record"
	}
	return fmt.Sprintf("%s (%s): %s", location, i.Code, i.Message)
}

// ValidationError preserves every independent issue in deterministic order.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ErrInvalidRecord.Error()
	}
	parts := make([]string, len(e.Issues))
	for index, issue := range e.Issues {
		parts[index] = issue.Error()
	}
	return strings.Join(parts, "; ")
}

func (e *ValidationError) Unwrap() error { return ErrInvalidRecord }

// IssuesFromError extracts validation issues without discarding wrapped context.
func IssuesFromError(err error) []Issue {
	var validation *ValidationError
	if !errors.As(err, &validation) {
		return nil
	}
	return append([]Issue(nil), validation.Issues...)
}

type issueCollector struct{ issues []Issue }

func (c *issueCollector) add(code, field, format string, args ...any) {
	redactor := safex.NewRedactor()
	field, _ = redactor.SafeDiagnostic(field, 4<<10)
	message, _ := redactor.SafeDiagnostic(fmt.Sprintf(format, args...), 16<<10)
	c.issues = append(c.issues, Issue{Code: code, Field: field, Message: message})
}

func (c *issueCollector) err() error {
	if len(c.issues) == 0 {
		return nil
	}
	sort.SliceStable(c.issues, func(i, j int) bool {
		if c.issues[i].Field != c.issues[j].Field {
			return c.issues[i].Field < c.issues[j].Field
		}
		if c.issues[i].Code != c.issues[j].Code {
			return c.issues[i].Code < c.issues[j].Code
		}
		return c.issues[i].Message < c.issues[j].Message
	})
	return &ValidationError{Issues: append([]Issue(nil), c.issues...)}
}
