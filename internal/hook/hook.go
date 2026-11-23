// Package hook notifies a URL you choose when new texts arrive, so an
// assistant (for example a Claude scheduled task with an API trigger) can
// react right away instead of polling.
//
// Privacy: the request contains only conversation IDs and counts, never
// message text, names or phone numbers. The assistant reads the messages
// itself through gm-reader's tools. The hook is off by default, and it is the
// only network traffic gm-reader sends anywhere other than Google.
package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// DefaultBatch is how long new texts are collected before one request is sent.
const DefaultBatch = 45 * time.Second

// Config is the hook's destination.
type Config struct {
	URL   string
	Token string
	Batch time.Duration
}

// Validate checks the URL is HTTPS (plain HTTP only for localhost testing).
func Validate(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("that is not a valid URL")
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"):
		return nil
	}
	return errors.New("the hook URL must start with https://")
}

// Host returns just the host of a URL, for display without paths or tokens.
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// Dispatcher batches notifications and sends them.
type Dispatcher struct {
	cfg    Config
	client *http.Client
	log    zerolog.Logger

	mu      sync.Mutex
	pending map[string]int
	timer   *time.Timer
	sent    int
	lastErr string
}

// New returns a Dispatcher.
func New(cfg Config, log zerolog.Logger) *Dispatcher {
	if cfg.Batch <= 0 {
		cfg.Batch = DefaultBatch
	}
	return &Dispatcher{cfg: cfg, client: &http.Client{Timeout: 20 * time.Second}, log: log, pending: map[string]int{}}
}

// Notify records a new text in a conversation; a request goes out after the
// batch window.
func (d *Dispatcher) Notify(conversationID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending[conversationID]++
	if d.timer == nil {
		d.timer = time.AfterFunc(d.cfg.Batch, d.flush)
	}
}

// Stats reports how many requests were sent and the last error.
func (d *Dispatcher) Stats() (sent int, lastErr string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sent, d.lastErr
}

func (d *Dispatcher) flush() {
	d.mu.Lock()
	pending := d.pending
	d.pending, d.timer = map[string]int{}, nil
	d.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	err := d.post(context.Background(), Text(pending))
	if err != nil {
		time.Sleep(10 * time.Second)
		err = d.post(context.Background(), Text(pending))
	}
	d.mu.Lock()
	if err != nil {
		d.lastErr = err.Error()
	} else {
		d.sent++
		d.lastErr = ""
	}
	d.mu.Unlock()
	if err != nil {
		d.log.Warn().Err(err).Int("conversations", len(pending)).Msg("New-text hook failed")
	} else {
		d.log.Info().Int("conversations", len(pending)).Msg("New-text hook sent")
	}
}

// Text builds the request text: IDs and counts only.
func Text(pending map[string]int) string {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("conversation_id %s (%d new)", id, pending[id])
	}
	return "gm-reader: new texts in " + strings.Join(parts, ", ") + "."
}

// Test sends a test request right away.
func (d *Dispatcher) Test(ctx context.Context) error {
	return d.post(ctx, "gm-reader: test from the new-text hook. No new texts.")
}

func (d *Dispatcher) post(ctx context.Context, text string) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Claude scheduled-task API triggers expect this header; other receivers ignore it.
	req.Header.Set("anthropic-version", "2023-06-01")
	if d.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.cfg.Token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", Host(d.cfg.URL), err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered %s", Host(d.cfg.URL), resp.Status)
	}
	return nil
}
