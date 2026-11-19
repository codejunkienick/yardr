package store

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// The queue is read whole on every call while the yard stands at one
// head: the second read decodes no bead (beadReads), the answers agree,
// and a write moves the head and is read fresh.
func TestUserAsksReusedWhileHeadStands(t *testing.T) {
	t.Parallel()
	y := decisionYard(t)
	seq := y.ask(t, linksAsk(), "yardmaster")

	first, err := y.Store.UserAsks(y.ctx, "mckean", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Open) != 1 || len(first.Decided) != 0 {
		t.Fatalf("first queue = %+v", first)
	}
	decoded := y.beadReads.Load()
	second, err := y.Store.UserAsks(y.ctx, "mckean", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second queue = %+v, want the first's answer", second)
	}
	if got := y.beadReads.Load(); got != decoded {
		t.Errorf("second read decoded %d beads at one head, want none", got-decoded)
	}

	// A note moves the head: the next read computes again, and a
	// shaping of one caller's copy never reaches the next caller.
	if err := y.Store.AddNote(y.ctx, y.bead, "still working", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	third, err := y.Store.UserAsks(y.ctx, "mckean", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if got := y.beadReads.Load(); got <= decoded {
		t.Errorf("read after a write decoded %d beads, want a fresh computation", got-decoded)
	}
	if len(third.Open) != 1 {
		t.Fatalf("third queue = %+v", third)
	}
	third.Open[0].Question = "shaped by its caller"
	fourth, err := y.Store.UserAsks(y.ctx, "mckean", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Open[0].Question == "shaped by its caller" {
		t.Errorf("a caller's shaping reached the kept queue: %+v", fourth.Open[0])
	}

	y.answer(t, Answer{Ask: seq, Option: 1}, "mckean")
	fifth, err := y.Store.UserAsks(y.ctx, "mckean", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if len(fifth.Open) != 0 || len(fifth.Decided) != 1 {
		t.Errorf("queue after the answer = %+v, want the ask decided", fifth)
	}
}

// Concurrent reads of one queue at one head share one computation: the
// yard's beads are decoded once for all of them, and every caller reads
// the same queue.
func TestUserAsksSharedWhileHeadStands(t *testing.T) {
	t.Parallel()
	y := decisionYard(t)
	y.ask(t, linksAsk(), "yardmaster")

	decoded := y.beadReads.Load()
	queues := make([]*AskQueue, 8)
	var wg sync.WaitGroup
	for i := range queues {
		wg.Go(func() {
			queues[i], _ = y.Store.UserAsks(y.ctx, "mckean", "mckean")
		})
	}
	wg.Wait()
	for i, q := range queues {
		if q == nil || len(q.Open) != 1 {
			t.Fatalf("queue %d = %+v", i, q)
		}
		if !reflect.DeepEqual(queues[0], q) {
			t.Errorf("queue %d differs from the first", i)
		}
	}
	// One computation lists the yard's one bead; eight unshared ones
	// would list it eight times.
	if got := y.beadReads.Load() - decoded; got != 1 {
		t.Errorf("concurrent reads decoded %d beads, want 1 (one shared computation)", got)
	}
}

// A put-aside ask comes back after its day with no write at all, so a
// kept queue stops standing at its return even while the head stands.
func TestUserAsksAsideReturnsWhileHeadStands(t *testing.T) {
	// Sequential: the package's fake clock.
	pinClockFixed(t, askNow)
	y := decisionYard(t)
	pinClock(t, &y.now)
	seq := y.ask(t, linksAsk(), "yardmaster")
	if err := y.Store.DeferAsk(y.ctx, y.bead, seq, "mckean"); err != nil {
		t.Fatal(err)
	}
	kept, err := y.Store.UserAsks(y.ctx, "mckean", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Open) != 0 || len(kept.Decided) != 1 {
		t.Fatalf("queue with the ask aside = %+v", kept)
	}
	y.tick(25 * time.Hour) // past the return, and no write since
	back, err := y.Store.UserAsks(y.ctx, "mckean", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Open) != 1 || len(back.Decided) != 0 {
		t.Errorf("queue after the return = %+v, want the ask open again", back)
	}
}

// waitingCacheYard is a yard with one bead parked in ready, a manual
// stage of ada's group people, under the depot shop's flow.
func waitingCacheYard(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.AddUser(ctx, "ada", "ada")
	must(err)
	must(s.SetSetting(ctx, OperatorSetting, "ada", "ada"))
	must(s.PutDepot(ctx, Depot{Name: "shop", Base: "origin/main"}, "ada"))
	f, err := ParseFlow("backlog -> building\nbuilding -> ready on done\nready -> merged on done\nmerged terminal\n")
	must(err)
	f.Name = "shop"
	_, err = s.PutFlow(ctx, *f, "ada")
	must(err)
	must(s.SetFlowPointer(ctx, "shop", "", "shop", "ada"))
	must(s.PutGroup(ctx, Group{Name: "people", Runner: "manual", Members: []string{"ada"}}, "ada"))
	_, err = s.AddRoute(ctx, Route{Stage: "ready", Group: "people"}, "ada")
	must(err)
	_, err = s.CreateBead(ctx, NewBead{ID: "pk-ready", Title: "Publish the notes", Stage: "ready", Depot: "shop"}, "ada")
	must(err)
	return s
}

// The parked rows stand while the head stands, as the queue does: the
// second read decodes no bead, and a write is read fresh.
func TestWaitingReusedWhileHeadStands(t *testing.T) {
	t.Parallel()
	s := waitingCacheYard(t)
	ctx := context.Background()

	first, err := s.Waiting(ctx, "ada", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Bead != "pk-ready" {
		t.Fatalf("first rows = %+v", first)
	}
	decoded := s.beadReads.Load()
	second, err := s.Waiting(ctx, "ada", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second rows = %+v, want the first's answer", second)
	}
	if got := s.beadReads.Load(); got != decoded {
		t.Errorf("second read decoded %d beads at one head, want none", got-decoded)
	}
	if err := s.AddNote(ctx, "pk-ready", "still parked", "ada"); err != nil {
		t.Fatal(err)
	}
	third, err := s.Waiting(ctx, "ada", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.beadReads.Load(); got <= decoded {
		t.Errorf("read after a write decoded %d beads, want a fresh computation", got-decoded)
	}
	if len(third) != 1 || third[0].Bead != "pk-ready" {
		t.Errorf("rows after the write = %+v", third)
	}
}
