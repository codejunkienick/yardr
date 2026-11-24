package cli

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// yardr bead files lists the files a bead's notes carry, one row each,
// <seq> <n> <kind> <hash> <path>, the oldest note first and a note's files
// in the order it was written with them. The path is the words of the note's
// marked line in bead show, name and quoting included, and the hash names
// the file in blobs/. --json prints the same as {seq, n, hash, type, bytes,
// name}. A bead whose notes carry none prints nothing, [] under --json, and
// an unknown bead is refused as bead show refuses it.
func TestBeadFiles(t *testing.T) {
	t.Parallel()
	api, s := storetest.NewOwned(t)
	st := userYardOn(t, api)
	home := yardEnv(t, st)["YARDR_HOME"]
	ctx := context.Background()
	b, err := api.CreateBead(ctx, store.NewBead{Title: "the shed"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	bare, err := api.CreateBead(ctx, store.NewBead{Title: "the roof"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	png := file("shot.png", "\x89PNG\r\n\x1a\n the shed")
	page := file("page.html", "<!DOCTYPE html><html><head><title>the shed</title></head><body><h1>the shed</h1></body></html>")
	// A name holding a parenthesis stands quoted in its parentheses, as the
	// note's marked line has it.
	costs := file("costs (final).csv", "shed,paint\nroof,tar\n")

	// Files on two notes with one between that has none: the listing has
	// each note's number, not its place among the bead's notes.
	mustAs(t, st, "jo", "bead", "note", b.ID, "the shed, painted", "--file", png)
	mustAs(t, st, "jo", "bead", "note", b.ID, "no file on this one")
	mustAs(t, st, "jo", "bead", "note", b.ID, "the shed, mocked up and costed", "--file", page, "--file", costs)
	mustAs(t, st, "jo", "bead", "note", bare.ID, "nothing attached")

	notes, err := api.Notes(ctx, b.ID)
	if err != nil || len(notes) != 3 || len(notes[0].Files) != 1 || len(notes[1].Files) != 0 || len(notes[2].Files) != 2 {
		t.Fatalf("the notes = %+v, %v", notes, err)
	}
	type wantFile struct {
		seq       uint64
		n         int
		kind      string
		path      string // the file on disk the note was written with
		shown     string // its name as the marked line shows it
		name      string
		mediaType string
	}
	wants := []wantFile{
		{notes[0].Seq, 1, "image", png, `(shot.png)`, "shot.png", "image/png"},
		{notes[2].Seq, 1, "page", page, `(page.html)`, "page.html", "text/html; charset=utf-8"},
		{notes[2].Seq, 2, "file", costs, `("costs (final).csv")`, "costs (final).csv", "text/plain; charset=utf-8"},
	}
	var rows []string
	for _, w := range wants {
		hash := hashOfFile(t, w.path)
		// The hash is what the file is called in blobs/, which holds its bytes.
		if got, err := os.ReadFile(s.BlobPath(hash)); err != nil {
			t.Fatalf("%s has no file in blobs/: %v", hash, err)
		} else if want, _ := os.ReadFile(w.path); string(got) != string(want) {
			t.Fatalf("blobs/%s holds %q, want %q", hash, got, want)
		}
		rows = append(rows, strconv.FormatUint(w.seq, 10)+" "+strconv.Itoa(w.n)+" "+w.kind+" "+hash+" "+store.BlobPath(home, hash)+" "+w.shown)
	}

	out := mustAs(t, st, "jo", "bead", "files", b.ID)
	if want := strings.Join(rows, "\n") + "\n"; out != want {
		t.Fatalf("bead files:\n%s\nwant:\n%s", out, want)
	}
	// Each row's path and name are the note's own marked line's words.
	show := mustAs(t, st, "jo", "bead", "show", b.ID)
	for _, row := range rows {
		words := strings.SplitN(row, " ", 5)[4]
		if !strings.Contains(show, words) {
			t.Errorf("bead show has no %q among its marked lines:\n%s", words, show)
		}
	}

	var listed []struct {
		Seq   uint64 `json:"seq"`
		N     int    `json:"n"`
		Hash  string `json:"hash"`
		Type  string `json:"type"`
		Bytes int64  `json:"bytes"`
		Name  string `json:"name"`
	}
	raw := mustAs(t, st, "jo", "bead", "files", b.ID, "--json")
	if err := json.Unmarshal([]byte(raw), &listed); err != nil || len(listed) != len(wants) {
		t.Fatalf("bead files --json = %q, %v", raw, err)
	}
	for i, w := range wants {
		content, _ := os.ReadFile(w.path)
		g := listed[i]
		if g.Seq != w.seq || g.N != w.n || g.Hash != hashOfFile(t, w.path) || g.Type != w.mediaType || g.Bytes != int64(len(content)) || g.Name != w.name {
			t.Errorf("bead files --json[%d] = %+v, want %+v", i, g, w)
		}
	}
	// The array holds the six keys and no more: the file's record is not
	// the listing's.
	var keyed []map[string]any
	if err := json.Unmarshal([]byte(raw), &keyed); err != nil {
		t.Fatal(err)
	}
	for i, m := range keyed {
		if keys := slices.Sorted(maps.Keys(m)); !slices.Equal(keys, []string{"bytes", "hash", "n", "name", "seq", "type"}) {
			t.Errorf("bead files --json[%d] has the keys %v", i, keys)
		}
	}

	// A bead whose notes carry no file prints nothing, and an empty array.
	if out := mustAs(t, st, "jo", "bead", "files", bare.ID); out != "" {
		t.Errorf("bead files of a bead with no files = %q", out)
	}
	if out := mustAs(t, st, "jo", "bead", "files", bare.ID, "--json"); strings.TrimSpace(out) != "[]" {
		t.Errorf("bead files --json of a bead with no files = %q", out)
	}

	// An unknown bead fails as bead show fails.
	_, shown := as(t, st, "jo", "bead", "show", "yd-nope")
	if shown == nil {
		t.Fatal("bead show of an unknown bead did not fail")
	}
	if out, err := as(t, st, "jo", "bead", "files", "yd-nope"); err == nil || err.Error() != shown.Error() || out != "" {
		t.Errorf("bead files yd-nope = %q, %v, want %v", out, err, shown)
	}
}
