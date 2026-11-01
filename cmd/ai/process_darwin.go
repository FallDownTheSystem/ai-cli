package main

import (
	"bytes"

	"golang.org/x/sys/unix"
)

func parentProcess(pid int) (ppid int, name string, ok bool) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, "", false
	}
	return int(info.Eproc.Ppid), unix.ByteSliceToString(info.Proc.P_comm[:]), true
}

// processPath is the executable a process runs, or "" when it cannot be read.
// kern.procargs2 starts with the argument count and then the executable path.
func processPath(pid int) string {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) <= 4 {
		return ""
	}
	path, _, _ := bytes.Cut(buf[4:], []byte{0})
	return string(path)
}
