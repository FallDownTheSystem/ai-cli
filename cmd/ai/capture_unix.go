//go:build !windows

package main

// captureConsole has no equivalent outside Windows: a Unix terminal emulator
// does not let its child processes read the screen.
func captureConsole(int) (string, error) {
	return "", errNotAvailable
}
