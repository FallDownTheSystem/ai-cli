package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// runAsk answers a question. On a terminal the answer is rendered Markdown,
// streamed or shown once complete; through a pipe it is the plain text.
func runAsk(ctx context.Context, p provider, env environment, req request, style string, stream bool) int {
	c := env.newCall(askSystemPrompt(env), req)
	onTerminal := isTerminal(os.Stdout)
	if stream && onTerminal {
		if err := streamAnswer(ctx, p, c, style); err != nil {
			return fail(err)
		}
		return 0
	}
	text, err := completeWithSpinner(ctx, p, c)
	if err != nil {
		return fail(err)
	}
	if onTerminal {
		text = newMarkdownRenderer(style).render(text)
	}
	fmt.Println(strings.TrimSpace(text))
	return 0
}

// streamAnswer shows the answer while it is written. Each Markdown block goes
// to the scrollback once it is complete, rendered for good; the block still
// being written shows below it, rendered again as it grows.
func streamAnswer(ctx context.Context, p provider, c call, style string) error {
	m := &streamModel{
		progress: newProgress(p),
		answer:   answerStream{md: newMarkdownRenderer(style)},
	}
	_, m.height = terminalSize()
	_, err := runWithProgress(ctx, p, c, m, os.Stdout)
	return err
}

type streamModel struct {
	progress
	height int
	answer answerStream
	// tail is the rendered block still being written. It is rendered again on
	// the spinner's tick rather than on every delta, which can come hundreds of
	// times a second.
	tail  string
	stale bool
	queue printQueue
	done  bool
}

func (m *streamModel) Init() tea.Cmd { return m.spinner.Tick }

func (m *streamModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.progress.update(msg)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
	case deltaMsg:
		complete := m.answer.delta(string(msg))
		if complete == "" {
			m.stale = true
			return m, nil
		}
		m.print(complete)
		m.renderTail()
		return m, m.next()
	case toolMsg:
		m.print(m.answer.tool(toolLine(string(msg), m.width)))
		m.renderTail()
		return m, m.next()
	case doneMsg:
		m.done = true
		m.print(m.answer.done(msg.text))
		return m, m.next()
	case printedMsg:
		m.queue.printed()
		return m, m.next()
	case spinner.TickMsg:
		if m.stale {
			m.renderTail()
		}
	}
	return m, cmd
}

func (m *streamModel) renderTail() {
	m.tail, m.stale = m.answer.tail(), false
}

// print queues text for the scrollback above the live view.
func (m *streamModel) print(text string) {
	if text != "" {
		m.queue.add(m.split(text)...)
	}
}

// next starts the next queued print, or quits once the answer is complete and
// everything is printed.
func (m *streamModel) next() tea.Cmd {
	if m.done && m.queue.empty() {
		return tea.Quit
	}
	return m.queue.next()
}

// tailRows caps the live block. A print goes above the view by scrolling the
// screen and then moving up over the view; if the print and the view together
// are taller than the terminal, the move stops at the top row and the view's
// first lines stay behind in the scrollback. Half the terminal for the view
// leaves the other half for each print.
func (m *streamModel) tailRows() int {
	if m.height <= 0 {
		return 10
	}
	return max(m.height/2-3, 1)
}

// viewRows is the most rows View takes: the tail, the gap above it, the blank
// line under it, and the status line.
func (m *streamModel) viewRows() int { return m.tailRows() + 3 }

// split cuts text into parts that fit beside the tallest view, with a row to
// spare.
func (m *streamModel) split(text string) []string {
	room := 1 << 30
	if m.height > 0 {
		room = max(m.height-m.viewRows()-1, 1)
	}
	var parts []string
	var part []string
	rows := 0
	for _, line := range strings.Split(text, "\n") {
		n := m.rowsOf(line)
		if rows+n > room && len(part) > 0 {
			parts = append(parts, strings.Join(part, "\n"))
			part, rows = nil, 0
		}
		part = append(part, line)
		rows += n
	}
	return append(parts, strings.Join(part, "\n"))
}

// rowsOf is how many rows a line takes once the terminal wraps it.
func (m *streamModel) rowsOf(line string) int {
	width := max(m.width, 1)
	return max(1, (ansi.StringWidth(line)+width-1)/width)
}

func (m *streamModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	if m.tail == "" {
		return tea.NewView(m.line())
	}
	// A block taller than the view shows its latest lines until it is complete
	// and goes to the scrollback whole. Lines are cut at the edge, so that each
	// takes one row; code lines are not wrapped.
	lines := last(strings.Split(m.tail, "\n"), m.tailRows())
	for i, l := range lines {
		lines[i] = truncateLine(l, m.width)
	}
	return tea.NewView(m.answer.gap() + strings.Join(lines, "\n") + "\n\n" + m.line())
}

// printQueue prints parts to the scrollback one at a time. Each command runs
// on its own goroutine, so two prints in flight could land in the wrong order.
type printQueue struct {
	parts    []string
	printing bool
}

type printedMsg struct{}

func (q *printQueue) add(parts ...string) { q.parts = append(q.parts, parts...) }

// next starts the next print, unless one is in flight or none is queued. The
// print reports back with a printedMsg, which the model passes to printed.
func (q *printQueue) next() tea.Cmd {
	if q.printing || len(q.parts) == 0 {
		return nil
	}
	part := q.parts[0]
	q.parts = q.parts[1:]
	q.printing = true
	return tea.Sequence(tea.Println(part), func() tea.Msg { return printedMsg{} })
}

func (q *printQueue) printed() { q.printing = false }

func (q *printQueue) empty() bool { return !q.printing && len(q.parts) == 0 }
