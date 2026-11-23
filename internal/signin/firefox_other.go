//go:build !windows

package signin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// FindFirefox returns the path to Firefox, or an error if it is not
// installed. GM_READER_FIREFOX overrides the search.
func FindFirefox() (string, error) {
	if p := os.Getenv("GM_READER_FIREFOX"); p != "" {
		return p, nil
	}
	for _, name := range []string{"firefox", "firefox-esr"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	if p := "/Applications/Firefox.app/Contents/MacOS/firefox"; fileExists(p) {
		return p, nil
	}
	return "", errors.New("Firefox is not installed")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func killTree(p *os.Process) {
	if p != nil {
		_ = p.Kill()
	}
}

// profileInUse reports whether Firefox still has the profile open. On Linux
// and macOS Firefox keeps a "lock" symlink in the profile while it runs.
func profileInUse(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, "lock"))
	return err == nil
}
