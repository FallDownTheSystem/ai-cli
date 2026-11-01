package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

var (
	accent        = lipgloss.Color("#8B7CF6")
	dimStyle      = lipgloss.NewStyle().Faint(true)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F25F5C"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5A84B"))
	commandStyle  = lipgloss.NewStyle().Bold(true)
	keyStyle      = lipgloss.NewStyle().Foreground(accent).Bold(true)
	promptMark    = keyStyle.Render("❯")
	cancelledNote = dimStyle.Render("✗ cancelled")
)

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// terminalSize is the size of the terminal that stdout or stderr shows on. A
// height of 0 means unknown.
func terminalSize() (width, height int) {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if w, h, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w, h
		}
	}
	return 80, 0
}

func terminalWidth() int {
	w, _ := terminalSize()
	return w
}

// beside puts body to the right of a one-column mark, so that body's wrapped
// lines stay clear of the mark.
func beside(mark, body string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, mark+" ", body)
}

// besideWidth is the room beside a mark on a terminal width wide: the mark and
// its space, and one column spare, where the cursor sits at the end of a line.
func besideWidth(width int) int { return max(width-3, 20) }

// truncateLine cuts a line one column short of the edge. A line that fills the
// last column makes some terminals wrap it, which moves every line below.
func truncateLine(line string, width int) string {
	return ansi.Truncate(line, max(width-1, 20), "…")
}

// capitalize writes a name the way a status line shows it, such as Luna for luna.
func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// last is the final n elements of s, or all of s when it is shorter.
func last[T any](s []T, n int) []T { return s[max(len(s)-n, 0):] }

// trimBlankLines drops the blank lines at the start and the end. A line that
// holds only escape codes and spaces counts as blank.
func trimBlankLines(lines []string) []string {
	blank := func(line string) bool { return strings.TrimSpace(ansi.Strip(line)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// relativePath shows a path under dir relative to it, and any other path whole.
func relativePath(path, dir string) string {
	if rel, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// shortPath writes the home directory as ~, so the path fits on one line.
func shortPath(path string) string {
	if rel := relativePath(path, homeDir()); rel != path {
		return filepath.Join("~", rel)
	}
	return path
}
