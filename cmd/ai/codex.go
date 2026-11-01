package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"

	"github.com/BurntSushi/toml"
)

var codexSpec = providerSpec{
	name:         "codex",
	models:       codexAliasNames(),
	defaultModel: "luna",
	efforts:      func(model string) []string { return loadCodexCatalog().efforts(model) },
	complete:     completeCodex,
}

// completeCodex talks to codex app-server over JSON-RPC on stdio. Unlike
// codex exec, the app server streams the answer as it is written.
func completeCodex(ctx context.Context, p provider, c call) (string, error) {
	catalog := loadCodexCatalog()
	model := catalog.slug(p.model)
	args := codexArgs(c)
	if path, ok := writeCodexCatalog(catalog, model); ok {
		// Removed once the server has stopped, since Windows cannot remove a file
		// that is still open.
		defer os.Remove(path)
		args = append(args, "-c", "model_catalog_json="+tomlString(path))
	}
	cmd := exec.CommandContext(ctx, p.bin, args...)
	cmd.Dir = c.workDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	agent, err := startAgent(cmd)
	if err != nil {
		return "", fmt.Errorf("codex: %w", err)
	}
	// The server keeps running until its input closes; nothing is left to wait for
	// once the turn is over.
	defer func() {
		stdin.Close()
		agent.stop()
	}()

	s := &codexSession{in: stdin, out: agent}
	answer, err := s.run(c, model, p.effort)
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case errors.Is(err, io.EOF):
		return "", fmt.Errorf("codex: %w", agent.withStderr(errors.New("the app server stopped")))
	case err != nil:
		return "", fmt.Errorf("codex: %w", err)
	}
	return answer, nil
}

// writeCodexCatalog writes the directCatalog of the model to a temp file for
// model_catalog_json, which Codex reads in place of its own catalog. Without
// one, Codex runs with its own catalog and the larger requests that come with it.
func writeCodexCatalog(catalog codexCatalog, model string) (string, bool) {
	data, ok := catalog.directCatalog(model)
	if !ok {
		return "", false
	}
	f, err := os.CreateTemp("", "ai-codex-catalog-*.json")
	if err != nil {
		return "", false
	}
	_, err = f.Write(data)
	if err = cmp.Or(err, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", false
	}
	return f.Name(), true
}

// codexFeaturesOff lists features that add tools or catalogs to every request
// and that a one-shot answer never uses. Codex ignores names it does not know,
// so the list is safe across versions.
var codexFeaturesOff = []string{
	"apps", "plugins", "remote_plugin", "memories", "multi_agent", "image_generation",
	"computer_use", "browser_use", "browser_use_external", "in_app_browser", "hooks",
	"goals", "sleep_tool", "tool_suggest", "skill_search", "skill_mcp_dependency_install",
	"workspace_dependencies", "view_image", "realtime_conversation", "shell_snapshot",
	"worktrees",
}

// codexArgs overrides the parts of the user's Codex config that a one-shot
// answer must not load. The app server has no switch to skip that config as a whole.
func codexArgs(c call) []string {
	webSearch := "disabled"
	if c.tools {
		webSearch = "live"
	}
	args := []string{
		"app-server",
		"-c", "web_search=" + tomlString(webSearch),
		"-c", "project_doc_max_bytes=0",
		"-c", "agents.enabled=false",
		"-c", "memories.use_memories=false",
		// There is no switch for the skills catalog, so it gets the smallest budget,
		// which drops every description.
		"-c", "skills.max_context_tokens=1",
		"-c", `personality="none"`,
		"-c", "check_for_update_on_startup=false",
		"-c", "analytics.enabled=false",
		"-c", "notify=[]",
		// Commands start without the user's shell profile, which is slow and can print errors.
		"-c", "allow_login_shell=false",
	}
	for _, f := range codexFeaturesOff {
		args = append(args, "-c", "features."+f+"=false")
	}
	// The shell tool is how Codex reads files, so only -t keeps it.
	if !c.tools {
		args = append(args, "-c", "features.shell_tool=false")
	}
	// An override merges into the user's mcp_servers table instead of replacing
	// it, so each server is switched off by name.
	for _, name := range codexMCPServers() {
		args = append(args, "-c", "mcp_servers."+tomlKey(name)+".enabled=false")
	}
	// Without a sandbox setting, a read-only run on Windows rejects every
	// command, even one that only reads.
	if c.tools && runtime.GOOS == "windows" {
		args = append(args, "-c", `windows.sandbox="unelevated"`)
	}
	return args
}

// codexMCPServers names the MCP servers in the user's Codex config.
func codexMCPServers() []string {
	var cfg struct {
		MCPServers map[string]toml.Primitive `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(filepath.Join(codexHome(), "config.toml"), &cfg); err != nil {
		return nil
	}
	return slices.Sorted(maps.Keys(cfg.MCPServers))
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(s string) string {
	if bareKey.MatchString(s) {
		return s
	}
	return tomlString(s)
}

// codexSession is one conversation with the app server: it starts a thread
// and runs one turn in it.
type codexSession struct {
	in     io.Writer
	out    *agentProcess
	lastID int
	turn   codexTurn
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (s *codexSession) run(c call, model, effort string) (string, error) {
	s.turn = codexTurn{c: c}
	if err := s.initialize(); err != nil {
		return "", err
	}
	thread, err := s.startThread(c, model)
	if err != nil {
		return "", err
	}
	if _, err := s.request("turn/start", map[string]any{
		"threadId": thread,
		"effort":   effort,
		"input":    []map[string]any{{"type": "text", "text": c.prompt}},
	}); err != nil {
		return "", err
	}
	for !s.turn.done {
		m, err := s.next()
		if err != nil {
			return "", err
		}
		s.handle(m)
	}
	return s.turn.answer()
}

func (s *codexSession) initialize() error {
	if _, err := s.request("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "ai", "version": version},
	}); err != nil {
		return err
	}
	return s.write(map[string]any{"jsonrpc": "2.0", "method": "initialized"})
}

// startThread starts a thread that keeps nothing and may change nothing, and
// returns its id.
func (s *codexSession) startThread(c call, model string) (string, error) {
	result, err := s.request("thread/start", map[string]any{
		"ephemeral":      true,
		"cwd":            c.workDir(),
		"sandbox":        "read-only",
		"approvalPolicy": "never",
		// Replaces Codex's built-in coding-agent prompt, which is most of the request.
		"baseInstructions": c.system,
		// An empty value replaces the developer_instructions in the user's
		// config, which are written for their coding sessions, not for ai.
		"developerInstructions": "",
		"model":                 model,
	})
	if err != nil {
		return "", err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &started); err != nil || started.Thread.ID == "" {
		return "", errors.New("the app server started no thread")
	}
	return started.Thread.ID, nil
}

// request sends one call and handles everything that arrives before its response.
func (s *codexSession) request(method string, params any) (json.RawMessage, error) {
	s.lastID++
	id := strconv.Itoa(s.lastID)
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": s.lastID, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		m, err := s.next()
		if err != nil {
			return nil, err
		}
		if m.Method == "" && string(m.ID) == id {
			if m.Error != nil {
				return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
			}
			return m.Result, nil
		}
		s.handle(m)
	}
}

func (s *codexSession) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.in.Write(append(b, '\n'))
	return err
}

func (s *codexSession) next() (rpcMessage, error) {
	for {
		line, err := s.out.next()
		if err != nil {
			return rpcMessage{}, err
		}
		var m rpcMessage
		if json.Unmarshal(line, &m) == nil {
			return m, nil
		}
	}
}

func (s *codexSession) handle(m rpcMessage) {
	if m.Method != "" && len(m.ID) > 0 {
		// A request from the server, such as a question for the user. A one-shot
		// run has nobody to answer it, and the turn would wait for ever.
		s.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "ai cannot answer " + m.Method}})
		return
	}
	s.turn.handle(m.Method, m.Params)
}

// codexTurn follows the events of one turn to its answer.
type codexTurn struct {
	c call
	// finalAnswer is the message the model marked as its final answer;
	// lastMessage stands in for a model that marks none.
	finalAnswer string
	lastMessage string
	streamed    bool
	failure     string
	done        bool
	status      string
}

type codexItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Text    string `json:"text"`
	Phase   string `json:"phase"`
	Command string `json:"command"`
	Query   string `json:"query"`
	Action  struct {
		Query string `json:"query"`
	} `json:"action"`
}

func (t *codexTurn) handle(method string, params json.RawMessage) {
	var p struct {
		Item  codexItem `json:"item"`
		Delta string    `json:"delta"`
		Turn  struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool `json:"willRetry"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	switch method {
	case "item/started":
		switch p.Item.Type {
		case "commandExecution":
			t.c.sendTool("run " + unwrapShell(p.Item.Command))
		case "agentMessage":
			// Messages arrive one after another, so a new one starts a new paragraph.
			if t.streamed {
				t.c.sendDelta("\n\n")
			}
		}
	case "item/agentMessage/delta":
		t.streamed = true
		t.c.sendDelta(p.Delta)
	case "item/completed":
		switch p.Item.Type {
		case "agentMessage":
			t.lastMessage = p.Item.Text
			if p.Item.Phase == "final_answer" {
				t.finalAnswer = p.Item.Text
			}
		case "webSearch":
			// The query is still empty when the search starts.
			t.c.sendTool("search " + cmp.Or(p.Item.Query, p.Item.Action.Query))
		}
	case "error":
		// Codex retries some stream errors itself; only the last word counts.
		if !p.WillRetry {
			t.failure = p.Error.Message
		}
	case "turn/completed":
		t.done = true
		t.status = p.Turn.Status
		if p.Turn.Error != nil && p.Turn.Error.Message != "" {
			t.failure = p.Turn.Error.Message
		}
	}
}

// answer is the outcome of the completed turn.
func (t *codexTurn) answer() (string, error) {
	switch {
	case t.status == "completed" && t.finalAnswer != "":
		return t.finalAnswer, nil
	case t.status == "completed" && t.lastMessage != "":
		return t.lastMessage, nil
	case t.failure != "":
		return "", errors.New(t.failure)
	case t.status == "completed":
		return "", errors.New("no answer")
	}
	// A turn that failed or broke off may still have sent messages, but those are
	// what the model said on the way, such as "I'll check the files".
	return "", fmt.Errorf("the turn ended as %s", cmp.Or(t.status, "unknown"))
}
