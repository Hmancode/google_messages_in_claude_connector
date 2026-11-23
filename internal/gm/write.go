package gm

// This is the only file allowed to call libgm's write methods (SendMessage,
// SendReaction, GetOrCreateConversation, UploadMedia). The guard test enforces
// that. Every function here checks the user's settings first, and message
// sends, attachments and new chats also require the user to click Yes in the
// confirmation box before anything leaves the computer.

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/confirm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
)

// Policy is which write actions the user turned on.
type Policy struct {
	AllowSend        bool
	AllowReact       bool
	AllowAttachments bool
	AllowNewChats    bool
}

// Limits.
const (
	MaxTextRunes      = 8000
	MaxAttachmentSize = 100 << 20 // 100 MB
	MaxNewChatMembers = 20
)

// ErrNotAllowed means the user has not turned this action on.
type ErrNotAllowed struct{ Action, Setting string }

func (e ErrNotAllowed) Error() string {
	return fmt.Sprintf("%s is turned off. The user can turn it on by running `gm-reader settings --allow %s` on their computer and restarting Claude", e.Action, e.Setting)
}

// Writer performs write actions.
type Writer struct {
	c       *Client
	policy  Policy
	confirm confirm.Confirmer

	labeledMu sync.Mutex
	labeled   *LabeledConfig
	ledger    ReplyLedger
	now       func() time.Time
}

// NewWriter returns a Writer bound to a client, policy and confirmer.
func NewWriter(c *Client, p Policy, cf confirm.Confirmer) *Writer {
	return &Writer{c: c, policy: p, confirm: cf, now: time.Now}
}

// SendResult reports a sent message.
type SendResult struct {
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status"`
}

func (w *Writer) sendable(ctx context.Context, convID string) (*gmproto.Conversation, model.Conversation, error) {
	if convID == "" {
		return nil, model.Conversation{}, errors.New("conversation_id is required")
	}
	conv, err := w.c.api.GetConversation(ctx, convID)
	if err != nil {
		return nil, model.Conversation{}, fmt.Errorf("could not look up that conversation: %w", err)
	}
	if conv.GetConversationID() == "" {
		return nil, model.Conversation{}, fmt.Errorf("conversation %q does not exist; use list_conversations, or start_chat for a new one", convID)
	}
	if conv.GetReadOnly() {
		return nil, model.Conversation{}, errors.New("this conversation is read-only on the phone")
	}
	return conv, w.c.noteConversation(conv), nil
}

func (w *Writer) simFor(conv *gmproto.Conversation) *gmproto.SIMPayload {
	w.c.mu.RLock()
	sim := w.c.sims[conv.GetDefaultOutgoingID()]
	w.c.mu.RUnlock()
	if p := sim.GetSIMData().GetSIMPayload(); p != nil {
		return p
	}
	return conv.GetSimCard().GetSIMData().GetSIMPayload()
}

func describeConversation(m model.Conversation) string {
	var names []string
	for _, p := range m.Participants {
		if !p.IsMe {
			s := p.Name
			if p.Phone != "" && p.Phone != p.Name {
				s += " " + p.Phone
			}
			names = append(names, s)
		}
	}
	return fmt.Sprintf("To: %s\nPeople: %s", m.Name, strings.Join(names, ", "))
}

func (w *Writer) send(ctx context.Context, conv *gmproto.Conversation, infos []*gmproto.MessageInfo, replyTo string) (SendResult, error) {
	tmpID := uuid.NewString()
	req := &gmproto.SendMessageRequest{
		ConversationID: conv.GetConversationID(),
		MessagePayload: &gmproto.MessagePayload{
			TmpID:          tmpID,
			ConversationID: conv.GetConversationID(),
			ParticipantID:  conv.GetDefaultOutgoingID(),
			TmpID2:         tmpID,
			MessageInfo:    infos,
		},
		SIMPayload: w.simFor(conv),
		TmpID:      tmpID,
	}
	if replyTo != "" {
		req.Reply = &gmproto.ReplyPayload{MessageID: replyTo}
	}
	resp, err := w.c.api.SendMessage(ctx, req)
	if err != nil {
		return SendResult{}, fmt.Errorf("sending failed: %w", err)
	}
	if resp.GetStatus() != gmproto.SendMessageResponse_SUCCESS {
		return SendResult{}, fmt.Errorf("the phone rejected the message (status %s)", resp.GetStatus())
	}
	return SendResult{ConversationID: conv.GetConversationID(), Status: "sent to phone"}, nil
}

func checkText(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("text is empty")
	}
	if utf8.RuneCountInString(text) > MaxTextRunes {
		return fmt.Errorf("text is longer than %d characters", MaxTextRunes)
	}
	return nil
}

// SendText sends a text message to an existing conversation.
func (w *Writer) SendText(ctx context.Context, convID, text, replyTo string) (SendResult, error) {
	if !w.policy.AllowSend {
		return SendResult{}, ErrNotAllowed{"Sending messages", "send"}
	}
	if err := checkText(text); err != nil {
		return SendResult{}, err
	}
	conv, m, err := w.sendable(ctx, convID)
	if err != nil {
		return SendResult{}, err
	}
	details := describeConversation(m) + "\n\nMessage:\n" + text
	if replyTo != "" {
		details += "\n\n(as a reply to an earlier message)"
	}
	if err := w.confirm.Confirm(ctx, confirm.Request{Action: "send a message", Details: details}); err != nil {
		return SendResult{}, err
	}
	return w.send(ctx, conv, []*gmproto.MessageInfo{{
		Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{Content: text}},
	}}, replyTo)
}

func detectMime(path string, data []byte) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); t != "" {
		if i := strings.IndexByte(t, ';'); i > 0 {
			t = t[:i]
		}
		return t
	}
	t := http.DetectContentType(data)
	if i := strings.IndexByte(t, ';'); i > 0 {
		t = t[:i]
	}
	return t
}

// SendFile sends a file from this computer as an attachment.
func (w *Writer) SendFile(ctx context.Context, convID, path, caption string) (SendResult, error) {
	if !w.policy.AllowAttachments {
		return SendResult{}, ErrNotAllowed{"Sending attachments", "attachments"}
	}
	if caption != "" {
		if !w.policy.AllowSend {
			return SendResult{}, ErrNotAllowed{"Sending messages (needed for a caption)", "send"}
		}
		if err := checkText(caption); err != nil {
			return SendResult{}, err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return SendResult{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return SendResult{}, fmt.Errorf("cannot read that file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return SendResult{}, errors.New("that path is not a regular file")
	}
	if st.Size() == 0 || st.Size() > MaxAttachmentSize {
		return SendResult{}, fmt.Errorf("file must be between 1 byte and %d MB", MaxAttachmentSize>>20)
	}
	conv, m, err := w.sendable(ctx, convID)
	if err != nil {
		return SendResult{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return SendResult{}, fmt.Errorf("cannot read that file: %w", err)
	}
	mt := detectMime(abs, data)
	details := fmt.Sprintf("%s\n\nFile: %s\nSize: %.1f MB (%s)\nFull path: %s",
		describeConversation(m), filepath.Base(abs), float64(st.Size())/(1<<20), mt, abs)
	if caption != "" {
		details += "\n\nCaption:\n" + caption
	}
	if err := w.confirm.Confirm(ctx, confirm.Request{Action: "send a file", Details: details}); err != nil {
		return SendResult{}, err
	}
	media, err := w.c.api.UploadMedia(ctx, data, filepath.Base(abs), mt)
	if err != nil {
		return SendResult{}, fmt.Errorf("upload failed: %w", err)
	}
	infos := []*gmproto.MessageInfo{{Data: &gmproto.MessageInfo_MediaContent{MediaContent: media}}}
	if caption != "" {
		infos = append(infos, &gmproto.MessageInfo{
			Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{Content: caption}},
		})
	}
	return w.send(ctx, conv, infos, "")
}

// React adds or removes an emoji reaction on a message. Reactions are
// low-risk and reversible, so they do not show the confirmation box; the
// Claude app's own approval prompt still applies.
func (w *Writer) React(ctx context.Context, convID, messageID, emoji string, remove bool) error {
	if !w.policy.AllowReact {
		return ErrNotAllowed{"Reacting to messages", "react"}
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || utf8.RuneCountInString(emoji) > 16 {
		return errors.New("emoji must be a single emoji")
	}
	if messageID == "" {
		return errors.New("message_id is required")
	}
	conv, _, err := w.sendable(ctx, convID)
	if err != nil {
		return err
	}
	action := gmproto.SendReactionRequest_ADD
	if remove {
		action = gmproto.SendReactionRequest_REMOVE
	}
	resp, err := w.c.api.SendReaction(ctx, &gmproto.SendReactionRequest{
		MessageID:    messageID,
		ReactionData: gmproto.MakeReactionData(emoji),
		Action:       action,
		SIMPayload:   w.simFor(conv),
	})
	if err != nil {
		return fmt.Errorf("reaction failed: %w", err)
	}
	if !resp.GetSuccess() {
		return errors.New("the phone rejected the reaction")
	}
	return nil
}

var phoneChars = regexp.MustCompile(`[\s().\-]`)
var phoneOK = regexp.MustCompile(`^\+?[0-9]{3,15}$`)

// NormalizePhone strips formatting and checks the result looks like a number.
func NormalizePhone(s string) (string, error) {
	n := phoneChars.ReplaceAllString(strings.TrimSpace(s), "")
	if !phoneOK.MatchString(n) {
		return "", fmt.Errorf("%q is not a phone number", s)
	}
	return n, nil
}

// StartChat creates (or finds) a conversation with the given numbers and
// optionally sends a first message. One confirmation covers both.
func (w *Writer) StartChat(ctx context.Context, numbers []string, groupName, firstMessage string) (model.Conversation, *SendResult, error) {
	if !w.policy.AllowNewChats {
		return model.Conversation{}, nil, ErrNotAllowed{"Starting new chats", "new-chats"}
	}
	if firstMessage != "" {
		if !w.policy.AllowSend {
			return model.Conversation{}, nil, ErrNotAllowed{"Sending messages (needed for a first message)", "send"}
		}
		if err := checkText(firstMessage); err != nil {
			return model.Conversation{}, nil, err
		}
	}
	if len(numbers) == 0 || len(numbers) > MaxNewChatMembers {
		return model.Conversation{}, nil, fmt.Errorf("give between 1 and %d phone numbers", MaxNewChatMembers)
	}
	clean := make([]string, 0, len(numbers))
	seen := map[string]bool{}
	for _, n := range numbers {
		p, err := NormalizePhone(n)
		if err != nil {
			return model.Conversation{}, nil, err
		}
		if !seen[p] {
			seen[p] = true
			clean = append(clean, p)
		}
	}
	if groupName != "" && len(clean) < 2 {
		return model.Conversation{}, nil, errors.New("a group name needs at least two phone numbers")
	}
	details := "With: " + strings.Join(clean, ", ")
	if len(clean) > 1 {
		details += "\n(This creates a group chat. Everyone listed may be notified.)"
	}
	if groupName != "" {
		details += "\nGroup name: " + groupName
	}
	if firstMessage != "" {
		details += "\n\nFirst message:\n" + firstMessage
	}
	if err := w.confirm.Confirm(ctx, confirm.Request{Action: "start a new chat", Details: details}); err != nil {
		return model.Conversation{}, nil, err
	}
	req := &gmproto.GetOrCreateConversationRequest{Numbers: make([]*gmproto.ContactNumber, len(clean))}
	for i, p := range clean {
		req.Numbers[i] = &gmproto.ContactNumber{MysteriousInt: 2, Number: p, Number2: p}
	}
	if groupName != "" {
		req.RCSGroupName = &groupName
	}
	resp, err := w.c.api.GetOrCreateConversation(ctx, req)
	if err != nil {
		return model.Conversation{}, nil, fmt.Errorf("could not start the chat: %w", err)
	}
	conv := resp.GetConversation()
	if conv.GetConversationID() == "" {
		return model.Conversation{}, nil, fmt.Errorf("Google Messages couldn't start the chat (status %s)", resp.GetStatus())
	}
	m := w.c.noteConversation(conv)
	if firstMessage == "" {
		return m, nil, nil
	}
	res, err := w.send(ctx, conv, []*gmproto.MessageInfo{{
		Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{Content: firstMessage}},
	}}, "")
	if err != nil {
		return m, nil, fmt.Errorf("chat was created but the first message failed: %w", err)
	}
	return m, &res, nil
}

// --- Labeled replies ---------------------------------------------------------
//
// A labeled reply is a short message sent WITHOUT the confirmation box, for
// building an assistant that answers simple texts. Because no human approves
// each one, every limit below is enforced here in code, not left to the model:
//   - the configured signature is always appended, so recipients can tell;
//   - only one-on-one chats with saved contacts, or group chats on the allowed list;
//   - only as an answer: the newest message must be theirs and recent;
//   - short, with no links, email addresses or long numbers (blocks leaking
//     verification codes, phone numbers or addresses);
//   - at most one per chat per gap, and a daily cap across all chats.

// LabeledConfig is the policy for labeled replies.
type LabeledConfig struct {
	Signature string
	MaxChars  int
	Gap       time.Duration
	DailyMax  int
	Window    time.Duration
	Groups    map[string]bool
}

// ReplyLedger remembers when labeled replies were sent, for the rate limits.
type ReplyLedger interface {
	LastReply(conversationID string) time.Time
	CountSince(t time.Time) int
	Record(conversationID string, t time.Time) error
}

// EnableLabeledReplies turns on labeled replies with the given policy.
func (w *Writer) EnableLabeledReplies(cfg LabeledConfig, ledger ReplyLedger) {
	w.labeled, w.ledger = &cfg, ledger
}

// LabeledReplies returns the active policy, or nil when labeled replies are off.
func (w *Writer) LabeledReplies() *LabeledConfig {
	if w == nil {
		return nil
	}
	return w.labeled
}

var (
	linkRe   = regexp.MustCompile(`(?i)(https?://|www\.|\b[a-z0-9-]+\.(com|net|org|edu|gov|io|co|me|ly|app|us|uk|ca|info|biz|xyz|link|gg|tv|dev|ai|site|online|shop)\b)`)
	emailRe  = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)
	digitsRe = regexp.MustCompile(`\d(?:[\s\-.]?\d){4,}`)
)

// CheckLabeledText enforces the content limits for a labeled reply.
func CheckLabeledText(text string, maxChars int) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("text is empty")
	}
	if n := utf8.RuneCountInString(text); n > maxChars {
		return fmt.Errorf("labeled replies are limited to %d characters (this is %d)", maxChars, n)
	}
	for _, r := range text {
		if (unicode.IsControl(r) && r != '\n') || isBidi(r) {
			return errors.New("labeled replies can't contain control or hidden characters")
		}
	}
	switch {
	case linkRe.MatchString(text):
		return errors.New("labeled replies can't contain links or web addresses")
	case emailRe.MatchString(text):
		return errors.New("labeled replies can't contain email addresses")
	case digitsRe.MatchString(text):
		return errors.New("labeled replies can't contain long numbers (5 or more digits), such as codes or phone numbers")
	}
	return nil
}

func isBidi(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F || r == 0x061C
}

// SendLabeledReply sends a short reply labeled with the configured signature,
// without the confirmation box, if every limit allows it.
func (w *Writer) SendLabeledReply(ctx context.Context, convID, text string) (SendResult, error) {
	if w.labeled == nil || w.ledger == nil || strings.TrimSpace(w.labeled.Signature) == "" {
		return SendResult{}, errors.New("labeled replies are turned off. The user can turn them on with `gm-reader replies on` on their computer and restarting Claude")
	}
	w.labeledMu.Lock()
	defer w.labeledMu.Unlock()
	cfg := *w.labeled
	text = strings.TrimSpace(text)
	if err := CheckLabeledText(text, cfg.MaxChars); err != nil {
		return SendResult{}, err
	}
	conv, m, err := w.sendable(ctx, convID)
	if err != nil {
		return SendResult{}, err
	}
	if m.IsGroup {
		if !cfg.Groups[convID] {
			return SendResult{}, errors.New("this group chat is not on the list of chats allowed for labeled replies")
		}
	} else {
		var others []model.Participant
		for _, p := range m.Participants {
			if !p.IsMe {
				others = append(others, p)
			}
		}
		if len(others) != 1 || !others[0].IsContact {
			return SendResult{}, errors.New("labeled replies only go to people saved in the phone's contacts")
		}
	}
	msgs, err := w.c.Messages(ctx, convID, 5, nil)
	if err != nil {
		return SendResult{}, fmt.Errorf("could not check the latest message: %w", err)
	}
	var latest *model.Message
	for i := len(msgs) - 1; i >= 0; i-- {
		if !msgs[i].System {
			latest = &msgs[i]
			break
		}
	}
	now := w.now()
	switch {
	case latest == nil:
		return SendResult{}, errors.New("there is no message to reply to in this chat")
	case latest.FromMe:
		return SendResult{}, errors.New("the newest message in this chat is already from the user; labeled replies only answer a new message")
	case now.Sub(latest.Time) > cfg.Window:
		return SendResult{}, fmt.Errorf("the newest message is older than %d minutes; labeled replies only answer recent messages", int(cfg.Window.Minutes()))
	}
	if last := w.ledger.LastReply(convID); !last.IsZero() && now.Sub(last) < cfg.Gap {
		wait := cfg.Gap - now.Sub(last)
		return SendResult{}, fmt.Errorf("a labeled reply was already sent in this chat recently; the next one is allowed in %d minutes", int(wait.Minutes())+1)
	}
	if n := w.ledger.CountSince(now.Add(-24 * time.Hour)); n >= cfg.DailyMax {
		return SendResult{}, fmt.Errorf("the daily limit of %d labeled replies has been reached", cfg.DailyMax)
	}
	body := text + "\n" + strings.TrimSpace(cfg.Signature)
	res, err := w.send(ctx, conv, []*gmproto.MessageInfo{{
		Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{Content: body}},
	}}, "")
	if err != nil {
		return res, err
	}
	if err := w.ledger.Record(convID, now); err != nil {
		w.c.log.Warn().Err(err).Msg("Could not record labeled reply for rate limits")
	}
	return res, nil
}
