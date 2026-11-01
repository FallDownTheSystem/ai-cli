package main

import (
	"strings"
	"testing"
)

func TestSystemPromptsCarryInstructions(t *testing.T) {
	env := environment{os: "Linux", shell: shell{name: "bash"}, instructions: "Answer in Finnish."}
	for name, prompt := range map[string]string{"ask": askSystemPrompt(env), "execute": executeSystemPrompt(env)} {
		if !strings.HasSuffix(prompt, "<instructions>\nAnswer in Finnish.\n</instructions>") {
			t.Errorf("%s prompt does not end with the instructions:\n%s", name, prompt)
		}
	}
	env.instructions = ""
	if strings.Contains(askSystemPrompt(env), "<instructions>") {
		t.Error("the ask prompt has an instructions block without instructions")
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct{ in, command, refusal string }{
		{"ls -la", "ls -la", ""},
		{"```bash\nls -la\n```", "ls -la", ""},
		{"```\nGet-ChildItem\n```\n", "Get-ChildItem", ""},
		{"$ ls -la", "ls -la", ""},
		{"  git status  ", "git status", ""},
		{"```powershell\na\nb\n```", "a\nb", ""},
		{"# No command can read your mind.", "", "No command can read your mind."},
		{"# list files\nls", "# list files\nls", ""},
	}
	for _, tt := range tests {
		if command, refusal := parseCommand(tt.in); command != tt.command || refusal != tt.refusal {
			t.Errorf("parseCommand(%q) = %q, %q; want %q, %q", tt.in, command, refusal, tt.command, tt.refusal)
		}
	}
}
