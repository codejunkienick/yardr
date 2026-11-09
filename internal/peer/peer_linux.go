package peer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// Linux reports the peer's credentials through SO_PEERCRED, and a
// process's state, start time and parent in /proc/<pid>/stat.
func peerPID(fd int) (int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(cred.Pid), nil
}

var procOnce = sync.OnceValue(func() bool {
	_, err := os.Stat("/proc/self/stat")
	return err == nil
})

// HaveStat is false where there is no /proc.
func HaveStat() bool { return procOnce() }

// readFile reads a file under /proc. It is a variable so that a test can
// hand stat the error a process that exits mid-read gives, which no real
// process can be made to give on demand.
var readFile = os.ReadFile

// stat reads /proc/<pid>/stat. The start time is in clock ticks after
// boot. A pid with no process is fs.ErrNotExist, and so is one that exits
// while its stat is read.
func stat(pid int) (Stat, error) {
	b, err := readFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, unix.ESRCH) {
		// A process that goes after the open found it answers the read with
		// ESRCH, where one gone before the open is ENOENT. Callers take only
		// fs.ErrNotExist for no process, so both must read as that.
		return Stat{}, fmt.Errorf("pid %d: %w", pid, fs.ErrNotExist)
	}
	if err != nil {
		return Stat{}, err
	}
	// The command name may hold spaces and parentheses; fields resume after
	// the last ')'. State is field 3, ppid field 4, starttime field 22.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return Stat{}, fmt.Errorf("pid %d: unexpected stat %q", pid, b)
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return Stat{}, fmt.Errorf("pid %d: unexpected stat %q", pid, b)
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return Stat{}, fmt.Errorf("pid %d: ppid: %w", pid, err)
	}
	start, err := strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return Stat{}, fmt.Errorf("pid %d: start time: %w", pid, err)
	}
	return Stat{State: f[0][0], Start: start, PPID: ppid}, nil
}
