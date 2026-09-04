package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/config"
	"golang.org/x/term"
)

const (
	colorAuto   = "auto"
	colorAlways = "always"
	colorNever  = "never"

	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

// cliStyle is the only component allowed to introduce ANSI into CLI output.
// Callers must pass text through safeHumanOutput or safeDiagnosticText before
// invoking a role method with dynamic content.
type cliStyle struct {
	enabled bool
}

func validateColorMode(mode string) error {
	switch mode {
	case colorAuto, colorAlways, colorNever:
		return nil
	default:
		return fmt.Errorf("invalid --color value %q: use auto, always, or never", mode)
	}
}

type colorFlagValue struct {
	target *string
}

func (value colorFlagValue) String() string {
	if value.target == nil || *value.target == "" {
		return colorAuto
	}
	return *value.target
}

func (value colorFlagValue) Set(mode string) error {
	if err := validateColorMode(mode); err != nil {
		return err
	}
	if value.target != nil {
		*value.target = mode
	}
	return nil
}

func (colorFlagValue) Type() string { return "string" }

func (app *App) applyEffectiveColor(configuration *config.Result) error {
	if app == nil || app.colorExplicit || configuration == nil {
		return nil
	}
	mode := string(configuration.Effective.UI.Color)
	if err := validateColorMode(mode); err != nil {
		return fmt.Errorf("invalid effective ui.color value %q", mode)
	}
	app.colorMode = mode
	return nil
}

func styleForWriter(writer io.Writer, mode string) cliStyle {
	return styleForWriterWithTerminal(writer, mode, func(file *os.File) bool {
		return term.IsTerminal(int(file.Fd()))
	})
}

func styleForWriterWithTerminal(writer io.Writer, mode string, isTerminal func(*os.File) bool) cliStyle {
	switch mode {
	case colorAlways:
		return cliStyle{enabled: true}
	case colorNever:
		return cliStyle{}
	}
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return cliStyle{}
	}
	file, ok := writer.(*os.File)
	return cliStyle{enabled: ok && isTerminal != nil && isTerminal(file)}
}

func (style cliStyle) paint(code, text string) string {
	if !style.enabled || text == "" {
		return text
	}
	return code + text + ansiReset
}

func (style cliStyle) title(text string) string   { return style.paint(ansiBold+ansiCyan, text) }
func (style cliStyle) header(text string) string  { return style.paint(ansiBold+ansiCyan, text) }
func (style cliStyle) prompt(text string) string  { return style.paint(ansiBold+ansiCyan, text) }
func (style cliStyle) label(text string) string   { return style.paint(ansiDim, text) }
func (style cliStyle) success(text string) string { return style.paint(ansiGreen, text) }
func (style cliStyle) warning(text string) string { return style.paint(ansiYellow, text) }
func (style cliStyle) danger(text string) string  { return style.paint(ansiBold+ansiRed, text) }
func (style cliStyle) review(text string) string  { return style.paint(ansiMagenta, text) }
func (style cliStyle) code(text string) string    { return style.paint(ansiCyan, text) }
func (style cliStyle) strong(text string) string  { return style.paint(ansiBold, text) }

// domainState applies meaning rather than a color tied to any one record type.
// Healthy terminal states are success, actionable transitional states are
// warnings, hard failures are danger, and explicit human gates are review.
func (style cliStyle) domainState(state, text string) string {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(state)), "-", "_")
	switch normalized {
	case "accepted", "active", "adopted", "built_in", "built_in_native", "clean", "closed", "compatible", "complete", "completed", "concluded", "current", "enabled", "healthy", "included", "merged", "passed", "published", "qualified", "ready", "recovered", "registered", "resolvable", "succeeded", "supported", "valid", "validated":
		return style.success(text)
	case "abandoned", "assisted", "cancelled", "capture", "capturing", "degraded", "developing", "dirty", "dismissed", "draft", "dropped", "excluded", "fallback", "inconclusive", "installed_not_probed", "limited", "open", "partial", "paused", "pending", "planned", "preempted", "queued", "retired", "rolled_back", "running", "saturated", "stale", "started", "starting", "superseded", "uncertain", "unknown", "untrusted":
		return style.warning(text)
	case "blocked", "conflict", "error", "errored", "failed", "incompatible", "invalid", "misconfigured", "missing", "orphaned", "out_of_memory", "refuted", "rejected", "timed_out", "unavailable", "unsafe", "unsupported":
		return style.danger(text)
	case "human_review", "manual", "needs_review", "proposed", "review", "review_required":
		return style.review(text)
	default:
		return text
	}
}

func (style cliStyle) readinessState(text string) string {
	return style.domainState(text, text)
}

func (app *App) outStyle() cliStyle {
	if app == nil {
		return cliStyle{}
	}
	return styleForWriter(app.Out, app.colorMode)
}

func (app *App) errStyle() cliStyle {
	if app == nil {
		return cliStyle{}
	}
	return styleForWriter(app.Err, app.colorMode)
}

// renderSemanticHuman receives already-sanitized human text and adds styles
// without changing its printable content or spacing.
func renderSemanticHuman(body string, style cliStyle) string {
	if !style.enabled || body == "" {
		return body
	}
	parts := strings.SplitAfter(body, "\n")
	var output strings.Builder
	output.Grow(len(body) + len(parts)*8)
	for _, part := range parts {
		if part == "" {
			continue
		}
		line := strings.TrimSuffix(part, "\n")
		newline := ""
		if len(part) != len(line) {
			newline = "\n"
		}
		if looksLikeTableHeader(line) {
			output.WriteString(paintHeaderColumns(line, style))
		} else {
			output.WriteString(paintHumanLine(line, style))
		}
		output.WriteString(newline)
	}
	return output.String()
}

func looksLikeTableHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || (!strings.Contains(line, "\t") && !strings.Contains(line, "  ")) {
		return false
	}
	letters := 0
	for _, character := range trimmed {
		switch {
		case unicode.IsLetter(character):
			letters++
			if unicode.IsLower(character) {
				return false
			}
		case unicode.IsDigit(character), unicode.IsSpace(character), character == '_', character == '-', character == '/':
		default:
			return false
		}
	}
	return letters > 0
}

func paintHeaderColumns(line string, style cliStyle) string {
	var output strings.Builder
	for index := 0; index < len(line); {
		r, size := utf8.DecodeRuneInString(line[index:])
		if unicode.IsSpace(r) {
			output.WriteString(line[index : index+size])
			index += size
			continue
		}
		start := index
		for index < len(line) {
			r, size = utf8.DecodeRuneInString(line[index:])
			if unicode.IsSpace(r) {
				break
			}
			index += size
		}
		output.WriteString(style.header(line[start:index]))
	}
	return output.String()
}

func paintHumanLine(line string, style cliStyle) string {
	if line == "" {
		return line
	}
	for _, prefix := range []struct {
		text string
		role func(string) string
	}{
		{"ERROR", style.danger},
		{"WARNING", style.warning},
		{"INFO", style.label},
	} {
		if line == prefix.text || strings.HasPrefix(line, prefix.text+" ") {
			line = prefix.role(prefix.text) + line[len(prefix.text):]
			break
		}
	}
	if colon := strings.IndexByte(stripANSI(line), ':'); colon > 0 && colon <= 32 {
		plain := stripANSI(line)
		label := plain[:colon+1]
		if !strings.ContainsAny(label, "\t\n") && !strings.HasPrefix(line, "\x1b[") {
			line = style.label(line[:colon+1]) + line[colon+1:]
		}
	}
	return paintSemanticTokens(line, style)
}

func paintSemanticTokens(line string, style cliStyle) string {
	var output strings.Builder
	output.Grow(len(line) + 16)
	for index := 0; index < len(line); {
		if end := ansiSequenceEnd(line, index); end > index {
			output.WriteString(line[index:end])
			index = end
			continue
		}
		r, size := utf8.DecodeRuneInString(line[index:])
		if !semanticTokenRune(r) {
			output.WriteString(line[index : index+size])
			index += size
			continue
		}
		start := index
		for index < len(line) {
			r, size = utf8.DecodeRuneInString(line[index:])
			if !semanticTokenRune(r) {
				break
			}
			index += size
		}
		token := line[start:index]
		output.WriteString(style.domainState(token, token))
	}
	return output.String()
}

func semanticTokenRune(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' || character == '-'
}

func renderCobraHelp(body string, style cliStyle) string {
	if !style.enabled || body == "" {
		return body
	}
	headings := map[string]bool{
		"Usage:": true, "Aliases:": true, "Examples:": true,
		"Available Commands:": true, "Flags:": true, "Global Flags:": true,
		"Additional help topics:": true,
	}
	section := ""
	lines := strings.Split(body, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if headings[trimmed] {
			section = trimmed
			lines[index] = style.header(line)
			continue
		}
		if trimmed == "" {
			section = ""
			continue
		}
		switch section {
		case "Available Commands:", "Additional help topics:", "Flags:", "Global Flags:":
			lines[index] = paintHelpFirstColumn(line, style)
		}
	}
	return strings.Join(lines, "\n")
}

func paintHelpFirstColumn(line string, style cliStyle) string {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent == 0 || indent >= len(line) {
		return line
	}
	remainder := line[indent:]
	gap := strings.Index(remainder, "  ")
	if gap <= 0 {
		return line[:indent] + style.code(remainder)
	}
	return line[:indent] + style.code(remainder[:gap]) + remainder[gap:]
}

// renderGuideMarkdown performs a deliberately small terminal-only styling pass.
// It never wraps or otherwise rewrites Markdown, so stripping ANSI recovers the
// embedded bytes exactly.
func renderGuideMarkdown(body string, style cliStyle) string {
	if !style.enabled || body == "" {
		return body
	}
	parts := strings.SplitAfter(body, "\n")
	var output strings.Builder
	inFence := false
	for _, part := range parts {
		if part == "" {
			continue
		}
		line := strings.TrimSuffix(part, "\n")
		newline := part[len(line):]
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			output.WriteString(style.label(line))
			inFence = !inFence
		case inFence:
			output.WriteString(style.code(line))
		case strings.HasPrefix(trimmed, "#"):
			output.WriteString(style.title(line))
		default:
			output.WriteString(renderGuideInline(line, style))
		}
		output.WriteString(newline)
	}
	return output.String()
}

func renderGuideInline(line string, style cliStyle) string {
	var output strings.Builder
	for index := 0; index < len(line); {
		if strings.HasPrefix(line[index:], "**") {
			if end := strings.Index(line[index+2:], "**"); end >= 0 {
				end += index + 4
				output.WriteString(style.strong(line[index:end]))
				index = end
				continue
			}
		}
		if line[index] == '`' {
			if end := strings.IndexByte(line[index+1:], '`'); end >= 0 {
				end += index + 2
				output.WriteString(style.code(line[index:end]))
				index = end
				continue
			}
		}
		output.WriteByte(line[index])
		index++
	}
	return output.String()
}
