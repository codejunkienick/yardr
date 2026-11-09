package diskkeep

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A rotated GOCACHE directory can only be held by a go process that started
// before the keeping moved GOCACHE away from it: go reads GOCACHE once, as it
// starts, from the go env file the rotation wrote (a keeping refuses a rotation
// where the environment wins over that file), and `go test` does not hand it to
// its test binaries, which run under their go. So a cache is idle when no
// process whose command is go started before the record of its rotation, and
// the process table says so.
//
// A process the table does not show can hold a cache all the same (a virtual
// machine or a container that mounts the directory), and so can a go command
// that was given this directory in its own environment, or a tool that is not
// go and reads files at paths it got from `go list -export` before the
// rotation: docs/setup.md, "The machine's disk", names them. They are a rarer
// exposure than the grace's own guess that a build is over in 30 minutes.

// psWait is how long ps is given to print the table. A ps that does not answer
// must not hold a landing's keeping or the daemon's sweep; a table that did not
// come holds every cache.
var psWait = 10 * time.Second

// psTable prints the process table: the process id, when the process started
// and its command, one process to a line. Tests stand a fake in.
//
// ps is asked for UTC. Left to itself it prints lstart in local time, which is
// read wrongly in two ways, each of which can make a process look younger than
// it is, the unsafe way: the hour when daylight saving ends is on the clock
// twice, and the zone a daemon first read (Go reads it once) is not the one ps
// reads afresh once the machine has crossed a zone. In UTC there is no fold to
// find and no zone to have gone stale. LC_ALL=C keeps the names of the weekday
// and the month English. Both macOS's ps and procps's honor TZ for lstart.
var psTable = func(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, psWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,lstart=,comm=")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	// A ps that was killed at the deadline must not leave Output waiting for a
	// child that holds its pipe.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return out, nil
}

// startLayout is lstart as ps prints it in the C locale, "Mon Nov  9 06:21:15
// 2026", once strings.Fields has taken the padding of a one-digit day out. The
// weekday is not checked against the date.
const startLayout = "Mon Jan 2 15:04:05 2006"

// goProcs says when the oldest go process started, from the process table:
// running is false where none is. A cache rotated away from at rotated is held
// by it if the oldest started at that time or before: ps counts whole seconds
// and cuts them off, which errs toward held.
type goProcs struct {
	oldest  time.Time
	running bool
}

// holds reports whether a go process may be using the cache that was rotated
// away from at rotated.
func (p goProcs) holds(rotated time.Time) bool {
	return p.running && !p.oldest.After(rotated)
}

// readGoProcs reads the process table. Anything it cannot read is an error, and
// the caller takes an error for a go process older than every rotation, so a
// table that is wrong holds every cache: ps missing or failing, no process at
// all, a line without a process id or without the fields before the command,
// and a go process whose start does not parse.
func readGoProcs(ctx context.Context) (goProcs, error) {
	table, err := psTable(ctx)
	if err != nil {
		return goProcs{}, err
	}
	return parseGoProcs(string(table))
}

// parseGoProcs reads a table as psTable prints it. A line is the process id,
// the five words of lstart, and then the command, which is the rest of the line
// since a path can hold spaces (macOS prints the full path, procps the bare
// name). A command is go by its basename, so both are one rule, and the start
// of a process that is not go is not read.
func parseGoProcs(table string) (goProcs, error) {
	var procs goProcs
	lines := 0
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if _, err := strconv.Atoi(f[0]); err != nil || len(f) < 6 {
			return goProcs{}, fmt.Errorf("ps printed %q, which is no process", line)
		}
		lines++
		if filepath.Base(strings.Join(f[6:], " ")) != "go" {
			continue
		}
		started, err := time.Parse(startLayout, strings.Join(f[1:6], " "))
		if err != nil {
			return goProcs{}, fmt.Errorf("go process %s: its start time %q cannot be read: %w", f[0], strings.Join(f[1:6], " "), err)
		}
		if !procs.running || started.Before(procs.oldest) {
			procs.oldest, procs.running = started, true
		}
	}
	if lines == 0 {
		return goProcs{}, fmt.Errorf("ps printed no process")
	}
	return procs, nil
}
