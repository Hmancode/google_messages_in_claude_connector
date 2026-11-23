package gm

import (
	"strings"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
)

func participantName(p *gmproto.Participant) string {
	for _, s := range []string{p.GetFullName(), p.GetFirstName(), p.GetFormattedNumber(), p.GetID().GetNumber()} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return "Unknown"
}

func convertConversation(conv *gmproto.Conversation) model.Conversation {
	out := model.Conversation{
		ID:              conv.GetConversationID(),
		Name:            strings.TrimSpace(conv.GetName()),
		IsGroup:         conv.GetIsGroupChat(),
		LastMessageTime: time.UnixMicro(conv.GetLastMessageTimestamp()).UTC(),
		Unread:          conv.GetUnread(),
		ReadOnly:        conv.GetReadOnly(),
	}
	var others []string
	for _, p := range conv.GetParticipants() {
		if !p.GetIsVisible() && !p.GetIsMe() {
			continue
		}
		phone := p.GetFormattedNumber()
		if phone == "" {
			phone = p.GetID().GetNumber()
		}
		mp := model.Participant{
			ID:        p.GetID().GetParticipantID(),
			Name:      participantName(p),
			Phone:     phone,
			IsMe:      p.GetIsMe(),
			IsContact: p.GetContactID() != "",
		}
		if mp.IsMe {
			mp.Name = "Me"
		} else {
			others = append(others, mp.Name)
		}
		out.Participants = append(out.Participants, mp)
	}
	if out.Name == "" {
		out.Name = strings.Join(others, ", ")
	}
	return out
}

func statusText(t gmproto.MessageStatusType) (status string, fromMe, system bool) {
	n := int32(t)
	name := strings.ToLower(t.String())
	switch {
	case n >= 200:
		return strings.TrimPrefix(name, "tombstone_"), false, true
	case n >= 100:
		s := strings.TrimPrefix(name, "incoming_")
		if s == "complete" || s == "delivered" || s == "displayed" {
			s = "received"
		}
		return s, false, false
	case n >= 1:
		return strings.TrimPrefix(name, "outgoing_"), true, false
	}
	return "unknown", false, false
}

func attachmentType(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	}
	return "file"
}

// convertMessage turns a libgm message into a plain message. Media is
// described but never downloaded.
func (c *Client) convertMessage(m *gmproto.Message) model.Message {
	convID := m.GetConversationID()
	status, fromMe, system := statusText(m.GetMessageStatus().GetStatus())
	out := model.Message{
		ID:             m.GetMessageID(),
		ConversationID: convID,
		SenderID:       m.GetParticipantID(),
		Time:           time.UnixMicro(m.GetTimestamp()).UTC(),
		Status:         status,
		FromMe:         fromMe || c.isSelf(m.GetParticipantID()),
		System:         system,
		ReplyToID:      m.GetReplyMessage().GetMessageID(),
	}
	var texts []string
	if subj := strings.TrimSpace(m.GetSubject()); subj != "" {
		texts = append(texts, subj)
	}
	for _, info := range m.GetMessageInfo() {
		switch d := info.GetData().(type) {
		case *gmproto.MessageInfo_MessageContent:
			if t := d.MessageContent.GetContent(); t != "" {
				texts = append(texts, t)
			}
		case *gmproto.MessageInfo_MediaContent:
			mc := d.MediaContent
			out.Attachments = append(out.Attachments, model.Attachment{
				Type:     attachmentType(mc.GetMimeType()),
				MimeType: mc.GetMimeType(),
				Name:     mc.GetMediaName(),
				Size:     mc.GetSize(),
			})
		case *gmproto.MessageInfo_LocationContent:
			out.Attachments = append(out.Attachments, model.Attachment{Type: "location"})
		}
	}
	out.Text = strings.Join(texts, "\n")
	if system && out.Text == "" {
		out.Text = "[" + strings.ReplaceAll(status, "_", " ") + "]"
	}

	switch {
	case out.FromMe:
		out.SenderName = "Me"
	case m.GetSenderParticipant() != nil && participantName(m.GetSenderParticipant()) != "Unknown":
		out.SenderName = participantName(m.GetSenderParticipant())
	default:
		out.SenderName = c.nameFor(convID, out.SenderID)
	}
	if out.SenderName == "" {
		if system {
			out.SenderName = "System"
		} else {
			out.SenderName = "Unknown"
		}
	}

	for _, r := range m.GetReactions() {
		emoji := r.GetData().GetUnicode()
		if emoji == "" {
			continue
		}
		rx := model.Reaction{Emoji: emoji, Count: len(r.GetParticipantIDs())}
		for _, pid := range r.GetParticipantIDs() {
			if c.isSelf(pid) {
				rx.By = append(rx.By, "Me")
			} else if n := c.nameFor(convID, pid); n != "" {
				rx.By = append(rx.By, n)
			}
		}
		out.Reactions = append(out.Reactions, rx)
	}
	return out
}
