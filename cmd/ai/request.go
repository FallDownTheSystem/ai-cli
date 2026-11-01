package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// environment holds what the system prompt tells the model about the user's machine.
type environment struct {
	os    string
	shell shell
	cwd   string
	tools bool
	// instructions are the user's own, from the instructions setting.
	instructions string
}

func detectEnv(s settings, tools bool) environment {
	cwd, _ := os.Getwd()
	return environment{os: osName(), shell: selectShell(s.shell), cwd: cwd, tools: tools, instructions: s.instructions}
}

// userRules passes on the user's own instructions. The reply format comes
// first, since in execute mode the reply runs as the command.
func (env environment) userRules() string {
	if env.instructions == "" {
		return ""
	}
	return "\n\nThe user's own instructions follow. Follow them, except where they conflict with the reply format above.\n<instructions>\n" + env.instructions + "\n</instructions>"
}

// newCall builds the call that sends req under the system prompt.
func (env environment) newCall(system string, req request) call {
	return call{system: system, prompt: req.message(), tools: env.tools, dir: env.cwd}
}

// toolRules tells the model what it can do, so it neither guesses at facts it
// could look up nor claims it read something it could not.
func (env environment) toolRules() string {
	if !env.tools {
		return "You cannot run tools or read files. Work only from the text you receive."
	}
	return `You can read files under the working directory and search and fetch web pages. You cannot change anything.
Use the tools when the answer depends on files or current facts you would otherwise guess. Make only the calls you need, then answer.`
}

func osName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	default:
		return strings.ToUpper(runtime.GOOS[:1]) + runtime.GOOS[1:]
	}
}

// request is everything the user sends the model in one run.
type request struct {
	question string
	stdin    string
	terminal string
	// declined and feedback carry a command the user turned down into the next attempt.
	declined string
	feedback string
}

const contextRules = `If a <terminal> block is present, it holds the recent contents of the user's terminal window, oldest line first. It can include earlier questions to "ai" and their answers. Use it to understand follow-ups such as "why did that fail" or "now do the same for X".
If a <stdin> block is present, it holds data piped into the command.`

func askSystemPrompt(env environment) string {
	tools := env.toolRules()
	if env.tools {
		tools += " Cite web sources as links."
	}
	return fmt.Sprintf(`You are "ai", a command-line assistant. The user runs you from a terminal.
Environment: %s, shell %s, working directory %s.

Answer directly and concisely. Do not greet, restate the question, or offer further help.
Use Markdown that reads well in a terminal: short paragraphs, lists, and fenced code blocks with a language tag.
When the question is how to do something on the command line, give the command for the user's shell first, then a short explanation only if it helps.
%s
%s%s`, env.os, env.shell.name, env.cwd, tools, contextRules, env.userRules())
}

func executeSystemPrompt(env environment) string {
	tools := env.toolRules()
	if env.tools {
		// The reply runs as typed, so a cited source would become part of the command.
		tools += " Your last reply runs as the command, so it holds the command alone, with no sources or notes."
	}
	return fmt.Sprintf(`You turn a request into one shell command.
Environment: %s, shell %s, working directory %s.

Reply with only the command, exactly as it must be typed into %s. No Markdown, no code fences, no explanation, no prompt characters.
Prefer one line; join steps with the shell's own operators. Use several lines only when the shell needs them.
If no command can do what the user asks, reply with one line that starts with "# " and says why.
%s
%s
If a <declined> block is present, the user rejected that command. Follow their <feedback> and reply with a new command.%s`,
		env.os, env.shell.name, env.cwd, env.shell.name, tools, contextRules, env.userRules())
}

// message is the request as the model reads it: each piece of context in a
// tagged block, then the question.
func (r request) message() string {
	var b strings.Builder
	block := func(tag, body string) {
		if body != "" {
			fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n\n", tag, body, tag)
		}
	}
	block("terminal", r.terminal)
	block("stdin", r.stdin)
	block("declined", r.declined)
	block("feedback", r.feedback)
	b.WriteString(r.question)
	return b.String()
}

// parseCommand reads a reply to the execute prompt: the command, or the reason
// the model gave for writing none, as one line that starts with "# ".
func parseCommand(reply string) (command, refusal string) {
	command = cleanCommand(reply)
	if strings.HasPrefix(command, "# ") && !strings.Contains(command, "\n") {
		return "", strings.TrimPrefix(command, "# ")
	}
	return command, ""
}

// cleanCommand removes the code fences and prompt markers that models add despite instructions.
func cleanCommand(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	s = strings.TrimSpace(s)
	for _, p := range []string{"$ ", "> ", "PS> "} {
		s = strings.TrimPrefix(s, p)
	}
	return strings.TrimSpace(s)
}
