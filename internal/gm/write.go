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
}

// NewWriter returns a Writer bound to a client, policy and confirmer.
func NewWriter(c *Client, p Policy, cf confirm.Confirmer) *Writer {
	return &Writer{c: c, policy: p, confirm: cf}
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
