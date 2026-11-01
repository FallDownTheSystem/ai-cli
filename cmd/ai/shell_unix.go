//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// fallbackShell is the login shell, for when no shell shows in the process tree.
func fallbackShell() shell {
	if s := os.Getenv("SHELL"); s != "" {
		return lookupShell(s)
	}
	return lookupShell("sh")
}

func cmdCommand(path, line string) *exec.Cmd {
	return exec.Command(path, "/c", line)
}
