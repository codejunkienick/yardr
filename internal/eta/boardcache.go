package eta

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"yardr.dev/yardr/internal/store"
)

// The boards (ArrivalsViaStore, FlowsViaStore) are computed from the
// store on every call, and a device following a busy yard asks for both
// on every bead event, often overlapped from several devices: each fresh
// computation re-reads the travelling beads, their stays, sessions and
// holdings. The store's head (its newest event's seq) names the state a
// board was computed from, so a board is reused per yard and viewer
// exactly while the head stands, and concurrent calls asking for the
// same board share one computation. The boards print their times as
// rounded spans, so the minute of the asking instant stands in the key
// beside the head: two calls in one minute at one head ask the same
// question, and a later minute asks again without any freshness rule of
// the cache's own.
//
// A board is never handed out as kept: every caller receives a copy
// (cloneArrivalBoard, cloneFlowsBoard), so a caller shaping its copy
// (the listener holding a depot's flow error back from a device) never
// touches what the next caller reads. Errors are not kept: a failed
// computation is shared with the callers waiting on it and forgotten.

// boardKey is one board's key: the yard it is read from (the store API
// it is asked of, which names one yard in a process), who it is read
// as, the head it was computed at, and the minute it was asked at.
type boardKey struct {
	st     store.API
	viewer string
	head   uint64
	minute time.Time
}

// boardCache keeps the last board computed for each key and shares one
// computation among concurrent callers asking for the same key.
type boardCache[V any] struct {
	mu     sync.Mutex
	kept   map[boardKey]V
	flying map[boardKey]*boardFlight[V]
}

// maxKeptBoards bounds the boards a boardCache keeps across keys: on
// keeping past the cap the stalest heads are dropped (evict). 32 bounds
// the worst case near 128 MB of board-size answers while real yards keep
// a handful of viewers.
const maxKeptBoards = 32

// boardFlight is one computation in flight for a key: the callers
// asking for it wait on done and share its board.
type boardFlight[V any] struct {
	done chan struct{}
	val  V
	err  error
}

var (
	arrivalBoards boardCache[*ArrivalBoard]
	flowBoards    boardCache[*store.FlowsBoard]
)

// do answers key: the kept board, else one computation shared by every
// concurrent caller asking for it. The computation runs on the first
// caller's context, so its cancellation fails the callers sharing it
// alike.
func (c *boardCache[V]) do(ctx context.Context, key boardKey,
	compute func() (V, error), clone func(V) V) (V, error) {
	c.mu.Lock()
	if val, ok := c.kept[key]; ok {
		c.mu.Unlock()
		return clone(val), nil
	}
	if f, ok := c.flying[key]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			if f.err != nil {
				var zero V
				return zero, f.err
			}
			return clone(f.val), nil
		case <-ctx.Done():
			var zero V
			return zero, ctx.Err()
		}
	}
	f := &boardFlight[V]{done: make(chan struct{})}
	if c.flying == nil {
		c.flying = map[boardKey]*boardFlight[V]{}
	}
	c.flying[key] = f
	c.mu.Unlock()

	val, err := compute()

	c.mu.Lock()
	if err == nil {
		if c.kept == nil {
			c.kept = map[boardKey]V{}
		}
		// One board stands per yard and viewer: the new head's (or
		// minute's) board replaces the last, so a busy yard's moving
		// head leaves no pile of boards behind.
		for k := range c.kept {
			if k != key && k.st == key.st && k.viewer == key.viewer {
				delete(c.kept, k)
			}
		}
		c.kept[key] = val
		c.evict()
	}
	delete(c.flying, key)
	f.val, f.err = val, err
	close(f.done)
	c.mu.Unlock()
	if err != nil {
		var zero V
		return zero, err
	}
	return clone(val), nil
}

// evict drops kept boards with the smallest head, then minute, until the
// cache fits the cap, so however many distinct viewers ask, the server
// holds a bounded number of boards. The key already orders the entries,
// so no list of use is kept. Called with c.mu held.
func (c *boardCache[V]) evict() {
	for len(c.kept) > maxKeptBoards {
		var oldest boardKey
		first := true
		for k := range c.kept {
			if first || cmp.Or(cmp.Compare(k.head, oldest.head), k.minute.Compare(oldest.minute)) < 0 {
				oldest, first = k, false
			}
		}
		delete(c.kept, oldest)
	}
}

// ArrivalsViaStore answers the board at now: the rows beside the platforms
// in the order the beads travel, so a reader groups the rows by the served
// list without knowing the flows. The rows are the open beads still
// travelling — everything but the backlog — up to ArrivalLimit, in
// arrival order, with the expected arrival as the yard's printed range
// and the remark. It is what footplate draws: the yard computes once so
// the readers draw without computing (design footplate-8v5t, section 3).
// The board holds what viewer may see, "" for the yard itself.
// The order and the remark are the board's, ported in the eta package
// from signalbox src/board.ts.
//
// The board is arrivalsViaStore's, reused while the store stands at
// the head it was computed at (boardCache).
func ArrivalsViaStore(ctx context.Context, st store.API, viewer string, now time.Time) (*ArrivalBoard, error) {
	head, err := st.Head(ctx)
	if err != nil {
		// The head names the state a kept board stands at; a yard
		// that cannot say it is computed from, not failed.
		return arrivalsViaStore(ctx, st, viewer, now)
	}
	key := boardKey{st: st, viewer: viewer, head: head, minute: now.Truncate(time.Minute)}
	return arrivalBoards.do(ctx, key, func() (*ArrivalBoard, error) {
		return arrivalsViaStore(ctx, st, viewer, now)
	}, cloneArrivalBoard)
}

// FlowsViaStore answers the flows view at now: the depots each with its
// flows, each flow its stages in travel order, each stage its beads, each
// flow its landings of the trailing 24 hours, the per-depot Elsewhere rows,
// the unassigned rows, and the travelling totals. Its stages, beads and
// lamps are /flows and the panel as served; its unassigned rows are the open
// beads outside the backlog with no depot. An omitted viewer is the yard
// itself; a viewer's board holds only the beads they may see, landings
// among them.
//
// The board is flowsViaStore's, reused while the store stands at the
// head it was computed at (boardCache).
func FlowsViaStore(ctx context.Context, st store.API, viewer string, now time.Time) (*store.FlowsBoard, error) {
	head, err := st.Head(ctx)
	if err != nil {
		return flowsViaStore(ctx, st, viewer, now)
	}
	key := boardKey{st: st, viewer: viewer, head: head, minute: now.Truncate(time.Minute)}
	return flowBoards.do(ctx, key, func() (*store.FlowsBoard, error) {
		return flowsViaStore(ctx, st, viewer, now)
	}, cloneFlowsBoard)
}

// cloneArrivalBoard copies a board for one caller: its rows and
// platforms are the caller's own.
func cloneArrivalBoard(b *ArrivalBoard) *ArrivalBoard {
	if b == nil {
		return nil
	}
	return &ArrivalBoard{Rows: slices.Clone(b.Rows), Platforms: slices.Clone(b.Platforms)}
}

// cloneFlowsBoard copies a board for one caller, as cloneArrivalBoard
// does: every slice of rows down to a stage's beads is the caller's
// own, so shaping one (a depot's error, a stage's beads) leaves the
// kept board whole. A bead's entered-stage instant and a stage's seat
// counts are shared, as no caller writes through them.
func cloneFlowsBoard(b *store.FlowsBoard) *store.FlowsBoard {
	if b == nil {
		return nil
	}
	out := &store.FlowsBoard{Travelling: b.Travelling, Unassigned: slices.Clone(b.Unassigned)}
	out.Depots = make([]store.FlowsDepot, len(b.Depots))
	for i, d := range b.Depots {
		nd := store.FlowsDepot{Name: d.Name, Kind: d.Kind, Travelling: d.Travelling,
			Error: d.Error, Elsewhere: slices.Clone(d.Elsewhere)}
		nd.Flows = make([]store.FlowsFlow, len(d.Flows))
		for j, f := range d.Flows {
			nf := store.FlowsFlow{Name: f.Name, Type: f.Type, Source: f.Source,
				Landings: slices.Clone(f.Landings)}
			nf.Stages = make([]store.FlowsStage, len(f.Stages))
			for k, s := range f.Stages {
				nf.Stages[k] = store.FlowsStage{Stage: s.Stage, Group: s.Group,
					Human: s.Human, Lamp: s.Lamp, Terminal: s.Terminal,
					Limit: s.Limit, Taken: s.Taken, Beads: slices.Clone(s.Beads)}
			}
			nd.Flows[j] = nf
		}
		out.Depots[i] = nd
	}
	return out
}
