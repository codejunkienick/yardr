package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A note told to a session that is still live is that session's to ack, even
// to the session that is the bead's own: a session's tells stay its own while
// it lives. No call of the store reaches that state, which ends a bead's
// session as the bead leaves it (endSession), so the first session's record
// is put back to running by hand, as a session the daemon has not yet
// reaped would read.
func TestAckNoteToASessionStillLiveIsItsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(ctx) })
	if err := s.PutGroup(ctx, Group{Name: "g", Runner: "fake", Limit: 5}, "me"); err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBead(ctx, NewBead{Title: "x", Stage: "new"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Claim(ctx, b.ID, "g", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.AddNotedAs(ctx, b.ID, "narrower now", "yardmaster", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndSession(ctx, first.ID, "died", "x"); err != nil {
		t.Fatal(err)
	}
	second, err := s.Claim(ctx, b.ID, "g", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.changeSession(first.ID, func(_ *wtx, sess *Session) error {
		sess.State, sess.EndedAt = "running", nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AckNote(ctx, b.ID, n.Seq, second.ID, "builder"); err == nil || !strings.Contains(err.Error(), "ack it as that session") {
		t.Fatalf("the bead's live session acked a note told to a session still live: %v", err)
	}
	if notes, _ := s.Notes(ctx, b.ID); notes[0].Acked || notes[0].Notify != first.ID {
		t.Fatalf("Notes = %+v, want the note unread and still %s's", notes, first.ID)
	}
	// The session it was told to still acks it.
	if got, err := s.AckNote(ctx, b.ID, n.Seq, first.ID, "builder"); err != nil || !got.Acked || got.Notify != first.ID {
		t.Fatalf("the session the note was told to acked it: %+v, %v", got, err)
	}
}
