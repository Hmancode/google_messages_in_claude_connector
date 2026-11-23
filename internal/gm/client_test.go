package gm

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
)

const canary = "CANARY-secret-message-body-7f3a"

type recordEvents struct {
	nopEvents
	msgs []model.Message
}

func (r *recordEvents) Message(m model.Message) { r.msgs = append(r.msgs, m) }

func testMessage() *gmproto.Message {
	return &gmproto.Message{
		MessageID:      "m1",
		ConversationID: "c1",
		ParticipantID:  "p2",
		Timestamp:      1_700_000_000_000_000,
		MessageStatus:  &gmproto.MessageStatus{Status: gmproto.MessageStatusType_INCOMING_COMPLETE},
		MessageInfo: []*gmproto.MessageInfo{
			{Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{Content: canary}}},
			{Data: &gmproto.MessageInfo_MediaContent{MediaContent: &gmproto.MediaContent{MimeType: "image/jpeg", MediaName: "a.jpg", Size: 42}}},
		},
		Reactions: []*gmproto.ReactionEntry{{Data: &gmproto.ReactionData{Unicode: "👍"}, ParticipantIDs: []string{"me1"}}},
	}
}

func TestIncomingMessageConvertedAndNotLogged(t *testing.T) {
	var logBuf bytes.Buffer
	log := zerolog.New(&logBuf).Level(zerolog.TraceLevel)
	ev := &recordEvents{}
	f := newFake()
	c := newWithAPI(f, log, ev)
	c.handle(&gmproto.Settings{SIMCards: []*gmproto.SIMCard{{SIMParticipant: &gmproto.SIMParticipant{ID: "me1"}}}})
	c.noteConversation(f.convs["c1"])
	c.handle(&libgm.WrappedMessage{Message: testMessage()})
	c.handle(struct{ Unknown string }{canary}) // unknown event type is logged by type only

	if len(ev.msgs) != 1 {
		t.Fatalf("expected one message event, got %d", len(ev.msgs))
	}
	m := ev.msgs[0]
	if m.Text != canary || m.SenderName != "Mom" || m.FromMe || m.Status != "received" {
		t.Fatalf("bad conversion: %+v", m)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Type != "image" || m.Attachments[0].Size != 42 {
		t.Fatalf("bad attachment: %+v", m.Attachments)
	}
	if len(m.Reactions) != 1 || m.Reactions[0].By[0] != "Me" {
		t.Fatalf("bad reactions: %+v", m.Reactions)
	}
	if strings.Contains(logBuf.String(), canary) {
		t.Fatal("message text leaked into the log")
	}
}

func TestMessagesFetchedOldestFirst(t *testing.T) {
	f := newFake()
	a, b := testMessage(), testMessage()
	b.MessageID, b.Timestamp = "m2", a.Timestamp-1000
	f.messages = []*gmproto.Message{a, b}
	c := newWithAPI(f, zerolog.Nop(), nopEvents{})
	msgs, err := c.Messages(context.Background(), "c1", 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].ID != "m2" {
		t.Fatalf("expected oldest first, got %+v", msgs)
	}
}

func TestLibgmLogRedaction(t *testing.T) {
	var buf bytes.Buffer
	l := LibgmLogger(&buf, true)
	l.Warn().Str("data", canary).Bytes("resp_body", []byte(canary)).Msg("Got unknown message")
	l.Info().Str("action", "fine").Msg("ok")
	out := buf.String()
	if strings.Contains(out, canary) {
		t.Fatalf("redaction failed: %s", out)
	}
	if !strings.Contains(out, "Got unknown message") || !strings.Contains(out, `"action":"fine"`) {
		t.Fatalf("redaction dropped too much: %s", out)
	}
}

func TestConversationConversion(t *testing.T) {
	conv := convertConversation(newFake().convs["c1"])
	if conv.Name != "Family" || !conv.IsGroup || len(conv.Participants) != 2 {
		t.Fatalf("bad conversation: %+v", conv)
	}
	if conv.Participants[0].Name != "Me" || conv.Participants[1].Phone != "+15550001111" {
		t.Fatalf("bad participants: %+v", conv.Participants)
	}
}

func TestSuccessfulRequestCountsAsPhoneContact(t *testing.T) {
	f := newFake()
	c := newWithAPI(f, zerolog.Nop(), nopEvents{})
	if c.Status().PhoneResponding || !c.Status().LastData.IsZero() {
		t.Fatal("expected no contact before any request")
	}
	if _, err := c.Conversations(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if st := c.Status(); !st.PhoneResponding || st.LastData.IsZero() {
		t.Fatalf("a successful live request should mark the phone as responding: %+v", st)
	}
}
