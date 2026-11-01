package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Messages a provider call sends to the program that shows its progress.
type (
	deltaMsg string
	toolMsg  string
	doneMsg  struct {
		text string
		err  error
	}
)

// runWithProgress runs the provider call while m shows its progress on out.
// The model gets each delta and tool call, then a doneMsg, and quits once it
// has shown what it needs to.
func runWithProgress(ctx context.Context, p provider, c call, m tea.Model, out *os.File) (string, error) {
	// Without input the terminal stays in cooked mode, so Ctrl+C arrives as a
	// signal and cancels ctx, which also stops the provider process.
	prog := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(out), tea.WithoutSignalHandler())
	c.onDelta = func(delta string) { prog.Send(deltaMsg(delta)) }
	c.onTool = func(tool string) { prog.Send(toolMsg(tool)) }
	result := make(chan doneMsg, 1)
	go func() {
		text, err := p.complete(ctx, c)
		result <- doneMsg{text, err}
		prog.Send(doneMsg{text, err})
	}()
	if _, err := prog.Run(); err != nil {
		return "", err
	}
	r := <-result
	return r.text, r.err
}

// progress is the status line of a running call: a spinner, the provider,
// model and effort, the time so far, and how many tools the model has called.
type progress struct {
	spinner   spinner.Model
	label     string
	start     time.Time
	width     int
	toolCalls int
}

func newProgress(p provider) progress {
	return progress{
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(accent))),
		label:   capitalize(p.spec.name) + " · " + capitalize(p.model) + " · " + capitalize(p.effort),
		start:   time.Now(),
		width:   terminalWidth(),
	}
}

func (p *progress) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
	case toolMsg:
		p.toolCalls++
	case spinner.TickMsg:
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(msg)
		return cmd
	}
	return nil
}

func (p progress) line() string {
	elapsed := time.Since(p.start).Truncate(100 * time.Millisecond)
	line := fmt.Sprintf("%s %s %s", p.spinner.View(), p.label, dimStyle.Render(fmt.Sprintf("%.1fs", elapsed.Seconds())))
	if p.toolCalls > 0 {
		line += dimStyle.Render(fmt.Sprintf(" · %d tool calls", p.toolCalls))
	}
	return line
}

// toolLine shows one tool call on one line, indented to the answer's margin.
func toolLine(tool string, width int) string {
	return dimStyle.Render(truncateLine("  ↳ "+tool, width))
}

// completeWithSpinner runs the provider behind a spinner on stderr, with the
// latest tool calls listed under it, and returns the answer once it is complete.
func completeWithSpinner(ctx context.Context, p provider, c call) (string, error) {
	if !isTerminal(os.Stderr) {
		return p.complete(ctx, c)
	}
	m := &spinnerModel{progress: newProgress(p)}
	text, err := runWithProgress(ctx, p, c, m, os.Stderr)
	// The tool calls stay in the scrollback, so it is clear what the answer is based
	// on. They print after the spinner is gone because the spinner's view cannot
	// be taller than the terminal, so a long list would lose its first calls.
	for _, t := range m.tools {
		lipgloss.Fprintln(os.Stderr, toolLine(t, m.width))
	}
	return text, err
}

type spinnerModel struct {
	progress
	tools []string
	done  bool
}

// shownTools is how many of the latest tool calls stay listed under the spinner.
const shownTools = 4

func (m *spinnerModel) Init() tea.Cmd { return m.spinner.Tick }

func (m *spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.progress.update(msg)
	switch msg := msg.(type) {
	case toolMsg:
		m.tools = append(m.tools, string(msg))
	case doneMsg:
		m.done = true
		return m, tea.Quit
	}
	return m, cmd
}

func (m *spinnerModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	var b strings.Builder
	b.WriteString(m.line())
	for _, t := range last(m.tools, shownTools) {
		b.WriteString("\n" + toolLine(t, m.width))
	}
	return tea.NewView(b.String())
}
