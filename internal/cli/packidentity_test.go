package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/pack"
)

// withPublisher is the pack in dir with its pack.yaml saying, at the pack API
// that holds both keys, who published it and where.
func withPublisher(t *testing.T, dir string) string {
	t.Helper()
	file := filepath.Join(dir, "pack.yaml")
	text, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	with := strings.Replace(string(text), "kind: source\n", "api: 5\nauthor: Ada Lovelace\nid: https://packs.example.com/acme-pack\nkind: source\n", 1)
	if with == string(text) {
		t.Fatal("pack.yaml has no kind: source line")
	}
	if err := os.WriteFile(file, []byte(with), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const publisherLine = "pack acme-pack, by Ada Lovelace, published at https://packs.example.com/acme-pack (from the pack, not checked by yardr)\n"

// A pack that says who published it and where checks, shows both as the
// pack's own words, and applies. Its plan says nothing of either, since
// neither changes what an import does, and a pack that says neither shows
// as it did.
func TestAPackWithAnAuthorAndAnIDChecksShowsAndApplies(t *testing.T) {
	t.Parallel()
	a := packApp(t)
	requireNonInteractive(t)
	dir := withPublisher(t, kindPack(t))

	out, err := run(t, a.packCmd(), "check", dir)
	if err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	if strings.Contains(out, "Ada Lovelace") || strings.Contains(out, "packs.example.com") {
		t.Fatalf("the plan names the author or the id:\n%s", out)
	}

	// Shown first, a paragraph of its own, before the notes or their absence.
	if out, err := run(t, a.packCmd(), "show", dir); err != nil || out != publisherLine+"\npack acme-pack carries no setup notes\n" {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if out, err := run(t, a.packCmd(), "show", withPublisher(t, setupPack(t))); err != nil || out != publisherLine+"\n"+setupMark+"\n"+setupNotes {
		t.Fatalf("show of a pack with notes: %v\n%s", err, out)
	}

	// As data, the two keys are there for a pack that says them and absent for one that says neither.
	a.json = true
	shown, err := run(t, a.packCmd(), "show", dir)
	plain, plainErr := run(t, a.packCmd(), "show", kindPack(t))
	a.json = false
	if err != nil || plainErr != nil {
		t.Fatalf("show --json: %v, %v", err, plainErr)
	}
	var got, none map[string]any
	if err := json.Unmarshal([]byte(shown), &got); err != nil || len(got) != 3 || got["pack"] != "acme-pack" || got["author"] != "Ada Lovelace" || got["id"] != "https://packs.example.com/acme-pack" {
		t.Fatalf("show --json = %v, %v", got, err)
	}
	if err := json.Unmarshal([]byte(plain), &none); err != nil || len(none) != 1 || none["pack"] != "acme-pack" {
		t.Fatalf("show --json of a pack that says neither = %v, %v", none, err)
	}

	out, err = run(t, a.packCmd(), "apply", dir, "--yes")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "applied pack acme-pack to the yard") || strings.Contains(out, "Ada Lovelace") || strings.Contains(out, "packs.example.com") {
		t.Fatalf("apply:\n%s", out)
	}
}

// printPublisher says what the pack says and no more: both, one of them, or
// nothing at all.
func TestPrintPublisherSaysOnlyWhatThePackSays(t *testing.T) {
	t.Parallel()
	const marked = " (from the pack, not checked by yardr)\n\n"
	for _, c := range []struct{ author, id, want string }{
		{"Ada Lovelace", "https://packs.example.com/acme", "pack acme, by Ada Lovelace, published at https://packs.example.com/acme" + marked},
		{"Ada Lovelace", "", "pack acme, by Ada Lovelace" + marked},
		{"", "https://packs.example.com/acme", "pack acme, published at https://packs.example.com/acme" + marked},
		{"", "", ""},
	} {
		var b strings.Builder
		printPublisher(&b, &pack.Pack{Name: "acme", Author: c.author, ID: c.id})
		if b.String() != c.want {
			t.Errorf("author %q, id %q: printed %q, want %q", c.author, c.id, b.String(), c.want)
		}
	}
}
