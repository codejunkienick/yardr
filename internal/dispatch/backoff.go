package dispatch

import (
	"context"
	"fmt"
	"time"

	"yardr.dev/yardr/internal/store"
)

// returnsPage bounds the events backingOff reads per bead: the returns it
// counts are the latest, and a bead with more events than this since it
// last entered its stage otherwise is retried as if it had this many.
const returnsPage = 200

// backingOff reports whether b is to wait before being dispatched again:
// its flow has sent it back to the stage it is in n times in a row (an
// advance from and to b.Stage, as open -> open on failed when integrating
// a train fails for a reason no child fixes), and the last was less than
// Backoff doubled n-1 times, up to BackoffMax, ago. The count is read from
// the bead's events, so a restarted daemon keeps waiting, and it starts
// over when the bead enters the stage any other way or a human un-holds
// it. Each wait is logged once on the bead as a backoff event.
func (d *Dispatcher) backingOff(ctx context.Context, b store.Bead) (bool, error) {
	evs, err := d.Store.Events(ctx, b.ID, returnsPage)
	if err != nil {
		return false, err
	}
	n, last := 0, time.Time{}
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if e.Kind == "unheld" {
			break
		}
		if e.Kind != "advanced" {
			continue
		}
		if e.Data["from"] != b.Stage || e.Data["to"] != b.Stage {
			break
		}
		if n == 0 {
			last = e.At
		}
		n++
	}
	if n == 0 {
		return false, nil
	}
	wait := min(d.Backoff<<min(n-1, 30), d.BackoffMax)
	until := last.Add(wait)
	if !d.now().Before(until) {
		return false, nil
	}
	d.reportOnce(ctx, b, "backoff", fmt.Sprintf("sent back to %s %d times in a row; next try at %s", b.Stage, n, until.UTC().Format(time.RFC3339)))
	return true, nil
}
