package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAppServer answers the start-up calls and then plays the turn's events.
// It returns everything it read from the client once the client closes its
// side. The events go out from their own goroutine, since a pipe has no buffer
// and the client may be writing a reply at the same time.
func fakeAppServer(t *testing.T, in io.Reader, out io.WriteCloser, turn []string) <-chan []map[string]any {
	seen := make(chan []map[string]any, 1)
	go func() {
		var got []map[string]any
		r := bufio.NewReader(in)
		send := func(s string) { fmt.Fprintln(out, s) }
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				break
			}
			var m map[string]any
			if json.Unmarshal(line, &m) != nil {
				t.Errorf("client sent bad JSON: %s", line)
				continue
			}
			got = append(got, m)
			id, _ := json.Marshal(m["id"])
			switch m["method"] {
			case "initialize":
				send(`{"id":` + string(id) + `,"result":{}}`)
			case "thread/start":
				send(`{"id":` + string(id) + `,"result":{"thread":{"id":"t1"}}}`)
			case "turn/start":
				go func() {
					send(`{"id":` + string(id) + `,"result":{"turn":{"id":"u1"}}}`)
					for _, e := range turn {
						send(e)
					}
				}()
			}
		}
		seen <- got
	}()
	return seen
}

func runFakeTurn(t *testing.T, events []string) (answer, deltas string, tools []string, err error, seen []map[string]any) {
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	done := fakeAppServer(t, serverIn, serverOut, events)
	var d strings.Builder
	c := call{system: "sys", prompt: "q", onDelta: func(s string) { d.WriteString(s) }, onTool: func(s string) { tools = append(tools, s) }}
	s := &codexSession{in: clientOut, out: &agentProcess{out: bufio.NewReader(clientIn)}}
	answer, err = s.run(c, "gpt-x", "low")
	clientOut.Close()
	return answer, d.String(), tools, err, <-done
}

func TestCodexSessionStreamsAnswer(t *testing.T) {
	answer, deltas, tools, err, seen := runFakeTurn(t, []string{
		`{"method":"item/started","params":{"item":{"type":"agentMessage","id":"m1","phase":"commentary"}}}`,
		`{"method":"item/agentMessage/delta","params":{"itemId":"m1","delta":"Checking."}}`,
		`{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"m1","phase":"commentary","text":"Checking."}}}`,
		`{"method":"item/started","params":{"item":{"type":"commandExecution","id":"c1","command":"/bin/bash -lc 'ls -la'"}}}`,
		`{"id":99,"method":"item/tool/requestUserInput","params":{}}`,
		`{"method":"error","params":{"error":{"message":"reconnecting"},"willRetry":true}}`,
		`{"method":"item/started","params":{"item":{"type":"agentMessage","id":"m2","phase":"final_answer"}}}`,
		`{"method":"item/agentMessage/delta","params":{"itemId":"m2","delta":"The "}}`,
		`{"method":"item/agentMessage/delta","params":{"itemId":"m2","delta":"answer."}}`,
		`{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"m2","phase":"final_answer","text":"The answer."}}}`,
		`{"method":"item/completed","params":{"item":{"type":"webSearch","id":"w1","query":"","action":{"query":"go release"}}}}`,
		`{"method":"turn/completed","params":{"turn":{"status":"completed"}}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "The answer." {
		t.Errorf("answer = %q", answer)
	}
	if deltas != "Checking.\n\nThe answer." {
		t.Errorf("deltas = %q", deltas)
	}
	if strings.Join(tools, "|") != "run ls -la|search go release" {
		t.Errorf("tools = %q", tools)
	}
	var thread, reply map[string]any
	for _, m := range seen {
		if m["method"] == "thread/start" {
			thread, _ = m["params"].(map[string]any)
		}
		if m["id"] == float64(99) {
			reply = m
		}
	}
	if thread["baseInstructions"] != "sys" || thread["developerInstructions"] != "" || thread["ephemeral"] != true || thread["sandbox"] != "read-only" || thread["model"] != "gpt-x" {
		t.Errorf("thread/start params = %v", thread)
	}
	if reply == nil || reply["error"] == nil {
		t.Errorf("the server's request got no error reply: %v", reply)
	}
}

func TestCodexSessionFailedTurn(t *testing.T) {
	_, _, _, err, _ := runFakeTurn(t, []string{
		`{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"m1","phase":"commentary","text":"I'll check the files."}}}`,
		`{"method":"turn/completed","params":{"turn":{"status":"failed","error":{"message":"usage limit reached"}}}}`,
	})
	if err == nil || !strings.Contains(err.Error(), "usage limit reached") {
		t.Errorf("err = %v, want the turn's failure", err)
	}
}

func TestCodexArgsDisableUserMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cfg := "[mcp_servers.docs]\ncommand = \"x\"\n[mcp_servers.docs.env]\nA = \"1\"\n[mcp_servers.\"odd.name\"]\nurl = \"https://x\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(codexArgs(call{}), " ")
	for _, want := range []string{`mcp_servers.docs.enabled=false`, `mcp_servers."odd.name".enabled=false`, "features.shell_tool=false", `web_search="disabled"`} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %s: %s", want, args)
		}
	}
	tools := strings.Join(codexArgs(call{tools: true}), " ")
	if strings.Contains(tools, "features.shell_tool=false") || !strings.Contains(tools, `web_search="live"`) {
		t.Errorf("-t args: %s", tools)
	}
}

func TestCodexCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if got := readCodexCatalog().slug("luna"); got != "gpt-6-luna" {
		t.Errorf("no cache: slug(luna) = %q", got)
	}
	cache := `{"models":[
		{"slug":"gpt-5.6-sol","visibility":"list","priority":6},
		{"slug":"gpt-7-sol","visibility":"list","priority":1,"supported_reasoning_levels":[{"effort":"low"},{"effort":"max"},{"effort":"ultra"}]},
		{"slug":"gpt-8-sol","visibility":"hide","priority":0},
		{"slug":"gpt-6-luna","visibility":"list","priority":5,"supported_reasoning_levels":[{"effort":"low"},{"effort":"max"}],
			"tool_mode":"code_mode_only","apply_patch_tool_type":"freeform","experimental_supported_tools":["clock"],"context_window":272000}]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := readCodexCatalog()
	for in, want := range map[string]string{"sol": "gpt-7-sol", "luna": "gpt-6-luna", "terra": "gpt-6.1-terra", "gpt-x": "gpt-x"} {
		if got := catalog.slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"sol": "low max ultra", "luna": "low max", "gpt-x": "low medium high xhigh"} {
		if got := strings.Join(catalog.efforts(in), " "); got != want {
			t.Errorf("efforts(%q) = %q, want %q", in, got, want)
		}
	}

	data, ok := catalog.directCatalog("gpt-6-luna")
	if !ok {
		t.Fatal("no direct catalog for a listed model")
	}
	var direct struct{ Models []map[string]any }
	if err := json.Unmarshal(data, &direct); err != nil || len(direct.Models) != 1 {
		t.Fatalf("direct catalog %s: %v", data, err)
	}
	m := direct.Models[0]
	if m["slug"] != "gpt-6-luna" || m["tool_mode"] != "direct" || m["apply_patch_tool_type"] != nil ||
		len(m["experimental_supported_tools"].([]any)) != 0 || m["context_window"] != float64(272000) {
		t.Errorf("direct catalog entry = %v", m)
	}
	if _, ok := catalog.directCatalog("gpt-x"); ok {
		t.Error("a direct catalog for a model the cache does not list")
	}
}

func TestUnwrapShell(t *testing.T) {
	// Codex quotes each word as a POSIX shell reads it: single quotes when they
	// fit, double quotes with backslash escapes when not, in chunks if needed.
	tests := []struct{ in, want string }{
		{`"C:\\Program Files\\PowerShell\\7\\pwsh.exe" -Command 'Get-Content go.mod'`, "Get-Content go.mod"},
		{`"C:\\Program Files\\PowerShell\\7\\pwsh.exe" -NoProfile -Command 'Get-ChildItem -Force'`, "Get-ChildItem -Force"},
		{`pwsh.exe -Command "Write-Output 'it''s'"`, "Write-Output 'it''s'"},
		{`pwsh.exe -Command "Write-Output '"'$x'"'"`, "Write-Output '$x'"},
		{`/bin/bash -lc "grep -n \"a\\\\b\" *.go"`, `grep -n "a\\b" *.go`},
		{`/bin/bash -lc 'ls -la'`, "ls -la"},
		{"/bin/bash -lc 'cd cmd\nls'", "cd cmd\nls"},
		{`/bin/zsh -c ls`, "ls"},
		{`ls -la`, "ls -la"},
		{`python -c 'print(1)'`, `python -c 'print(1)'`},
		{`/bin/bash -lc 'ls`, `/bin/bash -lc 'ls`},
	}
	for _, tt := range tests {
		if got := unwrapShell(tt.in); got != tt.want {
			t.Errorf("unwrapShell(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCodexSessionServerGone(t *testing.T) {
	s := &codexSession{in: io.Discard, out: &agentProcess{out: bufio.NewReader(strings.NewReader(""))}}
	if _, err := s.run(call{}, "gpt-x", "low"); err == nil {
		t.Error("want an error when the server closes its output")
	}
}
