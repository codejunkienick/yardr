// The served device calls, counted by method and device, as the operator
// reads them: recent calls per minute, off the server's log. The socket's
// calls are not counted: they carry no caller (api.call).
package rpc

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// A callMeter counts served device calls into buckets of one minute, by
// method, device and whether the call came down the edge's link. On the
// first counted call past a minute boundary it logs one Info line with
// the finished minutes' tables and drops their buckets; minutes that
// held no calls log nothing. There is no ticker: the next call carries
// the last minute's line, a minute late at most.
type callMeter struct {
	mu  sync.Mutex
	now func() time.Time
	log func() *slog.Logger
	// buckets holds the minutes not yet logged, the current one among
	// them: every count drops all but it, so it never piles up.
	buckets map[time.Time]map[callCount]int
}

// A callCount is one counted call's shape: the method's name, the name
// of the device that made it, and whether it came down the link.
type callCount struct {
	method, device string
	edge           bool
}

func newCallMeter(log func() *slog.Logger) *callMeter {
	return &callMeter{now: time.Now, log: log, buckets: map[time.Time]map[callCount]int{}}
}

// count counts one served call of the method name from the device, which
// came down the edge's link when edge is set.
func (m *callMeter) count(name, device string, edge bool) {
	minute := m.now().Truncate(time.Minute)
	m.mu.Lock()
	bucket := m.buckets[minute]
	if bucket == nil {
		bucket = map[callCount]int{}
		m.buckets[minute] = bucket
	}
	bucket[callCount{name, device, edge}]++
	var done []finishedMinute
	for finished := range m.buckets {
		if finished.Before(minute) {
			done = append(done, finishedMinute{finished, m.buckets[finished]})
			delete(m.buckets, finished)
		}
	}
	m.mu.Unlock()
	if len(done) == 0 {
		return
	}
	log := m.log()
	if log == nil {
		return
	}
	log.Info("device calls served by method and device", "minutes", minuteTable(done))
}

// finishedMinute is one minute's counts to log.
type finishedMinute struct {
	minute time.Time
	counts map[callCount]int
}

// minuteTable is the finished minutes' tables as one line of the log:
// each minute with its per-method-and-device counts. The log line's own
// timestamp carries the date; the table names the minutes of the hour.
func minuteTable(done []finishedMinute) string {
	slices.SortFunc(done, func(a, b finishedMinute) int { return a.minute.Compare(b.minute) })
	minutes := make([]string, 0, len(done))
	for _, d := range done {
		counts := slices.SortedFunc(maps.Keys(d.counts), func(a, b callCount) int {
			if c := strings.Compare(a.method, b.method); c != 0 {
				return c
			}
			if c := strings.Compare(a.device, b.device); c != 0 {
				return c
			}
			switch {
			case a.edge == b.edge:
				return 0
			case !a.edge:
				return -1
			default:
				return 1
			}
		})
		entries := make([]string, 0, len(counts))
		for _, c := range counts {
			entry := c.method + " " + c.device
			if c.edge {
				entry += " via edge"
			}
			entries = append(entries, fmt.Sprintf("%s=%d", entry, d.counts[c]))
		}
		minutes = append(minutes, d.minute.Format("15:04")+" "+strings.Join(entries, ", "))
	}
	return strings.Join(minutes, "; ")
}
