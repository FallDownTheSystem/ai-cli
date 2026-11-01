package main

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
)

type shell struct {
	name string // one of shellNames, unless the user configured another
	path string
}

// shellNames are the shells ai recognises, in the order the settings picker offers them.
var shellNames = []string{"pwsh", "powershell", "cmd", "bash", "zsh", "fish", "nu", "sh", "dash", "ksh", "elvish", "xonsh"}

func isShell(name string) bool { return slices.Contains(shellNames, name) }

// shellName reduces a path or process name to the shell's name. A login shell
// is named with a leading dash, such as -zsh.
func shellName(path string) string {
	return strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(filepath.Base(path)), ".exe"), "-")
}

// selectShell is the configured shell, or the one the user is typing into.
func selectShell(configured string) shell {
	if configured != "" {
		return lookupShell(configured)
	}
	return currentShell()
}

// lookupShell finds a shell by name on PATH, or takes a path as it is.
func lookupShell(nameOrPath string) shell {
	path := nameOrPath
	if p, err := exec.LookPath(nameOrPath); err == nil {
		path = p
	}
	return shell{name: shellName(nameOrPath), path: path}
}

// currentShell walks up the process tree to the shell that started ai, so
// commands are written for the shell the user is actually typing into. On Unix
// $SHELL is only the login shell, which differs when the user starts another
// shell such as fish or pwsh from it.
func currentShell() shell {
	pid := os.Getppid()
	for range 8 {
		ppid, exe, ok := parentProcess(pid)
		if !ok {
			break
		}
		if name := shellName(exe); isShell(name) {
			// The shell's own executable runs the command, because a lookup by name
			// on PATH can find another version or another program.
			if path := processPath(pid); path != "" {
				return shell{name: name, path: path}
			}
			return lookupShell(name)
		}
		pid = ppid
	}
	return fallbackShell()
}

// shellChoices are the shells the settings picker offers on this system: the
// ones found on PATH.
func shellChoices() []string {
	return slices.DeleteFunc(slices.Clone(shellNames), func(name string) bool {
		_, err := exec.LookPath(name)
		return err != nil
	})
}

// command builds the process that runs line in this shell, attached to the terminal.
func (s shell) command(line string) *exec.Cmd {
	var cmd *exec.Cmd
	switch s.name {
	case "pwsh", "powershell":
		// EncodedCommand avoids every layer of Windows argument quoting.
		cmd = exec.Command(s.path, "-NoProfile", "-NoLogo", "-EncodedCommand", encodePowerShell(line))
	case "cmd":
		cmd = cmdCommand(s.path, line)
	default:
		cmd = exec.Command(s.path, "-c", line)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// encodePowerShell encodes a command for -EncodedCommand: base64 of UTF-16LE.
func encodePowerShell(line string) string {
	var buf []byte
	for _, u := range utf16.Encode([]rune(line)) {
		buf = binary.LittleEndian.AppendUint16(buf, u)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// unwrapShell drops the shell invocation Codex puts around each command, such as
// "C:\\...\\pwsh.exe" -NoProfile -Command 'Get-Content go.mod' or /bin/bash -lc 'ls'.
func unwrapShell(command string) string {
	words, ok := splitWords(command)
	if n := len(words); ok && n >= 3 && isShell(shellName(words[0])) {
		switch strings.ToLower(words[n-2]) {
		case "-c", "-lc", "-command":
			return words[n-1]
		}
	}
	return command
}

// splitWords splits a command into words the way a POSIX shell reads its quotes,
// which is how Codex quotes them. It reports false when a quote is left open.
func splitWords(s string) ([]string, bool) {
	var words []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
			continue
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, false
			}
			word.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\", s[i+1]) >= 0 {
					i++
				}
				word.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
		case c == '\\' && i+1 < len(s):
			i++
			word.WriteByte(s[i])
		default:
			word.WriteByte(c)
		}
		inWord = true
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, true
}
