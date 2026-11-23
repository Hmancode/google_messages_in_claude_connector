package syncer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/hook"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
)

func TestHookOnlyForNewTextsFromAllowedSenders(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]string
		_ = json.Unmarshal(b, &p)
		mu.Lock()
		texts = append(texts, p["text"])
		mu.Unlock()
	}))
	defer srv.Close()

	s := newSyncer(t, newPhone(), true)
	s.settings.Automation.Groups = []string{"g1"}
	s.hook = hook.New(hook.Config{URL: srv.URL, Batch: 50 * time.Millisecond}, zerolog.Nop())
	me := model.Participant{ID: "me", Name: "Me", IsMe: true}
	for _, c := range []model.Conversation{
		{ID: "d1", Participants: []model.Participant{me, {ID: "a", Name: "Saved Friend", IsContact: true}}},
		{ID: "d2", Participants: []model.Participant{me, {ID: "b", Name: "(555) 000-1111"}}},
		{ID: "g1", IsGroup: true, Participants: []model.Participant{me, {ID: "a", IsContact: true}, {ID: "c"}}},
		{ID: "g2", IsGroup: true, Participants: []model.Participant{me, {ID: "a", IsContact: true}, {ID: "c"}}},
	} {
		if err := s.db.UpsertConversation(c); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	msg := func(id, conv string, live, fromMe bool, at time.Time) model.Message {
		return model.Message{ID: id, ConversationID: conv, Live: live, FromMe: fromMe, Time: at, Text: "secret body text"}
	}
	s.Message(msg("1", "d1", true, false, now))                      // contact 1:1: yes
	s.Message(msg("2", "d2", true, false, now))                      // unknown number: no
	s.Message(msg("3", "g1", true, false, now))                      // allowed group: yes
	s.Message(msg("4", "g2", true, false, now))                      // other group: no
	s.Message(msg("5", "d1", false, false, now))                     // replayed history: no
	s.Message(msg("6", "d1", true, true, now))                       // sent by the user (or a labeled reply): no
	s.Message(msg("7", "d1", true, false, now.Add(-30*time.Minute))) // stale: no
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 1 {
		t.Fatalf("expected one batched hook request, got %d: %v", len(texts), texts)
	}
	if texts[0] != "gm-reader: new texts in conversation_id d1 (1 new), conversation_id g1 (1 new)." {
		t.Fatalf("unexpected hook text: %q", texts[0])
	}
	texts = nil
	mu.Unlock()

	// With groups_with_contacts on, g2 (has a saved contact) counts too, but a
	// group of strangers still doesn't.
	if err := s.db.UpsertConversation(model.Conversation{ID: "g3", IsGroup: true, Participants: []model.Participant{me, {ID: "x"}, {ID: "y"}}}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.settings.Automation.Groups = nil
	s.settings.Automation.GroupsWithContacts = true
	s.mu.Unlock()
	s.Message(msg("8", "g2", true, false, time.Now()))
	s.Message(msg("9", "g3", true, false, time.Now()))
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if len(texts) != 1 || texts[0] != "gm-reader: new texts in conversation_id g2 (1 new)." {
		t.Fatalf("unexpected hook requests with groups_with_contacts: %v", texts)
	}
	if strings.Contains(texts[0], "secret") || strings.Contains(texts[0], "Saved Friend") {
		t.Fatal("hook must not carry message text or names")
	}
}

func TestReplyLedgerPersistsAndTrims(t *testing.T) {
	s := newSyncer(t, newPhone(), true)
	if err := s.sess.Save(&session.File{Auth: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	s.replyLog = map[string][]time.Time{}
	now := time.Now()
	if err := s.Record("d1", now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("d1", now); err != nil {
		t.Fatal(err)
	}
	if got := s.CountSince(now.Add(-24 * time.Hour)); got != 1 {
		t.Fatalf("expected 1 reply in the last day, got %d", got)
	}
	f, err := s.sess.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ReplyLog["d1"]) != 1 || !f.ReplyLog["d1"][0].Equal(now) {
		t.Fatalf("log not persisted or not trimmed: %v", f.ReplyLog)
	}
	if got := s.ChatRepliesSince("d1", now.Add(-time.Hour)); len(got) != 1 || !got[0].Equal(now) {
		t.Fatalf("ChatRepliesSince wrong: %v", got)
	}
}
