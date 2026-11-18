package mcp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/store"
)

// hideBead is a store policy that hides one bead from every viewer and
// lets everything else through.
type hideBead string

func (h hideBead) May(_ context.Context, _ *bolt.Tx, actor string, act store.Action, obj store.Object) error {
	if act == store.ActSee && obj.Kind == store.KindBead && obj.Name == string(h) {
		return fmt.Errorf("hidden from %s", actor)
	}
	return nil
}

// A note goes on a piece of work the person filed or may see, and
// leaves any question on it open: the question is still listed, and the
// piece's own view shows the note. A name that matches nothing and one
// that is not theirs to see are told alike.
func TestAddNote(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	s := svc(y, "")
	// On work they filed themselves.
	filed, err := s.AskForSomething(y.ctx, "Move the links", "")
	if err != nil {
		t.Fatal(err)
	}
	work := filed.Structured["work"].(string)
	res, err := s.AddNote(y.ctx, work, "the old place stays until Friday")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if res.Structured["work"] != work {
		t.Fatalf("noted as %+v", res.Structured)
	}
	// On work the crew filed for them at their request: theirs too.
	crew := y.work(t, "Paint the shed", "yardr", "ada")
	seq := y.ask(t, crew, choicesAsk("ada"))
	res, err = s.AddNote(y.ctx, crew, "the gate first")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if !strings.Contains(res.Text, fmt.Sprintf("Question %d is still open", seq)) {
		t.Fatalf("the note does not say the question is still open:\n%s", res.Text)
	}
	// The question is still listed, and the piece's view shows the
	// note with it.
	waiting, err := s.WhatWaitsForMe(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(waiting.Text, "Move the links now?") {
		t.Fatalf("the question is answered or gone:\n%s", waiting.Text)
	}
	one, err := s.WhatIsHappening(y.ctx, crew, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one.Text, "the gate first") {
		t.Fatalf("the piece's view shows no note:\n%s", one.Text)
	}
	// Nothing to note on, and nothing said, are refused before the
	// yard is read.
	for _, tc := range []struct{ work, note string }{
		{"", "the gate first"},
		{crew, ""},
		{crew, "  "},
	} {
		if _, err := s.AddNote(y.ctx, tc.work, tc.note); err == nil {
			t.Fatalf("AddNote(%q, %q) wrote a note", tc.work, tc.note)
		}
	}
	// A name that matches nothing, and one that is not theirs to
	// see, are told alike.
	y.owned.SetPolicy(hideBead(crew))
	for _, miss := range []string{"yd-nothing-here", crew} {
		if _, err := s.AddNote(y.ctx, miss, "the gate first"); err == nil ||
			err.Error() != "I can't find that piece of work among yours" {
			t.Fatalf("AddNote(%q) = %v", miss, err)
		}
	}
	// A title in place of the id is refused as no id, naming where the
	// id is read: it is not told the piece is not theirs.
	if _, err := s.AddNote(y.ctx, "Paint the shed", "the gate first"); err == nil ||
		err.Error() != "that is no work id: say the id of the piece, from what_is_happening" {
		t.Fatalf("AddNote(a title) = %v", err)
	}
}
