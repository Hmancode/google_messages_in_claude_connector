package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/clipboard"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/curlcookies"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/gm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/logx"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/paths"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/signin"
)

const firefoxDownload = "https://www.mozilla.org/firefox/new/"

// manualSteps is the fallback: copying the sign-in out of Firefox's
// developer tools by hand.
const manualSteps = `
Copy the Google sign-in by hand (about 2 minutes):

  1. In Firefox, press Ctrl+Shift+P to open a private window.%s
  2. Go to this address and sign in with the Google account your phone's
     Google Messages uses (only that one):
       ` + signin.SignInURL + `
     Afterwards you land on a page full of code-like text. That is the right page.
  3. Press F12, click the "Network" tab, then press F5 to reload.
  4. Right-click the row named "config" > Copy Value > Copy as cURL (POSIX).
%s
Don't click "Sign out" in that window; just close it when gm-reader is done.
`

// cookieSource says how to get the Google sign-in cookies.
type cookieSource struct {
	curlFile string // read a saved "Copy as cURL" from this file
	firefox  string // path to Firefox, if given
	manual   bool   // skip the automatic sign-in
}

// getCookies gets the Google sign-in cookies: automatically through a
// Firefox window gm-reader opens, or by hand if that's not possible.
func getCookies(ctx context.Context, src cookieSource) (map[string]string, error) {
	if src.curlFile != "" {
		b, err := os.ReadFile(src.curlFile)
		if err != nil {
			return nil, err
		}
		return parseCopied(string(b))
	}
	ff := src.firefox
	if ff == "" {
		ff, _ = signin.FindFirefox()
	}
	if src.manual {
		return manualCookies(ctx, ff)
	}
	for ff == "" {
		fmt.Println("  Firefox isn't installed. The sign-in needs Firefox (Chrome and Edge")
		fmt.Println("  usually don't work for this). Opening the Firefox download page...")
		openURL(firefoxDownload)
		switch strings.ToLower(ask("  Install Firefox, then press Enter here (or type m to do it by hand): ")) {
		case "m":
			return manualCookies(ctx, "")
		}
		ff, _ = signin.FindFirefox()
	}
	for {
		cookies, err := autoSignIn(ctx, ff)
		if err == nil {
			return cookies, nil
		}
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		fmt.Println("  The sign-in didn't finish: " + err.Error() + ".")
		if strings.ToLower(ask("  Press Enter to try again, or type m to copy the sign-in by hand: ")) == "m" {
			return manualCookies(ctx, ff)
		}
	}
}

func autoSignIn(ctx context.Context, firefox string) (map[string]string, error) {
	dir, err := paths.Dir()
	if err != nil {
		return nil, err
	}
	fmt.Println("  gm-reader will open a Firefox window just for this sign-in. Sign in with")
	fmt.Println("  the Google account your phone's Google Messages uses (only that one).")
	fmt.Println("  When a page of code-like text appears, you're done: gm-reader picks up")
	fmt.Println("  the sign-in and closes the window by itself.")
	ask("  Press Enter to open Firefox... ")
	fmt.Println("  Waiting for you to sign in (up to 10 minutes)...")
	res, err := signin.Capture(ctx, signin.Options{
		Firefox:    firefox,
		ProfileDir: filepath.Join(dir, signin.DefaultProfileName),
		Closed: func() {
			fmt.Println("  Got it. Please close the Firefox sign-in window now (don't sign out).")
		},
	})
	if err != nil {
		return nil, err
	}
	fmt.Printf("  Signed in (found %s).\n", strings.Join(curlcookies.Names(res.Cookies), ", "))
	if !res.ProfileDeleted {
		fmt.Println("  The temporary sign-in folder will be removed next time gm-reader runs.")
	}
	return res.Cookies, nil
}

func manualCookies(ctx context.Context, firefox string) (map[string]string, error) {
	opened, finish := "", "  5. Come back to this window and press Enter.\n"
	if firefox != "" {
		if err := exec.Command(firefox, "-private-window", signin.SignInURL).Start(); err == nil {
			opened = "\n     (gm-reader just opened one for you at the right address.)"
		}
	}
	if clipboard.Supported {
		finish = "  5. That's it: gm-reader notices the copy by itself.\n"
	}
	fmt.Printf(manualSteps, opened, finish)
	if clipboard.Supported {
		fmt.Println("Waiting for the copy (up to 10 minutes)...")
		return watchClipboard(ctx, 10*time.Minute)
	}
	fmt.Println("Paste the cURL command, then press Ctrl+D:")
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	return parseCopied(string(b))
}

// watchClipboard waits until a usable "Copy as cURL" lands on the clipboard.
// Nothing copied is logged; only the cookie names are shown.
func watchClipboard(ctx context.Context, timeout time.Duration) (map[string]string, error) {
	last, _ := clipboard.ReadText()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("nothing usable was copied in time")
		case <-tick.C:
		}
		text, err := clipboard.ReadText()
		if err != nil || text == last {
			continue
		}
		last = text
		if cookies, err := curlcookies.Parse(text); err == nil {
			fmt.Printf("Found the cookies gm-reader needs (%s).\n", strings.Join(curlcookies.Names(cookies), ", "))
			fmt.Println("Tip: copy something else now so the cookies don't stay on your clipboard.")
			return cookies, nil
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "curl") {
			fmt.Println("That copy isn't quite right: " + curlcookies.Describe(text) + ". Try again.")
		}
	}
}

func parseCopied(text string) (map[string]string, error) {
	cookies, err := curlcookies.Parse(text)
	if err != nil {
		fmt.Println("\nWhat was copied: " + curlcookies.Describe(text))
		return nil, err
	}
	fmt.Printf("Found the cookies gm-reader needs (%s).\n", strings.Join(curlcookies.Names(cookies), ", "))
	return cookies, nil
}

// openURL opens a web page in the default browser.
func openURL(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		fmt.Println("  Open this page yourself: " + u)
	}
}

// pairPhone links gm-reader to the phone. A re-pair keeps the existing
// settings; a first pairing asks whether Claude may send.
func pairPhone(ctx context.Context, st *session.Store, existing *session.File, cookies map[string]string, libgmLog zerolog.Logger) error {
	fmt.Println("\n  Connecting to Google...")
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	p, emoji, err := gm.StartPairing(sctx, cookies, libgmLog)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("  On your phone, Google Messages should now show a pairing request.")
	fmt.Println("  Tap this emoji there:")
	fmt.Println()
	fmt.Printf("          %s\n\n", emoji)
	fmt.Println("  If you see a box instead of an emoji, open this link to see it:")
	fmt.Println("    " + gm.EmojiImageURL(emoji))
	fmt.Println("\n  Waiting for you to tap it (up to 3 minutes)...")
	wctx, wcancel := context.WithTimeout(ctx, 3*time.Minute)
	defer wcancel()
	auth, err := p.Wait(wctx)
	if err != nil {
		return err
	}
	fmt.Println("\n  Linked to your phone.")

	now := time.Now()
	f := &session.File{Auth: auth, PairedAt: now, LastContact: now}
	if existing != nil {
		f.Settings, f.ReplyLog = existing.Settings, existing.ReplyLog
		fmt.Println("  Kept your current settings (see them with: gm-reader settings).")
	} else {
		fmt.Println()
		fmt.Println("  Claude can always read and search your messages. It can also send")
		fmt.Println("  messages, react, start new chats and send files, but only if you allow it.")
		fmt.Println("  If you allow it, every message, new chat and file pops up a Yes/No box on")
		fmt.Println("  this computer first, and nothing is sent unless you click Yes.")
		fmt.Println("  (Reactions don't show the box. You can change this later with `gm-reader settings`.)")
		allow := yes("\n  Let Claude send messages, react, start chats and send files?")
		f.Settings = session.Settings{
			AllowSend: allow, AllowReact: allow, AllowAttachments: allow, AllowNewChats: allow,
			ConfirmMode: session.ConfirmDialog,
		}
	}
	if err := st.Save(f); err != nil {
		return fmt.Errorf("linked, but could not save the pairing: %w", err)
	}
	return nil
}

// refreshSignIn replaces the Google cookies in an existing pairing.
func refreshSignIn(ctx context.Context, st *session.Store, existing *session.File, cookies map[string]string, libgmLog zerolog.Logger) error {
	fmt.Println("  Checking the new sign-in with Google...")
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	auth, err := gm.RefreshCookies(cctx, existing.Auth, cookies, libgmLog)
	if err != nil {
		return err
	}
	if err := st.Update(func(f *session.File) error { f.Auth = auth; f.LastContact = time.Now(); return nil }); err != nil {
		return err
	}
	fmt.Println("  Sign-in updated.")
	return nil
}

func libgmLogger(debug bool) (zerolog.Logger, func()) {
	_, libgmLog, closer, err := logx.Open(debug)
	if err != nil {
		return zerolog.Nop(), func() {}
	}
	return libgmLog, func() { closer.Close() }
}

func runPair(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	refresh := fs.Bool("refresh-cookies", false, "sign in to Google again without re-pairing the phone")
	curlFile := fs.String("curl-file", "", "read a copied cURL command from this file instead of signing in")
	manual := fs.Bool("manual", false, "copy the sign-in from Firefox by hand instead of automatically")
	firefox := fs.String("firefox", "", "path to firefox.exe, if gm-reader can't find it")
	debug := fs.Bool("debug", false, "write detailed logs (may include more technical detail; never message text)")
	_ = fs.Parse(args)

	libgmLog, closeLog := libgmLogger(*debug)
	defer closeLog()
	st, err := session.Open()
	if err != nil {
		return err
	}
	existing, loadErr := st.Load()
	src := cookieSource{curlFile: *curlFile, firefox: *firefox, manual: *manual}

	if *refresh {
		if loadErr != nil {
			return fmt.Errorf("no existing pairing to refresh (%v); run `gm-reader pair` instead", loadErr)
		}
		cookies, err := getCookies(ctx, src)
		if err != nil {
			return err
		}
		if err := refreshSignIn(ctx, st, existing, cookies, libgmLog); err != nil {
			return err
		}
		fmt.Println("\nDone. Restart Claude to use the new sign-in.")
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
	} else {
		existing = nil
	}
	fmt.Println()
	cookies, err := getCookies(ctx, src)
	if err != nil {
		return err
	}
	if err := pairPhone(ctx, st, existing, cookies, libgmLog); err != nil {
		return err
	}
	fmt.Println("\nSaved.")
	printClaudeSetup()
	return nil
}

func printClaudeSetup() {
	fmt.Println("\nNext: connect gm-reader to Claude")
	fmt.Println("---------------------------------")
	if yes("Add gm-reader to Claude Desktop's settings now?") {
		if err := runSetupClaude(nil); err != nil {
			fmt.Println("Could not update the Claude config:", err)
			fmt.Println("See SETUP.md to add it by hand.")
		}
		return
	}
	fmt.Println("You can do it later by running:  gm-reader setup-claude")
}
