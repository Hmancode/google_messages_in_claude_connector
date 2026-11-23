package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// claudeDesktopConfigPaths returns every place Claude Desktop may read its MCP
// config from. On Windows, the Microsoft Store / MSIX install of Claude keeps a
// private copy under %LOCALAPPDATA%\Packages\Claude_*\LocalCache\Roaming\Claude,
// which it reads instead of %APPDATA%\Claude, so both are updated.
func claudeDesktopConfigPaths() ([]string, error) {
	if runtime.GOOS != "windows" {
		p, err := claudeDesktopConfigPath()
		if err != nil {
			return nil, err
		}
		return []string{p}, nil
	}
	var out []string
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs, _ := filepath.Glob(filepath.Join(local, "Packages", "Claude_*", "LocalCache", "Roaming", "Claude"))
		for _, d := range dirs {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				out = append(out, filepath.Join(d, "claude_desktop_config.json"))
			}
		}
	}
	p, err := claudeDesktopConfigPath()
	if err != nil && len(out) == 0 {
		return nil, err
	} else if err == nil {
		out = append(out, p)
	}
	return out, nil
}

// claudeDesktopConfigPath returns the standard Claude Desktop config location.
func claudeDesktopConfigPath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
	default:
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "Claude", "claude_desktop_config.json"), nil
	}
}

// addToClaudeDesktop adds or updates the gm-reader entry in Claude Desktop's
// config, keeping everything else in the file. It writes a .bak copy first.
func addToClaudeDesktop(path, exe string) error {
	cfg := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case len(bytes.TrimSpace(raw)) > 0:
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&cfg); err != nil {
			return fmt.Errorf("the existing Claude config is not valid JSON, so it was not changed (%v). Fix it or add gm-reader by hand", err)
		}
		if err := os.WriteFile(path+".bak", raw, 0o600); err != nil {
			return fmt.Errorf("could not back up the config: %w", err)
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		if _, exists := cfg["mcpServers"]; exists {
			return errors.New(`"mcpServers" in the Claude config is not an object; add gm-reader by hand`)
		}
		servers = map[string]any{}
	}
	servers["gm-reader"] = map[string]any{"command": exe, "args": []string{"serve"}}
	cfg["mcpServers"] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return `C:\Tools\gm-reader\gm-reader.exe`
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	abs, _ := filepath.Abs(exe)
	return abs
}

func runSetupClaude(args []string) error {
	fs := flag.NewFlagSet("setup-claude", flag.ExitOnError)
	_ = fs.Parse(args)
	exe := exePath()
	paths, err := claudeDesktopConfigPaths()
	if err != nil {
		return err
	}
	updated := 0
	for _, path := range paths {
		if err := addToClaudeDesktop(path, exe); err != nil {
			fmt.Println("Could not update", path+":", err)
			continue
		}
		fmt.Println("Added gm-reader to Claude Desktop's config:")
		fmt.Println("  " + path)
		updated++
	}
	if updated == 0 {
		return errors.New("no Claude Desktop config could be updated; add gm-reader by hand (SETUP.md, step 5)")
	}
	fmt.Println("Fully quit Claude Desktop (right-click its icon in the system tray > Quit) and open it again.")
	fmt.Println("\nFor Claude Code, run this once:")
	fmt.Printf("  claude mcp add --transport stdio --scope user gm-reader -- \"%s\" serve\n", exe)
	fmt.Println("\nThen ask Claude: \"list my group chats\".")
	return nil
}
