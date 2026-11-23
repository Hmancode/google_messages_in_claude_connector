// Package signin gets the Google sign-in cookies that pairing needs without
// the user opening developer tools.
//
// It starts Firefox with a throwaway profile that gm-reader creates in its own
// data folder, at Google's sign-in page for Messages for Web. After the user
// signs in, it reads the needed cookies from that profile's cookie database,
// closes the window and deletes the profile. It never opens or reads the
// user's own Firefox profiles, and it never logs or prints cookie values.
package signin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3/driver"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/curlcookies"
)

// SignInURL signs in to Google and then lands on Messages for Web's config
// page, which sets the Messages cookie (OSID) that pairing needs.
const SignInURL = "https://accounts.google.com/AccountChooser?continue=https://messages.google.com/web/config"

// The cookies are the ones a browser would send to this address.
const (
	targetHost = "messages.google.com"
	targetPath = "/web/config"
)

// ErrClosedEarly means the Firefox window was closed before sign-in finished.
var ErrClosedEarly = errors.New("the Firefox window was closed before the sign-in finished")

// ErrTimeout means the user did not finish signing in in time.
var ErrTimeout = errors.New("the sign-in was not finished in time")

// Options configures Capture.
type Options struct {
	Firefox    string        // path to the Firefox program
	ProfileDir string        // throwaway profile folder; replaced at the start and deleted at the end
	Timeout    time.Duration // how long to wait for the sign-in (default 10 minutes)
	Poll       time.Duration // how often to look for the cookies (default 2 seconds)
	// Closed is called when the cookies were found but the window could not be
	// closed automatically, to ask the user to close it.
	Closed func()
}

// Result is what Capture found.
type Result struct {
	Cookies map[string]string
	// ProfileDeleted is false if the throwaway profile could not be deleted
	// (usually because the window is still open). Cleanup removes it later.
	ProfileDeleted bool
}

// userJS keeps the throwaway profile quiet: no welcome pages, no saved
// passwords, no telemetry, no default-browser prompt.
const userJS = `// Written by gm-reader for a one-time Google sign-in. This folder is deleted afterwards.
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.aboutwelcome.enabled", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("startup.homepage_welcome_url", "");
user_pref("startup.homepage_welcome_url.additional", "");
user_pref("datareporting.policy.firstRunURL", "");
user_pref("datareporting.policy.dataSubmissionEnabled", false);
user_pref("datareporting.healthreport.uploadEnabled", false);
user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);
user_pref("app.normandy.enabled", false);
user_pref("signon.rememberSignons", false);
user_pref("browser.privatebrowsing.autostart", false);
user_pref("browser.sessionstore.resume_from_crash", false);
user_pref("browser.tabs.warnOnClose", false);
user_pref("browser.warnOnQuit", false);
`

// Args returns the Firefox command-line arguments for a sign-in window.
// -no-remote and -new-instance keep it separate from any Firefox already
// open; -wait-for-browser keeps the started process alive until the window
// closes, so gm-reader can close it.
func Args(profileDir string) []string {
	return []string{"-no-remote", "-new-instance", "-wait-for-browser", "-profile", profileDir, SignInURL}
}

// Capture opens the sign-in window and waits until the needed cookies appear.
func Capture(ctx context.Context, o Options) (Result, error) {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Minute
	}
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}
	if err := Cleanup(o.ProfileDir); err != nil {
		return Result{}, fmt.Errorf("could not clear the old sign-in folder %s: %w (close any Firefox window gm-reader opened and try again)", o.ProfileDir, err)
	}
	if err := os.MkdirAll(o.ProfileDir, 0o700); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(o.ProfileDir, "user.js"), []byte(userJS), 0o600); err != nil {
		return Result{}, err
	}

	cmd := exec.Command(o.Firefox, Args(o.ProfileDir)...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		_ = Cleanup(o.ProfileDir)
		return Result{}, fmt.Errorf("could not start Firefox: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	isExited := func() bool {
		select {
		case <-exited:
			return true
		default:
			return false
		}
	}

	finish := func(res Result, err error) (Result, error) {
		if !isExited() {
			killTree(cmd.Process)
			select {
			case <-exited:
			case <-time.After(10 * time.Second):
			}
		}
		if res.Cookies != nil && isExited() && profileInUse(o.ProfileDir) && o.Closed != nil {
			// The window outlived the process we started; ask the user.
			o.Closed()
		}
		res.ProfileDeleted = waitAndCleanup(o.ProfileDir, 2*time.Minute)
		return res, err
	}

	deadline := time.NewTimer(o.Timeout)
	defer deadline.Stop()
	tick := time.NewTicker(o.Poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return finish(Result{}, ctx.Err())
		case <-deadline.C:
			return finish(Result{}, ErrTimeout)
		case <-tick.C:
		}
		if c, err := ReadProfileCookies(o.ProfileDir); err == nil && complete(c) {
			return finish(Result{Cookies: c}, nil)
		}
		if isExited() && !profileInUse(o.ProfileDir) {
			// Firefox is gone. Its last writes are on disk now: look once more.
			if c, err := ReadProfileCookies(o.ProfileDir); err == nil && complete(c) {
				return finish(Result{Cookies: c}, nil)
			}
			return finish(Result{}, ErrClosedEarly)
		}
	}
}

func complete(c map[string]string) bool {
	for _, n := range curlcookies.Required {
		if c[n] == "" {
			return false
		}
	}
	return true
}

// ReadProfileCookies reads the cookies a browser would send to Messages for
// Web from a Firefox profile's cookie database. Firefox may be running, so it
// reads a copy of the database. Only the cookies pairing uses are returned.
func ReadProfileCookies(profileDir string) (map[string]string, error) {
	src := filepath.Join(profileDir, "cookies.sqlite")
	if _, err := os.Stat(src); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(profileDir), ".gm-reader-cookies-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	dst := filepath.Join(tmp, "cookies.sqlite")
	if err := copyFile(src, dst); err != nil {
		return nil, err
	}
	if err := copyFile(src+"-wal", dst+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	db, err := driver.Open(dst)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return selectCookies(db)
}

type cookieRow struct{ name, value, host, path string }

func selectCookies(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT name, value, host, path FROM moz_cookies WHERE originAttributes = ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	wanted := map[string]bool{}
	for _, n := range append(append([]string{}, curlcookies.Required...), curlcookies.Optional...) {
		wanted[n] = true
	}
	best := map[string]cookieRow{}
	for rows.Next() {
		var r cookieRow
		if err := rows.Scan(&r.name, &r.value, &r.host, &r.path); err != nil {
			return nil, err
		}
		if !wanted[r.name] || r.value == "" || !hostMatches(r.host, targetHost) || !pathMatches(r.path, targetPath) {
			continue
		}
		// Like a browser, prefer the most specific host, then the longest path.
		if b, ok := best[r.name]; ok && (len(b.host) > len(r.host) || (len(b.host) == len(r.host) && len(b.path) >= len(r.path))) {
			continue
		}
		best[r.name] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(best))
	for n, r := range best {
		out[n] = r.value
	}
	return out, nil
}

// hostMatches applies the cookie domain rule: ".google.com" matches
// messages.google.com, "accounts.google.com" does not.
func hostMatches(cookieHost, host string) bool {
	h := strings.ToLower(cookieHost)
	if d, ok := strings.CutPrefix(h, "."); ok {
		return host == d || strings.HasSuffix(host, "."+d)
	}
	return h == host
}

func pathMatches(cookiePath, path string) bool {
	if cookiePath == "" || cookiePath == "/" || cookiePath == path {
		return true
	}
	if !strings.HasPrefix(path, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || path[len(cookiePath)] == '/'
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Cleanup deletes the throwaway profile folder, if it exists.
func Cleanup(profileDir string) error {
	if profileDir == "" || filepath.Base(profileDir) != filepath.Base(DefaultProfileName) {
		return fmt.Errorf("refusing to delete %q: not gm-reader's sign-in folder", profileDir)
	}
	return os.RemoveAll(profileDir)
}

// DefaultProfileName is the throwaway profile's folder name inside
// gm-reader's data folder. Cleanup only deletes folders with this name.
const DefaultProfileName = "signin-profile"

func waitAndCleanup(dir string, max time.Duration) bool {
	stop := time.Now().Add(max)
	for {
		if !profileInUse(dir) {
			if err := Cleanup(dir); err == nil {
				return true
			}
		}
		if time.Now().After(stop) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}
