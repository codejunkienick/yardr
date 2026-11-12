package pprof

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestPprofDoesNotRemoveExistingNonSocketOrLiveSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "pp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	file := filepath.Join(dir, "existing")
	if err := os.WriteFile(file, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := Serve(file, quiet)
	stop()
	if got, err := os.ReadFile(file); err != nil || string(got) != "keep me" {
		t.Fatalf("existing file = %q, %v; want it preserved", got, err)
	}

	sock := filepath.Join(dir, "active.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	stop = Serve(sock, quiet)
	stop()
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("existing listener no longer accepts connections: %v", err)
	}
	conn.Close()
}

// Unset, Serve is a no-op: the empty path is what an unset variable
// reads as. That no socket file is made is pinned where each process is
// wired (the server's and the daemon's tests); here the call is only
// safe to make and to stop.
func TestServeEmptyPathServesNothing(t *testing.T) {
	stop := Serve("", quiet)
	if stop == nil {
		t.Fatal("Serve(\"\") returned nil; want a stop func")
	}
	stop()
}
