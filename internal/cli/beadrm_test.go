package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// bead rm removes a memory for good with --yes or a typed yes; any other
// answer aborts before the store is called, so the bead stands.
func TestBeadRm(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	a := &app{st: st, actor: "jo"}
	remember := func(title string) *store.Bead {
		t.Helper()
		m, err := st.Remember(ctx, store.NewMemory{Title: title, Scope: "all"}, "jo")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddNote(ctx, m.ID, "evidence", "jo"); err != nil {
			t.Fatal(err)
		}
		return m
	}
	gone := func(id string) bool {
		t.Helper()
		_, err := st.GetBead(ctx, id)
		return err != nil
	}

	// --yes removes, and says what went.
	m := remember("by flag")
	out, err := run(t, a.beadCmd(), "rm", m.ID, "--yes")
	if err != nil {
		t.Fatalf("rm --yes: %v\n%s", err, out)
	}
	for _, want := range []string{"removed " + m.ID, "1 notes", "events redacted"} {
		if !strings.Contains(out, want) {
			t.Errorf("rm --yes output lacks %q:\n%s", want, out)
		}
	}
	if !gone(m.ID) {
		t.Fatal("rm --yes left the bead")
	}

	// A typed yes removes.
	m = remember("by typing")
	c := a.beadCmd()
	c.SetIn(strings.NewReader("yes\n"))
	out, err = run(t, c, "rm", m.ID)
	if err != nil {
		t.Fatalf("rm with a typed yes: %v\n%s", err, out)
	}
	if !gone(m.ID) {
		t.Fatal("rm with a typed yes left the bead")
	}

	// Anything else removes nothing.
	for _, said := range []string{"y\n", "no\n", "\n", ""} {
		m = remember("kept")
		c := a.beadCmd()
		c.SetIn(strings.NewReader(said))
		_, err = run(t, c, "rm", m.ID)
		if err == nil || !strings.Contains(err.Error(), "was not removed") {
			t.Fatalf("rm with %q typed: %v, want a refusal that removes nothing", said, err)
		}
		if gone(m.ID) {
			t.Fatalf("rm with %q typed removed the bead", said)
		}
	}

	// --json carries what went.
	m = remember("as json")
	a.json = true
	out, err = run(t, a.beadCmd(), "rm", m.ID, "--yes")
	if err != nil {
		t.Fatalf("rm --json: %v\n%s", err, out)
	}
	var r store.BeadRemoval
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("rm --json: %v\n%s", err, out)
	}
	if r.Bead != m.ID || r.Notes != 1 || r.EventsRedacted < 1 {
		t.Fatalf("rm --json = %+v", r)
	}
}
