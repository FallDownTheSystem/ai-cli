package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// captureTerminal reads the last n lines visible in the user's terminal. The
// terminal itself is the conversation history, so nothing is ever stored.
// Multiplexers come first because they also expose scrollback.
func captureTerminal(n int) (string, error) {
	sources := []func(int) (string, error){
		captureKero,
		captureTmux,
		captureZellij,
		captureScreen,
		captureWezTerm,
		captureKitty,
		captureConsole,
	}
	var errs []error
	for _, capture := range sources {
		text, err := capture(n)
		if errors.Is(err, errNotAvailable) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if text = tidy(text, n); text != "" {
			return text, nil
		}
	}
	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	return "", errors.New("this terminal cannot be read (run inside tmux, Zellij, screen, WezTerm, kitty, Kero, or a Windows console)")
}

var errNotAvailable = errors.New("not available")

func captureKero(n int) (string, error) {
	pane := os.Getenv("KERO_PANE")
	if pane == "" {
		return "", errNotAvailable
	}
	return runCapture("kero", "read", pane, "-a", "-n", strconv.Itoa(n))
}

func captureTmux(n int) (string, error) {
	if os.Getenv("TMUX") == "" {
		return "", errNotAvailable
	}
	args := []string{"capture-pane", "-p", "-J", "-S", "-" + strconv.Itoa(n)}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "-t", pane)
	}
	return runCapture("tmux", args...)
}

func captureZellij(int) (string, error) {
	if os.Getenv("ZELLIJ") == "" {
		return "", errNotAvailable
	}
	// Zellij dumps the focused pane, which is the one running ai. Since 0.44 it
	// prints to stdout; older versions only write to a file.
	text, stdoutErr := runCapture("zellij", "action", "dump-screen", "--full")
	if stdoutErr == nil {
		return text, nil
	}
	text, fileErr := captureToFile(func(path string) error {
		_, err := runCapture("zellij", "action", "dump-screen", path, "--full")
		return err
	})
	if fileErr != nil {
		// A current Zellij fails on the file only for the reason it failed on
		// stdout, so the first error is the one that explains.
		return "", stdoutErr
	}
	return text, nil
}

func captureScreen(int) (string, error) {
	sty := os.Getenv("STY")
	if sty == "" {
		return "", errNotAvailable
	}
	return captureToFile(func(path string) error {
		args := []string{"-S", sty}
		if w := os.Getenv("WINDOW"); w != "" {
			args = append(args, "-p", w)
		}
		if _, err := runCapture("screen", append(args, "-X", "hardcopy", "-h", path)...); err != nil {
			return err
		}
		// -X returns before the session writes the file. A query waits for its
		// answer, which comes after the hardcopy. Screen before 4.2 has no -Q.
		runCapture("screen", "-S", sty, "-Q", "echo", "sync")
		return nil
	})
}

// captureToFile runs a capture that writes to a file instead of stdout. The file
// goes in a new directory, so a write that comes too late fails instead of
// leaving a file behind.
func captureToFile(write func(path string) error) (string, error) {
	dir, err := os.MkdirTemp("", "ai-capture-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "capture.txt")
	if err := write(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return string(data), nil
}

// captureKitty needs allow_remote_control in kitty.conf; without it kitty
// answers "Remote control is disabled", which the error passes on.
func captureKitty(int) (string, error) {
	id := os.Getenv("KITTY_WINDOW_ID")
	if id == "" {
		return "", errNotAvailable
	}
	return runCapture("kitty", "@", "get-text", "--match", "id:"+id, "--extent", "all")
}

func captureWezTerm(n int) (string, error) {
	pane := os.Getenv("WEZTERM_PANE")
	if pane == "" {
		return "", errNotAvailable
	}
	return runCapture("wezterm", "cli", "get-text", "--pane-id", pane, "--start-line", "-"+strconv.Itoa(n))
}

func runCapture(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		// The first line of stderr says why, where the exit status alone does not.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if line, _, _ := strings.Cut(strings.TrimSpace(string(exitErr.Stderr)), "\n"); line != "" {
				return "", errors.New(name + ": " + strings.TrimSpace(line))
			}
		}
		return "", errors.New(name + ": " + err.Error())
	}
	return string(out), nil
}

// tidy strips escape codes and blank padding and keeps the last n lines.
func tidy(text string, n int) string {
	text = ansi.Strip(strings.ReplaceAll(text, "\r\n", "\n"))
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(last(trimBlankLines(lines), n), "\n")
}
