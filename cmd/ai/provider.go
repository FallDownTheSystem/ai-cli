package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// providerSpec describes one agent CLI: the models ai offers for it, the
// efforts each model accepts, and how it runs a call.
type providerSpec struct {
	name         string
	models       []string
	defaultModel string
	efforts      func(model string) []string
	// installPaths are checked when the CLI is not on PATH.
	installPaths []string
	complete     func(ctx context.Context, p provider, c call) (string, error)
}

// A provider runs one stateless prompt through a subscription-backed agent CLI.
type provider struct {
	spec   providerSpec
	bin    string
	model  string
	effort string
}

func (p provider) complete(ctx context.Context, c call) (string, error) {
	return p.spec.complete(ctx, p, c)
}

// providers are the agent CLIs ai can run, in the order the settings picker shows them.
var providers = []providerSpec{claudeSpec, codexSpec}

func providerNames() []string {
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.name
	}
	return names
}

func lookupProvider(name string) (providerSpec, error) {
	for _, p := range providers {
		if p.name == name {
			return p, nil
		}
	}
	return providerSpec{}, fmt.Errorf("unknown provider %q (use %s)", name, strings.Join(providerNames(), " or "))
}

func newProvider(s settings) (provider, error) {
	spec, err := lookupProvider(s.provider)
	if err != nil {
		return provider{}, err
	}
	bin, err := findBinary(s.binPath, spec.name, spec.installPaths...)
	if err != nil {
		return provider{}, err
	}
	return provider{spec: spec, bin: bin, model: s.model, effort: s.effort}, nil
}

func findBinary(configured, name string, fallbacks ...string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, p := range fallbacks {
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s CLI not found on PATH (install it, or set its path with: ai --config %s.path=...)", name, name)
}

type call struct {
	system string
	prompt string
	// tools lets the agent read files under dir and search the web, over as many
	// turns as it needs. Without it the agent gets no tools and runs from the temp
	// directory, so nothing about the current folder reaches it.
	tools bool
	dir   string
	// onDelta receives answer text as it streams.
	onDelta func(string)
	// onTool receives a one-line description of each tool call as it starts.
	onTool func(string)
}

func (c call) sendDelta(s string) {
	if c.onDelta != nil {
		c.onDelta(s)
	}
}

func (c call) sendTool(s string) {
	// Each call shows as one line, so a command of several lines is joined up.
	s = strings.Join(strings.Fields(s), " ")
	if c.onTool != nil && s != "" {
		c.onTool(s)
	}
}

func (c call) workDir() string {
	if c.tools && c.dir != "" {
		return c.dir
	}
	return os.TempDir()
}

// agentProcess is a running agent CLI that writes one JSON object per line.
type agentProcess struct {
	cmd    *exec.Cmd
	out    *bufio.Reader
	stderr bytes.Buffer
}

func startAgent(cmd *exec.Cmd) (*agentProcess, error) {
	a := &agentProcess{cmd: cmd}
	cmd.Stderr = &a.stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	a.out = bufio.NewReader(stdout)
	return a, nil
}

// next returns the next line that holds a JSON object, or io.EOF at the end of
// the output. Lines have no size limit: a reader that gave up on a long line
// would leave the agent blocked on a full pipe.
func (a *agentProcess) next() ([]byte, error) {
	for {
		line, err := a.out.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 && line[0] == '{' {
			return line, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// lines yields every JSON line until the output ends. A read that fails before
// the end kills the agent, so that wait reports the failure.
func (a *agentProcess) lines() iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		for {
			line, err := a.next()
			if err != nil {
				if err != io.EOF {
					a.cmd.Process.Kill()
				}
				return
			}
			if !yield(line) {
				return
			}
		}
	}
}

// wait waits for the agent to exit and says why it failed. Once ctx is
// cancelled, the exit status only says that the agent was killed.
func (a *agentProcess) wait(ctx context.Context) error {
	err := a.cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return a.withStderr(err)
	}
	return nil
}

// stop ends an agent that would keep running, such as a server.
func (a *agentProcess) stop() {
	a.cmd.Process.Kill()
	a.cmd.Wait()
}

// withStderr adds the last lines the agent wrote to stderr, which say why it
// failed where the exit status alone does not.
func (a *agentProcess) withStderr(err error) error {
	msg := strings.TrimSpace(a.stderr.String())
	if msg == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, strings.Join(last(strings.Split(msg, "\n"), 5), "\n"))
}
