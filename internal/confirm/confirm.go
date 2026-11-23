// Package confirm asks the human, outside of Claude, before a risky action.
//
// The default (dialog mode) shows a Yes/No box on this Windows computer with
// the exact recipient and content. Claude cannot click it, and neither can a
// message that tries to talk Claude into sending something. If nobody answers
// within Timeout, the action is cancelled.
package confirm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
)

// Timeout is how long the dialog waits before cancelling.
const Timeout = 2 * time.Minute

// ErrDeclined is returned when the user said no or did not answer in time.
var ErrDeclined = errors.New("the user did not approve this action in the confirmation box on their computer (declined or timed out); do not retry unless the user asks")

// Request describes what is about to happen.
type Request struct {
	Action  string // short, e.g. "send a message"
	Details string // multi-line description with recipient and content
}

// Confirmer asks for approval.
type Confirmer interface {
	Confirm(ctx context.Context, r Request) error
}

// New returns the Confirmer for a confirmation mode.
func New(mode string) Confirmer {
	if mode == session.ConfirmClaudeApp {
		return appApproval{}
	}
	return &dialog{}
}

// appApproval relies on the Claude app's own per-tool approval prompt.
type appApproval struct{}

func (appApproval) Confirm(context.Context, Request) error { return nil }

type dialog struct{ mu sync.Mutex }

func (d *dialog) Confirm(ctx context.Context, r Request) error {
	// One box at a time, so approvals can't stack up unseen.
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	title := "gm-reader: Claude wants to " + r.Action
	body := fmt.Sprintf("Claude wants to %s.\n\n%s\n\nClick Yes to allow it, or No to cancel.\nThis box cancels automatically after %d minutes.",
		strings.ToUpper(r.Action), Sanitize(r.Details, 1500), int(Timeout.Minutes()))
	ok, err := showYesNo(title, body, Timeout)
	if err != nil {
		return fmt.Errorf("could not show the confirmation box: %w", err)
	}
	if !ok {
		return ErrDeclined
	}
	return nil
}

// Sanitize makes text safe to show in a confirmation box: it removes
// invisible direction-changing characters that could disguise content,
// replaces other control characters, and truncates long text.
func Sanitize(s string, max int) string {
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= max {
			rest := utf8.RuneCountInString(s) - count
			fmt.Fprintf(&b, "… (%d more characters not shown)", rest)
			break
		}
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case isBidiControl(r):
			b.WriteString("[hidden-char]")
		case r == 0 || unicode.IsControl(r):
			b.WriteRune('�')
		default:
			b.WriteRune(r)
		}
		count++
	}
	return b.String()
}

func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F || r == 0x061C
}
