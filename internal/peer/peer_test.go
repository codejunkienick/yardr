package peer

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

// The peer of a unix connection is the connecting process, and its
// ancestors begin with it and its parent, unless that is pid 1.
func TestPIDAndAncestors(t *testing.T) {
	dir, err := os.MkdirTemp("", "yp") // t.TempDir is too long for a socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			t.Error(err)
			close(accepted)
			return
		}
		accepted <- c
	}()
	client, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	if server == nil {
		t.FailNow()
	}
	defer server.Close()
	pid, err := PID(server)
	if err != nil {
		t.Fatalf("PID: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("peer pid %d, want this process %d", pid, os.Getpid())
	}
	// Ancestors leaves out pid 1 and anything below it (Parent answers 0 for
	// them), so a test binary that is a direct child of pid 1, as when a
	// container's entrypoint runs it, has itself alone; any other has its
	// parent next. (yardr-t6ah: slicing two off the pid alone panicked.)
	anc := Ancestors(os.Getpid())
	if ppid := os.Getppid(); ppid <= 1 {
		if !slices.Equal(anc, []int{os.Getpid()}) {
			t.Fatalf("ancestors %v, want %d alone: its parent is pid %d, which Ancestors leaves out", anc, os.Getpid(), ppid)
		}
	} else if len(anc) < 2 || !slices.Equal(anc[:2], []int{os.Getpid(), ppid}) {
		t.Fatalf("ancestors %v, want to begin with %d, %d", anc, os.Getpid(), ppid)
	}
	// A pid no process has is nobody's child.
	if p, err := Parent(1<<22 - 7); err != nil || p != 0 {
		t.Fatalf("parent of a free pid = %d, %v", p, err)
	}
	if _, err := PID(fakeConn{}); err == nil {
		t.Fatal("a non-unix connection has a peer")
	}
}

// Alive tells a process from a later one with its pid by the start time,
// and knows a reaped one is gone.
func TestAlive(t *testing.T) {
	if !HaveStat() {
		t.Skip("this OS reports no process state")
	}
	me := os.Getpid()
	st, err := Describe(me)
	if err != nil {
		t.Fatal(err)
	}
	if !Alive(me, st.Start) {
		t.Fatal("this process is not alive")
	}
	if Alive(me, st.Start+1) {
		t.Fatal("another start time is the same process")
	}
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if Alive(cmd.Process.Pid, 0) {
		t.Fatal("a reaped process is alive")
	}
	if _, err := Describe(cmd.Process.Pid); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a reaped process: %v, want %v", err, fs.ErrNotExist)
	}
}

// A kill refused with EPERM is another user's process: not ours, not
// alive. (yardr-4wfs: it counted as alive.)
func TestAnotherUsersProcessIsNotOurs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can signal any process")
	}
	if err := unixKill(1, 0); !errors.Is(err, syscall.EPERM) {
		t.Skipf("pid 1 answers %v, not EPERM; no foreign process to try", err)
	}
	if kill0(1) {
		t.Fatal("kill0(1) is true for another user's process")
	}
}

type fakeConn struct{ net.Conn }
