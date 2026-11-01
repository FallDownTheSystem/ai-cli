package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

var version = "dev"

const usage = `ai - ask a question or get a shell command from the command line

Usage:
  ai [flags] <prompt...>
  <command> | ai [flags] [prompt...]
  ai [flags]            Type the prompt on the next line, where the shell does not read it

Everything after the flags is the prompt. Quotes are optional, except around
characters the shell reads itself, such as ' or $; ai --setup handles those.

Flags:
  -e, --execute         Generate a shell command and ask before it runs
  -c, --continue        Send the terminal since the last ai question, for a follow-up
  -cc, -c3, -c=3        Reach back 2 or 3 ai questions (--continue=3)
  -t, --tools           Let the model read files here and search the web, over several turns
  -p, --provider NAME   Provider for this run: claude or codex
  -m, --model NAME      Model for this run
  --stream, --no-stream Show the answer as it is written, or only when it is done
  --config [key=value]  Edit every setting in a picker, or set keys directly
  --setup, --init       Add a key handler to the shell's startup file that passes prompts to ai
                        as typed (pwsh, powershell, zsh, bash, fish); through a pipe, print it
  -h, --help            Show this help
  -v, --version         Show the version
  --                    Stop reading flags

Settings come from flags, then the config file, then the environment.

Config file (~/.config/ai/config.toml), keys for ai --config key=value:
  provider              codex or claude (default codex)
  model                 codex: luna, terra, sol, astra (default luna); claude: haiku, sonnet, opus, fable (default sonnet)
  path                  CLI path of the provider (claude.path, codex.path)
  effort                Reasoning effort (default low); the levels depend on the model
  instructions          Your own instructions, added to every prompt
  shell                 Shell for --execute (default: the calling shell)
  stream                true or false: show the answer as it is written (default true)
  context_commands      Commands --continue sends per level with no earlier ai question (default 5)
  context_lines         Most terminal lines --continue reads (default 500)
  style                 Markdown style: dark, light, dracula, tokyo-night, pink, ascii, notty, or a JSON file

Environment fallbacks:
  AI_PROVIDER, AI_MODEL, AI_EFFORT, AI_INSTRUCTIONS, AI_SHELL, AI_STREAM, AI_CONTEXT_COMMANDS, AI_CONTEXT_LINES,
  AI_CLAUDE_PATH, AI_CODEX_PATH, GLAMOUR_STYLE

Examples:
  ai how do I find files larger than 1GB
  ai -e find files larger than 1GB
  ai -c why did that fail
  git diff | ai write a commit message
  ai -t what changed in the latest Go release
  ai --config provider=claude model=opus
  ai --setup
`

// Exit codes besides 0 and 1. A shell reports 130 for a command stopped by Ctrl+C.
const (
	exitUsage     = 2
	exitCancelled = 130
)

type options struct {
	execute bool
	tools   bool
	// depth is how many earlier ai questions -c reaches back; 0 sends no terminal context.
	depth    int
	provider string
	model    string
	// stream is "true" or "false" when a flag sets it, and "" otherwise.
	stream   string
	question string
	// configure is set by --config; assignments holds the key=value words after
	// it, kept whole so a quoted path with spaces survives.
	configure   bool
	assignments []string
	// setup is set by --setup or --init; setupShell names the shell, or "" for
	// the shell that runs ai.
	setup      bool
	setupShell string
}

func main() {
	os.Exit(run())
}

func run() int {
	opts, err := parseArgs(os.Args[1:])
	switch {
	case errors.Is(err, errHelp):
		fmt.Print(usage)
		return 0
	case errors.Is(err, errVersion):
		fmt.Println("ai", version)
		return 0
	case err != nil:
		return fail(err)
	case opts.setup:
		return runSetup(opts.setupShell)
	case opts.configure:
		return runConfig(opts)
	}

	cfg, err := loadConfig()
	if err != nil {
		return fail(err)
	}
	s := resolve(opts, cfg)
	provider, err := newProvider(s)
	if err != nil {
		return fail(err)
	}
	stdin, err := readPipedStdin()
	if err != nil {
		return fail(err)
	}
	req := request{stdin: stdin, terminal: terminalContext(opts.depth, s)}
	req.question, err = questionFor(opts, req)
	if errors.Is(err, errNoQuestion) {
		fmt.Print(usage)
		return exitUsage
	}
	if err != nil {
		return fail(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	env := detectEnv(s, opts.tools)
	if opts.execute {
		return runExecute(ctx, provider, env, req)
	}
	return runAsk(ctx, provider, env, req, s.style, s.stream)
}

var errNoQuestion = errors.New("no question")

// questionFor is what the user asks. Without words after the flags, piped or
// terminal text gets a question of its own, and on a terminal the user types
// the question on an input line.
func questionFor(opts options, req request) (string, error) {
	switch {
	case opts.question != "":
		return opts.question, nil
	case req.stdin != "" || req.terminal != "":
		return "Explain this.", nil
	case !isTerminal(os.Stdin) || !isTerminal(os.Stderr):
		return "", errNoQuestion
	case opts.execute:
		return readQuestion("describe the command")
	}
	return readQuestion("ask anything")
}

// terminalContext is the terminal text that -c sends: everything since the
// depth-th earlier ai question, or "" without -c.
func terminalContext(depth int, s settings) string {
	if depth == 0 {
		return ""
	}
	text, err := captureTerminal(s.contextLines)
	if err != nil {
		lipgloss.Fprintln(os.Stderr, warnStyle.Render("ai: no terminal context: "+err.Error()))
	}
	return recentHistory(text, depth, s.contextCommands)
}

var (
	errHelp    = errors.New("help")
	errVersion = errors.New("version")
)

// parseArgs reads flags only before the first prompt word, so a question such
// as "what does ls -c do" keeps its own dashes.
func parseArgs(args []string) (options, error) {
	var opts options
	i := 0
flags:
	for ; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			i++
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		// PowerShell's stop-parsing token, which the profile key handler inserts. PowerShell
		// removes it from the arguments, but it stays in the terminal text that -c reads.
		if arg == "--%" {
			continue
		}

		name, value, hasValue := strings.Cut(arg, "=")
		// The key handlers in init/ list the flags that take a separate value, so
		// they can tell the value from the first prompt word. A new one goes there too.
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s needs a value", name)
			}
			i++
			return args[i], nil
		}

		switch name {
		case "-h", "--help":
			return opts, errHelp
		case "-v", "--version":
			return opts, errVersion
		case "--config":
			opts.configure = true
		case "--setup", "--init":
			opts.setup = true
			// The shell name is optional; setup takes no prompt, so a next word names it.
			if hasValue {
				opts.setupShell = value
			} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				opts.setupShell = args[i]
			}
		case "-e", "--execute":
			opts.execute = true
		case "-t", "--tools":
			opts.tools = true
		case "--stream":
			opts.stream = "true"
		case "--no-stream":
			opts.stream = "false"
		case "-c", "--continue":
			if !hasValue {
				opts.depth++
				continue
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return opts, fmt.Errorf("%s needs a positive number, not %q", name, value)
			}
			opts.depth = n
		case "-p", "--provider":
			v, err := takeValue()
			if err != nil {
				return opts, err
			}
			opts.provider = strings.ToLower(v)
		case "-m", "--model":
			v, err := takeValue()
			if err != nil {
				return opts, err
			}
			opts.model = v
		default:
			if !expandShortFlags(arg, &opts) {
				// An unknown dash word starts the prompt, so "ai -h264 vs av1" still works as a question.
				break flags
			}
		}
	}
	if opts.configure {
		opts.assignments = args[i:]
		return opts, nil
	}
	opts.question = strings.TrimSpace(strings.Join(args[i:], " "))
	return opts, nil
}

// expandShortFlags accepts grouped flags such as -ec, -cc and -c3. Each c
// reaches one more ai question back; a number after c sets the depth directly.
func expandShortFlags(arg string, opts *options) bool {
	if strings.HasPrefix(arg, "--") || len(arg) < 3 {
		return false
	}
	next := *opts
	rest := arg[1:]
	for rest != "" {
		switch rest[0] {
		case 'e':
			next.execute = true
			rest = rest[1:]
		case 't':
			next.tools = true
			rest = rest[1:]
		case 'c':
			rest = rest[1:]
			digits := len(rest) - len(strings.TrimLeft(rest, "0123456789"))
			if digits == 0 {
				next.depth++
				continue
			}
			n, err := strconv.Atoi(rest[:digits])
			if err != nil || n < 1 {
				return false
			}
			next.depth = n
			rest = rest[digits:]
		default:
			return false
		}
	}
	*opts = next
	return true
}

func readPipedStdin() (string, error) {
	if isTerminal(os.Stdin) {
		return "", nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// fail reports err and returns the exit code for it. A cancelled run says
// nothing more, since whatever cancelled it has already shown that.
func fail(err error) int {
	if errors.Is(err, context.Canceled) {
		return exitCancelled
	}
	lipgloss.Fprintln(os.Stderr, errorStyle.Render("ai: "+err.Error()))
	return 1
}
