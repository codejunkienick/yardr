// Package diskkeep keeps the machine's disk free for landings' builds.
// Before a landing's tests run, the assembly keeps the disk: under
// disk_keep_gb free on GOCACHE's volume it rotates GOCACHE at a fresh
// sibling directory (so builders that start afterwards use it) unless the
// last rotation is less than 30 minutes old, removes a rotated directory
// once it has stood aside for 30 minutes, and clears go-build directories in
// GOTMPDIR older than 3 hours. It logs what it did as a disk_kept event, and
// says it gave up when free space is still under 10 GB afterwards.
//
// GOCACHE is the machine's, one go env file for every yard on it, while the
// record of rotations is one yard's store. So a keeping also records the
// rotated caches no record names, whichever yard left them.
package diskkeep

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"yardr.dev/yardr/internal/store"
)

// Actor is who the disk_kept event is told as: the yard itself, whatever
// ran the keeping.
const Actor = "yard"

// GB is a gigabyte, the unit disk_keep_gb and the gave-up floor are read in.
const GB = 1 << 30

// GaveUpGB is the free space below which a keeping that is over still says
// it gave up: rotation and pruning freed nothing like enough, and a person
// has to look at the disk.
const GaveUpGB = 10

// OldGrace is how long a rotated GOCACHE directory stands aside before a
// landing removes it: builds that started before the rotation still write
// to it, and files are never taken from under a running build. It is also
// how long after a rotation the next one waits: a rotation inside it frees
// nothing, since the directory it leaves cannot go yet, and the cache it
// starts must be filled from empty.
const OldGrace = 30 * time.Minute

// goCacheMarker is the first line of the README Go writes into every build
// cache it makes: a directory without it is no Go cache, whatever it is
// called, and is never taken as one.
const goCacheMarker = "This directory holds cached build artifacts from the Go build system."

// TmpAge is how old a go-build directory in GOTMPDIR must be before it is
// cleared: a build's own scratch is fresh, and only the stale is taken.
const TmpAge = 3 * time.Hour

// freeDisk reports the bytes free for an unprivileged process on the volume
// holding path; tests stand a fake in.
var freeDisk = func(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// goEnv runs go env for the names; tests stand a fake in.
var goEnv = func(names ...string) (string, error) {
	out, err := exec.Command("go", append([]string{"env"}, names...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// goEnvSet runs go env -w for one assignment; tests stand a fake in.
var goEnvSet = func(kv string) error {
	return exec.Command("go", "env", "-w", kv).Run()
}

// hasGo reports whether go is on PATH; tests stand a fake in. Without go
// there is no GOCACHE to rotate and no go-tmp to clear, so there is nothing
// to keep.
var hasGo = func() bool {
	_, err := exec.LookPath("go")
	return err == nil
}

// now is the time; tests stand a fake in.
var now = time.Now

// Report is what one keeping did.
type Report struct {
	// Acted is whether the disk was under the threshold and anything was
	// done at all.
	Acted bool `json:"acted"`
	// FreeBefore and FreeAfter are the free bytes on GOCACHE's volume
	// before and after.
	FreeBefore uint64 `json:"free_before"`
	FreeAfter  uint64 `json:"free_after"`
	// RotatedFrom and RotatedTo are the GOCACHE the keeping moved away
	// from and the fresh sibling it points at now; both are empty when
	// NotRotated says why it did not.
	RotatedFrom string `json:"rotated_from"`
	RotatedTo   string `json:"rotated_to"`
	// NotRotated says why GOCACHE stayed where it is though the disk was
	// under the threshold: a rotation stands inside OldGrace. Empty when it
	// was rotated.
	NotRotated string `json:"not_rotated,omitempty"`
	// Adopted is every rotated GOCACHE directory no record named, which the
	// keeping recorded as stood aside from now.
	Adopted []string `json:"adopted,omitempty"`
	// Removed is every directory the keeping took from the disk: rotated
	// GOCACHE directories past their grace, and stale go-tmp.
	Removed []string `json:"removed"`
	// GaveUp is whether free space is still under 10 GB after the keeping.
	GaveUp bool `json:"gave_up"`
	// Errors is what went wrong past the rotation, each a sentence: a
	// directory that could not be removed stays for the next landing.
	Errors []string `json:"errors,omitempty"`
}

// Lines says the report in human lines: nothing when nothing was done.
func (r *Report) Lines() []string {
	if r == nil || !r.Acted {
		return nil
	}
	lines := []string{fmt.Sprintf("disk kept: free %s -> %s", gigabytes(r.FreeBefore), gigabytes(r.FreeAfter))}
	if r.NotRotated != "" {
		lines = append(lines, "GOCACHE not rotated: "+r.NotRotated)
	} else {
		lines = append(lines, fmt.Sprintf("rotated GOCACHE %s -> %s", r.RotatedFrom, r.RotatedTo))
	}
	for _, dir := range r.Adopted {
		lines = append(lines, fmt.Sprintf("adopted %s: no record named it, so its %d minutes start now", dir, int(OldGrace.Minutes())))
	}
	for _, dir := range r.Removed {
		lines = append(lines, "removed "+dir)
	}
	for _, err := range r.Errors {
		lines = append(lines, "disk keep: "+err)
	}
	if r.GaveUp {
		lines = append(lines, fmt.Sprintf("gave up: free space still under %d GB after keeping the disk", GaveUpGB))
	}
	return lines
}

// gigabytes says bytes in gigabytes with one decimal.
func gigabytes(n uint64) string {
	return fmt.Sprintf("%.1f GB", float64(n)/GB)
}

// Keep keeps the disk when it is under the threshold in settings, and
// reports what it did. Over the threshold, with the keeping turned off, or
// without go, it does nothing and reports that, with no event: an idle
// keeping leaves no record.
//
// Under it, GOCACHE is rotated unless a rotation stands inside OldGrace:
// that one's directory cannot go yet, so another rotation frees nothing and
// only starts one more cache from empty. A keeping that holds the rotation
// still removes what is past its grace, clears go-tmp, and says so.
//
// Past the rotation (or the decision to hold it) everything is best effort:
// a directory that cannot be removed, or a free-space reading that fails,
// joins the report's errors and the disk_kept event carries them, so the
// landing goes on to its tests with the disk as it is. Only what fails
// before anything was done is an error.
func Keep(ctx context.Context, st store.API, settings map[string]string) (*Report, error) {
	thresholdGB := uint64(store.DiskKeepGB(settings))
	if thresholdGB == 0 || !hasGo() {
		return &Report{}, nil
	}
	gocache, err := goEnv("GOCACHE")
	if err != nil {
		return nil, fmt.Errorf("go env GOCACHE: %w", err)
	}
	if gocache == "" {
		return nil, fmt.Errorf("go env GOCACHE: empty")
	}
	free, err := freeDisk(existingDir(gocache))
	if err != nil {
		return nil, fmt.Errorf("free space on %s: %w", gocache, err)
	}
	// Comparing whole gigabytes avoids overflowing when a valid setting is
	// larger than the largest byte count uint64 can hold.
	if free/GB >= thresholdGB {
		return &Report{FreeBefore: free, FreeAfter: free}, nil
	}
	// Every yard on the machine shares go's GOCACHE setting but not its
	// rotation record, so serialize the decision and the pointer update across
	// yards. Re-read GOCACHE after taking the lock: another keeping may have
	// rotated it while this one waited.
	var lock *os.File
	defer func() {
		if lock != nil {
			_ = lock.Close()
		}
	}()
	for {
		lock, err = lockGOCACHE(gocache)
		if err != nil {
			return nil, fmt.Errorf("lock GOCACHE keeping: %w", err)
		}
		current, err := goEnv("GOCACHE")
		if err != nil {
			return nil, fmt.Errorf("go env GOCACHE: %w", err)
		}
		if current == "" {
			return nil, fmt.Errorf("go env GOCACHE: empty")
		}
		if filepath.Dir(current) != filepath.Dir(gocache) {
			if err := lock.Close(); err != nil {
				lock = nil
				return nil, fmt.Errorf("close GOCACHE keeping lock: %w", err)
			}
			lock = nil
			gocache = current
			free, err = freeDisk(existingDir(gocache))
			if err != nil {
				return nil, fmt.Errorf("free space on %s: %w", gocache, err)
			}
			if free/GB >= thresholdGB {
				return &Report{FreeBefore: free, FreeAfter: free}, nil
			}
			continue
		}
		gocache = current
		break
	}
	rep := &Report{Acted: true, FreeBefore: free, FreeAfter: free, Removed: []string{}}
	at := now()
	olds, err := st.GOCACHEOldDirs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list rotated GOCACHE directories: %w", err)
	}
	live := gocache
	strays := strayCaches(rep, olds, gocache)
	if why := holdsRotation(olds, strays, gocache, at); why != "" {
		rep.NotRotated = why
		rep.Adopted = adopt(ctx, st, rep, strays, at)
	} else {
		if err := rotate(ctx, st, rep, gocache); err != nil {
			return nil, err
		}
		live = rep.RotatedTo
	}
	// The old caches already have their grace records; don't keep the lock
	// while removing their files or cleaning Go's scratch directories.
	if err := lock.Close(); err != nil {
		lock = nil
		rep.Errors = append(rep.Errors, fmt.Sprintf("could not close GOCACHE keeping lock: %v", err))
	} else {
		lock = nil
	}
	rep.Removed = append(rep.Removed, removeOld(ctx, st, rep, live, at)...)
	rep.Removed = append(rep.Removed, pruneTmp(rep, at)...)
	if free, err := freeDisk(existingDir(live)); err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("could not read free space after: %v", err))
	} else {
		rep.FreeAfter = free
	}
	rep.GaveUp = rep.FreeAfter < GaveUpGB*GB
	data := map[string]any{
		"free_before":  rep.FreeBefore,
		"free_after":   rep.FreeAfter,
		"rotated_from": rep.RotatedFrom,
		"rotated_to":   rep.RotatedTo,
		"removed":      rep.Removed,
		"gave_up":      rep.GaveUp,
	}
	if rep.NotRotated != "" {
		data["not_rotated"] = rep.NotRotated
	}
	if len(rep.Adopted) > 0 {
		data["adopted"] = rep.Adopted
	}
	if len(rep.Errors) > 0 {
		data["errors"] = rep.Errors
	}
	if err := st.LogDispatch(ctx, "", store.DiskKept, Actor, data); err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("could not log the %s event: %v", store.DiskKept, err))
	}
	return rep, nil
}

// lockGOCACHE serializes keepings that use the same cache parent, even when
// they run from different yards with different stores. The lock is a sibling
// of GOCACHE, outside the numbered cache directories; closing it releases
// the lock before the keeping removes anything.
func lockGOCACHE(gocache string) (*os.File, error) {
	path := filepath.Join(filepath.Dir(gocache), ".yardr-diskkeep.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("set permissions on %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

// holdsRotation says why GOCACHE is not rotated now, or is "" where it may
// be. It is not while a rotation stands inside OldGrace: the newest recorded
// one, or one nothing recorded, which a rotated cache no record names
// betrays and which, for all that is known, was just now. GOCACHE is the
// machine's and the record one yard's, so a rotation by another yard on the
// machine is seen no other way.
func holdsRotation(olds []store.GOCACHEDir, strays []string, live string, at time.Time) string {
	if len(strays) > 0 {
		return "a rotated cache no record names stands beside it, so GOCACHE may have been rotated just now"
	}
	last := lastRotation(olds, live, at)
	if last.IsZero() || at.Sub(last) >= OldGrace {
		return ""
	}
	return fmt.Sprintf("the last rotation was %s ago, inside the %d minutes a rotated cache stands aside", at.Sub(last).Truncate(time.Second), int(OldGrace.Minutes()))
}

// lastRotation is the time of the newest record of a rotation away from a
// sibling of live, or the zero time where there is none. A record of live
// itself, of a path that is no rotated sibling of it, or dated after at says
// nothing of when a rotation happened: the first is a pointer moved back, the
// second a claim any caller of the socket could make, the third a clock set
// back or a hand's record, and a time ahead would hold the rotation until the
// clock caught up.
func lastRotation(olds []store.GOCACHEDir, live string, at time.Time) time.Time {
	var last time.Time
	for _, old := range olds {
		if old.Dir == live || !rotatedSibling(old.Dir, live) || old.At.After(at) {
			continue
		}
		if old.At.After(last) {
			last = old.At
		}
	}
	return last
}

// rotate points GOCACHE at the next fresh sibling of gocache and records
// gocache as stood aside from now.
func rotate(ctx context.Context, st store.API, rep *Report, gocache string) error {
	next, err := freshSibling(gocache)
	if err != nil {
		return err
	}
	if err := goEnvSet("GOCACHE=" + next); err != nil {
		return fmt.Errorf("go env -w GOCACHE=%s: %w", next, err)
	}
	// The rotation took only if go now answers with the new directory: a
	// GOCACHE from the environment wins over the file `go env -w` wrote,
	// and recording the old one as stood aside would lose it. Another
	// keeping on the machine that rotated in between answers otherwise too;
	// the directory this one left is then found by the next keeping.
	if cur, err := goEnv("GOCACHE"); err != nil {
		return fmt.Errorf("go env GOCACHE: %w", err)
	} else if cur != next {
		return fmt.Errorf("GOCACHE is %s after go env -w GOCACHE=%s: the environment overrides the go env file, or another keeping rotated it just now", cur, next)
	}
	rep.RotatedFrom, rep.RotatedTo = gocache, next
	if err := st.RecordGOCACHEOld(ctx, gocache, now()); err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("could not record %s as rotated away from: %v", gocache, err))
	}
	return nil
}

// strayCaches lists the rotated caches beside live that no record names. A
// rotation leaves one when it was not recorded here: another yard on the
// machine rotates the same GOCACHE and records into its own store, and a
// keeping killed between its rotation and its record records nothing. With
// no record nothing would ever remove it. Only a numbered sibling of live
// with a lower number is taken, a real directory holding the README Go
// writes into every cache it makes: rotations count upward, so a higher
// number is the live cache of whoever rotated last, and what is no Go cache
// is never ours to remove.
func strayCaches(rep *Report, olds []store.GOCACHEDir, live string) []string {
	stem, n, numbered := splitNumber(filepath.Base(live))
	if !numbered {
		// Never rotated to, so nothing was rotated away from before it.
		return nil
	}
	parent := filepath.Dir(live)
	entries, err := os.ReadDir(parent)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("could not read %s: %v", parent, err))
		return nil
	}
	known := make(map[string]bool, len(olds))
	for _, old := range olds {
		known[old.Dir] = true
	}
	var strays []string
	for _, e := range entries {
		have, m, ok := splitNumber(e.Name())
		dir := filepath.Join(parent, e.Name())
		if !e.IsDir() || !ok || have != stem || m >= n || known[dir] || !isGoCache(dir) {
			continue
		}
		strays = append(strays, dir)
	}
	return strays
}

// isGoCache reports whether dir holds the README Go writes into a build
// cache when it makes one.
func isGoCache(dir string) bool {
	readme := filepath.Join(dir, "README")
	info, err := os.Lstat(readme)
	if err != nil || !info.Mode().IsRegular() {
		// Opening a FIFO here would wait for a writer and stop the keeping.
		return false
	}
	f, err := os.Open(readme)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(goCacheMarker))
	n, _ := io.ReadFull(f, buf)
	return string(buf[:n]) == goCacheMarker
}

// adopt records each stray as stood aside from at, and returns the ones it
// recorded. Its grace starts at first sight, which is never sooner than it
// was left, so it is removed no sooner than any rotation's directory is.
func adopt(ctx context.Context, st store.API, rep *Report, strays []string, at time.Time) []string {
	var adopted []string
	for _, dir := range strays {
		if err := st.RecordGOCACHEOld(ctx, dir, at); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("could not record %s as rotated away from: %v", dir, err))
			continue
		}
		adopted = append(adopted, dir)
	}
	return adopted
}

// removeOld removes the recorded GOCACHE directories whose grace has
// passed, except the live one, and drops each from the record once it is
// gone. A directory that cannot be removed stays recorded for the next
// landing.
func removeOld(ctx context.Context, st store.API, rep *Report, live string, at time.Time) []string {
	olds, err := st.GOCACHEOldDirs(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("could not list rotated GOCACHE directories: %v", err))
		return nil
	}
	var removed []string
	for _, old := range olds {
		if old.Dir == live {
			continue
		}
		// The record is writable by any process on the yard's socket, so a
		// path in it is only a claim: only a rotation's own kind of
		// directory (a numbered sibling of the live cache) is ever removed,
		// whatever time it carries. Anything else is dropped unremoved.
		if !rotatedSibling(old.Dir, live) {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s is not a rotated sibling of %s: dropped from the record, not removed", old.Dir, live))
			if err := st.RemoveGOCACHEOld(ctx, old.Dir); err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("could not drop %s from the record: %v", old.Dir, err))
			}
			continue
		}
		if at.Sub(old.At) < OldGrace {
			continue
		}
		if err := os.RemoveAll(old.Dir); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("could not remove %s: %v", old.Dir, err))
			continue
		}
		if err := st.RemoveGOCACHEOld(ctx, old.Dir); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("could not drop %s from the record: %v", old.Dir, err))
			continue
		}
		removed = append(removed, old.Dir)
	}
	return removed
}

// pruneTmp removes the go-build directories in GOTMPDIR older than TmpAge.
// Without a GOTMPDIR, or with none there, there is nothing to clear.
func pruneTmp(rep *Report, at time.Time) []string {
	tmp, err := goEnv("GOTMPDIR")
	if err != nil || tmp == "" {
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("go env GOTMPDIR: %v", err))
		}
		return nil
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		if !os.IsNotExist(err) {
			rep.Errors = append(rep.Errors, fmt.Sprintf("could not read %s: %v", tmp, err))
		}
		return nil
	}
	var removed []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "go-build") || !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || at.Sub(info.ModTime()) < TmpAge {
			continue
		}
		dir := filepath.Join(tmp, e.Name())
		if err := os.RemoveAll(dir); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("could not remove %s: %v", dir, err))
			continue
		}
		removed = append(removed, dir)
	}
	return removed
}

// existingDir is path where it is a directory, else the nearest directory
// above it that is: a GOCACHE rotated to but not yet built in does not
// exist, and free space is its volume's all the same.
func existingDir(path string) string {
	for {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// freshSibling is the next fresh sibling directory after dir: its numeric
// suffix raised by one (go-build-8 gives go-build-9), or -1 where it has
// none, past any that already exist.
func freshSibling(dir string) (string, error) {
	next := dir
	for range 100 {
		next = nextName(next)
		if _, err := os.Stat(next); os.IsNotExist(err) {
			return next, nil
		}
	}
	return "", fmt.Errorf("no fresh sibling directory after %s", dir)
}

// splitNumber splits name at its numeric suffix: go-build-8 gives go-build
// and 8. A name with none gives itself, 0 and false.
func splitNumber(name string) (stem string, n int, numbered bool) {
	if i := strings.LastIndex(name, "-"); i >= 0 {
		if num, err := strconv.Atoi(name[i+1:]); err == nil {
			return name[:i], num, true
		}
	}
	return name, 0, false
}

// nextName raises the numeric suffix of dir's last path element by one.
func nextName(dir string) string {
	if stem, n, ok := splitNumber(filepath.Base(dir)); ok {
		return filepath.Join(filepath.Dir(dir), stem+"-"+strconv.Itoa(n+1))
	}
	return dir + "-1"
}

// rotatedSibling reports whether dir is a directory a rotation of live
// could have stood aside: in live's parent, named as live is with its
// numeric suffix left off, bare or numbered.
func rotatedSibling(dir, live string) bool {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || filepath.Dir(dir) != filepath.Dir(live) {
		return false
	}
	want, _, _ := splitNumber(filepath.Base(live))
	have, _, numbered := splitNumber(filepath.Base(dir))
	return have == want && (numbered || filepath.Base(dir) == want) && want != ""
}
