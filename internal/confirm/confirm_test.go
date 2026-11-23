package confirm

import (
	"strings"
	"testing"
)

func TestSanitizeRemovesBidiAndControls(t *testing.T) {
	in := "pay ‮evil‬ now\x00\x07"
	out := Sanitize(in, 100)
	if strings.ContainsRune(out, '‮') || strings.ContainsRune(out, 0) {
		t.Fatalf("sanitize left dangerous characters: %q", out)
	}
	if !strings.Contains(out, "[hidden-char]") {
		t.Fatalf("expected a visible marker for hidden characters: %q", out)
	}
}

func TestSanitizeTruncates(t *testing.T) {
	out := Sanitize(strings.Repeat("a", 50), 10)
	if !strings.HasPrefix(out, strings.Repeat("a", 10)) || !strings.Contains(out, "40 more characters") {
		t.Fatalf("unexpected truncation: %q", out)
	}
}
