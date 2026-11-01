package main

import (
	"os"
	"strconv"
	"strings"
)

// parentProcess reads a process's command name and parent from /proc. The name
// comes from stat rather than comm because it is followed by the parent id.
func parentProcess(pid int) (ppid int, name string, ok bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, "", false
	}
	// The format is "pid (name) state ppid ..."; the name can contain spaces
	// and parentheses, so it ends at the last ")".
	s := string(data)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return 0, "", false
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 2 {
		return 0, "", false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", false
	}
	return ppid, s[open+1 : end], true
}

// processPath is the executable a process runs, or "" when it cannot be read.
func processPath(pid int) string {
	path, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	return path
}
