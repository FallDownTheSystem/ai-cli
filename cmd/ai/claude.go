package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var claudeSpec = providerSpec{
	name: "claude",
	// The Claude CLI resolves these aliases itself, to the newest model of each family.
	models:       []string{"haiku", "sonnet", "opus", "fable"},
	defaultModel: "sonnet",
	efforts:      func(string) []string { return []string{"low", "medium", "high", "xhigh", "max"} },
	installPaths: []string{filepath.Join(homeDir(), ".local", "bin", "claude")},
	complete:     completeClaude,
}

// completeClaude runs the Claude CLI in print mode and reads its stream-json events.
func completeClaude(ctx context.Context, p provider, c call) (string, error) {
	cmd := exec.CommandContext(ctx, p.bin, claudeArgs(p, c)...)
	cmd.Dir = c.workDir()
	cmd.Stdin = strings.NewReader(c.prompt)
	// Skips telemetry, update checks and the built-in plugin and agent loading.
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")

	agent, err := startAgent(cmd)
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	turn := &claudeTurn{c: c}
	for line := range agent.lines() {
		turn.handle(line)
	}
	return turn.answer(agent.wait(ctx))
}

// claudeTools are read-only: they look at files and the web but change nothing.
const claudeTools = "Read,Grep,Glob,WebSearch,WebFetch"

// claudeArgs switches off every extra the CLI would load. --bare would skip
// the most startup work, but it refuses subscription (OAuth) login.
func claudeArgs(p provider, c call) []string {
	tools := ""
	if c.tools {
		tools = claudeTools
	}
	args := []string{
		"-p",
		// Skips hooks, MCP servers, user skills, plugins and CLAUDE.md, keeps auth.
		"--safe-mode",
		"--disable-slash-commands",
		"--tools", tools,
		"--strict-mcp-config",
		"--no-session-persistence",
		"--model", p.model,
		"--effort", p.effort,
		// Replaces the whole default agent prompt, so no tool, skill or
		// output-style text reaches the model.
		"--system-prompt", c.system,
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
	}
	if c.tools {
		args = append(args,
			// The folder may not be trusted, and -p skips the trust prompt, so its
			// .claude settings, which can set permissions and environment, stay unread.
			"--setting-sources", "user",
			// A call that is not allowed is refused instead of asked about, whatever
			// mode the user's settings pick.
			"--permission-mode", "dontAsk",
			// Read, Grep and Glob work inside the working directory and nowhere else.
			"--settings", `{"permissions":{"blockReadsOutsideWorkingDirectories":true}}`,
			"--allowedTools", "WebSearch,WebFetch",
		)
	}
	return args
}

// claudeTurn follows the stream-json events of one run.
type claudeTurn struct {
	c        call
	streamed strings.Builder
	// result is the text of the result event, which only a finished run sends.
	result *string
	err    error
}

type claudeEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Event   struct {
		Type         string `json:"type"`
		ContentBlock struct {
			Type string `json:"type"`
		} `json:"content_block"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	Message struct {
		Content []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

func (t *claudeTurn) handle(line []byte) {
	var ev claudeEvent
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	switch ev.Type {
	case "stream_event":
		switch {
		case ev.Event.Type == "content_block_start" && ev.Event.ContentBlock.Type == "text" && t.streamed.Len() > 0:
			// Text blocks arrive one after another, so a new one starts a new paragraph.
			t.write("\n\n")
		case ev.Event.Type == "content_block_delta" && ev.Event.Delta.Type == "text_delta":
			t.write(ev.Event.Delta.Text)
		}
	case "assistant":
		for _, block := range ev.Message.Content {
			if block.Type == "tool_use" {
				t.c.sendTool(describeClaudeTool(block.Name, block.Input, t.c.workDir()))
				// Text before a tool call is the model's narration, not the answer.
				t.streamed.Reset()
			}
		}
	case "result":
		t.result = &ev.Result
		if ev.IsError {
			t.err = errors.New("claude: " + cmp.Or(ev.Result, ev.Subtype))
		}
	}
}

func (t *claudeTurn) write(text string) {
	t.streamed.WriteString(text)
	t.c.sendDelta(text)
}

// answer is the outcome of the run once the process has ended with runErr.
func (t *claudeTurn) answer(runErr error) (string, error) {
	switch {
	case t.err != nil:
		return "", t.err
	case runErr != nil:
		return "", fmt.Errorf("claude: %w", runErr)
	case t.result == nil:
		// Without the result event the text may be cut off.
		return "", errors.New("claude: the run ended without a result")
	}
	return cmp.Or(*t.result, t.streamed.String()), nil
}

func describeClaudeTool(name string, input map[string]any, dir string) string {
	str := func(key string) string {
		s, _ := input[key].(string)
		return s
	}
	switch name {
	case "Read":
		return "read " + relativePath(str("file_path"), dir)
	case "Grep":
		if path := str("path"); path != "" {
			return "grep " + str("pattern") + " in " + relativePath(path, dir)
		}
		return "grep " + str("pattern")
	case "Glob":
		return "glob " + str("pattern")
	case "WebSearch":
		return "search " + str("query")
	case "WebFetch":
		return "fetch " + str("url")
	}
	return strings.ToLower(name)
}
