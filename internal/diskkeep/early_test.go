package diskkeep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// early is the keeping fixture with the floor on (its default, whatever the
// machine's variable says) and a disk and a process table of its own: the disk
// has avail free with every cache standing, and each cache that goes frees its
// size, so that a removal changes what the next one is judged by as it does on
// a disk.
type early struct {
	*keeping
	base   string
	live   string
	avail  uint64
	sizes  map[string]uint64
	tables int // how often the process table was read
}

func newEarly(t *testing.T, live string, avail uint64) *early {
	t.Helper()
	k, _ := newKeeping(t, nil)
	t.Setenv(floorEnv, "")
	e := &early{keeping: k, base: t.TempDir(), avail: avail, sizes: map[string]uint64{}}
	e.live = filepath.Join(e.base, live)
	mkcache(t, e.live)
	k.fake.gocache = e.live
	freeDisk = func(string) (uint64, error) {
		e.frees++
		free := e.avail
		for dir, n := range e.sizes {
			if _, err := os.Lstat(dir); err != nil {
				free += n
			}
		}
		return free, nil
	}
	e.procs()
	return e
}

// rotated makes a rotated cache that frees size when it goes, recorded as
// rotated away from ago before the fixture's clock.
func (e *early) rotated(name string, size uint64, ago time.Duration) string {
	e.t.Helper()
	dir := filepath.Join(e.base, name)
	mkcache(e.t, dir)
	e.sizes[dir] = size
	must(e.t, e.st.RecordGOCACHEOld(context.Background(), dir, e.now.Add(-ago)))
	return dir
}

// goStarted is the line of a go process that started ago before the clock, as
// macOS prints it (the full path) or as procps does (the name).
func (e *early) goStarted(ago time.Duration, mac bool) string {
	start := e.now.Add(-ago).Format("Mon Jan _2 15:04:05 2006")
	if mac {
		return fmt.Sprintf("%5d %s     /opt/homebrew/bin/go", 4711, start)
	}
	return fmt.Sprintf("%7d %s go", 857012, start)
}

// procs sets the process table to a few processes that are not go and the
// lines given.
func (e *early) procs(lines ...string) {
	table := strings.Join(append([]string{
		"    1 Thu Oct 15 08:00:00 2026     /sbin/launchd",
		"  329 Thu Oct 15 08:00:01 2026     /System/Library/CoreServices/Software Update.app/Contents/Resources/suhelperd",
	}, lines...), "\n") + "\n"
	psTable = func(context.Context) ([]byte, error) {
		e.tables++
		return []byte(table), nil
	}
}

func (e *early) exists(dir string) bool {
	_, err := os.Lstat(dir)
	return err == nil
}

func (e *early) sweep() *Report {
	e.t.Helper()
	rep, err := Sweep(context.Background(), e.st, e.live, e.now)
	if err != nil {
		e.t.Fatal(err)
	}
	return rep
}

func (e *early) recorded() []string {
	var dirs []string
	for _, o := range e.olds() {
		dirs = append(dirs, o.Dir)
	}
	slices.Sort(dirs)
	return dirs
}

// While the disk is under the gate's floor, a sweep takes the rotated caches
// that stand inside their grace and that no go process started before the
// rotation of can hold: the older rotation first, and no more once the volume is
// over the floor. It takes none above the floor or at it (the gate refuses only
// under), none that an older go process may hold, and it reads the process table
// only when it may take one. The records stay, so the hold of a keeping goes on
// seeing the rotations. A table that cannot be read, or has a go process whose
// start cannot be, holds every cache and is told. Each row is run against both
// formats of the table.
func TestSweepTakesAnIdleCacheInsideItsGraceUnderTheFloor(t *testing.T) {
	// The names are the ones whose order in the record is not their age: the
	// store lists go-build-10 before go-build-9, and the older rotation goes first.
	const (
		old7 = "go-build-9"  // 12 GB, rotated away from 20 minutes ago
		old8 = "go-build-10" // 19 GB, 10 minutes ago
	)
	for _, c := range []struct {
		name   string
		avail  uint64 // GB free with both standing
		floor  string // the floor variable
		gos    []time.Duration
		failed string // "", "ps" for a ps that fails, "time" for a go whose start is unreadable
		want   []string
		tables int
		errs   string // in the report's errors
	}{
		{name: "no go process: the older first, and no more once over the floor", avail: 15, want: []string{old7}, tables: 1},
		{name: "still under the floor after the first: the second goes too", avail: 5, want: []string{old7, old8}, tables: 1},
		{name: "exactly at the floor", avail: 20},
		{name: "over the floor", avail: 25},
		{name: "a go process older than both holds both", avail: 5, gos: []time.Duration{30 * time.Minute}, tables: 1},
		{name: "one between the rotations holds the later one's cache only", avail: 5, gos: []time.Duration{15 * time.Minute}, want: []string{old7}, tables: 1},
		{name: "one started in the second of a rotation holds that one's cache", avail: 5, gos: []time.Duration{10 * time.Minute}, want: []string{old7}, tables: 1},
		{name: "one started after both holds neither", avail: 5, gos: []time.Duration{5 * time.Minute}, want: []string{old7, old8}, tables: 1},
		{name: "the oldest of several decides", avail: 5, gos: []time.Duration{time.Minute, 15 * time.Minute, 5 * time.Minute}, want: []string{old7}, tables: 1},
		{name: "a ps that fails holds both", avail: 5, failed: "ps", tables: 1, errs: "could not tell whether a go process holds a rotated cache"},
		{name: "a go process whose start is unreadable holds both", avail: 5, failed: "time", tables: 1, errs: "cannot be read"},
		{name: "the gate's check turned off", avail: 5, floor: "0"},
		{name: "the floor of the variable", avail: 25, floor: "30", want: []string{old7}, tables: 1},
		{name: "a variable that is no number is the floor of 20", avail: 15, floor: "x", want: []string{old7}, tables: 1},
	} {
		for _, mac := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s (mac %v)", c.name, mac), func(t *testing.T) {
				e := newEarly(t, "go-build-11", c.avail*GB)
				if c.floor != "" {
					t.Setenv(floorEnv, c.floor)
				}
				dirs := map[string]string{old7: e.rotated(old7, 12*GB, 20*time.Minute), old8: e.rotated(old8, 19*GB, 10*time.Minute)}
				var lines []string
				for _, ago := range c.gos {
					lines = append(lines, e.goStarted(ago, mac))
				}
				switch c.failed {
				case "ps":
					psTable = func(context.Context) ([]byte, error) { e.tables++; return nil, errors.New("ps: exit status 1") }
				case "time":
					e.procs(" 4711 Mon Xxx  9 06:10:03 2026     /opt/homebrew/bin/go")
				default:
					e.procs(lines...)
				}
				recordedBefore := e.recorded()

				rep := e.sweep()

				var want []string
				for _, name := range c.want {
					want = append(want, dirs[name])
				}
				if !slices.Equal(rep.Early, want) || (len(want) > 0 && !slices.Equal(rep.Removed, want)) {
					t.Errorf("Early = %v, Removed = %v, want %v", rep.Early, rep.Removed, want)
				}
				for _, name := range []string{old7, old8} {
					if gone := !e.exists(dirs[name]); gone != slices.Contains(c.want, name) {
						t.Errorf("%s gone = %v, want %v", name, gone, slices.Contains(c.want, name))
					}
				}
				if !e.exists(e.live) {
					t.Errorf("the live cache went")
				}
				if got := e.recorded(); !slices.Equal(got, recordedBefore) {
					t.Errorf("recorded = %v, want %v: the records stand until the grace ends", got, recordedBefore)
				}
				if e.tables != c.tables {
					t.Errorf("the process table was read %d times, want %d", e.tables, c.tables)
				}
				if c.errs == "" && len(rep.Errors) != 0 {
					t.Errorf("Errors = %v", rep.Errors)
				}
				if c.errs != "" && !strings.Contains(strings.Join(rep.Errors, "\n"), c.errs) {
					t.Errorf("Errors = %v, want one with %q", rep.Errors, c.errs)
				}
			})
		}
	}
}

// The event, the report's lines and its JSON say which directories went before
// their grace ended, apart from those past it, and a sweep that finds nothing to
// take says nothing.
func TestSweepSaysWhichWentEarly(t *testing.T) {
	e := newEarly(t, "go-build-9", 5*GB)
	past := e.rotated("go-build-6", 3*GB, 31*time.Minute)
	idle := e.rotated("go-build-8", 19*GB, 10*time.Minute)
	e.procs(e.goStarted(5*time.Minute, true))

	rep := e.sweep()

	if want := []string{past, idle}; !slices.Equal(rep.Removed, want) || !slices.Equal(rep.Early, []string{idle}) {
		t.Fatalf("Removed = %v, Early = %v, want %v and [%s]", rep.Removed, rep.Early, want, idle)
	}
	evs := e.events()
	if len(evs) != 1 {
		t.Fatalf("%d disk_kept events, want 1", len(evs))
	}
	if removed, _ := evs[0].Data["removed"].([]any); len(removed) != 2 || removed[0] != past || removed[1] != idle {
		t.Errorf("event removed = %v, want both", evs[0].Data["removed"])
	}
	if early, _ := evs[0].Data["removed_early"].([]any); len(early) != 1 || early[0] != idle {
		t.Errorf("event removed_early = %v, want [%s]", evs[0].Data["removed_early"], idle)
	}
	var lines []string
	for _, line := range rep.Lines() {
		if strings.HasPrefix(line, "removed ") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 2 || lines[0] != "removed "+past || !strings.HasPrefix(lines[1], "removed "+idle+" inside its grace") {
		t.Errorf("removed lines = %q", lines)
	}
	raw, err := json.Marshal(rep)
	if err != nil || !strings.Contains(string(raw), `"removed_early":["`+idle+`"]`) {
		t.Errorf("JSON = %s, %v", raw, err)
	}

	// Nothing left to take: no event, and no key in the JSON of a report that
	// took none early.
	e.now = e.now.Add(time.Second)
	rep = e.sweep()
	if rep.Acted || len(rep.Early) != 0 {
		t.Errorf("an idle sweep reports %+v", rep)
	}
	if evs := e.events(); len(evs) != 1 {
		t.Errorf("%d disk_kept events after an idle sweep, want still 1", len(evs))
	}
	if raw, _ := json.Marshal(rep); strings.Contains(string(raw), "removed_early") {
		t.Errorf("JSON of a report with none early = %s", raw)
	}
}

// What an early removal never takes, however idle and however short the disk:
// the live cache (even where a record names it), a directory that is no rotated
// sibling of it, one that is not on the disk, and one a record dates past its
// grace (the grace's own removal takes that, not this). It is called alone, not
// through a sweep, whose removal of what is past its grace and of what is no
// sibling would keep some of these from it before it looked.
func TestEarlyRemovalTakesOnlyWhatARotationLeft(t *testing.T) {
	e := newEarly(t, "go-build-9", 5*GB)
	ctx := context.Background()
	record := func(dir string, ago time.Duration) {
		t.Helper()
		must(t, e.st.RecordGOCACHEOld(ctx, dir, e.now.Add(-ago)))
	}
	// The pointer was moved back: the live cache is also recorded as rotated away from.
	record(e.live, 5*time.Minute)
	foreign := filepath.Join(t.TempDir(), "precious")
	mkcache(t, foreign)
	record(foreign, 5*time.Minute)
	record(filepath.Join(e.base, "go-build-5"), 5*time.Minute) // not on the disk
	past := filepath.Join(e.base, "go-build-6")
	mkcache(t, past)
	record(past, 31*time.Minute)
	for _, dir := range []string{e.live, foreign, past} {
		e.sizes[dir] = 40 * GB
	}

	rep := &Report{}
	if got := removeIdle(ctx, e.st, rep, e.live, e.now); len(got) != 0 || len(rep.Early) != 0 || len(rep.Errors) != 0 {
		t.Errorf("removeIdle took %v (Early %v, errors %v), want nothing", got, rep.Early, rep.Errors)
	}
	for _, dir := range []string{e.live, foreign, past} {
		if !e.exists(dir) {
			t.Errorf("%s was taken", dir)
		}
	}
	if e.tables != 0 {
		t.Errorf("the process table was read %d times with no rotated cache to take", e.tables)
	}

	// Through a sweep, the one past its grace goes by the grace and not early,
	// the path that is no sibling is dropped from the record and left on the
	// disk, and the rest stands.
	got := e.sweep()
	if len(got.Early) != 0 || !slices.Equal(got.Removed, []string{past}) || !e.exists(foreign) || !e.exists(e.live) {
		t.Errorf("Early = %v, Removed = %v, foreign standing %v, live standing %v", got.Early, got.Removed, e.exists(foreign), e.exists(e.live))
	}
}

// A cache no record names is recorded at first sight, which is never before it
// was left, so a go process that is running at all may have started before it:
// it is taken early in the sweep that finds it only where none runs.
func TestSweepTakesAnAdoptedCacheEarlyOnlyWhereNoGoRuns(t *testing.T) {
	for _, c := range []struct {
		name string
		gos  []time.Duration
		took bool
	}{
		{"a go process is running", []time.Duration{time.Hour}, false},
		{"none is", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEarly(t, "go-build-9", 5*GB)
			stray := filepath.Join(e.base, "go-build-4")
			mkcache(t, stray)
			e.sizes[stray] = 19 * GB
			var lines []string
			for _, ago := range c.gos {
				lines = append(lines, e.goStarted(ago, false))
			}
			e.procs(lines...)

			rep := e.sweep()

			if !slices.Equal(rep.Adopted, []string{stray}) || (len(rep.Early) > 0) != c.took || e.exists(stray) == c.took {
				t.Errorf("Adopted = %v, Early = %v, %s standing %v; want it adopted, and taken early %v", rep.Adopted, rep.Early, stray, e.exists(stray), c.took)
			}
			if got := e.olds(); len(got) != 1 || got[0].Dir != stray || !got[0].At.Equal(e.now) {
				t.Errorf("recorded = %+v, want the stray at first sight", got)
			}
		})
	}
}

// A keeping under the floor takes an idle cache inside its grace, and goes on
// holding the rotation: the record stands, so the next keeping does not rotate
// again at once (the churn the hold was made to stop), and when the grace ends
// the record is dropped without a second "removed" for a directory that has
// been gone since.
func TestKeepTakesAnIdleCacheInsideItsGraceAndStillHoldsTheRotation(t *testing.T) {
	e := newEarly(t, "go-build-9", 5*GB)
	old8 := e.rotated("go-build-8", 19*GB, 10*time.Minute)
	ctx := context.Background()
	keep := func() *Report {
		t.Helper()
		rep, err := Keep(ctx, e.st, map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	rep := keep()

	if !strings.Contains(rep.NotRotated, "the last rotation was 10m0s ago") || len(e.fake.sets) != 0 {
		t.Fatalf("NotRotated = %q, go env -w ran %v: the rotation 10 minutes ago holds", rep.NotRotated, e.fake.sets)
	}
	if !slices.Equal(rep.Early, []string{old8}) || e.exists(old8) {
		t.Fatalf("Early = %v, %s standing %v; want it taken", rep.Early, old8, e.exists(old8))
	}
	if got := e.olds(); len(got) != 1 || got[0].Dir != old8 || !got[0].At.Equal(e.now.Add(-10*time.Minute)) {
		t.Fatalf("recorded = %+v, want the early-removed directory's record as it was", got)
	}
	evs := e.events()
	if len(evs) != 1 || evs[0].Data["removed_early"] == nil {
		t.Fatalf("events = %+v, want one that names the directory taken early", evs)
	}

	// Two minutes on, the volume is still short of the 30 GB a keeping acts
	// under, the record still holds the rotation, and there is nothing standing
	// to take: no second read of the process table.
	e.now = e.now.Add(2 * time.Minute)
	rep = keep()
	if !strings.Contains(rep.NotRotated, "the last rotation was 12m0s ago") || len(e.fake.sets) != 0 {
		t.Fatalf("NotRotated = %q, go env -w ran %v: rotated again after an early removal", rep.NotRotated, e.fake.sets)
	}
	if len(rep.Early) != 0 || e.tables != 1 {
		t.Errorf("Early = %v after %d reads of the process table, want none and still 1", rep.Early, e.tables)
	}

	// The grace ends: the rotation goes ahead, and the record of the directory
	// that is long gone is dropped without being called removed.
	e.now = e.now.Add(20 * time.Minute)
	rep = keep()
	wantNext := filepath.Join(e.base, "go-build-10")
	if rep.RotatedTo != wantNext || len(e.fake.sets) != 1 {
		t.Errorf("rotated to %q, go env -w ran %v, want the rotation to %s", rep.RotatedTo, e.fake.sets, wantNext)
	}
	if slices.Contains(rep.Removed, old8) || len(rep.Errors) != 0 {
		t.Errorf("Removed = %v, Errors = %v: %s was taken by an earlier keeping", rep.Removed, rep.Errors, old8)
	}
	if got := e.olds(); len(got) != 1 || got[0].Dir != e.live {
		t.Errorf("recorded = %+v, want the just-rotated directory alone", got)
	}
}

// The landing's own case: a keeping that rotates leaves the cache it moved away
// from beside the live one with a record of this minute, and where the disk is
// under the floor and no go process is older than that, it is taken at once, so
// that the gate that follows is not stopped by it. A go process that started
// before the rotation holds it.
func TestKeepTakesTheCacheItJustRotatedAwayFrom(t *testing.T) {
	for _, c := range []struct {
		name string
		gos  []time.Duration
		held bool
	}{
		{"no go process", nil, false},
		{"a builder's go started before the rotation", []time.Duration{3 * time.Minute}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEarly(t, "go-build-8", 5*GB)
			old := e.live
			e.sizes[old] = 19 * GB
			var lines []string
			for _, ago := range c.gos {
				lines = append(lines, e.goStarted(ago, true))
			}
			e.procs(lines...)

			rep, err := Keep(context.Background(), e.st, map[string]string{})
			if err != nil {
				t.Fatal(err)
			}

			if rep.RotatedFrom != old || rep.RotatedTo != filepath.Join(e.base, "go-build-9") {
				t.Fatalf("rotated %q -> %q", rep.RotatedFrom, rep.RotatedTo)
			}
			if gone := !e.exists(old); gone == c.held {
				t.Errorf("%s gone = %v, want %v", old, gone, !c.held)
			}
			if c.held != (len(rep.Early) == 0) {
				t.Errorf("Early = %v", rep.Early)
			}
			if got := e.olds(); len(got) != 1 || got[0].Dir != old {
				t.Errorf("recorded = %+v, want the rotated directory's record", got)
			}
			if e.tables != 1 {
				t.Errorf("the process table was read %d times, want 1", e.tables)
			}
		})
	}
}

// Early is the daemon's look at each pass, with no process table: a recorded
// directory on the disk inside its grace, and its volume under the floor,
// judged at the directory's own parent since the live cache is not known to it.
func TestEarly(t *testing.T) {
	e := newEarly(t, "go-build-9", 15*GB)
	dir := e.rotated("go-build-8", 19*GB, 10*time.Minute)
	var asked []string
	save := freeDisk
	freeDisk = func(path string) (uint64, error) { asked = append(asked, path); return save(path) }

	if !Early(e.olds(), e.now) || !slices.Equal(asked, []string{e.base}) {
		t.Errorf("Early = false or asked %v, want true and the parent of the directory", asked)
	}
	if Early(nil, e.now) {
		t.Errorf("Early of nothing recorded")
	}
	e.avail = 20 * GB
	if Early(e.olds(), e.now) {
		t.Errorf("Early at the floor")
	}
	e.avail = 25 * GB
	if Early(e.olds(), e.now) {
		t.Errorf("Early over the floor")
	}
	e.avail = 15 * GB
	if Early(e.olds(), e.now.Add(20*time.Minute)) {
		t.Errorf("Early for a directory past its grace")
	}
	t.Setenv(floorEnv, "0")
	asked = nil
	if Early(e.olds(), e.now) || len(asked) != 0 {
		t.Errorf("Early with the gate's check off, after reading %v", asked)
	}
	t.Setenv(floorEnv, "")
	must(t, os.RemoveAll(dir))
	e.avail = 5 * GB
	if Early(e.olds(), e.now) {
		t.Errorf("Early for a directory that is not on the disk")
	}
}

// The floor is the gate's: 20 GB unless the variable the gate reads says
// otherwise, 0 for a yard that turned the check off.
func TestFloorBytes(t *testing.T) {
	for _, c := range []struct {
		env  string
		want uint64
	}{
		{"", FloorGB},
		{"0", 0},
		{"35", 35},
		{"1", 1},
		{"x", FloorGB},
		{"20x", FloorGB},
		{"-1", FloorGB},
		{"+5", FloorGB},
		{" 5", FloorGB},
		{"1.5", FloorGB},
		{"99999999999", FloorGB},
	} {
		t.Setenv(floorEnv, c.env)
		if got := floorBytes(); got != c.want*GB {
			t.Errorf("floor of %q = %d GB, want %d", c.env, got/GB, c.want)
		}
	}
}
