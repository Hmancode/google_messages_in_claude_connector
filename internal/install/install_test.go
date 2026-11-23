package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMain lets a copy of the test binary act as "gm-reader, running".
func TestMain(m *testing.M) {
	if os.Getenv("GM_READER_INSTALL_SLEEP") != "" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func copyBinary(t *testing.T, dst string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(os.Args[0], dst); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCopyFreshAndUpdate(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "download", ExeName())
	_ = os.MkdirAll(filepath.Dir(src), 0o755)
	write(t, src, "v1")
	dst := filepath.Join(d, "Programs", "gm-reader", ExeName())
	if err := Copy(src, dst); err != nil {
		t.Fatal(err)
	}
	if read(t, dst) != "v1" {
		t.Fatal("fresh install wrong")
	}
	write(t, src, "v2")
	if err := Copy(src, dst); err != nil {
		t.Fatal(err)
	}
	if read(t, dst) != "v2" || read(t, oldName(dst, 0)) != "v1" {
		t.Fatal("update wrong")
	}
	CleanupOld(filepath.Dir(dst))
	if _, err := os.Stat(oldName(dst, 0)); !os.IsNotExist(err) {
		t.Fatal("old copy not cleaned up")
	}
}

func TestUpdateWhileRunning(t *testing.T) {
	d := t.TempDir()
	dst := filepath.Join(d, "Programs", ExeName())
	copyBinary(t, dst)
	cmd := exec.Command(dst)
	cmd.Env = append(os.Environ(), "GM_READER_INSTALL_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(200 * time.Millisecond)

	src := filepath.Join(d, "new-"+ExeName())
	write(t, src, "v2")
	if err := Copy(src, dst); err != nil {
		t.Fatalf("update while the old copy runs: %v", err)
	}
	if read(t, dst) != "v2" {
		t.Fatal("new copy not in place")
	}
	// The running copy can't always be deleted yet; cleanup must not fail
	// and must not touch the new copy.
	CleanupOld(filepath.Dir(dst))
	if read(t, dst) != "v2" {
		t.Fatal("cleanup removed the current copy")
	}
	// A second update while the first old copy still runs uses another name.
	write(t, src, "v3")
	if err := Copy(src, dst); err != nil {
		t.Fatalf("second update: %v", err)
	}
	if read(t, dst) != "v3" {
		t.Fatal("second update wrong")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	time.Sleep(200 * time.Millisecond)
	CleanupOld(filepath.Dir(dst))
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), "gm-reader.old*"))
	if len(left) != 0 {
		t.Fatalf("old copies left after the program stopped: %v", left)
	}
}

func TestCopySameFileIsNoop(t *testing.T) {
	p := filepath.Join(t.TempDir(), ExeName())
	write(t, p, "x")
	if err := Copy(p, p); err != nil {
		t.Fatal(err)
	}
	if read(t, p) != "x" {
		t.Fatal("changed")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.3.0", "v0.2.0", 1, true},
		{"v0.2.0", "v0.10.0", -1, true},
		{"v0.3.0", "v0.3.0", 0, true},
		{"v0.3.0", "v0.3.0-rc1", 1, true},
		{"v0.3.0-rc1", "v0.3.0-rc2", -1, true},
		{"dev", "v0.3.0", 0, false},
		{"v1.2", "v1.2.0", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("CompareVersions(%q, %q) = %d, %v; want %d, %v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}
