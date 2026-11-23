// Package install copies gm-reader to a fixed folder and updates it in place.
//
// Windows doesn't let a running program be overwritten, but it does let it be
// renamed. So an update renames the old copy to gm-reader.old.exe, puts the
// new one in its place, and the old copy is deleted the next time gm-reader
// starts. Claude can keep running the old copy until it restarts.
package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ExeName is gm-reader's file name on this system.
func ExeName() string {
	if runtime.GOOS == "windows" {
		return "gm-reader.exe"
	}
	return "gm-reader"
}

// DefaultDir is where a new install goes: a per-user folder that needs no
// administrator rights and isn't synced by OneDrive.
func DefaultDir() (string, error) {
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", errors.New("LOCALAPPDATA is not set")
		}
		return filepath.Join(local, "Programs", "gm-reader"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

// SameFile reports whether two paths are the same file on disk.
func SameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func oldName(dst string, n int) string {
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)
	if n == 0 {
		return base + ".old" + ext
	}
	return base + ".old" + strconv.Itoa(n) + ext
}

// Copy puts the program at src into dst, replacing any copy already there.
// It works even while the old copy is running.
func Copy(src, dst string) error {
	if SameFile(src, dst) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := copyFile(src, tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("could not copy gm-reader to %s: %w", filepath.Dir(dst), err)
	}
	var moved string
	if _, err := os.Stat(dst); err == nil {
		// Move the old copy aside. An earlier .old may still be running, so
		// fall back to numbered names.
		for n := 0; n < 20; n++ {
			o := oldName(dst, n)
			if err := os.Remove(o); err != nil && !errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err := os.Rename(dst, o); err == nil {
				moved = o
				break
			}
		}
		if moved == "" {
			os.Remove(tmp)
			return fmt.Errorf("could not replace %s (is it open in another program?)", dst)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if moved != "" {
			_ = os.Rename(moved, dst)
		}
		os.Remove(tmp)
		return err
	}
	return nil
}

// CleanupOld deletes copies left over from earlier updates. Copies still in
// use are left for next time.
func CleanupOld(dir string) {
	ext := filepath.Ext(ExeName())
	base := strings.TrimSuffix(ExeName(), ext)
	matches, _ := filepath.Glob(filepath.Join(dir, base+".old*"+ext))
	for _, m := range matches {
		_ = os.Remove(m)
	}
	_ = os.Remove(filepath.Join(dir, ExeName()+".new"))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// CompareVersions compares release versions like "v0.3.0" or "v0.3.0-rc1".
// It returns -1, 0 or 1, and ok=false when either isn't a release version
// (for example a "dev" build).
func CompareVersions(a, b string) (int, bool) {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			if pa.n[i] < pb.n[i] {
				return -1, true
			}
			return 1, true
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0, true
	case pa.pre == "": // a release is newer than its pre-releases
		return 1, true
	case pb.pre == "":
		return -1, true
	case pa.pre < pb.pre:
		return -1, true
	default:
		return 1, true
	}
}

type ver struct {
	n   [3]int
	pre string
}

func parse(s string) (ver, bool) {
	s, ok := strings.CutPrefix(strings.TrimSpace(s), "v")
	if !ok {
		return ver{}, false
	}
	var v ver
	s, v.pre, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return ver{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return ver{}, false
		}
		v.n[i] = n
	}
	return v, true
}
