package secure

import (
	"bytes"
	"runtime"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	secret := []byte(`{"cookies":{"SID":"canary-secret-value"}}`)
	sealed, err := Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if !Encrypted {
			t.Fatal("Windows build must use DPAPI")
		}
		if bytes.Contains(sealed, []byte("canary-secret-value")) {
			t.Fatal("sealed session contains the plaintext secret; DPAPI was not applied")
		}
		if sealed[len(magic)] != modeDPAPI {
			t.Fatalf("expected DPAPI mode byte, got %d", sealed[len(magic)])
		}
	}
	opened, err := Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, secret) {
		t.Fatal("round trip changed the data")
	}
}

func TestRejectsGarbage(t *testing.T) {
	if _, err := Open([]byte("not a session")); err == nil {
		t.Fatal("expected error for garbage input")
	}
}

func TestRejectsTamperedDPAPI(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI only on Windows")
	}
	sealed, err := Seal([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 0xff
	if _, err := Open(sealed); err == nil {
		t.Fatal("tampered DPAPI blob should not decrypt")
	}
}
