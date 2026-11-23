// Package session stores the pairing credential and user settings in one
// encrypted file (see package secure). The write-action switches live here,
// inside the encrypted file, so they cannot be flipped by editing a plain
// text config.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/paths"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/secure"
)

// ErrNotPaired means there is no session file yet.
var ErrNotPaired = errors.New("not paired: run `gm-reader pair` first")

// Confirmation modes for write actions.
const (
	// ConfirmDialog shows a Windows Yes/No dialog on this computer before each
	// message send, attachment, or new chat. This is the default.
	ConfirmDialog = "dialog"
	// ConfirmClaudeApp relies only on the Claude app's own approval prompt.
	// Meant for a future headless host where no dialog can be shown.
	ConfirmClaudeApp = "claude-app"
)

// Settings are the user's choices about what Claude may do.
type Settings struct {
	AllowSend        bool   `json:"allow_send"`
	AllowReact       bool   `json:"allow_react"`
	AllowAttachments bool   `json:"allow_attachments"`
	AllowNewChats    bool   `json:"allow_new_chats"`
	ConfirmMode      string `json:"confirm_mode"`
}

// AnyWrite reports whether any write action is turned on.
func (s Settings) AnyWrite() bool {
	return s.AllowSend || s.AllowReact || s.AllowAttachments || s.AllowNewChats
}

// Mode returns the confirmation mode, defaulting to the dialog.
func (s Settings) Mode() string {
	if s.ConfirmMode == ConfirmClaudeApp {
		return ConfirmClaudeApp
	}
	return ConfirmDialog
}

// File is the decrypted content of the session file.
type File struct {
	Version  int             `json:"version"`
	Auth     json.RawMessage `json:"auth"` // libgm AuthData, opaque outside internal/gm
	Settings Settings        `json:"settings"`
	PairedAt time.Time       `json:"paired_at"`
	// LastContact is the last time data arrived from the phone. Google drops
	// the pairing after about 14 days without contact.
	LastContact time.Time `json:"last_contact"`
}

// Store reads and writes the session file. Safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
}

// Open returns a Store for the default session path.
func Open() (*Store, error) {
	p, err := paths.SessionFile()
	if err != nil {
		return nil, err
	}
	return &Store{path: p}, nil
}

// OpenAt returns a Store for a specific path (tests).
func OpenAt(path string) *Store { return &Store{path: path} }

// Path returns the session file location.
func (s *Store) Path() string { return s.path }

// Load reads and decrypts the session.
func (s *Store) Load() (*File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (*File, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotPaired
	} else if err != nil {
		return nil, err
	}
	plain, err := secure.Open(raw)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(plain, &f); err != nil {
		return nil, fmt.Errorf("session file is corrupted: %w", err)
	}
	if len(f.Auth) == 0 {
		return nil, ErrNotPaired
	}
	return &f, nil
}

// Save encrypts and writes the session atomically.
func (s *Store) Save(f *File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(f)
}

func (s *Store) saveLocked(f *File) error {
	f.Version = 1
	plain, err := json.Marshal(f)
	if err != nil {
		return err
	}
	sealed, err := secure.Seal(plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Update loads the session, applies fn, and saves it, all under one lock.
func (s *Store) Update(fn func(*File) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		return err
	}
	return s.saveLocked(f)
}

// Delete removes the session file.
func (s *Store) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
