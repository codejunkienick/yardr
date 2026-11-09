package dispatch

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"yardr.dev/yardr/internal/diskkeep"
	"yardr.dev/yardr/internal/store"
)

// Rotated GOCACHE directories (internal/diskkeep) are the yard's to remove
// when their grace ends, and the daemon does it. A landing's keeping removes
// them only under disk_keep_gb free and only where a landing starts one, so a
// directory could stand for hours beside the live cache after anything used
// it, and a gate that met its free-space floor in that time was stopped by it.
// The pass notes when the earliest recorded directory is due, from the record
// in the store (so a daemon that was restarted goes by what is there, as
// backups does), and Run wakes for it (nextDue).
//
// A removal of 20 GiB takes tens of seconds and, on a disk that is failing,
// can take far longer, so it is not the pass's: at the due the pass starts
// one goroutine that sweeps (diskkeep.Sweep), as route hands a start to a
// worker (starts.go), and goes on. Nothing waits for it. A pass that finds it
// running leaves it, its end wakes the loop for the pass that notes what is
// due next, and a daemon that stops or replaces itself in the middle of a
// removal leaves the record, which is dropped only once the directory is
// gone, so the next daemon removes what is left.

// gocacheKeeping is the daemon's removal of rotated GOCACHE directories.
type gocacheKeeping struct {
	// due is when the pass next has a sweep to start, as of the last pass; nil
	// with none: nothing recorded, a sweep in flight (its end wakes Run), or
	// nothing but what the last sweep left. The pass owns it, as it does its
	// siblings in Dispatcher.
	due *time.Time

	// mu guards the rest, which the sweep's goroutine writes and the pass
	// reads.
	mu sync.Mutex
	// done is open while a sweep is in flight, and closed when it ends.
	done chan struct{}
	// down is the outage of a sweep that failed or left a directory past its
	// grace, by Backoff doubling to BackoffMax as backups' is; nil while the
	// sweeps leave nothing.
	down *outage
	// lives are the records that named the live cache at the last sweep. A
	// sweep never removes the live cache, so such a record (a pointer moved
	// back to a directory that was rotated away from) is no due: counted as
	// one it would be past at every pass, and put the loop on Run's one-second
	// floor for a removal that never comes. It counts again once it is
	// recorded anew, which a later rotation away from the directory does.
	lives []store.GOCACHEDir
	// liveOf tells the live GOCACHE; nil asks go (diskkeep.LiveGOCACHE). A
	// test sets it, so that no go runs.
	liveOf func() (string, error)
}

// gocacheOld is the pass's step of the removal: it notes when the earliest
// recorded directory is due, and starts a sweep when one already is.
func (d *Dispatcher) gocacheOld(ctx context.Context) {
	g := &d.gocache
	olds, err := d.Store.GOCACHEOldDirs(ctx)
	if err != nil {
		// The next pass tries again: a directory goes late rather than not.
		d.Log.Warn("rotated GOCACHE directories", "err", err)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	olds = slices.DeleteFunc(olds, g.isLive)
	due, ok := diskkeep.Due(olds)
	now := d.now()
	switch {
	case !ok:
		g.due, g.down = nil, nil
	case g.done != nil:
		g.due = nil
	case g.down != nil && now.Before(g.down.until):
		// A sweep that failed holds the others back to its retry, at most
		// BackoffMax away, so that a directory that cannot be removed is not
		// tried at every pass.
		until := g.down.until
		g.due = &until
	case now.Before(due):
		g.due = &due
	default:
		g.due, g.done = nil, make(chan struct{})
		go d.sweepGOCACHE(ctx, g.done, d.poke)
	}
}

// isLive reports whether o is one of the records of the live cache the last
// sweep saw.
func (g *gocacheKeeping) isLive(o store.GOCACHEDir) bool {
	return slices.ContainsFunc(g.lives, func(l store.GOCACHEDir) bool { return l.Dir == o.Dir && l.At.Equal(o.At) })
}

// sweepGOCACHE is one sweep on its goroutine: it removes what is past its
// grace and leaves the pass what it needs of how that went, under g.mu. poke
// wakes Run's loop (nil outside Run, where the pass is run by hand).
func (d *Dispatcher) sweepGOCACHE(ctx context.Context, done chan struct{}, poke func()) {
	g := &d.gocache
	err := d.sweep(ctx)
	g.mu.Lock()
	g.done = nil
	switch {
	case err == nil:
		g.down = nil
	case ctx.Err() != nil:
		// The daemon was told to stop: the next one removes what is left.
	case g.down == nil:
		g.down = &outage{delay: d.Backoff}
		d.Log.Warn("rotated GOCACHE directories past their grace not removed", "err", err)
	default:
		g.down.delay = min(2*g.down.delay, d.BackoffMax)
	}
	if g.down != nil {
		g.down.until = d.now().Add(g.down.delay)
	}
	g.mu.Unlock()
	close(done)
	if poke != nil {
		poke()
	}
}

// sweep asks go which cache is live, runs diskkeep's removal, and says why a
// directory past its grace still stands: nil when none does.
func (d *Dispatcher) sweep(ctx context.Context) error {
	g := &d.gocache
	liveOf := g.liveOf
	if liveOf == nil {
		liveOf = diskkeep.LiveGOCACHE
	}
	live, err := liveOf()
	if err != nil {
		return fmt.Errorf("the live GOCACHE cannot be told: %w", err)
	}
	at := d.now()
	rep, err := diskkeep.Sweep(ctx, d.Store, live, at)
	if err != nil {
		return err
	}
	if len(rep.Removed)+len(rep.Adopted) > 0 {
		d.Log.Info("rotated GOCACHE directories", "removed", rep.Removed, "adopted", rep.Adopted)
	}
	olds, err := d.Store.GOCACHEOldDirs(ctx)
	if err != nil {
		return err
	}
	var lives, rest []store.GOCACHEDir
	for _, o := range olds {
		if o.Dir == live {
			lives = append(lives, o)
		} else {
			rest = append(rest, o)
		}
	}
	g.mu.Lock()
	g.lives = lives
	g.mu.Unlock()
	if due, ok := diskkeep.Due(rest); ok && !at.Before(due) {
		return fmt.Errorf("a recorded directory stands past its grace: %s", strings.Join(rep.Errors, "; "))
	}
	return nil
}
