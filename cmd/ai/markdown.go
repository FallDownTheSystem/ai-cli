package main

import (
	"regexp"
	"strconv"
	"strings"

	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
)

// markdownStyles are the styles built into glamour; a path to a JSON style also works.
var markdownStyles = []string{"dark", "light", "dracula", "tokyo-night", "pink", "ascii", "notty"}

// markdownRenderer renders Markdown for the terminal. One renderer serves a
// whole answer, so every part of a streamed answer has the same width and style.
type markdownRenderer struct {
	r *glamour.TermRenderer
}

func newMarkdownRenderer(style string) *markdownRenderer {
	r, err := glamour.NewTermRenderer(
		glamourStyle(style),
		glamour.WithWordWrap(min(terminalWidth(), 120)),
	)
	if err != nil {
		return &markdownRenderer{}
	}
	return &markdownRenderer{r: r}
}

// glamourStyle loads a built-in style without margins and without the
// highlighter's error token. Margins indent every line, so text and code
// copied from the terminal would carry extra leading spaces. The lexers mark
// text they do not understand as an error, such as the colon in a URL inside a
// PowerShell block, and the style paints it red. Glamour registers the code
// theme of the first renderer that runs for the whole process, so ai must not
// render with any other style config.
func glamourStyle(name string) glamour.TermRendererOption {
	base, ok := glamourstyles.DefaultStyles[name]
	if !ok {
		return glamour.WithStylePath(name)
	}
	cfg := *base
	noMargin := uint(0)
	cfg.Document.Margin = &noMargin
	cfg.CodeBlock.Margin = &noMargin
	if cfg.CodeBlock.Chroma != nil {
		chroma := *cfg.CodeBlock.Chroma
		chroma.Error = glamouransi.StylePrimitive{}
		cfg.CodeBlock.Chroma = &chroma
	}
	return glamour.WithStyles(cfg)
}

// render returns the rendered text without the blank lines glamour puts
// around a document, so that parts rendered one by one can be joined with a
// single blank line, which is how glamour spaces blocks in a whole document.
func (m *markdownRenderer) render(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if m.r == nil {
		return strings.TrimSpace(text)
	}
	out, err := m.r.Render(text)
	if err != nil {
		return strings.TrimSpace(text)
	}
	lines := trimBlankLines(strings.Split(out, "\n"))
	for i, line := range lines {
		lines[i] = trimPadding(line)
	}
	rendered := strings.Join(lines, "\n")
	// Code keeps its tabs, but the live view measures a tab as no width while
	// the terminal moves it to the next stop, so the view would erase too few
	// cells on its next frame.
	return strings.ReplaceAll(rendered, "\t", "    ")
}

// paddingCell is one cell of the padding glamour adds to fill a line to the
// wrap width: a space, or a space or nothing in a style of its own.
var paddingCell = regexp.MustCompile(`(?: |\x1b\[([0-9;]*)m ?\x1b\[0?m)$`)

// trimPadding removes the padding at the end of a rendered line, so that text
// copied from the terminal has no trailing spaces. A cell with a background
// color is part of the text, such as the edge of a heading, and stays.
func trimPadding(line string) string {
	for {
		m := paddingCell.FindStringSubmatchIndex(line)
		if m == nil {
			return line
		}
		if m[2] >= 0 && hasBackground(line[m[2]:m[3]]) {
			return line
		}
		line = line[:m[0]]
	}
}

// hasBackground reports whether SGR parameters set a background color.
func hasBackground(params string) bool {
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		n, err := strconv.Atoi(ps[i])
		if err != nil {
			continue
		}
		switch {
		case n == 48, n >= 40 && n <= 47, n >= 100 && n <= 107:
			return true
		case n == 38 || n == 58:
			// The color after a foreground or underline code is not a code itself.
			if i+1 < len(ps) && ps[i+1] == "5" {
				i += 2
			} else if i+1 < len(ps) && ps[i+1] == "2" {
				i += 4
			}
		}
	}
	return false
}

// answerStream turns the events of a streamed answer into rendered pieces for
// the scrollback, each printed on lines of its own. Together the pieces read
// like the whole answer rendered at once, with each tool call where it
// happened: a blank line between blocks and around tool calls, none between
// tool calls in a row.
type answerStream struct {
	md       *markdownRenderer
	blocks   markdownStream
	streamed bool
	printed  bool
	lastTool bool
}

// delta adds answer text and returns the blocks it completed, or "".
func (a *answerStream) delta(text string) string {
	a.streamed = true
	return a.block(a.md.render(a.blocks.write(text)))
}

// tool returns the block being written, which the call ends, and then line.
func (a *answerStream) tool(line string) string {
	block := a.block(a.md.render(a.blocks.rest()))
	call := a.toolCall(line)
	if block == "" {
		return call
	}
	return block + "\n" + call
}

// done returns the rest of the answer, given the provider's final text. A
// provider that streamed nothing sends the whole answer here.
func (a *answerStream) done(text string) string {
	rest := a.blocks.rest()
	if !a.streamed {
		rest = text
	}
	return a.block(a.md.render(rest))
}

// tail renders the block still being written.
func (a *answerStream) tail() string { return a.md.render(a.blocks.tail()) }

// gap is the blank line that sets a block apart from what is printed above it.
func (a *answerStream) gap() string {
	if a.printed {
		return "\n"
	}
	return ""
}

// block sets rendered text apart from what is printed above it.
func (a *answerStream) block(text string) string {
	if text == "" {
		return ""
	}
	text = a.gap() + text
	a.printed, a.lastTool = true, false
	return text
}

// toolCall sets a tool line apart like a block, but keeps tool calls in a row together.
func (a *answerStream) toolCall(line string) string {
	if !a.lastTool {
		line = a.gap() + line
	}
	a.printed, a.lastTool = true, true
	return line
}

// markdownStream collects streamed Markdown and hands out each block once it
// is complete. The text after the last complete block stays pending.
type markdownStream struct {
	pending strings.Builder
}

// write adds text and returns the Markdown of the blocks it completed, or "".
func (s *markdownStream) write(text string) string {
	s.pending.WriteString(text)
	all := s.pending.String()
	end := completeBlocks(all)
	if end == 0 {
		return ""
	}
	s.pending.Reset()
	s.pending.WriteString(all[end:])
	return all[:end]
}

// rest returns the pending text and empties the stream.
func (s *markdownStream) rest() string {
	text := s.pending.String()
	s.pending.Reset()
	return text
}

func (s *markdownStream) tail() string { return s.pending.String() }

var (
	fenceOpen = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	listItem  = regexp.MustCompile(`^(?:[-*+]|\d{1,9}[.)])(?:\s|$)`)
)

// completeBlocks returns the length of the longest prefix of text that ends
// with complete blocks, or 0. A block is complete once a blank line outside a
// code fence follows it and the next block has begun.
func completeBlocks(text string) int {
	end := 0
	fence := ""
	boundary := false
	for start := 0; start < len(text); {
		line := text[start:]
		complete := false
		if nl := strings.IndexByte(line, '\n'); nl >= 0 {
			line, complete = line[:nl], true
		}
		// An unfinished short line could still turn out to be a list item, such as "1".
		known := complete || len(line) >= 4
		if fence == "" && boundary && known && startsBlock(line) {
			end = start
		}
		if !complete {
			break
		}
		if fence != "" {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
				fence = ""
				// Nothing can continue a closed fence, so the next line starts a block.
				boundary = true
			}
		} else if open := fenceOpen.FindStringSubmatch(line); open != nil {
			fence, boundary = open[1], false
		} else {
			boundary = strings.TrimSpace(line) == ""
		}
		start += len(line) + 1
	}
	return end
}

// startsBlock reports whether a line after a blank line starts a new block. It
// must start at the margin, since an indented line can still belong to the
// block before it. A list item does not count either: a list rendered in parts
// spaces its items apart.
func startsBlock(line string) bool {
	return line != "" && line[0] != ' ' && line[0] != '\t' && !listItem.MatchString(line)
}
