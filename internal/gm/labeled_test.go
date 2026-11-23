package gm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

type memLedger struct{ log map[string][]time.Time }

func (l *memLedger) ChatRepliesSince(id string, t time.Time) []time.Time {
	var out []time.Time
	for _, x := range l.log[id] {
		if x.After(t) {
			out = append(out, x)
		}
	}
	return out
}
func (l *memLedger) CountSince(t time.Time) int {
	n := 0
	for _, ts := range l.log {
		for _, x := range ts {
			if x.After(t) {
				n++
			}
		}
	}
	return n
}
func (l *memLedger) Record(id string, t time.Time) error {
	l.log[id] = append(l.log[id], t)
	return nil
}

var labeledNow = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func incoming(id string, minsAgo int, fromMe bool) *gmproto.Message {
	st := gmproto.MessageStatusType_INCOMING_COMPLETE
	if fromMe {
		st = gmproto.MessageStatusType_OUTGOING_COMPLETE
	}
	return &gmproto.Message{
		MessageID: id, ConversationID: "x", ParticipantID: "p",
		Timestamp:     labeledNow.Add(-time.Duration(minsAgo) * time.Minute).UnixMicro(),
		MessageStatus: &gmproto.MessageStatus{Status: st},
		MessageInfo:   []*gmproto.MessageInfo{{Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{Content: "are you free later?"}}}},
	}
}

func labeledSetup(t *testing.T, groups ...string) (*fakeAPI, *scripted, *Writer, *memLedger) {
	return labeledSetupCfg(t, LabeledConfig{Signature: "— Sam's assistant", MaxChars: 280, PerChatHour: 2, DailyMax: 3, Window: 2 * time.Hour}, groups...)
}

func labeledSetupCfg(t *testing.T, cfg LabeledConfig, groups ...string) (*fakeAPI, *scripted, *Writer, *memLedger) {
	t.Helper()
	f := newFake()
	f.convs["d1"] = &gmproto.Conversation{ConversationID: "d1", Name: "Tony", DefaultOutgoingID: "me1", Participants: []*gmproto.Participant{
		{ID: &gmproto.SmallInfo{ParticipantID: "me1"}, IsMe: true, IsVisible: true},
		{ID: &gmproto.SmallInfo{ParticipantID: "t1", Number: "+15550009999"}, FullName: "Tony", ContactID: "c-77", IsVisible: true},
	}}
	f.convs["d2"] = &gmproto.Conversation{ConversationID: "d2", Name: "(555) 000-1234", DefaultOutgoingID: "me1", Participants: []*gmproto.Participant{
		{ID: &gmproto.SmallInfo{ParticipantID: "me1"}, IsMe: true, IsVisible: true},
		{ID: &gmproto.SmallInfo{ParticipantID: "u1", Number: "+15550001234"}, FormattedNumber: "(555) 000-1234", IsVisible: true},
	}}
	f.convs["g8"] = &gmproto.Conversation{ConversationID: "g8", Name: "Team", IsGroupChat: true, DefaultOutgoingID: "me1", Participants: []*gmproto.Participant{
		{ID: &gmproto.SmallInfo{ParticipantID: "me1"}, IsMe: true, IsVisible: true},
		{ID: &gmproto.SmallInfo{ParticipantID: "t1", Number: "+15550009999"}, FullName: "Tony", ContactID: "c-77", IsVisible: true},
		{ID: &gmproto.SmallInfo{ParticipantID: "u4", Number: "+15550004444"}, IsVisible: true},
	}}
	f.convs["g9"] = &gmproto.Conversation{ConversationID: "g9", Name: "Strangers", IsGroupChat: true, DefaultOutgoingID: "me1", Participants: []*gmproto.Participant{
		{ID: &gmproto.SmallInfo{ParticipantID: "me1"}, IsMe: true, IsVisible: true},
		{ID: &gmproto.SmallInfo{ParticipantID: "u2", Number: "+15550002222"}, IsVisible: true},
		{ID: &gmproto.SmallInfo{ParticipantID: "u3", Number: "+15550003333"}, IsVisible: true},
	}}
	f.messages = []*gmproto.Message{incoming("m1", 5, false)}
	c := newWithAPI(f, zerolog.Nop(), nopEvents{})
	s := &scripted{answer: true}
	w := NewWriter(c, Policy{}, s)
	w.now = func() time.Time { return labeledNow }
	g := map[string]bool{}
	for _, id := range groups {
		g[id] = true
	}
	l := &memLedger{log: map[string][]time.Time{}}
	cfg.Groups = g
	w.EnableLabeledReplies(cfg, l)
	return f, s, w, l
}

func TestLabeledReplyOffByDefault(t *testing.T) {
	f := newFake()
	w := NewWriter(newWithAPI(f, zerolog.Nop(), nopEvents{}), Policy{}, &scripted{})
	if _, err := w.SendLabeledReply(context.Background(), "d1", "hi"); err == nil {
		t.Fatal("labeled replies must be off unless enabled")
	}
	if countCalls(f, "SendMessage") != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestLabeledReplyToContactIsSignedAndSkipsDialog(t *testing.T) {
	f, s, w, l := labeledSetup(t)
	if _, err := w.SendLabeledReply(context.Background(), "d1", "Sam's busy until 4:30, free after. On 11/24 he's free all day."); err != nil {
		t.Fatal(err)
	}
	if len(s.seen) != 0 {
		t.Fatal("labeled replies must not show the confirmation box")
	}
	body := f.sent[0].GetMessagePayload().GetMessageInfo()[0].GetMessageContent().GetContent()
	if !strings.HasSuffix(body, "\n— Sam's assistant") {
		t.Fatalf("signature missing: %q", body)
	}
	if len(l.log["d1"]) != 1 {
		t.Fatal("reply was not recorded for rate limits")
	}
}

func TestLabeledReplyWhoLimits(t *testing.T) {
	f, _, w, _ := labeledSetup(t)
	ctx := context.Background()
	if _, err := w.SendLabeledReply(ctx, "d2", "hi"); err == nil || !strings.Contains(err.Error(), "contacts") {
		t.Fatalf("non-contact must be refused, got %v", err)
	}
	if _, err := w.SendLabeledReply(ctx, "c1", "hi"); err == nil || !strings.Contains(err.Error(), "group chat") {
		t.Fatalf("group not on the list must be refused, got %v", err)
	}
	if countCalls(f, "SendMessage") != 0 {
		t.Fatal("nothing may be sent")
	}
	f2, _, w2, _ := labeledSetup(t, "c1")
	if _, err := w2.SendLabeledReply(ctx, "c1", "Sam's in a meeting until noon."); err != nil {
		t.Fatalf("allowed group should work: %v", err)
	}
	if countCalls(f2, "SendMessage") != 1 {
		t.Fatal("expected one send")
	}
}

func TestLabeledReplyGroupsWithContacts(t *testing.T) {
	ctx := context.Background()
	f, _, w, _ := labeledSetupCfg(t, LabeledConfig{Signature: "— Sam's assistant", MaxChars: 280, PerChatHour: 2, DailyMax: 5, Window: 2 * time.Hour, GroupsWithContacts: true})
	if _, err := w.SendLabeledReply(ctx, "g8", "Sam's in class until 2."); err != nil {
		t.Fatalf("group with a saved contact should work: %v", err)
	}
	if _, err := w.SendLabeledReply(ctx, "g9", "hi"); err == nil || !strings.Contains(err.Error(), "no one saved") {
		t.Fatalf("group with no saved contacts must be refused, got %v", err)
	}
	if _, err := w.SendLabeledReply(ctx, "d2", "hi"); err == nil || !strings.Contains(err.Error(), "contacts") {
		t.Fatalf("non-contact one-on-one must still be refused, got %v", err)
	}
	if countCalls(f, "SendMessage") != 1 {
		t.Fatalf("expected one send, got %d", countCalls(f, "SendMessage"))
	}
}

func TestLabeledReplyOnlyAnswersRecentIncoming(t *testing.T) {
	ctx := context.Background()
	f, _, w, _ := labeledSetup(t)
	f.messages = []*gmproto.Message{incoming("m1", 10, false), incoming("m2", 5, true)}
	if _, err := w.SendLabeledReply(ctx, "d1", "hi"); err == nil || !strings.Contains(err.Error(), "already from the user") {
		t.Fatalf("must not reply when the user spoke last, got %v", err)
	}
	f.messages = []*gmproto.Message{incoming("m1", 300, false)}
	if _, err := w.SendLabeledReply(ctx, "d1", "hi"); err == nil || !strings.Contains(err.Error(), "older than") {
		t.Fatalf("must not answer old messages, got %v", err)
	}
	if countCalls(f, "SendMessage") != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestLabeledReplyContentLimits(t *testing.T) {
	ctx := context.Background()
	bad := map[string]string{
		"link":       "see https://example.com",
		"bare link":  "check example.com for it",
		"www":        "go to www.thing.org",
		"email":      "email sam@example.edu",
		"code":       "the code is 482913",
		"split code": "code 482 913",
		"phone":      "call 315-680-9885",
		"too long":   strings.Repeat("a", 281),
		"hidden":     "busy ‮ later",
	}
	for name, text := range bad {
		f, _, w, _ := labeledSetup(t)
		if _, err := w.SendLabeledReply(ctx, "d1", text); err == nil {
			t.Errorf("%s: %q should be refused", name, text)
		}
		if countCalls(f, "SendMessage") != 0 {
			t.Errorf("%s: nothing may be sent", name)
		}
	}
}

func TestLabeledReplyRateLimits(t *testing.T) {
	ctx := context.Background()
	f, _, w, l := labeledSetup(t, "c1")
	// An older reply outside the hour doesn't count toward the per-chat limit.
	l.log["d1"] = []time.Time{labeledNow.Add(-61 * time.Minute)}
	if _, err := w.SendLabeledReply(ctx, "d1", "busy until 4"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SendLabeledReply(ctx, "d1", "still busy"); err != nil {
		t.Fatalf("second reply in the hour is allowed: %v", err)
	}
	if _, err := w.SendLabeledReply(ctx, "d1", "busy busy"); err == nil || !strings.Contains(err.Error(), "last hour") {
		t.Fatalf("per-chat hourly limit must apply, got %v", err)
	}
	// Daily cap is 3 across all chats: 3 in the log within 24 hours (one older than an hour).
	if _, err := w.SendLabeledReply(ctx, "c1", "busy until 4"); err == nil || !strings.Contains(err.Error(), "daily limit") {
		t.Fatalf("daily cap must apply, got %v", err)
	}
	if countCalls(f, "SendMessage") != 2 {
		t.Fatalf("expected exactly two sends, got %d", countCalls(f, "SendMessage"))
	}
}

func TestContactFlag(t *testing.T) {
	c := convertConversation(&gmproto.Conversation{ConversationID: "x", Participants: []*gmproto.Participant{
		{ID: &gmproto.SmallInfo{ParticipantID: "a"}, FullName: "Saved", ContactID: "1", IsVisible: true},
		{ID: &gmproto.SmallInfo{ParticipantID: "b"}, FormattedNumber: "(555) 111-2222", IsVisible: true},
	}})
	if !c.Participants[0].IsContact || c.Participants[1].IsContact {
		t.Fatalf("contact flags wrong: %+v", c.Participants)
	}
}
