package skill

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// CommandsReferencePath is the embedded, generated command reference.
const CommandsReferencePath = "references/commands.md"

// CommandMetadata is the cycle-free description accepted from the CLI layer.
// The CLI owns traversal of its command framework; this package only validates
// and renders plain metadata.
type CommandMetadata struct {
	Path    string         `json:"path"`
	Use     string         `json:"use"`
	Summary string         `json:"summary"`
	Hidden  bool           `json:"hidden,omitempty"`
	Flags   []FlagMetadata `json:"flags,omitempty"`
}

// FlagMetadata describes one visible command option.
type FlagMetadata struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Usage     string `json:"usage"`
}

// CommandReferenceCheck reports an exact deterministic content comparison.
type CommandReferenceCheck struct {
	Current      bool   `json:"current"`
	ExpectedHash string `json:"expected_hash"`
	ActualHash   string `json:"actual_hash"`
	Expected     string `json:"-"`
}

// GenerateCommandReference renders visible command metadata in deterministic
// path and flag-name order. It deliberately has no dependency on internal/cli,
// Cobra, or another command framework.
func GenerateCommandReference(commands []CommandMetadata) (string, error) {
	visible := make([]CommandMetadata, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		if command.Hidden {
			continue
		}
		if err := validateRawCommandMetadata(command); err != nil {
			return "", err
		}
		command.Path = strings.TrimSpace(command.Path)
		command.Use = strings.TrimSpace(command.Use)
		command.Summary = strings.TrimSpace(command.Summary)
		if err := validateCommandMetadata(command); err != nil {
			return "", err
		}
		if _, duplicate := seen[command.Path]; duplicate {
			return "", fmt.Errorf("duplicate command metadata for %q", command.Path)
		}
		seen[command.Path] = struct{}{}
		command.Flags = append([]FlagMetadata(nil), command.Flags...)
		for index := range command.Flags {
			if err := validateRawFlagMetadata(command.Path, command.Flags[index]); err != nil {
				return "", err
			}
			command.Flags[index].Name = strings.TrimLeft(strings.TrimSpace(command.Flags[index].Name), "-")
			command.Flags[index].Shorthand = strings.TrimLeft(strings.TrimSpace(command.Flags[index].Shorthand), "-")
			command.Flags[index].Usage = strings.TrimSpace(command.Flags[index].Usage)
		}
		sort.Slice(command.Flags, func(left, right int) bool {
			if command.Flags[left].Name == command.Flags[right].Name {
				return command.Flags[left].Shorthand < command.Flags[right].Shorthand
			}
			return command.Flags[left].Name < command.Flags[right].Name
		})
		if err := validateFlags(command.Path, command.Flags); err != nil {
			return "", err
		}
		visible = append(visible, command)
	}
	sort.Slice(visible, func(left, right int) bool {
		return visible[left].Path < visible[right].Path
	})

	var output strings.Builder
	output.WriteString("<!-- generated from exp command metadata; do not edit -->\n\n")
	output.WriteString("# Current `exp` commands\n\n")
	output.WriteString("This reference contains only the command metadata supplied by this build's CLI layer. " +
		"It is not a roadmap for deferred commands.\n\n")
	for _, command := range visible {
		fmt.Fprintf(&output, "## `%s`\n\n%s\n\n", command.Path, command.Summary)
		fmt.Fprintf(&output, "```text\n%s\n```\n\n", command.Use)
		if len(command.Flags) == 0 {
			continue
		}
		output.WriteString("Options:\n\n")
		for _, flag := range command.Flags {
			name := "--" + flag.Name
			if flag.Shorthand != "" {
				name = "-" + flag.Shorthand + ", " + name
			}
			fmt.Fprintf(&output, "- `%s` — %s\n", name, flag.Usage)
		}
		output.WriteString("\n")
	}
	return output.String(), nil
}

// CheckCommandReference compares arbitrary reference bytes with deterministic
// output from command metadata. It performs no writes.
func CheckCommandReference(commands []CommandMetadata, actual []byte) (CommandReferenceCheck, error) {
	expected, err := GenerateCommandReference(commands)
	if err != nil {
		return CommandReferenceCheck{}, err
	}
	return CommandReferenceCheck{
		Current:      bytes.Equal([]byte(expected), actual),
		ExpectedHash: digest([]byte(expected)),
		ActualHash:   digest(actual),
		Expected:     expected,
	}, nil
}

// CheckEmbeddedCommandReference checks this build's bundled commands.md against
// metadata supplied by the CLI layer.
func CheckEmbeddedCommandReference(commands []CommandMetadata) (CommandReferenceCheck, error) {
	files, err := Files()
	if err != nil {
		return CommandReferenceCheck{}, err
	}
	actual, ok := files[CommandsReferencePath]
	if !ok {
		return CommandReferenceCheck{}, fmt.Errorf("bundled skill is missing %s", CommandsReferencePath)
	}
	return CheckCommandReference(commands, actual)
}

func validateRawCommandMetadata(command CommandMetadata) error {
	for _, field := range []struct {
		name   string
		value  string
		fenced bool
	}{
		{name: "path", value: command.Path},
		// Pipes, brackets, and flag punctuation are literal command syntax inside
		// the fenced Use block (for example, "print|install|check").
		{name: "use", value: command.Use, fenced: true},
		{name: "summary", value: command.Summary},
	} {
		if containsUnsafeMarkdown(field.value, field.fenced) {
			return fmt.Errorf("command %q %s contains unsupported Markdown syntax or control characters", command.Path, field.name)
		}
	}
	return nil
}

func validateRawFlagMetadata(commandPath string, flag FlagMetadata) error {
	if containsUnsafeMarkdown(flag.Name, false) ||
		containsUnsafeMarkdown(flag.Shorthand, false) ||
		containsUnsafeMarkdown(flag.Usage, false) {
		return fmt.Errorf("command %s flag --%s contains unsupported Markdown syntax or control characters", commandPath, flag.Name)
	}
	return nil
}

func containsUnsafeMarkdown(value string, fenced bool) bool {
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
			return true
		}
	}
	if strings.ContainsAny(value, "`<>") {
		return true
	}
	if fenced {
		// Use is rendered inside a text fence. Brackets, pipes, and leading
		// command punctuation are literal there and are normal usage syntax.
		return false
	}
	return strings.ContainsRune(value, '|') ||
		containsMarkdownBlockMarker(value) ||
		containsMarkdownLinkOrImage(value)
}

func containsMarkdownBlockMarker(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	if value[0] == '#' {
		count := 0
		for count < len(value) && value[count] == '#' {
			count++
		}
		if count <= 6 && (count == len(value) || value[count] == ' ') {
			return true
		}
	}

	if (value[0] == '-' || value[0] == '+' || value[0] == '*') &&
		(len(value) == 1 || value[1] == ' ') {
		return true
	}

	digits := 0
	for digits < len(value) && digits < 9 && value[digits] >= '0' && value[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits < len(value) && (value[digits] == '.' || value[digits] == ')') &&
		(digits+1 == len(value) || value[digits+1] == ' ') {
		return true
	}

	marker := value[0]
	if marker != '-' && marker != '_' && marker != '*' {
		return false
	}
	markers := 0
	for _, character := range value {
		switch character {
		case rune(marker):
			markers++
		case ' ':
		default:
			return false
		}
	}
	return markers >= 3
}

func containsMarkdownLinkOrImage(value string) bool {
	// Brackets have no required prose use here. Rejecting either delimiter also
	// covers inline, full-reference, collapsed-reference, and image forms without
	// attempting to duplicate Markdown's nested-label grammar.
	return strings.ContainsAny(value, "[]")
}

func validateCommandMetadata(command CommandMetadata) error {
	if command.Path == "" || command.Use == "" || command.Summary == "" {
		return fmt.Errorf("command metadata requires path, use, and summary")
	}
	if strings.Join(strings.Fields(command.Path), " ") != command.Path {
		return fmt.Errorf("command path %q is not normalized", command.Path)
	}
	if command.Path != "exp" && !strings.HasPrefix(command.Path, "exp ") {
		return fmt.Errorf("command path %q is outside the exp command tree", command.Path)
	}
	return nil
}

func validateFlags(commandPath string, flags []FlagMetadata) error {
	seen := make(map[string]struct{}, len(flags))
	for _, flag := range flags {
		if flag.Name == "" || flag.Usage == "" {
			return fmt.Errorf("command %s has a flag without name or usage", commandPath)
		}
		if strings.ContainsAny(flag.Name, " \t") || strings.ContainsAny(flag.Shorthand, " \t") {
			return fmt.Errorf("command %s flag names may not contain whitespace", commandPath)
		}
		if _, duplicate := seen[flag.Name]; duplicate {
			return fmt.Errorf("command %s contains duplicate flag --%s", commandPath, flag.Name)
		}
		seen[flag.Name] = struct{}{}
	}
	return nil
}
