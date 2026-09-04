package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// unknownCommandError keeps Cobra's own suggestion engine while adding a
// targeted family help pointer. The full usage block stays silenced.
func unknownCommandError(command *cobra.Command, name string) error {
	var message strings.Builder
	fmt.Fprintf(&message, "unknown command %q for %q", name, command.CommandPath())
	if suggestions := commandSuggestions(command, name); len(suggestions) > 0 {
		message.WriteString("\n\nDid you mean this?\n")
		for _, suggestion := range suggestions {
			fmt.Fprintf(&message, "\t%s\n", suggestion)
		}
	}
	fmt.Fprintf(&message, "\nRun %q for available commands.", command.CommandPath()+" --help")
	return errors.New(message.String())
}

func commandSuggestions(command *cobra.Command, name string) []string {
	if suggestions := command.SuggestionsFor(name); len(suggestions) > 0 {
		return suggestions
	}
	const fallbackDistance = 3
	best := fallbackDistance + 1
	var suggestions []string
	for _, child := range command.Commands() {
		if !child.IsAvailableCommand() {
			continue
		}
		distance := editDistance(strings.ToLower(name), strings.ToLower(child.Name()))
		if distance > fallbackDistance || distance > best {
			continue
		}
		if distance < best {
			best = distance
			suggestions = suggestions[:0]
		}
		suggestions = append(suggestions, child.Name())
	}
	return suggestions
}

func editDistance(left, right string) int {
	leftRunes, rightRunes := []rune(left), []rune(right)
	previous := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range leftRunes {
		current := make([]int, len(rightRunes)+1)
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range rightRunes {
			cost := 1
			if leftRune == rightRune {
				cost = 0
			}
			deletion := previous[rightIndex+1] + 1
			insertion := current[rightIndex] + 1
			substitution := previous[rightIndex] + cost
			current[rightIndex+1] = min(deletion, insertion, substitution)
		}
		previous = current
	}
	return previous[len(rightRunes)]
}

func installHelpRecovery(root *cobra.Command) {
	if root == nil {
		return
	}
	var help *cobra.Command
	for _, child := range root.Commands() {
		if child.Name() == "help" {
			help = child
			break
		}
	}
	if help == nil {
		return
	}
	help.Args = func(_ *cobra.Command, args []string) error {
		_, err := resolveHelpTarget(root, args)
		return err
	}
	help.Run = nil
	help.RunE = func(command *cobra.Command, args []string) error {
		target, err := resolveHelpTarget(root, args)
		if err != nil {
			return err
		}
		target.SetContext(command.Context())
		return target.Help()
	}
}

func installCompletionRecovery(root *cobra.Command) {
	if root == nil {
		return
	}
	for _, child := range root.Commands() {
		if child.Name() != "completion" || child.Runnable() {
			continue
		}
		child.RunE = func(command *cobra.Command, _ []string) error {
			return command.Help()
		}
		return
	}
}

func resolveHelpTarget(root *cobra.Command, path []string) (*cobra.Command, error) {
	current := root
	for _, name := range path {
		var next *cobra.Command
		for _, child := range current.Commands() {
			if child.Name() == name || child.HasAlias(name) {
				next = child
				break
			}
		}
		if next == nil {
			return nil, unknownCommandError(current, name)
		}
		current = next
	}
	return current, nil
}

// installUsageRecovery gives every command family the same unknown-subcommand
// behavior. Existing leaf argument validators remain untouched.
func installUsageRecovery(root *cobra.Command) {
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		for _, child := range command.Commands() {
			visit(child)
		}
		if !command.HasSubCommands() {
			return
		}
		validate := command.Args
		command.Args = func(current *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommandError(current, args[0])
			}
			if validate != nil {
				return validate(current, args)
			}
			return nil
		}
	}
	visit(root)
}
