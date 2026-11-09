package dispatch_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/diskkeep"
	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// gocacheYard is a dispatcher over a throwaway store, in a home as the daemon
// is, with real caches on disk beside the live one it is told of and a clock
// the test sets. The sweep's goroutine reads that clock, so it is atomic.
type gocacheYard struct {
	t     *testing.T
	s     store.API
	d     *dispatch.Dispatcher
	log   *lockedWriter
	clock atomic.Pointer[time.Time]
	base  string
	live  string
	asked atomic.Int32                  // how often a sweep asked which cache is live
	fail  atomic.Bool                   // whether it is answered with an error
	hold  atomic.Pointer[chan struct{}] // when set, the answer waits until it is closed
}

func newGOCacheYard(t *testing.T) *gocacheYard {
	t.Helper()
	home := t.TempDir()
	y := &gocacheYard{t: t, s: storetest.NewIn(t, home), log: &lockedWriter{}, base: t.TempDir()}
	y.d = &dispatch.Dispatcher{Store: y.s, Runners: map[string]dispatch.Runner{}, Home: home,
		Interval: time.Hour, Backoff: time.Minute, Log: slog.New(slog.NewTextHandler(y.log, nil))}
	y.set(time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC))
	dispatch.SetClock(y.d, func() time.Time { return *y.clock.Load() })
	y.live = y.cache("go-build-9")
	dispatch.SetLiveGOCACHE(y.d, func() (string, error) {
		y.asked.Add(1)
		if h := y.hold.Load(); h != nil {
			<-*h
		}
		if y.fail.Load() {
			return "", errors.New("exec: \"go\": executable file not found in $PATH")
		}
		return y.live, nil
	})
	t.Cleanup(func() { dispatch.WaitGOCACHE(y.d) })
	return y
}

func (y *gocacheYard) set(at time.Time) { y.clock.Store(&at) }

// cache makes a cache beside the live one, with a file in it so that a
// removal proves recursive, and says where it is.
func (y *gocacheYard) cache(name string) string {
	y.t.Helper()
	dir := filepath.Join(y.base, name)
	must(y.t, os.MkdirAll(dir, 0o755))
	must(y.t, os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644))
	return dir
}

func (y *gocacheYard) record(dir string, at time.Time) {
	y.t.Helper()
	must(y.t, y.s.RecordGOCACHEOld(context.Background(), dir, at))
}

func (y *gocacheYard) recorded() []string {
	y.t.Helper()
	olds, err := y.s.GOCACHEOldDirs(context.Background())
	must(y.t, err)
	var dirs []string
	for _, o := range olds {
		dirs = append(dirs, o.Dir)
	}
	slices.Sort(dirs)
	return dirs
}

func (y *gocacheYard) exists(dir string) bool {
	_, err := os.Stat(dir)
	return err == nil
}

// pass runs the pass that starts a sweep where one is due, waits for the
// sweep (a pass run alone does not), and runs the pass its end wakes Run's
// loop for, which notes what is due next.
func (y *gocacheYard) pass() {
	y.t.Helper()
	must(y.t, y.d.Reconcile(context.Background()))
	dispatch.WaitGOCACHE(y.d)
	must(y.t, y.d.Reconcile(context.Background()))
}

func sorted(dirs ...string) []string {
	slices.Sort(dirs)
	return dirs
}

// The daemon removes a rotated cache when its grace ends, with no landing in
// between: the pass notes the earliest recorded cache's grace end as the due
// Run wakes for, and at it a sweep removes that cache and leaves what a
// keeping would leave: a cache still inside its grace, the live cache (even
// where a record names it) and a path that is no rotated sibling of it, which
// is only dropped from the record. The live cache's record is no due, so it
// does not make the loop sweep again at every pass.
func TestPassRemovesARotatedCacheWhenItsGraceEnds(t *testing.T) {
	t.Parallel()
	y := newGOCacheYard(t)
	t0 := *y.clock.Load()
	first, second := y.cache("go-build-7"), y.cache("go-build-8")
	y.record(first, t0)
	y.record(second, t0.Add(10*time.Minute))

	// A second short of the first one's grace: nothing goes, no sweep asks
	// for the live cache, and the due is the earliest grace end.
	y.set(t0.Add(diskkeep.OldGrace - time.Second))
	y.pass()
	due := t0.Add(diskkeep.OldGrace)
	if got := dispatch.GOCACHEDue(y.d); got == nil || !got.Equal(due) {
		t.Fatalf("due = %v, want %v, the earliest record's grace end", got, due)
	}
	if got := dispatch.NextDue(y.d); got == nil || !got.Equal(due) {
		t.Fatalf("Run wakes at %v, want %v: the due is not folded into its timer", got, due)
	}
	if y.asked.Load() != 0 || !y.exists(first) || !y.exists(second) {
		t.Fatalf("a sweep ran before the grace ended: asked %d times, first %v, second %v", y.asked.Load(), y.exists(first), y.exists(second))
	}

	// At the end of the grace the first goes, and the second, ten minutes
	// younger, stays recorded and is the next due.
	y.set(due)
	y.pass()
	if y.exists(first) || !y.exists(second) || !y.exists(y.live) {
		t.Fatalf("at the grace's end: first %v, second %v, live %v; want only the first gone", y.exists(first), y.exists(second), y.exists(y.live))
	}
	if got := y.recorded(); !slices.Equal(got, []string{second}) {
		t.Fatalf("recorded = %v, want the second alone", got)
	}
	if got := dispatch.GOCACHEDue(y.d); got == nil || !got.Equal(due.Add(10*time.Minute)) {
		t.Fatalf("due after the first = %v, want the second's grace end", got)
	}
	evs := eventsOf(t, y.s, store.DiskKept)
	if len(evs) != 1 {
		t.Fatalf("%d disk_kept events, want 1", len(evs))
	}
	data := evs[0].Data
	if removed, _ := data["removed"].([]any); len(removed) != 1 || removed[0] != first {
		t.Errorf("event removed = %v, want [%s]", data["removed"], first)
	}
	if data["rotated_to"] != "" || data["not_rotated"] == nil || evs[0].Actor != "yard" || evs[0].Bead != nil {
		t.Errorf("event = %+v, want one that rotated nothing, by the yard, about no bead", evs[0])
	}

	// A path that is no rotated sibling, and a record of the live cache,
	// both long past their grace: the sweep drops the first from the record
	// and takes neither off the disk, and the live cache's record is no due.
	foreign := filepath.Join(t.TempDir(), "precious")
	must(t, os.MkdirAll(foreign, 0o755))
	y.record(foreign, t0)
	y.record(y.live, t0)
	y.set(due.Add(time.Minute))
	y.pass()
	if !y.exists(foreign) || !y.exists(y.live) || !y.exists(second) {
		t.Fatalf("a sweep took what is no rotated cache of its own: foreign %v, live %v, second %v", y.exists(foreign), y.exists(y.live), y.exists(second))
	}
	if got, want := y.recorded(), sorted(second, y.live); !slices.Equal(got, want) {
		t.Fatalf("recorded = %v, want %v: the foreign path dropped, the live cache's record kept", got, want)
	}
	if got := dispatch.GOCACHEDue(y.d); got == nil || !got.Equal(due.Add(10*time.Minute)) {
		t.Fatalf("due with the live cache recorded = %v, want the second's grace end", got)
	}
	if asked := y.asked.Load(); asked != 2 {
		t.Fatalf("sweeps = %d, want 2", asked)
	}
	y.set(due.Add(2 * time.Minute))
	y.pass()
	if asked := y.asked.Load(); asked != 2 {
		t.Errorf("sweeps = %d after another pass, want the record of the live cache to be no due", asked)
	}

	// The second's grace ends: it goes, and with only the live cache's
	// record left nothing is due.
	y.set(due.Add(10 * time.Minute))
	y.pass()
	if y.exists(second) || !y.exists(y.live) {
		t.Fatalf("at the second's grace end: second %v, live %v", y.exists(second), y.exists(y.live))
	}
	if got := y.recorded(); !slices.Equal(got, []string{y.live}) {
		t.Fatalf("recorded = %v, want the live cache's record alone", got)
	}
	if got := dispatch.NextDue(y.d); got != nil {
		t.Fatalf("Run wakes at %v with nothing due", got)
	}
	if evs := eventsOf(t, y.s, store.DiskKept); len(evs) != 3 {
		t.Fatalf("%d disk_kept events, want one for each sweep that did something", len(evs))
	}
	if errs, _ := eventsOf(t, y.s, store.DiskKept)[1].Data["errors"].([]any); len(errs) != 1 || !strings.Contains(errs[0].(string), foreign) {
		t.Errorf("the sweep that dropped the foreign path told %v", errs)
	}
}

// A sweep that cannot tell the live cache removes nothing, and is told once
// as a warning and tried again after Backoff, doubling, as the store's
// copies are: not at every pass, and not by a loop that wakes each second for
// a due that is already past. The retry that can goes ahead.
func TestPassBacksOffASweepThatCannotTellTheLiveCache(t *testing.T) {
	t.Parallel()
	y := newGOCacheYard(t)
	t0 := *y.clock.Load()
	old := y.cache("go-build-7")
	y.record(old, t0)
	y.fail.Store(true)
	end := t0.Add(diskkeep.OldGrace)

	y.set(end)
	y.pass()
	if y.asked.Load() != 1 || !y.exists(old) {
		t.Fatalf("asked %d times, old standing %v; want one try and the cache kept", y.asked.Load(), y.exists(old))
	}
	y.pass()
	if y.asked.Load() != 1 {
		t.Fatalf("asked %d times in the next pass, want the back-off held", y.asked.Load())
	}
	retry := end.Add(time.Minute)
	if got := dispatch.GOCACHEDue(y.d); got == nil || !got.Equal(retry) {
		t.Fatalf("due = %v, want the retry at %v, not the past due", got, retry)
	}

	y.set(retry.Add(-time.Second))
	y.pass()
	if y.asked.Load() != 1 {
		t.Fatalf("asked %d times a second before the retry, want 1", y.asked.Load())
	}
	// The retry fails too: the wait doubles.
	y.set(retry)
	y.pass()
	if y.asked.Load() != 2 {
		t.Fatalf("asked %d times at the retry, want 2", y.asked.Load())
	}
	y.pass()
	if got, want := dispatch.GOCACHEDue(y.d), retry.Add(2*time.Minute); got == nil || !got.Equal(want) {
		t.Fatalf("due = %v after the second failure, want %v: the back-off doubles", got, want)
	}
	if n := strings.Count(y.log.String(), "not removed"); n != 1 {
		t.Fatalf("%d warnings for one outage, want it told once:\n%s", n, y.log.String())
	}

	// The next retry finds go, removes the cache, and ends the outage.
	y.fail.Store(false)
	y.set(retry.Add(2 * time.Minute))
	y.pass()
	if y.exists(old) || len(y.recorded()) != 0 {
		t.Fatalf("after the outage: old standing %v, recorded %v", y.exists(old), y.recorded())
	}
	if got := dispatch.NextDue(y.d); got != nil {
		t.Fatalf("Run wakes at %v with nothing recorded", got)
	}
}

// deafStore is a store whose wake-up stream never fires, so that nothing but
// what a test names wakes Run's loop.
type deafStore struct{ store.API }

func (deafStore) Wake(context.Context) (<-chan struct{}, error) { return make(chan struct{}), nil }

// With the daemon running and nothing else (no landing, no keeping, no write
// to wake it, an interval of an hour), a recorded cache is gone when its grace
// ends: the loop wakes for the due the pass noted, the sweep runs off the
// pass, and its end wakes the loop again for the next due, which is none.
func TestRunRemovesARotatedCacheWhenItsGraceEnds(t *testing.T) {
	t.Parallel()
	y := newGOCacheYard(t)
	y.d.Store = deafStore{y.s}
	t0 := *y.clock.Load()
	old := y.cache("go-build-7")
	y.record(old, t0)
	before := t0.Add(diskkeep.OldGrace - time.Second)
	y.set(before)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- y.d.Run(ctx) }()
	waitFor(t, "the first pass", func() bool {
		stamp, err := config.ReadPassStamp(y.d.Home)
		return err == nil && stamp.At.Equal(before)
	})
	if !y.exists(old) {
		t.Fatal("the cache went before its grace ended")
	}

	// The clock reaches the end of the grace and nothing else happens: the
	// timer the pass armed is the only thing that can run the next pass.
	y.set(t0.Add(diskkeep.OldGrace + time.Second))
	waitFor(t, "the cache to go", func() bool { return !y.exists(old) && len(y.recorded()) == 0 })

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A removal that does not end holds up neither the pass nor a stop: the pass
// that starts the sweep returns at once, the next finds it in flight and
// starts no other, a daemon told to stop while it is stuck stops, and the sweep
// goes on beside them and removes the cache when it is let go.
func TestAStuckSweepHoldsUpNeitherThePassNorTheStop(t *testing.T) {
	t.Parallel()
	y := newGOCacheYard(t)
	y.d.Store = deafStore{y.s}
	t0 := *y.clock.Load()
	old := y.cache("go-build-7")
	y.record(old, t0)
	y.set(t0.Add(diskkeep.OldGrace))
	stuck := make(chan struct{})
	y.hold.Store(&stuck)
	ctx := context.Background()

	passed := make(chan error, 1)
	go func() { passed <- y.d.Reconcile(ctx) }()
	select {
	case err := <-passed:
		must(t, err)
	case <-time.After(dispatch.WaitFuse):
		t.Fatal("the pass waits for the sweep")
	}
	waitFor(t, "the sweep to start", func() bool { return y.asked.Load() == 1 })
	must(t, y.d.Reconcile(ctx))
	if y.asked.Load() != 1 || dispatch.GOCACHEDue(y.d) != nil || !y.exists(old) {
		t.Fatalf("with a sweep in flight: asked %d times, due %v, cache standing %v; want one sweep, no due and the cache kept", y.asked.Load(), dispatch.GOCACHEDue(y.d), y.exists(old))
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- y.d.Run(runCtx) }()
	waitFor(t, "the first pass", func() bool {
		stamp, err := config.ReadPassStamp(y.d.Home)
		return err == nil && stamp.At.Equal(t0.Add(diskkeep.OldGrace))
	})
	cancel()
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(dispatch.WaitFuse):
		t.Fatal("the daemon does not stop while a sweep is stuck")
	}

	close(stuck)
	dispatch.WaitGOCACHE(y.d)
	if y.exists(old) || len(y.recorded()) != 0 {
		t.Fatalf("the sweep let go: cache standing %v, recorded %v", y.exists(old), y.recorded())
	}
}

// waitFor waits for cond, which only a hang keeps from being true: the fuse
// is dispatch's, as the other waits on an effect.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(dispatch.WaitFuse); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
