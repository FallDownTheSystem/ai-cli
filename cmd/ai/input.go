package main

import (
	"context"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// readQuestion asks for the question when ai runs without one. The shell never
// sees this text, so it needs no quotes in any shell. A user who cancels gets
// context.Canceled.
func readQuestion(placeholder string) (string, error) {
	m := &questionModel{input: newEditor(terminalWidth())}
	m.input.Placeholder = placeholder
	if _, err := tea.NewProgram(m, tea.WithOutput(os.Stderr)).Run(); err != nil {
		return "", err
	}
	if m.question == "" {
		return "", context.Canceled
	}
	return m.question, nil
}

type questionModel struct {
	input    textarea.Model
	question string
	done     bool
}

func (m *questionModel) Init() tea.Cmd { return m.input.Focus() }

func (m *questionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.input.SetWidth(besideWidth(msg.Width))
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m.finish("", cancelledNote)
		case "enter":
			question := strings.TrimSpace(m.input.Value())
			if question == "" {
				return m.finish("", cancelledNote)
			}
			// The question stays in the scrollback above the answer, where ai -c finds it.
			return m.finish(question, beside(promptMark, question))
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *questionModel) finish(question, note string) (tea.Model, tea.Cmd) {
	m.question, m.done = question, true
	return m, tea.Sequence(tea.Println(note), tea.Quit)
}

func (m *questionModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	return tea.NewView(editorView(promptMark, m.input, "enter ask · ctrl+j new line · esc cancel"))
}

// newEditor is a text area that grows with its text. Enter answers the
// question, so a new line takes another key.
func newEditor(width int) textarea.Model {
	t := textarea.New()
	t.Prompt = ""
	t.ShowLineNumbers = false
	t.DynamicHeight = true
	t.MinHeight = 1
	t.MaxHeight = 12
	t.EndOfBufferCharacter = ' '
	t.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "alt+enter", "shift+enter"))
	s := t.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	t.SetStyles(s)
	t.SetWidth(besideWidth(width))
	return t
}

// editorView shows an editor beside its mark, with a line of key help under it.
func editorView(mark string, editor textarea.Model, help string) string {
	return beside(mark, editor.View()) + "\n" + dimStyle.Render(help)
}
