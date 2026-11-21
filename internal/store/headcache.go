package store

import (
	"context"
	"slices"
	"sync"
	"time"
)

// A headCache keeps the last answer computed for each key and shares one
// computation among concurrent callers asking for the same key: the
// device-board reads (UserAsks, Waiting) re-read and re-decode the store
// on every call, and a busy yard asks for the same viewer's answer many
// times between writes, often overlapped. The store's head (the newest
// event's seq) names the state an answer was computed from, so an answer
// is reused exactly while the head stands: every write that could change
// it appends an event and moves the head. There is no freshness rule of
// the cache's own beyond the one instant a computation itself reports
// (a put-aside ask coming back), at which the answer would change with
// no write at all.
//
// An answer is never handed out as kept: every caller receives a copy
// (clone), so a caller shaping its copy (the listener holding operator
// words back from a device) never touches what the next caller reads.
// Errors are not kept: a failed computation is shared with the callers
// waiting on it and then forgotten, so the next call tries again.
type headCache[K comparable, V any] struct {
	mu     sync.Mutex
	gen    uint64
	kept   map[K]headEntry[V]
	flying map[K]*headFlight[V]
}

// clear drops every kept answer and marks in-flight computations stale so
// they do not repopulate the cache: for a write that changes what a board
// reads without advancing the event head (RemoveBead).
func (c *headCache[K, V]) clear() {
	c.mu.Lock()
	c.gen++
	c.kept = nil
	c.mu.Unlock()
}

// maxKeptHeadAnswers bounds the answers a headCache keeps across keys:
// on keeping past the cap the stalest heads are dropped (evict). 32
// bounds the worst case near 128 MB of UserAsks-size answers while real
// yards keep a handful of viewers.
const maxKeptHeadAnswers = 32

// headEntry is one key's last answer: the head it was computed at, the
// instant it stops being the answer with no write (zero for none), and
// the answer as computed, never handed out directly.
type headEntry[V any] struct {
	head  uint64
	until time.Time
	val   V
}

// headFlight is one computation in flight for a key, at one head: the
// callers asking at the same head wait on done and share its answer.
// A caller at another head waits too, then computes its own: answers
// for two heads of one key never run together, so the kept answer is
// never replaced by an older head's.
type headFlight[V any] struct {
	head uint64
	gen  uint64
	done chan struct{}
	val  V
	err  error
}

// viewerKey is a board answer's key: whose queue it is, who reads it and
// who it is read as. The gate that refuses a reader runs on every call
// before the cache is consulted; the key only names the answer, so two
// actors reading one person's queue as one viewer share it.
type viewerKey struct {
	name, actor, viewer string
}

// do answers key at head: the kept answer while it stands (same head,
// and now before the instant the computation said it changes), else one
// computation shared by every concurrent caller at that head. compute
// returns the answer and the instant it next changes with no write at
// all (zero for none). clone copies the answer for the caller getting
// it. The computation runs on the first caller's context, so its
// cancellation fails the callers sharing it alike.
func (c *headCache[K, V]) do(ctx context.Context, key K, head uint64, now time.Time,
	compute func() (V, time.Time, error), clone func(V) V) (V, error) {
	for {
		c.mu.Lock()
		if e, ok := c.kept[key]; ok && e.head == head && (e.until.IsZero() || now.Before(e.until)) {
			val := e.val
			c.mu.Unlock()
			return clone(val), nil
		}
		if f, ok := c.flying[key]; ok {
			gen := c.gen
			c.mu.Unlock()
			select {
			case <-f.done:
				if f.head != head || f.gen != gen {
					continue // another head's or invalidated answer: ask again
				}
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
		f := &headFlight[V]{head: head, gen: c.gen, done: make(chan struct{})}
		if c.flying == nil {
			c.flying = map[K]*headFlight[V]{}
		}
		c.flying[key] = f
		c.mu.Unlock()

		val, until, err := compute()

		c.mu.Lock()
		if err == nil && c.gen == f.gen {
			if c.kept == nil {
				c.kept = map[K]headEntry[V]{}
			}
			// A computation that started at an older head than the
			// kept answer's never replaces it: its answer stands
			// for its head, and the newer one keeps standing.
			if e, ok := c.kept[key]; !ok || e.head <= head {
				c.kept[key] = headEntry[V]{head: head, until: until, val: val}
			}
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
}

// evict drops kept answers with the smallest head until the cache fits
// the cap, so however many distinct keys compute, the server holds a
// bounded number of answers. The head already orders the entries, so no
// list of use is kept. Called with c.mu held.
func (c *headCache[K, V]) evict() {
	for len(c.kept) > maxKeptHeadAnswers {
		var oldest K
		var oldestHead uint64
		first := true
		for k, e := range c.kept {
			if first || e.head < oldestHead {
				oldest, oldestHead, first = k, e.head, false
			}
		}
		delete(c.kept, oldest)
	}
}

// cloneAskQueue copies a queue for one caller: the rows are copied, so
// replacing a card or its words on the copy (userAsks shapes what a
// device is told) leaves the kept queue whole. The cards themselves are
// shared, as the shaping helpers copy a card before cutting it.
func cloneAskQueue(q *AskQueue) *AskQueue {
	if q == nil {
		return nil
	}
	return &AskQueue{Open: slices.Clone(q.Open), Decided: slices.Clone(q.Decided)}
}

// cloneWaiting copies parked rows for one caller, as cloneAskQueue
// copies a queue: the rows and their moves are the caller's own.
func cloneWaiting(rows []WaitingBead) []WaitingBead {
	out := slices.Clone(rows)
	for i := range out {
		out[i].Actions = slices.Clone(rows[i].Actions)
	}
	return out
}
