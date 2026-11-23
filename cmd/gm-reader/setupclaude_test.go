package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAddToClaudeDesktopKeepsOtherSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Claude", "claude_desktop_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	orig := `{"mcpServers":{"other":{"command":"x"}},"globalShortcut":"Ctrl+Space","n":1.50}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := addToClaudeDesktop(path, `C:\Tools\gm-reader\gm-reader.exe`); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok || cfg["globalShortcut"] != "Ctrl+Space" {
		t.Fatalf("existing settings were lost: %s", b)
	}
	gm := servers["gm-reader"].(map[string]any)
	if gm["command"] != `C:\Tools\gm-reader\gm-reader.exe` || gm["args"].([]any)[0] != "serve" {
		t.Fatalf("bad entry: %v", gm)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != orig {
		t.Fatal("backup missing or wrong")
	}
}

func TestAddToClaudeDesktopNewFileAndBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Claude", "claude_desktop_config.json")
	if err := addToClaudeDesktop(path, "gm-reader"); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "c.json")
	_ = os.WriteFile(bad, []byte("{not json"), 0o600)
	if err := addToClaudeDesktop(bad, "gm-reader"); err == nil {
		t.Fatal("invalid JSON must not be overwritten")
	}
	if b, _ := os.ReadFile(bad); string(b) != "{not json" {
		t.Fatal("invalid config was modified")
	}
}

func TestConfigPathsIncludeStoreInstall(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Store install layout is Windows-only")
	}
	local, roaming := t.TempDir(), t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", roaming)
	pkg := filepath.Join(local, "Packages", "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	paths, err := claudeDesktopConfigPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(pkg, "claude_desktop_config.json"), filepath.Join(roaming, "Claude", "claude_desktop_config.json")}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("got %v want %v", paths, want)
	}
}
