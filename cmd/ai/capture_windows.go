package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procReadConsoleOutputCharacterW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleOutputCharacterW")

// captureConsole reads the console screen buffer up to the cursor. Under
// Windows Terminal the buffer is the visible window, so this sees what the
// user sees, but not lines that have scrolled away.
func captureConsole(n int) (string, error) {
	h, err := windows.CreateFile(
		windows.StringToUTF16Ptr("CONOUT$"),
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0,
	)
	if err != nil {
		return "", errNotAvailable
	}
	defer windows.CloseHandle(h)

	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(h, &info); err != nil {
		return "", fmt.Errorf("console: %w", err)
	}
	width := int(info.Size.X)
	bottom := int(info.CursorPosition.Y)

	row := make([]uint16, width)
	var b strings.Builder
	for y := max(0, bottom-n); y <= bottom; y++ {
		var read uint32
		coord := uintptr(uint32(uint16(y)) << 16)
		r, _, callErr := procReadConsoleOutputCharacterW.Call(
			uintptr(h), uintptr(unsafe.Pointer(&row[0])), uintptr(width), coord, uintptr(unsafe.Pointer(&read)),
		)
		if r == 0 {
			return "", fmt.Errorf("console: %w", callErr)
		}
		b.WriteString(strings.TrimRight(windows.UTF16ToString(row[:read]), " "))
		b.WriteByte('\n')
	}
	return b.String(), nil
}
