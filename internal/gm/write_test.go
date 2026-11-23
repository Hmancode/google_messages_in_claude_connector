package gm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/confirm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
)

// fakeAPI records calls instead of talking to Google.
type fakeAPI struct {
	calls     []string
	sent      []*gmproto.SendMessageRequest
	reactions []*gmproto.SendReactionRequest
	created   []*gmproto.GetOrCreateConversationRequest
	convs     map[string]*gmproto.Conversation
	messages  []*gmproto.Message
	cursors   []*gmproto.Cursor
}

func newFake() *fakeAPI {
	return &fakeAPI{convs: map[string]*gmproto.Conversation{
		"c1": {
			ConversationID:    "c1",
			Name:              "Family",
			IsGroupChat:       true,
			DefaultOutgoingID: "me1",
			Participants: []*gmproto.Participant{
				{ID: &gmproto.SmallInfo{ParticipantID: "me1"}, IsMe: true, IsVisible: true},
				{ID: &gmproto.SmallInfo{ParticipantID: "p2", Number: "+15550001111"}, FullName: "Mom", IsVisible: true},
			},
		},
	}}
}

func (f *fakeAPI) rec(s string)                       { f.calls = append(f.calls, s) }
func (f *fakeAPI) SetEventHandler(libgm.EventHandler) {}
func (f *fakeAPI) Connect(context.Context) error      { f.rec("Connect"); return nil }
func (f *fakeAPI) Disconnect()                        {}
func (f *fakeAPI) IsConnected() bool                  { return true }
func (f *fakeAPI) IsLoggedIn() bool                   { return true }
func (f *fakeAPI) FetchConfig(context.Context) error  { return nil }
func (f *fakeAPI) StartGaiaPairing(context.Context, context.Context) (string, *libgm.PairingSession, error) {
	return "", nil, errors.New("not in tests")
}
func (f *fakeAPI) FinishGaiaPairing(context.Context, *libgm.PairingSession) (string, error) {
	return "", errors.New("not in tests")
}
func (f *fakeAPI) ListConversations(context.Context, *gmproto.ListConversationsRequest) (*gmproto.ListConversationsResponse, error) {
	f.rec("ListConversations")
	out := &gmproto.ListConversationsResponse{}
	for _, c := range f.convs {
		out.Conversations = append(out.Conversations, c)
	}
	return out, nil
}
func (f *fakeAPI) GetConversation(_ context.Context, id string) (*gmproto.Conversation, error) {
	f.rec("GetConversation")
	if c, ok := f.convs[id]; ok {
		return c, nil
	}
	return &gmproto.Conversation{}, nil
}
func (f *fakeAPI) FetchMessages(_ context.Context, _ string, _ int64, cur *gmproto.Cursor) (*gmproto.ListMessagesResponse, error) {
	f.rec("FetchMessages")
	f.cursors = append(f.cursors, cur)
	return &gmproto.ListMessagesResponse{Messages: f.messages}, nil
}
func (f *fakeAPI) ListContacts(context.Context) (*gmproto.ListContactsResponse, error) {
	return &gmproto.ListContactsResponse{}, nil
}
func (f *fakeAPI) SendMessage(_ context.Context, r *gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
	f.rec("SendMessage")
	f.sent = append(f.sent, r)
	return &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_SUCCESS}, nil
}
func (f *fakeAPI) SendReaction(_ context.Context, r *gmproto.SendReactionRequest) (*gmproto.SendReactionResponse, error) {
	f.rec("SendReaction")
	f.reactions = append(f.reactions, r)
	return &gmproto.SendReactionResponse{Success: true}, nil
}
func (f *fakeAPI) GetOrCreateConversation(_ context.Context, r *gmproto.GetOrCreateConversationRequest) (*gmproto.GetOrCreateConversationResponse, error) {
	f.rec("GetOrCreateConversation")
	f.created = append(f.created, r)
	c := &gmproto.Conversation{ConversationID: "new1", DefaultOutgoingID: "me1"}
	f.convs["new1"] = c
	return &gmproto.GetOrCreateConversationResponse{Conversation: c, Status: gmproto.GetOrCreateConversationResponse_SUCCESS}, nil
}
func (f *fakeAPI) UploadMedia(context.Context, []byte, string, string) (*gmproto.MediaContent, error) {
	f.rec("UploadMedia")
	return &gmproto.MediaContent{MediaID: "m1"}, nil
}

type nopEvents struct{}

func (nopEvents) Conversation(model.Conversation) {}
func (nopEvents) Message(model.Message)           {}
func (nopEvents) Ready()                          {}
func (nopEvents) SessionChanged()                 {}
func (nopEvents) StatusChanged(Status)            {}

// scripted is a Confirmer that answers yes or no and records what it was shown.
type scripted struct {
	answer bool
	seen   []confirm.Request
}

func (s *scripted) Confirm(_ context.Context, r confirm.Request) error {
	s.seen = append(s.seen, r)
	if !s.answer {
		return confirm.ErrDeclined
	}
	return nil
}

var allOn = Policy{AllowSend: true, AllowReact: true, AllowAttachments: true, AllowNewChats: true}

func setup(p Policy, answer bool) (*fakeAPI, *scripted, *Writer) {
	f := newFake()
	c := newWithAPI(f, zerolog.Nop(), nopEvents{})
	s := &scripted{answer: answer}
	return f, s, NewWriter(c, p, s)
}

func countCalls(f *fakeAPI, name string) int {
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

func TestEverythingOffByPolicy(t *testing.T) {
	f, s, w := setup(Policy{}, true)
	ctx := context.Background()
	var na ErrNotAllowed
	if _, err := w.SendText(ctx, "c1", "hi", ""); !errors.As(err, &na) {
		t.Errorf("send: expected ErrNotAllowed, got %v", err)
	}
	if err := w.React(ctx, "c1", "m1", "👍", false); !errors.As(err, &na) {
		t.Errorf("react: expected ErrNotAllowed, got %v", err)
	}
	if _, err := w.SendFile(ctx, "c1", "x.png", ""); !errors.As(err, &na) {
		t.Errorf("file: expected ErrNotAllowed, got %v", err)
	}
	if _, _, err := w.StartChat(ctx, []string{"+15550001111"}, "", ""); !errors.As(err, &na) {
		t.Errorf("start chat: expected ErrNotAllowed, got %v", err)
	}
	if len(f.calls) != 0 || len(s.seen) != 0 {
		t.Fatalf("nothing should reach the API or the dialog when turned off: calls=%v dialogs=%d", f.calls, len(s.seen))
	}
}

func TestDeclinedSendSendsNothing(t *testing.T) {
	f, s, w := setup(allOn, false)
	_, err := w.SendText(context.Background(), "c1", "hello there", "")
	if !errors.Is(err, confirm.ErrDeclined) {
		t.Fatalf("expected ErrDeclined, got %v", err)
	}
	if countCalls(f, "SendMessage") != 0 {
		t.Fatal("message was sent even though the user clicked No")
	}
	if len(s.seen) != 1 || !strings.Contains(s.seen[0].Details, "hello there") || !strings.Contains(s.seen[0].Details, "Mom") {
		t.Fatalf("dialog must show the recipient and text: %+v", s.seen)
	}
}

func TestApprovedSend(t *testing.T) {
	f, _, w := setup(allOn, true)
	res, err := w.SendText(context.Background(), "c1", "hello", "m0")
	if err != nil {
		t.Fatal(err)
	}
	if res.ConversationID != "c1" || len(f.sent) != 1 {
		t.Fatalf("unexpected result %+v sent=%d", res, len(f.sent))
	}
	req := f.sent[0]
	if req.GetMessagePayload().GetParticipantID() != "me1" || req.GetReply().GetMessageID() != "m0" {
		t.Fatalf("request built wrong: %+v", req)
	}
	if got := req.GetMessagePayload().GetMessageInfo()[0].GetMessageContent().GetContent(); got != "hello" {
		t.Fatalf("wrong text %q", got)
	}
}

func TestSendOnlyToExistingChats(t *testing.T) {
	f, s, w := setup(allOn, true)
	if _, err := w.SendText(context.Background(), "nope", "hi", ""); err == nil {
		t.Fatal("expected an error for an unknown conversation")
	}
	if countCalls(f, "SendMessage") != 0 || len(s.seen) != 0 {
		t.Fatal("unknown conversation must fail before the dialog and before sending")
	}
}

func TestReactNoDialog(t *testing.T) {
	f, s, w := setup(allOn, false)
	if err := w.React(context.Background(), "c1", "m1", "❤️", false); err != nil {
		t.Fatal(err)
	}
	if len(s.seen) != 0 || len(f.reactions) != 1 || f.reactions[0].GetAction() != gmproto.SendReactionRequest_ADD {
		t.Fatalf("react should not show a dialog and should add: dialogs=%d reactions=%v", len(s.seen), f.reactions)
	}
}

func TestAttachmentNeedsApprovalAndShowsPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(p, bytes.Repeat([]byte{1}, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	f, s, w := setup(allOn, false)
	if _, err := w.SendFile(context.Background(), "c1", p, ""); !errors.Is(err, confirm.ErrDeclined) {
		t.Fatalf("expected ErrDeclined, got %v", err)
	}
	if countCalls(f, "UploadMedia") != 0 {
		t.Fatal("file was uploaded before approval")
	}
	if !strings.Contains(s.seen[0].Details, p) {
		t.Fatalf("dialog must show the full file path: %q", s.seen[0].Details)
	}
	f2, _, w2 := setup(allOn, true)
	if _, err := w2.SendFile(context.Background(), "c1", p, ""); err != nil {
		t.Fatal(err)
	}
	if countCalls(f2, "UploadMedia") != 1 || countCalls(f2, "SendMessage") != 1 {
		t.Fatalf("expected one upload and one send, got %v", f2.calls)
	}
}

func TestCaptionNeedsSendPermission(t *testing.T) {
	f, _, w := setup(Policy{AllowAttachments: true}, true)
	var na ErrNotAllowed
	if _, err := w.SendFile(context.Background(), "c1", "x.png", "look"); !errors.As(err, &na) {
		t.Fatalf("expected ErrNotAllowed for caption without send permission, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("no API calls expected")
	}
}

func TestStartChat(t *testing.T) {
	f, s, w := setup(allOn, true)
	conv, res, err := w.StartChat(context.Background(), []string{"(555) 000-1111", "+1 555 000 2222"}, "Trip", "hi all")
	if err != nil {
		t.Fatal(err)
	}
	if conv.ID != "new1" || res == nil {
		t.Fatalf("unexpected result %+v %+v", conv, res)
	}
	if len(s.seen) != 1 {
		t.Fatalf("one dialog should cover creating the chat and the first message, got %d", len(s.seen))
	}
	nums := f.created[0].GetNumbers()
	if nums[0].GetNumber() != "5550001111" || nums[1].GetNumber() != "+15550002222" {
		t.Fatalf("numbers not normalized: %v", nums)
	}
	if _, _, err := w.StartChat(context.Background(), []string{"not a number"}, "", ""); err == nil {
		t.Fatal("expected an error for a bad number")
	}
}

func TestStartChatDeclined(t *testing.T) {
	f, _, w := setup(allOn, false)
	if _, _, err := w.StartChat(context.Background(), []string{"+15550001111"}, "", "hey"); !errors.Is(err, confirm.ErrDeclined) {
		t.Fatalf("expected ErrDeclined, got %v", err)
	}
	if len(f.created) != 0 || len(f.sent) != 0 {
		t.Fatal("nothing should be created or sent when declined")
	}
}
