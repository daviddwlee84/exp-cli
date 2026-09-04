package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/term"
)

const maxPromptInputBytes = 64 << 10

// PromptCanceled is the single cancellation type returned for an interactive
// decline, Escape, Ctrl-C, Ctrl-D, or EOF. Callers can use errors.Is with
// ErrPromptCanceled and can therefore guarantee that no mutation follows it.
type PromptCanceled struct{}

func (*PromptCanceled) Error() string { return "interactive operation canceled" }

// ErrPromptCanceled is returned by every interactive cancellation path.
var ErrPromptCanceled error = &PromptCanceled{}

// InvalidUsage marks invocation input that a non-interactive or JSON caller
// must provide explicitly. It is deliberately distinct from validation and
// runtime failures.
type InvalidUsage struct {
	Message string
}

func (failure *InvalidUsage) Error() string {
	if failure == nil || strings.TrimSpace(failure.Message) == "" {
		return "invalid command usage"
	}
	return failure.Message
}

// ErrInvalidUsage supports errors.Is checks without exposing Cobra internals.
var ErrInvalidUsage = errors.New("invalid command usage")

func (failure *InvalidUsage) Is(target error) bool { return target == ErrInvalidUsage }

func invalidUsagef(format string, arguments ...any) error {
	return &InvalidUsage{Message: fmt.Sprintf(format, arguments...)}
}

// PromptRequest describes one line of non-secret input. DefaultDisplay is the
// only default text rendered to the terminal; Default itself is never echoed
// implicitly. This lets a safe display fallback differ from the stored value.
type PromptRequest struct {
	Label          string
	Description    string
	Default        string
	DefaultDisplay string
}

// Prompter is the small invocation-scoped line-editing boundary injected via
// App. Flow validation and confirmation policy intentionally remain outside it.
type Prompter interface {
	Ask(context.Context, PromptRequest) (string, error)
}

// InteractiveCheck decides whether a command may enter a wizard. Production
// uses both-terminal detection. Tests must inject an explicit true result before
// buffered deterministic input is accepted.
type InteractiveCheck func(io.Reader, io.Writer) bool

func terminalPair(input io.Reader, output io.Writer) bool {
	in, inputFile := input.(*os.File)
	out, outputFile := output.(*os.File)
	return inputFile && outputFile && term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd()))
}

type terminalPrompter struct {
	app *App

	mu       sync.Mutex
	input    *os.File
	output   *os.File
	terminal *term.Terminal
}

type promptReadWriter struct {
	reader io.Reader
	writer io.Writer
}

func (streams promptReadWriter) Read(buffer []byte) (int, error) {
	return streams.reader.Read(buffer)
}

func (streams promptReadWriter) Write(buffer []byte) (int, error) {
	return streams.writer.Write(buffer)
}

// escapeCancelReader maps Escape onto the same control byte x/term already
// treats as cancellation for Ctrl-C. CSI cursor/editing sequences remain intact;
// standalone, trailing, and Alt-style Escape bytes all cancel the interaction.
type escapeCancelReader struct{ input io.Reader }

func (reader escapeCancelReader) Read(buffer []byte) (int, error) {
	count, err := reader.input.Read(buffer)
	for index := 0; index < count; index++ {
		if buffer[index] == 0x04 {
			buffer[index] = 0x03
			continue
		}
		if buffer[index] != 0x1b {
			continue
		}
		if index+1 < count && buffer[index+1] == '[' {
			continue
		}
		buffer[index] = 0x03
	}
	return count, err
}

func (prompter *terminalPrompter) Ask(ctx context.Context, request PromptRequest) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if prompter == nil || prompter.app == nil {
		return "", errors.New("interactive prompter is unavailable")
	}
	actualTerminal := terminalPair(prompter.app.In, prompter.app.Out)
	if !actualTerminal && (prompter.app.IsInteractive == nil || !prompter.app.IsInteractive(prompter.app.In, prompter.app.Out)) {
		return "", invalidUsagef("interactive prompting requires terminal stdin and stdout")
	}
	prompter.mu.Lock()
	defer prompter.mu.Unlock()

	label := safePromptText(request.Label)
	description := safePromptText(request.Description)
	defaultDisplay := safePromptText(request.DefaultDisplay)
	if description != "" {
		if _, err := fmt.Fprintf(prompter.app.Out, "%s\n", prompter.app.outStyle().label(description)); err != nil {
			return "", err
		}
	}
	prompt := prompter.app.outStyle().prompt(label)
	if defaultDisplay != "" {
		prompt += " " + prompter.app.outStyle().label("["+defaultDisplay+"]")
	}
	prompt += ": "

	var (
		line string
		err  error
	)
	in, inputFile := prompter.app.In.(*os.File)
	out, outputFile := prompter.app.Out.(*os.File)
	if inputFile && outputFile && term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd())) {
		line, err = prompter.readTerminalLine(in, out, prompt)
	} else {
		line, err = readDeterministicPromptLine(ctx, prompter.app.In, prompter.app.Out, prompt)
	}
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, ErrPromptCanceled) {
			return "", ErrPromptCanceled
		}
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if line == "" {
		return request.Default, nil
	}
	return line, nil
}

func (prompter *terminalPrompter) readTerminalLine(input, output *os.File, prompt string) (string, error) {
	if prompter.terminal == nil || prompter.input != input || prompter.output != output {
		prompter.input = input
		prompter.output = output
		prompter.terminal = term.NewTerminal(promptReadWriter{
			reader: escapeCancelReader{input: input},
			writer: output,
		}, prompt)
		if width, height, err := term.GetSize(int(output.Fd())); err == nil {
			if width > 0 && height > 0 {
				prompter.terminal.SetSize(width, height)
			}
		}
	} else {
		prompter.terminal.SetPrompt(prompt)
	}
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return "", fmt.Errorf("enable terminal line editing: %w", err)
	}
	line, readErr := prompter.terminal.ReadLine()
	restoreErr := term.Restore(int(input.Fd()), state)
	if readErr != nil {
		_, _ = io.WriteString(output, "\r\n")
	}
	if readErr != nil || restoreErr != nil {
		return "", errors.Join(readErr, restoreErr)
	}
	return line, nil
}

func readDeterministicPromptLine(ctx context.Context, input io.Reader, output io.Writer, prompt string) (string, error) {
	if _, err := io.WriteString(output, prompt); err != nil {
		return "", err
	}
	buffer := make([]byte, 0, 128)
	one := []byte{0}
	for len(buffer) <= maxPromptInputBytes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count, err := input.Read(one)
		if count > 0 {
			character := one[0]
			switch character {
			case 0x03, 0x04, 0x1b:
				return "", ErrPromptCanceled
			case '\n':
				if _, writeErr := io.WriteString(output, "\n"); writeErr != nil {
					return "", writeErr
				}
				line := strings.TrimSuffix(string(buffer), "\r")
				if !utf8.ValidString(line) {
					return "", errors.New("prompt input is not valid UTF-8")
				}
				return line, nil
			default:
				buffer = append(buffer, character)
			}
		}
		if err != nil {
			// EOF is cancellation even when a partial line preceded it; an
			// unterminated scripted answer must never authorize an effect.
			if errors.Is(err, io.EOF) {
				return "", ErrPromptCanceled
			}
			return "", err
		}
		if count == 0 {
			return "", io.ErrNoProgress
		}
	}
	return "", errors.New("prompt input exceeds the byte limit")
}

func safePromptText(value string) string {
	value = safeHumanOutput(value)
	value = strings.NewReplacer("\x00", "", "\r", " ", "\n", " ", "\t", " ").Replace(value)
	return strings.TrimSpace(value)
}

func (app *App) interactive(machine bool) bool {
	if app == nil || machine || app.machineOutput || app.IsInteractive == nil {
		return false
	}
	return app.IsInteractive(app.In, app.Out)
}

func (app *App) askValidated(ctx context.Context, request PromptRequest, validate func(string) error) (string, error) {
	if app == nil || app.Prompter == nil {
		return "", errors.New("interactive prompter is unavailable")
	}
	for {
		value, err := app.Prompter.Ask(ctx, request)
		if err != nil {
			return "", err
		}
		if validate == nil {
			return value, nil
		}
		if validationErr := validate(value); validationErr == nil {
			return value, nil
		} else if _, writeErr := fmt.Fprintf(app.Out, "%s %s\n", app.outStyle().warning("Invalid:"), safePromptText(validationErr.Error())); writeErr != nil {
			return "", writeErr
		}
	}
}

func (app *App) askChoice(ctx context.Context, label, description, fallback string, choices ...string) (string, error) {
	allowed := make(map[string]string, len(choices))
	for _, choice := range choices {
		allowed[strings.ToLower(choice)] = choice
	}
	value, err := app.askValidated(ctx, PromptRequest{
		Label: label, Description: description, Default: fallback, DefaultDisplay: fallback,
	}, func(value string) error {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(value))]; ok {
			return nil
		}
		return fmt.Errorf("choose one of %s", strings.Join(choices, ", "))
	})
	if err != nil {
		return "", err
	}
	return allowed[strings.ToLower(strings.TrimSpace(value))], nil
}

func (app *App) askYesNo(ctx context.Context, label, description string, fallback bool) (bool, error) {
	defaultValue, defaultDisplay := "no", "no"
	if fallback {
		defaultValue, defaultDisplay = "yes", "yes"
	}
	value, err := app.askValidated(ctx, PromptRequest{
		Label: label, Description: description, Default: defaultValue, DefaultDisplay: defaultDisplay,
	}, func(value string) error {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "y", "yes", "n", "no":
			return nil
		default:
			return errors.New("answer yes or no")
		}
	})
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(value), "y") || strings.EqualFold(strings.TrimSpace(value), "yes"), nil
}

func (app *App) requireConfirmation(ctx context.Context, label, description, exactToken string) error {
	if exactToken != "" {
		value, err := app.Prompter.Ask(ctx, PromptRequest{Label: label, Description: description})
		if err != nil {
			return err
		}
		if value != exactToken {
			return ErrPromptCanceled
		}
		return nil
	}
	confirmed, err := app.askYesNo(ctx, label, description, false)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrPromptCanceled
	}
	return nil
}
