package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeTurn(t *testing.T) {
	var deltas strings.Builder
	var tools []string
	turn := &claudeTurn{c: call{
		onDelta: func(s string) { deltas.WriteString(s) },
		onTool:  func(s string) { tools = append(tools, s) },
	}}
	for _, line := range []string{
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Let me look."}}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"WebSearch","input":{"query":"go 1.27"}}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Go 1.27 "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"is out."}}}`,
		`not json`,
	} {
		turn.handle([]byte(line))
	}
	if got := deltas.String(); got != "Let me look.Go 1.27 \n\nis out." {
		t.Errorf("deltas = %q", got)
	}
	if strings.Join(tools, "|") != "search go 1.27" {
		t.Errorf("tools = %q", tools)
	}
	if _, err := turn.answer(nil); err == nil {
		t.Error("a run without a result event must fail")
	}

	// The streamed text after the last tool call is the answer when the result is empty.
	turn.handle([]byte(`{"type":"result","result":""}`))
	if answer, err := turn.answer(nil); err != nil || answer != "Go 1.27 \n\nis out." {
		t.Errorf("answer = %q, %v", answer, err)
	}
	turn.handle([]byte(`{"type":"result","is_error":true,"subtype":"error_max_turns"}`))
	if _, err := turn.answer(nil); err == nil || !strings.Contains(err.Error(), "error_max_turns") {
		t.Errorf("error result: %v", err)
	}
}

func TestDescribeClaudeTool(t *testing.T) {
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	dir := filepath.Join(root, "proj")
	other := filepath.Join(root, "other", "x.go")
	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"Read", map[string]any{"file_path": filepath.Join(dir, "cmd", "main.go")}, "read " + filepath.Join("cmd", "main.go")},
		{"Read", map[string]any{"file_path": other}, "read " + other},
		{"Grep", map[string]any{"pattern": "func main"}, "grep func main"},
		{"WebSearch", map[string]any{"query": "go 1.27"}, "search go 1.27"},
		{"WebFetch", map[string]any{"url": "https://go.dev"}, "fetch https://go.dev"},
	}
	for _, tt := range tests {
		if got := describeClaudeTool(tt.name, tt.input, dir); got != tt.want {
			t.Errorf("describeClaudeTool(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
