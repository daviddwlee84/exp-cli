//go:build !aix

package tui

import (
	"context"
	"errors"
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

// Available reports whether this build includes the interactive runner.
func Available() bool { return true }

// Run owns only terminal event handling. All application reads remain in the
// injected callbacks held by Options.
func Run(ctx context.Context, input io.Reader, output io.Writer, options Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if input == nil || output == nil {
		return errors.New("exp ui requires input and output streams")
	}
	options.Context = ctx
	model := NewModel(options)
	program := tea.NewProgram(model,
		tea.WithContext(ctx),
		tea.WithInput(input),
		tea.WithOutput(output),
	)
	_, err := program.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
