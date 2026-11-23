package syncer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/gm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/store"
)

var base = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

// fakePhone holds 300 messages in one conversation, one per minute.
type fakePhone struct {
	connected bool
	fail      bool
	msgs      []model.Message
	calls     int
}

func newPhone() *fakePhone {
	p := &fakePhone{connected: true}
	for i := 0; i < 300; i++ {
		p.msgs = append(p.msgs, model.Message{ID: fmt.Sprintf("m%03d", i), ConversationID: "c1", SenderName: "Ana",
			Time: base.Add(time.Duration(i) * time.Minute), Text: fmt.Sprintf("message number %d", i)})
	}
	return p
}

func (p *fakePhone) Start(context.Context) error { return nil }
func (p *fakePhone) Close()                      {}
func (p *fakePhone) Status() gm.Status {
	return gm.Status{Connected: p.connected, PhoneResponding: true}
}
func (p *fakePhone) AuthJSON() ([]byte, error) { return []byte(`{}`), nil }
func (p *fakePhone) Conversations(context.Context, int) ([]model.Conversation, error) {
	if p.fail {
		return nil, errors.New("phone offline")
	}
	return []model.Conversation{{ID: "c1", Name: "Crew", IsGroup: true, LastMessageTime: p.msgs[len(p.msgs)-1].Time}}, nil
}
func (p *fakePhone) Messages(_ context.Context, _ string, limit int, before *model.Cursor) ([]model.Message, error) {
	p.calls++
	if p.fail {
		return nil, errors.New("phone offline")
	}
	end := len(p.msgs)
	if before != nil {
		for i, m := range p.msgs {
			if m.ID == before.MessageID {
				end = i
				break
			}
		}
	}
	// Like the real phone, return a couple fewer than asked for.
	if limit > 3 {
		limit -= 2
	}
	start := max(0, end-limit)
	return append([]model.Message(nil), p.msgs[start:end]...), nil
}
func (p *fakePhone) Contacts(context.Context) ([]model.Contact, error) {
	return []model.Contact{{Name: "Ana Lopez", Phone: "+1 (555) 010-2000"}, {Name: "Ben", Phone: "+15550103000"}}, nil
}

func newSyncer(t *testing.T, p *fakePhone, inMemory bool) *Syncer {
	t.Helper()
	path := ""
	if !inMemory {
		path = filepath.Join(t.TempDir(), "cache.db")
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := New(zerolog.Nop(), zerolog.Nop(), session.OpenAt(filepath.Join(t.TempDir(), "s.bin")), db, Options{History: 120})
	if p != nil {
		s.client = p
		s.paired = true
	}
	return s
}

func TestNotPaired(t *testing.T) {
	s := newSyncer(t, nil, true)
	if _, _, err := s.ReadMessages(context.Background(), "c1", 10, time.Time{}); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("expected ErrNotPaired, got %v", err)
	}
	r := s.StatusReport()
	if r.Paired || len(r.Warnings) == 0 {
		t.Fatalf("status should explain how to pair: %+v", r)
	}
}

func TestSyncBackfillsHistory(t *testing.T) {
	p := newPhone()
	s := newSyncer(t, p, true)
	s.Sync(context.Background())
	if n, _ := s.db.MessageCount("c1"); n != 120 {
		t.Fatalf("expected 120 messages after first sync, got %d", n)
	}
	// Nothing new: a second sync must not fetch messages again.
	calls := p.calls
	s.Sync(context.Background())
	if p.calls != calls {
		t.Fatalf("second sync refetched messages (%d calls)", p.calls-calls)
	}
	hits, err := s.Search("number 299", "", time.Time{}, 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search after sync: %v %+v", err, hits)
	}
}

func TestReadLatestAndPageBack(t *testing.T) {
	p := newPhone()
	s := newSyncer(t, p, false)
	ctx := context.Background()
	msgs, live, err := s.ReadMessages(ctx, "c1", 50, time.Time{})
	if err != nil || !live || len(msgs) != 50 || msgs[49].ID != "m299" || msgs[0].ID != "m250" {
		t.Fatalf("latest: live=%v err=%v n=%d", live, err, len(msgs))
	}
	older, _, err := s.ReadMessages(ctx, "c1", 50, msgs[0].Time)
	if err != nil || len(older) != 50 || older[49].ID != "m249" || older[0].ID != "m200" {
		t.Fatalf("page back: err=%v n=%d first=%v", err, len(older), older)
	}
	// A time far back with nothing cached there walks back to it.
	far, _, err := s.ReadMessages(ctx, "c1", 10, base.Add(30*time.Minute))
	if err != nil || len(far) != 10 || far[9].ID != "m029" {
		t.Fatalf("far page: err=%v %+v", err, far)
	}
}

func TestOfflineFallsBackToCache(t *testing.T) {
	p := newPhone()
	s := newSyncer(t, p, true)
	ctx := context.Background()
	if _, _, err := s.ReadMessages(ctx, "c1", 20, time.Time{}); err != nil {
		t.Fatal(err)
	}
	p.connected = false
	msgs, live, err := s.ReadMessages(ctx, "c1", 20, time.Time{})
	if err != nil || live || len(msgs) != 20 {
		t.Fatalf("offline read should come from cache: live=%v err=%v n=%d", live, err, len(msgs))
	}
	p.connected, p.fail = true, true
	msgs, live, err = s.ReadMessages(ctx, "c1", 20, time.Time{})
	if err != nil || live || len(msgs) != 20 {
		t.Fatalf("failing phone should fall back to cache: live=%v err=%v n=%d", live, err, len(msgs))
	}
	convs, live, err := s.ListConversations(ctx, 10, false)
	if err != nil || live {
		t.Fatalf("list should fall back: %v %v %+v", err, live, convs)
	}
}

func TestContacts(t *testing.T) {
	s := newSyncer(t, newPhone(), true)
	got, err := s.Contacts(context.Background(), "ana", 10)
	if err != nil || len(got) != 1 || got[0].Name != "Ana Lopez" {
		t.Fatalf("name search: %v %+v", err, got)
	}
	got, _ = s.Contacts(context.Background(), "555-010-30", 10)
	if len(got) != 1 || got[0].Name != "Ben" {
		t.Fatalf("number search: %+v", got)
	}
}

func TestRepairCountdown(t *testing.T) {
	s := newSyncer(t, newPhone(), true)
	s.lastContact = time.Now().Add(-11 * 24 * time.Hour)
	r := s.StatusReport()
	if r.DaysUntilRepair == nil || *r.DaysUntilRepair > 3 {
		t.Fatalf("expected about 3 days left, got %v", r.DaysUntilRepair)
	}
	found := false
	for _, w := range r.Warnings {
		if len(w) > 0 && w[:7] == "The pai" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an early expiry warning: %v", r.Warnings)
	}
}
