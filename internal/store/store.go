// Package store is the local SQLite cache of conversations and messages,
// with an FTS5 full-text index for search.
//
// The cache file is not encrypted by gm-reader. On Windows it is protected
// only if the drive uses BitLocker (or similar). `serve --no-cache` keeps the
// cache in memory instead, so nothing is written to disk.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
	_ "github.com/ncruces/go-sqlite3/vfs/memdb"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/model"
)

const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS conversations (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	is_group INTEGER NOT NULL DEFAULT 0,
	unread INTEGER NOT NULL DEFAULT 0,
	read_only INTEGER NOT NULL DEFAULT 0,
	last_message_ts INTEGER NOT NULL DEFAULT 0,
	participants_json TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS messages (
	rid INTEGER PRIMARY KEY,
	id TEXT NOT NULL UNIQUE,
	conversation_id TEXT NOT NULL,
	sender_id TEXT NOT NULL DEFAULT '',
	sender_name TEXT NOT NULL DEFAULT '',
	from_me INTEGER NOT NULL DEFAULT 0,
	ts INTEGER NOT NULL,
	text TEXT NOT NULL DEFAULT '',
	attachments_json TEXT NOT NULL DEFAULT '',
	reactions_json TEXT NOT NULL DEFAULT '',
	reply_to_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	system INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS messages_conv_ts ON messages(conversation_id, ts);
CREATE INDEX IF NOT EXISTS messages_ts ON messages(ts);
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
	text, sender_name, content='messages', content_rowid='rid',
	tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
	INSERT INTO messages_fts(rowid, text, sender_name) VALUES (new.rid, new.text, new.sender_name);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
	INSERT INTO messages_fts(messages_fts, rowid, text, sender_name) VALUES ('delete', old.rid, old.text, old.sender_name);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
	INSERT INTO messages_fts(messages_fts, rowid, text, sender_name) VALUES ('delete', old.rid, old.text, old.sender_name);
	INSERT INTO messages_fts(rowid, text, sender_name) VALUES (new.rid, new.text, new.sender_name);
END;
`

// Store is the cache.
type Store struct {
	db       *sql.DB
	inMemory bool
}

// Open opens the cache at path. An empty path keeps the cache in memory only.
func Open(path string) (*Store, error) {
	inMemory := path == ""
	dsn := path
	if inMemory {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		dsn = "file:/gm-reader-" + hex.EncodeToString(b) + ".db?vfs=memdb"
	}
	db, err := driver.Open(dsn, func(c *sqlite3.Conn) error {
		if err := fts5.Register(c); err != nil {
			return err
		}
		pragmas := "PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;"
		if !inMemory {
			pragmas += " PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;"
		}
		return c.Exec(pragmas)
	})
	if err != nil {
		return nil, fmt.Errorf("cannot open cache: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, inMemory: inMemory}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// InMemory reports whether the cache lives only in memory.
func (s *Store) InMemory() bool { return s.inMemory }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("cache was made by a newer gm-reader (schema %d); delete cache.db to rebuild it", v)
	}
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("cannot create cache tables: %w", err)
	}
	_, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion))
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	if string(b) == "null" {
		return ""
	}
	return string(b)
}

// UpsertConversation inserts or updates a conversation.
func (s *Store) UpsertConversation(c model.Conversation) error {
	if c.ID == "" {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO conversations (id, name, is_group, unread, read_only, last_message_ts, participants_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, is_group=excluded.is_group, unread=excluded.unread,
			read_only=excluded.read_only,
			last_message_ts=max(conversations.last_message_ts, excluded.last_message_ts),
			participants_json=excluded.participants_json`,
		c.ID, c.Name, b2i(c.IsGroup), b2i(c.Unread), b2i(c.ReadOnly), c.LastMessageTime.UnixMicro(), toJSON(c.Participants))
	return err
}

// UpsertMessages inserts or updates messages in one transaction.
func (s *Store) UpsertMessages(msgs []model.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO messages (id, conversation_id, sender_id, sender_name, from_me, ts, text,
			attachments_json, reactions_json, reply_to_id, status, system)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			sender_name=excluded.sender_name, from_me=excluded.from_me, ts=excluded.ts,
			text=excluded.text, attachments_json=excluded.attachments_json,
			reactions_json=excluded.reactions_json, reply_to_id=excluded.reply_to_id,
			status=excluded.status, system=excluded.system`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range msgs {
		if m.ID == "" || m.ConversationID == "" {
			continue
		}
		if _, err := stmt.Exec(m.ID, m.ConversationID, m.SenderID, m.SenderName, b2i(m.FromMe), m.Time.UnixMicro(),
			m.Text, toJSON(m.Attachments), toJSON(m.Reactions), m.ReplyToID, m.Status, b2i(m.System)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanConversation(row interface{ Scan(...any) error }) (model.Conversation, error) {
	var c model.Conversation
	var isGroup, unread, ro int
	var ts int64
	var parts string
	if err := row.Scan(&c.ID, &c.Name, &isGroup, &unread, &ro, &ts, &parts); err != nil {
		return c, err
	}
	c.IsGroup, c.Unread, c.ReadOnly = isGroup == 1, unread == 1, ro == 1
	c.LastMessageTime = time.UnixMicro(ts).UTC()
	if parts != "" {
		_ = json.Unmarshal([]byte(parts), &c.Participants)
	}
	return c, nil
}

const convCols = `id, name, is_group, unread, read_only, last_message_ts, participants_json`

// Conversations lists cached conversations, most recent first.
func (s *Store) Conversations(limit int, onlyGroups bool) ([]model.Conversation, error) {
	q := `SELECT ` + convCols + ` FROM conversations`
	if onlyGroups {
		q += ` WHERE is_group = 1`
	}
	q += ` ORDER BY last_message_ts DESC LIMIT ?`
	rows, err := s.db.Query(q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Conversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Conversation returns one cached conversation.
func (s *Store) Conversation(id string) (model.Conversation, bool, error) {
	c, err := scanConversation(s.db.QueryRow(`SELECT `+convCols+` FROM conversations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

const msgCols = `id, conversation_id, sender_id, sender_name, from_me, ts, text, attachments_json, reactions_json, reply_to_id, status, system`

func scanMessage(row interface{ Scan(...any) error }, extra ...any) (model.Message, error) {
	var m model.Message
	var fromMe, system int
	var ts int64
	var att, rx string
	dest := append([]any{&m.ID, &m.ConversationID, &m.SenderID, &m.SenderName, &fromMe, &ts, &m.Text, &att, &rx, &m.ReplyToID, &m.Status, &system}, extra...)
	if err := row.Scan(dest...); err != nil {
		return m, err
	}
	m.FromMe, m.System = fromMe == 1, system == 1
	m.Time = time.UnixMicro(ts).UTC()
	if att != "" {
		_ = json.Unmarshal([]byte(att), &m.Attachments)
	}
	if rx != "" {
		_ = json.Unmarshal([]byte(rx), &m.Reactions)
	}
	return m, nil
}

// Messages returns up to limit cached messages in a conversation that are
// older than before (or the newest ones if before is zero), oldest first.
func (s *Store) Messages(convID string, limit int, before time.Time) ([]model.Message, error) {
	bound := int64(1<<62 - 1)
	if !before.IsZero() {
		bound = before.UnixMicro()
	}
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages WHERE conversation_id = ? AND ts < ? ORDER BY ts DESC LIMIT ?`,
		convID, bound, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// CursorAt returns the oldest cached message at or after t, which can be used
// to fetch older messages from the phone.
func (s *Store) CursorAt(convID string, t time.Time) (model.Cursor, bool, error) {
	var id string
	var ts int64
	err := s.db.QueryRow(`SELECT id, ts FROM messages WHERE conversation_id = ? AND ts >= ? ORDER BY ts ASC LIMIT 1`,
		convID, t.UnixMicro()).Scan(&id, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Cursor{}, false, nil
	} else if err != nil {
		return model.Cursor{}, false, err
	}
	return model.Cursor{MessageID: id, Time: time.UnixMicro(ts)}, true, nil
}

// OldestCursor returns the oldest cached message in a conversation.
func (s *Store) OldestCursor(convID string) (model.Cursor, bool, error) {
	return s.CursorAt(convID, time.Unix(0, 0))
}

// LatestMessageTime returns the newest cached message time in a conversation.
func (s *Store) LatestMessageTime(convID string) (time.Time, error) {
	var ts sql.NullInt64
	err := s.db.QueryRow(`SELECT max(ts) FROM messages WHERE conversation_id = ?`, convID).Scan(&ts)
	if err != nil || !ts.Valid {
		return time.Time{}, err
	}
	return time.UnixMicro(ts.Int64).UTC(), nil
}

// MessageCount returns the number of cached messages in a conversation.
func (s *Store) MessageCount(convID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM messages WHERE conversation_id = ?`, convID).Scan(&n)
	return n, err
}

// Totals returns the number of cached conversations and messages.
func (s *Store) Totals() (convs, msgs int, err error) {
	if err = s.db.QueryRow(`SELECT count(*) FROM conversations`).Scan(&convs); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&msgs)
	return
}

// Hit is one search result.
type Hit struct {
	model.Message
	ConversationName string `json:"conversation_name"`
	Snippet          string `json:"snippet"`
}

var tokenRe = regexp.MustCompile(`"[^"]+"|\S+`)

// ftsQuery turns free text into a safe FTS5 query: every word or "quoted
// phrase" must appear. FTS5 operators typed by the user are treated as words.
func ftsQuery(q string) string {
	var parts []string
	for _, tok := range tokenRe.FindAllString(q, -1) {
		tok = strings.Trim(tok, `"`)
		hasWord := false
		for _, r := range tok {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				hasWord = true
				break
			}
		}
		if !hasWord {
			continue
		}
		parts = append(parts, `"`+strings.ReplaceAll(tok, `"`, `""`)+`"`)
	}
	return strings.Join(parts, " ")
}

// Search finds cached messages containing all words of query, newest first.
func (s *Store) Search(query, convID string, since time.Time, limit int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is empty")
	}
	var conds []string
	var args []any
	fq := ftsQuery(query)
	useFTS := fq != ""
	var q string
	if useFTS {
		q = `SELECT ` + prefixed("m.", msgCols) + `, coalesce(c.name, ''),
			snippet(messages_fts, 0, '[', ']', '…', 16)
			FROM messages_fts f JOIN messages m ON m.rid = f.rowid
			LEFT JOIN conversations c ON c.id = m.conversation_id`
		conds = append(conds, `messages_fts MATCH ?`)
		args = append(args, fq)
	} else {
		// Emoji or punctuation only: FTS ignores those, so scan with LIKE.
		q = `SELECT ` + prefixed("m.", msgCols) + `, coalesce(c.name, ''), substr(m.text, 1, 200)
			FROM messages m LEFT JOIN conversations c ON c.id = m.conversation_id`
		conds = append(conds, `m.text LIKE ? ESCAPE '\'`)
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
		args = append(args, "%"+esc+"%")
	}
	if convID != "" {
		conds = append(conds, `m.conversation_id = ?`)
		args = append(args, convID)
	}
	if !since.IsZero() {
		conds = append(conds, `m.ts >= ?`)
		args = append(args, since.UnixMicro())
	}
	q += ` WHERE ` + strings.Join(conds, " AND ") + ` ORDER BY m.ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	defer rows.Close()
	out := []Hit{}
	for rows.Next() {
		var h Hit
		m, err := scanMessage(rows, &h.ConversationName, &h.Snippet)
		if err != nil {
			return nil, err
		}
		h.Message = m
		out = append(out, h)
	}
	return out, rows.Err()
}

func prefixed(p, cols string) string {
	fields := strings.Split(cols, ",")
	for i, f := range fields {
		fields[i] = p + strings.TrimSpace(f)
	}
	return strings.Join(fields, ", ")
}
