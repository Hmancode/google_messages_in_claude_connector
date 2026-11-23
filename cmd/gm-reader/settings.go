package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/confirm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/paths"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/secure"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/store"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/syncer"
)

var actionNames = []string{"send", "react", "attachments", "new-chats"}

func applyAction(s *session.Settings, name string, on bool) error {
	switch name {
	case "send":
		s.AllowSend = on
	case "react":
		s.AllowReact = on
	case "attachments":
		s.AllowAttachments = on
	case "new-chats":
		s.AllowNewChats = on
	case "all":
		s.AllowSend, s.AllowReact, s.AllowAttachments, s.AllowNewChats = on, on, on, on
	default:
		return fmt.Errorf("unknown action %q (use %s, or all)", name, strings.Join(actionNames, ", "))
	}
	return nil
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "off"
}

func printSettings(s session.Settings) {
	fmt.Println("What Claude may do:")
	fmt.Println("  read and search messages   always ON")
	fmt.Printf("  send messages (send)       %s\n", onOff(s.AllowSend))
	fmt.Printf("  react (react)              %s\n", onOff(s.AllowReact))
	fmt.Printf("  send files (attachments)   %s\n", onOff(s.AllowAttachments))
	fmt.Printf("  start chats (new-chats)    %s\n", onOff(s.AllowNewChats))
	if s.Mode() == session.ConfirmDialog {
		fmt.Println("Confirmation: a Yes/No box on this computer before each message, file and new chat")
	} else {
		fmt.Println("Confirmation: only the Claude app's own approval prompt (no box on this computer)")
	}
}

// approveChange asks the person at this computer to approve loosening a
// setting, so a program cannot quietly turn write actions on.
func approveChange(ctx context.Context, what string) error {
	if runtime.GOOS == "windows" {
		return confirm.New(session.ConfirmDialog).Confirm(ctx, confirm.Request{
			Action:  "change gm-reader settings",
			Details: what,
		})
	}
	if strings.ToLower(ask(what+"\nType yes to confirm: ")) != "yes" {
		return confirm.ErrDeclined
	}
	return nil
}

func runSettings(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settings", flag.ExitOnError)
	allow := fs.String("allow", "", "turn actions on: comma-separated from send, react, attachments, new-chats, or all")
	deny := fs.String("deny", "", "turn actions off: comma-separated from send, react, attachments, new-chats, or all")
	mode := fs.String("confirm-mode", "", "dialog (Yes/No box on this computer, default) or claude-app (only Claude's approval prompt)")
	_ = fs.Parse(args)

	st, err := session.Open()
	if err != nil {
		return err
	}
	f, err := st.Load()
	if err != nil {
		return err
	}
	if *allow == "" && *deny == "" && *mode == "" {
		printSettings(f.Settings)
		fmt.Println("\nChange with, for example:  gm-reader settings --allow send,react   or   --deny all")
		return nil
	}
	next := f.Settings
	var loosened []string
	for _, n := range strings.Split(*allow, ",") {
		if n = strings.TrimSpace(n); n != "" {
			if err := applyAction(&next, n, true); err != nil {
				return err
			}
			loosened = append(loosened, "turn ON: "+n)
		}
	}
	for _, n := range strings.Split(*deny, ",") {
		if n = strings.TrimSpace(n); n != "" {
			if err := applyAction(&next, n, false); err != nil {
				return err
			}
		}
	}
	switch *mode {
	case "":
	case session.ConfirmDialog:
		next.ConfirmMode = session.ConfirmDialog
	case session.ConfirmClaudeApp:
		next.ConfirmMode = session.ConfirmClaudeApp
		if f.Settings.Mode() != session.ConfirmClaudeApp {
			loosened = append(loosened, "stop showing the Yes/No box on this computer (rely on the Claude app's prompt only)")
		}
	default:
		return fmt.Errorf("unknown confirm mode %q (use dialog or claude-app)", *mode)
	}
	if len(loosened) > 0 {
		fmt.Println("Waiting for you to approve the change on this computer...")
		if err := approveChange(ctx, strings.Join(loosened, "\n")); err != nil {
			return errors.New("settings not changed: " + err.Error())
		}
	}
	if err := st.Update(func(f *session.File) error { f.Settings = next; return nil }); err != nil {
		return err
	}
	printSettings(next)
	fmt.Println("\nSaved. Restart Claude for the change to take effect.")
	return nil
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	_ = fs.Parse(args)
	dir, err := paths.Dir()
	if err != nil {
		return err
	}
	fmt.Println("Data folder:", dir)
	st, err := session.Open()
	if err != nil {
		return err
	}
	f, err := st.Load()
	if err != nil {
		fmt.Println("Pairing: none (" + err.Error() + ")")
		return nil
	}
	fmt.Println("Pairing: saved, paired", f.PairedAt.Local().Format("2006-01-02 15:04"))
	if secure.Encrypted {
		fmt.Println("Session file: encrypted with Windows DPAPI (only your Windows user can read it)")
	} else {
		fmt.Println("Session file: NOT encrypted on this operating system")
	}
	ref := f.LastContact
	if ref.IsZero() {
		ref = f.PairedAt
	}
	days := int((syncer.PairingLifetime - time.Since(ref)).Hours() / 24)
	fmt.Printf("Last contact with phone: %s (about %d days left before Google may drop the pairing)\n",
		ref.Local().Format("2006-01-02 15:04"), max(days, 0))
	if dbPath, err := paths.DatabaseFile(); err == nil {
		if _, err := os.Stat(dbPath); err == nil {
			if db, err := store.Open(dbPath); err == nil {
				c, m, _ := db.Totals()
				db.Close()
				fmt.Printf("Cache: %d conversations, %d messages (not encrypted by gm-reader; protected only by BitLocker if on)\n", c, m)
			}
		} else {
			fmt.Println("Cache: none yet")
		}
	}
	fmt.Println()
	printSettings(f.Settings)
	return nil
}

func runForget(args []string) error {
	fs := flag.NewFlagSet("forget", flag.ExitOnError)
	_ = fs.Parse(args)
	dir, err := paths.Dir()
	if err != nil {
		return err
	}
	fmt.Println("This deletes the pairing, the message cache and the logs from:")
	fmt.Println("  " + dir)
	if strings.ToLower(ask("Type yes to delete them: ")) != "yes" {
		return errors.New("cancelled")
	}
	// Only gm-reader's own files are removed, never anything else in the folder.
	for _, name := range []string{"session.bin", "session.bin.tmp", "cache.db", "cache.db-wal", "cache.db-shm", "gm-reader.log", "gm-reader.log.1"} {
		err := os.Remove(filepath.Join(dir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Println("Could not delete", name+":", err, "(quit Claude and try again)")
		}
	}
	_ = os.Remove(dir) // only succeeds if the folder is now empty
	fmt.Println("Deleted.")
	fmt.Println("\nTo fully revoke access, also remove the pairing on your phone:")
	fmt.Println("  Google Messages > your profile picture > Device pairing > remove the device")
	return nil
}
