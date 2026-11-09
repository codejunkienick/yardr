package diskkeep

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// fakeGo is go as the tests see it: GOCACHE and GOTMPDIR by value, every
// `go env -w` recorded. With sticky set, `go env -w` changes nothing, as
// when GOCACHE comes from the environment and wins over the go env file.
type fakeGo struct {
	gocache string
	gotmp   string
	sets    []string
	sticky  bool
	gets    int
}

func (f *fakeGo) env(names ...string) (string, error) {
	f.gets++
	if len(names) != 1 {
		panic("goEnv takes one name")
	}
	switch names[0] {
	case "GOCACHE":
		return f.gocache, nil
	case "GOTMPDIR":
		return f.gotmp, nil
	}
	panic("goEnv for " + names[0])
}

func (f *fakeGo) set(kv string) error {
	f.sets = append(f.sets, kv)
	if !f.sticky {
		f.gocache = strings.TrimPrefix(kv, "GOCACHE=")
	}
	return nil
}

// keeping is a Keep with faked go, disk and clock over a throwaway store.
type keeping struct {
	t     *testing.T
	st    store.API
	fake  *fakeGo
	free  []uint64
	frees int
	now   time.Time
}

func newKeeping(t *testing.T, settings map[string]string) (*keeping, map[string]string) {
	t.Helper()
	k := &keeping{t: t, st: storetest.New(t), fake: &fakeGo{}, now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	saveFree, saveEnv, saveSet, saveHas, saveNow := freeDisk, goEnv, goEnvSet, hasGo, now
	t.Cleanup(func() { freeDisk, goEnv, goEnvSet, hasGo, now = saveFree, saveEnv, saveSet, saveHas, saveNow })
	freeDisk = func(string) (uint64, error) {
		if k.frees >= len(k.free) {
			t.Fatalf("free space read %d times, scripted %d", k.frees+1, len(k.free))
		}
		free := k.free[k.frees]
		k.frees++
		return free, nil
	}
	goEnv = k.fake.env
	goEnvSet = k.fake.set
	hasGo = func() bool { return true }
	now = func() time.Time { return k.now }
	if settings == nil {
		settings = map[string]string{}
	}
	return k, settings
}

func (k *keeping) run(settings map[string]string) *Report {
	k.t.Helper()
	rep, err := Keep(context.Background(), k.st, settings)
	if err != nil {
		k.t.Fatal(err)
	}
	return rep
}

func (k *keeping) events() []store.Event {
	k.t.Helper()
	evs, err := k.st.EventsOfKind(context.Background(), store.DiskKept, 10)
	if err != nil {
		k.t.Fatal(err)
	}
	return evs
}

func (k *keeping) olds() []store.GOCACHEDir {
	k.t.Helper()
	olds, err := k.st.GOCACHEOldDirs(context.Background())
	if err != nil {
		k.t.Fatal(err)
	}
	return olds
}

// mkdir writes dir with a file in it, so a removal proves recursive.
func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkcache writes dir as a Go build cache: with the README Go leaves in each
// one it makes, which is all that tells a rotated cache from any other
// directory of the same name.
func mkcache(t *testing.T, dir string) {
	t.Helper()
	mkdir(t, dir)
	readme := goCacheMarker + "\nRun \"go clean -cache\" if the directory is getting too large.\n"
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKeepIdleOverThreshold(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	k.free = []uint64{31 * GB}

	rep := k.run(settings)

	if rep.Acted {
		t.Errorf("Acted = true over the threshold")
	}
	if k.frees != 1 {
		t.Errorf("free space read %d times, want 1", k.frees)
	}
	if len(k.fake.sets) > 0 {
		t.Errorf("go env -w ran %v over the threshold", k.fake.sets)
	}
	if evs := k.events(); len(evs) != 0 {
		t.Errorf("%d disk_kept events, want none", len(evs))
	}
	if olds := k.olds(); len(olds) != 0 {
		t.Errorf("%d recorded directories, want none", len(olds))
	}
	if lines := rep.Lines(); len(lines) != 0 {
		t.Errorf("Lines = %v, want none", lines)
	}
}

func TestKeepOff(t *testing.T) {
	k, settings := newKeeping(t, map[string]string{store.DiskKeepGBSetting: "0"})
	k.free = []uint64{GB}

	rep := k.run(settings)

	if rep.Acted {
		t.Errorf("Acted = true with disk_keep_gb=0")
	}
	if k.frees != 0 || k.fake.gets != 0 {
		t.Errorf("the disk and go were consulted with the keeping off (%d reads, %d go env)", k.frees, k.fake.gets)
	}
	if evs := k.events(); len(evs) != 0 {
		t.Errorf("%d disk_kept events, want none", len(evs))
	}
}

func TestKeepWithoutGo(t *testing.T) {
	k, settings := newKeeping(t, nil)
	hasGo = func() bool { return false }

	rep := k.run(settings)

	if rep.Acted {
		t.Errorf("Acted = true without go")
	}
	if evs := k.events(); len(evs) != 0 {
		t.Errorf("%d disk_kept events, want none", len(evs))
	}
}

func TestKeepRotates(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	k.free = []uint64{20 * GB, 40 * GB, 20 * GB, 5 * GB, 20 * GB, 40 * GB}

	rep := k.run(settings)

	if !rep.Acted {
		t.Fatalf("Acted = false under the threshold")
	}
	wantFrom := filepath.Join(base, "go-build-8")
	wantTo := filepath.Join(base, "go-build-9")
	if len(k.fake.sets) != 1 || k.fake.sets[0] != "GOCACHE="+wantTo {
		t.Fatalf("go env -w ran %v, want [GOCACHE=%s]", k.fake.sets, wantTo)
	}
	if rep.RotatedFrom != wantFrom || rep.RotatedTo != wantTo {
		t.Errorf("rotated %s -> %s, want %s -> %s", rep.RotatedFrom, rep.RotatedTo, wantFrom, wantTo)
	}
	olds := k.olds()
	if len(olds) != 1 || olds[0].Dir != filepath.Join(base, "go-build-8") || !olds[0].At.Equal(k.now) {
		t.Errorf("recorded = %+v, want the old directory at now", olds)
	}
	evs := k.events()
	if len(evs) != 1 {
		t.Fatalf("%d disk_kept events, want 1", len(evs))
	}
	data := evs[0].Data
	if data["free_before"] != float64(20*GB) || data["free_after"] != float64(40*GB) {
		t.Errorf("event free %v -> %v, want %d -> %d", data["free_before"], data["free_after"], 20*GB, 40*GB)
	}
	if data["rotated_from"] != rep.RotatedFrom || data["rotated_to"] != rep.RotatedTo {
		t.Errorf("event rotated %v -> %v", data["rotated_from"], data["rotated_to"])
	}
	if data["gave_up"] != false {
		t.Errorf("event gave_up = %v, want false", data["gave_up"])
	}
	if evs[0].Actor != Actor {
		t.Errorf("event actor = %q, want %q", evs[0].Actor, Actor)
	}
	if evs[0].Bead != nil {
		t.Errorf("event bead = %v, want none", *evs[0].Bead)
	}
	lines := rep.Lines()
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "disk kept: free 20.0 GB -> 40.0 GB") {
		t.Errorf("Lines = %v", lines)
	}

	// Five minutes later, another low disk keeping holds the rotation, but
	// still clears stale go-tmp and reports the space it found.
	k.now = k.now.Add(5 * time.Minute)
	k.fake.gotmp = filepath.Join(base, "go-tmp")
	stale := filepath.Join(k.fake.gotmp, "go-build111")
	mkdir(t, stale)
	past := k.now.Add(-4 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}
	rep = k.run(settings)
	if len(k.fake.sets) != 1 || k.fake.gocache != wantTo {
		t.Errorf("go env -w ran %v, GOCACHE is %s: rotated again inside the grace", k.fake.sets, k.fake.gocache)
	}
	if rep.RotatedFrom != "" || rep.RotatedTo != "" || rep.NotRotated != "the last rotation was 5m0s ago, inside the 30 minutes a rotated cache stands aside" {
		t.Errorf("rotation = %q -> %q, not rotated %q", rep.RotatedFrom, rep.RotatedTo, rep.NotRotated)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != stale || !rep.GaveUp || rep.FreeAfter != 5*GB {
		t.Errorf("Removed = %v, gave up = %v, free after = %d; want stale go-tmp and 5 GB", rep.Removed, rep.GaveUp, rep.FreeAfter)
	}
	if olds := k.olds(); len(olds) != 1 || olds[0].Dir != wantFrom {
		t.Errorf("recorded = %+v, want only the first rotation's directory", olds)
	}
	evs = k.events()
	if len(evs) != 2 {
		t.Fatalf("%d disk_kept events, want one for each keeping", len(evs))
	}
	if evs[1].Data["not_rotated"] != rep.NotRotated || evs[1].Data["rotated_to"] != "" || evs[1].Data["gave_up"] != true {
		t.Errorf("second event = %v, want the held rotation and gave_up", evs[1].Data)
	}
	if _, ok := evs[0].Data["not_rotated"]; ok {
		t.Errorf("the rotating keeping's event says it did not rotate: %v", evs[0].Data)
	}

	// Thirty-one minutes after the rotation, it can go ahead and take the
	// first cache.
	k.now = k.now.Add(26 * time.Minute)
	rep = k.run(settings)
	wantThird := filepath.Join(base, "go-build-10")
	if len(k.fake.sets) != 2 || k.fake.gocache != wantThird || rep.RotatedTo != wantThird {
		t.Errorf("go env -w ran %v, GOCACHE is %s, rotated to %s; want %s", k.fake.sets, k.fake.gocache, rep.RotatedTo, wantThird)
	}
	if rep.NotRotated != "" || len(rep.Removed) != 1 || rep.Removed[0] != wantFrom {
		t.Errorf("NotRotated = %q, Removed = %v, want rotation that takes %s", rep.NotRotated, rep.Removed, wantFrom)
	}
}

func TestLockGOCACHESerializesKeepings(t *testing.T) {
	gocache := filepath.Join(t.TempDir(), "go-build-8")
	first, err := lockGOCACHE(gocache)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	path := filepath.Join(filepath.Dir(gocache), ".yardr-diskkeep.lock")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("lock mode = %04o, want 0600", got)
	}

	second, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		_ = syscall.Flock(int(second.Fd()), syscall.LOCK_UN)
		t.Fatal("second keeping acquired the held lock")
	} else if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
		t.Fatalf("second lock attempt: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("second keeping could not acquire the released lock: %v", err)
	}
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}

	// A keeping that waited on the shared lock uses the cache another yard
	// selected while it waited, then adopts the cache that yard left.
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	gocache = filepath.Join(base, "go-build-8")
	k.fake.gocache = gocache
	mkcache(t, gocache)
	held, err := lockGOCACHE(gocache)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	started, resume := make(chan struct{}), make(chan struct{})
	reads := 0
	freeDisk = func(string) (uint64, error) {
		reads++
		if reads == 1 {
			close(started)
			<-resume
		}
		return 20 * GB, nil
	}
	type keepResult struct {
		report *Report
		err    error
	}
	done := make(chan keepResult, 1)
	go func() {
		rep, err := Keep(context.Background(), k.st, settings)
		done <- keepResult{report: rep, err: err}
	}()
	<-started
	current := filepath.Join(base, "go-build-9")
	k.fake.gocache = current
	close(resume)
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(k.fake.sets) != 0 || result.report.NotRotated == "" || len(result.report.Adopted) != 1 || result.report.Adopted[0] != gocache {
		t.Errorf("sets = %v, not rotated %q, adopted %v; want no rotation and [%s]", k.fake.sets, result.report.NotRotated, result.report.Adopted, gocache)
	}
}

func TestKeepLargeThresholdDoesNotWrap(t *testing.T) {
	k, settings := newKeeping(t, nil)
	settings[store.DiskKeepGBSetting] = strconv.Itoa(int(^uint(0) >> 1))
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	k.free = []uint64{20 * GB, 40 * GB}

	rep := k.run(settings)

	if !rep.Acted {
		t.Fatalf("Acted = false under the largest accepted threshold")
	}
	if len(k.fake.sets) != 1 {
		t.Errorf("go env -w ran %v, want one rotation", k.fake.sets)
	}
}

func TestKeepRemovesOldPastGrace(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	past := filepath.Join(base, "go-build-7")
	young := filepath.Join(base, "go-build-6")
	mkdir(t, past)
	mkdir(t, young)
	ctx := context.Background()
	must(t, k.st.RecordGOCACHEOld(ctx, past, k.now.Add(-31*time.Minute)))
	must(t, k.st.RecordGOCACHEOld(ctx, young, k.now.Add(-29*time.Minute)))
	k.free = []uint64{20 * GB, 40 * GB}

	rep := k.run(settings)

	if _, err := os.Stat(past); !os.IsNotExist(err) {
		t.Errorf("%s stands past its grace", past)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("%s went before its grace: %v", young, err)
	}
	// The young one was rotated away from 29 minutes ago, which holds the
	// rotation: what is past its grace goes all the same.
	if len(k.fake.sets) != 0 || rep.RotatedTo != "" {
		t.Errorf("go env -w ran %v, rotated to %q, inside the grace of a rotation", k.fake.sets, rep.RotatedTo)
	}
	if olds := k.olds(); len(olds) != 1 || olds[0].Dir != young {
		t.Errorf("recorded = %+v, want only the young one", olds)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != past {
		t.Errorf("Removed = %v, want [%s]", rep.Removed, past)
	}

	// Two minutes on it is 31 minutes old: the rotation goes ahead, and the
	// young one is the next to go.
	k.now = k.now.Add(2 * time.Minute)
	k.free = append(k.free, 20*GB, 40*GB)

	rep = k.run(settings)

	if _, err := os.Stat(young); !os.IsNotExist(err) {
		t.Errorf("%s stands past its grace: %v", young, err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != young {
		t.Errorf("Removed = %v, want [%s]", rep.Removed, young)
	}
	if want := filepath.Join(base, "go-build-9"); rep.RotatedTo != want {
		t.Errorf("rotated to %q, want %s", rep.RotatedTo, want)
	}
	if olds := k.olds(); len(olds) != 1 || olds[0].Dir != filepath.Join(base, "go-build-8") {
		t.Errorf("recorded = %+v, want only the just-rotated one", olds)
	}
}

func TestKeepNeverRemovesTheLiveCache(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	// A record pointing at what the rotation lands on: stale from a
	// pointer moved back, and past its grace. It is the live cache from
	// the rotation on, so it stays recorded and out of the removed.
	live := filepath.Join(base, "go-build-9")
	must(t, k.st.RecordGOCACHEOld(context.Background(), live, k.now.Add(-time.Hour)))
	k.free = []uint64{20 * GB, 40 * GB}

	rep := k.run(settings)

	for _, dir := range rep.Removed {
		if dir == live {
			t.Errorf("Removed holds the live cache: %v", rep.Removed)
		}
	}
	olds := k.olds()
	var dirs []string
	for _, o := range olds {
		dirs = append(dirs, o.Dir)
	}
	if len(olds) != 2 || dirs[0] != filepath.Join(base, "go-build-8") || dirs[1] != live {
		t.Errorf("recorded = %v, want the just-rotated and the live cache", dirs)
	}
}

// TestRemoveOldKeepsAnExistingLiveCache is the race removeOld guards: a
// build that started in the new cache between the rotation and the removal
// made the live directory exist, and past-grace record or not, it stands.
func TestRemoveOldKeepsAnExistingLiveCache(t *testing.T) {
	k, _ := newKeeping(t, nil)
	live := filepath.Join(t.TempDir(), "go-build-9")
	mkdir(t, live)
	ctx := context.Background()
	must(t, k.st.RecordGOCACHEOld(ctx, live, k.now.Add(-time.Hour)))
	rep := &Report{Removed: []string{}}

	removed := removeOld(ctx, k.st, rep, live, k.now)

	if len(removed) != 0 || len(rep.Errors) != 0 {
		t.Errorf("removed = %v, errors = %v", removed, rep.Errors)
	}
	if _, err := os.Stat(filepath.Join(live, "f")); err != nil {
		t.Errorf("the live cache went: %v", err)
	}
	if olds := k.olds(); len(olds) != 1 {
		t.Errorf("recorded = %+v, want the live cache kept", olds)
	}
}

func TestKeepPrunesTmpByAge(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	k.fake.gotmp = filepath.Join(base, "go-tmp")
	stale := filepath.Join(k.fake.gotmp, "go-build111")
	fresh := filepath.Join(k.fake.gotmp, "go-build222")
	other := filepath.Join(k.fake.gotmp, "other")
	mkdir(t, stale)
	mkdir(t, fresh)
	mkdir(t, other)
	plain := filepath.Join(k.fake.gotmp, "go-build333")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := k.now.Add(-4 * time.Hour)
	for _, p := range []string{stale, other, plain} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	k.free = []uint64{20 * GB, 40 * GB}

	rep := k.run(settings)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale go-tmp stands: %v", err)
	}
	for _, p := range []string{fresh, other, plain} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s went: %v", p, err)
		}
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != stale {
		t.Errorf("Removed = %v, want [%s]", rep.Removed, stale)
	}
}

func TestKeepWithoutTmpDir(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	k.fake.gotmp = "" // GOTMPDIR unset: nothing known to clear
	k.free = []uint64{20 * GB, 40 * GB}

	rep := k.run(settings)

	if !rep.Acted || len(rep.Errors) != 0 {
		t.Errorf("Acted = %v, Errors = %v", rep.Acted, rep.Errors)
	}
}

func TestKeepGaveUp(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "gocache")
	mkdir(t, k.fake.gocache)
	k.free = []uint64{20 * GB, 5 * GB}

	rep := k.run(settings)

	if !rep.GaveUp {
		t.Fatalf("GaveUp = false at 5 GB free after")
	}
	// No numeric suffix: the first sibling is -1.
	if want := filepath.Join(base, "gocache-1"); k.fake.gocache != want || rep.RotatedTo != want {
		t.Errorf("rotated to %s, want %s", rep.RotatedTo, want)
	}
	evs := k.events()
	if len(evs) != 1 || evs[0].Data["gave_up"] != true {
		t.Fatalf("events = %+v, want one with gave_up", evs)
	}
	lines := rep.Lines()
	if lines[len(lines)-1] != "gave up: free space still under 10 GB after keeping the disk" {
		t.Errorf("Lines = %v", lines)
	}
}

func TestKeepSkipsTakenSiblings(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "cache")
	mkdir(t, k.fake.gocache)
	mkdir(t, filepath.Join(base, "cache-1"))
	k.free = []uint64{20 * GB, 40 * GB}

	rep := k.run(settings)

	if want := filepath.Join(base, "cache-2"); rep.RotatedTo != want {
		t.Errorf("rotated to %s, want %s", rep.RotatedTo, want)
	}
}

func TestKeepRefusesAnUntakenRotation(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	k.fake.sticky = true // GOCACHE from the environment wins over go env -w
	k.free = []uint64{20 * GB}

	_, err := Keep(context.Background(), k.st, settings)

	if err == nil || !strings.Contains(err.Error(), "after go env -w") {
		t.Fatalf("err = %v, want the untaken rotation", err)
	}
	if evs := k.events(); len(evs) != 0 {
		t.Errorf("%d disk_kept events, want none", len(evs))
	}
	if olds := k.olds(); len(olds) != 0 {
		t.Errorf("%d recorded directories, want none", len(olds))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A path any socket caller recorded is a claim: a directory that is not a
// rotated sibling of the live cache is never removed, however old.
func TestKeepNeverRemovesARecordedForeignPath(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkdir(t, k.fake.gocache)
	foreign := filepath.Join(t.TempDir(), "precious")
	mkdir(t, foreign)
	parent := filepath.Join(base, "work")
	mkdir(t, parent)
	ctx := context.Background()
	for _, d := range []string{foreign, parent, filepath.Join(base, "go-build-7", "..", "work"), base} {
		must(t, k.st.RecordGOCACHEOld(ctx, d, k.now.Add(-24*time.Hour)))
	}
	k.free = []uint64{20 * GB, 40 * GB}

	rep := k.run(settings)

	for _, d := range []string{foreign, parent, base} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed: %v", d, err)
		}
	}
	if len(rep.Removed) != 0 {
		t.Errorf("Removed = %v", rep.Removed)
	}
}

// GOCACHE is the machine's and the record one yard's store, so a rotation by
// another yard on the machine is in no record here. The caches it left are
// recorded at first sight, hold the rotation (it may have been just now), and
// go when their grace, counted from that sight, has passed. Nothing else
// beside the cache is touched: not a directory that is no Go cache, a name
// that is no rotation's, a number past the live one's (the live cache of
// whoever rotated last), a file, or a link.
func TestKeepAdoptsACacheNoRecordNames(t *testing.T) {
	k, settings := newKeeping(t, nil)
	base := t.TempDir()
	k.fake.gocache = filepath.Join(base, "go-build-8")
	mkcache(t, k.fake.gocache)
	stray := filepath.Join(base, "go-build-7")
	mkcache(t, stray)
	left := []string{
		filepath.Join(base, "go-build-6"),  // no README: no Go cache
		filepath.Join(base, "go-build-12"), // a number past the live one's
		filepath.Join(base, "go-build-ym"), // no number
		filepath.Join(base, "go-build"),    // the bare name: whoever's default cache it may be
		filepath.Join(base, "other-3"),     // another stem
		filepath.Join(base, "go-build-5"),  // a file
		filepath.Join(base, "go-build-4"),  // a link
		filepath.Join(base, "go-build-3"),  // a link in place of Go's README
	}
	mkdir(t, left[0])
	mkcache(t, left[1])
	mkcache(t, left[2])
	mkcache(t, left[3])
	mkcache(t, left[4])
	if err := os.WriteFile(left[5], []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	mkcache(t, linked)
	if err := os.Symlink(linked, left[6]); err != nil {
		t.Fatal(err)
	}
	mkdir(t, left[7])
	if err := os.Symlink(filepath.Join(linked, "README"), filepath.Join(left[7], "README")); err != nil {
		t.Fatal(err)
	}
	k.free = []uint64{20 * GB, 20 * GB}

	rep := k.run(settings)

	if len(k.fake.sets) != 0 || rep.RotatedTo != "" {
		t.Errorf("go env -w ran %v, rotated to %q, with a cache just left beside the live one", k.fake.sets, rep.RotatedTo)
	}
	if !strings.Contains(rep.NotRotated, "no record names") {
		t.Errorf("NotRotated = %q", rep.NotRotated)
	}
	if len(rep.Adopted) != 1 || rep.Adopted[0] != stray {
		t.Errorf("Adopted = %v, want [%s]", rep.Adopted, stray)
	}
	if len(rep.Removed) != 0 {
		t.Errorf("Removed = %v, want nothing before the grace", rep.Removed)
	}
	if olds := k.olds(); len(olds) != 1 || olds[0].Dir != stray || !olds[0].At.Equal(k.now) {
		t.Errorf("recorded = %+v, want the stray at now", olds)
	}
	evs := k.events()
	if len(evs) != 1 {
		t.Fatalf("%d disk_kept events, want 1", len(evs))
	}
	if adopted, _ := evs[0].Data["adopted"].([]any); len(adopted) != 1 || adopted[0] != stray {
		t.Errorf("event adopted = %v, want [%s]", evs[0].Data["adopted"], stray)
	}
	if lines := rep.Lines(); len(lines) < 3 || !strings.HasPrefix(lines[2], "adopted "+stray+": ") {
		t.Errorf("Lines = %v", lines)
	}

	// Past the grace it counts from the sight: the stray goes, the rotation
	// with it, and nothing of the rest.
	k.now = k.now.Add(OldGrace + time.Minute)
	k.free = append(k.free, 20*GB, 40*GB)

	rep = k.run(settings)

	if len(rep.Adopted) != 0 || rep.NotRotated != "" {
		t.Errorf("Adopted = %v, NotRotated = %q on the second keeping", rep.Adopted, rep.NotRotated)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != stray {
		t.Errorf("Removed = %v, want [%s]", rep.Removed, stray)
	}
	if len(k.fake.sets) != 1 {
		t.Errorf("go env -w ran %v, want the rotation the held keeping left", k.fake.sets)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("%s stands past its grace: %v", stray, err)
	}
	for _, dir := range left {
		if _, err := os.Lstat(dir); err != nil {
			t.Errorf("%s was taken: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(linked, "README")); err != nil {
		t.Errorf("the link's target was taken: %v", err)
	}
}

// What a record says of the last rotation: a rotated sibling of the live
// cache, at or before now.
func TestLastRotation(t *testing.T) {
	live := filepath.Join("/cache", "go-build-8")
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	rec := func(dir string, ago time.Duration) store.GOCACHEDir {
		return store.GOCACHEDir{Dir: dir, At: at.Add(-ago)}
	}
	for _, c := range []struct {
		name string
		olds []store.GOCACHEDir
		want time.Duration // ago; 0 for none
	}{
		{"none", nil, 0},
		{"one", []store.GOCACHEDir{rec("/cache/go-build-7", 5*time.Minute)}, 5 * time.Minute},
		{"the newest of several", []store.GOCACHEDir{rec("/cache/go-build-5", time.Hour), rec("/cache/go-build-7", time.Minute), rec("/cache/go-build-6", 20*time.Minute)}, time.Minute},
		{"the bare name", []store.GOCACHEDir{rec("/cache/go-build", 3*time.Minute)}, 3 * time.Minute},
		{"the live cache itself", []store.GOCACHEDir{rec(live, time.Minute)}, 0},
		{"no sibling", []store.GOCACHEDir{rec("/elsewhere/go-build-7", time.Minute), rec("/cache/work", time.Minute), rec("/cache/go-build-7/..", time.Minute)}, 0},
		{"ahead of the clock", []store.GOCACHEDir{rec("/cache/go-build-7", -time.Hour)}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := lastRotation(c.olds, live, at)
			var want time.Time
			if c.want != 0 {
				want = at.Add(-c.want)
			}
			if !got.Equal(want) {
				t.Errorf("lastRotation = %v, want %v", got, want)
			}
		})
	}
}

// Sweep is the removal half of Keep and nothing else: whatever disk_keep_gb
// says, the directories whose grace has ended go, a rotation is not made and
// go-tmp is not touched, and what a keeping would not take stays: a cache
// younger than its grace by a second, the live cache and a path that is no
// rotated sibling (dropped from the record, never removed). A cache no record
// names is adopted as in Keep, and one disk_kept event says the sweep, with
// not_rotated; a sweep with nothing to do says nothing.
func TestSweepRemovesWhatIsPastItsGrace(t *testing.T) {
	k, _ := newKeeping(t, nil)
	base := t.TempDir()
	live := filepath.Join(base, "go-build-9")
	mkcache(t, live)
	exact := filepath.Join(base, "go-build-7")  // stood aside for exactly the grace
	almost := filepath.Join(base, "go-build-8") // a second short of it
	stray := filepath.Join(base, "go-build-5")  // a cache no record names
	for _, dir := range []string{exact, almost, stray} {
		mkcache(t, dir)
	}
	foreign := filepath.Join(t.TempDir(), "precious")
	mkdir(t, foreign)
	k.fake.gotmp = filepath.Join(base, "go-tmp")
	stale := filepath.Join(k.fake.gotmp, "go-build111")
	mkdir(t, stale)
	past := k.now.Add(-4 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	must(t, k.st.RecordGOCACHEOld(ctx, exact, k.now.Add(-OldGrace)))
	must(t, k.st.RecordGOCACHEOld(ctx, almost, k.now.Add(-OldGrace+time.Second)))
	// The live cache's own record, a day past its grace: a pointer moved back.
	must(t, k.st.RecordGOCACHEOld(ctx, live, k.now.Add(-24*time.Hour)))
	must(t, k.st.RecordGOCACHEOld(ctx, foreign, k.now.Add(-24*time.Hour)))
	// Over every threshold: a keeping would do nothing at all.
	k.free = []uint64{50 * GB, 60 * GB}

	rep, err := Sweep(ctx, k.st, live, k.now)
	if err != nil {
		t.Fatal(err)
	}

	if !rep.Acted || len(rep.Removed) != 1 || rep.Removed[0] != exact {
		t.Errorf("Acted = %v, Removed = %v, want [%s]", rep.Acted, rep.Removed, exact)
	}
	if len(rep.Adopted) != 1 || rep.Adopted[0] != stray {
		t.Errorf("Adopted = %v, want [%s]", rep.Adopted, stray)
	}
	if _, err := os.Stat(exact); !os.IsNotExist(err) {
		t.Errorf("%s stands at the end of its grace: %v", exact, err)
	}
	for _, dir := range []string{almost, live, foreign, stray, stale} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s was taken: %v", dir, err)
		}
	}
	if len(k.fake.sets) != 0 {
		t.Errorf("go env -w ran %v: a sweep rotates nothing", k.fake.sets)
	}
	var recorded []string
	for _, o := range k.olds() {
		recorded = append(recorded, o.Dir)
	}
	want := []string{almost, live, stray}
	slices.Sort(want)
	if !slices.Equal(recorded, want) {
		t.Errorf("recorded = %v, want %v: the removed and the foreign dropped, the adopted added", recorded, want)
	}
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "not a rotated sibling") {
		t.Errorf("Errors = %v, want the foreign path told", rep.Errors)
	}
	evs := k.events()
	if len(evs) != 1 {
		t.Fatalf("%d disk_kept events, want 1", len(evs))
	}
	data := evs[0].Data
	if data["free_before"] != float64(50*GB) || data["free_after"] != float64(60*GB) || data["gave_up"] != false {
		t.Errorf("event free %v -> %v, gave_up %v", data["free_before"], data["free_after"], data["gave_up"])
	}
	if data["rotated_from"] != "" || data["rotated_to"] != "" || data["not_rotated"] != rep.NotRotated || rep.NotRotated == "" {
		t.Errorf("event rotation %v -> %v, not_rotated %v: a sweep says it rotated nothing", data["rotated_from"], data["rotated_to"], data["not_rotated"])
	}
	if removed, _ := data["removed"].([]any); len(removed) != 1 || removed[0] != exact {
		t.Errorf("event removed = %v, want [%s]", data["removed"], exact)
	}
	if adopted, _ := data["adopted"].([]any); len(adopted) != 1 || adopted[0] != stray {
		t.Errorf("event adopted = %v, want [%s]", data["adopted"], stray)
	}
	if evs[0].Actor != Actor || evs[0].Bead != nil {
		t.Errorf("event actor = %q, bead = %v, want %q and none", evs[0].Actor, evs[0].Bead, Actor)
	}

	// With the second gone the same sweep finds nothing past its grace: it
	// reads the free space once, logs nothing and says it did not act.
	k.free = append(k.free, 50*GB)
	rep, err = Sweep(ctx, k.st, live, k.now.Add(time.Second-1))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Acted || len(rep.Removed) != 0 || len(rep.Adopted) != 0 {
		t.Errorf("an idle sweep reports %+v", rep)
	}
	if evs := k.events(); len(evs) != 1 {
		t.Errorf("%d disk_kept events after an idle sweep, want still 1", len(evs))
	}
}

// The time the next sweep has something to remove from: the earliest record
// and the grace, whatever the record names.
func TestDue(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	rec := func(dir string, ago time.Duration) store.GOCACHEDir {
		return store.GOCACHEDir{Dir: dir, At: at.Add(-ago)}
	}
	if _, ok := Due(nil); ok {
		t.Errorf("Due of nothing recorded is a time")
	}
	got, ok := Due([]store.GOCACHEDir{rec("/cache/go-build-6", 5*time.Minute), rec("/cache/go-build-5", 20*time.Minute), rec("/cache/go-build-7", time.Minute)})
	if want := at.Add(-20*time.Minute + OldGrace); !ok || !got.Equal(want) {
		t.Errorf("Due = %v, %v, want %v: the earliest record's grace", got, ok, want)
	}
}

// The live cache is the one go answers with, as a clean absolute path: any
// other answer would make every record look foreign to a sweep, which drops
// what is foreign from the record unremoved. Keep asks the same, and refuses
// before it does anything.
func TestLiveGOCACHE(t *testing.T) {
	k, settings := newKeeping(t, nil)
	k.fake.gocache = "/cache/go-build-8"
	if got, err := LiveGOCACHE(); err != nil || got != "/cache/go-build-8" {
		t.Fatalf("LiveGOCACHE = %q, %v", got, err)
	}
	for _, answer := range []string{
		"",
		"go-build-8",
		"/cache/go-build-8/",
		"/cache/../cache/go-build-8",
		"go: downloading go1.99 (darwin/arm64)\n/cache/go-build-8",
	} {
		k.fake.gocache = answer
		if got, err := LiveGOCACHE(); err == nil {
			t.Errorf("LiveGOCACHE of %q = %q, want an error", answer, got)
		}
	}
	if _, err := Keep(context.Background(), k.st, settings); err == nil || !strings.Contains(err.Error(), "go env GOCACHE") {
		t.Errorf("Keep with a garbled GOCACHE: %v, want the refusal", err)
	}
	if len(k.fake.sets) != 0 || len(k.events()) != 0 {
		t.Errorf("Keep did something about a GOCACHE it could not read: %v", k.fake.sets)
	}
}
