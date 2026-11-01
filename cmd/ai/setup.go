package main

import (
	"bufio"
	"cmp"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed init
var initScripts embed.FS

// shellSetup is how one shell loads the key handler that passes prompts to ai
// as typed.
type shellSetup struct {
	script      string // the handler, in init/
	line        string // the line that loads it from the startup file
	startupFile func(sh shell) (string, error)
}

var shellSetups = map[string]shellSetup{
	"pwsh":       {"init/ai.ps1", "ai --init pwsh | Out-String | Invoke-Expression", powerShellProfile},
	"powershell": {"init/ai.ps1", "ai --init powershell | Out-String | Invoke-Expression", powerShellProfile},
	"zsh":        {"init/ai.zsh", `eval "$(ai --init zsh)"`, zshrc},
	"bash":       {"init/ai.bash", `eval "$(ai --init bash)"`, bashrc},
	"fish":       {"init/ai.fish", "ai --init fish | source", fishConfig},
}

// powerShellProfile asks PowerShell for $PROFILE, since only PowerShell knows
// where it is: Documents can be redirected, for example to OneDrive.
func powerShellProfile(sh shell) (string, error) {
	out, err := exec.Command(sh.path, "-NoProfile", "-NoLogo", "-NonInteractive", "-Command", "$PROFILE").Output()
	if path := strings.TrimSpace(string(out)); err == nil && path != "" {
		return path, nil
	}
	return "", fmt.Errorf("%s did not say where its profile is: %v", sh.name, err)
}

func zshrc(shell) (string, error) {
	return filepath.Join(cmp.Or(os.Getenv("ZDOTDIR"), homeDir()), ".zshrc"), nil
}

func bashrc(shell) (string, error) { return filepath.Join(homeDir(), ".bashrc"), nil }

func fishConfig(shell) (string, error) {
	return filepath.Join(configHome(), "fish", "config.fish"), nil
}

// handlerMarks are names from the handlers. A startup file that holds one has
// the handler already, loaded by the line or pasted in.
var handlerMarks = []string{"ai --init", "AcceptLineWithAiPrompt", "_ai_quote_prompt", "_ai_accept_line"}

// runSetup handles ai --setup and ai --init. The startup line reads the handler
// through a pipe, so there it prints the handler; on a terminal it adds that
// line to the shell's startup file, once the user agrees.
func runSetup(name string) int {
	sh := selectShell(strings.ToLower(name))
	setup, ok := shellSetups[sh.name]
	if !ok {
		return noSetup(sh.name)
	}
	if !isTerminal(os.Stdout) {
		return printHandler(setup)
	}
	return installHandler(sh, setup)
}

func printHandler(setup shellSetup) int {
	data, err := initScripts.ReadFile(setup.script)
	if err != nil {
		return fail(err)
	}
	// Windows checkouts can turn the embedded files into CRLF, which bash rejects.
	fmt.Print(strings.ReplaceAll(string(data), "\r\n", "\n"))
	return 0
}

func installHandler(sh shell, setup shellSetup) int {
	path, err := setup.startupFile(sh)
	if err != nil {
		return fail(err)
	}
	if hasHandler(path) {
		fmt.Printf("%s already loads the ai key handler: %s\n", sh.name, path)
		return 0
	}
	fmt.Printf("In %s, the shell reads ', $, ( and ) in a prompt before ai starts.\n", sh.name)
	fmt.Printf("A key handler can pass the prompt to ai as typed. This line loads it:\n\n  %s\n\n", setup.line)
	fmt.Printf("Add it to %s? [y/N] ", path)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
		fmt.Println(cancelledNote)
		return exitCancelled
	}
	if err := appendLine(path, setup.line); err != nil {
		return fail(err)
	}
	fmt.Printf("%s Added. Open a new terminal to use it.\n", promptMark)
	return 0
}

// noSetup explains a shell that has no key handler.
func noSetup(name string) int {
	if name == "cmd" {
		fmt.Println("cmd passes ', $, ( and ) to ai as typed, so it needs no setup.")
		fmt.Println("Put quotes around a prompt that holds &, |, <, >, ^ or %.")
		return 0
	}
	return fail(fmt.Errorf("ai --setup supports pwsh, powershell, zsh, bash and fish, not %q; "+
		"quote a prompt that holds characters the shell reads, or run ai alone and type the prompt", name))
}

func hasHandler(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(data)
	return slices.ContainsFunc(handlerMarks, func(mark string) bool { return strings.Contains(text, mark) })
}

// appendLine adds the line at the end of the file, which it creates if needed.
func appendLine(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	text := "# Pass prompts to ai as typed (added by ai --setup).\n" + line + "\n"
	if len(data) > 0 {
		text = "\n" + text
		if data[len(data)-1] != '\n' {
			text = "\n" + text
		}
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
