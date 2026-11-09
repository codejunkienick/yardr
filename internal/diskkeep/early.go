package diskkeep

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"yardr.dev/yardr/internal/store"
)

// A rotated GOCACHE directory stands aside for OldGrace because a build that
// started before the rotation still writes to it. Where no go process started
// before the rotation (goprocs.go) that reason cannot apply, and the grace only
// keeps gigabytes on a disk that a gate may be refused by: the gate stops at
// FloorGB free (.yardr/test, docs/testing.md "The disk floor"), and on 9 Nov
// 2026 a landing was stopped there while a rotated cache that nothing held
// stood inside its grace. So while the disk is under the floor an idle cache
// goes before its grace ends. Above the floor nothing does, so the grace stays
// what it was: the guard of every disk that is not short.

// FloorGB is the free space, in whole gigabytes, under which the gate refuses to
// start: floorgib in .yardr/test, which a test holds this to. It is a constant
// beside the script's and no setting of the yard: a setting would be one more
// number for an operator to keep equal to the script's.
const FloorGB = 20

// floorEnv is the variable a yard gives its gates another floor with, and 0
// turns their check off.
const floorEnv = "YARDR_TEST_FLOOR_GIB"

// floorBytes is the floor in bytes, read as the gate reads it: FloorGB where
// floorEnv is unset or is not all digits, else what it says. It is 0 for a yard
// that turned the gate's check off, and then no cache goes before its grace:
// there is no floor to be held at. The variable is the gate's own, which the
// docs already have exported in the daemon's environment, so the two floors are
// one number wherever they share one. It is also how a test of another package,
// which runs a real Sweep or Keep against the machine's real disk, stays
// clear of it (the gate's own tests give 0 the same way).
func floorBytes() uint64 {
	gib := uint64(FloorGB)
	if n, err := strconv.ParseUint(os.Getenv(floorEnv), 10, 32); err == nil {
		gib = n
	}
	return gib * GB
}

// standing lists the recorded directories that stand on the disk inside their
// grace at at: the ones an early removal could take.
func standing(olds []store.GOCACHEDir, at time.Time) []store.GOCACHEDir {
	var out []store.GOCACHEDir
	for _, old := range olds {
		if at.Sub(old.At) >= OldGrace {
			continue
		}
		if _, err := os.Lstat(old.Dir); err != nil {
			continue
		}
		out = append(out, old)
	}
	return out
}

// Early reports whether a sweep could take a rotated cache before its grace
// ends: a recorded directory stands on the disk inside its grace, and the free
// space on its volume is under the floor. It is the daemon's look at each pass,
// so it reads no process table, which only a sweep that finds it true does. The
// daemon does not know the live cache, nor does it ask go on each pass, so a
// directory is judged by its own parent: a rotated cache is beside the live one.
func Early(olds []store.GOCACHEDir, at time.Time) bool {
	floor := floorBytes()
	if floor == 0 {
		return false
	}
	for _, old := range standing(olds, at) {
		if free, err := freeDisk(existingDir(filepath.Dir(old.Dir))); err == nil && free < floor {
			return true
		}
	}
	return false
}

// removeIdle removes the recorded GOCACHE directories that stand inside their
// grace, while free space on their volume is under the floor and no go process
// can hold them, and returns them; they are also the report's Early. It takes
// what removeOld does, with the same rules for what may be taken (a recorded
// rotated sibling of the live cache, never the live one), oldest rotation
// first, since a go process that started before a rotation is the only one to
// hold what that rotation left, and an older rotation is held by fewer.
//
// The record stays. It is the keeping's hold: the next keeping must still see
// the rotation, or it would rotate again at once, the churn a rotation inside
// the last one's grace was made to stop. removeOld drops it when the grace ends.
//
// The process table is read once, and only when a directory stands and the
// volume is under the floor; free space is read again after each removal, so
// nothing goes once the volume is over the floor. A table that cannot be read
// holds every cache and is told as an error, as is a removal that fails: the
// directory is still taken at the end of its grace.
func removeIdle(ctx context.Context, st store.API, rep *Report, live string, at time.Time) []string {
	floor := floorBytes()
	if floor == 0 {
		return nil
	}
	olds, err := st.GOCACHEOldDirs(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("could not list rotated GOCACHE directories: %v", err))
		return nil
	}
	var cands []store.GOCACHEDir
	for _, old := range standing(olds, at) {
		if old.Dir != live && rotatedSibling(old.Dir, live) {
			cands = append(cands, old)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	free, err := freeDisk(existingDir(live))
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("free space on %s: %v", live, err))
		return nil
	}
	if free >= floor {
		return nil
	}
	procs, err := readGoProcs(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("could not tell whether a go process holds a rotated cache, so none is taken inside its grace: %v", err))
		return nil
	}
	slices.SortFunc(cands, func(a, b store.GOCACHEDir) int { return a.At.Compare(b.At) })
	var removed []string
	for _, c := range cands {
		if procs.holds(c.At) {
			continue
		}
		if err := os.RemoveAll(c.Dir); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("could not remove %s: %v", c.Dir, err))
			continue
		}
		removed = append(removed, c.Dir)
		if free, err = freeDisk(existingDir(live)); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("free space on %s: %v", live, err))
			break
		}
		if free >= floor {
			break
		}
	}
	rep.Early = append(rep.Early, removed...)
	return removed
}
