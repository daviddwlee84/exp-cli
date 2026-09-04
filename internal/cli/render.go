package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	textwidth "golang.org/x/text/width"
)

// humanTable aligns sanitized cells by terminal display columns. ANSI control
// sequences contribute no width, combining marks contribute zero, and wide CJK
// characters contribute two columns.
type humanTable struct {
	headers    []string
	rows       [][]string
	style      cliStyle
	showHeader bool
	indent     string
}

func newHumanTable(headers ...string) *humanTable {
	return newStyledHumanTable(cliStyle{}, headers...)
}

func newHumanRows(columns int) *humanTable {
	if columns < 0 {
		columns = 0
	}
	return &humanTable{headers: make([]string, columns)}
}

func newStyledHumanTable(style cliStyle, headers ...string) *humanTable {
	table := &humanTable{headers: make([]string, len(headers)), style: style, showHeader: true}
	for index, header := range headers {
		table.headers[index] = normalizeTableCell(header)
	}
	return table
}

func (table *humanTable) Add(cells ...string) {
	if table == nil {
		return
	}
	row := make([]string, len(table.headers))
	for index := range row {
		if index < len(cells) {
			row[index] = normalizeTableCell(cells[index])
		}
	}
	table.rows = append(table.rows, row)
}

func (table *humanTable) Len() int {
	if table == nil {
		return 0
	}
	return len(table.rows)
}

func (table *humanTable) Indent(prefix string) *humanTable {
	if table != nil {
		table.indent = prefix
	}
	return table
}

func (table *humanTable) Render(writer io.Writer) error {
	if table == nil || len(table.headers) == 0 {
		return nil
	}
	widths := make([]int, len(table.headers))
	if table.showHeader {
		for index, header := range table.headers {
			widths[index] = displayWidth(header)
		}
	}
	for _, row := range table.rows {
		for index, cell := range row {
			if measured := displayWidth(cell); measured > widths[index] {
				widths[index] = measured
			}
		}
	}
	if table.showHeader {
		headers := make([]string, len(table.headers))
		for index, header := range table.headers {
			headers[index] = table.style.header(header)
		}
		if err := writeTableRow(writer, table.indent, headers, widths); err != nil {
			return err
		}
	}
	for _, row := range table.rows {
		if err := writeTableRow(writer, table.indent, row, widths); err != nil {
			return err
		}
	}
	return nil
}

func (table *humanTable) String() (string, error) {
	var output strings.Builder
	if err := table.Render(&output); err != nil {
		return "", err
	}
	return output.String(), nil
}

func writeTableRow(writer io.Writer, indent string, cells []string, widths []int) error {
	var row strings.Builder
	row.WriteString(indent)
	for index, cell := range cells {
		row.WriteString(cell)
		if index == len(cells)-1 {
			break
		}
		padding := widths[index] - displayWidth(cell) + 2
		if padding > 0 {
			row.WriteString(strings.Repeat(" ", padding))
		}
	}
	row.WriteByte('\n')
	_, err := io.WriteString(writer, row.String())
	return err
}

func normalizeTableCell(value string) string {
	value = safeHumanOutput(value)
	value = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(value)
	return strings.TrimSpace(value)
}

func displayWidth(value string) int {
	columns := 0
	for index := 0; index < len(value); {
		if end := ansiSequenceEnd(value, index); end > index {
			index = end
			continue
		}
		character, size := utf8.DecodeRuneInString(value[index:])
		columns += runeDisplayWidth(character)
		index += size
	}
	return columns
}

func runeDisplayWidth(character rune) int {
	switch {
	case character == utf8.RuneError:
		return 1
	case character == '\t':
		return 1
	case unicode.IsControl(character), unicode.Is(unicode.Mn, character), unicode.Is(unicode.Me, character), unicode.Is(unicode.Cf, character):
		return 0
	case character >= 0xFE00 && character <= 0xFE0F,
		character >= 0xE0100 && character <= 0xE01EF:
		return 0
	case character >= 0x1F300 && character <= 0x1FAFF,
		character >= 0x1F1E6 && character <= 0x1F1FF:
		return 2
	}
	switch textwidth.LookupRune(character).Kind() {
	case textwidth.EastAsianWide, textwidth.EastAsianFullwidth:
		return 2
	default:
		return 1
	}
}

// ansiSequenceEnd recognizes terminal CSI sequences produced by cliStyle. It
// intentionally does not interpret arbitrary terminal protocols.
func ansiSequenceEnd(value string, start int) int {
	if start < 0 || start+2 > len(value) || value[start] != '\x1b' || value[start+1] != '[' {
		return start
	}
	for index := start + 2; index < len(value); index++ {
		if value[index] >= 0x40 && value[index] <= 0x7e {
			return index + 1
		}
	}
	return start
}

func stripANSI(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for index := 0; index < len(value); {
		if end := ansiSequenceEnd(value, index); end > index {
			index = end
			continue
		}
		character, size := utf8.DecodeRuneInString(value[index:])
		output.WriteRune(character)
		index += size
	}
	return output.String()
}

// truncateDisplay preserves complete ANSI sequences and combining marks while
// limiting printable terminal width. A truncation marker occupies one column.
func truncateDisplay(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if displayWidth(value) <= limit {
		return value
	}
	target := limit - 1
	var output strings.Builder
	used := 0
	hasANSI := false
	for index := 0; index < len(value); {
		if end := ansiSequenceEnd(value, index); end > index {
			hasANSI = true
			output.WriteString(value[index:end])
			index = end
			continue
		}
		character, size := utf8.DecodeRuneInString(value[index:])
		characterWidth := runeDisplayWidth(character)
		if characterWidth > 0 && used+characterWidth > target {
			break
		}
		output.WriteString(value[index : index+size])
		used += characterWidth
		index += size
	}
	output.WriteRune('…')
	if hasANSI && !strings.HasSuffix(output.String(), ansiReset) {
		output.WriteString(ansiReset)
	}
	return output.String()
}

func mustRenderTable(table *humanTable) string {
	output, err := table.String()
	if err != nil {
		panic(fmt.Sprintf("render in-memory human table: %v", err))
	}
	return output
}
