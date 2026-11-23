package gm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/util/exhttp"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
)

// Status describes the connection to the phone.
type Status struct {
	Connected       bool
	PhoneResponding bool
	// ActiveElsewhere is true when another Messages for Web session (for
	// example a messages.google.com tab) has taken over. gm-reader does not
	// grab the session back on its own.
	ActiveElsewhere bool
	// Unpaired is true when Google or the phone revoked this pairing.
	Unpaired  bool
	LoggedOut bool // cookies expired or were signed out; refresh them
	LastError string
	LastData  time.Time
}

// Events receives updates from the phone. Methods must return quickly.
type Events interface {
	Conversation(model.Conversation)
	Message(model.Message)
	Ready()
	SessionChanged()
	StatusChanged(Status)
}

// Client wraps a libgm client.
type Client struct {
	raw *libgm.Client
	api api
	log zerolog.Logger
	ev  Events

	mu     sync.RWMutex
	status Status
	sims   map[string]*gmproto.SIMCard      // self participant ID -> SIM
	names  map[string]map[string]string     // conversation ID -> participant ID -> name
	convs  map[string]*gmproto.Conversation // latest known conversation data
}

// LibgmLogger builds the logger handed to libgm. Its output passes through a
// filter that removes raw protocol data and response bodies.
func LibgmLogger(w io.Writer, debug bool) zerolog.Logger {
	level := zerolog.WarnLevel
	if debug {
		level = zerolog.DebugLevel
	}
	return zerolog.New(redactingWriter{w}).Level(level).With().Timestamp().Str("component", "libgm").Logger()
}

// New creates a client from a saved session. It does not connect.
func New(authJSON []byte, libgmLog, log zerolog.Logger, ev Events) (*Client, error) {
	ad := libgm.NewAuthData()
	if err := json.Unmarshal(authJSON, ad); err != nil {
		return nil, fmt.Errorf("saved session is unreadable: %w", err)
	}
	raw := libgm.NewClient(ad, nil, libgmLog, exhttp.SensibleClientSettings)
	c := newWithAPI(raw, log, ev)
	c.raw = raw
	if !raw.IsLoggedIn() {
		return nil, errors.New("saved session is incomplete; run `gm-reader pair` again")
	}
	return c, nil
}

func newWithAPI(a api, log zerolog.Logger, ev Events) *Client {
	c := &Client{
		api:   a,
		log:   log,
		ev:    ev,
		sims:  map[string]*gmproto.SIMCard{},
		names: map[string]map[string]string{},
		convs: map[string]*gmproto.Conversation{},
	}
	a.SetEventHandler(c.handle)
	return c
}

// AuthJSON returns the current session credential for saving.
func (c *Client) AuthJSON() ([]byte, error) {
	if c.raw == nil {
		return nil, errors.New("no session")
	}
	return json.Marshal(c.raw.AuthData)
}

// Start connects to Google's relay and begins receiving updates.
func (c *Client) Start(ctx context.Context) error {
	return c.api.Connect(ctx)
}

// Close disconnects.
func (c *Client) Close() {
	c.api.Disconnect()
	c.setStatus(func(s *Status) { s.Connected = false })
}

// Status returns the current connection status.
func (c *Client) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.status
	s.Connected = s.Connected && c.api.IsConnected()
	return s
}

func (c *Client) setStatus(fn func(*Status)) {
	c.mu.Lock()
	fn(&c.status)
	s := c.status
	c.mu.Unlock()
	if c.ev != nil {
		c.ev.StatusChanged(s)
	}
}

func (c *Client) touch() {
	c.setStatus(func(s *Status) {
		s.LastData = time.Now()
		s.PhoneResponding = true
	})
}

// handle receives every libgm event. It must not block or make requests.
func (c *Client) handle(raw any) {
	switch evt := raw.(type) {
	case *events.ClientReady:
		c.setStatus(func(s *Status) {
			s.Connected, s.PhoneResponding, s.LastError, s.LastData = true, true, "", time.Now()
		})
		for _, conv := range evt.Conversations {
			c.ev.Conversation(c.noteConversation(conv))
		}
		go c.ev.Ready()
	case *libgm.WrappedMessage:
		c.touch()
		if evt.Message != nil {
			m := c.convertMessage(evt.Message)
			m.Live = !evt.IsOld
			c.ev.Message(m)
		}
	case *gmproto.Conversation:
		c.touch()
		c.ev.Conversation(c.noteConversation(evt))
	case *gmproto.Settings:
		c.mu.Lock()
		for _, sim := range evt.GetSIMCards() {
			if id := sim.GetSIMParticipant().GetID(); id != "" {
				c.sims[id] = sim
			}
		}
		c.mu.Unlock()
	case *gmproto.UserAlertEvent:
		switch evt.GetAlertType() {
		case gmproto.AlertType_BROWSER_ACTIVE:
			c.setStatus(func(s *Status) { s.ActiveElsewhere = false; s.Connected = true })
		case gmproto.AlertType_BROWSER_INACTIVE,
			gmproto.AlertType_BROWSER_INACTIVE_FROM_TIMEOUT,
			gmproto.AlertType_BROWSER_INACTIVE_FROM_INACTIVITY:
			// Deliberately not calling SetActiveSession to take it back.
			c.setStatus(func(s *Status) { s.ActiveElsewhere = true })
		}
	case *events.AuthTokenRefreshed:
		go c.ev.SessionChanged()
	case *events.ListenFatalError:
		c.setStatus(func(s *Status) {
			s.Connected = false
			s.LastError = "connection stopped: " + evt.Error.Error()
			switch credentialProblem(evt.Error) {
			case problemUnpaired:
				s.Unpaired = true
			case problemLoggedOut:
				s.LoggedOut = true
			}
		})
	case *events.ListenTemporaryError:
		c.setStatus(func(s *Status) { s.LastError = "temporary connection problem: " + evt.Error.Error() })
	case *events.ListenRecovered:
		c.setStatus(func(s *Status) { s.LastError = ""; s.Connected = true })
	case *events.PhoneNotResponding:
		c.setStatus(func(s *Status) { s.PhoneResponding = false })
	case *events.PhoneRespondingAgain:
		c.setStatus(func(s *Status) { s.PhoneResponding = true })
	case *events.PingFailed:
		if errors.Is(evt.Error, events.ErrRequestedEntityNotFound) {
			c.setStatus(func(s *Status) { s.Unpaired = true; s.Connected = false })
		}
	case *gmproto.RevokePairData, *events.GaiaLoggedOut:
		c.setStatus(func(s *Status) { s.Unpaired = true; s.Connected = false })
	case *events.NoDataReceived:
		c.setStatus(func(s *Status) { s.LastError = "no data received from the phone recently" })
	case *events.HackySetActiveMayFail:
		// The bridge re-claims the active session here. gm-reader does not.
	default:
		c.log.Debug().Type("event_type", raw).Msg("Ignoring event")
	}
}

type problem int

const (
	problemNone problem = iota
	problemUnpaired
	problemLoggedOut
)

func credentialProblem(err error) problem {
	var httpErr events.HTTPError
	switch {
	case errors.Is(err, events.ErrRequestedEntityNotFound):
		return problemUnpaired
	case errors.Is(err, events.ErrInvalidCredentials):
		return problemLoggedOut
	case errors.As(err, &httpErr) && httpErr.Action == "polling" && httpErr.Resp != nil && httpErr.Resp.StatusCode == http.StatusUnauthorized:
		return problemLoggedOut
	}
	return problemNone
}

// noteConversation caches participant names and SIM data for a conversation.
func (c *Client) noteConversation(conv *gmproto.Conversation) model.Conversation {
	m := convertConversation(conv)
	names := map[string]string{}
	for _, p := range m.Participants {
		names[p.ID] = p.Name
	}
	c.mu.Lock()
	c.names[conv.GetConversationID()] = names
	c.convs[conv.GetConversationID()] = conv
	c.mu.Unlock()
	return m
}

func (c *Client) nameFor(convID, participantID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.names[convID][participantID]
}

func (c *Client) isSelf(participantID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.sims[participantID]
	return ok
}

// Conversations lists inbox conversations, most recent first.
func (c *Client) Conversations(ctx context.Context, limit int) ([]model.Conversation, error) {
	resp, err := c.api.ListConversations(ctx, &gmproto.ListConversationsRequest{
		Count:  int64(limit),
		Folder: gmproto.ListConversationsRequest_INBOX,
	})
	if err != nil {
		return nil, err
	}
	c.touch()
	out := make([]model.Conversation, 0, len(resp.GetConversations()))
	for _, conv := range resp.GetConversations() {
		out = append(out, c.noteConversation(conv))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastMessageTime.After(out[j].LastMessageTime) })
	return out, nil
}

// Conversation fetches one conversation.
func (c *Client) Conversation(ctx context.Context, id string) (model.Conversation, error) {
	conv, err := c.api.GetConversation(ctx, id)
	if err != nil {
		return model.Conversation{}, err
	}
	c.touch()
	if conv.GetConversationID() == "" {
		return model.Conversation{}, fmt.Errorf("conversation %q not found", id)
	}
	return c.noteConversation(conv), nil
}

// Messages fetches up to limit messages, oldest first. With before set, it
// returns messages older than that point.
func (c *Client) Messages(ctx context.Context, convID string, limit int, before *model.Cursor) ([]model.Message, error) {
	// Learn participant names first so senders can be labeled.
	c.mu.RLock()
	_, known := c.names[convID]
	c.mu.RUnlock()
	if !known {
		if _, err := c.Conversation(ctx, convID); err != nil {
			return nil, err
		}
	}
	var cur *gmproto.Cursor
	if before != nil {
		// The protocol's cursor timestamp is in milliseconds (message
		// timestamps are in microseconds). See mautrix-gmessages backfill.go.
		cur = &gmproto.Cursor{LastItemID: before.MessageID, LastItemTimestamp: before.Time.UnixMilli()}
	}
	resp, err := c.api.FetchMessages(ctx, convID, int64(limit), cur)
	if err != nil {
		return nil, err
	}
	c.touch()
	out := make([]model.Message, 0, len(resp.GetMessages()))
	for _, m := range resp.GetMessages() {
		out = append(out, c.convertMessage(m))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// Contacts returns the phone's contacts.
func (c *Client) Contacts(ctx context.Context) ([]model.Contact, error) {
	resp, err := c.api.ListContacts(ctx)
	if err != nil {
		return nil, err
	}
	c.touch()
	out := make([]model.Contact, 0, len(resp.GetContacts()))
	for _, ct := range resp.GetContacts() {
		phone := ct.GetNumber().GetFormattedNumber()
		if phone == "" {
			phone = ct.GetNumber().GetNumber()
		}
		out = append(out, model.Contact{Name: ct.GetName(), Phone: phone})
	}
	return out, nil
}
