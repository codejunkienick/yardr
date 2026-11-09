package dispatch

import "time"

// nextDue is the earliest of the times the last pass noted for Run to wake at
// between passes: a timer, a look at a pane or a prompt, a push, a siding to
// fold, a rotated GOCACHE directory out of its grace. Nil when nothing is due
// before the next interval.
func (d *Dispatcher) nextDue() *time.Time {
	var next *time.Time
	for _, due := range []*time.Time{d.timerDue, d.lookDue, d.pushDue, d.sidingDue, d.gocache.due} {
		if due != nil && (next == nil || due.Before(*next)) {
			next = due
		}
	}
	return next
}
