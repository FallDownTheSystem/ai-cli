//go:build !windows && !linux && !darwin

package main

// parentProcess has no portable implementation on the other Unix systems, so
// currentShell falls back to $SHELL there.
func parentProcess(int) (int, string, bool) {
	return 0, "", false
}

func processPath(int) string { return "" }
