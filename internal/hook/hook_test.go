package hook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestBatchesAndSendsIDsOnly(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
	}))
	defer srv.Close()
	d := New(Config{URL: srv.URL, Token: "tok", Batch: 50 * time.Millisecond}, zerolog.Nop())
	d.Notify("122")
	d.Notify("122")
	d.Notify("884")
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("expected one batched request, got %d", len(bodies))
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["text"] != "gm-reader: new texts in conversation_id 122 (2 new), conversation_id 884 (1 new)." {
		t.Fatalf("unexpected text: %q", payload["text"])
	}
	if auth[0] != "Bearer tok" {
		t.Fatalf("missing bearer token: %q", auth[0])
	}
	if sent, _ := d.Stats(); sent != 1 {
		t.Fatalf("stats: %d", sent)
	}
}

func TestValidate(t *testing.T) {
	for _, ok := range []string{"https://api.anthropic.com/v1/claude_code/routines/abc/fire", "http://localhost:8788/x"} {
		if err := Validate(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://example.com/x", "ftp://x", "not a url", ""} {
		if err := Validate(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	if h := Host("https://api.anthropic.com/v1/x?token=secret"); h != "api.anthropic.com" || strings.Contains(h, "secret") {
		t.Fatalf("host: %q", h)
	}
}
