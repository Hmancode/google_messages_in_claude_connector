package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoBrowserProfileAccess backs the README's promise that the automatic
// sign-in only reads the throwaway Firefox profile gm-reader creates: no Go
// file may name the places where people's real browser profiles live.
func TestNoBrowserProfileAccess(t *testing.T) {
	root := repoRoot(t)
	banned := []string{
		"profiles.ini", `Mozilla\\Firefox`, "Mozilla/Firefox", ".mozilla/firefox",
		"Firefox/Profiles", `Firefox\\Profiles`,
		"User Data", "Login Data", "Local State", // Chrome / Edge profile data
		"key4.db", "logins.json",
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "browser_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, s := range banned {
			if strings.Contains(string(b), s) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s mentions %q: gm-reader must not read people's own browser profiles", rel, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
