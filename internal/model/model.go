// Package model holds the plain data types that the rest of gm-reader works with.
// Nothing here depends on libgm, so code outside internal/gm never sees libgm types.
package model

import "time"

// Participant is one member of a conversation.
type Participant struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
	IsMe  bool   `json:"is_me,omitempty"`
}

// Conversation is a chat thread (one-to-one or group).
type Conversation struct {
	ID              string        `json:"conversation_id"`
	Name            string        `json:"name"`
	IsGroup         bool          `json:"is_group"`
	Participants    []Participant `json:"participants"`
	LastMessageTime time.Time     `json:"last_message_time"`
	Unread          bool          `json:"unread"`
	ReadOnly        bool          `json:"read_only,omitempty"`
}

// Attachment describes media on a message. Media is never downloaded.
type Attachment struct {
	Type     string `json:"type"` // image, video, audio, file, location
	MimeType string `json:"mime_type,omitempty"`
	Name     string `json:"name,omitempty"`
	Size     int64  `json:"size_bytes,omitempty"`
}

// Reaction is one emoji and who used it.
type Reaction struct {
	Emoji string   `json:"emoji"`
	Count int      `json:"count"`
	By    []string `json:"by,omitempty"`
}

// Message is one message in a conversation.
type Message struct {
	ID             string       `json:"message_id"`
	ConversationID string       `json:"conversation_id"`
	SenderID       string       `json:"sender_id,omitempty"`
	SenderName     string       `json:"sender_name"`
	FromMe         bool         `json:"from_me"`
	Time           time.Time    `json:"time"`
	Text           string       `json:"text"`
	Attachments    []Attachment `json:"attachments,omitempty"`
	Reactions      []Reaction   `json:"reactions,omitempty"`
	ReplyToID      string       `json:"reply_to_message_id,omitempty"`
	Status         string       `json:"status,omitempty"`
	System         bool         `json:"system,omitempty"`
}

// Contact is an entry from the phone's contact list.
type Contact struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

// Cursor marks a position in a conversation's history for paging backwards.
type Cursor struct {
	MessageID string
	Time      time.Time
}
