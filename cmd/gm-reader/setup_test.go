package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/install"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
)

// TestMain answers "version" like an installed gm-reader would, because the
// setup test installs this test binary and then asks the copy its version.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("gm-reader v0.0.1")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSetupInstallsKeepsPairingAndConnectsClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GM_READER_HOME", filepath.Join(home, "data"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("HOME", home)
	st, err := session.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(&session.File{Auth: []byte(`{}`), Settings: session.Settings{AllowReact: true}}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "Programs", "gm-reader")
	target := filepath.Join(dir, install.ExeName())
	ctx := context.Background()

	answer("\n") // keep the existing pairing
	if err := runSetup(ctx, []string{"-dir", dir}); err != nil {
		t.Fatal(err)
	}
	if !fileExists(target) {
		t.Fatal("gm-reader was not installed")
	}
	cfg, err := claudeDesktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := configuredExe(cfg); !ok || got != target {
		t.Fatalf("Claude config points to %q, want %q", got, target)
	}
	f, err := st.Load()
	if err != nil || !f.Settings.AllowReact {
		t.Fatalf("the pairing and its settings must be kept: %v %+v", err, f)
	}

	// Running setup again updates the installed copy in place; with no -dir
	// it finds the install through Claude's config.
	answer("\n")
	if err := runSetup(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := configuredExe(cfg); got != target {
		t.Fatalf("second run moved the install: %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "gm-reader.old*"))
	if len(left) != 0 {
		t.Fatalf("old copies left behind: %v", left)
	}
}

func TestInstalledVersion(t *testing.T) {
	if v := installedVersion(os.Args[0]); v != "v0.0.1" {
		t.Fatalf("installedVersion = %q", v)
	}
	if v := installedVersion(filepath.Join(t.TempDir(), "missing")); v != "" {
		t.Fatalf("missing program should give no version, got %q", v)
	}
}
