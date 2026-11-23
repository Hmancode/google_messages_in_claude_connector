//go:build windows

package signin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

// FindFirefox returns the path to firefox.exe, or an error if Firefox is not
// installed. GM_READER_FIREFOX overrides the search.
func FindFirefox() (string, error) {
	if p := os.Getenv("GM_READER_FIREFOX"); p != "" {
		return p, nil
	}
	const appPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\firefox.exe`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, appPath, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		p, _, err := k.GetStringValue("")
		k.Close()
		if err == nil && isFile(p) {
			return p, nil
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" {
			if p := filepath.Join(base, "Mozilla Firefox", "firefox.exe"); isFile(p) {
				return p, nil
			}
		}
	}
	return "", errors.New("Firefox is not installed")
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// killTree closes the Firefox process gm-reader started and everything it
// started (the browser and its tabs).
func killTree(p *os.Process) {
	if p == nil {
		return
	}
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(p.Pid), "/T", "/F").Run()
}

// profileInUse reports whether a Firefox window still has the profile open.
// Firefox keeps parent.lock open without sharing while it runs.
func profileInUse(dir string) bool {
	f, err := os.OpenFile(filepath.Join(dir, "parent.lock"), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	f.Close()
	return false
}
