package main

import (
	"cmp"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	t.Setenv("AI_PROVIDER", "")
	t.Setenv("AI_MODEL", "")
	tests := []struct {
		args []string
		want options
	}{
		{[]string{"how", "do", "I", "x"}, options{question: "how do I x"}},
		{[]string{"-e", "list", "files"}, options{execute: true, question: "list files"}},
		{[]string{"-ec", "again"}, options{execute: true, depth: 1, question: "again"}},
		{[]string{"-c", "-e", "again"}, options{execute: true, depth: 1, question: "again"}},
		{[]string{"-cc", "again"}, options{depth: 2, question: "again"}},
		{[]string{"-c", "-c", "again"}, options{depth: 2, question: "again"}},
		{[]string{"-c3", "again"}, options{depth: 3, question: "again"}},
		{[]string{"-ec4", "again"}, options{execute: true, depth: 4, question: "again"}},
		{[]string{"-c=3", "again"}, options{depth: 3, question: "again"}},
		{[]string{"--continue=2", "again"}, options{depth: 2, question: "again"}},
		{[]string{"-c", "3", "files", "are", "missing"}, options{depth: 1, question: "3 files are missing"}},
		{[]string{"-c0", "zero"}, options{question: "-c0 zero"}},
		{[]string{"-p", "codex", "-m=gpt", "hi"}, options{provider: "codex", model: "gpt", question: "hi"}},
		{[]string{"--provider=codex", "hi"}, options{provider: "codex", question: "hi"}},
		{[]string{"what", "does", "ls", "-c", "do"}, options{question: "what does ls -c do"}},
		{[]string{"--", "-e", "is", "a", "flag"}, options{question: "-e is a flag"}},
		{[]string{"-h264", "vs", "av1"}, options{question: "-h264 vs av1"}},
		{[]string{"-e"}, options{execute: true}},
		{[]string{"-t", "latest", "go"}, options{tools: true, question: "latest go"}},
		{[]string{"-etc2", "again"}, options{execute: true, tools: true, depth: 2, question: "again"}},
		{[]string{"--%", "-c", "what's", "up"}, options{depth: 1, question: "what's up"}},
		{[]string{"--no-stream", "hi"}, options{stream: "false", question: "hi"}},
		{[]string{"--stream", "-t", "hi"}, options{stream: "true", tools: true, question: "hi"}},
		{[]string{"--setup"}, options{setup: true}},
		{[]string{"--init", "zsh"}, options{setup: true, setupShell: "zsh"}},
		{[]string{"--init=fish"}, options{setup: true, setupShell: "fish"}},
		{[]string{"--config"}, options{configure: true, assignments: []string{}}},
		{[]string{"--config", "provider=codex", "codex.path=C:\\a b\\codex.exe"}, options{configure: true, assignments: []string{"provider=codex", "codex.path=C:\\a b\\codex.exe"}}},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.args)
		if err != nil {
			t.Errorf("parseArgs(%q) error: %v", tt.args, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseArgs(%q) = %+v, want %+v", tt.args, got, tt.want)
		}
	}
}

func TestUsageNamesEverySetting(t *testing.T) {
	for _, k := range keySpecs {
		// Per-provider keys show once, by their field: model, path.
		name := cmp.Or(k.field, k.key)
		if !strings.Contains(usage, name) {
			t.Errorf("usage does not name the setting %s", k.key)
		}
		if k.env != "" && !strings.Contains(usage, k.env) {
			t.Errorf("usage does not name the variable %s", k.env)
		}
	}
}

func TestParseArgsSpecial(t *testing.T) {
	if _, err := parseArgs([]string{"-h"}); !errors.Is(err, errHelp) {
		t.Errorf("-h: got %v", err)
	}
	if _, err := parseArgs([]string{"--version"}); !errors.Is(err, errVersion) {
		t.Errorf("--version: got %v", err)
	}
	if _, err := parseArgs([]string{"-p"}); err == nil {
		t.Error("-p without value: want error")
	}
	if _, err := parseArgs([]string{"-c=x", "hi"}); err == nil {
		t.Error("-c=x: want error")
	}
}
