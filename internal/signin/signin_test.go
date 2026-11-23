package signin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
)

// TestMain lets the test binary stand in for Firefox: Capture starts it with
// Firefox's arguments and GM_READER_FAKE_FIREFOX set.
func TestMain(m *testing.M) {
	if mode := os.Getenv("GM_READER_FAKE_FIREFOX"); mode != "" {
		fakeFirefox(mode, os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func profileArg(args []string) string {
	for i, a := range args {
		if a == "-profile" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// writeCookieDB creates a Firefox-like cookie database and returns it open
// (WAL mode, not checkpointed), like a running Firefox.
func writeCookieDB(path string, withOSID bool) (func(), error) {
	db, err := driver.Open(path)
	if err != nil {
		return nil, err
	}
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE moz_cookies (id INTEGER PRIMARY KEY, originAttributes TEXT NOT NULL DEFAULT '', name TEXT, value TEXT, host TEXT, path TEXT, expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER)`,
		`INSERT INTO moz_cookies (name, value, host, path) VALUES
			('SID', 'sid-google', '.google.com', '/'),
			('SID', 'sid-accounts', 'accounts.google.com', '/'),
			('HSID', 'hsid', '.google.com', '/'),
			('SSID', 'ssid', '.google.com', '/'),
			('APISID', 'apisid', '.google.com', '/'),
			('SAPISID', 'sapisid', '.google.com', '/'),
			('__Secure-1PSIDTS', 'ts', '.google.com', '/'),
			('NID', 'not-needed', '.google.com', '/'),
			('HSID', 'wrong-path', '.google.com', '/other')`,
		`INSERT INTO moz_cookies (originAttributes, name, value, host, path) VALUES ('^partitionKey=%28https%2Cexample.com%29', 'SSID', 'partitioned', '.google.com', '/')`,
	}
	if withOSID {
		stmts = append(stmts, `INSERT INTO moz_cookies (name, value, host, path) VALUES ('OSID', 'osid-messages', 'messages.google.com', '/'), ('OSID', 'osid-mail', 'mail.google.com', '/')`)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			db.Close()
			return nil, err
		}
	}
	return func() { db.Close() }, nil
}

func fakeFirefox(mode string, args []string) {
	dir := profileArg(args)
	if dir == "" {
		os.Exit(3)
	}
	if _, err := os.Stat(filepath.Join(dir, "user.js")); err != nil {
		os.Exit(4) // gm-reader must write user.js before starting Firefox
	}
	switch mode {
	case "signin":
		time.Sleep(300 * time.Millisecond)
		closeDB, err := writeCookieDB(filepath.Join(dir, "cookies.sqlite"), true)
		if err != nil {
			os.Exit(5)
		}
		defer closeDB()
		time.Sleep(60 * time.Second) // "the window stays open" until gm-reader closes it
	case "partial":
		closeDB, err := writeCookieDB(filepath.Join(dir, "cookies.sqlite"), false)
		if err != nil {
			os.Exit(5)
		}
		closeDB()
	case "exit":
	}
}

var wantCookies = map[string]string{
	"SID": "sid-google", "HSID": "hsid", "SSID": "ssid", "OSID": "osid-messages",
	"APISID": "apisid", "SAPISID": "sapisid", "__Secure-1PSIDTS": "ts",
}

func checkCookies(t *testing.T, got map[string]string) {
	t.Helper()
	if len(got) != len(wantCookies) {
		t.Fatalf("got %d cookies, want %d: %v", len(got), len(wantCookies), names(got))
	}
	for k, v := range wantCookies {
		if got[k] != v {
			t.Errorf("cookie %s: got %q, want %q", k, got[k], v)
		}
	}
}

func names(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestReadProfileCookiesFromRunningWAL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DefaultProfileName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	closeDB, err := writeCookieDB(filepath.Join(dir, "cookies.sqlite"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	got, err := ReadProfileCookies(dir)
	if err != nil {
		t.Fatal(err)
	}
	checkCookies(t, got)
	if !complete(got) {
		t.Fatal("expected a complete set")
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range entries {
		if e.Name() != DefaultProfileName {
			t.Fatalf("temporary copy left behind: %s", e.Name())
		}
	}
}

func capture(t *testing.T, mode string) (Result, error, string) {
	t.Helper()
	t.Setenv("GM_READER_FAKE_FIREFOX", mode)
	dir := filepath.Join(t.TempDir(), DefaultProfileName)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res, err := Capture(ctx, Options{Firefox: os.Args[0], ProfileDir: dir, Timeout: 30 * time.Second, Poll: 100 * time.Millisecond})
	return res, err, dir
}

func TestCaptureFindsCookiesClosesWindowAndDeletesProfile(t *testing.T) {
	start := time.Now()
	res, err, dir := capture(t, "signin")
	if err != nil {
		t.Fatal(err)
	}
	checkCookies(t, res.Cookies)
	if !res.ProfileDeleted {
		t.Fatal("profile not deleted")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile folder still exists: %v", err)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("the window was not closed by gm-reader")
	}
}

func TestCaptureWindowClosedEarly(t *testing.T) {
	for _, mode := range []string{"exit", "partial"} {
		_, err, dir := capture(t, mode)
		if !errors.Is(err, ErrClosedEarly) {
			t.Fatalf("%s: expected ErrClosedEarly, got %v", mode, err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: profile folder still exists", mode)
		}
	}
}

func TestCaptureCancelled(t *testing.T) {
	t.Setenv("GM_READER_FAKE_FIREFOX", "exit-never")
	dir := filepath.Join(t.TempDir(), DefaultProfileName)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// "exit-never" isn't a known mode, so the fake exits at once; use a long
	// poll so the context ends first.
	_, err := Capture(ctx, Options{Firefox: os.Args[0], ProfileDir: dir, Timeout: time.Minute, Poll: 5 * time.Second})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the context error, got %v", err)
	}
}

func TestCleanupOnlyDeletesSignInFolder(t *testing.T) {
	other := t.TempDir()
	if err := Cleanup(other); err == nil {
		t.Fatal("Cleanup must refuse folders not named " + DefaultProfileName)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("folder was deleted")
	}
}

func TestCookieMatching(t *testing.T) {
	hosts := map[string]bool{
		".google.com": true, "messages.google.com": true, ".messages.google.com": true,
		"accounts.google.com": false, ".accounts.google.com": false, "google.com": false, ".evilgoogle.com": false,
	}
	for h, want := range hosts {
		if got := hostMatches(h, targetHost); got != want {
			t.Errorf("hostMatches(%q) = %v, want %v", h, got, want)
		}
	}
	paths := map[string]bool{"/": true, "": true, "/web": true, "/web/": true, "/web/config": true, "/we": false, "/other": false}
	for p, want := range paths {
		if got := pathMatches(p, targetPath); got != want {
			t.Errorf("pathMatches(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestArgsKeepWindowSeparate(t *testing.T) {
	a := Args(`C:\x\signin-profile`)
	want := []string{"-no-remote", "-new-instance", "-wait-for-browser", "-profile", `C:\x\signin-profile`, SignInURL}
	if len(a) != len(want) {
		t.Fatalf("args %v", a)
	}
	for i := range a {
		if a[i] != want[i] {
			t.Fatalf("args %v", a)
		}
	}
}
