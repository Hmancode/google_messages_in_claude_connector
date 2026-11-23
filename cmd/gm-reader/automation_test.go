package main

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
)

// On Windows, approving a change shows a dialog; these tests drive the typed
// "yes" path used on other systems.
func seedSession(t *testing.T) *session.Store {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("approval uses a dialog on Windows")
	}
	t.Setenv("GM_READER_HOME", t.TempDir())
	st, err := session.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(&session.File{Auth: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	return st
}

func answer(s string) { stdin = bufio.NewReader(strings.NewReader(s)) }

func TestAutomationRepliesApproveAndDecline(t *testing.T) {
	st := seedSession(t)
	ctx := context.Background()

	answer("no\n")
	if err := runAutomation(ctx, []string{"replies", "on", "--signature", "- Sam's assistant"}); err == nil {
		t.Fatal("declining must not change anything")
	}
	f, _ := st.Load()
	if f.Settings.Automation.Replies.Enabled {
		t.Fatal("replies turned on despite declining")
	}

	if err := runAutomation(ctx, []string{"replies", "on"}); err == nil {
		t.Fatal("a signature must be required")
	}

	answer("yes\n")
	if err := runAutomation(ctx, []string{"replies", "on", "--signature", "- Sam's assistant", "--daily", "5"}); err != nil {
		t.Fatal(err)
	}
	f, _ = st.Load()
	r := f.Settings.Automation.Replies
	if !r.Enabled || r.Signature != "- Sam's assistant" || r.DailyMax != 5 || r.MaxChars != 280 {
		t.Fatalf("unexpected settings: %+v", r)
	}

	// Turning off needs no approval.
	answer("")
	if err := runAutomation(ctx, []string{"replies", "off"}); err != nil {
		t.Fatal(err)
	}
	f, _ = st.Load()
	if f.Settings.Automation.Replies.Enabled {
		t.Fatal("replies still on")
	}
}

func TestAutomationGroupsAndHook(t *testing.T) {
	st := seedSession(t)
	ctx := context.Background()
	answer("yes\n")
	if err := runAutomation(ctx, []string{"groups", "884,1028"}); err != nil {
		t.Fatal(err)
	}
	if err := runAutomation(ctx, []string{"groups", "PPS"}); err == nil {
		t.Fatal("names are not conversation IDs")
	}
	tok := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(tok, []byte("sk-test-token\n"), 0o600)
	if err := runAutomation(ctx, []string{"hook", "on", "--url", "http://example.com/x", "--token-file", tok}); err == nil {
		t.Fatal("plain http must be refused")
	}
	answer("yes\n")
	if err := runAutomation(ctx, []string{"hook", "on", "--url", "https://api.anthropic.com/v1/claude_code/routines/r1/fire", "--token-file", tok}); err != nil {
		t.Fatal(err)
	}
	f, _ := st.Load()
	a := f.Settings.Automation
	if strings.Join(a.Groups, ",") != "884,1028" || !a.Hook.Enabled || a.Hook.Token != "sk-test-token" {
		t.Fatalf("unexpected automation settings: %+v", a)
	}
}
