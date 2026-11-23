package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/clipboard"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/curlcookies"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/gm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/logx"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
)

const cookieSteps = `
Get your Google sign-in cookies (about 2 minutes):

  1. Open Firefox and press Ctrl+Shift+P to open a private window.
     (Chrome often does not work for this; Firefox does.)
  2. In that window, go to:  https://messages.google.com/web/config
     Sign in with the Google account that your phone's Google Messages uses.
     Sign in to only that one account.
  3. Press F12 to open the developer tools, then click the "Network" tab.
  4. Press F5 to reload the page. In the list, click the row named "config".
  5. Right-click that row, choose "Copy Value", then "Copy as cURL"
     (either the Windows or the POSIX version is fine).
  6. Come back to this window.

Do NOT click "Sign out" in that window afterwards, because that cancels the
cookies. Just close the private window when gm-reader says it is done.
`

func readCookies(curlFile string) (map[string]string, error) {
	var text string
	if curlFile != "" {
		b, err := os.ReadFile(curlFile)
		if err != nil {
			return nil, err
		}
		text = string(b)
	} else if clipboard.Supported {
		ask("When the cURL command is copied, press Enter here... ")
		t, err := clipboard.ReadText()
		if err != nil {
			return nil, err
		}
		text = t
	} else {
		fmt.Println("Paste the cURL command, then press Ctrl+D:")
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		text = string(b)
	}
	cookies, err := curlcookies.Parse(text)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Found the cookies gm-reader needs (%s).\n", strings.Join(curlcookies.Names(cookies), ", "))
	fmt.Println("Tip: copy something else now so the cookies don't stay on your clipboard.")
	return cookies, nil
}

func runPair(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	refresh := fs.Bool("refresh-cookies", false, "replace the Google cookies in an existing pairing without re-pairing the phone")
	curlFile := fs.String("curl-file", "", "read the copied cURL command from this file instead of the clipboard")
	debug := fs.Bool("debug", false, "write detailed logs (may include more technical detail; never message text)")
	_ = fs.Parse(args)

	_, libgmLog, closer, err := logx.Open(*debug)
	if err != nil {
		libgmLog = zerolog.Nop()
	} else {
		defer closer.Close()
	}
	st, err := session.Open()
	if err != nil {
		return err
	}
	existing, loadErr := st.Load()

	if *refresh {
		if loadErr != nil {
			return fmt.Errorf("no existing pairing to refresh (%v); run `gm-reader pair` instead", loadErr)
		}
		fmt.Print(cookieSteps)
		cookies, err := readCookies(*curlFile)
		if err != nil {
			return err
		}
		fmt.Println("Checking the new cookies with Google...")
		cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		auth, err := gm.RefreshCookies(cctx, existing.Auth, cookies, libgmLog)
		if err != nil {
			return err
		}
		if err := st.Update(func(f *session.File) error { f.Auth = auth; f.LastContact = time.Now(); return nil }); err != nil {
			return err
		}
		fmt.Println("\nDone. The cookies are updated. Close the private window (don't sign out), then restart Claude.")
		return nil
	}

	fmt.Println("gm-reader pairing")
	fmt.Println("=================")
	fmt.Println("This links gm-reader to Google Messages on your phone, like messages.google.com does.")
	fmt.Println("The pairing can read every conversation in the account: treat it like a password.")
	if loadErr == nil {
		if !yes("\nThis computer is already paired. Pair again and replace the old pairing?") {
			return errors.New("cancelled")
		}
	}
	fmt.Print(cookieSteps)
	cookies, err := readCookies(*curlFile)
	if err != nil {
		return err
	}

	fmt.Println("\nConnecting to Google...")
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	p, emoji, err := gm.StartPairing(sctx, cookies, libgmLog)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("On your phone, Google Messages should now show a pairing request.")
	fmt.Println("Tap this emoji there:")
	fmt.Println()
	fmt.Printf("        %s\n\n", emoji)
	fmt.Println("If you see a box instead of an emoji, open this link to see it:")
	fmt.Println("  " + gm.EmojiImageURL(emoji))
	fmt.Println("\nWaiting for you to tap it (up to 3 minutes)...")
	wctx, wcancel := context.WithTimeout(ctx, 3*time.Minute)
	defer wcancel()
	auth, err := p.Wait(wctx)
	if err != nil {
		return err
	}
	fmt.Println("\nPaired with your phone.")

	fmt.Println()
	fmt.Println("Claude can always read and search your messages. It can also send")
	fmt.Println("messages, react, start new chats and send files, but only if you allow it.")
	fmt.Println("If you allow it, every message, new chat and file pops up a Yes/No box on")
	fmt.Println("this computer first, and nothing is sent unless you click Yes.")
	fmt.Println("(Reactions don't show the box. You can change this later with `gm-reader settings`.)")
	allow := yes("\nLet Claude send messages, react, start chats and send files?")

	f := &session.File{
		Auth:        auth,
		PairedAt:    time.Now(),
		LastContact: time.Now(),
		Settings: session.Settings{
			AllowSend: allow, AllowReact: allow, AllowAttachments: allow, AllowNewChats: allow,
			ConfirmMode: session.ConfirmDialog,
		},
	}
	if loadErr == nil {
		// Keep the previous confirmation mode if one was chosen.
		f.Settings.ConfirmMode = existing.Settings.Mode()
	}
	if err := st.Save(f); err != nil {
		return fmt.Errorf("paired, but could not save the session: %w", err)
	}
	fmt.Println("\nSaved. You can now close the Firefox private window (don't sign out).")
	printClaudeSetup()
	return nil
}

func printClaudeSetup() {
	fmt.Println("\nNext: connect gm-reader to Claude")
	fmt.Println("---------------------------------")
	if yes("Add gm-reader to Claude Desktop's settings now?") {
		if err := runSetupClaude(nil); err != nil {
			fmt.Println("Could not update the Claude config:", err)
			fmt.Println("See SETUP.md, step 6, to add it by hand.")
		}
		return
	}
	fmt.Println("You can do it later by running:  gm-reader setup-claude")
}
