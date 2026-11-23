// Package claudeapp finds a running Claude Desktop and restarts it, so it
// loads a new or updated gm-reader. Only the desktop app is touched: Claude
// Code's command-line program is also named claude.exe, so processes are
// matched by where they are installed, not just by name.
package claudeapp

import (
	"os"
	"path/filepath"
	"strings"
)

// Proc is a running Claude Desktop process.
type Proc struct {
	PID  int
	Path string
}

// IsDesktop reports whether a program path is Claude Desktop: the regular
// installer puts it under %LOCALAPPDATA%\AnthropicClaude, the Microsoft Store
// / MSIX package under C:\Program Files\WindowsApps\Claude_...
func IsDesktop(path string) bool {
	p := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	if !strings.HasSuffix(p, `\claude.exe`) {
		return false
	}
	if strings.Contains(p, `\anthropicclaude\`) {
		return true
	}
	return strings.Contains(p, `\windowsapps\claude_`) || strings.Contains(p, `\windowsapps\anthropic`)
}

// LaunchPath returns the program to start Claude Desktop again, or "" when it
// can't be started directly (Store/MSIX installs open from the Start menu).
// For the regular installer it prefers the launcher in the install folder,
// which always starts the newest version.
func LaunchPath(running string) string {
	if strings.Contains(strings.ToLower(strings.ReplaceAll(running, `\`, "/")), "/windowsapps/") {
		return ""
	}
	dir := filepath.Dir(running) // ...\AnthropicClaude\app-0.14.2
	if strings.HasPrefix(strings.ToLower(filepath.Base(dir)), "app-") {
		launcher := filepath.Join(filepath.Dir(dir), filepath.Base(running))
		if st, err := os.Stat(launcher); err == nil && !st.IsDir() {
			return launcher
		}
	}
	return running
}
