package main

import "testing"

func TestCallToolOneLine(t *testing.T) {
	var got []string
	c := call{onTool: func(s string) { got = append(got, s) }}
	c.sendTool("run cd cmd\n  ls -la\t")
	c.sendTool(" \n ")
	if len(got) != 1 || got[0] != "run cd cmd ls -la" {
		t.Errorf("tool lines = %q", got)
	}
}
