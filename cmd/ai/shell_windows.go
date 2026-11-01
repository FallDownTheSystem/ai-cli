package main

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fallbackShell is PowerShell, for when no shell shows in the process tree.
func fallbackShell() shell {
	if _, err := exec.LookPath("pwsh"); err == nil {
		return lookupShell("pwsh")
	}
	return lookupShell("powershell")
}

type processInfo struct {
	ppid int
	exe  string
}

// processTable is one snapshot of every process, taken once, since a walk up
// the tree asks about several of them.
var processTable = sync.OnceValue(func() map[int]processInfo {
	table := map[int]processInfo{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return table
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		table[int(entry.ProcessID)] = processInfo{
			ppid: int(entry.ParentProcessID),
			exe:  windows.UTF16ToString(entry.ExeFile[:]),
		}
	}
	return table
})

func parentProcess(pid int) (ppid int, name string, ok bool) {
	p, ok := processTable()[pid]
	return p.ppid, p.exe, ok
}

// processPath is the executable a process runs, or "" when it cannot be read.
func processPath(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// cmdCommand passes the line verbatim, because cmd.exe does not parse its
// command line with the escaping rules that exec.Command applies.
func cmdCommand(path, line string) *exec.Cmd {
	cmd := exec.Command(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + path + `" /d /s /c "` + line + `"`}
	return cmd
}
