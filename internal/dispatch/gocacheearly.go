package dispatch

import (
	"context"
	"time"

	"yardr.dev/yardr/internal/diskkeep"
	"yardr.dev/yardr/internal/store"
)

// A rotated GOCACHE directory inside its grace is also taken while the disk is
// under the gate's floor and no go process can hold it (diskkeep.Sweep says
// which, and reads the process table only then). The disk falls under the floor
// at any time, not at the end of a grace, and a builder's gate starts no
// keeping, so the pass looks: while every recorded directory is inside its
// grace (when one is past it, the sweep that removes it does all of this too) it
// asks diskkeep.Early, which reads no process table, and starts the sweep of the
// grace's end.
//
// A pass runs on every write, many times a second, and a sweep that finds the
// directory held has read the process table for nothing, so the looks are
// spaced: the next waits Backoff after the end of the last, and after one that
// ended in errors twice the wait it ended, up to BackoffMax, as a sweep past its
// grace does. The wait is its own state (gocacheKeeping.early) and not the
// outage of that sweep: a process table that cannot be read must not hold back
// the removal at a grace's end. It is a due for Run's timer while the disk stays
// short, so that a yard with nothing else to wake it looks again.

// lookGOCACHE is the pass's look at the disk, with g.mu held. It starts a sweep
// when Early says there is a directory to take and the last look's wait is over,
// and else notes when that wait ends.
func (d *Dispatcher) lookGOCACHE(ctx context.Context, olds []store.GOCACHEDir, now time.Time) {
	g := &d.gocache
	early := g.earlyOf
	if early == nil {
		early = diskkeep.Early
	}
	if !early(olds, now) {
		return
	}
	if now.Before(g.early.until) {
		if until := g.early.until; g.due == nil || until.Before(*g.due) {
			g.due = &until
		}
		return
	}
	g.due, g.done = nil, make(chan struct{})
	go d.sweepGOCACHE(ctx, g.done, d.poke, true)
}

// lookEnded sets the wait before the next look, with g.mu held: Backoff after a
// look that went well, and after one that failed twice the wait it ended, up to
// BackoffMax.
func (d *Dispatcher) lookEnded(failed bool) {
	g := &d.gocache
	delay := d.Backoff
	if failed {
		delay = min(2*max(g.early.delay, d.Backoff), d.BackoffMax)
	}
	g.early = outage{delay: delay, until: d.now().Add(delay)}
}
