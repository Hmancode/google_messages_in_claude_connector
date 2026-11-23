package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
)

func fill(t *testing.T, s *Store) time.Time {
	t.Helper()
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertConversation(model.Conversation{ID: "g1", Name: "Climbing crew", IsGroup: true, LastMessageTime: base.Add(3 * time.Minute),
		Participants: []model.Participant{{ID: "p1", Name: "Ana"}, {ID: "me", Name: "Me", IsMe: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertConversation(model.Conversation{ID: "d1", Name: "Ben", LastMessageTime: base}); err != nil {
		t.Fatal(err)
	}
	msgs := []model.Message{
		{ID: "m1", ConversationID: "g1", SenderName: "Ana", Time: base.Add(1 * time.Minute), Text: "Meet at the bouldering gym at 6?"},
		{ID: "m2", ConversationID: "g1", SenderName: "Me", FromMe: true, Time: base.Add(2 * time.Minute), Text: "Sounds good 👍",
			Reactions: []model.Reaction{{Emoji: "❤️", Count: 1}}},
		{ID: "m3", ConversationID: "g1", SenderName: "Ana", Time: base.Add(3 * time.Minute), Text: "",
			Attachments: []model.Attachment{{Type: "image", MimeType: "image/jpeg"}}},
		{ID: "m4", ConversationID: "d1", SenderName: "Ben", Time: base, Text: "Did you finish the café report?"},
	}
	if err := s.UpsertMessages(msgs); err != nil {
		t.Fatal(err)
	}
	return base
}

func TestStoreOnDiskAndMemory(t *testing.T) {
	for name, path := range map[string]string{"disk": filepath.Join(t.TempDir(), "cache.db"), "memory": ""} {
		t.Run(name, func(t *testing.T) {
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.InMemory() != (path == "") {
				t.Fatal("InMemory mismatch")
			}
			base := fill(t, s)

			groups, err := s.Conversations(10, true)
			if err != nil || len(groups) != 1 || groups[0].Name != "Climbing crew" || len(groups[0].Participants) != 2 {
				t.Fatalf("groups: %v %+v", err, groups)
			}
			all, _ := s.Conversations(10, false)
			if len(all) != 2 || all[0].ID != "g1" {
				t.Fatalf("expected most recent first: %+v", all)
			}

			msgs, err := s.Messages("g1", 2, time.Time{})
			if err != nil || len(msgs) != 2 || msgs[0].ID != "m2" || msgs[1].ID != "m3" {
				t.Fatalf("latest two oldest-first: %v %+v", err, msgs)
			}
			if len(msgs[0].Reactions) != 1 || len(msgs[1].Attachments) != 1 {
				t.Fatalf("json fields lost: %+v", msgs)
			}
			older, _ := s.Messages("g1", 10, base.Add(2*time.Minute))
			if len(older) != 1 || older[0].ID != "m1" {
				t.Fatalf("before filter: %+v", older)
			}

			hits, err := s.Search("bouldering gym", "", time.Time{}, 10)
			if err != nil || len(hits) != 1 || hits[0].ID != "m1" || hits[0].ConversationName != "Climbing crew" {
				t.Fatalf("search: %v %+v", err, hits)
			}
			if hits[0].Snippet == "" {
				t.Fatal("expected a snippet")
			}
			// Accents are ignored, and FTS operators in user input are harmless.
			if hits, err := s.Search("cafe", "", time.Time{}, 10); err != nil || len(hits) != 1 {
				t.Fatalf("diacritics: %v %+v", err, hits)
			}
			if _, err := s.Search(`report" OR "x NEAR(`, "", time.Time{}, 10); err != nil {
				t.Fatalf("operator injection should not error: %v", err)
			}
			if hits, _ := s.Search("👍", "", time.Time{}, 10); len(hits) != 1 {
				t.Fatalf("emoji search should fall back to LIKE: %+v", hits)
			}
			if hits, _ := s.Search("gym", "d1", time.Time{}, 10); len(hits) != 0 {
				t.Fatal("conversation filter ignored")
			}
			if hits, _ := s.Search("report", "", base.Add(time.Hour), 10); len(hits) != 0 {
				t.Fatal("since filter ignored")
			}

			// Updating a message updates the index.
			if err := s.UpsertMessages([]model.Message{{ID: "m1", ConversationID: "g1", SenderName: "Ana", Time: base.Add(time.Minute), Text: "Changed plans, lead climbing instead"}}); err != nil {
				t.Fatal(err)
			}
			if hits, _ := s.Search("bouldering", "", time.Time{}, 10); len(hits) != 0 {
				t.Fatal("stale index entry after update")
			}
			if hits, _ := s.Search("lead climbing", "", time.Time{}, 10); len(hits) != 1 {
				t.Fatal("updated text not indexed")
			}

			cur, ok, _ := s.CursorAt("g1", base.Add(150*time.Second))
			if !ok || cur.MessageID != "m3" {
				t.Fatalf("cursor: %+v %v", cur, ok)
			}
			latest, _ := s.LatestMessageTime("g1")
			if !latest.Equal(base.Add(3 * time.Minute)) {
				t.Fatalf("latest: %v", latest)
			}
		})
	}
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s)
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, msgs, _ := s.Totals(); msgs != 4 {
		t.Fatalf("expected 4 messages after reopen, got %d", msgs)
	}
}
