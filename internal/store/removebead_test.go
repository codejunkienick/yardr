package store_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// removeYard is a store whose operator is jo, with a helper that remembers
// a memory as jo.
type removeYard struct {
	api store.API
	ctx context.Context
}

func newRemoveYard(t *testing.T) *removeYard {
	t.Helper()
	y := &removeYard{api: storetest.New(t), ctx: context.Background()}
	if err := y.api.SetSetting(y.ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	return y
}

func (y *removeYard) remember(t *testing.T, title, body string) *store.Bead {
	t.Helper()
	m, err := y.api.Remember(y.ctx, store.NewMemory{Title: title, Body: body, Scope: "all"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (y *removeYard) mustRemove(t *testing.T, id string) *store.BeadRemoval {
	t.Helper()
	r, err := y.api.RemoveBead(y.ctx, id, "jo")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (y *removeYard) refused(t *testing.T, id, want string) {
	t.Helper()
	_, err := y.api.RemoveBead(y.ctx, id, "jo")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("remove %s: %v, want a refusal naming %q", id, err, want)
	}
	if _, err := y.api.GetBead(y.ctx, id); err != nil {
		t.Fatalf("refused removal of %s changed it: %v", id, err)
	}
}

// A memory with notes and a file removes whole: its row, handle and notes
// are gone from every listing, its events stay with their text redacted,
// and the file its note carried is freed by the next prune.
func TestRemoveBeadRemovesAMemoryWhole(t *testing.T) {
	t.Parallel()
	api, s := storetest.NewOwned(t)
	ctx := context.Background()
	if err := api.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	mem, err := api.Remember(ctx, store.NewMemory{Title: "zebra stripes", Body: "the zebra count is twelve", Scope: "all"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.AddNote(ctx, mem.ID, "a first note", "jo"); err != nil {
		t.Fatal(err)
	}
	blob, err := api.PutBlob(ctx, []byte("a picture of the zebra"))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.AddNote(ctx, mem.ID, "a note with a file", "jo", store.BlobFile{Hash: blob.Hash}); err != nil {
		t.Fatal(err)
	}
	title, body := "zebra stripes, counted", "the zebra count is thirteen"
	if _, err := api.UpdateBead(ctx, mem.ID, store.BeadPatch{Title: &title, Body: &body}, store.Expect{}, "jo"); err != nil {
		t.Fatal(err)
	}
	before, err := api.Events(ctx, mem.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 4 { // created, noted, noted, updated
		t.Fatalf("events before removal = %d, want 4: %+v", len(before), before)
	}

	r, err := api.RemoveBead(ctx, mem.ID, "jo")
	if err != nil {
		t.Fatal(err)
	}
	if r.Bead != mem.ID || r.Notes != 2 || r.EventsRedacted != 2 {
		t.Fatalf("removal = %+v, want the bead, 2 notes and 2 events redacted", r)
	}

	// The row, the handle and the notes are gone, to every read.
	if _, err := api.GetBead(ctx, mem.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removed bead reads back: %v", err)
	}
	if _, err := api.GetBead(ctx, mem.UUID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removed bead reads back by uuid: %v", err)
	}
	if _, err := api.Notes(ctx, mem.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("notes of a removed bead: %v", err)
	}
	all, err := api.ListBeads(ctx, store.BeadFilter{All: true, Type: store.TypeMemory})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		if b.ID == mem.ID {
			t.Fatalf("removed bead still lists: %+v", all)
		}
	}
	if got, err := api.Recall(ctx, "", "zebra"); err != nil || len(got) != 0 {
		t.Fatalf("recall after removal = %+v, %v", got, err)
	}

	// Its events stay, in their places, with the bead's words redacted and
	// nothing else of them changed.
	after, err := api.EventsSince(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var kept []store.Event
	for _, e := range after {
		if e.Bead != nil && *e.Bead == mem.UUID {
			kept = append(kept, e)
		}
	}
	if len(kept) != len(before) {
		t.Fatalf("events after removal = %d, want the %d that were there", len(kept), len(before))
	}
	for i, e := range kept {
		if e.Seq != before[i].Seq || e.Kind != before[i].Kind || e.Actor != before[i].Actor {
			t.Fatalf("event %d changed past its text: %+v, was %+v", i, e, before[i])
		}
		for k, v := range e.Data {
			if text, ok := v.(string); ok && strings.Contains(text, "zebra") {
				t.Fatalf("event %d (%s) still carries the bead's words in %s: %q", i, e.Kind, k, text)
			}
		}
		if was, ok := e.Data["was"].(map[string]any); ok {
			for k, v := range was {
				if text, ok := v.(string); ok && strings.Contains(text, "zebra") {
					t.Fatalf("event %d (%s) still carries the bead's old words in was.%s: %q", i, e.Kind, k, text)
				}
			}
		}
	}
	if kept[0].Data["title"] != "[removed]" || kept[0].Data["type"] != store.TypeMemory {
		t.Fatalf("created event after removal = %+v", kept[0].Data)
	}
	// The kind index still finds them.
	created, err := api.EventsOfKind(ctx, "created", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range created {
		if e.Seq == kept[0].Seq {
			found = true
		}
	}
	if !found {
		t.Fatal("the created event left its kind index")
	}

	// The file the note carried is named by nothing now, and the prune
	// frees it past its grace.
	old := time.Now().Add(-2 * store.BlobGrace)
	if err := os.Chtimes(s.BlobPath(blob.Hash), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Prune(ctx, store.Retention{}, "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.BlobPath(blob.Hash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the removed note's blob is still there: %v", err)
	}

	// A closed memory removes the same way: close keeps it, rm does not,
	// and the close reason is redacted from its closed event.
	closed := newRemoveYard(t)
	cm := closed.remember(t, "shut away", "kept by a close")
	if _, err := closed.api.CloseBead(closed.ctx, cm.ID, "secret close reason", store.Expect{}, "jo"); err != nil {
		t.Fatal(err)
	}
	closed.mustRemove(t, cm.ID)
	if _, err := closed.api.GetBead(closed.ctx, cm.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("closed memory after removal: %v", err)
	}
	closedEvs, err := closed.api.EventsSince(closed.ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range closedEvs {
		if e.Bead != nil && *e.Bead == cm.UUID && e.Kind == "closed" {
			if e.Data["reason"] != "[removed]" {
				t.Fatalf("closed event after removal = %+v, want redacted reason", e.Data)
			}
		}
	}

	// Removing a memory that carried an ask on itself invalidates the
	// head-keyed UserAsks cache even though removal writes no new event,
	// and drops any issue link so another bead can track that URL.
	extra := newRemoveYard(t)
	linked := extra.remember(t, "linked and asked", "")
	issueURL := "https://github.com/org/repo/issues/99"
	if _, err := extra.api.AddLink(extra.ctx, linked.ID, store.NewLink{Kind: store.LinkTracks, Source: "github", URL: issueURL}, "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := extra.api.AddAsk(extra.ctx, linked.ID, store.Ask{
		To: "jo", Question: "keep this?", Context: []string{"line one", "line two"},
	}, "jo"); err != nil {
		t.Fatal(err)
	}
	q, err := extra.api.UserAsks(extra.ctx, "jo", "jo")
	if err != nil || len(q.Open) != 1 {
		t.Fatalf("UserAsks before removal = %+v, %v", q, err)
	}
	extra.mustRemove(t, linked.ID)
	q, err = extra.api.UserAsks(extra.ctx, "jo", "jo")
	if err != nil || len(q.Open) != 0 {
		t.Fatalf("UserAsks after removal = %+v, %v (want stale cache cleared)", q, err)
	}
	if ls, err := extra.api.IssueLinks(extra.ctx, issueURL); err != nil || len(ls) != 0 {
		t.Fatalf("IssueLinks after removal = %+v, %v", ls, err)
	}
	other, err := extra.api.CreateBead(extra.ctx, store.NewBead{Title: "new tracker"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := extra.api.AddLink(extra.ctx, other.ID, store.NewLink{Kind: store.LinkTracks, Source: "github", URL: issueURL}, "jo"); err != nil {
		t.Fatalf("re-tracking URL after memory removal: %v", err)
	}
}

// Each thing that still names a memory refuses its removal, naming the
// holder, and clearing the holder lets the removal through.
func TestRemoveBeadRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// A dep edge either way.
	y := newRemoveYard(t)
	other, err := y.api.CreateBead(ctx, store.NewBead{Title: "the work"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	out := y.remember(t, "outgoing", "")
	if err := y.api.AddDep(ctx, out.ID, other.ID, store.DepBlocks, "", "", "jo"); err != nil {
		t.Fatal(err)
	}
	y.refused(t, out.ID, other.ID)
	in := y.remember(t, "incoming", "")
	if err := y.api.AddDep(ctx, other.ID, in.ID, store.DepBlocks, "", "", "jo"); err != nil {
		t.Fatal(err)
	}
	y.refused(t, in.ID, other.ID)
	if err := y.api.RemoveDep(ctx, other.ID, in.ID, store.DepBlocks, "", "", "jo"); err != nil {
		t.Fatal(err)
	}
	y.mustRemove(t, in.ID)

	// An ask on another bead that cites the memory: open and put-aside asks
	// refuse, while taking the ask back or answering it clears the holder.
	cited := y.remember(t, "cited", "")
	asker, err := y.api.CreateBead(ctx, store.NewBead{Title: "the question's bead"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	askSeq, err := y.api.AddAsk(ctx, asker.ID, store.Ask{To: "jo", Question: "which memory?",
		Context: []string{"line one", "line two"}, About: []string{cited.ID}}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	y.refused(t, cited.ID, asker.ID)
	if err := y.api.AddAnswer(ctx, asker.ID, store.Answer{Ask: askSeq, Later: true}, "jo"); err != nil {
		t.Fatal(err)
	}
	y.refused(t, cited.ID, asker.ID)
	if err := y.api.TakeBackAsk(ctx, asker.ID, askSeq, "no longer needed", "jo"); err != nil {
		t.Fatal(err)
	}
	y.mustRemove(t, cited.ID)

	answered := y.remember(t, "cited then answered", "")
	ansSeq, err := y.api.AddAsk(ctx, asker.ID, store.Ask{To: "jo", Question: "still needed?",
		Context: []string{"line one", "line two"}, About: []string{answered.ID}}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	y.refused(t, answered.ID, asker.ID)
	if err := y.api.AddAnswer(ctx, asker.ID, store.Answer{Ask: ansSeq, Words: "no"}, "jo"); err != nil {
		t.Fatal(err)
	}
	y.mustRemove(t, answered.ID)

	// A live session on the bead. A memory is never claimed, so the bead
	// is claimed as work and retyped under its session.
	worked, err := y.api.CreateBead(ctx, store.NewBead{Title: "claimed, then a memory"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	if err := y.api.PutGroup(ctx, store.Group{Name: "builders", Runner: "fake", Limit: 5}, "jo"); err != nil {
		t.Fatal(err)
	}
	sess, err := y.api.Claim(ctx, worked.ID, "builders", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	memType := store.TypeMemory
	if _, err := y.api.UpdateBead(ctx, worked.ID, store.BeadPatch{Type: &memType}, store.Expect{}, "jo"); err != nil {
		t.Fatal(err)
	}
	y.refused(t, worked.ID, sess.ID)

	// A child of a train.
	train, err := y.api.CreateBead(ctx, store.NewBead{Title: "the train", Type: store.TypeTrain}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	child := y.remember(t, "on a train", "")
	if err := y.api.AddDep(ctx, train.ID, child.ID, store.DepParent, "", "", "jo"); err != nil {
		t.Fatal(err)
	}
	y.refused(t, child.ID, train.ID)
	if err := y.api.RemoveDep(ctx, train.ID, child.ID, store.DepParent, "", "", "jo"); err != nil {
		t.Fatal(err)
	}
	y.mustRemove(t, child.ID)

	// No removal is refused twice over for the asking: an unknown bead is
	// not found, as any unknown bead's is.
	if _, err := y.api.RemoveBead(ctx, "mem-nope", "jo"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remove an unknown bead: %v", err)
	}
}

// A bead that is not a memory is refused, and so is anyone but the
// operator: the type gate and the operator's gate each leave the bead as
// it was.
func TestRemoveBeadTypeAndOperatorGates(t *testing.T) {
	t.Parallel()
	y := newRemoveYard(t)
	work, err := y.api.CreateBead(y.ctx, store.NewBead{Title: "real work", Type: "task"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.api.RemoveBead(y.ctx, work.ID, "jo"); err == nil ||
		!strings.Contains(err.Error(), "not a memory") || !strings.Contains(err.Error(), work.ID) {
		t.Fatalf("remove a task: %v", err)
	}
	if _, err := y.api.GetBead(y.ctx, work.ID); err != nil {
		t.Fatalf("refused removal changed the bead: %v", err)
	}

	mem := y.remember(t, "the operator's alone", "")
	if _, err := y.api.RemoveBead(y.ctx, mem.ID, "me"); err == nil ||
		!strings.Contains(err.Error(), "may remove a bead; this is me") {
		t.Fatalf("remove as a non-operator: %v", err)
	}
	if _, err := y.api.GetBead(y.ctx, mem.ID); err != nil {
		t.Fatalf("refused removal changed the memory: %v", err)
	}
	y.mustRemove(t, mem.ID)
}
