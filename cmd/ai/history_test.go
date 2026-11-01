package main

import (
	"strings"
	"testing"
)

func TestRecentHistory(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		depth    int // 0 means 1
		fallback int
		want     string
	}{
		{
			name: "depth 2 reaches back two ai topics",
			in: `PS C:\a> ai first topic
one
PS C:\a> ls
PS C:\a> ai second topic
two
PS C:\a> ai -c follow-up
three
PS C:\a> ai third topic
four
PS C:\a> ai -cc now`,
			depth:    2,
			fallback: 5,
			want: `PS C:\a> ai second topic
two
PS C:\a> ai -c follow-up
three
PS C:\a> ai third topic
four`,
		},
		{
			name: "stop-parsing token from the profile handler keeps a follow-up chain",
			in: `PS C:\a> ai --% what's this
one
PS C:\a> ai --% -c and that?
two
PS C:\a> ai --% -c why`,
			fallback: 5,
			want: `PS C:\a> ai --% what's this
one
PS C:\a> ai --% -c and that?
two`,
		},
		{
			name: "depth beyond the ai topics adds fallback commands per level",
			in: `PS C:\a> one
PS C:\a> two
PS C:\a> three
PS C:\a> ai topic
answer
PS C:\a> ai -c3 more`,
			depth:    3,
			fallback: 1,
			want: `PS C:\a> two
PS C:\a> three
PS C:\a> ai topic
answer`,
		},
		{
			name: "depth with no ai topics multiplies the fallback",
			in: `PS C:\a> one
PS C:\a> two
PS C:\a> three
PS C:\a> four
PS C:\a> ai -cc what happened`,
			depth:    2,
			fallback: 1,
			want: `PS C:\a> three
PS C:\a> four`,
		},
		{
			name: "depth larger than everything keeps all commands",
			in: `banner
PS C:\a> ai topic
answer
PS C:\a> ai -c9 more`,
			depth:    9,
			fallback: 5,
			want: `PS C:\a> ai topic
answer`,
		},
		{
			name: "stops at the previous ai command",
			in: `PS C:\a> git status
clean
PS C:\a> ai how do I list files
Use Get-ChildItem.
PS C:\b> Get-ChildItem
x.txt
PS C:\b> ai -c why only one file`,
			fallback: 5,
			want: `PS C:\a> ai how do I list files
Use Get-ChildItem.
PS C:\b> Get-ChildItem
x.txt`,
		},
		{
			name: "keeps a chain of follow-ups whole",
			in: `PS C:\a> ls
PS C:\a> ai first question
answer one
PS C:\a> ai -c second question
answer two
PS C:\a> ai -ec third`,
			fallback: 5,
			want: `PS C:\a> ai first question
answer one
PS C:\a> ai -c second question
answer two`,
		},
		{
			name: "falls back to the last commands",
			in: `PS C:\a> one
1
PS C:\a> two
2
PS C:\a> three
3
PS C:\a> ai -c what happened`,
			fallback: 2,
			want: `PS C:\a> two
2
PS C:\a> three
3`,
		},
		{
			name: "fewer commands than the fallback keeps them all",
			in: `banner text
PS C:\a> one
1
PS C:\a> ai -c what happened`,
			fallback: 5,
			want: `PS C:\a> one
1`,
		},
		{
			name: "bash prompt after cd, with ai.exe path and output that mentions ai",
			in: `me@box:~/x$ ./ai.exe what is tar
tar archives files. Pipe it into ai for more.
me@box:~/y$ tar -xf a.tar
me@box:~/y$ ai -c what did that do`,
			fallback: 5,
			want: `me@box:~/x$ ./ai.exe what is tar
tar archives files. Pipe it into ai for more.
me@box:~/y$ tar -xf a.tar`,
		},
		{
			name: "single-character prompt on its own line",
			in: `~/proj on main
❯ ai what is go
A language.
~/proj on main
❯ go version
go1.27
~/proj on main
❯ ai -c is that current`,
			fallback: 5,
			want: `❯ ai what is go
A language.
~/proj on main
❯ go version
go1.27
~/proj on main`,
		},
		{
			name: "a wrapped current command is still found",
			in: `PS C:\a> ai first
answer
PS C:\a> ai -c a very long follow-up question that the termin
al wrapped onto a second line`,
			fallback: 5,
			want: `PS C:\a> ai first
answer`,
		},
		{
			name: "ai after a separator or in a pipe",
			in: `PS C:\a> one
PS C:\a> git diff | ai write a message
msg
PS C:\a> two
PS C:\a> ai -c shorter`,
			fallback: 5,
			want: `PS C:\a> git diff | ai write a message
msg
PS C:\a> two`,
		},
		{
			name: "the current command pipes into ai -c",
			in: `PS C:\a> ai what changed
answer
PS C:\a> ls
PS C:\a> git diff | ai -c explain this diff`,
			fallback: 5,
			want: `PS C:\a> ai what changed
answer
PS C:\a> ls`,
		},
		{
			name: "a quoted separator does not hide that a prompt continues",
			in: `me@box:~$ ai first topic
one
me@box:~$ ai -c "is a | b the same; or not"
two
me@box:~$ ai -c why`,
			fallback: 5,
			want: `me@box:~$ ai first topic
one
me@box:~$ ai -c "is a | b the same; or not"
two`,
		},
		{
			name:     "unknown prompt keeps everything",
			in:       "some output\nmore output",
			fallback: 5,
			want:     "some output\nmore output",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depth := max(tt.depth, 1)
			if got := recentHistory(tt.in, depth, tt.fallback); got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestCommandParts(t *testing.T) {
	tests := map[string]string{
		`cd x; ai why`:                      "cd x|ai why",
		`a && b || c | d`:                   "a|b|c|d",
		`ai "why does a | b; fail"`:         `ai "why does a | b; fail"`,
		`ai 'it''s | here'`:                 `ai 'it''s | here'`,
		`echo "a \" | b" | ai explain`:      `echo "a \" | b"|ai explain`,
		`git diff 2>&1 | ai write a commit`: "git diff 2>|1|ai write a commit",
	}
	for in, want := range tests {
		if got := strings.Join(commandParts(in), "|"); got != want {
			t.Errorf("commandParts(%q) = %q, want %q", in, got, want)
		}
	}
}
