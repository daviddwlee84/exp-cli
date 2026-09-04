package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type theme struct {
	monochrome bool
	title      lipgloss.Style
	active     lipgloss.Style
	dim        lipgloss.Style
	good       lipgloss.Style
	warn       lipgloss.Style
	bad        lipgloss.Style
	selected   lipgloss.Style
}

func newTheme(monochrome bool) theme {
	return theme{
		monochrome: monochrome,
		title:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		active:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5")),
		dim:        lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		good:       lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		warn:       lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		bad:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		selected:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
	}
}

func (style theme) render(value string, target lipgloss.Style) string {
	if style.monochrome || value == "" {
		return value
	}
	return target.Render(value)
}

func (style theme) state(value string) string {
	normalized := strings.ReplaceAll(strings.ToLower(value), "-", "_")
	switch normalized {
	case "active", "adopted", "built_in", "clean", "closed", "compatible", "complete", "concluded", "current", "enabled", "healthy", "passed", "ready", "registered", "succeeded", "supported", "valid", "validated":
		return style.render(value, style.good)
	case "blocked", "error", "failed", "incompatible", "invalid", "misconfigured", "missing", "out_of_memory", "refuted", "rejected", "timed_out", "unavailable", "unsafe", "unsupported":
		return style.render(value, style.bad)
	case "abandoned", "cancelled", "degraded", "dirty", "draft", "installed_not_probed", "open", "partial", "paused", "pending", "planned", "preempted", "queued", "retired", "running", "stale", "starting", "uncertain", "unknown", "unprobed":
		return style.render(value, style.warn)
	default:
		return value
	}
}

// View renders an immediate frame. It never calls a callback or touches a
// service, filesystem, subprocess, or provider.
func (model Model) View() string {
	width := model.width
	height := model.height
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	style := newTheme(model.monochrome)
	status := model.statuses[model.identity.view]

	header := []string{
		clip(style.render("exp ui", style.title)+"  "+style.render("read-only", style.dim), width),
	}
	workspace := model.identity.workspace
	if (model.hasLocal || model.hasProbe) && model.snapshot.workspace != "" {
		workspace = model.snapshot.workspace
	}
	header = append(header, clip("Workspace: "+workspace, width))
	header = append(header, packTokens(model.tabTokens(style), width)...)
	header = append(header, clip(model.statusLine(status, style), width))

	footerTokens := []string{"tab/←/→ views", "j/k move", "r refresh", "? help", "q/Ctrl-C quit"}
	if model.showHelp {
		footerTokens = []string{"? close help", "q/Ctrl-C quit"}
	}
	footer := packTokens(footerTokens, width)
	if len(footer) == 0 {
		footer = []string{""}
	}

	contentHeight := height - len(header) - len(footer)
	if contentHeight < 0 {
		contentHeight = 0
	}
	var content []string
	if model.showHelp {
		content = model.renderHelp(style, width)
	} else {
		content = model.renderCurrent(style, status, width)
	}
	content = fitLines(content, contentHeight, width)

	lines := make([]string, 0, len(header)+len(content)+len(footer))
	lines = append(lines, header...)
	lines = append(lines, content...)
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	lines = append(lines, footer...)
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = clip(lines[index], width)
	}
	return strings.Join(lines, "\n") + "\n"
}

func (model Model) statusLine(status viewStatus, style theme) string {
	metadata := model.snapshot.metadataFor(model.identity.view)
	badges := make([]string, 0, 3)
	switch status.state {
	case stateIdle:
		badges = append(badges, "[IDLE]")
	case stateLoading:
		if model.hasLocal || model.hasProbe {
			badges = append(badges, "[REFRESHING]")
		} else {
			badges = append(badges, "[LOADING]")
		}
	case stateReady:
		badges = append(badges, "[CURRENT]")
	case stateEmpty:
		badges = append(badges, "[EMPTY]")
	case stateError:
		badges = append(badges, "[ERROR]")
	case stateStale:
		badges = append(badges, "[STALE]")
	}
	if status.partial || metadata.partial {
		badges = append(badges, "[PARTIAL]")
	}
	for index, badge := range badges {
		switch badge {
		case "[CURRENT]":
			badges[index] = style.render(badge, style.good)
		case "[ERROR]":
			badges[index] = style.render(badge, style.bad)
		case "[STALE]", "[PARTIAL]", "[REFRESHING]", "[LOADING]":
			badges[index] = style.render(badge, style.warn)
		default:
			badges[index] = style.render(badge, style.dim)
		}
	}
	line := ViewTitle(model.identity.view) + " " + strings.Join(badges, " ")
	if !metadata.observedAt.IsZero() && (model.hasLocal || model.hasProbe) {
		line += "  observed " + formatObserved(metadata.observedAt)
	}
	return line
}

func (model Model) tabTokens(style theme) []string {
	tokens := make([]string, 0, len(orderedViews))
	for index, view := range orderedViews {
		label := fmt.Sprintf("%d:%s", index+1, ViewTitle(view))
		if view == model.identity.view {
			label = style.render("["+label+"]", style.active)
		}
		tokens = append(tokens, label)
	}
	return tokens
}

func (model Model) renderCurrent(style theme, status viewStatus, width int) []string {
	metadata := model.snapshot.metadataFor(model.identity.view)
	lines := make([]string, 0, 16)
	if status.state == stateLoading {
		message := "Loading local canonical, association, config, and operation summaries…"
		if model.identity.view == ViewReadiness && model.probe != nil {
			message = "Running explicit bounded read-only provider probes…"
		}
		lines = append(lines, clip(model.spinner.View()+" "+message, width))
	}
	if status.err != "" {
		prefix := "Error: "
		if status.state == stateStale {
			prefix = "Refresh failed; retained rows are stale: "
		}
		lines = append(lines, clip(style.render(prefix+status.err, style.bad), width))
	}
	if !model.hasLocal && !model.hasProbe {
		if status.state == stateError {
			lines = append(lines, "No snapshot is available.")
		}
		return lines
	}

	sections := model.snapshot.Sections(model.identity.view)
	if model.snapshot.rowCount(model.identity.view) == 0 {
		emptyLines := 0
		for _, section := range sections {
			empty := section.Empty()
			if empty == "" {
				empty = "No entries."
			}
			if section.Title() != "" {
				lines = append(lines, style.render(section.Title(), style.title))
			}
			lines = append(lines, style.render(empty, style.dim))
			emptyLines++
		}
		if emptyLines == 0 {
			lines = append(lines, style.render("No entries.", style.dim))
		}
		return lines
	}

	first, last := model.visibleRowRange()
	global := 0
	for _, section := range sections {
		sectionShown := false
		for _, row := range section.Rows() {
			if global >= first && global < last {
				if !sectionShown && section.Title() != "" {
					lines = append(lines, style.render(section.Title(), style.title))
					sectionShown = true
				}
				lines = append(lines, renderRow(row, global == model.selection, width, style)...)
			}
			global++
		}
	}
	if first > 0 {
		lines = append([]string{style.render(fmt.Sprintf("↑ %d earlier row(s)", first), style.dim)}, lines...)
	}
	remaining := model.snapshot.rowCount(model.identity.view) - last
	if remaining > 0 {
		lines = append(lines, style.render(fmt.Sprintf("↓ %d later row(s)", remaining), style.dim))
	}
	if metadata.note != "" {
		lines = append(lines, style.render(metadata.note, style.dim))
	}
	return lines
}

func renderRow(row Row, selected bool, width int, style theme) []string {
	marker := "  "
	if selected {
		marker = style.render("> ", style.selected)
	}
	title := row.Title()
	if title == "" {
		title = row.ID()
	}
	state := row.State()
	if width >= 64 {
		first := marker + title
		if state != "" {
			first += "  [" + style.state(state) + "]"
		}
		if row.Detail() != "" {
			first += "  " + row.Detail()
		}
		secondParts := make([]string, 0, 3)
		if row.ID() != "" && row.ID() != title {
			secondParts = append(secondParts, row.ID())
		}
		if row.Reason() != "" {
			secondParts = append(secondParts, "reason: "+row.Reason())
		}
		if row.Remediation() != "" {
			secondParts = append(secondParts, "next: "+row.Remediation())
		}
		lines := []string{clip(first, width)}
		if len(secondParts) > 0 {
			lines = append(lines, clip(style.render("  "+strings.Join(secondParts, " · "), style.dim), width))
		}
		return lines
	}

	lines := []string{clip(marker+title, width)}
	if state != "" {
		lines = append(lines, clip("  state: "+style.state(state), width))
	}
	if row.Detail() != "" {
		lines = append(lines, prefixedWrap("  ", row.Detail(), width)...)
	}
	if row.Reason() != "" {
		lines = append(lines, prefixedWrap("  reason: ", row.Reason(), width)...)
	}
	if row.Remediation() != "" {
		lines = append(lines, prefixedWrap("  next: ", row.Remediation(), width)...)
	}
	if row.ID() != "" && row.ID() != title {
		lines = append(lines, clip(style.render("  id: "+row.ID(), style.dim), width))
	}
	return lines
}

func (model Model) renderHelp(style theme, width int) []string {
	lines := []string{
		style.render("Read-only keys", style.title),
		"tab, right, l       next view",
		"shift-tab, left, h  previous view",
		"1..7                select a view",
		"up/down, j/k        move selection",
		"pgup/pgdown, g/G    move selection window",
		"r                   refresh current data",
		"?                   close this help",
		"q, Ctrl-C           cancel reads and quit",
		"",
		"No key mutates records, starts services, logs in, installs tools,",
		"opens an editor, or hands off to an external command.",
	}
	for index := range lines {
		lines[index] = clip(lines[index], width)
	}
	return lines
}

func packTokens(tokens []string, width int) []string {
	if width <= 0 || len(tokens) == 0 {
		return nil
	}
	const gap = "  "
	lines := make([]string, 0, 2)
	current := ""
	for _, token := range tokens {
		token = clip(token, width)
		candidate := token
		if current != "" {
			candidate = current + gap + token
		}
		if current != "" && ansi.StringWidth(candidate) > width {
			lines = append(lines, current)
			current = token
		} else {
			current = candidate
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func prefixedWrap(prefix, value string, width int) []string {
	if width <= 0 {
		return nil
	}
	available := width - ansi.StringWidth(prefix)
	if available < 1 {
		return []string{clip(prefix, width)}
	}
	wrapped := ansi.Wordwrap(value, available, " ")
	parts := strings.Split(wrapped, "\n")
	lines := make([]string, 0, len(parts))
	for index, part := range parts {
		linePrefix := prefix
		if index > 0 {
			linePrefix = strings.Repeat(" ", ansi.StringWidth(prefix))
		}
		lines = append(lines, clip(linePrefix+part, width))
	}
	return lines
}

func fitLines(lines []string, height, width int) []string {
	if height <= 0 {
		return nil
	}
	if len(lines) > height {
		lines = append([]string(nil), lines[:height]...)
		if height > 0 {
			lines[height-1] = clip("…", width)
		}
	}
	for index := range lines {
		lines[index] = clip(lines[index], width)
	}
	return lines
}

func clip(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(value, width, "…")
}

func formatObserved(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format("2006-01-02 15:04:05Z")
}
