package session

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadUpdate(t *testing.T) {
	st := OpenAt(filepath.Join(t.TempDir(), "session.bin"))
	if _, err := st.Load(); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("expected ErrNotPaired, got %v", err)
	}
	f := &File{Auth: []byte(`{"x":1}`), PairedAt: time.Now().UTC().Truncate(time.Second)}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.AnyWrite() {
		t.Fatal("write actions must default to off")
	}
	if got.Settings.Mode() != ConfirmDialog {
		t.Fatal("confirmation must default to the dialog")
	}
	if err := st.Update(func(f *File) error { f.Settings.AllowReact = true; return nil }); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Load()
	if !got.Settings.AllowReact || got.Settings.AllowSend {
		t.Fatalf("unexpected settings after update: %+v", got.Settings)
	}
	if err := st.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(); !errors.Is(err, ErrNotPaired) {
		t.Fatal("expected ErrNotPaired after delete")
	}
}
