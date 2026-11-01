package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fish", "config.fish")
	if hasHandler(path) {
		t.Fatal("a missing file has no handler")
	}
	if err := appendLine(path, "ai --init fish | source"); err != nil {
		t.Fatal(err)
	}
	if !hasHandler(path) {
		t.Error("the added line is not found")
	}

	// The line goes after a blank line, also when the file ends without a newline.
	if err := os.WriteFile(path, []byte("set -x A 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendLine(path, "ai --init fish | source"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "set -x A 1\n\n# Pass prompts to ai as typed (added by ai --setup).\nai --init fish | source\n"
	if string(data) != want {
		t.Errorf("file = %q, want %q", data, want)
	}
}

func TestHandlerScripts(t *testing.T) {
	for name, setup := range shellSetups {
		data, err := initScripts.ReadFile(setup.script)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// A startup file with a pasted handler counts as set up.
		found := false
		for _, mark := range handlerMarks {
			found = found || strings.Contains(string(data), mark)
		}
		if !found {
			t.Errorf("%s: the handler holds none of the marks that hasHandler looks for", name)
		}
	}
}
