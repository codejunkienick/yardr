package testload

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Running reports whether pid is a live process. A zombie is not running:
// kill -0 still answers for one, so where it says alive the state is read
// with ps and a zombie counts as dead. ps listing nothing means the process
// died between the checks.
//
// A test that waits for a process it killed asks this, not kill -0. A killed
// process stays a zombie until its parent, or the init that adopted it, waits
// for it. Where that init does not (go itself, as the first process of a
// `docker run` without --init) it never goes, and a wait on kill -0 runs out.
func Running(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return !Zombie(strings.TrimSpace(string(out)))
}

// GroupRunning is Running's question asked of a process group: whether any
// process of group pgid is running, for a test that waits for everything a
// killed command started. kill -0 on the group answers for a zombie of it as
// it does for one process, so where it says alive ps lists every process by
// group and state, and a group of zombies alone is not running. Like kill -0
// it signals nothing: a group id the kernel has handed to unrelated processes
// since is never harmed.
func GroupRunning(pgid int) bool {
	if pgid <= 1 || syscall.Kill(-pgid, 0) != nil {
		return false
	}
	out, err := exec.Command("ps", "-A", "-o", "pgid=,stat=").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == strconv.Itoa(pgid) && !Zombie(f[1]) {
			return true
		}
	}
	return false
}

// Zombie reports whether a ps STAT word names a process that is not
// running: a zombie, or nothing at all where ps lists no such process.
func Zombie(stat string) bool { return stat == "" || strings.Contains(stat, "Z") }
