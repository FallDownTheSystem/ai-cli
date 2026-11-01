package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// runExecute asks for a command, shows it, and runs it once the user agrees.
// A revise sends the user's feedback back with the declined command.
func runExecute(ctx context.Context, p provider, env environment, req request) int {
	system := executeSystemPrompt(env)
	for {
		reply, err := completeWithSpinner(ctx, p, env.newCall(system, req))
		if err != nil {
			return fail(err)
		}
		command, refusal := parseCommand(reply)
		if refusal != "" {
			lipgloss.Fprintln(os.Stderr, warnStyle.Render(refusal))
			return 1
		}
		if command == "" {
			return fail(errors.New("the model returned no command"))
		}

		choice, err := confirm(command)
		if err != nil {
			return fail(err)
		}
		switch choice.action {
		case actionRun:
			return runCommand(env.shell, choice.command)
		case actionRevise:
			req.declined = command
			req.feedback = choice.feedback
		default:
			return exitCancelled
		}
	}
}

// runCommand runs the command in the user's shell and returns its exit code.
func runCommand(sh shell, command string) int {
	cmd := sh.command(command)
	// The child shares the console, so Ctrl+C reaches it directly; ai only
	// waits and passes the exit code through.
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return fail(err)
	}
	return 0
}

type action int

const (
	actionCancel action = iota
	actionRun
	actionRevise
)

type choice struct {
	action   action
	command  string
	feedback string
}

type confirmState int

const (
	stateChoose confirmState = iota
	stateEdit
	stateRevise
)

type confirmModel struct {
	command string
	state   confirmState
	// input edits the command in stateEdit and takes the feedback in stateRevise.
	input  textarea.Model
	width  int
	result choice
	done   bool
}

// confirm shows the command and asks what to do with it. The command goes to
// the scrollback before the question, because the question's view cannot be
// taller than the terminal and a command cut short could hide anything.
func confirm(command string) (choice, error) {
	width := terminalWidth()
	lipgloss.Fprintln(os.Stderr, commandLine(command, width))
	if hasHiddenRunes(command) {
		lipgloss.Fprintln(os.Stderr, warnStyle.Render("The command holds control or direction characters, shown as escapes above."))
	}
	m := &confirmModel{command: command, input: newEditor(width), width: width}
	if _, err := tea.NewProgram(m, tea.WithOutput(os.Stderr)).Run(); err != nil {
		return choice{}, err
	}
	return m.result, nil
}

func (m *confirmModel) Init() tea.Cmd { return nil }

func (m *confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.input.SetWidth(besideWidth(size.Width))
		return m, nil
	}
	key, isKey := msg.(tea.KeyPressMsg)
	if isKey && key.String() == "ctrl+c" {
		return m.cancel()
	}
	if m.state != stateChoose {
		return m.updateInput(msg)
	}
	if !isKey {
		return m, nil
	}
	switch key.String() {
	case "y", "enter":
		return m.finish(choice{action: actionRun, command: m.command}, "")
	case "e":
		return m.startInput(stateEdit, m.command, "")
	case "r", "n":
		return m.startInput(stateRevise, "", "what should change? (enter on empty line to quit)")
	case "c", "q", "esc":
		return m.cancel()
	}
	return m, nil
}

func (m *confirmModel) startInput(state confirmState, value, placeholder string) (tea.Model, tea.Cmd) {
	m.state = state
	m.input.SetValue(value)
	m.input.Placeholder = placeholder
	return m, m.input.Focus()
}

func (m *confirmModel) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.state = stateChoose
			m.input.Blur()
			return m, nil
		case "enter":
			value := strings.TrimSpace(m.input.Value())
			switch {
			case value == "":
				return m.cancel()
			case m.state == stateRevise:
				return m.finish(choice{action: actionRevise, feedback: value}, dimStyle.Render("↻ revise: "+value))
			case value == m.command:
				return m.finish(choice{action: actionRun, command: value}, "")
			}
			// The edited command shows in the scrollback, so it is clear what ran.
			return m.finish(choice{action: actionRun, command: value}, commandLine(value, m.width))
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *confirmModel) cancel() (tea.Model, tea.Cmd) {
	return m.finish(choice{action: actionCancel}, cancelledNote)
}

// finish records the decision in the scrollback, below the command shown before.
func (m *confirmModel) finish(c choice, note string) (tea.Model, tea.Cmd) {
	m.result, m.done = c, true
	if note == "" {
		return m, tea.Quit
	}
	return m, tea.Sequence(tea.Println(note), tea.Quit)
}

func (m *confirmModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	switch m.state {
	case stateEdit:
		return tea.NewView(editorView(promptMark, m.input, "enter run · ctrl+j new line · esc back"))
	case stateRevise:
		return tea.NewView(editorView(keyStyle.Render("?"), m.input, "enter revise · esc back"))
	}
	return tea.NewView(fmt.Sprintf("%s run  %s edit  %s revise  %s cancel",
		keyStyle.Render("y"), keyStyle.Render("e"), keyStyle.Render("r"), keyStyle.Render("c")))
}

// commandLine wraps the command beside the prompt mark, with every character
// that could hide or move text shown as an escape.
func commandLine(command string, width int) string {
	return beside(promptMark, commandStyle.Width(besideWidth(width)).Render(visible(command)))
}

// visible escapes control characters other than newline and tab, and the
// Unicode marks that reorder text, so what is shown is what will run.
func visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		if hidden(r) {
			b.WriteString(warnStyle.Render(strings.Trim(fmt.Sprintf("%+q", r), "'")))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hidden(r rune) bool {
	return r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r))
}

func hasHiddenRunes(s string) bool { return strings.IndexFunc(s, hidden) >= 0 }
