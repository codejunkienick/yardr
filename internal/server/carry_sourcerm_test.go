package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

func sourceRmAsk(name string) store.Ask {
	return store.Ask{Question: "Remove the " + name + " source?", To: "mckean",
		Context: []string{"nothing is told through it any more"},
		Run:     &store.AskRun{Argv: []string{"source rm " + name}}}
}

// An approved source rm for a source whose commands were copied to the home
// removes the source in the approval and the copies in the carrying out,
// and the card then reads that the files went with it.
func TestCarrySourceRm(t *testing.T) {
	ctx := context.Background()
	st, home, bead := carryYard(t)
	askApprove(t, st, bead, looseAsk())
	drain(t, st, home, "boot-one")
	dir := filepath.Join(home, "sources", "loose")
	if _, err := os.Stat(filepath.Join(dir, "loose", "limits")); err != nil {
		t.Fatalf("the source's copies do not stand: %v", err)
	}
	seq := askApprove(t, st, bead, sourceRmAsk("loose"))
	if _, err := st.GetSource(ctx, "loose"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the approval left the source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "loose", "limits")); err != nil {
		t.Fatalf("the approval removed the copies before the server carried it out: %v", err)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryApproved {
		t.Fatalf("the record before the carrying out = %+v", rec)
	}
	drain(t, st, home, "boot-one")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the copies stand after the carrying out: %v", err)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone {
		t.Fatalf("the record = %+v", rec)
	}
	if lines := resolvedLines(t, st, bead, seq); !strings.Contains(lines, "loose: removed, and the files of its commands with it") {
		t.Fatalf("the carried-out card reads:\n%s", lines)
	}
}

// A source added again between the approval and the carrying out owns the
// directory the removal would take: the copies stay, and the record says why.
func TestCarrySourceRmLeavesASourceAddedAgain(t *testing.T) {
	ctx := context.Background()
	st, home, bead := carryYard(t)
	askApprove(t, st, bead, looseAsk())
	drain(t, st, home, "boot-one")
	seq := askApprove(t, st, bead, sourceRmAsk("loose"))
	if _, err := st.PutSource(ctx, "loose.yaml", looseDecl, "mckean"); err != nil {
		t.Fatal(err)
	}
	drain(t, st, home, "boot-one")
	if _, err := os.Stat(filepath.Join(home, "sources", "loose", "loose", "limits")); err != nil {
		t.Fatalf("the carrying out removed the copies of a source that stands again: %v", err)
	}
	rec := carryState(t, st, bead, seq)
	if rec.State != store.CarryFailed || !strings.Contains(rec.Result, "is in the yard again since the ask was approved") {
		t.Fatalf("the record = %+v", rec)
	}
}
