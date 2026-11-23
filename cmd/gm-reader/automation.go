package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/clipboard"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/hook"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
)

const automationUsage = `gm-reader automation: features for building an assistant on gm-reader

  gm-reader automation                          Show the current automation settings
  gm-reader automation groups contacts          Any group chat with at least one saved contact
  gm-reader automation groups 884,1028          Only these group chats (by conversation ID)
  gm-reader automation groups none              No group chats
  gm-reader automation replies on --signature "- Sam's assistant"
        [--max-chars 280] [--per-chat-hour 3] [--daily 15] [--window-minutes 120]
  gm-reader automation replies off
  gm-reader automation hook on --url https://...  (copy the token first; it's read from the clipboard)
        [--token-file PATH] [--batch-seconds 45]
  gm-reader automation hook off
  gm-reader automation hook test

One-on-one chats with saved contacts are always included. Turning anything on
asks for approval on this computer. Restart Claude after a change.
`

func printAutomation(a session.Automation) {
	fmt.Println("Automation")
	switch {
	case a.GroupsWithContacts && len(a.Groups) > 0:
		fmt.Println("  Chats:            one-on-one chats with saved contacts, group chats with at least one saved contact, plus groups " + strings.Join(a.Groups, ", "))
	case a.GroupsWithContacts:
		fmt.Println("  Chats:            one-on-one chats with saved contacts, and group chats with at least one saved contact")
	case len(a.Groups) == 0:
		fmt.Println("  Chats:            one-on-one chats with saved contacts (no group chats)")
	default:
		fmt.Println("  Chats:            one-on-one chats with saved contacts, plus groups " + strings.Join(a.Groups, ", "))
	}
	r := a.Replies.Limits()
	if a.Replies.Enabled {
		fmt.Printf("  Labeled replies:  ON, signed %q\n", r.Signature)
		fmt.Printf("                    up to %d characters, %d per chat per hour, %d per day, only answering messages under %d min old\n",
			r.MaxChars, r.PerChatHour, r.DailyMax, r.WindowMinutes)
	} else {
		fmt.Println("  Labeled replies:  off")
	}
	if a.Hook.Enabled {
		fmt.Printf("  New-text hook:    ON, sends to %s (token saved: %v)\n", hook.Host(a.Hook.URL), a.Hook.Token != "")
	} else {
		fmt.Println("  New-text hook:    off")
	}
}

func checkSignature(sig string) error {
	sig = strings.TrimSpace(sig)
	if sig == "" {
		return errors.New("--signature is required, so people can tell the reply didn't come from you personally")
	}
	if utf8.RuneCountInString(sig) > 60 {
		return errors.New("keep the signature under 60 characters")
	}
	for _, r := range sig {
		if unicode.IsControl(r) {
			return errors.New("the signature can't contain line breaks or control characters")
		}
	}
	return nil
}

func readToken(tokenFile string) (string, error) {
	var t string
	switch {
	case tokenFile != "":
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", err
		}
		t = string(b)
	case clipboard.Supported:
		ask("Copy the hook's token to your clipboard, then press Enter here... ")
		c, err := clipboard.ReadText()
		if err != nil {
			return "", err
		}
		t = c
	default:
		t = ask("Paste the hook's token and press Enter: ")
	}
	t = strings.TrimSpace(t)
	if t == "" || strings.ContainsAny(t, " \t\r\n") {
		return "", errors.New("that doesn't look like a token")
	}
	return t, nil
}

func runAutomation(ctx context.Context, args []string) error {
	st, err := session.Open()
	if err != nil {
		return err
	}
	f, err := st.Load()
	if err != nil {
		return err
	}
	a := f.Settings.Automation
	if len(args) == 0 {
		printAutomation(a)
		fmt.Println()
		fmt.Print(automationUsage)
		return nil
	}

	var loosen string // description of anything being turned on, needs approval
	switch args[0] {
	case "groups":
		if len(args) != 2 {
			return errors.New("usage: gm-reader automation groups contacts   (or: groups 884,1028, or: groups none)")
		}
		if args[1] == "contacts" {
			if !a.GroupsWithContacts {
				loosen += "allow automation in every group chat that includes at least one person saved in your contacts\n"
			}
			a.GroupsWithContacts = true
			break
		}
		a.GroupsWithContacts = false
		var ids []string
		if args[1] != "none" {
			for _, id := range strings.Split(args[1], ",") {
				id = strings.TrimSpace(id)
				if id == "" {
					continue
				}
				for _, r := range id {
					if r < '0' || r > '9' {
						return fmt.Errorf("%q is not a conversation ID (ask Claude to list your conversations to find IDs)", id)
					}
				}
				ids = append(ids, id)
				if !a.AllowsGroup(id) {
					loosen += "allow automation in group chat " + id + "\n"
				}
			}
		}
		a.Groups = ids

	case "replies":
		if len(args) < 2 {
			return errors.New("usage: gm-reader automation replies on --signature \"...\"   (or: replies off)")
		}
		switch args[1] {
		case "off":
			a.Replies.Enabled = false
		case "on":
			fs := flag.NewFlagSet("replies on", flag.ExitOnError)
			sig := fs.String("signature", a.Replies.Signature, "signature added to every labeled reply")
			maxChars := fs.Int("max-chars", a.Replies.Limits().MaxChars, "maximum characters per reply")
			perHour := fs.Int("per-chat-hour", a.Replies.Limits().PerChatHour, "maximum labeled replies per chat in any hour")
			daily := fs.Int("daily", a.Replies.Limits().DailyMax, "maximum labeled replies per day")
			window := fs.Int("window-minutes", a.Replies.Limits().WindowMinutes, "only answer messages newer than this")
			_ = fs.Parse(args[2:])
			if err := checkSignature(*sig); err != nil {
				return err
			}
			switch {
			case *maxChars < 20 || *maxChars > 600:
				return errors.New("--max-chars must be between 20 and 600")
			case *perHour < 1 || *perHour > 10:
				return errors.New("--per-chat-hour must be between 1 and 10")
			case *daily < 1 || *daily > 100:
				return errors.New("--daily must be between 1 and 100")
			case *window < 5 || *window > 24*60:
				return errors.New("--window-minutes must be between 5 and 1440")
			}
			a.Replies = session.ReplySettings{Enabled: true, Signature: strings.TrimSpace(*sig), MaxChars: *maxChars, PerChatHour: *perHour, DailyMax: *daily, WindowMinutes: *window}
			loosen += fmt.Sprintf("turn ON labeled replies: Claude may send short replies signed %q WITHOUT asking you first\n", a.Replies.Signature)
		default:
			return errors.New("use: replies on   or   replies off")
		}

	case "hook":
		if len(args) < 2 {
			return errors.New("usage: gm-reader automation hook on --url https://...   (or: hook off, hook test)")
		}
		switch args[1] {
		case "off":
			a.Hook.Enabled = false
		case "test":
			if !a.Hook.Enabled {
				return errors.New("the hook is off")
			}
			tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := hook.New(hook.Config{URL: a.Hook.URL, Token: a.Hook.Token}, nopLog()).Test(tctx); err != nil {
				return err
			}
			fmt.Println("Test sent to", hook.Host(a.Hook.URL)+". It was accepted.")
			return nil
		case "on":
			fs := flag.NewFlagSet("hook on", flag.ExitOnError)
			u := fs.String("url", a.Hook.URL, "HTTPS address to notify")
			tokenFile := fs.String("token-file", "", "read the token from this file instead of the clipboard")
			batch := fs.Int("batch-seconds", 45, "collect new texts this long before sending one request")
			_ = fs.Parse(args[2:])
			if err := hook.Validate(*u); err != nil {
				return err
			}
			if *batch < 5 || *batch > 600 {
				return errors.New("--batch-seconds must be between 5 and 600")
			}
			tok, err := readToken(*tokenFile)
			if err != nil {
				return err
			}
			fmt.Println("Token read. Tip: copy something else now so it doesn't stay on your clipboard.")
			a.Hook = session.HookSettings{Enabled: true, URL: *u, Token: tok, BatchSeconds: *batch}
			loosen += "turn ON the new-text hook: when texts arrive, gm-reader will notify " + hook.Host(*u) + " (conversation IDs only, no message text)\n"
		default:
			return errors.New("use: hook on, hook off, or hook test")
		}

	default:
		fmt.Print(automationUsage)
		return fmt.Errorf("unknown automation command %q", args[0])
	}

	if loosen != "" {
		fmt.Println("Waiting for you to approve the change on this computer...")
		if err := approveChange(ctx, strings.TrimSpace(loosen)); err != nil {
			return errors.New("not changed: " + err.Error())
		}
	}
	if err := st.Update(func(f *session.File) error { f.Settings.Automation = a; return nil }); err != nil {
		return err
	}
	printAutomation(a)
	fmt.Println("\nSaved. Restart Claude for the change to take effect.")
	return nil
}
