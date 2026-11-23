// Package syncer connects to the phone, keeps the local cache current, and
// answers the questions the MCP tools ask.
package syncer

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/confirm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/gm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/secure"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/store"
)

// PairingLifetime is how long Google keeps an idle pairing.
const PairingLifetime = 14 * 24 * time.Hour

const pageSize = 50

// Options tune the background sync.
type Options struct {
	Conversations int           // recent conversations to keep synced
	History       int           // messages per conversation to fetch on first sync
	RefreshEvery  time.Duration // how often to look for changes
	LiveTimeout   time.Duration // how long a live request may take
}

func (o *Options) defaults() {
	if o.Conversations <= 0 {
		o.Conversations = 50
	}
	if o.History <= 0 {
		o.History = 100
	}
	if o.RefreshEvery <= 0 {
		o.RefreshEvery = 10 * time.Minute
	}
	if o.LiveTimeout <= 0 {
		o.LiveTimeout = 30 * time.Second
	}
}

// client is what the syncer needs from gm.Client (an interface for tests).
type client interface {
	Start(ctx context.Context) error
	Close()
	Status() gm.Status
	AuthJSON() ([]byte, error)
	Conversations(ctx context.Context, limit int) ([]model.Conversation, error)
	Messages(ctx context.Context, convID string, limit int, before *model.Cursor) ([]model.Message, error)
	Contacts(ctx context.Context) ([]model.Contact, error)
}

// Syncer owns the connection and the cache.
type Syncer struct {
	log, libgmLog zerolog.Logger
	sess          *session.Store
	db            *store.Store
	opts          Options

	mu           sync.RWMutex
	client       client
	writer       *gm.Writer
	settings     session.Settings
	paired       bool
	loadErr      error
	pairedAt     time.Time
	lastContact  time.Time
	contactSaved time.Time
	lastSync     time.Time
	syncErr      string
	status       gm.Status
	contacts     []model.Contact
	contactsAt   time.Time

	syncing atomic.Bool
	kick    chan struct{}
}

// New creates a Syncer. Call Load, then Run.
func New(log, libgmLog zerolog.Logger, sess *session.Store, db *store.Store, opts Options) *Syncer {
	opts.defaults()
	return &Syncer{log: log, libgmLog: libgmLog, sess: sess, db: db, opts: opts, kick: make(chan struct{}, 1)}
}

// Load reads the saved session and prepares the client. No network traffic.
func (s *Syncer) Load() error {
	f, err := s.sess.Load()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.loadErr = err
		return err
	}
	c, err := gm.New(f.Auth, s.libgmLog, s.log, s)
	if err != nil {
		s.loadErr = err
		return err
	}
	s.client = c
	s.paired = true
	s.settings = f.Settings
	s.pairedAt, s.lastContact, s.contactSaved = f.PairedAt, f.LastContact, f.LastContact
	if f.Settings.AnyWrite() {
		s.writer = gm.NewWriter(c, gm.Policy{
			AllowSend:        f.Settings.AllowSend,
			AllowReact:       f.Settings.AllowReact,
			AllowAttachments: f.Settings.AllowAttachments,
			AllowNewChats:    f.Settings.AllowNewChats,
		}, confirm.New(f.Settings.Mode()))
	}
	return nil
}

// Settings returns the loaded settings.
func (s *Syncer) Settings() session.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Writer returns the write-action handler, or nil if all writes are off.
func (s *Syncer) Writer() *gm.Writer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writer
}

func (s *Syncer) getClient() client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// Run connects and keeps the cache fresh until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	c := s.getClient()
	if c == nil {
		return
	}
	defer c.Close()
	backoff := 5 * time.Second
	for {
		err := c.Start(ctx)
		if err == nil {
			break
		}
		s.log.Warn().Err(err).Dur("retry_in", backoff).Msg("Connect failed")
		s.setSyncErr("could not connect: " + err.Error())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Minute {
			backoff *= 2
		}
	}
	ticker := time.NewTicker(s.opts.RefreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st := c.Status()
			if !st.Connected && !st.Unpaired && !st.LoggedOut && strings.HasPrefix(st.LastError, "connection stopped") {
				s.log.Info().Msg("Reconnecting after the connection stopped")
				if err := c.Start(ctx); err != nil {
					s.setSyncErr("reconnect failed: " + err.Error())
				}
			}
			go s.Sync(ctx)
		case <-s.kick:
			go s.Sync(ctx)
		}
	}
}

func (s *Syncer) setSyncErr(msg string) {
	s.mu.Lock()
	s.syncErr = msg
	s.mu.Unlock()
}

// Sync refreshes the conversation list and fetches new messages.
func (s *Syncer) Sync(ctx context.Context) {
	if !s.syncing.CompareAndSwap(false, true) {
		return
	}
	defer s.syncing.Store(false)
	c := s.getClient()
	if c == nil {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, s.opts.LiveTimeout)
	convs, err := c.Conversations(lctx, s.opts.Conversations)
	cancel()
	if err != nil {
		s.log.Warn().Err(err).Msg("Conversation list sync failed")
		s.setSyncErr("sync failed: " + err.Error())
		return
	}
	fetched := 0
	for _, conv := range convs {
		if ctx.Err() != nil {
			return
		}
		if err := s.db.UpsertConversation(conv); err != nil {
			s.log.Warn().Err(err).Msg("Cache write failed")
			continue
		}
		latest, _ := s.db.LatestMessageTime(conv.ID)
		count, _ := s.db.MessageCount(conv.ID)
		want := 0
		switch {
		case count == 0:
			want = s.opts.History
		case conv.LastMessageTime.After(latest):
			want = pageSize
		}
		if want == 0 {
			continue
		}
		if _, err := s.fetchPages(ctx, c, conv.ID, want, nil); err != nil {
			s.log.Warn().Err(err).Str("conversation_id", conv.ID).Msg("Message sync failed")
			continue
		}
		fetched++
		time.Sleep(200 * time.Millisecond) // be gentle with the phone
	}
	s.mu.Lock()
	s.lastSync, s.syncErr = time.Now(), ""
	s.mu.Unlock()
	s.log.Info().Int("conversations", len(convs)).Int("fetched", fetched).Msg("Sync finished")
}

// fetchPages fetches up to want messages, paging backwards from cursor, and
// stores them. It returns the oldest cursor reached.
func (s *Syncer) fetchPages(ctx context.Context, c client, convID string, want int, cursor *model.Cursor) (*model.Cursor, error) {
	got := 0
	for got < want {
		n := min(pageSize, want-got)
		lctx, cancel := context.WithTimeout(ctx, s.opts.LiveTimeout)
		msgs, err := c.Messages(lctx, convID, n, cursor)
		cancel()
		if err != nil {
			return cursor, err
		}
		if err := s.db.UpsertMessages(msgs); err != nil {
			return cursor, err
		}
		if len(msgs) == 0 {
			break
		}
		oldest := msgs[0]
		if cursor != nil && (oldest.ID == cursor.MessageID || !oldest.Time.Before(cursor.Time)) {
			break // no progress: reached the start of the conversation
		}
		got += len(msgs)
		cursor = &model.Cursor{MessageID: oldest.ID, Time: oldest.Time}
		// The phone often returns slightly fewer messages than asked for, so a
		// short page does not mean the start of the conversation; only an
		// empty page or no progress does.
	}
	return cursor, nil
}

// --- gm.Events ---

// Conversation stores a conversation update.
func (s *Syncer) Conversation(c model.Conversation) {
	if err := s.db.UpsertConversation(c); err != nil {
		s.log.Warn().Err(err).Msg("Cache write failed")
	}
}

// Message stores an incoming message.
func (s *Syncer) Message(m model.Message) {
	if err := s.db.UpsertMessages([]model.Message{m}); err != nil {
		s.log.Warn().Err(err).Msg("Cache write failed")
	}
}

// Ready starts a sync after connecting.
func (s *Syncer) Ready() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// SessionChanged saves refreshed credentials.
func (s *Syncer) SessionChanged() {
	c := s.getClient()
	if c == nil {
		return
	}
	auth, err := c.AuthJSON()
	if err != nil {
		return
	}
	if err := s.sess.Update(func(f *session.File) error { f.Auth = auth; return nil }); err != nil {
		s.log.Warn().Err(err).Msg("Could not save refreshed session")
	}
}

// StatusChanged records connection status and the last contact time.
func (s *Syncer) StatusChanged(st gm.Status) {
	s.mu.Lock()
	s.status = st
	save := false
	if st.LastData.After(s.lastContact) {
		s.lastContact = st.LastData
		if time.Since(s.contactSaved) > time.Hour {
			s.contactSaved = st.LastData
			save = true
		}
	}
	t := s.lastContact
	s.mu.Unlock()
	if save {
		go func() {
			_ = s.sess.Update(func(f *session.File) error { f.LastContact = t; return nil })
		}()
	}
}

// --- queries used by the MCP tools ---

// ErrNotPaired is returned by queries when there is no pairing.
var ErrNotPaired = errors.New("gm-reader is not paired with a phone yet. The user needs to run `gm-reader pair` on their computer")

func (s *Syncer) live() (client, bool) {
	c := s.getClient()
	if c == nil {
		return nil, false
	}
	st := c.Status()
	return c, st.Connected && !st.Unpaired && !st.LoggedOut
}

// ListConversations returns recent conversations. live reports whether the
// phone was reached; otherwise the answer comes from the cache.
func (s *Syncer) ListConversations(ctx context.Context, limit int, onlyGroups bool) ([]model.Conversation, bool, error) {
	c, live := s.live()
	if c == nil {
		return nil, false, ErrNotPaired
	}
	if live {
		lctx, cancel := context.WithTimeout(ctx, s.opts.LiveTimeout)
		convs, err := c.Conversations(lctx, min(max(limit*2, s.opts.Conversations), 100))
		cancel()
		if err == nil {
			for _, conv := range convs {
				_ = s.db.UpsertConversation(conv)
			}
		} else {
			live = false
		}
	}
	convs, err := s.db.Conversations(limit, onlyGroups)
	return convs, live, err
}

// ReadMessages returns up to limit messages older than before (or the newest
// if before is zero), oldest first.
func (s *Syncer) ReadMessages(ctx context.Context, convID string, limit int, before time.Time) ([]model.Message, bool, error) {
	c, live := s.live()
	if c == nil {
		return nil, false, ErrNotPaired
	}
	if live {
		var err error
		if before.IsZero() {
			_, err = s.fetchPages(ctx, c, convID, limit, nil)
		} else {
			err = s.fetchBefore(ctx, c, convID, limit, before)
		}
		if err != nil {
			s.log.Warn().Err(err).Str("conversation_id", convID).Msg("Live read failed, using cache")
			live = false
		}
	}
	msgs, err := s.db.Messages(convID, limit, before)
	return msgs, live, err
}

// maxWalkPages limits how far back fetchBefore walks to reach an old date.
const maxWalkPages = 20

// fetchBefore makes sure the cache holds the limit messages just before
// `before`. It starts from the oldest cached message at or after `before`
// (or from the newest message), walks back page by page until it passes
// `before`, then fetches limit more.
func (s *Syncer) fetchBefore(ctx context.Context, c client, convID string, limit int, before time.Time) error {
	var cur *model.Cursor
	if cc, ok, _ := s.db.CursorAt(convID, before); ok {
		cur = &cc
	}
	for i := 0; i < maxWalkPages && (cur == nil || !cur.Time.Before(before)); i++ {
		next, err := s.fetchPages(ctx, c, convID, pageSize, cur)
		if err != nil {
			return err
		}
		if next == nil || (cur != nil && next.MessageID == cur.MessageID) {
			return nil // reached the start of the conversation
		}
		cur = next
	}
	if cur == nil || !cur.Time.Before(before) {
		return nil // too far back; answer with what we have
	}
	_, err := s.fetchPages(ctx, c, convID, limit, cur)
	return err
}

// Search searches cached messages.
func (s *Syncer) Search(query, convID string, since time.Time, limit int) ([]store.Hit, error) {
	if s.getClient() == nil {
		return nil, ErrNotPaired
	}
	return s.db.Search(query, convID, since, limit)
}

// Contacts searches the phone's contacts by name or number.
func (s *Syncer) Contacts(ctx context.Context, query string, limit int) ([]model.Contact, error) {
	c, live := s.live()
	if c == nil {
		return nil, ErrNotPaired
	}
	s.mu.RLock()
	cached, at := s.contacts, s.contactsAt
	s.mu.RUnlock()
	if live && time.Since(at) > 10*time.Minute {
		lctx, cancel := context.WithTimeout(ctx, s.opts.LiveTimeout)
		list, err := c.Contacts(lctx)
		cancel()
		if err == nil {
			s.mu.Lock()
			s.contacts, s.contactsAt = list, time.Now()
			s.mu.Unlock()
			cached = list
		}
	}
	if cached == nil && !live {
		return nil, errors.New("contacts need a live connection to the phone, which is not available right now")
	}
	q := strings.ToLower(strings.TrimSpace(query))
	digits := onlyDigits(q)
	out := []model.Contact{}
	for _, ct := range cached {
		if strings.Contains(strings.ToLower(ct.Name), q) || (digits != "" && strings.Contains(onlyDigits(ct.Phone), digits)) {
			out = append(out, ct)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Report is what pairing_status returns.
type Report struct {
	Paired              bool       `json:"paired"`
	Connected           bool       `json:"connected"`
	PhoneResponding     bool       `json:"phone_responding"`
	ActiveElsewhere     bool       `json:"messages_for_web_open_elsewhere"`
	PairedAt            *time.Time `json:"paired_at,omitempty"`
	LastContact         *time.Time `json:"last_contact_with_phone,omitempty"`
	LastSync            *time.Time `json:"last_sync,omitempty"`
	DaysUntilRepair     *int       `json:"days_until_repair_needed,omitempty"`
	CachedConversations int        `json:"cached_conversations"`
	CachedMessages      int        `json:"cached_messages"`
	CacheMode           string     `json:"cache_mode"`
	SessionEncrypted    bool       `json:"session_file_encrypted"`
	WriteActions        []string   `json:"write_actions_enabled"`
	ConfirmMode         string     `json:"confirmation_mode"`
	Warnings            []string   `json:"warnings"`
}

// StatusReport summarizes pairing and sync health.
func (s *Syncer) StatusReport() Report {
	s.mu.RLock()
	r := Report{
		Paired:           s.paired,
		SessionEncrypted: secure.Encrypted,
		ConfirmMode:      s.settings.Mode(),
		WriteActions:     []string{},
		Warnings:         []string{},
	}
	st := s.status
	c := s.client
	lastContact, lastSync, pairedAt, syncErr, loadErr := s.lastContact, s.lastSync, s.pairedAt, s.syncErr, s.loadErr
	settings := s.settings
	s.mu.RUnlock()
	if c != nil {
		st = c.Status()
	}
	r.Connected, r.PhoneResponding, r.ActiveElsewhere = st.Connected, st.PhoneResponding, st.ActiveElsewhere
	tp := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		l := t.Local()
		return &l
	}
	r.PairedAt, r.LastContact, r.LastSync = tp(pairedAt), tp(lastContact), tp(lastSync)
	r.CachedConversations, r.CachedMessages, _ = s.db.Totals()
	if s.db.InMemory() {
		r.CacheMode = "memory only (nothing written to disk)"
	} else {
		r.CacheMode = "on disk, not encrypted by gm-reader (protected only if the drive uses BitLocker)"
	}
	for _, a := range []struct {
		on   bool
		name string
	}{{settings.AllowSend, "send"}, {settings.AllowReact, "react"}, {settings.AllowAttachments, "attachments"}, {settings.AllowNewChats, "new-chats"}} {
		if a.on {
			r.WriteActions = append(r.WriteActions, a.name)
		}
	}

	if !r.Paired {
		msg := "Not paired. Run `gm-reader pair` on the computer."
		if loadErr != nil && !errors.Is(loadErr, session.ErrNotPaired) {
			msg = "Could not load the saved pairing (" + loadErr.Error() + "). Run `gm-reader pair` again."
		}
		r.Warnings = append(r.Warnings, msg)
		return r
	}
	ref := lastContact
	if ref.IsZero() {
		ref = pairedAt
	}
	if !ref.IsZero() {
		days := int((PairingLifetime - time.Since(ref)).Hours() / 24)
		if days < 0 {
			days = 0
		}
		r.DaysUntilRepair = &days
		if days <= 4 {
			r.Warnings = append(r.Warnings, "The pairing may expire soon: Google drops it after about 14 days without contact. Keep the phone online, and open Claude so gm-reader can connect.")
		}
	}
	switch {
	case st.Unpaired:
		r.Warnings = append(r.Warnings, "Google or the phone removed this pairing. Run `gm-reader pair` again.")
	case st.LoggedOut:
		r.Warnings = append(r.Warnings, "The Google sign-in cookies stopped working. Run `gm-reader pair --refresh-cookies`.")
	case !st.Connected:
		r.Warnings = append(r.Warnings, "Not connected right now; answers come from the cache.")
	}
	if st.Connected && !st.PhoneResponding {
		r.Warnings = append(r.Warnings, "The phone is not responding. It must be on and online.")
	}
	if st.ActiveElsewhere {
		r.Warnings = append(r.Warnings, "Google Messages for Web is open somewhere else (for example a browser tab), so live updates are paused. Close it and restart Claude to resume.")
	}
	if syncErr != "" {
		r.Warnings = append(r.Warnings, syncErr)
	}
	if st.LastError != "" && st.LastError != syncErr {
		r.Warnings = append(r.Warnings, st.LastError)
	}
	if !secure.Encrypted {
		r.Warnings = append(r.Warnings, "The session file is not encrypted on this operating system.")
	}
	return r
}
