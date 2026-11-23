//go:build windows

// Package clipboard reads text from the Windows clipboard, so the pairing
// step never needs a huge paste into PowerShell.
package clipboard

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
)

const cfUnicodeText = 13

// ReadText returns the text currently on the clipboard.
func ReadText() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return "", errors.New("could not open the clipboard: " + err.Error())
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", errors.New("the clipboard has no text in it")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", errors.New("could not read the clipboard")
	}
	defer procGlobalUnlock.Call(h)
	// Reinterpret the locked handle address as a pointer without a uintptr->Pointer cast.
	return windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&p))), nil
}

// Supported reports whether ReadText works on this system.
const Supported = true
