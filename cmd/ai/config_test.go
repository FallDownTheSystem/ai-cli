package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func clearAIEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AI_PROVIDER", "AI_MODEL", "AI_EFFORT", "AI_SHELL", "AI_STREAM", "AI_CONTEXT_LINES", "AI_CONTEXT_COMMANDS", "AI_CLAUDE_PATH", "AI_CODEX_PATH", "GLAMOUR_STYLE"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestConfigRoundTrip(t *testing.T) {
	clearAIEnv(t)
	want := config{
		"provider":         "codex",
		"effort":           "medium",
		"stream":           "false",
		"context_commands": "7",
		"claude.model":     "haiku",
		"claude.path":      "/opt/\"odd\" dir/claude",
		"codex.model":      "gpt-6-astra",
		"codex.path":       `C:\Program Files\codex\codex.exe`,
	}
	if err := saveConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got, want) {
		t.Errorf("round trip:\n got %v\nwant %v", got, want)
	}
}

func TestTOMLString(t *testing.T) {
	got := tomlString("a \"b\" C:\\x\nnext\x01")
	want := `"a \"b\" C:\\x\nnext\u0001"`
	if got != want {
		t.Errorf("tomlString = %s, want %s", got, want)
	}
}

func TestLoadConfigHandWritten(t *testing.T) {
	clearAIEnv(t)
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "# mine\nprovider = \"claude\"   # comment\nstream = false\ncontext_lines = 300\n\n[claude]\nmodel = 'opus'\npath = 'C:\\tools\\claude.exe'\n[codex]\nmodel = \"gpt # not a comment\"\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := config{
		"provider":      "claude",
		"stream":        "false",
		"context_lines": "300",
		"claude.model":  "opus",
		"claude.path":   `C:\tools\claude.exe`,
		"codex.model":   "gpt # not a comment",
	}
	if !maps.Equal(c, want) {
		t.Errorf("got %v", c)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	clearAIEnv(t)
	for _, text := range []string{
		"nonsense\n", "provider = claude\n", "provider = \"gemini\"\n", "colour = \"red\"\n",
		"context_lines = -1\n", "stream = \"maybe\"\n", "[gemini]\nmodel = \"x\"\n", "[claude.extra]\nx = 1\n",
	} {
		if err := os.MkdirAll(filepath.Dir(configPath()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath(), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(); err == nil {
			t.Errorf("%q: want error", text)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	clearAIEnv(t)

	s := resolve(options{}, config{})
	if s.provider != "codex" || s.model != "luna" || s.effort != "low" || s.contextLines != 500 || s.contextCommands != 5 || s.style != "dark" || !s.stream {
		t.Errorf("defaults: %+v", s)
	}
	if s = resolve(options{provider: "claude"}, config{}); s.model != "sonnet" {
		t.Errorf("claude default model: %+v", s)
	}

	t.Setenv("AI_STREAM", "0")
	if s = resolve(options{}, config{}); s.stream {
		t.Errorf("AI_STREAM=0 still streams")
	}
	if s = resolve(options{}, config{"stream": "true"}); !s.stream {
		t.Errorf("config stream=true lost to the environment")
	}
	if s = resolve(options{stream: "false"}, config{"stream": "true"}); s.stream {
		t.Errorf("--no-stream lost to the config")
	}

	// The provider name is matched without case, and AI_MODEL still belongs to it.
	t.Setenv("AI_PROVIDER", "Codex")
	t.Setenv("AI_MODEL", "gpt-env")
	if s = resolve(options{}, config{}); s.provider != "codex" || s.model != "gpt-env" {
		t.Errorf("mixed-case AI_PROVIDER: %+v", s)
	}

	t.Setenv("AI_PROVIDER", "codex")
	t.Setenv("AI_MODEL", "gpt-env")
	t.Setenv("AI_EFFORT", "high")
	s = resolve(options{}, config{})
	if s.provider != "codex" || s.model != "gpt-env" || s.effort != "high" {
		t.Errorf("env fallback: %+v", s)
	}

	c := config{"provider": "claude", "effort": "medium", "claude.model": "opus"}
	s = resolve(options{}, c)
	if s.provider != "claude" || s.model != "opus" || s.effort != "medium" {
		t.Errorf("config over env: %+v", s)
	}

	// AI_MODEL names a codex model, so it must not reach claude.
	s = resolve(options{}, config{"provider": "claude"})
	if s.model != "sonnet" {
		t.Errorf("env model leaked to another provider: %+v", s)
	}

	s = resolve(options{provider: "codex"}, c)
	if s.provider != "codex" || s.model != "gpt-env" {
		t.Errorf("flag provider: %+v", s)
	}
	s = resolve(options{model: "haiku"}, c)
	if s.model != "haiku" {
		t.Errorf("flag model: %+v", s)
	}
}

func TestConfigModelEdit(t *testing.T) {
	clearAIEnv(t)
	m := newConfigModel(config{})
	row := func(key string) int {
		return slices.IndexFunc(m.rows(), func(k keySpec) bool { return k.key == key })
	}
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	typeValue := func(label, value string) {
		t.Helper()
		m.row = row(label)
		m.Update(enter)
		if !m.editing {
			t.Fatalf("%s: enter did not open the editor", label)
		}
		m.input.SetValue(value)
		m.Update(enter)
	}

	// A rejected value keeps the editor open; fixing it must close it again.
	typeValue("context_lines", "lots")
	if !m.editing || m.err == "" {
		t.Fatalf("bad number: editing=%v err=%q", m.editing, m.err)
	}
	m.input.SetValue("300")
	m.Update(enter)
	if m.editing || m.err != "" || m.cfg["context_lines"] != "300" {
		t.Errorf("fixed number: editing=%v err=%q cfg=%v", m.editing, m.err, m.cfg)
	}

	// Leaving the editor drops its error.
	typeValue("context_commands", "-1")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, set := m.cfg["context_commands"]; m.editing || m.err != "" || set {
		t.Errorf("esc: editing=%v err=%q cfg=%v", m.editing, m.err, m.cfg)
	}

	// A path pasted from Explorer's "Copy as path" comes in quotes.
	typeValue("claude.path", `"C:\Program Files\claude\claude.exe"`)
	if got := m.cfg["claude.path"]; got != `C:\Program Files\claude\claude.exe` {
		t.Errorf("quoted path saved as %q", got)
	}
}

func TestApplyAssignments(t *testing.T) {
	c := config{"claude.model": "opus"}
	if err := applyAssignments(c, "claude", []string{"model=gpt-6-astra", "provider=codex", "effort=high"}); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(c, config{"provider": "codex", "codex.model": "gpt-6-astra", "claude.model": "opus", "effort": "high"}) {
		t.Errorf("got %v", c)
	}

	if err := applyAssignments(c, "codex", []string{"claude.model=haiku", "model="}); err != nil {
		t.Fatal(err)
	}
	if _, set := c["codex.model"]; c["claude.model"] != "haiku" || set {
		t.Errorf("explicit and clearing: %v", c)
	}

	for _, bad := range [][]string{{"model"}, {"provider=gemini"}, {"colour=red"}} {
		if err := applyAssignments(c, "claude", bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
