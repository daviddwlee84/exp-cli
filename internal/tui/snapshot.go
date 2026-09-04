// Package tui implements exp's read-only terminal presentation model.
//
// Domain and CLI services stay outside this package. The model accepts only
// immutable display snapshots through injected, cancellable callbacks.
package tui

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// ErrUnavailable reports that the interactive terminal runner is not compiled
// for the current platform. Canonical and noninteractive CLI commands remain
// available when this error is returned.
var ErrUnavailable = errors.New("exp ui is unavailable on this platform")

// ViewID is one stable read-only tab identity.
type ViewID string

const (
	ViewWorkflow   ViewID = "workflow"
	ViewWorkspace  ViewID = "workspace"
	ViewTries      ViewID = "tries"
	ViewQueue      ViewID = "queue"
	ViewAttempts   ViewID = "attempts"
	ViewCandidates ViewID = "candidates"
	ViewReadiness  ViewID = "readiness"
)

var orderedViews = []ViewID{
	ViewWorkflow,
	ViewWorkspace,
	ViewTries,
	ViewQueue,
	ViewAttempts,
	ViewCandidates,
	ViewReadiness,
}

// Views returns the stable tab order as a defensive copy.
func Views() []ViewID { return append([]ViewID(nil), orderedViews...) }

// ViewTitle returns the compact human title for a tab.
func ViewTitle(view ViewID) string {
	switch view {
	case ViewWorkflow:
		return "Workflow"
	case ViewWorkspace:
		return "Workspace"
	case ViewTries:
		return "Tries"
	case ViewQueue:
		return "Queue"
	case ViewAttempts:
		return "Attempts"
	case ViewCandidates:
		return "Candidates"
	case ViewReadiness:
		return "Readiness"
	default:
		return "Unknown"
	}
}

func validView(view ViewID) bool {
	for _, candidate := range orderedViews {
		if candidate == view {
			return true
		}
	}
	return false
}

// RowSpec is constructor input for an immutable display Row. Text is stripped
// of terminal control sequences before it crosses the TUI boundary.
type RowSpec struct {
	ID          string
	Title       string
	State       string
	Detail      string
	Reason      string
	Remediation string
}

// Row is an immutable, terminal-safe display row.
type Row struct {
	id          string
	title       string
	state       string
	detail      string
	reason      string
	remediation string
}

// NewRow sanitizes and copies a row value.
func NewRow(spec RowSpec) Row {
	return Row{
		id:          safeText(spec.ID),
		title:       safeText(spec.Title),
		state:       safeText(spec.State),
		detail:      safeText(spec.Detail),
		reason:      safeText(spec.Reason),
		remediation: safeText(spec.Remediation),
	}
}

func (row Row) ID() string          { return row.id }
func (row Row) Title() string       { return row.title }
func (row Row) State() string       { return row.state }
func (row Row) Detail() string      { return row.detail }
func (row Row) Reason() string      { return row.reason }
func (row Row) Remediation() string { return row.remediation }

// Section is an immutable group of rows in one view.
type Section struct {
	title string
	empty string
	rows  []Row
}

// NewSection copies all rows. Empty describes a successful zero-row result.
func NewSection(title, empty string, rows ...Row) Section {
	return Section{
		title: safeText(title),
		empty: safeText(empty),
		rows:  append([]Row(nil), rows...),
	}
}

func (section Section) Title() string { return section.title }
func (section Section) Empty() string { return section.empty }
func (section Section) Rows() []Row   { return append([]Row(nil), section.rows...) }

func cloneSection(section Section) Section {
	section.rows = append([]Row(nil), section.rows...)
	return section
}

type viewMetadata struct {
	observedAt time.Time
	partial    bool
	note       string
}

// Snapshot is an immutable display projection. It contains no domain objects,
// provider-native authority, callback, or writable service reference.
type Snapshot struct {
	workspace     string
	observedAt    time.Time
	sections      map[ViewID][]Section
	partial       bool
	note          string
	metadata      map[ViewID]viewMetadata
	monochrome    bool
	hasMonochrome bool
}

// NewSnapshot copies every section and row into an immutable value.
func NewSnapshot(workspace string, observedAt time.Time, sections map[ViewID][]Section, partial bool, note string) Snapshot {
	snapshot := Snapshot{
		workspace:  safeText(workspace),
		observedAt: observedAt.UTC(),
		sections:   make(map[ViewID][]Section, len(sections)),
		partial:    partial,
		note:       safeText(note),
		metadata:   make(map[ViewID]viewMetadata, len(sections)),
	}
	for view, values := range sections {
		if !validView(view) {
			continue
		}
		copied := make([]Section, len(values))
		for index, section := range values {
			copied[index] = cloneSection(section)
		}
		snapshot.sections[view] = copied
		snapshot.metadata[view] = viewMetadata{observedAt: snapshot.observedAt, partial: partial, note: snapshot.note}
	}
	return snapshot
}

func (snapshot Snapshot) Workspace() string     { return snapshot.workspace }
func (snapshot Snapshot) ObservedAt() time.Time { return snapshot.observedAt }
func (snapshot Snapshot) Partial() bool         { return snapshot.partial }
func (snapshot Snapshot) Note() string          { return snapshot.note }

// WithMonochrome returns a defensive copy carrying the resolved presentation
// policy for frames rendered after this snapshot is accepted.
func (snapshot Snapshot) WithMonochrome(monochrome bool) Snapshot {
	copy := snapshot.clone()
	copy.monochrome = monochrome
	copy.hasMonochrome = true
	return copy
}

// Monochrome reports the resolved presentation policy when the producer knew it.
func (snapshot Snapshot) Monochrome() (bool, bool) {
	return snapshot.monochrome, snapshot.hasMonochrome
}

// Sections returns a deep defensive copy for one view.
func (snapshot Snapshot) Sections(view ViewID) []Section {
	values := snapshot.sections[view]
	copied := make([]Section, len(values))
	for index, section := range values {
		copied[index] = cloneSection(section)
	}
	return copied
}

func (snapshot Snapshot) clone() Snapshot {
	copy := Snapshot{
		workspace:     snapshot.workspace,
		observedAt:    snapshot.observedAt,
		sections:      make(map[ViewID][]Section, len(snapshot.sections)),
		partial:       snapshot.partial,
		note:          snapshot.note,
		metadata:      make(map[ViewID]viewMetadata, len(snapshot.metadata)),
		monochrome:    snapshot.monochrome,
		hasMonochrome: snapshot.hasMonochrome,
	}
	for view, sections := range snapshot.sections {
		values := make([]Section, len(sections))
		for index, section := range sections {
			values[index] = cloneSection(section)
		}
		copy.sections[view] = values
	}
	for view, metadata := range snapshot.metadata {
		copy.metadata[view] = metadata
	}
	return copy
}

func (snapshot Snapshot) withView(view ViewID, sections []Section, observedAt time.Time, partial bool, note string) Snapshot {
	copy := snapshot.clone()
	if copy.sections == nil {
		copy.sections = make(map[ViewID][]Section)
	}
	if copy.metadata == nil {
		copy.metadata = make(map[ViewID]viewMetadata)
	}
	values := make([]Section, len(sections))
	for index, section := range sections {
		values[index] = cloneSection(section)
	}
	copy.sections[view] = values
	copy.metadata[view] = viewMetadata{observedAt: observedAt.UTC(), partial: partial, note: safeText(note)}
	return copy
}

func (snapshot Snapshot) metadataFor(view ViewID) viewMetadata {
	if metadata, found := snapshot.metadata[view]; found {
		return metadata
	}
	return viewMetadata{observedAt: snapshot.observedAt, partial: snapshot.partial, note: snapshot.note}
}

func (snapshot Snapshot) rowCount(view ViewID) int {
	count := 0
	for _, section := range snapshot.sections[view] {
		count += len(section.rows)
	}
	return count
}

// Identity binds an asynchronous operation to the exact workspace selector and
// active view that initiated it.
type Identity struct {
	workspace string
	view      ViewID
}

func NewIdentity(workspace string, view ViewID) Identity {
	if !validView(view) {
		view = ViewWorkflow
	}
	workspace = safeTUIDiagnostic(workspace)
	if workspace == "" {
		workspace = "current"
	}
	return Identity{workspace: workspace, view: view}
}

func (identity Identity) Workspace() string { return identity.workspace }
func (identity Identity) View() ViewID      { return identity.view }

// Request is immutable metadata passed to every asynchronous callback.
type Request struct {
	generation uint64
	identity   Identity
	observedAt time.Time
}

func NewRequest(generation uint64, identity Identity, observedAt time.Time) Request {
	return Request{generation: generation, identity: identity, observedAt: observedAt.UTC()}
}

func (request Request) Generation() uint64    { return request.generation }
func (request Request) Identity() Identity    { return request.identity }
func (request Request) ObservedAt() time.Time { return request.observedAt }
func (request Request) Workspace() string     { return request.identity.workspace }
func (request Request) View() ViewID          { return request.identity.view }

// Response carries the initiating request metadata verbatim. Snapshot has its
// own data-observation timestamp; Request.ObservedAt identifies the request and
// participates in stale-response rejection.
type Response struct {
	request  Request
	snapshot Snapshot
	err      error
}

func Success(request Request, snapshot Snapshot) Response {
	return Response{request: request, snapshot: snapshot.clone()}
}

func Failure(request Request, err error) Response {
	if err == nil {
		err = errors.New("asynchronous read failed without an error")
	}
	return Response{request: request, err: err}
}

func (response Response) Request() Request   { return response.request }
func (response Response) Snapshot() Snapshot { return response.snapshot.clone() }
func (response Response) Err() error         { return response.err }

// Loader is the complete injected service boundary. Implementations must honor
// cancellation and return Success or Failure with the exact supplied Request.
type Loader func(context.Context, Request) Response

// Callbacks separate local canonical/operation reads from explicit live probes.
type Callbacks struct {
	LoadLocal Loader
	ProbeLive Loader
}

func safeText(value string) string {
	value = ansi.Strip(value)
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
			return ' '
		}
		return character
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	const maxDisplayRunes = 4096
	if utf8.RuneCountInString(value) > maxDisplayRunes {
		value = string([]rune(value)[:maxDisplayRunes])
	}
	return strings.TrimSpace(value)
}
