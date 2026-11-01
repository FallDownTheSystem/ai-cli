package main

import "testing"

func TestTidy(t *testing.T) {
	in := "\n\x1b[31mred\x1b[0m   \r\nline2\n\n\n"
	if got := tidy(in, 10); got != "red\nline2" {
		t.Errorf("tidy = %q", got)
	}
	if got := tidy("a\nb\nc", 2); got != "b\nc" {
		t.Errorf("tidy limit = %q", got)
	}
}
