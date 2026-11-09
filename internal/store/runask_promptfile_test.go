package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// A prompt file is said to be rewritten only where its name is one the yard
// writes and the digest in it is not its text's: a name the yard would not
// write says nothing of the text, and a digest is read in either case.
func TestPromptRewritten(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte("Look twice.\n"))
	digest := hex.EncodeToString(sum[:])
	for _, c := range []struct {
		name, path, text string
		want             bool
	}{
		{"holds what its name says", "/yard/roles/scouts." + digest + ".md", "Look twice.\n", false},
		{"holds other text", "/yard/roles/scouts." + digest + ".md", "Look once.\n", true},
		{"holds the text without its newline", "/yard/roles/scouts." + digest + ".md", "Look twice.", true},
		{"a digest of other text", "/yard/roles/scouts." + strings.Repeat("0", 64) + ".md", "Look twice.\n", true},
		{"the digest in capitals, text as it says", "/yard/roles/scouts." + strings.ToUpper(digest) + ".md", "Look twice.\n", false},
		{"the digest in capitals, other text", "/yard/roles/scouts." + strings.ToUpper(digest) + ".md", "Look once.\n", true},
		{"a name the yard does not write", "/yard/roles/scouts.md", "Look once.\n", false},
		{"a digest that is not the end of the name", "/yard/roles/scouts." + digest + ".md.bak", "Look once.\n", false},
		{"a digest one character short", "/yard/roles/scouts." + digest[1:] + ".md", "Look once.\n", false},
		{"a digest one character long", "/yard/roles/scouts.a" + digest + ".md", "Look once.\n", false},
	} {
		if got := promptRewritten(c.path, c.text); got != c.want {
			t.Errorf("%s: promptRewritten(%q, %q) = %v, want %v", c.name, c.path, c.text, got, c.want)
		}
	}
}
