package peer

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// A process that exits while its stat is read is gone, as one that had
// exited before the open is: the open succeeds, /proc answers the read with
// ESRCH, and every caller takes only fs.ErrNotExist for no process.
// (yardr-sjgk: Exec.Alive returned it as a fault, and a pack contract test
// failed on it in CI.) Linux only, as the seam is: macOS's sysctl answers a
// missing process with no record, which is fs.ErrNotExist already.
func TestProcessExitingMidReadIsGone(t *testing.T) {
	// Serial: it swaps the package's read.
	old := readFile
	readFile = func(name string) ([]byte, error) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: syscall.ESRCH}
	}
	t.Cleanup(func() { readFile = old })

	const pid = 4242
	_, err := Describe(pid)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a process that exits mid-read: %v, want %v", err, fs.ErrNotExist)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(pid)) {
		t.Fatalf("%q does not name pid %d", err, pid)
	}
	if p, err := Parent(pid); err != nil || p != 0 {
		t.Fatalf("parent of a process that exits mid-read = %d, %v, want 0, nil", p, err)
	}
}
