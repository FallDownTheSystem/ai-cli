package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCompleteBlocks(t *testing.T) {
	tests := []struct {
		text string
		want string // the complete prefix
	}{
		{"one paragraph", ""},
		{"first\n\n", ""},
		{"first\n\nsec", ""},
		{"first\n\nsecond", "first\n\n"},
		{"first\n\n  indented", ""},
		{"first\n\n- item", ""},
		{"- a\n\n- b\n\nAfter", "- a\n\n- b\n\n"},
		{"1. a\n\n2", ""},
		{"```\ncode\n\nmore\n```\nnext", "```\ncode\n\nmore\n```\n"},
		{"```\ncode\n\nstill code", ""},
		{"~~~~\n```\n\nx\n~~~~\n\nending", "~~~~\n```\n\nx\n~~~~\n\n"},
		{"# Title\n\nText\n\n| a |\n|---|\n\nEnding", "# Title\n\nText\n\n| a |\n|---|\n\n"},
	}
	for _, tt := range tests {
		if got := tt.text[:completeBlocks(tt.text)]; got != tt.want {
			t.Errorf("completeBlocks(%q) keeps %q, want %q", tt.text, got, tt.want)
		}
	}
}

const sampleAnswer = "# Title\n\nFirst paragraph with `code` and **bold** text that is long enough to wrap around the width.\n\n" +
	"- one\n- two\n\n- loose three\n\n1. first\n2. second\n\n```go\nfunc main() {\n\n\tfmt.Println(\"hi\")\n}\n```\n" +
	"After the code.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n> quote\n\nEnd."

// printed joins the pieces of an answer stream the way the scrollback shows
// them: each piece on lines of its own.
func printed(pieces ...string) string {
	var kept []string
	for _, p := range pieces {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return plain(strings.Join(kept, "\n"))
}

// A streamed answer, printed piece by piece, must look like the whole answer rendered at once.
func TestStreamMatchesWholeRender(t *testing.T) {
	md := newMarkdownRenderer("dark")
	a := &answerStream{md: md}
	var pieces []string
	for _, r := range sampleAnswer {
		if p := a.delta(string(r)); p != "" {
			pieces = append(pieces, p)
		}
	}
	pieces = append(pieces, a.done(sampleAnswer))
	if len(pieces) < 5 {
		t.Fatalf("the answer came out in %d pieces; blocks were not split", len(pieces))
	}
	if got, want := printed(pieces...), plain(md.render(sampleAnswer)); got != want {
		t.Errorf("streamed:\n%s\n\nwhole:\n%s", got, want)
	}
}

func TestAnswerStreamToolCalls(t *testing.T) {
	md := newMarkdownRenderer("dark")
	a := &answerStream{md: md}
	got := printed(
		a.delta("Let me look"),
		a.tool("TOOL ONE"),
		a.tool("TOOL TWO"),
		a.delta("The answer.\n\nMore"),
		a.done("The answer.\n\nMore"),
	)
	want := plain(md.render("Let me look")) + "\n\nTOOL ONE\nTOOL TWO\n\n" + plain(md.render("The answer.\n\nMore"))
	if got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}

	// A provider that streamed nothing sends the whole answer at the end.
	whole := &answerStream{md: md}
	if got, want := printed(whole.done("Only at the end.")), plain(md.render("Only at the end.")); got != want {
		t.Errorf("unstreamed answer: %q, want %q", got, want)
	}
}

func plain(s string) string {
	lines := strings.Split(ansi.Strip(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func TestGlamourStyleDropsErrorToken(t *testing.T) {
	const block = "```pwsh\nStart-Process https://example.com\n```"
	// The stock style paints the colon in the URL with a red background; no other
	// token here has one. Only this renderer may run in the test binary, because
	// glamour keeps the first code theme it registers for the whole process.
	if out := newMarkdownRenderer("dark").render(block); strings.Contains(out, "[48;") {
		t.Errorf("error token still styled: %q", out)
	}
}
