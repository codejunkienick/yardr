package cli

import (
	"context"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
)

// Applying the linear pack adds the source it declares; running it twice
// changes nothing.
func TestPackApplyLinearAddsTheDeclaredSource(t *testing.T) {
	t.Parallel()
	a := packApp(t)
	requireNonInteractive(t)
	ctx := context.Background()
	out, err := run(t, a.packCmd(), "apply", "linear", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "source linear is declared") {
		t.Errorf("the source was not added:\n%s", out)
	}
	src, err := a.st.GetSource(ctx, "linear")
	if err != nil {
		t.Fatalf("the source was not added: %v", err)
	}
	// The shipped source declares the calls the member flow grants by
	// name: comment (update) to the two exec groups, read-comments (read)
	// to the claim group alone, never the blanket read grant.
	dec, err := src.Declaration()
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := dec.Call("comment"); !ok || c.Kind != source.Update {
		t.Fatalf("comment = %+v, %v, want an update call", c, ok)
	}
	if c, ok := dec.Call("read-comments"); !ok || c.Kind != source.Read {
		t.Fatalf("read-comments = %+v, %v, want a read call", c, ok)
	}
	for _, g := range []string{"report-claim", "report-post"} {
		if err := a.st.PutGroup(ctx, store.Group{Name: g, Runner: "exec", Config: map[string]any{"cmd": "true"}}, "me"); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := run(t, a.sourceCmd(), "grant", "linear", "comment", "--group", "report-claim", "--group", "report-post"); err != nil ||
		!strings.Contains(out, "group report-claim may call linear: comment (update)") ||
		!strings.Contains(out, "group report-post may call linear: comment (update)") {
		t.Fatalf("grant comment = %q, %v", out, err)
	}
	if out, err := run(t, a.sourceCmd(), "grant", "linear", "read-comments", "--group", "report-claim"); err != nil ||
		!strings.Contains(out, "group report-claim may call linear: comment (update), read-comments") {
		t.Fatalf("grant read-comments = %q, %v", out, err)
	}
	out, err = run(t, a.sourceCmd(), "show", "linear")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  call comment  update  POST /graphql  Post one top-level comment on an issue, with the key as its own id\n",
		"  call read-comments  read  POST /graphql  Read one issue's newest comments, one page of 50\n",
		"  calls: 60 an hour for each caller, 10 of them updates at most\n",
		"may be called by group report-claim, group report-post\n",
		"may be called by group report-claim\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("source show holds no %q:\n%s", want, out)
		}
	}
	out, err = run(t, a.packCmd(), "apply", "linear", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing changed") {
		t.Errorf("second apply changed something:\n%s", out)
	}
}
