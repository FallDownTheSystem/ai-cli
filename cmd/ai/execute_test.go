package main

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestVisible(t *testing.T) {
	in := "echo ok\x1b[2K\rrm -rf ~ ‮evil\n\tnext"
	got := ansi.Strip(visible(in))
	want := "echo ok\\x1b[2K\\rrm -rf ~ \\u202eevil\n\tnext"
	if got != want {
		t.Errorf("visible = %q, want %q", got, want)
	}
	if !hasHiddenRunes(in) || hasHiddenRunes("ls -la\n\techo") {
		t.Error("hasHiddenRunes is wrong")
	}
}
