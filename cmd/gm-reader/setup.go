package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/claudeapp"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/install"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
)

// runSetup is the guided setup that runs when gm-reader is double-clicked:
// install, link the phone, connect Claude. Running it again updates an
// existing install and keeps the pairing.
func runSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	dir := fs.String("dir", "", "install gm-reader in this folder")
	firefox := fs.String("firefox", "", "path to firefox.exe, if gm-reader can't find it")
	manual := fs.Bool("manual", false, "copy the Google sign-in from Firefox by hand instead of automatically")
	_ = fs.Parse(args)

	title := fmt.Sprintf("gm-reader %s setup", version)
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", len(title)))
	fmt.Println("This lets Claude read your Google Messages texts. It takes about 3 minutes.")
	fmt.Println("You'll need Firefox and your phone with Google Messages.")

	fmt.Println("\nStep 1 of 3: Install")
	exe, installed, err := installStep(*dir)
	if err != nil {
		return err
	}

	fmt.Println("\nStep 2 of 3: Link your phone")
	linked, err := linkStep(ctx, cookieSource{firefox: *firefox, manual: *manual})
	if err != nil {
		return err
	}

	fmt.Println("\nStep 3 of 3: Connect to Claude")
	return claudeStep(exe, installed || linked)
}

// installStep puts gm-reader in its permanent folder: where Claude already
// runs it from, or the default folder for a new install. It reports whether
// the installed program changed.
func installStep(dir string) (string, bool, error) {
	cur := exePath()
	target := ""
	if dir != "" {
		target = filepath.Join(dir, install.ExeName())
	} else {
		if cfg, err := claudeDesktopConfigPath(); err == nil {
			if p, ok := configuredExe(cfg); ok && fileExists(p) {
				target = p
			}
		}
		if target == "" {
			d, err := install.DefaultDir()
			if err != nil {
				return "", false, err
			}
			target = filepath.Join(d, install.ExeName())
		}
	}
	if install.SameFile(cur, target) {
		install.CleanupOld(filepath.Dir(target))
		fmt.Println("  gm-reader is installed at " + target)
		return target, false, nil
	}
	if fileExists(target) {
		old := installedVersion(target)
		cmp, ok := install.CompareVersions(version, old)
		switch {
		case ok && cmp == 0:
			fmt.Printf("  gm-reader %s is already installed at %s\n", old, target)
			return target, false, nil
		case ok && cmp < 0:
			if !yes(fmt.Sprintf("  The installed copy (%s) is newer than this one (%s). Replace it anyway?", old, version)) {
				fmt.Println("  Keeping the installed copy.")
				return target, false, nil
			}
		}
		if old == "" {
			old = "an older version"
		}
		fmt.Printf("  Updating %s (%s to %s)...\n", target, old, version)
	} else {
		fmt.Println("  Installing to " + filepath.Dir(target) + " ...")
	}
	if err := install.Copy(cur, target); err != nil {
		return "", false, err
	}
	install.CleanupOld(filepath.Dir(target))
	fmt.Println("  Done.")
	return target, true, nil
}

// installedVersion asks an installed copy for its version ("" if unknown).
func installedVersion(exe string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) == 2 && f[0] == "gm-reader" {
		return f[1]
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// linkStep pairs the phone, or keeps an existing pairing. It reports whether
// anything changed.
func linkStep(ctx context.Context, src cookieSource) (bool, error) {
	libgmLog, closeLog := libgmLogger(false)
	defer closeLog()
	st, err := session.Open()
	if err != nil {
		return false, err
	}
	existing, loadErr := st.Load()
	if loadErr == nil {
		since := "earlier"
		if !existing.PairedAt.IsZero() {
			since = existing.PairedAt.Local().Format("Jan 2, 2006")
		}
		fmt.Printf("  This computer is already linked to your phone (since %s).\n", since)
		fmt.Println("    Press Enter to keep it.")
		fmt.Println("    Type s to sign in to Google again (if Claude says the sign-in expired).")
		fmt.Println("    Type p to link your phone again from scratch.")
		switch strings.ToLower(ask("  > ")) {
		case "s":
			cookies, err := getCookies(ctx, src)
			if err != nil {
				return false, err
			}
			return true, refreshSignIn(ctx, st, existing, cookies, libgmLog)
		case "p":
			cookies, err := getCookies(ctx, src)
			if err != nil {
				return false, err
			}
			return true, pairPhone(ctx, st, existing, cookies, libgmLog)
		default:
			fmt.Println("  Keeping the current link.")
			return false, nil
		}
	}
	if !errors.Is(loadErr, session.ErrNotPaired) {
		fmt.Println("  The saved link couldn't be read (" + loadErr.Error() + "), so it will be replaced.")
	}
	fmt.Println("  gm-reader links to Google Messages on your phone the same way")
	fmt.Println("  messages.google.com does. The link can read every conversation in the")
	fmt.Println("  account, so treat it like a password.")
	fmt.Println()
	cookies, err := getCookies(ctx, src)
	if err != nil {
		return false, err
	}
	return true, pairPhone(ctx, st, nil, cookies, libgmLog)
}

// claudeStep points Claude Desktop at the installed gm-reader and restarts
// Claude if it needs to load a change.
func claudeStep(exe string, changed bool) error {
	cfg, err := claudeDesktopConfigPath()
	if err != nil {
		return err
	}
	if cur, ok := configuredExe(cfg); ok && samePath(cur, exe) {
		fmt.Println("  Claude Desktop is already set up to use gm-reader.")
	} else if err := addToClaudeDesktop(cfg, exe); err != nil {
		fmt.Println("  Could not update Claude's settings: " + err.Error())
		fmt.Println("  See \"Connect Claude by hand\" in SETUP.md.")
	} else {
		fmt.Println("  Added gm-reader to Claude Desktop's settings.")
		changed = true
	}
	restartClaude(changed)
	fmt.Println("\n  For Claude Code, run this once:")
	fmt.Printf("    claude mcp add --transport stdio --scope user gm-reader -- \"%s\" serve\n", exe)
	fmt.Println("\nAll set. In Claude, try: \"list my recent text conversations\".")
	return nil
}

func samePath(a, b string) bool {
	return install.SameFile(a, b) || strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func restartClaude(changed bool) {
	if !changed {
		return
	}
	if !claudeapp.Supported {
		fmt.Println("  Fully quit Claude Desktop and open it again so it loads gm-reader.")
		return
	}
	procs, err := claudeapp.Running()
	if err != nil || len(procs) == 0 {
		fmt.Println("  Open Claude when you're ready.")
		return
	}
	fmt.Println("  Claude is open and needs to restart to load gm-reader.")
	if !yes("  Close Claude and open it again now? Anything Claude is doing right now will stop.") {
		fmt.Println("  OK. Before using gm-reader, quit Claude from its system tray icon (right-click > Quit) and open it again.")
		return
	}
	launch := claudeapp.LaunchPath(procs[0].Path)
	if err := claudeapp.Quit(procs); err != nil {
		fmt.Println("  " + err.Error() + ". Quit it from the system tray icon (right-click > Quit) and open it again.")
		return
	}
	if launch == "" || claudeapp.Start(launch) != nil {
		fmt.Println("  Claude is closed. Open it again from the Start menu.")
		return
	}
	fmt.Println("  Claude is restarting.")
}

// stdinIsTerminal reports whether a person can answer questions, as opposed
// to a program (like Claude) talking to gm-reader through a pipe.
func stdinIsTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
