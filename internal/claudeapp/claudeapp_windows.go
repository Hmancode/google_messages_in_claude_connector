//go:build windows

package claudeapp

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supported reports whether this system can restart Claude Desktop.
const Supported = true

// Running lists Claude Desktop processes.
func Running() ([]Proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []Proc
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if !strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), "claude.exe") {
			continue
		}
		path := imagePath(e.ProcessID)
		if path != "" && IsDesktop(path) {
			out = append(out, Proc{PID: int(e.ProcessID), Path: path})
		}
	}
	return out, nil
}

func imagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// Quit closes Claude Desktop completely and waits for it to exit.
func Quit(procs []Proc) error {
	for _, p := range procs {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(p.PID), "/T", "/F").Run()
	}
	for i := 0; i < 40; i++ {
		left, err := Running()
		if err == nil && len(left) == 0 {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("Claude did not close")
}

// Start opens Claude Desktop, detached from this window.
func Start(path string) error {
	cmd := exec.Command(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
