//go:build windows

package confirm

import (
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxTimeoutW = user32.NewProc("MessageBoxTimeoutW")
)

const (
	mbYesNo         = 0x00000004
	mbIconWarning   = 0x00000030
	mbDefButton2    = 0x00000100 // "No" is the default button
	mbSetForeground = 0x00010000
	mbTopmost       = 0x00040000
	idYes           = 6
)

// showYesNo shows a topmost Yes/No box and returns true only if Yes was clicked.
func showYesNo(title, body string, timeout time.Duration) (bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return false, err
	}
	b, err := windows.UTF16PtrFromString(body)
	if err != nil {
		return false, err
	}
	// MessageBoxTimeoutW is exported by user32 (since Windows XP) but not
	// documented. It behaves like MessageBoxW and returns 32000 on timeout.
	ret, _, callErr := procMessageBoxTimeoutW.Call(
		0,
		uintptr(unsafe.Pointer(b)),
		uintptr(unsafe.Pointer(t)),
		uintptr(mbYesNo|mbIconWarning|mbDefButton2|mbSetForeground|mbTopmost),
		0,
		uintptr(timeout.Milliseconds()),
	)
	if ret == 0 {
		return false, callErr
	}
	return ret == idYes, nil
}
