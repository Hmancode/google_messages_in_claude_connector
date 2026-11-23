// Package gm is the only package in gm-reader that talks to libgm.
//
// Everything libgm can do goes through the small api interface below. The
// guard test (internal/guard) parses this repository and fails the build if:
//   - any package other than internal/gm imports libgm, or
//   - any code calls a libgm method that is not on its allowlist, or
//   - a write method (send, react, new chat, upload) is called from any file
//     other than internal/gm/write.go.
//
// Methods that are never allowed include MarkRead, DeleteMessage,
// DeleteConversation, SetTyping, UpdateConversation, UpdateSettings,
// RegisterPush, SetActiveSession and the Unpair family.
package gm

import (
	"context"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// api is the subset of *libgm.Client that gm-reader uses.
type api interface {
	// Connection and pairing.
	SetEventHandler(libgm.EventHandler)
	Connect(ctx context.Context) error
	Disconnect()
	IsConnected() bool
	IsLoggedIn() bool
	FetchConfig(ctx context.Context) error
	StartGaiaPairing(ctx, bgCtx context.Context) (string, *libgm.PairingSession, error)
	FinishGaiaPairing(ctx context.Context, ps *libgm.PairingSession) (string, error)

	// Reading.
	ListConversations(ctx context.Context, req *gmproto.ListConversationsRequest) (*gmproto.ListConversationsResponse, error)
	GetConversation(ctx context.Context, conversationID string) (*gmproto.Conversation, error)
	FetchMessages(ctx context.Context, conversationID string, count int64, cursor *gmproto.Cursor) (*gmproto.ListMessagesResponse, error)
	ListContacts(ctx context.Context) (*gmproto.ListContactsResponse, error)

	// Writing. Only internal/gm/write.go may call these, and only after the
	// user's settings allow it and (for sends, attachments and new chats) the
	// user has confirmed in the dialog.
	SendMessage(ctx context.Context, req *gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error)
	SendReaction(ctx context.Context, req *gmproto.SendReactionRequest) (*gmproto.SendReactionResponse, error)
	GetOrCreateConversation(ctx context.Context, req *gmproto.GetOrCreateConversationRequest) (*gmproto.GetOrCreateConversationResponse, error)
	UploadMedia(ctx context.Context, data []byte, fileName, mime string) (*gmproto.MediaContent, error)
}

var _ api = (*libgm.Client)(nil)
