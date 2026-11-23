package claudeapp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDesktop(t *testing.T) {
	cases := map[string]bool{
		`C:\Users\sam\AppData\Local\AnthropicClaude\app-0.14.2\claude.exe`:                   true,
		`C:\Users\sam\AppData\Local\AnthropicClaude\claude.exe`:                              true,
		`C:\Program Files\WindowsApps\Claude_1.0.734.0_x64__pzs8sxrjxfjjc\app\claude.exe`:    true,
		`C:\Users\sam\.local\bin\claude.exe`:                                                 false, // Claude Code
		`C:\Users\sam\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\claude.exe`: false,
		`C:\Users\sam\AppData\Local\AnthropicClaude\app-0.14.2\gm-reader.exe`:                false,
	}
	for p, want := range cases {
		if got := IsDesktop(p); got != want {
			t.Errorf("IsDesktop(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestLaunchPath(t *testing.T) {
	if got := LaunchPath(`C:\Program Files\WindowsApps\Claude_1.0_x64__abc\app\claude.exe`); got != "" {
		t.Errorf("Store installs open from the Start menu, got %q", got)
	}
	// Regular install: prefer the launcher next to the app-x.y.z folders.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "claude.exe"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	running := filepath.Join(root, "app-0.14.2", "claude.exe")
	if got := LaunchPath(running); got != filepath.Join(root, "claude.exe") {
		t.Errorf("LaunchPath = %q, want the launcher in %q", got, root)
	}
	if got := LaunchPath(`C:\nowhere\AnthropicClaude\app-1\claude.exe`); got != `C:\nowhere\AnthropicClaude\app-1\claude.exe` {
		t.Errorf("without a launcher, use the running path; got %q", got)
	}
}
