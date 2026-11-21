package eta_test

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"yardr.dev/yardr/internal/eta"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// countingAPI counts the bead listings asked of the yard behind it, so
// a test can tell a board was reused rather than computed again.
type countingAPI struct {
	store.API
	listings atomic.Int64
}

func (c *countingAPI) ListBeads(ctx context.Context, f store.BeadFilter) ([]store.Bead, error) {
	c.listings.Add(1)
	return c.API.ListBeads(ctx, f)
}

// boardYard is a counting yard with one travelling bead.
func boardYard(t *testing.T) (*countingAPI, context.Context) {
	t.Helper()
	base, _ := storetest.NewOwned(t)
	ctx := context.Background()
	if _, err := base.CreateBead(ctx, store.NewBead{Title: "the work", Stage: "new"}, "me"); err != nil {
		t.Fatal(err)
	}
	return &countingAPI{API: base}, ctx
}

var boardNow = time.Date(2099, 1, 1, 12, 0, 0, 0, time.UTC)

// The arrivals board is computed once while the yard stands at one
// head: the second read lists no bead, a caller's shaping of its copy
// never reaches the next caller, and a write is read fresh.
func TestArrivalsBoardReusedWhileHeadStands(t *testing.T) {
	t.Parallel()
	y, ctx := boardYard(t)

	first, err := eta.ArrivalsViaStore(ctx, y, "", boardNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 1 {
		t.Fatalf("first board = %+v", first)
	}
	second, err := eta.ArrivalsViaStore(ctx, y, "", boardNow)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second board = %+v, want the first's answer", second)
	}
	if got := y.listings.Load(); got != 1 {
		t.Errorf("two reads listed beads %d times at one head, want 1", got)
	}

	first.Rows[0].Title = "shaped by its caller"
	third, err := eta.ArrivalsViaStore(ctx, y, "", boardNow)
	if err != nil {
		t.Fatal(err)
	}
	if third.Rows[0].Title == "shaped by its caller" {
		t.Errorf("a caller's shaping reached the kept board: %+v", third.Rows[0])
	}

	if _, err := y.CreateBead(ctx, store.NewBead{Title: "more work", Stage: "new"}, "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := eta.ArrivalsViaStore(ctx, y, "", boardNow); err != nil {
		t.Fatal(err)
	}
	if got := y.listings.Load(); got != 2 {
		t.Errorf("read after a write listed beads %d times, want 2", got)
	}
}

// Concurrent reads of one board at one head share one computation.
func TestArrivalsBoardSharedWhileHeadStands(t *testing.T) {
	t.Parallel()
	y, ctx := boardYard(t)

	boards := make([]*eta.ArrivalBoard, 8)
	var wg sync.WaitGroup
	for i := range boards {
		wg.Go(func() {
			boards[i], _ = eta.ArrivalsViaStore(ctx, y, "", boardNow)
		})
	}
	wg.Wait()
	for i, b := range boards {
		if b == nil || len(b.Rows) != 1 {
			t.Fatalf("board %d = %+v", i, b)
		}
		if !reflect.DeepEqual(boards[0], b) {
			t.Errorf("board %d differs from the first", i)
		}
	}
	if got := y.listings.Load(); got != 1 {
		t.Errorf("concurrent reads listed beads %d times, want 1 (one shared computation)", got)
	}
}

// boardEvictRun numbers this test's runs in the binary: each run fills
// minutes later than the last one's, so a repeated run (-count=2) evicts
// the earlier run's boards first, and all of them before boardNow, so the
// parallel board tests' keeps evict these leftovers and never their own.
var boardEvictRun atomic.Int64

// Thirty-three distinct viewers ask for the arrivals board at one head in
// successive minutes: the cache keeps 32, the stalest minute's board is
// dropped, and asking it again computes it whole. Sequential: the boards'
// cache is shared across the package's tests, and the parallel ones run
// after the sequential ones, on these leftovers.
func TestArrivalsBoardEvictsStalestPastCap(t *testing.T) {
	y, ctx := boardYard(t)
	base := boardNow.Add(time.Duration(-100000+100*boardEvictRun.Add(1)) * time.Minute)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }
	for i := 0; i < 33; i++ {
		if _, err := eta.ArrivalsViaStore(ctx, y, fmt.Sprintf("viewer-%d", i), at(i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := y.listings.Load(); got != 33 {
		t.Fatalf("filling 33 viewers listed beads %d times, want 33", got)
	}
	// The 32 kept viewers reuse without listing; the stalest minute's
	// viewer asked again is computed whole.
	for i := 1; i < 33; i++ {
		if _, err := eta.ArrivalsViaStore(ctx, y, fmt.Sprintf("viewer-%d", i), at(i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := y.listings.Load(); got != 33 {
		t.Errorf("re-asking the 32 kept viewers listed beads %d times, want 33 (reused)", got)
	}
	board, err := eta.ArrivalsViaStore(ctx, y, "viewer-0", at(0))
	if err != nil {
		t.Fatal(err)
	}
	if got := y.listings.Load(); got != 34 {
		t.Errorf("the dropped viewer listed beads %d times, want 34 (computed whole)", got)
	}
	if len(board.Rows) != 1 {
		t.Errorf("the dropped viewer's board = %+v, want the yard's row", board)
	}
}

// The flows board stands while the head stands, as the arrivals board
// does, and a shaping of one copy (a depot's error) reaches no other.
func TestFlowsBoardReusedWhileHeadStands(t *testing.T) {
	t.Parallel()
	y, ctx := boardYard(t)

	first, err := eta.FlowsViaStore(ctx, y, "", boardNow)
	if err != nil {
		t.Fatal(err)
	}
	second, err := eta.FlowsViaStore(ctx, y, "", boardNow)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second board = %+v, want the first's answer", second)
	}
	if got := y.listings.Load(); got != 1 {
		t.Errorf("two reads listed beads %d times at one head, want 1", got)
	}
	if len(first.Depots) > 0 {
		first.Depots[0].Error = "shaped by its caller"
		third, err := eta.FlowsViaStore(ctx, y, "", boardNow)
		if err != nil {
			t.Fatal(err)
		}
		if third.Depots[0].Error == "shaped by its caller" {
			t.Errorf("a caller's shaping reached the kept board: %+v", third.Depots[0])
		}
	}
}
