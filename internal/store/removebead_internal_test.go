package store

import (
	"context"
	"strings"
	"testing"
)

// An ask another yard brought here cites its about-beads as a snapshot
// (peerAskBead), not as a note of this yard's: that cite holds a removal
// too, naming the bead the ask was about there.
func TestRemoveBeadForeignAskCite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir() + "/y.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(ctx) })
	if err := s.SetSetting(ctx, OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	mem, err := s.Remember(ctx, NewMemory{Title: "cited from away", Scope: "all"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	err = s.write(func(w *wtx) error {
		return put(w.Tx, bPeerAsks, join("peerkey", "msg1"), foreignAsk{
			Message: "msg1", Key: "peerkey", Bead: "yard-q1", Seq: 7,
			About: []peerAskBead{{ID: mem.ID, Title: mem.Title, Stage: mem.Stage}},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveBead(ctx, mem.ID, "jo"); err == nil ||
		!strings.Contains(err.Error(), "ask 7 on yard-q1") {
		t.Fatalf("remove a memory a foreign ask cites: %v", err)
	}
	if _, err := s.GetBead(ctx, mem.ID); err != nil {
		t.Fatalf("refused removal changed the memory: %v", err)
	}
	// Once the foreign ask is settled, it no longer holds the memory.
	err = s.write(func(w *wtx) error {
		return put(w.Tx, bPeerAsks, join("peerkey", "msg1"), foreignAsk{
			Message: "msg1", Key: "peerkey", Bead: "yard-q1", Seq: 7,
			About:   []peerAskBead{{ID: mem.ID, Title: mem.Title, Stage: mem.Stage}},
			Outcome: RefusedAskAnswered,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveBead(ctx, mem.ID, "jo"); err != nil {
		t.Fatalf("remove after foreign ask settled: %v", err)
	}
}
