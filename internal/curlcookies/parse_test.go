package curlcookies

import (
	"errors"
	"testing"
)

const cookieLine = "SID=sid.val-1_x; __Secure-1PSID=wrong1; HSID=hsid2; SSID=ssid3; __Secure-1PSIDTS=ts4; APISID=api5/x; SAPISID=sapi6/y; OSID=osid7"

func check(t *testing.T, input string) {
	t.Helper()
	got, err := Parse(input)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	want := map[string]string{
		"SID": "sid.val-1_x", "HSID": "hsid2", "SSID": "ssid3", "OSID": "osid7",
		"APISID": "api5/x", "SAPISID": "sapi6/y", "__Secure-1PSIDTS": "ts4",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestChromeBash(t *testing.T) {
	check(t, "curl 'https://messages.google.com/web/config' \\\n  -H 'accept: */*' \\\n  -b '"+cookieLine+"' \\\n  -H 'user-agent: Mozilla'")
}

func TestChromeBashAnsiQuoted(t *testing.T) {
	check(t, "curl 'https://messages.google.com/web/config' -b $'"+cookieLine+"'")
}

func TestFirefoxHeader(t *testing.T) {
	check(t, "curl 'https://messages.google.com/web/config' -H 'Cookie: "+cookieLine+"'")
}

func TestWindowsCmd(t *testing.T) {
	check(t, "curl ^\"https://messages.google.com/web/config^\" ^\r\n  -H ^\"accept: */*^\" ^\r\n  -b ^\""+cookieLine+"^\"")
}

func TestJSONObject(t *testing.T) {
	check(t, `{"SID":"sid.val-1_x","HSID":"hsid2","SSID":"ssid3","OSID":"osid7","APISID":"api5/x","SAPISID":"sapi6/y","__Secure-1PSIDTS":"ts4"}`)
}

func TestCookieEditorExport(t *testing.T) {
	check(t, `[{"name":"SID","value":"sid.val-1_x"},{"name":"HSID","value":"hsid2"},{"name":"SSID","value":"ssid3"},
	{"name":"OSID","value":"osid7"},{"name":"APISID","value":"api5/x"},{"name":"SAPISID","value":"sapi6/y"},{"name":"__Secure-1PSIDTS","value":"ts4"}]`)
}

func TestMissing(t *testing.T) {
	_, err := Parse("curl 'https://example.com' -b 'SID=a; HSID=b'")
	var m ErrMissing
	if !errors.As(err, &m) || len(m.Names) != 4 {
		t.Fatalf("expected 4 missing cookies, got %v", err)
	}
}

func TestDoesNotConfuseSimilarNames(t *testing.T) {
	got, _ := Parse("-b '__Secure-3PSID=bad; SAPISID=good; APISID=good2'")
	if got["SID"] != "" {
		t.Fatalf("SID must not match inside __Secure-3PSID, got %q", got["SID"])
	}
	if got["APISID"] != "good2" || got["SAPISID"] != "good" {
		t.Fatalf("APISID/SAPISID confused: %v", got)
	}
}
